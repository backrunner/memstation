#!/usr/bin/env bash
set -euo pipefail
# Model and alias are explicit deployment choices; no download or conversion here.
: "${MEMSTATION_RERANK_GGUF:?Set the path to a ranking-capable GGUF}"
: "${MEMSTATION_RERANK_ALIAS:?Set the model ID used by MemStation}"
exec "${LLAMA_SERVER_BIN:-llama-server}" \
  --model "$MEMSTATION_RERANK_GGUF" \
  --alias "$MEMSTATION_RERANK_ALIAS" \
  --reranking --host "${MEMSTATION_RERANK_HOST:-127.0.0.1}" \
  --port "${MEMSTATION_RERANK_PORT:-18082}" \
  --ctx-size "${MEMSTATION_RERANK_CONTEXT:-8192}" \
  --batch-size "${MEMSTATION_RERANK_CONTEXT:-8192}" \
  --ubatch-size "${MEMSTATION_RERANK_CONTEXT:-8192}" \
  --parallel 1 --log-verbosity 1 "$@"
