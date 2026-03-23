#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "$0")/.." && pwd)
COMPOSE_FILE="$ROOT_DIR/deploy/compose/docker-compose.yml"
PROJECT_NAME=${MEMSTATION_COMPOSE_PROJECT:-memstation-dev}
ENV_FILE=${MEMSTATION_ENV_FILE:-"$ROOT_DIR/.env.memstation-dev"}
RUN_SEED=1
START_SERVICES=1
START_MANAGER=1

usage() {
  cat <<USAGE
Usage: $(basename "$0") [--env-file PATH] [--project-name NAME] [--no-seed] [--no-start] [--no-manager]

Resets the local Docker Compose development stack, removes Postgres/Redis volumes,
re-applies the fresh baseline migration, optionally seeds defaults, and starts the stack.
USAGE
}

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "required command not found: $1" >&2
    exit 1
  fi
}

random_hex() {
  local size=$1
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -hex "$size"
    return
  fi
  if command -v python3 >/dev/null 2>&1; then
    python3 - "$size" <<'PY'
import secrets
import sys

size = int(sys.argv[1])
print(secrets.token_hex(size))
PY
    return
  fi
  echo "openssl or python3 is required to generate local secrets" >&2
  exit 1
}

ensure_env_file() {
  if [[ -f "$ENV_FILE" ]]; then
    return
  fi
  mkdir -p "$(dirname "$ENV_FILE")"
  cat >"$ENV_FILE" <<EOF
MEMSTATION_ADMIN_TOKEN=ms-admin-$(random_hex 12)
MEMSTATION_MASTER_KEY=ms-master-$(random_hex 24)
MEMSTATION_DEV_API_KEY=ms-local-dev
EOF
  echo "Created local development env file at $ENV_FILE"
}

compose() {
  docker compose \
    --project-name "$PROJECT_NAME" \
    --env-file "$ENV_FILE" \
    -f "$COMPOSE_FILE" \
    "$@"
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

wait_for_http_ready() {
  local url=$1
  local timeout=${2:-120}
  if ! command -v curl >/dev/null 2>&1; then
    return
  fi
  local start_ts
  start_ts=$(date +%s)
  while true; do
    if curl -fsS "$url" >/dev/null 2>&1; then
      return 0
    fi
    if (( $(date +%s) - start_ts >= timeout )); then
      echo "timed out waiting for $url" >&2
      exit 1
    fi
    sleep 2
  done
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --env-file)
      ENV_FILE=$2
      shift 2
      ;;
    --project-name)
      PROJECT_NAME=$2
      shift 2
      ;;
    --no-seed)
      RUN_SEED=0
      shift
      ;;
    --no-start)
      START_SERVICES=0
      shift
      ;;
    --no-manager)
      START_MANAGER=0
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage >&2
      exit 1
      ;;
  esac
done

require_command docker
docker compose version >/dev/null 2>&1
ensure_env_file

echo "Resetting local MemStation dev stack..."
compose down --volumes --remove-orphans
compose up -d postgres redis
wait_for_healthy_service postgres 120
wait_for_healthy_service redis 120

echo "Applying migrations..."
compose run --rm memstation migrate

if [[ $RUN_SEED -eq 1 ]]; then
  echo "Seeding defaults..."
  compose run --rm memstation seed
fi

if [[ $START_SERVICES -eq 1 ]]; then
  services=(memstation worker scheduler)
  if [[ $START_MANAGER -eq 1 ]]; then
    services+=(manager)
  fi
  echo "Starting application services: ${services[*]}"
  compose up -d "${services[@]}"
  wait_for_http_ready "http://127.0.0.1:8080/health/ready" 120
fi

set -a
# shellcheck disable=SC1090
source "$ENV_FILE"
set +a

echo "MemStation dev reset complete."
echo
echo "- Compose project: $PROJECT_NAME"
echo "- Compose file: $COMPOSE_FILE"
echo "- Env file: $ENV_FILE"
if [[ $START_SERVICES -eq 1 ]]; then
  echo "- Server URL: http://127.0.0.1:8080"
  if [[ $START_MANAGER -eq 1 ]]; then
    echo "- Manager URL: http://127.0.0.1:4173"
  fi
fi
echo "- Dev gateway API key: ${MEMSTATION_DEV_API_KEY:-ms-local-dev}"
echo
echo "Admin token and master key are stored in $ENV_FILE."
