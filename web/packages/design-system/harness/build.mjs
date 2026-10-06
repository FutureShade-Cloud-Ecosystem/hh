// Builds the static harness pages to harness/, committed: the pages Colton
// and Grant open at the design sync must exist as files, not only under a
// Node server. Four variants ship as files, so the double-click page shows
// both themes and both brands without a server: index.html (dark, base brand,
// the default), index-light.html, index-hh-pro.html and index-light-hh-pro.html.
// Each cell of each page paints inside its own declarative shadow root, so the
// build also writes each kit's stylesheet once, to harness/sheets/<tag>.css,
// for the cells to link; the sheets folder travels with the pages (a page
// without it renders unstyled). test/harness.test.mjs holds the committed
// bytes of all four pages and every sheet to a fresh build, so a stale file
// fails the suite.
//
//   node --import ./src/_ts-js-loader.mjs harness/build.mjs

import { mkdirSync, readFileSync, readdirSync, statSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

import { buildHarnessPage, harnessSheets } from '../src/harness-page.js';

const here = dirname(fileURLToPath(import.meta.url));
const pages = [
  ['index.html', { theme: 'dark', brand: 'base' }],
  ['index-light.html', { theme: 'light', brand: 'base' }],
  ['index-hh-pro.html', { theme: 'dark', brand: 'hh-pro' }],
  ['index-light-hh-pro.html', { theme: 'light', brand: 'hh-pro' }],
];
for (const [name, opts] of pages) {
  const out = join(here, name);
  writeFileSync(out, buildHarnessPage(opts));
  console.log(`wrote ${out}`);
}

const sheetsDir = join(here, 'sheets');
mkdirSync(sheetsDir, { recursive: true });
const written = [];
for (const { tag, css } of harnessSheets()) {
  const sheetPath = join(sheetsDir, `${tag}.css`);
  writeFileSync(sheetPath, css);
  written.push(sheetPath);
}
for (const p of written) console.log(`wrote ${p}`);

// The vendored face travels with the pages the same way the sheets do: the
// token sheet the build inlines registers Inter through @font-face URLs
// relative to the page (./fonts/...), so the static pages open from a file
// with the face beside them and the served variant finds them at /fonts/.
// Byte-for-byte copies of src/fonts (the woff2 quartet and the OFL licence),
// held exact by the harness drift test.
const fontsSrc = join(here, '..', 'src', 'fonts');
const fontsDir = join(here, 'fonts');
mkdirSync(fontsDir, { recursive: true });
for (const name of readdirSync(fontsSrc)) {
  const from = join(fontsSrc, name);
  if (!statSync(from).isFile()) continue;
  const to = join(fontsDir, name);
  writeFileSync(to, readFileSync(from));
  written.push(to);
}
for (const p of written.slice(-1)) console.log(`wrote ${fontsDir}/ (${written.length - harnessSheets().length} font files)`);
