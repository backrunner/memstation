#!/usr/bin/env bash
set -euo pipefail
if [[ ${MEMSTATION_EVAL_RERANK_SCORE_POLICY+x} || ${MEMSTATION_EVAL_RERANK_BLEND_WEIGHT+x} ]]; then
  echo "Removed rerank settings are unsupported; configure the model and candidate limits." >&2
  exit 1
fi

ROOT_DIR=$(cd "$(dirname "$0")/../.." && pwd)
: "${LOCOMO_DATASET_PATH:?Set the external dataset path}"

: "${MEMSTATION_EVAL_PREFIX:?Set a unique eval- prefix}"
: "${MEMSTATION_EVAL_CODE_REVISION:?Set the tested source revision/fingerprint}"


: "${ANSWERER_MODEL:?Set the answer model}"
: "${JUDGE_MODEL:?Set the judge model; explicitly label self-judged diagnostics}"
OUT_DIR=${1:-"$ROOT_DIR/.artifacts/eval/locomo-pipeline"}
cd "$ROOT_DIR/apps/server"
go run ./tests/eval/cmd/locomopipeline \
  -dataset "$LOCOMO_DATASET_PATH" -out "$OUT_DIR" \
  -prefix "$MEMSTATION_EVAL_PREFIX" \
  -sample-id "${MEMSTATION_EVAL_SAMPLE_ID:-}" \
  -questions-per-category "${MEMSTATION_EVAL_QUESTIONS_PER_CATEGORY:-0}" \
  -top-n "${MEMSTATION_EVAL_TOP_N:-20}" \
  -context-bytes "${MEMSTATION_EVAL_CONTEXT_BYTES:-16000}" \
  -reader-policy "${MEMSTATION_EVAL_READER_POLICY:-baseline}" \
  -semantic-episodes="${MEMSTATION_EVAL_SEMANTIC_EPISODES:-false}" \
  -refine-evidence="${MEMSTATION_EVAL_REFINE_EVIDENCE:-false}" \
  -context-format "${MEMSTATION_EVAL_CONTEXT_FORMAT:-plain}" \
  -source-neighbor-turns "${MEMSTATION_EVAL_SOURCE_NEIGHBOR_TURNS:-0}" \
  -code-revision "$MEMSTATION_EVAL_CODE_REVISION" \
  -embedding-revision "${EMBEDDING_REVISION:-}" \
  -modes "${MEMSTATION_EVAL_MODES:-raw,derived,combined}" \
  -rerank-candidate-selection "${MEMSTATION_EVAL_CANDIDATE_SELECTION:-balanced}" \
  -rerank-candidate-multiplier "${MEMSTATION_EVAL_CANDIDATE_MULTIPLIER:-6}" \
  -rerank-min-candidates "${MEMSTATION_EVAL_MIN_CANDIDATES:-20}" \
  -rerank-max-candidates "${MEMSTATION_EVAL_MAX_CANDIDATES:-50}" \
  -oracle-context-bytes "${MEMSTATION_EVAL_ORACLE_CONTEXT_BYTES:-512000}" \
  -oracle-neighbor-turns "${MEMSTATION_EVAL_ORACLE_NEIGHBOR_TURNS:-0}"
