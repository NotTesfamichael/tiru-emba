package shellpath

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveDetectsExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "photo.png")
	if err := os.WriteFile(path, []byte("fake image bytes"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got, ok := Resolve(path); !ok || got != path {
		t.Errorf("Resolve(%q) = (%q, %v), want (%q, true)", path, got, ok, path)
	}
	if got, ok := Resolve("'" + path + "'"); !ok || got != path {
		t.Errorf("quoted Resolve(%q) = (%q, %v), want (%q, true)", path, got, ok, path)
	}
	if _, ok := Resolve("just a normal message"); ok {
		t.Error("Resolve should not match ordinary text")
	}
	if _, ok := Resolve(dir); ok {
		t.Error("Resolve should not match a directory")
	}
}

// This is the exact bug reported live: dragging a file with a space in its
// name into a terminal that backslash-escapes (rather than quotes) special
// characters produced a literal "\ " in the input, which didn't match the
// real file on disk (a real space, not a backslash-space).
func TestResolveHandlesShellEscapedSpaces(t *testing.T) {
	dir := t.TempDir()
	name := "photo_2026-07-24 16.11.01.jpeg"
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("fake image bytes"), 0o644); err != nil {
		t.Fatal(err)
	}

	escaped := filepath.Join(dir, `photo_2026-07-24\ 16.11.01.jpeg`)
	got, ok := Resolve(escaped)
	if !ok {
		t.Fatalf("Resolve(%q) = not found, want the unescaped path recognized", escaped)
	}
	if got != path {
		t.Errorf("Resolve(%q) = %q, want %q", escaped, got, path)
	}
}

func TestResolveReturnsUnescapedPathEvenWhenNotFound(t *testing.T) {
	got, ok := Resolve(`/no/such/dir/a\ b.png`)
	if ok {
		t.Fatal("expected ok=false for a nonexistent file")
	}
	if got != "/no/such/dir/a b.png" {
		t.Errorf("got = %q, want the unescaped path even on failure", got)
	}
}
