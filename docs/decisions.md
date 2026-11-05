# Design decisions

Each choice lists its reason. The list separates what we rule out from what we simply have not built yet.

## Principles

- Modular monolith, not distributed services.
- The core provides infrastructure. Modules provide features. Adapters connect external systems. Scopes define ownership and lifetime.
- Composition over abstraction. Add an interface when a second implementation or a test boundary needs one.
- No mandatory infrastructure. Lukky runs without brokers, databases, or external services.
- Real adapters and features drive the next steps, not speculative abstractions.

## Decided

**Commands are not events.** Events report that something happened. Commands parse chat text and act on it. A chat adapter publishes message events, and an ingress module forwards them to the command router.

**Features are modules.** A module's registrations and background work belong to its scope. Its data may outlive it.

**Static logic, configurable data.** User-managed settings, such as text aliases like `!discord`, are data that an existing module reads. They never become new executable code. One matcher can resolve every alias through a lookup.

**In-process and synchronous delivery.** The event bus and the router call handlers in order, on the caller's goroutine. Adapters buffer bursty input in their own bounded queues. Slow handlers move their work to background tasks. *Why:* ordering stays predictable, unload stays simple, and the core needs no drop policy.

**Ephemeral timers.** Missed runs, remaining time, and history are not stored. Each load creates fresh timers.

**All matching commands run.** No priorities and no exclusive routing. Cooldowns and similar rules can come later as matcher or handler wrappers.

**Matchers do no I/O.** Matchers may read in-memory state only. *Why:* a blocking matcher would stall dispatch for every route. Repository lookups belong in handlers.

**Reload is unload followed by load.** The short gap with no handler is acceptable for chat. A staged swap would briefly run two instances, which means duplicate timers and name clashes.

**Dependencies are declared and checked, never resolved.** A module lists the names it requires. Load and unload refuse to break those requirements. *Why:* a full dependency graph turns into a universal registry, and simple checks also work for script manifests.

**Host services form the shared surface.** Every host capability will be available to every module runtime. Direct typed interfaces between compiled modules stay compiled-only. Script modules use events to reach other modules. *Why:* a binding for every service of every module does not scale.

**Plain-data event payloads with typed topics.** Payloads use only data that every runtime can convert. Each topic declares its payload type. Mistakes then show at compile time instead of at runtime.

**Platform-neutral messages with normalized roles.** Modules check roles without knowing the platform. A matcher wrapper can require a minimum role. Out of scope: a plugin permission system.

**Namespaced key-value storage as the default.** Each module gets its own namespace in host storage, and the namespace survives reloads. A compiled module with complex needs may bring its own repository instead. The default backend is one SQLite file with a single key-value table. *Why:* a standard format that common tools can open, no server, and room for module-owned tables later. The pure-Go driver needs no C toolchain. An exclusive lock keeps it to one Lukky process.

**Crashes in module code are contained.** The scope catches crashes, logs them with a stack trace, and turns them into errors.

**Layered configuration.** Settings come from `lukky.toml`, then `.env`, then the environment, with later sources winning. Secrets live only in `.env` or the environment. *Why:* one mechanism serves application settings, module settings, and secrets. Script manifests will be written in the script language itself.

**Lua modules on `iceisfun/golua` v2.** *Why:* it is the only candidate that stops a running script on context cancellation and frees the goroutines of abandoned coroutines. A Lua module gets one state behind a per-module lock and runs sandboxed with an instruction limit per callback. See `lua.md`.

**Go for the core.** Go fits "simple and understandable" and fast iteration while the API is still changing. A pure-Go Lua runtime covers the extension goals.

## Deferred

- **HTTP and WebSocket services.** Added when an integration needs them. An outbound HTTP client is a service, not an event transport.
- **Stronger lifecycle.** Atomic replacement, stop timeouts, and cascading unload are deferred until a real need appears.

## Out of scope

- A custom DSL, AST evaluator, workflow engine, or arbitrary user pipelines.
- Dynamic compilation of native modules.
- Mandatory persistence for events or timers.
- Distributed processing or external brokers as core requirements.
- A universal registry for application components.
- A complex plugin permission system.

## Known trade-offs

- Synchronous delivery: one slow handler delays the handlers after it.
- Cooperative cancellation: code that ignores cancellation can block unload indefinitely.
- Serialized lifecycle: a module that calls manager operations from its own hooks deadlocks.
- Reload gap: during a reload, commands for that module go unanswered.
- Role trust: role checks are only as reliable as the adapter that fills in roles. The demo console treats its user as the broadcaster.

## Implementation order

1. Rename Streamkit to Lukky (done).
2. Crash containment, delayed tasks, regex matcher, module logger (done).
3. Platform-neutral message, reply target, and roles (done).
4. Twitch adapter with its own input queue (done).
5. Namespaced storage service (done).
6. Typed topics (done).
7. Declared module dependencies (done).
8. Review the host API against Lua constraints (done, see `lua.md`).
9. Lua runtime prototype and implementation (done: `iceisfun/golua` v2, see `lua.md`).
