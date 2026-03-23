#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "$0")/.." && pwd)
OUT_DIR=${1:-"$ROOT_DIR/.artifacts/eval/perf"}
PERF_P95_MS=${MEMSTATION_PERF_P95_MS:-50}

cd "$ROOT_DIR/apps/server"
go run ./tests/eval/cmd/evalrun \
  -format repo-native \
  -dataset ./tests/eval/testdata/repo_native_golden.json \
  -out "$OUT_DIR" \
  -variants full,full_no_relations,semantic_only,semantic_importance \
  -top-n 5 \
  -enforce-smoke \
  -perf-smoke \
  -perf-iterations 100 \
  -perf-max-p95-ms "$PERF_P95_MS"
