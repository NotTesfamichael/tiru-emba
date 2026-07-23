package ludo

import "testing"

func TestChooseAIMovePrefersCapture(t *testing.T) {
	g := newTwoPlayerGame()
	red, green := g.Players[0], g.Players[1]

	// Green sits on a non-safe square Red can reach exactly with this
	// roll; Red also has another token that could move instead but
	// wouldn't capture anything.
	green.Tokens[0].State = OnTrack
	green.Tokens[0].Position = 5 // global 18

	red.Tokens[0].State = OnTrack
	red.Tokens[0].Position = 17 // one roll of 1 lands on global 18
	red.Tokens[1].State = OnTrack
	red.Tokens[1].Position = 10 // legal too, but capturees nothing

	g.Turn = 0
	g.SetDice(1)

	got := ChooseAIMove(g)
	if got != 0 {
		t.Errorf("ChooseAIMove = %d, want 0 (the capturing move)", got)
	}
}

func TestChooseAIMovePrefersYardExitOverAdvancing(t *testing.T) {
	g := newTwoPlayerGame()
	red := g.Players[0]

	// Token 0 is already on the track and could legally advance, but with
	// a 6 rolled, bringing a fresh token out of the yard should win.
	red.Tokens[0].State = OnTrack
	red.Tokens[0].Position = 10

	g.Turn = 0
	g.SetDice(6)

	got := ChooseAIMove(g)
	if red.Tokens[got].State != InYard {
		t.Errorf("ChooseAIMove = %d (state %v), want a yard-exit move", got, red.Tokens[got].State)
	}
}

func TestChooseAIMovePrefersFurthestTokenWhenNoCaptureOrYardExit(t *testing.T) {
	g := newTwoPlayerGame()
	red := g.Players[0]

	red.Tokens[0].State = OnTrack
	red.Tokens[0].Position = 5
	red.Tokens[1].State = OnTrack
	red.Tokens[1].Position = 20 // furthest along

	g.Turn = 0
	g.SetDice(3) // not a 6, so no yard-exit option muddies the choice

	got := ChooseAIMove(g)
	if got != 1 {
		t.Errorf("ChooseAIMove = %d, want 1 (the furthest-along token)", got)
	}
}

func TestChooseAIMovePanicsWithNoLegalMoves(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("expected ChooseAIMove to panic when there are no legal moves")
		}
	}()
	g := newTwoPlayerGame()
	g.Dice = 3 // all tokens in the yard, no legal moves for a non-6
	ChooseAIMove(g)
}
