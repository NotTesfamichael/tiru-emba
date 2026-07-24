package orgselect

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	colorAccent = lipgloss.Color("#7D56F4")
	colorMuted  = lipgloss.Color("#626262")
	colorError  = lipgloss.Color("#E5484D")

	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FAFAFA")).
			Background(colorAccent).
			Padding(0, 1)

	optionStyle         = lipgloss.NewStyle()
	selectedOptionStyle = lipgloss.NewStyle().Bold(true).Foreground(colorAccent)
	promptStyle         = lipgloss.NewStyle().Bold(true)
	errorStyle          = lipgloss.NewStyle().Foreground(colorError)
	hintStyle           = lipgloss.NewStyle().Foreground(colorMuted).Italic(true)
	containerStyle      = lipgloss.NewStyle().Padding(1, 2)
)

func (m Model) View() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Choose your organization"))
	b.WriteString("\n\n")

	if m.loading {
		b.WriteString("loading...")
		return containerStyle.Render(b.String())
	}

	if m.mode != modeList {
		label := "New org name"
		if m.mode == modeJoin {
			label = "Invite code"
		}
		b.WriteString(promptStyle.Render(label) + "\n")
		b.WriteString(m.input.View())
		if m.err != "" {
			b.WriteString("\n\n" + errorStyle.Render(m.err))
		}
		b.WriteString("\n\n" + hintStyle.Render("enter to submit, esc to go back"))
		return containerStyle.Render(b.String())
	}

	for i, org := range m.orgs {
		b.WriteString(renderRow(i == m.cursor, fmt.Sprintf("%s [%d]", org.Name, org.ID)) + "\n")
	}
	for i, act := range m.trailingActions() {
		label := "+ Join with an invite code"
		if act == actionCreate {
			label = "+ Create a new organization"
		}
		b.WriteString(renderRow(m.cursor == len(m.orgs)+i, label) + "\n")
	}

	if len(m.orgs) == 0 {
		hint := "you don't belong to any organization yet -- create or join one to continue"
		if !m.isAdmin {
			hint = "you don't belong to any organization yet -- ask an admin for an invite code"
		}
		b.WriteString("\n" + hintStyle.Render(hint))
	}
	if m.err != "" {
		b.WriteString("\n" + errorStyle.Render(m.err))
	}
	b.WriteString("\n" + hintStyle.Render("up/down to choose, enter to select, ctrl+c to quit"))
	return containerStyle.Render(b.String())
}

func renderRow(selected bool, text string) string {
	cursor := "  "
	style := optionStyle
	if selected {
		cursor = "> "
		style = selectedOptionStyle
	}
	return cursor + style.Render(text)
}
