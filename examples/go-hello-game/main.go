package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/gorilla/websocket"
)

const (
	protocolCellV1   = "ggp.cell.v1"
	typeHello        = "hello"
	typeReady        = "ready"
	typeFrame        = "frame"
	typeInput        = "input"
	typeResize       = "resize"
	capRenderCell    = "render.cell.v1"
	capInputKeyboard = "input.keyboard.v1"
)

type envelope struct {
	Type string `json:"type"`
}

type hello struct {
	Type     string `json:"type"`
	Protocol string `json:"protocol"`
	Player   player `json:"player"`
	Viewport struct {
		Cols int `json:"cols"`
		Rows int `json:"rows"`
	} `json:"viewport"`
}

type player struct {
	Name string `json:"name"`
}

type ready struct {
	Type         string   `json:"type"`
	Title        string   `json:"title"`
	TargetFPS    int      `json:"targetFps"`
	Capabilities []string `json:"capabilities"`
}

type frame struct {
	Type   string `json:"type"`
	Seq    int    `json:"seq"`
	Mode   string `json:"mode"`
	Status string `json:"status,omitempty"`
	Cells  []cell `json:"cells"`
}

type cell struct {
	X     int      `json:"x"`
	Y     int      `json:"y"`
	Ch    string   `json:"ch"`
	Fg    string   `json:"fg,omitempty"`
	Bg    string   `json:"bg,omitempty"`
	Attrs []string `json:"attrs,omitempty"`
}

type input struct {
	Type string `json:"type"`
	Key  string `json:"key"`
}

type resize struct {
	Type string `json:"type"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

type session struct {
	conn   *websocket.Conn
	name   string
	cols   int
	rows   int
	x      int
	y      int
	seq    int
	status string
}

var upgrader = websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/ggp", handleGGP)
	addr := ":" + env("PORT", "8081")
	log.Printf("example GGP game listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
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
	var h hello
	if err := json.Unmarshal(payload, &h); err != nil || h.Type != typeHello || h.Protocol != protocolCellV1 {
		return
	}

	s := &session{conn: conn, name: h.Player.Name, cols: max(h.Viewport.Cols, 40), rows: max(h.Viewport.Rows, 12), x: 10, y: 5, status: "Move with arrow keys or WASD."}
	_ = conn.WriteJSON(ready{Type: typeReady, Title: "Hello GGP", TargetFPS: 8, Capabilities: []string{capRenderCell, capInputKeyboard}})
	s.sendFrame()

	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return
		}
		s.handle(msg)
	}
}

func (s *session) handle(payload []byte) {
	var env envelope
	if err := json.Unmarshal(payload, &env); err != nil {
		return
	}
	switch env.Type {
	case typeInput:
		var in input
		if err := json.Unmarshal(payload, &in); err != nil {
			return
		}
		s.move(in.Key)
	case typeResize:
		var r resize
		if err := json.Unmarshal(payload, &r); err != nil {
			return
		}
		s.cols, s.rows = max(r.Cols, 40), max(r.Rows, 12)
	}
	s.sendFrame()
}

func (s *session) move(key string) {
	switch key {
	case "left", "a", "h":
		s.x--
	case "right", "d", "l":
		s.x++
	case "up", "w", "k":
		s.y--
	case "down", "s", "j":
		s.y++
	}
	s.x = clamp(s.x, 1, s.cols-2)
	s.y = clamp(s.y, 1, s.rows-2)
	s.status = "Hello, " + defaultName(s.name) + "."
}

func (s *session) sendFrame() {
	s.seq++
	cells := make([]cell, 0, s.cols*s.rows)
	for y := 0; y < s.rows; y++ {
		for x := 0; x < s.cols; x++ {
			ch, fg, bg := " ", "#94a3b8", "#0f172a"
			if x == s.x && y == s.y {
				ch, fg = "@", "#7dd3fc"
			} else if y == 0 || y == s.rows-1 || x == 0 || x == s.cols-1 {
				bg = "#1e293b"
			}
			cells = append(cells, cell{X: x, Y: y, Ch: ch, Fg: fg, Bg: bg})
		}
	}
	writeText(cells, s.cols, 3, 2, "Hello GGP", "#facc15")
	writeText(cells, s.cols, 3, 3, "Container image game", "#bae6fd")
	_ = s.conn.WriteJSON(frame{Type: typeFrame, Seq: s.seq, Mode: "full", Status: s.status, Cells: cells})
}

func writeText(cells []cell, cols, x, y int, text, fg string) {
	for i, r := range text {
		idx := y*cols + x + i
		if idx >= 0 && idx < len(cells) {
			cells[idx].Ch = string(r)
			cells[idx].Fg = fg
			cells[idx].Attrs = []string{"bold"}
		}
	}
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

func clamp(value, minValue, maxValue int) int {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
