package ggp

const (
	ProtocolCellV1 = "ggp.cell.v1"

	CapRenderCell    = "render.cell.v1"
	CapInputKeyboard = "input.keyboard.v1"
	CapInputMouse    = "input.mouse.v1"
	CapChatBridge    = "chat.bridge.v1"
	CapScoreReport   = "score.report.v1"

	TypeHello  = "hello"
	TypeReady  = "ready"
	TypeFrame  = "frame"
	TypeInput  = "input"
	TypeResize = "resize"
	TypeFocus  = "focus"
	TypeScore  = "score"
	TypeError  = "error"

	FrameFull  = "full"
	FramePatch = "patch"
)

type Envelope struct {
	Type string `json:"type"`
}

type Player struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	SSHKeyFingerprint string `json:"sshKeyFingerprint"`
}

type Viewport struct {
	Cols int `json:"cols"`
	Rows int `json:"rows"`
}

type Hello struct {
	Type         string   `json:"type"`
	Protocol     string   `json:"protocol"`
	SessionID    string   `json:"sessionId"`
	RoomID       string   `json:"roomId"`
	Player       Player   `json:"player"`
	Viewport     Viewport `json:"viewport"`
	Capabilities []string `json:"capabilities"`
}

type Ready struct {
	Type         string   `json:"type"`
	Title        string   `json:"title"`
	TargetFPS    int      `json:"targetFps"`
	Capabilities []string `json:"capabilities"`
}

type Cell struct {
	X     int      `json:"x"`
	Y     int      `json:"y"`
	Ch    string   `json:"ch"`
	Fg    string   `json:"fg,omitempty"`
	Bg    string   `json:"bg,omitempty"`
	Attrs []string `json:"attrs,omitempty"`
}

type Frame struct {
	Type   string `json:"type"`
	Seq    int    `json:"seq"`
	Mode   string `json:"mode"`
	Status string `json:"status,omitempty"`
	Cells  []Cell `json:"cells"`
}

type Score struct {
	Type  string `json:"type"`
	Value int64  `json:"value"`
}

type Input struct {
	Type string   `json:"type"`
	Kind string   `json:"kind"`
	Key  string   `json:"key"`
	Text string   `json:"text,omitempty"`
	Mods []string `json:"mods,omitempty"`
}

type Resize struct {
	Type string `json:"type"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

type Focus struct {
	Type    string `json:"type"`
	Focused bool   `json:"focused"`
}

type Error struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}
