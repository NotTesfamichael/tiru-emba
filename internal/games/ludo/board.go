// Package ludo implements a 2-to-4-player Ludo game as a Bubble Tea
// sub-model. Unlike tictactoe, it starts out local-only: every player is
// either the person at the keyboard or a simple AI, all driven from one
// process. The engine (this file plus game.go) is deliberately kept free of
// any Bubble Tea or network dependency so it can be unit tested on its own
// and, later, wrapped by a networked Session the same way tictactoe is.
package ludo

// Color identifies one of the four players/quadrants. Turn order always
// proceeds Red -> Green -> Yellow -> Blue.
type Color int

const (
	Red Color = iota
	Green
	Yellow
	Blue
)

func (c Color) String() string {
	switch c {
	case Red:
		return "Red"
	case Green:
		return "Green"
	case Yellow:
		return "Yellow"
	case Blue:
		return "Blue"
	default:
		return "?"
	}
}

const (
	// TrackSquares is the length of the single shared outer track every
	// color's tokens walk around before turning off into their own home
	// stretch.
	TrackSquares = 52

	// HomeStretchSquares is the length of each color's private run-in, not
	// shared with any other color.
	HomeStretchSquares = 5

	// SharedSquares is how many of a token's relative positions (0-based)
	// are spent on the shared track before it turns into the home stretch:
	// positions 0-50 inclusive, 51 squares in total. A token's own start
	// square counts as position 0, so it never re-crosses it on the way
	// around.
	SharedSquares = TrackSquares - 1

	// FinishPos is the relative position representing having reached the
	// center Home/Goal exactly. Relative positions 0-50 are on the shared
	// track, 51-55 are the home stretch, and 56 (FinishPos) is the goal.
	FinishPos = SharedSquares + HomeStretchSquares

	// YardExitRoll is the die value required to bring a token out of the
	// Yard and onto its color's start square.
	YardExitRoll = 6

	// TokensPerPlayer is how many tokens start in each player's Yard.
	TokensPerPlayer = 4
)

// startOffset is each color's entry square on the shared track, spaced a
// quarter of the way around from each other (52/4 = 13).
var startOffset = [4]int{
	Red:    0,
	Green:  13,
	Yellow: 26,
	Blue:   39,
}

// StartSquare returns c's entry point on the shared 52-square track.
func StartSquare(c Color) int {
	return startOffset[c]
}

// safeSquares are the global track indices where a token can never be
// captured: every color's start square, plus one additional "star" square
// roughly a third of the way to the next color's start.
var safeSquares = buildSafeSquares()

func buildSafeSquares() map[int]bool {
	m := make(map[int]bool, len(startOffset)*2)
	for _, off := range startOffset {
		m[off] = true
		m[(off+8)%TrackSquares] = true
	}
	return m
}

// IsSafeSquare reports whether global (an index into the shared track, 0 to
// TrackSquares-1) is a safe square, protecting any token there from capture.
func IsSafeSquare(global int) bool {
	return safeSquares[global]
}

// GlobalIndex maps a token's relative position on the shared track (0 to
// SharedSquares-1) to its absolute square on that track. Positions in the
// home stretch (>= SharedSquares) have no meaningful global index -- that
// stretch is private to c and never shared with other colors.
func GlobalIndex(c Color, relPos int) int {
	return (startOffset[c] + relPos) % TrackSquares
}

// Board is a stateless view over the shared geometry of a Ludo board. The
// dynamic part -- which token currently sits where -- lives on the
// Players/Tokens in Game; Board only answers geometry questions that engine
// and UI code both need, so neither has to duplicate the track math.
type Board struct{}

func (Board) StartSquare(c Color) int             { return StartSquare(c) }
func (Board) IsSafeSquare(global int) bool        { return IsSafeSquare(global) }
func (Board) GlobalIndex(c Color, relPos int) int { return GlobalIndex(c, relPos) }

// The classic cross-shaped Ludo board maps naturally onto a 15x15 grid of
// cells: a 6x6 yard in each corner, a 3-wide arm connecting each yard to the
// 3x3 center Home, and every arm's middle lane reserved as one color's
// private home stretch. GridSize and gridCenter describe that grid;
// everything below derives real board coordinates for it from the abstract
// track math above, instead of hand-listing all 52+20+16 cells directly.
const (
	GridSize   = 15
	gridCenter = 7 // the (7,7) cell is the board's exact center
)

// rotateCW rotates (row, col) by quarter*90 degrees clockwise around the
// board's center cell. Red's quarter of the board is quarter 0 (no
// rotation); Green/Yellow/Blue are 1/2/3, exactly matching both Color's
// iota values and startOffset's spacing of one quarter of the track (13 of
// 52 squares) per color -- so the same quarter number derived from a
// global track index also picks the right rotation of Red's geometry to
// get any other color's.
func rotateCW(row, col, quarter int) (int, int) {
	dr, dc := row-gridCenter, col-gridCenter
	for i := 0; i < quarter%4; i++ {
		dr, dc = dc, -dr
	}
	return gridCenter + dr, gridCenter + dc
}

// trackQuarterBase is Red's 13-cell quarter of the shared track (global
// indices 0-12), in path order: rightward along the top of the left arm,
// up the middle column of the top arm, across its top edge. Every other
// color's quarter is this exact shape, rotated.
var trackQuarterBase = [13][2]int{
	{6, 1}, {6, 2}, {6, 3}, {6, 4}, {6, 5},
	{5, 6}, {4, 6}, {3, 6}, {2, 6}, {1, 6}, {0, 6},
	{0, 7},
	{0, 8},
}

// homeStretchBase is Red's home-stretch cells (relative positions
// SharedSquares..FinishPos-1), ordered from the shared track toward the
// center. Every other color's is this rotated the same way as its track
// quarter.
var homeStretchBase = [HomeStretchSquares][2]int{
	{7, 1}, {7, 2}, {7, 3}, {7, 4}, {7, 5},
}

// yardSlotBase is Red's 4 yard slots (one fixed slot per token ID). Every
// other color's is this rotated the same way as its track quarter.
var yardSlotBase = [TokensPerPlayer][2]int{
	{1, 1}, {1, 2}, {2, 1}, {2, 2},
}

// TrackCellCoord returns the (row, col) on the GridSize x GridSize board
// grid for shared-track square global (0 to TrackSquares-1).
func TrackCellCoord(global int) (row, col int) {
	quarter := global / 13
	base := trackQuarterBase[global%13]
	return rotateCW(base[0], base[1], quarter)
}

// HomeStretchCellCoord returns the (row, col) for color c's home-stretch
// cell at stretchIdx (0-based: 0 is closest to the shared track,
// HomeStretchSquares-1 is closest to the center).
func HomeStretchCellCoord(c Color, stretchIdx int) (row, col int) {
	base := homeStretchBase[stretchIdx]
	return rotateCW(base[0], base[1], int(c))
}

// YardSlotCoord returns the (row, col) for color c's yard slot (0 to
// TokensPerPlayer-1) -- token ID i always sits at slot i while InYard.
func YardSlotCoord(c Color, slot int) (row, col int) {
	base := yardSlotBase[slot]
	return rotateCW(base[0], base[1], int(c))
}
