package tictactoe

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/NotTesfamichael/tiru-emba/internal/network"
)

// Session is what the game needs from a network.GameSession -- narrowed to
// an interface so game logic can be tested against a fake instead of a real
// TCP connection.
type Session interface {
	SendMove(pos int) error
	Resign() error
	Close() error
	Events() <-chan network.GameEvent
}

// GameOverMsg is emitted once the game ends, for a hosting router to catch
// and switch back to whatever view it came from. resultText is a
// human-readable one-liner suitable for dropping straight into a chat log.
type GameOverMsg struct {
	ResultText string
}

// gameEventMsg wraps a network.GameEvent so it can travel through Bubble
// Tea's Msg pipeline.
type gameEventMsg network.GameEvent

// moveSentMsg reports whether an outgoing move actually reached the
// opponent.
type moveSentMsg struct{ err error }

// Model is a Tic-Tac-Toe game as a Bubble Tea sub-model.
type Model struct {
	session  Session
	board    board
	mySymbol Mark
	turn     Mark
	cursor   int

	self, opponent string

	done       bool
	resultText string
	warning    string // transient, e.g. "not your turn" -- cleared on the next input
}

// New starts a fresh game. mySymbol determines who moves first: X always
// goes first, by the standard Tic-Tac-Toe convention, so the challenger
// (who sent the invite) should be given X and the invitee O.
func New(session Session, mySymbol Mark, self, opponent string) Model {
	return Model{
		session:  session,
		mySymbol: mySymbol,
		turn:     X,
		cursor:   4, // start at center, a reasonable default focus point
		self:     self,
		opponent: opponent,
	}
}

func (m Model) Init() tea.Cmd {
	return waitForGameEvent(m.session)
}

func waitForGameEvent(s Session) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-s.Events()
		if !ok {
			return gameEventMsg{Kind: network.GameEventDisconnected}
		}
		return gameEventMsg(ev)
	}
}

func sendMove(s Session, pos int) tea.Cmd {
	return func() tea.Msg {
		return moveSentMsg{err: s.SendMove(pos)}
	}
}

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKey(msg)
	case gameEventMsg:
		return m.handleEvent(network.GameEvent(msg))
	case moveSentMsg:
		if msg.err != nil {
			m.done = true
			m.resultText = fmt.Sprintf("connection to %s was lost", m.opponent)
		}
		return m, nil
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	m.warning = ""

	if m.done {
		return m, nil // router owns transitioning away once GameOverMsg fires
	}

	switch msg.String() {
	case "up", "w":
		if m.cursor >= 3 {
			m.cursor -= 3
		}
	case "down", "s":
		if m.cursor < 6 {
			m.cursor += 3
		}
	case "left", "a":
		if m.cursor%3 != 0 {
			m.cursor--
		}
	case "right", "d":
		if m.cursor%3 != 2 {
			m.cursor++
		}
	case "enter", " ":
		return m.attemptMove(m.cursor)
	case "esc", "ctrl+c":
		_ = m.session.Resign()
		_ = m.session.Close()
		m.done = true
		m.resultText = fmt.Sprintf("you resigned against %s", m.opponent)
		return m, func() tea.Msg { return GameOverMsg{ResultText: m.resultText} }
	}
	return m, nil
}

func (m Model) attemptMove(pos int) (Model, tea.Cmd) {
	if m.turn != m.mySymbol {
		m.warning = "not your turn"
		return m, nil
	}
	if m.board[pos] != Empty {
		m.warning = "that square is taken"
		return m, nil
	}

	m.board[pos] = m.mySymbol
	moveCmd := sendMove(m.session, pos)

	if winner := m.board.winner(); winner != Empty {
		m.done = true
		m.resultText = "you win!"
		_ = m.session.Close()
		return m, tea.Batch(moveCmd, func() tea.Msg { return GameOverMsg{ResultText: m.resultText} })
	}
	if m.board.full() {
		m.done = true
		m.resultText = "draw"
		_ = m.session.Close()
		return m, tea.Batch(moveCmd, func() tea.Msg { return GameOverMsg{ResultText: m.resultText} })
	}

	m.turn = m.turn.Other()
	return m, moveCmd
}

func (m Model) handleEvent(ev network.GameEvent) (Model, tea.Cmd) {
	switch ev.Kind {
	case network.GameEventMove:
		if ev.Position >= 0 && ev.Position < len(m.board) && m.board[ev.Position] == Empty {
			m.board[ev.Position] = m.mySymbol.Other()
		}

		if winner := m.board.winner(); winner != Empty {
			m.done = true
			if winner == m.mySymbol {
				m.resultText = "you win!"
			} else {
				m.resultText = fmt.Sprintf("%s wins", m.opponent)
			}
			_ = m.session.Close()
			return m, func() tea.Msg { return GameOverMsg{ResultText: m.resultText} }
		}
		if m.board.full() {
			m.done = true
			m.resultText = "draw"
			_ = m.session.Close()
			return m, func() tea.Msg { return GameOverMsg{ResultText: m.resultText} }
		}

		m.turn = m.mySymbol
		return m, waitForGameEvent(m.session)

	case network.GameEventResign:
		m.done = true
		m.resultText = fmt.Sprintf("%s resigned -- you win!", m.opponent)
		_ = m.session.Close()
		return m, func() tea.Msg { return GameOverMsg{ResultText: m.resultText} }

	case network.GameEventDisconnected:
		m.done = true
		m.resultText = fmt.Sprintf("connection to %s was lost", m.opponent)
		return m, func() tea.Msg { return GameOverMsg{ResultText: m.resultText} }
	}
	return m, waitForGameEvent(m.session)
}
