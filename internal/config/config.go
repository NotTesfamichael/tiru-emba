// Package config persists small pieces of local client configuration --
// the registered handle, relay server settings, a resumable session token,
// and the last-known network status -- to ~/.tiru-emba/config.json, so a
// returning user doesn't have to pass --handle (or log in from scratch) on
// every run.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Config is the on-disk configuration format.
type Config struct {
	Handle string `json:"handle"`

	// ServerURL is the last --server address used, persisted the same way
	// Handle is so a returning user doesn't have to retype it.
	ServerURL string `json:"server_url,omitempty"`
	// LANStatus and WLANStatus reflect live network state as of the last
	// run ("connected"/"disconnected"): LANStatus for local UDP/TCP
	// discovery, WLANStatus for the relay/server connection specifically.
	// Written back after startup, not read as input.
	LANStatus  string `json:"lan_status,omitempty"`
	WLANStatus string `json:"wlan_status,omitempty"`

	// BackgroundNotification persists the --background-notification flag
	// so it doesn't need to be passed on every run.
	BackgroundNotification bool `json:"background_notification"`

	// SessionToken/SessionExpiresAt let a relay login be resumed
	// automatically on the next launch instead of prompting for a
	// password again -- org selection is still always required afresh,
	// regardless of whether the session itself was resumed.
	SessionToken     string    `json:"session_token,omitempty"`
	SessionExpiresAt time.Time `json:"session_expires_at,omitempty"`
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

// Update loads the existing config (if any), applies mutate, and saves the
// result -- so persisting just one setting never clobbers the others the
// way overwriting with a freshly zero-valued Config{} would.
func Update(mutate func(*Config)) error {
	c, err := Load()
	if err != nil {
		return err
	}
	mutate(&c)
	return Save(c)
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
