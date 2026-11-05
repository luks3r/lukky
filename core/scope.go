package core

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"sync"
)

var ErrScopeClosed = errors.New("module scope is closed")

// scope owns the registrations and goroutines of one loaded module.
type scope struct {
	ctx      context.Context
	cancel   context.CancelFunc
	log      *slog.Logger
	mu       sync.Mutex
	closed   bool
	cleanups []func()
	wg       sync.WaitGroup
	once     sync.Once
}

func newScope(parent context.Context, log *slog.Logger) *scope {
	ctx, cancel := context.WithCancel(parent)
	return &scope{ctx: ctx, cancel: cancel, log: log}
}

// own registers cleanup for close. On a closed scope, cleanup runs at once.
func (s *scope) own(cleanup func()) error {
	if cleanup == nil {
		return errors.New("cleanup is required")
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		cleanup()
		return ErrScopeClosed
	}
	s.cleanups = append(s.cleanups, cleanup)
	s.mu.Unlock()
	return nil
}

// call runs module code. A panic is logged with its stack and returned as a *PanicError.
func (s *scope) call(what string, fn func() error) error {
	err := protect(fn)
	if p, ok := errors.AsType[*PanicError](err); ok {
		s.log.Error("module code panicked", "in", what, "panic", p.Value, "stack", string(p.Stack))
	}
	return err
}

// acquire marks a handler call as running, so close waits for it. The returned
// context ends when either parent or the scope ends. done must be called.
func (s *scope) acquire(parent context.Context) (ctx context.Context, done func(), ok bool) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, nil, false
	}
	s.wg.Add(1)
	s.mu.Unlock()
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(s.ctx, cancel)
	return ctx, func() { stop(); cancel(); s.wg.Done() }, true
}

func (s *scope) goTask(task func(context.Context)) error {
	if task == nil {
		return errors.New("task is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrScopeClosed
	}
	// Started under the lock, so close cannot begin waiting before the task is counted.
	s.wg.Go(func() {
		_ = s.call("background task", func() error { task(s.ctx); return nil })
	})
	return nil
}

// close cancels the scope, runs cleanups in reverse order, and waits for
// running work. It must not be called from the scope's own callbacks.
func (s *scope) close() {
	s.once.Do(func() {
		s.mu.Lock()
		s.closed = true
		cleanups := s.cleanups
		s.cleanups = nil
		s.cancel()
		s.mu.Unlock()
		for _, cleanup := range slices.Backward(cleanups) {
			cleanup()
		}
		s.wg.Wait()
	})
}
