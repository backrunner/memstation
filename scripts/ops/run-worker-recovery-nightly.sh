#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "$0")/../.." && pwd)
OUT_DIR=${1:-"$ROOT_DIR/.artifacts/eval/recovery-nightly"}
CONVERSATIONS=${MEMSTATION_RECOVERY_CONVERSATIONS:-256}
WORKER_CONCURRENCY=${MEMSTATION_RECOVERY_WORKER_CONCURRENCY:-12}
MESSAGES_PER_TURN=${MEMSTATION_RECOVERY_MESSAGES_PER_TURN:-4}
MAX_DRAIN=${MEMSTATION_RECOVERY_MAX_DRAIN:-20s}
TIMEOUT=${MEMSTATION_RECOVERY_TIMEOUT:-45s}

cd "$ROOT_DIR/apps/server"
go run ./tests/eval/cmd/recoveryrun \
  -out "$OUT_DIR" \
  -conversations "$CONVERSATIONS" \
  -worker-concurrency "$WORKER_CONCURRENCY" \
  -messages-per-turn "$MESSAGES_PER_TURN" \
  -timeout "$TIMEOUT" \
  -max-drain "$MAX_DRAIN"
