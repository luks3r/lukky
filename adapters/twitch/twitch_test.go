package twitch

import (
	"context"
	"log/slog"
	"testing"
	"time"

	irc "github.com/gempir/go-twitch-irc/v4"

	"github.com/luks3r/lukky/core"
)

type staticToken string

func (s staticToken) Token(context.Context) (string, error) { return string(s), nil }
func (staticToken) Invalidate()                             {}

func newTestAdapter(t *testing.T, queue int) *Adapter {
	t.Helper()
	a, err := New(Config{Username: "lukserbot", Tokens: staticToken("secret"), Channels: []string{"somebody"}, QueueSize: queue},
		core.NewBus(), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestToMessage(t *testing.T) {
	m := irc.PrivateMessage{
		ID:      "abc",
		Channel: "somebody",
		Message: "!ping",
		User: irc.User{
			ID: "42", Name: "viewer1", DisplayName: "Viewer1",
			Badges: map[string]int{"subscriber": 0}, IsMod: true,
		},
	}
	got := toMessage(m)
	if got.ID != "abc" || got.Text != "!ping" || got.Source != (core.Target{Platform: Platform, Channel: "somebody"}) {
		t.Fatalf("got %+v", got)
	}
	if got.User.ID != "42" || got.User.Name != "viewer1" || got.User.DisplayName != "Viewer1" {
		t.Fatalf("user %+v", got.User)
	}
	if got.User.Roles != core.RoleSubscriber|core.RoleModerator {
		t.Fatalf("roles %b", got.User.Roles)
	}
}

func TestRolesFromFlags(t *testing.T) {
	cases := []struct {
		user irc.User
		want core.Role
	}{
		{irc.User{}, 0},
		{irc.User{Badges: map[string]int{"founder": 0}}, core.RoleSubscriber},
		{irc.User{IsVip: true}, core.RoleVIP},
		{irc.User{IsBroadcaster: true}, core.RoleBroadcaster},
	}
	for _, c := range cases {
		if got := roles(c.user); got != c.want {
			t.Errorf("roles(%+v) = %b, want %b", c.user, got, c.want)
		}
	}
}

func TestSanitizeRemovesLineBreaks(t *testing.T) {
	if got := sanitize("hi\r\nPRIVMSG #x :spam\nend"); got != "hi PRIVMSG #x :spam end" {
		t.Fatalf("got %q", got)
	}
}

func TestSendRejectsOtherPlatforms(t *testing.T) {
	a := newTestAdapter(t, 1)
	if err := a.Send(context.Background(), core.Target{Platform: "console", Channel: "local"}, "hi"); err == nil {
		t.Fatal("expected error for foreign platform")
	}
	if err := a.Send(context.Background(), core.Target{Platform: Platform}, "hi"); err == nil {
		t.Fatal("expected error for empty channel")
	}
}

func TestEnqueueDropsWhenFull(t *testing.T) {
	a := newTestAdapter(t, 1)
	a.enqueue(core.Message{Text: "first"})
	a.enqueue(core.Message{Text: "second"}) // must not block
	if len(a.queue) != 1 || (<-a.queue).Text != "first" {
		t.Fatal("queue should keep the first message and drop the second")
	}
}

func TestNewValidatesConfig(t *testing.T) {
	bus := core.NewBus()
	for _, cfg := range []Config{
		{Tokens: staticToken("t"), Channels: []string{"c"}},
		{Username: "u", Channels: []string{"c"}},
		{Username: "u", Tokens: staticToken("t")},
	} {
		if _, err := New(cfg, bus, nil); err == nil {
			t.Errorf("New(%+v) succeeded", cfg)
		}
	}
}

func TestRunStopsOnCancelWhileReconnecting(t *testing.T) {
	a := newTestAdapter(t, 1)
	a.client.IrcAddress = "127.0.0.1:1" // nothing listens here, so every dial fails
	a.client.TLS = false
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop after cancellation")
	}
}
