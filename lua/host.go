package lua

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/iceisfun/golua/v2/vm"

	"github.com/luks3r/lukky/core"
)

// newHostTable builds the `host` table passed to start. Every function raises
// a Lua error when the underlying host call fails.
func newHostTable(m *Module) vm.Value {
	h := m.host
	t := vm.NewEmptyTable()
	set := func(name string, f vm.NativeFunc) { t.SetString(name, vm.NewNativeFunc(f)) }

	set("command", commandFunc(m, "command", core.Prefix))
	set("exact", commandFunc(m, "exact", core.Exact))
	set("regex", commandFunc(m, "regex", func(pattern string) core.Matcher {
		re, err := regexp.Compile(pattern)
		check(err)
		return core.Regex(re)
	}))

	set("on", func(v *vm.VM) int {
		topic, fn := argString(v, 1, "on"), argFunc(v, 2, "on")
		check(h.On(topic, func(ctx context.Context, event core.Event) error {
			payload, err := plain(event.Payload)
			if err != nil {
				return fmt.Errorf("event %q: %w", topic, err)
			}
			return m.call(ctx, fn, toLua(payload))
		}))
		return 0
	})
	set("emit", func(v *vm.VM) int {
		topic := argString(v, 1, "emit")
		payload, err := fromLua(v.Get(2))
		check(err)
		check(h.Publish(m.ctx, core.Event{Topic: topic, Payload: payload}))
		return 0
	})

	set("every", timerFunc(m, "every", h.Every))
	set("after", timerFunc(m, "after", h.After))
	set("own", func(v *vm.VM) int {
		fn := argFunc(v, 1, "own")
		check(h.Own(func() {
			if err := m.call(context.Background(), fn); err != nil {
				h.Logger().Error("cleanup failed", "error", err)
			}
		}))
		return 0
	})

	set("send", func(v *vm.VM) int {
		to := targetArg(v, 1, "send")
		check(h.Send(m.ctx, to, argString(v, 2, "send")))
		return 0
	})
	set("reply", func(v *vm.VM) int {
		msg := v.Get(1)
		if !msg.IsTable() {
			raise("reply: argument 1 must be a message")
		}
		to := targetFrom(msg.AsTable().Get(vm.NewString("source")), "reply")
		check(h.Send(m.ctx, to, argString(v, 2, "reply")))
		return 0
	})

	t.SetString("overlay", vm.NewTable(overlayTable(m)))
	t.SetString("store", vm.NewTable(storeTable(m)))
	t.SetString("log", vm.NewTable(logTable(h.Logger())))
	t.SetString("json", vm.NewTable(jsonTable()))
	m.vm.SetGlobal("print", vm.NewNativeFunc(func(v *vm.VM) int {
		parts := make([]string, v.ArgCount())
		for i := range parts {
			parts[i] = vm.ValueToString(v.Get(i + 1))
		}
		h.Logger().Info(strings.Join(parts, "\t"))
		return 0
	}))
	return vm.NewTable(t)
}

// commandFunc implements host.command, host.exact, and host.regex:
// fn(pattern, handler [, {role = "moderator"}]). The handler receives the
// message and the parsed arguments.
func commandFunc(m *Module, name string, matcherFor func(string) core.Matcher) vm.NativeFunc {
	return func(v *vm.VM) int {
		matcher := matcherFor(argString(v, 1, name))
		handler := argFunc(v, 2, name)
		if opts := v.Get(3); opts.IsTable() {
			if role := opts.AsTable().Get(vm.NewString("role")); !role.IsNil() {
				r, err := core.ParseRole(vm.ValueToString(role))
				check(err)
				matcher = core.RequireRole(r, matcher)
			}
		}
		check(m.host.Handle(matcher, func(ctx context.Context, msg core.Message, match core.Match) error {
			payload, err := plain(msg)
			if err != nil {
				return err
			}
			return m.call(ctx, handler, toLua(payload), toLua(match.Args))
		}))
		return 0
	}
}

// timerFunc implements host.every and host.after: fn(seconds, callback).
func timerFunc(m *Module, name string, schedule func(time.Duration, func(context.Context)) error) vm.NativeFunc {
	return func(v *vm.VM) int {
		seconds := argNumber(v, 1, name)
		fn := argFunc(v, 2, name)
		check(schedule(time.Duration(seconds*float64(time.Second)), func(ctx context.Context) {
			if err := m.call(ctx, fn); err != nil && ctx.Err() == nil {
				m.host.Logger().Error("timer failed", "error", err)
			}
		}))
		return 0
	}
}

func overlayTable(m *Module) *vm.Table {
	overlay := m.host.Overlay()
	t := vm.NewEmptyTable()
	for name, send := range map[string]func(context.Context, string, any) error{
		"push": overlay.Push, "set": overlay.Set,
	} {
		t.SetString(name, vm.NewNativeFunc(func(v *vm.VM) int {
			key := argString(v, 1, "overlay."+name)
			data, err := fromLua(v.Get(2))
			check(err)
			check(send(m.ctx, key, data))
			return 0
		}))
	}
	return t
}

func storeTable(m *Module) *vm.Table {
	store := m.host.Store()
	t := vm.NewEmptyTable()
	t.SetString("get", vm.NewNativeFunc(func(v *vm.VM) int {
		value, err := store.Get(m.ctx, argString(v, 1, "store.get"))
		if errors.Is(err, core.ErrNotFound) {
			v.Set(0, vm.Nil)
			return 1
		}
		check(err)
		v.Set(0, vm.NewString(string(value)))
		return 1
	}))
	t.SetString("put", vm.NewNativeFunc(func(v *vm.VM) int {
		check(store.Put(m.ctx, argString(v, 1, "store.put"), []byte(argString(v, 2, "store.put"))))
		return 0
	}))
	t.SetString("delete", vm.NewNativeFunc(func(v *vm.VM) int {
		err := store.Delete(m.ctx, argString(v, 1, "store.delete"))
		if !errors.Is(err, core.ErrNotFound) {
			check(err)
		}
		v.Set(0, vm.NewBool(err == nil))
		return 1
	}))
	t.SetString("list", vm.NewNativeFunc(func(v *vm.VM) int {
		values, err := store.List(m.ctx)
		check(err)
		out := make(map[string]any, len(values))
		for key, value := range values {
			out[key] = string(value)
		}
		v.Set(0, toLua(out))
		return 1
	}))
	return t
}

func logTable(logger *slog.Logger) *vm.Table {
	t := vm.NewEmptyTable()
	for name, level := range map[string]slog.Level{
		"debug": slog.LevelDebug, "info": slog.LevelInfo, "warn": slog.LevelWarn, "error": slog.LevelError,
	} {
		t.SetString(name, vm.NewNativeFunc(func(v *vm.VM) int {
			var attrs []any
			if fields := v.Get(2); fields.IsTable() {
				converted, err := fromLua(fields)
				check(err)
				if m, ok := converted.(map[string]any); ok {
					for _, key := range slices.Sorted(maps.Keys(m)) {
						attrs = append(attrs, key, m[key])
					}
				}
			}
			logger.Log(context.Background(), level, vm.ValueToString(v.Get(1)), attrs...)
			return 0
		}))
	}
	return t
}

func jsonTable() *vm.Table {
	t := vm.NewEmptyTable()
	t.SetString("encode", vm.NewNativeFunc(func(v *vm.VM) int {
		value, err := fromLua(v.Get(1))
		check(err)
		data, err := json.Marshal(value)
		check(err)
		v.Set(0, vm.NewString(string(data)))
		return 1
	}))
	t.SetString("decode", vm.NewNativeFunc(func(v *vm.VM) int {
		var value any
		check(json.Unmarshal([]byte(argString(v, 1, "json.decode")), &value))
		v.Set(0, toLua(value))
		return 1
	}))
	return t
}

func check(err error) {
	if err != nil {
		raise("%s", err)
	}
}

func argString(v *vm.VM, i int, fn string) string {
	arg := v.Get(i)
	if !arg.IsString() {
		raise("%s: argument %d must be a string, got %s", fn, i, arg.Type())
	}
	return arg.AsString()
}

func argFunc(v *vm.VM, i int, fn string) vm.Value {
	arg := v.Get(i)
	if !arg.IsCallable() {
		raise("%s: argument %d must be a function, got %s", fn, i, arg.Type())
	}
	return arg
}

func argNumber(v *vm.VM, i int, fn string) float64 {
	arg := v.Get(i)
	switch {
	case arg.IsInt():
		return float64(arg.AsInt())
	case arg.IsFloat():
		return arg.AsFloat()
	}
	raise("%s: argument %d must be a number, got %s", fn, i, arg.Type())
	return 0
}

func targetArg(v *vm.VM, i int, fn string) core.Target { return targetFrom(v.Get(i), fn) }

func targetFrom(value vm.Value, fn string) core.Target {
	if !value.IsTable() {
		raise("%s: target must be a table with platform and channel", fn)
	}
	t := value.AsTable()
	platform, channel := t.Get(vm.NewString("platform")), t.Get(vm.NewString("channel"))
	if !platform.IsString() || !channel.IsString() || platform.AsString() == "" || channel.AsString() == "" {
		raise("%s: target must be a table with platform and channel", fn)
	}
	return core.Target{Platform: platform.AsString(), Channel: channel.AsString()}
}
