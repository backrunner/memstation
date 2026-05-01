#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "$0")/../.." && pwd)
MANAGER_PORT=${MEMSTATION_MANAGER_DEV_PORT:-5173}
MANAGER_PROXY_TARGET=${MEMSTATION_MANAGER_DEV_PROXY_TARGET:-http://127.0.0.1:8080}
SKIP_INSTALL=${MEMSTATION_SKIP_PNPM_INSTALL:-0}
SKIP_MIGRATE=${MEMSTATION_SKIP_DEV_MIGRATE:-0}
SERVER_SCRIPT="$ROOT_DIR/scripts/dev/run-server-watch.sh"
SERVER_PID=""
MANAGER_PID=""
WAIT_STATUS=0

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "required command not found: $1" >&2
    exit 1
  fi
}

cleanup() {
  local pid
  for pid in "$SERVER_PID" "$MANAGER_PID"; do
    if [[ -n "$pid" ]] && kill -0 "$pid" >/dev/null 2>&1; then
      kill "$pid" >/dev/null 2>&1 || true
      wait "$pid" >/dev/null 2>&1 || true
    fi
  done
}

trap cleanup EXIT INT TERM

require_command go
require_command node

if ! command -v pnpm >/dev/null 2>&1; then
  if ! command -v corepack >/dev/null 2>&1; then
    echo "pnpm is not available and corepack could not be found" >&2
    exit 1
  fi
  corepack enable >/dev/null 2>&1
  corepack prepare pnpm@10.32.1 --activate >/dev/null 2>&1
fi

if [[ "$SKIP_INSTALL" != "1" ]]; then
  echo "Ensuring workspace dependencies are installed..."
  (
    cd "$ROOT_DIR"
    pnpm install --frozen-lockfile=false
  )
fi

if [[ "$SKIP_MIGRATE" != "1" ]]; then
  echo "Applying latest database migrations before starting debug services..."
  (
    cd "$ROOT_DIR/apps/server"
    go run ./cmd/memstation migrate
  )
fi

echo "Starting MemStation backend watch..."
bash "$SERVER_SCRIPT" &
SERVER_PID=$!

echo "Starting MemStation manager debug server on port ${MANAGER_PORT}..."
(
  cd "$ROOT_DIR"
  export MEMSTATION_MANAGER_DEV_PROXY_TARGET="$MANAGER_PROXY_TARGET"
  pnpm --filter @memstation/manager dev -- --host 0.0.0.0 --port "$MANAGER_PORT"
) &
MANAGER_PID=$!

echo "Full-stack debug is running."
echo "- Server:  http://127.0.0.1:8080"
echo "- Manager: http://127.0.0.1:${MANAGER_PORT}"
echo "- API proxy target: ${MANAGER_PROXY_TARGET}"

set +e
wait -n "$SERVER_PID" "$MANAGER_PID"
WAIT_STATUS=$?
set -e

exit "$WAIT_STATUS"
