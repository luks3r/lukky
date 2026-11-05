package aliases

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/luks3r/lukky/core"
)

var (
	channel   = core.Target{Platform: "test", Channel: "main"}
	moderator = core.User{Name: "mod", Roles: core.RoleModerator}
	viewer    = core.User{Name: "viewer"}
)

type outbox struct {
	mu       sync.Mutex
	messages []string
	targets  []core.Target
}

func (o *outbox) Send(_ context.Context, to core.Target, text string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.messages = append(o.messages, text)
	o.targets = append(o.targets, to)
	return nil
}

func (o *outbox) last() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.messages) == 0 {
		return ""
	}
	return o.messages[len(o.messages)-1]
}

func (o *outbox) count() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.messages)
}

type fixture struct {
	t       *testing.T
	manager *core.Manager
	router  *core.Router
	out     *outbox
	storage *core.MemoryStorage
}

func setup(t *testing.T, reserved ...string) *fixture {
	t.Helper()
	f := &fixture{t: t, router: core.NewRouter(), out: &outbox{}, storage: core.NewMemoryStorage()}
	manager, err := core.NewManager(context.Background(), core.Services{
		Events: core.NewBus(), Commands: f.router, Chat: f.out, Storage: f.storage,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	f.manager = manager
	if err := manager.Load(New(reserved...)); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *fixture) say(user core.User, text string) string {
	f.t.Helper()
	before := f.out.count()
	if err := f.router.Dispatch(context.Background(), core.Message{Source: channel, User: user, Text: text}); err != nil {
		f.t.Fatal(err)
	}
	if f.out.count() == before {
		return ""
	}
	return f.out.last()
}

func TestAliasStoresArbitraryText(t *testing.T) {
	f := setup(t)
	if got := f.say(moderator, "!addalias discord  Join us:   https://example.com/discord  "); got != "Saved !discord" {
		t.Fatalf("add: %q", got)
	}
	if got := f.say(viewer, "!discord"); got != "Join us:   https://example.com/discord" {
		t.Fatalf("got %q", got)
	}
	if f.out.targets[len(f.out.targets)-1] != channel {
		t.Fatal("reply went to the wrong channel")
	}
}

func TestAliasNamesAcceptAnyCharacters(t *testing.T) {
	f := setup(t)
	for _, name := range []string{"привет", "c++", "a.b/c?", "🎲", "!!x"} {
		f.say(moderator, "!addalias "+name+" text for "+name)
		trigger := "!" + strings.TrimPrefix(name, "!")
		if got := f.say(viewer, trigger); got != "text for "+name {
			t.Errorf("%s: got %q", trigger, got)
		}
	}
}

func TestAliasNamesAreCaseInsensitiveAndAcceptLeadingBang(t *testing.T) {
	f := setup(t)
	f.say(moderator, "!addalias !Discord hello")
	if got := f.say(viewer, "!DISCORD"); got != "hello" {
		t.Fatalf("got %q", got)
	}
	if got := f.say(moderator, "!delalias !discord"); got != "Removed !discord" {
		t.Fatalf("delete: %q", got)
	}
	if got := f.say(viewer, "!discord"); got != "" {
		t.Fatalf("deleted alias answered %q", got)
	}
}

func TestAliasRejectsReservedAndOversized(t *testing.T) {
	f := setup(t, "ping")
	if got := f.say(moderator, "!addalias ping pong2"); !strings.Contains(got, "built-in") {
		t.Fatalf("reserved: %q", got)
	}
	if got := f.say(moderator, "!addalias aliases x"); !strings.Contains(got, "built-in") {
		t.Fatalf("own command: %q", got)
	}
	if got := f.say(moderator, "!addalias long "+strings.Repeat("x", maxTextLength+1)); !strings.Contains(got, "at most") {
		t.Fatalf("long text: %q", got)
	}
	if got := f.say(moderator, "!addalias "+strings.Repeat("n", maxNameLength+1)+" x"); !strings.Contains(got, "at most") {
		t.Fatalf("long name: %q", got)
	}
	if got := f.say(moderator, "!addalias onlyname"); !strings.HasPrefix(got, "Usage") {
		t.Fatalf("missing text: %q", got)
	}
}

func TestAliasesSurviveModuleReload(t *testing.T) {
	f := setup(t)
	f.say(moderator, "!addalias discord https://example.com/discord")
	if err := f.manager.Unload("aliases"); err != nil {
		t.Fatal(err)
	}
	if err := f.manager.Load(New()); err != nil {
		t.Fatal(err)
	}
	if got := f.say(viewer, "!discord"); got != "https://example.com/discord" {
		t.Fatalf("after reload got %q", got)
	}
}

func TestViewersCannotEditAliases(t *testing.T) {
	f := setup(t)
	if got := f.say(viewer, "!addalias discord hello"); got != "" {
		t.Fatalf("viewer got a reply: %q", got)
	}
	ctx := context.Background()
	if _, err := f.storage.Get(ctx, "aliases", "discord"); err == nil {
		t.Fatal("viewer added an alias")
	}
	_ = f.storage.Put(ctx, "aliases", "discord", []byte("hello"))
	f.say(viewer, "!delalias discord")
	if _, err := f.storage.Get(ctx, "aliases", "discord"); err != nil {
		t.Fatal("viewer removed an alias")
	}
}

func TestListAliases(t *testing.T) {
	f := setup(t)
	if got := f.say(viewer, "!aliases"); got != "No aliases configured" {
		t.Fatalf("empty: %q", got)
	}
	f.say(moderator, "!addalias b two")
	f.say(moderator, "!addalias a one")
	if got := f.say(viewer, "!aliases"); got != "Aliases: !a, !b" {
		t.Fatalf("got %q", got)
	}
}

func TestAfterFields(t *testing.T) {
	cases := map[string]string{
		"!addalias x hello":        "hello",
		"  !addalias  x   a  b  ":  "a  b",
		"!addalias x":              "",
		"!addalias\tx\tline text ": "line text",
	}
	for in, want := range cases {
		if got := afterFields(in, 2); got != want {
			t.Errorf("afterFields(%q) = %q, want %q", in, got, want)
		}
	}
}
