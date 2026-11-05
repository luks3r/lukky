package core

import (
	"context"
	"fmt"
)

// ChatSender delivers outgoing chat messages. Adapters implement it.
type ChatSender interface {
	Send(ctx context.Context, to Target, text string) error
}

// ChatByPlatform sends each message through the sender for its target's platform.
type ChatByPlatform map[string]ChatSender

func (c ChatByPlatform) Send(ctx context.Context, to Target, text string) error {
	sender, ok := c[to.Platform]
	if !ok {
		return fmt.Errorf("no chat sender for platform %q", to.Platform)
	}
	return sender.Send(ctx, to, text)
}
