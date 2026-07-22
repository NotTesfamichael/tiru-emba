// Command tiru-emba is a zero-configuration, LAN-only terminal chat client:
// peer discovery over UDP multicast/broadcast, direct messaging over TCP,
// and a Bubble Tea shell tying them together.
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
	"github.com/NotTesfamichael/tiru-emba/internal/filedrop"
	"github.com/NotTesfamichael/tiru-emba/internal/network"
	"github.com/NotTesfamichael/tiru-emba/internal/ui"
)

func main() {
	handle := flag.String("handle", "", `your display handle, e.g. "@alex" (required)`)
	tcpPort := flag.Int("port", 7777, "TCP port to listen on for direct messages")
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
	// ~/Tiru_File is where accepted incoming file transfers land. Created up
	// front so it's there before anyone ever sends a file, not just on first
	// receipt; a failure here isn't fatal (chat still works), just a warning.
	fileDir, err := filedrop.Dir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "warning: could not create", filedrop.DirName, "directory:", err)
		fmt.Fprintln(os.Stderr, "file transfers will fail to save until this is fixed")
	}

	msgServer, err := network.NewServer(*tcpPort, fileDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: could not start message server:", err)
		fmt.Fprintf(os.Stderr, "hint: another process may already be using TCP port %d (try --port)\n", *tcpPort)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	peerC := make(chan discovery.PeerSeen, 32)
	msgC := make(chan network.Received, 32)
	fileOfferC := make(chan network.FileOffer, 8)
	fileResultC := make(chan network.FileResult, 8)
	gameInviteC := make(chan network.GameInvite, 8)

	go broadcaster.Run(ctx)
	go listener.Run(ctx, selfID, peerC)
	go msgServer.Run(ctx, msgC, fileOfferC, fileResultC, gameInviteC)

	chat := ui.New(ctx, selfID, h, peerC, msgC, fileOfferC, fileResultC, gameInviteC)
	app := ui.NewApp(chat)
	p := tea.NewProgram(app, tea.WithAltScreen())

	final, err := p.Run()
	if a, ok := final.(ui.App); ok {
		a.Close()
	}
	if err != nil {
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
