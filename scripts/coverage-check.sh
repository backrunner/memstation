#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "$0")/.." && pwd)
SERVER_THRESHOLD=${MEMSTATION_SERVER_COVERAGE_THRESHOLD:-55}
MANAGER_THRESHOLD=${MEMSTATION_MANAGER_COVERAGE_THRESHOLD:-20}
COVER_PROFILE=$(mktemp)
trap 'rm -f "$COVER_PROFILE"' EXIT
SERVER_COVERPKG=${MEMSTATION_SERVER_COVERPKG:-./internal/cache,./internal/service/analysis,./internal/service/memory,./internal/service/settings,./internal/worker}
SERVER_TEST_TARGETS=${MEMSTATION_SERVER_COVERAGE_TEST_TARGETS:-./internal/cache ./internal/service/analysis ./internal/service/memory ./internal/service/settings ./internal/worker ./tests/contract ./tests/e2e ./tests/eval ./tests/unit}

cd "$ROOT_DIR/apps/server"
go test -covermode=atomic -coverpkg="$SERVER_COVERPKG" -coverprofile="$COVER_PROFILE" $SERVER_TEST_TARGETS
SERVER_TOTAL=$(go tool cover -func="$COVER_PROFILE" | awk '/^total:/ {gsub("%", "", $3); print $3}')
if [[ -z "$SERVER_TOTAL" ]]; then
  echo "failed to compute server coverage" >&2
  exit 1
fi
awk -v actual="$SERVER_TOTAL" -v threshold="$SERVER_THRESHOLD" 'BEGIN { exit !(actual + 0 >= threshold + 0) }' || {
  echo "server coverage ${SERVER_TOTAL}% is below threshold ${SERVER_THRESHOLD}%" >&2
  exit 1
}

echo "server coverage ${SERVER_TOTAL}% >= ${SERVER_THRESHOLD}%"

cd "$ROOT_DIR/apps/manager"
pnpm exec vitest run --coverage --reporter=dot
MANAGER_SUMMARY="$ROOT_DIR/apps/manager/coverage/coverage-summary.json"
if [[ -f "$MANAGER_SUMMARY" ]]; then
  MANAGER_TOTAL=$(node -e "const summary = require(process.argv[1]); console.log(summary.total.lines.pct);" "$MANAGER_SUMMARY")
else
  MANAGER_FINAL="$ROOT_DIR/apps/manager/coverage/coverage-final.json"
  if [[ ! -f "$MANAGER_FINAL" ]]; then
    echo "manager coverage summary not found at $MANAGER_SUMMARY or $MANAGER_FINAL" >&2
    exit 1
  fi
  MANAGER_TOTAL=$(node -e "const coverage = require(process.argv[1]); let total = 0; let covered = 0; for (const entry of Object.values(coverage)) { for (const count of Object.values(entry.s || {})) { total += 1; if (count > 0) covered += 1; } } console.log(total === 0 ? '0' : ((covered / total) * 100).toFixed(2));" "$MANAGER_FINAL")
fi
if [[ -z "$MANAGER_TOTAL" ]]; then
  echo "failed to compute manager coverage" >&2
  exit 1
fi
awk -v actual="$MANAGER_TOTAL" -v threshold="$MANAGER_THRESHOLD" 'BEGIN { exit !(actual + 0 >= threshold + 0) }' || {
  echo "manager coverage ${MANAGER_TOTAL}% is below threshold ${MANAGER_THRESHOLD}%" >&2
  exit 1
}

echo "manager coverage ${MANAGER_TOTAL}% >= ${MANAGER_THRESHOLD}%"
