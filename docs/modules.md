# Writing a module

A module is a small, compiled-in Go component. You don't need a plugin loader, dependency-injection framework, or reflection to add one.

## A minimal example

```go
package shout

import (
    "context"

    "github.com/luks3r/lukky/core"
)

type Module struct{}

func (Module) Name() string { return "shout" }

func (Module) Start(host *core.Host) error {
    return host.Handle(core.Prefix("!shout"),
        func(ctx context.Context, msg core.Message, match core.Match) error {
            return host.Reply(ctx, msg, "Hello, "+msg.User.DisplayName)
        })
}
```

Register it during application setup:

```go
if err := manager.Load(shout.Module{}); err != nil {
    return err
}
```

That is the entire module contract. `Stop(context.Context) error` is optional. You need it only if your module has additional resources to release after scoped registrations and work have stopped.

## APIs available to a module

- `host.Handle(matcher, handler)` registers a command.
- `core.Subscribe(host, topic, handler)` subscribes to a typed event topic.
- `host.Every(interval, job)` starts a periodic task.
- `host.After(delay, job)` runs a task once after a delay, unless the module unloads first.
- `host.Go(job)` starts background work tied to module cancellation.
- `host.Logger()` returns a `*slog.Logger` tagged with the module name.
- `host.Own(cleanup)` attaches an additional cleanup function to the module's scope.
- `host.Send(ctx, target, text)` sends a message to a channel through the configured chat output.
- `host.Reply(ctx, msg, text)` sends a message to the channel `msg` came from.
- `core.Emit(ctx, host, topic, payload)` publishes a typed in-process event.
- `host.Context()` returns the module's lifetime context.

There is no need to save unregister functions. Closing the module scope performs that cleanup for you.

## Writing a matcher

A matcher returns a `core.Match` and a boolean. A handler is called only when the boolean is true.

```go
matcher := func(msg core.Message) (core.Match, bool) {
    if msg.Text != "hello bot" {
        return core.Match{}, false
    }
    return core.Match{}, true
}
```

Built-in matchers are `core.Prefix`, `core.Exact`, and `core.Regex`. `` core.Regex(regexp.MustCompile(`^!roll (\d+)d(\d+)$`)) `` matches the trimmed text and puts capture groups in `match.Args`. Add `^` and `$` yourself to match the whole message.

Wrap a matcher with `core.RequireRole` to limit a command to trusted users. Senders below the role are ignored without a reply:

```go
host.Handle(core.RequireRole(core.RoleModerator, core.Prefix("!addalias")), handler)
```

Match functions currently cannot perform asynchronous database lookups or return errors. If a feature needs storage access, prefer matching the command's *shape* and doing the lookup in the handler. That is how the aliases module handles user-managed aliases.

For example, `!addalias discord Join us at https://example.com` updates the module's storage. Later `!discord` matches a single alias-shaped route, which looks up `discord` at execution time. No new router registration is necessary.

## Events

A topic names an event and fixes its payload type. The producer declares it once:

```go
type Donation struct {
    From    string
    Amount  int // in cents
    Message string
}

var DonationReceived = core.Topic[Donation]{Name: "donation.received"}
```

Consumers subscribe with a handler that receives the payload type directly:

```go
err := core.Subscribe(host, DonationReceived, func(ctx context.Context, d Donation) error {
    return announceDonation(ctx, d)
})
```

Producers emit through the host, or through the bus if they are adapters:

```go
err := core.Emit(ctx, host, DonationReceived, Donation{From: "ann", Amount: 500})
```

Keep payloads plain data: primitives, slices, maps, and structs of those. Avoid functions, channels, and pointers to shared state. Plain data stays safe to deliver to several handlers, and future script modules can convert it. Built-in topics include `core.ChatMessage`.

`host.On` and `host.Publish` remain as untyped, lower-level APIs. Prefer the typed ones.

`Donation` and `announceDonation` are intentionally feature-specific. The core does not have a donation model. An adapter or integration can publish this event when it receives and validates data from an external service.

## Schedule recurring work

```go
err := host.Every(5*time.Minute, func(ctx context.Context) {
    if err := refreshStats(ctx); err != nil {
        host.Logger().Error("refresh stats", "error", err)
    }
})
```

This scheduler does not return errors from jobs, retry failures, or persist timing. The callback owns its logging and retry policy. It must stop promptly when `ctx` is cancelled. A panicking run is logged with its stack trace; later runs continue.

Use `host.After(delay, job)` for a one-shot task. It is cancelled if the module unloads before the delay ends.

## Store data

`host.Store()` returns key-value storage private to the module. Keys are strings and values are bytes. Encode structured values yourself, for example as JSON.

```go
store := host.Store()
if err := store.Put(ctx, "discord", []byte("Join us")); err != nil {
    return err
}
text, err := store.Get(ctx, "discord") // core.ErrNotFound if missing
```

The namespace is the module name. Data survives unload and reload, and with the default database it also survives restarts. Renaming a module starts it with an empty namespace.

## Overlays

A module can have an overlay page, for example an OBS browser source. Mount its files once, then send data:

```go
//go:embed overlay
var overlayFiles embed.FS

func (m *Module) Start(host *core.Host) error {
    files, _ := fs.Sub(overlayFiles, "overlay")
    if err := host.Overlay().Mount(files); err != nil {
        return err
    }
    ...
}

host.Overlay().Push(ctx, "alert", Alert{User: "ann"}) // one-time event
host.Overlay().Set(ctx, "goal", Goal{Current: 3})    // stored; replayed to pages that connect later
```

The page is served at `http://127.0.0.1:7878/overlays/<module>/`. In the page, load `/lukky.js` and call `lukky.on("alert", data => ...)`. Without an overlay server, these calls return `core.ErrNoOverlays`.

## Require other modules

A module that depends on another one implements `core.Requirer`:

```go
func (Module) Requires() []string { return []string{"aliases"} }
```

`Load` fails with `core.ErrMissingRequirement` unless every required module is already loaded. `Unload` of a required module fails with `core.ErrRequiredByOthers` while its dependents are loaded. Load requirements first; the manager never reorders or loads modules for you.

Requiring a module only checks that it is present. To call into it, pass its Go interface through your constructor, as shown below, or communicate through events.

## State and dependencies

Pass dependencies through the module's constructor instead of reaching into a global service locator:

```go
type Module struct {
    repo Repository
}

func New(repo Repository) *Module {
    return &Module{repo: repo}
}
```

Use your own repository only when key-value storage does not fit, for example for queries across many records. Whether that state survives module unload depends on the repository's lifetime, not the scheduler or event bus.

## Before adding a core abstraction

Ask whether two features actually need the same contract. A chat alias and a donation record are not automatically the same kind of entity. Keep validation, permissions, external API data, and user configuration inside the owning module unless a cross-cutting use case is demonstrated.
