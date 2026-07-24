package orgselect

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/NotTesfamichael/tiru-emba/internal/relay"
)

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

func newTestModel(orgs []relay.OrgSummary) Model {
	return newTestModelAs(orgs, true)
}

func newTestModelAs(orgs []relay.OrgSummary, isAdmin bool) Model {
	m := New(nil, isAdmin)
	m.loading = false
	m.orgs = orgs
	return m
}

func TestListNavigationBounds(t *testing.T) {
	m := newTestModel([]relay.OrgSummary{{ID: 1, Name: "Acme"}, {ID: 2, Name: "Globex"}})
	// rowCount = 2 orgs + create + join = 4, cursor indices 0..3
	if m.rowCount() != 4 {
		t.Fatalf("rowCount = %d, want 4", m.rowCount())
	}
	for i := 0; i < 5; i++ {
		m, _ = m.Update(key("down"))
	}
	if m.cursor != 3 {
		t.Errorf("cursor = %d, want to clamp at 3", m.cursor)
	}
	for i := 0; i < 5; i++ {
		m, _ = m.Update(key("up"))
	}
	if m.cursor != 0 {
		t.Errorf("cursor = %d, want to clamp at 0", m.cursor)
	}
}

func TestEnterOnOrgRowEmitsSelectedMsg(t *testing.T) {
	m := newTestModel([]relay.OrgSummary{{ID: 42, Name: "Acme"}})
	m, cmd := m.Update(key("enter"))
	if cmd == nil {
		t.Fatal("expected a Cmd from selecting an org row")
	}
	msg, ok := cmd().(SelectedMsg)
	if !ok {
		t.Fatalf("Cmd produced %T, want SelectedMsg", cmd())
	}
	if msg.OrgID != 42 || msg.OrgName != "Acme" {
		t.Errorf("SelectedMsg = %+v, want {42 Acme}", msg)
	}
}

func TestEnterOnCreateRowEntersCreateMode(t *testing.T) {
	m := newTestModel([]relay.OrgSummary{{ID: 1, Name: "Acme"}})
	m.cursor = 1 // the "+ Create a new organization" row
	m, cmd := m.Update(key("enter"))
	if cmd != nil {
		t.Error("expected no Cmd yet -- entering create mode shouldn't submit anything")
	}
	if m.mode != modeCreate {
		t.Errorf("mode = %v, want modeCreate", m.mode)
	}
}

func TestEnterOnJoinRowEntersJoinMode(t *testing.T) {
	m := newTestModel(nil)
	m.cursor = 1 // with no orgs, row 0 = create, row 1 = join
	m, _ = m.Update(key("enter"))
	if m.mode != modeJoin {
		t.Errorf("mode = %v, want modeJoin", m.mode)
	}
}

func TestEscFromInputModeReturnsToList(t *testing.T) {
	m := newTestModel(nil)
	m.mode = modeCreate
	m, _ = m.Update(key("esc"))
	if m.mode != modeList {
		t.Errorf("mode = %v, want modeList after esc", m.mode)
	}
}

func TestBlankInputIsRejected(t *testing.T) {
	m := newTestModel(nil)
	m.mode = modeCreate
	m, cmd := m.Update(key("enter"))
	if cmd != nil {
		t.Error("expected no Cmd for a blank org name")
	}
	if m.err == "" {
		t.Error("expected a non-empty error for a blank org name")
	}
}

func TestNonAdminHasNoCreateOption(t *testing.T) {
	m := newTestModelAs([]relay.OrgSummary{{ID: 1, Name: "Acme"}}, false)
	// rowCount = 1 org + join only (no create) = 2
	if got := m.rowCount(); got != 2 {
		t.Fatalf("rowCount = %d, want 2 (no create option for a non-admin)", got)
	}
	m.cursor = 1 // the only trailing row
	m, _ = m.Update(key("enter"))
	if m.mode != modeJoin {
		t.Errorf("mode = %v, want modeJoin -- a non-admin's only trailing action", m.mode)
	}
}
