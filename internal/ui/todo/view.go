package todo

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	colorAccent = lipgloss.Color("#7D56F4")
	colorMuted  = lipgloss.Color("#626262")
	colorError  = lipgloss.Color("#E5484D")
	colorDone   = lipgloss.Color("#3FB950")

	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FAFAFA")).
			Background(colorAccent).
			Padding(0, 1)

	sectionStyle        = lipgloss.NewStyle().Bold(true).Foreground(colorAccent)
	optionStyle         = lipgloss.NewStyle()
	selectedOptionStyle = lipgloss.NewStyle().Bold(true).Foreground(colorAccent)
	doneStyle           = lipgloss.NewStyle().Foreground(colorDone).Strikethrough(true)
	promptStyle         = lipgloss.NewStyle().Bold(true)
	errorStyle          = lipgloss.NewStyle().Foreground(colorError)
	hintStyle           = lipgloss.NewStyle().Foreground(colorMuted).Italic(true)
	containerStyle      = lipgloss.NewStyle().Padding(1, 2)
)

func (m Model) View() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Todo"))
	b.WriteString("\n\n")

	if m.adding {
		label := "New personal todo"
		if m.target == targetShared {
			label = fmt.Sprintf("New shared todo (%s)", m.orgName)
		}
		b.WriteString(promptStyle.Render(label) + "\n")
		b.WriteString(m.input.View())
		if m.err != "" {
			b.WriteString("\n\n" + errorStyle.Render(m.err))
		}
		b.WriteString("\n\n" + hintStyle.Render("enter to add, esc to cancel"))
		return containerStyle.Render(b.String())
	}

	i := 0

	b.WriteString(sectionStyle.Render("Personal") + "\n")
	for _, it := range m.personal {
		b.WriteString(renderItemRow(i == m.cursor, it.Text, it.Done) + "\n")
		i++
	}
	b.WriteString(renderActionRow(i == m.cursor, "+ Add personal todo") + "\n")
	i++

	if m.hasOrg() {
		b.WriteString("\n" + sectionStyle.Render(fmt.Sprintf("Shared (%s)", m.orgName)) + "\n")
		if m.loading {
			b.WriteString(hintStyle.Render("loading...") + "\n")
		}
		for _, t := range m.shared {
			b.WriteString(renderItemRow(i == m.cursor, fmt.Sprintf("%s (%s)", t.Text, t.CreatedBy), t.Done) + "\n")
			i++
		}
		b.WriteString(renderActionRow(i == m.cursor, "+ Add shared todo") + "\n")
	} else {
		b.WriteString("\n" + hintStyle.Render("connect with --server and select an org to see shared todos"))
	}

	if m.err != "" {
		b.WriteString("\n\n" + errorStyle.Render(m.err))
	}
	b.WriteString("\n" + hintStyle.Render("up/down to browse, enter/space to toggle or add, esc to go back"))
	return containerStyle.Render(b.String())
}

func renderItemRow(selected bool, text string, done bool) string {
	cursor := "  "
	style := optionStyle
	box := "[ ]"
	if done {
		box = "[x]"
		style = doneStyle
	}
	if selected {
		cursor = "> "
		if !done {
			style = selectedOptionStyle
		}
	}
	return cursor + style.Render(box+" "+text)
}

func renderActionRow(selected bool, text string) string {
	cursor := "  "
	style := optionStyle
	if selected {
		cursor = "> "
		style = selectedOptionStyle
	}
	return cursor + style.Render(text)
}
