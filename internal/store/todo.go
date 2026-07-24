// Package store also persists a small personal todo list per handle to
// ~/.tiru-emba/todos/<handle>.json -- unlike chat history (append-only,
// one line per entry), a todo list is tiny and its items get toggled in
// place, so it's simplest to just rewrite the whole file on every change.
package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// TodoItem is one personal (local-only, never shared) task.
type TodoItem struct {
	ID   int       `json:"id"`
	Text string    `json:"text"`
	Done bool      `json:"done"`
	At   time.Time `json:"at"`
}

// TodoStore persists a handle's personal todo list. Not safe for
// concurrent use, matching every other piece of Model state -- it's only
// ever touched from the Bubble Tea Update loop.
type TodoStore struct {
	path   string
	nextID int
}

// OpenTodos loads any existing personal todo list for handle, returning a
// TodoStore ready to append/update more. A missing file isn't an error --
// it just means no todos have been added yet.
func OpenTodos(handle string) (*TodoStore, []TodoItem, error) {
	path, err := todoPathFor(handle)
	if err != nil {
		return nil, nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, nil, fmt.Errorf("store: create todos dir: %w", err)
	}

	var items []TodoItem
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &items)
	} else if !os.IsNotExist(err) {
		return nil, nil, fmt.Errorf("store: read %s: %w", path, err)
	}

	maxID := 0
	for _, it := range items {
		if it.ID > maxID {
			maxID = it.ID
		}
	}
	return &TodoStore{path: path, nextID: maxID + 1}, items, nil
}

// Add appends a new personal todo and persists the updated list, returning
// the created item (with its assigned ID).
func (s *TodoStore) Add(items []TodoItem, text string) ([]TodoItem, TodoItem, error) {
	item := TodoItem{ID: s.nextID, Text: text, At: time.Now()}
	s.nextID++
	items = append(items, item)
	return items, item, s.save(items)
}

// Complete marks the todo with the given id done and persists the updated
// list. Returns false if no such id exists (a no-op, not an error).
func (s *TodoStore) Complete(items []TodoItem, id int) ([]TodoItem, bool, error) {
	for i, it := range items {
		if it.ID == id {
			items[i].Done = true
			return items, true, s.save(items)
		}
	}
	return items, false, nil
}

func (s *TodoStore) save(items []TodoItem) error {
	b, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return fmt.Errorf("store: marshal todos: %w", err)
	}
	if err := os.WriteFile(s.path, b, 0o600); err != nil {
		return fmt.Errorf("store: write %s: %w", s.path, err)
	}
	return nil
}

func todoPathFor(handle string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("store: resolve home dir: %w", err)
	}
	safe := unsafeChars.ReplaceAllString(strings.TrimPrefix(handle, "@"), "_")
	if safe == "" {
		safe = "default"
	}
	return filepath.Join(home, ".tiru-emba", "todos", safe+".json"), nil
}
