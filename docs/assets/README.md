# MemStation brand assets

The **memory terminal** combines an arched station entrance with a folded,
brain-like memory canopy. Three recall paths converge at a docking platform:
a place for memories to arrive, stay and reconnect. The icon contains no letters.
Forest green and a sage accent connect it to Manager's light theme.

- [Original source](memstation-source.png): 1024px generated PNG with transparency.
- [512px PNG](../../apps/manager/static/brand/memstation.png): README and repository avatar.
- [Dark-background variant](../../apps/manager/static/brand/memstation-dark.png): pale monochrome export selected by both READMEs in dark mode.
- [96px PNG](../../apps/manager/static/brand/memstation-96.png): shared Manager `BrandMark`, including high-density displays.
- [32px favicon](../../apps/manager/static/favicon.png) and [16px favicon](../../apps/manager/static/favicon-16.png): pale rounded backing for light and dark browser tabs.
- [Apple touch icon](../../apps/manager/static/apple-touch-icon.png): opaque 180px export on a pale background.

The artwork was generated through Replicate CLI using
[`openai/gpt-image-2.5-flare`](https://replicate.com/openai/gpt-image-2.5-flare).
The selected source bitmap is retained so exports do not depend on an expiring
model delivery URL. Prompts, prediction IDs and intermediate concepts are local
agent archives, excluded from Git and the public asset documentation.

The source is a bitmap, not an SVG. Exports preserve its alpha and artwork,
trim excess canvas space, and apply consistent optical padding. To regenerate
them locally (no model request or API credentials needed):

```bash
pnpm --dir apps/manager exec playwright install chromium
node scripts/brand/generate.mjs
```

Original brand assets are covered by the project's [Apache-2.0 license](../../LICENSE).
`manager-preview.png`, `manager-memory.png` and `manager-mobile.png` show the current
Manager dashboard and Personal workspace browser, captured on 2026-10-25 with the
repository's synthetic Playwright fixtures. They contain no production conversations
or credentials. Design prototypes and validation captures stay in local agent archives and are not
used as product screenshots.

To capture the running UI again, install the locked dependencies and Chromium, then:

```bash
pnpm --dir apps/manager exec playwright install chromium
node scripts/brand/capture-manager.mjs
```

The script starts isolated fixture and Manager development servers on loopback
ports 18472 and 18471, signs in with the fixture token, checks the mobile viewport
for horizontal overflow and stops its own processes. Both ports must be available.
It does not use the local application's database, model endpoints or credentials.
