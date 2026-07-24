// Command tiru-emba is a zero-configuration, LAN-only terminal chat client:
// peer discovery over UDP multicast/broadcast, direct messaging over TCP,
// and a Bubble Tea shell tying them together. In relay mode (--server), it
// additionally shows an in-TUI onboarding flow (Welcome -> Login/Register,
// with a mandatory org-select step afterward) instead of a pre-TUI prompt.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/NotTesfamichael/tiru-emba/internal/config"
	"github.com/NotTesfamichael/tiru-emba/internal/discovery"
	"github.com/NotTesfamichael/tiru-emba/internal/filedrop"
	"github.com/NotTesfamichael/tiru-emba/internal/network"
	"github.com/NotTesfamichael/tiru-emba/internal/ui"
	"github.com/NotTesfamichael/tiru-emba/internal/ui/onboarding"
)

func main() {
	handle := flag.String("handle", "", `your display handle, e.g. "@alex" (registers it for next time; omit to reuse a saved handle, or run anonymously if none is saved)`)
	tcpPort := flag.Int("port", 7777, "TCP port to listen on for direct messages")
	serverAddr := flag.String("server", "", "relay server address (host:port) for cross-network chat -- omit to stay LAN-only")
	lanEnabled := flag.Bool("lan", true, "enable LAN discovery/chat; --lan=false runs relay-only, e.g. to avoid a UDP port conflict with another local instance")
	backgroundNotification := flag.Bool("background-notification", false, "show OS desktop notifications for incoming messages/alerts (persisted for next time; omit to reuse the saved setting)")
	flag.Parse()

	notifyEnabled := resolveBackgroundNotification(*backgroundNotification)

	if !*lanEnabled && *serverAddr == "" {
		fmt.Fprintln(os.Stderr, "error: --lan=false with no --server would leave nothing to talk to")
		os.Exit(1)
	}

	h, err := resolveHandle(*handle)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
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

	chatArgs := ui.ChatArgs{
		Ctx: ctx, SelfID: selfID,
		PeerC: peerC, MsgC: msgC, OfferC: fileOfferC, ResultC: fileResultC, InviteC: gameInviteC,
		NotifyEnabled: notifyEnabled,
	}

	var app tea.Model
	if *serverAddr == "" {
		writeNetworkStatus(*lanEnabled, false, "")
		chat := ui.New(ctx, selfID, h, peerC, msgC, fileOfferC, fileResultC, gameInviteC, nil, notifyEnabled, nil, "")
		app = ui.NewApp(chat)
	} else {
		writeNetworkStatus(*lanEnabled, false, *serverAddr)
		onboard := onboarding.New(*serverAddr, h, resolveSavedToken(*serverAddr))
		app = ui.NewAppWithOnboarding(onboard, chatArgs)
	}

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

// resolveSavedToken returns a previously-persisted relay session token,
// but only if it was issued for this same server address and hasn't
// expired -- a token for a different --server (or a stale one) is
// meaningless to try resuming, so onboarding just falls back to a normal
// login in that case instead of failing confusingly against the server.
func resolveSavedToken(serverAddr string) string {
	cfg, err := config.Load()
	if err != nil || cfg.ServerURL != serverAddr || cfg.SessionToken == "" {
		return ""
	}
	if !cfg.SessionExpiresAt.After(time.Now()) {
		return ""
	}
	return cfg.SessionToken
}

// resolveHandle implements a lightweight "registration" flow: an explicit
// --handle registers itself for next time (saved to config), no flag reuses
// a previously-registered handle silently, and with neither, a random
// anonymous handle is generated -- and deliberately not persisted, so every
// anonymous run gets a fresh one rather than "registering" anonymity. This
// is always the LAN discovery identity; in relay mode it also pre-fills
// (editable) the onboarding Login/Register handle field, so a normal run
// keeps a single consistent identity across both without forcing one.
func resolveHandle(flagValue string) (string, error) {
	h := strings.TrimSpace(flagValue)
	if h != "" {
		if !strings.HasPrefix(h, "@") {
			h = "@" + h
		}
		if err := config.Update(func(c *config.Config) { c.Handle = h }); err != nil {
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

// resolveBackgroundNotification mirrors resolveHandle's "explicit flag wins
// and is persisted; otherwise reuse the saved setting" pattern, using
// flag.Visit to tell an explicitly-passed "--background-notification=false"
// apart from the flag simply not being passed at all (flag.Bool's own
// default is indistinguishable from an explicit false otherwise).
func resolveBackgroundNotification(flagValue bool) bool {
	explicit := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "background-notification" {
			explicit = true
		}
	})
	if explicit {
		if err := config.Update(func(c *config.Config) { c.BackgroundNotification = flagValue }); err != nil {
			fmt.Fprintln(os.Stderr, "warning: could not save notification setting for next time:", err)
		}
		return flagValue
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "warning: could not read saved notification setting:", err)
		return false
	}
	return cfg.BackgroundNotification
}

// writeNetworkStatus persists a best-effort snapshot of this run's network
// state to config.json (lan_status/wlan_status/server_url) -- purely
// informational, read by external tooling/inspection rather than by
// tiru-emba itself on a later run, so a failure here is just a warning.
// wlan_status starts "disconnected" here and is flipped to "connected"
// from within the onboarding flow (internal/ui) once relay auth actually
// succeeds, since that isn't known synchronously anymore.
func writeNetworkStatus(lanEnabled, relayConnected bool, serverAddr string) {
	lanStatus := "disconnected"
	if lanEnabled {
		lanStatus = "connected"
	}
	wlanStatus := "disconnected"
	if relayConnected {
		wlanStatus = "connected"
	}
	err := config.Update(func(c *config.Config) {
		c.LANStatus = lanStatus
		c.WLANStatus = wlanStatus
		if serverAddr != "" {
			c.ServerURL = serverAddr
		}
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "warning: could not save network status:", err)
	}
}

func randomID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
