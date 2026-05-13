# GGP Go Hello Game

Minimal Docker-packaged `ggp.cell.v1` game.

## Run Locally

```sh
go run .
```

The game listens on `PORT` or `8081` and exposes:

- `GET /healthz`
- `GET /ggp` WebSocket

## Build And Push

Use a pinned, non-`latest` tag when submitting.

```sh
docker build -t docker.io/YOURNAME/ggp-hello:0.1.0 .
docker push docker.io/YOURNAME/ggp-hello:0.1.0
```

Submit this image ref in the gateway:

```text
docker.io/YOURNAME/ggp-hello:0.1.0
```

Use container port `8081`.

## Multiplayer

For multiplayer games, read `GGP_SESSION_SECRET` and validate `hello.auth.token` before trusting player or room fields. This minimal example is single-player per connection.
