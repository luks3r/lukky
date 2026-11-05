// Package aliases implements user-managed text aliases. Moderators save a reply
// under a name, and anyone can then trigger it with !name. One static route
// resolves every alias, so adding an alias never registers a new handler.
package aliases

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/luks3r/lukky/core"
)

const (
	maxNameLength = 64
	// Twitch rejects chat messages longer than 500 characters.
	maxTextLength = 500
)

// Module stores aliases in its host storage namespace, keyed by normalized name.
type Module struct {
	reserved map[string]bool
}

// New creates the module. Reserved names are commands owned by other modules,
// such as "ping"; an alias with one of those names would answer alongside them.
func New(reserved ...string) *Module {
	m := &Module{reserved: map[string]bool{"addalias": true, "delalias": true, "aliases": true}}
	for _, name := range reserved {
		m.reserved[normalize(name)] = true
	}
	return m
}

func (m *Module) Name() string { return "aliases" }

func (m *Module) Start(host *core.Host) error {
	store := host.Store()
	moderator := func(command string) core.Matcher {
		return core.RequireRole(core.RoleModerator, core.Prefix(command))
	}
	if err := host.Handle(moderator("!addalias"), func(ctx context.Context, msg core.Message, match core.Match) error {
		if len(match.Args) < 2 {
			return host.Reply(ctx, msg, "Usage: !addalias <name> <text>")
		}
		name := normalize(match.Args[0])
		if problem := m.checkName(name); problem != "" {
			return host.Reply(ctx, msg, problem)
		}
		text := afterFields(msg.Text, 2)
		if utf8.RuneCountInString(text) > maxTextLength {
			return host.Reply(ctx, msg, fmt.Sprintf("Text must be at most %d characters", maxTextLength))
		}
		if err := store.Put(ctx, name, []byte(text)); err != nil {
			return err
		}
		return host.Reply(ctx, msg, "Saved !"+name)
	}); err != nil {
		return err
	}
	if err := host.Handle(moderator("!delalias"), func(ctx context.Context, msg core.Message, match core.Match) error {
		if len(match.Args) != 1 {
			return host.Reply(ctx, msg, "Usage: !delalias <name>")
		}
		name := normalize(match.Args[0])
		if err := store.Delete(ctx, name); err != nil {
			if errors.Is(err, core.ErrNotFound) {
				return host.Reply(ctx, msg, "Unknown alias")
			}
			return err
		}
		return host.Reply(ctx, msg, "Removed !"+name)
	}); err != nil {
		return err
	}
	if err := host.Handle(core.Exact("!aliases"), func(ctx context.Context, msg core.Message, _ core.Match) error {
		values, err := store.List(ctx)
		if err != nil {
			return err
		}
		if len(values) == 0 {
			return host.Reply(ctx, msg, "No aliases configured")
		}
		names := slices.Sorted(maps.Keys(values))
		for i, name := range names {
			names[i] = "!" + name
		}
		return host.Reply(ctx, msg, "Aliases: "+strings.Join(names, ", "))
	}); err != nil {
		return err
	}
	// One static route resolves all user-managed aliases on demand.
	return host.Handle(func(msg core.Message) (core.Match, bool) {
		fields := strings.Fields(msg.Text)
		if len(fields) != 1 || !strings.HasPrefix(fields[0], "!") {
			return core.Match{}, false
		}
		name := normalize(fields[0])
		if name == "" || m.reserved[name] {
			return core.Match{}, false
		}
		// This is syntax recognition only. Storage lookup belongs in the handler.
		return core.Match{Args: []string{name}}, true
	}, func(ctx context.Context, msg core.Message, match core.Match) error {
		text, err := store.Get(ctx, match.Args[0])
		if errors.Is(err, core.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		return host.Reply(ctx, msg, string(text))
	})
}

// normalize lowercases a name and drops one leading "!", so "!Discord" and
// "discord" refer to the same alias.
func normalize(name string) string {
	return strings.ToLower(strings.TrimPrefix(name, "!"))
}

// checkName returns a user-facing problem, or "" when the name is usable.
// Names may contain any characters except whitespace and control characters.
func (m *Module) checkName(name string) string {
	switch {
	case name == "":
		return "Alias name must not be empty"
	case utf8.RuneCountInString(name) > maxNameLength:
		return fmt.Sprintf("Alias name must be at most %d characters", maxNameLength)
	case strings.ContainsFunc(name, unicode.IsControl):
		return "Alias name must not contain control characters"
	case m.reserved[name]:
		return "!" + name + " is a built-in command"
	}
	return ""
}

// afterFields returns text after its first n whitespace-separated fields,
// keeping the original spacing of the remainder.
func afterFields(text string, n int) string {
	text = strings.TrimLeftFunc(text, unicode.IsSpace)
	for range n {
		end := strings.IndexFunc(text, unicode.IsSpace)
		if end < 0 {
			return ""
		}
		text = strings.TrimLeftFunc(text[end:], unicode.IsSpace)
	}
	return strings.TrimRightFunc(text, unicode.IsSpace)
}
