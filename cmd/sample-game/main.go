package main

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"gamegateway/internal/ggp"
)

const (
	gameID       = "cell-garden"
	worldWidth   = 180
	worldHeight  = 92
	maxPlayers   = 16
	helloTimeout = 5 * time.Second
)

type gameServer struct {
	secret   string
	issuer   string
	endpoint string
	replay   *ggp.ReplayCache

	mu    sync.Mutex
	rooms map[string]*room
}

type room struct {
	id       string
	players  map[string]*actor
	sessions map[*session]struct{}
	mu       sync.Mutex
}

type actor struct {
	id    string
	name  string
	x     int
	y     int
	color string
}

type session struct {
	conn    *websocket.Conn
	room    *room
	player  actor
	cols    int
	rows    int
	seq     int
	message string
	mu      sync.Mutex
}

type house struct {
	x int
	y int
	w int
	h int
}

var (
	upgrader = websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	houses   = []house{
		{x: 7, y: 5, w: 11, h: 6},
		{x: 30, y: 4, w: 13, h: 7},
		{x: 51, y: 9, w: 12, h: 6},
		{x: 14, y: 22, w: 13, h: 7},
		{x: 45, y: 23, w: 14, h: 6},
		{x: 78, y: 14, w: 16, h: 8},
		{x: 112, y: 10, w: 14, h: 7},
		{x: 136, y: 27, w: 17, h: 8},
		{x: 98, y: 47, w: 13, h: 7},
		{x: 35, y: 56, w: 15, h: 8},
		{x: 68, y: 68, w: 18, h: 8},
		{x: 126, y: 64, w: 15, h: 7},
		{x: 153, y: 50, w: 14, h: 7},
	}
	spawnPoints = [][2]int{{88, 38}, {84, 38}, {92, 38}, {88, 34}, {88, 42}, {35, 58}, {126, 58}, {154, 58}}
	nameColors  = []string{"#7dd3fc", "#f472b6", "#facc15", "#4ade80", "#fb923c", "#c084fc", "#2dd4bf", "#f87171"}
)

func main() {
	server := &gameServer{
		secret:   env("GGP_SESSION_SECRET", ""),
		issuer:   env("GGP_ISSUER", "gamegateway"),
		endpoint: env("GGP_ENDPOINT_URL", ""),
		replay:   ggp.NewReplayCache(),
		rooms:    make(map[string]*room),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/ggp", server.handleGGP)

	addr := ":" + env("PORT", "8081")
	mode := "single-player"
	if server.secret != "" {
		mode = "secure multiplayer"
	}
	log.Printf("sample RPG game listening on %s (%s)", addr, mode)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}

func (g *gameServer) handleGGP(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("upgrade failed: %v", err)
		return
	}
	defer conn.Close()
	conn.SetReadLimit(64 * 1024)

	hello, err := readHello(conn)
	if err != nil {
		_ = conn.WriteJSON(ggp.Error{Type: ggp.TypeError, Code: ggp.ErrorUnsupportedProtocol, Message: err.Error()})
		return
	}

	roomID := "session:" + hello.SessionID
	if g.secret != "" {
		if err := g.validateHello(hello); err != nil {
			_ = conn.WriteJSON(ggp.Error{Type: ggp.TypeError, Code: ggp.ErrorAuthInvalid, Message: err.Error()})
			return
		}
		roomID = hello.RoomID
	}

	s, err := g.join(conn, roomID, hello)
	if err != nil {
		_ = conn.WriteJSON(ggp.Error{Type: ggp.TypeError, Code: ggp.ErrorRoomFull, Message: err.Error()})
		return
	}
	defer func() {
		s.room.leave(s)
		s.room.broadcast()
	}()

	ready := ggp.Ready{
		Type:         ggp.TypeReady,
		Title:        "Meadow Village",
		TargetFPS:    8,
		Capabilities: []string{ggp.CapRenderCell, ggp.CapInputKeyboard},
	}
	if g.secret != "" {
		ready.Capabilities = append(ready.Capabilities, ggp.CapAuthSession, ggp.CapMultiplayer, ggp.CapPresence)
		ready.Multiplayer = &ggp.Multiplayer{Mode: ggp.MultiplayerModeRoom, MaxPlayers: maxPlayers, Presence: true}
	}
	_ = conn.WriteJSON(ready)
	s.room.broadcast()

	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if !s.handleMessage(msg) {
			return
		}
	}
}

func readHello(conn *websocket.Conn) (ggp.Hello, error) {
	_ = conn.SetReadDeadline(time.Now().Add(helloTimeout))
	_, payload, err := conn.ReadMessage()
	_ = conn.SetReadDeadline(time.Time{})
	if err != nil {
		return ggp.Hello{}, err
	}
	var hello ggp.Hello
	if err := json.Unmarshal(payload, &hello); err != nil {
		return ggp.Hello{}, err
	}
	if hello.Type != ggp.TypeHello || hello.Protocol != ggp.ProtocolCellV1 || hello.SessionID == "" || hello.Player.ID == "" {
		return ggp.Hello{}, errors.New("expected valid ggp.cell.v1 hello")
	}
	return hello, nil
}

func (g *gameServer) validateHello(hello ggp.Hello) error {
	if hello.Auth == nil || hello.Auth.Type != ggp.AuthTypeSessionJWT || hello.Auth.Token == "" {
		return errors.New("multiplayer requires gateway session auth")
	}
	_, err := ggp.ValidateSessionToken(g.secret, hello.Auth.Token, ggp.SessionTokenExpected{
		Issuer:      g.issuer,
		Audience:    gameID,
		Player:      hello.Player,
		SessionID:   hello.SessionID,
		RoomID:      hello.RoomID,
		GameID:      gameID,
		Endpoint:    g.endpoint,
		ReplayCache: g.replay,
	})
	return err
}

func (g *gameServer) join(conn *websocket.Conn, roomID string, hello ggp.Hello) (*session, error) {
	g.mu.Lock()
	r := g.rooms[roomID]
	if r == nil {
		r = &room{id: roomID, players: make(map[string]*actor), sessions: make(map[*session]struct{})}
		g.rooms[roomID] = r
	}
	g.mu.Unlock()

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.players[hello.Player.ID]; exists {
		return nil, errors.New("player already has an active session in this room")
	}
	if len(r.players) >= maxPlayers {
		return nil, errors.New("room is full")
	}
	spawn := spawnPoints[len(r.players)%len(spawnPoints)]
	player := actor{
		id:    hello.Player.ID,
		name:  hello.Player.Name,
		x:     spawn[0],
		y:     spawn[1],
		color: nameColors[len(r.players)%len(nameColors)],
	}
	r.players[player.id] = &player
	s := &session{
		conn:    conn,
		room:    r,
		player:  player,
		cols:    max(hello.Viewport.Cols, 40),
		rows:    max(hello.Viewport.Rows, 14),
		message: "Walk the village with arrow keys or WASD. Other players share this room.",
	}
	r.sessions[s] = struct{}{}
	return s, nil
}

func (r *room) leave(s *session) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.sessions, s)
	delete(r.players, s.player.id)
}

func (r *room) move(playerID, key string) string {
	dx, dy := 0, 0
	switch key {
	case "left", "h", "a":
		dx = -1
	case "right", "l", "d":
		dx = 1
	case "up", "k", "w":
		dy = -1
	case "down", "j", "s":
		dy = 1
	default:
		return ""
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	player := r.players[playerID]
	if player == nil {
		return "Player is no longer in this room."
	}
	nx, ny := player.x+dx, player.y+dy
	if blocked(nx, ny) || r.occupiedLocked(playerID, nx, ny) {
		return "That way is blocked. Try the village paths."
	}
	player.x, player.y = nx, ny
	return describeTile(nx, ny)
}

func (r *room) occupiedLocked(playerID string, x, y int) bool {
	for id, player := range r.players {
		if id != playerID && player.x == x && player.y == y {
			return true
		}
	}
	return false
}

func (r *room) broadcast() {
	sessions, players := r.snapshot()
	for _, s := range sessions {
		s.sendFrame(players)
	}
	if len(sessions) > 0 {
		presence := r.presence(players)
		for _, s := range sessions {
			_ = s.writeJSON(presence)
		}
	}
}

func (r *room) snapshot() ([]*session, []actor) {
	r.mu.Lock()
	defer r.mu.Unlock()
	sessions := make([]*session, 0, len(r.sessions))
	for s := range r.sessions {
		sessions = append(sessions, s)
	}
	players := make([]actor, 0, len(r.players))
	for _, player := range r.players {
		players = append(players, *player)
	}
	return sessions, players
}

func (r *room) presence(players []actor) ggp.Presence {
	presence := ggp.Presence{Type: ggp.TypePresence, RoomID: r.id, MaxPlayers: maxPlayers, Players: make([]ggp.PresencePlayer, 0, len(players))}
	for _, player := range players {
		presence.Players = append(presence.Players, ggp.PresencePlayer{ID: player.id, Name: player.name, State: "playing"})
	}
	return presence
}

func (s *session) handleMessage(payload []byte) bool {
	var envelope ggp.Envelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return true
	}

	switch envelope.Type {
	case ggp.TypeInput:
		var input ggp.Input
		if err := json.Unmarshal(payload, &input); err != nil {
			return true
		}
		if message := s.room.move(s.player.id, input.Key); message != "" {
			s.message = message
		}
		s.room.broadcast()
	case ggp.TypeResize:
		var resize ggp.Resize
		if err := json.Unmarshal(payload, &resize); err != nil {
			return true
		}
		s.cols = max(resize.Cols, 40)
		s.rows = max(resize.Rows, 14)
		_, players := s.room.snapshot()
		s.sendFrame(players)
	case ggp.TypeLeave:
		return false
	}
	return true
}

func (s *session) sendFrame(players []actor) {
	frame := ggp.Frame{
		Type:   ggp.TypeFrame,
		Mode:   ggp.FrameFull,
		Status: s.message,
		Cells:  s.renderCells(players),
	}
	s.mu.Lock()
	s.seq++
	frame.Seq = s.seq
	s.mu.Unlock()
	_ = s.writeJSON(frame)
}

func (s *session) writeJSON(value any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conn.WriteJSON(value)
}

func (s *session) renderCells(players []actor) []ggp.Cell {
	cols := max(s.cols, 40)
	rows := max(s.rows, 14)
	cells := make([]ggp.Cell, 0, cols*rows)
	self := s.player
	for _, player := range players {
		if player.id == s.player.id {
			self = player
			break
		}
	}

	viewRows := max(rows, 8)
	camX := clamp(self.x-cols/2, 0, max(worldWidth-cols, 0))
	camY := clamp(self.y-viewRows/2, 0, max(worldHeight-viewRows, 0))

	for y := 0; y < rows; y++ {
		for x := 0; x < cols; x++ {
			wx, wy := camX+x, camY+y
			ch, fg, bg := tile(wx, wy)
			if player, ok := playerAt(players, wx, wy); ok {
				ch, fg = playerGlyph(player, player.id == s.player.id), player.color
			}
			cells = append(cells, ggp.Cell{X: x, Y: y, Ch: ch, Fg: fg, Bg: bg})
		}
	}

	return cells
}

func playerAt(players []actor, x, y int) (actor, bool) {
	for _, player := range players {
		if player.x == x && player.y == y {
			return player, true
		}
	}
	return actor{}, false
}

func playerGlyph(player actor, self bool) string {
	if self {
		return "@"
	}
	name := strings.TrimSpace(player.name)
	if name == "" {
		return "?"
	}
	return strings.ToUpper(string([]rune(name)[0]))
}

func tile(x, y int) (string, string, string) {
	if x < 0 || y < 0 || x >= worldWidth || y >= worldHeight {
		return " ", "#94a3b8", "#1e293b"
	}
	if x == 0 || y == 0 || x == worldWidth-1 || y == worldHeight-1 {
		return " ", "#94a3b8", "#1e293b"
	}
	if isWater(x, y) {
		return " ", "#bae6fd", "#075985"
	}
	for _, h := range houses {
		if inHouse(x, y, h) {
			return houseTile(x, y, h)
		}
	}
	if onPath(x, y) {
		return " ", "#fef3c7", "#a16207"
	}
	if isTree(x, y) {
		return " ", "#bbf7d0", "#166534"
	}
	if (x*3+y*5)%19 == 0 {
		return " ", "#d9f99d", "#3f6212"
	}
	return " ", "#86efac", "#14532d"
}

func blocked(x, y int) bool {
	if x <= 0 || y <= 0 || x >= worldWidth-1 || y >= worldHeight-1 {
		return true
	}
	if isWater(x, y) && !isBridge(x, y) {
		return true
	}
	for _, h := range houses {
		if inHouse(x, y, h) {
			return true
		}
	}
	return false
}

func describeTile(x, y int) string {
	switch {
	case nearHouse(x, y):
		return "A warm window glows nearby. Nobody answers the door yet."
	case isBridge(x, y):
		return "The bridge creaks over bright water."
	case isWater(x+1, y) || isWater(x-1, y) || isWater(x, y+1) || isWater(x, y-1):
		return "You hear water moving nearby."
	case isTree(x, y):
		return "You rustle under the trees."
	case onPath(x, y):
		return "Your boots crunch on the village road."
	default:
		return "You cross soft meadow grass."
	}
}

func houseTile(x, y int, h house) (string, string, string) {
	if y == h.y {
		return " ", "#fed7aa", "#9a3412"
	}
	if y == h.y+h.h-1 && x == h.x+h.w/2 {
		return " ", "#422006", "#facc15"
	}
	if x == h.x || x == h.x+h.w-1 || y == h.y+h.h-1 {
		return " ", "#fef3c7", "#78350f"
	}
	if y == h.y+2 && (x == h.x+2 || x == h.x+h.w-3) {
		return " ", "#082f49", "#7dd3fc"
	}
	return " ", "#fde68a", "#92400e"
}

func inHouse(x, y int, h house) bool {
	return x >= h.x && x < h.x+h.w && y >= h.y && y < h.y+h.h
}

func nearHouse(x, y int) bool {
	for _, h := range houses {
		if x >= h.x-1 && x <= h.x+h.w && y >= h.y-1 && y <= h.y+h.h {
			return true
		}
	}
	return false
}

func onPath(x, y int) bool {
	if isBridge(x, y) {
		return true
	}
	return y == 38 || x == 88 || (x >= 8 && x <= 94 && y == 13) || (x >= 18 && x <= 148 && y == 25) || (x >= 26 && x <= 168 && y == 58) || (x >= 42 && x <= 128 && y == 76) || x == 35 || x == 126 || x == 154
}

func isTree(x, y int) bool {
	if x > 8 && x < 28 && y > 36 && y < 52 && (x+y)%3 == 0 {
		return true
	}
	if x > 144 && x < 174 && y > 6 && y < 24 && (x*2+y)%4 == 0 {
		return true
	}
	if x > 12 && x < 46 && y > 72 && y < 88 && (x+y*2)%4 == 0 {
		return true
	}
	return (x*11+y*7)%37 == 0
}

func isWater(x, y int) bool {
	if x >= 150 && y >= 58 {
		return true
	}
	if x >= 118 && x <= 123 && y >= 28 && y <= 90 {
		return true
	}
	if x >= 120 && x <= 154 && y >= 40 && y <= 45 {
		return true
	}
	return false
}

func isBridge(x, y int) bool {
	return (x >= 116 && x <= 125 && y == 58) || (x >= 118 && x <= 123 && y == 76) || (x == 154 && y >= 56 && y <= 62)
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func clamp(v, minValue, maxValue int) int {
	if v < minValue {
		return minValue
	}
	if v > maxValue {
		return maxValue
	}
	return v
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
