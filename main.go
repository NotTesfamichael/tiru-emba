// Command tiru-emba is a zero-configuration, LAN-only terminal chat client.
// Phase 1: peer discovery over UDP multicast + the Bubble Tea shell.
// Phase 2 (not yet implemented): direct TCP messaging between peers.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/NotTesfamichael/tiru-emba/internal/discovery"
	"github.com/NotTesfamichael/tiru-emba/internal/ui"
)

func main() {
	handle := flag.String("handle", "", `your display handle, e.g. "@alex" (required)`)
	tcpPort := flag.Int("port", 7777, "TCP port reserved for direct messaging (Phase 2)")
	flag.Parse()

	h := strings.TrimSpace(*handle)
	if h == "" {
		fmt.Fprintln(os.Stderr, `error: --handle is required, e.g. --handle=@alex`)
		os.Exit(1)
	}
	if !strings.HasPrefix(h, "@") {
		h = "@" + h
	}

	selfID, err := randomID()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: could not generate instance id:", err)
		os.Exit(1)
	}

	hb := discovery.Heartbeat{ID: selfID, Handle: h, TCPPort: *tcpPort}

	// Bind both sockets synchronously, before starting the TUI, so a failure
	// here (e.g. another process already using UDP port discovery.Port) is a
	// visible startup error instead of a goroutine failure silently
	// swallowed once Bubble Tea takes over the terminal.
	broadcaster, err := discovery.NewBroadcaster(hb)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: could not start peer discovery:", err)
		os.Exit(1)
	}
	listener, err := discovery.NewListener()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: could not start peer discovery:", err)
		fmt.Fprintf(os.Stderr, "hint: another process may already be using UDP port %d\n", discovery.Port)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	peerC := make(chan discovery.PeerSeen, 32)

	go broadcaster.Run(ctx)
	go listener.Run(ctx, selfID, peerC)

	model := ui.New(ctx, selfID, h, peerC)
	p := tea.NewProgram(model, tea.WithAltScreen())

	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "error running program:", err)
		os.Exit(1)
	}
}

func randomID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
