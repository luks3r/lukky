# Architecture

Lukky is a modular, event-driven runtime for stream automation. It runs as a single process. The core supplies infrastructure, modules supply features, and adapters connect external platforms.

Items marked **(planned)** are agreed design that is not implemented yet. See [Design decisions](decisions.md) for the reasoning behind them.

## Overview

```text
Twitch / donations / webhooks / console
                 |
              adapters            translate external input into events
                 |
           event dispatcher       in-process publish/subscribe
            /           \
   chat ingress       event handlers (modules)
         |                  |
   command router           |
         |                  |
   command handlers         |
          \                /
           host services          chat, logging, storage, HTTP, ...
                 |
      Twitch / OBS / HTTP / ...
```

The scheduler runs module tasks on its own, outside this flow. Modules may also call host services directly. Not every operation has to go through an event.

## Building blocks

**Core.** The runtime and its shared services. It knows nothing about Twitch, OBS, donations, or any other integration.

**Module.** An independent unit of functionality with a name. When it starts, it registers commands, event subscriptions, and timers. It owns its business logic, its configuration, and its data. Modules are compiled in today. Script-based extensions are planned for later, and they will follow the same contract.

**Module manager.** Loads, unloads, and reloads modules. Lifecycle changes happen one at a time. A reload is an unload followed by a load. In between there is a short gap when the module handles nothing. **(planned)** A module may declare the names of modules it requires. Loading fails if a required module is missing. Unloading fails while dependent modules are still loaded. There is no automatic ordering and no dependency graph.

**Scope.** Every loaded module gets its own scope. Each command, subscription, timer, and background task belongs to the scope that created it. On unload, the scope:

1. cancels its context,
2. removes registrations in reverse order,
3. waits for running work to finish,
4. calls the module's optional stop hook.

If a module fails or crashes during startup, everything it registered is rolled back. A crash (panic) anywhere in module code is caught at the scope boundary and logged with its stack trace. For commands and event handlers, it is also returned as an error. A crashing timer run does not stop later runs. The process keeps running.

**Event dispatcher.** An in-process bus. An event has a topic and a payload, and each topic can have many subscribers. Delivery is synchronous, in subscription order. A failing subscriber does not stop later subscribers. The bus has no queue, persistence, retry, or replay, and it uses no external broker.

**Command router.** Turns chat text into actions, separately from events. Each route pairs a matcher with a handler. Every matching route runs, in registration order. There is no priority and no exclusive handling.

**Matcher.** Decides whether a message matches. It may return parsed arguments. A matcher is fast and side-effect free. It may read in-memory state, but it does no I/O. Built-in matchers are *prefix*, *exact*, and *regex* (capture groups become arguments). Modules can write custom matchers. Wrapper matchers add conditions to another matcher. The built-in *require role* wrapper ignores senders below a minimum role.

**Handler.** Performs an operation. It receives a context and its input, and it does not know what triggered it. Handlers must return quickly. Slow work belongs in a background task owned by the module.

**Scheduler.** Runs periodic tasks and one-shot delayed tasks, entirely in memory. Timers belong to module scopes and stop on unload. Missed runs are not replayed. Remaining time and execution history are not stored. A module recreates its schedule each time it starts.

**Host services.** The capabilities a module receives from the host:

| Service | Status |
|---|---|
| Event subscribe / publish | Implemented |
| Command registration | Implemented |
| Scheduling (periodic and delayed) | Implemented |
| Background tasks | Implemented |
| Chat output, to a target or as a reply | Implemented |
| Logging, tagged with the module name | Implemented |
| Key-value storage, namespaced per module | Implemented |
| HTTP / WebSocket | Added when a real integration needs it |

Events report that something happened. Services perform operations or return information.

**Adapters.** Connect external platforms. An adapter translates platform input into internal events and implements services such as chat output. Platform protocols stay in the adapter. An adapter that receives bursty input buffers it in its own bounded queue, so the dispatcher can stay synchronous. When the queue is full, new input is dropped and logged. The Twitch adapter follows this pattern: it reconnects with backoff, maps badges to normalized roles, and replaces line breaks in outgoing text. It also owns its credentials: it validates the token hourly, refreshes it when needed, and saves rotated tokens to a local state file. When several platforms are active, chat output is routed by the target's platform.

## Messages

A chat message is platform-neutral. It carries:

- message ID,
- source (platform and channel),
- user (ID, name, display name, normalized roles),
- text,
- opaque raw data from the adapter.

Roles are normalized as subscriber, VIP, moderator, and broadcaster. A user may hold several; no role means a plain viewer. Roles are ranked in that order, so a check for "at least moderator" also accepts the broadcaster. Modules should not depend on the raw data. Replies go to the source of the message. Messages that have no source message, such as scheduled ones, need an explicit target.

## Communication between modules

- **Events** carry occurrences between modules. They work for every module, whatever its implementation language.
- **Direct typed interfaces** between compiled-in modules are passed in when the application is wired. No global service registry exists.
- **Event payloads** are plain data: primitives, lists, maps, and nested records, with no functions or shared mutable state. Every module runtime can then read and convert them. Topics carry their payload type, so producers and consumers agree on the payload at compile time. A payload of the wrong type, published through the untyped API, is reported as an error and never reaches the handler.

## State and lifetime

The host owns registrations and executions. A feature owns the meaning and storage of its own data.

Module lifetime and data lifetime are separate. Unloading a module removes its handlers, but its data can stay. Host storage is namespaced by module name, so a reloaded module finds its data again. The application stores data in a local database file, so it also survives restarts. Tests and embedders can use in-memory storage instead.

## Concurrency and shutdown

The dispatcher and router take snapshots of their registrations before calling handlers. Handlers therefore never run under internal locks. A scope tracks its active handlers and background work, and waits for them during shutdown.

Cancellation is cooperative. Code that blocks must watch its context, or unloading waits forever. A module must not call manager operations from its start or stop hook. Its callbacks must not unload their own module, because the operation would wait on itself.

## Extensions (future)

Script-based extensions will load from the filesystem. They will use ordinary Lua with LuaLS annotations and StyLua formatting. An extension is a complete module: it has commands, event handlers, timers, and storage. It follows the same lifecycle and scope rules as a compiled module, and it reaches the host only through the documented host services. Interfaces passed directly between compiled modules are not available to extensions. Extensions communicate with other modules through events.

The Lua runtime is not chosen yet. Candidates will be compared with a small prototype. The prototype must show that unloading interrupts a busy script, that no goroutines leak, and that payload conversion stays simple.
