package ui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/NotTesfamichael/tiru-emba/internal/games/tictactoe"
)

// screen identifies which view App is currently showing.
type screen int

const (
	screenChat screen = iota
	screenGame
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
		// Keep the chat screen's dimensions fresh even while the game screen
		// is active, so returning to chat after a mid-game resize doesn't
		// render with stale width/height.
		newChat, _ := a.chat.Update(msg)
		a.chat = newChat.(Model)
		if a.screen == screenGame {
			game, cmd := a.game.Update(msg)
			a.game = game
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
			a.screen = screenGame
			a.game = tictactoe.New(msg.session, tictactoe.O, a.chat.Handle(), msg.invite.From)
			return a, a.game.Init()
		}
		newChat, cmd := a.chat.Update(msg)
		a.chat = newChat.(Model)
		return a, cmd

	case tictactoe.GameOverMsg:
		a.screen = screenChat
		a.chat = a.chat.WithSystemNote(msg.ResultText)
		a.game = tictactoe.Model{}
		return a, nil
	}

	if a.screen == screenGame {
		game, cmd := a.game.Update(msg)
		a.game = game
		return a, cmd
	}

	newChat, cmd := a.chat.Update(msg)
	a.chat = newChat.(Model)
	return a, cmd
}

func (a App) View() string {
	if a.screen == screenGame {
		return a.game.View()
	}
	return a.chat.View()
}

// Close releases resources owned by the chat screen (its history file).
// Call once after the Bubble Tea program exits.
func (a App) Close() {
	a.chat.Close()
}
