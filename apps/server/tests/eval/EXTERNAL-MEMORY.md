# LongMemEval and MemoryArena evaluation

These manual, provider-backed evaluations exercise the production `MemoryEngine`, `VectorStore`, memory repository and configured cross-encoder. They are excluded from normal CI and nightly benchmark gates. They use isolated in-memory stores; they do not contact an application database or prove persistent ingestion/worker correctness.

## Declared tracks

| Dataset | Inputs and execution | Measures |
| --- | --- | --- |
| [LongMemEval cleaned V1-S](https://github.com/xiaowu0162/LongMemEval) | Six questions per capability group plus six abstention questions by default; every history session is indexed, including distractors. Original full-session retrieval, real embeddings, cross-encoder, bounded whole-record context, plain-text reader. | QA reference agreement with upstream task-specific rubrics; retrieval Recall/MRR/nDCG on answerable questions; all-evidence-session hit and recall after context admission. |
| [MemoryArena formal reasoning](https://github.com/ZexueHe/MemoryArena/tree/6cd9de14b71915e39ac742a20dc33785e14b6aab) | Complete math and physics task chains in original order. Each task has independent state. Only the current question/background and previously generated own question/solution pairs are available. | Final-subtask success, macro progress per task, micro subtask accuracy, step accuracy, latency and usage; separate paired no-memory runs. |

LongMemEval-S V1 is not LongMemEval-M, oracle, or V2. The default 42-question selection is a diagnostic sample, not a full 500-question result or a pristine holdout. Selection uses offline capability labels and a fixed hash seed; neither labels nor reference evidence enter retrieval or reading. LongMemEval abstention remains in QA denominators and is excluded only from retrieval metrics that require positive evidence.

The MemoryArena agent is a declared single-call solver with configured thinking, not the upstream multi-call reasoning-tool agent. Retrieval uses the current question text; the current public background is provided to the solver rather than appended to the retrieval query. Memory is a raw-experience RAG backend through MemStation, without summary/fact extraction workers. Its reference judge's equivalence prompt is retained, including the upstream environment's literal `Question: None`; output is strictly parsed as yes/no. Agent/model/judge differences must be reported; scores are not directly comparable to a published baseline. Shopping, progressive search and group travel are not covered by these formal tracks and require their own environments and evaluators.

Both tracks use the same reader/judge settings in memory and no-memory conditions. No-memory runs always receive an empty memory context, including later arena subtasks. Current public background remains available in both conditions. References, grades, task labels and future subtasks/backgrounds are never inserted into memory. Wrong own solutions remain in memory; judge outcomes do not control writes. There is no selected-case retry, answer cache or successful-row splicing. Provider retries/output continuation inherited from the existing adapters must remain visible through usage and errors.

## Prepare and run

Dataset revisions are pinned in `scripts/eval/prepare-memory-external.py`. Its default selection contains six samples from each of six non-abstention types and six abstentions. It downloads the full official math/physics files and writes a private plan with source hashes. Supply a previously downloaded cleaned S file to avoid downloading it again:

```sh
python3 scripts/eval/prepare-memory-external.py \
  --output .agents/archive/external-inputs \
  --arena-baseline-run /absolute/path/completed-arena-low-run \
  --longmemeval-path /absolute/path/longmemeval_s_cleaned.json
```

Configure these environment variables through a private local environment, without committing secrets:

- `MEMSTATION_EVAL_EXTERNAL_PLAN`: absolute path to prepared `plan.json`.
- `MEMSTATION_EVAL_CODE_REVISION`: exact tested source revision; archive the binary/source fingerprint for dirty-tree runs.
- `MEMSTATION_EVAL_EXTERNAL_CACHE`: private embedding computation cache directory outside the fresh run directory.
- `MEMSTATION_EVAL_EMBEDDING_REVISION`: installed model weight digest.
- `EMBEDDING_MODEL`, `EMBEDDING_BASE_URL`, `EMBEDDING_API_KEY`: actual provider embedding model and endpoint.
- `RERANK_MODEL`, `RERANK_MODEL_REVISION`, `RERANK_BASE_URL`, and optional `RERANK_API_KEY`.
- `ANSWERER_MODEL`, `ANSWERER_BASE_URL`, `ANSWERER_API_KEY`, `ANSWERER_TEMPERATURE`, `ANSWERER_REASONING_EFFORT`.
- `JUDGE_MODEL`, `JUDGE_BASE_URL`, `JUDGE_API_KEY`, `JUDGE_TEMPERATURE`, optional `JUDGE_REASONING_EFFORT`.

Use a thinking value the configured model actually supports; matching names across models do not establish equal compute. Pin sampling explicitly, including temperature zero, and record the locally served runtime version. Ollama cloud is remote inference even when accessed through a local endpoint.

```sh
bash scripts/eval/run-memory-external.sh /absolute/path/to/a/fresh/run-directory
```

The run preallocates every expected row before inference. Each job has its own checkpoint file; all errors and unexecuted rows remain visible. The default selection plus complete formal tracks means 964 expected answers across the two conditions. Memory and no-memory conditions never share generated answers. Embedding computations can be reused only for identical content/model/revision; the cache contains neither answers nor judgments. Local rerank inference is limited to one request at a time; the configurable `rerank_timeout_seconds` defaults to 180 and starts after this queue. Search latency includes queueing. Cloud readers can still run concurrently. Preserve an interrupted infrastructure attempt and restart the whole declared matrix in a fresh directory; never reuse only successful answer rows.

## Artifacts and interpretation

Keep plans, external data, raw answers/judgments, source contexts, caches, manifests and generated summaries under ignored `.agents/archive/`. Source records enter the prompt with request-local labels and original session dates; persistent record IDs, `has_answer`, references and question-type metadata are excluded. Whole source records are packed into a 64,000-byte context budget by default; omitted records are counted. LongMemEval starts with 4,096 output tokens and formal reasoning with 8,192. The existing production `CompleteWithBudget` helper retries explicit output truncation once with the identical prompt: 4,096→8,192 or 8,192→16,384. Judge calls start at 4,096 and have the same truncation policy. These plan fields are initial request budgets, not hard total-token caps; usage sums both attempts and final exhaustion remains an error. The policy is uniform across conditions and is not a correctness-based retry. These are explicit configuration, not official leaderboard defaults.

Report expected/completed/judged/error counts separately. LongMemEval official session recall uses a per-question Boolean `recall_all@k` (every evidence session found), which differs from average fractional `Recall@k`; label both explicitly. Repository nDCG uses its standard logarithmic discount and must not be labelled identical to the upstream `ndcg_any` implementation. Audit duplicate session IDs when mapping record-level metrics to official session metrics. Retrieval success before context packing and evidence survival after packing are different measurements. Keep every selected question, failed generation and failed task chain in the relevant denominator. For arena, final-step success differs from solving every step, macro progress differs from micro accuracy, and unequal chain lengths must not silently change the metric. Missing/unexecuted final steps are failures. A zero no-memory score is not proof of absence of benchmark contamination. Model-family self-judging is not independent factual calibration.

External data remains outside the repository regardless of redistribution terms. LongMemEval's official repository declares MIT; MemoryArena's [official dataset card](https://huggingface.co/datasets/ZexueHe/memoryarena) declares CC BY 4.0. The MemoryArena code preview and external task environments have separate licensing/dependency boundaries; no upstream source package is vendored by this adapter.

The summary command `python3 scripts/eval/summarize-memory-external.py --run /absolute/run` verifies the original source hashes and refuses missing or reduced case files. The shell runner invokes it even after an execution failure while preserving the original nonzero exit code. It writes `reports/summary.json` and `reports/failures.json`. Paired differences with identical context hashes, including arena first steps without history, are reported separately as generation/judging variation rather than evidence of a memory effect. One run per condition does not establish statistical significance.

## Continue an interrupted journal

The runner seals its full case journal after the Go process exits, including all successful, incorrect and failed outcomes. Never seal a process that is still writing. When supervising the test binary directly, run `python3 scripts/eval/seal-memory-external.py --run /absolute/prior/run --process-exit-code <actual-exit-code>` after the process has stopped.

For a continuation, keep the exact plan and provider configuration, set `MEMSTATION_EVAL_EXTERNAL_PARENT=/absolute/prior/run`, and invoke the shell runner with a fresh output directory. The default process deadline is 12 hours, adjustable with `MEMSTATION_EVAL_EXTERNAL_TIMEOUT`. The continuation verifies the sealed case hashes, every original question/reference, input hashes, reader/judge/model settings, retrieval settings, prompt hashes and source policy. It refuses missing cases, reordered checkpoints or changed protocols.

Every attempted outcome remains unchanged. An in-flight call interrupted before its checkpoint is explicitly failed without retry; only literal `not executed` steps receive their first model call. Previous complete own solutions are restored into the isolated raw-memory store, including wrong solutions and solutions whose judge failed; partial reader outputs and grading feedback are excluded. A fully resolved task performs no new model calls. All expected tasks and failures remain in the child journal, and the child manifest records parent hashes and code revision. This is execution-journal continuation, not taking successful rows from different complete runs or rerunning failures until they pass. Record the binary/source fingerprint for each process; cloud sampling and weights remain subject to the same stated limitations.

Select an independent track with `prepare-memory-external.py --tracks memoryarena` or `--tracks longmemeval`; the default `all` prepares both. Arena-only plans omit LongMemEval and never execute its completed phase again. The Arena agent identifier is `single_call_no_external_tools`; its reader thinking level is configured explicitly through `ANSWERER_REASONING_EFFORT` and recorded in the manifest. MemoryArena `none` is a model-only control, not an engine optimization target. Reuse the completed low control with `--arena-baseline-run`; the runner pins hashes and model identity, retains all original failures, and performs no reader/judge calls for those rows. Historical usage is excluded from current spending, and changed prompt/token policies prevent same-budget causal claims. Missing or mismatched controls fail before provider work. Only a separately intended new-model experiment should use the explicit `--create-arena-baseline` option. Preserve prior batches and their provenance; do not selectively refresh wrong answers.
