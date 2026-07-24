package account

import (
	"errors"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/NotTesfamichael/tiru-emba/internal/relay"
)

type fakeClient struct {
	bio          relay.AccountBio
	bioErr       error
	unlockables  []relay.UnlockableInfo
	unlockErr    error
	redeemErr    error
	setActiveErr error
	redeemed     []int64
	activeSet    []int64
}

func (f *fakeClient) Bio() (relay.AccountBio, error) { return f.bio, f.bioErr }
func (f *fakeClient) ListUnlockables() ([]relay.UnlockableInfo, error) {
	return f.unlockables, f.unlockErr
}
func (f *fakeClient) RedeemUnlockable(id int64) error {
	f.redeemed = append(f.redeemed, id)
	return f.redeemErr
}
func (f *fakeClient) SetAvatar(id int64) error {
	f.activeSet = append(f.activeSet, id)
	return f.setActiveErr
}

// singleFlightClient models relay.Client's real one-request-at-a-time
// constraint (see Client's own doc comment): if a second call starts
// before the first one finishes, it errors instead of racing -- exactly
// what a real *relay.Client does, and how the tea.Batch(bioCmd,
// unlockablesCmd) bug was actually caught via live testing (a real
// "already in flight" error from the server).
type singleFlightClient struct {
	fakeClient
	inFlight bool
}

func (f *singleFlightClient) call(fn func() error) error {
	if f.inFlight {
		return errors.New("relay: a request is already in flight on this connection")
	}
	f.inFlight = true
	defer func() { f.inFlight = false }()
	return fn()
}

func (f *singleFlightClient) Bio() (relay.AccountBio, error) {
	var bio relay.AccountBio
	err := f.call(func() error {
		var err error
		bio, err = f.fakeClient.Bio()
		return err
	})
	return bio, err
}

func (f *singleFlightClient) ListUnlockables() ([]relay.UnlockableInfo, error) {
	var list []relay.UnlockableInfo
	err := f.call(func() error {
		var err error
		list, err = f.fakeClient.ListUnlockables()
		return err
	})
	return list, err
}

// runAllCmds fully drains a chain of Cmds -- each Cmd may itself return a
// message that produces a further Cmd (e.g. bioResultMsg -> unlockablesCmd)
// -- feeding each result back into Update, mirroring the real Bubble Tea
// runtime's loop closely enough to exercise a sequential-chain bug.
func runAllCmds(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	for i := 0; cmd != nil; i++ {
		if i > 20 {
			t.Fatal("runAllCmds: too many iterations, suspected infinite loop")
		}
		msg := cmd()
		m, cmd = m.Update(msg)
	}
	return m
}

func TestInitFetchesBioAndUnlockablesSequentiallyNotConcurrently(t *testing.T) {
	client := &singleFlightClient{fakeClient: fakeClient{
		bio:         relay.AccountBio{Handle: "@alex", Points: 10},
		unlockables: []relay.UnlockableInfo{{ID: 1, Name: "Shades"}},
	}}
	m := New(client)

	final := runAllCmds(t, m, m.Init())
	if final.err != "" {
		t.Fatalf("unexpected error (likely a concurrent-request race): %q", final.err)
	}
	if final.bio.Handle != "@alex" || final.bio.Points != 10 {
		t.Errorf("bio = %+v, want Handle=@alex Points=10", final.bio)
	}
	if len(final.unlockables) != 1 {
		t.Errorf("unlockables = %+v, want one entry", final.unlockables)
	}
	if final.loading {
		t.Error("expected loading=false once both fetches complete")
	}
}

func TestActionRefreshFetchesSequentiallyNotConcurrently(t *testing.T) {
	client := &singleFlightClient{fakeClient: fakeClient{
		bio:         relay.AccountBio{Handle: "@alex", Points: 5},
		unlockables: []relay.UnlockableInfo{{ID: 1, Name: "Shades", Owned: true}},
	}}
	m := New(client)
	m.loading = false

	final := runAllCmds(t, m, redeemCmd(client, 1))
	if final.err != "" {
		t.Fatalf("unexpected error (likely a concurrent-request race): %q", final.err)
	}
	if final.bio.Handle != "@alex" {
		t.Errorf("expected bio to be refreshed after the action, got %+v", final.bio)
	}
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

func TestEnterOnUnownedRowRedeems(t *testing.T) {
	client := &fakeClient{unlockables: []relay.UnlockableInfo{{ID: 1, Name: "Shades", Cost: 5}}}
	m := New(client)
	m.loading = false
	m.unlockables = client.unlockables

	_, cmd := m.Update(key("enter"))
	if cmd == nil {
		t.Fatal("expected a Cmd for redeeming")
	}
	cmd() // execute the returned tea.Cmd
	if len(client.redeemed) != 1 || client.redeemed[0] != 1 {
		t.Errorf("redeemed = %v, want [1]", client.redeemed)
	}
}

func TestEnterOnOwnedNotActiveRowEquips(t *testing.T) {
	client := &fakeClient{unlockables: []relay.UnlockableInfo{{ID: 1, Name: "Shades", Owned: true}}}
	m := New(client)
	m.loading = false
	m.unlockables = client.unlockables

	_, cmd := m.Update(key("enter"))
	if cmd == nil {
		t.Fatal("expected a Cmd for equipping")
	}
	cmd()
	if len(client.activeSet) != 1 || client.activeSet[0] != 1 {
		t.Errorf("activeSet = %v, want [1]", client.activeSet)
	}
	if len(client.redeemed) != 0 {
		t.Error("should not have redeemed an already-owned item")
	}
}

func TestEnterOnActiveRowIsNoop(t *testing.T) {
	client := &fakeClient{unlockables: []relay.UnlockableInfo{{ID: 1, Name: "Shades", Owned: true, Active: true}}}
	m := New(client)
	m.loading = false
	m.unlockables = client.unlockables

	_, cmd := m.Update(key("enter"))
	if cmd != nil {
		t.Error("expected no Cmd for an already-active item")
	}
}

func TestEscEmitsClosedMsg(t *testing.T) {
	m := New(&fakeClient{})
	_, cmd := m.Update(key("esc"))
	if cmd == nil {
		t.Fatal("expected a Cmd from esc")
	}
	if _, ok := cmd().(ClosedMsg); !ok {
		t.Error("expected esc to produce ClosedMsg")
	}
}

func TestBioErrorIsSurfaced(t *testing.T) {
	m := New(&fakeClient{})
	updated, _ := m.Update(bioResultMsg{err: errors.New("boom")})
	if updated.err == "" {
		t.Error("expected a non-empty error after a failed bio fetch")
	}
}

func TestCursorClampedToUnlockablesLength(t *testing.T) {
	m := New(&fakeClient{})
	m.unlockables = []relay.UnlockableInfo{{ID: 1}, {ID: 2}}
	for i := 0; i < 5; i++ {
		m, _ = m.Update(key("down"))
	}
	if m.cursor != 1 {
		t.Errorf("cursor = %d, want to clamp at 1", m.cursor)
	}
}
