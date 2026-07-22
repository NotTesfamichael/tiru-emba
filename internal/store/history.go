// Package store persists chat history to disk (one JSON object per line,
// under ~/.tiru-emba/history/<handle>.jsonl) so it survives an app restart
// or a lost network connection, not just the lifetime of the process.
package store

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Kind distinguishes why an Entry exists, for rendering and filtering.
type Kind string

const (
	KindSystem     Kind = "system"      // joined/left/errors -- session-only, never persisted
	KindSelfNote   Kind = "self_note"   // plain typed text with no @handle target
	KindDirectSent Kind = "direct_sent" // you -> Peer
	KindDirectRecv Kind = "direct_recv" // Peer -> you
)

// Entry is one line of chat history.
type Entry struct {
	Kind Kind      `json:"kind"`
	Peer string    `json:"peer,omitempty"` // the OTHER party's handle, for the two Direct kinds
	Body string    `json:"body"`
	At   time.Time `json:"at"`
}

// Store appends Entries to a per-handle history file. It's not safe for
// concurrent use, matching every other piece of Model state -- it's only
// ever touched from the Bubble Tea Update loop.
type Store struct {
	f *os.File
}

// Open loads any existing history for handle and returns a Store ready to
// append more. If the history file/directory can't be created, history
// simply won't persist for this run (err is returned so the caller can
// decide whether to warn, but it's not fatal -- the chat still works).
func Open(handle string) (*Store, []Entry, error) {
	path, err := pathFor(handle)
	if err != nil {
		return nil, nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, nil, fmt.Errorf("store: create history dir: %w", err)
	}

	var entries []Entry
	if existing, err := os.Open(path); err == nil {
		scanner := bufio.NewScanner(existing)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			var e Entry
			if err := json.Unmarshal(scanner.Bytes(), &e); err == nil {
				entries = append(entries, e)
			}
		}
		existing.Close()
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("store: open history file: %w", err)
	}

	return &Store{f: f}, entries, nil
}

// Append persists one entry. KindSystem entries are meant to be session-only
// and are silently skipped rather than cluttering the saved history with
// join/leave noise across restarts.
func (s *Store) Append(e Entry) error {
	if s == nil || e.Kind == KindSystem {
		return nil
	}
	b, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("store: marshal entry: %w", err)
	}
	b = append(b, '\n')
	if _, err := s.f.Write(b); err != nil {
		return fmt.Errorf("store: write entry: %w", err)
	}
	return nil
}

func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	return s.f.Close()
}

var unsafeChars = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

func pathFor(handle string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("store: resolve home dir: %w", err)
	}
	safe := unsafeChars.ReplaceAllString(strings.TrimPrefix(handle, "@"), "_")
	if safe == "" {
		safe = "default"
	}
	return filepath.Join(home, ".tiru-emba", "history", safe+".jsonl"), nil
}
