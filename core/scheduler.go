package core

import (
	"context"
	"errors"
	"time"
)

// Timers run in memory only. Missed runs are not replayed after a restart.

// every runs job every interval in a background task. A panicking run is
// logged and does not stop later runs.
func every(s *scope, interval time.Duration, job func(context.Context)) error {
	if interval <= 0 {
		return errors.New("interval must be positive")
	}
	if job == nil {
		return errors.New("job is required")
	}
	return s.goTask(func(ctx context.Context) {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if ctx.Err() != nil {
					return
				}
				_ = s.call("scheduled task", func() error { job(ctx); return nil })
			}
		}
	})
}

func after(s *scope, delay time.Duration, job func(context.Context)) error {
	if delay < 0 {
		return errors.New("delay must not be negative")
	}
	if job == nil {
		return errors.New("job is required")
	}
	return s.goTask(func(ctx context.Context) {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
		case <-timer.C:
			if ctx.Err() == nil {
				job(ctx)
			}
		}
	})
}
