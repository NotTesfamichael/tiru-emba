// Package filedrop resolves the shared directory every installed client
// saves accepted file transfers into.
package filedrop

import (
	"fmt"
	"os"
	"path/filepath"
)

// DirName is the folder under the user's home directory that accepted file
// transfers are saved into -- the same place browsers save downloads to, so
// it's already familiar and already exists on virtually every machine.
const DirName = "Downloads"

// Dir returns the absolute path to ~/Downloads, creating it if it somehow
// doesn't already exist.
func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("filedrop: resolve home dir: %w", err)
	}
	dir := filepath.Join(home, DirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("filedrop: create %s: %w", dir, err)
	}
	return dir, nil
}
