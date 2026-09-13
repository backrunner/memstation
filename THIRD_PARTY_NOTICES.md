# Third-party notices

MemStation's original source code and brand assets are licensed under Apache-2.0.
Dependencies and third-party assets retain their own licenses; the project license
does not replace those terms.

## Bundled font

- **IBM Plex Sans**, Latin variable WOFF2, Google Fonts v23.
- Copyright © 2017 IBM Corp. with Reserved Font Name "Plex".
- License: **SIL Open Font License 1.1**.
- Files, source URL and license: [apps/manager/static/fonts](apps/manager/static/fonts).

Retain the font's [copyright and license notice](apps/manager/static/fonts/OFL-IBM-Plex-Sans.txt)
when redistributing Manager or its static assets.

## Dependencies

Go modules are recorded in [go.mod](apps/server/go.mod) and `go.sum`;
JavaScript packages are recorded in the workspace manifests and `pnpm-lock.yaml`.
Their upstream copyright notices, licenses and applicable NOTICE files remain in
effect when distributing source, binaries or container images that include them.

## Evaluation datasets

- The repository's [native golden dataset](apps/server/tests/eval/testdata) is
  distributed with MemStation under Apache-2.0.
- **LOCOMO** is external data from [Snap Research](https://github.com/snap-research/locomo),
  licensed under **CC BY-NC 4.0** as reviewed on 2026-09-09. Raw LOCOMO data is not
  bundled with MemStation and is not covered by the project's Apache license.
- Other externally downloaded datasets, including LongMemEval, remain subject to
  their upstream terms. Adapters do not relicense those datasets.

Keep downloaded datasets and evaluation artifacts outside source control. See the
[evaluation review](apps/server/tests/eval/REVIEW-2026-09-09.md) for pinned upstream
revisions and methodology.
