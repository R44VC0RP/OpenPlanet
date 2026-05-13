package main

import (
	"encoding/json"
	"errors"
	"fmt"
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
	id            string
	players       map[string]*actor
	monsters      map[string]*monster
	loot          map[string]*lootItem
	searchedHouse map[int]bool
	nextMonsterID int
	nextLootID    int
	sessions      map[*session]struct{}
	mu            sync.Mutex
}

type actor struct {
	id      string
	name    string
	x       int
	y       int
	dirX    int
	dirY    int
	color   string
	hp      int
	maxHP   int
	gold    int
	potions int
	power   int
	score   int64
}

type monster struct {
	id     string
	name   string
	glyph  string
	x      int
	y      int
	hp     int
	maxHP  int
	damage int
	reward int64
	color  string
}

type lootItem struct {
	id    string
	kind  string
	name  string
	x     int
	y     int
	value int
	color string
	glyph string
}

type session struct {
	conn      *websocket.Conn
	room      *room
	playerID  string
	cols      int
	rows      int
	seq       int
	message   string
	lastScore int64
	scoreSent bool
	mu        sync.Mutex
}

type house struct {
	x int
	y int
	w int
	h int
}

type roomView struct {
	players  []actor
	monsters []monster
	loot     []lootItem
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
		Title:        "Meadow Quest",
		TargetFPS:    8,
		Capabilities: []string{ggp.CapRenderCell, ggp.CapInputKeyboard, ggp.CapScoreReport},
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
		r = newRoom(roomID)
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
		id:      hello.Player.ID,
		name:    hello.Player.Name,
		x:       spawn[0],
		y:       spawn[1],
		dirX:    0,
		dirY:    1,
		color:   nameColors[len(r.players)%len(nameColors)],
		hp:      10,
		maxHP:   10,
		potions: 1,
		power:   1,
	}
	r.players[player.id] = &player
	s := &session{
		conn:     conn,
		room:     r,
		playerID: player.id,
		cols:     max(hello.Viewport.Cols, 40),
		rows:     max(hello.Viewport.Rows, 14),
		message:  "Welcome to Meadow Quest. Move with WASD/arrows, attack with f/space, search with e, drink potions with p.",
	}
	r.sessions[s] = struct{}{}
	return s, nil
}

func newRoom(id string) *room {
	r := &room{
		id:            id,
		players:       make(map[string]*actor),
		monsters:      make(map[string]*monster),
		loot:          make(map[string]*lootItem),
		searchedHouse: make(map[int]bool),
		sessions:      make(map[*session]struct{}),
	}
	for _, spec := range []monster{
		{name: "Meadow Slime", glyph: "s", x: 71, y: 33, hp: 3, maxHP: 3, damage: 1, reward: 15, color: "#4ade80"},
		{name: "River Imp", glyph: "i", x: 112, y: 51, hp: 4, maxHP: 4, damage: 1, reward: 25, color: "#38bdf8"},
		{name: "Old Skeleton", glyph: "k", x: 46, y: 72, hp: 5, maxHP: 5, damage: 2, reward: 40, color: "#e5e7eb"},
		{name: "Wolf", glyph: "w", x: 31, y: 46, hp: 4, maxHP: 4, damage: 2, reward: 30, color: "#94a3b8"},
		{name: "Bog Beast", glyph: "b", x: 132, y: 48, hp: 8, maxHP: 8, damage: 3, reward: 80, color: "#a3e635"},
		{name: "Bandit", glyph: "B", x: 142, y: 26, hp: 6, maxHP: 6, damage: 2, reward: 65, color: "#fb923c"},
		{name: "Cave Bat", glyph: "v", x: 78, y: 77, hp: 3, maxHP: 3, damage: 1, reward: 20, color: "#c084fc"},
	} {
		r.addMonsterLocked(spec)
	}
	for _, item := range []lootItem{
		{kind: "gold", name: "copper coins", x: 84, y: 35, value: 8, color: "#facc15", glyph: "$"},
		{kind: "gold", name: "silver coins", x: 128, y: 57, value: 18, color: "#fde047", glyph: "$"},
		{kind: "potion", name: "red potion", x: 33, y: 59, value: 5, color: "#f87171", glyph: "!"},
		{kind: "weapon", name: "iron sword", x: 83, y: 18, value: 40, color: "#e5e7eb", glyph: "/"},
		{kind: "gem", name: "river sapphire", x: 116, y: 76, value: 75, color: "#38bdf8", glyph: "*"},
	} {
		r.addLootLocked(item)
	}
	return r
}

func (r *room) addMonsterLocked(spec monster) {
	r.nextMonsterID++
	spec.id = fmt.Sprintf("m%d", r.nextMonsterID)
	if spec.maxHP == 0 {
		spec.maxHP = spec.hp
	}
	r.monsters[spec.id] = &spec
}

func (r *room) addLootLocked(item lootItem) {
	r.nextLootID++
	item.id = fmt.Sprintf("l%d", r.nextLootID)
	if item.color == "" {
		item.color = "#facc15"
	}
	if item.glyph == "" {
		item.glyph = "$"
	}
	r.loot[item.id] = &item
}

func (r *room) leave(s *session) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.sessions, s)
	delete(r.players, s.playerID)
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
		if message := s.room.handleInput(s.playerID, input.Key); message != "" {
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
		_, view := s.room.snapshot()
		s.sendFrame(view)
		s.sendScore(view)
	case ggp.TypeLeave:
		return false
	}
	return true
}

func (r *room) handleInput(playerID, key string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	player := r.players[playerID]
	if player == nil {
		return "Player is no longer in this room."
	}

	var message string
	switch key {
	case "left", "h", "a":
		message = r.moveLocked(player, -1, 0)
	case "right", "l", "d":
		message = r.moveLocked(player, 1, 0)
	case "up", "k", "w":
		message = r.moveLocked(player, 0, -1)
	case "down", "j", "s":
		message = r.moveLocked(player, 0, 1)
	case "f", "space":
		message = r.attackLocked(player)
	case "e", "enter":
		message = r.interactLocked(player)
	case "p":
		message = drinkPotion(player)
	default:
		return "Controls: move WASD/arrows, attack f/space, search e, potion p."
	}
	if message == "" {
		message = describeTile(player.x, player.y)
	}
	monsterMessage := r.monsterTurnLocked(player)
	if monsterMessage != "" {
		message += " " + monsterMessage
	}
	return message
}

func (r *room) moveLocked(player *actor, dx, dy int) string {
	player.dirX, player.dirY = dx, dy
	nx, ny := player.x+dx, player.y+dy
	if target := r.monsterAtLocked(nx, ny); target != nil {
		return r.attackMonsterLocked(player, target)
	}
	if blocked(nx, ny) || r.occupiedLocked(player.id, nx, ny) {
		return "That way is blocked. Look for a door, bridge, or open path."
	}
	player.x, player.y = nx, ny
	if item := r.lootAtLocked(nx, ny); item != nil {
		return r.pickupLocked(player, item)
	}
	return describeTile(nx, ny)
}

func (r *room) attackLocked(player *actor) string {
	tx, ty := player.x+player.dirX, player.y+player.dirY
	if target := r.monsterAtLocked(tx, ty); target != nil {
		return r.attackMonsterLocked(player, target)
	}
	for _, target := range r.monsters {
		if adjacent(player.x, player.y, target.x, target.y) {
			return r.attackMonsterLocked(player, target)
		}
	}
	return "You swing at the air. No monster is close enough."
}

func (r *room) attackMonsterLocked(player *actor, target *monster) string {
	damage := 2 + player.power
	target.hp -= damage
	if target.hp > 0 {
		player.score += int64(damage)
		return fmt.Sprintf("You hit the %s for %d damage. It has %d HP left.", target.name, damage, target.hp)
	}
	delete(r.monsters, target.id)
	player.score += target.reward
	player.gold += int(target.reward / 10)
	r.dropMonsterLootLocked(*target)
	return fmt.Sprintf("You defeat the %s! +%d score and +%d gold.", target.name, target.reward, target.reward/10)
}

func (r *room) interactLocked(player *actor) string {
	if item := r.lootAtLocked(player.x, player.y); item != nil {
		return r.pickupLocked(player, item)
	}
	if index, ok := houseIndexAt(player.x, player.y); ok {
		if !r.searchedHouse[index] {
			r.searchedHouse[index] = true
			bonus := 20 + index*3
			player.score += int64(bonus)
			player.gold += 3
			if player.potions < 5 && index%2 == 0 {
				player.potions++
				return fmt.Sprintf("You search the house and find a potion, 3 gold, and %d score.", bonus)
			}
			return fmt.Sprintf("You search the house and find 3 gold and %d score.", bonus)
		}
		return "This house has already been searched. The hearth is still warm."
	}
	for _, h := range houses {
		if adjacent(player.x, player.y, doorX(h), doorY(h)) {
			return "The door is open. Step onto the golden doorway to enter and search inside."
		}
	}
	return "You search the meadow but find only wildflowers."
}

func drinkPotion(player *actor) string {
	if player.potions <= 0 {
		return "You do not have any potions."
	}
	if player.hp >= player.maxHP {
		return "You are already at full health."
	}
	player.potions--
	heal := min(5, player.maxHP-player.hp)
	player.hp += heal
	return fmt.Sprintf("You drink a potion and recover %d HP.", heal)
}

func (r *room) pickupLocked(player *actor, item *lootItem) string {
	delete(r.loot, item.id)
	switch item.kind {
	case "potion":
		player.potions++
		player.score += int64(item.value)
		return fmt.Sprintf("You pick up a %s. +%d score.", item.name, item.value)
	case "weapon":
		player.power++
		player.score += int64(item.value)
		return fmt.Sprintf("You equip the %s. Attack power increased! +%d score.", item.name, item.value)
	case "gem":
		player.gold += item.value
		player.score += int64(item.value * 2)
		return fmt.Sprintf("You claim the %s. +%d gold and +%d score.", item.name, item.value, item.value*2)
	default:
		player.gold += item.value
		player.score += int64(item.value)
		return fmt.Sprintf("You collect %s worth %d gold. +%d score.", item.name, item.value, item.value)
	}
}

func (r *room) dropMonsterLootLocked(target monster) {
	if target.reward >= 60 {
		r.addLootLocked(lootItem{kind: "potion", name: "monster tonic", x: target.x, y: target.y, value: 12, color: "#f87171", glyph: "!"})
		return
	}
	r.addLootLocked(lootItem{kind: "gold", name: "monster coins", x: target.x, y: target.y, value: max(3, int(target.reward/5)), color: "#facc15", glyph: "$"})
}

func (r *room) monsterTurnLocked(player *actor) string {
	var messages []string
	for _, target := range r.monsters {
		if adjacent(player.x, player.y, target.x, target.y) {
			player.hp -= target.damage
			messages = append(messages, fmt.Sprintf("The %s hits you for %d.", target.name, target.damage))
			if player.hp <= 0 {
				messages = append(messages, r.respawnPlayerLocked(player))
				break
			}
		}
	}
	return strings.Join(messages, " ")
}

func (r *room) respawnPlayerLocked(player *actor) string {
	spawn := spawnPoints[0]
	player.x, player.y = spawn[0], spawn[1]
	player.hp = player.maxHP
	player.potions = max(player.potions, 1)
	player.score = max64(player.score-25, 0)
	return "You collapse and wake up at the village square. -25 score."
}

func (r *room) monsterAtLocked(x, y int) *monster {
	for _, target := range r.monsters {
		if target.x == x && target.y == y {
			return target
		}
	}
	return nil
}

func (r *room) lootAtLocked(x, y int) *lootItem {
	for _, item := range r.loot {
		if item.x == x && item.y == y {
			return item
		}
	}
	return nil
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
	sessions, view := r.snapshot()
	for _, s := range sessions {
		s.sendFrame(view)
		s.sendScore(view)
	}
	if len(sessions) > 0 {
		presence := r.presence(view.players)
		for _, s := range sessions {
			_ = s.writeJSON(presence)
		}
	}
}

func (r *room) snapshot() ([]*session, roomView) {
	r.mu.Lock()
	defer r.mu.Unlock()
	sessions := make([]*session, 0, len(r.sessions))
	for s := range r.sessions {
		sessions = append(sessions, s)
	}
	view := roomView{
		players:  make([]actor, 0, len(r.players)),
		monsters: make([]monster, 0, len(r.monsters)),
		loot:     make([]lootItem, 0, len(r.loot)),
	}
	for _, player := range r.players {
		view.players = append(view.players, *player)
	}
	for _, target := range r.monsters {
		view.monsters = append(view.monsters, *target)
	}
	for _, item := range r.loot {
		view.loot = append(view.loot, *item)
	}
	return sessions, view
}

func (r *room) presence(players []actor) ggp.Presence {
	presence := ggp.Presence{Type: ggp.TypePresence, RoomID: r.id, MaxPlayers: maxPlayers, Players: make([]ggp.PresencePlayer, 0, len(players))}
	for _, player := range players {
		state := fmt.Sprintf("HP %d/%d · score %d", player.hp, player.maxHP, player.score)
		presence.Players = append(presence.Players, ggp.PresencePlayer{ID: player.id, Name: player.name, State: state})
	}
	return presence
}

func (s *session) sendFrame(view roomView) {
	frame := ggp.Frame{
		Type:   ggp.TypeFrame,
		Mode:   ggp.FrameFull,
		Status: s.message,
		Cells:  s.renderCells(view),
	}
	s.mu.Lock()
	s.seq++
	frame.Seq = s.seq
	s.mu.Unlock()
	_ = s.writeJSON(frame)
}

func (s *session) sendScore(view roomView) {
	self, ok := s.self(view.players)
	if !ok {
		return
	}
	s.mu.Lock()
	if s.scoreSent && s.lastScore == self.score {
		s.mu.Unlock()
		return
	}
	s.scoreSent = true
	s.lastScore = self.score
	s.mu.Unlock()
	_ = s.writeJSON(ggp.Score{Type: ggp.TypeScore, Value: self.score})
}

func (s *session) writeJSON(value any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conn.WriteJSON(value)
}

func (s *session) renderCells(view roomView) []ggp.Cell {
	cols := max(s.cols, 40)
	rows := max(s.rows, 14)
	cells := make([]ggp.Cell, 0, cols*rows)
	self, ok := s.self(view.players)
	if !ok {
		self = actor{x: worldWidth / 2, y: worldHeight / 2, hp: 1, maxHP: 1}
	}

	camX := clamp(self.x-cols/2, 0, max(worldWidth-cols, 0))
	camY := clamp(self.y-rows/2, 0, max(worldHeight-rows, 0))

	for y := 0; y < rows; y++ {
		for x := 0; x < cols; x++ {
			wx, wy := camX+x, camY+y
			ch, fg, bg, attrs := renderWorldCell(wx, wy, view, s.playerID)
			cells = append(cells, ggp.Cell{X: x, Y: y, Ch: ch, Fg: fg, Bg: bg, Attrs: attrs})
		}
	}
	s.drawHUD(cells, cols, rows, self, view)
	return cells
}

func (s *session) self(players []actor) (actor, bool) {
	for _, player := range players {
		if player.id == s.playerID {
			return player, true
		}
	}
	return actor{}, false
}

func renderWorldCell(x, y int, view roomView, selfID string) (string, string, string, []string) {
	ch, fg, bg := tile(x, y)
	attrs := []string(nil)
	if item, ok := lootAt(view.loot, x, y); ok {
		ch, fg = item.glyph, item.color
		attrs = []string{"bold"}
	}
	if target, ok := monsterAt(view.monsters, x, y); ok {
		ch, fg = target.glyph, target.color
		attrs = []string{"bold"}
	}
	if player, ok := playerAt(view.players, x, y); ok {
		ch, fg = playerGlyph(player, player.id == selfID), player.color
		attrs = []string{"bold"}
	}
	return ch, fg, bg, attrs
}

func (s *session) drawHUD(cells []ggp.Cell, cols, rows int, self actor, view roomView) {
	status := fmt.Sprintf("Meadow Quest  HP %d/%d  Gold %d  Potions %d  Power %d  Score %d  Monsters %d  Loot %d", self.hp, self.maxHP, self.gold, self.potions, self.power, self.score, len(view.monsters), len(view.loot))
	writeCells(cells, cols, rows, 0, 0, status, "#020617", "#facc15", []string{"bold"})
	controls := "Move WASD/arrows · attack f/space · search/open e · potion p · tab chat/leaderboard"
	writeCells(cells, cols, rows, 0, rows-1, controls, "#cbd5e1", "#0f172a", nil)
	if s.message != "" && rows > 2 {
		writeCells(cells, cols, rows, 0, rows-2, s.message, "#e0f2fe", "#0f172a", nil)
	}
}

func writeCells(cells []ggp.Cell, cols, rows, x, y int, text, fg, bg string, attrs []string) {
	if y < 0 || y >= rows {
		return
	}
	for offset, r := range []rune(text) {
		cx := x + offset
		if cx < 0 || cx >= cols {
			continue
		}
		cells[y*cols+cx] = ggp.Cell{X: cx, Y: y, Ch: string(r), Fg: fg, Bg: bg, Attrs: attrs}
	}
}

func playerAt(players []actor, x, y int) (actor, bool) {
	for _, player := range players {
		if player.x == x && player.y == y {
			return player, true
		}
	}
	return actor{}, false
}

func monsterAt(monsters []monster, x, y int) (monster, bool) {
	for _, target := range monsters {
		if target.x == x && target.y == y {
			return target, true
		}
	}
	return monster{}, false
}

func lootAt(loot []lootItem, x, y int) (lootItem, bool) {
	for _, item := range loot {
		if item.x == x && item.y == y {
			return item, true
		}
	}
	return lootItem{}, false
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
	if isWater(x, y) && !isBridge(x, y) {
		return "≈", "#bae6fd", "#075985"
	}
	for _, h := range houses {
		if inHouse(x, y, h) {
			return houseTile(x, y, h)
		}
	}
	if isBridge(x, y) {
		return "=", "#fef3c7", "#a16207"
	}
	if onPath(x, y) {
		return "·", "#fef3c7", "#a16207"
	}
	if isTree(x, y) {
		return "♣", "#bbf7d0", "#166534"
	}
	if (x*3+y*5)%19 == 0 {
		return "'", "#d9f99d", "#3f6212"
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
		if houseWall(x, y, h) {
			return true
		}
	}
	return false
}

func describeTile(x, y int) string {
	switch {
	case insideHouse(x, y):
		return "You step inside a cozy village house. Press e to search shelves and chests."
	case nearHouse(x, y):
		return "A warm window glows nearby. Doors can be entered now."
	case isBridge(x, y):
		return "The bridge creaks over bright water."
	case isWater(x+1, y) || isWater(x-1, y) || isWater(x, y+1) || isWater(x, y-1):
		return "You hear water moving nearby."
	case isTree(x, y):
		return "You rustle under the trees. Monsters may lurk in the shade."
	case onPath(x, y):
		return "Your boots crunch on the village road."
	default:
		return "You cross soft meadow grass."
	}
}

func houseTile(x, y int, h house) (string, string, string) {
	if doorX(h) == x && doorY(h) == y {
		return "▣", "#422006", "#facc15"
	}
	if y == h.y {
		return "▄", "#fed7aa", "#9a3412"
	}
	if x == h.x || x == h.x+h.w-1 || y == h.y+h.h-1 {
		return "█", "#fef3c7", "#78350f"
	}
	if y == h.y+2 && (x == h.x+2 || x == h.x+h.w-3) {
		return "□", "#082f49", "#7dd3fc"
	}
	if x == h.x+h.w/2 && y == h.y+h.h/2 {
		return "⌂", "#fed7aa", "#451a03"
	}
	return "·", "#fde68a", "#92400e"
}

func inHouse(x, y int, h house) bool {
	return x >= h.x && x < h.x+h.w && y >= h.y && y < h.y+h.h
}

func houseWall(x, y int, h house) bool {
	if !inHouse(x, y, h) {
		return false
	}
	if x == doorX(h) && y == doorY(h) {
		return false
	}
	return x == h.x || x == h.x+h.w-1 || y == h.y || y == h.y+h.h-1
}

func houseIndexAt(x, y int) (int, bool) {
	for i, h := range houses {
		if inHouse(x, y, h) && !houseWall(x, y, h) {
			return i, true
		}
	}
	return 0, false
}

func insideHouse(x, y int) bool {
	_, ok := houseIndexAt(x, y)
	return ok
}

func nearHouse(x, y int) bool {
	for _, h := range houses {
		if x >= h.x-1 && x <= h.x+h.w && y >= h.y-1 && y <= h.y+h.h {
			return true
		}
	}
	return false
}

func doorX(h house) int { return h.x + h.w/2 }
func doorY(h house) int { return h.y + h.h - 1 }

func onPath(x, y int) bool {
	if isBridge(x, y) {
		return true
	}
	return y == 38 || x == 88 || (x >= 8 && x <= 94 && y == 13) || (x >= 18 && x <= 148 && y == 25) || (x >= 26 && x <= 168 && y == 58) || (x >= 42 && x <= 128 && y == 76) || x == 35 || x == 126 || x == 154
}

func isTree(x, y int) bool {
	if insideHouse(x, y) {
		return false
	}
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

func adjacent(ax, ay, bx, by int) bool {
	dx := ax - bx
	if dx < 0 {
		dx = -dx
	}
	dy := ay - by
	if dy < 0 {
		dy = -dy
	}
	return dx+dy == 1
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

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
