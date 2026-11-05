package core

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
)

// Module is a unit of functionality. Start registers its commands, event
// handlers, and timers through the host. A module may also implement Stopper.
type Module interface {
	Name() string
	Start(*Host) error
}

// Stopper is an optional module hook. Stop runs after the module's registrations
// are removed and its running work has finished.
type Stopper interface {
	Stop(context.Context) error
}

// Manager loads and unloads modules. Lifecycle changes run one at a time, so
// module hooks and callbacks must not call Load or Unload themselves.
type Manager struct {
	mu       sync.Mutex
	parent   context.Context
	services Services
	modules  map[string]loadedModule
	order    []string
	closed   bool
}

type loadedModule struct {
	module Module
	scope  *scope
}

func NewManager(parent context.Context, services Services) (*Manager, error) {
	if parent == nil {
		return nil, errors.New("parent context is required")
	}
	if services.Events == nil || services.Commands == nil || services.Chat == nil {
		return nil, errors.New("events, commands, and chat services are required")
	}
	if services.Logger == nil {
		services.Logger = slog.Default()
	}
	if services.Storage == nil {
		services.Storage = NewMemoryStorage()
	}
	return &Manager{parent: parent, services: services, modules: make(map[string]loadedModule)}, nil
}

// Load starts module. If Start fails or panics, everything it registered is removed.
func (m *Manager) Load(module Module) error {
	if module == nil || module.Name() == "" {
		return errors.New("module with a nonempty name is required")
	}
	name := module.Name()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errors.New("manager is closed")
	}
	if _, exists := m.modules[name]; exists {
		return fmt.Errorf("module %q already loaded", name)
	}
	s := newScope(m.parent, m.services.Logger.With("module", name))
	host := &Host{name: name, scope: s, services: m.services}
	if err := s.call("start", func() error { return module.Start(host) }); err != nil {
		s.close()
		_ = stopModule(module, s)
		return fmt.Errorf("start module %q: %w", name, err)
	}
	m.modules[name] = loadedModule{module, s}
	m.order = append(m.order, name)
	return nil
}

func (m *Manager) Unload(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, exists := m.modules[name]
	if !exists {
		return fmt.Errorf("module %q not loaded", name)
	}
	delete(m.modules, name)
	m.order = slices.DeleteFunc(m.order, func(n string) bool { return n == name })
	entry.scope.close()
	if err := stopModule(entry.module, entry.scope); err != nil {
		return fmt.Errorf("stop module %q: %w", name, err)
	}
	return nil
}

// List returns loaded module names in load order.
func (m *Manager) List() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.order)
}

// Close unloads all modules in reverse load order. The services stay open.
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	m.closed = true
	var errs []error
	for _, name := range slices.Backward(m.order) {
		entry := m.modules[name]
		entry.scope.close()
		if err := stopModule(entry.module, entry.scope); err != nil {
			errs = append(errs, fmt.Errorf("stop module %q: %w", name, err))
		}
		delete(m.modules, name)
	}
	m.order = nil
	return errors.Join(errs...)
}

func stopModule(module Module, s *scope) error {
	stopper, ok := module.(Stopper)
	if !ok {
		return nil
	}
	return s.call("stop", func() error { return stopper.Stop(context.Background()) })
}
