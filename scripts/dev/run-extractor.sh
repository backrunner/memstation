#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "$0")/../.." && pwd)
# shellcheck source=./extractor-lib.sh
source "$ROOT_DIR/scripts/dev/extractor-lib.sh"
memstation_extractor_configure

if ! command -v uv >/dev/null 2>&1; then
  echo "required command not found: uv (https://docs.astral.sh/uv/)" >&2
  exit 1
fi

cd "$ROOT_DIR/apps/extractor"
uv sync --locked --extra "$MEMSTATION_EXTRACTOR_BACKEND"

port=${MEMSTATION_MEMORY_PROCESSING_ENDPOINT##*:}
export MEMSTATION_EXTRACTOR_HOST=127.0.0.1
export MEMSTATION_EXTRACTOR_PORT=${port%%/*}
export MEMSTATION_EXTRACTOR_TOKEN=$MEMSTATION_MEMORY_PROCESSING_TOKEN
export MEMSTATION_EXTRACTOR_MODEL=$MEMSTATION_MEMORY_PROCESSING_MODEL
export MEMSTATION_EXTRACTOR_REVISION=$MEMSTATION_MEMORY_PROCESSING_REVISION
export MEMSTATION_EXTRACTOR_SPLITTER=$MEMSTATION_MEMORY_PROCESSING_SPLITTER
export HF_HOME=${HF_HOME:-$ROOT_DIR/.artifacts/extractor-hf}
exec .venv/bin/python server.py
