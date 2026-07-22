package store

import (
	"testing"
	"time"
)

func TestRoundTripPersistsAcrossReopen(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	s1, entries, err := Open("@kal")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected no prior entries, got %d", len(entries))
	}

	want := Entry{Kind: KindDirectRecv, Peer: "@sam", Body: "hey", At: time.Now().Truncate(time.Second)}
	if err := s1.Append(want); err != nil {
		t.Fatalf("Append: %v", err)
	}
	// A system entry must NOT survive a reopen -- it's session-only noise.
	if err := s1.Append(Entry{Kind: KindSystem, Body: "@sam joined"}); err != nil {
		t.Fatalf("Append system: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	_, reloaded, err := Open("@kal")
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if len(reloaded) != 1 {
		t.Fatalf("reloaded = %d entries, want 1 (system entries should not persist)", len(reloaded))
	}
	got := reloaded[0]
	if got.Kind != want.Kind || got.Peer != want.Peer || got.Body != want.Body || !got.At.Equal(want.At) {
		t.Errorf("reloaded entry = %+v, want %+v", got, want)
	}
}

func TestDifferentHandlesGetSeparateHistories(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	sKal, _, err := Open("@kal")
	if err != nil {
		t.Fatalf("Open @kal: %v", err)
	}
	if err := sKal.Append(Entry{Kind: KindSelfNote, Body: "kal's note"}); err != nil {
		t.Fatal(err)
	}
	sKal.Close()

	_, samEntries, err := Open("@sam")
	if err != nil {
		t.Fatalf("Open @sam: %v", err)
	}
	if len(samEntries) != 0 {
		t.Errorf("@sam's history should be independent of @kal's, got %d entries", len(samEntries))
	}
}
