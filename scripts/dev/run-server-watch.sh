#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "$0")/../.." && pwd)
APP_DIR="$ROOT_DIR/apps/server"
WATCH_INTERVAL_SECONDS=${MEMSTATION_SERVER_WATCH_INTERVAL_SECONDS:-1}
SERVER_COMMAND=${MEMSTATION_SERVER_DEV_COMMAND:-go run ./cmd/memstation serve}
SERVER_PID=""
CURRENT_STATE=""

export MEMSTATION_PORT=${MEMSTATION_PORT:-8080}
export DATABASE_URL=${DATABASE_URL:-postgres://memstation:memstation_dev@postgres:5432/memstation?sslmode=disable}
export REDIS_URL=${REDIS_URL:-redis://redis:6379/0}
export VECTOR_STORE_BACKEND=${VECTOR_STORE_BACKEND:-pgvector}
export MEMSTATION_ADMIN_TOKEN=${MEMSTATION_ADMIN_TOKEN:-memstation-local-admin-token}
export MEMSTATION_MASTER_KEY=${MEMSTATION_MASTER_KEY:-memstation-local-master-key}
export MEMSTATION_DEV_API_KEY=${MEMSTATION_DEV_API_KEY:-ms-local-dev}
export MEMSTATION_CORS_ALLOWED_ORIGINS=${MEMSTATION_CORS_ALLOWED_ORIGINS:-http://127.0.0.1:5173,http://localhost:5173}
export MEMSTATION_SESSION_COOKIE_SECURE=${MEMSTATION_SESSION_COOKIE_SECURE:-false}
export MEMSTATION_SESSION_COOKIE_CROSS_SITE=${MEMSTATION_SESSION_COOKIE_CROSS_SITE:-false}

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "required command not found: $1" >&2
    exit 1
  fi
}

tracked_state() {
  find "$APP_DIR" \
    \( -path "$APP_DIR/ent" -o -path "$APP_DIR/ent/*" -o -path "$APP_DIR/graph/generated" -o -path "$APP_DIR/graph/generated/*" -o -path "$APP_DIR/dist" -o -path "$APP_DIR/dist/*" \) -prune \
    -o -type f \( -name '*.go' -o -name 'go.mod' -o -name 'go.sum' -o -name '.env' -o -name '.env.local' \) -print0 \
    | sort -z \
    | xargs -0 -r cksum \
    | cksum \
    | awk '{print $1}'
}

stop_server() {
  if [[ -n "$SERVER_PID" ]] && kill -0 "$SERVER_PID" >/dev/null 2>&1; then
    kill "$SERVER_PID" >/dev/null 2>&1 || true
    wait "$SERVER_PID" >/dev/null 2>&1 || true
  fi
  SERVER_PID=""
}

start_server() {
  echo "Launching MemStation server with watch support..."
  (
    cd "$APP_DIR"
    exec bash -lc "$SERVER_COMMAND"
  ) &
  SERVER_PID=$!
}

cleanup() {
  stop_server
}

trap cleanup EXIT INT TERM

require_command go
require_command find
require_command sort
require_command xargs
require_command cksum

CURRENT_STATE=$(tracked_state)
start_server

while true; do
  sleep "$WATCH_INTERVAL_SECONDS"

  if [[ -n "$SERVER_PID" ]] && ! kill -0 "$SERVER_PID" >/dev/null 2>&1; then
    echo "MemStation server exited; restarting..."
    start_server
  fi

  next_state=$(tracked_state)
  if [[ "$next_state" != "$CURRENT_STATE" ]]; then
    echo "Detected backend source changes; restarting server..."
    CURRENT_STATE=$next_state
    stop_server
    start_server
  fi
done
