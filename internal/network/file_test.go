package network

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func startTestServer(t *testing.T, fileDir string) (addr string, offerC chan FileOffer, resultC chan FileResult) {
	t.Helper()
	addr, _, offerC, resultC, _ = startTestServerFull(t, fileDir)
	return addr, offerC, resultC
}

func startTestServerFull(t *testing.T, fileDir string) (addr string, msgC chan Received, offerC chan FileOffer, resultC chan FileResult, inviteC chan GameInvite) {
	t.Helper()
	srv, err := NewServer(0, fileDir)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	addr = srv.ln.Addr().String()

	msgC = make(chan Received, 4)
	offerC = make(chan FileOffer, 4)
	resultC = make(chan FileResult, 4)
	inviteC = make(chan GameInvite, 4)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go srv.Run(ctx, msgC, offerC, resultC, inviteC)

	return addr, msgC, offerC, resultC, inviteC
}

func TestSendFileAcceptedRoundTrip(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()

	content := []byte("hello from @alex, this is the file body")
	srcPath := filepath.Join(srcDir, "note.txt")
	if err := os.WriteFile(srcPath, content, 0o644); err != nil {
		t.Fatal(err)
	}

	addr, offerC, resultC := startTestServer(t, dstDir)

	// Simulate the receiving human accepting.
	go func() {
		offer := <-offerC
		if offer.From != "@alex" || offer.FileName != "note.txt" || offer.FileSize != int64(len(content)) {
			t.Errorf("unexpected offer: %+v", offer)
		}
		offer.Respond(true)
	}()

	accepted, reason, err := SendFile(addr, "@alex", srcPath)
	if err != nil {
		t.Fatalf("SendFile: %v", err)
	}
	if !accepted {
		t.Fatalf("expected accepted=true, reason=%q", reason)
	}

	select {
	case result := <-resultC:
		if result.Err != nil {
			t.Fatalf("receive side error: %v", result.Err)
		}
		got, err := os.ReadFile(result.SavedPath)
		if err != nil {
			t.Fatalf("reading saved file: %v", err)
		}
		if string(got) != string(content) {
			t.Errorf("saved content = %q, want %q", got, content)
		}
		if filepath.Dir(result.SavedPath) != dstDir {
			t.Errorf("saved to %q, want inside %q", result.SavedPath, dstDir)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for FileResult")
	}
}

func TestSendFileDenied(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()

	srcPath := filepath.Join(srcDir, "note.txt")
	if err := os.WriteFile(srcPath, []byte("nope"), 0o644); err != nil {
		t.Fatal(err)
	}

	addr, offerC, _ := startTestServer(t, dstDir)

	go func() {
		offer := <-offerC
		offer.Respond(false)
	}()

	accepted, reason, err := SendFile(addr, "@alex", srcPath)
	if err != nil {
		t.Fatalf("SendFile: %v", err)
	}
	if accepted {
		t.Fatal("expected accepted=false")
	}
	if reason != "declined" {
		t.Errorf("reason = %q, want %q", reason, "declined")
	}

	entries, _ := os.ReadDir(dstDir)
	if len(entries) != 0 {
		t.Errorf("expected no file written on denial, found %d entries", len(entries))
	}
}

func TestSendFileOversizedAutoDenied(t *testing.T) {
	dstDir := t.TempDir()

	// The server auto-denies before ever surfacing an offer to the human,
	// based purely on the claimed size in the envelope. Real SendFile
	// refuses locally for an oversized file (no real 200MB+ file needed),
	// so this exercises the server's own defense directly at the wire level
	// -- it must not trust a peer's claimed size unconditionally.
	addr, offerC, _ := startTestServer(t, dstDir)
	go func() {
		select {
		case offer := <-offerC:
			t.Errorf("oversized offer should never reach the human, got %+v", offer)
			offer.Respond(false)
		case <-time.After(500 * time.Millisecond):
		}
	}()

	conn, err := net.DialTimeout("tcp", addr, dialTimeout)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	env := envelope{Type: typeFileOffer, From: "@alex", FileName: "huge.bin", FileSize: MaxFileSize + 1, TransferID: "t1"}
	b, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write(append(b, '\n')); err != nil {
		t.Fatalf("write offer: %v", err)
	}

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	reader := bufio.NewReader(conn)
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("reading response: %v", err)
	}
	var resp envelope
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Type != typeFileDeny {
		t.Errorf("response type = %q, want %q", resp.Type, typeFileDeny)
	}
	if resp.Reason == "" {
		t.Error("expected a non-empty deny reason for an oversized claim")
	}
}

func TestSanitizeFileNameRejectsPathTraversal(t *testing.T) {
	cases := map[string]bool{ // input -> must NOT contain a path separator after sanitizing
		"normal.txt":          true,
		"../../etc/passwd":    true,
		"..":                  true,
		".":                   true,
		"/etc/passwd":         true,
		"a/b/c.png":           true,
		"  spaced name.pdf  ": true,
	}
	for in := range cases {
		got := sanitizeFileName(in)
		if got == "" || got == "." || got == ".." {
			t.Errorf("sanitizeFileName(%q) = %q, want a safe non-empty basename", in, got)
			continue
		}
		if filepath.Base(got) != got {
			t.Errorf("sanitizeFileName(%q) = %q, contains path separators", in, got)
		}
	}
}

func TestUniquePathAvoidsCollisions(t *testing.T) {
	dir := t.TempDir()
	first := uniquePath(dir, "photo.png")
	if filepath.Base(first) != "photo.png" {
		t.Errorf("first uniquePath = %q, want photo.png", first)
	}
	os.WriteFile(first, []byte("a"), 0o644)

	second := uniquePath(dir, "photo.png")
	if filepath.Base(second) != "photo (1).png" {
		t.Errorf("second uniquePath = %q, want photo (1).png", second)
	}
	os.WriteFile(second, []byte("b"), 0o644)

	third := uniquePath(dir, "photo.png")
	if filepath.Base(third) != "photo (2).png" {
		t.Errorf("third uniquePath = %q, want photo (2).png", third)
	}
}

func TestHumanSize(t *testing.T) {
	cases := []struct {
		n    int64
		want string
	}{
		{500, "500 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1024 * 1024, "1.0 MB"},
	}
	for _, c := range cases {
		if got := HumanSize(c.n); got != c.want {
			t.Errorf("HumanSize(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}
