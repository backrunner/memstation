# Contributing to MemStation

Bug reports, documentation improvements and focused pull requests are welcome.
For substantial changes, open an issue describing the problem and proposed behavior
before changing the architecture. Use [SECURITY.md](SECURITY.md) for vulnerabilities.

## Development

Start with the [README](README.md) and [local debugging guide](docs/local-debugging.md).
The repository uses Go 1.26.1 and pnpm 12.3.4; CI uses Node.js 24.11.1.
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

Keep commits focused and use `type: message`, for example `fix: preserve memory filters`
or `feat: add provider diagnostics`. Dependency upgrades must include compatibility
notes and regression results. Do not lower correctness or coverage gates to get a green build.

## License

By submitting a contribution for inclusion in this project, you agree to license it
under [Apache License 2.0](LICENSE). Preserve third-party notices and identify any
new third-party material in your contribution.
