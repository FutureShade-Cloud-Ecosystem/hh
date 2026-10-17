// Builds the static demo pages to demo/, committed: the page the fidelity
// pass renders must exist as files, not only under a server. Two variants
// ship as files, so the double-click page shows both themes without a
// server: index.html (brand mode, the default) and index-light.html. The
// build also writes each element's sheet once, to demo/sheets/<tag>.css;
// that folder travels with the pages (a page copied without it renders
// unstyled). test/harness.test.mjs holds the committed bytes to a fresh
// build.
//
//   node --import ./test/_ts-js-loader.mjs demo/build.mjs

import { mkdirSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

import { buildDemoPage, demoSheets } from './demo-page.js';

const here = dirname(fileURLToPath(import.meta.url));

const pages = [
  ['index.html', { theme: 'dark' }],
  ['index-light.html', { theme: 'light' }],
];
for (const [name, opts] of pages) {
  const out = join(here, name);
  writeFileSync(out, buildDemoPage(opts));
  console.log(`wrote ${out}`);
}

const sheetsDir = join(here, 'sheets');
mkdirSync(sheetsDir, { recursive: true });
for (const { tag, css } of demoSheets()) {
  const sheetPath = join(sheetsDir, `${tag}.css`);
  writeFileSync(sheetPath, css);
  console.log(`wrote ${sheetPath}`);
}
