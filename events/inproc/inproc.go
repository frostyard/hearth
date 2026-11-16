// Package inproc is hearth's in-process event transport: 1 bus delivers each
// event to the subscribers of the same process, on the goroutine that published
// it, and it drops an event that it has already delivered.
//
// The bus keeps the last 1024 ids it delivered, so a relay that publishes an
// event again after a lost acknowledgement is harmless: the de-duplication is
// the 1 that the broker's JetStream does with the message's id.
package inproc

import (
	"context"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/frostyard/hearth/events"
)

// window is how many of the delivered ids the bus remembers.
const window = 1024

// Bus is an in-process transport: every subscriber of the process, in the order
// it subscribed.
//
// A handler runs on the goroutine that published, outside the bus's lock, so it
// may subscribe and publish. It must not block for long: it holds the
// publisher.
type Bus struct {
	// mu guards the subscribers and the delivered ids.
	mu sync.Mutex
	// subs are the subscribers, in the order they subscribed.
	subs []*subscription
	// delivered are the last ids the bus delivered, at most window of them.
	delivered []string
	// next is the place of the next id in delivered.
	next int
}

// subscription is 1 subscriber of the bus.
type subscription struct {
	// filter is the subscriber's broker filter.
	filter string
	// handle is the subscriber's function.
	handle func(events.Event)
	// gone is set when the subscription is cancelled.
	gone atomic.Bool
	// once makes a 2nd cancel harmless.
	once sync.Once
}

// New returns a bus with no subscriber.
func New() *Bus {
	return &Bus{}
}

// Publish implements events.Transport: it delivers the event to each subscriber
// whose filter takes the event's topic, in the order they subscribed, and it
// skips an event whose id is among the last 1024 ids the bus delivered.
//
// The context is not read: a subscriber of the process holds the event at once.
func (b *Bus) Publish(_ context.Context, e events.Event) error {
	b.mu.Lock()
	if e.ID != "" && slices.Contains(b.delivered, e.ID) {
		b.mu.Unlock()
		return nil
	}
	if e.ID != "" {
		b.remember(e.ID)
	}
	subs := slices.Clone(b.subs)
	b.mu.Unlock()

	for _, sub := range subs {
		if sub.gone.Load() || !events.Match(sub.filter, e.Topic) {
			continue
		}
		sub.handle(e)
	}
	return nil
}

// Subscribe subscribes 1 handler to the events that filter takes. The filter
// must be valid (see events.ValidFilter).
//
// The cancel function removes the subscription, and it is safe to call twice.
func (b *Bus) Subscribe(filter string, handle func(events.Event)) (func(), error) {
	if err := events.ValidFilter(filter); err != nil {
		return nil, err
	}
	sub := &subscription{filter: filter, handle: handle}
	b.mu.Lock()
	b.subs = append(b.subs, sub)
	b.mu.Unlock()
	return func() {
		sub.once.Do(func() {
			sub.gone.Store(true)
			b.mu.Lock()
			defer b.mu.Unlock()
			b.subs = slices.DeleteFunc(b.subs, func(s *subscription) bool { return s == sub })
		})
	}, nil
}

// remember records 1 delivered id, and forgets the oldest 1 past the window.
// b.mu must be held.
func (b *Bus) remember(id string) {
	if len(b.delivered) < window {
		b.delivered = append(b.delivered, id)
		return
	}
	b.delivered[b.next] = id
	b.next = (b.next + 1) % window
}
