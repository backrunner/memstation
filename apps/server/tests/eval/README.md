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
- Raw MemoryArena data is not committed to this repository. The [external memory guide](EXTERNAL-MEMORY.md) describes paired LongMemEval-S and sequential MemoryArena math/physics evaluations, fixed input selection, model budgets, error denominators and protocol limits.

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
- LongMemEval-S + MemoryArena formal tracks: prepare an immutable plan with `python3 scripts/eval/prepare-memory-external.py`, then run `bash scripts/eval/run-memory-external.sh /absolute/fresh/output` with the private provider environment described in [EXTERNAL-MEMORY.md](EXTERNAL-MEMORY.md). This invokes real retrieval and reading with paired no-memory controls; it does not re-run summary/fact ingestion workers or cover the shopping/search/travel environments.
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
- The raw LOCOMO track directly indexes turns; it does not execute production LLM
  memory extraction. `full` names the ranking formula, not end-to-end ingestion.
  Coverage reports source/excluded/eligible/scored questions and unresolved evidence;
  partially unresolved queries are scored against only their resolvable references.
  `evalrun -locomo-include-captions` enables a separately named, opt-in track with
  explicitly marked machine captions. Raw text remains the default.
- LongMemEval uses the cleaned V1 session/turn evidence track for retrieval diagnostics and official-style QA prompts (including abstention) for end-task accuracy. Gold `has_answer` and question-type annotations stay outside indexed memory. QA rejects duplicate reference/hypothesis IDs before calling the judge; reports include coverage and missing IDs so partial submissions cannot look like full evaluations.
- Graph retrieval uses bounded, bidirectional semantic neighbourhood expansion. The first hop has no additional distance penalty; edge weights and relation factors still apply. Deeper hops decay, zero-weight edges do not propagate, and contradiction/replacement/supersession edges do not promote stale history. The full vs `full_no_relations` comparison is diagnostic and should be read together with leakage and stale-fact rates.
- Official-source review, benchmark versions, and remaining external validation scope: [2026-09-09 review](REVIEW-2026-09-09.md).

CI policy
- GitHub CI does not run retrieval/ranking benchmark jobs and does not use Recall, nDCG, or benchmark latency as redlines.
- Benchmark scripts still write artifacts for local, manual, or dedicated eval environments.
- Hard benchmark enforcement is explicit opt-in via `MEMSTATION_ENFORCE_BENCHMARKS=true`; benchmark latency thresholds also require `MEMSTATION_PERF_P95_MS`.
- Full repo-native and performance benchmark tests are skipped by default in `go test`; opt into diagnostic execution with `MEMSTATION_RUN_BENCHMARKS=true`. Parser, metric, isolation, and runner contract tests remain blocking correctness tests.

Validation scope
- Source replay, rebuild equivalence and activation/injection safety should be
  evaluated separately; see `reliability_benchmark_plan.md` for the methodology.
- Store per-run narratives, aggregate JSON and verification captures in ignored
  local agent archives. Keep raw model replies, private configuration and datasets
  in ignored runtime storage. Public documentation describes methods, not run logs.

Artifacts
- `aggregate.json`: aggregate metrics by variant
- includes `embedding.mode`, `embedding.model`, and provider endpoint host metadata
- `results.json`: per-query ranked results with score breakdowns
- `failures.json`: failure samples for misses, leakage, or stale hits
- `report.md`: human-readable summary
- `perf.json`: retrieval latency smoke report when enabled

Manual LOCOMO input-policy diagnosis (run from `apps/server`):

```sh
go run ./tests/eval/cmd/locomodiag \
  -dataset /absolute/private/locomo.json \
  -out /absolute/private/locomo-diagnosis \
  -cache /absolute/private/locomo-vectors \
  -model-revision PINNED_EMBEDDING_DIGEST
```

Supply `EMBEDDING_MODEL`, `EMBEDDING_BASE_URL`, and `EMBEDDING_API_KEY` through
the environment. `-sample-id` optionally selects a predefined development pilot.
The runner compares raw queries, one fixed query instruction, and instruction plus
marked captions using real provider vectors and the production MemoryEngine.
It exports 40 candidates per query but computes headline case metrics on the
first 10; `candidate_recall_at_40` is a separate diagnostic. `diagnosis.json` also
contains category aggregates and coverage. Cached vectors are keyed by endpoint,
model, declared revision and exact input, and must never be used for latency claims.
Keep output/cache directories private and ignored by Git. These are diagnosis
results on observed data, not blind holdout or answer-quality measurements.


Manual LOCOMO production ingestion + QA:

```sh
# Create and migrate a NEW loopback PostgreSQL database first.
# Its name must start with memstation_eval_ or memstation_quality_.
export MEMSTATION_EVAL_DATABASE_URL='postgres://.../memstation_eval_locomo_RUN'
export LOCOMO_DATASET_PATH=/absolute/private/locomo.json
export MEMSTATION_EVAL_PREFIX=eval-locomo-RUN
export MEMSTATION_EVAL_CODE_REVISION=EXACT_SOURCE_REVISION_OR_FINGERPRINT
export EMBEDDING_REVISION=PINNED_EMBEDDING_DIGEST
# Supply MODEL, BASE_URL and API_KEY for EMBEDDING, EXTRACTION, ANSWERER, JUDGE.
# A pilot still imports the complete source history of the selected sample.
export MEMSTATION_EVAL_SAMPLE_ID=conv-26
export MEMSTATION_EVAL_QUESTIONS_PER_CATEGORY=3
bash scripts/eval/run-locomo-pipeline.sh /absolute/private/locomo-pipeline-RUN
```

Unset the two pilot variables to evaluate all samples and all category 1–4 QA,
including questions without evidence labels. Category 5 is excluded explicitly.
The three tracks share the same answerer, judge, prompts, top-20 cap and 16,000-byte
context cap by default. This is a UTF-8 byte cap, not an equal token count; actual
answer/judge token usage is recorded. QA starts at 4,096 output tokens. Explicit
truncation permits one bounded retry, with twice the initial budget or 8,192,
whichever is larger, capped at 16,384. Both attempts contribute to recorded usage.

- `raw`: individual original turns, indexed by the existing in-memory production
  MemoryEngine harness with real embeddings.
- `derived`: production Summary/Analysis/Embedding Worker output in PostgreSQL,
  without source segments/conversation expansion.
- `combined`: the same persistent engine with source segments and conversation
  expansion enabled. Whole records are packed in rank order; oversized records
  are skipped rather than silently truncating negation or attribution.

Select tracks with `MEMSTATION_EVAL_MODES` (CLI `-modes`). Three additional tracks
are explicitly diagnostic and never enter the memory ingestion/search path:

- `question_only`: the same reader receives the question and an empty context.
- `full_history`: all original dated, speaker-attributed turns, in chronological
  order. The independent `-oracle-context-bytes` safety limit defaults to 512,000;
  exceeding it fails the question instead of silently truncating the baseline.
- `gold_evidence`: source turns selected by benchmark evidence IDs, optionally
  expanded within their session by `-oracle-neighbor-turns` (default zero).
  Missing or unresolved evidence fails this diagnostic but remains in its QA
  denominator. It is not a production retrieval score or an absolute ceiling.

A controls-only or raw-only run needs no database or extraction model. For example:

```sh
export MEMSTATION_EVAL_MODES=question_only,full_history,gold_evidence
export MEMSTATION_EVAL_ORACLE_NEIGHBOR_TURNS=1
bash scripts/eval/run-locomo-pipeline.sh /absolute/private/locomo-controls-RUN
```

To use the configured production reranker in **both** raw and persistent engines,
provide `RERANK_MODEL`, `RERANK_BASE_URL`, `RERANK_REVISION` and, if required by the
provider, `RERANK_API_KEY`. The model uses the production provider prefix, for
example `rerank_compatible:<model-id>`. CLI flags expose the candidate multiplier,
minimum/maximum, blend weight and timeout. An empty model disables reranking;
there is no implicit dependency on the live server's environment or database.
The manifest records these settings and an endpoint hash without credentials.
Each retrieval answer and mode summary records applied/skipped/disabled/degraded
status. A fallback is visible and must not be reported as successful reranking.

`-rerank-candidate-selection balanced` (default) reserves one third of the model
pool for dense candidates and one third for lexical candidates, counting overlap
toward both reservations, then fills by preliminary score. `preliminary` disables
these reservations for an ablation. Successful reranking restricts final results
to the fully scored pool; failure preserves the complete first-stage fallback.
If requested `top_n` exceeds the inference cap, reranking is explicitly skipped
instead of silently shrinking the page or exceeding the configured model budget.
The candidate prefetch uses at least the inference budget, preserves the wider
four-times final-result prefetch, and caps each direct channel at 200. Disabling
the model retains the same candidate-budget configuration. The shell wrapper
exposes `MEMSTATION_EVAL_CANDIDATE_SELECTION`, `MEMSTATION_EVAL_CANDIDATE_MULTIPLIER`,
`MEMSTATION_EVAL_MIN_CANDIDATES` and `MEMSTATION_EVAL_MAX_CANDIDATES`.
Replay persists both human speakers as user-origin material, preserving speaker
and original session date in each turn. It calls real finalization/outbox relay
and Worker handlers through a persistent synchronous local task queue. It does
not measure HTTP gateway overhead, Redis delivery, concurrent workers or the
production SLO. Raw and persistent search latency use different stores and must
not be presented as a fair speed comparison. Personal-only scope remains intact.

A database-wide advisory lease and an output-directory lease reject concurrent runners. Resume requires the
same source/model/budget manifest, database and checkpoint directory; use a fresh
database, directory and prefix for changed experiments. Each queued task has at
most three attempts per invocation and remains checkpointed on failure. Failed
or unexecuted questions stay in the expected denominator and the process exits
nonzero. No model response is replaced by a rule-generated memory.

Protocol v2 freezes every answer attempt, including failures. Intent is persisted
before model work, so a crashed attempt remains interrupted rather than being
silently retried. Resume executes only missing attempts; a fresh run is required
to repeat failed questions. Prompt hashes, context hashes, question identity and
manifest identity protect checkpoint reuse. The provider's existing bounded
transport/output-truncation recovery remains part of one declared attempt.
Remote model IDs returned by answerer/judge are recorded; they do not attest to
immutable remote weights. Older v1 checkpoint directories cannot be mixed into v2.

`pipeline.json` contains stage completion, all expected answers, per-mode accuracy
and coverage, token usage, retrieval score breakdown and errors. Stage files and
`answers/` retain original/derived material and exact contexts for private audit.
`citation_recall` measures whether packed records cite gold source turns; it is
not proof that a derived fact preserves the answer. Summaries do not claim every
turn in their source session as evidence. Raw corpus, contexts, references,
checkpoints and model responses must remain private and ignored by Git.

Reports also include per-conversation/per-category denominators and paired
comparisons against full-history/gold controls when those tracks are run together.
The conditional retention metric counts how many reference-correct questions the
retrieval track also answers correctly; independently correct retrieval answers
are reported separately. Pair counts exclude failed judgments, while the main
accuracy denominator continues to include all expected questions.

The answerer never sees reference answers. Retrieval tracks never use evidence
labels; only the explicit gold diagnostic uses them to select source context. Only the judge sees
the reference answer. Using the same model for answering and judging must be
reported as a self-judged diagnostic, not an independent or vendor-comparable
leaderboard score. Keep measured limits and run records in ignored local archives.

The synthetic contract `TestLOCOMOPipelinePostgresReplayResumeAndLease` accepts
`MEMSTATION_PIPELINE_TEST_DATABASE_URL` pointing to a dedicated empty migrated
loopback database. It verifies actual PostgreSQL/Worker replay, no gold leakage,
exact resume, exclusive lease and manifest rejection with a local fake provider.
It is a correctness test, not a model-quality benchmark.


To audit a revised judge without changing ingestion, retrieval or any answer:

```sh
cd apps/server
go run ./tests/eval/cmd/locomopipeline \
  -rescore-report /absolute/private/original/pipeline.json \
  -out /absolute/private/rescored-v2.json \
  -code-revision EXACT_RESCORE_SOURCE_REVISION
```

Supply `JUDGE_MODEL`, `JUDGE_BASE_URL`, and `JUDGE_API_KEY`. The parent must have
complete ingestion and all expected answers; the output must be a new file.
Every answer is rejudged, the original report stays unchanged, and the new
manifest records its SHA-256 and separate judge revision. Failures remain in the
denominator. The v2 judge measures agreement with the required reference facts;
short references are not treated as exhaustive proof against every extra detail.
This does not independently verify factual grounding, and ambiguous inference
questions still require sensitivity analysis or a separate human/independent audit.

The explicit rescore-only flag `-rescore-judge-policy alternatives_v3` distinguishes
alternative acceptable answers to singular/hypothetical questions from factual
enumerations requiring multiple items. The default remains `reference_v2`.
Rescore complete comparison groups into new files; retain both protocols and do
not selectively rejudge failures. This policy changes scoring, not retrieval or
answers, and is not an official LOCOMO evaluator.

For a source-pinned external judge-prompt diagnostic, the manual test
`TestLOCOMOFrozenJudgeProtocolProbe` accepts `MEMSTATION_EVAL_JUDGE_PROBE_PLAN`
(private JSON with `reports`, `system`, `user_template`, `source_url`,
`source_sha256`) and `MEMSTATION_EVAL_JUDGE_PROBE_OUT` (a new private file), using
the existing `JUDGE_*` model configuration. Templates use `{question}`,
`{golden_answer}` and `{generated_answer}` once each. It freezes parent/answer/
context hashes and records all failures and provider outputs. The companion
`TestLOCOMOJudgeProbeReparse` reads `MEMSTATION_EVAL_JUDGE_PROBE_REPARSE` and writes
a new output without model calls. Keep raw outputs, aggregate JSON and per-run analysis in ignored local archives.
This is protocol sensitivity, not execution of the other memory engine.

### Selected-cohort replay with current retrieval

`TestLiveLOCOMOEvidenceReplay` is a manual diagnostic for rerunning a declared
cohort against the current engine without repeating extraction. Derived/combined
tracks require an isolated snapshot of the evaluation database; raw-only replay
does not connect to a database. Verify the restored
table contents against the source, then set that database's
`default_transaction_read_only=on`; the test checks this setting and rejects
mixed-user or incomplete source corpora. The regular evaluation URL restrictions
still apply. Do not point it at the application database.

Supply `MEMSTATION_EVAL_EVIDENCE_REPLAY_PLAN`,
`MEMSTATION_EVAL_EVIDENCE_REPLAY_OUT`, `MEMSTATION_EVAL_CODE_REVISION`,
`MEMSTATION_EVAL_DATABASE_URL` and the usual embedding, reranker, answerer and
judge environment variables, then run:

```bash
go test ./tests/eval -run '^TestLiveLOCOMOEvidenceReplay$' -count=1 -v -timeout=56m
```

The private JSON plan contains `sample_id`, `dataset_path`, `dataset_sha256`,
`parent_sha256`, optional `recent_sha256`, `database_snapshot_sha256` for database tracks, the parent
`manifest`, and `rows`. Each row contains a `baseline` PipelineAnswer and a
`group` of `previous_failure`, `success_control` or `evaluation`. Select and freeze the rows
before observing new outcomes; question IDs, text, references and categories are
validated against the dataset. Use the same model revisions and context budgets
as the parent, include success controls, and retain execution errors in the
denominator. Repeats require a new output directory.

This runs fresh queries, current ranking/evidence assembly and new reader/judge
calls. Stored memory extraction, document embeddings and graph relations remain
fixed. The raw track rebuilds its turn index. Results measure recovery on the
selected subset, not overall LOCOMO accuracy or a fresh ingestion benchmark.
The plan manifest explicitly records `refine_evidence` and
`source_neighbor_turns`. Use identical model revisions and final context budgets
for both arms, and freeze transfer-cohort IDs before reading their outcomes.
Keep plans, snapshots, answers and reports in ignored private directories.

### Bounded evidence refinement

`MEMSTATION_EVAL_READER_POLICY=coverage` selects an alternative reader prompt;
`MEMSTATION_EVAL_REFINE_EVIDENCE=true` independently enables the shared engine's
LLM question-aspect planner and complementary evidence selector. Defaults remain `baseline` and `false`. Configure
`PLANNER_MODEL`, `PLANNER_BASE_URL`, `PLANNER_API_KEY` to use a separate planner;
otherwise it uses the answerer configuration. Diagnostic controls cannot be
combined with refinement. Use separate output directories for each arm.

The planner receives only the question. It returns one to eight requested aspects
and one or two complementary semantic queries, with no source-based sufficiency
stop. Each follow-up keeps the original authorization, workspace, private partition,
generation, memory types and temporal filters. Complete records from the initial
and follow-up pools are interleaved within a bounded candidate budget. Discovery
uses the authorized, scored candidate pool before the final result-page cutoff;
this does not enlarge the search prefetch or cross-encoder budget.

A second bounded LLM call chooses complementary evidence using the original
question, requested aspects and source content. Source labels are temporary and
opaque; every selected ID must belong to a submitted record and be cited by an
aspect check. Invented or malformed selections fail, and generated text never
becomes a memory. Selection prioritizes additions and retains the original
retrieval records in their original order. The evidence path first determines
which original records fit, protects those records, and uses remaining space for
discoveries and optional source context. This preserves source support, not a
guarantee that a stochastic reader's answer will stay correct. The selector has a
separate intermediate budget of twice the final context bytes, capped at 32KB;
the final context retains its declared byte limit.

The model-facing evidence formatter assigns temporary memory
and conversation labels before packing, so byte/token budgets cover delivered
content rather than long persistence IDs. Canonical source IDs remain in the
out-of-band packet mapping and retrieval trace.

The scored first-query pool is supplied through a typed `EvidenceSeed`; its
original page must match that pool before any model call. Up to three scored
pools of 50 records reach a bounded interleaved union, after which the selector's
byte budget applies. This avoids dropping first-query bridge facts before
selection. Evidence-format contexts also admit already-scored candidates by byte
budget, independently of public result pagination; this does not increase search
or rerank calls. These source-retention properties do not establish a QA gain.

`refinement.selection` records the validated checks;
`refinement.core` records selected record IDs, and `refinement.context_core`
reports any subsequent whole-record omissions.
`refinement.baseline` and `refinement.context_baseline` separately report retained
original records; selected discoveries that do not fit remain explicitly omitted.
Planner/selector usage is summed; follow-up rerank and complete evidence-ready
time are reported separately from
initial search time. Keep all errors in the denominator. This is an explicitly
invoked service operation, not a change to the default gateway path. Compare the
entire declared cohort and success controls before a promotion.

Oracle/full-history and question-only modes remain separately labelled diagnostics.
The former measures an empirical complete-input baseline, not a proven theoretical
upper bound; neither contributes to retrieval quality scores. Captions/images are a
separate input track, and references, gold evidence and categories never enter
planning, retrieval, selection or reader prompts.

### Frozen-context reading diagnostics

`TestLiveLOCOMOReaderReplay` compares reading policies using exact context bytes
from completed pipeline reports. It performs no retrieval, ingestion or database
access. Set `MEMSTATION_EVAL_READER_REPLAY_PLAN` and
`MEMSTATION_EVAL_READER_REPLAY_OUT` to absolute paths, with a fresh output
directory and `MEMSTATION_EVAL_CODE_REVISION` identifying the frozen source or
binary. Input report paths in the plan must also be absolute when using `go test`.

The plan contains `inputs` (objects with `path` and `sha256`), `policy`
(`coverage_inference`, `grounded` or `interpretation`), and `max_output_tokens`
(4096). The grounded
policy invokes the shared `memoryllm.ReadEvidence` operation: it produces an
aspect/item ledger with verbatim source quotations before the final answer.
Quotation presence is validated against the supplied evidence; this does not
prove semantic entailment or answer completeness. Invalid quotations, output
budget exhaustion and other failures remain in the denominator. The operation
does not persist generated items or change the default gateway reader.

The `interpretation` reader is a separately labelled protocol ablation. Personal
facts must still come from authorized memory; general language understanding,
calculation and background knowledge may interpret facts that are actually
present. This is not permission to supply missing personal events, identities,
preferences or locations. Run the entire cohort and separate empty-memory
controls; any change is a reader-policy result, not a retrieval improvement.

Ordinary QA readers return final answer text, with no JSON response-format request
or answer-object decoder. A nonempty natural-language answer must reach the
unchanged judge; only genuinely structured tasks request JSON. Empty responses,
transport errors and exhausted output budgets still fail. Previous JSON-wrapper
experiments remain separate, immutable reports: rerun complete cohorts after a
contract change instead of repairing or regrading old answers. The `grounded`
ledger experiment has its own structured schema because its output actually
contains typed evidence items.

The manual reader and evidence replay tests accept `ANSWERER_TEMPERATURE`,
`JUDGE_TEMPERATURE`, and (for evidence replay) `PLANNER_TEMPERATURE`. An explicit
zero is sent to the provider and recorded in the manifest; an unset value uses
the provider default. Pin sampling parameters before comparing policies and keep
earlier protocols separate. Temperature zero cannot guarantee a deterministic
remote model. Report ledger output costs, all-case accuracy, execution errors and
correct-to-wrong changes alongside gains.

Configured thinking levels are model-specific. Verify the selected runtime's
supported names before using them; a requested unsupported level may resolve to
the provider default. Comparing different effective efforts is a reader
configuration comparison, not an equal-compute model-only ablation. Raw answer
and judge responses, including the last partial response on output-budget
failure, are retained only in private evaluation artifacts for schema and
failure diagnosis. Keep all such errors in the reported denominator.

### Semantic episode extraction

`MEMSTATION_EVAL_SEMANTIC_EPISODES=true` (or the eval runner's
`-semantic-episodes=true`) enables experimental episodes in the production
Worker ingestion track. Apply the semantic-episode migration first. The flag
and extraction policy hash enter the manifest, so use a fresh output directory
and a dedicated loopback `memstation_eval_` / `memstation_quality_` database.
Raw retrieval does not exercise extraction; include derived / combined modes.

Set `EXTRACTION_REASONING_EFFORT` explicitly when investigating a thinking
model's output exhaustion; leave it empty to preserve provider defaults.
`ANSWERER_REASONING_EFFORT` and `JUDGE_REASONING_EFFORT` are separate controls
and enter the manifest, so never silently change them between quality arms.
The extraction setting also applies to the production replay's scorer.
Check supported values through the model provider rather than assuming that
`none` is reliable or that every model accepts the same effort names.

The manual `TestLiveSemanticEpisodeExtractionAndRecall` uses five
repository-owned synthetic sources with real configured models and embeddings:

```bash
MEMSTATION_EVAL_EPISODE_OUT=/absolute/path/to/fresh-output \
MEMSTATION_EVAL_CODE_REVISION=source-or-binary-sha256 \
go test ./tests/eval -run '^TestLiveSemanticEpisodeExtractionAndRecall$' -count=1 -timeout=30m
```

Configure the existing `EXTRACTION_*`, `ANSWERER_*`, `JUDGE_*` and `EMBEDDING_*`
environment variables. The default diagnostic indexes only episodes. To compare
all derived types using the exact saved extraction responses, set
`MEMSTATION_EVAL_EPISODE_REPLAY_SOURCE=/parent/report.json` and
`MEMSTATION_EVAL_EPISODE_ALL_DERIVED=true`, with a fresh output directory.
Replay validates source and prompt hashes; extraction costs remain historical.
Both arms regenerate reader/judge outputs. Outputs, failures and raw model replies
stay private. This test is skipped by default and reports functional behavior,
not LOCOMO quality or a CI quality gate. Configuration and source semantics are
described in [LLM memory generation](../../../../docs/llm-memory.md).

For a source-only diagnosis of one external session, run
`TestLiveSemanticEpisodeLongSession` with `MEMSTATION_EVAL_EPISODE_SESSION_OUT`
(fresh directory), `MEMSTATION_EVAL_LOCOMO_PATH`, `MEMSTATION_EVAL_SAMPLE`,
`MEMSTATION_EVAL_SESSION`, `MEMSTATION_EVAL_CODE_REVISION`, and `EXTRACTION_*`.
Optionally set `MEMSTATION_EVAL_EPISODE_CONTEXT_REPORT` to a frozen pipeline
report: only Facts preceding the selected session are supplied as context;
answers and gold references never enter extraction. The diagnostic regenerates
summary and extraction, saves source/policy fingerprints, calls, usage and
failures, and produces no QA score. It does not resume or repair that parent run.
To freeze the summary for an effort comparison, set
`MEMSTATION_EVAL_EPISODE_FROZEN_SUMMARY` to a successful session probe report;
only its `summary.generate` output is reused, with a parent hash and historical
cost flag. The new report's elapsed time then covers analysis only.
Enabled episodes use at most twelve source pieces / 6,000 source-text bytes per
analysis batch; summary and default extraction retain their original batching.
Production analysis persists validated batch checkpoints before advancing.
Failed later batches do not publish a partial result or cause earlier validated
batches to be regenerated on retry. Record task attempts, failures and phase timing
with each run; final success does not mean that no retries occurred.

`TestLiveLOCOMOSourceClockReplay` is a manual frozen-candidate ablation. Set
`MEMSTATION_EVAL_CLOCK_REPLAY_PARENT` to a completed evidence-format pipeline
report, `MEMSTATION_EVAL_CLOCK_REPLAY_OUT` to a fresh directory,
`MEMSTATION_EVAL_DATABASE_URL` to its dedicated evaluation database, and the
existing reader/judge credentials and code fingerprint. Explicit session dates
are read only at matching source revisions; reference answers do not select
documents or supply the dates. The control context must equal the parent text.
All arms rerun answers and strict alternatives_v3 judgments, keep failures in
the denominator, and retain invalid model replies as strings.

Set `MEMSTATION_EVAL_CLOCK_REPLAY_INFERENCE_ABLATION=true` for two fresh reader
arms with identical source-clock contexts: `coverage` and experimental
`coverage_inference`. The latter requests complete supported sets and qualified
inferences when asked. It is also available through `-reader-policy` on dedicated
pipeline runs. It does not modify the default gateway, stored facts or judge.
Report extraction/retrieval costs as historical and count unique questions
separately from the repeated raw/derived/combined contexts.

### Scoring evaluation

The pipeline uses the current production scoring contract described in
[reranking](../../../../docs/reranking.md). It does not expose switches to restore
retired scoring. Record signal source, candidate coverage, packing, answer quality
and failures separately. Developmental comparisons against retired implementations
are retained only in ignored local archives; their historical results do not
validate a changed current pipeline. Keep datasets and generated reports outside
tracked files.

### Local reranker diagnostic

See [reranking setup and diagnostic methods](../../../../docs/reranking.md). `TestLiveRerankFrozenCandidates` is explicitly opt-in and isolates provider ordering on saved raw-dialogue candidates. It does not replace MemoryEngine ranking ablations or end-to-end LOCOMO evaluation.

For a fixed failure-enriched, real-worker development cohort spanning LOCOMO, LongMemEval and MemoryArena, see [Mixed memory regression](REGRESSION.md). This is a manual diagnostic; it does not become a default CI gate.
