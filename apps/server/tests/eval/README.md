# MemStation Eval

This directory holds the retrieval and ranking benchmark foundation for MemStation v1.

Datasets
- `testdata/repo_native_golden.json`: small repo-distributed golden dataset used by CI smoke.
- `testdata/locomo_fixture.json`: tiny LOCOMO-shaped adapter fixture used only for parser smoke.
- `testdata/longmemeval_fixture.json`: tiny LongMemEval-shaped adapter fixture used only for parser smoke.
- Raw LOCOMO data is not committed to this repository.
- Raw LongMemEval data is not committed to this repository.

LOCOMO policy
- MemStation uses a `LOCOMO-first` benchmark method, but the raw upstream dataset stays external.
- As of 2026-03-15, the upstream LOCOMO dataset is distributed under `CC BY-NC 4.0`.
- Only the adapter, schema expectations, runner, and download instructions belong in the repo.

LongMemEval policy
- MemStation uses LongMemEval as a first-class external benchmark for long-term memory.
- Prefer the cleaned release announced by the authors in September 2025.
- MemStation supports both evidence retrieval/ranking benchmarking and the official-style QA auto-eval prompt flow.
- For retrieval parity with the official repo, abstention questions are skipped because they do not have positive evidence locations.
- QA auto-eval keeps abstention questions and judges `question_id + hypothesis` files against the raw LongMemEval reference data.
- Embedding-model leaderboards such as MTEB, BEIR, and MIRACL are optional provider bake-off inputs only. They are not MemStation long-memory release gates.

Commands
- Repo-native smoke: `./scripts/eval-memory.sh`
  This always runs in explicit `deterministic` embedding mode for CI-safe regression smoke.
- External LOCOMO run: `./scripts/eval-memory-locomo.sh`
  By default this expects `provider` embedding mode and requires `EMBEDDING_MODEL`, `EMBEDDING_BASE_URL`, and `EMBEDDING_API_KEY`.
  Override the dataset source with `LOCOMO_DATASET_PATH` or `LOCOMO_DATASET_URL`.
- External LongMemEval run: `./scripts/eval-memory-longmemeval.sh`
  By default this expects `provider` embedding mode and uses `longmemeval_s_cleaned.json` at `session` granularity.
  Override the dataset source with `LONGMEMEVAL_DATASET_PATH` or `LONGMEMEVAL_DATASET_URL`, and granularity with `LONGMEMEVAL_GRANULARITY=session|turn`.
- LongMemEval QA auto-eval: `./scripts/eval-memory-longmemeval-qa.sh path/to/hypotheses.jsonl`
  This expects `LONGMEMEVAL_QA_JUDGE_MODEL`, `LONGMEMEVAL_QA_JUDGE_BASE_URL`, and `LONGMEMEVAL_QA_JUDGE_API_KEY`.
- Nightly bundle: `./scripts/eval-memory-nightly.sh`
- Worker recovery smoke: `./scripts/worker-backlog-smoke.sh`
- Worker recovery nightly: `./scripts/worker-backlog-nightly.sh`

Embedding modes
- `deterministic`: local deterministic vectors, only for smoke/regression and offline CI. This is not a real embedding-quality benchmark.
- `provider`: OpenAI-compatible `/embeddings` evaluation using the configured model/endpoint/key. Use this for accuracy-sensitive local and nightly benchmark runs.

Next step
- See `reliability_benchmark_plan.md` for the proposed source-replay, rebuild-equivalence, activation-safety, and injection-reliability benchmark layers.

Artifacts
- `aggregate.json`: aggregate metrics by variant
- includes `embedding.mode`, `embedding.model`, and provider endpoint host metadata
- `results.json`: per-query ranked results with score breakdowns
- `failures.json`: failure samples for misses, leakage, or stale hits
- `report.md`: human-readable summary
- `perf.json`: retrieval latency smoke report when enabled
