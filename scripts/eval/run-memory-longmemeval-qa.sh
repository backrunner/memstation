#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "$0")/../.." && pwd)
OUT_DIR=${2:-"$ROOT_DIR/.artifacts/eval/longmemeval-qa"}
HYPOTHESES_PATH=${1:-${LONGMEMEVAL_QA_HYPOTHESES_PATH:-}}
LONGMEMEVAL_VARIANT=${LONGMEMEVAL_VARIANT:-s}
DATASET_PATH=${LONGMEMEVAL_DATASET_PATH:-}
DATASET_URL=${LONGMEMEVAL_DATASET_URL:-}
JUDGE_MODEL=${LONGMEMEVAL_QA_JUDGE_MODEL:-}
JUDGE_BASE_URL=${LONGMEMEVAL_QA_JUDGE_BASE_URL:-}
JUDGE_API_KEY=${LONGMEMEVAL_QA_JUDGE_API_KEY:-}

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

if [[ -z "$HYPOTHESES_PATH" ]]; then
  echo "a LongMemEval hypotheses file is required" >&2
  exit 1
fi
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
if [[ "$HYPOTHESES_PATH" != /* ]]; then
  HYPOTHESES_PATH="$ROOT_DIR/$HYPOTHESES_PATH"
fi

if [[ -z "$JUDGE_MODEL" || -z "$JUDGE_BASE_URL" || -z "$JUDGE_API_KEY" ]]; then
  echo "LongMemEval QA auto-eval requires LONGMEMEVAL_QA_JUDGE_MODEL, LONGMEMEVAL_QA_JUDGE_BASE_URL, and LONGMEMEVAL_QA_JUDGE_API_KEY" >&2
  exit 1
fi

cd "$ROOT_DIR/apps/server"
go run ./tests/eval/cmd/qaevalrun \
  -dataset "$DATASET_PATH" \
  -hypotheses "$HYPOTHESES_PATH" \
  -out "$OUT_DIR" \
  -judge-model "$JUDGE_MODEL" \
  -judge-base-url "$JUDGE_BASE_URL" \
  -judge-api-key "$JUDGE_API_KEY"
