package ludo

import (
	"encoding/json"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/NotTesfamichael/tiru-emba/internal/network"
)

// Session is what networked Ludo needs from a network.GameSession --
// narrowed to an interface so it can be tested against a fake instead of a
// real TCP connection. Used symmetrically by both sides of a match: the
// host calls SendData to broadcast a full state snapshot to each guest
// after every action, and a guest calls SendData to send back its chosen
// action on its own turn.
type Session interface {
	SendData(data string) error
	Resign() error
	Close() error
	Events() <-chan network.GameEvent
}

// netRole distinguishes a purely local match (hotseat + AI, the original
// Step 1-4 design) from a networked one. Zero value is netRoleNone, so
// existing local-only construction via New() is unaffected.
type netRole int

const (
	netRoleNone netRole = iota
	netRoleHost
	netRoleGuest
)

// guestAction is what a guest sends the host on its own turn: either "roll"
// (host performs the actual RollDice; the guest never rolls locally) or
// "move" with the chosen token ID.
type guestAction struct {
	Kind    string `json:"kind"`
	TokenID int    `json:"token_id,omitempty"`
}

// hostState is the full snapshot the host broadcasts to every guest after
// each state-changing action, so guests never replay engine logic
// themselves -- they just replace their local view with whatever they're
// told, including the final state once the match ends.
type hostState struct {
	Game       *Game  `json:"game"`
	StatusLine string `json:"status_line"`
}

// NewHost starts a networked Ludo match as its host: self is seated as Red,
// and guestHandles (1-3 handles, matched 1:1 with guestSessions, already
// each holding an accepted session back to this host) fill Green/Yellow/
// Blue in order. The host owns the single authoritative *Game and
// broadcasts its full state to every guest after each roll or move --
// guests hold no authority of their own.
func NewHost(self string, guestHandles []string, guestSessions []Session) Model {
	colors := []Color{Green, Yellow, Blue}
	players := make([]*Player, 0, len(guestHandles)+1)
	players = append(players, NewPlayer(Red, self, false))
	sessions := make(map[Color]Session, len(guestHandles))
	for i, handle := range guestHandles {
		c := colors[i]
		players = append(players, NewPlayer(c, handle, false))
		sessions[c] = guestSessions[i]
	}

	return Model{
		game:     NewGame(players),
		self:     self,
		rng:      newRNG(),
		role:     netRoleHost,
		sessions: sessions,
	}
}

// NewGuest starts a networked Ludo match as a guest. game stays nil until
// the host's first broadcast arrives -- View renders a "waiting for the
// host" screen until then.
func NewGuest(self string, session Session) Model {
	return Model{
		self:    self,
		role:    netRoleGuest,
		session: session,
	}
}

// mySeatColor finds which seat's Player.Name matches this instance's own
// handle -- the same field (Player.Name) is set to the real handle for
// every seat in both local mode (the human, via New) and networked mode
// (host and every guest, via NewHost), so this one lookup identifies "my
// seat" uniformly across all three roles without a separately-tracked
// field that could drift out of sync with the actual game snapshot.
func (m Model) mySeatColor() Color {
	for _, p := range m.game.Players {
		if p.Name == m.self {
			return p.Color
		}
	}
	return Red // unreachable in practice; Red is always a party to the match
}

// nameFor looks up a seat's display name, for messages about a color the
// model isn't necessarily tracking a live Player pointer for right now.
func (m Model) nameFor(c Color) string {
	for _, p := range m.game.Players {
		if p.Color == c {
			return p.Name
		}
	}
	return c.String()
}

// guestActionMsg wraps a guest's decoded action, tagged with which seat it
// came from so the host knows whose Session to keep listening on next.
type guestActionMsg struct {
	seat   Color
	action guestAction
	left   bool // true if that guest resigned, disconnected, or sent something unparsable
}

func waitForGuestAction(sess Session, seat Color) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-sess.Events()
		if !ok || ev.Kind != network.GameEventMove {
			return guestActionMsg{seat: seat, left: true}
		}
		var a guestAction
		if err := json.Unmarshal([]byte(ev.Data), &a); err != nil {
			return guestActionMsg{seat: seat, left: true}
		}
		return guestActionMsg{seat: seat, action: a}
	}
}

// hostStateMsg wraps a guest's decoded snapshot of the host's broadcast.
type hostStateMsg struct {
	state  hostState
	lost   bool
	reason string // meaningful only when lost
}

func waitForHostState(sess Session) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-sess.Events()
		if !ok {
			return hostStateMsg{lost: true, reason: "connection to the host was lost"}
		}
		switch ev.Kind {
		case network.GameEventMove:
			var st hostState
			if err := json.Unmarshal([]byte(ev.Data), &st); err != nil {
				return hostStateMsg{lost: true, reason: "received something unreadable from the host"}
			}
			return hostStateMsg{state: st}
		case network.GameEventResign:
			return hostStateMsg{lost: true, reason: "the host ended the game"}
		default: // network.GameEventDisconnected
			return hostStateMsg{lost: true, reason: "connection to the host was lost"}
		}
	}
}

// broadcastState sends the current authoritative Game (plus the latest
// status line) to every connected guest. Marshal failure is silently
// dropped -- Game's fields are all plain, JSON-safe types, so it isn't
// expected to ever actually fail; if it somehow did, guests would simply
// time out waiting and see a disconnect, rather than the host crashing.
func (m Model) broadcastState() {
	b, err := json.Marshal(hostState{Game: m.game, StatusLine: m.statusLine})
	if err != nil {
		return
	}
	payload := string(b)
	for _, sess := range m.sessions {
		_ = sess.SendData(payload)
	}
}

// applyGuestAction is the host's handler for a decoded action arriving
// from one of its guests.
func (m Model) applyGuestAction(msg guestActionMsg) (Model, tea.Cmd) {
	if msg.left {
		return m.finishWithReason(fmt.Sprintf("%s left the game", m.nameFor(msg.seat)))
	}

	if m.game.CurrentPlayer().Color != msg.seat {
		// A straggling action from a seat whose turn has already passed
		// (e.g. it arrived just after a disconnect check moved things
		// along); ignore it and keep listening for that seat's next one.
		return m, waitForGuestAction(m.sessions[msg.seat], msg.seat)
	}

	p := m.game.CurrentPlayer()
	switch msg.action.Kind {
	case "roll":
		val := m.game.RollDice(m.rng)
		m.statusLine = fmt.Sprintf("%s rolled %d", p.Name, val)
	case "move":
		captured, err := m.game.MoveToken(msg.action.TokenID)
		if err != nil {
			// A guest can only ever send an action its own copy of the
			// state thought was legal; if it's somehow not (e.g. a stale
			// snapshot), just ignore it rather than let a bad message
			// desync the host's authoritative game.
			return m, waitForGuestAction(m.sessions[msg.seat], msg.seat)
		}
		verb := "moved"
		if captured {
			verb = "moved and captured a token!"
		}
		m.statusLine = fmt.Sprintf("%s %s %s", p.Name, verb, glyph(p.Tokens[msg.action.TokenID]))
	default:
		return m, waitForGuestAction(m.sessions[msg.seat], msg.seat)
	}

	return m.afterAction()
}

// applyHostState is a guest's handler for a decoded broadcast from the host.
func (m Model) applyHostState(msg hostStateMsg) (Model, tea.Cmd) {
	if msg.lost {
		return m.finishWithReason(msg.reason)
	}

	m.game = msg.state.Game
	m.statusLine = msg.state.StatusLine
	m.cursor = 0

	if m.game.Phase == PhaseGameOver {
		return m.finish()
	}
	return m, waitForHostState(m.session)
}

// closeNetwork releases whatever network resources this instance holds,
// per its role. A no-op for local (non-networked) play.
func (m Model) closeNetwork() {
	switch m.role {
	case netRoleHost:
		for _, sess := range m.sessions {
			_ = sess.Close()
		}
	case netRoleGuest:
		_ = m.session.Close()
	}
}

func marshalAction(a guestAction) string {
	b, err := json.Marshal(a)
	if err != nil {
		return "" // host will fail to decode and safely ignore it
	}
	return string(b)
}
