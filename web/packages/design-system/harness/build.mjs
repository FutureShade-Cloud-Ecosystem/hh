// Builds the static harness page to harness/index.html, committed: the page
// Colton and Grant open at the design sync must exist as a file, not only
// under a Node server. Each cell of the page paints inside its own declarative
// shadow root, so this build also writes each kit's stylesheet once, to
// harness/sheets/<tag>.css, for the cells to link. test/harness.test.mjs holds
// the committed bytes to a fresh build, page and sheets both, so a stale file
// fails the suite.
//
//   node --import ./src/_ts-js-loader.mjs harness/build.mjs

import { mkdirSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

import { buildHarnessPage, harnessSheets } from '../src/harness-page.js';

const here = dirname(fileURLToPath(import.meta.url));
const out = join(here, 'index.html');
writeFileSync(out, buildHarnessPage({ theme: 'dark', brand: 'base' }));

const sheetsDir = join(here, 'sheets');
mkdirSync(sheetsDir, { recursive: true });
const written = [];
for (const { tag, css } of harnessSheets()) {
  const sheetPath = join(sheetsDir, `${tag}.css`);
  writeFileSync(sheetPath, css);
  written.push(sheetPath);
}
console.log(`wrote ${out}`);
for (const p of written) console.log(`wrote ${p}`);
