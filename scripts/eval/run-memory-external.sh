#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "$0")/../.." && pwd)
: "${MEMSTATION_EVAL_EXTERNAL_PLAN:?set an immutable plan.json path}"
: "${MEMSTATION_EVAL_CODE_REVISION:?set the tested source or binary revision}"
: "${MEMSTATION_EVAL_EMBEDDING_REVISION:?pin the installed embedding model digest}"
: "${MEMSTATION_EVAL_EXTERNAL_CACHE:?set a private embedding computation cache directory}"
: "${RERANK_MODEL:?configure the rerank model}"
: "${RERANK_MODEL_REVISION:?pin the rerank weight digest}"
: "${RERANK_BASE_URL:?configure the rerank endpoint}"
: "${ANSWERER_MODEL:?configure the reader model}"
: "${JUDGE_MODEL:?configure the judge model}"

export MEMSTATION_EVAL_EXTERNAL_OUT="${1:-$ROOT_DIR/.agents/archive/external-memory-$(date -u +%Y%m%dT%H%M%SZ)}"
MEMSTATION_EVAL_EXTERNAL_OUT=$(python3 -c 'from pathlib import Path; import sys; print(Path(sys.argv[1]).resolve())' "$MEMSTATION_EVAL_EXTERNAL_OUT")
if [[ -e "$MEMSTATION_EVAL_EXTERNAL_OUT" ]]; then
  echo "external evaluation requires a fresh output directory" >&2
  exit 1
fi
cd "$ROOT_DIR/apps/server"
run_status=0
go test ./tests/eval -run '^TestLiveExternalMemoryBenchmarks$' -count=1 -v -timeout "${MEMSTATION_EVAL_EXTERNAL_TIMEOUT:-12h}" || run_status=$?
report_status=0
if [[ -f "$MEMSTATION_EVAL_EXTERNAL_OUT/manifest.json" ]]; then
  python3 "$ROOT_DIR/scripts/eval/seal-memory-external.py" --run "$MEMSTATION_EVAL_EXTERNAL_OUT" --process-exit-code "$run_status" || report_status=$?
  python3 "$ROOT_DIR/scripts/eval/summarize-memory-external.py" --run "$MEMSTATION_EVAL_EXTERNAL_OUT" || report_status=$?
fi
if [[ "$run_status" -ne 0 ]]; then
  exit "$run_status"
fi
exit "$report_status"
