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
[`openai/gpt-image-2.5-flare`](https://replicate.com/openai/gpt-image-2.5-flare):
three `low` quality concepts followed by a `medium` quality refinement of the
memory terminal. [Generation record](logo-generation.json) preserves the exact
prompts, settings and prediction IDs; the selected [reference concept](memstation-concept.png)
is retained locally so the refinement does not depend on an expiring delivery URL. The API reports the model version as
`hidden`; the record does not imply a pinned, reproducible model revision.

The source is a bitmap, not an SVG. Exports preserve its alpha and artwork,
trim excess canvas space, and apply consistent optical padding. To regenerate
them locally (no model request or API credentials needed):

```bash
pnpm --dir apps/manager exec playwright install chromium
node scripts/brand/generate.mjs
```

Original brand assets are covered by the project's [Apache-2.0 license](../../LICENSE).
`manager-preview.png` is a screenshot of Manager using the repository's synthetic
Playwright fixture data; it contains no production conversations or credentials.
