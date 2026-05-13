# Game Gateway Protocol: `ggp.cell.v1`

`ggp.cell.v1` is a language-neutral WebSocket protocol for games rendered inside the SSH gateway TUI.

Game servers render directly in terminal cells. One protocol cell maps to exactly one terminal cell in the gateway game pane. The gateway clips frames to the pane, applies terminal-supported styles, and reserves surrounding UI space for navigation and chat.

This document describes the base single-session protocol and the draft multiplayer extension. Multiplayer is opt-in: games that do not advertise the multiplayer capability continue to receive isolated player sessions.

## Trust Model

The gateway is the identity authority. Players authenticate to the gateway with SSH keys; game servers must not accept player identity, room membership, or permissions from an unauthenticated WebSocket client.

Games are authoritative for their own world state. Clients only send input through the gateway. The game server decides whether an input is valid, updates simulation state, and sends rendered frames back to each connection.

For production multiplayer over an untrusted network, a game MUST require gateway-signed session authentication before joining a player to a room. Direct WebSocket clients, replayed tokens, forged player IDs, and forged room IDs must be rejected.

Use `wss://` for any game endpoint outside a private trusted network. `ws://` is acceptable only for loopback, private Docker networks, or equivalent trusted transport.

## Capabilities

Base capabilities:

| Capability | Direction | Meaning |
| --- | --- | --- |
| `render.cell.v1` | gateway and game | Terminal-cell frame rendering |
| `input.keyboard.v1` | gateway and game | Keyboard input forwarding |
| `input.mouse.v1` | gateway and game | Mouse input forwarding, if supported by the game |
| `chat.bridge.v1` | gateway | Gateway-owned room chat exists beside the game |

Multiplayer and security capabilities:

| Capability | Direction | Meaning |
| --- | --- | --- |
| `auth.session-token.v1` | gateway and game | `hello.auth` contains a short-lived gateway-signed session token |
| `multiplayer.room.v1` | game | Multiple authenticated player sessions may join the same `roomId` |
| `presence.roster.v1` | game | Game may send roster snapshots for gateway UI |

The gateway should only open multiplayer rooms for games configured with `maxPlayers > 1`. The game must still enforce its own room capacity because configuration can be stale or bypassed.

## Handshake

Gateway sends `hello` with the terminal-cell viewport currently available for the game pane:

```json
{
  "type": "hello",
  "protocol": "ggp.cell.v1",
  "sessionId": "sess_...",
  "roomId": "game:lobby",
  "player": { "id": "...", "name": "ryan", "sshKeyFingerprint": "SHA256:..." },
  "viewport": { "cols": 120, "rows": 36 },
  "capabilities": ["render.cell.v1", "input.keyboard.v1", "chat.bridge.v1", "score.report.v1", "auth.session-token.v1"],
  "auth": {
    "type": "ggp-session-jwt",
    "token": "eyJ..."
  }
}
```

`auth` is optional for local single-player games and mandatory for production multiplayer. When `auth.session-token.v1` is present, the game must validate the token before trusting any other `hello` field.

Game replies with `ready`:

```json
{
  "type": "ready",
  "title": "Meadow Quest",
  "targetFps": 8,
  "capabilities": ["render.cell.v1", "input.keyboard.v1", "score.report.v1", "auth.session-token.v1", "multiplayer.room.v1"],
  "multiplayer": {
    "mode": "room",
    "maxPlayers": 16,
    "presence": true
  }
}
```

After `ready`, the gateway sends `resize` with the current terminal-cell viewport. All future resize events use the same direct terminal-cell coordinate space.

If the game cannot validate authentication, cannot support the requested room, or the room is full, it should send an `error` message and close the socket.

## Session Authentication

`auth.session-token.v1` protects multiplayer rooms from direct-client spoofing. The token proves that the gateway created this connection for this player, room, game, endpoint, and protocol.

The preferred token format is a compact JWS/JWT with an asymmetric signature such as EdDSA or ES256. Shared-secret HMAC is allowed only when the secret is unique per game endpoint and rotated independently. The algorithm `none` is never valid.

The current gateway implementation uses `HS256`. Built-in trusted games can use the gateway-level `GGP_SESSION_SECRET`. Submitted third-party multiplayer games use a per-game 32+ byte secret provided during submission; set that same secret in your game server and keep it out of logs/source control. Single-player games can omit a secret.

The token should be sent in `hello.auth.token`, not in the WebSocket URL, to reduce accidental logging. Gateways and games must redact tokens from logs.

Required JWT header fields:

| Field | Requirement |
| --- | --- |
| `typ` | `ggp-session+jwt` |
| `alg` | Allowed signing algorithm for this gateway/game pair |
| `kid` | Key identifier when the gateway publishes multiple keys |

Required JWT claims:

| Claim | Requirement |
| --- | --- |
| `iss` | Stable gateway issuer ID |
| `aud` | Registered game ID or endpoint audience; must be specific to this game |
| `sub` | Player ID |
| `jti` | Unique token ID for replay prevention |
| `iat` | Issued-at time |
| `nbf` | Not-before time |
| `exp` | Expiration time, recommended 30-120 seconds after `iat` |
| `ggp_protocol` | `ggp.cell.v1` |
| `ggp_session_id` | Must equal `hello.sessionId` |
| `ggp_room_id` | Must equal `hello.roomId` |
| `ggp_game_id` | Registered game ID |
| `ggp_endpoint` | Canonical endpoint URL or endpoint hash |
| `ggp_player_name` | Player display name at connection time |
| `ggp_ssh_fingerprint` | Player SSH key fingerprint, if exposed to the game |

Game validation requirements:

| Check | Requirement |
| --- | --- |
| Signature | Verify with the configured gateway key and allowed algorithms |
| Issuer | Reject unknown `iss` values |
| Audience | Reject tokens whose `aud` is not this game |
| Time | Enforce `nbf`, `iat`, and `exp` with small clock skew only |
| Replay | Store each accepted `jti` until after expiration and reject duplicates |
| Binding | Match token claims to `hello.sessionId`, `hello.roomId`, endpoint, protocol, and player fields |
| First message | Require `hello` within a short timeout, recommended 5 seconds |
| Fail closed | Do not create a room session until all checks pass |

Unsigned `hello.player` fields are convenience fields only. After token validation, the authenticated player is the token subject plus the token-bound player metadata. If token claims and `hello` fields disagree, reject the connection.

## Multiplayer Rooms

`multiplayer.room.v1` means the game accepts multiple authenticated WebSocket sessions with the same `roomId` and maintains shared authoritative state for that room.

Each player still has a separate WebSocket connection. The game should group connections by `roomId`, bind each connection to exactly one authenticated `player.id`, and send frames independently per connection. Per-connection frames allow fog-of-war, private UI, and different viewport sizes.

The gateway should select rooms and enforce configured capacity before opening a game connection. The game must also enforce room capacity and permissions after token validation.

Default room semantics:

| Rule | Requirement |
| --- | --- |
| Identity | One connection maps to one authenticated player/session |
| Authority | Game server owns world state and validates all player input |
| Actor binding | Inputs apply only to the player bound to that WebSocket |
| No peer trust | Players never send messages directly to other players |
| Join | A validated `hello` joins the room |
| Leave | Socket close leaves the room; gateway may also send `leave` when graceful |
| Rejoin | A new token and new session may reattach to the same `player.id` and `roomId` if the game allows it |
| Duplicate sessions | Game decides whether a second active session for the same player replaces, rejects, or coexists with the old one |

Recommended graceful leave message from gateway to game:

```json
{ "type": "leave", "reason": "user-exit" }
```

Allowed `reason` values are `user-exit`, `disconnect`, `kicked`, `room-closed`, and `gateway-shutdown`. A game must treat an ungraceful socket close as `disconnect`.

## Presence

Presence is optional. If a game advertises `presence.roster.v1`, it may send roster snapshots for gateway UI and audit visibility:

```json
{
  "type": "presence",
  "seq": 7,
  "roomId": "cell-garden:lobby",
  "maxPlayers": 16,
  "players": [
    { "id": "...", "name": "ryan", "state": "playing" },
    { "id": "...", "name": "sam", "state": "spectating" }
  ]
}
```

Allowed player states are `joining`, `playing`, `spectating`, `disconnected`, and `left`. Presence is advisory UI data; the gateway must not use game-sent presence to authenticate players.

## Multiplayer Input Security

Input messages are scoped to the authenticated WebSocket session. A game must ignore any future input field that attempts to specify another actor, player ID, or room ID.

The game should validate every input against authoritative state. Movement limits, cooldowns, turn order, inventory changes, combat results, score changes, and win conditions are game-owned decisions, not client-owned claims.

Games should apply protocol-level abuse limits:

| Control | Recommendation |
| --- | --- |
| Message size | Set a small maximum JSON frame size |
| Message type | Reject unknown message types unless explicitly forward-compatible |
| Rate limit | Apply per-connection input rate limits |
| Idle timeout | Close idle unauthenticated sockets quickly |
| Schema validation | Validate all required fields and bounds |
| Ordering | Treat WebSocket order as transport order, not proof of game validity |
| Logging | Log auth failures, joins, leaves, kicks, abnormal closes, and rate-limit events with redacted tokens |

Gateway-owned chat remains outside game authority unless a future chat extension is negotiated. A game must not treat player-supplied in-game text as gateway chat unless it receives a gateway-authenticated chat bridge message defined by that future extension.

## Building A Multiplayer Game

Use this flow for a game that wants native multiplayer:

1. Configure the gateway game record with `maxPlayers > 1`.
2. Generate a 32+ byte random game session secret and configure it in your game server.
3. Accept WebSocket connections on your game endpoint.
4. Require the first message to be `hello` within a short timeout.
5. Validate `hello.auth.token` before trusting `hello.player`, `hello.roomId`, or `hello.sessionId`.
6. Group accepted connections by validated `roomId`.
7. Bind each socket to exactly one validated `player.id`.
8. Apply inputs only to that socket's bound player.
9. Keep authoritative room state on the game server.
10. Send each player their own `frame` messages.

Minimum game-side validation checklist:

| Item | Required behavior |
| --- | --- |
| Secret/key | Use a per-game secret or public key configured outside source control |
| Token signature | Reject invalid signatures and `none` algorithms |
| Token lifetime | Reject expired tokens and tokens not valid yet |
| Replay | Reject a reused `jti` until after its `exp` time |
| Audience | Require `aud` to match your game ID |
| Room binding | Require token room to match `hello.roomId` |
| Session binding | Require token session to match `hello.sessionId` |
| Player binding | Require token subject/name/fingerprint to match `hello.player` |
| Capacity | Enforce your own room capacity after auth succeeds |
| Inputs | Ignore any input that tries to name a different player or room |

Simple room loop:

```text
websocket accept
read hello
validate token
room = rooms[validated.roomId]
player = validated.sub
room.add(player, websocket)
send ready with multiplayer.room.v1
send first frame

for each input from websocket:
  actor = socket.boundPlayer
  validate action against room state
  update room state
  send frames to affected players
```

The sample game in `cmd/sample-game` demonstrates this model. With a configured session secret, Meadow Quest becomes a shared room: players see each other, combat and loot are room-authoritative, duplicate active sessions are rejected, and the game rejects forged or replayed tokens.

## Submitting A Game

Players submit Docker/OCI images from the gateway lobby. Submitted games are not visible in the public game list until an admin approves them and the deploy process starts their containers.

Game image requirements:

1. Listen on the `PORT` environment variable.
2. Serve the GGP WebSocket endpoint at `/ggp`.
3. Serve `GET /healthz` when possible.
4. Run without privileged mode, host networking, or host mounts.
5. Store no secrets in the image.
6. For multiplayer, read `GGP_SESSION_SECRET` and validate `hello.auth.token`.

The gateway runs submitted images on its private Docker network and generates the internal endpoint:

```text
ws://gamegateway-game-<game_id>:<container_port>/ggp
```

Submission fields:

| Field | Meaning |
| --- | --- |
| Game ID | Stable lowercase ID used as the token audience, e.g. `my-arena` |
| Name | Display name shown in the lobby |
| Description | Short lobby description |
| Docker image | Pinned image ref, e.g. `docker.io/alice/my-game:0.1.0` or an image digest |
| Container port | Port your process listens on, usually `8081` |
| Min cols / rows | Minimum terminal-cell viewport your game expects |
| Max players | `1` for single-player, greater than `1` for multiplayer |
| Game secret | Required only when `maxPlayers > 1`; your game uses it to validate `hello.auth.token` |
| Supports mouse | Whether the game expects mouse input |

Submission does not immediately run arbitrary images. Deploy pulls and starts pending/approved image games in constrained containers. Admin checks then probe the internal container endpoint:

1. Pulls the submitted image ref.
2. Starts it as `gamegateway-game-<game_id>` on the gateway Docker network.
3. Injects `PORT`, `GGP_GAME_ID`, `GGP_ENDPOINT_URL`, and `GGP_SESSION_SECRET` when set.
4. Opens a WebSocket to `/ggp` with a short timeout.
5. Sends a synthetic `hello` using the submitted game ID.
6. Includes `hello.auth.token` when a game secret was provided.
7. Requires a valid `ready` response.
8. Requires `render.cell.v1`.
9. For multiplayer, also requires `auth.session-token.v1` and `multiplayer.room.v1`.

Admin review flow:

1. Admin opens `Submitted Games` from the lobby.
2. After deploy has started the pending image, admin can test-play it.
3. Admin can re-run the container check.
4. Approving re-runs the container check, marks the game `approved`, and exposes it in the public lobby.
5. Rejecting marks the game `rejected` and keeps it out of the public lobby.

The `examples/go-hello-game` directory contains a minimal Docker-packaged GGP game that can be cloned and pushed as a starting point.

## Rendering

Games send terminal-style cell frames. Coordinates are zero-based and use the terminal-cell viewport from `hello` or the latest `resize` message.

```text
protocol 80x24 -> terminal 80x24
```

Games are responsible for choosing glyphs, colors, spacing, and any aspect-ratio tradeoffs that fit normal terminal cells. The gateway does not stretch, pack, or remap game cells.

## Frames

Frames are full snapshots or patches in terminal-cell coordinates:

```json
{
  "type": "frame",
  "seq": 12,
  "mode": "full",
  "status": "You cross soft meadow grass.",
  "cells": [
    { "x": 10, "y": 6, "ch": " ", "fg": "#86efac", "bg": "#14532d" },
    { "x": 11, "y": 6, "ch": "@", "fg": "#7dd3fc", "bg": "#14532d", "attrs": ["bold"] }
  ]
}
```

Supported cell fields:

| Field | Purpose |
| --- | --- |
| `x`, `y` | Terminal-cell coordinates |
| `ch` | Single display glyph; use a space for colored tile blocks |
| `fg` | Foreground color, any terminal-supported color string accepted by the gateway renderer |
| `bg` | Background color |
| `attrs` | Optional attributes, currently `bold` |

Use `status` for player-facing text that should be rendered by the gateway outside the cell grid.

## Scores

Games can report the current player's score when the gateway advertises `score.report.v1`:

```json
{ "type": "score", "value": 4200 }
```

The gateway stores the player's best score for the active game and shows the leaderboard alongside chat. Higher scores rank first.

## Input

The gateway forwards keyboard input:

```json
{ "type": "input", "kind": "key", "key": "left" }
```

Resize events use terminal-cell dimensions:

```json
{ "type": "resize", "cols": 80, "rows": 24 }
```

## Errors

Games should send an error before closing when rejecting a connection:

```json
{ "type": "error", "code": "room_full", "message": "Room is full" }
```

Recommended error codes:

| Code | Meaning |
| --- | --- |
| `auth_required` | Multiplayer requires `auth.session-token.v1` |
| `auth_invalid` | Token is malformed, expired, replayed, or failed validation |
| `unsupported_protocol` | Protocol or required capability is unsupported |
| `room_full` | The room has reached capacity |
| `room_closed` | The room no longer accepts joins |
| `rate_limited` | Connection exceeded input or message limits |
| `internal_error` | The game failed unexpectedly |

## Compatibility

Existing single-player games remain valid if they only implement `hello`, `ready`, `frame`, `input`, and `resize`.

Multiplayer games must implement `auth.session-token.v1` and `multiplayer.room.v1`. A gateway should not route more than one player into a room unless both the game configuration and negotiated capabilities allow it.

The base protocol version remains `ggp.cell.v1` because rendering and input wire formats are unchanged. Multiplayer is negotiated through capabilities so older games fail closed into single-player behavior.
