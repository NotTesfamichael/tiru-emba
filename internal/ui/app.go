package ui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/NotTesfamichael/tiru-emba/internal/config"
	"github.com/NotTesfamichael/tiru-emba/internal/discovery"
	"github.com/NotTesfamichael/tiru-emba/internal/games/ludo"
	"github.com/NotTesfamichael/tiru-emba/internal/games/tictactoe"
	"github.com/NotTesfamichael/tiru-emba/internal/network"
	"github.com/NotTesfamichael/tiru-emba/internal/relay"
	"github.com/NotTesfamichael/tiru-emba/internal/ui/account"
	"github.com/NotTesfamichael/tiru-emba/internal/ui/onboarding"
	"github.com/NotTesfamichael/tiru-emba/internal/ui/orgselect"
	"github.com/NotTesfamichael/tiru-emba/internal/ui/todo"
)

// screen identifies which view App is currently showing.
type screen int

const (
	screenChat screen = iota
	screenGame
	screenLudo
	screenOnboarding
	screenOrgSelect
	screenAccount
	screenTodo
)

// ChatArgs bundles everything the eventual chat Model needs to be
// constructed, gathered once at startup -- in relay mode, the chat Model
// itself can't be built until onboarding and org-select both finish (it
// needs an authenticated *relay.Client and a chosen org), so App holds
// onto these in the meantime.
type ChatArgs struct {
	Ctx     context.Context
	SelfID  string
	PeerC   <-chan discovery.PeerSeen
	MsgC    <-chan network.Received
	OfferC  <-chan network.FileOffer
	ResultC <-chan network.FileResult
	InviteC <-chan network.GameInvite

	NotifyEnabled bool
}

// App is the top-level Bubble Tea model. It owns which screen is active and
// delegates Init/Update/View to it, except for a small set of "navigation"
// messages (a game starting or ending, onboarding finishing, an org being
// chosen) that it intercepts itself before delegating -- the standard
// Bubble Tea pattern for a router over several sub-models: the parent
// type-switches on messages it cares about first, and falls through to
// whichever child is active otherwise.
type App struct {
	screen screen
	chat   Model
	game   tictactoe.Model
	ludo   ludo.Model

	onboarding onboarding.Model
	orgSelect  orgselect.Model
	account    account.Model
	todo       todo.Model
	chatArgs   ChatArgs

	// authHandle/authClient are set once onboarding.AuthenticatedMsg
	// arrives, and consumed once orgselect.SelectedMsg arrives to finally
	// construct the chat Model -- the values a relay-mode chat Model needs
	// but that only become known once authentication succeeds.
	authHandle string
	authClient *relay.Client

	// width/height are the last known terminal size. Bubble Tea only ever
	// delivers tea.WindowSizeMsg once at startup (and again on an actual
	// resize) -- not every time a new sub-model appears -- so the chat
	// Model built later, once org-select finishes, needs this fed to it
	// explicitly right after construction or it renders "initializing..."
	// forever.
	width, height int
}

// NewApp constructs the router already on the chat screen -- used for
// LAN-only mode (no --server), which skips onboarding/org-select entirely.
func NewApp(chat Model) App {
	return App{screen: screenChat, chat: chat}
}

// NewAppWithOnboarding constructs the router starting on the onboarding
// screen -- used whenever --server is set. chatArgs bundles everything the
// eventual chat Model will need once onboarding and org-select both finish.
func NewAppWithOnboarding(onboard onboarding.Model, chatArgs ChatArgs) App {
	return App{screen: screenOnboarding, onboarding: onboard, chatArgs: chatArgs}
}

func (a App) Init() tea.Cmd {
	if a.screen == screenOnboarding {
		return a.onboarding.Init()
	}
	return a.chat.Init()
}

func (a App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		// Keep the chat screen's dimensions fresh even while another screen
		// is active, so returning to chat after a mid-game resize doesn't
		// render with stale width/height.
		a.width, a.height = msg.Width, msg.Height
		newChat, _ := a.chat.Update(msg)
		a.chat = newChat.(Model)
		switch a.screen {
		case screenGame:
			game, cmd := a.game.Update(msg)
			a.game = game
			return a, cmd
		case screenLudo:
			lg, cmd := a.ludo.Update(msg)
			a.ludo = lg
			return a, cmd
		case screenOnboarding:
			ob, cmd := a.onboarding.Update(msg)
			a.onboarding = ob
			return a, cmd
		case screenOrgSelect:
			os, cmd := a.orgSelect.Update(msg)
			a.orgSelect = os
			return a, cmd
		case screenAccount:
			acc, cmd := a.account.Update(msg)
			a.account = acc
			return a, cmd
		case screenTodo:
			td, cmd := a.todo.Update(msg)
			a.todo = td
			return a, cmd
		}
		return a, nil

	case openAccountMsg:
		a.screen = screenAccount
		a.account = account.New(a.chat.relay)
		return a, a.account.Init()

	case account.ClosedMsg:
		a.screen = screenChat
		return a, nil

	case openTodoMsg:
		a.screen = screenTodo
		a.todo = todo.New(a.chat.relay, a.chat.currentOrgID, a.chat.currentOrgName, a.chat.handle)
		return a, a.todo.Init()

	case todo.ClosedMsg:
		a.screen = screenChat
		return a, nil

	case onboarding.AuthenticatedMsg:
		// Session token/handle/server persisted here so the next launch
		// can auto-resume -- org selection is still always required
		// afresh next, regardless of whether this session was itself
		// resumed.
		err := config.Update(func(c *config.Config) {
			c.Handle = msg.Handle
			c.SessionToken = msg.Token
			c.SessionExpiresAt = msg.ExpiresAt
			c.WLANStatus = "connected"
		})
		_ = err // best-effort; a failed save just means the next run logs in fresh
		a.authHandle = msg.Handle
		a.authClient = msg.Client
		a.screen = screenOrgSelect
		a.orgSelect = orgselect.New(msg.Client, msg.IsAdmin)
		return a, a.orgSelect.Init()

	case orgselect.SelectedMsg:
		orgID := msg.OrgID
		a.screen = screenChat
		a.chat = New(
			a.chatArgs.Ctx, a.chatArgs.SelfID, a.authHandle,
			a.chatArgs.PeerC, a.chatArgs.MsgC, a.chatArgs.OfferC, a.chatArgs.ResultC, a.chatArgs.InviteC,
			a.authClient, a.chatArgs.NotifyEnabled, &orgID, msg.OrgName,
		)
		// The chat Model is only just now being constructed, well after
		// Bubble Tea's one-time startup tea.WindowSizeMsg already fired --
		// without this, it would render "initializing..." forever, never
		// receiving a size on its own.
		newChat, _ := a.chat.Update(tea.WindowSizeMsg{Width: a.width, Height: a.height})
		a.chat = newChat.(Model)
		return a, a.chat.Init()

	case gameChallengeResultMsg:
		if msg.err == nil && msg.accepted {
			a.screen = screenGame
			a.game = tictactoe.New(msg.session, tictactoe.X, a.chat.Handle(), msg.opponent)
			return a, a.game.Init()
		}
		newChat, cmd := a.chat.Update(msg)
		a.chat = newChat.(Model)
		return a, cmd

	case gameInviteAcceptedMsg:
		if msg.err == nil {
			switch msg.invite.GameType {
			case "ludo":
				a.screen = screenLudo
				a.ludo = ludo.NewGuest(a.chat.Handle(), msg.session)
				return a, a.ludo.Init()
			default: // "tictactoe"
				a.screen = screenGame
				a.game = tictactoe.New(msg.session, tictactoe.O, a.chat.Handle(), msg.invite.From)
				return a, a.game.Init()
			}
		}
		newChat, cmd := a.chat.Update(msg)
		a.chat = newChat.(Model)
		return a, cmd

	case tictactoe.GameOverMsg:
		a.screen = screenChat
		a.chat = a.chat.WithSystemNote(msg.ResultText)
		a.game = tictactoe.Model{}
		return a, nil

	case startLudoMsg:
		a.screen = screenLudo
		a.ludo = ludo.New(a.chat.Handle(), msg.numAI)
		return a, a.ludo.Init()

	case startNetworkedLudoMsg:
		a.screen = screenLudo
		sessions := make([]ludo.Session, len(msg.sessions))
		for i, s := range msg.sessions {
			sessions[i] = s
		}
		a.ludo = ludo.NewHost(a.chat.Handle(), msg.guestHandles, sessions)
		return a, a.ludo.Init()

	case ludo.GameOverMsg:
		a.screen = screenChat
		a.chat = a.chat.WithSystemNote(msg.ResultText)
		a.ludo = ludo.Model{}
		return a, nil
	}

	switch a.screen {
	case screenGame:
		game, cmd := a.game.Update(msg)
		a.game = game
		return a, cmd
	case screenLudo:
		lg, cmd := a.ludo.Update(msg)
		a.ludo = lg
		return a, cmd
	case screenOnboarding:
		ob, cmd := a.onboarding.Update(msg)
		a.onboarding = ob
		return a, cmd
	case screenOrgSelect:
		os, cmd := a.orgSelect.Update(msg)
		a.orgSelect = os
		return a, cmd
	case screenAccount:
		acc, cmd := a.account.Update(msg)
		a.account = acc
		return a, cmd
	case screenTodo:
		td, cmd := a.todo.Update(msg)
		a.todo = td
		return a, cmd
	}

	newChat, cmd := a.chat.Update(msg)
	a.chat = newChat.(Model)
	return a, cmd
}

func (a App) View() string {
	switch a.screen {
	case screenGame:
		return a.game.View()
	case screenLudo:
		return a.ludo.View()
	case screenOnboarding:
		return a.onboarding.View()
	case screenOrgSelect:
		return a.orgSelect.View()
	case screenAccount:
		return a.account.View()
	case screenTodo:
		return a.todo.View()
	}
	return a.chat.View()
}

// Close releases resources owned by whichever screen is/was active: the
// chat screen's history file and relay connection, or (if the program
// quit before onboarding ever finished) the dialed-but-not-yet-authenticated
// connection onboarding was holding. Call once after the Bubble Tea
// program exits.
func (a App) Close() {
	a.chat.Close()
	if a.screen == screenOnboarding {
		a.onboarding.Close()
	}
}
