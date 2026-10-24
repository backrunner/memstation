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
- Real source replay: `./scripts/eval/run-source-replay.sh`
  Requires a dedicated loopback MemStation service with serve/worker/scheduler running,
  `MEMSTATION_EVAL_BASE_URL`, `MEMSTATION_ADMIN_TOKEN`, and `CHAT_MODEL`. Creates new
  synthetic `eval-` principals, finalizes real gateway conversations, waits for summary
  and analysis, then compares default injection, derived text only, and no memory.
  Reports use all expected questions as the denominator, including pipeline failures.
  Derived-only context excludes source segments, evidence quotes, and source metadata.
- Persistent HTTP load: `./scripts/eval/run-store-load.sh`
  Also requires `MEMSTATION_EVAL_DATABASE_URL` and embedding provider variables. The
  migrated database must be on loopback and named `memstation_eval_*` or
  `memstation_quality_*`; the HTTP service must use that same database. Defaults to
  10,000 repeated synthetic rows and 12 offered RPS with concurrency caps 1/4/8.
  Use `MEMSTATION_EVAL_RPS=0 MEMSTATION_EVAL_REQUESTS=32` for closed-loop concurrency;
  the report records actual peak in-flight requests. Warmup is excluded. Timings cover
  HTTP request execution, excluding the client's wait for a concurrency slot.
  Set `MEMSTATION_PERF_P95_MS=250` to explicitly enforce the latency target after
  writing the report. This is a capacity fixture, not 10,000 independent accuracy cases.

Both new runners retain synthetic source data in the evaluation database, revoke their
temporary client keys on completion, and write private reports under `.artifacts/`.
They do not reset databases or Redis. Keep credentials in environment variables and
keep external benchmark data and raw reports outside Git. Relative output paths are
resolved from `apps/server`; prefer an absolute output directory.

Embedding modes
- `deterministic`: local deterministic vectors, only for smoke/regression and offline manual runs. This is not a real embedding-quality benchmark.
- `provider`: OpenAI-compatible `/embeddings` evaluation using the configured model/endpoint/key. Use this for accuracy-sensitive local, manual, or dedicated benchmark runs.
  Provider failures fail these in-process quality/performance runs even if the online
  engine can fall back to lexical retrieval. Latency smoke preserves all query flags
  used by the accuracy runner and exports individual samples and result IDs.

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
- [2026-10-25 real-model results](../../../../docs/memory-quality-2026-10-25.md)
  record passing smoke thresholds and the remaining external recall, extraction, and
  concurrency limitations. Source replay is implemented; rebuild equivalence and full
  activation/injection safety evaluation remain in `reliability_benchmark_plan.md`.

Artifacts
- `aggregate.json`: aggregate metrics by variant
- includes `embedding.mode`, `embedding.model`, and provider endpoint host metadata
- `results.json`: per-query ranked results with score breakdowns
- `failures.json`: failure samples for misses, leakage, or stale hits
- `report.md`: human-readable summary
- `perf.json`: retrieval latency smoke report when enabled
