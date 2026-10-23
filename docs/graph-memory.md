# Graph memory retrieval and provenance

MemStation retains PostgreSQL, `MemoryNode`, `MemoryRelation` and the `VectorStore` abstraction. Migration `202609170001_graph_retrieval.sql` adds fact history, reversible entity mappings and ranked adjacency indexes. Run the versioned migration before starting the updated server and workers. These auxiliary tables, triggers and partial indexes are owned by Atlas SQL migrations, like source snapshots and evidence tables.

## Bounded traversal

Online retrieval defaults to two hops, 24 edges per node, 256 total edges, 128 candidate nodes and a 150 ms graph-fetch budget. The SQL repository applies directional adjacency limits before returning rows. Direct seeds are sorted before the node budget is applied. A graph timeout retains direct retrieval and marks returned records with `metadata.graph_timeout`; parent request cancellation still cancels the request.

Deployment settings (prefix `MEMSTATION_MEMORY_RELATION_`):

| Suffix | Default | Maximum |
| --- | --- | --- |
| `MAX_EDGES_PER_NODE` | 24 | 64 |
| `MAX_EDGES` | 256 | 1024 |
| `MAX_NODES` | 128 | 512 |
| `TIMEOUT_MS` | 150 | 1000 |

Invalid or nonpositive budgets fall back to safe defaults. Existing hop configuration remains available. The time budget covers graph fetching and traversal, not the embedding provider or the complete request.

Search accepts `relation_mode`: `neighborhood` (default), `evidence`, `dependencies`, `dependents`, or `conflicts`. Dependency and causal relations preserve their direction. Conflict mode can recall contradictory evidence; ordinary neighborhood expansion does not promote contradictions, replacements or superseded facts. Edge confidence and a bounded-neighborhood degree penalty affect graph contribution. The strongest path wins, preventing repeated derivations from accumulating artificial corroboration. `metadata.recall_path` exposes the seed, traversed nodes, relation IDs/types and contribution. Ranking continues through the existing importance and time-decay scorer.

## Historical facts

Memory REST and Admin REST search accept RFC3339 `as_of` and `known_at`. GraphQL uses `asOf` / `knownAt`; gRPC and `recall_memory` expose the corresponding fields. Manager provides local date-time controls and converts them to timestamps.

```json
{
  "query": "home location",
  "as_of": "2026-02-15T00:00:00Z",
  "known_at": "2026-03-01T00:00:00Z",
  "top_n": 10
}
```

`as_of` selects the fact's half-open validity interval `[valid_from, valid_to)`. `known_at` selects the recorded version visible at that time. Omitting `known_at` uses current knowledge about the requested historical instant. Omitting `as_of` retains current-fact validity semantics while restricting recorded knowledge. Explicitly dated late arrivals are inserted between chronological predecessor/successor facts. Content-only manual edits preserve the inferred validity start. Supersession and correction snapshots are recorded in the same database transaction as the fact write.

Historical search returns **fact snapshots**, not snapshots of every memory type or the entire graph. It does not hydrate current relations over historical results. It considers at most 512 historical candidates, using lexical retrieval and the existing scorer/reranker; `as_of`-only searches can also use the configured vector signals. `known_at` searches omit live-vector signals so later content changes cannot influence historical knowledge through their embeddings. Returned records carry `historical_snapshot`, `as_of`, `known_at` and `recorded_at` metadata. Opening a Manager result shows the current editable record.

History starts at migration: existing facts are backfilled as current knowledge at that point. The service cannot reconstruct unrecorded earlier versions. Deleting the owning node deletes its history; generation retention also bounds retained history. This feature is not an immutable compliance archive.

## Evidence

Grounding accepts verbatim spans from user statements and tool observations and records message/part locators, byte offsets, exact quotations and stable source IDs. An assistant assertion alone is not user evidence. Summary references that cannot be matched to the specific claim remain labeled `context_reference` / `model_inference`, with lower weight. They remain useful for source inspection but do not count as independent supporting evidence. Relations record supporting evidence/source IDs. Repeated derivations of one source do not become multiple confirmations.

The current deterministic grounding step is conservative: paraphrases can remain inferred even when a human would recognize support. Precision of provenance does not establish that a user's statement or a tool's output is objectively true.

## Entity names and reversible merges

Canonical entity identity includes an optional `entity_scope` from conversation metadata. Within a scope, administrators can propose and review alternate names or connect two entity records. Candidate mappings have no retrieval effect. Confirmed mappings connect their original neighborhoods; source records and evidence remain intact. Revoking a mapping removes that retrieval connection.

`GET /api/admin/memory/entity-aliases?user_id=...&generation_id=...` lists mappings. `PUT` on the same endpoint accepts:

```json
{
  "generation_id": "gen:current:USER_ID",
  "scope": "work",
  "alias": "记忆站",
  "source_node_id": "OPTIONAL_SOURCE_ENTITY_NODE",
  "target_node_id": "TARGET_ENTITY_NODE",
  "status": "candidate",
  "reason": "Operator supplied project nickname"
}
```

Use the actual generation returned by the API. Omit `source_node_id` for a name-only alias. Review by resubmitting the same scoped alias and target with `confirmed` or `revoked`. The authenticated admin route scopes the operation by query `user_id`; a body cannot override it. Endpoint types, users and generations are validated. Ambiguous confirmations, cycles and conflicting source mappings are rejected. PostgreSQL serializes review within a user/generation and commits its audit record atomically. Manager entity details provide candidate creation and confirmation/revocation dialogs.

Aliases currently match normalized exact query text within the supplied `entity_scope`; they do not automatically resolve arbitrary pronouns or fuzzy names. Name similarity alone never confirms a merge. Mappings are generation-scoped and must be reviewed for a new generation.

## Validation and evaluation

Storage contract tests run against both in-memory and PostgreSQL repositories and cover high-degree graphs, direction, budgets, user/generation isolation, cancellation, ambiguous/cyclic aliases, revocation, historical knowledge, late arrivals and manual corrections. Engine tests cover timeout fallback and historical hydration isolation. Grounding tests cover exact spans and shared source identity. Manager fixture E2E covers historical filters and mapping review/revocation.

The native evaluation dataset now includes historical validity, a scoped Chinese alias, contradictory evidence and a high-degree node. Run `scripts/eval/run-memory-smoke.sh` locally to export per-query results, score components, aggregate metrics and failures. The deterministic smoke run observed zero wrong-user, negative and stale-fact leakage, but no positive aggregate relation-expansion gain. This verifies execution and provides diagnostics; it is not evidence of improved semantic recall or production latency. Provider-embedding evaluation and production-scale load measurement remain separate work.
