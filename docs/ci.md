# CI and reliability checks

[← README](../README.md)

The [CI workflow](../.github/workflows/ci.yml) runs on pushes, pull requests and manual
`workflow_dispatch`. Correctness gates remain blocking; diagnostic uploads are
best-effort with three-day retention. No provider credentials are required.
The Manager production build is a blocking step alongside lint, unit tests and browser
E2E. Real-model benchmarks remain separate manual diagnostics.

## Reproduce the checks

Use pnpm 12.15.0, Go 1.27.3 and Node.js 26.12.1, matching the workflow. Start with:

```bash
pnpm install --frozen-lockfile
bash scripts/go/format.sh --check
pnpm lint
pnpm test
pnpm --dir apps/manager build
pnpm --dir apps/manager test:e2e:install
pnpm test:e2e:manager
bash scripts/test/run-contract-tests.sh
bash scripts/test/check-coverage.sh
bash scripts/ops/run-worker-recovery-smoke.sh
bash scripts/ops/run-backup-restore-drill.sh --dry-run
```

Run `go generate ./...` from `apps/server`; generated files must match the committed
schemas. CI requires `git diff --exit-code` after generation. Keep source and generated
changes in the same commit. Go formatting and linting use the repository's pinned
`scripts/go/format.sh` and `scripts/go/lint.sh` tools.

Atlas checks require **a disposable, empty PostgreSQL database with pgvector available**.
`migrate lint` uses it as a scratch database. Never point these commands at an existing
MemStation instance. CI uses its isolated PostgreSQL service:

```bash
export DATABASE_URL='postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable'
# Use this URL only for a disposable database created for these checks.
bash scripts/ops/check-atlas-migrations.sh
atlas migrate apply --dir file://apps/server/ent/migrate/migrations --url "$DATABASE_URL"
export MEMSTATION_TEST_DATABASE_URL="$DATABASE_URL"
(cd apps/server && go test ./...)
```

CI pins Atlas **v0.37.0** because that release includes Community Edition `migrate lint`.
Newer Atlas releases require a separate compatibility/licensing review before changing
this check. The workflow now applies all migrations before Go tests and sets
`MEMSTATION_TEST_DATABASE_URL`, so PostgreSQL graph, vector and generation regressions
execute instead of silently skipping.

Current coverage gates are **55% server** for the selected business packages and
**20% Manager**. Use the coverage script for the exact package selection. Retrieval
and ranking benchmarks are explicit local/manual evaluation jobs, outside default
CI gates; see the [evaluation guide](../apps/server/tests/eval/README.md).

## Reliability drills

The worker backlog recovery drill and the PostgreSQL/Redis backup-and-restore
drill run on demand:

```bash
bash scripts/ops/run-worker-recovery-nightly.sh
bash scripts/ops/run-backup-restore-drill.sh
```

The backup drill requires Docker Compose, creates isolated source/restore projects,
and removes its temporary stacks afterward. It verifies database table counts and
Redis markers. See the [backup guide](../deploy/compose/BACKUP_RESTORE.md).
