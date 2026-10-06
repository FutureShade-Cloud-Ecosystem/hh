// The kit's half of the add-path census, carried over from the Counter Till's
// app tree when the product grid moved here (the app's copy, adapted from the
// reference's whole-tree census half, keeps its claim over its own tree and
// names this copy for the grid). The claim: every surface in this tree that
// renders an add affordance dispatches through the shared double-add guard,
// hands it the ticket generation unconditionally, and imports the rule rather
// than copying it. Node built-in runner, same loader as the other ported
// suites.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = new URL('.', import.meta.url).pathname;

// The whole ported tree, walked (the same whole-tree claim the reference's
// census made, over this package's own layout).
const sources = new Map();
for (const entry of readdirSync(here)) {
  if (entry.endsWith('.ts') && !entry.endsWith('.test.ts')) {
    sources.set(entry, readFileSync(join(here, entry), 'utf8'));
  }
}

// 1 · the census: which files dispatch an add through the guard. A SECOND add
// surface appearing anywhere in the kit's tree fails this, exactly as a
// fourth surface failed the reference's walk and a fourth the app's.
const surfaces = [...sources.entries()]
  .filter(([rel, code]) => rel !== '_pos-add-dedupe.ts' && code.includes('this.adder.add('))
  .map(([rel]) => rel)
  .sort();
assert.deepEqual(
  surfaces,
  ['pos-product-grid.ts'],
  'the one surface in this tree that dispatches an add through the shared guard',
);

// 2 · it takes the rule from the shared module rather than restating it,
// and hands the ticket generation to the guard unconditionally (the bare CALL
// is what the reference's round settled on: a condition a `false &&` could
// hang off no longer exists).
for (const rel of surfaces) {
  const code = sources.get(rel);
  assert.match(code, /from '\.\/_pos-add-dedupe\.js'/, `${rel} must import the rule, not copy it`);
  assert.match(code, /this\.adder\.ticketAt\(/, `${rel} must hand the ticket generation to the guard`);
}
