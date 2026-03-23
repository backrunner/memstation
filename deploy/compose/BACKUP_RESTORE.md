# Backup and Restore Drill

MemStation v1 keeps backup and restore as a hardening requirement, not an afterthought.

Scripts
- `./scripts/backup-export.sh`: bootstraps an isolated source stack, applies migrations, seeds deterministic fixture data, and exports `postgres.dump` plus `redis.rdb`.
- `./scripts/restore-drill.sh --backup-dir <dir>`: restores a previously exported backup into an isolated restore stack and verifies table counts plus Redis markers.
- `./scripts/backup-restore-drill.sh`: runs the full source export plus restore verification end to end.

Artifacts
- `report.json`: machine-readable drill result
- `report.md`: human-readable summary
- `backup/postgres.dump`: PostgreSQL logical backup
- `backup/redis.rdb`: Redis snapshot
- `backup/metadata.json`: marker metadata and expected table counts

Usage
- Dry-run shape check: `./scripts/backup-restore-drill.sh --dry-run`
- Full local drill: `./scripts/backup-restore-drill.sh --out-dir .artifacts/drills/backup-restore`
- Export only: `./scripts/backup-export.sh --out-dir .artifacts/drills/backup-export`
- Restore only: `./scripts/restore-drill.sh --backup-dir .artifacts/drills/backup-export/backup --out-dir .artifacts/drills/restore-only`

Notes
- The drill uses isolated Docker Compose projects and does not touch the primary Compose stack.
- PostgreSQL backup/restore runs with `pg_dump` and `pg_restore` from the `pgvector/pgvector:pg16` image.
- Redis verification restores from the generated `dump.rdb` and checks deterministic marker keys.
