#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "$0")/.." && pwd)
OUT_DIR=${1:-"$ROOT_DIR/.artifacts/eval/nightly"}
DATASET_PATH=${LOCOMO_DATASET_PATH:-}
DATASET_URL=${LOCOMO_DATASET_URL:-https://raw.githubusercontent.com/snap-research/locomo/main/data/locomo10.json}
PERF_P95_MS=${MEMSTATION_PERF_P95_MS:-50}
EMBEDDING_MODE=${EVAL_EMBEDDING_MODE:-provider}
EMBEDDING_MODEL=${EMBEDDING_MODEL:-}
EMBEDDING_BASE_URL=${EMBEDDING_BASE_URL:-}
EMBEDDING_API_KEY=${EMBEDDING_API_KEY:-}
LOCOMO_SKIP_CATEGORY_FIVE=${LOCOMO_SKIP_CATEGORY_FIVE:-true}
LONGMEMEVAL_ENABLED=${LONGMEMEVAL_ENABLED:-true}
LONGMEMEVAL_VARIANT=${LONGMEMEVAL_VARIANT:-s}
LONGMEMEVAL_GRANULARITY=${LONGMEMEVAL_GRANULARITY:-session}
LONGMEMEVAL_TOP_N=${LONGMEMEVAL_TOP_N:-10}
LONGMEMEVAL_DATASET_PATH=${LONGMEMEVAL_DATASET_PATH:-}
LONGMEMEVAL_DATASET_URL=${LONGMEMEVAL_DATASET_URL:-}
LONGMEMEVAL_QA_HYPOTHESES_PATH=${LONGMEMEVAL_QA_HYPOTHESES_PATH:-}
LONGMEMEVAL_QA_JUDGE_MODEL=${LONGMEMEVAL_QA_JUDGE_MODEL:-}
LONGMEMEVAL_QA_JUDGE_BASE_URL=${LONGMEMEVAL_QA_JUDGE_BASE_URL:-}
LONGMEMEVAL_QA_JUDGE_API_KEY=${LONGMEMEVAL_QA_JUDGE_API_KEY:-}

resolve_longmemeval_url() {
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
  if [[ -n "${TMP_LOCOMO_FILE:-}" && -f "$TMP_LOCOMO_FILE" ]]; then
    rm -f "$TMP_LOCOMO_FILE"
  fi
  if [[ -n "${TMP_LONGMEMEVAL_FILE:-}" && -f "$TMP_LONGMEMEVAL_FILE" ]]; then
    rm -f "$TMP_LONGMEMEVAL_FILE"
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

if [[ "$EMBEDDING_MODE" == "provider" ]]; then
  if [[ -z "$EMBEDDING_MODEL" || -z "$EMBEDDING_BASE_URL" || -z "$EMBEDDING_API_KEY" ]]; then
    echo "nightly external benchmarks in provider mode require EMBEDDING_MODEL, EMBEDDING_BASE_URL, and EMBEDDING_API_KEY" >&2
    exit 1
  fi
fi

if [[ \( "$LONGMEMEVAL_ENABLED" == "true" || -n "$LONGMEMEVAL_QA_HYPOTHESES_PATH" \) && -z "$LONGMEMEVAL_DATASET_URL" ]]; then
  LONGMEMEVAL_DATASET_URL=$(resolve_longmemeval_url)
fi

if [[ \( "$LONGMEMEVAL_ENABLED" == "true" || -n "$LONGMEMEVAL_QA_HYPOTHESES_PATH" \) && -z "$LONGMEMEVAL_DATASET_PATH" ]]; then
  TMP_LONGMEMEVAL_FILE=$(mktemp)
  curl -fsSL "$LONGMEMEVAL_DATASET_URL" -o "$TMP_LONGMEMEVAL_FILE"
  LONGMEMEVAL_DATASET_PATH="$TMP_LONGMEMEVAL_FILE"
fi
if [[ -n "$LONGMEMEVAL_DATASET_PATH" && "$LONGMEMEVAL_DATASET_PATH" != /* ]]; then
  LONGMEMEVAL_DATASET_PATH="$ROOT_DIR/$LONGMEMEVAL_DATASET_PATH"
fi

mkdir -p "$OUT_DIR"

cd "$ROOT_DIR/apps/server"
go run ./tests/eval/cmd/evalrun \
  -format repo-native \
  -dataset ./tests/eval/testdata/repo_native_golden.json \
  -out "$OUT_DIR/repo-native" \
  -embedding-mode deterministic \
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
  -embedding-mode "$EMBEDDING_MODE" \
  -embedding-model "$EMBEDDING_MODEL" \
  -embedding-base-url "$EMBEDDING_BASE_URL" \
  -embedding-api-key "$EMBEDDING_API_KEY" \
  -locomo-skip-category-five="$LOCOMO_SKIP_CATEGORY_FIVE" \
  -variants full,full_no_relations,semantic_only,semantic_importance \
  -top-n 10 \
  -perf-smoke \
  -perf-iterations 25

if [[ "$LONGMEMEVAL_ENABLED" == "true" ]]; then
  go run ./tests/eval/cmd/evalrun \
    -format longmemeval \
    -dataset "$LONGMEMEVAL_DATASET_PATH" \
    -out "$OUT_DIR/longmemeval" \
    -embedding-mode "$EMBEDDING_MODE" \
    -embedding-model "$EMBEDDING_MODEL" \
    -embedding-base-url "$EMBEDDING_BASE_URL" \
    -embedding-api-key "$EMBEDDING_API_KEY" \
    -longmemeval-granularity "$LONGMEMEVAL_GRANULARITY" \
    -variants full,full_no_relations,semantic_only,semantic_importance \
    -top-n "$LONGMEMEVAL_TOP_N"
fi

if [[ -n "$LONGMEMEVAL_QA_HYPOTHESES_PATH" ]]; then
  if [[ -z "$LONGMEMEVAL_QA_JUDGE_MODEL" || -z "$LONGMEMEVAL_QA_JUDGE_BASE_URL" || -z "$LONGMEMEVAL_QA_JUDGE_API_KEY" ]]; then
    echo "nightly LongMemEval QA auto-eval requires LONGMEMEVAL_QA_JUDGE_MODEL, LONGMEMEVAL_QA_JUDGE_BASE_URL, and LONGMEMEVAL_QA_JUDGE_API_KEY" >&2
    exit 1
  fi
  if [[ "$LONGMEMEVAL_QA_HYPOTHESES_PATH" != /* ]]; then
    LONGMEMEVAL_QA_HYPOTHESES_PATH="$ROOT_DIR/$LONGMEMEVAL_QA_HYPOTHESES_PATH"
  fi
  go run ./tests/eval/cmd/qaevalrun \
    -dataset "$LONGMEMEVAL_DATASET_PATH" \
    -hypotheses "$LONGMEMEVAL_QA_HYPOTHESES_PATH" \
    -out "$OUT_DIR/longmemeval-qa" \
    -judge-model "$LONGMEMEVAL_QA_JUDGE_MODEL" \
    -judge-base-url "$LONGMEMEVAL_QA_JUDGE_BASE_URL" \
    -judge-api-key "$LONGMEMEVAL_QA_JUDGE_API_KEY"
fi
