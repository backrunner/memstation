# Reranking

MemStation can score a bounded set of retrieved memories with a second model. Final ordering uses relevance with bounded importance and time adjustments. The model and candidate limits can be changed live in Manager. Generation, embeddings and reranking use separate provider scopes.

## Configure in Manager

1. Open **Settings → Providers → Rerank Providers**. Add the service and save its API root and credentials.
2. Open **Settings → System → Rerank**. Choose the service, enter its model ID and save.
3. Inspect memory search results: successful reranking adds `rerank_applied`, `rerank_rank` and `rerank_score` metadata. Debug score components record the actual `score_policy` and applied factors. Final order can differ from pure reranker order within the policy's adjustments.

Select **Disabled** to turn off reranking. Model IDs are free text and preserve vendor namespaces and tags.

| Service | Model reference | API root | Credential environment |
| --- | --- | --- | --- |
| Cohere | `cohere:<model-id>` | `https://api.cohere.com/v2` | `COHERE_API_KEY` |
| Jina | `jina:<model-id>` | `https://api.jina.ai/v1` | `JINA_API_KEY` |
| OpenRouter | `openrouter:<vendor/model-id>` | `https://openrouter.ai/api/v1` | `OPENROUTER_API_KEY` |
| Local / compatible | `rerank_compatible:<model-id>` | e.g. `http://127.0.0.1:18082` | optional `MEMSTATION_RERANK_API_KEY` |

API roots can also be set with `COHERE_BASE_URL`, `JINA_BASE_URL`, `OPENROUTER_BASE_URL`, and `MEMSTATION_RERANK_BASE_URL`. Do not append `/rerank`: the adapter appends it. The adapter sends `model`, `query`, string `documents`, and `top_n`, and accepts indexed `results` with finite `relevance_score` in `[0,1]`. Arbitrary chat or embedding models are not rerank models. OpenRouter must use a model offered by its [rerank endpoint](https://openrouter.ai/docs/api/api-reference/rerank/submit-a-rerank-request).

Persisted Manager provider overrides take precedence over environment defaults. Secrets are encrypted in runtime state and are never returned to the browser. Rerank-only services are not added to the gateway's chat-provider enum.

## Local llama.cpp

Use a GGUF converted for ranking with a dedicated classifier head. A GGUF with
only a language-model head cannot be treated as a reliable reranker probability API.
`scripts/dev/prepare-qwen-reranker.py` prepares the yes/no head from compatible Qwen
weights, following [llama.cpp's conversion](https://github.com/ggml-org/llama.cpp/blob/620d2225c/conversion/qwen.py).
A GGUF already converted for ranking can be used directly. Generation and embedding
providers remain independent of the local reranker.

```bash
python3 -m venv .artifacts/rerank-venv
.artifacts/rerank-venv/bin/pip install gguf==0.19.0 numpy==2.5.4
.artifacts/rerank-venv/bin/python scripts/dev/prepare-qwen-reranker.py \
  /path/to/source-reranker.gguf /path/to/ranking-reranker.gguf

MEMSTATION_RERANK_GGUF=/path/to/ranking-reranker.gguf \
MEMSTATION_RERANK_ALIAS=local-ranker \
bash scripts/dev/run-reranker.sh
```

Then set the MemStation deployment:

```dotenv
MEMSTATION_RERANK_MODEL=rerank_compatible:local-ranker
MEMSTATION_RERANK_BASE_URL=http://127.0.0.1:18082
MEMSTATION_RERANK_API_KEY=
```

The launcher binds loopback and uses an 8192-token context with one inference slot. Context, port, bind address and binary path are configurable in the script. Start the reranker before MemStation when testing. The [llama.cpp endpoint](https://github.com/ggml-org/llama.cpp/blob/620d2225c/tools/server/README.md#post-reranking-rerank-documents-according-to-a-given-query) supplies the Jina-compatible wrapper when `documents` is used.

## Bounds and failures

The default candidate multiplier is 6, minimum 20, maximum 50. Change these in System settings or `MEMSTATION_RERANK_CANDIDATE_MULTIPLIER`, `MEMSTATION_RERANK_MIN_CANDIDATES`, `MEMSTATION_RERANK_MAX_CANDIDATES`.

Calls use the existing provider timeout, retry and telemetry path. Unknown/duplicate indexes, missing fields, nonfinite/out-of-range scores and incomplete batches cannot partially modify ranking. Provider failures fall back to the original candidate scores. Cancellation of the caller remains cancellation. Engine diagnostics and trace attributes distinguish `disabled`, `skipped`, `applied`, and `degraded`; they contain fixed error codes, not upstream response bodies or credentials.

## Scoring

There is one scoring contract. A complete, validated reranker batch supplies query
relevance. Final score is relevance multiplied by importance clamped to
`[0.9, 1.1]`, time adjustment `0.9 + 0.1 × time_decay`, and the stale-fact penalty.
A true zero relevance stays zero. Conversation and supersession signals remain
visible for explaining candidate admission, but are not late relevance bonuses.
Historical validity, explicit time filters and authorization still run before ranking.

Without a configured model, or when inference fails validation, bounded hybrid
relevance is `clamp(max(dense, lexical, graph), 0, 1)` with the same bounded
secondary factors. Invalid batches apply atomically: they never partly overwrite
scores. Components identify `relevance_v1` or `hybrid_v1` according to the signal
actually used. These are diagnostic labels, not configurable alternate policies.
Clear the model reference to disable reranking. Removed settings are rejected.

## Candidate selection and evaluation

Candidate collection, model admission and final Top N are separate. Successful
inference admits only the fully scored pool; invalid batches preserve first-stage
results. Dense and lexical channels each reserve a third of the bounded model pool
before preliminary-score filling. The `preliminary` selection remains available
for ablation. This is channel reservation, not RRF or BM25.

Relevance scores are not calibrated probabilities of answer correctness. Validate
candidate coverage, final ranking, evidence packing, answer quality and latency
separately, keeping failed/degraded requests in the expected denominator. See the
[evaluation guide](../apps/server/tests/eval/README.md). Per-run measurements and
verification records belong in local ignored archives.

To reproduce against a private pipeline artifact, run from `apps/server`:

```bash
MEMSTATION_RERANK_PILOT_SOURCE=/absolute/path/to/pipeline.json \
MEMSTATION_RERANK_TEST_URL=http://127.0.0.1:18082 \
MEMSTATION_RERANK_TEST_MODEL=local-ranker \
MEMSTATION_RERANK_TEST_OUTPUT=/absolute/path/to/frozen-rerank.json \
go test ./tests/eval -run '^TestLiveRerankFrozenCandidates$' -v -count=1
```

The test skips unless explicitly configured. It exports per-question results, scores, latency and the parent artifact hash; it does not enforce benchmark quality thresholds in CI. Keep original LOCOMO data and full artifacts outside version control.
