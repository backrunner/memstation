#!/usr/bin/env bash
# Starts the complete local benchmark stack. Commands: up [--service], down, status,
# newdb <name>. Settings come from the environment, then MEMSTATION_EVAL_ENV_FILE
# (default .env.memstation-eval). Only what this script started is ever stopped.
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "$0")/../.." && pwd)
STATE_DIR="$ROOT_DIR/.artifacts/eval-stack"
ENV_FILE=${MEMSTATION_EVAL_ENV_FILE:-"$ROOT_DIR/.env.memstation-eval"}
# shellcheck source=../dev/extractor-lib.sh
source "$ROOT_DIR/scripts/dev/extractor-lib.sh"

load_settings() {
  local key value
  if [[ -f "$ENV_FILE" ]]; then
    while IFS='=' read -r key value || [[ -n "${key:-}" ]]; do
      [[ "$key" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]] || continue
      if [[ -z "${!key:-}" ]]; then
        export "$key=$value"
      fi
    done <"$ENV_FILE"
  fi
  export MEMSTATION_EVAL_POSTGRES_CONTAINER=${MEMSTATION_EVAL_POSTGRES_CONTAINER:-memstation-eval-postgres}
  export MEMSTATION_EVAL_POSTGRES_PORT=${MEMSTATION_EVAL_POSTGRES_PORT:-55432}
  export MEMSTATION_EVAL_POSTGRES_PASSWORD=${MEMSTATION_EVAL_POSTGRES_PASSWORD:-memstation_eval}
  export MEMSTATION_EVAL_REDIS_CONTAINER=${MEMSTATION_EVAL_REDIS_CONTAINER:-memstation-eval-redis}
  export MEMSTATION_EVAL_REDIS_PORT=${MEMSTATION_EVAL_REDIS_PORT:-56379}
  export MEMSTATION_RERANK_PORT=${MEMSTATION_RERANK_PORT:-18082}
  export MEMSTATION_RERANK_GGUF=${MEMSTATION_RERANK_GGUF:-$ROOT_DIR/.artifacts/rerank-20261025/qwen3-reranker-4b-q8_0-rank.gguf}
  export MEMSTATION_RERANK_ALIAS=${MEMSTATION_RERANK_ALIAS:-qwen3-reranker-4b-q8_0}
  # One configured model serves every generation role unless a role overrides it.
  local role
  for role in ANSWERER JUDGE EXTRACTION; do
    local model="${role}_MODEL" url="${role}_BASE_URL" api="${role}_API_KEY" temp="${role}_TEMPERATURE" effort="${role}_REASONING_EFFORT" timeout="${role}_TIMEOUT"
    export "$model=${!model:-${CHAT_MODEL:-}}" "$url=${!url:-${OPENAI_BASE_URL:-}}" "$api=${!api:-${OPENAI_API_KEY:-}}"
    export "$temp=${!temp:-0}" "$effort=${!effort:-low}" "$timeout=${!timeout:-10m}"
  done
  export ANSWERER_MAX_OUTPUT_TOKENS=${ANSWERER_MAX_OUTPUT_TOKENS:-16384} JUDGE_MAX_OUTPUT_TOKENS=${JUDGE_MAX_OUTPUT_TOKENS:-4096} EXTRACTION_MAX_OUTPUT_TOKENS=${EXTRACTION_MAX_OUTPUT_TOKENS:-65536}
  export RERANK_MODEL=${RERANK_MODEL:-rerank_compatible:$MEMSTATION_RERANK_ALIAS} RERANK_BASE_URL=${RERANK_BASE_URL:-http://127.0.0.1:$MEMSTATION_RERANK_PORT} RERANK_API_KEY=${RERANK_API_KEY:-local}
  export MEMSTATION_MEMORY_PROCESSING_STRATEGY=${MEMSTATION_MEMORY_PROCESSING_STRATEGY:-generative}
  memstation_extractor_configure
}

runtime() {
  if command -v container >/dev/null 2>&1; then
    container "$@"
  else
    docker "$@"
  fi
}

container_running() {
  runtime exec "$1" true >/dev/null 2>&1
}

start_container() {
  local name=$1
  shift
  if container_running "$name"; then
    return
  fi
  if ! runtime start "$name" >/dev/null 2>&1; then
    runtime run --detach --name "$name" "$@" >/dev/null
  fi
  echo "$name" >>"$STATE_DIR/containers"
}

wait_for() {
  local label=$1 deadline=$((SECONDS + ${2:-180}))
  shift 2
  until "$@" >/dev/null 2>&1; do
    if ((SECONDS >= deadline)); then
      echo "$label not ready" >&2
      exit 1
    fi
    sleep 1
  done
}

start_background() {
  local name=$1
  shift
  nohup "$@" >>"$STATE_DIR/$name.log" 2>&1 </dev/null &
  echo $! >"$STATE_DIR/$name.pid"
}

rerank_ready() {
  curl --silent --fail --max-time 2 "http://127.0.0.1:$MEMSTATION_RERANK_PORT/health"
}

model_ready() {
  curl --silent --fail --max-time 5 -H "Authorization: Bearer ${ANSWERER_API_KEY:-}" "${ANSWERER_BASE_URL%/}/models"
}

database_url() {
  echo "postgres://postgres:$MEMSTATION_EVAL_POSTGRES_PASSWORD@127.0.0.1:$MEMSTATION_EVAL_POSTGRES_PORT/$1?sslmode=disable"
}

new_database() {
  local name=$1
  if [[ ! "$name" =~ ^memstation_(eval|quality)_[a-z0-9_]+$ ]]; then
    echo "evaluation database names start with memstation_eval_ or memstation_quality_" >&2
    exit 1
  fi
  runtime exec "$MEMSTATION_EVAL_POSTGRES_CONTAINER" createdb -U postgres "$name"
  { echo "SET client_min_messages = warning;"; cat "$ROOT_DIR"/apps/server/ent/migrate/migrations/*.sql; } |
    runtime exec -i "$MEMSTATION_EVAL_POSTGRES_CONTAINER" psql -q -U postgres -d "$name" -v ON_ERROR_STOP=1 >/dev/null
  database_url "$name"
}

start_service() {
  local name=memstation_eval_service role
  if ! runtime exec "$MEMSTATION_EVAL_POSTGRES_CONTAINER" psql -U postgres -lqt | cut -d'|' -f1 | grep -qw "$name"; then
    new_database "$name" >/dev/null
  fi
  : "${MEMSTATION_ADMIN_TOKEN:?Set MEMSTATION_ADMIN_TOKEN for the evaluation service}"
  : "${MEMSTATION_MASTER_KEY:?Set MEMSTATION_MASTER_KEY for the evaluation service}"
  (cd "$ROOT_DIR/apps/server" && go build -o "$STATE_DIR/memstation" ./cmd/memstation)
  export DATABASE_URL MEMSTATION_PORT=${MEMSTATION_EVAL_HTTP_PORT:-18080} MEMSTATION_GRPC_PORT=${MEMSTATION_EVAL_GRPC_PORT:-19090}
  DATABASE_URL=$(database_url "$name")
  export REDIS_URL="redis://127.0.0.1:$MEMSTATION_EVAL_REDIS_PORT/0" VECTOR_STORE_BACKEND=pgvector
  for role in serve worker scheduler; do
    start_background "$role" "$STATE_DIR/memstation" "$role"
  done
  wait_for "evaluation service" 90 curl --silent --fail --max-time 2 "http://127.0.0.1:$MEMSTATION_PORT/health/ready"
  echo "MEMSTATION_EVAL_BASE_URL=http://127.0.0.1:$MEMSTATION_PORT"
}

up() {
  mkdir -p "$STATE_DIR"
  chmod 700 "$STATE_DIR"
  start_container "$MEMSTATION_EVAL_POSTGRES_CONTAINER" --publish "127.0.0.1:$MEMSTATION_EVAL_POSTGRES_PORT:5432" --env "POSTGRES_PASSWORD=$MEMSTATION_EVAL_POSTGRES_PASSWORD" pgvector/pgvector:pg16
  wait_for PostgreSQL 90 runtime exec "$MEMSTATION_EVAL_POSTGRES_CONTAINER" pg_isready -U postgres
  start_container "$MEMSTATION_EVAL_REDIS_CONTAINER" --publish "127.0.0.1:$MEMSTATION_EVAL_REDIS_PORT:6379" redis:7.4.2-alpine
  wait_for Redis 60 runtime exec "$MEMSTATION_EVAL_REDIS_CONTAINER" redis-cli ping
  if ! memstation_extractor_ready; then
    start_background extractor bash "$ROOT_DIR/scripts/dev/run-extractor.sh"
    wait_for "memory extractor" "${MEMSTATION_EXTRACTOR_READY_TIMEOUT_SECONDS:-300}" memstation_extractor_ready
  fi
  if ! rerank_ready >/dev/null 2>&1; then
    if [[ ! -f "$MEMSTATION_RERANK_GGUF" ]]; then
      echo "reranker weights missing: $MEMSTATION_RERANK_GGUF (see docs/reranking.md)" >&2
      exit 1
    fi
    start_background reranker bash "$ROOT_DIR/scripts/dev/run-reranker.sh"
    wait_for reranker 120 rerank_ready
  fi
  if ! model_ready >/dev/null 2>&1; then
    echo "model endpoint not reachable at ${ANSWERER_BASE_URL:-<unset>}; start it before running benchmarks" >&2
    exit 1
  fi
  if [[ "${1:-}" == "--service" ]]; then
    start_service
  fi
  export -p | grep -E 'declare -x (ANSWERER|JUDGE|EXTRACTION|RERANK|EMBEDDING|MEMSTATION_MEMORY_PROCESSING|MEMSTATION_EVAL_POSTGRES)_' | sed 's/^declare -x //' >"$STATE_DIR/env"
  chmod 600 "$STATE_DIR/env"
  status
}

status() {
  local report=down
  container_running "$MEMSTATION_EVAL_POSTGRES_CONTAINER" && report=up
  echo "postgres  $report"
  report=down
  container_running "$MEMSTATION_EVAL_REDIS_CONTAINER" && report=up
  echo "redis     $report"
  report=down
  memstation_extractor_ready && report=up
  echo "extractor $report"
  report=down
  rerank_ready >/dev/null 2>&1 && report=up
  echo "reranker  $report"
  report=down
  model_ready >/dev/null 2>&1 && report=up
  echo "models    $report"
}

down() {
  local file pid name
  for file in "$STATE_DIR"/*.pid; do
    [[ -e "$file" ]] || continue
    pid=$(cat "$file")
    kill "$pid" 2>/dev/null || true
    rm -f "$file"
  done
  if [[ -f "$STATE_DIR/containers" ]]; then
    while read -r name; do
      runtime stop "$name" >/dev/null 2>&1 || true
    done <"$STATE_DIR/containers"
    rm -f "$STATE_DIR/containers"
  fi
}

load_settings
case "${1:-}" in
  up) up "${2:-}" ;;
  down) down ;;
  status) status ;;
  newdb) new_database "${2:?database name required}" ;;
  *)
    echo "usage: stack.sh up [--service] | down | status | newdb <name>" >&2
    exit 1
    ;;
esac
