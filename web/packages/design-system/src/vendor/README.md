# Vendored: modern-screenshot

The in-page screen capture the feedback widget's "Capture this screen" button
uses. The source bytes live here, in the repository, and nothing is fetched at
runtime: no CDN, no package dependency, no network read of any kind.

- Upstream: `modern-screenshot` (npm), version 4.7.0, MIT licence (the licence
  file sits beside this README).
- Upstream file: `dist/index.mjs`, the library's zero-dependency ESM build.
- Upstream sha256: `d6ce0ad42851eb3c6a56b89ab15839f5eaee512df6b27845c42e58d3605dd945`
- This file's sha256 (after the one patch below):
  `f92ccddfd06f707fe67056e856e12fb72f504307c9071a59c163d65a35db579c`

## The one patch

A single change from the upstream bytes, marked inline with a `VENDOR PATCH
(hh, Feature Polish)` comment: the SVG use-element fallback appended fetched
markup through a raw parser path (`insertAdjacentHTML`); the patched build
appends nodes parsed by `DOMParser` instead, so no string reaches the HTML
parser and the design system's raw markup gate (test/no-raw-markup.test.mjs)
holds over vendored code exactly as it holds over the package's own sources.
The path only runs for cross file SVG `use` references, which none of this
repository's apps exercise; the patch is semantic parity for the case that
does.

## Why this library

It renders the live DOM (shadow roots included; it walks
`node.shadowRoot`) into an SVG foreignObject and rasterises it, in the page,
with no service call and no dependency of its own. The capture filter hides
the widget itself from its own shot. Regenerating or upgrading means
re-vendoring the upstream file, re-applying the marked patch, and updating
both checksums here.
