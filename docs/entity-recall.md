# Entity-assisted memory retrieval

Entity recall adds a bounded name-index channel to the existing keyword, vector
and graph search pipeline. It can admit a linked record that was absent from the
direct candidate pool. It never creates facts, confirms aliases, assigns Actor
IDs or merges same-named people.

## Configuration

Entity recall is a mandatory part of memory retrieval and has no enable/disable
setting. Configure the pinned local encoder described
in [memory processing](memory-processing.md) with the same settings for server,
worker and scheduler roles. The effective defaults are:

```sh
MEMSTATION_MEMORY_PROCESSING_ENTITY_RECALL_TIMEOUT_MS=200
```

Compose includes the extractor in its normal service set. Original source indexing also records local mentions in generative
mode; its LLM summarization/analysis still runs. A missing encoder configuration
is rejected at startup. New conversations use the policy immediately after
restart. Use the existing generation-rebuild workflow to index retained older
sources; changing configuration does not automatically rebuild or delete data.
Configured source-extraction failures retry their task after raw indexing.

Migration `202610270002_entity_name_index.sql` gives current LLM entity records
the same explicit `canonical_name` metadata field already used by local mentions,
and adds a partial expression index. Distinct entity nodes and provenance remain
separate. No semantic name equivalence is inferred by the migration.

## Query path and bounds

1. Keyword/vector recall and local query extraction run concurrently.
2. `POST /entities` returns exact spans from the question, without executing the
   source-routing classifier. Only the question and configured model identity
   reach the encoder; workspace IDs, source IDs and benchmark metadata do not.
3. At most eight distinct person/organization/location/project/product/technical
   names with encoder confidence at least `0.5` are admitted. Support depends on
   the configured encoder labels; the bundled GLiNER profile currently emits
   person, organization, location and project among these categories.
4. SQL matches lowercased, outer-whitespace-trimmed names exactly. Each name admits
   at most 16 distinct entity nodes, at most 128 total. The count used for popular
   name downweighting includes only authorized, active records in the selected
   user/workspace/generation and query filters. An explicit `entity_scope` further
   restricts matches. No embedding-based name merge or fuzzy identity match runs.
5. A single bounded `mentions_entity` posting lookup admits linked source/semantic
   records. Other relation types are excluded. This lookup has at most 256 nodes,
   256 edges and 16 edges per root. The existing subsequent neighborhood expansion
   retains its own configured budgets and relation mode.

Input is limited to 512 code points. Oversized questions explicitly skip the
entity channel; they are not silently truncated. The configured time budget
bounds how long search waits for local inference, name lookup and posting lookup.
It does not preempt CPU inference: the encoder and usage-ledger finalization may
finish after cancellation, but late results cannot change that search response. Local model unavailability
or timeout produces explicit degradation diagnostics while current keyword/vector
search continues. Scope/authorization failures fail the whole search. A query
without extracted names is an ordinary `no_entities` outcome.

The encoder serializes inference through a bounded FIFO. Interactive queries may
wait for at most half the remaining caller budget, capped at 100 ms, so brief
overlap does not immediately discard the entity channel. The client sends the
remaining timeout in `X-Memstation-Timeout-Ms` (1–5,000 ms); `/entities` rejects
requests without this bounded deadline. Queue overflow or a longer wait returns a busy
response. The caller's total timeout remains authoritative, and disconnected
queued requests do not start inference. This adds neither retries nor model calls.

The query-only process cache has a two-minute TTL and 256-entry ceiling. Its key
includes the exact question, model/policy identity, user, workspace and audience
partitions. Only extracted names are cached, never matching memory IDs, posting
lists, answers or retrieval results. Every request checks current memory access
again, including deletion and publication revocation. Cache hits make no model
call. Concurrent misses may each call the encoder; no cross-request inference
coalescing is claimed.

## Ranking and diagnostics

`entity_score` is a retrieval relevance signal, not confidence in a fact. A name
match starts at `0.65 / sqrt(1 + log(occurrences))`; larger posting lists, edge
weights and edge confidence can reduce a linked record's contribution. Repeated
mentions never accumulate as independent corroboration. The strongest keyword,
vector, entity or graph signal is used before importance/time/staleness factors
under `hybrid_v2`. A validated reranker remains authoritative for final relevance
under `relevance_v1`; entity evidence does not get added again after reranking.

REST and gRPC search results expose `entity_recall` with `status`, `cache_hit`,
`query_entities`, `matched_nodes`, and `latency_ms`. GraphQL exposes `entityRecall`.
States include `ablation`, `no_entities`, `complete`, `unavailable`,
`timed_out`, and `budget_exceeded`. Historical/as-of reads do not consult this
mutable live entity index. Matches appear in record metadata as `matched_entities`;
the debug score breakdown retains `entity_score` separately from keyword/vector
and relation contributions. The recall trace flags entity-channel degradation.

Actual local query calls are recorded as `entity.query.extract`, with the
configured strategy and local model revision, and appear in workspace **Usage**.
This channel makes no additional entity-embedding requests; ordinary query and
document embeddings retain their existing accounting. Local CPU/hosting costs
are not estimated as zero API charges.

## Validation and evaluation

Production LOCOMO measurements require `complete` or `no_entities` entity recall.
Missing, unavailable, timed-out or budget-exceeded channels are execution failures,
even when a degraded live search can still return useful records. Such failures
remain in the denominator, skip reader/judge generation, and cannot authorize a
quality-candidate replacement.

Contract tests cover admission beyond the direct pool, same-name separation,
confirmed aliases, scope/audience/generation boundaries, deletion, bounded fanout,
query cache reuse, timeout and fail-closed authorization. The opt-in
`TestLiveLightweightSourceIndex` also exercises a real encoder through source
indexing, query extraction, SQL-style name matching in the in-memory repository,
posting lookup, ranking and cache reuse. PostgreSQL has separate real-store
contract tests.

Use `full_no_entities` to ablate the newly added channel while retaining the
pre-existing reviewed-alias behavior and ordinary graph recall. Corpus evaluations
must fix inputs, model revisions, budgets and samples before execution, and report
recall/ranking, stale facts, audience leakage, P95 and total reader/write cost.
Mechanism tests and a real-model smoke do not establish corpus quality or achieved
monetary savings. External memory benchmarks remain manual/specialized runs.
