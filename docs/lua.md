# Lua extensions: host API review

This review checks each part of the host API against what a Lua runtime can support. The goal is that a Lua module can do what a Go module does, without the core knowing which language a module uses. Nothing here is implemented yet, apart from the changes listed under "Changes made now".

## Shape of a Lua module

A Lua module is a directory with an `init.lua` that returns a table:

```lua
---@type lukky.Module
return {
  name = "shoutout",
  requires = { "aliases" },

  ---@param host lukky.Host
  start = function(host)
    host.command("!so", function(msg, args)
      host.reply(msg, "Check out " .. (args[1] or "everyone") .. "!")
    end, { role = "moderator" })
  end,

  stop = function() end, -- optional
}
```

A Go adapter type wraps the table and implements `core.Module`, `core.Stopper`, and `core.Requirer`. The manager, scopes, and cleanup rules stay the same for both languages.

## API mapping

| Go host API | Lua | Notes |
|---|---|---|
| `Name`, `Requires`, `Start`, `Stop` | table fields | Read once at load. |
| `host.Handle(Prefix(x), h)` | `host.command(x, fn, opts)` | `opts.role` wraps the matcher with `RequireRole`. |
| `Exact`, `Regex` | `host.exact`, `host.regex` | Regex uses Go RE2 syntax, not Lua patterns. |
| custom `Matcher` | `host.match(fn, handler)` | Allowed, but runs on every message: keep it cheap. |
| `core.Subscribe` / `core.Emit` | `host.on(topic, fn)` / `host.emit(topic, table)` | Untyped at the boundary; see "Events". |
| `host.Every`, `host.After` | same names, seconds as numbers | |
| `host.Go` | not offered at first | See "Concurrency". |
| `host.Own` | `host.own(fn)` | |
| `host.Store()` | `host.store.get/put/delete/list` | Values are strings. A `json` helper converts tables. |
| `host.Send`, `host.Reply` | same names | |
| `host.Logger()` | `host.log.info(msg, fields)` and so on | |
| `host.Context()` | none | Cancellation is handled by the runtime; see below. |

## Constraints and decisions

**Concurrency.** A Lua state is not safe for concurrent use. Each Lua module gets one state, and every callback into it runs under a per-module lock. Handlers, timers, and event callbacks of one Lua module therefore never run in parallel. This is acceptable at chat volume.

**Re-entrancy.** The bus and router are synchronous. A Lua handler that emits an event can reach another callback of the same module on the same call stack, and a plain lock would deadlock there. The bridge marks the context while it holds the module lock, and nested calls that carry the mark skip locking. No goroutine identity tricks are needed: the context already flows through `Publish` and `Dispatch`.

**Cancellation.** Lua code cannot watch a Go context. The runtime must interrupt a running script when the module unloads, and it must enforce an instruction limit per callback, so that `while true do end` cannot block unload or the dispatcher. This is a hard requirement for choosing the runtime.

**Background tasks.** A long-running Lua loop would hold the module lock and block that module's other callbacks. Lua modules therefore get timers and handlers but no `host.go` at first. If a real need appears, a coroutine-based API can be added later.

**Events and payloads.** Go generics do not cross into Lua, so Lua sees topics by name. Payloads are converted with these rules:

- exported struct fields become table keys, named by their `json` tag
- fields tagged `json:"-"` are skipped (for example `Message.Raw`)
- `core.Role` converts to a list of role names
- slices become arrays and maps become tables

The other direction, a Lua module emitting on a topic that a Go module consumes with `core.Subscribe`, needs decoding into the Go type. Plan: `Subscribe[T]` decodes a table-shaped payload into `T` through its JSON form. This lands with the runtime, because nothing produces such payloads before then.

**Errors.** Go errors returned to Lua become Lua errors. Lua errors and panics become Go errors with the Lua traceback, and go through the same panic containment as Go modules.

**Sandboxing.** Lua modules get no `os`, `io`, `debug`, or module loading from arbitrary paths. The host table is their only capability. This also makes the instruction limit meaningful.

**Equivalence limits.** Two gaps remain by design: Lua modules cannot call Go interfaces of other modules directly (they use events), and they have no background tasks at first. Everything else in the host API is available.

## Changes made now

To fix the payload contract before any Lua code exists:

- `Message`, `User`, and `Target` have snake_case `json` tags, and `Message.Raw` is excluded.
- `core.Role` marshals to and from a list of names, such as `["subscriber","moderator"]`.

## Runtime choice: iceisfun/golua v2

A prototype (November 2026) ran the same test module on `iceisfun/golua` v2.0.5 and `arnodel/golua` (lua5.5 branch, 2026-11-03). Both had a host table with `reply`, `put`, and `get`, received a `core.Message` converted through its JSON form, and ran each call under a per-module lock.

| Check | iceisfun/golua v2 | arnodel/golua |
|---|---|---|
| Payload conversion and host calls | pass | pass, after declaring every host function time-safe |
| Lua error becomes a Go error; state stays usable | pass | pass |
| Busy loop stops on context cancel | pass: returns at once | no: no `context.Context` support; a running call cannot be stopped from another goroutine |
| Runaway stopped by a limit | pass: instruction limit, 8 ms | pass: CPU limit, 6 ms |
| No goroutine leak (50 cycles) | pass, also with suspended coroutines | leaks one goroutine per abandoned coroutine; the runtime has no close |
| Call cost (`on_message`) | 7.6 µs, 87 allocations | 10.2 µs, 107 allocations |
| Glue code for the prototype | 106 lines | 108 lines |
| Binary size added (stdlib) | 3.1 MB | 8.3 MB |
| Language version | Lua 5.5 | Lua 5.5 |

arnodel can only bound a call with a wall-clock limit, so unload would wait up to that limit, and the limit would also cap every legitimate callback. Its leaking coroutines would need extra tracking in the bridge.

**Decision:** use `iceisfun/golua/v2`. It meets both hard requirements directly: `SetContext` per call stops a script on unload, and `VM.Close` releases coroutine goroutines. `MaxInstructions` with `ResetInstructionCount` gives a per-callback budget. A bare `vm.New()` has no file, OS, or process access.

**Risk:** the project has a single maintainer and few users. Mitigations:

- pin the version in `go.mod` and review upgrades
- keep all runtime-specific code in one package, so a swap stays local
- port the prototype checks into that package's tests, so a replacement must pass the same criteria

LuaLS lists Lua 5.5 as supported ([marketplace](https://marketplace.visualstudio.com/items?itemName=sumneko.lua), [PR #3218](https://github.com/LuaLS/lua-language-server/pull/3218)); set `runtime.version` to `"Lua 5.5"`, or `"Lua 5.4"` if an editor's LuaLS build predates it.
