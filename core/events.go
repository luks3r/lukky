package core

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
)

// Event is an untyped event. Prefer a Topic, which fixes the payload type.
type Event struct {
	Topic   string
	Payload any
}

type EventHandler func(context.Context, Event) error

type subscription struct {
	id      uint64
	scope   *scope
	handler EventHandler
}

// Bus delivers events in process, synchronously, in subscription order.
// It does not queue, persist, or retry events.
type Bus struct {
	mu     sync.RWMutex
	next   uint64
	topics map[string][]subscription
}

func NewBus() *Bus { return &Bus{topics: make(map[string][]subscription)} }

func (b *Bus) subscribe(s *scope, topic string, handler EventHandler) error {
	if topic == "" || handler == nil {
		return errors.New("topic and handler are required")
	}
	b.mu.Lock()
	b.next++
	id := b.next
	b.topics[topic] = append(b.topics[topic], subscription{id, s, handler})
	b.mu.Unlock()
	return s.own(func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		subs := slices.DeleteFunc(b.topics[topic], func(sub subscription) bool { return sub.id == id })
		if len(subs) == 0 {
			delete(b.topics, topic)
		} else {
			b.topics[topic] = subs
		}
	})
}

// Publish calls every subscriber of the event's topic. A failing subscriber
// does not stop later ones; all errors are returned together.
func (b *Bus) Publish(ctx context.Context, event Event) error {
	if event.Topic == "" {
		return errors.New("event topic is required")
	}
	b.mu.RLock()
	subs := slices.Clone(b.topics[event.Topic])
	b.mu.RUnlock()
	var errs []error
	for _, sub := range subs {
		callCtx, done, ok := sub.scope.acquire(ctx)
		if !ok {
			continue
		}
		err := func() error {
			defer done()
			return sub.scope.call("event handler", func() error { return sub.handler(callCtx, event) })
		}()
		if err != nil {
			errs = append(errs, fmt.Errorf("event %q: %w", event.Topic, err))
		}
	}
	return errors.Join(errs...)
}
