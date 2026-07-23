package tictactoe

import (
	"errors"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/NotTesfamichael/tiru-emba/internal/network"
)

type fakeSession struct {
	sent     []int
	resigned bool
	closed   bool
	sendErr  error
	events   chan network.GameEvent
}

func newFakeSession() *fakeSession {
	return &fakeSession{events: make(chan network.GameEvent, 8)}
}

func (f *fakeSession) SendMove(pos int) error {
	f.sent = append(f.sent, pos)
	return f.sendErr
}
func (f *fakeSession) Resign() error                    { f.resigned = true; return nil }
func (f *fakeSession) Close() error                     { f.closed = true; return nil }
func (f *fakeSession) Events() <-chan network.GameEvent { return f.events }

func key(s string) tea.KeyMsg {
	switch s {
	case "up", "down", "left", "right", "enter", "esc":
		types := map[string]tea.KeyType{
			"up": tea.KeyUp, "down": tea.KeyDown, "left": tea.KeyLeft,
			"right": tea.KeyRight, "enter": tea.KeyEnter, "esc": tea.KeyEsc,
		}
		return tea.KeyMsg{Type: types[s]}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

func TestWinnerDetectsAllLines(t *testing.T) {
	cases := []struct {
		name  string
		cells map[int]Mark
		want  Mark
	}{
		{"top row", map[int]Mark{0: X, 1: X, 2: X}, X},
		{"middle col", map[int]Mark{1: O, 4: O, 7: O}, O},
		{"main diagonal", map[int]Mark{0: X, 4: X, 8: X}, X},
		{"anti diagonal", map[int]Mark{2: O, 4: O, 6: O}, O},
		{"no winner yet", map[int]Mark{0: X, 1: O, 4: X}, Empty},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var b board
			for i, m := range c.cells {
				b[i] = m
			}
			if got := b.winner(); got != c.want {
				t.Errorf("winner() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestBoardFull(t *testing.T) {
	var b board
	if b.full() {
		t.Error("empty board should not be full")
	}
	for i := range b {
		b[i] = X
	}
	if !b.full() {
		t.Error("fully-occupied board should be full")
	}
}

func TestAttemptMoveRejectsOccupiedCell(t *testing.T) {
	sess := newFakeSession()
	m := New(sess, X, "@me", "@kal")
	m.board[4] = X
	m.turn = X
	m.cursor = 4

	m, _ = m.handleKey(key("enter"))
	if m.warning == "" {
		t.Error("expected a warning for placing on an occupied cell")
	}
	if len(sess.sent) != 0 {
		t.Error("no move should have been sent for a rejected placement")
	}
}

func TestAttemptMoveRejectsWhenNotYourTurn(t *testing.T) {
	sess := newFakeSession()
	m := New(sess, O, "@me", "@kal") // O never moves first
	m.cursor = 0

	m, _ = m.handleKey(key("enter"))
	if m.warning == "" {
		t.Error("expected a warning for moving out of turn")
	}
	if len(sess.sent) != 0 {
		t.Error("no move should have been sent out of turn")
	}
}

func TestAttemptMoveSendsAndPassesTurn(t *testing.T) {
	sess := newFakeSession()
	m := New(sess, X, "@me", "@kal")
	m.cursor = 0

	m, cmd := m.handleKey(key("enter"))
	if cmd == nil {
		t.Fatal("expected a non-nil cmd to actually deliver the move")
	}
	cmd() // tea.Cmd is lazy -- SendMove isn't called until this executes

	if len(sess.sent) != 1 || sess.sent[0] != 0 {
		t.Errorf("sent = %v, want [0]", sess.sent)
	}
	if m.turn != O {
		t.Errorf("turn = %v, want O after X moves", m.turn)
	}
	if m.done {
		t.Error("game should not be over after one move")
	}
}

func TestAttemptMoveWinEndsGame(t *testing.T) {
	sess := newFakeSession()
	m := New(sess, X, "@me", "@kal")
	m.board[0], m.board[1] = X, X
	m.turn = X
	m.cursor = 2

	m, cmd := m.handleKey(key("enter"))
	if !m.done {
		t.Fatal("game should be over after completing a line")
	}
	if !sess.closed {
		t.Error("session should be closed once the game ends")
	}
	if cmd == nil {
		t.Fatal("expected a batched cmd")
	}
	msg := findGameOverMsg(t, cmd)
	if msg.ResultText != "you win!" {
		t.Errorf("ResultText = %q, want %q", msg.ResultText, "you win!")
	}
}

func TestHandleEventOpponentMoveThenMyTurn(t *testing.T) {
	sess := newFakeSession()
	m := New(sess, X, "@me", "@kal")
	m.turn = O // waiting on opponent

	m, cmd := m.handleEvent(network.GameEvent{Kind: network.GameEventMove, Position: 3})
	if m.board[3] != O {
		t.Errorf("board[3] = %v, want O", m.board[3])
	}
	if m.turn != X {
		t.Errorf("turn = %v, want X after opponent moves", m.turn)
	}
	if cmd == nil {
		t.Fatal("expected the event pump to be re-armed")
	}
}

func TestHandleEventOpponentWinDetected(t *testing.T) {
	sess := newFakeSession()
	m := New(sess, X, "@me", "@kal")
	m.board[3], m.board[4] = O, O
	m.turn = O

	m, cmd := m.handleEvent(network.GameEvent{Kind: network.GameEventMove, Position: 5})
	if !m.done {
		t.Fatal("game should be over once the opponent completes a line")
	}
	msg := findGameOverMsg(t, cmd)
	if msg.ResultText != "@kal wins" {
		t.Errorf("ResultText = %q, want %q", msg.ResultText, "@kal wins")
	}
}

func TestHandleEventDraw(t *testing.T) {
	sess := newFakeSession()
	m := New(sess, X, "@me", "@kal")
	// X O X
	// X O O
	// O X _   <- opponent (O) places the last mark at 8, no winner
	m.board = board{X, O, X, X, O, O, O, X, Empty}
	m.turn = O

	m, cmd := m.handleEvent(network.GameEvent{Kind: network.GameEventMove, Position: 8})
	if !m.done {
		t.Fatal("expected the game to end in a draw")
	}
	msg := findGameOverMsg(t, cmd)
	if msg.ResultText != "draw" {
		t.Errorf("ResultText = %q, want %q", msg.ResultText, "draw")
	}
}

func TestHandleEventResign(t *testing.T) {
	sess := newFakeSession()
	m := New(sess, X, "@me", "@kal")

	m, cmd := m.handleEvent(network.GameEvent{Kind: network.GameEventResign})
	if !m.done {
		t.Fatal("expected the game to end on opponent resign")
	}
	msg := findGameOverMsg(t, cmd)
	if msg.ResultText == "" {
		t.Error("expected a non-empty result text")
	}
}

func TestEscResignsAndEndsGame(t *testing.T) {
	sess := newFakeSession()
	m := New(sess, X, "@me", "@kal")

	m, cmd := m.handleKey(key("esc"))
	if !sess.resigned {
		t.Error("expected Resign to have been called")
	}
	if !sess.closed {
		t.Error("expected the session to be closed")
	}
	if !m.done {
		t.Error("expected the game to be marked done")
	}
	findGameOverMsg(t, cmd)
}

func TestCursorNavigationStaysInBounds(t *testing.T) {
	sess := newFakeSession()
	m := New(sess, X, "@me", "@kal")
	m.cursor = 0

	m, _ = m.handleKey(key("up")) // already on top row, should stay
	if m.cursor != 0 {
		t.Errorf("cursor = %d, want 0 (up from top row should be a no-op)", m.cursor)
	}
	m, _ = m.handleKey(key("left")) // already on left column, should stay
	if m.cursor != 0 {
		t.Errorf("cursor = %d, want 0 (left from left column should be a no-op)", m.cursor)
	}
	m, _ = m.handleKey(key("right"))
	if m.cursor != 1 {
		t.Errorf("cursor = %d, want 1", m.cursor)
	}
	m, _ = m.handleKey(key("down"))
	if m.cursor != 4 {
		t.Errorf("cursor = %d, want 4", m.cursor)
	}
}

func TestMoveSentErrorEndsGame(t *testing.T) {
	sess := newFakeSession()
	sess.sendErr = errors.New("connection reset")
	m := New(sess, X, "@me", "@kal")

	m, _ = m.Update(moveSentMsg{err: sess.sendErr})
	if !m.done {
		t.Error("a failed send should end the game rather than leave it stuck")
	}
}

func TestHandleEventIgnoredOnceGameIsDone(t *testing.T) {
	sess := newFakeSession()
	m := New(sess, X, "@me", "@kal")
	m.board[0], m.board[1] = X, X
	m.turn = X
	m.cursor = 2

	m, _ = m.handleKey(key("enter")) // wins the game, closes the session
	if !m.done || m.resultText != "you win!" {
		t.Fatalf("setup: expected the win to already be recorded, got done=%v resultText=%q", m.done, m.resultText)
	}

	// A waitForGameEvent armed before the win can still deliver one more
	// event -- e.g. the disconnect our own opponent's socket sees once our
	// Close() runs. That must not overwrite the win with a second,
	// contradictory GameOverMsg.
	m, cmd := m.handleEvent(network.GameEvent{Kind: network.GameEventDisconnected})
	if cmd != nil {
		t.Errorf("expected no cmd once the game is already done, got %v", cmd())
	}
	if m.resultText != "you win!" {
		t.Errorf("resultText = %q, want the original %q to survive a stray post-game event", m.resultText, "you win!")
	}
}

// findGameOverMsg executes cmd (and, if it's a batch, its sub-commands)
// looking for a GameOverMsg, failing the test if none is found.
func findGameOverMsg(t *testing.T, cmd tea.Cmd) GameOverMsg {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected a cmd, got nil")
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, sub := range batch {
			if m, ok := sub().(GameOverMsg); ok {
				return m
			}
		}
		t.Fatal("batch cmd did not contain a GameOverMsg")
	}
	if m, ok := msg.(GameOverMsg); ok {
		return m
	}
	t.Fatalf("cmd produced %T, want GameOverMsg", msg)
	return GameOverMsg{}
}
