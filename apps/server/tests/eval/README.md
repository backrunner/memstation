# MemStation Eval

This directory holds the retrieval and ranking benchmark foundation for MemStation v1.

Datasets
- `testdata/repo_native_golden.json`: small repo-distributed golden dataset used by local smoke runs.
  It now includes multimodal text-proxy fixtures for image OCR/caption, PDF parsing, audio transcription, and tool-output-derived evidence metadata.
  It also includes generic agent-memory retrieval cases for `event`, `task`, `procedure`, `artifact`, `entity`, and `agent_action`.
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
- Repo-native smoke: `./scripts/eval/run-memory-smoke.sh`
  This always runs in explicit `deterministic` embedding mode for offline regression smoke.
- External LOCOMO run: `./scripts/eval/run-memory-locomo.sh`
  By default this expects `provider` embedding mode and requires `EMBEDDING_MODEL`, `EMBEDDING_BASE_URL`, and `EMBEDDING_API_KEY`.
  Override the dataset source with `LOCOMO_DATASET_PATH` or `LOCOMO_DATASET_URL`.
- External LongMemEval run: `./scripts/eval/run-memory-longmemeval.sh`
  By default this expects `provider` embedding mode and uses `longmemeval_s_cleaned.json` at `session` granularity.
  Override the dataset source with `LONGMEMEVAL_DATASET_PATH` or `LONGMEMEVAL_DATASET_URL`, and granularity with `LONGMEMEVAL_GRANULARITY=session|turn`.
- LongMemEval QA auto-eval: `./scripts/eval/run-memory-longmemeval-qa.sh path/to/hypotheses.jsonl`
  This expects `LONGMEMEVAL_QA_JUDGE_MODEL`, `LONGMEMEVAL_QA_JUDGE_BASE_URL`, and `LONGMEMEVAL_QA_JUDGE_API_KEY`.
- Nightly bundle: `./scripts/eval/run-memory-nightly.sh`
- Worker recovery smoke: `./scripts/ops/run-worker-recovery-smoke.sh`
- Worker recovery nightly: `./scripts/ops/run-worker-recovery-nightly.sh`

Embedding modes
- `deterministic`: local deterministic vectors, only for smoke/regression and offline manual runs. This is not a real embedding-quality benchmark.
- `provider`: OpenAI-compatible `/embeddings` evaluation using the configured model/endpoint/key. Use this for accuracy-sensitive local, manual, or dedicated benchmark runs.

Evaluation contract
- Accuracy claims must use provider embeddings and an explicit temporal/user split. Deterministic embeddings are only a code-path smoke test.
- Retrieval is reported with Recall/Precision/Hit at k, MRR, graded nDCG, stale-fact exposure, negative leakage, wrong-user leakage, and relation-ablation deltas. Duplicate IDs earn no extra relevance but still occupy their original ranks. nDCG uses linear relevance gain and an ideal ranking of all positive judgments up to k, even when fewer results are returned.
- LOCOMO indexes raw dialogue only: annotated `event_summary`, upstream `observation`, and `session_summary` are excluded. All turns in one sample share one benchmark principal; speakers and session dates remain available as context. This is evidence-turn retrieval, not the official end-task QA score.
- LongMemEval uses the cleaned V1 session/turn evidence track for retrieval diagnostics and official-style QA prompts (including abstention) for end-task accuracy. Gold `has_answer` and question-type annotations stay outside indexed memory. QA rejects duplicate reference/hypothesis IDs before calling the judge; reports include coverage and missing IDs so partial submissions cannot look like full evaluations.
- Graph retrieval uses bounded, bidirectional semantic neighbourhood expansion. The first hop has no additional distance penalty; edge weights and relation factors still apply. Deeper hops decay, zero-weight edges do not propagate, and contradiction/replacement/supersession edges do not promote stale history. The full vs `full_no_relations` comparison is diagnostic and should be read together with leakage and stale-fact rates.
- Official-source review, benchmark versions, and remaining external validation scope: [2026-09-09 review](REVIEW-2026-09-09.md).

CI policy
- GitHub CI does not run retrieval/ranking benchmark jobs and does not use Recall, nDCG, or benchmark latency as redlines.
- Benchmark scripts still write artifacts for local, manual, or dedicated eval environments.
- Hard benchmark enforcement is explicit opt-in via `MEMSTATION_ENFORCE_BENCHMARKS=true`; benchmark latency thresholds also require `MEMSTATION_PERF_P95_MS`.
- Full repo-native and performance benchmark tests are skipped by default in `go test`; opt into diagnostic execution with `MEMSTATION_RUN_BENCHMARKS=true`. Parser, metric, isolation, and runner contract tests remain blocking correctness tests.

Next step
- See `reliability_benchmark_plan.md` for the proposed source-replay, rebuild-equivalence, activation-safety, and injection-reliability benchmark layers.

Artifacts
- `aggregate.json`: aggregate metrics by variant
- includes `embedding.mode`, `embedding.model`, and provider endpoint host metadata
- `results.json`: per-query ranked results with score breakdowns
- `failures.json`: failure samples for misses, leakage, or stale hits
- `report.md`: human-readable summary
- `perf.json`: retrieval latency smoke report when enabled
