package broker_test

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/frostyard/hearth/authz"
	"github.com/frostyard/hearth/events"
	"github.com/frostyard/hearth/events/broker"
	"github.com/frostyard/hearth/internal/natstest"
)

// app is the app of the tests, and the account and stream of the test server.
const app = "notes"

// spec is the event of 1 created note, filed in a group.
var spec = events.Spec{
	App:            app,
	Type:           "org.frostyard.notes.note.created",
	Topic:          "notes.note.created.group",
	Selector:       "group",
	ReadPermission: "notes:read",
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

// dial connects a transport to the test server's socket, and closes it at the
// end of the test.
func dial(t *testing.T, srv *natstest.Server, logger *slog.Logger) *broker.Transport {
	t.Helper()
	transport, err := broker.Dial(broker.Options{Socket: srv.Socket, User: app, Name: t.Name(), Logger: logger})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(transport.Close)
	return transport
}

// TestPublish checks that 1 publish stores 1 message: the event's topic as its
// subject, its id as the message's id, and its envelope as its data.
func TestPublish(t *testing.T) {
	srv := natstest.Start(t, app)
	transport := dial(t, srv, nil)
	written := event(t, "n-1")
	if err := transport.Publish(t.Context(), written); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if got := srv.Count(t); got != 1 {
		t.Fatalf("the stream holds %d messages, want 1", got)
	}
	messages := srv.Messages(t)
	stored := messages[0]
	if stored.Subject != written.Topic {
		t.Errorf("the message's subject = %q, want %q", stored.Subject, written.Topic)
	}
	if got := stored.Header.Get(jetstream.MsgIDHeader); got != written.ID {
		t.Errorf("the message's id = %q, want %q", got, written.ID)
	}
	if got := stored.Header.Get("Content-Type"); got != "application/cloudevents+json" {
		t.Errorf("the message's content type = %q, want application/cloudevents+json", got)
	}
	parsed, err := events.Parse(stored.Data)
	if err != nil {
		t.Fatalf("events.Parse of the message's data: %v", err)
	}
	if parsed.ID != written.ID || parsed.Type != written.Type || parsed.Subject != written.Subject {
		t.Errorf("the message's event = %+v, want %+v", parsed, written)
	}
	if parsed.Author != "local" || parsed.Selector != "/work" || parsed.Source != "/notes" {
		t.Errorf("the message's event = %+v, want the event that was published", parsed)
	}
	if parsed.Topic != "" {
		t.Errorf("the event's envelope holds the topic %q, and it carries none", parsed.Topic)
	}
}

// TestPublishTwice checks that 1 event that is published twice stores 1 message:
// JetStream drops the duplicate id.
func TestPublishTwice(t *testing.T) {
	srv := natstest.Start(t, app)
	transport := dial(t, srv, nil)
	written := event(t, "n-1")
	for range 2 {
		if err := transport.Publish(t.Context(), written); err != nil {
			t.Fatalf("Publish: %v", err)
		}
	}
	if got := srv.Count(t); got != 1 {
		t.Errorf("the stream holds %d messages, want 1", got)
	}
}

// TestPublishChecks checks that an event without an id or without a topic is an
// error before any publish.
func TestPublishChecks(t *testing.T) {
	srv := natstest.Start(t, app)
	transport := dial(t, srv, nil)
	written := event(t, "n-1")
	if err := transport.Publish(t.Context(), events.Event{Topic: written.Topic}); err == nil {
		t.Error("Publish took an event with no id")
	}
	if err := transport.Publish(t.Context(), events.Event{ID: written.ID}); err == nil {
		t.Error("Publish took an event with no topic")
	}
	if got := srv.Count(t); got != 0 {
		t.Errorf("the stream holds %d messages, want none", got)
	}
}

// TestRefusedPublish checks that the app's user may not publish another app's
// subject: the server reports the permissions violation, the publish waits for
// an acknowledgement that never comes, and the stream holds nothing.
func TestRefusedPublish(t *testing.T) {
	srv := natstest.Start(t, app)
	records := make(chan string, 16)
	transport := dial(t, srv, slog.New(recordHandler{records: records}))
	written := event(t, "n-1")
	written.Topic = "other.note.created.group.work"

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- transport.Publish(ctx, written) }()

	record := <-records
	if !strings.Contains(record, "permissions violation") {
		t.Fatalf("the connection's error = %q, want a permissions violation", record)
	}
	cancel()
	if err := <-done; err == nil {
		t.Error("Publish succeeded on another app's subject")
	}
	if got := srv.Count(t); got != 0 {
		t.Errorf("the stream holds %d messages, want none", got)
	}
}

// recordHandler is a slog handler that sends each record's message and
// attributes on a channel.
type recordHandler struct {
	// records is the channel that each record goes on.
	records chan<- string
}

// Enabled implements slog.Handler.
func (recordHandler) Enabled(context.Context, slog.Level) bool { return true }

// Handle implements slog.Handler.
func (h recordHandler) Handle(_ context.Context, record slog.Record) error {
	line := record.Message
	record.Attrs(func(attr slog.Attr) bool {
		line += " " + attr.Key + "=" + attr.Value.String()
		return true
	})
	h.records <- line
	return nil
}

// WithAttrs implements slog.Handler.
func (h recordHandler) WithAttrs([]slog.Attr) slog.Handler { return h }

// WithGroup implements slog.Handler.
func (h recordHandler) WithGroup(string) slog.Handler { return h }
