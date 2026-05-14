#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "$0")/../.." && pwd)
OUT_DIR=${1:-"$ROOT_DIR/.artifacts/eval/smoke"}
ENFORCE_BENCHMARKS=${MEMSTATION_ENFORCE_BENCHMARKS:-false}

cd "$ROOT_DIR/apps/server"
args=(
  -format repo-native
  -dataset ./tests/eval/testdata/repo_native_golden.json
  -out "$OUT_DIR"
  -embedding-mode deterministic
  -variants full,full_no_relations,semantic_only,semantic_importance
  -top-n 5
)

if [[ "$ENFORCE_BENCHMARKS" == "true" ]]; then
  args+=(-enforce-smoke)
fi

go run ./tests/eval/cmd/evalrun "${args[@]}"
