package ludo

import "fmt"

// TokenState is where a token currently sits.
type TokenState int

const (
	InYard TokenState = iota
	OnTrack
	InHomeStretch
	Finished
)

// Token is one of a player's four pieces.
type Token struct {
	ID    int // 0-3, index into the owning Player's Tokens array
	Color Color
	State TokenState

	// Position's meaning depends on State:
	//   InYard:        unused, always 0.
	//   OnTrack:       relative position 0 to SharedSquares-1 on the shared
	//                  track; GlobalIndex(Color, Position) gives the
	//                  absolute square.
	//   InHomeStretch: relative position SharedSquares to FinishPos-1,
	//                  private to Color.
	//   Finished:      always FinishPos.
	Position int
}

// Player is one seat at the board.
type Player struct {
	Color  Color
	Name   string
	IsAI   bool
	Tokens [TokensPerPlayer]*Token
}

// NewPlayer builds a player with all tokens starting in the Yard.
func NewPlayer(color Color, name string, isAI bool) *Player {
	p := &Player{Color: color, Name: name, IsAI: isAI}
	for i := range p.Tokens {
		p.Tokens[i] = &Token{ID: i, Color: color, State: InYard}
	}
	return p
}

// hasWon reports whether every one of the player's tokens has reached Home.
func (p *Player) hasWon() bool {
	for _, t := range p.Tokens {
		if t.State != Finished {
			return false
		}
	}
	return true
}

// Phase identifies where in a turn the game currently is.
type Phase int

const (
	// PhaseRollDice: waiting for the current player to roll.
	PhaseRollDice Phase = iota
	// PhaseSelectToken: a roll landed with at least one legal move; waiting
	// for the current player to pick which token moves.
	PhaseSelectToken
	// PhaseGameOver: Winner is set, no further moves are accepted.
	PhaseGameOver
)

// Game holds the full state of one Ludo match. All mutation goes through
// its methods (RollDice/SetDice, MoveToken) so game rules stay in one
// place, independent of however a UI or network layer chooses to drive it.
type Game struct {
	Players []*Player
	Turn    int // index into Players
	Dice    int
	Phase   Phase
	Winner  *Color

	consecutiveSixes int
}

// NewGame starts a fresh match for the given players, in the order they'll
// take turns. len(players) must be 2-4; standard Ludo colors not
// represented simply never get a turn.
func NewGame(players []*Player) *Game {
	return &Game{
		Players: players,
		Phase:   PhaseRollDice,
	}
}

// CurrentPlayer returns whoever's turn it is.
func (g *Game) CurrentPlayer() *Player {
	return g.Players[g.Turn]
}

// Roller supplies dice values. *rand.Rand satisfies this via its Intn
// method; tests inject a deterministic fake instead.
type Roller interface {
	Intn(n int) int
}

// RollDice rolls the die for the current player and applies the result
// (see applyRoll), returning the value rolled.
func (g *Game) RollDice(r Roller) int {
	val := r.Intn(6) + 1
	g.applyRoll(val)
	return val
}

// SetDice force-sets the die value and runs the same phase transition
// RollDice would have. Exported so tests (and, potentially, deterministic
// replay/network sync) can drive specific scenarios without randomness.
func (g *Game) SetDice(val int) {
	if val < 1 || val > 6 {
		panic(fmt.Sprintf("ludo: die value out of range: %d", val))
	}
	g.applyRoll(val)
}

func (g *Game) applyRoll(val int) {
	if g.Phase == PhaseGameOver {
		return
	}
	g.Dice = val

	if val == YardExitRoll {
		g.consecutiveSixes++
	} else {
		g.consecutiveSixes = 0
	}

	// Three consecutive sixes forfeits the whole turn immediately, with no
	// move allowed on this roll -- the standard rule that keeps a lucky
	// streak from letting one player run away unchecked.
	if g.consecutiveSixes == 3 {
		g.consecutiveSixes = 0
		g.endTurn(false)
		return
	}

	if len(g.LegalMoves()) == 0 {
		g.endTurn(val == YardExitRoll)
		return
	}
	g.Phase = PhaseSelectToken
}

// LegalMoves returns the indices (0-3) of the current player's tokens that
// can legally move with the current dice value.
func (g *Game) LegalMoves() []int {
	p := g.CurrentPlayer()
	var moves []int
	for _, t := range p.Tokens {
		if g.canMove(t) {
			moves = append(moves, t.ID)
		}
	}
	return moves
}

func (g *Game) canMove(t *Token) bool {
	switch t.State {
	case InYard:
		return g.Dice == YardExitRoll
	case OnTrack, InHomeStretch:
		return t.Position+g.Dice <= FinishPos
	default: // Finished
		return false
	}
}

// MoveToken moves the current player's token tokenID by the current dice
// value: out of the Yard if it's sitting there and a 6 was rolled, or
// forward along the track/home stretch otherwise. A capture happens if the
// token lands on a non-safe shared-track square occupied by an opponent.
// Advances to the next phase/player per standard rules (extra roll on a 6,
// unless it was the third consecutive one -- see applyRoll).
func (g *Game) MoveToken(tokenID int) (captured bool, err error) {
	if g.Phase != PhaseSelectToken {
		return false, fmt.Errorf("ludo: no token selection expected in phase %v", g.Phase)
	}
	p := g.CurrentPlayer()
	if tokenID < 0 || tokenID >= len(p.Tokens) {
		return false, fmt.Errorf("ludo: invalid token id %d", tokenID)
	}
	t := p.Tokens[tokenID]
	if !g.canMove(t) {
		return false, fmt.Errorf("ludo: token %d has no legal move for a roll of %d", tokenID, g.Dice)
	}

	if t.State == InYard {
		t.State = OnTrack
		t.Position = 0
	} else {
		t.Position += g.Dice
	}

	switch {
	case t.Position == FinishPos:
		t.State = Finished
	case t.Position >= SharedSquares:
		t.State = InHomeStretch
	default:
		t.State = OnTrack
	}

	if t.State == OnTrack {
		captured = g.capture(t)
	}

	if p.hasWon() {
		w := p.Color
		g.Winner = &w
		g.Phase = PhaseGameOver
		return captured, nil
	}

	g.endTurn(g.Dice == YardExitRoll)
	return captured, nil
}

// wouldCaptureAt reports whether color c landing at relative position pos
// would capture an opponent token there, without actually moving or
// mutating anything -- used by AI move selection to compare legal moves
// before committing to one via MoveToken.
func (g *Game) wouldCaptureAt(c Color, pos int) bool {
	if pos >= SharedSquares {
		return false // the home stretch is private; nothing to capture there
	}
	global := GlobalIndex(c, pos)
	if IsSafeSquare(global) {
		return false
	}
	for _, other := range g.TokensOnSquare(global) {
		if other.Color != c {
			return true
		}
	}
	return false
}

// TokensOnSquare returns every token, from any player, currently occupying
// global track square idx.
func (g *Game) TokensOnSquare(idx int) []*Token {
	var found []*Token
	for _, p := range g.Players {
		for _, t := range p.Tokens {
			if t.State == OnTrack && GlobalIndex(t.Color, t.Position) == idx {
				found = append(found, t)
			}
		}
	}
	return found
}

// capture sends any opposing tokens sharing t's current global square back
// to their Yard, unless that square is safe. A player's own tokens simply
// stack together and are never captured by each other.
func (g *Game) capture(t *Token) bool {
	global := GlobalIndex(t.Color, t.Position)
	if IsSafeSquare(global) {
		return false
	}

	captured := false
	for _, other := range g.TokensOnSquare(global) {
		if other.Color == t.Color {
			continue
		}
		other.State = InYard
		other.Position = 0
		captured = true
	}
	return captured
}

func (g *Game) endTurn(extraRoll bool) {
	if extraRoll {
		g.Phase = PhaseRollDice
		return
	}
	g.consecutiveSixes = 0
	g.Turn = (g.Turn + 1) % len(g.Players)
	g.Phase = PhaseRollDice
}
