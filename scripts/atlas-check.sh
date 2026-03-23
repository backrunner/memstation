#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "$0")/.." && pwd)
DATABASE_URL=${DATABASE_URL:-postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable}
ATLAS_BIN=${ATLAS_BIN:-atlas}

if ! command -v "$ATLAS_BIN" >/dev/null 2>&1; then
  echo "atlas is required on PATH for migration checks" >&2
  exit 1
fi

cd "$ROOT_DIR/apps/server"
"$ATLAS_BIN" migrate lint \
  --dir "file://ent/migrate/migrations" \
  --dev-url "$DATABASE_URL" \
  --latest 1
"$ATLAS_BIN" migrate apply \
  --dir "file://ent/migrate/migrations" \
  --url "$DATABASE_URL" \
  --dry-run
