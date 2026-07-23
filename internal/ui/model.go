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
	"github.com/NotTesfamichael/tiru-emba/internal/relay"
	"github.com/NotTesfamichael/tiru-emba/internal/store"
)

// relayClient is what Model needs from a relay.Client -- narrowed to an
// interface so it can be tested against a fake instead of a real
// connection, the same pattern the game packages use for their own
// Session interfaces. nil (the interface itself, not just what it wraps --
// see New) when not connected to a relay server, i.e. LAN-only mode.
type relayClient interface {
	CreateOrg(name string) (relay.OrgSummary, error)
	ListOrgs() ([]relay.OrgSummary, error)
	InviteToOrg(orgID int64) (code string, expiresAt time.Time, err error)
	JoinOrg(code string) (relay.OrgSummary, error)
	SendRelay(to, body string) error
	Events() <-chan relay.Envelope
	Close() error
}

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

// relayEventMsg wraps an asynchronous push from the relay server --
// presence changes, or an incoming relayed message -- so it can travel
// through Bubble Tea's Msg pipeline.
type relayEventMsg relay.Envelope

// relaySendResultMsg reports a LOCAL write failure from an async SendRelay
// call. A successful send produces no message at all (fire-and-forget by
// protocol design -- see relay.Client); application-level failures like
// "not online" or "no shared org" arrive later as a relayEventMsg instead,
// same as any other unsolicited push.
type relaySendResultMsg struct {
	target string
	err    error
}

// orgActionResultMsg reports the outcome of an async /org request, tagged
// by which one it was so Update can react appropriately.
type orgActionResultMsg struct {
	kind    string // "create", "list", "invite", "join"
	org     relay.OrgSummary
	orgs    []relay.OrgSummary
	code    string
	expires time.Time
	err     error
}

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

	// relay is nil in LAN-only mode. orgMates mirrors relay presence
	// pushes: the set of org-mates currently online (keyed by their exact
	// handle as the server sent it), parallel to peers for LAN discovery.
	relay    relayClient
	orgMates map[string]bool

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

// New constructs the chat model. relayC is nil for LAN-only mode; when
// non-nil, the caller is expected to have already registered/logged in
// with it (see main.go's connectToRelay) -- New just starts consuming its
// Events. Accepting the concrete *relay.Client here (rather than the
// relayClient interface directly) and nil-checking before storing it
// avoids the classic Go "typed nil interface" trap: a nil *relay.Client
// stored directly into an interface-typed field would make that field
// compare != nil even though nothing is actually there.
func New(ctx context.Context, selfID, handle string, peerC <-chan discovery.PeerSeen, msgC <-chan network.Received, offerC <-chan network.FileOffer, resultC <-chan network.FileResult, inviteC <-chan network.GameInvite, relayC *relay.Client) Model {
	ti := textinput.New()
	ti.Placeholder = "@handle your message, or just type to broadcast..."
	ti.Focus()
	ti.CharLimit = 500
	ti.Prompt = "> "

	hist, saved, err := store.Open(handle)

	history := make([]store.Entry, 0, len(saved)+2)
	history = append(history, saved...)
	welcome := fmt.Sprintf("welcome, %s. discovering teammates on the LAN...", handle)
	if relayC != nil {
		welcome += " connected to the relay server."
	}
	history = append(history, store.Entry{Kind: store.KindSystem, Body: welcome, At: time.Now()})
	if err != nil {
		history = append(history, store.Entry{
			Kind: store.KindSystem,
			Body: fmt.Sprintf("history won't be saved this run: %v", err),
			At:   time.Now(),
		})
	}

	m := Model{
		ctx:      ctx,
		selfID:   selfID,
		handle:   handle,
		peers:    peer.NewRegistry(),
		peerC:    peerC,
		msgC:     msgC,
		offerC:   offerC,
		resultC:  resultC,
		inviteC:  inviteC,
		orgMates: make(map[string]bool),
		hist:     hist,
		history:  history,
		input:    ti,
	}
	if relayC != nil {
		m.relay = relayC
	}
	return m
}

// Close flushes/closes the history file and (in relay mode) the relay
// connection. Call once after the Bubble Tea program exits.
func (m Model) Close() {
	m.hist.Close()
	if m.relay != nil {
		_ = m.relay.Close()
	}
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
	cmds := []tea.Cmd{
		waitForPeer(m.peerC),
		waitForMessage(m.msgC),
		waitForFileOffer(m.offerC),
		waitForFileResult(m.resultC),
		waitForGameInvite(m.inviteC),
		tea.Tick(pruneInterval, func(t time.Time) tea.Msg { return pruneTickMsg(t) }),
		textinput.Blink,
	}
	if m.relay != nil {
		cmds = append(cmds, waitForRelayEvent(m.relay))
	}
	return tea.Batch(cmds...)
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

// waitForRelayEvent blocks on the relay client's event channel and turns
// the next push into a tea.Msg, mirroring waitForPeer for the LAN side.
// Update() re-issues this Cmd after each message so the pump keeps running
// for the lifetime of the connection.
func waitForRelayEvent(c relayClient) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-c.Events()
		if !ok {
			return nil // connection closed; let the pump stop quietly
		}
		return relayEventMsg(ev)
	}
}

// sendRelay delivers body to target over the relay in the background,
// mirroring sendMessage for the LAN side. See relaySendResultMsg for why
// only a local write failure surfaces from this Cmd.
func sendRelay(c relayClient, target, body string) tea.Cmd {
	return func() tea.Msg {
		err := c.SendRelay(target, body)
		return relaySendResultMsg{target: target, err: err}
	}
}

func createOrgCmd(c relayClient, name string) tea.Cmd {
	return func() tea.Msg {
		org, err := c.CreateOrg(name)
		return orgActionResultMsg{kind: "create", org: org, err: err}
	}
}

func listOrgsCmd(c relayClient) tea.Cmd {
	return func() tea.Msg {
		orgs, err := c.ListOrgs()
		return orgActionResultMsg{kind: "list", orgs: orgs, err: err}
	}
}

func inviteOrgCmd(c relayClient, orgID int64) tea.Cmd {
	return func() tea.Msg {
		code, expiresAt, err := c.InviteToOrg(orgID)
		return orgActionResultMsg{kind: "invite", code: code, expires: expiresAt, err: err}
	}
}

func joinOrgCmd(c relayClient, code string) tea.Cmd {
	return func() tea.Msg {
		org, err := c.JoinOrg(code)
		return orgActionResultMsg{kind: "join", org: org, err: err}
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

	case relayEventMsg:
		return m.handleRelayEvent(msg)

	case relaySendResultMsg:
		if msg.err != nil {
			m.history = recordEntry(m.history, m.hist, systemNote(fmt.Sprintf("failed to deliver to %s: %v", msg.target, msg.err)))
		}
		return m, nil

	case orgActionResultMsg:
		return m.handleOrgActionResult(msg)

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

// orgMateOnline case-insensitively matches target against currently-online
// org-mates, mirroring peer.Registry.Lookup's case-insensitive handle
// comparison (there's no registration step to enforce a canonical case on
// either side).
func (m Model) orgMateOnline(target string) (string, bool) {
	for handle := range m.orgMates {
		if strings.EqualFold(handle, target) {
			return handle, true
		}
	}
	return "", false
}

// sendBroadcast delivers text to every currently online peer -- both LAN
// (direct TCP) and, in relay mode, every online org-mate. There's no
// shared broadcast channel on either transport -- this is really just the
// same direct-message send fanned out to everyone currently known about --
// but from the user's side it reads as "just type to broadcast", matching
// the input placeholder.
func (m Model) sendBroadcast(text string) (tea.Model, tea.Cmd) {
	m.history = recordEntry(m.history, m.hist, store.Entry{Kind: store.KindSelfNote, Body: text, At: time.Now()})

	var cmds []tea.Cmd
	for _, p := range m.peers.List() {
		addr := fmt.Sprintf("%s:%d", p.Addr, p.TCPPort)
		cmds = append(cmds, sendMessage(addr, m.handle, p.Handle, text))
	}
	if m.relay != nil {
		for handle := range m.orgMates {
			cmds = append(cmds, sendRelay(m.relay, handle, text))
		}
	}
	if len(cmds) == 0 {
		return m, nil
	}
	return m, tea.Batch(cmds...)
}

// sendDirect parses leading "@handle" tokens (however many appear before the
// first non-@ word) as the recipient list, and delivers the remaining text
// to each of them independently -- one history entry per recipient, so
// filtering/coloring stays a clean one-entry-one-peer relationship. Each
// target resolves against LAN peers first, then online org-mates (relay
// mode); file transfer (see filePathIn) is LAN-only for now. If the
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
		if info, ok := m.peers.Lookup(target); ok {
			m.history = recordEntry(m.history, m.hist, store.Entry{
				Kind: store.KindDirectSent,
				Peer: info.Handle,
				Body: body,
				At:   time.Now(),
			})
			m.lastDMHandle = info.Handle
			addr := fmt.Sprintf("%s:%d", info.Addr, info.TCPPort)
			cmds = append(cmds, sendMessage(addr, m.handle, info.Handle, body))
			continue
		}
		if handle, ok := m.orgMateOnline(target); ok {
			m.history = recordEntry(m.history, m.hist, store.Entry{
				Kind: store.KindDirectSent,
				Peer: handle,
				Body: body,
				At:   time.Now(),
			})
			m.lastDMHandle = handle
			cmds = append(cmds, sendRelay(m.relay, handle, body))
			continue
		}
		m.history = recordEntry(m.history, m.hist, systemNote(fmt.Sprintf("no such peer online: %s", target)))
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
	{"/org create <name>", "create an organization (you become its admin) -- requires --server"},
	{"/org list", "list the organizations you belong to -- requires --server"},
	{"/org invite <id>", "generate an invite code for an org you admin -- requires --server"},
	{"/org join <code>", "redeem an invite code to join an org -- requires --server"},
	{"/help", "list every slash command"},
}

// handleCommand implements the slash-command set: /filter @handle (hide
// everything except that conversation), /clear (show everything), /play
// (challenge a peer or start a local game), /org (organization management,
// relay mode only), and /help (list all of these).
func (m Model) handleCommand(text string) (tea.Model, tea.Cmd) {
	fields := strings.Fields(text)
	switch strings.ToLower(fields[0]) {
	case "/play":
		return m.handlePlayCommand(fields)
	case "/org":
		return m.handleOrgCommand(fields)
	case "/filter":
		if len(fields) < 2 {
			m.history = recordEntry(m.history, m.hist, systemNote("usage: /filter @handle"))
			return m, nil
		}
		target := fields[1]
		_, lanOK := m.peers.Lookup(target)
		_, orgOK := m.orgMateOnline(target)
		if !lanOK && !orgOK {
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

// handleOrgCommand implements "/org create|list|invite|join", all of which
// require a relay connection (organizations don't exist in LAN-only mode).
// Each subcommand dispatches an async Cmd; the result comes back later as
// an orgActionResultMsg.
func (m Model) handleOrgCommand(fields []string) (tea.Model, tea.Cmd) {
	if m.relay == nil {
		m.history = recordEntry(m.history, m.hist, systemNote("orgs require a relay server -- restart with --server=host:port"))
		return m, nil
	}
	if len(fields) < 2 {
		m.history = recordEntry(m.history, m.hist, systemNote("usage: /org create <name> | /org list | /org invite <id> | /org join <code>"))
		return m, nil
	}

	switch strings.ToLower(fields[1]) {
	case "create":
		if len(fields) < 3 {
			m.history = recordEntry(m.history, m.hist, systemNote("usage: /org create <name>"))
			return m, nil
		}
		name := strings.Join(fields[2:], " ")
		return m, createOrgCmd(m.relay, name)

	case "list":
		return m, listOrgsCmd(m.relay)

	case "invite":
		if len(fields) != 3 {
			m.history = recordEntry(m.history, m.hist, systemNote("usage: /org invite <org-id> (see /org list for IDs)"))
			return m, nil
		}
		orgID, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil {
			m.history = recordEntry(m.history, m.hist, systemNote("org id must be a number (see /org list)"))
			return m, nil
		}
		return m, inviteOrgCmd(m.relay, orgID)

	case "join":
		if len(fields) != 3 {
			m.history = recordEntry(m.history, m.hist, systemNote("usage: /org join <code>"))
			return m, nil
		}
		return m, joinOrgCmd(m.relay, fields[2])

	default:
		m.history = recordEntry(m.history, m.hist, systemNote(fmt.Sprintf("unknown /org subcommand: %s (try create, list, invite, join)", fields[1])))
		return m, nil
	}
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

// handleRelayEvent reacts to one asynchronous push from the relay server:
// presence changes update orgMates the same way peerSeenMsg updates peers,
// and an incoming relayed message is recorded exactly like a LAN
// incomingMsgMsg -- from the chat log's point of view, a relay message and
// a LAN message are indistinguishable once they've arrived.
func (m Model) handleRelayEvent(msg relayEventMsg) (tea.Model, tea.Cmd) {
	ev := relay.Envelope(msg)
	switch ev.Type {
	case relay.MsgPresenceJoined:
		if !m.orgMates[ev.Handle] {
			m.orgMates[ev.Handle] = true
			m.history = recordEntry(m.history, m.hist, systemNote(fmt.Sprintf("%s %s joined (org)", avatarForHandle(ev.Handle), ev.Handle)))
		}

	case relay.MsgPresenceLeft:
		delete(m.orgMates, ev.Handle)
		m.history = recordEntry(m.history, m.hist, systemNote(fmt.Sprintf("%s left (org)", ev.Handle)))

	case relay.MsgRelay:
		m.history = recordEntry(m.history, m.hist, store.Entry{
			Kind: store.KindDirectRecv,
			Peer: ev.Handle,
			Body: ev.Body,
			At:   time.Now(),
		})
		m.lastDMHandle = ev.Handle
		notify.Alert(fmt.Sprintf("Message from %s", ev.Handle), ev.Body)

	case relay.MsgError:
		// An unsolicited error, e.g. a fire-and-forget SendRelay that
		// failed (see relaySendResultMsg's doc comment).
		m.history = recordEntry(m.history, m.hist, systemNote(fmt.Sprintf("relay: %s", ev.Reason)))
	}
	return m, waitForRelayEvent(m.relay)
}

// handleOrgActionResult reports the outcome of an async /org request.
func (m Model) handleOrgActionResult(msg orgActionResultMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.history = recordEntry(m.history, m.hist, systemNote(fmt.Sprintf("org %s failed: %v", msg.kind, msg.err)))
		return m, nil
	}

	switch msg.kind {
	case "create":
		m.history = recordEntry(m.history, m.hist, systemNote(fmt.Sprintf("created org %q (id %d) -- you're its admin", msg.org.Name, msg.org.ID)))

	case "list":
		if len(msg.orgs) == 0 {
			m.history = recordEntry(m.history, m.hist, systemNote("you don't belong to any orgs yet -- /org create <name> or /org join <code>"))
			break
		}
		m.history = recordEntry(m.history, m.hist, systemNote("your orgs:"))
		for _, o := range msg.orgs {
			m.history = recordEntry(m.history, m.hist, systemNote(fmt.Sprintf("  [%d] %s", o.ID, o.Name)))
		}

	case "invite":
		m.history = recordEntry(m.history, m.hist, systemNote(fmt.Sprintf(
			"invite code: %s (expires %s) -- share it with whoever you're inviting", msg.code, msg.expires.Format(time.RFC1123))))

	case "join":
		m.history = recordEntry(m.history, m.hist, systemNote(fmt.Sprintf("joined org %q (id %d)", msg.org.Name, msg.org.ID)))
	}
	return m, nil
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
	for h := range m.orgMates {
		if strings.HasPrefix(strings.ToLower(h), partial) {
			matches = append(matches, h)
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
	b.WriteString(sidebarHeaderStyle.Render(fmt.Sprintf("LAN (%d)", m.peers.Len())))
	b.WriteString("\n\n")
	for _, p := range m.peers.List() {
		b.WriteString(m.renderSidebarLine(p.Handle))
		b.WriteString("\n")
	}
	if m.peers.Len() == 0 {
		b.WriteString(statusStyle.Render("searching..."))
	}

	if m.relay != nil {
		b.WriteString("\n")
		b.WriteString(sidebarHeaderStyle.Render(fmt.Sprintf("Org (%d)", len(m.orgMates))))
		b.WriteString("\n\n")
		mates := make([]string, 0, len(m.orgMates))
		for h := range m.orgMates {
			mates = append(mates, h)
		}
		sort.Strings(mates)
		for _, h := range mates {
			b.WriteString(m.renderSidebarLine(h))
			b.WriteString("\n")
		}
		if len(m.orgMates) == 0 {
			b.WriteString(statusStyle.Render("none online"))
		}
	}

	return sidebarStyle.Width(width).Height(height).Render(b.String())
}

// renderSidebarLine renders one handle for either the LAN or Org section --
// same avatar/color/filter-highlight treatment regardless of which.
func (m Model) renderSidebarLine(handle string) string {
	style := lipgloss.NewStyle().Foreground(colorForHandle(handle))
	line := avatarForHandle(handle) + " " + handle
	if strings.EqualFold(strings.TrimPrefix(handle, "@"), m.filterPeer) {
		line += " [filter]"
		style = style.Bold(true)
	}
	return style.Render(line)
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
