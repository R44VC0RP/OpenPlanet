package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"

	"github.com/gorilla/websocket"

	"gamegateway/internal/ggp"
)

const (
	worldWidth  = 72
	worldHeight = 34
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
		cols:    max(hello.Viewport.Cols, 40),
		rows:    max(hello.Viewport.Rows, 14),
		x:       36,
		y:       17,
		message: "Walk the village with arrow keys or WASD. Houses block movement.",
	}

	_ = conn.WriteJSON(ggp.Ready{
		Type:         ggp.TypeReady,
		Title:        "Meadow Village",
		TargetFPS:    8,
		Capabilities: []string{ggp.CapRenderCell, ggp.CapInputKeyboard},
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
		Type:  ggp.TypeFrame,
		Seq:   s.seq,
		Mode:  ggp.FrameFull,
		Cells: s.renderCells(),
	}
	_ = s.conn.WriteJSON(frame)
}

func (s *session) renderCells() []ggp.Cell {
	cols := max(s.cols, 40)
	rows := max(s.rows, 14)
	cells := make([]ggp.Cell, 0, cols*rows)

	viewRows := max(rows-4, 8)
	camX := clamp(s.x-cols/2, 0, max(worldWidth-cols, 0))
	camY := clamp(s.y-viewRows/2, 0, max(worldHeight-viewRows, 0))

	for y := 0; y < rows; y++ {
		for x := 0; x < cols; x++ {
			ch, fg, bg := " ", "#cbd5e1", "#0f172a"
			switch {
			case y == 0 || y == rows-1 || x == 0 || x == cols-1:
				ch, fg, bg = "#", "#94a3b8", "#1e293b"
			case y == 1:
				ch, fg, bg = " ", "#cbd5e1", "#1e293b"
			case y == rows-3:
				ch, fg, bg = "-", "#475569", "#0f172a"
			case y == rows-2:
				ch, fg, bg = " ", "#cbd5e1", "#0f172a"
			default:
				wx, wy := camX+x-1, camY+y-2
				ch, fg, bg = tile(wx, wy)
				if wx == s.x && wy == s.y {
					ch, fg, bg = "@", "#7dd3fc", bg
				}
			}
			cells = append(cells, ggp.Cell{X: x, Y: y, Ch: ch, Fg: fg, Bg: bg})
		}
	}

	s.writeText(cells, 2, 1, fmt.Sprintf("Meadow Village | %s | arrows/WASD move | tab chat | esc gateway", s.player), "#f8fafc", "#1e293b")
	s.writeText(cells, 2, rows-2, s.message, "#fde68a", "#0f172a")
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
		return "#", "#94a3b8", "#1e293b"
	}
	if x == 0 || y == 0 || x == worldWidth-1 || y == worldHeight-1 {
		return "#", "#94a3b8", "#1e293b"
	}
	if y >= 27 && x >= 58 {
		return "~", "#38bdf8", "#082f49"
	}
	for _, h := range houses {
		if inHouse(x, y, h) {
			return houseTile(x, y, h)
		}
	}
	if onPath(x, y) {
		return ".", "#fbbf24", "#422006"
	}
	if isTree(x, y) {
		return "T", "#22c55e", "#052e16"
	}
	if (x*3+y*5)%19 == 0 {
		return "'", "#86efac", "#052e16"
	}
	return " ", "#86efac", "#052e16"
}

func blocked(x, y int) bool {
	if x <= 0 || y <= 0 || x >= worldWidth-1 || y >= worldHeight-1 {
		return true
	}
	if y >= 27 && x >= 58 {
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
	case y >= 25 && x >= 54:
		return "You hear water moving southeast of the village."
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
		return "^", "#f97316", "#431407"
	}
	if y == h.y+h.h-1 && x == h.x+h.w/2 {
		return "+", "#facc15", "#78350f"
	}
	if x == h.x || x == h.x+h.w-1 || y == h.y+h.h-1 {
		return "#", "#fef3c7", "#78350f"
	}
	if y == h.y+2 && (x == h.x+2 || x == h.x+h.w-3) {
		return "o", "#bfdbfe", "#1e3a8a"
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
	return y == 17 || x == 36 || (x >= 8 && x <= 60 && y == 13) || (x >= 18 && x <= 58 && y == 25)
}

func isTree(x, y int) bool {
	return (x*11+y*7)%37 == 0 || (x > 4 && x < 16 && y > 14 && y < 20 && (x+y)%4 == 0)
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
