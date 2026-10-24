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

The 2026-10-25 local inventory of the server's 57 runtime Go modules and Manager's
153 installed production package entries identified permissive licenses and
MPL-2.0 components. In particular:

- [`github.com/hashicorp/hcl/v2`](https://github.com/hashicorp/hcl/tree/v2.18.1) retains **MPL-2.0**; its source files are not
  relicensed by MemStation's Apache license.
- [`lightningcss`](https://github.com/parcel-bundler/lightningcss/tree/v1.33.0) and its platform package retain **MPL-2.0**.
- [`dompurify`](https://github.com/cure53/DOMPurify/tree/3.4.17) is dual-licensed **MPL-2.0 OR Apache-2.0**.
- [`github.com/coder/websocket`](https://github.com/coder/websocket/tree/v1.8.15) retains its **ISC** license.

This is an inventory of the installed checkout, not a permanent bill of materials.
Recheck resolved packages and preserve their actual license/NOTICE files when
packaging a release. Changes to MPL-covered files retain their applicable source
and notice obligations; even unchanged MPL components require source-availability
information when distributed in executable form. The links above identify the
corresponding upstream sources. See the [Mozilla FAQ](https://www.mozilla.org/en-US/MPL/2.0/FAQ/)
and [publication scope](docs/open-source.md).

## Models and services

Model weights, Ollama downloads and hosted model accounts are not bundled with the
repository. The MemStation license does not grant rights to third-party models,
datasets or API services. Review their own terms for your chosen deployment.

## Evaluation datasets

- The repository's [native golden dataset and small synthetic adapter fixtures](apps/server/tests/eval/testdata)
  are distributed with MemStation under Apache-2.0. The LOCOMO-shaped and
  LongMemEval-shaped fixture filenames identify a format, not bundled upstream data.
- **LOCOMO** is external data from [Snap Research](https://github.com/snap-research/locomo),
  licensed under **CC BY-NC 4.0** as reviewed on 2026-09-09. Raw LOCOMO data is not
  bundled with MemStation and is not covered by the project's Apache license.
- Other externally downloaded datasets, including LongMemEval, remain subject to
  their upstream terms. Adapters do not relicense those datasets.

Keep downloaded datasets and evaluation artifacts outside source control. See the
[evaluation review](apps/server/tests/eval/REVIEW-2026-09-09.md) for pinned upstream
revisions and methodology.
