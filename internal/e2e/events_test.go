package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/frostyard/hearth/authz"
	"github.com/frostyard/hearth/events"
	demov1 "github.com/frostyard/hearth/internal/e2e/gen/frostyard/demo/v1"
)

// local is the context of the app's own user.
func local() context.Context {
	return authz.WithCaller(context.Background(), authz.LocalCaller())
}

// TestEventID checks that the id of a generated event is the event's own, and
// that 2 events of 1 note get 2 ids.
func TestEventID(t *testing.T) {
	ctx := local()
	first, err := demov1.NewNoteCreated(ctx, "n-1", "/work")
	if err != nil {
		t.Fatalf("NewNoteCreated: %v", err)
	}
	second, err := demov1.NewNoteCreated(ctx, "n-1", "/work")
	if err != nil {
		t.Fatalf("NewNoteCreated: %v", err)
	}
	if first.ID == "" || first.ID == first.Subject {
		t.Errorf("the id %q is the note's id", first.ID)
	}
	if first.ID == second.ID {
		t.Errorf("2 events of n-1 hold the id %q", first.ID)
	}
	if first.Type != "org.frostyard.demo.note.created" {
		t.Errorf("the type = %q, want org.frostyard.demo.note.created", first.Type)
	}
}

// TestEventAuthor checks that the author of a generated event is the caller of
// the context: the app's own user, a lodge principal, and no caller at all.
func TestEventAuthor(t *testing.T) {
	event, err := demov1.NewNoteCreated(local(), "n-1", "/work")
	if err != nil {
		t.Fatalf("NewNoteCreated: %v", err)
	}
	if event.Author != "local" {
		t.Errorf("the author of the local user = %q, want local", event.Author)
	}

	event, err = demov1.NewNoteCreated(caller(t, authz.LocalCaller().Grant), "n-1", "/work")
	if err != nil {
		t.Fatalf("NewNoteCreated: %v", err)
	}
	if event.Author != "chani" {
		t.Errorf("the author of the lodge principal = %q, want chani", event.Author)
	}

	if _, err := demov1.NewNoteCreated(t.Context(), "n-1", "/work"); !errors.Is(err, events.ErrNoCaller) {
		t.Errorf("NewNoteCreated with no caller = %v, want an error that wraps ErrNoCaller", err)
	}
}

// TestEventTopic checks the topic of a generated event: 1 percent-encoded
// segment for each segment of the group.
func TestEventTopic(t *testing.T) {
	event, err := demov1.NewNoteCreated(local(), "n-1", "/Work Notes/2026_Q1")
	if err != nil {
		t.Fatalf("NewNoteCreated: %v", err)
	}
	if want := "demo.note.created.group.Work%20Notes.2026%5FQ1"; event.Topic != want {
		t.Errorf("the topic = %q, want %q", event.Topic, want)
	}
	if want := "/Work Notes/2026_Q1"; event.Selector != want {
		t.Errorf("the path = %q, want %q", event.Selector, want)
	}
}

// TestEventRootTopic checks that a note in the root group has the spec's topic
// and no segment.
func TestEventRootTopic(t *testing.T) {
	for _, group := range []string{"", "/"} {
		event, err := demov1.NewNoteCreated(local(), "n-1", group)
		if err != nil {
			t.Fatalf("NewNoteCreated(%q): %v", group, err)
		}
		if want := "demo.note.created.group"; event.Topic != want {
			t.Errorf("the topic of the group %q = %q, want %q", group, event.Topic, want)
		}
		if event.Selector != "/" {
			t.Errorf("the path of the group %q = %q, want /", group, event.Selector)
		}
	}
}

// TestEventNoSelector checks the event that carries no selector: its topic is
// the spec's topic, its path is empty, and its envelope holds no selector.
func TestEventNoSelector(t *testing.T) {
	event, err := demov1.NewNotePublished(local(), "n-1")
	if err != nil {
		t.Fatalf("NewNotePublished: %v", err)
	}
	if want := "demo.note.published"; event.Topic != want {
		t.Errorf("the topic = %q, want %q", event.Topic, want)
	}
	if event.Selector != "" {
		t.Errorf("the path = %q, want none", event.Selector)
	}
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if bytes.Contains(data, []byte("selector")) {
		t.Errorf("the envelope of an event without a selector holds 1:\n%s", data)
	}
	parsed, err := events.Parse(data)
	if err != nil {
		t.Fatalf("events.Parse: %v", err)
	}
	if parsed.Type != "org.frostyard.demo.note.published" || parsed.Subject != "n-1" {
		t.Errorf("the envelope = %+v, want the published event of n-1", parsed)
	}
}

// writer is an events.Writer that keeps the events it is given.
type writer struct {
	// events are the events it wrote.
	events []events.Event
}

// Write implements events.Writer.
func (w *writer) Write(_ context.Context, e events.Event) error {
	w.events = append(w.events, e)
	return nil
}

// TestEmitNoteCreated checks that the generated emit function writes the
// change's event into the writer: the outbox of the transaction.
func TestEmitNoteCreated(t *testing.T) {
	out := &writer{}
	if err := demov1.EmitNoteCreated(local(), out, "n-1", "/work"); err != nil {
		t.Fatalf("EmitNoteCreated: %v", err)
	}
	if len(out.events) != 1 {
		t.Fatalf("the writer took %d events, want 1", len(out.events))
	}
	written := out.events[0]
	if written.Subject != "n-1" || written.Author != "local" {
		t.Errorf("the event = %+v, want the created event of n-1 by local", written)
	}
	if want := "demo.note.created.group.work"; written.Topic != want {
		t.Errorf("the topic = %q, want %q", written.Topic, want)
	}
	if written.ID == "" {
		t.Error("the event has no id")
	}

	out = &writer{}
	if err := demov1.EmitNotePublished(local(), out, "n-1"); err != nil {
		t.Fatalf("EmitNotePublished: %v", err)
	}
	if len(out.events) != 1 {
		t.Fatalf("the writer took %d events, want 1", len(out.events))
	}
	if want := "demo.note.published"; out.events[0].Topic != want {
		t.Errorf("the topic = %q, want %q", out.events[0].Topic, want)
	}
}
