#!/usr/bin/env bash
set -euo pipefail
ROOT_DIR=$(cd "$(dirname "$0")/../.." && pwd)
: "${MEMSTATION_TEST_DATABASE_URL:?Use a dedicated PostgreSQL test database.}"
: "${MEMSTATION_TEST_REDIS_URL:?Use a dedicated Redis test database.}"
OUT_DIR=${MEMSTATION_INTEGRATION_ARTIFACT_DIR:-"$ROOT_DIR/.artifacts/independent-memory"}
mkdir -p "$OUT_DIR"
OUT_DIR=$(cd "$OUT_DIR" && pwd)
cd "$ROOT_DIR/apps/server"
go test -json ./internal/security ./internal/service/ingestion ./internal/service/lifecycle ./internal/service/aibroker ./internal/store/deletionjournal ./internal/store/sql/... ./internal/transport/http ./internal/transport/grpc ./tests/integration -count=1 >"$OUT_DIR/contracts.jsonl"
printf '{"status":"passed","broker":"deterministic fixture","mimic_joint_acceptance":false,"evidence":"contracts.jsonl"}\n' >"$OUT_DIR/report.json"
