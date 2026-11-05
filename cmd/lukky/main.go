// Command lukky runs the bot with a local console and, when configured, Twitch chat.
//
// Settings come from lukky.toml, .env, and the environment; see Config.
// Twitch is enabled when twitch.client_id (TWITCH_CLIENT_ID) is set.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/lmittmann/tint"

	"github.com/luks3r/lukky/adapters/twitch"
	"github.com/luks3r/lukky/core"
	"github.com/luks3r/lukky/modules/aliases"
	"github.com/luks3r/lukky/modules/greeter"
	"github.com/luks3r/lukky/storage/sqlite"
)

const (
	configPath      = "lukky.toml"
	dotenvPath      = ".env"
	databasePath    = ".lukky/lukky.sqlite"
	twitchStatePath = ".lukky/twitch-token.json" // holds secrets
)

// otherCommands are commands of other modules that aliases must not shadow.
var otherCommands = []string{"ping"}

func main() {
	cfg, err := loadConfig(configPath, dotenvPath, os.Environ)
	if err != nil {
		log.Fatal(err)
	}
	logger, err := newLogger(cfg.Log.Level)
	if err != nil {
		log.Fatal(err)
	}
	slog.SetDefault(logger)
	if err := run(cfg); err != nil {
		slog.Error("lukky stopped", "error", err)
		os.Exit(1)
	}
}

// newLogger writes colored text to a terminal and JSON otherwise,
// for example under a service manager.
func newLogger(level string) (*slog.Logger, error) {
	var l slog.Level
	if err := l.UnmarshalText([]byte(level)); err != nil {
		return nil, fmt.Errorf("log.level: %w", err)
	}
	if info, err := os.Stderr.Stat(); err == nil && info.Mode()&os.ModeCharDevice != 0 {
		return slog.New(tint.NewTextHandler(os.Stderr, &tint.Options{Level: l, TimeFormat: time.TimeOnly})), nil
	}
	return slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: l})), nil
}

// run starts everything and blocks until a signal, :quit, or a closed stdin
// without Twitch. Deferred calls shut down in reverse: adapters, then modules,
// then storage.
func run(cfg Config) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Open storage first: it also fails fast when another instance is running.
	storage, err := sqlite.Open(ctx, databasePath)
	if err != nil {
		return err
	}
	defer closeAndLog("storage", storage.Close)

	events, commands := core.NewBus(), core.NewRouter()
	chat := core.ChatByPlatform{consoleTarget.Platform: &consoleChat{}}
	tw, auth, err := newTwitch(ctx, cfg, events)
	if err != nil {
		return err
	}
	if tw != nil {
		chat[twitch.Platform] = tw
	}

	manager, err := core.NewManager(ctx, core.Services{Events: events, Commands: commands, Chat: chat, Storage: storage})
	if err != nil {
		return err
	}
	defer closeAndLog("modules", manager.Close)
	// Go modules load in this order; Lua modules follow.
	goModules := []struct {
		name string
		new  factory
	}{
		{"chat-ingress", func() (core.Module, error) { return chatIngress{router: commands}, nil }},
		{"greeter", func() (core.Module, error) {
			return &greeter.Module{Interval: cfg.Greeter.Interval, Channel: consoleTarget}, nil
		}},
		{"aliases", func() (core.Module, error) { return aliases.New(otherCommands...), nil }},
	}
	factories := map[string]factory{}
	for _, m := range goModules {
		module, err := m.new()
		if err != nil {
			return err
		}
		if err := manager.Load(module); err != nil {
			return err
		}
		factories[m.name] = m.new
	}
	for name, newModule := range loadLuaModules(manager, cfg) {
		factories[name] = newModule
	}

	var adapters sync.WaitGroup
	defer adapters.Wait()
	defer stop() // runs before adapters.Wait, so the adapters see cancellation
	if tw != nil {
		adapters.Go(func() { auth.Run(ctx) })
		adapters.Go(func() {
			if err := tw.Run(ctx); err != nil {
				slog.Error("twitch adapter stopped", "error", err)
			}
		})
	}

	printConsoleHelp()
	quit := make(chan bool, 1)
	go func() { quit <- readConsole(ctx, events, manager, factories) }()
	select {
	case <-ctx.Done():
	case requested := <-quit:
		if !requested && tw != nil {
			// Stdin closed, for example under a service manager: keep serving Twitch.
			<-ctx.Done()
		}
	}
	return nil
}

func closeAndLog(what string, closeFn func() error) {
	if err := closeFn(); err != nil {
		slog.Error("close "+what, "error", err)
	}
}

// chatIngress routes chat messages from every adapter to the command router.
type chatIngress struct{ router *core.Router }

func (chatIngress) Name() string { return "chat-ingress" }

func (m chatIngress) Start(host *core.Host) error {
	return core.Subscribe(host, core.ChatMessage, m.router.Dispatch)
}

// newTwitch returns nil values when Twitch is not configured.
func newTwitch(ctx context.Context, cfg Config, events *core.Bus) (*twitch.Adapter, *twitch.Auth, error) {
	c := cfg.Twitch
	if c.ClientID == "" {
		return nil, nil, nil
	}
	if len(c.Channels) == 0 {
		return nil, nil, errors.New("twitch.channels (TWITCH_CHANNELS) is required when Twitch is configured")
	}
	auth, err := twitch.NewAuth(twitch.Credentials{
		ClientID:     c.ClientID,
		ClientSecret: c.ClientSecret,
		AccessToken:  c.UserToken,
		RefreshToken: c.UserRefreshToken,
	}, twitchStatePath, slog.Default())
	if err != nil {
		return nil, nil, err
	}
	login, err := auth.Login(ctx)
	if err != nil {
		return nil, nil, err
	}
	slog.Info("twitch account", "login", login, "channels", c.Channels)
	tw, err := twitch.New(twitch.Config{Username: login, Tokens: auth, Channels: c.Channels}, events, slog.Default())
	if err != nil {
		return nil, nil, err
	}
	return tw, auth, nil
}
