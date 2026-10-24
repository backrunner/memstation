# Agent management — 2026-10-24

This increment completes the Manager workflow for the existing Personal-workspace Agent foundation. An operator can create an Agent, edit its name and permissions, issue limited credentials, revoke individual credentials, and disable or re-enable the Agent. It does not enable additional workspaces or claim P1–P6 completion.

## Product workflow

From **Users → user → Manage agents**, open `/users/:user/agents`. Search by name or ID, filter active/disabled Agents, and load further pages. Filtering happens on the server, including inventories larger than 100 Agents. Results use an ID cursor; changing filters resets pagination.

Creating an Agent opens its access page at `/users/:user/agents/:agent`. The default permissions are model invocation, memory read and file read. All Agents currently share the owner's Personal memory; separate credentials and conversation sessions do not make the memory private.

The access page provides:

- Name editing and Personal workspace permissions, with independent revision checks. A conflict keeps the submitted values visible and disables further saves until the operator reloads current values. There is no automatic mutation retry.
- Credential issuance in the page header. The operator chooses a name, an explicit subset of the Agent's effective permissions, and 7/30/90 days or no expiry; the UI defaults to 30 days. The secret is displayed once and can be hidden. It is not stored in browser drafts, localStorage or sessionStorage.
- A paginated credential list showing the immutable permission ceiling and the effective permission snapshot separately. Refreshing checks current grants, workspace policy, Agent status, expiry and revocation. Runtime authentication remains authoritative.
- Individual credential revocation, or Agent disable with confirmation. Disabling revokes all credentials atomically. Re-enabling allows new issuance; it does not revive old credentials or remove existing conversations/memory.
- Collapsed identity IDs, visible errors, pending-action protection, unsaved-edit navigation warnings, keyboard-operable controls and short-screen scrolling dialogs.

Target, user and Agent route changes destroy the old page instance, abort its reads and ignore late mutation responses. A response cannot insert a secret or another Agent's form into the new route. Name/permission drafts remain in memory during an edit; they are not restored across a reload.

The original user-page key selector still contains its first 100 Agents for compatibility. The new directory and per-Agent issuance flow provide access to the full inventory.

## Admin REST additions

All routes require the existing admin session and use `Cache-Control: no-store`.

| Method and path | Behavior |
| --- | --- |
| `GET /api/admin/users/:user/agents?q=:nameOrID&status=active&after=:id&limit=50` | Literal, case-insensitive substring search and optional `active`/`disabled` status; cursor page, maximum 100 items |
| `GET /api/admin/users/:user/agents/:agent` | `{ agent, grant, workspace, effective_capabilities }`; names and permissions retain their independent revisions |
| `GET /api/admin/users/:user/agents/:agent/api-keys?after=:id&limit=50` | Owner- and Agent-scoped credential metadata with `effective_capabilities`; no raw keys or hashes |

Existing create/update/grant/credential routes are described in [the foundation implementation](implementation-2026-10-24.md). A page containing exactly `limit` rows may expose a cursor whose next page is empty; clients stop when `next_cursor` is empty.

Credential insertion now locks and rechecks the Agent, workspace policy and grant in its transaction. If permission was withdrawn after the service's preliminary check, insertion rejects the stale ceiling. This closes a race where restoring a grant later could otherwise activate a credential issued against outdated authority. Credential issuance and its audit entry still commit together. Agent creation audits include the initial capabilities.

## Integration

Create one Agent for each stable client identity, then issue a credential per installation or process as appropriate. Configure that credential as the client's MemStation API key. Continue using a stable external `X-MemStation-Session` value for each conversation; key rotation preserves the Agent's session mapping. A different Agent gets a different internal conversation for the same external session value.

Read-only credentials can use memory retrieval but cannot invoke models unless `gateway.invoke` is also granted. Expanding an Agent's permissions cannot expand a credential's immutable ceiling. Issue a new credential when a client needs a broader ceiling. Do not retry paid gateway generation automatically after a revision conflict.

## Validation and limits

Local artifacts are in `.artifacts/agent-management-20261024/` (ignored).

- In-memory and real PostgreSQL tests exercise 105-Agent search/cursor traversal, literal wildcard characters, status validation, 53-credential pagination, cross-owner rejection, ceiling/effective distinction, disable/revoke behavior and stale-grant issuance rejection.
- HTTP router tests exercise authenticated reads, no-store headers, invalid filters and cross-owner detail/credential paths.
- Browser coverage includes creation, rename, grant editing, issuance, raw-key storage checks, disable/enable, revision conflicts, pagination, delayed responses across client-side navigation, 320px layout and short-screen dialogs.
- Full Go tests against disposable PostgreSQL, affected-package race tests, Go format/lint, Manager lint/check, unit tests and production build are the local verification scope. The full browser regression passed **56/56** (Chromium 40, mobile WebKit 16); all 15 Manager unit tests passed. Screenshots were inspected on desktop and mobile.

The PostgreSQL instance is disposable; no existing user database is modified. Browser screenshots and E2E use the fixture target. This is not a real-service Manager end-to-end test, physical-device test, deployment or production migration. There are no new dependencies, generated schemas or migrations in this increment; it uses the previous Agent identity migration.

Manager still uses its existing direct-target session transport. The planned server-side Target registry/BFF, workspace navigation, private memory, Integration events, stable subjects/member profiles, Runs and new GraphQL/gRPC Agent administration remain pending. The next data-plane stage must cover workspace/partition filtering in SQL, vector candidates, graph expansion, direct reads, blobs, caches, generations and worker payloads before any additional workspace becomes writable.
