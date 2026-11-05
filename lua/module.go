// Package lua runs Lukky modules written in Lua 5.5.
//
// A Lua module is a directory with an init.lua that returns a table with a
// name, optional requires, a start function, and an optional stop function.
// Each module gets its own Lua state. Calls into one module are serialized,
// interrupted on unload, and limited to a number of instructions.
// See docs/lua.md for the host API.
package lua

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/iceisfun/golua/v2/compiler"
	"github.com/iceisfun/golua/v2/parser"
	"github.com/iceisfun/golua/v2/stdlib"
	"github.com/iceisfun/golua/v2/vm"

	"github.com/luks3r/lukky/core"
)

// DefaultMaxInstructions bounds one callback, so a runaway loop fails fast.
const DefaultMaxInstructions = 10_000_000

type Options struct {
	// MaxInstructions limits each call into the module; 0 means DefaultMaxInstructions.
	MaxInstructions int64
}

// Module is a Lua module. It implements core.Module, core.Stopper, and
// core.Requirer. A Module can be started once; load it again to reload.
type Module struct {
	path     string
	name     string
	requires []string
	start    vm.Value
	stop     vm.Value

	mu   sync.Mutex
	vm   *vm.VM
	host *core.Host
	ctx  context.Context // context of the call in progress; guarded by mu
}

// Load reads dir/init.lua and evaluates it to get the module definition.
func Load(dir string, opts Options) (*Module, error) {
	path := filepath.Join(dir, "init.lua")
	src, err := os.ReadFile(path) //nolint:gosec // G304: module directories are chosen by the operator
	if err != nil {
		return nil, err
	}
	if opts.MaxInstructions <= 0 {
		opts.MaxInstructions = DefaultMaxInstructions
	}
	m := &Module{path: path, vm: newVM(opts.MaxInstructions)}
	if err := m.define(string(src)); err != nil {
		_ = m.vm.Close(context.Background())
		return nil, fmt.Errorf("load %s: %w", path, err)
	}
	return m, nil
}

func newVM(maxInstructions int64) *vm.VM {
	v := vm.New(vm.WithLimits(vm.Limits{MaxInstructions: maxInstructions}))
	// Without providers, stdlib has no io, os, debug, or process access.
	stdlib.Open(v)
	// Loading code from strings or files would bypass the module boundary.
	for _, name := range []string{"load", "loadfile", "dofile", "require", "package", "_lastoutput", "_outputlines"} {
		v.SetGlobal(name, vm.Nil)
	}
	return v
}

func (m *Module) define(src string) error {
	block, err := parser.Parse(m.path, src)
	if err != nil {
		return err
	}
	proto, err := compiler.Compile(m.path, block)
	if err != nil {
		return err
	}
	m.vm.SetContext(context.Background())
	results, err := m.vm.Run(proto)
	if err != nil {
		return err
	}
	if len(results) == 0 || !results[0].IsTable() {
		return errors.New("init.lua must return a module table")
	}
	def := results[0].AsTable()
	name := def.Get(vm.NewString("name"))
	if !name.IsString() || name.AsString() == "" {
		return errors.New("module table needs a nonempty string name")
	}
	m.name = name.AsString()
	m.start = def.Get(vm.NewString("start"))
	if !m.start.IsCallable() {
		return errors.New("module table needs a start function")
	}
	m.stop = def.Get(vm.NewString("stop"))
	if !m.stop.IsNil() && !m.stop.IsCallable() {
		return errors.New("stop must be a function")
	}
	requires, err := fromLua(def.Get(vm.NewString("requires")))
	if err != nil {
		return fmt.Errorf("requires: %w", err)
	}
	if requires != nil {
		list, ok := requires.([]any)
		if !ok {
			return errors.New("requires must be a list of module names")
		}
		for _, item := range list {
			req, ok := item.(string)
			if !ok {
				return errors.New("requires must be a list of module names")
			}
			m.requires = append(m.requires, req)
		}
	}
	return nil
}

func (m *Module) Name() string       { return m.name }
func (m *Module) Requires() []string { return m.requires }

func (m *Module) Start(host *core.Host) error {
	m.host = host
	return m.call(host.Context(), m.start, newHostTable(m))
}

// Stop runs the module's stop function, if any, and releases the Lua state.
func (m *Module) Stop(ctx context.Context) error {
	var err error
	if m.stop.IsCallable() {
		err = m.call(ctx, m.stop)
	}
	return errors.Join(err, m.vm.Close(ctx))
}

// heldKey marks a context while its module's lock is held up the call stack.
type heldKey struct{ m *Module }

// call runs fn in the module's Lua state. Calls are serialized per module.
// A call made while the same module is already running further up the stack,
// for example through an event the module emitted, reuses the held lock.
func (m *Module) call(ctx context.Context, fn vm.Value, args ...vm.Value) error {
	if ctx.Value(heldKey{m}) == nil {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.vm.ResetInstructionCount()
		ctx = context.WithValue(ctx, heldKey{m}, true)
	}
	outer := m.ctx
	m.ctx = ctx
	m.vm.SetContext(ctx)
	defer func() {
		m.ctx = outer
		if outer != nil {
			m.vm.SetContext(outer)
		}
	}()
	if _, err := m.vm.ProtectedCall(fn, args); err != nil {
		return fmt.Errorf("lua module %q: %w", m.name, err)
	}
	return nil
}

// raise aborts the running Lua function with an error message.
func raise(format string, args ...any) {
	panic(&vm.LuaError{Value: vm.NewString(fmt.Sprintf(format, args...))})
}

var (
	_ core.Module   = (*Module)(nil)
	_ core.Stopper  = (*Module)(nil)
	_ core.Requirer = (*Module)(nil)
)
