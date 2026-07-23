package ludo

import "testing"

// fakeRoller replays a fixed sequence of die values, so tests can drive
// exact scenarios instead of depending on real randomness.
type fakeRoller struct {
	rolls []int
	next  int
}

func (f *fakeRoller) Intn(int) int {
	v := f.rolls[f.next]
	f.next++
	return v - 1 // RollDice adds 1 back; Intn(6) is 0-5
}

func newTwoPlayerGame() *Game {
	red := NewPlayer(Red, "Red", false)
	green := NewPlayer(Green, "Green", false)
	return NewGame([]*Player{red, green})
}

func TestGlobalIndexWrapsAroundTrack(t *testing.T) {
	cases := []struct {
		color  Color
		relPos int
		want   int
	}{
		{Red, 0, 0},
		{Red, 51, 51 % TrackSquares}, // not reachable in play (max shared relPos is 50) but math must still wrap
		{Green, 0, 13},
		{Yellow, 0, 26},
		{Blue, 0, 39},
		{Blue, 20, (39 + 20) % TrackSquares},   // 59 % 52 = 7, wraps past square 51 back to 7
		{Yellow, 30, (26 + 30) % TrackSquares}, // 56 % 52 = 4
	}
	for _, c := range cases {
		got := GlobalIndex(c.color, c.relPos)
		if got != c.want {
			t.Errorf("GlobalIndex(%v, %d) = %d, want %d", c.color, c.relPos, got, c.want)
		}
	}
}

func TestMoveTokenWrapsAroundSharedTrack(t *testing.T) {
	g := newTwoPlayerGame()
	blue := NewPlayer(Blue, "Blue", false)
	g.Players = append(g.Players, blue)

	// Drive Blue's token 0 from the Yard (global start 39) out past global
	// square 51 and around to square 2, one legal die value (1-6) at a
	// time. Resetting g.Turn before each SetDice keeps it Blue's turn
	// regardless of whether the previous roll happened to pass it on --
	// this test is only about the wrap-around math, not turn order.
	rolls := []int{6, 6, 5, 4} // exit yard, then +6, +5, +4 = relative position 15
	for _, roll := range rolls {
		g.Turn = 2
		g.SetDice(roll)
		if _, err := g.MoveToken(0); err != nil {
			t.Fatalf("MoveToken(roll=%d): %v", roll, err)
		}
	}

	tok := blue.Tokens[0]
	if tok.State != OnTrack || tok.Position != 15 {
		t.Fatalf("expected token on track at relative position 15, got state=%v pos=%d", tok.State, tok.Position)
	}
	if global := GlobalIndex(tok.Color, tok.Position); global != 2 {
		t.Fatalf("wrap-around: Blue at relative position 15 (39+15=54) should be global 2, got %d", global)
	}
}

func TestYardExitRequiresSix(t *testing.T) {
	g := newTwoPlayerGame()

	g.Dice = 4 // bypass SetDice so LegalMoves can be inspected before any turn transition
	if moves := g.LegalMoves(); len(moves) != 0 {
		t.Fatalf("expected no legal moves with a roll of 4 while all tokens are in the yard, got %v", moves)
	}

	g.SetDice(4)
	if g.Phase != PhaseRollDice || g.Turn != 1 {
		t.Fatalf("expected turn to pass to player 1 after a dead roll, got phase=%v turn=%d", g.Phase, g.Turn)
	}

	g.Turn = 0
	g.SetDice(6)
	if moves := g.LegalMoves(); len(moves) != TokensPerPlayer {
		t.Fatalf("expected all 4 tokens to have a legal yard-exit move on a 6, got %v", moves)
	}
	if _, err := g.MoveToken(0); err != nil {
		t.Fatalf("MoveToken: %v", err)
	}
	if red := g.Players[0].Tokens[0]; red.State != OnTrack || red.Position != 0 {
		t.Fatalf("expected token to be on track at position 0, got state=%v pos=%d", red.State, red.Position)
	}
}

func TestCaptureSendsOpponentTokenBackToYard(t *testing.T) {
	g := newTwoPlayerGame()
	red, green := g.Players[0], g.Players[1]

	// Place Green's token 0 on a non-safe shared square (relative position
	// 5 from Green's start, global 18 -- not a start or star square).
	green.Tokens[0].State = OnTrack
	green.Tokens[0].Position = 5

	// Bring Red's token 0 out and move it to the same global square.
	// Red's start is global 0; global 18 is relative position 18 for Red.
	red.Tokens[0].State = OnTrack
	red.Tokens[0].Position = 18 - 1 // one square short; next roll lands exactly on it
	g.Turn = 0
	g.SetDice(1)

	captured, err := g.MoveToken(0)
	if err != nil {
		t.Fatalf("MoveToken: %v", err)
	}
	if !captured {
		t.Fatalf("expected landing on Green's token to capture it")
	}
	if green.Tokens[0].State != InYard || green.Tokens[0].Position != 0 {
		t.Fatalf("expected captured token back in yard, got state=%v pos=%d", green.Tokens[0].State, green.Tokens[0].Position)
	}
	if red.Tokens[0].State != OnTrack || GlobalIndex(Red, red.Tokens[0].Position) != 18 {
		t.Fatalf("expected capturing token to land on global square 18")
	}
}

func TestSafeSquareBlocksCapture(t *testing.T) {
	g := newTwoPlayerGame()
	red, green := g.Players[0], g.Players[1]

	// Green's start square (global 13) is safe. Put Green's token 0 there,
	// and Red's token one square away so it lands exactly on 13.
	green.Tokens[0].State = OnTrack
	green.Tokens[0].Position = 0 // Green's start -> global 13

	red.Tokens[0].State = OnTrack
	red.Tokens[0].Position = 12 // Red's relative 13 = global 13, one roll of 1 away
	g.Turn = 0
	g.SetDice(1)

	captured, err := g.MoveToken(0)
	if err != nil {
		t.Fatalf("MoveToken: %v", err)
	}
	if captured {
		t.Fatalf("expected no capture on a safe square")
	}
	if green.Tokens[0].State != OnTrack {
		t.Fatalf("expected Green's token to remain on track, untouched")
	}
}

func TestOwnTokensNeverCaptureEachOther(t *testing.T) {
	g := newTwoPlayerGame()
	red := g.Players[0]

	red.Tokens[0].State = OnTrack
	red.Tokens[0].Position = 18
	red.Tokens[1].State = OnTrack
	red.Tokens[1].Position = 17
	g.Turn = 0
	g.SetDice(1)

	captured, err := g.MoveToken(1)
	if err != nil {
		t.Fatalf("MoveToken: %v", err)
	}
	if captured {
		t.Fatalf("expected stacking on your own token to never count as a capture")
	}
	if red.Tokens[0].State != OnTrack || red.Tokens[0].Position != 18 {
		t.Fatalf("expected the stationary token to be undisturbed")
	}
}

func TestSharedTrackToHomeStretchBoundary(t *testing.T) {
	g := newTwoPlayerGame()
	red := g.Players[0]
	red.Tokens[0].State = OnTrack
	red.Tokens[0].Position = SharedSquares - 2 // 2 short of the boundary

	g.Turn = 0
	g.SetDice(1) // lands exactly on SharedSquares-1, still the last shared square
	if _, err := g.MoveToken(0); err != nil {
		t.Fatalf("MoveToken: %v", err)
	}
	if red.Tokens[0].State != OnTrack || red.Tokens[0].Position != SharedSquares-1 {
		t.Fatalf("expected token still OnTrack at %d, got state=%v pos=%d", SharedSquares-1, red.Tokens[0].State, red.Tokens[0].Position)
	}

	g.Turn = 0
	g.SetDice(1) // one more step crosses into the home stretch
	if _, err := g.MoveToken(0); err != nil {
		t.Fatalf("MoveToken: %v", err)
	}
	if red.Tokens[0].State != InHomeStretch || red.Tokens[0].Position != SharedSquares {
		t.Fatalf("expected token InHomeStretch at %d, got state=%v pos=%d", SharedSquares, red.Tokens[0].State, red.Tokens[0].Position)
	}
}

func TestExactRollRequiredToFinish(t *testing.T) {
	g := newTwoPlayerGame()
	red := g.Players[0]
	red.Tokens[0].State = InHomeStretch
	red.Tokens[0].Position = FinishPos - 2

	g.Turn = 0
	g.SetDice(5) // overshoots FinishPos by 3
	if moves := g.LegalMoves(); len(moves) != 0 {
		t.Fatalf("expected overshooting Home to be illegal, got legal moves %v", moves)
	}

	g.Turn = 0
	g.SetDice(2) // exact
	if _, err := g.MoveToken(0); err != nil {
		t.Fatalf("MoveToken: %v", err)
	}
	if red.Tokens[0].State != Finished {
		t.Fatalf("expected token to finish on an exact roll, got state=%v", red.Tokens[0].State)
	}
}

func TestAllFourTokensFinishedWins(t *testing.T) {
	g := newTwoPlayerGame()
	red := g.Players[0]
	for i := 0; i < 3; i++ {
		red.Tokens[i].State = Finished
		red.Tokens[i].Position = FinishPos
	}
	red.Tokens[3].State = InHomeStretch
	red.Tokens[3].Position = FinishPos - 1

	g.Turn = 0
	g.SetDice(1)
	if _, err := g.MoveToken(3); err != nil {
		t.Fatalf("MoveToken: %v", err)
	}
	if g.Phase != PhaseGameOver {
		t.Fatalf("expected game over once all 4 tokens finish, got phase=%v", g.Phase)
	}
	if g.Winner == nil || *g.Winner != Red {
		t.Fatalf("expected Red to be recorded as the winner, got %v", g.Winner)
	}
}

func TestRollingSixGrantsAnExtraRoll(t *testing.T) {
	g := newTwoPlayerGame()
	g.SetDice(6)
	if _, err := g.MoveToken(0); err != nil {
		t.Fatalf("MoveToken: %v", err)
	}
	if g.Phase != PhaseRollDice || g.Turn != 0 {
		t.Fatalf("expected same player to roll again after a 6, got phase=%v turn=%d", g.Phase, g.Turn)
	}
}

func TestThreeConsecutiveSixesForfeitsTurn(t *testing.T) {
	g := newTwoPlayerGame()
	roller := &fakeRoller{rolls: []int{6, 6, 6}}

	g.RollDice(roller)
	if _, err := g.MoveToken(0); err != nil {
		t.Fatalf("MoveToken (1st six): %v", err)
	}
	g.RollDice(roller)
	if _, err := g.MoveToken(0); err != nil {
		t.Fatalf("MoveToken (2nd six): %v", err)
	}
	// Third consecutive six: turn is forfeited before reaching
	// PhaseSelectToken, so no move is possible.
	g.RollDice(roller)
	if g.Phase != PhaseRollDice || g.Turn != 1 {
		t.Fatalf("expected turn forfeited to player 1 after three sixes, got phase=%v turn=%d", g.Phase, g.Turn)
	}
}

func TestNoLegalMovePassesTurn(t *testing.T) {
	g := newTwoPlayerGame()
	// All tokens in the yard, roll something other than 6: nothing can move.
	g.SetDice(3)
	if g.Phase != PhaseRollDice {
		t.Fatalf("expected phase to fall back to PhaseRollDice, got %v", g.Phase)
	}
	if g.Turn != 1 {
		t.Fatalf("expected turn to pass to the next player, got %d", g.Turn)
	}
}
