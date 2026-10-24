# Personal generation, source and media lifecycle scope

Date: 2026-10-24. Status: implemented locally. This continues the [memory scope increment](memory-scope-2026-10-24.md). Additional workspaces remain disabled; the complete P1–P6 expansion is still in progress.

## Delivered behavior

Generation, retained source snapshots/messages, generation sources, blobs, message parts and extraction artifacts now carry a workspace. Every current public generation/media operation validates the Personal owner/workspace boundary before accessing storage. Direct identifiers, lists, counts, rebuild selection, activation and retention use the same boundary. Legacy internal calls without an explicit workspace map to the owner's deterministic Personal workspace; an explicit unsupported workspace is rejected.

Generation persistence moved from the memorygen service into `internal/store/sql/memorygeneration`. The service prepares retained source material; the repository owns transactions, scoped queries and state transitions. Existing service entry points remain compatible. Nested parts/artifacts inherit missing legacy scope fields and reject explicit foreign owner/workspace/conversation fields, including when loading retained snapshots.

The lifecycle changes include:

- Snapshot capture serializes by owner/workspace/conversation. Concurrent identical captures return the same snapshot/version and only one reports creation. Snapshot identity keys use structured values rather than owner-prefix matching.
- Active generation initialization and rebuild creation serialize within Personal. Concurrent rebuild requests cannot create multiple in-progress generations.
- Adding a source reloads its persisted identity and increments the source count only on the first insert. Completion/failure/retry counters transition once under the generation/source locks; completion does not regress to running or failed.
- Activation rechecks readiness under the workspace/generation locks. Automatic activation only accepts a completed, auto-activating generation. Replayed worker activation cannot restore a historical version over a newer one. Explicit administrator activation retains the existing historical-generation behavior.
- A completion retry can republish a failed activation task without repeating extraction or changing counters. Superseded source completion uses the same activation publication path. Discarded or already activated rebuild jobs exit without rebuilding.
- Discard and retention target a scoped parent-node set. Registered discarded generations retain their tombstone; late document, vector and conversation-segment writes share the generation lock and cannot recreate their data. A batch of segments cannot mix generations.

An active generation in another workspace belonging to the same owner is not deactivated, selected for rebuild or removed by these Personal operations. Tests deliberately insert such otherwise inaccessible fixtures; this does not enable public use of those workspaces.

## Files and retained sources

New local uploads use `v2/<workspace>/<opaque blob identity>/content`. Original filenames and owner strings are not path components. Files are written to an exclusive temporary file and published without overwriting an existing blob; failed copies do not publish partial content. Filesystem operations use `os.Root`. Legacy files remain readable/deletable only at their exact owner/blob-relative path; this increment does not move existing bytes.

Media rows cannot be rebound to a different scope or storage key. Live parts reference a same-owner, same-workspace conversation/blob, and artifacts reference their corresponding part. Invalid replacement rolls back instead of losing the previous records. Tombstones remain available as metadata for diagnostics/retry, but blob opening and new references reject deleted blobs.

PostgreSQL automatic cleanup locks the blob, checks other live conversations and retained snapshot references, and marks it deleted only if those references do not need it. Part insertion and snapshot capture take a conflicting shared blob lock, so cleanup cannot race a new reference. Physical deletion follows the tombstone; a filesystem error is returned for retry. Explicit administrator deletion retains its existing explicit-delete semantics rather than silently becoming reference-aware automatic cleanup.

The in-memory fallback validates Personal identity and live-part references, but has no shared retained-snapshot registry or cross-repository lifecycle transaction. Its tests/recovery drill do not establish PostgreSQL retention guarantees. Production lifecycle guarantees in this increment rely on PostgreSQL. Legacy/default or fixture generation IDs without a registry row remain accepted for compatibility; registered generations use the lifecycle lock.

## Migration and rollback

`202610240004_generation_media_scope.sql` follows the three preceding identity/conversation/memory scope migrations. These seven tables were already managed by SQL migrations, not Ent schemas. Existing Ent/GraphQL/protobuf generation remains unchanged and repeatable.

Use the maintenance process in [delivery](delivery.md): stop old writers/workers, keep a verified backup, apply the complete migration sequence and start the new binaries together. The migration:

1. Creates missing deterministic Personal workspaces for legacy owners and backfills the lifecycle/media rows from their owner and parent records.
2. Preserves retained snapshots whose live conversation has already been removed. If a live parent exists, its scope must agree.
3. Stops on ambiguous owner links, orphan child rows or cross-scope live blob references; it does not guess, delete or silently reassign them.
4. Adds required scope columns, immutable ownership triggers and composite parent references. Active-generation uniqueness changes from one per owner to one per owner/workspace.

Atlas reports nine data-dependent diagnostics: four nullable-to-required child columns and five composite unique constraints. Explicit unresolved-reference checks precede NOT NULL; existing primary keys already make the scoped composite identities unique. The warnings are recorded, not suppressed. Unicode owners, retained sources, migration rollback on ambiguity, immutable scope and cross-scope FK rejection have real PostgreSQL coverage.

An old binary cannot write the new required columns. A binary/database rollback requires restoring the pre-migration database together with the old binary. No existing user database was changed during this work.

## Validation

Local artifacts: `.artifacts/lifecycle-scope-20261024/` (ignored).

- Full Go test suite with real PostgreSQL integration enabled, including existing protocol/server E2E tests.
- Race checks for SQL/vector/blob stores, memorygen/multimodal services and workers; repository format and lint pass with zero issues.
- Complete code generation plus a second pass checked by source/generated/migration/module hashes; the existing dirty worktree is not compared against HEAD as though earlier increments were committed.
- All 17 migrations replay on an empty PostgreSQL/pgvector instance. Latest Atlas migration lint and complete apply dry-run pass with the nine diagnostics above.
- Sixteen-way capture/create/source-add/completion tests verify idempotence; negative fixtures verify same-owner different-workspace isolation. Late writes, activation publication failure/retry and stale activation delivery have regression tests.
- Media tests verify ownership, atomic replacement, parent/blob constraints, shared live references, retained source references, deleted-blob refusal and filesystem paths/partial writes.
- Backup/restore dry-run passes. Its fixture now covers all seven lifecycle/media tables; all 29 insert statements execute in a real PostgreSQL transaction and roll back. Restore counts now include those tables. This does not exercise local file-byte backup or a full PostgreSQL/Redis restore.
- The existing deterministic worker recovery drill completes 256 conversations / 3,840 tasks with zero pending outbox events. It is not a production throughput measurement.

No Manager UI changed in this increment, so no new browser/UI validation is claimed. No remote CI, production deployment or model-quality benchmark was run.

## Next delivery boundary

The next step is to implement Workspace lifecycle/access contracts across services and transports, with typed scope for the remaining subordinate entities and explicit private/shared visibility. Complete the Manager server-side Target/session path and Workspace navigation against those contracts. Only open non-Personal workspace creation/use after those checks pass.

Group SourceEvent ingestion, speaker/subject identity and profile attribution, Run, publication/retraction, workspace archive/delete and their Manager/mobile flows remain required by [delivery](delivery.md) and [Manager design](manager.md). Retained conversation snapshots in this increment are not the group SourceEvent model. Current source/provider URL metadata is not a new authorization mechanism for external files.
