package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"sync"

	"github.com/gorilla/websocket"

	"gamegateway/internal/ggp"
)

const (
	worldWidth  = 180
	worldHeight = 92
)

type session struct {
	conn    *websocket.Conn
	player  string
	cols    int
	rows    int
	x       int
	y       int
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
)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/ggp", handleGGP)

	addr := ":" + env("PORT", "8081")
	log.Printf("sample RPG game listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}

func handleGGP(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	_, payload, err := conn.ReadMessage()
	if err != nil {
		return
	}

	var hello ggp.Hello
	if err := json.Unmarshal(payload, &hello); err != nil || hello.Type != ggp.TypeHello {
		_ = conn.WriteJSON(ggp.Error{Type: ggp.TypeError, Message: "expected hello"})
		return
	}

	s := &session{
		conn:    conn,
		player:  hello.Player.Name,
		cols:    max(hello.Viewport.Cols/2, 40),
		rows:    max(hello.Viewport.Rows, 14),
		x:       88,
		y:       38,
		message: "Walk the village with arrow keys or WASD. Houses block movement.",
	}

	_ = conn.WriteJSON(ggp.Ready{
		Type:         ggp.TypeReady,
		Title:        "Meadow Village",
		TargetFPS:    8,
		Capabilities: []string{ggp.CapRenderCell, ggp.CapRenderSquare, ggp.CapInputKeyboard},
		Render:       ggp.Render{Mode: ggp.RenderModeCells, CellAspect: ggp.CellAspectSquareWide},
	})
	s.sendFrame()

	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return
		}
		s.handleMessage(msg)
	}
}

func (s *session) handleMessage(payload []byte) {
	var envelope ggp.Envelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	switch envelope.Type {
	case ggp.TypeInput:
		var input ggp.Input
		if err := json.Unmarshal(payload, &input); err != nil {
			return
		}
		s.move(input.Key)
	case ggp.TypeResize:
		var resize ggp.Resize
		if err := json.Unmarshal(payload, &resize); err != nil {
			return
		}
		s.cols = max(resize.Cols, 40)
		s.rows = max(resize.Rows, 14)
	}

	s.sendFrameLocked()
}

func (s *session) move(key string) {
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
		return
	}

	nx, ny := s.x+dx, s.y+dy
	if blocked(nx, ny) {
		s.message = "That way is blocked. Try the village paths."
		return
	}

	s.x, s.y = nx, ny
	s.message = describeTile(nx, ny)
}

func (s *session) sendFrame() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sendFrameLocked()
}

func (s *session) sendFrameLocked() {
	s.seq++
	frame := ggp.Frame{
		Type:   ggp.TypeFrame,
		Seq:    s.seq,
		Mode:   ggp.FrameFull,
		Status: s.message,
		Cells:  s.renderCells(),
	}
	_ = s.conn.WriteJSON(frame)
}

func (s *session) renderCells() []ggp.Cell {
	cols := max(s.cols, 40)
	rows := max(s.rows, 14)
	cells := make([]ggp.Cell, 0, cols*rows)

	viewRows := max(rows, 8)
	camX := clamp(s.x-cols/2, 0, max(worldWidth-cols, 0))
	camY := clamp(s.y-viewRows/2, 0, max(worldHeight-viewRows, 0))

	for y := 0; y < rows; y++ {
		for x := 0; x < cols; x++ {
			wx, wy := camX+x, camY+y
			ch, fg, bg := tile(wx, wy)
			if wx == s.x && wy == s.y {
				ch, fg, bg = "@", "#7dd3fc", bg
			}
			cells = append(cells, ggp.Cell{X: x, Y: y, Ch: ch, Fg: fg, Bg: bg})
		}
	}

	return cells
}

func (s *session) writeText(cells []ggp.Cell, x, y int, text, fg, bg string) {
	for i, r := range text {
		cx := x + i
		if cx >= s.cols-2 || y < 0 || y >= s.rows {
			return
		}
		idx := y*s.cols + cx
		if idx >= 0 && idx < len(cells) {
			cells[idx] = ggp.Cell{X: cx, Y: y, Ch: string(r), Fg: fg, Bg: bg, Attrs: []string{"bold"}}
		}
	}
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
