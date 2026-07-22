package ui

import (
	"hash/fnv"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// peerPalette is a fixed set of visually distinct, readable-on-dark-bg
// colors. Deliberately excludes colors already meaningful elsewhere in the
// UI: colorAccent (chrome/title), colorSelf (orange, your own handle),
// colorMuted (system/status text).
var peerPalette = []lipgloss.Color{
	lipgloss.Color("#3FB950"), // green
	lipgloss.Color("#58A6FF"), // blue
	lipgloss.Color("#F778BA"), // pink
	lipgloss.Color("#D2A8FF"), // violet
	lipgloss.Color("#79C0FF"), // sky
	lipgloss.Color("#7EE787"), // mint
	lipgloss.Color("#FFA657"), // amber (distinct enough from colorSelf's orange)
	lipgloss.Color("#F85149"), // red
	lipgloss.Color("#A5D6FF"), // pale blue
	lipgloss.Color("#DDBA7D"), // tan
}

// colorForHandle deterministically maps a handle to a palette color, stable
// across restarts and across every client's own instance (it depends only
// on the handle string, not any per-run randomness), so the same teammate
// always renders in the same color for everyone.
func colorForHandle(handle string) lipgloss.Color {
	h := fnv.New32a()
	_, _ = h.Write([]byte(strings.ToLower(handle)))
	return peerPalette[h.Sum32()%uint32(len(peerPalette))]
}
