package core

import (
	"context"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestTickerStopsOnUnload(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, _, _ := newTestManager(t)
		var runs atomic.Int32
		mod := &testModule{name: "timer", start: func(h *Host) error {
			return h.Every(5*time.Millisecond, func(context.Context) { runs.Add(1) })
		}}
		if err := m.Load(mod); err != nil {
			t.Fatal(err)
		}
		time.Sleep(12 * time.Millisecond)
		synctest.Wait()
		if got := runs.Load(); got != 2 {
			t.Fatalf("runs before unload = %d, want 2", got)
		}
		if err := m.Unload("timer"); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Hour)
		synctest.Wait()
		if got := runs.Load(); got != 2 {
			t.Fatalf("ticker ran after unload: runs = %d", got)
		}
	})
}

func TestTickerSurvivesPanickingRun(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, _, _ := newTestManager(t)
		var runs atomic.Int32
		mod := &testModule{name: "timer", start: func(h *Host) error {
			return h.Every(5*time.Millisecond, func(context.Context) {
				if runs.Add(1) == 1 {
					panic("first run")
				}
			})
		}}
		if err := m.Load(mod); err != nil {
			t.Fatal(err)
		}
		time.Sleep(16 * time.Millisecond)
		synctest.Wait()
		if got := runs.Load(); got != 3 {
			t.Fatalf("runs = %d, want 3", got)
		}
	})
}

func TestAfterRunsOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, _, _ := newTestManager(t)
		var runs atomic.Int32
		mod := &testModule{name: "delay", start: func(h *Host) error {
			return h.After(5*time.Millisecond, func(context.Context) { runs.Add(1) })
		}}
		if err := m.Load(mod); err != nil {
			t.Fatal(err)
		}
		time.Sleep(4 * time.Millisecond)
		synctest.Wait()
		if runs.Load() != 0 {
			t.Fatal("ran before the delay")
		}
		time.Sleep(time.Hour)
		synctest.Wait()
		if got := runs.Load(); got != 1 {
			t.Fatalf("runs = %d, want 1", got)
		}
	})
}

func TestAfterCancelledOnUnload(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, _, _ := newTestManager(t)
		var runs atomic.Int32
		mod := &testModule{name: "delay", start: func(h *Host) error {
			return h.After(50*time.Millisecond, func(context.Context) { runs.Add(1) })
		}}
		if err := m.Load(mod); err != nil {
			t.Fatal(err)
		}
		if err := m.Unload("delay"); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Hour)
		synctest.Wait()
		if runs.Load() != 0 {
			t.Fatal("delayed task ran after unload")
		}
	})
}
