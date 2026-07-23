package ui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/NotTesfamichael/tiru-emba/internal/discovery"
	"github.com/NotTesfamichael/tiru-emba/internal/filedrop"
	"github.com/NotTesfamichael/tiru-emba/internal/network"
	"github.com/NotTesfamichael/tiru-emba/internal/notify"
	"github.com/NotTesfamichael/tiru-emba/internal/peer"
	"github.com/NotTesfamichael/tiru-emba/internal/store"
)

const (
	pruneInterval  = 1 * time.Second
	maxSuggestions = 6
)

// peerSeenMsg wraps a discovery.PeerSeen so it can travel through Bubble
// Tea's Msg pipeline.
type peerSeenMsg discovery.PeerSeen

// incomingMsgMsg wraps a network.Received direct message.
type incomingMsgMsg network.Received

// sendResultMsg reports the outcome of an async, in-flight Send.
type sendResultMsg struct {
	target string
	err    error
}

// fileOfferMsg wraps an incoming network.FileOffer.
type fileOfferMsg network.FileOffer

// fileResultMsg wraps the eventual outcome of an accepted incoming transfer.
type fileResultMsg network.FileResult

// fileSendResultMsg reports the outcome of an async, in-flight SendFile.
type fileSendResultMsg struct {
	target   string
	fileName string
	accepted bool
	reason   string
	err      error
}

// gameInviteMsg wraps an incoming network.GameInvite.
type gameInviteMsg network.GameInvite

// gameInviteAcceptedMsg reports the outcome of accepting an incoming
// invite. On success, the hosting router (App) intercepts this before it
// ever reaches Model.Update, to switch to the game screen; Model only ever
// sees the error case, to report it as a system note.
type gameInviteAcceptedMsg struct {
	invite  network.GameInvite
	session *network.GameSession
	err     error
}

// startLudoMsg signals that a local Ludo match should begin. Unlike a
// tictactoe challenge, there's no network round-trip to wait on, so
// handlePlayCommand requests the switch immediately, in the same keypress
// that typed "/play ludo" -- App intercepts this the same way it intercepts
// gameChallengeResultMsg/gameInviteAcceptedMsg to switch screens.
type startLudoMsg struct{ numAI int }

// gameChallengeResultMsg reports the outcome of an outgoing /play challenge.
// Same split as gameInviteAcceptedMsg: App handles the accepted case (screen
// switch), Model handles everything else (denied, timed out, failed to
// connect).
type gameChallengeResultMsg struct {
	opponent string
	session  *network.GameSession
	accepted bool
	reason   string
	err      error
}

// ludoLobby tracks an in-flight, host-side "/play ludo @a @b ..." multi-
// invite round: each target's SendGameInvite runs concurrently, and the
// lobby is only ready once every target has responded -- accepted,
// declined, or failed. All-or-nothing: if any target doesn't accept, the
// whole match is aborted and any already-accepted sessions are closed,
// rather than starting a partially-filled board.
type ludoLobby struct {
	targets    []string
	sessions   []*network.GameSession // parallel to targets; nil until that target responds
	resolved   []bool                 // parallel to targets
	failReason string
}

// ludoChallengeResultMsg reports one target's response within a
// multi-guest Ludo lobby, tagged by index into the lobby's target list so
// results arriving out of order still land on the right slot.
type ludoChallengeResultMsg struct {
	idx      int
	opponent string
	session  *network.GameSession
	accepted bool
	reason   string
	err      error
}

// startNetworkedLudoMsg signals that a Ludo lobby is fully ready: every
// invited guest accepted, and their sessions (parallel to guestHandles, in
// seat order Green/Yellow/Blue) are ready to hand off to ludo.NewHost. App
// intercepts this to switch screens, the same way it does startLudoMsg for
// the local-only mode.
type startNetworkedLudoMsg struct {
	guestHandles []string
	sessions     []*network.GameSession
}

// pruneTickMsg fires periodically so the peer list drops teammates whose
// heartbeat has gone stale (e.g. they closed the app or left the network).
type pruneTickMsg time.Time

type Model struct {
	ctx    context.Context
	selfID string
	handle string

	peers   *peer.Registry
	peerC   <-chan discovery.PeerSeen
	msgC    <-chan network.Received
	offerC  <-chan network.FileOffer
	resultC <-chan network.FileResult
	inviteC <-chan network.GameInvite

	// fileOfferQueue holds pending incoming file-transfer requests; index 0
	// is the one currently shown to the user, and while it's non-empty,
	// normal input is suspended in favor of an accept/deny prompt.
	fileOfferQueue []network.FileOffer

	// gameInviteQueue mirrors fileOfferQueue for incoming game challenges.
	// Checked after fileOfferQueue so the two modal prompts never collide.
	gameInviteQueue []network.GameInvite

	// ludoLobby tracks an outstanding host-side "/play ludo @a @b ..."
	// multi-invite round; nil whenever no such invite is in flight.
	ludoLobby *ludoLobby

	hist    *store.Store
	history []store.Entry

	// filterPeer, when non-empty, is a lowercased handle (no "@") and the
	// chat pane shows only entries tagged with that peer -- effectively
	// "only this color", since color is derived deterministically from
	// handle.
	filterPeer string

	// lastDMHandle is whoever we most recently exchanged a direct message
	// with (sent to or received from), so pressing Up on an empty input
	// can prefill "@handle " without retyping it.
	lastDMHandle string

	input          textinput.Model
	suggestions    []string
	cmdSuggestions []commandSpec
	suggestIdx     int

	width, height int
}

func New(ctx context.Context, selfID, handle string, peerC <-chan discovery.PeerSeen, msgC <-chan network.Received, offerC <-chan network.FileOffer, resultC <-chan network.FileResult, inviteC <-chan network.GameInvite) Model {
	ti := textinput.New()
	ti.Placeholder = "@handle your message, or just type to broadcast..."
	ti.Focus()
	ti.CharLimit = 500
	ti.Prompt = "> "

	hist, saved, err := store.Open(handle)

	history := make([]store.Entry, 0, len(saved)+2)
	history = append(history, saved...)
	history = append(history, store.Entry{
		Kind: store.KindSystem,
		Body: fmt.Sprintf("welcome, %s. discovering teammates on the LAN...", handle),
		At:   time.Now(),
	})
	if err != nil {
		history = append(history, store.Entry{
			Kind: store.KindSystem,
			Body: fmt.Sprintf("history won't be saved this run: %v", err),
			At:   time.Now(),
		})
	}

	return Model{
		ctx:     ctx,
		selfID:  selfID,
		handle:  handle,
		peers:   peer.NewRegistry(),
		peerC:   peerC,
		msgC:    msgC,
		offerC:  offerC,
		resultC: resultC,
		inviteC: inviteC,
		hist:    hist,
		history: history,
		input:   ti,
	}
}

// Close flushes/closes the history file. Call once after the Bubble Tea
// program exits.
func (m Model) Close() {
	m.hist.Close()
}

// Handle returns this client's own handle, e.g. for a hosting router that
// needs it to construct a game screen.
func (m Model) Handle() string {
	return m.handle
}

// WithSystemNote appends a session-only note to the chat log -- meant for a
// hosting router to call when handing control back to Model, e.g. reporting
// how a game just ended.
func (m Model) WithSystemNote(text string) Model {
	m.history = recordEntry(m.history, m.hist, systemNote(text))
	return m
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		waitForPeer(m.peerC),
		waitForMessage(m.msgC),
		waitForFileOffer(m.offerC),
		waitForFileResult(m.resultC),
		waitForGameInvite(m.inviteC),
		tea.Tick(pruneInterval, func(t time.Time) tea.Msg { return pruneTickMsg(t) }),
		textinput.Blink,
	)
}

// waitForPeer blocks on the discovery channel and turns the next sighting
// into a tea.Msg. Update() re-issues this Cmd after each message so the
// pump keeps running for the lifetime of the program.
func waitForPeer(ch <-chan discovery.PeerSeen) tea.Cmd {
	return func() tea.Msg {
		p, ok := <-ch
		if !ok {
			return nil
		}
		return peerSeenMsg(p)
	}
}

// waitForMessage mirrors waitForPeer for incoming direct messages.
func waitForMessage(ch <-chan network.Received) tea.Cmd {
	return func() tea.Msg {
		r, ok := <-ch
		if !ok {
			return nil
		}
		return incomingMsgMsg(r)
	}
}

// sendMessage delivers a direct message in the background (network.Send
// blocks on a TCP dial+write) so Update never stalls the UI on a slow or
// unreachable peer.
func sendMessage(addr, from, target, body string) tea.Cmd {
	return func() tea.Msg {
		err := network.Send(addr, network.Message{From: from, Body: body})
		return sendResultMsg{target: target, err: err}
	}
}

// waitForFileOffer mirrors waitForPeer for incoming file-transfer requests.
func waitForFileOffer(ch <-chan network.FileOffer) tea.Cmd {
	return func() tea.Msg {
		o, ok := <-ch
		if !ok {
			return nil
		}
		return fileOfferMsg(o)
	}
}

// waitForFileResult mirrors waitForPeer for the eventual outcome of an
// accepted incoming transfer.
func waitForFileResult(ch <-chan network.FileResult) tea.Cmd {
	return func() tea.Msg {
		r, ok := <-ch
		if !ok {
			return nil
		}
		return fileResultMsg(r)
	}
}

// sendFile offers path to whoever's listening at addr and blocks (in the
// background, via network.SendFile) until they accept/deny or it times out.
func sendFile(addr, from, target, path string) tea.Cmd {
	return func() tea.Msg {
		accepted, reason, err := network.SendFile(addr, from, path)
		return fileSendResultMsg{target: target, fileName: filepath.Base(path), accepted: accepted, reason: reason, err: err}
	}
}

// waitForGameInvite mirrors waitForPeer for incoming game challenges.
func waitForGameInvite(ch <-chan network.GameInvite) tea.Cmd {
	return func() tea.Msg {
		i, ok := <-ch
		if !ok {
			return nil
		}
		return gameInviteMsg(i)
	}
}

// acceptGameInvite accepts invite in the background (the write itself is
// quick, but it's kept off Update like every other network call here for
// consistency and so a slow/dead peer can never stall the UI).
func acceptGameInvite(invite network.GameInvite) tea.Cmd {
	return func() tea.Msg {
		session, err := invite.Accept()
		return gameInviteAcceptedMsg{invite: invite, session: session, err: err}
	}
}

// challengeToGame dials addr and issues a game challenge, blocking in the
// background until the opponent accepts/denies or it times out.
func challengeToGame(ctx context.Context, addr, from, opponent, gameType string) tea.Cmd {
	return func() tea.Msg {
		session, reason, err := network.SendGameInvite(ctx, addr, from, gameType)
		return gameChallengeResultMsg{opponent: opponent, session: session, accepted: session != nil, reason: reason, err: err}
	}
}

// challengeToLudoLobby is challengeToGame's counterpart for a multi-guest
// Ludo lobby: same dial-and-wait, but tagged with idx so the result lands
// on the right slot in ludoLobby regardless of response order.
func challengeToLudoLobby(ctx context.Context, addr, from, opponent string, idx int) tea.Cmd {
	return func() tea.Msg {
		session, reason, err := network.SendGameInvite(ctx, addr, from, "ludo")
		return ludoChallengeResultMsg{idx: idx, opponent: opponent, session: session, accepted: session != nil, reason: reason, err: err}
	}
}

// recordEntry appends e to the in-memory history and (best-effort) persists
// it to disk. KindSystem entries are session-only and never written to disk
// (see store.Store.Append).
func recordEntry(history []store.Entry, hist *store.Store, e store.Entry) []store.Entry {
	history = append(history, e)
	_ = hist.Append(e)
	return history
}

func systemNote(body string) store.Entry {
	return store.Entry{Kind: store.KindSystem, Body: body, At: time.Now()}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case tea.KeyMsg:
		if len(m.fileOfferQueue) > 0 {
			return m.handleOfferKey(msg)
		}
		if len(m.gameInviteQueue) > 0 {
			return m.handleGameInviteKey(msg)
		}
		switch msg.String() {
		case "ctrl+c", "esc":
			return m, tea.Quit
		case "enter":
			return m.submitInput()
		case "tab":
			return m.acceptSuggestion()
		case "up":
			if len(m.cmdSuggestions) > 0 {
				m.suggestIdx = (m.suggestIdx - 1 + len(m.cmdSuggestions)) % len(m.cmdSuggestions)
				return m, nil
			}
			if len(m.suggestions) > 0 {
				m.suggestIdx = (m.suggestIdx - 1 + len(m.suggestions)) % len(m.suggestions)
				return m, nil
			}
			if m.input.Value() == "" && m.lastDMHandle != "" {
				m.input.SetValue(m.lastDMHandle + " ")
				m.input.CursorEnd()
				return m, nil
			}
		case "down":
			if len(m.cmdSuggestions) > 0 {
				m.suggestIdx = (m.suggestIdx + 1) % len(m.cmdSuggestions)
				return m, nil
			}
			if len(m.suggestions) > 0 {
				m.suggestIdx = (m.suggestIdx + 1) % len(m.suggestions)
				return m, nil
			}
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		m.updateSuggestions()
		return m, cmd

	case peerSeenMsg:
		info := peer.Info{
			ID:       msg.ID,
			Handle:   msg.Handle,
			Addr:     msg.Addr.String(),
			TCPPort:  msg.TCPPort,
			LastSeen: msg.SeenAt,
		}
		firstSighting := true
		for _, p := range m.peers.List() {
			if p.ID == msg.ID {
				firstSighting = false
				break
			}
		}
		m.peers.Upsert(info)
		if firstSighting {
			m.history = recordEntry(m.history, m.hist, systemNote(fmt.Sprintf("%s %s joined", avatarForHandle(msg.Handle), msg.Handle)))
		}
		return m, waitForPeer(m.peerC)

	case incomingMsgMsg:
		m.history = recordEntry(m.history, m.hist, store.Entry{
			Kind: store.KindDirectRecv,
			Peer: msg.From,
			Body: msg.Body,
			At:   msg.At,
		})
		m.lastDMHandle = msg.From
		notify.Alert(fmt.Sprintf("Message from %s", msg.From), msg.Body)
		return m, waitForMessage(m.msgC)

	case sendResultMsg:
		if msg.err != nil {
			m.history = recordEntry(m.history, m.hist, systemNote(fmt.Sprintf("failed to deliver to %s: %v", msg.target, msg.err)))
		}
		return m, nil

	case fileOfferMsg:
		m.fileOfferQueue = append(m.fileOfferQueue, network.FileOffer(msg))
		return m, waitForFileOffer(m.offerC)

	case fileResultMsg:
		if msg.Err != nil {
			m.history = recordEntry(m.history, m.hist, systemNote(fmt.Sprintf("failed to receive %s from %s: %v", msg.FileName, msg.From, msg.Err)))
		} else {
			m.history = recordEntry(m.history, m.hist, systemNote(fmt.Sprintf("received %s from %s -> saved to %s", msg.FileName, msg.From, msg.SavedPath)))
			notify.Alert(fmt.Sprintf("File from %s", msg.From), fmt.Sprintf("%s saved to %s", msg.FileName, filedrop.DirName))
		}
		return m, waitForFileResult(m.resultC)

	case fileSendResultMsg:
		switch {
		case msg.err != nil:
			m.history = recordEntry(m.history, m.hist, systemNote(fmt.Sprintf("failed to send %s to %s: %v", msg.fileName, msg.target, msg.err)))
		case !msg.accepted:
			reason := msg.reason
			if reason == "" {
				reason = "declined"
			}
			m.history = recordEntry(m.history, m.hist, systemNote(fmt.Sprintf("%s did not accept %s: %s", msg.target, msg.fileName, reason)))
		default:
			m.history = recordEntry(m.history, m.hist, systemNote(fmt.Sprintf("%s delivered to %s", msg.fileName, msg.target)))
		}
		return m, nil

	case gameInviteMsg:
		m.gameInviteQueue = append(m.gameInviteQueue, network.GameInvite(msg))
		return m, waitForGameInvite(m.inviteC)

	case gameInviteAcceptedMsg:
		// The success path is handled by the hosting router (App) before
		// this ever reaches here; only the failure path shows up.
		if msg.err != nil {
			m.history = recordEntry(m.history, m.hist, systemNote(fmt.Sprintf("couldn't start the game with %s: %v", msg.invite.From, msg.err)))
		}
		return m, nil

	case gameChallengeResultMsg:
		// Same split as above: App handles accepted, Model handles the rest.
		if msg.err != nil {
			m.history = recordEntry(m.history, m.hist, systemNote(fmt.Sprintf("failed to challenge %s: %v", msg.opponent, msg.err)))
		} else if !msg.accepted {
			reason := msg.reason
			if reason == "" {
				reason = "declined"
			}
			m.history = recordEntry(m.history, m.hist, systemNote(fmt.Sprintf("%s did not accept your tic-tac-toe challenge: %s", msg.opponent, reason)))
		}
		return m, nil

	case ludoChallengeResultMsg:
		return m.handleLudoChallengeResult(msg)

	case pruneTickMsg:
		m.peers.Prune(time.Time(msg), discovery.PeerTTL)
		return m, tea.Tick(pruneInterval, func(t time.Time) tea.Msg { return pruneTickMsg(t) })
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// submitInput handles the Enter key: "/command ..." runs a command,
// "@handle [@handle ...] body" resolves each handle against the peer
// registry and delivers body to each over TCP, anything else broadcasts to
// every currently online peer.
func (m Model) submitInput() (tea.Model, tea.Cmd) {
	text := strings.TrimSpace(m.input.Value())
	if text == "" {
		return m, nil
	}
	m.input.Reset()
	m.suggestions = nil
	m.cmdSuggestions = nil

	switch {
	case strings.HasPrefix(text, "/"):
		return m.handleCommand(text)
	case strings.HasPrefix(text, "@"):
		return m.sendDirect(text)
	default:
		return m.sendBroadcast(text)
	}
}

// sendBroadcast delivers text to every currently online peer. There's no
// shared broadcast channel on the wire -- this is really just the same
// direct-message send fanned out to everyone the registry currently knows
// about -- but from the user's side it reads as "just type to broadcast",
// matching the input placeholder.
func (m Model) sendBroadcast(text string) (tea.Model, tea.Cmd) {
	m.history = recordEntry(m.history, m.hist, store.Entry{Kind: store.KindSelfNote, Body: text, At: time.Now()})

	peers := m.peers.List()
	if len(peers) == 0 {
		return m, nil
	}
	cmds := make([]tea.Cmd, 0, len(peers))
	for _, p := range peers {
		addr := fmt.Sprintf("%s:%d", p.Addr, p.TCPPort)
		cmds = append(cmds, sendMessage(addr, m.handle, p.Handle, text))
	}
	return m, tea.Batch(cmds...)
}

// sendDirect parses leading "@handle" tokens (however many appear before the
// first non-@ word) as the recipient list, and delivers the remaining text
// to each of them independently -- one history entry per recipient, so
// filtering/coloring stays a clean one-entry-one-peer relationship. If the
// remaining text is actually a path to an existing file (dragging a file
// onto most terminals inserts its absolute path as text), it's offered as a
// file transfer instead of sent as a chat message.
func (m Model) sendDirect(text string) (tea.Model, tea.Cmd) {
	fields := strings.Fields(text)
	i := 0
	var targets []string
	for i < len(fields) && strings.HasPrefix(fields[i], "@") {
		targets = append(targets, fields[i])
		i++
	}
	body := strings.Join(fields[i:], " ")

	if path, ok := filePathIn(body); ok {
		return m.sendFileToTargets(targets, path)
	}

	var cmds []tea.Cmd
	for _, target := range targets {
		info, ok := m.peers.Lookup(target)
		if !ok {
			m.history = recordEntry(m.history, m.hist, systemNote(fmt.Sprintf("no such peer online: %s", target)))
			continue
		}
		m.history = recordEntry(m.history, m.hist, store.Entry{
			Kind: store.KindDirectSent,
			Peer: info.Handle,
			Body: body,
			At:   time.Now(),
		})
		m.lastDMHandle = info.Handle
		addr := fmt.Sprintf("%s:%d", info.Addr, info.TCPPort)
		cmds = append(cmds, sendMessage(addr, m.handle, info.Handle, body))
	}
	return m, tea.Batch(cmds...)
}

// sendFileToTargets offers the file at path to every target handle.
func (m Model) sendFileToTargets(targets []string, path string) (tea.Model, tea.Cmd) {
	info, err := os.Stat(path)
	if err != nil {
		m.history = recordEntry(m.history, m.hist, systemNote(fmt.Sprintf("can't read file %s: %v", path, err)))
		return m, nil
	}
	if info.Size() > network.MaxFileSize {
		m.history = recordEntry(m.history, m.hist, systemNote(fmt.Sprintf(
			"%s (%s) exceeds the %s limit", filepath.Base(path), network.HumanSize(info.Size()), network.HumanSize(network.MaxFileSize))))
		return m, nil
	}

	var cmds []tea.Cmd
	for _, target := range targets {
		peerInfo, ok := m.peers.Lookup(target)
		if !ok {
			m.history = recordEntry(m.history, m.hist, systemNote(fmt.Sprintf("no such peer online: %s", target)))
			continue
		}
		m.history = recordEntry(m.history, m.hist, systemNote(fmt.Sprintf(
			"offering %s (%s) to %s, waiting for them to accept...", filepath.Base(path), network.HumanSize(info.Size()), peerInfo.Handle)))
		addr := fmt.Sprintf("%s:%d", peerInfo.Addr, peerInfo.TCPPort)
		cmds = append(cmds, sendFile(addr, m.handle, peerInfo.Handle, path))
	}
	return m, tea.Batch(cmds...)
}

// filePathIn recognizes a message body that's actually a dropped file path.
// Dragging a file onto a terminal inserts its absolute path as literal text,
// and terminals disagree on how they escape spaces/special characters in
// that path: some wrap the whole thing in matching quotes, others (e.g.
// Ghostty) backslash-escape each special character shell-style instead, so
// "My Photo.png" arrives as `My\ Photo.png`. Both are handled here.
func filePathIn(body string) (string, bool) {
	body = strings.TrimSpace(body)
	if len(body) >= 2 {
		if (body[0] == '\'' && body[len(body)-1] == '\'') || (body[0] == '"' && body[len(body)-1] == '"') {
			body = body[1 : len(body)-1]
		} else {
			body = unescapeShellPath(body)
		}
	}
	if body == "" {
		return "", false
	}
	info, err := os.Stat(body)
	if err != nil || !info.Mode().IsRegular() {
		return "", false
	}
	return body, true
}

// unescapeShellPath undoes shell-style backslash escaping: a backslash
// followed by any character is replaced with just that character.
func unescapeShellPath(s string) string {
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

// handleOfferKey is the modal key handler while a file-transfer prompt is
// showing: every other key is ignored until the user explicitly decides, to
// avoid an accidental accept/deny.
func (m Model) handleOfferKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		return m, tea.Quit
	}
	offer := m.fileOfferQueue[0]
	switch strings.ToLower(msg.String()) {
	case "y", "enter":
		offer.Respond(true)
		m.fileOfferQueue = m.fileOfferQueue[1:]
		m.history = recordEntry(m.history, m.hist, systemNote(fmt.Sprintf("accepted %s from %s -- receiving...", offer.FileName, offer.From)))
	case "n", "esc":
		offer.Respond(false)
		m.fileOfferQueue = m.fileOfferQueue[1:]
		m.history = recordEntry(m.history, m.hist, systemNote(fmt.Sprintf("declined %s from %s", offer.FileName, offer.From)))
	}
	return m, nil
}

// handleGameInviteKey is the modal key handler while a game challenge
// prompt is showing: every other key is ignored until the user explicitly
// decides, same as handleOfferKey for file transfers.
func (m Model) handleGameInviteKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		return m, tea.Quit
	}
	invite := m.gameInviteQueue[0]
	switch strings.ToLower(msg.String()) {
	case "y", "enter":
		m.gameInviteQueue = m.gameInviteQueue[1:]
		return m, acceptGameInvite(invite)
	case "n", "esc":
		invite.Deny()
		m.gameInviteQueue = m.gameInviteQueue[1:]
		m.history = recordEntry(m.history, m.hist, systemNote(fmt.Sprintf("declined %s's tic-tac-toe challenge", invite.From)))
	}
	return m, nil
}

// commandSpec documents one slash command for the "/" help/autocomplete
// list and for "/help" itself, so both stay in sync with a single source
// of truth instead of two hand-maintained lists drifting apart.
type commandSpec struct {
	usage string
	desc  string
}

var commandSpecs = []commandSpec{
	{"/play tictactoe @handle", "challenge a peer to Tic-Tac-Toe"},
	{"/play ludo", "start a local Ludo match (you + AI opponents)"},
	{"/play ludo @handle", "invite up to 3 peers to a networked Ludo match"},
	{"/filter @handle", "show only your conversation with that peer"},
	{"/clear", "clear the active filter"},
	{"/help", "list every slash command"},
}

// handleCommand implements the slash-command set: /filter @handle (hide
// everything except that conversation), /clear (show everything), /play
// (challenge a peer or start a local game), and /help (list all of these).
func (m Model) handleCommand(text string) (tea.Model, tea.Cmd) {
	fields := strings.Fields(text)
	switch strings.ToLower(fields[0]) {
	case "/play":
		return m.handlePlayCommand(fields)
	case "/filter":
		if len(fields) < 2 {
			m.history = recordEntry(m.history, m.hist, systemNote("usage: /filter @handle"))
			return m, nil
		}
		target := fields[1]
		if _, ok := m.peers.Lookup(target); !ok {
			m.history = recordEntry(m.history, m.hist, systemNote(fmt.Sprintf("no such peer online: %s", target)))
			return m, nil
		}
		m.filterPeer = strings.ToLower(strings.TrimPrefix(target, "@"))
		m.history = recordEntry(m.history, m.hist, systemNote(fmt.Sprintf("filtering: only showing %s", target)))
	case "/clear", "/unfilter":
		m.filterPeer = ""
		m.history = recordEntry(m.history, m.hist, systemNote("filter cleared"))
	case "/help":
		m.history = recordEntry(m.history, m.hist, systemNote("commands:"))
		for _, c := range commandSpecs {
			m.history = recordEntry(m.history, m.hist, systemNote(fmt.Sprintf("  %-24s %s", c.usage, c.desc)))
		}
	default:
		m.history = recordEntry(m.history, m.hist, systemNote(fmt.Sprintf("unknown command: %s (try /help)", fields[0])))
	}
	return m, nil
}

// handlePlayCommand implements "/play tictactoe @handle" (challenge a peer
// over the network) and "/play ludo [2-4]" (start a local match against AI
// opponents -- no network round-trip, so it switches screens immediately).
func (m Model) handlePlayCommand(fields []string) (tea.Model, tea.Cmd) {
	if len(fields) < 2 {
		m.history = recordEntry(m.history, m.hist, systemNote("usage: /play tictactoe @handle | /play ludo [2-4]"))
		return m, nil
	}

	switch strings.ToLower(fields[1]) {
	case "tictactoe":
		if len(fields) != 3 {
			m.history = recordEntry(m.history, m.hist, systemNote("usage: /play tictactoe @handle"))
			return m, nil
		}
		target := fields[2]
		info, ok := m.peers.Lookup(target)
		if !ok {
			m.history = recordEntry(m.history, m.hist, systemNote(fmt.Sprintf("no such peer online: %s", target)))
			return m, nil
		}
		m.history = recordEntry(m.history, m.hist, systemNote(fmt.Sprintf("challenging %s to tic-tac-toe, waiting for them to accept...", info.Handle)))
		addr := fmt.Sprintf("%s:%d", info.Addr, info.TCPPort)
		return m, challengeToGame(m.ctx, addr, m.handle, info.Handle, "tictactoe")

	case "ludo":
		args := fields[2:]
		if len(args) > 0 && strings.HasPrefix(args[0], "@") {
			return m.startLudoLobby(args)
		}

		numPlayers := 4
		if len(args) >= 1 {
			n, err := strconv.Atoi(args[0])
			if err != nil || n < 2 || n > 4 {
				m.history = recordEntry(m.history, m.hist, systemNote("usage: /play ludo [2-4] | /play ludo @handle [@handle ...] (up to 3)"))
				return m, nil
			}
			numPlayers = n
		}
		numAI := numPlayers - 1
		m.history = recordEntry(m.history, m.hist, systemNote(fmt.Sprintf("starting a local Ludo match: you + %d AI opponent(s)", numAI)))
		return m, func() tea.Msg { return startLudoMsg{numAI: numAI} }

	default:
		m.history = recordEntry(m.history, m.hist, systemNote(fmt.Sprintf("unknown game: %s (try tictactoe or ludo)", fields[1])))
		return m, nil
	}
}

// startLudoLobby issues a networked Ludo invite to 1-3 online peers
// concurrently. It's all-or-nothing: the match only starts once every
// target has accepted (see handleLudoChallengeResult); if the lobby is
// still waiting on responses, a new /play ludo @... is rejected rather
// than starting a second, overlapping lobby.
func (m Model) startLudoLobby(rawTargets []string) (tea.Model, tea.Cmd) {
	if m.ludoLobby != nil {
		m.history = recordEntry(m.history, m.hist, systemNote("a Ludo invite is already pending -- wait for it to resolve first"))
		return m, nil
	}
	if len(rawTargets) > 3 {
		m.history = recordEntry(m.history, m.hist, systemNote("usage: /play ludo @handle [@handle ...] (up to 3)"))
		return m, nil
	}

	targets := make([]string, len(rawTargets))
	addrs := make([]string, len(rawTargets))
	for i, t := range rawTargets {
		info, ok := m.peers.Lookup(t)
		if !ok {
			m.history = recordEntry(m.history, m.hist, systemNote(fmt.Sprintf("no such peer online: %s", t)))
			return m, nil
		}
		targets[i] = info.Handle
		addrs[i] = fmt.Sprintf("%s:%d", info.Addr, info.TCPPort)
	}

	cmds := make([]tea.Cmd, len(targets))
	for i := range targets {
		cmds[i] = challengeToLudoLobby(m.ctx, addrs[i], m.handle, targets[i], i)
	}

	m.ludoLobby = &ludoLobby{
		targets:  targets,
		sessions: make([]*network.GameSession, len(targets)),
		resolved: make([]bool, len(targets)),
	}
	m.history = recordEntry(m.history, m.hist, systemNote(fmt.Sprintf("inviting %s to Ludo, waiting for them to accept...", strings.Join(targets, ", "))))
	return m, tea.Batch(cmds...)
}

// handleLudoChallengeResult records one target's response and, once every
// target in the lobby has responded, either starts the match (everyone
// accepted) or cancels it (closing any sessions that did accept, so nobody
// is left waiting on a match that isn't going to start).
func (m Model) handleLudoChallengeResult(msg ludoChallengeResultMsg) (tea.Model, tea.Cmd) {
	lobby := m.ludoLobby
	if lobby == nil || msg.idx >= len(lobby.resolved) {
		return m, nil // a stale result from an already-resolved/abandoned lobby
	}

	lobby.resolved[msg.idx] = true
	lobby.sessions[msg.idx] = msg.session
	if !msg.accepted && lobby.failReason == "" {
		reason := msg.reason
		switch {
		case msg.err != nil:
			reason = msg.err.Error()
		case reason == "":
			reason = "declined"
		}
		lobby.failReason = fmt.Sprintf("%s: %s", msg.opponent, reason)
	}

	for _, resolved := range lobby.resolved {
		if !resolved {
			return m, nil // still waiting on the rest
		}
	}
	m.ludoLobby = nil

	if lobby.failReason != "" {
		for _, sess := range lobby.sessions {
			if sess != nil {
				_ = sess.Resign()
				_ = sess.Close()
			}
		}
		m.history = recordEntry(m.history, m.hist, systemNote(fmt.Sprintf("Ludo invite canceled (%s)", lobby.failReason)))
		return m, nil
	}

	m.history = recordEntry(m.history, m.hist, systemNote("everyone accepted -- starting the Ludo match"))
	return m, func() tea.Msg {
		return startNetworkedLudoMsg{guestHandles: lobby.targets, sessions: lobby.sessions}
	}
}

// updateSuggestions recomputes whichever autocomplete list applies to the
// input's current contents: matching slash commands while a "/word" is
// still being typed (no space yet), matching @handles while the last word
// starts with "@", or neither.
func (m *Model) updateSuggestions() {
	m.suggestions = nil
	m.cmdSuggestions = nil

	text := m.input.Value()
	if text == "" {
		return
	}

	if strings.HasPrefix(text, "/") && !strings.Contains(text, " ") {
		partial := strings.ToLower(text)
		var matches []commandSpec
		for _, c := range commandSpecs {
			if strings.HasPrefix(strings.ToLower(c.usage), partial) {
				matches = append(matches, c)
			}
		}
		m.cmdSuggestions = matches
		if m.suggestIdx >= len(matches) {
			m.suggestIdx = 0
		}
		return
	}

	if strings.HasSuffix(text, " ") {
		return
	}
	fields := strings.Fields(text)
	last := fields[len(fields)-1]
	if !strings.HasPrefix(last, "@") {
		return
	}

	partial := strings.ToLower(last)
	var matches []string
	for _, p := range m.peers.List() {
		if strings.HasPrefix(strings.ToLower(p.Handle), partial) {
			matches = append(matches, p.Handle)
		}
	}
	sort.Strings(matches)
	if len(matches) > maxSuggestions {
		matches = matches[:maxSuggestions]
	}
	m.suggestions = matches
	if m.suggestIdx >= len(matches) {
		m.suggestIdx = 0
	}
}

// acceptSuggestion fills in whichever suggestion is currently highlighted --
// a full command template (usage string, ready to have its placeholder
// edited) or an in-progress "@partial" handle. With nothing showing, Tab is
// just forwarded to the input as normal.
func (m Model) acceptSuggestion() (tea.Model, tea.Cmd) {
	if len(m.cmdSuggestions) > 0 {
		chosen := m.cmdSuggestions[m.suggestIdx]
		m.cmdSuggestions = nil
		m.suggestIdx = 0

		value := chosen.usage
		if strings.Contains(value, "@handle") {
			// Leave a bare "@" rather than the literal "@handle" placeholder,
			// with no trailing space, so it reads as an in-progress handle
			// and immediately triggers the real @handle autocomplete below --
			// picking who to actually play/filter with, not editing a
			// placeholder word by hand.
			m.input.SetValue(strings.Replace(value, "@handle", "@", 1))
			m.input.CursorEnd()
			m.updateSuggestions()
			return m, nil
		}
		m.input.SetValue(value + " ")
		m.input.CursorEnd()
		return m, nil
	}
	if len(m.suggestions) == 0 {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(tea.KeyMsg{Type: tea.KeyTab})
		return m, cmd
	}
	chosen := m.suggestions[m.suggestIdx]
	fields := strings.Fields(m.input.Value())
	if len(fields) == 0 {
		return m, nil
	}
	fields[len(fields)-1] = chosen
	m.input.SetValue(strings.Join(fields, " ") + " ")
	m.input.CursorEnd()
	m.suggestions = nil
	m.suggestIdx = 0
	return m, nil
}

func (m Model) View() string {
	if m.width == 0 {
		return "initializing..."
	}

	title := titleStyle.Render(fmt.Sprintf(" LAN Chat — %s ", m.handle))
	if m.filterPeer != "" {
		title += statusStyle.Render(fmt.Sprintf("  [filtered: @%s — /clear to reset]", m.filterPeer))
	}

	sidebarWidth := 24
	mainWidth := m.width - sidebarWidth - 6 // borders/padding fudge
	bodyHeight := m.height - 7              // title + input + borders

	sidebar := m.renderSidebar(sidebarWidth, bodyHeight)
	chat := m.renderChat(mainWidth, bodyHeight)

	body := lipgloss.JoinHorizontal(lipgloss.Top, sidebar, chat)
	input := inputStyle.Width(m.width - 4).Render(m.input.View())

	sections := []string{title, body}
	switch {
	case m.renderFileOfferPrompt() != "":
		sections = append(sections, m.renderFileOfferPrompt())
	case m.renderGameInvitePrompt() != "":
		sections = append(sections, m.renderGameInvitePrompt())
	case m.renderSuggestions() != "":
		sections = append(sections, m.renderSuggestions())
	}
	sections = append(sections, input)

	return appStyle.Render(lipgloss.JoinVertical(lipgloss.Left, sections...))
}

func (m Model) renderFileOfferPrompt() string {
	if len(m.fileOfferQueue) == 0 {
		return ""
	}
	o := m.fileOfferQueue[0]
	text := fmt.Sprintf("%s wants to send %s (%s) -- accept? [y]es  [n]o", o.From, o.FileName, network.HumanSize(o.FileSize))
	return lipgloss.NewStyle().Bold(true).Foreground(colorAccent).Render(text)
}

func (m Model) renderGameInvitePrompt() string {
	if len(m.gameInviteQueue) == 0 {
		return ""
	}
	i := m.gameInviteQueue[0]
	text := fmt.Sprintf("%s challenges you to %s -- accept? [y]es  [n]o", i.From, i.GameType)
	return lipgloss.NewStyle().Bold(true).Foreground(colorAccent).Render(text)
}

func (m Model) renderSuggestions() string {
	if len(m.cmdSuggestions) > 0 {
		lines := make([]string, len(m.cmdSuggestions))
		for i, c := range m.cmdSuggestions {
			line := fmt.Sprintf("%-24s %s", c.usage, c.desc)
			style := statusStyle
			if i == m.suggestIdx {
				style = lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Underline(true)
			}
			lines[i] = style.Render(line)
		}
		return strings.Join(lines, "\n")
	}
	if len(m.suggestions) == 0 {
		return ""
	}
	parts := make([]string, len(m.suggestions))
	for i, s := range m.suggestions {
		style := lipgloss.NewStyle().Foreground(colorForHandle(s))
		if i == m.suggestIdx {
			style = style.Bold(true).Underline(true)
		}
		parts[i] = style.Render(s)
	}
	return statusStyle.Render("→ ") + strings.Join(parts, "  ")
}

func (m Model) renderSidebar(width, height int) string {
	var b strings.Builder
	b.WriteString(sidebarHeaderStyle.Render(fmt.Sprintf("Online (%d)", m.peers.Len())))
	b.WriteString("\n\n")
	for _, p := range m.peers.List() {
		style := lipgloss.NewStyle().Foreground(colorForHandle(p.Handle))
		line := avatarForHandle(p.Handle) + " " + p.Handle
		if strings.EqualFold(strings.TrimPrefix(p.Handle, "@"), m.filterPeer) {
			line += " [filter]"
			style = style.Bold(true)
		}
		b.WriteString(style.Render(line))
		b.WriteString("\n")
	}
	if m.peers.Len() == 0 {
		b.WriteString(statusStyle.Render("searching..."))
	}
	return sidebarStyle.Width(width).Height(height).Render(b.String())
}

func (m Model) renderChat(width, height int) string {
	lines := make([]string, 0, len(m.history))
	for _, e := range m.history {
		if m.filterPeer != "" && !strings.EqualFold(strings.TrimPrefix(e.Peer, "@"), m.filterPeer) {
			continue
		}
		lines = append(lines, renderEntry(m.handle, e))
	}
	maxLines := height - 2
	if maxLines > 0 && len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return chatStyle.Width(width).Height(height).Render(strings.Join(lines, "\n"))
}

func renderEntry(selfHandle string, e store.Entry) string {
	switch e.Kind {
	case store.KindSelfNote:
		return fmt.Sprintf("%s: %s", selfStyle.Render(selfHandle), e.Body)
	case store.KindDirectSent:
		peerStyle := lipgloss.NewStyle().Foreground(colorForHandle(e.Peer))
		return fmt.Sprintf("%s %s: %s", selfStyle.Render(selfHandle+" ->"), peerStyle.Render(e.Peer), e.Body)
	case store.KindDirectRecv:
		peerStyle := lipgloss.NewStyle().Foreground(colorForHandle(e.Peer))
		return fmt.Sprintf("%s: %s", peerStyle.Render(e.Peer), e.Body)
	default: // store.KindSystem and anything unrecognized
		return statusStyle.Render(e.Body)
	}
}
