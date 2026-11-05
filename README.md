# Lukky

Lukky is an early-stage Go runtime for stream automation. It handles chat commands, in-process events, and scheduled work without tying the application to Twitch or any other platform.

The aim is straightforward: write a feature as a Go module, give it access to a small set of services, and let the host manage its lifetime. Modules can register commands, react to events, schedule jobs, and send messages. When a module is unloaded, its registrations and running work are cancelled and cleaned up.

This repository is a working foundation, **not yet a production-ready Twitch bot**. A local console is always available for testing. Twitch chat is enabled with environment variables.

## Requirements

- Go 1.27 or newer
- Main libraries: [`go-twitch-irc`](https://github.com/gempir/go-twitch-irc) for Twitch chat, [`modernc.org/sqlite`](https://pkg.go.dev/modernc.org/sqlite) for storage (pure Go, no cgo), [`koanf`](https://github.com/knadh/koanf) for configuration, [`tint`](https://github.com/lmittmann/tint) for terminal logs, and [`golua`](https://github.com/iceisfun/golua) for Lua modules
- For development: [Task](https://taskfile.dev) and [golangci-lint](https://golangci-lint.run) (`brew install go-task golangci-lint`)

## Run it

From the repository root:

```sh
go run ./cmd/lukky
```

You will get a simple prompt. Try:

```text
> !ping
bot: pong
> !addalias discord Join us at https://example.com/community
bot: Saved !discord
> !discord
bot: Join us at https://example.com/community
> !aliases
bot: Aliases: !discord
> :reload aliases
aliases reloaded
> !discord
bot: Join us at https://example.com/community
> :quit
```

An alias name may contain any characters except whitespace, for example `!c++` or `!привет`. The text is any message up to 500 characters. `!delalias discord` removes an alias. Only moderators and the broadcaster can add or remove aliases; the console user counts as the broadcaster. `:modules` lists loaded modules, and `:reload <module>` reloads one. Control commands start with `:` and exist only in the console, not in chat.

The greeter module also posts a reminder every two minutes. Timers exist only in memory, so their schedule starts fresh each time the module loads.

## Lua modules

Write a module in Lua instead of Go: put a directory with an `init.lua` into `scripts/`. Lukky loads it at startup, and `:reload <module>` picks up edits without a restart.

```lua
---@type lukky.Module
return {
  name = "hello",
  start = function(host)
    host.command("!hello", function(msg)
      host.reply(msg, "Hello, " .. msg.user.display_name)
    end)
  end,
}
```

Lua modules have the same commands, events, timers, and storage as Go modules, and run sandboxed. See [Lua modules](docs/lua.md) and the example in `examples/lua/shoutout`.

## Overlays

Lukky serves overlay pages for OBS on `http://127.0.0.1:7878/`. A Lua module with an `overlay/` directory gets a page at `/overlays/<module>/`; add that URL as a Browser Source in OBS. The module sends live data with `host.overlay.push` (one-time events, such as alerts) and `host.overlay.set` (values a reloaded page receives again). Pages load `/lukky.js` and call `lukky.on("event", handler)`. Go modules use `host.Overlay()`.

## Configuration

Lukky reads its settings from three places in the working directory. Later ones win:

1. `lukky.toml`: settings without secrets, such as channels and the reminder interval. Copy `lukky.example.toml` to start.
2. `.env`: secrets. Copy `.env.example` and fill it in. Git-ignored.
3. Real environment variables.

Environment names map to settings by prefix. `TWITCH_CHANNELS` sets `twitch.channels`, and `LUKKY_GREETER_INTERVAL` sets `greeter.interval`. Logs are colored in a terminal and JSON otherwise. Set the level with `log.level` or `LUKKY_LOG_LEVEL`.

## Connect to Twitch

Put the credentials in `.env` and the channels in `lukky.toml` or `.env`.

| Variable | Meaning |
|---|---|
| `TWITCH_CLIENT_ID`, `TWITCH_CLIENT_SECRET` | Your Twitch application. Setting the client ID enables Twitch. |
| `TWITCH_USER_TOKEN`, `TWITCH_USER_REFRESH_TOKEN` | User tokens for the bot account, with `chat:read` and `chat:edit` scopes. |
| `TWITCH_CHANNELS` | Comma-separated channel logins to join, for example `luks3r`. Or `channels = ["luks3r"]` under `[twitch]` in `lukky.toml`. |

The bot's login, such as `lukserbot`, comes from the token itself. Lukky validates the token at startup and every hour, as Twitch requires. It refreshes the token before it expires, or when Twitch rejects it. Twitch may rotate the refresh token. Lukky therefore saves the current tokens to `.lukky/twitch-token.json`, with owner-only permissions, and prefers that file over `.env` on the next start. If the refresh token stops working, re-authorize the bot, update `.env`, and delete the state file.

Twitch users get the same commands as the console. Moderators and the broadcaster can manage aliases. If stdin closes, for example under a service manager, the bot keeps serving Twitch until it receives `SIGINT` or `SIGTERM`.

`.env` and `.lukky/` contain secrets and are git-ignored. Never commit them or paste their contents anywhere. If a token or the client secret leaks, revoke it in the Twitch developer console.

## How it is organized

```text
cmd/lukky/            Application wiring, configuration, and the local console
core/                 Module lifecycle, event bus, command router, scheduler
adapters/twitch/      Twitch chat over IRC and token management
modules/greeter/      Small static command and timer example
modules/aliases/      User-managed text aliases stored in host storage
lua/                  Lua module runtime; LuaLS type definitions in lua/types
overlay/              Overlay server for OBS browser sources
examples/lua/         Example Lua modules
storage/sqlite/       SQLite database for host storage
storage/storagetest/  Shared tests for storage backends
docs/                 Architecture, module guide, Lua plan, and design decisions
```

The central concept is a **module**. A module declares its name and registers the work it wants to perform. It does not need to manage individual subscription or timer lifetimes: the host tracks them under the module's scope.

A **command** is a way of handling chat text. An **event** is a notification from a producer, which might be a chat adapter, donation integration, or webhook receiver. They are deliberately separate concepts. A console adapter publishes `chat.message` events; a small ingress module forwards them to the command router.

The current router runs all matching handlers in registration order. The event bus likewise delivers matching subscriptions in registration order. Neither system uses an external broker or stores events.

## Build and test

```sh
task          # format, lint, test with the race detector, and scan for vulnerabilities
task run      # run the bot
task build    # build ./lukky
task --list   # all tasks
```

Without Task, the plain commands are `go test -race ./...`, `golangci-lint run ./...`, `go tool govulncheck ./...`, and `go build ./cmd/lukky`.

The tests cover command routing, event and command cleanup, failed module startup, cancellation of in-flight handlers, timer shutdown, and alias behavior across a module reload.

## Add a feature

Start with [Writing a module](docs/modules.md). A feature usually needs only a struct implementing `Name()` and `Start(*core.Host) error`. Use `host.Handle` to register commands, `core.Subscribe` to react to events, `host.Every` to schedule periodic work, and `host.Store` to keep data.

The aliases module demonstrates an intentional distinction: `!addalias` is a built-in command, but `!discord` is stored as data. Adding an alias doesn't create a new Go handler or change the router's registrations. That distinction lets each feature manage its own user-facing configuration without turning the application into a general-purpose scripting or workflow language.

## Current limits

Lukky currently has no HTTP host API. Module data, such as aliases, is stored in the SQLite database `.lukky/lukky.sqlite` and survives restarts. You can inspect it with `sqlite3`, but only while Lukky is stopped: Lukky holds an exclusive lock, so only one process can use the file at a time. Those are possible integrations, not hidden dependencies. Go modules are compiled into the application; `Load` and `Unload` control their runtime registrations, not Go dynamic linking. Lua modules load from disk at runtime.

The host expects module callbacks to cooperate with context cancellation. Unloading a module waits for its running work to finish; a callback that ignores cancellation can therefore block shutdown. Also, callbacks must not synchronously unload their own module.

See [Architecture](docs/architecture.md) for the boundaries and lifecycle, and [Design decisions](docs/decisions.md) for what we are deliberately not building yet.

## Project status

This is a starting point for a personal stream-automation project. Interfaces and package layout may change as real integrations expose missing requirements. The focus is a readable, testable core, not a framework with every extension point imagined in advance.
