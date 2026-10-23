# Shared controls and dependency migration — 2026-10-23

## UI contract

- `Button` wraps Bits UI Button for all 84 ordinary action buttons, forwarding events, disabled state, attributes and refs. Dialog/Select/Collapsible triggers remain their dedicated Bits UI primitives. Existing shared button sizes and visual variants are preserved.

- `CollapsibleSection` is the only composition of Bits UI Collapsible. Memory records, recall explanations, chat/tool debugging, task forms and filter toolbars use it. The plain, panel and toolbar variants share focus, chevron, animation and reduced-motion behavior. Closed content is hidden and inert while retaining field state.
- `SelectField` is the only composition of Bits UI Select, including the target switcher, provider routes and statistics filters. It supports accessible hidden labels, custom option/selection snippets, disabled options, native form names, required fields, selection indicators, keyboard/typeahead navigation and viewport-aware portals.
- `TextInputField` and `TextareaField` wrap native inputs with Bits UI Label. Bits UI does not provide a generic text input primitive. Both forward native attributes/events and associate help plus error descriptions. The playground composer uses the shared textarea.
- Shared field height is 36px on desktop and at least 44px on touch/narrow layouts. Text inputs use 16px on mobile. No per-page `details`, Select composition or raw input/textarea remains.

## Versions

Versions were checked against npm registry, Go module metadata and official runtime release indexes on 2026-10-23. Manifests and lockfiles pin resolved versions; TypeScript stays on 6.x.

| Dependency | Before | After |
| --- | --- | --- |
| `@sveltejs/adapter-node` | `5.5.4` | `6.0.0` |
| `@sveltejs/kit` | `2.55.0` | `3.1.0` |
| `bits-ui` | `2.16.3` | `2.20.2` |
| `echarts` | `6.0.0` | `6.1.0` |
| `isomorphic-dompurify` | `3.3.0` | `4.6.0` |
| `marked` | `17.0.4` | `18.1.1` |
| `shadcn-svelte` | `1.1.1` | `1.7.0` |
| `shiki` | `4.0.2` | `4.5.0` |
| `svelte` | `5.53.12` | `5.57.2` |
| `@lucide/svelte` | `lucide-svelte 0.577.0` | `1.61.0` |
| `typescript` | `5.9.3` | `6.0.3` |
| `vite` | `8.0.0` | `8.3.5` |
| `vitest` | `4.1.0` | `5.0.5` |
| `@playwright/test` | `1.58.2` | `1.64.1` |
| `eslint` | `10.0.3` | `10.13.0` |
| `svelte-check` | `4.4.5` | `4.7.7` |
| Node | 24.11.1 | 26.12.1 |
| pnpm | 12.3.4 | 12.15.0 |
| Turbo | 2.8.16 | 2.11.10 |
| Go | 1.26.1 | 1.27.3 |
| Ent | 0.14.5 | 0.14.6 |
| gqlgen | 0.17.88 | 0.17.98 |
| chi | 5.2.5 | 5.5.0 |
| pgx | 5.8.0 | 5.11.0 |
| go-redis | 9.18.0 | 9.23.0 |
| gRPC | 1.79.2 | 1.84.0 |
| Protobuf | 1.36.11 | 1.36.12 |
| OTel core / log / SDK | 1.42.0 / 0.18.0 | 1.47.0 |
| golangci-lint | 2.13.2 | 2.15.0 |
| gofumpt / goimports | 0.9.2 / 0.43.0 | 0.12.0 / 0.52.0 |
| Buf | 1.67.0 | 1.75.0 |
| protoc-gen-go-grpc | 1.6.1 | 1.6.2 |

## Compatibility changes

- Followed the [SvelteKit 3 migration guide](https://svelte.dev/docs/kit/migrating-to-sveltekit-3): Vite plugin owns config, `#lib` subpath imports include extensions, TypeScript extends `$app/tsconfig`, assets use `asset`, navigation uses `reset`, and public runtime environment is declared in `src/env.ts`.
- The main layout uses runes and `$app/state`. Remaining legacy pages use a component-local `createPageStore` bridge; the snapshot is created inside component rendering to preserve SSR request isolation and reactive same-route navigation.
- Replaced deprecated `lucide-svelte` with `@lucide/svelte`. Pinned transitive `runed` to 0.38.0 because Bits UI / svelte-toolbelt otherwise select a release whose peer only supports SvelteKit 2. The upgraded graph passes `pnpm peers check`; interaction tests cover the affected primitives.
- OTel uses `otel.SetLoggerProvider`. Removed chi's deprecated RealIP middleware; client-supplied forwarded headers no longer rewrite `RemoteAddr`. Current authentication/rate limits are keyed by API key/user, not client IP.
- Regenerated Ent, GraphQL and Protobuf artifacts using the upgraded toolchain. gqlgen's deferred-response structure changed, so generated code must accompany the dependency update.
- Docker, devcontainer, CI and local bootstrap scripts use the same Node/Go/pnpm baseline. PostgreSQL/Redis storage versions are unchanged; no existing database was migrated during this work.
- Atlas CLI remains 0.37.0 for the existing CE migration-lint gate; later versions require Pro for that gate. The Atlas Go library follows Ent's compatible dependency. These are deliberate exceptions to latest-version upgrades.

## Validation

All checks below ran locally. Artifacts are under `.artifacts/ui-dependencies-20261023/`.

| Check | Result |
| --- | --- |
| Manager lint / Svelte check | 0 errors, 0 warnings |
| Vitest 5 | 15 / 15 tests |
| Playwright 1.64 | 48 / 48: Chromium 36, mobile WebKit 12 |
| Production adapter-node build | Passed on Node 26.12.1 |
| UI verification | Tasks, memory detail, providers and usage at 1440 / 390 / 320px; screenshots and `visual/report.json` |
| Go full suite with disposable PostgreSQL 16.14 / pgvector | All 25 test-bearing packages passed |
| gRPC / protocol contracts after generator upgrade | Passed |
| Go format / golangci-lint | Passed, 0 lint issues |
| Repeated `go generate ./...` | Working-tree diff unchanged, including go.mod/go.sum |
| Atlas CE lint / dry-run / disposable DB migration | Passed; existing column-alignment advisories remain nonblocking |
| Worker recovery smoke | 64 conversations, 896 tasks completed |
| Backup / restore | Dry-run passed; no claim of a live restore drill |
| Dependency peers / git whitespace | Passed |

Coverage includes default-open and independently collapsible panels, Enter/Space toggles, reduced motion, preserved drafts, Select keyboard selection/Escape focus return, portal interaction inside short dialogs, 44px touch controls and 16px mobile text inputs. The fixture now implements generation listing so the default-open lifecycle panel is exercised instead of silently receiving a 404. Code highlighting renders an escaped text fallback until the client highlighter is ready, avoiding raw-HTML hydration mismatches.

The existing uncommitted graph-memory work is preserved. Generation determinism is checked against a snapshot of that working tree, because HEAD was already dirty. Browser evidence uses fixture data and WebKit device emulation, not a physical iPhone. Remote CI and production deployment were outside this run.
