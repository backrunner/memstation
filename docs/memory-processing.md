# Memory processing and usage

MemStation supports two extraction engines under one source, scope and storage
contract. The deployment policy is explicitly `generative` (default),
`lightweight`, or `hybrid`. Server and worker roles must use the same configuration.
An unavailable encoder or LLM fails its task; neither silently switches engines.

## Processing policies

All policies index retained original text before narrative summarization and
semantic analysis. Source indexing materializes message parts without calling a
generation, media-extraction or scoring model. Embedding tasks run independently.
Raw segments use explicit neutral importance/decay values of 1, with
`score_reason=neutral_source_policy`; these are ranking defaults, not semantic
importance judgments. LLM-derived records retain their validated model scores.

| Policy | Local encoder | Generative processing |
| --- | --- | --- |
| `generative` | Always used for source entity indexing | Existing summary, direct-original-source analysis and Key Memory consolidation |
| `lightweight` | Exact entity mentions and a routing classification | Not requested |
| `hybrid` | Same grounded observations | Model signal, unsupported attachment input or a reproducible audit sample promotes the original conversation |

Lightweight records are `entity` observations whose `interpretation` is
`source_mention`. They preserve the exact source quote, code-point SourceRef and
UTF-8 evidence offsets. They do not assert user preferences, completed events,
actor identity, canonical entity equivalence or fact supersession. Assistant
messages are not admitted as user evidence. Relations and durable semantic facts
remain in the validated generative path; no keyword/template fact fallback is
restored. Recognized names never become Actor IDs through string matching. In lightweight
mode, explicit Key Memory writes preserve supplied/default scores and do not
invoke the scoring LLM; this does not create inferred facts.

The encoder receives only source text windows and the configured inference
identity, not persistent user/workspace IDs, benchmark IDs, answers or labels.
Windows have at most 512 code points with 64-code-point overlap, at most 256
windows per request. Overflow fails explicitly after original indexing. Responses
are limited to 1 MiB, and checkpoints to 4 MiB. Returned model revision,
splitter, spans, confidence ranges and record counts are validated before writes.
Overlap mentions are deduplicated by exact original source position.

The hybrid signal threshold is configurable and **not a calibrated probability
of semantic correctness**. An empty entity list alone never suppresses promotion.
Audit sampling is deterministic and scope/source/policy bound. The semantic text
budget applies to the promoted original conversation, retaining neighboring
speaker turns and dates rather than sending only extracted names. A budget block
is `deferred_budget`; a nonselected hybrid conversation is `deferred`.
Lightweight mode is `not_requested`. These states are distinct from successful
semantic processing. The default stays generative until a workload-specific
paired evaluation justifies another policy.

Original indexing records `source_index_status=stored`; this means relational
source records exist, not that every asynchronous vector has completed. Model
calls, indexing jobs and semantic progress are separate. Operational task replay
can retry failures after correcting configuration. Explicit generation rebuilds
also use the configured policy and immutable retained snapshots; historical data
is not automatically deleted or rebuilt when changing policy.

Validated local extraction checkpoints are persisted with first-writer-wins
semantics. Their identity includes the exact source revision/content, generation,
model/weight revision, splitter, schema version, threshold and audit/budget policy.
Replay validates persisted spans again. Source checkpoints are storage internals,
excluded from public conversation and memory metadata. Source deletion and
generation disposal continue through existing lifecycle controls.

## Local encoder service

The local `apps/extractor` service implements the version-1 authenticated
`POST /extract` (source extraction and routing) and `POST /entities`
(query spans only) contracts. Query extraction skips the semantic-routing
classifier. It contains model inference only; Go owns validation,
authorization, promotion, persistence and task scheduling. It serializes inference
through a bounded FIFO and rejects queue overflow or expired waits, keeping
worker retry/backpressure explicit. Queries require `X-Memstation-Timeout-Ms`
with the caller's remaining timeout (1–5,000 ms). Their queue wait is limited to
half that budget and at most 100 ms; Go still enforces the total deadline. Its bind
address defaults to loopback. It does not log source text or credentials.

Two installation profiles are provided, with a frozen `uv.lock`:

```sh
cd apps/extractor
uv sync --locked --extra gliner
# Or, in a separate environment:
uv sync --locked --extra uie
```

Set the following environment variables before running `.venv/bin/python server.py`:

| Variable | Meaning |
| --- | --- |
| `MEMSTATION_EXTRACTOR_BACKEND` | `gliner` or `uie` |
| `MEMSTATION_EXTRACTOR_MODEL` | Configured model reference; no runtime model is hardcoded |
| `MEMSTATION_EXTRACTOR_REVISION` | Immutable 40-character HF commit for GLiNER; `sha256:<manifest hash>` for UIE |
| `MEMSTATION_EXTRACTOR_SPLITTER` | `char` or `whitespace`; must match the server policy |
| `MEMSTATION_EXTRACTOR_TOKEN` | Shared service credential |
| `MEMSTATION_EXTRACTOR_HOST` / `PORT` | Defaults `127.0.0.1` / `8092` |
| `MEMSTATION_EXTRACTOR_DEVICE` | GLiNER device; defaults `cpu` |
| `MEMSTATION_EXTRACTOR_MODEL_PATH` | Required local UIE model directory |

GLiNER uses `AutoExtractor` and a pinned Hugging Face snapshot, supporting span
and boundary checkpoints. The current UIE dependency graph requires Linux x86_64 (its `tool-helpers` dependency
has no macOS/ARM wheel); other platforms are explicitly rejected rather than
silently using another engine. Use a Linux x86_64 environment or an amd64 Docker
build for this profile. UIE uses the small-model PaddleNLP Taskflow API, not
generative PP-UIE. For UIE, provide a complete local model directory containing
`memstation-manifest.json`: a JSON object mapping every reviewed model/config/
tokenizer artifact filename to its SHA256. Set the revision to the SHA256 of the
manifest's exact bytes. Startup verifies artifact hashes and path containment.
Review each selected weight's license independently from the library license.

For the Go service, set:

| Variable | Default / requirement |
| --- | --- |
| `MEMSTATION_MEMORY_PROCESSING_STRATEGY` | `generative` |
| `MEMSTATION_MEMORY_PROCESSING_ENTITY_RECALL_TIMEOUT_MS` | `200`; query extraction plus indexed lookup/posting budget, maximum `5000` |
| `MEMSTATION_MEMORY_PROCESSING_ENDPOINT` | Required encoder base URL for all processing strategies |
| `MEMSTATION_MEMORY_PROCESSING_TOKEN` | Required; same credential as the encoder |
| `MEMSTATION_MEMORY_PROCESSING_MODEL` | Required; matches the encoder |
| `MEMSTATION_MEMORY_PROCESSING_REVISION` | Required; matches pinned weights |
| `MEMSTATION_MEMORY_PROCESSING_SPLITTER` | `char` |
| `MEMSTATION_MEMORY_PROCESSING_THRESHOLD` | `0.75` |
| `MEMSTATION_MEMORY_PROCESSING_AUDIT_RATE` | `0.1` |
| `MEMSTATION_MEMORY_PROCESSING_MAX_SEMANTIC_BYTES` | `131072` source-text bytes |

Model/splitter support does not establish language quality. In particular, test
Chinese variants, negation, quoted authors, plans and later corrections before
selecting a default. Missing media cannot become text-derived visual facts.

Entity-assisted query retrieval is a mandatory part of every ingestion strategy.
See [entity recall](entity-recall.md) for configuration, bounds and diagnostics.

The local encoder runs one inference at a time. Valid source requests can wait
in FIFO order for up to 10 seconds, with at most eight waiting requests. This
absorbs ordinary worker contention without increasing model calls or weakening
source-span validation. Overflow or queue expiry returns `503 busy` with
`Retry-After: 1`; the worker retains its bounded retry/failure contract. Query
entity requests do not wait behind source jobs and keep their short caller
deadline. Invalid requests are rejected before queue admission. HTTP rejection
status codes are retained in Go task errors; response bodies, source text and
credentials are never included in those errors. Queueing does not guarantee
completion under sustained overload or a failed encoder.

## Usage panel and API

Open a workspace and select **Usage**. The SSR/BFF page shows UTC date filters,
stage, strategy, model, provider outcome, daily tokens, stage mean duration,
job-retry call counts and paginated call records. Audience partitions retain the
same scoped authorization checks as individual ledger reads.

`GET /api/v1/usage/summary` and
`GET /api/admin/workspaces/{workspace}/lifecycle/usage/summary` accept RFC3339
`since`/`until` (half-open interval), `stage`, `strategy`, `status`, and `model`.
The report defaults to the last 30 days and accepts at most 366 days. The
corresponding `/usage` list accepts the same filters plus `after`/`limit`.
Reports aggregate the whole interval, never just the visible page. `usage.read`
is required; private calls are visible only when the caller can read all of their
participating partitions.

GraphQL exposes `memoryUsageReport` and filtered `memoryBackgroundUsage` under
`WorkspaceScope`. gRPC `AIUsageRequest` carries the same filters; set
`include_report=true` to return the aggregate report with the records. Both data
and administrative transports share the same service.

The ledger now covers direct summary/analysis/scoring/evidence generation,
embedding and rerank calls, broker calls, and local extraction. Each provider
adapter invocation has its own record, including output-truncation retries.
Provider/SDK-internal network attempts are not claimed as separately observed.
Job attempt numbers identify requests from retried tasks. A `complete` provider
outcome means a response was returned, not that downstream memory validation
succeeded. Use job status for processing failures.

Tokens come from returned usage, never token limits or request-length estimates.
Absent/zero-only usage is conservatively unreported. Local encoders have no
generation-token billing. Costs are only reported upstream charges, summed as
exact decimals per currency; missing prices remain unpriced. Direct calls do not
receive invented API prices. Broker amounts remain references, never additional
charges in MemStation's token-usage billing. Host amortization, storage and
downstream reader spend are not inferred from this panel.

The versioned migration `202610270001_memory_processing_usage.sql` adds ledger
stage, strategy, duration and usage-known fields. Existing historical rows retain
unrecorded strategy/stage metadata; positive recorded token counts are marked known,
without inventing missing usage, strategy or historical prices.

## Dependencies and validation boundaries

Top-level inference dependencies were checked against PyPI when added:
GLiNER2 `2.1.0`, Hugging Face Hub `2.3.0`, PaddleNLP `2.8.1`, PaddlePaddle `3.3.1`.
Profiles are separate and the lockfile pins transitives. Python `3.12.13` is an
explicit compatibility choice within the GLiNER2 documented/tested 3.10–3.12
range. Transitive constraints such as NumPy 1.x follow the inference libraries;
do not upgrade them independently without a runtime comparison.

The automated contract/provenance tests use deterministic fixtures, not model
quality claims. `TestLiveLightweightSourceIndex` can explicitly exercise a real
local encoder using `MEMSTATION_TEST_EXTRACTOR_ENDPOINT`, `TOKEN`, `MODEL` and
`REVISION` variables with that same prefix. It checks actual Worker storage and
source attribution without invoking an LLM. Model selection, router calibration,
joint summary/extraction and reader-cost ablations remain manual paired
experiments, outside default CI/nightly benchmark gates. Do not infer achieved
cost savings or corpus quality from a transport smoke test.

The memory preview uses the same deployment processing policy, including the hybrid
semantic budget. Lightweight preview emits only source mentions; it does not call
the generative pipeline. Preview calls are recorded with a `preview.` stage prefix.

UIE classification uses PaddleNLP's [enumerated schema contract](https://paddlenlp.readthedocs.io/en/stable/_modules/paddlenlp/taskflow/information_extraction.html). The UIE adapter requires a separate Linux runtime validation; the local end-to-end smoke used GLiNER.
