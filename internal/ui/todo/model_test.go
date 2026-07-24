package todo

import (
	"errors"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/NotTesfamichael/tiru-emba/internal/relay"
)

type fakeClient struct {
	todos       []relay.TodoInfo
	listErr     error
	added       []string
	addErr      error
	completed   []int64
	completeErr error
}

func (f *fakeClient) ListTodos(orgID int64) ([]relay.TodoInfo, error) { return f.todos, f.listErr }
func (f *fakeClient) AddTodo(orgID int64, text string) (relay.TodoInfo, error) {
	f.added = append(f.added, text)
	return relay.TodoInfo{ID: 99, Text: text}, f.addErr
}
func (f *fakeClient) CompleteTodo(orgID, todoID int64) error {
	f.completed = append(f.completed, todoID)
	return f.completeErr
}

func key(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	}
	return tea.KeyMsg{}
}

func TestLANOnlyModeHasNoSharedSection(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := New(nil, nil, "", "@alex")
	if m.hasOrg() {
		t.Error("expected hasOrg() to be false with nil client/orgID")
	}
	if got := len(m.rows()); got != 1 {
		t.Errorf("rows() = %d, want 1 (just the add-personal row)", got)
	}
}

func TestPersonalAddAndComplete(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := New(nil, nil, "", "@alex")

	// Row 0 is "+ Add personal todo".
	m, _ = m.Update(key("enter"))
	if !m.adding || m.target != targetPersonal {
		t.Fatalf("expected to enter personal add mode, got adding=%v target=%v", m.adding, m.target)
	}
	m = typeString(t, m, "buy milk")
	m, _ = m.Update(key("enter"))
	if m.adding {
		t.Fatal("expected to leave add mode after submit")
	}
	if len(m.personal) != 1 || m.personal[0].Text != "buy milk" {
		t.Fatalf("personal = %+v, want one item 'buy milk'", m.personal)
	}

	// Row 0 is now the todo itself; enter should complete it.
	m.cursor = 0
	m, _ = m.Update(key("enter"))
	if !m.personal[0].Done {
		t.Error("expected the personal todo to be marked done")
	}
}

func typeString(t *testing.T, m Model, s string) Model {
	t.Helper()
	for _, r := range s {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return m
}

func TestSharedSectionAppearsWithClientAndOrg(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	orgID := int64(7)
	client := &fakeClient{todos: []relay.TodoInfo{{ID: 1, Text: "shared task", CreatedBy: "@bob"}}}
	m := New(client, &orgID, "Acme", "@alex")
	if !m.hasOrg() {
		t.Fatal("expected hasOrg() to be true")
	}

	updated, _ := m.Update(listSharedResultMsg{todos: client.todos})
	if len(updated.shared) != 1 {
		t.Fatalf("shared = %+v, want one item", updated.shared)
	}
	// rows: 1 add-personal + 1 shared item + 1 add-shared = 3
	if got := len(updated.rows()); got != 3 {
		t.Errorf("rows() = %d, want 3", got)
	}
}

func TestCompleteSharedDispatchesCmd(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	orgID := int64(7)
	client := &fakeClient{todos: []relay.TodoInfo{{ID: 1, Text: "shared task"}}}
	m := New(client, &orgID, "Acme", "@alex")
	m, _ = m.Update(listSharedResultMsg{todos: client.todos})

	// rows: [0]=add-personal, [1]=shared item
	m.cursor = 1
	_, cmd := m.Update(key("enter"))
	if cmd == nil {
		t.Fatal("expected a Cmd for completing a shared todo")
	}
	cmd()
	if len(client.completed) != 1 || client.completed[0] != 1 {
		t.Errorf("completed = %v, want [1]", client.completed)
	}
}

func TestEscEmitsClosedMsg(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := New(nil, nil, "", "@alex")
	_, cmd := m.Update(key("esc"))
	if cmd == nil {
		t.Fatal("expected a Cmd from esc")
	}
	if _, ok := cmd().(ClosedMsg); !ok {
		t.Error("expected esc to produce ClosedMsg")
	}
}

func TestAddRejectsBlankText(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := New(nil, nil, "", "@alex")
	m, _ = m.Update(key("enter")) // enter add-personal mode
	m, _ = m.Update(key("enter")) // submit blank
	if !m.adding {
		t.Error("expected to remain in add mode for blank text")
	}
	if m.err == "" {
		t.Error("expected a non-empty error for blank todo text")
	}
}

func TestListSharedErrorSurfaced(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	orgID := int64(1)
	m := New(&fakeClient{}, &orgID, "Acme", "@alex")
	updated, _ := m.Update(listSharedResultMsg{err: errors.New("boom")})
	if updated.err == "" {
		t.Error("expected a non-empty error")
	}
}
