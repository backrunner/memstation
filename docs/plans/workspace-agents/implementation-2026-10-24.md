# Agent identity foundation — 2026-10-24

This is the first implementation slice of P1, with a small update to the existing user credential screen. P1–P6 are **not complete**. This version supports multiple authenticated Agents sharing one owner's Personal memory. It does not yet support isolated group/project workspaces, private memory, speaker profiles, event ingestion, Runs or cross-workspace publication.

Follow-ups: [Agent management UI and bounded administration reads](agent-management-2026-10-24.md) now complete the Personal Agent lifecycle in Manager; [conversation scope and durable finalization](conversation-scope-2026-10-24.md) add typed conversation ownership, atomic finalization/outbox and Personal worker scope. The evidence below records the original foundation slice.

## Implemented behavior

- Versioned migration `202610240001_agent_identity.sql` creates Workspace, Actor, Agent and WorkspaceGrant, extends APIKey, and adds a conversation write revision. Ent schemas and generated code accompany it.
- Existing owners receive a deterministic Personal workspace, owner Actor and legacy Agent. Existing API key hashes, expiry and revocation remain intact. Historically unused `scopes` are preserved; the new explicit capability ceiling starts with the capabilities the legacy runtime actually allowed.
- Key authentication returns owner, Agent, credential, workspace, capability intersection, policy version and grant revision. Expired, deleted and revoked keys and disabled Agents cannot authenticate. SQL errors never fall back to in-memory grants.
- The effective capability set is credential ceiling ∩ current WorkspaceGrant ∩ workspace policy. Every existing proxy HTTP route and proxy gRPC method enforces its capability. Built-in memory tools recheck authority, including after an upstream response, and later tool rounds recheck gateway access.
- Agent disable and credential revocation are transactional. Re-enabling an Agent does not revive old keys. Agent and grant updates compare revisions. SQL mutations and their audit record commit together; the development fallback connects to the existing audit feed.
- New Agents use `(owner, personal workspace, agent, external session)` to map session names to internal conversation IDs. Key rotation and protocol changes do not change the session. Legacy keys retain the original session mapping.
- Conversation persistence cannot change its owner or recorded Agent. PostgreSQL serializes writes with a transaction advisory lock, including first creation. New-Agent gateway requests read the conversation revision before calling the provider; a stale completed request cannot overwrite another committed snapshot. Conflicts return HTTP 409 / gRPC Aborted, or an error event if SSE has already started.
- The existing Manager user page can issue a key for a registered Agent, choose the current grant or memory-read-only ceiling, and select 7/30/90 days or no expiry. The new UI defaults to 30 days; the API retains optional expiry. Agent name, ceiling, expiration and revocation are visible. Secret responses are not cached or persisted in drafts, and late user-detail/key responses cannot populate a different target/user scope.

Only the deterministic Personal workspace is accepted by the current data plane. `X-MemStation-Workspace` (gRPC metadata `x-memstation-workspace`) must be absent or match that workspace. Other values are rejected, never ignored or widened into an owner-wide search. The remaining user-scoped memory and worker implementation is why creating additional workspaces is not exposed yet.

`GET /api/v1/identity` and `GET /api/v1/workspaces` expose the authenticated context and `workspace_mode: personal_only`. The identity response also advertises `private_memory: false` and `event_ingestion: false`.

## Admin REST contract available now

These routes use the existing server-side admin session cookie. The bootstrap admin token still belongs only in the login exchange.

| Method and path | Body / result |
| --- | --- |
| `POST /api/admin/users/:user/agents` | `{ "name": "Coding agent", "capabilities": ["gateway.invoke", "memory.read", "files.read"] }` → Agent, HTTP 201 |
| `GET /api/admin/users/:user/agents?after=:cursor&limit=50` | `{ items, next_cursor, workspace_mode }`; maximum page size 100 |
| `PUT /api/admin/users/:user/agents/:agent` | `{ "name": "Coding agent", "status": "disabled", "expected_revision": 1 }` → updated Agent |
| `GET /api/admin/users/:user/agents/:agent/grant` | Current Personal workspace grant and revision |
| `PUT /api/admin/users/:user/agents/:agent/grant` | `{ "capabilities": ["memory.read"], "expected_revision": 1 }` → updated grant; an empty array removes all operational capabilities |
| `POST /api/admin/users/:user/api-keys` | `{ "name": "Laptop", "agent_id": "...", "capabilities": ["memory.read"], "expires_at": "2026-12-01T00:00:00Z" }` → `{ record, api_key }`, raw key once |
| `DELETE /api/admin/users/:user/api-keys/:key` | Owner-scoped revoke, including already-revoked keys |

Supported capabilities are `gateway.invoke`, `memory.read`, `memory.edit`, `files.read`, `files.write`. Omitted Agent capabilities default to gateway invocation, memory read and file read; explicit `[]` grants none. An omitted key ceiling snapshots its Agent's current grant. Expanding a grant cannot expand an existing key ceiling. Issuance outside the current grant/policy fails. Built-in writes additionally retain the existing tool-write setting and allowed-key validation.

For existing gateway requests, continue sending the Agent's API key and a stable **external** `X-MemStation-Session` value. Do not replace that value with the returned internal conversation ID. Agent identity is resolved from the key, never from a client-supplied Agent header or message role.

A 409 after an upstream call does not undo that model call or its charge. This slice does not implement gateway request idempotency, interrupted-response replay or the proposed append-only collaboration event API. Do not automatically retry paid generation after a conflict or SSE interruption.

## Migration and rollback

Use a maintenance window: back up the database, stop old writers/workers, apply versioned migrations, then start the new binary. New non-null credential fields mean the old binary must not keep issuing keys after migration. A rollback requires restoring the pre-migration backup with writes stopped; do not run old and new writers together.

The migration does not rewrite historical memory subjects or claim that a legacy transcript has known human speakers. Owners found only in legacy conversation/memory/key records retain their own Personal workspace, rather than being reassigned to admin. New business tables have composite ownership foreign keys. New users receive the same deterministic mapping when identities are first used.

No existing user database was migrated during development. Testing used a disposable PostgreSQL 16.14 + pgvector container on a separate port.

## Remaining work

- P1: explicit workspace and visibility partitions on every memory/source/vector/cache/blob path, stable subjects, complete workspace policy/lifecycle, per-workspace generation and worker payloads.
- P2/P3: trusted Integration identities, append-only source events, runs/windows, speaker-aware extraction, candidate CAS, source withdrawal, publication and private-memory boundaries.
- P4/P5: Manager server-side Target session registry/BFF, workspace routing and picker, group members, sources, Runs and memory review workflows. This increment only extends the existing user credential screen and its transport; it does not claim the current direct-target Manager architecture has already been replaced. The follow-up implements Personal Agent/grant management in Manager and Admin REST; new GraphQL/gRPC management contracts remain pending.
- P6: complete migration/restore and backlog drills, real-service Manager E2E, capacity/SLO measurements and the full A01–A22 / U01–U12 matrix.

The existing Manager credential selector includes the first 100 Agents returned with user detail. The paginated Agent endpoint supports larger inventories; the follow-up searchable Agent management UI uses the paginated endpoint and supports per-Agent issuance beyond that limit. `MEMSTATION_DEV_API_KEY` remains a deployment development credential, outside stored-key expiration/revocation; production Agents should use issued keys.

## Validation evidence

Local evidence is under `.artifacts/agent-identity-20261024/` (ignored, contains only disposable test data).

- Complete Go suite against the disposable PostgreSQL database, plus normal `pnpm test`.
- Race tests for identity credentials, SQL/in-memory conversation writes, gateway and both proxy transports.
- New tests cover grant/ceiling intersection, immediate grant revocation during tool continuation, Agent disable/re-enable, cross-owner issuance/revoke, expiry/deletion, owner-preserving migration (including Unicode IDs), composite FKs, concurrent grant CAS, same-session Agent separation, credential rotation and stale conversation writes.
- Atlas CE lint and apply dry-run passed. Lint reports data-dependent NOT NULL advisories and column-padding advisories; backfill was additionally applied to an empty database and a populated legacy schema. Legacy owners/keys/conversations and revocation state were checked. No live production migration is implied.
- Ent/GraphQL/protobuf regeneration is deterministic after removal of temporary checksum-tool module sums; `generate-diff.json` records no changes.
- Manager lint/check, Vitest and production build; new credential issuance flow tested on Chromium and mobile WebKit. Screenshots are fixture-backed browser evidence, not a physical-device or real-service test.
- Backup/restore script dry-run and actual insertion of the updated SQL backup fixture inside a rollback transaction passed. This is not a live Redis/PostgreSQL restore drill.

The final full browser regression passed **50/50** (Chromium 37, mobile WebKit 13), recorded in `manager-e2e-final.log`. The first run passed 49/50 with an existing protocol-playground case timing out before its response tabs appeared. Its isolated rerun and a full sequential rerun both passed without changing that case or relaxing assertions. A concurrent production build in the first run may have interfered; the cause is not proven. Screenshots are retained as `credentials-desktop.png` and `credentials-mobile.png`.

## Workspace management follow-up

Existing Personal workspace discovery, policy/name administration, transactional audit and scoped Manager directory/settings/access routes are now implemented. See [Workspace management](workspace-management-2026-10-24.md) for contracts and validation. Other workspace kinds, all remaining read/write scope propagation and the full Manager route migration remain pending.
