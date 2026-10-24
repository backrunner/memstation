#!/usr/bin/env bash
set -euo pipefail
ROOT_DIR=$(cd "$(dirname "$0")/../.." && pwd)
: "${MEMSTATION_ADMIN_TOKEN:?Set the isolated service admin token in the environment}"
: "${CHAT_MODEL:?Set the configured chat model in the environment}"
: "${MEMSTATION_EVAL_BASE_URL:?Set the dedicated loopback evaluation service URL}"
OUT_DIR=${1:-"$ROOT_DIR/.artifacts/eval/source-replay"}
cd "$ROOT_DIR/apps/server"
go run ./tests/eval/cmd/liveevalrun \
  -base-url "$MEMSTATION_EVAL_BASE_URL" \
  -out "$OUT_DIR" -concurrency "${MEMSTATION_EVAL_CONCURRENCY:-2}"
