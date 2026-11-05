package core

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
)

var discardLogger = slog.New(slog.DiscardHandler)

type testChat struct{}

func (testChat) Send(context.Context, Target, string) error { return nil }

func (c *recordingChat) Send(_ context.Context, to Target, _ string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.to = append(c.to, to)
	return nil
}

type testModule struct {
	name    string
	start   func(*Host) error
	stopped *atomic.Int32
}

func (m *testModule) Name() string { return m.name }

func (m *testModule) Start(h *Host) error {
	if m.start != nil {
		return m.start(h)
	}
	return nil
}

func (m *testModule) Stop(context.Context) error {
	if m.stopped != nil {
		m.stopped.Add(1)
	}
	return nil
}

func newTestManager(t *testing.T) (*Manager, *Bus, *Router) {
	t.Helper()
	bus, router := NewBus(), NewRouter()
	m, err := NewManager(context.Background(), Services{Events: bus, Commands: router, Chat: testChat{}, Logger: discardLogger})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := m.Close(); err != nil {
			t.Error(err)
		}
	})
	return m, bus, router
}

func isPanic(err error) bool {
	_, ok := errors.AsType[*PanicError](err)
	return ok
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type recordingChat struct {
	mu sync.Mutex
	to []Target
}
