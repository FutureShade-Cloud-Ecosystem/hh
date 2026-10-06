// Ported from hardscapeos_dibbits master 87d7941b app/src/_pos-add-dedupe.test.mjs (the
// whole-tree census half, rebased onto this repository's tree); adapted for HH.

// The wiring item's own census, owed by the port (CLOSURE.md, "Reference tests
// brought along"): the reference's census walked ITS src tree and named its
// add surfaces. This one walks the ported surface's tree and holds the same
// claim over it: every surface that renders an add affordance dispatches
// through the shared double-add guard, hands it the ticket generation
// unconditionally, and imports the rule rather than copying it. Node built-in
// runner, same loader as the other ported suites.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readdirSync, readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { join } from 'node:path';

const here = new URL('.', import.meta.url).pathname;

// The whole ported tree, walked (the same whole-tree claim the reference's
// census made, over this repository's own layout).
const sources = new Map();
for (const entry of readdirSync(here)) {
  if (entry.endsWith('.ts') && !entry.endsWith('.test.ts')) {
    sources.set(entry, readFileSync(join(here, entry), 'utf8'));
  }
}

// 1 · the census: which files dispatch an add through the guard. A FOURTH add
// surface appearing anywhere in the ported tree fails this, exactly as a
// fourth surface failed the reference's walk.
const surfaces = [...sources.entries()]
  .filter(([rel, code]) => rel !== '_pos-add-dedupe.ts' && code.includes('this.adder.add('))
  .map(([rel]) => rel)
  .sort();
// HH note: the product grid moved into @hardscapehouse/pos-kit
// (web/packages/pos-kit, src/ported); the grid's half of this census moved
// with it and runs there as this tree's own census copy. The app tree keeps
// these two surfaces.
assert.deepEqual(
  surfaces,
  ['pos-hot-list.ts', 'pos-search-drop.ts'],
  'the two surfaces left in this tree that dispatch an add through the shared guard',
);

// 2 · each takes the rule from the shared module rather than restating it,
// and hands the ticket generation to the guard unconditionally (the bare CALL
// is what the reference's round settled on: a condition a `false &&` could
// hang off no longer exists).
for (const rel of surfaces) {
  const code = sources.get(rel);
  // The shared rule itself moved into @hardscapehouse/pos-kit, so the
  // surfaces here import it by the kit's deep path; either spelling must be
  // an import, never a local copy.
  assert.match(
    code,
    /from '(?:\.\/|@hardscapehouse\/pos-kit\/src\/ported\/)_pos-add-dedupe\.js'/,
    `${rel} must import the rule, not copy it`,
  );
  assert.match(code, /this\.adder\.ticketAt\(/, `${rel} must hand the ticket generation to the guard`);
}

// 3 · the terminal itself takes NO add path around the guard: its own add
// entry (addProduct, the one funnel the census's callers all land in) is the
// single writer of cart lines, so the rule holds structurally: the guard
// swallows the echo before the funnel is reached.
const terminal = sources.get('pos-terminal.ts');
assert.match(terminal, /private async addProduct\(/, 'the funnel exists');
assert.doesNotMatch(
  terminal,
  /this\.adder\./,
  'the funnel itself stays behind the guards: the echo rule fires in the surfaces, before the add',
);
