package ludo

// ChooseAIMove picks which of the current player's legal tokens to move for
// the current dice roll. Priority order: capture an opponent if any legal
// move lands on one, else bring a fresh token out of the Yard (more tokens
// in play beats advancing one further), else advance whichever of your own
// tokens is furthest along (finishing tokens beats spreading progress
// thin), else whatever's legal first.
//
// Panics if called with no legal moves -- callers are expected to check
// Game.Phase == PhaseSelectToken first, same precondition MoveToken has.
func ChooseAIMove(g *Game) int {
	moves := g.LegalMoves()
	if len(moves) == 0 {
		panic("ludo: ChooseAIMove called with no legal moves")
	}

	p := g.CurrentPlayer()

	for _, id := range moves {
		t := p.Tokens[id]
		if t.State == InYard {
			continue // a yard-exit always lands on your own safe start square
		}
		if g.wouldCaptureAt(t.Color, t.Position+g.Dice) {
			return id
		}
	}

	for _, id := range moves {
		if p.Tokens[id].State == InYard {
			return id
		}
	}

	best := moves[0]
	for _, id := range moves[1:] {
		if p.Tokens[id].Position > p.Tokens[best].Position {
			best = id
		}
	}
	return best
}
