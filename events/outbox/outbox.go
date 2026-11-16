// Package outbox is hearth's transactional outbox: an app writes each event in
// the transaction that makes the change, and a relay publishes the events in
// order, after the transaction has committed.
//
// The outbox row and the change commit together, so a change never happens
// without its event, and an event is never lost when the app stops before the
// broker has it: the row stays, and the next relay publishes it again. The
// event's id makes that repeat harmless, because the broker stores 1 message per
// id.
package outbox

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/frostyard/hearth/events"
)

// Schema is the outbox's table: the row's place in the order, the event's id
// and topic, and its envelope.
const Schema = `CREATE TABLE IF NOT EXISTS hearth_outbox (
	seq   INTEGER PRIMARY KEY AUTOINCREMENT,
	id    TEXT NOT NULL UNIQUE,
	topic TEXT NOT NULL,
	event BLOB NOT NULL
)`

// The defaults of a relay.
const (
	// defaultInterval is how often a relay drains the outbox when nothing
	// notifies it.
	defaultInterval = 5 * time.Second
	// defaultBatch is how many rows a relay reads at a time.
	defaultBatch = 100
)

// The outbox's statements.
const (
	insertSQL = `INSERT INTO hearth_outbox (id, topic, event) VALUES (?, ?, ?)`
	selectSQL = `SELECT seq, id, topic, event FROM hearth_outbox ORDER BY seq LIMIT ?`
	deleteSQL = `DELETE FROM hearth_outbox WHERE seq = ?`
)

// Init makes the outbox's table, when the database has none.
func Init(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, Schema); err != nil {
		return fmt.Errorf("events/outbox: make the outbox's table: %w", err)
	}
	return nil
}

// writer is the outbox of 1 transaction.
type writer struct {
	// tx is the change's transaction.
	tx *sql.Tx
}

// Tx returns the outbox of 1 transaction: every event the app writes in the
// transaction commits with it, and no event of a transaction that rolls back
// stays.
func Tx(tx *sql.Tx) events.Writer {
	return &writer{tx: tx}
}

// Write writes 1 event in the transaction: its id, its topic and its envelope.
func (w *writer) Write(ctx context.Context, e events.Event) error {
	if e.ID == "" {
		return errors.New("events/outbox: the event has no id")
	}
	if e.Topic == "" {
		return fmt.Errorf("events/outbox: the event %s has no topic", e.ID)
	}
	data, err := e.MarshalJSON()
	if err != nil {
		return err
	}
	if _, err := w.tx.ExecContext(ctx, insertSQL, e.ID, e.Topic, data); err != nil {
		return fmt.Errorf("events/outbox: write the event %s on %s: %w", e.ID, e.Topic, err)
	}
	return nil
}

// Options configure a relay.
type Options struct {
	// Interval is how often the relay drains the outbox when nothing notifies
	// it. 0 means 5 s.
	Interval time.Duration
	// Batch is how many rows the relay reads at a time. 0 means 100.
	Batch int
	// Logger is where the relay logs a failed drain. nil means slog.Default().
	Logger *slog.Logger
}

// Relay publishes the outbox's events to 1 transport, in the order they were
// written, and it deletes each row once the transport holds the event.
type Relay struct {
	// db is the outbox's database.
	db *sql.DB
	// transport is where the events go.
	transport events.Transport
	// interval is how often Run drains the outbox.
	interval time.Duration
	// batch is how many rows 1 read of the outbox takes.
	batch int
	// log is where a failed drain goes.
	log *slog.Logger
	// wake notifies Run that the outbox holds an event. It holds 1 value at
	// most, so a notify never waits.
	wake chan struct{}
}

// NewRelay returns a relay of the outbox in db to t.
func NewRelay(db *sql.DB, t events.Transport, opts Options) *Relay {
	r := &Relay{
		db:        db,
		transport: t,
		interval:  opts.Interval,
		batch:     opts.Batch,
		log:       opts.Logger,
		wake:      make(chan struct{}, 1),
	}
	if r.interval <= 0 {
		r.interval = defaultInterval
	}
	if r.batch <= 0 {
		r.batch = defaultBatch
	}
	if r.log == nil {
		r.log = slog.Default()
	}
	return r
}

// Drain publishes every row of the outbox in the order it was written, 1 batch
// at a time, and it deletes a row only after the transport held its event.
//
// It stops at the first error, and it keeps that row and every later 1, so that
// the outbox holds each event until the transport has it. It returns how many
// events it published.
func (r *Relay) Drain(ctx context.Context) (int, error) {
	published := 0
	for {
		rows, err := r.read(ctx)
		if err != nil {
			return published, err
		}
		if len(rows) == 0 {
			return published, nil
		}
		for _, row := range rows {
			event, err := events.Parse(row.data)
			if err != nil {
				return published, fmt.Errorf("events/outbox: read the row %d: %w", row.seq, err)
			}
			event.Topic = row.topic
			if err := r.transport.Publish(ctx, event); err != nil {
				return published, fmt.Errorf("events/outbox: publish the event %s on %s: %w", row.id, row.topic, err)
			}
			if _, err := r.db.ExecContext(ctx, deleteSQL, row.seq); err != nil {
				return published, fmt.Errorf("events/outbox: delete the row %d of the event %s: %w", row.seq, row.id, err)
			}
			published++
		}
	}
}

// Notify wakes Run. It never waits: 1 wake drains every row, because Drain
// reads the outbox until it is empty.
func (r *Relay) Notify() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// Run drains the outbox at once, and then on each Notify and each Interval,
// until the context ends. A drain that fails is logged, and the next wake tries
// again.
//
// It returns nil when the context ends, and it never returns an error itself: a
// transport's failure is the next drain's work, not the relay's end.
func (r *Relay) Run(ctx context.Context) error {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		if _, err := r.Drain(ctx); err != nil && ctx.Err() == nil {
			r.log.Error("the relay could not drain the outbox", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-r.wake:
		case <-ticker.C:
		}
	}
}

// row is 1 outbox row: its place in the order, the event's id and topic, and
// its envelope.
type row struct {
	// seq is the row's place in the order.
	seq int64
	// id is the event's id.
	id string
	// topic is the event's topic, which the envelope does not carry.
	topic string
	// data is the event's envelope.
	data []byte
}

// read reads 1 batch of the outbox's rows, in the order they were written.
func (r *Relay) read(ctx context.Context) ([]row, error) {
	rows, err := r.db.QueryContext(ctx, selectSQL, r.batch)
	if err != nil {
		return nil, fmt.Errorf("events/outbox: read the outbox: %w", err)
	}
	defer rows.Close()
	var out []row
	for rows.Next() {
		var next row
		if err := rows.Scan(&next.seq, &next.id, &next.topic, &next.data); err != nil {
			return nil, fmt.Errorf("events/outbox: read a row of the outbox: %w", err)
		}
		out = append(out, next)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("events/outbox: read the outbox: %w", err)
	}
	return out, nil
}
