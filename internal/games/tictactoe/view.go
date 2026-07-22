package tictactoe

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
)

var (
	colorAccent = lipgloss.Color("#7D56F4")
	colorMuted  = lipgloss.Color("#626262")
	colorX      = lipgloss.Color("#FFA657")
	colorO      = lipgloss.Color("#58A6FF")
	colorWarn   = lipgloss.Color("#F85149")

	gttTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FAFAFA")).
			Background(colorAccent).
			Padding(0, 1)

	cellStyle = lipgloss.NewStyle().
			Width(5).
			Height(3).
			Align(lipgloss.Center, lipgloss.Center).
			Border(lipgloss.NormalBorder()).
			BorderForeground(colorMuted)

	cursorCellStyle = cellStyle.BorderForeground(colorAccent).Bold(true)

	xMarkStyle = lipgloss.NewStyle().Foreground(colorX).Bold(true)
	oMarkStyle = lipgloss.NewStyle().Foreground(colorO).Bold(true)

	statusStyle  = lipgloss.NewStyle().Foreground(colorMuted).Italic(true)
	warningStyle = lipgloss.NewStyle().Foreground(colorWarn)
	hintStyle    = lipgloss.NewStyle().Foreground(colorMuted)
)

func (m Model) View() string {
	title := gttTitleStyle.Render(fmt.Sprintf(" Tic-Tac-Toe — %s (%s) vs %s (%s) ",
		m.self, m.mySymbol, m.opponent, m.mySymbol.Other()))

	grid := m.renderGrid()

	var status string
	switch {
	case m.done:
		status = m.resultText
	case m.turn == m.mySymbol:
		status = "your turn"
	default:
		status = fmt.Sprintf("waiting for %s...", m.opponent)
	}

	lines := []string{title, "", grid, "", statusStyle.Render(status)}
	if m.warning != "" {
		lines = append(lines, warningStyle.Render(m.warning))
	}
	if m.done {
		lines = append(lines, "", hintStyle.Render("press any key to return to chat"))
	} else {
		lines = append(lines, "", hintStyle.Render("arrows/WASD to move, enter to place, esc to resign"))
	}

	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

func (m Model) renderGrid() string {
	rows := make([]string, 3)
	for r := 0; r < 3; r++ {
		cells := make([]string, 3)
		for c := 0; c < 3; c++ {
			idx := r*3 + c
			cells[c] = m.renderCell(idx)
		}
		rows[r] = lipgloss.JoinHorizontal(lipgloss.Top, cells...)
	}
	return lipgloss.JoinVertical(lipgloss.Left, rows...)
}

func (m Model) renderCell(idx int) string {
	var content string
	switch m.board[idx] {
	case X:
		content = xMarkStyle.Render("X")
	case O:
		content = oMarkStyle.Render("O")
	default:
		content = " "
	}

	style := cellStyle
	if idx == m.cursor && !m.done {
		style = cursorCellStyle
	}
	return style.Render(content)
}
