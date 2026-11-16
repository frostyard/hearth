package broker_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/frostyard/hearth/events"
	"github.com/frostyard/hearth/events/broker"
	"github.com/frostyard/hearth/events/outbox"
	"github.com/frostyard/hearth/internal/natstest"
)

// open returns the outbox of a database file.
func open(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open the database: %v", err)
	}
	// SQLite takes 1 writer at a time, so the test's database holds 1
	// connection: a write and the relay's drain never collide.
	db.SetMaxOpenConns(1)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close the database: %v", err)
		}
	})
	if err := outbox.Init(t.Context(), db); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return db
}

// write writes 1 event in the outbox's transaction, and commits it.
func write(t *testing.T, db *sql.DB, e events.Event) {
	t.Helper()
	ctx := t.Context()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin the transaction: %v", err)
	}
	if err := outbox.Tx(tx).Write(ctx, e); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit the transaction: %v", err)
	}
}

// rows returns how many rows the outbox holds.
func rows(t *testing.T, db *sql.DB) int {
	t.Helper()
	var count int
	if err := db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM hearth_outbox").Scan(&count); err != nil {
		t.Fatalf("count the outbox's rows: %v", err)
	}
	return count
}

// lostAck is an events.Transport that publishes the event and then answers as
// if the acknowledgement was lost.
type lostAck struct {
	// transport is the transport that holds the event.
	transport events.Transport
}

// Publish implements events.Transport.
func (l lostAck) Publish(ctx context.Context, e events.Event) error {
	if err := l.transport.Publish(ctx, e); err != nil {
		return err
	}
	return errors.New("the acknowledgement was lost")
}

// TestRelayAfterCommit checks that an event that committed before the app
// stopped reaches the stream once when the relay runs again.
func TestRelayAfterCommit(t *testing.T) {
	srv := natstest.Start(t, app)
	path := filepath.Join(t.TempDir(), "outbox.db")
	db := open(t, path)
	written := event(t, "n-1")
	write(t, db, written)
	// The app stops before any relay drains the outbox.
	if err := db.Close(); err != nil {
		t.Fatalf("close the database: %v", err)
	}

	db = open(t, path)
	relay := outbox.NewRelay(db, dial(t, srv, nil), outbox.Options{})
	published, err := relay.Drain(t.Context())
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if published != 1 {
		t.Errorf("Drain published %d events, want 1", published)
	}
	if got := srv.Count(t); got != 1 {
		t.Errorf("the stream holds %d messages, want 1", got)
	}
	if got := rows(t, db); got != 0 {
		t.Errorf("the outbox holds %d rows, want none", got)
	}
	messages := srv.Messages(t)
	if got := messages[0].Subject; got != written.Topic {
		t.Errorf("the message's subject = %q, want %q", got, written.Topic)
	}
}

// TestRelayAfterLostAck checks that an event whose acknowledgement was lost
// stays in the outbox, and that the next relay publishes it again without a
// second message: JetStream drops the duplicate id.
func TestRelayAfterLostAck(t *testing.T) {
	srv := natstest.Start(t, app)
	path := filepath.Join(t.TempDir(), "outbox.db")
	db := open(t, path)
	written := event(t, "n-1")
	write(t, db, written)

	relay := outbox.NewRelay(db, lostAck{transport: dial(t, srv, nil)}, outbox.Options{})
	published, err := relay.Drain(t.Context())
	if err == nil {
		t.Fatal("Drain succeeded, and its transport lost the acknowledgement")
	}
	if published != 0 {
		t.Errorf("Drain published %d events, want 0", published)
	}
	if got := srv.Count(t); got != 1 {
		t.Fatalf("the stream holds %d messages, want 1", got)
	}
	if got := rows(t, db); got != 1 {
		t.Fatalf("the outbox holds %d rows, want the 1 that was not acknowledged", got)
	}

	// The app stops, comes back, and its new relay publishes the row again.
	if err := db.Close(); err != nil {
		t.Fatalf("close the database: %v", err)
	}
	db = open(t, path)
	relay = outbox.NewRelay(db, dial(t, srv, nil), outbox.Options{})
	published, err = relay.Drain(t.Context())
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if published != 1 {
		t.Errorf("Drain published %d events, want 1", published)
	}
	if got := srv.Count(t); got != 1 {
		t.Errorf("the stream holds %d messages, want 1", got)
	}
	if got := rows(t, db); got != 0 {
		t.Errorf("the outbox holds %d rows, want none", got)
	}
}

// TestDialChecksSocket checks that a transport needs the broker's socket.
func TestDialChecksSocket(t *testing.T) {
	if _, err := broker.Dial(broker.Options{}); err == nil {
		t.Error("Dial took no socket")
	}
}
