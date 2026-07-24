package account

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/NotTesfamichael/tiru-emba/internal/relay"
)

var (
	colorAccent = lipgloss.Color("#7D56F4")
	colorMuted  = lipgloss.Color("#626262")
	colorError  = lipgloss.Color("#E5484D")
	colorOnline = lipgloss.Color("#3FB950")

	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FAFAFA")).
			Background(colorAccent).
			Padding(0, 1)

	sectionStyle        = lipgloss.NewStyle().Bold(true).Foreground(colorAccent)
	optionStyle         = lipgloss.NewStyle()
	selectedOptionStyle = lipgloss.NewStyle().Bold(true).Foreground(colorAccent)
	ownedStyle          = lipgloss.NewStyle().Foreground(colorOnline)
	errorStyle          = lipgloss.NewStyle().Foreground(colorError)
	hintStyle           = lipgloss.NewStyle().Foreground(colorMuted).Italic(true)
	containerStyle      = lipgloss.NewStyle().Padding(1, 2)
)

func (m Model) View() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Account"))
	b.WriteString("\n\n")

	if m.loading {
		b.WriteString("loading...")
		return containerStyle.Render(b.String())
	}

	for _, line := range m.summaryLines() {
		b.WriteString(line + "\n")
	}
	b.WriteString("\n" + sectionStyle.Render("Unlockables") + "\n\n")

	for i, u := range m.unlockables {
		b.WriteString(renderUnlockableRow(i == m.cursor, u) + "\n")
	}
	if len(m.unlockables) == 0 {
		b.WriteString(hintStyle.Render("nothing in the catalog yet"))
	}

	if m.err != "" {
		b.WriteString("\n" + errorStyle.Render(m.err))
	}
	b.WriteString("\n" + hintStyle.Render("up/down to browse, enter to redeem/equip, esc to go back"))
	return containerStyle.Render(b.String())
}

func renderUnlockableRow(selected bool, u relay.UnlockableInfo) string {
	cursor := "  "
	style := optionStyle
	if selected {
		cursor = "> "
		style = selectedOptionStyle
	}

	status := fmt.Sprintf("%d pts", u.Cost)
	switch {
	case u.Active:
		status = "equipped"
	case u.Owned:
		status = "owned -- enter to equip"
	default:
		status = fmt.Sprintf("%d pts -- enter to redeem", u.Cost)
	}
	if u.Owned {
		style = ownedStyle
		if selected {
			style = selectedOptionStyle
		}
	}

	line := fmt.Sprintf("%-10s %-8s %-6s  %s", u.Name, u.Kind, u.AsciiArt, status)
	return cursor + style.Render(line)
}
