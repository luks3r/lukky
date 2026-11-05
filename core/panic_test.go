package core

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestCommandPanicBecomesErrorAndLaterRoutesRun(t *testing.T) {
	m, _, router := newTestManager(t)
	var later atomic.Int32
	mod := &testModule{name: "panicky", start: func(h *Host) error {
		if err := h.Handle(Exact("!boom"), func(context.Context, Message, Match) error { panic("boom") }); err != nil {
			return err
		}
		return h.Handle(Exact("!boom"), func(context.Context, Message, Match) error { later.Add(1); return nil })
	}}
	if err := m.Load(mod); err != nil {
		t.Fatal(err)
	}
	err := router.Dispatch(context.Background(), Message{Text: "!boom"})
	if p, ok := errors.AsType[*PanicError](err); !ok || p.Value != "boom" || len(p.Stack) == 0 {
		t.Fatalf("expected PanicError with stack, got %v", err)
	}
	if later.Load() != 1 {
		t.Fatal("later route did not run")
	}
}

func TestEventPanicBecomesError(t *testing.T) {
	m, bus, _ := newTestManager(t)
	mod := &testModule{name: "panicky", start: func(h *Host) error {
		return h.On("test", func(context.Context, Event) error { panic("boom") })
	}}
	if err := m.Load(mod); err != nil {
		t.Fatal(err)
	}
	if err := bus.Publish(context.Background(), Event{Topic: "test"}); !isPanic(err) {
		t.Fatalf("expected PanicError, got %v", err)
	}
}

func TestStartPanicRollsBack(t *testing.T) {
	m, _, router := newTestManager(t)
	var called atomic.Int32
	mod := &testModule{name: "bad", start: func(h *Host) error {
		if err := h.Handle(Exact("bad"), func(context.Context, Message, Match) error { called.Add(1); return nil }); err != nil {
			return err
		}
		panic("broken start")
	}}
	if err := m.Load(mod); !isPanic(err) {
		t.Fatalf("expected PanicError, got %v", err)
	}
	if err := router.Dispatch(context.Background(), Message{Text: "bad"}); err != nil {
		t.Fatal(err)
	}
	if called.Load() != 0 || len(m.List()) != 0 {
		t.Fatal("panicking start was not rolled back")
	}
}

func TestBackgroundPanicIsContained(t *testing.T) {
	m, _, _ := newTestManager(t)
	done := make(chan struct{})
	mod := &testModule{name: "bg", start: func(h *Host) error {
		return h.Go(func(context.Context) { defer close(done); panic("background") })
	}}
	if err := m.Load(mod); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("task did not run")
	}
	if err := m.Unload("bg"); err != nil {
		t.Fatal(err)
	}
}
