#!/usr/bin/env bash
set -euo pipefail

REPO_URL="${REPO_URL:-https://github.com/zhangdaozhu/new-api.git}"
BRANCH="${BRANCH:-main}"
APP_DIR="${APP_DIR:-/opt/new-api}"
COMPOSE_FILE="docker-compose.server.yml"
ENV_FILE=".env.server"

need_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "Missing required command: $1"
    exit 1
  fi
}

need_cmd git
need_cmd docker

if ! docker compose version >/dev/null 2>&1; then
  echo "Docker Compose plugin is required (docker compose)."
  exit 1
fi

if [ ! -d "$APP_DIR/.git" ]; then
  mkdir -p "$(dirname "$APP_DIR")"
  git clone --branch "$BRANCH" "$REPO_URL" "$APP_DIR"
else
  git -C "$APP_DIR" fetch --all --prune
  git -C "$APP_DIR" checkout "$BRANCH"
  git -C "$APP_DIR" pull --ff-only origin "$BRANCH"
fi

cd "$APP_DIR"

if [ ! -f "$COMPOSE_FILE" ]; then
  echo "Missing $COMPOSE_FILE in repository."
  exit 1
fi

if [ ! -f "$ENV_FILE" ]; then
  if [ -f ".env.server.example" ]; then
    cp .env.server.example "$ENV_FILE"
  else
    echo "Missing .env.server.example; please create $ENV_FILE manually."
    exit 1
  fi
fi

rand_secret() {
  tr -dc 'A-Za-z0-9' </dev/urandom | head -c 32
}

if grep -q "change-me-mysql" "$ENV_FILE"; then
  MYSQL_PASSWORD="$(rand_secret)"
  sed -i.bak "s/change-me-mysql/${MYSQL_PASSWORD}/g" "$ENV_FILE"
  rm -f "${ENV_FILE}.bak"
fi

if grep -q "change-me-session" "$ENV_FILE"; then
  SESSION_SECRET_VALUE="$(rand_secret)"
  sed -i.bak "s/change-me-session/${SESSION_SECRET_VALUE}/g" "$ENV_FILE"
  rm -f "${ENV_FILE}.bak"
fi

docker compose -f "$COMPOSE_FILE" build --pull new-api
docker compose -f "$COMPOSE_FILE" up -d
docker compose -f "$COMPOSE_FILE" ps

echo "Deployment completed. Visit: http://<server-ip>:3000"
