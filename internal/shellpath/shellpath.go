// Package shellpath resolves a file path a user typed or dropped into a
// terminal, undoing whichever escaping convention their terminal used --
// most wrap the whole path in matching quotes when it's dragged in, but
// some (e.g. Ghostty) backslash-escape each special character instead, so
// "My Photo.png" arrives as `My\ Photo.png`. Shared by anywhere in the UI
// that accepts a typed/dropped file path: chat's file-send detection and
// registration's profile-picture prompt both hit the exact same escaping
// quirk.
package shellpath

import (
	"os"
	"strings"
)

// Resolve unescapes/unquotes input and reports whether the result names an
// existing regular file. The unescaped path is always returned, even when
// ok is false, so a caller can still show it in an error message.
func Resolve(input string) (path string, ok bool) {
	path = strings.TrimSpace(input)
	if len(path) >= 2 {
		if (path[0] == '\'' && path[len(path)-1] == '\'') || (path[0] == '"' && path[len(path)-1] == '"') {
			path = path[1 : len(path)-1]
		} else {
			path = unescape(path)
		}
	}
	if path == "" {
		return path, false
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return path, false
	}
	return path, true
}

// unescape undoes shell-style backslash escaping: a backslash followed by
// any character is replaced with just that character.
func unescape(s string) string {
	if !strings.ContainsRune(s, '\\') {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
