# Conversation scope and durable finalization

Date: 2026-10-24. Status: implemented for the existing Personal workspace. This increment extends the [identity foundation](implementation-2026-10-24.md) and [Agent management](agent-management-2026-10-24.md). It does not complete P1 or enable group/project workspaces.

## Delivered behavior

Conversations now have typed `workspace_id` and creator `agent_id` fields. Messages and token usage carry their parent workspace. Gateway requests derive identity from the authenticated AccessContext; arbitrary client metadata cannot select an owner, workspace or creator. Trusted legacy writes resolve to the owner's deterministic Personal workspace and legacy Agent. Typed columns are authoritative; historical metadata is not an identity source at runtime.

PostgreSQL enforces same-owner workspace/Agent references, composite parent references for messages/usage, and immutable ownership for conversations and their children. Conversation reads, lists and timeout selection currently filter to Personal. A manually inserted conversation in another workspace is inaccessible through those paths. Segment replacement validates the parent and each segment's ownership before writing.

Explicit end, message/token thresholds and scheduler timeout use one repository operation for the state transition and finalization outbox insertion. PostgreSQL commits both in one transaction; the in-memory implementation holds both repository locks. An outbox failure leaves the conversation active. Concurrent requests select one winning reason and one event; replay does not reset a processed event. Timeout candidates are checked again against the original cutoff inside the transaction, so a newly active conversation is not ended using a stale scheduler result. Persistence and retention flags come from the stored conversation.

An older gateway snapshot cannot restore active/pending lifecycle state over a finalization or worker update. In-memory state updates no longer rewrite messages using a separately loaded snapshot. This preserves the existing client-history contract; immutable SourceEvent windows and revision-specific analysis remain a later P2 deliverable.

## Worker and replay contract

- New task payloads carry `schema_version: 1`, `user_id` and `workspace_id`. Finalization events also record creator Agent and conversation revision. Conversation revision is provenance in this increment, not a frozen-window processing guarantee.
- Publisher task IDs include owner, workspace, task type and the existing logical unique key. Identical external IDs in different owners cannot suppress each other's jobs.
- All ten registered handlers validate scope before accessing services. Conversation jobs resolve their stored parent and check the creator when supplied. Unsupported workspaces, invalid versions/types and missing parents fail without retry; temporary repository failures remain retryable.
- Legacy version-zero payloads resolve only to Personal. The relay verifies payload ownership against the outbox row. Invalid outbox entries remain pending with an error for investigation; a dedicated quarantine workflow is not implemented yet.
- An existing Asynq task ID counts as a successful repeated enqueue. A relay interrupted after enqueue can finish marking its event processed on retry. The finalization handler also retries publishing the next job when summary state is already queued, closing a queued-state/publish-failure gap.

Worker concurrency checks also exposed an existing in-memory repository race: document reads decorated a shared metadata map. Reads now decorate independent snapshots. Regression coverage exercises concurrent reads and caller mutations across document lookup paths.

## Migration and integration

Apply `202610240002_conversation_scope.sql` after the Agent identity migration during the maintenance window described in [delivery.md](delivery.md#32-切换和任务排空). Drain or persist old jobs, stop writers/scheduler/workers, apply migrations transactionally, validate the migrated data, then start the matching binary. Do not run the old writer against the new NOT NULL columns or mix old/new task identities.

The migration backfills Personal scope from the exact same-owner parent. Conversations with unknown legacy creators use the deterministic legacy Agent; only identity-foundation writes with a positive write revision and a valid same-owner Agent preserve their recorded creator. Missing historical User rows do not cause conversation loss. Existing disabled Agents and revoked grants are preserved. Orphan or cross-owner messages/usage abort migration for review; they are neither deleted nor reassigned.

This does not repair conversations that were already stuck at `ending` without an outbox event before upgrading. Inspect those records during maintenance and recover them deliberately; the atomic operation prevents new instances of that failure. An old binary rollback requires the prior database backup, as described in the delivery plan.

Clients continue using their Agent key and stable session value. No new client headers or configuration are required for Personal. The previous Agent management UI remains usable; this increment does not add a Workspace picker or suggest group isolation is available.

## Local verification

Artifacts: `.artifacts/conversation-scope-20261024/` (ignored).

- Full Go suite with a disposable PostgreSQL instance; affected SQL store, conversation, gateway and worker packages under the race detector; repository format and lint.
- PostgreSQL and in-memory tests for scope rejection, 16 concurrent finalizers, injected outbox insertion failure/rollback, processed-event replay, stale timeout selection and lifecycle updates. PostgreSQL migration tests cover legacy/new creators, Unicode owners, disabled Agents, ambiguous children, composite references and immutable ownership.
- Asynq client/inspector against the existing miniredis test dependency checks enqueue-before-ack replay and distinct-owner task identities. This is not a production Redis failover test.
- Full code generation is stable after the intended Ent changes. Atlas lint for the new migration and dry-run of all 15 migrations pass; the new migration has five data-dependent warnings (four NOT NULL changes and one unique constraint), with populated migration tests covering the backfill and rejection paths.
- Backup/restore script dry-run and actual fixture SQL insertion in a rollback transaction pass. This is not a new full PostgreSQL/Redis backup-and-restore exercise.
- Local in-memory recovery smoke drains 256 conversations / 3,584 jobs with eight workers in about 1.17 seconds, with zero pending outbox entries and all summaries/analyses completed. These fixture timings are not production SLO measurements.

No existing user database was migrated. Manager UI is unchanged in this increment; its previous fixture browser results are recorded separately in the Agent management document. No deployment or remote CI run is claimed.

## Next dependency

Complete memory/entity/node/relation/evidence/history, vector candidate, blob, cache and generation scope propagation and their direct-read guards before enabling additional workspaces or private partitions. Source events, stable member subjects/profiles, Runs, publication/retraction, Manager BFF and workspace navigation remain in the full plan. Conversation columns alone do not establish complete memory isolation.

Follow-up: [memory, vector and cache scope](memory-scope-2026-10-24.md) implements the next Personal-only increment. Source/blob, generation, visibility and lifecycle isolation remain pending before additional workspaces can be enabled.
