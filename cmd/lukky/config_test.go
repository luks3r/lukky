package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestConfigDefaultsWithoutFiles(t *testing.T) {
	dir := t.TempDir()
	c, err := loadConfig(filepath.Join(dir, "lukky.toml"), filepath.Join(dir, ".env"), func() []string { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if c.Greeter.Interval != 2*time.Minute || c.Log.Level != "info" || c.Twitch.ClientID != "" {
		t.Fatalf("got %+v", c)
	}
}

func TestConfigLayers(t *testing.T) {
	dir := t.TempDir()
	tomlPath := writeFile(t, dir, "lukky.toml", `
[twitch]
channels = ["FromToml"]

[greeter]
interval = "5m"
`)
	envPath := writeFile(t, dir, ".env", `
TWITCH_CLIENT_ID="from-dotenv"
TWITCH_CLIENT_SECRET="secret"
UNRELATED="ignored"
`)
	environ := func() []string {
		return []string{"TWITCH_CLIENT_ID=from-env", "LUKKY_LOG_LEVEL=debug", "PATH=/bin"}
	}
	c, err := loadConfig(tomlPath, envPath, environ)
	if err != nil {
		t.Fatal(err)
	}
	if c.Twitch.ClientID != "from-env" {
		t.Errorf("environment should win: %q", c.Twitch.ClientID)
	}
	if c.Twitch.ClientSecret != "secret" {
		t.Errorf(".env value missing: %q", c.Twitch.ClientSecret)
	}
	if !slices.Equal(c.Twitch.Channels, []string{"fromtoml"}) {
		t.Errorf("channels %q", c.Twitch.Channels)
	}
	if c.Greeter.Interval != 5*time.Minute || c.Log.Level != "debug" {
		t.Errorf("got %+v", c)
	}
}

func TestConfigChannelsFromEnvironment(t *testing.T) {
	dir := t.TempDir()
	environ := func() []string { return []string{"TWITCH_CHANNELS= luks3r, Other ,"} }
	c, err := loadConfig(filepath.Join(dir, "none.toml"), filepath.Join(dir, "none.env"), environ)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(c.Twitch.Channels, []string{"luks3r", "other"}) {
		t.Fatalf("channels %q", c.Twitch.Channels)
	}
}

func TestConfigRejectsBadToml(t *testing.T) {
	dir := t.TempDir()
	tomlPath := writeFile(t, dir, "lukky.toml", "[greeter\n")
	if _, err := loadConfig(tomlPath, filepath.Join(dir, ".env"), func() []string { return nil }); err == nil {
		t.Fatal("expected parse error")
	}
}
