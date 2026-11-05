# AGENTS.md

Guidance for AI coding agents working on Lukky.

## Project

Lukky is a modular, event-driven runtime for stream automation, written in Go. It is a single process: the core supplies infrastructure, modules supply features, and adapters connect external platforms such as Twitch.

Read these before changing behavior:

- `docs/architecture.md`: building blocks and lifecycle
- `docs/decisions.md`: what is decided, deferred, and out of scope, plus the implementation order
- `docs/modules.md`: how to write a module
- `docs/lua.md`: Lua modules: usage, host API, constraints

## Layout

```text
cmd/lukky/            Wiring, configuration (koanf), local console
core/                 Manager, scopes, event bus, router, scheduler, storage interface
adapters/twitch/      Twitch chat over IRC and token management
modules/              Feature modules (greeter, aliases)
lua/                  Lua module runtime and LuaLS type definitions (lua/types)
overlay/              Overlay pages and live data for OBS browser sources
examples/lua/         Example Lua modules
storage/sqlite/       SQLite backend for host storage
storage/storagetest/  Contract tests every storage backend must pass
docs/                 Architecture, decisions, module guide
```

## Commands

```sh
task          # format, lint, test with -race, govulncheck
task run      # run the bot
task test     # go test -race ./...
task lint     # golangci-lint run ./...
task fmt      # gofumpt + goimports via golangci-lint
```

Run `task` before you finish. It must report 0 lint issues and passing tests.

## Rules

**Architecture**

- `core` stays platform-neutral. Twitch, OBS, or donation code belongs in adapters or modules.
- Modules use only `*core.Host`. Registrations go through the host, so unload cleans them up.
- Commands and events are separate. Matchers do no I/O; lookups belong in handlers.
- Prefer typed topics: `core.Topic[T]`, `core.Subscribe`, `core.Emit`. Payloads are plain data.
- Handlers must return quickly. Move slow work to `host.Go`.
- Module data goes through `host.Store()`. The namespace is the module name.
- Do not add speculative abstractions, registries, or configuration for fixed values.

**Go style**

- Go 1.27. Use the modern standard library: `slices`, `maps`, `errors.AsType`, `sync.WaitGroup.Go`, `strings.SplitSeq`.
- Run `go fix ./...` after larger edits.
- Write timer and scheduling tests with `testing/synctest`, not real sleeps.
- New storage backends must pass `storagetest.Run`.
- Keep `lua/types/lukky.lua` in sync with the host table in `lua/host.go`.
- Add a `//nolint` only with the linter name and a reason.
- Keep comments short and about why, not what.

**Secrets**

- Never read, print, or commit `.env`, `lukky.toml`, or anything in `.lukky/`. They hold credentials and local state.
- Never log tokens or the client secret.
- `.env.example` and `lukky.example.toml` are the committed templates. Keep them in sync with `cmd/lukky/config.go`.

**Docs**

- When behavior changes, update the matching doc in the same change: README, `docs/architecture.md`, `docs/modules.md`, or `docs/decisions.md`.
- Mark agreed but unbuilt items as **(planned)** in `docs/architecture.md`.
- Docs, comments, and commit messages are plain, concise English.

**Commits**

- Use Conventional Commits: `feat:`, `fix:`, `refactor:`, `docs:`, `test:`, `chore:`.
- Never commit secrets or build output.

## Status

All nine steps of the implementation order in `docs/decisions.md` are done, including Lua modules. Candidates next: migrating Twitch chat to EventSub plus Helix, and CI. Twitch recommends EventSub plus Helix over IRC for chatbots; a migration is a candidate step.

## Response style

Reply tersely: answer first, then the reason, then the next step. No greetings, filler, or recaps. Keep code, commands, paths, and error messages exact. Write plain prose for security warnings, irreversible actions, and questions to the user.
