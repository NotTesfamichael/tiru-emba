// Package account implements the full-screen "/account bio" view: the
// caller's own stats (handle, ASCII avatar, points, orgs, account age)
// plus a browsable shop of unlockable ASCII avatars/borders points can
// redeem and equip. Only reachable in relay mode -- LAN-only mode shows a
// much smaller local-only summary inline in chat instead (see
// internal/ui/model.go's handleAccountCommand), since there are no
// points/orgs/unlockables without a server.
package account

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/NotTesfamichael/tiru-emba/internal/relay"
)

// Client is the narrow slice of relayClient this screen needs, mirroring
// the same narrow-interface-per-screen pattern internal/ui/onboarding and
// internal/ui/model.go's own relayClient already use.
type Client interface {
	Bio() (relay.AccountBio, error)
	ListUnlockables() ([]relay.UnlockableInfo, error)
	RedeemUnlockable(unlockableID int64) error
	SetAvatar(unlockableID int64) error
}

// ClosedMsg is emitted when the user backs out of this screen (esc),
// telling the hosting router (internal/ui.App) to return to chat.
type ClosedMsg struct{}

// Model is the account screen. Value-receiver Update/View, following the
// same self-contained-screen shape internal/ui/onboarding uses.
type Model struct {
	client Client

	loading bool
	err     string

	bio         relay.AccountBio
	unlockables []relay.UnlockableInfo
	cursor      int

	width, height int
}

func New(client Client) Model {
	return Model{client: client, loading: true}
}

// Init only fires bioCmd -- not tea.Batch(bioCmd, unlockablesCmd). A
// relay.Client connection allows exactly one in-flight request at a time
// (see its own doc comment); firing two concurrently here raced and one
// came back with "a request is already in flight on this connection",
// caught via live testing. bioResultMsg's handler chains into
// unlockablesCmd next, so the two still both run, just sequentially.
func (m Model) Init() tea.Cmd {
	return bioCmd(m.client)
}

type bioResultMsg struct {
	bio relay.AccountBio
	err error
}

func bioCmd(client Client) tea.Cmd {
	return func() tea.Msg {
		bio, err := client.Bio()
		return bioResultMsg{bio: bio, err: err}
	}
}

type unlockablesResultMsg struct {
	unlockables []relay.UnlockableInfo
	err         error
}

func unlockablesCmd(client Client) tea.Cmd {
	return func() tea.Msg {
		list, err := client.ListUnlockables()
		return unlockablesResultMsg{unlockables: list, err: err}
	}
}

type actionResultMsg struct {
	err error
}

func redeemCmd(client Client, id int64) tea.Cmd {
	return func() tea.Msg {
		err := client.RedeemUnlockable(id)
		return actionResultMsg{err: err}
	}
}

func setAvatarCmd(client Client, id int64) tea.Cmd {
	return func() tea.Msg {
		err := client.SetAvatar(id)
		return actionResultMsg{err: err}
	}
}

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case bioResultMsg:
		if msg.err != nil {
			m.loading = false
			m.err = msg.err.Error()
			return m, nil
		}
		m.bio = msg.bio
		return m, unlockablesCmd(m.client)

	case unlockablesResultMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil
		}
		m.unlockables = msg.unlockables
		return m, nil

	case actionResultMsg:
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil
		}
		m.err = ""
		// Refresh the bio (points changed) and, once that's back, the
		// catalog (owned/active flags changed) -- sequentially, same
		// reasoning as Init().
		return m, bioCmd(m.client)

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		return m, func() tea.Msg { return ClosedMsg{} }
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.unlockables)-1 {
			m.cursor++
		}
	case "enter":
		if m.cursor < 0 || m.cursor >= len(m.unlockables) {
			return m, nil
		}
		item := m.unlockables[m.cursor]
		m.err = ""
		if !item.Owned {
			return m, redeemCmd(m.client, item.ID)
		}
		if !item.Active {
			return m, setAvatarCmd(m.client, item.ID)
		}
	}
	return m, nil
}

// summaryLines renders the plain-text bio stats shown above the
// unlockables list. The avatar (itself possibly several lines of ASCII
// art) gets its own "Avatar:" header line followed by the art on
// subsequent lines, rather than crammed onto the same line as the label --
// that badly mangled the layout when tested live with a real multi-line
// avatar.
func (m Model) summaryLines() []string {
	lines := []string{fmt.Sprintf("Handle:  %s", m.bio.Handle)}
	if m.bio.AvatarASCII == "" {
		lines = append(lines, "Avatar:  (none set)")
	} else {
		lines = append(lines, "Avatar:")
		lines = append(lines, strings.Split(m.bio.AvatarASCII, "\n")...)
	}
	lines = append(lines,
		fmt.Sprintf("Points:  %d", m.bio.Points),
		fmt.Sprintf("Orgs:    %s", strings.Join(m.bio.OrgNames, ", ")),
	)
	if !m.bio.CreatedAt.IsZero() {
		lines = append(lines, fmt.Sprintf("Member since: %s", m.bio.CreatedAt.Format("2006-01-02")))
	}
	return lines
}
