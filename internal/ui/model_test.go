package ui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NotTesfamichael/tiru-emba/internal/discovery"
	"github.com/NotTesfamichael/tiru-emba/internal/network"
	"github.com/NotTesfamichael/tiru-emba/internal/peer"
	"github.com/NotTesfamichael/tiru-emba/internal/store"
)

func newTestModel(t *testing.T) Model {
	t.Helper()
	t.Setenv("HOME", t.TempDir()) // sandbox history persistence away from the real user's home
	peerC := make(chan discovery.PeerSeen)
	msgC := make(chan network.Received)
	offerC := make(chan network.FileOffer)
	resultC := make(chan network.FileResult)
	return New(context.Background(), "self-id", "@me", peerC, msgC, offerC, resultC)
}

func TestSendDirectMultiTarget(t *testing.T) {
	m := newTestModel(t)
	m.peers.Upsert(peer.Info{ID: "1", Handle: "@kal", Addr: "127.0.0.1", TCPPort: 1})
	m.peers.Upsert(peer.Info{ID: "2", Handle: "@sam", Addr: "127.0.0.1", TCPPort: 2})

	m.input.SetValue("@kal @sam hello there")
	newModel, cmd := m.submitInput()
	m = newModel.(Model)

	if cmd == nil {
		t.Fatal("expected a non-nil batch cmd for two live sends")
	}

	var sentTo []string
	var bodies []string
	for _, e := range m.history {
		if e.Kind == store.KindDirectSent {
			sentTo = append(sentTo, e.Peer)
			bodies = append(bodies, e.Body)
		}
	}
	if len(sentTo) != 2 || sentTo[0] != "@kal" || sentTo[1] != "@sam" {
		t.Errorf("sentTo = %v, want [@kal @sam]", sentTo)
	}
	for _, b := range bodies {
		if b != "hello there" {
			t.Errorf("body = %q, want %q", b, "hello there")
		}
	}
}

func TestSendDirectUnknownPeer(t *testing.T) {
	m := newTestModel(t)
	m.input.SetValue("@ghost hi")
	newModel, _ := m.submitInput()
	m = newModel.(Model)

	found := false
	for _, e := range m.history {
		if e.Kind == store.KindSystem && e.Body == "no such peer online: @ghost" {
			found = true
		}
	}
	if !found {
		t.Error("expected a 'no such peer online' system note")
	}
}

func TestFilterCommand(t *testing.T) {
	m := newTestModel(t)
	m.peers.Upsert(peer.Info{ID: "1", Handle: "@kal", Addr: "127.0.0.1", TCPPort: 1})

	m.input.SetValue("/filter @kal")
	newModel, _ := m.submitInput()
	m = newModel.(Model)
	if m.filterPeer != "kal" {
		t.Errorf("filterPeer = %q, want %q", m.filterPeer, "kal")
	}

	m.input.SetValue("/clear")
	newModel, _ = m.submitInput()
	m = newModel.(Model)
	if m.filterPeer != "" {
		t.Errorf("filterPeer = %q, want empty after /clear", m.filterPeer)
	}
}

func TestFilterUnknownPeerLeavesFilterUnchanged(t *testing.T) {
	m := newTestModel(t)
	m.input.SetValue("/filter @ghost")
	newModel, _ := m.submitInput()
	m = newModel.(Model)
	if m.filterPeer != "" {
		t.Errorf("filterPeer = %q, want empty (unknown peer should not set a filter)", m.filterPeer)
	}
}

func TestColorForHandleDeterministicAndCaseInsensitive(t *testing.T) {
	if colorForHandle("@kal") != colorForHandle("@KAL") {
		t.Error("colorForHandle should be case-insensitive")
	}
	if colorForHandle("@kal") != colorForHandle("@kal") {
		t.Error("colorForHandle should be deterministic across calls")
	}
}

func TestFilePathInDetectsExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "photo.png")
	if err := os.WriteFile(path, []byte("fake image bytes"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got, ok := filePathIn(path); !ok || got != path {
		t.Errorf("filePathIn(%q) = (%q, %v), want (%q, true)", path, got, ok, path)
	}
	if got, ok := filePathIn("'" + path + "'"); !ok || got != path {
		t.Errorf("quoted filePathIn(%q) = (%q, %v), want (%q, true)", path, got, ok, path)
	}
	if _, ok := filePathIn("just a normal message"); ok {
		t.Error("filePathIn should not match ordinary chat text")
	}
	if _, ok := filePathIn(dir); ok {
		t.Error("filePathIn should not match a directory")
	}
}

// Ghostty (and other terminals) backslash-escape spaces/specials in a
// dropped file's path instead of quoting the whole thing -- this is the bug
// report that came in after v0.4.0 shipped: a real file with a space in its
// name never got recognized because the literal string (with the backslash)
// doesn't exist on disk.
func TestFilePathInHandlesShellEscapedSpaces(t *testing.T) {
	dir := t.TempDir()
	name := "2026-07-22 12.33.58.jpg"
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("fake image bytes"), 0o644); err != nil {
		t.Fatal(err)
	}

	escaped := filepath.Join(dir, `2026-07-22\ 12.33.58.jpg`)
	got, ok := filePathIn(escaped)
	if !ok {
		t.Fatalf("filePathIn(%q) = not found, want the unescaped path recognized", escaped)
	}
	if got != path {
		t.Errorf("filePathIn(%q) = %q, want %q", escaped, got, path)
	}
}

func TestSendDirectDetectsDroppedFile(t *testing.T) {
	m := newTestModel(t)
	m.peers.Upsert(peer.Info{ID: "1", Handle: "@kal", Addr: "127.0.0.1", TCPPort: 1})

	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(path, []byte("contents"), 0o644); err != nil {
		t.Fatal(err)
	}

	m.input.SetValue("@kal " + path)
	newModel, cmd := m.submitInput()
	m = newModel.(Model)
	if cmd == nil {
		t.Fatal("expected a non-nil cmd for the file offer")
	}

	found := false
	for _, e := range m.history {
		if e.Kind == store.KindSystem && strings.Contains(e.Body, "offering") && strings.Contains(e.Body, "note.txt") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected an 'offering note.txt' system note, history=%+v", m.history)
	}
}

func TestUpdateSuggestionsPrefixMatch(t *testing.T) {
	m := newTestModel(t)
	m.peers.Upsert(peer.Info{ID: "1", Handle: "@kal", Addr: "127.0.0.1", TCPPort: 1})
	m.peers.Upsert(peer.Info{ID: "2", Handle: "@karim", Addr: "127.0.0.1", TCPPort: 2})
	m.peers.Upsert(peer.Info{ID: "3", Handle: "@sam", Addr: "127.0.0.1", TCPPort: 3})

	m.input.SetValue("@ka")
	m.updateSuggestions()
	if len(m.suggestions) != 2 {
		t.Fatalf("suggestions = %v, want 2 matches for @ka", m.suggestions)
	}

	m.input.SetValue("@ka ")
	m.updateSuggestions()
	if len(m.suggestions) != 0 {
		t.Error("expected no suggestions once a trailing space closes the handle token")
	}
}
