package ui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/NotTesfamichael/tiru-emba/internal/games/ludo"
	"github.com/NotTesfamichael/tiru-emba/internal/games/tictactoe"
)

// screen identifies which view App is currently showing.
type screen int

const (
	screenChat screen = iota
	screenGame
	screenLudo
)

// App is the top-level Bubble Tea model. It owns which screen is active and
// delegates Init/Update/View to it, except for a small set of "navigation"
// messages (a game starting or ending) that it intercepts itself before
// delegating -- the standard Bubble Tea pattern for a router over several
// sub-models: the parent type-switches on messages it cares about first,
// and falls through to whichever child is active otherwise.
type App struct {
	screen screen
	chat   Model
	game   tictactoe.Model
	ludo   ludo.Model
}

// NewApp constructs the router, starting on the chat screen.
func NewApp(chat Model) App {
	return App{screen: screenChat, chat: chat}
}

func (a App) Init() tea.Cmd {
	return a.chat.Init()
}

func (a App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		// Keep the chat screen's dimensions fresh even while another screen
		// is active, so returning to chat after a mid-game resize doesn't
		// render with stale width/height.
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
		}
		return a, nil

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
	}
	return a.chat.View()
}

// Close releases resources owned by the chat screen (its history file).
// Call once after the Bubble Tea program exits.
func (a App) Close() {
	a.chat.Close()
}
