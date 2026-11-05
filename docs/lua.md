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

## Runtime prototype (step 9)

Compare `iceisfun/golua` and `arnodel/golua` with one test module that registers a command, subscribes to `chat.message`, starts a timer, and stores a value. Then measure:

1. Unloading a module that runs `while true do end` returns within 100 ms.
2. `runtime.NumGoroutine` returns to its baseline after unload.
3. The amount of glue code for payload conversion and the host table.
4. LuaLS support for the language version, set through `runtime.version`.

Choose the runtime that passes 1 and 2 with the least glue code.
