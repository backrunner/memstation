#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "$0")/../.." && pwd)
OUT_DIR=${1:-"$ROOT_DIR/.artifacts/eval/longmemeval"}
LONGMEMEVAL_VARIANT=${LONGMEMEVAL_VARIANT:-s}
LONGMEMEVAL_GRANULARITY=${LONGMEMEVAL_GRANULARITY:-session}
LONGMEMEVAL_TOP_N=${LONGMEMEVAL_TOP_N:-10}
DATASET_PATH=${LONGMEMEVAL_DATASET_PATH:-}
DATASET_URL=${LONGMEMEVAL_DATASET_URL:-}
EMBEDDING_MODE=${EVAL_EMBEDDING_MODE:-provider}
EMBEDDING_MODEL=${EMBEDDING_MODEL:-}
EMBEDDING_BASE_URL=${EMBEDDING_BASE_URL:-}
EMBEDDING_API_KEY=${EMBEDDING_API_KEY:-}

resolve_dataset_url() {
  case "$LONGMEMEVAL_VARIANT" in
    s)
      echo "https://huggingface.co/datasets/xiaowu0162/longmemeval-cleaned/resolve/main/longmemeval_s_cleaned.json"
      ;;
    m)
      echo "https://huggingface.co/datasets/xiaowu0162/longmemeval-cleaned/resolve/main/longmemeval_m_cleaned.json"
      ;;
    oracle)
      echo "https://huggingface.co/datasets/xiaowu0162/longmemeval-cleaned/resolve/main/longmemeval_oracle.json"
      ;;
    *)
      echo "unsupported LONGMEMEVAL_VARIANT: $LONGMEMEVAL_VARIANT" >&2
      exit 1
      ;;
  esac
}

cleanup() {
  if [[ -n "${TMP_LONGMEMEVAL_FILE:-}" && -f "$TMP_LONGMEMEVAL_FILE" ]]; then
    rm -f "$TMP_LONGMEMEVAL_FILE"
  fi
}
trap cleanup EXIT

if [[ -z "$DATASET_URL" ]]; then
  DATASET_URL=$(resolve_dataset_url)
fi
if [[ -z "$DATASET_PATH" ]]; then
  TMP_LONGMEMEVAL_FILE=$(mktemp)
  curl -fsSL "$DATASET_URL" -o "$TMP_LONGMEMEVAL_FILE"
  DATASET_PATH="$TMP_LONGMEMEVAL_FILE"
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
  -format longmemeval \
  -dataset "$DATASET_PATH" \
  -out "$OUT_DIR" \
  -embedding-mode "$EMBEDDING_MODE" \
  -embedding-model "$EMBEDDING_MODEL" \
  -embedding-base-url "$EMBEDDING_BASE_URL" \
  -embedding-api-key "$EMBEDDING_API_KEY" \
  -longmemeval-granularity "$LONGMEMEVAL_GRANULARITY" \
  -variants full,full_no_relations,semantic_only,semantic_importance \
  -top-n "$LONGMEMEVAL_TOP_N"
