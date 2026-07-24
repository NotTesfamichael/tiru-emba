package onboarding

import (
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
	switch m.step {
	case stepConnecting:
		return containerStyle.Render("connecting to " + m.serverAddr + "...")
	case stepResuming:
		return containerStyle.Render("resuming session as " + m.handle + "...")
	case stepError:
		return containerStyle.Render(errorStyle.Render(m.err) + "\n\n" + hintStyle.Render("enter to retry, ctrl+c to quit"))
	case stepWelcome:
		return containerStyle.Render(m.viewWelcome())
	case stepWizard:
		return containerStyle.Render(m.viewWizard())
	}
	return ""
}

func (m Model) viewWelcome() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("tiru-emba"))
	b.WriteString("\n\n")
	for i, opt := range welcomeOptions {
		b.WriteString(renderRow(i == m.welcomeCursor, opt) + "\n")
	}
	if m.err != "" {
		b.WriteString("\n" + errorStyle.Render(m.err) + "\n")
	}
	b.WriteString("\n" + hintStyle.Render("up/down to choose, enter to select, ctrl+c to quit"))
	return b.String()
}

func (m Model) viewWizard() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(flowTitle(m.flow)))
	b.WriteString("\n\n")

	// Once the last field is submitted, advanceWizard bumps fieldIdx to
	// len(fields) and returns submitWizard's Cmd -- but Bubble Tea renders
	// View() again immediately with that same updated Model, before the
	// async request's result ever arrives, so fieldIdx is transiently out
	// of bounds for every wizard's final field. Render a plain "submitting"
	// state instead of indexing m.fields in that window.
	if m.fieldIdx >= len(m.fields) {
		b.WriteString("submitting...")
		return b.String()
	}

	cur := m.fields[m.fieldIdx]
	b.WriteString(promptStyle.Render(cur.prompt) + "\n")
	if cur.hint != "" {
		b.WriteString(hintStyle.Render("("+cur.hint+")") + "\n")
	}

	if len(cur.choices) > 0 {
		for i, choice := range cur.choices {
			b.WriteString(renderRow(i == m.choiceCursor, choice) + "\n")
		}
	} else {
		b.WriteString(m.input.View())
	}

	if m.checkingHandle {
		b.WriteString("\n\n" + hintStyle.Render("checking availability..."))
	}
	if m.err != "" {
		b.WriteString("\n\n" + errorStyle.Render(m.err))
	}
	b.WriteString("\n\n" + hintStyle.Render("enter to continue, esc to go back, ctrl+c to quit"))
	return b.String()
}

func flowTitle(f flow) string {
	switch f {
	case flowLogin:
		return "Log in"
	case flowRegister:
		return "Register"
	case flowRecoverHandle, flowRecoverAnswer:
		return "Forgot password"
	}
	return ""
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
