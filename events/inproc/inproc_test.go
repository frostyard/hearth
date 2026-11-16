package inproc_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/frostyard/hearth/events"
	"github.com/frostyard/hearth/events/inproc"
)

// event returns 1 event with 1 id and 1 topic.
func event(id, topic string) events.Event {
	return events.Event{ID: id, Topic: topic}
}

// subscribe subscribes 1 channel to a filter, and stops the test on an error.
func subscribe(t *testing.T, bus *inproc.Bus, filter string) <-chan events.Event {
	t.Helper()
	got := make(chan events.Event, 16)
	cancel, err := bus.Subscribe(filter, func(e events.Event) { got <- e })
	if err != nil {
		t.Fatalf("Subscribe(%q): %v", filter, err)
	}
	t.Cleanup(cancel)
	return got
}

// receive reads 1 event from a channel, and stops the test when the channel is
// empty.
func receive(t *testing.T, got <-chan events.Event) events.Event {
	t.Helper()
	select {
	case e := <-got:
		return e
	default:
		t.Fatal("no event")
		return events.Event{}
	}
}

// TestFilters checks that each subscriber gets the events its filter takes, and
// no other event.
func TestFilters(t *testing.T) {
	bus := inproc.New()
	notes := subscribe(t, bus, "notes.>")
	all := subscribe(t, bus, ">")
	ctx := t.Context()
	if err := bus.Publish(ctx, event("1", "notes.note.created.group.work")); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if got := receive(t, notes); got.ID != "1" {
		t.Errorf("the notes subscriber got %q, want 1", got.ID)
	}
	if got := receive(t, all); got.ID != "1" {
		t.Errorf("the subscriber of every event got %q, want 1", got.ID)
	}
	if err := bus.Publish(ctx, event("2", "demo.note.created.group.work")); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(notes) != 0 {
		t.Error("the notes subscriber got another app's event")
	}
	if got := receive(t, all); got.ID != "2" {
		t.Errorf("the subscriber of every event got %q, want 2", got.ID)
	}
}

// TestOrder checks that the bus delivers to each subscriber in the order it
// subscribed, and that each subscriber gets the events in the published order.
func TestOrder(t *testing.T) {
	bus := inproc.New()
	var order []string
	first, err := bus.Subscribe(">", func(e events.Event) { order = append(order, "first:"+e.ID) })
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer first()
	second, err := bus.Subscribe(">", func(e events.Event) { order = append(order, "second:"+e.ID) })
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer second()
	for _, id := range []string{"1", "2"} {
		if err := bus.Publish(t.Context(), event(id, "demo.note.created.group.work")); err != nil {
			t.Fatalf("Publish: %v", err)
		}
	}
	want := "first:1 second:1 first:2 second:2"
	if got := strings.Join(order, " "); got != want {
		t.Errorf("the delivery order = %q, want %q", got, want)
	}
}

// TestCancel checks that a cancelled subscriber gets no event, and that 2
// cancels are harmless.
func TestCancel(t *testing.T) {
	bus := inproc.New()
	got := make(chan events.Event, 1)
	cancel, err := bus.Subscribe(">", func(e events.Event) { got <- e })
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	cancel()
	cancel()
	if err := bus.Publish(t.Context(), event("1", "demo.note.created.group.work")); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(got) != 0 {
		t.Error("a cancelled subscriber got an event")
	}
	// The cancellation of 1 subscriber leaves the other ones.
	other := subscribe(t, bus, ">")
	if err := bus.Publish(t.Context(), event("2", "demo.note.created.group.work")); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if e := receive(t, other); e.ID != "2" {
		t.Errorf("the other subscriber got %q, want 2", e.ID)
	}
}

// TestDuplicate checks that an event the bus already delivered is skipped, and
// that an event with no id is never a duplicate.
func TestDuplicate(t *testing.T) {
	bus := inproc.New()
	got := subscribe(t, bus, ">")
	ctx := t.Context()
	if err := bus.Publish(ctx, event("1", "demo.note.created.group.work")); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if err := bus.Publish(ctx, event("1", "demo.note.created.group.work")); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	receive(t, got)
	if len(got) != 0 {
		t.Error("the bus delivered an event with a known id twice")
	}
	for range 2 {
		if err := bus.Publish(ctx, events.Event{Topic: "demo.note.created.group.work"}); err != nil {
			t.Fatalf("Publish: %v", err)
		}
	}
	receive(t, got)
	receive(t, got)
}

// TestWindow checks the window of the last 1024 ids: the oldest id that the
// bus forgot is delivered again, and the ones it keeps are not.
func TestWindow(t *testing.T) {
	bus := inproc.New()
	var delivered []string
	cancel, err := bus.Subscribe(">", func(e events.Event) { delivered = append(delivered, e.ID) })
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer cancel()
	ctx := t.Context()
	for i := range 1025 {
		if err := bus.Publish(ctx, event(fmt.Sprintf("id-%d", i), "demo.note.created.group.work")); err != nil {
			t.Fatalf("Publish: %v", err)
		}
	}
	if len(delivered) != 1025 {
		t.Fatalf("the bus delivered %d events, want 1025", len(delivered))
	}
	// The window holds the last 1024 ids it delivered, so it forgot id-0 and
	// it holds id-1 and the newest 1.
	delivered = nil
	for _, id := range []string{"id-1", "id-1024"} {
		if err := bus.Publish(ctx, event(id, "demo.note.created.group.work")); err != nil {
			t.Fatalf("Publish: %v", err)
		}
	}
	if len(delivered) != 0 {
		t.Errorf("the bus delivered %v, and its window holds those ids", delivered)
	}
	if err := bus.Publish(ctx, event("id-0", "demo.note.created.group.work")); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(delivered) != 1 || delivered[0] != "id-0" {
		t.Errorf("the bus delivered %v, want id-0 again", delivered)
	}
}

// TestHandlerSubscribes checks that a handler may subscribe a new subscriber,
// which takes the events published after it subscribed, and no deadlock.
func TestHandlerSubscribes(t *testing.T) {
	bus := inproc.New()
	got := make(chan string, 4)
	cancel, err := bus.Subscribe("demo.>", func(e events.Event) {
		if e.ID != "1" {
			return
		}
		if _, err := bus.Subscribe("demo.>", func(e events.Event) { got <- e.ID }); err != nil {
			t.Errorf("Subscribe in a handler: %v", err)
		}
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer cancel()
	ctx := t.Context()
	if err := bus.Publish(ctx, event("1", "demo.note.created.group.work")); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if err := bus.Publish(ctx, event("2", "demo.note.created.group.work")); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	select {
	case id := <-got:
		if id != "2" {
			t.Errorf("the new subscriber got %q, want 2", id)
		}
	default:
		t.Fatal("the new subscriber got no event")
	}
}

// TestHandlerPublishes checks that a handler may publish, and no deadlock.
func TestHandlerPublishes(t *testing.T) {
	bus := inproc.New()
	got := subscribe(t, bus, "demo.>")
	inner, err := bus.Subscribe("demo.>", func(e events.Event) {
		if e.ID != "1" {
			return
		}
		if err := bus.Publish(context.Background(), event("2", "demo.note.created.group.work")); err != nil {
			t.Errorf("Publish in a handler: %v", err)
		}
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer inner()
	if err := bus.Publish(t.Context(), event("1", "demo.note.created.group.work")); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if e := receive(t, got); e.ID != "1" {
		t.Errorf("the subscriber got %q, want 1", e.ID)
	}
	if e := receive(t, got); e.ID != "2" {
		t.Errorf("the subscriber got %q, want 2", e.ID)
	}
}

// TestSubscribeChecksFilter checks that a subscriber needs a valid filter.
func TestSubscribeChecksFilter(t *testing.T) {
	bus := inproc.New()
	if _, err := bus.Subscribe("demo..work", func(events.Event) {}); err == nil {
		t.Error("Subscribe took the filter demo..work")
	}
}
