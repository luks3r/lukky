package core

import (
	"context"
	"reflect"
	"regexp"
	"sync"
	"testing"
)

func TestRouterOrderAndArgs(t *testing.T) {
	m, _, router := newTestManager(t)
	var mu sync.Mutex
	var hits []string
	module := &testModule{name: "routes", start: func(h *Host) error {
		for _, label := range []string{"first", "second"} {
			if err := h.Handle(Prefix("!run"), func(_ context.Context, _ Message, match Match) error {
				mu.Lock()
				defer mu.Unlock()
				hits = append(hits, label+":"+match.Args[0])
				return nil
			}); err != nil {
				return err
			}
		}
		return nil
	}}
	if err := m.Load(module); err != nil {
		t.Fatal(err)
	}
	if err := router.Dispatch(context.Background(), Message{Text: "!run now"}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(hits, []string{"first:now", "second:now"}) {
		t.Fatalf("got %v", hits)
	}
}

func TestRegexMatcher(t *testing.T) {
	match := Regex(regexp.MustCompile(`^!roll (\d+)d(\d+)$`))
	got, ok := match(Message{Text: "  !roll 2d6 "})
	if !ok || !reflect.DeepEqual(got.Args, []string{"2", "6"}) {
		t.Fatalf("got %v %v", got, ok)
	}
	if _, ok := match(Message{Text: "!roll many"}); ok {
		t.Fatal("unexpected match")
	}
}

func TestRequireRole(t *testing.T) {
	match := RequireRole(RoleModerator, Exact("!edit"))
	if _, ok := match(Message{Text: "!edit", User: User{Roles: RoleVIP}}); ok {
		t.Fatal("VIP passed moderator check")
	}
	if _, ok := match(Message{Text: "!edit", User: User{Roles: RoleBroadcaster}}); !ok {
		t.Fatal("broadcaster rejected")
	}
	if _, ok := match(Message{Text: "!other", User: User{Roles: RoleBroadcaster}}); ok {
		t.Fatal("inner matcher ignored")
	}
}
