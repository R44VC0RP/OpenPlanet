# Agent Notes

## Commands

- Verify Go changes with `go test ./...`.
- Validate Compose without leaking `.env` by running `docker compose config --quiet`.
- Build containers without applying DB migrations: `docker compose build gateway sample-game`.
- Start/restart locally: `docker compose up -d --build gateway sample-game`; then connect with `ssh -p 2222 localhost`.
- Deploy with `./deploy.sh`; it SSHes to `root@96.126.126.111` on port `2222`, builds `linux/amd64` images by default, loads them on the server, and exposes the gateway on server port `22`.
- If localhost SSH host keys change after container recreation, fix with `ssh-keygen -R "[localhost]:2222"`.

## Environment And Database

- `DATABASE_URL` is required and comes from `.env`; do not commit `.env`.
- `deploy.sh` expects `/opt/gamegateway/.env` on the server by default and normalizes it to `/opt/gamegateway/docker.env`; only set `UPLOAD_ENV=1` when intentionally copying local `.env` to the server.
- The configured DB is managed Postgres, usually PlanetScale Postgres. There is no active local Postgres service in `docker-compose.yml`.
- `cmd/gateway` runs migrations on startup via `internal/store`; restarting the gateway can apply schema changes to the managed DB.
- `opencode.json` configures the PlanetScale MCP server for DB debugging.

## Architecture

- `cmd/gateway` is the Wish SSH server and Bubble Tea app entrypoint.
- `cmd/sample-game` is only a reference external game endpoint; it communicates with the gateway over WebSocket using `ggp.cell.v1`.
- `internal/ggp` owns the protocol structs/client. Keep protocol changes documented in `docs/ggp-cell-v1.md`.
- `internal/ui/surface.go` maps logical game cells to terminal cells. Games can request `terminal`, `square-wide`, or `square-half` rendering in `ready.render`.
- For tile games, prefer `cellAspect: "square-wide"`; games send logical square tiles and the gateway doubles columns to compensate for terminal cell proportions.

## Product Rules

- SSH auth accepts any public key; the key fingerprint is identity, not authorization.
- Player names are `varchar(12)`, unique case-insensitively, and must match `^[A-Za-z0-9]{1,12}$`.
- New players should see the pre-lobby name confirmation screen before the game list.
