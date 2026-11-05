package main

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"

	"github.com/luks3r/lukky/core"
)

// The console is trusted local input, so its user is the broadcaster.
var (
	consoleTarget = core.Target{Platform: "console", Channel: "local"}
	consoleUser   = core.User{ID: "local", Name: "local", DisplayName: "local", Roles: core.RoleBroadcaster}
)

type consoleChat struct{ mu sync.Mutex }

func (c *consoleChat) Send(_ context.Context, to core.Target, text string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if to == consoleTarget {
		fmt.Println("bot:", text)
	} else {
		fmt.Printf("bot -> %s/%s: %s\n", to.Platform, to.Channel, text)
	}
	return nil
}

func printConsoleHelp() {
	fmt.Println("Lukky")
	fmt.Println("Commands: !ping, !addalias <name> <text>, !delalias <name>, !aliases, !<name>")
	fmt.Println("Control: :modules, :reload <module>, :quit")
}

// readConsole publishes each input line as a chat message. It returns true
// when the user types :quit and false when stdin closes.
func readConsole(ctx context.Context, events *core.Bus, manager *core.Manager, factories map[string]factory) bool {
	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("> ")
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				slog.Error("read console", "error", err)
			}
			return false
		}
		switch line := strings.TrimSpace(scanner.Text()); line {
		case "":
		case ":quit":
			return true
		case ":modules":
			fmt.Println("loaded:", strings.Join(manager.List(), ", "))
		default:
			if name, ok := strings.CutPrefix(line, ":reload "); ok {
				reload(manager, factories, strings.TrimSpace(name))
				continue
			}
			msg := core.Message{Source: consoleTarget, User: consoleUser, Text: line}
			if err := core.Emit(ctx, events, core.ChatMessage, msg); err != nil {
				slog.Error("handle console message", "error", err)
			}
		}
	}
}

// reload unloads a module and loads a fresh instance. Lua modules are read
// from disk again, so edits to init.lua take effect.
func reload(manager *core.Manager, factories map[string]factory, name string) {
	newModule, ok := factories[name]
	if !ok {
		fmt.Println("cannot reload", name)
		return
	}
	module, err := newModule()
	if err != nil {
		slog.Error("reload", "module", name, "error", err)
		return
	}
	if err := manager.Unload(name); err != nil {
		slog.Error("reload", "module", name, "error", err)
		return
	}
	if err := manager.Load(module); err != nil {
		slog.Error("reload", "module", name, "error", err)
		return
	}
	fmt.Println(name, "reloaded")
}
