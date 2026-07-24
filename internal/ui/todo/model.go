// Package todo implements the full-screen "/todo" task view: a personal
// list (local-only, works with or without a relay connection) and, when
// connected to a relay server with an org selected, a shared list scoped
// to that org. Personal todos never touch the network; shared todos are
// visible/editable by every member of the org.
package todo

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/NotTesfamichael/tiru-emba/internal/relay"
	"github.com/NotTesfamichael/tiru-emba/internal/store"
)

// Client is the narrow slice of relayClient this screen needs for shared
// (org-scoped) todos. nil in LAN-only mode or if no relay connection
// exists -- the screen still works, just with the shared section absent.
type Client interface {
	ListTodos(orgID int64) ([]relay.TodoInfo, error)
	AddTodo(orgID int64, text string) (relay.TodoInfo, error)
	CompleteTodo(orgID, todoID int64) error
}

// ClosedMsg is emitted when the user backs out of this screen (esc),
// telling the hosting router (internal/ui.App) to return to chat.
type ClosedMsg struct{}

type rowKind int

const (
	rowPersonal rowKind = iota
	rowAddPersonal
	rowShared
	rowAddShared
)

type row struct {
	kind rowKind
	idx  int // index into personal or shared, for rowPersonal/rowShared
}

type addTarget int

const (
	targetPersonal addTarget = iota
	targetShared
)

// Model is the todo screen. Value-receiver Update/View, following the same
// self-contained-screen shape internal/ui/onboarding and
// internal/ui/account use.
type Model struct {
	client  Client
	orgID   *int64
	orgName string

	personalStore *store.TodoStore
	personal      []store.TodoItem
	shared        []relay.TodoInfo

	loading bool
	err     string
	cursor  int

	adding bool
	target addTarget
	input  textinput.Model

	width, height int
}

// New constructs the todo screen. client/orgID may be nil (LAN-only mode,
// or relay mode with no org context) -- the shared section just doesn't
// appear in that case. handle resolves the personal (local-only) list.
func New(client Client, orgID *int64, orgName, handle string) Model {
	ps, items, _ := store.OpenTodos(handle) // best-effort, same as chat history
	ti := textinput.New()
	ti.CharLimit = 300
	return Model{
		client: client, orgID: orgID, orgName: orgName,
		personalStore: ps, personal: items,
		loading: client != nil && orgID != nil,
		input:   ti,
	}
}

func (m Model) Init() tea.Cmd {
	if m.client != nil && m.orgID != nil {
		return listSharedCmd(m.client, *m.orgID)
	}
	return nil
}

func (m Model) hasOrg() bool {
	return m.client != nil && m.orgID != nil
}

func (m Model) rows() []row {
	rows := make([]row, 0, len(m.personal)+len(m.shared)+2)
	for i := range m.personal {
		rows = append(rows, row{kind: rowPersonal, idx: i})
	}
	rows = append(rows, row{kind: rowAddPersonal})
	if m.hasOrg() {
		for i := range m.shared {
			rows = append(rows, row{kind: rowShared, idx: i})
		}
		rows = append(rows, row{kind: rowAddShared})
	}
	return rows
}

type listSharedResultMsg struct {
	todos []relay.TodoInfo
	err   error
}

func listSharedCmd(client Client, orgID int64) tea.Cmd {
	return func() tea.Msg {
		todos, err := client.ListTodos(orgID)
		return listSharedResultMsg{todos: todos, err: err}
	}
}

type addSharedResultMsg struct {
	todo relay.TodoInfo
	err  error
}

func addSharedCmd(client Client, orgID int64, text string) tea.Cmd {
	return func() tea.Msg {
		t, err := client.AddTodo(orgID, text)
		return addSharedResultMsg{todo: t, err: err}
	}
}

type completeSharedResultMsg struct {
	todoID int64
	err    error
}

func completeSharedCmd(client Client, orgID, todoID int64) tea.Cmd {
	return func() tea.Msg {
		err := client.CompleteTodo(orgID, todoID)
		return completeSharedResultMsg{todoID: todoID, err: err}
	}
}

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case listSharedResultMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil
		}
		m.shared = msg.todos
		return m, nil

	case addSharedResultMsg:
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil
		}
		m.err = ""
		m.shared = append(m.shared, msg.todo)
		return m, nil

	case completeSharedResultMsg:
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil
		}
		m.err = ""
		for i, t := range m.shared {
			if t.ID == msg.todoID {
				m.shared[i].Done = true
			}
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	if m.adding {
		return m.handleAddKey(msg)
	}
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
		if m.cursor < len(m.rows())-1 {
			m.cursor++
		}
	case "enter", " ":
		return m.activateRow()
	}
	return m, nil
}

func (m Model) activateRow() (Model, tea.Cmd) {
	rows := m.rows()
	if m.cursor < 0 || m.cursor >= len(rows) {
		return m, nil
	}
	r := rows[m.cursor]
	m.err = ""
	switch r.kind {
	case rowPersonal:
		if m.personal[r.idx].Done {
			return m, nil
		}
		items, _, err := m.personalStore.Complete(m.personal, m.personal[r.idx].ID)
		m.personal = items
		if err != nil {
			m.err = err.Error()
		}
		return m, nil

	case rowShared:
		t := m.shared[r.idx]
		if t.Done || m.orgID == nil {
			return m, nil
		}
		return m, completeSharedCmd(m.client, *m.orgID, t.ID)

	case rowAddPersonal:
		m.adding = true
		m.target = targetPersonal
		m.input.SetValue("")
		m.input.Focus()

	case rowAddShared:
		m.adding = true
		m.target = targetShared
		m.input.SetValue("")
		m.input.Focus()
	}
	return m, nil
}

func (m Model) handleAddKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.adding = false
		m.err = ""
		return m, nil
	case "enter":
		text := strings.TrimSpace(m.input.Value())
		if text == "" {
			m.err = "todo text can't be blank"
			return m, nil
		}
		m.adding = false
		m.err = ""
		if m.target == targetPersonal {
			items, _, err := m.personalStore.Add(m.personal, text)
			m.personal = items
			if err != nil {
				m.err = err.Error()
			}
			return m, nil
		}
		if m.orgID == nil {
			return m, nil
		}
		return m, addSharedCmd(m.client, *m.orgID, text)
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}
