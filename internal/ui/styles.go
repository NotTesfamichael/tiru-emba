package ui

import "github.com/charmbracelet/lipgloss"

var (
	colorAccent = lipgloss.Color("#7D56F4")
	colorMuted  = lipgloss.Color("#626262")
	colorOnline = lipgloss.Color("#3FB950")
	colorSelf   = lipgloss.Color("#FFB454")

	appStyle = lipgloss.NewStyle().Padding(0, 1)

	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FAFAFA")).
			Background(colorAccent).
			Padding(0, 1)

	sidebarStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorMuted).
			Padding(0, 1)

	sidebarHeaderStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorAccent)

	peerStyle = lipgloss.NewStyle().Foreground(colorOnline)
	selfStyle = lipgloss.NewStyle().Foreground(colorSelf).Bold(true)

	chatStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorMuted).
			Padding(0, 1)

	statusStyle = lipgloss.NewStyle().
			Foreground(colorMuted).
			Italic(true)

	inputStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorAccent).
			Padding(0, 1)
)
