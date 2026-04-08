#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "$0")/.." && pwd)
DATASET_PATH=${LOCOMO_DATASET_PATH:-}
DATASET_URL=${LOCOMO_DATASET_URL:-https://raw.githubusercontent.com/snap-research/locomo/main/data/locomo10.json}
OUT_DIR=${1:-"$ROOT_DIR/.artifacts/eval/locomo"}
EMBEDDING_MODE=${EVAL_EMBEDDING_MODE:-provider}
EMBEDDING_MODEL=${EMBEDDING_MODEL:-}
EMBEDDING_BASE_URL=${EMBEDDING_BASE_URL:-}
EMBEDDING_API_KEY=${EMBEDDING_API_KEY:-}
LOCOMO_SKIP_CATEGORY_FIVE=${LOCOMO_SKIP_CATEGORY_FIVE:-true}

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

if [[ "$EMBEDDING_MODE" == "provider" ]]; then
  if [[ -z "$EMBEDDING_MODEL" || -z "$EMBEDDING_BASE_URL" || -z "$EMBEDDING_API_KEY" ]]; then
    echo "provider embedding mode requires EMBEDDING_MODEL, EMBEDDING_BASE_URL, and EMBEDDING_API_KEY" >&2
    exit 1
  fi
fi

cd "$ROOT_DIR/apps/server"
go run ./tests/eval/cmd/evalrun \
  -format locomo \
  -dataset "$DATASET_PATH" \
  -out "$OUT_DIR" \
  -embedding-mode "$EMBEDDING_MODE" \
  -embedding-model "$EMBEDDING_MODEL" \
  -embedding-base-url "$EMBEDDING_BASE_URL" \
  -embedding-api-key "$EMBEDDING_API_KEY" \
  -locomo-skip-category-five="$LOCOMO_SKIP_CATEGORY_FIVE" \
  -variants full,full_no_relations,semantic_only,semantic_importance \
  -top-n 10
