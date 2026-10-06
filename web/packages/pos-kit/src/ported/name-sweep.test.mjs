// The name sweep (the train review's P2-3), carried over from the Counter
// Till's app tree when the shared pieces moved into this package: no real
// person's name and no real dealer's trading name anywhere in this package's
// tree. The till's own copy of this gate keeps its claim over the app's tree;
// this copy makes the same claim over the kit's tree, so a future port or
// paste cannot bring any of it back through the kit.
//
// WHY THE NEEDLES ARE ENCODED. A sweep test that carried its own needles in
// plain text would fail its own assertion: the names would still ship in the
// tree, inside the test. So the three strings (the two persons' names, then
// the dealer's trading name) are held base64-encoded and decoded in memory
// only.
//
// THE ONE ALLOWANCE. The source repository's own name contains the dealer's
// string, and the porting rule requires the provenance headers to name the
// source repository and sha (the review accepted those headers). The test
// scrubs that one token before matching, so a hit survives only as part of the
// repository's name, never anywhere else.
//
// node --import ./src/_ts-js-loader.mjs --test 'src/ported/*.test.mjs' (run from the kit)

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, readdirSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join, relative } from 'node:path';

const here = dirname(fileURLToPath(import.meta.url));
const kitRoot = join(here, '..', '..');

const decode = (b64) => Buffer.from(b64, 'base64').toString('utf8');

// Two real persons' full names, then the first dealer's trading name.
const NEEDLES = ['UnlhbiBSb29k', 'UnlhbiBCb3N0b2Nr', 'RGliYml0cw=='].map(decode);

// The source repository's name: the only carrier the dealer's string keeps.
const REPO_TOKEN = decode('aGFyZHNjYXBlb3NfZGliYml0cw==');

const TEXT_SUFFIXES = new Set([
  '.ts', '.mjs', '.js', '.css', '.html', '.json', '.md', '.yaml', '.yml', '.svg', '.txt',
]);
const SKIP_DIRS = new Set([
  'node_modules', 'dist', '.vite', 'coverage', 'test-results', 'playwright-report',
]);

function walk(dir, out = []) {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    if (entry.isDirectory()) {
      if (!SKIP_DIRS.has(entry.name)) walk(join(dir, entry.name), out);
    } else {
      const dot = entry.name.lastIndexOf('.');
      if (dot !== -1 && TEXT_SUFFIXES.has(entry.name.slice(dot))) out.push(join(dir, entry.name));
    }
  }
  return out;
}

const escapeRegex = (s) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');

test('name sweep: no real person or dealer name in the kit tree, outside the provenance repo name', () => {
  const offenders = [];
  for (const file of walk(kitRoot)) {
    const text = readFileSync(file, 'utf8');
    const scrubbed = text.replace(new RegExp(escapeRegex(REPO_TOKEN), 'gi'), '');
    for (const needle of NEEDLES) {
      if (scrubbed.toLowerCase().includes(needle.toLowerCase())) offenders.push(relative(kitRoot, file));
    }
  }
  assert.deepEqual(
    offenders,
    [],
    `real names found in the tree (sweep them to role words or invented demo names): ${offenders.join(', ')}`,
  );
});
