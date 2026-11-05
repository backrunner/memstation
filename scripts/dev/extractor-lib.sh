#!/usr/bin/env bash
# Sourced by dev and benchmark entry points so every path starts the same extractor.

EXTRACTOR_LIB_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
MEMSTATION_EXTRACTOR_STARTED_PID=""

# Fill unset settings from the pinned defaults and keep one local service token.
memstation_extractor_configure() {
  local key value token_file
  while IFS='=' read -r key value; do
    [[ "$key" =~ ^[A-Z_]+$ ]] || continue
    if [[ -z "${!key:-}" ]]; then
      export "$key=$value"
    fi
  done <"$EXTRACTOR_LIB_ROOT/scripts/dev/extractor.env"
  export MEMSTATION_MEMORY_PROCESSING_ENDPOINT=${MEMSTATION_MEMORY_PROCESSING_ENDPOINT:-http://127.0.0.1:8092}
  if [[ -z "${MEMSTATION_MEMORY_PROCESSING_TOKEN:-}" ]]; then
    token_file="$EXTRACTOR_LIB_ROOT/.artifacts/dev-extractor-token"
    if [[ ! -s "$token_file" ]]; then
      mkdir -p "$(dirname "$token_file")"
      (umask 077 && openssl rand -hex 32 >"$token_file")
    fi
    MEMSTATION_MEMORY_PROCESSING_TOKEN=$(cat "$token_file")
    export MEMSTATION_MEMORY_PROCESSING_TOKEN
  fi
}

memstation_extractor_ready() {
  curl --silent --fail --max-time 2 "${MEMSTATION_MEMORY_PROCESSING_ENDPOINT}/health/ready" >/dev/null 2>&1
}

# A local process keeps serving the code it started with, so reusing it after an
# edit would silently run old behaviour. Refuse instead of killing an unknown process.
memstation_extractor_check_current() {
  local wanted
  case "$MEMSTATION_MEMORY_PROCESSING_ENDPOINT" in
    http://127.0.0.1:* | http://localhost:*) ;;
    *) return 0 ;;
  esac
  wanted=$(openssl dgst -sha256 -r "$EXTRACTOR_LIB_ROOT/apps/extractor/server.py" | cut -d' ' -f1)
  if ! curl --silent --fail --max-time 2 "${MEMSTATION_MEMORY_PROCESSING_ENDPOINT}/health/ready" | grep -q "\"code_sha256\": \"$wanted\""; then
    echo "memory extractor at ${MEMSTATION_MEMORY_PROCESSING_ENDPOINT} runs different code than apps/extractor/server.py; stop it and retry" >&2
    return 1
  fi
}

# A loopback endpoint is started here when nothing answers. Any other endpoint is a
# separate service (Compose, Apple container), which this only waits for.
memstation_extractor_ensure() {
  local deadline=$((SECONDS + ${MEMSTATION_EXTRACTOR_READY_TIMEOUT_SECONDS:-300}))
  memstation_extractor_configure
  if memstation_extractor_ready; then
    memstation_extractor_check_current
    return
  fi
  case "$MEMSTATION_MEMORY_PROCESSING_ENDPOINT" in
    http://127.0.0.1:* | http://localhost:*)
      echo "Starting local memory extractor; the first run downloads its model..."
      mkdir -p "$EXTRACTOR_LIB_ROOT/.artifacts"
      bash "$EXTRACTOR_LIB_ROOT/scripts/dev/run-extractor.sh" >>"$EXTRACTOR_LIB_ROOT/.artifacts/dev-extractor.log" 2>&1 &
      MEMSTATION_EXTRACTOR_STARTED_PID=$!
      ;;
    *)
      echo "Waiting for memory extractor at ${MEMSTATION_MEMORY_PROCESSING_ENDPOINT}..."
      ;;
  esac
  until memstation_extractor_ready; do
    if [[ -n "$MEMSTATION_EXTRACTOR_STARTED_PID" ]] && ! kill -0 "$MEMSTATION_EXTRACTOR_STARTED_PID" 2>/dev/null; then
      echo "memory extractor exited; see .artifacts/dev-extractor.log" >&2
      return 1
    fi
    if ((SECONDS >= deadline)); then
      echo "memory extractor not ready at ${MEMSTATION_MEMORY_PROCESSING_ENDPOINT}" >&2
      return 1
    fi
    sleep 2
  done
}

memstation_extractor_stop() {
  if [[ -n "$MEMSTATION_EXTRACTOR_STARTED_PID" ]] && kill -0 "$MEMSTATION_EXTRACTOR_STARTED_PID" 2>/dev/null; then
    kill "$MEMSTATION_EXTRACTOR_STARTED_PID" 2>/dev/null || true
    wait "$MEMSTATION_EXTRACTOR_STARTED_PID" 2>/dev/null || true
  fi
  MEMSTATION_EXTRACTOR_STARTED_PID=""
}
