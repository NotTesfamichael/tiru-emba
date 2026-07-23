package ludo

import "testing"

func TestTrackCellCoordSpotChecks(t *testing.T) {
	cases := []struct {
		global   int
		row, col int
	}{
		{0, 6, 1},   // Red's start
		{13, 1, 8},  // Green's start (Red's start rotated 90cw)
		{26, 8, 13}, // Yellow's start (rotated 180)
		{39, 13, 6}, // Blue's start (rotated 270)
		{50, 7, 0},  // Red's last shared square, right before its home stretch
		{51, 6, 0},  // wraps back to the cell just before Red's own start
	}
	for _, c := range cases {
		row, col := TrackCellCoord(c.global)
		if row != c.row || col != c.col {
			t.Errorf("TrackCellCoord(%d) = (%d,%d), want (%d,%d)", c.global, row, col, c.row, c.col)
		}
	}
}

func TestTrackCellCoordAllDistinctAndInBounds(t *testing.T) {
	seen := make(map[[2]int]int)
	for g := 0; g < TrackSquares; g++ {
		row, col := TrackCellCoord(g)
		if row < 0 || row >= GridSize || col < 0 || col >= GridSize {
			t.Errorf("TrackCellCoord(%d) = (%d,%d) out of bounds", g, row, col)
		}
		key := [2]int{row, col}
		if prev, ok := seen[key]; ok {
			t.Errorf("TrackCellCoord(%d) and (%d) collide at (%d,%d)", g, prev, row, col)
		}
		seen[key] = g
	}
	if len(seen) != TrackSquares {
		t.Errorf("expected %d distinct cells, got %d", TrackSquares, len(seen))
	}
}

func TestHomeStretchAndYardCoordsDistinctPerColorAndInBounds(t *testing.T) {
	occupied := make(map[[2]int]string)
	record := func(label string, row, col int) {
		t.Helper()
		if row < 0 || row >= GridSize || col < 0 || col >= GridSize {
			t.Errorf("%s = (%d,%d) out of bounds", label, row, col)
		}
		key := [2]int{row, col}
		if prev, ok := occupied[key]; ok {
			t.Errorf("%s collides with %s at (%d,%d)", label, prev, row, col)
		}
		occupied[key] = label
	}

	for g := 0; g < TrackSquares; g++ {
		row, col := TrackCellCoord(g)
		record("track square", row, col)
	}
	for _, c := range []Color{Red, Green, Yellow, Blue} {
		for i := 0; i < HomeStretchSquares; i++ {
			row, col := HomeStretchCellCoord(c, i)
			record(c.String()+" home stretch", row, col)
		}
		for i := 0; i < TokensPerPlayer; i++ {
			row, col := YardSlotCoord(c, i)
			record(c.String()+" yard", row, col)
		}
	}
}

func TestHomeStretchLeadsTowardCenter(t *testing.T) {
	// Each color's home stretch should monotonically approach the center
	// cell (7,7) as stretchIdx increases, ending adjacent to it.
	for _, c := range []Color{Red, Green, Yellow, Blue} {
		var lastDist int
		for i := 0; i < HomeStretchSquares; i++ {
			row, col := HomeStretchCellCoord(c, i)
			dist := abs(row-gridCenter) + abs(col-gridCenter)
			if i > 0 && dist >= lastDist {
				t.Errorf("%s home stretch cell %d is not closer to center than cell %d", c, i, i-1)
			}
			lastDist = dist
		}
	}
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
