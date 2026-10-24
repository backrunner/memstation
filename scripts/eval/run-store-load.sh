#!/usr/bin/env bash
set -euo pipefail
ROOT_DIR=$(cd "$(dirname "$0")/../.." && pwd)
: "${MEMSTATION_ADMIN_TOKEN:?Set the isolated service admin token in the environment}"
: "${MEMSTATION_EVAL_DATABASE_URL:?Set a migrated, dedicated loopback evaluation database URL}"
: "${MEMSTATION_EVAL_BASE_URL:?Set the dedicated loopback evaluation service URL}"
: "${EMBEDDING_MODEL:?Set the deployment embedding model}"
: "${EMBEDDING_BASE_URL:?Set the embedding endpoint}"
: "${EMBEDDING_API_KEY:?Set the embedding credential in the environment}"
OUT_DIR=${1:-"$ROOT_DIR/.artifacts/eval/store-load"}
cd "$ROOT_DIR/apps/server"
args=(-base-url "$MEMSTATION_EVAL_BASE_URL" -out "$OUT_DIR"
  -rows "${MEMSTATION_EVAL_ROWS:-10000}" -requests "${MEMSTATION_EVAL_REQUESTS:-100}"
  -rps "${MEMSTATION_EVAL_RPS:-12}")
if [[ -n "${MEMSTATION_PERF_P95_MS:-}" ]]; then args+=(-max-p95-ms "$MEMSTATION_PERF_P95_MS"); fi
go run ./tests/eval/cmd/storeloadrun "${args[@]}"
