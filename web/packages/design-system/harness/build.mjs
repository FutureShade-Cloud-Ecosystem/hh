// Builds the static harness page to harness/index.html, committed: the page
// Colton and Grant open at the design sync must exist as a file, not only
// under a Node server. test/harness.test.mjs holds the committed bytes to a
// fresh build, so a stale file fails the suite.
//
//   node --import ./src/_ts-js-loader.mjs harness/build.mjs

import { writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

import { buildHarnessPage } from '../src/harness-page.js';

const here = dirname(fileURLToPath(import.meta.url));
const out = join(here, 'index.html');
writeFileSync(out, buildHarnessPage({ theme: 'dark', brand: 'base' }));
console.log(`wrote ${out}`);
