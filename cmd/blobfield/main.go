package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"log"
	"math"
	"math/rand"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"gamegateway/internal/ggp"
)

const (
	gameID       = "blobfield"
	worldWidth   = 260
	worldHeight  = 120
	maxPlayers   = 16
	pelletCount  = 320
	targetFPS    = 10
	helloTimeout = 5 * time.Second
	startMass    = 18.0
	minViewCols  = 50
	minViewRows  = 18
	maxViewCols  = 120
	maxViewRows  = 40
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
	server  *gameServer
	id      string
	rng     *rand.Rand
	stop    chan struct{}
	stopped sync.Once

	mu            sync.Mutex
	players       map[string]*blob
	sessions      map[*session]struct{}
	pellets       []pellet
	presenceSeq   int
	presenceAt    time.Time
	presenceDirty bool
}

type blob struct {
	id             string
	name           string
	x              float64
	y              float64
	dx             float64
	dy             float64
	mass           float64
	color          string
	alive          bool
	protectedUntil time.Time
	respawnAt      time.Time
}

type pellet struct {
	x     float64
	y     float64
	color string
}

type session struct {
	conn       *websocket.Conn
	room       *room
	playerID   string
	cols       int
	rows       int
	seq        int
	status     string
	lastCells  []ggp.Cell
	lastStatus string
	bestScore  int64
	scoreAt    time.Time
	mu         sync.Mutex
}

type roomState struct {
	players []blob
	pellets []pellet
}

var (
	upgrader     = websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	colors       = []string{"#7dd3fc", "#f472b6", "#facc15", "#4ade80", "#fb923c", "#c084fc", "#2dd4bf", "#f87171", "#a3e635", "#f0abfc"}
	pelletColors = []string{"#bae6fd", "#bbf7d0", "#fde68a", "#fecdd3", "#ddd6fe"}
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

	addr := ":" + env("PORT", "8082")
	mode := "single-player"
	if server.secret != "" {
		mode = "secure multiplayer"
	}
	log.Printf("blobfield game listening on %s (%s)", addr, mode)
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
		if s.room.leave(s) {
			g.dropRoom(s.room)
			return
		}
		s.room.broadcast()
	}()

	ready := ggp.Ready{
		Type:         ggp.TypeReady,
		Title:        "Blobfield",
		TargetFPS:    targetFPS,
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
	r := g.getRoom(roomID)

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.players[hello.Player.ID]; exists {
		return nil, errors.New("player already has an active blob in this room")
	}
	if len(r.players) >= maxPlayers {
		return nil, errors.New("room is full")
	}

	x, y := r.spawnPointLocked(startMass)
	player := &blob{
		id:             hello.Player.ID,
		name:           hello.Player.Name,
		x:              x,
		y:              y,
		dx:             1,
		dy:             0,
		mass:           startMass,
		color:          colors[len(r.players)%len(colors)],
		alive:          true,
		protectedUntil: time.Now().Add(3 * time.Second),
	}
	r.players[player.id] = player
	cols, rows := viewportSize(hello.Viewport.Cols, hello.Viewport.Rows)
	s := &session{
		conn:     conn,
		room:     r,
		playerID: player.id,
		cols:     cols,
		rows:     rows,
		status:   "Eat pellets, grow big, and swallow smaller blobs. Move with arrows or WASD; space stops drift.",
	}
	r.sessions[s] = struct{}{}
	r.presenceDirty = true
	return s, nil
}

func (g *gameServer) getRoom(roomID string) *room {
	g.mu.Lock()
	defer g.mu.Unlock()
	if r := g.rooms[roomID]; r != nil {
		return r
	}
	r := newRoom(g, roomID)
	g.rooms[roomID] = r
	go r.loop()
	return r
}

func newRoom(g *gameServer, roomID string) *room {
	r := &room{
		server:   g,
		id:       roomID,
		rng:      rand.New(rand.NewSource(time.Now().UnixNano() + int64(hashString(roomID)))),
		stop:     make(chan struct{}),
		players:  make(map[string]*blob),
		sessions: make(map[*session]struct{}),
	}
	r.pellets = make([]pellet, pelletCount)
	for i := range r.pellets {
		r.pellets[i] = r.randomPelletLocked()
	}
	return r
}

func (g *gameServer) dropRoom(r *room) {
	g.mu.Lock()
	defer g.mu.Unlock()
	r.mu.Lock()
	empty := len(r.sessions) == 0
	r.mu.Unlock()
	if !empty || g.rooms[r.id] != r {
		return
	}
	delete(g.rooms, r.id)
	r.stopped.Do(func() { close(r.stop) })
}

func (r *room) leave(s *session) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.sessions, s)
	delete(r.players, s.playerID)
	r.presenceDirty = true
	return len(r.sessions) == 0
}

func (r *room) loop() {
	ticker := time.NewTicker(time.Second / targetFPS)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			r.step()
			r.broadcast()
		case <-r.stop:
			return
		}
	}
}

func (r *room) step() {
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, player := range r.players {
		if !player.alive {
			if !player.respawnAt.IsZero() && now.After(player.respawnAt) {
				r.respawnLocked(player, now)
			}
			continue
		}
		speed := speedForMass(player.mass)
		player.x += player.dx * speed
		player.y += player.dy * speed
		radius := radiusForMass(player.mass)
		player.x = clampFloat(player.x, radius+1, worldWidth-radius-2)
		player.y = clampFloat(player.y, radius+1, worldHeight-radius-2)
		if player.mass > startMass {
			player.mass = maxFloat(startMass, player.mass*0.9994)
		}
	}

	for _, player := range r.players {
		if !player.alive {
			continue
		}
		pr := radiusForMass(player.mass)
		for i := range r.pellets {
			p := r.pellets[i]
			if distance(player.x, player.y, p.x, p.y) <= pr+0.75 {
				player.mass += 1.25
				r.pellets[i] = r.randomPelletLocked()
			}
		}
	}

	players := make([]*blob, 0, len(r.players))
	for _, player := range r.players {
		if player.alive {
			players = append(players, player)
		}
	}
	sort.Slice(players, func(i, j int) bool { return players[i].mass > players[j].mass })
	for _, hunter := range players {
		if !hunter.alive || now.Before(hunter.protectedUntil) {
			continue
		}
		for _, prey := range players {
			if hunter == prey || !prey.alive || now.Before(prey.protectedUntil) {
				continue
			}
			if hunter.mass < prey.mass*1.22 {
				continue
			}
			if distance(hunter.x, hunter.y, prey.x, prey.y) <= radiusForMass(hunter.mass)*0.70 {
				hunter.mass += prey.mass * 0.72
				prey.alive = false
				prey.respawnAt = now.Add(2500 * time.Millisecond)
				prey.mass = startMass
				prey.dx, prey.dy = 0, 0
				r.presenceDirty = true
			}
		}
	}
}

func (r *room) respawnLocked(player *blob, now time.Time) {
	x, y := r.spawnPointLocked(startMass)
	player.x, player.y = x, y
	player.dx, player.dy = 1, 0
	player.mass = startMass
	player.alive = true
	player.respawnAt = time.Time{}
	player.protectedUntil = now.Add(3 * time.Second)
	r.presenceDirty = true
}

func (r *room) setDirection(playerID, key string) string {
	dx, dy, ok := directionForKey(key)
	if !ok {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	player := r.players[playerID]
	if player == nil {
		return "Your blob has left the field."
	}
	if !player.alive {
		return "Respawning..."
	}
	player.dx, player.dy = dx, dy
	if dx == 0 && dy == 0 {
		return "Drift stopped. Pick a direction when you are ready."
	}
	return fmt.Sprintf("Drifting %s. Eat pellets and smaller blobs.", directionName(dx, dy))
}

func (s *session) setStatus(status string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = status
}

func (s *session) resize(cols, rows int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cols, s.rows = viewportSize(cols, rows)
	s.lastCells = nil
}

func directionForKey(key string) (float64, float64, bool) {
	switch key {
	case "left", "h", "a":
		return -1, 0, true
	case "right", "l", "d":
		return 1, 0, true
	case "up", "k", "w":
		return 0, -1, true
	case "down", "j", "s":
		return 0, 1, true
	case "space", " ":
		return 0, 0, true
	}
	return 0, 0, false
}

func directionName(dx, dy float64) string {
	switch {
	case dx < 0:
		return "left"
	case dx > 0:
		return "right"
	case dy < 0:
		return "up"
	case dy > 0:
		return "down"
	default:
		return "nowhere"
	}
}

func (r *room) snapshot() ([]*session, roomState, *ggp.Presence) {
	r.mu.Lock()
	defer r.mu.Unlock()
	sessions := make([]*session, 0, len(r.sessions))
	for s := range r.sessions {
		sessions = append(sessions, s)
	}
	state := r.stateLocked()
	now := time.Now()
	if !r.presenceDirty && now.Sub(r.presenceAt) < time.Second {
		return sessions, state, nil
	}
	r.presenceSeq++
	r.presenceAt = now
	r.presenceDirty = false
	presence := &ggp.Presence{Type: ggp.TypePresence, Seq: r.presenceSeq, RoomID: r.id, MaxPlayers: maxPlayers, Players: make([]ggp.PresencePlayer, 0, len(state.players))}
	for _, player := range state.players {
		stateName := "playing"
		if !player.alive {
			stateName = "joining"
		}
		presence.Players = append(presence.Players, ggp.PresencePlayer{ID: player.id, Name: player.name, State: stateName})
	}
	return sessions, state, presence
}

func (r *room) currentState() roomState {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stateLocked()
}

func (r *room) stateLocked() roomState {
	state := roomState{
		players: make([]blob, 0, len(r.players)),
		pellets: make([]pellet, len(r.pellets)),
	}
	for _, player := range r.players {
		state.players = append(state.players, *player)
	}
	copy(state.pellets, r.pellets)
	sort.Slice(state.players, func(i, j int) bool { return state.players[i].mass > state.players[j].mass })
	return state
}

func (r *room) broadcast() {
	sessions, state, presence := r.snapshot()
	for _, s := range sessions {
		s.sendFrame(state)
	}
	if presence != nil {
		for _, s := range sessions {
			_ = s.writeJSON(presence)
		}
	}
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
		if status := s.room.setDirection(s.playerID, input.Key); status != "" {
			s.setStatus(status)
		}
		s.sendFrame(s.room.currentState())
	case ggp.TypeResize:
		var resize ggp.Resize
		if err := json.Unmarshal(payload, &resize); err != nil {
			return true
		}
		s.resize(resize.Cols, resize.Rows)
		s.sendFrame(s.room.currentState())
	case ggp.TypeLeave:
		return false
	}
	return true
}

func (s *session) sendFrame(state roomState) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cells := s.renderCells(state)
	status := s.statusLine(state)
	self, ok := state.player(s.playerID)
	score := scoreForPlayer(self, ok)
	reportScore := s.shouldReportScoreLocked(score, time.Now())
	mode := ggp.FramePatch
	frameCells := changedCells(s.lastCells, cells)
	if len(s.lastCells) != len(cells) {
		mode = ggp.FrameFull
		frameCells = cells
	}
	sendFrame := len(frameCells) > 0 || status != s.lastStatus
	if !sendFrame && !reportScore {
		return
	}
	if sendFrame {
		s.lastCells = cells
		s.lastStatus = status
		s.seq++
		_ = s.conn.WriteJSON(ggp.Frame{
			Type:   ggp.TypeFrame,
			Seq:    s.seq,
			Mode:   mode,
			Status: status,
			Cells:  frameCells,
		})
	}
	if reportScore {
		_ = s.conn.WriteJSON(ggp.Score{Type: ggp.TypeScore, Value: score})
	}
}

func (s *session) writeJSON(value any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conn.WriteJSON(value)
}

func (s *session) statusLine(state roomState) string {
	self, ok := state.player(s.playerID)
	if !ok {
		return s.status
	}
	if !self.alive {
		return "You were swallowed. Respawning soon..."
	}
	rank := 1
	for _, player := range state.players {
		if player.mass > self.mass {
			rank++
		}
	}
	leader := self.name
	if len(state.players) > 0 {
		leader = state.players[0].name
	}
	protected := ""
	if time.Now().Before(self.protectedUntil) {
		protected = "  shielded"
	}
	return fmt.Sprintf("Mass %.0f  Rank %d/%d  Leader %s%s  |  %s", self.mass, rank, len(state.players), defaultName(leader), protected, s.status)
}

func (s *session) renderCells(state roomState) []ggp.Cell {
	cols, rows := viewportSize(s.cols, s.rows)
	cells := make([]ggp.Cell, cols*rows)
	for y := 0; y < rows; y++ {
		for x := 0; x < cols; x++ {
			bg := "#020617"
			if x == 0 || y == 0 || x == cols-1 || y == rows-1 {
				bg = "#111827"
			} else if (x+y)%17 == 0 {
				bg = "#07111f"
			}
			cells[y*cols+x] = ggp.Cell{X: x, Y: y, Ch: " ", Fg: "#64748b", Bg: bg}
		}
	}

	self, ok := state.player(s.playerID)
	if !ok {
		return cells
	}
	cameraX := clampFloat(self.x-float64(cols)/2, 0, maxFloat(worldWidth-float64(cols), 0))
	cameraY := clampFloat(self.y-float64(rows)/2, 0, maxFloat(worldHeight-float64(rows), 0))

	for _, p := range state.pellets {
		sx, sy := worldToScreen(p.x, p.y, cameraX, cameraY)
		if sx <= 0 || sy <= 0 || sx >= cols-1 || sy >= rows-1 {
			continue
		}
		idx := sy*cols + sx
		cells[idx].Ch = "·"
		cells[idx].Fg = p.color
		cells[idx].Attrs = nil
	}

	blobs := append([]blob(nil), state.players...)
	sort.Slice(blobs, func(i, j int) bool { return blobs[i].mass < blobs[j].mass })
	for _, player := range blobs {
		if !player.alive {
			continue
		}
		drawBlob(cells, cols, rows, cameraX, cameraY, player, player.id == s.playerID)
	}
	drawLeaderboard(cells, cols, rows, state.players)
	return cells
}

func drawBlob(cells []ggp.Cell, cols, rows int, cameraX, cameraY float64, player blob, self bool) {
	radius := radiusForMass(player.mass)
	if radius < 1.1 {
		radius = 1.1
	}
	cx, cy := worldToScreen(player.x, player.y, cameraX, cameraY)
	limit := int(math.Ceil(radius))
	for dy := -limit; dy <= limit; dy++ {
		for dx := -limit; dx <= limit; dx++ {
			sx, sy := cx+dx, cy+dy
			if sx <= 0 || sy <= 0 || sx >= cols-1 || sy >= rows-1 {
				continue
			}
			if math.Sqrt(float64(dx*dx)+float64(dy*dy)*1.7) > radius {
				continue
			}
			idx := sy*cols + sx
			cells[idx].Ch = " "
			cells[idx].Bg = player.color
			cells[idx].Fg = "#020617"
			if self {
				cells[idx].Attrs = []string{"bold"}
			}
		}
	}
	if cx > 0 && cy > 0 && cx < cols-1 && cy < rows-1 {
		idx := cy*cols + cx
		cells[idx].Ch = playerGlyph(player, self)
		cells[idx].Fg = "#020617"
		cells[idx].Bg = player.color
		cells[idx].Attrs = []string{"bold"}
	}
}

func drawLeaderboard(cells []ggp.Cell, cols, rows int, players []blob) {
	if cols < 72 || rows < 10 {
		return
	}
	limit := min(5, len(players))
	if limit == 0 {
		return
	}
	x := cols - 22
	writeText(cells, cols, x, 1, "TOP BLOBS", "#f8fafc", "#0f172a", true)
	for i := 0; i < limit; i++ {
		player := players[i]
		name := defaultName(player.name)
		if len([]rune(name)) > 10 {
			name = string([]rune(name)[:10])
		}
		line := fmt.Sprintf("%d %-10s %4.0f", i+1, name, player.mass)
		writeText(cells, cols, x, 2+i, line, player.color, "#020617", false)
	}
}

func writeText(cells []ggp.Cell, cols, x, y int, text, fg, bg string, bold bool) {
	for i, r := range text {
		idx := y*cols + x + i
		if idx < 0 || idx >= len(cells) || x+i >= cols {
			continue
		}
		cells[idx].Ch = string(r)
		cells[idx].Fg = fg
		cells[idx].Bg = bg
		if bold {
			cells[idx].Attrs = []string{"bold"}
		} else {
			cells[idx].Attrs = nil
		}
	}
}

func changedCells(previous, next []ggp.Cell) []ggp.Cell {
	if len(previous) != len(next) {
		return next
	}
	changed := make([]ggp.Cell, 0, min(len(next), 256))
	for i := range next {
		if !sameCell(previous[i], next[i]) {
			changed = append(changed, next[i])
		}
	}
	return changed
}

func sameCell(a, b ggp.Cell) bool {
	if a.X != b.X || a.Y != b.Y || a.Ch != b.Ch || a.Fg != b.Fg || a.Bg != b.Bg || len(a.Attrs) != len(b.Attrs) {
		return false
	}
	for i := range a.Attrs {
		if a.Attrs[i] != b.Attrs[i] {
			return false
		}
	}
	return true
}

func (s *session) shouldReportScoreLocked(score int64, now time.Time) bool {
	if score <= s.bestScore || score < int64(startMass) {
		return false
	}
	if score < s.bestScore+5 && now.Sub(s.scoreAt) < 5*time.Second {
		return false
	}
	s.bestScore = score
	s.scoreAt = now
	return true
}

func scoreForPlayer(player blob, ok bool) int64 {
	if !ok || !player.alive {
		return 0
	}
	return int64(math.Floor(player.mass))
}

func (state roomState) player(id string) (blob, bool) {
	for _, player := range state.players {
		if player.id == id {
			return player, true
		}
	}
	return blob{}, false
}

func (r *room) spawnPointLocked(mass float64) (float64, float64) {
	radius := radiusForMass(mass)
	for attempts := 0; attempts < 60; attempts++ {
		x := radius + 4 + r.rng.Float64()*(worldWidth-radius*2-8)
		y := radius + 4 + r.rng.Float64()*(worldHeight-radius*2-8)
		clear := true
		for _, player := range r.players {
			if player.alive && distance(x, y, player.x, player.y) < radiusForMass(player.mass)+radius+8 {
				clear = false
				break
			}
		}
		if clear {
			return x, y
		}
	}
	return worldWidth / 2, worldHeight / 2
}

func (r *room) randomPelletLocked() pellet {
	return pellet{
		x:     2 + r.rng.Float64()*(worldWidth-4),
		y:     2 + r.rng.Float64()*(worldHeight-4),
		color: pelletColors[r.rng.Intn(len(pelletColors))],
	}
}

func playerGlyph(player blob, self bool) string {
	if self {
		return "@"
	}
	name := strings.TrimSpace(player.name)
	if name == "" {
		return "o"
	}
	return strings.ToUpper(string([]rune(name)[0]))
}

func speedForMass(mass float64) float64 {
	return clampFloat(2.2-math.Sqrt(mass)/9, 0.45, 1.55)
}

func radiusForMass(mass float64) float64 {
	return clampFloat(math.Sqrt(mass)/2.2, 1, 9)
}

func worldToScreen(x, y, cameraX, cameraY float64) (int, int) {
	return int(math.Round(x - cameraX)), int(math.Round(y - cameraY))
}

func distance(ax, ay, bx, by float64) float64 {
	dx, dy := ax-bx, ay-by
	return math.Sqrt(dx*dx + dy*dy)
}

func hashString(value string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(value))
	return h.Sum32()
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func defaultName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "player"
	}
	return value
}

func clampFloat(value, minValue, maxValue float64) float64 {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func viewportSize(cols, rows int) (int, int) {
	return min(max(cols, minViewCols), maxViewCols), min(max(rows, minViewRows), maxViewRows)
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

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
