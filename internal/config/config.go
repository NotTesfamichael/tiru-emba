// Package config persists small pieces of local client configuration --
// currently just the registered handle -- to ~/.tiru-emba/config.json, so a
// returning user doesn't have to pass --handle on every run.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Config is the on-disk configuration format.
type Config struct {
	Handle string `json:"handle"`
}

func path() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("config: resolve home dir: %w", err)
	}
	return filepath.Join(home, ".tiru-emba", "config.json"), nil
}

// Load reads the saved config. A missing file isn't an error -- it just
// means no handle has been registered yet -- but any other read/parse
// failure is returned so the caller can decide whether to warn.
func Load() (Config, error) {
	p, err := path()
	if err != nil {
		return Config{}, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return Config{}, nil
		}
		return Config{}, fmt.Errorf("config: read %s: %w", p, err)
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return Config{}, fmt.Errorf("config: parse %s: %w", p, err)
	}
	return c, nil
}

// Save persists c, registering it for future runs.
func Save(c Config) error {
	p, err := path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return fmt.Errorf("config: create dir: %w", err)
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("config: marshal: %w", err)
	}
	if err := os.WriteFile(p, b, 0o600); err != nil {
		return fmt.Errorf("config: write %s: %w", p, err)
	}
	return nil
}
