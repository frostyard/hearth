package broker_test

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/frostyard/hearth/events"
	"github.com/frostyard/hearth/events/broker"
	"github.com/frostyard/hearth/events/outbox"
	"github.com/frostyard/hearth/internal/natstest"
)

// TestReconnect checks that 1 event of the outbox survives a broker that ends the
// connection: the relay keeps the row while there is no connection, and it
// publishes the row once the connection is back.
func TestReconnect(t *testing.T) {
	srv := natstest.Start(t, app)
	records := make(chan string, 32)
	transport, err := broker.Dial(broker.Options{
		Socket:        srv.Socket,
		User:          app,
		ReconnectWait: 5 * time.Millisecond,
		Logger:        slog.New(recordHandler{records: records}),
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(transport.Close)
	db := open(t, filepath.Join(t.TempDir(), "outbox.db"))
	relay := outbox.NewRelay(db, transport, outbox.Options{})

	// The first event reaches the stream while the connection is there.
	write(t, db, event(t, "n-1"))
	if published, err := relay.Drain(t.Context()); err != nil || published != 1 {
		t.Fatalf("Drain = %d, %v, want 1 event published", published, err)
	}

	// The broker ends the connection, and it holds the next 1.
	srv.Hold()
	srv.Drop()
	awaitRecord(t, records, "the broker's connection ended")

	// A drain with no connection fails at the publish, and the outbox keeps
	// its row: the test cancels the drain's context once the transport has the
	// event.
	second := event(t, "n-2")
	write(t, db, second)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	canceling := outbox.NewRelay(db, cancelOnPublish{cancel: cancel, transport: transport}, outbox.Options{})
	if _, err := canceling.Drain(ctx); err == nil {
		t.Fatal("Drain without a connection published the event")
	}
	if got := rows(t, db); got != 1 {
		t.Fatalf("the outbox holds %d row(s), want the 1 that no connection took", got)
	}

	// The connection comes back, and the relay publishes the row.
	srv.Release()
	awaitRecord(t, records, "the broker's connection is back")
	if published, err := relay.Drain(t.Context()); err != nil || published != 1 {
		t.Fatalf("Drain = %d, %v, want 1 event published", published, err)
	}
	if got := srv.Count(t); got != 2 {
		t.Errorf("the stream holds %d message(s), want 2", got)
	}
	if got := rows(t, db); got != 0 {
		t.Errorf("the outbox holds %d row(s), want none", got)
	}
	messages := srv.Messages(t)
	if got := messages[1].Subject; got != second.Topic {
		t.Errorf("the second message's subject = %q, want %q", got, second.Topic)
	}
}

// cancelOnPublish is an events.Transport that cancels 1 drain's context as it
// gets the event, and then publishes it: the test then sees the failure of a
// publish that no connection can acknowledge.
type cancelOnPublish struct {
	// cancel ends the drain's context.
	cancel context.CancelFunc
	// transport is the transport that publishes the event.
	transport events.Transport
}

// Publish implements events.Transport.
func (c cancelOnPublish) Publish(ctx context.Context, e events.Event) error {
	c.cancel()
	return c.transport.Publish(ctx, e)
}

// awaitRecord waits for 1 log record that holds text, and it stops the test when
// the channel ends.
func awaitRecord(t *testing.T, records <-chan string, text string) {
	t.Helper()
	for {
		record, ok := <-records
		if !ok {
			t.Fatalf("the log ended before it held %q", text)
		}
		if strings.Contains(record, text) {
			return
		}
	}
}
