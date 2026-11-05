// Package twitch connects Twitch chat to the core over IRC.
//
// Incoming chat messages become core.ChatMessage events. They pass
// through a bounded queue, so a slow handler never blocks the IRC reader.
// The adapter also implements core.ChatSender for the "twitch" platform.
package twitch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	irc "github.com/gempir/go-twitch-irc/v4"

	"github.com/luks3r/lukky/core"
)

const (
	Platform         = "twitch"
	defaultQueueSize = 256
)

type Config struct {
	Username  string      // bot account login
	Tokens    TokenSource // supplies the chat access token before each connection
	Channels  []string    // channel logins to join
	QueueSize int         // incoming message buffer; defaults to 256
}

type Adapter struct {
	client *irc.Client
	tokens TokenSource
	events *core.Bus
	log    *slog.Logger
	queue  chan core.Message
}

func New(cfg Config, events *core.Bus, log *slog.Logger) (*Adapter, error) {
	if cfg.Username == "" || cfg.Tokens == nil {
		return nil, errors.New("twitch: username and token source are required")
	}
	if len(cfg.Channels) == 0 {
		return nil, errors.New("twitch: at least one channel is required")
	}
	if events == nil {
		return nil, errors.New("twitch: event bus is required")
	}
	if log == nil {
		log = slog.Default()
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = defaultQueueSize
	}
	a := &Adapter{
		client: irc.NewClient(cfg.Username, ""),
		tokens: cfg.Tokens,
		events: events,
		log:    log.With("adapter", Platform),
		queue:  make(chan core.Message, cfg.QueueSize),
	}
	a.client.OnPrivateMessage(func(m irc.PrivateMessage) { a.enqueue(toMessage(m)) })
	a.client.Join(cfg.Channels...)
	return a, nil
}

// enqueue never blocks the IRC reader. When the queue is full, the message is dropped.
func (a *Adapter) enqueue(msg core.Message) {
	select {
	case a.queue <- msg:
	default:
		a.log.Warn("incoming queue full; message dropped", "channel", msg.Source.Channel, "user", msg.User.Name)
	}
}

// Run connects to Twitch and publishes incoming messages until ctx is cancelled.
// It reconnects with backoff after connection failures.
func (a *Adapter) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup
	defer wg.Wait()
	wg.Go(func() { a.publish(ctx) })

	// Disconnect on cancellation. OnConnect covers cancellation during a dial.
	stop := context.AfterFunc(ctx, func() { _ = a.client.Disconnect() })
	defer stop()
	a.client.OnConnect(func() {
		if ctx.Err() != nil {
			_ = a.client.Disconnect()
			return
		}
		a.log.Info("connected")
	})

	backoff := time.Second
	for {
		err := a.connect(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if errors.Is(err, irc.ErrLoginAuthenticationFailed) {
			a.tokens.Invalidate()
		}
		a.log.Warn("connection lost; reconnecting", "error", err, "in", backoff)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, time.Minute)
	}
}

// connect fetches a current token and blocks while the connection is up.
func (a *Adapter) connect(ctx context.Context) error {
	token, err := a.tokens.Token(ctx)
	if err != nil {
		return err
	}
	a.client.SetIRCToken("oauth:" + strings.TrimPrefix(token, "oauth:"))
	return a.client.Connect()
}

func (a *Adapter) publish(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-a.queue:
			if err := core.Emit(ctx, a.events, core.ChatMessage, msg); err != nil {
				a.log.Error("handle chat message", "error", err)
			}
		}
	}
}

// Send writes text to a Twitch channel. Line breaks are replaced with spaces,
// so text cannot inject extra IRC commands.
func (a *Adapter) Send(_ context.Context, to core.Target, text string) error {
	if to.Platform != Platform {
		return fmt.Errorf("twitch: cannot send to platform %q", to.Platform)
	}
	if to.Channel == "" {
		return errors.New("twitch: channel is required")
	}
	a.client.Say(to.Channel, sanitize(text))
	return nil
}

var lineBreaks = strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ")

func sanitize(text string) string { return lineBreaks.Replace(text) }

func toMessage(m irc.PrivateMessage) core.Message {
	return core.Message{
		ID:     m.ID,
		Source: core.Target{Platform: Platform, Channel: m.Channel},
		User: core.User{
			ID:          m.User.ID,
			Name:        m.User.Name,
			DisplayName: m.User.DisplayName,
			Roles:       roles(m.User),
		},
		Text: m.Message,
		Raw:  m,
	}
}

func roles(u irc.User) core.Role {
	var r core.Role
	// The badge version encodes tenure and may be 0, so check presence only.
	_, sub := u.Badges["subscriber"]
	_, founder := u.Badges["founder"]
	if sub || founder {
		r |= core.RoleSubscriber
	}
	if u.IsVip {
		r |= core.RoleVIP
	}
	if u.IsMod {
		r |= core.RoleModerator
	}
	if u.IsBroadcaster {
		r |= core.RoleBroadcaster
	}
	return r
}
