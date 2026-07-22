// Package filedrop resolves the shared directory every installed client
// saves accepted file transfers into.
package filedrop

import (
	"fmt"
	"os"
	"path/filepath"
)

// DirName is the folder created under the user's home directory.
const DirName = "Tiru_File"

// Dir returns the absolute path to ~/Tiru_File, creating it if needed so
// every install has it ready before any transfer arrives.
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
