# Memory Reliability Benchmark Plan

This document defines the next evaluation layer above the existing retrieval/ranking smoke suite in `apps/server/tests/eval`.

## Current Baseline

The repository already has a strong retrieval-focused benchmark foundation:

- repo-native golden dataset for CI smoke
- LOCOMO adapter for external retrieval evaluation
- ranking ablation across `semantic_only`, `semantic_importance`, `full_no_relations`, and `full`
- artifact export for aggregate metrics, per-query results, failures, and score breakdowns
- smoke gates for recall, MRR, nDCG, wrong-user leakage, stale fact top-k, and relation expansion gain

That baseline is necessary, but it is not sufficient to answer the fuller product question:

> "Can we trust the memory system to stay correct after ingestion, extraction, rebuild, activation, and injection?"

The missing piece is an end-to-end reliability benchmark that starts from retained source materials and validates the entire memory lifecycle.

## Reliability Dimensions

The benchmark suite should explicitly score five reliability dimensions.

### 1. Retrieval Correctness

This is the current baseline:

- relevant memories are recalled
- stale or superseded facts are demoted
- wrong-user leakage stays at zero
- relation expansion helps when expected

### 2. Pipeline Correctness

Starting from source snapshots and finalized conversations, the system should materialize the expected:

- summaries
- facts
- impressions
- key memory updates
- relations
- embeddings

This layer verifies extraction and write-path correctness rather than only top-k recall.

### 3. Rebuild Determinism

Given the same retained source snapshots, same structure version, and same embedding model, a rebuild should be functionally equivalent to the current active generation.

This does not require byte-identical IDs across generations, but it does require equivalent:

- normalized summary/fact/impression content
- relation topology
- retrieval behavior
- leakage and stale-fact behavior

### 4. Activation Safety

Switching generations should preserve or improve retrieval quality for unaffected conversations and must never leak other users or drop unrelated retained conversations.

### 5. Injection Reliability

The memory injected into model requests should stay relevant, bounded, and safe:

- relevant memories are included within token budget
- stale or negative memories are excluded
- key memories remain stable in top injection slots

## Proposed Benchmark Layers

### Layer A: Retrieval and Ranking

Keep the existing runner as the lowest-cost CI gate.

Use cases:

- smoke on every PR
- nightly regression on repo-native plus LOCOMO

Existing metrics:

- `Recall@1/3/5/10`
- `Precision@3/5`
- `Hit@1/3/5`
- `MRR`
- `nDCG@5/10`
- `wrong-user leakage`
- `stale fact top-k`
- `relation expansion gain`

### Layer B: Materialization Benchmark

Add a new harness that starts from source snapshots instead of prebuilt memory documents.

Per case:

1. seed retained source snapshots
2. run the real summary/analysis/embedding pipeline into a target generation
3. collect produced documents, relations, key memories, and embeddings
4. compare them against expectations

Required per-case expectations:

- expected summary content fragments
- expected fact IDs or fact content fragments
- expected impression fragments
- expected key memory keys and values
- expected relations
- forbidden stale facts
- forbidden wrong-user entities

Suggested artifact outputs:

- `materialization.json`
- `materialization_failures.json`
- `materialization_report.md`

### Layer C: Rebuild Equivalence Benchmark

Add a generation-aware benchmark that uses the same retained source snapshots to build:

- baseline generation `A`
- rebuilt generation `B`

Then compare:

- document coverage by logical type and conversation
- normalized content similarity for summary/fact/impression outputs
- relation overlap
- embedding count coverage
- retrieval metrics when searching `A` vs `B`
- key memory stability before and after activation

Suggested metrics:

- `generation_doc_coverage`
- `generation_relation_coverage`
- `generation_embedding_coverage`
- `rebuild_equivalence_recall_delta`
- `rebuild_equivalence_mrr_delta`
- `rebuild_equivalence_ndcg_delta`
- `rebuild_content_stability`
- `activation_regression_rate`

Recommended first gate:

- same-model rebuilds must keep retrieval deltas within a narrow tolerance
- no increase in wrong-user leakage
- no increase in stale fact top-k rate

### Layer D: Injection Reliability Benchmark

Run real `memory.inject` on query cases with explicit token budgets.

Per case verify:

- injected memories contain required positives
- injected memories omit negatives and stale facts
- key memories appear when expected
- injection stays under the configured token budget

Suggested metrics:

- `injection_recall_at_budget`
- `injection_key_memory_hit_rate`
- `injection_negative_leak_rate`
- `injection_stale_leak_rate`
- `injection_token_budget_violation_rate`

## Dataset Design

Use three dataset families.

### 1. Repo-Native Golden

Keep this small, deterministic, and CI-friendly.

Add more cases for:

- superseded facts
- multi-conversation rebuild preservation
- manual key memory persistence across generations
- segment-heavy conversations with `IncludeSegments=false`
- injection budget pressure

### 2. Source-Replay Golden

New dataset format driven by retained snapshots.

Each case should contain:

- user id
- conversation id
- normalized messages
- expected derived outputs
- rebuild expectations
- activation expectations

This dataset is the foundation for Layer B and Layer C.

### 3. LOCOMO External

Keep LOCOMO for external retrieval pressure-testing, not as the only truth source.

LOCOMO remains useful for:

- broader retrieval difficulty
- relation-heavy recall
- nightly drift detection

It is not sufficient for:

- MemStation-specific rebuild safety
- key memory semantics
- activation safety
- injection token-budget behavior

## Recommended CI and Nightly Split

### PR / CI

- existing retrieval smoke on repo-native golden
- small source-replay materialization smoke
- one same-model rebuild equivalence smoke
- one injection reliability smoke

### Nightly

- full repo-native retrieval suite
- external LOCOMO run
- larger source-replay suite
- cross-generation rebuild equivalence suite
- activation regression suite
- performance smoke for search and rebuild

## Implementation Path

Implement in this order.

### Phase 1

Extend the current retrieval smoke dataset and runner.

Deliverables:

- more retrieval edge cases
- generation-aware candidate vs active comparisons
- stronger wrong-user and stale-fact gates

### Phase 2

Add a `source-replay` dataset format and a `RunMaterializationBenchmark(...)` harness.

Deliverables:

- retained snapshot fixtures
- real worker-orchestrated derivation
- expectation matching for summaries, facts, impressions, relations, key memories

### Phase 3

Add `RunRebuildEquivalenceBenchmark(...)`.

Deliverables:

- baseline vs rebuilt generation comparison
- activation regression checks
- rebuild-specific artifacts

### Phase 4

Add `RunInjectionBenchmark(...)`.

Deliverables:

- request-level injection fixtures
- token-budget assertions
- negative/stale leak assertions

## Recommended First Acceptance Bar

For the first reliable-memory benchmark gate, require all of the following:

- wrong-user leakage stays at `0`
- stale fact top-3 rate does not regress
- same-model rebuild equivalence keeps `Recall@3`, `MRR`, and `nDCG@5` within a small tolerance
- conversation rebuilds never affect unrelated user-wide retrieval
- injection benchmark shows zero negative leaks in the repo-native suite

## Why This Fits the Current Codebase

This plan fits the current architecture because the repository already has:

- generation-aware memory reads
- retained source snapshots
- rebuild workers
- a reusable retrieval benchmark runner
- artifact writers and smoke gates

The main missing work is not foundational infrastructure. It is the addition of:

- source-replay datasets
- end-to-end harnesses
- generation comparison metrics
- injection-specific assertions
