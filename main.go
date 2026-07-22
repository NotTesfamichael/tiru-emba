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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	peerC := make(chan discovery.PeerSeen, 32)

	hb := discovery.Heartbeat{ID: selfID, Handle: h, TCPPort: *tcpPort}

	go func() {
		if err := discovery.Broadcast(ctx, hb); err != nil {
			fmt.Fprintln(os.Stderr, "discovery broadcast stopped:", err)
		}
	}()
	go func() {
		if err := discovery.Listen(ctx, selfID, peerC); err != nil {
			fmt.Fprintln(os.Stderr, "discovery listener stopped:", err)
		}
	}()

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
