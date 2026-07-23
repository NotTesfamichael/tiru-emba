package ludo

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	ludoColors = map[Color]lipgloss.Color{
		Red:    lipgloss.Color("#F85149"),
		Green:  lipgloss.Color("#3FB950"),
		Yellow: lipgloss.Color("#E3B341"),
		Blue:   lipgloss.Color("#58A6FF"),
	}
	mutedColor  = lipgloss.Color("#484f58")
	accentColor = lipgloss.Color("#7D56F4")
	warnColor   = lipgloss.Color("#F85149")

	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FAFAFA")).
			Background(accentColor).
			Padding(0, 1)

	cellStyle    = lipgloss.NewStyle().Width(3).Align(lipgloss.Center)
	statusStyle  = lipgloss.NewStyle().Foreground(mutedColor).Italic(true)
	warningStyle = lipgloss.NewStyle().Foreground(warnColor)
	hintStyle    = lipgloss.NewStyle().Foreground(mutedColor)
)

func colorFor(c Color) lipgloss.Color { return ludoColors[c] }

// cellKind distinguishes what's statically at a grid coordinate (which
// never changes during a game) from what token, if any, currently occupies
// it (which does).
type cellKind int

const (
	cellTrack cellKind = iota
	cellHomeStretch
	cellYard
)

type cellInfo struct {
	kind  cellKind
	owner Color // meaningful for cellHomeStretch/cellYard
	safe  bool  // cellTrack only
}

// boardGeometry maps every (row, col) on the grid that's part of the
// board -- track, home stretch, or yard slot -- to what's there. Computed
// once at package init since it's pure geometry, identical for every game.
var boardGeometry = buildBoardGeometry()

func buildBoardGeometry() map[[2]int]cellInfo {
	m := make(map[[2]int]cellInfo, TrackSquares+4*(HomeStretchSquares+TokensPerPlayer))
	for g := 0; g < TrackSquares; g++ {
		row, col := TrackCellCoord(g)
		m[[2]int{row, col}] = cellInfo{kind: cellTrack, safe: IsSafeSquare(g)}
	}
	for _, c := range []Color{Red, Green, Yellow, Blue} {
		for i := 0; i < HomeStretchSquares; i++ {
			row, col := HomeStretchCellCoord(c, i)
			m[[2]int{row, col}] = cellInfo{kind: cellHomeStretch, owner: c}
		}
		for i := 0; i < TokensPerPlayer; i++ {
			row, col := YardSlotCoord(c, i)
			m[[2]int{row, col}] = cellInfo{kind: cellYard, owner: c}
		}
	}
	return m
}

// glyph is the 2-character label for token t: its color's initial plus its
// 1-based token number, e.g. "R1", "G4".
func glyph(t *Token) string {
	initials := "RGYB"
	return fmt.Sprintf("%c%d", initials[t.Color], t.ID+1)
}

// occupancyGrid maps every (row, col) currently holding a token to that
// token. Finished tokens hold no grid cell -- they're tallied in the side
// panel instead, mirroring how the classic board's center triangle isn't
// really subdivided into individual resting spots either.
func occupancyGrid(g *Game) map[[2]int]*Token {
	occ := make(map[[2]int]*Token)
	for _, p := range g.Players {
		for _, t := range p.Tokens {
			var row, col int
			switch t.State {
			case OnTrack:
				row, col = TrackCellCoord(GlobalIndex(t.Color, t.Position))
			case InHomeStretch:
				row, col = HomeStretchCellCoord(t.Color, t.Position-SharedSquares)
			case InYard:
				row, col = YardSlotCoord(t.Color, t.ID)
			default: // Finished
				continue
			}
			occ[[2]int{row, col}] = t
		}
	}
	return occ
}

// renderBoard draws the full GridSize x GridSize cross-shaped board.
func (m Model) renderBoard() string {
	occ := occupancyGrid(m.game)
	rows := make([]string, GridSize)
	for r := 0; r < GridSize; r++ {
		cells := make([]string, GridSize)
		for c := 0; c < GridSize; c++ {
			cells[c] = m.renderCell(r, c, occ)
		}
		rows[r] = lipgloss.JoinHorizontal(lipgloss.Top, cells...)
	}
	return lipgloss.JoinVertical(lipgloss.Left, rows...)
}

func (m Model) renderCell(row, col int, occ map[[2]int]*Token) string {
	key := [2]int{row, col}
	info, isBoardCell := boardGeometry[key]
	tok, occupied := occ[key]

	style := cellStyle
	content := " "

	switch {
	case occupied:
		content = glyph(tok)
		style = style.Foreground(colorFor(tok.Color)).Bold(true)
		if choices := m.selectableTokens(); m.game.Phase == PhaseSelectToken && m.cursor < len(choices) &&
			tok.Color == m.game.CurrentPlayer().Color && tok.ID == choices[m.cursor] {
			style = style.Underline(true)
		}
	case isBoardCell && info.kind == cellTrack && info.safe:
		content = "*"
		style = style.Foreground(mutedColor)
	case isBoardCell && (info.kind == cellHomeStretch || info.kind == cellYard):
		content = "."
		style = style.Foreground(colorFor(info.owner))
	case isBoardCell && info.kind == cellTrack:
		content = "."
		style = style.Foreground(mutedColor)
	case row >= 6 && row <= 8 && col >= 6 && col <= 8:
		content = "H"
		style = style.Foreground(accentColor)
	default:
		content = " "
		style = style.Foreground(mutedColor)
	}

	return style.Render(content)
}

func (m Model) renderSidePanel() string {
	var b strings.Builder

	for _, p := range m.game.Players {
		name := p.Name
		if p.IsAI {
			name += " (AI)"
		}
		line := fmt.Sprintf("%s %s", glyph(&Token{Color: p.Color}), name)
		style := lipgloss.NewStyle().Foreground(colorFor(p.Color))
		if p.Color == m.game.CurrentPlayer().Color && m.game.Phase != PhaseGameOver {
			style = style.Bold(true).Underline(true)
		}
		finished := 0
		for _, t := range p.Tokens {
			if t.State == Finished {
				finished++
			}
		}
		b.WriteString(style.Render(line))
		b.WriteString(statusStyle.Render(fmt.Sprintf("  %d/%d home", finished, TokensPerPlayer)))
		b.WriteString("\n")
	}
	b.WriteString("\n")

	switch {
	case m.game.Phase == PhaseGameOver:
		b.WriteString(m.resultText)
	case m.game.CurrentPlayer().IsAI:
		b.WriteString(statusStyle.Render(fmt.Sprintf("%s is thinking...", m.game.CurrentPlayer().Name)))
	case m.game.Phase == PhaseRollDice:
		b.WriteString(fmt.Sprintf("%s: press [space] to roll", m.game.CurrentPlayer().Name))
	case m.game.Phase == PhaseSelectToken:
		b.WriteString(renderDie(m.game.Dice))
		b.WriteString("\n")
		b.WriteString(m.renderTokenChoices())
	}
	b.WriteString("\n")

	if m.statusLine != "" {
		b.WriteString("\n")
		b.WriteString(statusStyle.Render(m.statusLine))
	}
	if m.warning != "" {
		b.WriteString("\n")
		b.WriteString(warningStyle.Render(m.warning))
	}

	b.WriteString("\n\n")
	if m.done {
		b.WriteString(hintStyle.Render("press any key to return to chat"))
	} else {
		b.WriteString(hintStyle.Render("[space] roll  [left/right] pick token  [enter] move  [esc] leave"))
	}

	return b.String()
}

// renderTokenChoices lists the current player's legal tokens for this
// roll, highlighting whichever one the cursor is on.
func (m Model) renderTokenChoices() string {
	choices := m.selectableTokens()
	if len(choices) == 0 {
		return ""
	}
	p := m.game.CurrentPlayer()
	parts := make([]string, len(choices))
	for i, id := range choices {
		style := lipgloss.NewStyle().Foreground(colorFor(p.Color))
		if i == m.cursor {
			style = style.Bold(true).Underline(true)
		}
		parts[i] = style.Render(glyph(p.Tokens[id]))
	}
	return "move: " + strings.Join(parts, "  ")
}

// dieFaces are simple 3x3 ASCII dice faces, indexed 1-6.
var dieFaces = [7][3]string{
	{},
	{"     ", "  o  ", "     "},
	{"o    ", "     ", "    o"},
	{"o    ", "  o  ", "    o"},
	{"o   o", "     ", "o   o"},
	{"o   o", "  o  ", "o   o"},
	{"o   o", "o   o", "o   o"},
}

func renderDie(value int) string {
	if value < 1 || value > 6 {
		return ""
	}
	face := dieFaces[value]
	style := lipgloss.NewStyle().Border(lipgloss.NormalBorder()).Padding(0, 1)
	return style.Render(lipgloss.JoinVertical(lipgloss.Left, face[0], face[1], face[2]))
}

func (m Model) View() string {
	title := titleStyle.Render(fmt.Sprintf(" Ludo — %s ", m.self))

	if m.game == nil {
		// A guest before the host's first broadcast arrives.
		lines := []string{title, "", statusStyle.Render("waiting for the host to start the game...")}
		if m.done {
			lines = append(lines, "", hintStyle.Render("press any key to return to chat"))
		} else {
			lines = append(lines, "", hintStyle.Render("[esc] leave"))
		}
		return lipgloss.JoinVertical(lipgloss.Left, lines...)
	}

	board := m.renderBoard()
	panel := m.renderSidePanel()

	body := lipgloss.JoinHorizontal(lipgloss.Top, board, "   ", panel)
	return lipgloss.JoinVertical(lipgloss.Left, title, "", body)
}
