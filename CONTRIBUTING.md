# Contributing to MemStation

Bug reports, documentation improvements and focused pull requests are welcome.
For substantial changes, open an issue describing the problem and proposed behavior
before changing the architecture. Use [SECURITY.md](SECURITY.md) for vulnerabilities.

## Development

Start with the [README](README.md) and [local debugging guide](docs/local-debugging.md).
The repository uses Go 1.27.3 and pnpm 12.15.0; CI uses Node.js 26.12.1.
Install the locked workspace dependencies with `pnpm install --frozen-lockfile`.

[AGENTS.md](AGENTS.md) defines the current engineering boundaries and takes precedence
over historical planning documents. Business logic belongs in `apps/server`; Manager
consumes its public management interfaces. Keep generated Ent, GraphQL and protobuf
code synchronized with their sources.

## Before opening a pull request

Run the checks appropriate to your change:

```bash
bash scripts/go/format.sh --check
pnpm lint
pnpm test
pnpm --dir apps/manager build
pnpm --dir apps/manager test:e2e:install
pnpm test:e2e:manager
```

For Go generation, run `go generate ./...` from `apps/server` and review the generated
diff. Commit generated changes along with their schemas. CI regenerates everything
and requires a clean diff. Database changes also require versioned Atlas migrations
and the [CI migration checks](docs/ci.md), using a disposable database.

Describe the concrete problem, resulting behavior and validation in your PR. Include
screenshots for visual changes, using fixture data. Never commit credentials, private
conversations, downloaded benchmark datasets or local evaluation artifacts.

Before publishing a branch, scan its reachable history with Gitleaks 8.30.1:

```bash
gitleaks git --log-opts="--all --full-history" --redact --ignore-gitleaks-allow
```

Store any local reports under the ignored `.artifacts/` directory. Do not suppress
an alert without reviewing its origin. A clean scan complements manual review of
fixtures, screenshots and third-party rights; it does not establish their provenance.
See the [publication policy](docs/open-source.md).

Keep reusable guides and API contracts in `docs/`. Local plans, validation reports,
generated benchmark JSON and prototype captures belong in ignored `.agents/archive/`;
do not force-add them. Required generated source code and selected README/brand assets
remain part of the repository.

Keep commits focused and use `type: message`, for example `fix: preserve memory filters`
or `feat: add provider diagnostics`. Dependency upgrades must include compatibility
notes and regression results. Do not lower correctness or coverage gates to get a green build.

## License

By submitting a contribution for inclusion in this project, you agree to license it
under [Apache License 2.0](LICENSE). Preserve third-party notices and identify any
new third-party material in your contribution.

## Current contracts

This project does not retain legacy implementations or backward compatibility at
this stage. Update the server, Manager, protocol schemas, scripts, fixtures and
maintained documentation together when changing a contract. Remove retired paths,
aliases and format readers; reject unsupported inputs instead of silently
reconstructing an identity or switching to retired behavior. Historical facts,
audit provenance, supersession and temporal queries remain product features.

The unreleased database migration baseline initializes an empty database. It does
not convert a prior deployment's data. Keep existing databases and evaluation
archives intact; use a separate database to validate the current schema. Backup
recovery uses a current-schema backup together with its independent deletion log.

## Directory organization

Group implementations by responsibility rather than accumulating unrelated files in a shared directory. SQL repositories live in `internal/store/sql/{conversation,identity,memory,media,ingestion,lifecycle,aicall,memorygeneration}`; SQL-only shared code and fixtures live under `sql/internal`. Keep transaction ownership and authorization intact when extracting packages. Tests stay with their owning package; cross-repository contracts belong in integration tests.

Manager components are grouped into `ui`, `layout`, `content`, `charts` and business domains. Use `#lib/` imports and keep primitives independent of business components. Update imports, tests, scripts and maintained documentation together; remove old paths rather than adding compatibility re-exports. See AGENTS.md section 12.7 for the complete rules.
