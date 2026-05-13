package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"gamegateway/internal/chat"
	"gamegateway/internal/ggp"
	"gamegateway/internal/store"
)

type ModelConfig struct {
	Player store.Player
	Games  []store.Game
	Store  *store.Store
	Hub    *chat.Hub
	Width  int
	Height int
}

type viewMode int

const (
	modeName viewMode = iota
	modeMenu
	modeGame
	modeError
)

type Model struct {
	player store.Player
	games  []store.Game
	store  *store.Store
	hub    *chat.Hub

	width      int
	height     int
	mode       viewMode
	selected   int
	nameInput  string
	nameError  string
	nameSaving bool

	roomID      string
	activeGame  *store.Game
	gameClient  *ggp.Client
	surface     Surface
	gameTitle   string
	gameStatus  string
	gameMessage string
	chatInput   string
	chatOpen    bool
	exitArmed   bool
	messages    []store.ChatMessage
	leaderboard []store.LeaderboardEntry
	unsubscribe func()
	chatEvents  <-chan store.ChatMessage
	errorTitle  string
	errorText   string
}

type ErrorModel struct {
	title string
	err   error
	style lipgloss.Style
}

type gameConnectedMsg struct {
	game        store.Game
	roomID      string
	client      *ggp.Client
	chats       []store.ChatMessage
	leaderboard []store.LeaderboardEntry
}

type gameEventMsg struct{ event ggp.Event }
type gameDisconnectedMsg struct{}
type chatMsg struct{ message store.ChatMessage }
type chatClosedMsg struct{}
type chatPostedMsg struct{}
type leaderboardLoadedMsg struct{ entries []store.LeaderboardEntry }
type leaderboardFailedMsg struct{ err error }
type nameSavedMsg struct{ player store.Player }
type nameSaveFailedMsg struct{ err error }
type connectFailedMsg struct{ err error }
type chatPostFailedMsg struct{ err error }

func NewModel(cfg ModelConfig) Model {
	mode := modeMenu
	if !cfg.Player.NameConfirmed {
		mode = modeName
	}
	return Model{
		player:    cfg.Player,
		games:     cfg.Games,
		store:     cfg.Store,
		hub:       cfg.Hub,
		width:     max(cfg.Width, 80),
		height:    max(cfg.Height, 24),
		mode:      mode,
		nameInput: cfg.Player.DisplayName,
	}
}

func NewErrorModel(title string, err error) ErrorModel {
	return ErrorModel{title: title, err: err, style: lipgloss.NewStyle().Foreground(lipgloss.Color("#f87171")).Bold(true)}
}

func (m Model) Init() tea.Cmd { return nil }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		if m.mode == modeGame {
			m.resizeSurface()
			if m.gameClient != nil {
				cols, rows := m.surface.Viewport()
				_ = m.gameClient.SendResize(cols, rows)
			}
		}
		return m, nil
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	case nameSavedMsg:
		m.player = msg.player
		m.nameInput = msg.player.DisplayName
		m.nameError = ""
		m.nameSaving = false
		m.mode = modeMenu
		return m, nil
	case nameSaveFailedMsg:
		m.nameError = msg.err.Error()
		m.nameSaving = false
		return m, nil
	case gameConnectedMsg:
		m.activeGame = &msg.game
		m.roomID = msg.roomID
		m.gameClient = msg.client
		m.messages = msg.chats
		m.leaderboard = msg.leaderboard
		m.mode = modeGame
		m.chatOpen = false
		m.exitArmed = false
		m.gameStatus = "connected"
		m.resizeSurface()
		m.chatEvents, m.unsubscribe = m.hub.Subscribe(msg.roomID)
		return m, tea.Batch(waitGameEvent(msg.client.Events), waitChatEvent(m.chatEvents))
	case connectFailedMsg:
		m.mode = modeError
		m.errorTitle = "Game connection failed"
		m.errorText = msg.err.Error()
		return m, nil
	case gameEventMsg:
		if msg.event.Ready != nil {
			m.gameTitle = msg.event.Ready.Title
			m.gameStatus = "ready"
			if m.gameClient != nil {
				cols, rows := m.surface.Viewport()
				_ = m.gameClient.SendResize(cols, rows)
			}
		}
		if msg.event.Frame != nil {
			m.surface.Apply(*msg.event.Frame)
			if msg.event.Frame.Status != "" {
				m.gameMessage = msg.event.Frame.Status
			}
		}
		var scoreCmd tea.Cmd
		if msg.event.Score != nil && m.activeGame != nil {
			scoreCmd = recordScoreCmd(m.store, m.player, m.activeGame.ID, msg.event.Score.Value)
		}
		if msg.event.Error != nil {
			m.gameStatus = "game disconnected: " + msg.event.Error.Error()
		}
		if m.gameClient == nil {
			return m, scoreCmd
		}
		return m, tea.Batch(waitGameEvent(m.gameClient.Events), scoreCmd)
	case gameDisconnectedMsg:
		m.gameStatus = "game disconnected"
		return m, nil
	case chatMsg:
		m.messages = append(m.messages, msg.message)
		if len(m.messages) > 200 {
			m.messages = m.messages[len(m.messages)-200:]
		}
		return m, waitChatEvent(m.chatEvents)
	case chatClosedMsg:
		return m, nil
	case chatPostedMsg:
		return m, nil
	case chatPostFailedMsg:
		m.gameStatus = "chat failed: " + msg.err.Error()
		return m, nil
	case leaderboardLoadedMsg:
		m.leaderboard = msg.entries
		return m, nil
	case leaderboardFailedMsg:
		m.gameStatus = "leaderboard failed: " + msg.err.Error()
		return m, nil
	}
	return m, nil
}

func (m Model) View() tea.View {
	switch m.mode {
	case modeName:
		view := tea.NewView(m.renderName())
		view.AltScreen = true
		return view
	case modeError:
		return tea.NewView(m.renderError())
	case modeGame:
		view := tea.NewView(m.renderGame())
		view.AltScreen = true
		return view
	default:
		view := tea.NewView(m.renderMenu())
		view.AltScreen = true
		return view
	}
}

func (m ErrorModel) Init() tea.Cmd { return nil }

func (m ErrorModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m ErrorModel) View() tea.View {
	return tea.NewView(m.style.Render(m.title) + "\n\n" + m.err.Error() + "\n\nPress q to quit.")
}

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if key == "ctrl+c" {
		m.closeActiveGame()
		return m, tea.Quit
	}

	switch m.mode {
	case modeName:
		return m.handleNameKey(key)
	case modeMenu:
		return m.handleMenuKey(key)
	case modeGame:
		return m.handleGameKey(key)
	case modeError:
		if key == "esc" || key == "q" {
			m.mode = modeMenu
		}
	}
	return m, nil
}

func (m Model) handleNameKey(key string) (tea.Model, tea.Cmd) {
	if m.nameSaving {
		return m, nil
	}

	switch key {
	case "enter":
		name := strings.TrimSpace(m.nameInput)
		if name == "" {
			m.nameError = "Name is required. Use letters and numbers only."
			return m, nil
		}
		m.nameSaving = true
		m.nameError = ""
		return m, saveNameCmd(m.store, m.player, name)
	case "backspace", "ctrl+h":
		m.nameInput = dropLastRune(m.nameInput)
		m.nameError = ""
		return m, nil
	}

	if isNameKey(key) {
		if len(m.nameInput) < 12 {
			m.nameInput += key
			m.nameError = ""
		}
		return m, nil
	}

	if isPrintableKey(key) {
		m.nameError = "Only letters and numbers are allowed."
	}
	return m, nil
}

func (m Model) handleMenuKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "q":
		return m, tea.Quit
	case "up", "k":
		if m.selected > 0 {
			m.selected--
		}
	case "down", "j":
		if m.selected < len(m.games)-1 {
			m.selected++
		}
	case "enter":
		if len(m.games) == 0 {
			return m, nil
		}
		return m, connectGameCmd(m.store, m.player, m.games[m.selected], m.gameCols(), m.gameRows())
	}
	return m, nil
}

func (m Model) handleGameKey(key string) (tea.Model, tea.Cmd) {
	if m.chatOpen {
		switch key {
		case "tab", "esc":
			m.closeChat()
			return m, nil
		default:
			return m.handleChatKey(key)
		}
	}

	switch key {
	case "tab":
		m.openChat()
		return m, nil
	case "esc":
		if m.exitArmed {
			m.closeActiveGame()
			m.mode = modeMenu
			return m, nil
		}
		m.exitArmed = true
		return m, nil
	}

	m.exitArmed = false
	return m.forwardGameKey(key)
}

func (m Model) handleChatKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "enter":
		body := strings.TrimSpace(m.chatInput)
		m.chatInput = ""
		if body == "" {
			return m, nil
		}
		return m, postChatCmd(m.store, m.hub, m.roomID, m.player, body)
	case "backspace", "ctrl+h":
		m.chatInput = dropLastRune(m.chatInput)
		return m, nil
	}

	if isPrintableKey(key) && len([]rune(m.chatInput)) < 240 {
		m.chatInput += key
	}
	return m, nil
}

func (m Model) forwardGameKey(key string) (tea.Model, tea.Cmd) {
	if m.gameClient == nil {
		return m, nil
	}
	input := ggp.Input{Type: ggp.TypeInput, Kind: "key", Key: key}
	if isPrintableKey(key) {
		input.Text = key
	}
	_ = m.gameClient.SendInput(input)
	return m, nil
}

func (m *Model) resizeSurface() {
	m.surface.Resize(m.gameCols(), m.gameRows())
}

func (m Model) gameCols() int {
	return max(m.width-4, 20)
}

func (m Model) gameRows() int {
	return max(m.height-4, 8)
}

func (m *Model) openChat() {
	m.chatOpen = true
	m.exitArmed = false
	if m.gameClient != nil {
		_ = m.gameClient.SendFocus(false)
	}
}

func (m *Model) closeChat() {
	m.chatOpen = false
	m.exitArmed = false
	if m.gameClient != nil {
		_ = m.gameClient.SendFocus(true)
	}
}

func (m *Model) closeActiveGame() {
	if m.unsubscribe != nil {
		m.unsubscribe()
		m.unsubscribe = nil
	}
	if m.gameClient != nil {
		_ = m.gameClient.Close()
		m.gameClient = nil
	}
	m.activeGame = nil
	m.gameStatus = ""
	m.gameMessage = ""
	m.chatInput = ""
	m.chatOpen = false
	m.exitArmed = false
	m.messages = nil
	m.leaderboard = nil
}

func connectGameCmd(db *store.Store, player store.Player, game store.Game, cols, rows int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()

		roomID, err := db.EnsureRoom(ctx, game.ID)
		if err != nil {
			return connectFailedMsg{err: err}
		}

		chats, err := db.RecentChat(ctx, roomID, 50)
		if err != nil {
			return connectFailedMsg{err: err}
		}

		leaderboard, err := db.Leaderboard(ctx, game.ID, 10)
		if err != nil {
			return connectFailedMsg{err: err}
		}

		client, err := ggp.Connect(ctx, game.EndpointURL, ggp.Hello{
			Type:      ggp.TypeHello,
			Protocol:  ggp.ProtocolCellV1,
			SessionID: fmt.Sprintf("sess_%d", time.Now().UnixNano()),
			RoomID:    roomID,
			Player: ggp.Player{
				ID:                player.ID,
				Name:              player.DisplayName,
				SSHKeyFingerprint: player.Fingerprint,
			},
			Viewport: ggp.Viewport{Cols: cols, Rows: rows},
			Capabilities: []string{
				ggp.CapRenderCell,
				ggp.CapInputKeyboard,
				ggp.CapChatBridge,
				ggp.CapScoreReport,
			},
		})
		if err != nil {
			return connectFailedMsg{err: err}
		}

		return gameConnectedMsg{game: game, roomID: roomID, client: client, chats: chats, leaderboard: leaderboard}
	}
}

func waitGameEvent(events <-chan ggp.Event) tea.Cmd {
	return func() tea.Msg {
		event, ok := <-events
		if !ok {
			return gameDisconnectedMsg{}
		}
		return gameEventMsg{event: event}
	}
}

func waitChatEvent(events <-chan store.ChatMessage) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-events
		if !ok {
			return chatClosedMsg{}
		}
		return chatMsg{message: msg}
	}
}

func postChatCmd(db *store.Store, hub *chat.Hub, roomID string, player store.Player, body string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		msg, err := db.InsertChat(ctx, roomID, player, body)
		if err != nil {
			return chatPostFailedMsg{err: err}
		}
		hub.Publish(msg)
		return chatPostedMsg{}
	}
}

func recordScoreCmd(db *store.Store, player store.Player, gameID string, value int64) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := db.UpsertScore(ctx, gameID, player, value); err != nil {
			return leaderboardFailedMsg{err: err}
		}
		entries, err := db.Leaderboard(ctx, gameID, 10)
		if err != nil {
			return leaderboardFailedMsg{err: err}
		}
		return leaderboardLoadedMsg{entries: entries}
	}
}

func saveNameCmd(db *store.Store, player store.Player, name string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		updated, err := db.UpdatePlayerDisplayName(ctx, player, name)
		if err != nil {
			return nameSaveFailedMsg{err: err}
		}
		return nameSavedMsg{player: updated}
	}
}

func isNameKey(key string) bool {
	if len(key) != 1 {
		return false
	}
	r := rune(key[0])
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

func isPrintableKey(key string) bool {
	return len([]rune(key)) == 1 && key != "\x00"
}

func dropLastRune(value string) string {
	runes := []rune(value)
	if len(runes) == 0 {
		return value
	}
	return string(runes[:len(runes)-1])
}
