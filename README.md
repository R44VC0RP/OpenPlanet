# Game Gateway

SSH-first game gateway prototype. Players connect with SSH keys, the gateway renders a terminal portal, and games plug in as language-agnostic WebSocket endpoints that speak `ggp.cell.v1`.

## Architecture

```text
ssh -p 2222 localhost
        |
        v
Wish SSH server + Bubble Tea gateway
        |
        +-- Postgres: players, SSH keys, games, rooms, chat
        |
        +-- WebSocket game endpoint: ggp.cell.v1 frames and input
```

The gateway accepts any SSH public key and uses its SHA256 fingerprint as a stable identity. There is no password and no pre-registration.

## Run Locally

Create a local `.env` file with your managed Postgres connection string:

```sh
cp .env.example .env
```

Then edit `.env` and set `DATABASE_URL` to your PlanetScale Postgres URL.

The gateway also loads `.env` when run directly with `go run`, so Docker and non-Docker local runs use the same configuration.

```sh
docker compose up --build
ssh -p 2222 localhost
```

If your SSH client does not offer a key automatically, pass one explicitly:

```sh
ssh -i ~/.ssh/id_ed25519 -p 2222 localhost
```

## Game Gateway Protocol

Games expose a WebSocket endpoint and receive a `hello` message:

```json
{
  "type": "hello",
  "protocol": "ggp.cell.v1",
  "sessionId": "sess_...",
  "roomId": "cell-garden:lobby",
  "player": {
    "id": "...",
    "name": "ryan",
    "sshKeyFingerprint": "SHA256:..."
  },
  "viewport": { "cols": 96, "rows": 32 },
  "capabilities": ["render.cell.v1", "input.keyboard.v1", "chat.bridge.v1"]
}
```

Games respond with `ready`, then send `frame` messages. Frames can be full snapshots or patches:

```json
{
  "type": "frame",
  "seq": 1,
  "mode": "patch",
  "cells": [
    { "x": 10, "y": 4, "ch": "@", "fg": "#7dd3fc", "bg": "#020617", "attrs": ["bold"] }
  ]
}
```

The gateway forwards keyboard input:

```json
{ "type": "input", "kind": "key", "key": "left" }
```

And resize events:

```json
{ "type": "resize", "cols": 88, "rows": 28 }
```

## Project Layout

- `cmd/gateway`: SSH gateway server.
- `cmd/sample-game`: reference WebSocket RPG village endpoint.
- `internal/ggp`: protocol types and Go client.
- `internal/ui`: Bubble Tea gateway UI and cell renderer.
- `internal/store`: Postgres schema and queries.
- `internal/chat`: in-process chat fanout.

## Environment

| Variable | Default | Purpose |
| --- | --- | --- |
| `GATEWAY_HOST` | `0.0.0.0` | SSH bind host |
| `GATEWAY_PORT` | `2222` | SSH bind port |
| `HOST_KEY_PATH` | `.ssh/id_ed25519` | SSH host key path |
| `DATABASE_URL` | required in `.env` | Gateway database |
| `SAMPLE_GAME_URL` | `ws://localhost:8081/ggp` | Seeded sample game endpoint |

## Next Protocol Work

- Add a language-neutral spec file under `docs/ggp-cell-v1.md`.
- Add room-scoped game state APIs backed by Postgres.
- Add binary frame patches for high-FPS games.
- Add an ANSI compatibility adapter later for existing terminal games.
