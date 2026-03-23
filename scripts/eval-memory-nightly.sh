#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "$0")/.." && pwd)
OUT_DIR=${1:-"$ROOT_DIR/.artifacts/eval/nightly"}
DATASET_PATH=${LOCOMO_DATASET_PATH:-}
DATASET_URL=${LOCOMO_DATASET_URL:-https://raw.githubusercontent.com/snap-research/locomo/main/data/locomo10.json}
PERF_P95_MS=${MEMSTATION_PERF_P95_MS:-50}

cleanup() {
  if [[ -n "${TMP_LOCOMO_FILE:-}" && -f "$TMP_LOCOMO_FILE" ]]; then
    rm -f "$TMP_LOCOMO_FILE"
  fi
}
trap cleanup EXIT

if [[ -z "$DATASET_PATH" ]]; then
  TMP_LOCOMO_FILE=$(mktemp)
  curl -fsSL "$DATASET_URL" -o "$TMP_LOCOMO_FILE"
  DATASET_PATH="$TMP_LOCOMO_FILE"
fi

if [[ -z "$DATASET_PATH" ]]; then
  echo "LOCOMO benchmark dataset could not be resolved." >&2
  exit 1
fi
if [[ "$DATASET_PATH" != /* ]]; then
  DATASET_PATH="$ROOT_DIR/$DATASET_PATH"
fi

mkdir -p "$OUT_DIR"

cd "$ROOT_DIR/apps/server"
go run ./tests/eval/cmd/evalrun \
  -format repo-native \
  -dataset ./tests/eval/testdata/repo_native_golden.json \
  -out "$OUT_DIR/repo-native" \
  -variants full,full_no_relations,semantic_only,semantic_importance \
  -top-n 5 \
  -enforce-smoke \
  -perf-smoke \
  -perf-iterations 100 \
  -perf-max-p95-ms "$PERF_P95_MS"

go run ./tests/eval/cmd/evalrun \
  -format locomo \
  -dataset "$DATASET_PATH" \
  -out "$OUT_DIR/locomo" \
  -variants full,full_no_relations,semantic_only,semantic_importance \
  -top-n 10 \
  -perf-smoke \
  -perf-iterations 25
