package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/NotTesfamichael/tiru-emba/internal/discovery"
	"github.com/NotTesfamichael/tiru-emba/internal/peer"
)

const pruneInterval = 1 * time.Second

// peerSeenMsg wraps a discovery.PeerSeen so it can travel through Bubble
// Tea's Msg pipeline.
type peerSeenMsg discovery.PeerSeen

// pruneTickMsg fires periodically so the peer list drops teammates whose
// heartbeat has gone stale (e.g. they closed the app or left the network).
type pruneTickMsg time.Time

type Model struct {
	ctx    context.Context
	selfID string
	handle string

	peers *peer.Registry
	peerC <-chan discovery.PeerSeen

	history []string
	input   textinput.Model

	width, height int
}

func New(ctx context.Context, selfID, handle string, peerC <-chan discovery.PeerSeen) Model {
	ti := textinput.New()
	ti.Placeholder = "@handle your message, or just type to broadcast..."
	ti.Focus()
	ti.CharLimit = 500
	ti.Prompt = "> "

	return Model{
		ctx:     ctx,
		selfID:  selfID,
		handle:  handle,
		peers:   peer.NewRegistry(),
		peerC:   peerC,
		history: []string{fmt.Sprintf("welcome, %s. discovering teammates on the LAN...", handle)},
		input:   ti,
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		waitForPeer(m.peerC),
		tea.Tick(pruneInterval, func(t time.Time) tea.Msg { return pruneTickMsg(t) }),
		textinput.Blink,
	)
}

// waitForPeer blocks on the discovery channel and turns the next sighting
// into a tea.Msg. Update() re-issues this Cmd after each message so the
// pump keeps running for the lifetime of the program.
func waitForPeer(ch <-chan discovery.PeerSeen) tea.Cmd {
	return func() tea.Msg {
		p, ok := <-ch
		if !ok {
			return nil
		}
		return peerSeenMsg(p)
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			return m, tea.Quit
		case "enter":
			return m.submitInput()
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd

	case peerSeenMsg:
		info := peer.Info{
			ID:       msg.ID,
			Handle:   msg.Handle,
			Addr:     msg.Addr.String(),
			TCPPort:  msg.TCPPort,
			LastSeen: msg.SeenAt,
		}
		firstSighting := true
		for _, p := range m.peers.List() {
			if p.ID == msg.ID {
				firstSighting = false
				break
			}
		}
		m.peers.Upsert(info)
		if firstSighting {
			m.history = append(m.history, statusStyle.Render(fmt.Sprintf("%s joined", msg.Handle)))
		}
		return m, waitForPeer(m.peerC)

	case pruneTickMsg:
		m.peers.Prune(time.Time(msg), discovery.PeerTTL)
		return m, tea.Tick(pruneInterval, func(t time.Time) tea.Msg { return pruneTickMsg(t) })
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// submitInput handles the Enter key. Phase 1 has no TCP layer yet, so
// messages are only echoed locally -- Phase 2 will route "@handle ..." to
// the resolved peer's TCP address via peer.Registry.Lookup.
func (m Model) submitInput() (tea.Model, tea.Cmd) {
	text := strings.TrimSpace(m.input.Value())
	if text == "" {
		return m, nil
	}
	m.input.Reset()

	if strings.HasPrefix(text, "@") {
		fields := strings.SplitN(text, " ", 2)
		target := fields[0]
		if _, ok := m.peers.Lookup(target); !ok {
			m.history = append(m.history, statusStyle.Render(fmt.Sprintf("no such peer online: %s", target)))
			return m, nil
		}
		m.history = append(m.history, fmt.Sprintf("%s %s", selfStyle.Render(m.handle+" ->"), text))
		// TODO(Phase 2): dial peer's TCPPort and send the message body.
		return m, nil
	}

	m.history = append(m.history, fmt.Sprintf("%s: %s", selfStyle.Render(m.handle), text))
	return m, nil
}

func (m Model) View() string {
	if m.width == 0 {
		return "initializing..."
	}

	title := titleStyle.Render(fmt.Sprintf(" LAN Chat — %s ", m.handle))

	sidebarWidth := 24
	mainWidth := m.width - sidebarWidth - 6 // borders/padding fudge
	bodyHeight := m.height - 7              // title + input + borders

	sidebar := m.renderSidebar(sidebarWidth, bodyHeight)
	chat := m.renderChat(mainWidth, bodyHeight)

	body := lipgloss.JoinHorizontal(lipgloss.Top, sidebar, chat)
	input := inputStyle.Width(m.width - 4).Render(m.input.View())

	return appStyle.Render(lipgloss.JoinVertical(lipgloss.Left, title, body, input))
}

func (m Model) renderSidebar(width, height int) string {
	var b strings.Builder
	b.WriteString(sidebarHeaderStyle.Render(fmt.Sprintf("Online (%d)", m.peers.Len())))
	b.WriteString("\n\n")
	for _, p := range m.peers.List() {
		b.WriteString(peerStyle.Render("● " + p.Handle))
		b.WriteString("\n")
	}
	if m.peers.Len() == 0 {
		b.WriteString(statusStyle.Render("searching..."))
	}
	return sidebarStyle.Width(width).Height(height).Render(b.String())
}

func (m Model) renderChat(width, height int) string {
	lines := m.history
	maxLines := height - 2
	if maxLines > 0 && len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return chatStyle.Width(width).Height(height).Render(strings.Join(lines, "\n"))
}
