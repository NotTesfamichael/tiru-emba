// Package orgselect implements the mandatory "choose your active
// organization" screen shown after every successful relay authentication
// (fresh login/register, a resumed session, or a completed recovery) --
// required every launch, even when the session itself was resumed, since
// which org should be active isn't implied by the session token alone (a
// user may belong to several, or membership may have changed since last
// time).
package orgselect

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/NotTesfamichael/tiru-emba/internal/relay"
)

// SelectedMsg is emitted once the user has picked (or created/joined) an
// org, handing the router (internal/ui.App) the chosen org to scope chat
// and shared todos around.
type SelectedMsg struct {
	OrgID   int64
	OrgName string
}

type mode int

const (
	modeList mode = iota
	modeCreate
	modeJoin
)

// action identifies one of the trailing, always-selectable rows appended
// after the real org list -- which ones are available depends on isAdmin
// (see trailingActions).
type action int

const (
	actionCreate action = iota
	actionJoin
)

// Model is the org-select screen. Value-receiver Update/View, following
// the same self-contained-screen shape internal/ui/onboarding uses.
type Model struct {
	client  *relay.Client
	isAdmin bool
	orgs    []relay.OrgSummary
	cursor  int
	loading bool
	err     string

	mode  mode
	input textinput.Model

	width, height int
}

// New constructs the org-select screen. isAdmin gates whether "+ Create a
// new organization" appears at all -- only a system admin may create one
// (see relay.Orgs.Create); everyone else only ever sees "+ Join with an
// invite code".
func New(client *relay.Client, isAdmin bool) Model {
	ti := textinput.New()
	ti.CharLimit = 200
	return Model{client: client, isAdmin: isAdmin, loading: true, input: ti}
}

// trailingActions is the fixed set of action rows shown after the real
// org list, in display order.
func (m Model) trailingActions() []action {
	if m.isAdmin {
		return []action{actionCreate, actionJoin}
	}
	return []action{actionJoin}
}

func (m Model) Init() tea.Cmd {
	return listOrgsCmd(m.client)
}

type listOrgsResultMsg struct {
	orgs []relay.OrgSummary
	err  error
}

func listOrgsCmd(client *relay.Client) tea.Cmd {
	return func() tea.Msg {
		orgs, err := client.ListOrgs()
		return listOrgsResultMsg{orgs: orgs, err: err}
	}
}

type orgActionResultMsg struct {
	org relay.OrgSummary
	err error
}

func createOrgCmd(client *relay.Client, name string) tea.Cmd {
	return func() tea.Msg {
		org, err := client.CreateOrg(name)
		return orgActionResultMsg{org: org, err: err}
	}
}

func joinOrgCmd(client *relay.Client, code string) tea.Cmd {
	return func() tea.Msg {
		org, err := client.JoinOrg(code)
		return orgActionResultMsg{org: org, err: err}
	}
}

// rowCount is how many selectable rows the list shows: every org, plus
// whichever trailing actions apply.
func (m Model) rowCount() int {
	return len(m.orgs) + len(m.trailingActions())
}

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case listOrgsResultMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil
		}
		m.orgs = msg.orgs
		return m, nil

	case orgActionResultMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err.Error()
			m.mode = modeList
			return m, nil
		}
		org := msg.org
		return m, func() tea.Msg { return SelectedMsg{OrgID: org.ID, OrgName: org.Name} }

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		return m, tea.Quit
	}
	if m.mode != modeList {
		return m.handleInputModeKey(msg)
	}
	return m.handleListKey(msg)
}

func (m Model) handleListKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < m.rowCount()-1 {
			m.cursor++
		}
	case "enter":
		m.err = ""
		if m.cursor < len(m.orgs) {
			org := m.orgs[m.cursor]
			return m, func() tea.Msg { return SelectedMsg{OrgID: org.ID, OrgName: org.Name} }
		}
		switch m.trailingActions()[m.cursor-len(m.orgs)] {
		case actionCreate:
			m.mode = modeCreate
			m.input.Placeholder = "org name"
			m.input.SetValue("")
			m.input.Focus()
		case actionJoin:
			m.mode = modeJoin
			m.input.Placeholder = "invite code"
			m.input.SetValue("")
			m.input.Focus()
		}
	}
	return m, nil
}

func (m Model) handleInputModeKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeList
		m.err = ""
		return m, nil
	case "enter":
		value := strings.TrimSpace(m.input.Value())
		if value == "" {
			m.err = "can't be blank"
			return m, nil
		}
		m.loading = true
		m.err = ""
		if m.mode == modeCreate {
			return m, createOrgCmd(m.client, value)
		}
		return m, joinOrgCmd(m.client, value)
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}
