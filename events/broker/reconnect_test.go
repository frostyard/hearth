package broker_test

import (
	"context"
	"log/slog"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/frostyard/hearth/events/broker"
	"github.com/frostyard/hearth/events/outbox"
	"github.com/frostyard/hearth/internal/natstest"
)

// TestReconnect checks that 1 event of the outbox survives a broker that ends the
// connection: the relay keeps the row while the publish waits, and it publishes
// the row once the connection is back, for 1 message in the stream.
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
	first := event(t, "n-1")
	write(t, db, first)
	if published, err := relay.Drain(t.Context()); err != nil || published != 1 {
		t.Fatalf("Drain = %d, %v, want 1 event published", published, err)
	}

	// The broker ends the connection, and it holds the next 1.
	srv.Hold()
	srv.Drop()
	awaitRecord(t, records, "the broker's connection ended")

	// A drain that the test cancels while the publish waits in the client's
	// reconnect buffer fails, and the outbox keeps its row.
	second := event(t, "n-2")
	write(t, db, second)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := relay.Drain(ctx)
		done <- err
	}()
	// The client holds the publish in its reconnect buffer until the connection
	// is back: wait for it, and give the drain the processor meanwhile.
	for {
		select {
		case err := <-done:
			t.Fatalf("Drain ended before its publish waited in the reconnect buffer: %v", err)
		default:
		}
		buffered, err := transport.Buffered()
		if err != nil {
			t.Fatalf("Buffered: %v", err)
		}
		if buffered > 0 {
			break
		}
		runtime.Gosched()
	}
	cancel()
	if err := <-done; err == nil {
		t.Fatal("Drain without a connection published the event")
	}
	if got := rows(t, db); got != 1 {
		t.Fatalf("the outbox holds %d row(s), want the 1 that no connection took", got)
	}

	// The connection comes back: the client sends the buffered publish, and the
	// relay publishes the row again. The 2 carry 1 id, so the stream holds it 1
	// time.
	srv.Release()
	awaitRecord(t, records, "the broker's connection is back")
	if published, err := relay.Drain(t.Context()); err != nil || published != 1 {
		t.Fatalf("Drain = %d, %v, want 1 event published", published, err)
	}
	if got := rows(t, db); got != 0 {
		t.Errorf("the outbox holds %d row(s), want none", got)
	}
	if got := srv.Count(t); got != 2 {
		t.Errorf("the stream holds %d message(s), want 2", got)
	}
	held := make(map[string]int, 2)
	for _, message := range srv.Messages(t) {
		held[message.Header.Get(jetstream.MsgIDHeader)]++
	}
	for _, want := range []string{first.ID, second.ID} {
		if held[want] != 1 {
			t.Errorf("the stream holds the event %s %d time(s), want 1: the ids are %v", want, held[want], held)
		}
	}
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
