#!/usr/bin/env bash
set -euo pipefail
ROOT_DIR=$(cd "$(dirname "$0")/../.." && pwd)
OUT_DIR=${MEMSTATION_RECOVERY_ARTIFACT_DIR:-"$ROOT_DIR/.artifacts/drills/memory-recovery"}
mkdir -p "$OUT_DIR"
OUT_DIR=$(cd "$OUT_DIR" && pwd)
if [[ "${1:-}" == "--dry-run" ]]; then
  printf '{"status":"dry-run","scope":"isolated test schema","checks":["pg_dump","pg_restore","latest independent deletion journal","readiness barrier","cache invalidation"]}\n' >"$OUT_DIR/report.json"
  exit 0
fi
: "${MEMSTATION_TEST_DATABASE_URL:?Use a dedicated test database; the drill creates and removes a private schema.}"
command -v pg_dump >/dev/null
command -v pg_restore >/dev/null
cd "$ROOT_DIR/apps/server"
export MEMSTATION_TEST_BACKUP_RESTORE=1
go test -json ./internal/store/sql/lifecycle -run '^TestDeletionJournalActualBackupRestore$' -count=1 >"$OUT_DIR/go-test.jsonl"
printf '{"status":"passed","scope":"isolated test schema","evidence":"go-test.jsonl"}\n' >"$OUT_DIR/report.json"
