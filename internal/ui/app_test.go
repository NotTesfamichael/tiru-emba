package ui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/NotTesfamichael/tiru-emba/internal/discovery"
	"github.com/NotTesfamichael/tiru-emba/internal/games/tictactoe"
	"github.com/NotTesfamichael/tiru-emba/internal/network"
	"github.com/NotTesfamichael/tiru-emba/internal/store"
	"github.com/NotTesfamichael/tiru-emba/internal/ui/orgselect"
)

func TestAppStartsOnChatScreen(t *testing.T) {
	app := NewApp(newTestModel(t))
	if app.screen != screenChat {
		t.Errorf("screen = %v, want screenChat", app.screen)
	}
	if got := app.View(); got != app.chat.View() {
		t.Error("View() should delegate to chat while on the chat screen")
	}
}

func TestAppSwitchesToGameOnAcceptedChallenge(t *testing.T) {
	app := NewApp(newTestModel(t))

	newApp, cmd := app.Update(gameChallengeResultMsg{opponent: "@kal", session: nil, accepted: true, err: nil})
	a := newApp.(App)

	if a.screen != screenGame {
		t.Errorf("screen = %v, want screenGame", a.screen)
	}
	if cmd == nil {
		t.Error("expected a non-nil Init cmd for the new game screen")
	}
}

func TestAppStaysOnChatWhenChallengeDeclined(t *testing.T) {
	app := NewApp(newTestModel(t))

	newApp, _ := app.Update(gameChallengeResultMsg{opponent: "@kal", accepted: false, reason: "declined"})
	a := newApp.(App)

	if a.screen != screenChat {
		t.Errorf("screen = %v, want screenChat (declined challenges shouldn't switch screens)", a.screen)
	}
	found := false
	for _, e := range a.chat.history {
		if e.Kind == store.KindSystem && strings.Contains(e.Body, "did not accept") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a 'did not accept' system note, history=%+v", a.chat.history)
	}
}

func TestAppSwitchesToGameOnAcceptedInvite(t *testing.T) {
	app := NewApp(newTestModel(t))

	newApp, cmd := app.Update(gameInviteAcceptedMsg{invite: network.GameInvite{From: "@kal"}, session: nil, err: nil})
	a := newApp.(App)

	if a.screen != screenGame {
		t.Errorf("screen = %v, want screenGame", a.screen)
	}
	if cmd == nil {
		t.Error("expected a non-nil Init cmd for the new game screen")
	}
}

func TestAppReturnsToChatOnGameOver(t *testing.T) {
	app := NewApp(newTestModel(t))
	app.screen = screenGame
	app.game = tictactoe.New(nil, tictactoe.X, "@me", "@kal")

	newApp, _ := app.Update(tictactoe.GameOverMsg{ResultText: "you win!"})
	a := newApp.(App)

	if a.screen != screenChat {
		t.Errorf("screen = %v, want screenChat after GameOverMsg", a.screen)
	}
	found := false
	for _, e := range a.chat.history {
		if e.Kind == store.KindSystem && e.Body == "you win!" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected the result text appended as a system note, history=%+v", a.chat.history)
	}
}

func TestAppDelegatesKeysToChatWhenOnChatScreen(t *testing.T) {
	app := NewApp(newTestModel(t))

	newApp, _ := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("h")})
	a := newApp.(App)

	if a.chat.input.Value() != "h" {
		t.Errorf("chat input = %q, want %q", a.chat.input.Value(), "h")
	}
}

func TestAppKeepsChatDimensionsFreshDuringGame(t *testing.T) {
	app := NewApp(newTestModel(t))
	app.screen = screenGame
	app.game = tictactoe.New(nil, tictactoe.X, "@me", "@kal")

	newApp, _ := app.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	a := newApp.(App)

	if a.chat.width != 120 || a.chat.height != 40 {
		t.Errorf("chat dimensions = %dx%d, want 120x40 (should update even while the game screen is active)", a.chat.width, a.chat.height)
	}
}

// TestChatModelGetsSizeImmediatelyAfterOrgSelect is a regression test for a
// real bug caught via live/manual testing: the chat Model built once
// org-select finishes was constructed well after Bubble Tea's one-time
// startup tea.WindowSizeMsg already fired, so it never received a size of
// its own and rendered "initializing..." forever.
func TestChatModelGetsSizeImmediatelyAfterOrgSelect(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	app := App{
		screen: screenOrgSelect,
		chatArgs: ChatArgs{
			Ctx:     context.Background(),
			SelfID:  "self-id",
			PeerC:   make(chan discovery.PeerSeen),
			MsgC:    make(chan network.Received),
			OfferC:  make(chan network.FileOffer),
			ResultC: make(chan network.FileResult),
			InviteC: make(chan network.GameInvite),
		},
		authHandle: "@me",
		width:      120,
		height:     40,
	}

	newApp, _ := app.Update(orgselect.SelectedMsg{OrgID: 7, OrgName: "Acme"})
	a := newApp.(App)

	if a.screen != screenChat {
		t.Fatalf("screen = %v, want screenChat", a.screen)
	}
	if a.chat.width != 120 || a.chat.height != 40 {
		t.Errorf("chat dimensions = %dx%d, want 120x40 immediately after org-select, not left at zero", a.chat.width, a.chat.height)
	}
	if view := a.View(); strings.Contains(view, "initializing") {
		t.Errorf("chat screen still shows the zero-size placeholder right after org-select: %q", view)
	}
}
