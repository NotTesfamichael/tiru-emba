package ludo

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func key(s string) tea.KeyMsg {
	switch s {
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

func findGameOverMsg(t *testing.T, cmd tea.Cmd) GameOverMsg {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected a cmd, got nil")
	}
	msg, ok := cmd().(GameOverMsg)
	if !ok {
		t.Fatalf("cmd produced %T, want GameOverMsg", msg)
	}
	return msg
}

func TestQuitWorksEvenDuringAITurn(t *testing.T) {
	m := New("@you", 1) // Red (human) + Green (AI)
	m.game.Turn = 1     // force it to Green's turn

	m, cmd := m.handleKey(key("esc"))
	if !m.done {
		t.Fatal("expected esc to end the game even mid-AI-turn")
	}
	findGameOverMsg(t, cmd)
}

func TestOtherKeysIgnoredDuringAITurn(t *testing.T) {
	m := New("@you", 1)
	m.game.Turn = 1 // Green's (AI) turn
	wantPhase, wantDice := m.game.Phase, m.game.Dice

	m, cmd := m.handleKey(key(" "))
	if cmd != nil {
		t.Error("expected no cmd for input received during an AI turn")
	}
	if m.game.Phase != wantPhase || m.game.Dice != wantDice {
		t.Error("expected game state to be untouched by input during an AI turn")
	}
}

func TestHumanMoveViaNumberKey(t *testing.T) {
	m := New("@you", 1)
	m.game.SetDice(6) // deterministic, bypassing the RNG -- legal yard-exit for every token

	m, _ = m.handleKey(key("1"))
	if m.game.Players[0].Tokens[0].State != OnTrack {
		t.Fatalf("expected token 0 to have exited the yard, got state=%v", m.game.Players[0].Tokens[0].State)
	}
}

func TestHumanMoveViaCursorAndConfirm(t *testing.T) {
	m := New("@you", 1)
	m.game.SetDice(6)
	m.cursor = 0

	m, _ = m.handleKey(key("right")) // cycle to the next legal token
	m, _ = m.handleKey(key("enter"))

	moved := false
	for _, tok := range m.game.Players[0].Tokens {
		if tok.State == OnTrack {
			moved = true
		}
	}
	if !moved {
		t.Fatal("expected cursor + enter to move a token out of the yard")
	}
}

func TestFinishReportsWinnerCorrectly(t *testing.T) {
	m := New("@you", 1)
	red := m.game.Winner
	if red != nil {
		t.Fatal("setup: expected no winner yet")
	}
	w := Red
	m.game.Winner = &w
	m.game.Phase = PhaseGameOver

	m, cmd := m.finish()
	if !m.done {
		t.Fatal("expected finish to mark the game done")
	}
	msg := findGameOverMsg(t, cmd)
	if msg.ResultText != "you win!" {
		t.Errorf("ResultText = %q, want %q", msg.ResultText, "you win!")
	}
}

func TestFinishReportsAIWinnerByName(t *testing.T) {
	m := New("@you", 1)
	w := Green
	m.game.Winner = &w
	m.game.Phase = PhaseGameOver

	_, cmd := m.finish()
	msg := findGameOverMsg(t, cmd)
	if msg.ResultText != "Green wins" {
		t.Errorf("ResultText = %q, want %q", msg.ResultText, "Green wins")
	}
}
