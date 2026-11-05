package core

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestModuleUnloadRemovesRegistrations(t *testing.T) {
	m, bus, router := newTestManager(t)
	var events, commands atomic.Int32
	stopped := &atomic.Int32{}
	module := &testModule{name: "demo", stopped: stopped, start: func(h *Host) error {
		if err := h.On("test", func(context.Context, Event) error { events.Add(1); return nil }); err != nil {
			return err
		}
		return h.Handle(Prefix("!test"), func(context.Context, Message, Match) error { commands.Add(1); return nil })
	}}
	if err := m.Load(module); err != nil {
		t.Fatal(err)
	}
	if err := bus.Publish(context.Background(), Event{Topic: "test"}); err != nil {
		t.Fatal(err)
	}
	if err := router.Dispatch(context.Background(), Message{Text: "!test"}); err != nil {
		t.Fatal(err)
	}
	if err := m.Unload("demo"); err != nil {
		t.Fatal(err)
	}
	if err := bus.Publish(context.Background(), Event{Topic: "test"}); err != nil {
		t.Fatal(err)
	}
	if err := router.Dispatch(context.Background(), Message{Text: "!test"}); err != nil {
		t.Fatal(err)
	}
	if events.Load() != 1 || commands.Load() != 1 || stopped.Load() != 1 {
		t.Fatalf("events=%d commands=%d stops=%d", events.Load(), commands.Load(), stopped.Load())
	}
}

func TestFailedStartRollsBack(t *testing.T) {
	m, _, router := newTestManager(t)
	var called atomic.Int32
	failed := &testModule{name: "bad", start: func(h *Host) error {
		if err := h.Handle(Exact("bad"), func(context.Context, Message, Match) error { called.Add(1); return nil }); err != nil {
			return err
		}
		return errors.New("broken config")
	}}
	if err := m.Load(failed); err == nil {
		t.Fatal("expected load failure")
	}
	if err := router.Dispatch(context.Background(), Message{Text: "bad"}); err != nil {
		t.Fatal(err)
	}
	if called.Load() != 0 {
		t.Fatal("registration leaked after failed start")
	}
	if len(m.List()) != 0 {
		t.Fatal("failed module appears loaded")
	}
}

func TestUnloadWaitsForInFlightHandler(t *testing.T) {
	m, _, router := newTestManager(t)
	started := make(chan struct{})
	exited := make(chan struct{})
	mod := &testModule{name: "blocking", start: func(h *Host) error {
		return h.Handle(Exact("block"), func(ctx context.Context, _ Message, _ Match) error {
			close(started)
			<-ctx.Done()
			close(exited)
			return nil
		})
	}}
	if err := m.Load(mod); err != nil {
		t.Fatal(err)
	}
	published := make(chan struct{})
	go func() { _ = router.Dispatch(context.Background(), Message{Text: "block"}); close(published) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("handler did not start")
	}
	unloaded := make(chan error, 1)
	go func() { unloaded <- m.Unload("blocking") }()
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("handler did not receive cancellation")
	}
	select {
	case err := <-unloaded:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("unload did not finish")
	}
	<-published
}

func TestLoggerTaggedWithModuleName(t *testing.T) {
	out := &syncBuffer{}
	m, err := NewManager(context.Background(), Services{
		Events: NewBus(), Commands: NewRouter(), Chat: testChat{},
		Logger: slog.New(slog.NewTextHandler(out, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	mod := &testModule{name: "chatty", start: func(h *Host) error { h.Logger().Info("hello"); return nil }}
	if err := m.Load(mod); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "module=chatty") {
		t.Fatalf("log missing module tag: %q", out.String())
	}
}

func TestReplyGoesToSource(t *testing.T) {
	chat := &recordingChat{}
	router := NewRouter()
	m, err := NewManager(context.Background(), Services{Events: NewBus(), Commands: router, Chat: chat, Logger: discardLogger})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	mod := &testModule{name: "echo", start: func(h *Host) error {
		return h.Handle(Exact("!echo"), func(ctx context.Context, msg Message, _ Match) error { return h.Reply(ctx, msg, "echo") })
	}}
	if err := m.Load(mod); err != nil {
		t.Fatal(err)
	}
	src := Target{Platform: "twitch", Channel: "somebody"}
	if err := router.Dispatch(context.Background(), Message{Source: src, Text: "!echo"}); err != nil {
		t.Fatal(err)
	}
	if len(chat.to) != 1 || chat.to[0] != src {
		t.Fatalf("sent to %v", chat.to)
	}
}
