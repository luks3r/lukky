package main

import (
	"context"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/luks3r/lukky/core"
)

type nopChat struct{}

func (nopChat) Send(context.Context, core.Target, string) error { return nil }

func writeModule(t *testing.T, root, dir, src string) {
	t.Helper()
	path := filepath.Join(root, dir)
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "init.lua"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadLuaModules(t *testing.T) {
	root := t.TempDir()
	// "a-child" sorts first but requires "b-parent", so it must be retried.
	writeModule(t, root, "a-child", `return { name = "child", requires = { "parent" }, start = function() end }`)
	writeModule(t, root, "b-parent", `return { name = "parent", start = function() end }`)
	writeModule(t, root, "c-broken", `return {`)
	writeModule(t, root, "d-orphan", `return { name = "orphan", requires = { "missing" }, start = function() end }`)
	if err := os.Mkdir(filepath.Join(root, "e-empty"), 0o700); err != nil {
		t.Fatal(err)
	}

	slog.SetDefault(slog.New(slog.DiscardHandler))
	manager, err := core.NewManager(context.Background(), core.Services{Events: core.NewBus(), Commands: core.NewRouter(), Chat: nopChat{}})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	var cfg Config
	cfg.Lua.Dir = root
	factories := loadLuaModules(manager, cfg)

	if got := manager.List(); !slices.Equal(got, []string{"parent", "child"}) {
		t.Fatalf("loaded %v", got)
	}
	if got := slices.Sorted(maps.Keys(factories)); !slices.Equal(got, []string{"child", "parent"}) {
		t.Fatalf("factories %v", got)
	}
}

func TestLoadLuaModulesWithoutDirectory(t *testing.T) {
	var cfg Config
	cfg.Lua.Dir = filepath.Join(t.TempDir(), "missing")
	if got := loadLuaModules(nil, cfg); got != nil {
		t.Fatalf("got %v", got)
	}
}

func TestExampleModuleLoads(t *testing.T) {
	var cfg Config
	cfg.Lua.Dir = "../../examples/lua"
	manager, err := core.NewManager(context.Background(), core.Services{Events: core.NewBus(), Commands: core.NewRouter(), Chat: nopChat{}})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	loadLuaModules(manager, cfg)
	if got := manager.List(); !slices.Contains(got, "shoutout") {
		t.Fatalf("example not loaded: %v", got)
	}
}
