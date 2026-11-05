package main

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"time"

	"github.com/knadh/koanf/parsers/dotenv"
	"github.com/knadh/koanf/parsers/toml/v2"
	"github.com/knadh/koanf/providers/env/v2"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
)

// Config is read from these layers, later ones winning:
//
//  1. defaults below
//  2. lukky.toml: settings without secrets (see lukky.example.toml)
//  3. .env: secrets (see .env.example)
//  4. real environment variables
//
// Environment and .env keys map to config keys by section prefix:
// TWITCH_CLIENT_ID is twitch.client_id, LUKKY_GREETER_INTERVAL is greeter.interval.
type Config struct {
	Log struct {
		Level string `koanf:"level"` // debug, info, warn, error
	} `koanf:"log"`
	Twitch struct {
		ClientID         string `koanf:"client_id"`
		ClientSecret     string `koanf:"client_secret"`
		UserToken        string `koanf:"user_token"`
		UserRefreshToken string `koanf:"user_refresh_token"`
		// Broadcaster tokens enable channel events (follows, subs, cheers, raids, ...).
		BroadcasterToken        string   `koanf:"broadcaster_token"`
		BroadcasterRefreshToken string   `koanf:"broadcaster_refresh_token"`
		Channels                []string `koanf:"channels"`
	} `koanf:"twitch"`
	Greeter struct {
		Interval time.Duration `koanf:"interval"` // 0 disables the reminder
	} `koanf:"greeter"`
	Overlay struct {
		Address string `koanf:"address"` // host:port for overlay pages; empty disables them
	} `koanf:"overlay"`
	Lua struct {
		Dir             string `koanf:"dir"`              // one subdirectory per module
		MaxInstructions int64  `koanf:"max_instructions"` // per callback; 0 uses the default
	} `koanf:"lua"`
}

func defaultConfig() Config {
	var c Config
	c.Log.Level = "info"
	c.Greeter.Interval = 2 * time.Minute
	c.Lua.Dir = "scripts"
	c.Overlay.Address = "127.0.0.1:7878"
	return c
}

// envKey maps an environment variable to a config key, or "" to ignore it.
func envKey(name string) string {
	switch {
	case strings.HasPrefix(name, "TWITCH_"):
		return "twitch." + strings.ToLower(strings.TrimPrefix(name, "TWITCH_"))
	case strings.HasPrefix(name, "LUKKY_"):
		section, key, ok := strings.Cut(strings.ToLower(strings.TrimPrefix(name, "LUKKY_")), "_")
		if !ok {
			return ""
		}
		return section + "." + key
	}
	return ""
}

// envValue turns comma-separated text into a list for list-valued keys.
func envValue(key, value string) any {
	if key == "twitch.channels" {
		var list []string
		for item := range strings.SplitSeq(value, ",") {
			if item = strings.TrimSpace(item); item != "" {
				list = append(list, item)
			}
		}
		return list
	}
	return value
}

func transform(name, value string) (string, any) {
	key := envKey(name)
	if key == "" {
		return "", nil
	}
	return key, envValue(key, value)
}

func loadConfig(tomlPath, dotenvPath string, environ func() []string) (Config, error) {
	k := koanf.New(".")
	layers := []struct {
		path   string
		parser koanf.Parser
	}{
		{tomlPath, toml.Parser()},
		{dotenvPath, dotenv.ParserEnvWithValue("", ".", transform)},
	}
	for _, layer := range layers {
		err := k.Load(file.Provider(layer.path), layer.parser)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return Config{}, fmt.Errorf("load %s: %w", layer.path, err)
		}
	}
	if err := k.Load(env.Provider(".", env.Opt{TransformFunc: transform, EnvironFunc: environ}), nil); err != nil {
		return Config{}, fmt.Errorf("load environment: %w", err)
	}
	c := defaultConfig()
	if err := k.Unmarshal("", &c); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	for i, channel := range c.Twitch.Channels {
		c.Twitch.Channels[i] = strings.ToLower(strings.TrimSpace(channel))
	}
	return c, nil
}
