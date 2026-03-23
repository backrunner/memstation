#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "$0")/.." && pwd)
DATASET_PATH=${LOCOMO_DATASET_PATH:-}
DATASET_URL=${LOCOMO_DATASET_URL:-https://raw.githubusercontent.com/snap-research/locomo/main/data/locomo10.json}
OUT_DIR=${1:-"$ROOT_DIR/.artifacts/eval/locomo"}

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
if [[ "$DATASET_PATH" != /* ]]; then
  DATASET_PATH="$ROOT_DIR/$DATASET_PATH"
fi

cd "$ROOT_DIR/apps/server"
go run ./tests/eval/cmd/evalrun \
  -format locomo \
  -dataset "$DATASET_PATH" \
  -out "$OUT_DIR" \
  -variants full,full_no_relations,semantic_only,semantic_importance \
  -top-n 10
