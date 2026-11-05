package core

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

type requiringModule struct {
	testModule
	requires []string
}

func (m *requiringModule) Requires() []string { return m.requires }

func requiring(name string, requires ...string) *requiringModule {
	return &requiringModule{testModule: testModule{name: name}, requires: requires}
}

func TestLoadFailsWithoutRequirement(t *testing.T) {
	m, _, _ := newTestManager(t)
	err := m.Load(requiring("stats", "storage"))
	if !errors.Is(err, ErrMissingRequirement) {
		t.Fatalf("got %v", err)
	}
	if len(m.List()) != 0 {
		t.Fatal("module loaded despite missing requirement")
	}
}

func TestLoadSucceedsAfterRequirement(t *testing.T) {
	m, _, _ := newTestManager(t)
	if err := m.Load(&testModule{name: "storage"}); err != nil {
		t.Fatal(err)
	}
	if err := m.Load(requiring("stats", "storage")); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(m.List(), []string{"storage", "stats"}) {
		t.Fatalf("loaded %v", m.List())
	}
}

func TestUnloadFailsWhileRequired(t *testing.T) {
	m, _, _ := newTestManager(t)
	for _, mod := range []Module{&testModule{name: "base"}, requiring("a", "base"), requiring("b", "base")} {
		if err := m.Load(mod); err != nil {
			t.Fatal(err)
		}
	}
	err := m.Unload("base")
	if !errors.Is(err, ErrRequiredByOthers) {
		t.Fatalf("got %v", err)
	}
	if want := `["a" "b"]`; !strings.Contains(err.Error(), want) {
		t.Fatalf("error %q should name dependents %s", err, want)
	}
	if err := m.Unload("a"); err != nil {
		t.Fatal(err)
	}
	if err := m.Unload("b"); err != nil {
		t.Fatal(err)
	}
	if err := m.Unload("base"); err != nil {
		t.Fatalf("unload after dependents: %v", err)
	}
}

func TestModuleCannotRequireItself(t *testing.T) {
	m, _, _ := newTestManager(t)
	if err := m.Load(requiring("loop", "loop")); !errors.Is(err, ErrMissingRequirement) {
		t.Fatalf("got %v", err)
	}
}

func TestCloseStopsDependentsFirst(t *testing.T) {
	var stopped []string
	record := func(name string) *testModule {
		return &testModule{name: name, onStop: func() { stopped = append(stopped, name) }}
	}
	m, _, _ := newTestManager(t)
	base := record("base")
	dependent := &requiringModule{testModule: *record("dependent"), requires: []string{"base"}}
	if err := m.Load(base); err != nil {
		t.Fatal(err)
	}
	if err := m.Load(dependent); err != nil {
		t.Fatal(err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(stopped, []string{"dependent", "base"}) {
		t.Fatalf("stop order %v", stopped)
	}
}
