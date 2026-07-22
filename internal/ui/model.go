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
	"github.com/NotTesfamichael/tiru-emba/internal/network"
	"github.com/NotTesfamichael/tiru-emba/internal/peer"
)

const pruneInterval = 1 * time.Second

// peerSeenMsg wraps a discovery.PeerSeen so it can travel through Bubble
// Tea's Msg pipeline.
type peerSeenMsg discovery.PeerSeen

// incomingMsgMsg wraps a network.Received direct message.
type incomingMsgMsg network.Received

// sendResultMsg reports the outcome of an async, in-flight Send.
type sendResultMsg struct {
	target string
	err    error
}

// pruneTickMsg fires periodically so the peer list drops teammates whose
// heartbeat has gone stale (e.g. they closed the app or left the network).
type pruneTickMsg time.Time

type Model struct {
	ctx    context.Context
	selfID string
	handle string

	peers *peer.Registry
	peerC <-chan discovery.PeerSeen
	msgC  <-chan network.Received

	history []string
	input   textinput.Model

	width, height int
}

func New(ctx context.Context, selfID, handle string, peerC <-chan discovery.PeerSeen, msgC <-chan network.Received) Model {
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
		msgC:    msgC,
		history: []string{fmt.Sprintf("welcome, %s. discovering teammates on the LAN...", handle)},
		input:   ti,
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		waitForPeer(m.peerC),
		waitForMessage(m.msgC),
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

// waitForMessage mirrors waitForPeer for incoming direct messages.
func waitForMessage(ch <-chan network.Received) tea.Cmd {
	return func() tea.Msg {
		r, ok := <-ch
		if !ok {
			return nil
		}
		return incomingMsgMsg(r)
	}
}

// sendMessage delivers a direct message in the background (network.Send
// blocks on a TCP dial+write) so Update never stalls the UI on a slow or
// unreachable peer.
func sendMessage(addr, from, target, body string) tea.Cmd {
	return func() tea.Msg {
		err := network.Send(addr, network.Message{From: from, Body: body})
		return sendResultMsg{target: target, err: err}
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

	case incomingMsgMsg:
		m.history = append(m.history, fmt.Sprintf("%s: %s", peerStyle.Render(msg.From), msg.Body))
		return m, waitForMessage(m.msgC)

	case sendResultMsg:
		if msg.err != nil {
			m.history = append(m.history, statusStyle.Render(fmt.Sprintf("failed to deliver to %s: %v", msg.target, msg.err)))
		}
		return m, nil

	case pruneTickMsg:
		m.peers.Prune(time.Time(msg), discovery.PeerTTL)
		return m, tea.Tick(pruneInterval, func(t time.Time) tea.Msg { return pruneTickMsg(t) })
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// submitInput handles the Enter key: "@handle body" resolves the handle
// against the peer registry and delivers body over TCP; anything else is
// just a local note (there's no group broadcast channel, only direct
// messages).
func (m Model) submitInput() (tea.Model, tea.Cmd) {
	text := strings.TrimSpace(m.input.Value())
	if text == "" {
		return m, nil
	}
	m.input.Reset()

	if strings.HasPrefix(text, "@") {
		fields := strings.SplitN(text, " ", 2)
		target := fields[0]
		info, ok := m.peers.Lookup(target)
		if !ok {
			m.history = append(m.history, statusStyle.Render(fmt.Sprintf("no such peer online: %s", target)))
			return m, nil
		}
		body := ""
		if len(fields) > 1 {
			body = fields[1]
		}
		m.history = append(m.history, fmt.Sprintf("%s %s", selfStyle.Render(m.handle+" ->"), text))
		addr := fmt.Sprintf("%s:%d", info.Addr, info.TCPPort)
		return m, sendMessage(addr, m.handle, target, body)
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
