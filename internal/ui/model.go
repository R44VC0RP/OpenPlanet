package ui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"gamegateway/internal/activity"
	"gamegateway/internal/chat"
	"gamegateway/internal/ggp"
	"gamegateway/internal/store"
)

type ModelConfig struct {
	Player           store.Player
	Games            []store.Game
	Store            *store.Store
	Hub              *chat.Hub
	Activity         *activity.Tracker
	Width            int
	Height           int
	GGPIssuer        string
	GGPSessionSecret string
}

type viewMode int

const (
	modeName viewMode = iota
	modeMenu
	modeSubmit
	modeAdmin
	modeGame
	modeError
)

const (
	submitFieldCount = 10
	maxGameCols      = 120
	maxGameRows      = 40
)

type Model struct {
	player        store.Player
	games         []store.Game
	store         *store.Store
	hub           *chat.Hub
	activity      *activity.Tracker
	issuer        string
	sessionSecret string

	width      int
	height     int
	mode       viewMode
	selected   int
	nameInput  string
	nameError  string
	nameSaving bool

	submitIndex         int
	submitID            string
	submitName          string
	submitDescription   string
	submitImageRef      string
	submitContainerPort string
	submitMinCols       string
	submitMinRows       string
	submitMaxPlayers    string
	submitSessionSecret string
	submitSupportsMouse bool
	submitSaving        bool
	submitMessage       string

	adminGames    []store.Game
	adminSelected int
	adminLoading  bool
	adminMessage  string

	roomID       string
	activeGame   *store.Game
	activeGameID string
	gameClient   *ggp.Client
	surface      Surface
	gameTitle    string
	gameStatus   string
	gameMessage  string
	chatInput    string
	chatOpen     bool
	exitArmed    bool
	messages     []store.ChatMessage
	leaderboard  []store.LeaderboardEntry
	unsubscribe  func()
	chatEvents   <-chan store.ChatMessage
	errorTitle   string
	errorText    string
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
type gamesLoadedMsg struct{ games []store.Game }
type gamesLoadFailedMsg struct{ err error }
type submittedGamesLoadedMsg struct{ games []store.Game }
type submittedGamesFailedMsg struct{ err error }
type submitGameDoneMsg struct{ game store.Game }
type submitGameFailedMsg struct{ err error }
type adminActionDoneMsg struct{ message string }
type adminActionFailedMsg struct{ err error }
type activityTickMsg struct{}

func NewModel(cfg ModelConfig) Model {
	mode := modeMenu
	if !cfg.Player.NameConfirmed {
		mode = modeName
	}
	return Model{
		player:              cfg.Player,
		games:               cfg.Games,
		store:               cfg.Store,
		hub:                 cfg.Hub,
		activity:            cfg.Activity,
		issuer:              cfg.GGPIssuer,
		sessionSecret:       cfg.GGPSessionSecret,
		width:               max(cfg.Width, 80),
		height:              max(cfg.Height, 24),
		mode:                mode,
		nameInput:           cfg.Player.DisplayName,
		submitContainerPort: "8081",
		submitMinCols:       "80",
		submitMinRows:       "24",
		submitMaxPlayers:    "1",
	}
}

func NewErrorModel(title string, err error) ErrorModel {
	return ErrorModel{title: title, err: err, style: lipgloss.NewStyle().Foreground(lipgloss.Color("#f87171")).Bold(true)}
}

func (m Model) Init() tea.Cmd { return tickActivity() }

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
	case activityTickMsg:
		return m, tickActivity()
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
	case gamesLoadedMsg:
		m.games = msg.games
		m.selected = min(m.selected, max(m.menuItemCount()-1, 0))
		return m, nil
	case gamesLoadFailedMsg:
		m.mode = modeError
		m.errorTitle = "Game registry lookup failed"
		m.errorText = msg.err.Error()
		return m, nil
	case submittedGamesLoadedMsg:
		m.adminGames = msg.games
		m.adminLoading = false
		m.adminSelected = min(m.adminSelected, max(len(m.adminGames)-1, 0))
		return m, nil
	case submittedGamesFailedMsg:
		m.adminLoading = false
		m.adminMessage = msg.err.Error()
		return m, nil
	case submitGameDoneMsg:
		m.submitSaving = false
		m.submitMessage = "Submitted " + msg.game.Name + " for admin review."
		m.resetSubmitForm()
		return m, nil
	case submitGameFailedMsg:
		m.submitSaving = false
		m.submitMessage = msg.err.Error()
		return m, nil
	case adminActionDoneMsg:
		m.adminMessage = msg.message
		m.adminLoading = true
		return m, loadSubmittedGamesCmd(m.store)
	case adminActionFailedMsg:
		m.adminMessage = msg.err.Error()
		return m, nil
	case gameConnectedMsg:
		m.leaveActiveGamePresence()
		m.activeGame = &msg.game
		m.activeGameID = msg.game.ID
		m.activity.Enter(msg.game.ID, m.player.ID)
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
		if msg.event.Presence != nil && msg.event.Presence.MaxPlayers > 0 {
			m.gameStatus = fmt.Sprintf("%d/%d players", len(msg.event.Presence.Players), msg.event.Presence.MaxPlayers)
		}
		if msg.event.Error != nil {
			m.leaveActiveGamePresence()
			m.gameStatus = "game disconnected: " + msg.event.Error.Error()
		}
		if m.gameClient == nil {
			return m, scoreCmd
		}
		return m, tea.Batch(waitGameEvent(m.gameClient.Events), scoreCmd)
	case gameDisconnectedMsg:
		m.leaveActiveGamePresence()
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
	case modeSubmit:
		view := tea.NewView(m.renderSubmit())
		view.AltScreen = true
		return view
	case modeAdmin:
		view := tea.NewView(m.renderAdmin())
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
	case modeSubmit:
		return m.handleSubmitKey(key)
	case modeAdmin:
		return m.handleAdminKey(key)
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
	count := m.menuItemCount()
	switch key {
	case "q":
		return m, tea.Quit
	case "up", "k":
		if m.selected > 0 {
			m.selected--
		}
	case "down", "j":
		if m.selected < count-1 {
			m.selected++
		}
	case "enter":
		if m.selected < len(m.games) {
			return m, connectGameCmd(m.store, m.player, m.games[m.selected], m.gameCols(), m.gameRows(), m.issuer, m.sessionSecret)
		}
		if m.selected == len(m.games) {
			m.mode = modeSubmit
			m.submitMessage = ""
			return m, nil
		}
		if m.player.Role == store.RoleAdmin {
			m.mode = modeAdmin
			m.adminLoading = true
			m.adminMessage = ""
			return m, loadSubmittedGamesCmd(m.store)
		}
	}
	return m, nil
}

func (m Model) handleSubmitKey(key string) (tea.Model, tea.Cmd) {
	if m.submitSaving {
		return m, nil
	}
	switch key {
	case "esc":
		m.mode = modeMenu
		return m, nil
	case "up", "shift+tab":
		if m.submitIndex > 0 {
			m.submitIndex--
		}
		return m, nil
	case "down", "tab":
		if m.submitIndex < submitFieldCount {
			m.submitIndex++
		}
		return m, nil
	case "enter":
		if m.submitIndex == submitFieldCount {
			m.submitSaving = true
			m.submitMessage = "Saving image submission..."
			return m, submitGameCmd(m.store, m.player, m.submitSubmission(), m.issuer)
		}
		if m.submitIndex == 9 {
			m.submitSupportsMouse = !m.submitSupportsMouse
			return m, nil
		}
		if m.submitIndex < submitFieldCount {
			m.submitIndex++
		}
		return m, nil
	case "backspace", "ctrl+h":
		m.setSubmitField(dropLastRune(m.submitField()))
		return m, nil
	case " ":
		if m.submitIndex == 9 {
			m.submitSupportsMouse = !m.submitSupportsMouse
			return m, nil
		}
	}
	if isPrintableKey(key) && m.submitIndex < 9 {
		m.setSubmitField(m.submitField() + key)
	}
	return m, nil
}

func (m Model) handleAdminKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc":
		m.mode = modeMenu
		return m, refreshGamesCmd(m.store)
	case "up", "k":
		if m.adminSelected > 0 {
			m.adminSelected--
		}
	case "down", "j":
		if m.adminSelected < len(m.adminGames)-1 {
			m.adminSelected++
		}
	case "r":
		m.adminLoading = true
		return m, loadSubmittedGamesCmd(m.store)
	case "c":
		if game, ok := m.selectedAdminGame(); ok {
			m.adminMessage = "Checking " + game.Name + "..."
			return m, checkSubmittedGameCmd(m.store, game, m.issuer)
		}
	case "a":
		if game, ok := m.selectedAdminGame(); ok {
			m.adminMessage = "Approving " + game.Name + "..."
			return m, approveGameCmd(m.store, m.player, game, m.issuer)
		}
	case "x":
		if game, ok := m.selectedAdminGame(); ok {
			return m, rejectGameCmd(m.store, m.player, game.ID)
		}
	case "enter":
		if game, ok := m.selectedAdminGame(); ok {
			return m, connectGameCmd(m.store, m.player, game, m.gameCols(), m.gameRows(), m.issuer, m.sessionSecret)
		}
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
	case "space":
		if len([]rune(m.chatInput)) < 240 {
			m.chatInput += " "
		}
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
	return min(max(m.width-4, 20), maxGameCols)
}

func (m Model) gameRows() int {
	return min(max(m.height-4, 8), maxGameRows)
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
	m.leaveActiveGamePresence()
	if m.unsubscribe != nil {
		m.unsubscribe()
		m.unsubscribe = nil
	}
	if m.gameClient != nil {
		_ = m.gameClient.SendLeave(ggp.LeaveReasonUserExit)
		_ = m.gameClient.Close()
		m.gameClient = nil
	}
	m.activeGame = nil
	m.activeGameID = ""
	m.gameStatus = ""
	m.gameMessage = ""
	m.chatInput = ""
	m.chatOpen = false
	m.exitArmed = false
	m.messages = nil
	m.leaderboard = nil
}

func (m *Model) leaveActiveGamePresence() {
	if m.activeGameID == "" {
		return
	}
	m.activity.Leave(m.activeGameID, m.player.ID)
	m.activeGameID = ""
}

func (m Model) menuItemCount() int {
	count := len(m.games) + 1
	if m.player.Role == store.RoleAdmin {
		count++
	}
	return count
}

func (m Model) selectedAdminGame() (store.Game, bool) {
	if m.adminSelected < 0 || m.adminSelected >= len(m.adminGames) {
		return store.Game{}, false
	}
	return m.adminGames[m.adminSelected], true
}

func (m *Model) resetSubmitForm() {
	m.submitIndex = 0
	m.submitID = ""
	m.submitName = ""
	m.submitDescription = ""
	m.submitImageRef = ""
	m.submitContainerPort = "8081"
	m.submitMinCols = "80"
	m.submitMinRows = "24"
	m.submitMaxPlayers = "1"
	m.submitSessionSecret = ""
	m.submitSupportsMouse = false
}

func (m Model) submitField() string {
	switch m.submitIndex {
	case 0:
		return m.submitID
	case 1:
		return m.submitName
	case 2:
		return m.submitDescription
	case 3:
		return m.submitImageRef
	case 4:
		return m.submitContainerPort
	case 5:
		return m.submitMinCols
	case 6:
		return m.submitMinRows
	case 7:
		return m.submitMaxPlayers
	case 8:
		return m.submitSessionSecret
	}
	return ""
}

func (m *Model) setSubmitField(value string) {
	switch m.submitIndex {
	case 0:
		m.submitID = strings.ToLower(value)
	case 1:
		m.submitName = value
	case 2:
		m.submitDescription = value
	case 3:
		m.submitImageRef = value
	case 4:
		m.submitContainerPort = keepDigits(value)
	case 5:
		m.submitMinCols = keepDigits(value)
	case 6:
		m.submitMinRows = keepDigits(value)
	case 7:
		m.submitMaxPlayers = keepDigits(value)
	case 8:
		m.submitSessionSecret = value
	}
}

func (m Model) submitSubmission() store.GameSubmission {
	minCols, _ := strconv.Atoi(defaultIfEmpty(m.submitMinCols, "80"))
	minRows, _ := strconv.Atoi(defaultIfEmpty(m.submitMinRows, "24"))
	maxPlayers, _ := strconv.Atoi(defaultIfEmpty(m.submitMaxPlayers, "1"))
	containerPort, _ := strconv.Atoi(defaultIfEmpty(m.submitContainerPort, "8081"))
	id := strings.TrimSpace(m.submitID)
	if id == "" {
		id = store.SlugifyGameID(m.submitName)
	}
	return store.GameSubmission{
		ID:            id,
		Name:          strings.TrimSpace(m.submitName),
		Description:   strings.TrimSpace(m.submitDescription),
		ImageRef:      strings.TrimSpace(m.submitImageRef),
		ContainerPort: containerPort,
		MinCols:       minCols,
		MinRows:       minRows,
		MaxPlayers:    maxPlayers,
		SupportsMouse: m.submitSupportsMouse,
		SessionSecret: strings.TrimSpace(m.submitSessionSecret),
	}
}

func connectGameCmd(db *store.Store, player store.Player, game store.Game, cols, rows int, issuer, sessionSecret string) tea.Cmd {
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

		sessionID := fmt.Sprintf("sess_%d", time.Now().UnixNano())
		ggpPlayer := ggp.Player{
			ID:                player.ID,
			Name:              player.DisplayName,
			SSHKeyFingerprint: player.Fingerprint,
		}
		capabilities := []string{
			ggp.CapRenderCell,
			ggp.CapInputKeyboard,
			ggp.CapChatBridge,
			ggp.CapScoreReport,
		}
		var auth *ggp.Auth
		authSecret := game.SessionSecret
		if authSecret == "" {
			authSecret = sessionSecret
		}
		if authSecret != "" {
			token, err := ggp.NewSessionToken(authSecret, ggp.SessionTokenParams{
				Issuer:    issuer,
				Audience:  game.ID,
				Player:    ggpPlayer,
				SessionID: sessionID,
				RoomID:    roomID,
				GameID:    game.ID,
				Endpoint:  game.EndpointURL,
				TTL:       90 * time.Second,
			})
			if err != nil {
				return connectFailedMsg{err: err}
			}
			capabilities = append(capabilities, ggp.CapAuthSession)
			auth = &ggp.Auth{Type: ggp.AuthTypeSessionJWT, Token: token}
		}

		client, err := ggp.Connect(ctx, game.EndpointURL, ggp.Hello{
			Type:         ggp.TypeHello,
			Protocol:     ggp.ProtocolCellV1,
			SessionID:    sessionID,
			RoomID:       roomID,
			Player:       ggpPlayer,
			Viewport:     ggp.Viewport{Cols: cols, Rows: rows},
			Capabilities: capabilities,
			Auth:         auth,
		})
		if err != nil {
			return connectFailedMsg{err: err}
		}

		return gameConnectedMsg{game: game, roomID: roomID, client: client, chats: chats, leaderboard: leaderboard}
	}
}

func refreshGamesCmd(db *store.Store) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		games, err := db.ListGames(ctx)
		if err != nil {
			return gamesLoadFailedMsg{err: err}
		}
		return gamesLoadedMsg{games: games}
	}
}

func loadSubmittedGamesCmd(db *store.Store) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		games, err := db.ListSubmittedGames(ctx)
		if err != nil {
			return submittedGamesFailedMsg{err: err}
		}
		return submittedGamesLoadedMsg{games: games}
	}
}

func submitGameCmd(db *store.Store, player store.Player, submission store.GameSubmission, issuer string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		submission.SubmittedBy = player.ID
		submission.LastCheckStatus = "pending-deploy"
		game, err := db.SubmitGame(ctx, submission)
		if err != nil {
			return submitGameFailedMsg{err: err}
		}
		return submitGameDoneMsg{game: game}
	}
}

func checkSubmittedGameCmd(db *store.Store, game store.Game, issuer string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, err := ggp.ProbeEndpoint(ctx, ggp.ProbeOptions{
			EndpointURL:   game.EndpointURL,
			GameID:        game.ID,
			Issuer:        issuer,
			SessionSecret: game.SessionSecret,
			MaxPlayers:    game.MaxPlayers,
			AllowInsecure: true,
			AllowPrivate:  true,
		})
		status, message := store.CheckStatusPassed, "Container check passed."
		if err != nil {
			status, message = store.CheckStatusFailed, err.Error()
		}
		if updateErr := db.UpdateGameCheck(ctx, game.ID, status, message); updateErr != nil {
			return adminActionFailedMsg{err: updateErr}
		}
		if err != nil {
			return adminActionFailedMsg{err: err}
		}
		return adminActionDoneMsg{message: message}
	}
}

func approveGameCmd(db *store.Store, reviewer store.Player, game store.Game, issuer string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, err := ggp.ProbeEndpoint(ctx, ggp.ProbeOptions{
			EndpointURL:   game.EndpointURL,
			GameID:        game.ID,
			Issuer:        issuer,
			SessionSecret: game.SessionSecret,
			MaxPlayers:    game.MaxPlayers,
			AllowInsecure: true,
			AllowPrivate:  true,
		})
		if err != nil {
			_ = db.UpdateGameCheck(ctx, game.ID, store.CheckStatusFailed, err.Error())
			return adminActionFailedMsg{err: err}
		}
		if err := db.UpdateGameCheck(ctx, game.ID, store.CheckStatusPassed, "Container check passed."); err != nil {
			return adminActionFailedMsg{err: err}
		}
		if err := db.ApproveGame(ctx, game.ID, reviewer); err != nil {
			return adminActionFailedMsg{err: err}
		}
		return adminActionDoneMsg{message: "Approved " + game.Name + "."}
	}
}

func rejectGameCmd(db *store.Store, reviewer store.Player, gameID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := db.RejectGame(ctx, gameID, reviewer, "Rejected from admin review."); err != nil {
			return adminActionFailedMsg{err: err}
		}
		return adminActionDoneMsg{message: "Rejected submission."}
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

func keepDigits(value string) string {
	var b strings.Builder
	for _, r := range value {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func defaultIfEmpty(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func tickActivity() tea.Cmd {
	return tea.Tick(5*time.Second, func(time.Time) tea.Msg {
		return activityTickMsg{}
	})
}
