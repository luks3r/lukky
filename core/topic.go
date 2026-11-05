package core

import (
	"context"
	"encoding/json"
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

// Subscribe registers a typed handler for topic. A map or list payload, as sent
// by script modules, is decoded into T. Any other payload type is reported as
// an error and not passed on.
func Subscribe[T any](h *Host, topic Topic[T], handler func(context.Context, T) error) error {
	if handler == nil {
		return fmt.Errorf("handler for %q is required", topic.Name)
	}
	return h.On(topic.Name, func(ctx context.Context, event Event) error {
		payload, err := payloadAs[T](event.Payload)
		if err != nil {
			return err
		}
		return handler(ctx, payload)
	})
}

// payloadAs returns payload as T. Payloads from script modules arrive as
// maps or lists and are decoded into T through their JSON form.
func payloadAs[T any](payload any) (T, error) {
	var out T
	if p, ok := payload.(T); ok {
		return p, nil
	}
	switch payload.(type) {
	case map[string]any, []any:
		data, err := json.Marshal(payload)
		if err == nil {
			err = json.Unmarshal(data, &out)
		}
		if err != nil {
			return out, fmt.Errorf("decode payload into %T: %w", out, err)
		}
		return out, nil
	}
	return out, fmt.Errorf("payload is %T, want %T", payload, out)
}
