package ludo

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/NotTesfamichael/tiru-emba/internal/network"
)

// fakeSession is a minimal Session double: it records every SendData
// payload and lets a test push events for the model to receive.
type fakeSession struct {
	sent     []string
	resigned bool
	closed   bool
	events   chan network.GameEvent
}

func newFakeSession() *fakeSession {
	return &fakeSession{events: make(chan network.GameEvent, 8)}
}

func (f *fakeSession) SendData(data string) error {
	f.sent = append(f.sent, data)
	return nil
}
func (f *fakeSession) Resign() error                    { f.resigned = true; return nil }
func (f *fakeSession) Close() error                     { f.closed = true; return nil }
func (f *fakeSession) Events() <-chan network.GameEvent { return f.events }

func newTestHost() (Model, *fakeSession) {
	green := newFakeSession()
	m := NewHost("@host", []string{"@guest"}, []Session{green})
	return m, green
}

func TestHostAppliesGuestRollAndBroadcasts(t *testing.T) {
	m, green := newTestHost()
	// Red (host) always moves first; hand the turn to Green for this test.
	m.game.Turn = 1

	// RollDice uses real randomness, so whether Green gets a legal move
	// (staying Green's turn, cmd waits on Green again) or not (auto-passing
	// back to the host, cmd nil for local input) legitimately varies --
	// only the roll itself and the broadcast are deterministic to assert on.
	m, _ = m.applyGuestAction(guestActionMsg{seat: Green, action: guestAction{Kind: "roll"}})
	if m.game.Dice == 0 {
		t.Fatal("expected the host to have actually rolled for Green")
	}
	if len(green.sent) != 1 {
		t.Fatalf("expected exactly one broadcast to Green, got %d", len(green.sent))
	}
}

func TestHostAppliesGuestMove(t *testing.T) {
	m, green := newTestHost()
	m.game.Turn = 1
	m.game.SetDice(6) // legal yard-exit for every one of Green's tokens

	m, _ = m.applyGuestAction(guestActionMsg{seat: Green, action: guestAction{Kind: "move", TokenID: 0}})
	if m.game.Players[1].Tokens[0].State != OnTrack {
		t.Fatalf("expected Green's token 0 to have exited the yard, got state=%v", m.game.Players[1].Tokens[0].State)
	}
	if len(green.sent) == 0 {
		t.Fatal("expected the host to broadcast the new state")
	}
}

func TestHostIgnoresActionFromWrongSeat(t *testing.T) {
	m, green := newTestHost()
	// It's Red's (the host's own) turn, not Green's.
	m.game.Turn = 0
	before := m.game.Dice

	m, cmd := m.applyGuestAction(guestActionMsg{seat: Green, action: guestAction{Kind: "roll"}})
	if m.game.Dice != before {
		t.Error("expected a stale action from the wrong seat to be ignored, not applied")
	}
	if len(green.sent) != 0 {
		t.Error("expected no broadcast for an ignored stale action")
	}
	if cmd == nil {
		t.Fatal("expected to keep listening on Green's session")
	}
}

func TestHostEndsGameWhenGuestLeaves(t *testing.T) {
	m, _ := newTestHost()
	m, cmd := m.applyGuestAction(guestActionMsg{seat: Green, left: true})
	if !m.done {
		t.Fatal("expected the match to end when a guest leaves")
	}
	if m.resultText != "@guest left the game" {
		t.Errorf("resultText = %q, want %q", m.resultText, "@guest left the game")
	}
	findGameOverMsg(t, cmd)
}

func TestGuestReplacesStateFromHostBroadcast(t *testing.T) {
	sess := newFakeSession()
	m := NewGuest("@guest", sess)
	if m.game != nil {
		t.Fatal("expected a guest to start with no game until the first broadcast")
	}

	hostGame := NewGame([]*Player{NewPlayer(Red, "@host", false), NewPlayer(Green, "@guest", false)})
	hostGame.Turn = 1
	hostGame.Dice = 4

	m, cmd := m.applyHostState(hostStateMsg{state: hostState{Game: hostGame, StatusLine: "@host rolled 4"}})
	if m.game == nil {
		t.Fatal("expected the guest's game to be populated from the broadcast")
	}
	if m.game.Dice != 4 || m.statusLine != "@host rolled 4" {
		t.Errorf("guest state = dice:%d status:%q, want dice:4 status:%q", m.game.Dice, m.statusLine, "@host rolled 4")
	}
	if mySeat := m.mySeatColor(); mySeat != Green {
		t.Errorf("mySeatColor() = %v, want Green (matched by handle)", mySeat)
	}
	if cmd == nil {
		t.Fatal("expected the guest to keep listening for the next broadcast")
	}
}

func TestGuestEndsGameWhenHostIsLost(t *testing.T) {
	sess := newFakeSession()
	m := NewGuest("@guest", sess)

	m, cmd := m.applyHostState(hostStateMsg{lost: true, reason: "connection to the host was lost"})
	if !m.done || m.resultText != "connection to the host was lost" {
		t.Errorf("expected the match to end with the lost reason, got done=%v resultText=%q", m.done, m.resultText)
	}
	findGameOverMsg(t, cmd)
}

func TestGuestRollSendsActionWithoutMutatingLocalState(t *testing.T) {
	sess := newFakeSession()
	m := NewGuest("@guest", sess)
	hostGame := NewGame([]*Player{NewPlayer(Red, "@host", false), NewPlayer(Green, "@guest", false)})
	hostGame.Turn = 1 // Green's turn
	m.game = hostGame

	before := m.game.Dice
	m, _ = m.roll()
	if m.game.Dice != before {
		t.Error("expected a guest's roll to never mutate its local game -- only the host's RollDice is authoritative")
	}
	if len(sess.sent) != 1 || sess.sent[0] != `{"kind":"roll"}` {
		t.Errorf("sent = %v, want a single roll action", sess.sent)
	}
}

func TestGuestMoveValidatesLegalityLocallyBeforeSending(t *testing.T) {
	sess := newFakeSession()
	m := NewGuest("@guest", sess)
	hostGame := NewGame([]*Player{NewPlayer(Red, "@host", false), NewPlayer(Green, "@guest", false)})
	hostGame.Turn = 1
	hostGame.Dice = 3 // all Green's tokens in the yard: no legal move for a non-6
	m.game = hostGame

	m, _ = m.chooseToken(0)
	if len(sess.sent) != 0 {
		t.Error("expected an illegal move to never be sent to the host")
	}
	if m.warning == "" {
		t.Error("expected a warning for the illegal move")
	}
}

func TestQuitAsHostResignsAllGuestsAndEndsMatch(t *testing.T) {
	m, green := newTestHost()
	m, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	if !green.resigned {
		t.Error("expected the host to resign every guest session on quit")
	}
	if !m.done || m.resultText != "you left the game" {
		t.Errorf("done=%v resultText=%q, want done=true resultText=%q", m.done, m.resultText, "you left the game")
	}
	findGameOverMsg(t, cmd)
}

func TestQuitAsGuestResignsToHost(t *testing.T) {
	sess := newFakeSession()
	m := NewGuest("@guest", sess)

	m, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	if !sess.resigned {
		t.Error("expected the guest to resign its session to the host on quit")
	}
	if !m.done {
		t.Error("expected the match to end")
	}
	findGameOverMsg(t, cmd)
}

func TestUpdateIgnoresStrayMessagesOnceGameIsDone(t *testing.T) {
	sess := newFakeSession()
	m := NewGuest("@guest", sess)
	hostGame := NewGame([]*Player{NewPlayer(Red, "@host", false), NewPlayer(Green, "@guest", false)})
	m.game = hostGame

	m, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEsc}) // "you left the game"
	if !m.done || m.resultText != "you left the game" {
		t.Fatalf("setup: expected the quit to already be recorded, got done=%v resultText=%q", m.done, m.resultText)
	}

	// A waitForHostState armed before the quit can still deliver one more
	// message -- e.g. the disconnect our own Close() causes on the host's
	// end. That must not overwrite "you left the game" with a second,
	// contradictory GameOverMsg.
	m, cmd := m.Update(hostStateMsg{lost: true, reason: "connection to the host was lost"})
	if cmd != nil {
		t.Errorf("expected no cmd once the game is already done, got %v", cmd())
	}
	if m.resultText != "you left the game" {
		t.Errorf("resultText = %q, want the original %q to survive a stray post-game message", m.resultText, "you left the game")
	}
}

func TestFinishPersonalizesResultPerSeatBothRoles(t *testing.T) {
	// Host's own seat (Red) wins.
	m, _ := newTestHost()
	w := Red
	m.game.Winner = &w
	m.game.Phase = PhaseGameOver
	m, _ = m.finish()
	if m.resultText != "you win!" {
		t.Errorf("host perspective: resultText = %q, want %q", m.resultText, "you win!")
	}

	// From the guest's perspective, the same win reads as "@host wins".
	sess := newFakeSession()
	gm := NewGuest("@guest", sess)
	hostGame := NewGame([]*Player{NewPlayer(Red, "@host", false), NewPlayer(Green, "@guest", false)})
	hostGame.Winner = &w
	hostGame.Phase = PhaseGameOver
	gm.game = hostGame
	gm, _ = gm.finish()
	if gm.resultText != "@host wins" {
		t.Errorf("guest perspective: resultText = %q, want %q", gm.resultText, "@host wins")
	}
}
