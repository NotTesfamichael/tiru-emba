package ui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/NotTesfamichael/tiru-emba/internal/peer"
	"github.com/NotTesfamichael/tiru-emba/internal/relay"
	"github.com/NotTesfamichael/tiru-emba/internal/store"
)

// fakeRelayClient is a minimal relayClient double: it records every
// SendRelay call and lets a test script canned responses for the org_*
// methods, so Model's relay-integration logic is testable without a real
// server connection.
type fakeRelayClient struct {
	sent []struct{ to, body string }

	createOrg    relay.OrgSummary
	createErr    error
	listOrgs     []relay.OrgSummary
	listErr      error
	inviteCode   string
	inviteExp    time.Time
	inviteErr    error
	joinOrg      relay.OrgSummary
	joinErr      error
	sendRelayErr error

	events chan relay.Envelope
	closed bool
}

func newFakeRelayClient() *fakeRelayClient {
	return &fakeRelayClient{events: make(chan relay.Envelope, 8)}
}

func (f *fakeRelayClient) CreateOrg(name string) (relay.OrgSummary, error) {
	return f.createOrg, f.createErr
}
func (f *fakeRelayClient) ListOrgs() ([]relay.OrgSummary, error) { return f.listOrgs, f.listErr }
func (f *fakeRelayClient) InviteToOrg(orgID int64) (string, time.Time, error) {
	return f.inviteCode, f.inviteExp, f.inviteErr
}
func (f *fakeRelayClient) JoinOrg(code string) (relay.OrgSummary, error) { return f.joinOrg, f.joinErr }
func (f *fakeRelayClient) SendRelay(to, body string) error {
	f.sent = append(f.sent, struct{ to, body string }{to, body})
	return f.sendRelayErr
}
func (f *fakeRelayClient) Events() <-chan relay.Envelope { return f.events }
func (f *fakeRelayClient) Close() error                  { f.closed = true; return nil }

// newTestModelWithRelay mirrors newTestModel but wires in a fakeRelayClient
// directly (bypassing New, the same way the game packages' fake-session
// tests construct their Models directly via struct literal).
func newTestModelWithRelay(t *testing.T, fake *fakeRelayClient) Model {
	t.Helper()
	m := newTestModel(t)
	m.relay = fake
	m.orgMates = make(map[string]bool)
	return m
}

// runCmd executes cmd, unfurling a tea.BatchMsg into its individual
// sub-commands (same pattern as the tictactoe/ludo tests' findGameOverMsg).
func runCmd(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, sub := range batch {
			sub()
		}
	}
}

func TestSendDirectRoutesToOrgMateViaRelay(t *testing.T) {
	fake := newFakeRelayClient()
	m := newTestModelWithRelay(t, fake)
	m.orgMates["@kal"] = true

	m.input.SetValue("@kal hello over the relay")
	newModel, cmd := m.submitInput()
	m = newModel.(Model)
	runCmd(cmd)

	if len(fake.sent) != 1 || fake.sent[0].to != "@kal" || fake.sent[0].body != "hello over the relay" {
		t.Errorf("sent = %+v, want one relay to @kal", fake.sent)
	}

	var found bool
	for _, e := range m.history {
		if e.Kind == store.KindDirectSent && e.Peer == "@kal" && e.Body == "hello over the relay" {
			found = true
		}
	}
	if !found {
		t.Error("expected a KindDirectSent history entry for the relayed message")
	}
}

func TestSendDirectPrefersLANPeerOverOrgMateWithSameHandle(t *testing.T) {
	fake := newFakeRelayClient()
	m := newTestModelWithRelay(t, fake)
	m.peers.Upsert(peer.Info{ID: "1", Handle: "@kal", Addr: "127.0.0.1", TCPPort: 1})
	m.orgMates["@kal"] = true

	m.input.SetValue("@kal hi")
	newModel, cmd := m.submitInput()
	m = newModel.(Model)
	runCmd(cmd)

	if len(fake.sent) != 0 {
		t.Error("expected the LAN path to be used, not the relay, when both know this handle")
	}
}

func TestSendBroadcastFansOutToOrgMates(t *testing.T) {
	fake := newFakeRelayClient()
	m := newTestModelWithRelay(t, fake)
	m.orgMates["@kal"] = true
	m.orgMates["@sam"] = true

	m.input.SetValue("hello everyone")
	newModel, cmd := m.submitInput()
	m = newModel.(Model)
	if cmd == nil {
		t.Fatal("expected a non-nil batch cmd")
	}
	runCmd(cmd)

	if len(fake.sent) != 2 {
		t.Errorf("sent = %+v, want exactly 2 relay sends (one per org-mate)", fake.sent)
	}
}

func TestHandleRelayEventPresenceJoined(t *testing.T) {
	fake := newFakeRelayClient()
	m := newTestModelWithRelay(t, fake)

	newModel, _ := m.handleRelayEvent(relayEventMsg(relay.Envelope{Type: relay.MsgPresenceJoined, Handle: "@kal"}))
	m = newModel.(Model)
	if !m.orgMates["@kal"] {
		t.Error("expected @kal to be added to orgMates")
	}
	found := false
	for _, e := range m.history {
		if e.Kind == store.KindSystem && strings.Contains(e.Body, "@kal") && strings.Contains(e.Body, "joined") {
			found = true
		}
	}
	if !found {
		t.Error("expected a join system note")
	}
}

func TestHandleRelayEventPresenceLeft(t *testing.T) {
	fake := newFakeRelayClient()
	m := newTestModelWithRelay(t, fake)
	m.orgMates["@kal"] = true

	newModel, _ := m.handleRelayEvent(relayEventMsg(relay.Envelope{Type: relay.MsgPresenceLeft, Handle: "@kal"}))
	m = newModel.(Model)
	if m.orgMates["@kal"] {
		t.Error("expected @kal to be removed from orgMates")
	}
}

func TestHandleRelayEventIncomingRelayMessage(t *testing.T) {
	fake := newFakeRelayClient()
	m := newTestModelWithRelay(t, fake)

	newModel, _ := m.handleRelayEvent(relayEventMsg(relay.Envelope{Type: relay.MsgRelay, Handle: "@kal", Body: "hi there"}))
	m = newModel.(Model)

	var found bool
	for _, e := range m.history {
		if e.Kind == store.KindDirectRecv && e.Peer == "@kal" && e.Body == "hi there" {
			found = true
		}
	}
	if !found {
		t.Error("expected a KindDirectRecv history entry")
	}
	if m.lastDMHandle != "@kal" {
		t.Errorf("lastDMHandle = %q, want %q", m.lastDMHandle, "@kal")
	}
}

func TestHandleOrgActionResultCreate(t *testing.T) {
	fake := newFakeRelayClient()
	m := newTestModelWithRelay(t, fake)

	newModel, _ := m.handleOrgActionResult(orgActionResultMsg{kind: "create", org: relay.OrgSummary{ID: 5, Name: "Acme"}})
	m = newModel.(Model)

	found := false
	for _, e := range m.history {
		if strings.Contains(e.Body, "Acme") && strings.Contains(e.Body, "admin") {
			found = true
		}
	}
	if !found {
		t.Error("expected a system note about the created org")
	}
}

func TestHandleOrgActionResultError(t *testing.T) {
	fake := newFakeRelayClient()
	m := newTestModelWithRelay(t, fake)

	newModel, _ := m.handleOrgActionResult(orgActionResultMsg{kind: "invite", err: errors.New("simulated failure")})
	m = newModel.(Model)

	found := false
	for _, e := range m.history {
		if strings.Contains(e.Body, "invite failed") {
			found = true
		}
	}
	if !found {
		t.Error("expected an error system note")
	}
}

func TestOrgCommandRequiresRelay(t *testing.T) {
	m := newTestModel(t) // no relay wired in
	newModel, cmd := m.handleOrgCommand([]string{"/org", "list"})
	m = newModel.(Model)
	if cmd != nil {
		t.Error("expected no cmd when there's no relay connection")
	}
	found := false
	for _, e := range m.history {
		if strings.Contains(e.Body, "require a relay server") {
			found = true
		}
	}
	if !found {
		t.Error("expected a system note explaining orgs need --server")
	}
}

func TestOrgCommandCreateDispatchesCmd(t *testing.T) {
	fake := newFakeRelayClient()
	fake.createOrg = relay.OrgSummary{ID: 1, Name: "Acme"}
	m := newTestModelWithRelay(t, fake)

	_, cmd := m.handleOrgCommand([]string{"/org", "create", "Acme"})
	if cmd == nil {
		t.Fatal("expected a non-nil cmd")
	}
	msg := cmd()
	result, ok := msg.(orgActionResultMsg)
	if !ok || result.kind != "create" || result.org.Name != "Acme" {
		t.Errorf("msg = %+v, want an orgActionResultMsg for create/Acme", msg)
	}
}

func TestOrgCommandInviteRejectsNonNumericID(t *testing.T) {
	fake := newFakeRelayClient()
	m := newTestModelWithRelay(t, fake)

	newModel, cmd := m.handleOrgCommand([]string{"/org", "invite", "not-a-number"})
	m = newModel.(Model)
	if cmd != nil {
		t.Error("expected no cmd for a non-numeric org id")
	}
	found := false
	for _, e := range m.history {
		if strings.Contains(e.Body, "must be a number") {
			found = true
		}
	}
	if !found {
		t.Error("expected a usage system note")
	}
}
