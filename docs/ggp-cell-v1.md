# Game Gateway Protocol: `ggp.cell.v1`

`ggp.cell.v1` is a language-neutral WebSocket protocol for games rendered inside the SSH gateway TUI.

Game servers render directly in terminal cells. One protocol cell maps to exactly one terminal cell in the gateway game pane. The gateway clips frames to the pane, applies terminal-supported styles, and reserves surrounding UI space for navigation and chat.

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
  "capabilities": ["render.cell.v1", "input.keyboard.v1", "chat.bridge.v1"]
}
```

Game replies with `ready`:

```json
{
  "type": "ready",
  "title": "Meadow Village",
  "targetFps": 8,
  "capabilities": ["render.cell.v1", "input.keyboard.v1"]
}
```

After `ready`, the gateway sends `resize` with the current terminal-cell viewport. All future resize events use the same direct terminal-cell coordinate space.

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

## Input

The gateway forwards keyboard input:

```json
{ "type": "input", "kind": "key", "key": "left" }
```

Resize events use terminal-cell dimensions:

```json
{ "type": "resize", "cols": 80, "rows": 24 }
```
