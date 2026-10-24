# Personal memory scope implementation

Date: 2026-10-24. Status: implemented locally as the next P1 increment. This extends the [conversation and worker scope](conversation-scope-2026-10-24.md); P1 and the complete workspace expansion remain unfinished.

## Behavior

Authorized Agents belonging to the same Personal workspace continue sharing its memory. Memory services, repositories and vector operations now reject mismatched owners, explicit unsupported workspaces and contexts bound to another workspace. Trusted internal callers without a scope retain the legacy Personal mapping. These checks validate data scope; transport authentication and capability checks remain required.

`MemoryDocument`, `KeyMemory`, relation/alias and vector DTOs carry typed workspace fields. Memory nodes and embeddings persist their workspace, while the current entity, keyword, evidence, history, alias and relation read paths derive visibility from their parent nodes. This is a transitional Personal-only implementation, not general workspace selection or private partitions.

The SQL paths apply Personal visibility before candidate limits, including direct ID/node reads, conversation memory, keyword lookup, fact history, Key Memory counts and graph adjacency. Both graph endpoints and both populated alias endpoints must be visible. Fact lineage selection and mutation exclude other workspaces. Missing or hidden objects do not become readable through ID-based access. Unsupported scope returns access denied rather than falling back to an owner-wide search.

Vector candidate SQL filters owner, workspace, generation, model and requested memory types before its limit. Matching node ownership, generation/type and readiness are also required. This fixes type filtering after candidate truncation, which could previously lose a visible Fact behind higher-scoring Summary candidates. Current and historical searches propagate scope denial instead of using a fallback retrieval path.

Key Memory caches use a versioned owner/workspace identity and validate the owner/workspace of cached rows before returning a hit. Invalid or old unscoped entries are refetched. Invalidation is confined to that identity; Agents sharing the Personal workspace share the cache. This does not add grant-revocation caching: authentication still checks current effective access separately.

## Write integrity

- Memory entity/node writes lock and validate existing identities, preventing an explicit entity or node ID from being rebound to another owner or workspace.
- Key Memory upsert rejects a hidden existing key rather than overwriting it. Evidence replacement/merge validates the requested parent node and explicit evidence owner/node fields before mutation.
- Vector upsert locks its parent node and commits the embedding and node-ready state in one transaction. Parent scope/type/generation mismatches fail; deleted parents and stale revision jobs do not recreate an embedding. Failure while setting readiness rolls back the vector write.
- In-memory storage validates the same Personal boundary for known records and uses unambiguous structured identity keys; vector deletion matches exact stored owner/node identity instead of string containment.
- Real PostgreSQL tests also exposed and fixed invalid per-branch ordering/limits and missing output aliases in multi-type and conversation memory list queries.

## Migration and operation

Apply `202610240003_memory_node_scope.sql` after the identity and conversation migrations. Ent schemas, generated code and the Atlas checksum are updated together.

1. Stop old writers/workers in the maintenance window described in [delivery](delivery.md), retaining a verified pre-migration backup.
2. The migration creates any missing deterministic Personal workspace for legacy node owners, backfills node scope and derives embedding scope from a same-owner parent node.
3. Orphan or cross-owner embeddings stop the migration. Review and repair the ambiguous records explicitly; the migration does not delete or guess their ownership.
4. NOT NULL, owner/workspace and embedding/parent composite references, immutable ownership triggers and scoped indexes enforce the new node/vector structure. Node deletion cascades its embeddings.
5. Restart the new binary and run legacy Personal checks. An old binary cannot write the new required columns; rollback requires restoring the pre-migration database and binary together.

Atlas reports two data-dependent warnings: embedding `workspace_id` becoming NOT NULL and the scoped node unique constraint. The migration checks unresolved embeddings before enforcing NOT NULL, and the existing node primary key already guarantees ID uniqueness. Legacy/Unicode-owner, ambiguous embedding and immutable/FK regression tests cover these cases. The warnings are retained rather than silenced.

No existing user database was migrated during implementation. Validation used a disposable PostgreSQL/pgvector instance and a separate Atlas development database. The backup drill seed now supplies workspace columns on memory nodes and vectors.

## Validation

Local artifacts: `.artifacts/memory-scope-20261024/` (ignored).

- Full `go test ./...` with PostgreSQL integration enabled; protocol and server E2E packages included.
- Race checks for SQL/vector stores, memory/gateway services and workers.
- Repository Go format and lint; lint reports zero issues. Ent/GraphQL/protobuf regeneration is repeatable, checked by comparing all generated/source Go and migration/module hashes before and after a second generation pass because the worktree contains the preceding implementation increments.
- All 16 migrations replayed on an empty database; Atlas latest-migration lint and complete apply dry-run pass, with the two warnings described above.
- Same-owner different-workspace negative tests exercise direct IDs, candidate truncation, keywords, history, graph, counts, Key Memory conflicts, identity rebinding and evidence writes. Positive assertions ensure visible memory remains retrievable. Two authorized Agents share Personal memory.
- Cache tests cover scope checks before a warm hit, foreign-owner/workspace payloads, old unscoped payloads and neighboring owner invalidation. Search tests cover scope denial in both normal and historical vector paths.
- Vector tests place higher-ranked hidden or disallowed-type records ahead of an allowed Fact, reject a hidden parent and inject a ready-state failure to verify transaction rollback.
- Backup/restore dry-run passes; all 22 fixture inserts execute successfully in a real PostgreSQL transaction that is rolled back. This is not a new full PostgreSQL/Redis restore exercise.
- Local worker recovery completes 256 conversations and 3,840 tasks with zero pending outbox events. It uses the existing deterministic in-process drill and is not a production throughput claim.

This increment does not change Manager UI and does not claim new browser or real-service UI validation. No remote CI, deployment, retrieval-quality benchmark or production-scale performance measurement was performed.

## Remaining work and next increment

Follow-up: the next Personal generation/source/media increment is now implemented; see [lifecycle scope](lifecycle-scope-2026-10-24.md) for the current behavior and remaining limitations. The statements below describe the boundary at completion of this memory increment.

The creation and use of non-Personal workspaces stays disabled. Source events and participant subjects, Run, visibility/private partitions, workspace lifecycle, blob/source references and per-workspace generation are still pending. Existing generation/rebuild/retention operations retain their Personal-era owner-level assumptions; they must be migrated before any additional workspace can receive data. Node filtering alone does not close those lifecycle boundaries, nor does it establish speaker/profile attribution.

Next, migrate generation, source/media/blob ownership and lifecycle operations together, add cross-workspace rebuild/delete and late-task tests, then expose workspace lifecycle through every transport. Continue the Manager server-side Target/session work and workspace routing against those completed contracts. The later group event/profile and sharing workflows retain the acceptance criteria in [delivery](delivery.md) and [Manager design](manager.md).
