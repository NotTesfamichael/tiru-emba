package ui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/NotTesfamichael/tiru-emba/internal/discovery"
	"github.com/NotTesfamichael/tiru-emba/internal/network"
	"github.com/NotTesfamichael/tiru-emba/internal/notify"
	"github.com/NotTesfamichael/tiru-emba/internal/peer"
	"github.com/NotTesfamichael/tiru-emba/internal/store"
)

const (
	pruneInterval  = 1 * time.Second
	maxSuggestions = 6
)

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

	hist    *store.Store
	history []store.Entry

	// filterPeer, when non-empty, is a lowercased handle (no "@") and the
	// chat pane shows only entries tagged with that peer -- effectively
	// "only this color", since color is derived deterministically from
	// handle.
	filterPeer string

	input       textinput.Model
	suggestions []string
	suggestIdx  int

	width, height int
}

func New(ctx context.Context, selfID, handle string, peerC <-chan discovery.PeerSeen, msgC <-chan network.Received) Model {
	ti := textinput.New()
	ti.Placeholder = "@handle your message, or just type to broadcast..."
	ti.Focus()
	ti.CharLimit = 500
	ti.Prompt = "> "

	hist, saved, err := store.Open(handle)

	history := make([]store.Entry, 0, len(saved)+2)
	history = append(history, saved...)
	history = append(history, store.Entry{
		Kind: store.KindSystem,
		Body: fmt.Sprintf("welcome, %s. discovering teammates on the LAN...", handle),
		At:   time.Now(),
	})
	if err != nil {
		history = append(history, store.Entry{
			Kind: store.KindSystem,
			Body: fmt.Sprintf("history won't be saved this run: %v", err),
			At:   time.Now(),
		})
	}

	return Model{
		ctx:     ctx,
		selfID:  selfID,
		handle:  handle,
		peers:   peer.NewRegistry(),
		peerC:   peerC,
		msgC:    msgC,
		hist:    hist,
		history: history,
		input:   ti,
	}
}

// Close flushes/closes the history file. Call once after the Bubble Tea
// program exits.
func (m Model) Close() {
	m.hist.Close()
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

// recordEntry appends e to the in-memory history and (best-effort) persists
// it to disk. KindSystem entries are session-only and never written to disk
// (see store.Store.Append).
func recordEntry(history []store.Entry, hist *store.Store, e store.Entry) []store.Entry {
	history = append(history, e)
	_ = hist.Append(e)
	return history
}

func systemNote(body string) store.Entry {
	return store.Entry{Kind: store.KindSystem, Body: body, At: time.Now()}
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
		case "tab":
			return m.acceptSuggestion()
		case "up":
			if len(m.suggestions) > 0 {
				m.suggestIdx = (m.suggestIdx - 1 + len(m.suggestions)) % len(m.suggestions)
				return m, nil
			}
		case "down":
			if len(m.suggestions) > 0 {
				m.suggestIdx = (m.suggestIdx + 1) % len(m.suggestions)
				return m, nil
			}
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		m.updateSuggestions()
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
			m.history = recordEntry(m.history, m.hist, systemNote(fmt.Sprintf("%s joined", msg.Handle)))
		}
		return m, waitForPeer(m.peerC)

	case incomingMsgMsg:
		m.history = recordEntry(m.history, m.hist, store.Entry{
			Kind: store.KindDirectRecv,
			Peer: msg.From,
			Body: msg.Body,
			At:   msg.At,
		})
		notify.Alert(fmt.Sprintf("Message from %s", msg.From), msg.Body)
		return m, waitForMessage(m.msgC)

	case sendResultMsg:
		if msg.err != nil {
			m.history = recordEntry(m.history, m.hist, systemNote(fmt.Sprintf("failed to deliver to %s: %v", msg.target, msg.err)))
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

// submitInput handles the Enter key: "/command ..." runs a command,
// "@handle [@handle ...] body" resolves each handle against the peer
// registry and delivers body to each over TCP, anything else is just a
// local note (there's no group broadcast channel, only direct messages).
func (m Model) submitInput() (tea.Model, tea.Cmd) {
	text := strings.TrimSpace(m.input.Value())
	if text == "" {
		return m, nil
	}
	m.input.Reset()
	m.suggestions = nil

	switch {
	case strings.HasPrefix(text, "/"):
		return m.handleCommand(text)
	case strings.HasPrefix(text, "@"):
		return m.sendDirect(text)
	default:
		m.history = recordEntry(m.history, m.hist, store.Entry{Kind: store.KindSelfNote, Body: text, At: time.Now()})
		return m, nil
	}
}

// sendDirect parses leading "@handle" tokens (however many appear before the
// first non-@ word) as the recipient list, and delivers the remaining text
// to each of them independently -- one history entry per recipient, so
// filtering/coloring stays a clean one-entry-one-peer relationship.
func (m Model) sendDirect(text string) (tea.Model, tea.Cmd) {
	fields := strings.Fields(text)
	i := 0
	var targets []string
	for i < len(fields) && strings.HasPrefix(fields[i], "@") {
		targets = append(targets, fields[i])
		i++
	}
	body := strings.Join(fields[i:], " ")

	var cmds []tea.Cmd
	for _, target := range targets {
		info, ok := m.peers.Lookup(target)
		if !ok {
			m.history = recordEntry(m.history, m.hist, systemNote(fmt.Sprintf("no such peer online: %s", target)))
			continue
		}
		m.history = recordEntry(m.history, m.hist, store.Entry{
			Kind: store.KindDirectSent,
			Peer: info.Handle,
			Body: body,
			At:   time.Now(),
		})
		addr := fmt.Sprintf("%s:%d", info.Addr, info.TCPPort)
		cmds = append(cmds, sendMessage(addr, m.handle, info.Handle, body))
	}
	return m, tea.Batch(cmds...)
}

// handleCommand implements the small slash-command set: /filter @handle
// (hide everything except that conversation) and /clear (show everything).
func (m Model) handleCommand(text string) (tea.Model, tea.Cmd) {
	fields := strings.Fields(text)
	switch strings.ToLower(fields[0]) {
	case "/filter":
		if len(fields) < 2 {
			m.history = recordEntry(m.history, m.hist, systemNote("usage: /filter @handle"))
			return m, nil
		}
		target := fields[1]
		if _, ok := m.peers.Lookup(target); !ok {
			m.history = recordEntry(m.history, m.hist, systemNote(fmt.Sprintf("no such peer online: %s", target)))
			return m, nil
		}
		m.filterPeer = strings.ToLower(strings.TrimPrefix(target, "@"))
		m.history = recordEntry(m.history, m.hist, systemNote(fmt.Sprintf("filtering: only showing %s", target)))
	case "/clear", "/unfilter":
		m.filterPeer = ""
		m.history = recordEntry(m.history, m.hist, systemNote("filter cleared"))
	default:
		m.history = recordEntry(m.history, m.hist, systemNote(fmt.Sprintf("unknown command: %s", fields[0])))
	}
	return m, nil
}

// updateSuggestions recomputes the @handle autocomplete list from the
// input's current last word. Only active while that word starts with "@"
// and the cursor hasn't moved past it (approximated here by "no trailing
// space yet").
func (m *Model) updateSuggestions() {
	text := m.input.Value()
	if text == "" || strings.HasSuffix(text, " ") {
		m.suggestions = nil
		return
	}
	fields := strings.Fields(text)
	last := fields[len(fields)-1]
	if !strings.HasPrefix(last, "@") {
		m.suggestions = nil
		return
	}

	partial := strings.ToLower(last)
	var matches []string
	for _, p := range m.peers.List() {
		if strings.HasPrefix(strings.ToLower(p.Handle), partial) {
			matches = append(matches, p.Handle)
		}
	}
	sort.Strings(matches)
	if len(matches) > maxSuggestions {
		matches = matches[:maxSuggestions]
	}
	m.suggestions = matches
	if m.suggestIdx >= len(matches) {
		m.suggestIdx = 0
	}
}

// acceptSuggestion replaces the in-progress "@partial" word with the
// selected suggestion. With no suggestions showing, Tab is just forwarded to
// the input as normal.
func (m Model) acceptSuggestion() (tea.Model, tea.Cmd) {
	if len(m.suggestions) == 0 {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(tea.KeyMsg{Type: tea.KeyTab})
		return m, cmd
	}
	chosen := m.suggestions[m.suggestIdx]
	fields := strings.Fields(m.input.Value())
	if len(fields) == 0 {
		return m, nil
	}
	fields[len(fields)-1] = chosen
	m.input.SetValue(strings.Join(fields, " ") + " ")
	m.input.CursorEnd()
	m.suggestions = nil
	m.suggestIdx = 0
	return m, nil
}

func (m Model) View() string {
	if m.width == 0 {
		return "initializing..."
	}

	title := titleStyle.Render(fmt.Sprintf(" LAN Chat — %s ", m.handle))
	if m.filterPeer != "" {
		title += statusStyle.Render(fmt.Sprintf("  [filtered: @%s — /clear to reset]", m.filterPeer))
	}

	sidebarWidth := 24
	mainWidth := m.width - sidebarWidth - 6 // borders/padding fudge
	bodyHeight := m.height - 7              // title + input + borders

	sidebar := m.renderSidebar(sidebarWidth, bodyHeight)
	chat := m.renderChat(mainWidth, bodyHeight)

	body := lipgloss.JoinHorizontal(lipgloss.Top, sidebar, chat)
	input := inputStyle.Width(m.width - 4).Render(m.input.View())

	sections := []string{title, body}
	if suggestions := m.renderSuggestions(); suggestions != "" {
		sections = append(sections, suggestions)
	}
	sections = append(sections, input)

	return appStyle.Render(lipgloss.JoinVertical(lipgloss.Left, sections...))
}

func (m Model) renderSuggestions() string {
	if len(m.suggestions) == 0 {
		return ""
	}
	parts := make([]string, len(m.suggestions))
	for i, s := range m.suggestions {
		style := lipgloss.NewStyle().Foreground(colorForHandle(s))
		if i == m.suggestIdx {
			style = style.Bold(true).Underline(true)
		}
		parts[i] = style.Render(s)
	}
	return statusStyle.Render("→ ") + strings.Join(parts, "  ")
}

func (m Model) renderSidebar(width, height int) string {
	var b strings.Builder
	b.WriteString(sidebarHeaderStyle.Render(fmt.Sprintf("Online (%d)", m.peers.Len())))
	b.WriteString("\n\n")
	for _, p := range m.peers.List() {
		style := lipgloss.NewStyle().Foreground(colorForHandle(p.Handle))
		line := "● " + p.Handle
		if strings.EqualFold(strings.TrimPrefix(p.Handle, "@"), m.filterPeer) {
			line += " [filter]"
			style = style.Bold(true)
		}
		b.WriteString(style.Render(line))
		b.WriteString("\n")
	}
	if m.peers.Len() == 0 {
		b.WriteString(statusStyle.Render("searching..."))
	}
	return sidebarStyle.Width(width).Height(height).Render(b.String())
}

func (m Model) renderChat(width, height int) string {
	lines := make([]string, 0, len(m.history))
	for _, e := range m.history {
		if m.filterPeer != "" && !strings.EqualFold(strings.TrimPrefix(e.Peer, "@"), m.filterPeer) {
			continue
		}
		lines = append(lines, renderEntry(m.handle, e))
	}
	maxLines := height - 2
	if maxLines > 0 && len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return chatStyle.Width(width).Height(height).Render(strings.Join(lines, "\n"))
}

func renderEntry(selfHandle string, e store.Entry) string {
	switch e.Kind {
	case store.KindSelfNote:
		return fmt.Sprintf("%s: %s", selfStyle.Render(selfHandle), e.Body)
	case store.KindDirectSent:
		peerStyle := lipgloss.NewStyle().Foreground(colorForHandle(e.Peer))
		return fmt.Sprintf("%s %s: %s", selfStyle.Render(selfHandle+" ->"), peerStyle.Render(e.Peer), e.Body)
	case store.KindDirectRecv:
		peerStyle := lipgloss.NewStyle().Foreground(colorForHandle(e.Peer))
		return fmt.Sprintf("%s: %s", peerStyle.Render(e.Peer), e.Body)
	default: // store.KindSystem and anything unrecognized
		return statusStyle.Render(e.Body)
	}
}
