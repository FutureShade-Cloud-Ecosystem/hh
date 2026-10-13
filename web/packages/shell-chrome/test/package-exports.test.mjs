// A shell imports the chrome by the package name, never by a relative path:
// each of the four elements and the state module must answer to it, and the
// package must not claim to be free of side effects, because importing an
// element module registers its custom element.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

const pkg = JSON.parse(readFileSync(new URL('../package.json', import.meta.url), 'utf8'));

// Literal specifiers only: the design system's gate refuses a computed dynamic import in a dependent.
const ELEMENTS = [
  ['shell-header', 'ShellHeader', () => import('@hardscapehouse/shell-chrome/src/shell-header.js')],
  ['shell-rail', 'ShellRail', () => import('@hardscapehouse/shell-chrome/src/shell-rail.js')],
  ['shell-banner', 'ShellBanner', () => import('@hardscapehouse/shell-chrome/src/shell-banner.js')],
  ['shell-update', 'ShellUpdate', () => import('@hardscapehouse/shell-chrome/src/shell-update.js')],
];

for (const [file, exported, load] of ELEMENTS) {
  test(`${file} imports by the package name and exports ${exported}`, async () => {
    const mod = await load();
    assert.equal(typeof mod[exported], 'function', `${exported} is exported`);
  });
}

test('the state module still answers on the package root', async () => {
  const mod = await import('@hardscapehouse/shell-chrome');
  assert.equal(typeof mod.PinBook, 'function');
});

test('sideEffects is not false: the element modules register custom elements when imported', () => {
  assert.notEqual(pkg.sideEffects, false);
  const listed = Array.isArray(pkg.sideEffects) ? pkg.sideEffects : [];
  for (const [file] of ELEMENTS) {
    assert.ok(
      listed.some((entry) => entry.endsWith(`${file}.ts`)),
      `${file}.ts is listed as having side effects`,
    );
  }
});
