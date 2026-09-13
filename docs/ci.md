# CI and reliability checks

[← README](../README.md)

The [CI workflow](../.github/workflows/ci.yml) runs on pushes, pull requests and manual
`workflow_dispatch`. Correctness gates remain blocking; diagnostic uploads are
best-effort with three-day retention. No provider credentials are required.

## Reproduce the checks

Use pnpm 12.3.4, Go 1.26.1 and Node.js 24.11.1, matching the workflow. Start with:

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

## Failure review — 2026-09-09

- [CI run 34354409173](https://github.com/backrunner/memstation/actions/runs/34354409173)
  and [CI run 34124859700](https://github.com/backrunner/memstation/actions/runs/34124859700)
  failed at dependency installation: pnpm 12 rejects `--frozen-lockfile=false` with
  `unexpected value 'false'`. Tests had not started. CI and the Manager
  Docker build now use `--frozen-lockfile`; local editable installs use
  `--no-frozen-lockfile`. The Docker build also copies the committed lockfile and
  workspace manifests before installing.
- [Nightly 34196160611](https://github.com/backrunner/memstation/actions/runs/34196160611)
  failed on the same install flag. The following run
  [34321303206](https://github.com/backrunner/memstation/actions/runs/34321303206)
  was green only because its drills were skipped. The skip check now also requires
  the previous run to have succeeded.
- Go setup looked for a root `go.sum`, which does not exist in this monorepo.
  The CI workflow now sets `cache-dependency-path: apps/server/go.sum`.
- A clean Manager Docker build also reproduced an old Corepack launcher expecting
  `pnpm.cjs`, which pnpm 12 no longer ships. Docker/devcontainer setup and the local
  missing-pnpm fallback now install the same pinned pnpm version directly with npm.
- After unblocking installation, a local 256-conversation nightly run reproduced a
  queue deadlock: handlers blocked publishing follow-up tasks to their own full
  channel. The in-memory drill queue now grows with fan-out and drains all accepted
  tasks. Regression tests cover the nightly workload, an initial capacity of one,
  task counts and cancellation. The production Asynq queue is unchanged.
- The backup fixture predated graph foreign keys and inserted impressions, facts and
  Key Memory without their `MemoryNode` rows. PostgreSQL reproduced an
  `impressions_node_fk` violation. The fixture now creates those nodes first.

These fixes preserve the existing correctness, coverage and recovery gates. A local
pass does not change a historical GitHub run: push the changes and check the resulting
workflow, or manually dispatch CI on the updated branch.

Local validation used Go 1.26.1: full tests, format, lint, generated diff, Atlas checks,
PostgreSQL integration tests and recovery race tests passed. Coverage was 68.7% server
and 39.76% Manager. Manager checks, 15 unit tests, production build and all 42 browser
tests passed (33 Chromium, 9 WebKit), with temporary local port substitutions to
preserve already running development servers. The nightly workload processed 3,840
tasks from 256 conversations in about 1.3 seconds on this machine.

Backup dry-run and its SQL fixture passed. A complete container build and backup/restore
drill remain to be verified in a Docker environment with sufficient disk space: this
machine's Apple Container adapter encountered bind-mount ownership restrictions, and
the system disk filled during the extra container checks. These are recorded as
incomplete checks, not passing results. No changes were pushed to GitHub during this review.
