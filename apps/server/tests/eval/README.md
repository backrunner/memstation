# MemStation Eval

This directory holds the retrieval and ranking benchmark foundation for MemStation v1.

Datasets
- `testdata/repo_native_golden.json`: small repo-distributed golden dataset used by CI smoke.
- `testdata/locomo_fixture.json`: tiny LOCOMO-shaped adapter fixture used only for parser smoke.
- Raw LOCOMO data is not committed to this repository.

LOCOMO policy
- MemStation uses a `LOCOMO-first` benchmark method, but the raw upstream dataset stays external.
- As of 2026-03-15, the upstream LOCOMO dataset is distributed under `CC BY-NC 4.0`.
- Only the adapter, schema expectations, runner, and download instructions belong in the repo.

Commands
- Repo-native smoke: `./scripts/eval-memory.sh`
- External LOCOMO run: `./scripts/eval-memory-locomo.sh` (defaults to `https://raw.githubusercontent.com/snap-research/locomo/main/data/locomo10.json`, override with `LOCOMO_DATASET_PATH` or `LOCOMO_DATASET_URL`)
- Nightly bundle: `./scripts/eval-memory-nightly.sh`
- Worker recovery smoke: `./scripts/worker-backlog-smoke.sh`
- Worker recovery nightly: `./scripts/worker-backlog-nightly.sh`

Artifacts
- `aggregate.json`: aggregate metrics by variant
- `results.json`: per-query ranked results with score breakdowns
- `failures.json`: failure samples for misses, leakage, or stale hits
- `report.md`: human-readable summary
- `perf.json`: retrieval latency smoke report when enabled
