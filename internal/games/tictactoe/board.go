// Package tictactoe implements a 2-player Tic-Tac-Toe game played over an
// already-established network.GameSession, as a Bubble Tea sub-model meant
// to be hosted by a router that swaps it in for the chat view.
package tictactoe

// Mark is what occupies a board cell.
type Mark int

const (
	Empty Mark = iota
	X
	O
)

func (m Mark) String() string {
	switch m {
	case X:
		return "X"
	case O:
		return "O"
	default:
		return " "
	}
}

// Other returns the opposing mark (Empty maps to Empty).
func (m Mark) Other() Mark {
	switch m {
	case X:
		return O
	case O:
		return X
	default:
		return Empty
	}
}

// board is a 3x3 grid, indexed row-major: row*3+col, 0-8.
type board [9]Mark

var winLines = [8][3]int{
	{0, 1, 2}, {3, 4, 5}, {6, 7, 8}, // rows
	{0, 3, 6}, {1, 4, 7}, {2, 5, 8}, // columns
	{0, 4, 8}, {2, 4, 6}, // diagonals
}

// winner returns the mark occupying any completed line, or Empty if there
// isn't one yet.
func (b board) winner() Mark {
	for _, line := range winLines {
		a, c, d := b[line[0]], b[line[1]], b[line[2]]
		if a != Empty && a == c && c == d {
			return a
		}
	}
	return Empty
}

// full reports whether every cell is occupied.
func (b board) full() bool {
	for _, m := range b {
		if m == Empty {
			return false
		}
	}
	return true
}
