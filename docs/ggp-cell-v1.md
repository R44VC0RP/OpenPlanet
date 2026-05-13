# Game Gateway Protocol: `ggp.cell.v1`

`ggp.cell.v1` is a language-neutral WebSocket protocol for games rendered inside the SSH gateway TUI.

The game endpoint sends a logical grid of styled cells. The gateway maps that logical grid into terminal cells, handles terminal aspect ratio, clips it to the game pane, and renders it with the TUI renderer.

## Handshake

Gateway sends `hello` with the terminal-space viewport currently available for the game pane:

```json
{
  "type": "hello",
  "protocol": "ggp.cell.v1",
  "sessionId": "sess_...",
  "roomId": "game:lobby",
  "player": { "id": "...", "name": "ryan", "sshKeyFingerprint": "SHA256:..." },
  "viewport": { "cols": 120, "rows": 36 },
  "capabilities": ["render.cell.v1", "render.square.v1", "input.keyboard.v1", "chat.bridge.v1"]
}
```

Game replies with `ready` and chooses a render mapping:

```json
{
  "type": "ready",
  "title": "Meadow Village",
  "targetFps": 8,
  "capabilities": ["render.cell.v1", "render.square.v1", "input.keyboard.v1"],
  "render": {
    "mode": "cells",
    "cellAspect": "square-wide"
  }
}
```

After `ready`, the gateway sends `resize` using the logical viewport size for the chosen render mapping.

## Render Modes

### `cellAspect: "terminal"`

One game cell maps to one terminal cell.

Use this for text-heavy TUIs, roguelikes that expect terminal cell proportions, menus, dashboards, and games that intentionally want the classic terminal look.

```text
logical 80x24 -> terminal 80x24
```

### `cellAspect: "square-wide"`

One logical game tile maps to two terminal columns and one terminal row.

This approximates square tiles on terminals where cells are roughly twice as tall as they are wide. This is the best default for tile games, RPG maps, board games, and colored block worlds.

```text
logical 60x24 -> terminal 120x24
```

Games send normal cell frames in logical coordinates. The gateway doubles each tile horizontally.

### `cellAspect: "square-half"`

Two logical vertical pixels map into one terminal cell using Unicode half-block rendering.

This is for higher-resolution pixel art or simulations. It works best for color-only pixels and is less suitable for text overlays.

```text
logical 80x48 -> terminal 80x24
```

The gateway packs top and bottom logical cells into a single terminal cell using foreground/background color.

## Frames

Frames are full snapshots or patches in logical coordinates:

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
| `x`, `y` | Logical grid coordinates |
| `ch` | Single display glyph; use a space for colored tile blocks |
| `fg` | Foreground color, any terminal-supported color string accepted by the gateway renderer |
| `bg` | Background color |
| `attrs` | Optional attributes, currently `bold` |

Use `status` for player-facing text that should be rendered by the gateway outside the tile grid. This keeps tile maps square and avoids text being stretched in `square-wide` mode.

## Input

The gateway forwards keyboard input:

```json
{ "type": "input", "kind": "key", "key": "left" }
```

Resize events use logical dimensions after the selected render mapping:

```json
{ "type": "resize", "cols": 60, "rows": 24 }
```
