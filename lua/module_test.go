package lua

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/luks3r/lukky/core"
)

var (
	channel   = core.Target{Platform: "test", Channel: "main"}
	moderator = core.User{Name: "mod", DisplayName: "Mod", Roles: core.RoleModerator}
	viewer    = core.User{Name: "viewer", DisplayName: "Viewer"}
)

type outbox struct {
	mu   sync.Mutex
	sent []string
}

func (o *outbox) Send(_ context.Context, to core.Target, text string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.sent = append(o.sent, to.Platform+"/"+to.Channel+": "+text)
	return nil
}

func (o *outbox) all() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return slices.Clone(o.sent)
}

type fixture struct {
	t       *testing.T
	manager *core.Manager
	bus     *core.Bus
	router  *core.Router
	out     *outbox
	storage *core.MemoryStorage
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{t: t, bus: core.NewBus(), router: core.NewRouter(), out: &outbox{}, storage: core.NewMemoryStorage()}
	m, err := core.NewManager(context.Background(), core.Services{
		Events: f.bus, Commands: f.router, Chat: f.out, Storage: f.storage,
		Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	f.manager = m
	return f
}

// write creates a module directory with init.lua and returns its path.
func write(t *testing.T, src string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "init.lua"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func (f *fixture) load(src string) *Module {
	f.t.Helper()
	m, err := Load(write(f.t, src), Options{})
	if err != nil {
		f.t.Fatal(err)
	}
	if err := f.manager.Load(m); err != nil {
		f.t.Fatal(err)
	}
	return m
}

func (f *fixture) say(user core.User, text string) error {
	return f.router.Dispatch(context.Background(), core.Message{Source: channel, User: user, Text: text})
}

func TestCommandsAndReplies(t *testing.T) {
	f := newFixture(t)
	f.load(`
return {
  name = "greet",
  start = function(host)
    host.command("!hi", function(msg, args)
      host.reply(msg, "hi " .. msg.user.display_name .. " " .. (args[1] or "") .. " #roles=" .. #msg.user.roles)
    end)
    host.exact("!exact", function(msg) host.reply(msg, "exact") end)
    host.regex("^!roll (\\d+)$", function(msg, args) host.reply(msg, "rolled " .. args[1]) end)
    host.command("!mod", function(msg) host.reply(msg, "mod only") end, { role = "moderator" })
  end,
}`)
	for _, step := range []struct {
		user core.User
		text string
	}{{viewer, "!hi there"}, {viewer, "!exact"}, {viewer, "!roll 6"}, {viewer, "!mod"}, {moderator, "!mod"}} {
		if err := f.say(step.user, step.text); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"test/main: hi Viewer there #roles=0", "test/main: exact", "test/main: rolled 6", "test/main: mod only"}
	if got := f.out.all(); !slices.Equal(got, want) {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

type donation struct {
	From   string `json:"from"`
	Amount int    `json:"amount"`
}

var donationReceived = core.Topic[donation]{Name: "donation.received"}

func TestEventsCrossLanguages(t *testing.T) {
	f := newFixture(t)
	var got []donation
	goModule := &goModule{name: "go-listener", start: func(h *core.Host) error {
		return core.Subscribe(h, donationReceived, func(_ context.Context, d donation) error {
			got = append(got, d)
			return nil
		})
	}}
	if err := f.manager.Load(goModule); err != nil {
		t.Fatal(err)
	}
	f.load(`
return {
  name = "relay",
  start = function(host)
    host.on("donation.received", function(d)
      if d.from ~= "lua" then
        host.emit("donation.received", { from = "lua", amount = d.amount * 2 })
      end
    end)
  end,
}`)
	if err := core.Emit(context.Background(), f.bus, donationReceived, donation{From: "go", Amount: 5}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []donation{{"go", 5}, {"lua", 10}}) {
		t.Fatalf("got %+v", got)
	}
}

func TestReentrantEventDoesNotDeadlock(t *testing.T) {
	f := newFixture(t)
	f.load(`
return {
  name = "loop",
  start = function(host)
    host.on("ping", function(n) if n < 3 then host.emit("ping", n + 1) end end)
    host.on("ping", function(n) host.send({ platform = "test", channel = "main" }, "ping " .. n) end)
  end,
}`)
	done := make(chan error, 1)
	go func() { done <- f.bus.Publish(context.Background(), core.Event{Topic: "ping", Payload: 1.0}) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("deadlock on re-entrant event")
	}
	if got := f.out.all(); len(got) != 3 {
		t.Fatalf("got %q", got)
	}
}

func TestStoreSurvivesReload(t *testing.T) {
	f := newFixture(t)
	src := `
return {
  name = "counter",
  start = function(host)
    host.command("!count", function(msg)
      local n = tonumber(host.store.get("n") or "0") + 1
      host.store.put("n", tostring(n))
      host.reply(msg, "count " .. n .. " keys=" .. host.json.encode(host.store.list()))
    end)
  end,
}`
	dir := write(t, src)
	for range 2 {
		m, err := Load(dir, Options{})
		if err != nil {
			t.Fatal(err)
		}
		if err := f.manager.Load(m); err != nil {
			t.Fatal(err)
		}
		if err := f.say(viewer, "!count"); err != nil {
			t.Fatal(err)
		}
		if err := f.manager.Unload("counter"); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{`test/main: count 1 keys={"n":"1"}`, `test/main: count 2 keys={"n":"2"}`}
	if got := f.out.all(); !slices.Equal(got, want) {
		t.Fatalf("got %q", got)
	}
}

func TestTimers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		f.load(`
return {
  name = "timers",
  start = function(host)
    local target = { platform = "test", channel = "main" }
    host.every(1, function() host.send(target, "tick") end)
    host.after(1.5, function() host.send(target, "once") end)
  end,
}`)
		time.Sleep(2500 * time.Millisecond)
		synctest.Wait()
		if got := f.out.all(); !slices.Equal(got, []string{"test/main: tick", "test/main: once", "test/main: tick"}) {
			t.Fatalf("got %q", got)
		}
	})
}

func TestRequiresAndStop(t *testing.T) {
	f := newFixture(t)
	m, err := Load(write(t, `return { name = "child", requires = { "parent" }, start = function() end }`), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.manager.Load(m); !errors.Is(err, core.ErrMissingRequirement) {
		t.Fatalf("got %v", err)
	}
	f.load(`
return {
  name = "parent",
  start = function(host) host.own(function() host.log.info("cleanup") end) end,
  stop = function() error("stop failed on purpose") end,
}`)
	if err := f.manager.Unload("parent"); err == nil || !strings.Contains(err.Error(), "stop failed on purpose") {
		t.Fatalf("stop error not reported: %v", err)
	}
}

func TestStartErrorRollsBack(t *testing.T) {
	f := newFixture(t)
	m, err := Load(write(t, `
return {
  name = "broken",
  start = function(host)
    host.command("!broken", function(msg) host.reply(msg, "should not exist") end)
    error("bad config")
  end,
}`), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.manager.Load(m); err == nil || !strings.Contains(err.Error(), "bad config") {
		t.Fatalf("got %v", err)
	}
	if err := f.say(viewer, "!broken"); err != nil || len(f.out.all()) != 0 {
		t.Fatalf("command leaked: %v %q", err, f.out.all())
	}
}

func TestInvalidModules(t *testing.T) {
	for name, src := range map[string]string{
		"syntax":      `return {`,
		"not a table": `return 42`,
		"no name":     `return { start = function() end }`,
		"no start":    `return { name = "x" }`,
		"bad require": `return { name = "x", start = function() end, requires = "y" }`,
		"top loop":    `while true do end`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(write(t, src), Options{MaxInstructions: 100_000}); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestSandbox(t *testing.T) {
	f := newFixture(t)
	f.load(`
return {
  name = "probe",
  start = function(host)
    host.command("!probe", function(msg)
      local found = {}
      for _, name in ipairs({ "os", "io", "debug", "load", "loadfile", "dofile", "require", "package" }) do
        if _G[name] ~= nil then table.insert(found, name) end
      end
      host.reply(msg, "found: " .. table.concat(found, ","))
    end)
  end,
}`)
	f.load(`
return {
  name = "badtarget",
  start = function(host)
    host.command("!bad", function() host.send({ platform = "test" }, "x") end)
  end,
}`)
	if err := f.say(viewer, "!bad"); err == nil || !strings.Contains(err.Error(), "platform and channel") {
		t.Fatalf("incomplete target accepted: %v", err)
	}
	if err := f.say(viewer, "!probe"); err != nil {
		t.Fatal(err)
	}
	if got := f.out.all(); !slices.Equal(got, []string{"test/main: found: "}) {
		t.Fatalf("unsafe globals visible: %q", got)
	}
}

// Prototype criterion 1: unloading a module stuck in a loop returns within 100 ms.
func TestUnloadInterruptsBusyHandler(t *testing.T) {
	f := newFixture(t)
	f.load(`
return {
  name = "busy",
  start = function(host) host.command("!spin", function() while true do end end) end,
}`)
	go func() { _ = f.say(viewer, "!spin") }()
	time.Sleep(20 * time.Millisecond)
	start := time.Now()
	if err := f.manager.Unload("busy"); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("unload took %v", elapsed)
	}
}

func TestInstructionLimitStopsRunaway(t *testing.T) {
	f := newFixture(t)
	m, err := Load(write(t, `
return {
  name = "runaway",
  start = function(host)
    host.command("!spin", function() while true do end end)
    host.command("!ok", function(msg) host.reply(msg, "still alive") end)
  end,
}`), Options{MaxInstructions: 1_000_000})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.manager.Load(m); err != nil {
		t.Fatal(err)
	}
	if err := f.say(viewer, "!spin"); err == nil || !strings.Contains(err.Error(), "instruction limit") {
		t.Fatalf("got %v", err)
	}
	if err := f.say(viewer, "!ok"); err != nil || !slices.Equal(f.out.all(), []string{"test/main: still alive"}) {
		t.Fatalf("module unusable after limit: %v %q", err, f.out.all())
	}
}

// Prototype criterion 2: goroutines return to baseline after load and unload cycles.
func TestNoGoroutineLeak(t *testing.T) {
	dir := write(t, `
return {
  name = "co",
  start = function(host)
    local co = coroutine.create(function() coroutine.yield(1) end)
    coroutine.resume(co)
    host.every(60, function() end)
  end,
}`)
	runtime.GC()
	base := runtime.NumGoroutine()
	f := newFixture(t)
	for range 30 {
		m, err := Load(dir, Options{})
		if err != nil {
			t.Fatal(err)
		}
		if err := f.manager.Load(m); err != nil {
			t.Fatal(err)
		}
		if err := f.manager.Unload("co"); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > base && time.Now().Before(deadline) {
		runtime.GC()
		time.Sleep(10 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > base {
		t.Fatalf("goroutines %d, baseline %d", n, base)
	}
}

func TestConversionRoundTrip(t *testing.T) {
	in := map[string]any{"s": "x", "n": int64(2), "f": 1.5, "b": true, "list": []any{int64(1), "two"}, "empty": map[string]any{}}
	out, err := fromLua(toLua(in))
	if err != nil {
		t.Fatal(err)
	}
	got, _ := plain(out)
	want, _ := plain(in)
	if a, b := mustJSON(t, got), mustJSON(t, want); a != b {
		t.Fatalf("got %s, want %s", a, b)
	}
}

type goModule struct {
	name  string
	start func(*core.Host) error
}

func (m *goModule) Name() string             { return m.name }
func (m *goModule) Start(h *core.Host) error { return m.start(h) }

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
