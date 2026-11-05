package core

import (
	"context"
	"fmt"
)

// Topic names an event and fixes its payload type, so producers and consumers
// agree on the payload at compile time. Payloads should be plain data:
// primitives, slices, maps, and structs of those, with no functions, channels,
// or shared mutable state.
type Topic[T any] struct{ Name string }

// ChatMessage is published by chat adapters for every incoming message.
var ChatMessage = Topic[Message]{Name: "chat.message"}

// Publisher is satisfied by *Bus and *Host.
type Publisher interface {
	Publish(context.Context, Event) error
}

// Emit publishes payload on topic.
func Emit[T any](ctx context.Context, p Publisher, topic Topic[T], payload T) error {
	return p.Publish(ctx, Event{Topic: topic.Name, Payload: payload})
}

// Subscribe registers a typed handler for topic. An event published on the same
// name with a different payload type is reported as an error, not passed on.
func Subscribe[T any](h *Host, topic Topic[T], handler func(context.Context, T) error) error {
	if handler == nil {
		return fmt.Errorf("handler for %q is required", topic.Name)
	}
	return h.On(topic.Name, func(ctx context.Context, event Event) error {
		payload, ok := event.Payload.(T)
		if !ok {
			var want T
			return fmt.Errorf("payload is %T, want %T", event.Payload, want)
		}
		return handler(ctx, payload)
	})
}
