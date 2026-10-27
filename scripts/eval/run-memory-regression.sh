#!/usr/bin/env bash
set -euo pipefail
ROOT_DIR=$(cd "$(dirname "$0")/../.." && pwd)
: "${MEMSTATION_EVAL_REGRESSION_PLAN:?Set a frozen private plan}"
: "${MEMSTATION_EVAL_REGRESSION_OUT:?Set a fresh private output directory}"
: "${MEMSTATION_EVAL_CODE_REVISION:?Pin the code revision}"
: "${MEMSTATION_EVAL_DATABASE_URL:?Use an empty dedicated loopback evaluation database}"
: "${MEMSTATION_EVAL_EXTERNAL_CACHE:?Use a private content-only embedding cache}"
: "${MEMSTATION_EVAL_EMBEDDING_REVISION:?Pin the embedding revision}"
: "${RERANK_MODEL_REVISION:?Pin the reranker weights}"
PLAN=$(python3 -c 'import pathlib,sys;print(pathlib.Path(sys.argv[1]).resolve())' "$MEMSTATION_EVAL_REGRESSION_PLAN")
OUT=$(python3 -c 'import pathlib,sys;print(pathlib.Path(sys.argv[1]).resolve())' "$MEMSTATION_EVAL_REGRESSION_OUT")
[[ ! -e "$OUT" ]] || { echo "Use a fresh output directory" >&2; exit 1; }
export MEMSTATION_EVAL_REGRESSION_PLAN="$PLAN" MEMSTATION_EVAL_REGRESSION_OUT="$OUT"
cd "$ROOT_DIR/apps/server"
status=0
go test ./tests/eval -run '^TestLiveMemoryRegression$' -count=1 -v -timeout "${MEMSTATION_EVAL_REGRESSION_TIMEOUT:-12h}" || status=$?
if [[ -f "$OUT/manifest.json" ]]; then
  python3 "$ROOT_DIR/scripts/eval/summarize-memory-regression.py" --run "$OUT" || { [[ "$status" != 0 ]] || status=1; }
fi
exit "$status"
