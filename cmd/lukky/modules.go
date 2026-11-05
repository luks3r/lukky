package main

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/luks3r/lukky/core"
	"github.com/luks3r/lukky/lua"
)

// factory creates a fresh module instance, for loading and reloading.
type factory func() (core.Module, error)

// loadLuaModules loads each subdirectory of cfg.Lua.Dir that has an init.lua.
// Modules whose requirements are not loaded yet are retried after the others.
func loadLuaModules(manager *core.Manager, cfg Config) map[string]factory {
	entries, err := os.ReadDir(cfg.Lua.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		slog.Error("read lua modules", "dir", cfg.Lua.Dir, "error", err)
		return nil
	}
	opts := lua.Options{MaxInstructions: cfg.Lua.MaxInstructions}
	var pending []string
	for _, entry := range entries {
		dir := filepath.Join(cfg.Lua.Dir, entry.Name())
		if _, err := os.Stat(filepath.Join(dir, "init.lua")); entry.IsDir() && err == nil {
			pending = append(pending, dir)
		}
	}
	factories := map[string]factory{}
	for len(pending) > 0 {
		var retry []string
		var lastErr error
		for _, dir := range pending {
			newModule := func() (core.Module, error) { return lua.Load(dir, opts) }
			module, err := newModule()
			if err != nil {
				slog.Error("load lua module", "dir", dir, "error", err)
				continue
			}
			if err := manager.Load(module); err != nil {
				if errors.Is(err, core.ErrMissingRequirement) {
					retry = append(retry, dir)
					lastErr = err
					continue
				}
				slog.Error("load lua module", "dir", dir, "error", err)
				continue
			}
			factories[module.Name()] = newModule
			slog.Info("lua module loaded", "module", module.Name())
		}
		if len(retry) == len(pending) {
			for _, dir := range retry {
				slog.Error("load lua module", "dir", dir, "error", lastErr)
			}
			break
		}
		pending = retry
	}
	return factories
}
