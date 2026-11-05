// Package greeter answers !ping and can post a periodic reminder.
package greeter

import (
	"context"
	"errors"
	"time"

	"github.com/luks3r/lukky/core"
)

// Module answers !ping and, when Interval is set, posts a reminder to Channel.
type Module struct {
	Interval time.Duration
	Channel  core.Target
}

func (m Module) Name() string { return "greeter" }
func (m Module) Start(host *core.Host) error {
	if m.Interval > 0 && m.Channel == (core.Target{}) {
		return errors.New("reminder channel is required when interval is set")
	}
	if err := host.Handle(core.Prefix("!ping"), func(ctx context.Context, msg core.Message, _ core.Match) error {
		return host.Reply(ctx, msg, "pong")
	}); err != nil {
		return err
	}
	if m.Interval > 0 {
		return host.Every(m.Interval, func(ctx context.Context) {
			if err := host.Send(ctx, m.Channel, "Lukky is running. Type !ping or !aliases"); err != nil {
				host.Logger().Warn("send reminder", "error", err)
			}
		})
	}
	return nil
}
