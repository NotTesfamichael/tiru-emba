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
	"golang.org/x/term"

	"github.com/NotTesfamichael/tiru-emba/internal/config"
	"github.com/NotTesfamichael/tiru-emba/internal/discovery"
	"github.com/NotTesfamichael/tiru-emba/internal/filedrop"
	"github.com/NotTesfamichael/tiru-emba/internal/network"
	"github.com/NotTesfamichael/tiru-emba/internal/relay"
	"github.com/NotTesfamichael/tiru-emba/internal/ui"
)

func main() {
	handle := flag.String("handle", "", `your display handle, e.g. "@alex" (registers it for next time; omit to reuse a saved handle, or run anonymously if none is saved)`)
	tcpPort := flag.Int("port", 7777, "TCP port to listen on for direct messages")
	serverAddr := flag.String("server", "", "relay server address (host:port) for cross-network chat -- omit to stay LAN-only")
	serverRegister := flag.Bool("server-register", false, "register a new account on --server instead of logging into an existing one")
	lanEnabled := flag.Bool("lan", true, "enable LAN discovery/chat; --lan=false runs relay-only, e.g. to avoid a UDP port conflict with another local instance")
	flag.Parse()

	if !*lanEnabled && *serverAddr == "" {
		fmt.Fprintln(os.Stderr, "error: --lan=false with no --server would leave nothing to talk to")
		os.Exit(1)
	}

	h, err := resolveHandle(*handle)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	// Connect to the relay server (if requested) before starting the TUI,
	// same reasoning as the LAN sockets below: a bad address or rejected
	// login is a visible startup error, not something swallowed once
	// Bubble Tea takes over the terminal. An anonymous handle (no @handle
	// ever registered) can't usefully have a relay account either, since
	// the relay identifies accounts by handle -- caught here rather than
	// failing confusingly against the server.
	var relayClient *relay.Client
	if *serverAddr != "" {
		if strings.HasPrefix(h, "@anon-") {
			fmt.Fprintln(os.Stderr, "error: --server requires a registered handle (pass --handle=@you), not an anonymous one")
			os.Exit(1)
		}
		relayClient, err = connectToRelay(*serverAddr, h, *serverRegister)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	}

	selfID, err := randomID()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: could not generate instance id:", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	peerC := make(chan discovery.PeerSeen, 32)
	msgC := make(chan network.Received, 32)
	fileOfferC := make(chan network.FileOffer, 8)
	fileResultC := make(chan network.FileResult, 8)
	gameInviteC := make(chan network.GameInvite, 8)

	if *lanEnabled {
		hb := discovery.Heartbeat{ID: selfID, Handle: h, TCPPort: *tcpPort}

		// Bind both sockets synchronously, before starting the TUI, so a
		// failure here (e.g. another process already using UDP port
		// discovery.Port) is a visible startup error instead of a goroutine
		// failure silently swallowed once Bubble Tea takes over the terminal.
		broadcaster, err := discovery.NewBroadcaster(hb)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error: could not start peer discovery:", err)
			os.Exit(1)
		}
		listener, err := discovery.NewListener()
		if err != nil {
			fmt.Fprintln(os.Stderr, "error: could not start peer discovery:", err)
			fmt.Fprintf(os.Stderr, "hint: another process may already be using UDP port %d (or pass --lan=false for relay-only)\n", discovery.Port)
			os.Exit(1)
		}
		// ~/Downloads is where accepted incoming file transfers land.
		// Created up front so it's there before anyone ever sends a file,
		// not just on first receipt; a failure here isn't fatal (chat still
		// works), just a warning.
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

		go broadcaster.Run(ctx)
		go listener.Run(ctx, selfID, peerC)
		go msgServer.Run(ctx, msgC, fileOfferC, fileResultC, gameInviteC)
	}

	chat := ui.New(ctx, selfID, h, peerC, msgC, fileOfferC, fileResultC, gameInviteC, relayClient)
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

// connectToRelay dials addr and either registers a new account or logs
// into an existing one for handle, prompting for the password (masked,
// not echoed) synchronously before the TUI starts -- same reasoning as
// resolveHandle's messages: a rejected login should be a visible startup
// error, not something that surfaces confusingly once Bubble Tea owns the
// terminal.
func connectToRelay(addr, handle string, register bool) (*relay.Client, error) {
	client, err := relay.Dial(addr)
	if err != nil {
		return nil, fmt.Errorf("could not connect to relay server %s: %w", addr, err)
	}

	action := "log in to"
	if register {
		action = "register"
	}
	fmt.Printf("password to %s %s on %s: ", action, handle, addr)
	password, err := promptPassword()
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("could not read password: %w", err)
	}

	if register {
		_, _, err = client.Register(handle, password)
	} else {
		_, _, err = client.Login(handle, password)
	}
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("relay authentication failed: %w", err)
	}
	return client, nil
}

// promptPassword reads one line from stdin without echoing it to the
// terminal (via golang.org/x/term), printing a trailing newline afterward
// since the Enter keypress itself isn't echoed either.
func promptPassword() (string, error) {
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// resolveHandle implements a lightweight "registration" flow: an explicit
// --handle registers itself for next time (saved to config), no flag reuses
// a previously-registered handle silently, and with neither, a random
// anonymous handle is generated -- and deliberately not persisted, so every
// anonymous run gets a fresh one rather than "registering" anonymity.
func resolveHandle(flagValue string) (string, error) {
	h := strings.TrimSpace(flagValue)
	if h != "" {
		if !strings.HasPrefix(h, "@") {
			h = "@" + h
		}
		if err := config.Save(config.Config{Handle: h}); err != nil {
			fmt.Fprintln(os.Stderr, "warning: could not save handle for next time:", err)
		}
		return h, nil
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "warning: could not read saved handle:", err)
	} else if cfg.Handle != "" {
		return cfg.Handle, nil
	}

	suffix, err := randomID()
	if err != nil {
		return "", fmt.Errorf("no handle registered and could not generate an anonymous one: %w", err)
	}
	anon := "@anon-" + suffix[:6]
	fmt.Fprintf(os.Stderr, "no handle registered -- joining anonymously as %s\n", anon)
	fmt.Fprintln(os.Stderr, "hint: pass --handle=@you once to register a permanent handle for next time")
	return anon, nil
}

func randomID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
