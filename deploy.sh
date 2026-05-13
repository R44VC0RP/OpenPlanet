#!/usr/bin/env bash
set -euo pipefail

REMOTE_HOST="${REMOTE_HOST:-96.126.126.111}"
REMOTE_USER="${REMOTE_USER:-root}"
REMOTE_SSH_PORT="${REMOTE_SSH_PORT:-2222}"
REMOTE_DIR="${REMOTE_DIR:-/opt/gamegateway}"
REMOTE_ENV="${REMOTE_ENV:-$REMOTE_DIR/.env}"
REMOTE_DOCKER_ENV="${REMOTE_DOCKER_ENV:-$REMOTE_DIR/docker.env}"
REMOTE_SSH_KEY_DIR="${REMOTE_SSH_KEY_DIR:-$REMOTE_DIR/ssh}"
REMOTE_CADDY_DATA_DIR="${REMOTE_CADDY_DATA_DIR:-$REMOTE_DIR/caddy-data}"
REMOTE_CADDY_CONFIG_DIR="${REMOTE_CADDY_CONFIG_DIR:-$REMOTE_DIR/caddy-config}"
UPLOAD_ENV="${UPLOAD_ENV:-0}"

GATEWAY_IMAGE="${GATEWAY_IMAGE:-gamegateway-gateway:deploy}"
SAMPLE_GAME_IMAGE="${SAMPLE_GAME_IMAGE:-gamegateway-sample-game:deploy}"
BLOBFIELD_IMAGE="${BLOBFIELD_IMAGE:-gamegateway-blobfield:deploy}"
SPEC_SITE_IMAGE="${SPEC_SITE_IMAGE:-gamegateway-spec-site:deploy}"
PLATFORM="${PLATFORM:-linux/amd64}"
GATEWAY_CONTAINER="${GATEWAY_CONTAINER:-gamegateway-gateway}"
SAMPLE_GAME_CONTAINER="${SAMPLE_GAME_CONTAINER:-gamegateway-sample-game}"
BLOBFIELD_CONTAINER="${BLOBFIELD_CONTAINER:-gamegateway-blobfield}"
SPEC_SITE_CONTAINER="${SPEC_SITE_CONTAINER:-gamegateway-spec-site}"
NETWORK="${NETWORK:-gamegateway}"
GATEWAY_HOST_KEY_PATH="${GATEWAY_HOST_KEY_PATH:-/var/lib/gamegateway/ssh/id_ed25519}"
SPEC_SITE_DOMAIN="${SPEC_SITE_DOMAIN:-openplanet.ryan.ceo}"
ACME_EMAIL="${ACME_EMAIL:-ssl@ryan.ceo}"

SSH=(ssh -p "$REMOTE_SSH_PORT" "$REMOTE_USER@$REMOTE_HOST")
SCP=(scp -P "$REMOTE_SSH_PORT")

echo "==> Building Docker images locally for $PLATFORM"
docker build --platform "$PLATFORM" -f Dockerfile.gateway -t "$GATEWAY_IMAGE" .
docker build --platform "$PLATFORM" -f Dockerfile.sample-game -t "$SAMPLE_GAME_IMAGE" .
docker build --platform "$PLATFORM" -f Dockerfile.blobfield -t "$BLOBFIELD_IMAGE" .
docker build --platform "$PLATFORM" -f Dockerfile.spec-site -t "$SPEC_SITE_IMAGE" .

echo "==> Preparing remote directory"
"${SSH[@]}" "mkdir -p '$REMOTE_DIR' '$REMOTE_SSH_KEY_DIR' '$REMOTE_CADDY_DATA_DIR' '$REMOTE_CADDY_CONFIG_DIR' && chown -R 10001:10001 '$REMOTE_SSH_KEY_DIR' && chmod 700 '$REMOTE_SSH_KEY_DIR'"

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
"${SSH[@]}" "set -a; . '$REMOTE_ENV'; set +a; test -n \"\${DATABASE_URL:-}\" || (echo 'DATABASE_URL is missing in $REMOTE_ENV' >&2; exit 1); umask 077; { printf 'DATABASE_URL=%s\n' \"\$DATABASE_URL\"; if test -n \"\${GGP_SESSION_SECRET:-}\"; then printf 'GGP_SESSION_SECRET=%s\n' \"\$GGP_SESSION_SECRET\"; fi; if test -n \"\${GGP_ISSUER:-}\"; then printf 'GGP_ISSUER=%s\n' \"\$GGP_ISSUER\"; fi; } > '$REMOTE_DOCKER_ENV'"

echo "==> Uploading Docker images"
docker save "$GATEWAY_IMAGE" "$SAMPLE_GAME_IMAGE" "$BLOBFIELD_IMAGE" "$SPEC_SITE_IMAGE" | gzip | "${SSH[@]}" "cat > '$REMOTE_DIR/images.tar.gz'"

echo "==> Preserving gateway SSH host key"
"${SSH[@]}" "set -e; mkdir -p '$REMOTE_SSH_KEY_DIR'; if test ! -f '$REMOTE_SSH_KEY_DIR/id_ed25519'; then if docker cp '$GATEWAY_CONTAINER:$GATEWAY_HOST_KEY_PATH' '$REMOTE_SSH_KEY_DIR/id_ed25519' >/dev/null 2>&1; then true; elif docker cp '$GATEWAY_CONTAINER:/tmp/gamegateway/id_ed25519' '$REMOTE_SSH_KEY_DIR/id_ed25519' >/dev/null 2>&1; then true; fi; fi; chown -R 10001:10001 '$REMOTE_SSH_KEY_DIR'; chmod 700 '$REMOTE_SSH_KEY_DIR'; if test -f '$REMOTE_SSH_KEY_DIR/id_ed25519'; then chmod 600 '$REMOTE_SSH_KEY_DIR/id_ed25519'; fi"

echo "==> Stopping existing containers"
"${SSH[@]}" "docker rm -f '$GATEWAY_CONTAINER' '$SAMPLE_GAME_CONTAINER' '$BLOBFIELD_CONTAINER' '$SPEC_SITE_CONTAINER' gamegateway-tetris >/dev/null 2>&1 || true"

echo "==> Loading new images"
"${SSH[@]}" "gunzip -c '$REMOTE_DIR/images.tar.gz' | docker load && rm -f '$REMOTE_DIR/images.tar.gz'"

echo "==> Ensuring Docker network exists"
"${SSH[@]}" "docker network inspect '$NETWORK' >/dev/null 2>&1 || docker network create '$NETWORK' >/dev/null"

echo "==> Starting approved and pending image games"
"${SSH[@]}" "docker run --rm --env-file '$REMOTE_DOCKER_ENV' '$GATEWAY_IMAGE' list-image-games > '$REMOTE_DIR/image-games.tsv'"
"${SSH[@]}" "docker ps -aq --filter label=gamegateway.dynamic-game=true | xargs -r docker rm -f >/dev/null"
"${SSH[@]}" "bash -lc 'set -e; while IFS=\$'\''\t'\'' read -r game_id image_ref container_port session_secret game_status; do \
  test -n \"\$game_id\" || continue; \
  test -n \"\$image_ref\" || continue; \
  container=gamegateway-game-\$game_id; \
  if test \"\$session_secret\" = '-'; then session_secret=''; fi; \
  echo \"pulling \$image_ref\"; \
  docker pull \"\$image_ref\"; \
  docker run -d \
    --name \"\$container\" \
    --restart unless-stopped \
    --network '$NETWORK' \
    --label gamegateway.dynamic-game=true \
    --label gamegateway.game-id=\"\$game_id\" \
    --label gamegateway.game-status=\"\$game_status\" \
    -e PORT=\"\$container_port\" \
    -e GGP_GAME_ID=\"\$game_id\" \
    -e GGP_ENDPOINT_URL=ws://\$container:\$container_port/ggp \
    -e GGP_SESSION_SECRET=\"\$session_secret\" \
    \"\$image_ref\"; \
done < '\''$REMOTE_DIR/image-games.tsv'\'''"

echo "==> Starting sample game endpoint"
"${SSH[@]}" "docker run -d \
  --name '$SAMPLE_GAME_CONTAINER' \
  --restart unless-stopped \
  --network '$NETWORK' \
  --env-file '$REMOTE_DOCKER_ENV' \
  -e PORT=8081 \
  -e GGP_ENDPOINT_URL=ws://$SAMPLE_GAME_CONTAINER:8081/ggp \
  '$SAMPLE_GAME_IMAGE'"

echo "==> Starting Blobfield endpoint"
"${SSH[@]}" "docker run -d \
  --name '$BLOBFIELD_CONTAINER' \
  --restart unless-stopped \
  --network '$NETWORK' \
  --env-file '$REMOTE_DOCKER_ENV' \
  -e PORT=8082 \
  -e GGP_ENDPOINT_URL=ws://$BLOBFIELD_CONTAINER:8082/ggp \
  '$BLOBFIELD_IMAGE'"

echo "==> Starting gateway on host port 22"
"${SSH[@]}" "docker run -d \
  --name '$GATEWAY_CONTAINER' \
  --restart unless-stopped \
  --network '$NETWORK' \
  --env-file '$REMOTE_DOCKER_ENV' \
  -v '$REMOTE_SSH_KEY_DIR:/var/lib/gamegateway/ssh' \
  -e GATEWAY_HOST=0.0.0.0 \
  -e GATEWAY_PORT=2222 \
  -e HOST_KEY_PATH='$GATEWAY_HOST_KEY_PATH' \
  -e SAMPLE_GAME_URL=ws://$SAMPLE_GAME_CONTAINER:8081/ggp \
  -e BLOBFIELD_GAME_URL=ws://$BLOBFIELD_CONTAINER:8082/ggp \
  -p 22:2222 \
  '$GATEWAY_IMAGE'"

echo "==> Starting GGP spec site on HTTPS"
"${SSH[@]}" "docker run -d \
  --name '$SPEC_SITE_CONTAINER' \
  --restart unless-stopped \
  --network '$NETWORK' \
  -e SITE_DOMAIN='$SPEC_SITE_DOMAIN' \
  -e ACME_EMAIL='$ACME_EMAIL' \
  -v '$REMOTE_CADDY_DATA_DIR:/data' \
  -v '$REMOTE_CADDY_CONFIG_DIR:/config' \
  -p 80:80 \
  -p 443:443 \
  '$SPEC_SITE_IMAGE'"

echo "==> Deployment complete"
echo "Gateway should be reachable with: ssh $REMOTE_HOST"
echo "GGP spec should be reachable at: https://$SPEC_SITE_DOMAIN"
echo "Server admin SSH remains: ssh -p $REMOTE_SSH_PORT $REMOTE_USER@$REMOTE_HOST"
