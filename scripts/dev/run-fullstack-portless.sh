#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "$0")/../.." && pwd)
COMPOSE_FILE="$ROOT_DIR/deploy/compose/docker-compose.yml"
PROJECT_NAME=${MEMSTATION_COMPOSE_PROJECT:-memstation-dev}
ENV_FILE=${MEMSTATION_ENV_FILE:-"$ROOT_DIR/.env.memstation-dev"}
SERVER_SCRIPT="$ROOT_DIR/scripts/dev/run-server-watch.sh"
MANAGER_SCRIPT="$ROOT_DIR/scripts/dev/run-manager-portless.sh"
SKIP_INSTALL=${MEMSTATION_SKIP_PNPM_INSTALL:-0}
SKIP_MIGRATE=${MEMSTATION_SKIP_DEV_MIGRATE:-0}
DEV_INFRA=${MEMSTATION_DEV_INFRA:-auto}
READY_TIMEOUT=${MEMSTATION_SERVER_READY_TIMEOUT_SECONDS:-90}
SERVER_PID=""
MANAGER_PID=""

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "required command not found: $1" >&2
    exit 1
  fi
}

load_env_defaults() {
  if [[ ! -f "$ENV_FILE" ]]; then
    return
  fi

  while IFS='=' read -r key value || [[ -n "${key:-}" ]]; do
    if [[ ! "$key" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]]; then
      continue
    fi
    if [[ -z "${!key:-}" ]]; then
      export "$key=$value"
    fi
  done <"$ENV_FILE"
}

ensure_pnpm() {
  if command -v pnpm >/dev/null 2>&1; then
    return
  fi
  if ! command -v corepack >/dev/null 2>&1; then
    echo "pnpm is not available and corepack could not be found" >&2
    exit 1
  fi
  corepack enable >/dev/null 2>&1
  corepack prepare pnpm@10.32.1 --activate >/dev/null 2>&1
}

docker_ready() {
  command -v docker >/dev/null 2>&1 \
    && docker compose version >/dev/null 2>&1 \
    && docker info >/dev/null 2>&1
}

compose() {
  local args=(docker compose --project-name "$PROJECT_NAME")
  if [[ -f "$ENV_FILE" ]]; then
    args+=(--env-file "$ENV_FILE")
  fi
  args+=(-f "$COMPOSE_FILE")
  "${args[@]}" "$@"
}

wait_for_healthy_service() {
  local service=$1
  local timeout=${2:-120}
  local start_ts
  start_ts=$(date +%s)

  while true; do
    local container_id
    container_id=$(compose ps -q "$service")
    if [[ -n "$container_id" ]]; then
      local health
      health=$(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "$container_id" 2>/dev/null || true)
      if [[ "$health" == "healthy" || "$health" == "running" ]]; then
        return 0
      fi
    fi
    if (( $(date +%s) - start_ts >= timeout )); then
      echo "timed out waiting for service $service to become healthy" >&2
      compose ps >&2 || true
      exit 1
    fi
    sleep 2
  done
}

maybe_start_dev_infra() {
  case "$DEV_INFRA" in
    0|false|False|FALSE)
      echo "Skipping Docker dev infra startup."
      return
      ;;
    1|true|True|TRUE)
      require_command docker
      ;;
    auto)
      if ! docker_ready; then
        echo "Docker dev infra is not available; assuming PostgreSQL and Redis are already reachable."
        return
      fi
      ;;
    *)
      echo "MEMSTATION_DEV_INFRA must be auto, 1, or 0" >&2
      exit 1
      ;;
  esac

  echo "Ensuring local PostgreSQL and Redis are running..."
  compose up -d postgres redis
  compose stop memstation manager worker scheduler >/dev/null 2>&1 || true
  wait_for_healthy_service postgres 120
  wait_for_healthy_service redis 120
}

wait_for_http() {
  local url=$1
  local timeout=$2
  local start_ts
  start_ts=$(date +%s)

  while true; do
    if curl -fsS "$url" >/dev/null 2>&1; then
      return
    fi
    if (( $(date +%s) - start_ts >= timeout )); then
      echo "timed out waiting for $url" >&2
      exit 1
    fi
    sleep 1
  done
}

cleanup() {
  local pid
  for pid in "$MANAGER_PID" "$SERVER_PID"; do
    if [[ -n "$pid" ]] && kill -0 "$pid" >/dev/null 2>&1; then
      kill "$pid" >/dev/null 2>&1 || true
      wait "$pid" >/dev/null 2>&1 || true
    fi
  done
}

monitor_children() {
  while true; do
    if [[ -n "$SERVER_PID" ]] && ! kill -0 "$SERVER_PID" >/dev/null 2>&1; then
      wait "$SERVER_PID"
      exit $?
    fi
    if [[ -n "$MANAGER_PID" ]] && ! kill -0 "$MANAGER_PID" >/dev/null 2>&1; then
      wait "$MANAGER_PID"
      exit $?
    fi
    sleep 1
  done
}

trap cleanup EXIT INT TERM

load_env_defaults

export MEMSTATION_PORT=${MEMSTATION_PORT:-8080}
export DATABASE_URL=${DATABASE_URL:-postgres://memstation:memstation_dev@localhost:5432/memstation?sslmode=disable}
export REDIS_URL=${REDIS_URL:-redis://localhost:6379/0}
export VECTOR_STORE_BACKEND=${VECTOR_STORE_BACKEND:-pgvector}
export MEMSTATION_ADMIN_TOKEN=${MEMSTATION_ADMIN_TOKEN:-memstation-local-admin-token}
export MEMSTATION_MASTER_KEY=${MEMSTATION_MASTER_KEY:-memstation-local-master-key}
export MEMSTATION_DEV_API_KEY=${MEMSTATION_DEV_API_KEY:-ms-local-dev}
export MEMSTATION_MANAGER_PORTLESS_NAME=${MEMSTATION_MANAGER_PORTLESS_NAME:-memstation-manager}
export MEMSTATION_MANAGER_DEV_PROXY_TARGET=${MEMSTATION_MANAGER_DEV_PROXY_TARGET:-http://127.0.0.1:${MEMSTATION_PORT}}
export PUBLIC_MEMSTATION_DEFAULT_TARGET=${PUBLIC_MEMSTATION_DEFAULT_TARGET:-https://${MEMSTATION_MANAGER_PORTLESS_NAME}.localhost}
export MEMSTATION_CORS_ALLOWED_ORIGINS=${MEMSTATION_CORS_ALLOWED_ORIGINS:-}
export MEMSTATION_SESSION_COOKIE_SECURE=${MEMSTATION_SESSION_COOKIE_SECURE:-false}
export MEMSTATION_SESSION_COOKIE_CROSS_SITE=${MEMSTATION_SESSION_COOKIE_CROSS_SITE:-false}

require_command go
require_command node
require_command curl
ensure_pnpm

if [[ "$SKIP_INSTALL" != "1" ]]; then
  echo "Ensuring workspace dependencies are installed..."
  (
    cd "$ROOT_DIR"
    pnpm install --frozen-lockfile=false
  )
fi

maybe_start_dev_infra

if [[ "$SKIP_MIGRATE" != "1" ]]; then
  echo "Applying latest database migrations before starting debug services..."
  (
    cd "$ROOT_DIR/apps/server"
    go run ./cmd/memstation migrate
  )
fi

echo "Starting MemStation backend watch on ${MEMSTATION_MANAGER_DEV_PROXY_TARGET}..."
bash "$SERVER_SCRIPT" &
SERVER_PID=$!

wait_for_http "${MEMSTATION_MANAGER_DEV_PROXY_TARGET}/health/live" "$READY_TIMEOUT"

echo "Starting MemStation manager through Portless..."
bash "$MANAGER_SCRIPT" &
MANAGER_PID=$!

echo "Portless full-stack debug is running."
echo "- Server:  ${MEMSTATION_MANAGER_DEV_PROXY_TARGET}"
echo "- Manager: ${PUBLIC_MEMSTATION_DEFAULT_TARGET}"
echo "- API proxy target: ${MEMSTATION_MANAGER_DEV_PROXY_TARGET}"
echo
echo "Use Ctrl-C to stop the manager and backend watcher."

monitor_children
