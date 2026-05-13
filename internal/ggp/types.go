package ggp

const (
	ProtocolCellV1 = "ggp.cell.v1"

	CapRenderCell    = "render.cell.v1"
	CapInputKeyboard = "input.keyboard.v1"
	CapInputMouse    = "input.mouse.v1"
	CapChatBridge    = "chat.bridge.v1"
	CapScoreReport   = "score.report.v1"
	CapAuthSession   = "auth.session-token.v1"
	CapMultiplayer   = "multiplayer.room.v1"
	CapPresence      = "presence.roster.v1"

	TypeHello    = "hello"
	TypeReady    = "ready"
	TypeFrame    = "frame"
	TypeInput    = "input"
	TypeResize   = "resize"
	TypeFocus    = "focus"
	TypeScore    = "score"
	TypeLeave    = "leave"
	TypePresence = "presence"
	TypeError    = "error"

	FrameFull  = "full"
	FramePatch = "patch"

	AuthTypeSessionJWT = "ggp-session-jwt"

	MultiplayerModeRoom = "room"

	LeaveReasonUserExit        = "user-exit"
	LeaveReasonDisconnect      = "disconnect"
	LeaveReasonKicked          = "kicked"
	LeaveReasonRoomClosed      = "room-closed"
	LeaveReasonGatewayShutdown = "gateway-shutdown"

	ErrorAuthRequired        = "auth_required"
	ErrorAuthInvalid         = "auth_invalid"
	ErrorUnsupportedProtocol = "unsupported_protocol"
	ErrorRoomFull            = "room_full"
	ErrorRoomClosed          = "room_closed"
	ErrorRateLimited         = "rate_limited"
	ErrorInternal            = "internal_error"
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
	Auth         *Auth    `json:"auth,omitempty"`
}

type Ready struct {
	Type         string       `json:"type"`
	Title        string       `json:"title"`
	TargetFPS    int          `json:"targetFps"`
	Capabilities []string     `json:"capabilities"`
	Multiplayer  *Multiplayer `json:"multiplayer,omitempty"`
}

type Auth struct {
	Type  string `json:"type"`
	Token string `json:"token"`
}

type Multiplayer struct {
	Mode       string `json:"mode"`
	MaxPlayers int    `json:"maxPlayers"`
	Presence   bool   `json:"presence"`
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

type Leave struct {
	Type   string `json:"type"`
	Reason string `json:"reason"`
}

type Presence struct {
	Type       string           `json:"type"`
	Seq        int              `json:"seq"`
	RoomID     string           `json:"roomId"`
	MaxPlayers int              `json:"maxPlayers"`
	Players    []PresencePlayer `json:"players"`
}

type PresencePlayer struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	State string `json:"state"`
}

type Error struct {
	Type    string `json:"type"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
}
