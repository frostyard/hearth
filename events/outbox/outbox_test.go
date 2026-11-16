package outbox_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/frostyard/hearth/authz"
	"github.com/frostyard/hearth/events"
	"github.com/frostyard/hearth/events/inproc"
	"github.com/frostyard/hearth/events/outbox"
)

// spec is the event of 1 created note, filed in a group.
var spec = events.Spec{
	App:            "notes",
	Type:           "org.frostyard.notes.note.created",
	Topic:          "notes.note.created.group",
	Selector:       "group",
	ReadPermission: "notes:read",
}

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

// event returns the event of 1 note, written as the app's own user.
func event(t *testing.T, subject string) events.Event {
	t.Helper()
	ctx := authz.WithCaller(t.Context(), authz.LocalCaller())
	e, err := events.New(ctx, spec, subject, "/work")
	if err != nil {
		t.Fatalf("events.New: %v", err)
	}
	return e
}

// write writes each event in 1 transaction, and commits it.
func write(t *testing.T, db *sql.DB, list ...events.Event) {
	t.Helper()
	ctx := t.Context()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin the transaction: %v", err)
	}
	for _, e := range list {
		if err := outbox.Tx(tx).Write(ctx, e); err != nil {
			t.Fatalf("Write: %v", err)
		}
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

// recorder is an events.Transport that records the events it takes, and that
// fails at the publish numbers it is given.
type recorder struct {
	// events are the events it took, in order.
	events []events.Event
	// breaks are the publish numbers, counting from 1, that fail.
	breaks map[int]bool
	// count counts the publishes it was asked for.
	count int
}

// Publish implements events.Transport.
func (r *recorder) Publish(_ context.Context, e events.Event) error {
	r.count++
	if r.breaks[r.count] {
		return errors.New("the transport is down")
	}
	r.events = append(r.events, e)
	return nil
}

// lossy is an events.Transport that publishes the event and then answers as if
// the acknowledgement was lost.
type lossy struct {
	// transport is the transport that holds the event.
	transport events.Transport
}

// Publish implements events.Transport.
func (l lossy) Publish(ctx context.Context, e events.Event) error {
	if err := l.transport.Publish(ctx, e); err != nil {
		return err
	}
	return errors.New("the acknowledgement was lost")
}

// subscribe subscribes 1 channel to the events of the notes app.
func subscribe(t *testing.T, bus *inproc.Bus) <-chan events.Event {
	t.Helper()
	got := make(chan events.Event, 8)
	cancel, err := bus.Subscribe("notes.>", func(e events.Event) { got <- e })
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	t.Cleanup(cancel)
	return got
}

// TestInitTwice checks that the outbox's table is made once, and that a second
// Init is harmless.
func TestInitTwice(t *testing.T) {
	db := open(t, filepath.Join(t.TempDir(), "outbox.db"))
	if err := outbox.Init(t.Context(), db); err != nil {
		t.Errorf("a second Init: %v", err)
	}
}

// TestWriteRollback checks that an event of a transaction that rolls back stays
// out of the outbox.
func TestWriteRollback(t *testing.T) {
	db := open(t, filepath.Join(t.TempDir(), "outbox.db"))
	ctx := t.Context()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin the transaction: %v", err)
	}
	if err := outbox.Tx(tx).Write(ctx, event(t, "n-1")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("roll back the transaction: %v", err)
	}
	if got := rows(t, db); got != 0 {
		t.Errorf("the outbox holds %d rows after a rollback, want none", got)
	}
}

// TestWriteChecks checks that an event without an id or without a topic is an
// error, and that it writes no row.
func TestWriteChecks(t *testing.T) {
	db := open(t, filepath.Join(t.TempDir(), "outbox.db"))
	ctx := t.Context()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin the transaction: %v", err)
	}
	defer func() {
		if err := tx.Rollback(); err != nil {
			t.Errorf("roll back the transaction: %v", err)
		}
	}()
	good := event(t, "n-1")
	if err := outbox.Tx(tx).Write(ctx, events.Event{Topic: good.Topic}); err == nil {
		t.Error("Write took an event with no id")
	}
	if err := outbox.Tx(tx).Write(ctx, events.Event{ID: good.ID}); err == nil {
		t.Error("Write took an event with no topic")
	}
}

// TestWriteChecksInvalid checks that 1 event that no transport would take fails
// Write inside the transaction, so that the outbox holds no row that the relay
// cannot publish, and that a valid event still passes.
func TestWriteChecksInvalid(t *testing.T) {
	db := open(t, filepath.Join(t.TempDir(), "outbox.db"))
	ctx := t.Context()
	good := event(t, "n-1")
	noAuthor := good
	noAuthor.Author = ""
	longEnvelope := good
	longEnvelope.Subject = strings.Repeat("a", events.MaxEnvelopeLen)
	for _, tc := range []struct {
		name  string
		event events.Event
		want  string
	}{
		{"no author", noAuthor, "the author is required"},
		{"an envelope that is too long", longEnvelope, "the envelope is"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatalf("begin the transaction: %v", err)
			}
			err = outbox.Tx(tx).Write(ctx, tc.event)
			if err == nil {
				_ = tx.Rollback()
				t.Fatalf("Write took the event with %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the error = %q, want it to hold %q", err, tc.want)
			}
			var held int
			if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM hearth_outbox").Scan(&held); err != nil {
				t.Fatalf("count the outbox's rows: %v", err)
			}
			if held != 0 {
				t.Errorf("the transaction holds %d row(s), want none", held)
			}
			if err := tx.Rollback(); err != nil {
				t.Fatalf("roll back the transaction: %v", err)
			}
			if got := rows(t, db); got != 0 {
				t.Errorf("the outbox holds %d rows after the rollback, want none", got)
			}
		})
	}

	// A valid event still passes, and it commits.
	write(t, db, good)
	if got := rows(t, db); got != 1 {
		t.Errorf("the outbox holds %d rows, want the 1 valid event", got)
	}
}

// TestDrain checks that a committed event is published in the order it was
// written, with its topic, and that its row goes after the transport has it.
func TestDrain(t *testing.T) {
	db := open(t, filepath.Join(t.TempDir(), "outbox.db"))
	list := []events.Event{event(t, "n-1"), event(t, "n-2"), event(t, "n-3")}
	write(t, db, list...)
	transport := &recorder{}
	relay := outbox.NewRelay(db, transport, outbox.Options{})
	published, err := relay.Drain(t.Context())
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if published != len(list) {
		t.Fatalf("Drain published %d events, want %d", published, len(list))
	}
	if len(transport.events) != len(list) {
		t.Fatalf("the transport took %d events, want %d", len(transport.events), len(list))
	}
	for i, e := range transport.events {
		if e.ID != list[i].ID {
			t.Errorf("event %d = %q, want %q", i, e.ID, list[i].ID)
		}
		if e.Topic != list[i].Topic {
			t.Errorf("the topic of event %d = %q, want %q", i, e.Topic, list[i].Topic)
		}
		if e.Author != "local" || e.Subject != list[i].Subject || e.Selector != "/work" {
			t.Errorf("event %d = %+v, want the event that was written", i, e)
		}
	}
	if got := rows(t, db); got != 0 {
		t.Errorf("the outbox holds %d rows after a drain, want none", got)
	}
	// A drain of an empty outbox publishes nothing.
	published, err = relay.Drain(t.Context())
	if err != nil || published != 0 {
		t.Errorf("Drain of an empty outbox = %d, %v, want 0, nil", published, err)
	}
}

// TestDrainKeeps checks that a publish that fails keeps its row and every later
// 1, and that the next drain publishes them.
func TestDrainKeeps(t *testing.T) {
	db := open(t, filepath.Join(t.TempDir(), "outbox.db"))
	list := []events.Event{event(t, "n-1"), event(t, "n-2"), event(t, "n-3")}
	write(t, db, list...)
	transport := &recorder{breaks: map[int]bool{2: true}}
	relay := outbox.NewRelay(db, transport, outbox.Options{})
	published, err := relay.Drain(t.Context())
	if err == nil {
		t.Fatal("Drain succeeded, and its transport failed the 2nd publish")
	}
	if published != 1 {
		t.Errorf("Drain published %d events, want 1", published)
	}
	if got := rows(t, db); got != 2 {
		t.Errorf("the outbox holds %d rows, want the 2 that were not published", got)
	}
	// The next drain publishes the row that failed, and the 1 after it.
	transport.breaks = nil
	published, err = relay.Drain(t.Context())
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if published != 2 {
		t.Errorf("Drain published %d events, want 2", published)
	}
	if got := rows(t, db); got != 0 {
		t.Errorf("the outbox holds %d rows, want none", got)
	}
	if len(transport.events) != 3 {
		t.Fatalf("the transport took %d events, want 3", len(transport.events))
	}
	for i, e := range transport.events {
		if e.ID != list[i].ID {
			t.Errorf("event %d = %q, want %q", i, e.ID, list[i].ID)
		}
	}
}

// TestDrainBatches checks that a drain publishes the whole outbox, a batch at a
// time.
func TestDrainBatches(t *testing.T) {
	db := open(t, filepath.Join(t.TempDir(), "outbox.db"))
	var list []events.Event
	for i := range 5 {
		list = append(list, event(t, fmt.Sprintf("n-%d", i)))
	}
	write(t, db, list...)
	transport := &recorder{}
	relay := outbox.NewRelay(db, transport, outbox.Options{Batch: 2})
	published, err := relay.Drain(t.Context())
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if published != 5 {
		t.Errorf("Drain published %d events, want 5", published)
	}
	if got := rows(t, db); got != 0 {
		t.Errorf("the outbox holds %d rows, want none", got)
	}
}

// TestCrashBeforePublish checks that an event that commits before the app stops
// is published once when the relay runs again: the row was in the outbox, and
// the subscriber got the event 1 time.
func TestCrashBeforePublish(t *testing.T) {
	path := filepath.Join(t.TempDir(), "outbox.db")
	db := open(t, path)
	written := event(t, "n-1")
	write(t, db, written)
	// The app stops before any relay drains the outbox.
	if err := db.Close(); err != nil {
		t.Fatalf("close the database: %v", err)
	}

	bus := inproc.New()
	got := subscribe(t, bus)
	db = open(t, path)
	relay := outbox.NewRelay(db, bus, outbox.Options{})
	published, err := relay.Drain(t.Context())
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if published != 1 {
		t.Errorf("Drain published %d events, want 1", published)
	}
	if e := <-got; e.ID != written.ID {
		t.Errorf("the subscriber got %q, want %q", e.ID, written.ID)
	}
	if got := rows(t, db); got != 0 {
		t.Errorf("the outbox holds %d rows, want none", got)
	}
}

// TestCrashAfterPublish checks that an event whose acknowledgement was lost is
// in the outbox still, and that the bus's de-duplication delivers it 1 time
// when the relay publishes it again.
func TestCrashAfterPublish(t *testing.T) {
	path := filepath.Join(t.TempDir(), "outbox.db")
	db := open(t, path)
	written := event(t, "n-1")
	write(t, db, written)

	bus := inproc.New()
	got := subscribe(t, bus)
	relay := outbox.NewRelay(db, lossy{transport: bus}, outbox.Options{})
	published, err := relay.Drain(t.Context())
	if err == nil {
		t.Fatal("Drain succeeded, and its transport lost the acknowledgement")
	}
	if published != 0 {
		t.Errorf("Drain published %d events, want 0", published)
	}
	if e := <-got; e.ID != written.ID {
		t.Fatalf("the subscriber got %q, want %q", e.ID, written.ID)
	}
	if got := rows(t, db); got != 1 {
		t.Fatalf("the outbox holds %d rows, want the 1 that was not acknowledged", got)
	}

	// The app stops, comes back, and its new relay publishes the row again.
	if err := db.Close(); err != nil {
		t.Fatalf("close the database: %v", err)
	}
	db = open(t, path)
	published, err = outbox.NewRelay(db, bus, outbox.Options{}).Drain(t.Context())
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if published != 1 {
		t.Errorf("Drain published %d events, want 1", published)
	}
	if len(got) != 0 {
		t.Errorf("the subscriber got %d events more, want none", len(got))
	}
	if got := rows(t, db); got != 0 {
		t.Errorf("the outbox holds %d rows, want none", got)
	}
}

// TestRun checks that 1 Notify after a write publishes the event, and that Run
// returns nil when its context ends.
func TestRun(t *testing.T) {
	db := open(t, filepath.Join(t.TempDir(), "outbox.db"))
	bus := inproc.New()
	got := subscribe(t, bus)
	relay := outbox.NewRelay(db, bus, outbox.Options{Interval: time.Hour})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- relay.Run(ctx) }()

	written := event(t, "n-1")
	write(t, db, written)
	relay.Notify()
	if e := <-got; e.ID != written.ID {
		t.Fatalf("the subscriber got %q, want %q", e.ID, written.ID)
	}
	cancel()
	if err := <-done; err != nil {
		t.Errorf("Run = %v, want nil", err)
	}
	// The relay deletes the row once the transport has the event, and the
	// cancel may come between the 2, so 1 drain finishes the work.
	if _, err := relay.Drain(t.Context()); err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if got := rows(t, db); got != 0 {
		t.Errorf("the outbox holds %d rows, want none", got)
	}
	if len(got) != 0 {
		t.Errorf("the bus delivered %d events more, want none", len(got))
	}
}
