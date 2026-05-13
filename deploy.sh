#!/usr/bin/env bash
set -euo pipefail

REMOTE_HOST="${REMOTE_HOST:-96.126.126.111}"
REMOTE_USER="${REMOTE_USER:-root}"
REMOTE_SSH_PORT="${REMOTE_SSH_PORT:-2222}"
REMOTE_DIR="${REMOTE_DIR:-/opt/gamegateway}"
REMOTE_ENV="${REMOTE_ENV:-$REMOTE_DIR/.env}"
REMOTE_DOCKER_ENV="${REMOTE_DOCKER_ENV:-$REMOTE_DIR/docker.env}"
UPLOAD_ENV="${UPLOAD_ENV:-0}"

GATEWAY_IMAGE="${GATEWAY_IMAGE:-gamegateway-gateway:deploy}"
SAMPLE_GAME_IMAGE="${SAMPLE_GAME_IMAGE:-gamegateway-sample-game:deploy}"
PLATFORM="${PLATFORM:-linux/amd64}"
GATEWAY_CONTAINER="${GATEWAY_CONTAINER:-gamegateway-gateway}"
SAMPLE_GAME_CONTAINER="${SAMPLE_GAME_CONTAINER:-gamegateway-sample-game}"
NETWORK="${NETWORK:-gamegateway}"

SSH=(ssh -p "$REMOTE_SSH_PORT" "$REMOTE_USER@$REMOTE_HOST")
SCP=(scp -P "$REMOTE_SSH_PORT")

echo "==> Building Docker images locally for $PLATFORM"
docker build --platform "$PLATFORM" -f Dockerfile.gateway -t "$GATEWAY_IMAGE" .
docker build --platform "$PLATFORM" -f Dockerfile.sample-game -t "$SAMPLE_GAME_IMAGE" .

echo "==> Preparing remote directory"
"${SSH[@]}" "mkdir -p '$REMOTE_DIR'"

if [[ "$UPLOAD_ENV" == "1" ]]; then
  if [[ ! -f .env ]]; then
    echo "UPLOAD_ENV=1 was set, but local .env does not exist" >&2
    exit 1
  fi
  echo "==> Uploading .env to $REMOTE_ENV"
  "${SCP[@]}" .env "$REMOTE_USER@$REMOTE_HOST:$REMOTE_ENV"
else
  echo "==> Verifying remote env file exists at $REMOTE_ENV"
  "${SSH[@]}" "test -f '$REMOTE_ENV' || (echo 'Missing $REMOTE_ENV. Create it with DATABASE_URL=... or rerun with UPLOAD_ENV=1.' >&2; exit 1)"
fi

echo "==> Normalizing remote env for Docker"
"${SSH[@]}" "set -a; . '$REMOTE_ENV'; set +a; test -n \"\${DATABASE_URL:-}\" || (echo 'DATABASE_URL is missing in $REMOTE_ENV' >&2; exit 1); umask 077; printf 'DATABASE_URL=%s\n' \"\$DATABASE_URL\" > '$REMOTE_DOCKER_ENV'"

echo "==> Uploading Docker images"
docker save "$GATEWAY_IMAGE" "$SAMPLE_GAME_IMAGE" | gzip | "${SSH[@]}" "cat > '$REMOTE_DIR/images.tar.gz'"

echo "==> Stopping existing containers"
"${SSH[@]}" "docker rm -f '$GATEWAY_CONTAINER' '$SAMPLE_GAME_CONTAINER' >/dev/null 2>&1 || true"

echo "==> Loading new images"
"${SSH[@]}" "gunzip -c '$REMOTE_DIR/images.tar.gz' | docker load && rm -f '$REMOTE_DIR/images.tar.gz'"

echo "==> Ensuring Docker network exists"
"${SSH[@]}" "docker network inspect '$NETWORK' >/dev/null 2>&1 || docker network create '$NETWORK' >/dev/null"

echo "==> Starting sample game endpoint"
"${SSH[@]}" "docker run -d \
  --name '$SAMPLE_GAME_CONTAINER' \
  --restart unless-stopped \
  --network '$NETWORK' \
  -e PORT=8081 \
  '$SAMPLE_GAME_IMAGE'"

echo "==> Starting gateway on host port 22"
"${SSH[@]}" "docker run -d \
  --name '$GATEWAY_CONTAINER' \
  --restart unless-stopped \
  --network '$NETWORK' \
  --env-file '$REMOTE_DOCKER_ENV' \
  -e GATEWAY_HOST=0.0.0.0 \
  -e GATEWAY_PORT=2222 \
  -e HOST_KEY_PATH=/tmp/gamegateway/id_ed25519 \
  -e SAMPLE_GAME_URL=ws://$SAMPLE_GAME_CONTAINER:8081/ggp \
  -p 22:2222 \
  '$GATEWAY_IMAGE'"

echo "==> Deployment complete"
echo "Gateway should be reachable with: ssh $REMOTE_HOST"
echo "Server admin SSH remains: ssh -p $REMOTE_SSH_PORT $REMOTE_USER@$REMOTE_HOST"
