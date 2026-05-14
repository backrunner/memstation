#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "$0")/../.." && pwd)
OUT_DIR=${1:-"$ROOT_DIR/.artifacts/eval/perf"}
ENFORCE_BENCHMARKS=${MEMSTATION_ENFORCE_BENCHMARKS:-false}
PERF_P95_MS=${MEMSTATION_PERF_P95_MS:-}

cd "$ROOT_DIR/apps/server"
args=(
  -format repo-native
  -dataset ./tests/eval/testdata/repo_native_golden.json
  -out "$OUT_DIR"
  -variants full,full_no_relations,semantic_only,semantic_importance
  -top-n 5
  -perf-smoke
  -perf-iterations 100
)

if [[ "$ENFORCE_BENCHMARKS" == "true" ]]; then
  args+=(-enforce-smoke)
  if [[ -n "$PERF_P95_MS" ]]; then
    args+=(-perf-max-p95-ms "$PERF_P95_MS")
  fi
fi

go run ./tests/eval/cmd/evalrun "${args[@]}"
