#!/usr/bin/env bash
set -euo pipefail
ROOT_DIR=$(cd "$(dirname "$0")/../.." && pwd)
: "${MEMSTATION_EVAL_REGRESSION_PROFILE:?Set the preregistered private profile plan}"
: "${MEMSTATION_EVAL_REGRESSION_PLAN:?Set the original frozen cohort plan}"
: "${MEMSTATION_EVAL_REGRESSION_PARENT:?Set the completed primary run}"
: "${MEMSTATION_EVAL_REGRESSION_PROFILE_OUT:?Use a fresh private output directory}"
: "${MEMSTATION_EVAL_CODE_REVISION:?Pin the profile code revision}"
: "${MEMSTATION_EVAL_DATABASE_URL:?Use a SELECT-only role on the primary evaluation database}"
: "${MEMSTATION_EVAL_SMALL_RERANK_BASE_URL:?Run the separately pinned small reranker}"
for key in MEMSTATION_EVAL_REGRESSION_PROFILE MEMSTATION_EVAL_REGRESSION_PLAN MEMSTATION_EVAL_REGRESSION_PARENT MEMSTATION_EVAL_REGRESSION_PROFILE_OUT; do
  value=$(python3 -c 'import pathlib,sys;print(pathlib.Path(sys.argv[1]).resolve())' "${!key}")
  export "$key=$value"
done
[[ ! -e "$MEMSTATION_EVAL_REGRESSION_PROFILE_OUT" ]] || { echo "Use a fresh profile output directory" >&2; exit 1; }
cd "$ROOT_DIR/apps/server"
status=0
go test ./tests/eval -run '^TestLiveRegressionRetrievalProfile$' -count=1 -v -timeout "${MEMSTATION_EVAL_REGRESSION_TIMEOUT:-12h}" || status=$?
if [[ -f "$MEMSTATION_EVAL_REGRESSION_PROFILE_OUT/manifest.json" ]]; then
  python3 "$ROOT_DIR/scripts/eval/summarize-memory-profile.py" --profile "$MEMSTATION_EVAL_REGRESSION_PROFILE_OUT" --parent "$MEMSTATION_EVAL_REGRESSION_PARENT" || { [[ "$status" != 0 ]] || status=1; }
fi
exit "$status"
