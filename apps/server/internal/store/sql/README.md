# SQL repositories

Import the repository's responsibility package directly. The root is a directory index, with no implementation or compatibility exports.

| Directory | Responsibility |
| --- | --- |
| `conversation/` | Conversation/message/segment persistence, source windows, atomic finalization and outbox repositories |
| `identity/` | Actors, Agents, credentials, workspace grants and policy |
| `memory/` | Memory documents, facts and history, key memories, lexical/graph retrieval, editing and checkpoints |
| `media/` | Blobs, message parts, artifacts and cleanup references |
| `ingestion/` | Independent source events, window assembly, progress, evidence dependencies and transactional source invalidation |
| `lifecycle/` | Candidate review, publication/retraction, export, forgetting and deletion-log recovery |
| `aicall/` | Broker call accounting and embedding index identity |
| `memorygeneration/` | Generation snapshots, carry-forward, durable tasks and activation |
| `integration/` | Cross-repository browsing contracts |
| `internal/` | SQL-only database helpers, record metadata, access scope and reusable test fixtures |

Repositories depend on domain/ports and the narrowly scoped internal packages. Lifecycle composes ingestion and memory; these packages do not import lifecycle. Keep conversation finalization and its outbox writes in the same transaction and lock boundary. Lifecycle memory writes explicitly use `StoreDocumentTx` with the caller's transaction; starting another transaction would break atomic publication.

Keep tests with the owning implementation. Fixtures use public interfaces or explicit database handles and never require exposing repository fields. Update scripts and documentation whenever a package moves. See [AGENTS.md](../../../../../AGENTS.md#127-目录组织与职责边界).
