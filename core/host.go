package core

import (
	"context"
	"log/slog"
	"time"
)

// Services are shared by every module.
type Services struct {
	Events   *Bus
	Commands *Router
	Chat     ChatSender
	Storage  Storage      // optional; defaults to in-memory storage
	Logger   *slog.Logger // optional; defaults to slog.Default()
}

// Host is a module's access to the services. Everything registered through it
// belongs to the module and is removed when the module unloads.
type Host struct {
	name     string
	scope    *scope
	services Services
}

// Context is cancelled when the module unloads.
func (h *Host) Context() context.Context { return h.scope.ctx }

// Logger returns a logger tagged with the module name.
func (h *Host) Logger() *slog.Logger { return h.scope.log }

// Store returns key-value storage private to this module. Data survives
// unload and reload; whether it survives restarts depends on the backend.
func (h *Host) Store() Store { return Store{h.services.Storage, h.name} }

// Handle registers a command.
func (h *Host) Handle(matcher Matcher, handler CommandHandler) error {
	return h.services.Commands.handle(h.scope, matcher, handler)
}

// On subscribes to an event topic without type checking. Prefer Subscribe.
func (h *Host) On(topic string, handler EventHandler) error {
	return h.services.Events.subscribe(h.scope, topic, handler)
}

// Publish publishes an event without type checking. Prefer Emit.
func (h *Host) Publish(ctx context.Context, event Event) error {
	return h.services.Events.Publish(ctx, event)
}

// Every runs job every interval, first after one interval. Runs never overlap.
func (h *Host) Every(interval time.Duration, job func(context.Context)) error {
	return every(h.scope, interval, job)
}

// After runs job once after delay, unless the module unloads first.
func (h *Host) After(delay time.Duration, job func(context.Context)) error {
	return after(h.scope, delay, job)
}

// Go runs job in the background. Its context is cancelled on unload,
// and unload waits for it to return.
func (h *Host) Go(job func(context.Context)) error { return h.scope.goTask(job) }

// Own registers cleanup to run when the module unloads.
func (h *Host) Own(cleanup func()) error { return h.scope.own(cleanup) }

// Send sends text to a chat channel.
func (h *Host) Send(ctx context.Context, to Target, text string) error {
	return h.services.Chat.Send(ctx, to, text)
}

// Reply sends text to the channel msg came from.
func (h *Host) Reply(ctx context.Context, msg Message, text string) error {
	return h.Send(ctx, msg.Source, text)
}
