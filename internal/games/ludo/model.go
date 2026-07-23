package ludo

import (
	"fmt"
	"math/rand"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// GameOverMsg is emitted once the match ends -- a winner, a disconnect, or
// someone leaving early -- for a hosting router to catch and switch back
// to whatever view it came from. Mirrors tictactoe.GameOverMsg so both
// games plug into the same router pattern.
type GameOverMsg struct {
	ResultText string
}

// aiTickMsg drives the AI loop: one tick is one phase-appropriate action
// (a roll, or a move once legal moves are available), so a roll and its
// resulting move render as two separate, briefly-paced steps instead of
// jumping straight to the end result.
type aiTickMsg struct{}

func aiTick() tea.Cmd {
	return tea.Tick(650*time.Millisecond, func(time.Time) tea.Msg { return aiTickMsg{} })
}

func newRNG() *rand.Rand {
	return rand.New(rand.NewSource(time.Now().UnixNano()))
}

// Model is a Ludo match as a Bubble Tea sub-model, covering three roles
// (see netRole): a purely local hotseat+AI game, a networked host, or a
// networked guest. All three share the same engine (Game), rendering
// (view.go), and turn-dispatch logic (afterAction) -- only how a roll/move
// is actually carried out (locally vs. sent to/from the network) differs.
type Model struct {
	// game is nil only for a guest before the host's first broadcast
	// arrives; View renders a "waiting for the host" screen until then.
	game *Game
	self string // this instance's own display handle, shown in the title/panel

	rng *rand.Rand

	cursor     int    // index into selectableTokens(), while Phase == PhaseSelectToken
	statusLine string // last roll/move/capture, for the side panel
	warning    string // transient input error, cleared on the next key

	done       bool
	resultText string

	role     netRole
	sessions map[Color]Session // host only: each guest seat's Session
	session  Session           // guest only: the Session back to the host
}

// New starts a fresh local match: self is seated as Red (always first to
// move), followed by numAI computer-controlled opponents in Green, Yellow,
// Blue order. numAI must be 1-3 (2-4 total players).
func New(self string, numAI int) Model {
	colors := []Color{Red, Green, Yellow, Blue}
	names := []string{"Green", "Yellow", "Blue"}

	players := make([]*Player, 0, numAI+1)
	players = append(players, NewPlayer(Red, self, false))
	for i := 0; i < numAI; i++ {
		players = append(players, NewPlayer(colors[i+1], names[i], true))
	}

	return Model{
		game: NewGame(players),
		self: self,
		rng:  newRNG(),
	}
}

func (m Model) Init() tea.Cmd {
	switch m.role {
	case netRoleHost:
		cmds := make([]tea.Cmd, 0, len(m.sessions))
		for c, sess := range m.sessions {
			cmds = append(cmds, waitForGuestAction(sess, c))
		}
		return tea.Batch(cmds...)
	case netRoleGuest:
		return waitForHostState(m.session)
	default:
		if m.game.CurrentPlayer().IsAI {
			return aiTick()
		}
		return nil
	}
}

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	if m.done {
		// The match already concluded (win, quit, or a network event
		// already ended it) and closed our end of any session ourselves:
		// a Cmd armed before that point (aiTick, or a network listener)
		// can still deliver one more message, e.g. the disconnect our own
		// Close() causes on the other end. Without this guard that would
		// surface as a second, contradictory GameOverMsg -- "connection
		// lost" appearing right after "you left the game".
		return m, nil
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKey(msg)
	case aiTickMsg:
		return m.aiStep()
	case guestActionMsg:
		return m.applyGuestAction(msg)
	case hostStateMsg:
		return m.applyHostState(msg)
	}
	return m, nil
}

// selectableTokens returns the current player's legal token IDs for the
// current dice roll, in a stable ascending order (LegalMoves already walks
// tokens by ID) so the cursor has a consistent sequence to cycle through.
func (m Model) selectableTokens() []int {
	return m.game.LegalMoves()
}

func (m Model) handleKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	if m.done {
		return m, nil // router owns transitioning away once GameOverMsg fires
	}

	// Quitting must work regardless of whose turn it is -- mid-AI-turn,
	// mid-remote-guest-turn, or while still waiting on the host's first
	// broadcast -- otherwise esc/q appears to do nothing at the wrong
	// moment, which reads as the game hanging.
	switch msg.String() {
	case "esc", "q", "ctrl+c":
		switch m.role {
		case netRoleGuest:
			_ = m.session.Resign()
		case netRoleHost:
			for _, sess := range m.sessions {
				_ = sess.Resign()
			}
		}
		return m.finishWithReason("you left the game")
	}

	if m.game == nil {
		return m, nil // guest hasn't received the host's first broadcast yet
	}
	if m.game.CurrentPlayer().Color != m.mySeatColor() {
		return m, nil // not my turn -- an AI's, or another real player's
	}
	m.warning = ""

	switch msg.String() {
	case " ", "enter":
		switch m.game.Phase {
		case PhaseRollDice:
			return m.roll()
		case PhaseSelectToken:
			return m.confirmSelection()
		}

	case "left", "a":
		if choices := m.selectableTokens(); len(choices) > 0 {
			m.cursor = (m.cursor - 1 + len(choices)) % len(choices)
		}
	case "right", "d":
		if choices := m.selectableTokens(); len(choices) > 0 {
			m.cursor = (m.cursor + 1) % len(choices)
		}

	case "1", "2", "3", "4":
		if m.game.Phase == PhaseSelectToken {
			return m.chooseToken(int(msg.String()[0] - '1'))
		}
	}
	return m, nil
}

// roll performs a dice roll on my own turn: directly, if I'm playing
// locally or hosting; sent to the host to perform, if I'm a guest (the
// host's RNG is the only one that's authoritative).
func (m Model) roll() (Model, tea.Cmd) {
	if m.role == netRoleGuest {
		_ = m.session.SendData(marshalAction(guestAction{Kind: "roll"}))
		m.statusLine = "you rolled -- waiting for the host..."
		return m, nil
	}
	name := m.game.CurrentPlayer().Name
	val := m.game.RollDice(m.rng)
	m.statusLine = fmt.Sprintf("%s rolled %d", name, val)
	return m.afterAction()
}

func (m Model) confirmSelection() (Model, tea.Cmd) {
	choices := m.selectableTokens()
	if len(choices) == 0 {
		return m, nil
	}
	return m.chooseToken(choices[m.cursor])
}

// chooseToken moves tokenID on my own turn: directly, if local or hosting;
// sent to the host, if I'm a guest.
func (m Model) chooseToken(tokenID int) (Model, tea.Cmd) {
	p := m.game.CurrentPlayer()
	if tokenID < 0 || tokenID >= len(p.Tokens) {
		m.warning = "no such token"
		return m, nil
	}

	if m.role == netRoleGuest {
		legal := false
		for _, id := range m.selectableTokens() {
			if id == tokenID {
				legal = true
				break
			}
		}
		if !legal {
			m.warning = "that token has no legal move for this roll"
			return m, nil
		}
		_ = m.session.SendData(marshalAction(guestAction{Kind: "move", TokenID: tokenID}))
		m.statusLine = fmt.Sprintf("you moved %s -- waiting for the host...", glyph(p.Tokens[tokenID]))
		return m, nil
	}

	captured, err := m.game.MoveToken(tokenID)
	if err != nil {
		m.warning = "that token has no legal move for this roll"
		return m, nil
	}
	verb := "moved"
	if captured {
		verb = "moved and captured a token!"
	}
	m.statusLine = fmt.Sprintf("%s %s %s", p.Name, verb, glyph(p.Tokens[tokenID]))
	return m.afterAction()
}

// aiStep performs exactly one phase-appropriate action for the current
// (AI) player: a roll, or a move once legal moves are available. AI seats
// only ever occur in local play (networked matches are pure human), but
// this stays role-agnostic via afterAction for consistency.
func (m Model) aiStep() (Model, tea.Cmd) {
	p := m.game.CurrentPlayer()
	switch m.game.Phase {
	case PhaseRollDice:
		val := m.game.RollDice(m.rng)
		m.statusLine = fmt.Sprintf("%s rolled %d", p.Name, val)
	case PhaseSelectToken:
		id := ChooseAIMove(m.game)
		captured, _ := m.game.MoveToken(id)
		verb := "moved"
		if captured {
			verb = "moved and captured a token!"
		}
		m.statusLine = fmt.Sprintf("%s %s %s", p.Name, verb, glyph(p.Tokens[id]))
	}
	return m.afterAction()
}

// afterAction runs after any state-changing action -- a local human's,
// hosting player's, an AI's, or (via applyGuestAction) a remote guest's --
// to decide what happens next: game over, or hand off to whoever's turn it
// now is. When hosting, it also broadcasts the new state to every guest
// first, so their screens stay in lockstep with this one regardless of
// whose action just happened.
func (m Model) afterAction() (Model, tea.Cmd) {
	if m.role == netRoleHost {
		m.broadcastState()
	}

	if m.game.Phase == PhaseGameOver {
		return m.finish()
	}

	m.cursor = 0
	next := m.game.CurrentPlayer()

	switch {
	case m.role == netRoleHost && next.Color != Red:
		return m, waitForGuestAction(m.sessions[next.Color], next.Color)
	case next.IsAI:
		return m, aiTick()
	default:
		return m, nil // wait for local input: my own turn (local play, or hosting)
	}
}

// finish ends the match on a normal conclusion (a winner), personalizing
// the result text for this instance's own seat.
func (m Model) finish() (Model, tea.Cmd) {
	m.done = true
	if m.game.Winner != nil {
		for _, p := range m.game.Players {
			if p.Color == *m.game.Winner {
				if p.Color == m.mySeatColor() {
					m.resultText = "you win!"
				} else {
					m.resultText = fmt.Sprintf("%s wins", p.Name)
				}
				break
			}
		}
	}
	m.closeNetwork()
	return m, func() tea.Msg { return GameOverMsg{ResultText: m.resultText} }
}

// finishWithReason ends the match immediately with an explicit message --
// leaving early, a disconnect, or a guest dropping out -- bypassing the
// normal winner lookup.
func (m Model) finishWithReason(reason string) (Model, tea.Cmd) {
	m.done = true
	m.resultText = reason
	m.closeNetwork()
	return m, func() tea.Msg { return GameOverMsg{ResultText: m.resultText} }
}
