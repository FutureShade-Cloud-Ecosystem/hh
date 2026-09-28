// THE PHANTOM-TOKEN GATE, ported from the ERP's _theme-tokens.test.mjs section 2
// (DIB-400 and DIB-408): every var() reference in every css and Lit template in
// this package resolves to a definition inside the package. A token referenced
// but never defined silently falls back to its literal, or to unset, which is
// exactly how the ERP shipped white cards under a token that flips: invisible
// in review, visible in production, and explainable by nobody.
//
// The baseline is EMPTY and a lock test keeps it empty, on the ERP's terms: a
// phantom is mapped to the REAL token, never aliased, and never admitted as
// debt without deleting the lock in the same change.
//
// WHY THE TESTS ARE EXCLUDED FROM THE SCAN, stated here rather than left as a
// filter: these gate files name phantom tokens in their own prose (this header
// discusses `var(--hh-never-defined)` out loud), and a phantom in a test file
// paints nothing. Everything else that ships is in: src/** and harness/**,
// including the loader hook EXCLUDED below only as machinery (it defines no
// styles and references no tokens; the exclusion is named, not silent).

import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  definedTokens,
  readPkg,
  referencedTokens,
  shippedStyleFiles,
} from './_lib/sheet.mjs';

// PHANTOM_BASELINE is the per-file debt ledger: `'src/some-file.ts': ['--hh-whatever']`.
// It exists and it is EMPTY, and the lock below refuses to let it stop being
// empty. This package shipped with zero phantoms; a live allowlist would trade
// a gate that catches everything for one that catches only what someone
// remembered to leave out.
const PHANTOM_BASELINE = Object.freeze({});

test('tokens.css actually defines tokens (guards the parser above)', () => {
  const defined = definedTokens();
  // If the regex silently stopped matching, every phantom check below would
  // pass vacuously. This sheet defines well over a hundred names; a parse that
  // finds fewer has lost whole blocks.
  assert.ok(defined.size > 80, `expected tokens.css to define many tokens; parsed only ${defined.size}`);
  for (const anchor of ['--hh-text', '--hh-card', '--hh-line', '--hh-accent', '--brand', '--cbrand']) {
    assert.ok(defined.has(anchor), `tokens.css should define ${anchor}`);
  }
});

test('the file walker finds the package (guards the scan below)', () => {
  // Without this, a broken walk would scan zero files and the gate would pass
  // vacuously: the failure mode the phantom gate exists to prevent, one level up.
  const files = shippedStyleFiles();
  assert.ok(files.length >= 12, `expected the walker to find the package's sources; found ${files.length}`);
  for (const anchor of [
    'src/tokens.css',
    'src/lit.ts',
    'src/state.ts',
    'src/primitives.ts',
    'src/state-icons.ts',
    'src/kits/scan-and-confirm.ts',
    'src/kits/list-detail.ts',
    'src/kits/guided-flow.ts',
    'src/kits/dashboard.ts',
    'src/kits/inbox.ts',
    'src/harness-page.ts',
    'harness/serve.mjs',
  ]) {
    assert.ok(files.includes(anchor), `walker missed ${anchor}: its scan is not package-wide`);
  }
  const total = files.reduce((n, f) => n + referencedTokens(readPkg(f)).size, 0);
  assert.ok(total > 60, `expected many var() references across the package; parsed only ${total}`);
});

test('the reference scanner sees a phantom when one is in front of it (guards the gate below)', () => {
  // A synthetic string, not a package file: this proves the regex catches the
  // thing the gate exists to catch, so a clean result below is evidence and not
  // blindness. The planted name is deliberately never defined in tokens.css.
  const planted = 'color: var(--hh-totally-phantom, #fff); border-color: var(--hh-accent);';
  const refs = referencedTokens(planted);
  assert.ok(refs.has('--hh-totally-phantom'), 'the scanner missed the planted phantom');
  assert.ok(refs.has('--hh-accent'), 'the scanner missed the real token beside it');
});

test('NO file in the package references an undefined token', () => {
  const defined = definedTokens();
  const phantoms = [];
  for (const file of shippedStyleFiles()) {
    const allowed = new Set(PHANTOM_BASELINE[file] ?? []);
    for (const token of referencedTokens(readPkg(file))) {
      if (!defined.has(token) && !allowed.has(token)) phantoms.push(`${token} in ${file}`);
    }
  }
  assert.deepEqual(
    phantoms.sort(),
    [],
    `phantom design tokens (referenced, never defined in src/tokens.css): map each to the REAL token ` +
      `the style means, do not add an alias:\n  ${phantoms.sort().join('\n  ')}`,
  );
});

test('the phantom baseline is empty, and new debt may not be admitted quietly', () => {
  const entries = Object.entries(PHANTOM_BASELINE);
  assert.deepEqual(
    entries,
    [],
    `PHANTOM_BASELINE has grown entries. Adding to it WEAKENS the phantom gate, so it may only happen ` +
      `deliberately: map each phantom to the real token instead, or delete this lock test in the same ` +
      `change so the weakening is reviewed. Current entries:\n  ${entries
        .map(([f, t]) => `${f}: ${t.join(', ')}`)
        .join('\n  ')}`,
  );
});

test('a runtime-measured custom property lives outside the design-token namespaces', () => {
  // The ERP's --live-* rule, carried: a custom property SET AT RUNTIME from a
  // measurement is not a design token and must not squat on the sheet's
  // namespaces, where a reader greps for the design system and finds a pixel.
  // The package has no setProperty call site today, so the guard is synthetic:
  // it proves the scanner pattern sees the offence it exists to catch.
  const scan = (src) => [...src.matchAll(/setProperty\(\s*(['"`])(--[a-z0-9-]+)\1/g)].map((m) => m[2]);
  assert.deepEqual(
    scan(`el.setProperty('--hh-hub-height', px); el.setProperty('--live-hub-height', px);`),
    ['--hh-hub-height', '--live-hub-height'],
    'the setProperty scanner is stale',
  );
  const offenders = [];
  for (const file of shippedStyleFiles()) {
    const src = readPkg(file);
    for (const prop of scan(src)) {
      if (prop.startsWith('--hh-') || prop.startsWith('--brand') || prop.startsWith('--cbrand')) {
        offenders.push(`${prop} set at runtime in ${file}`);
      }
    }
  }
  assert.deepEqual(
    offenders.sort(),
    [],
    `a runtime-measured custom property must NOT use a design-token namespace: rename it outside ` +
      `the vocabulary (the --live-* precedent):\n  ${offenders.join('\n  ')}`,
  );
});

test('the tests are excluded from the scan, and the exclusion is honest', () => {
  // The exclusion is documentation, not silence: nothing under test/ or any
  // *.test.* file may appear in the walk, and this asserts it stays that way.
  const files = shippedStyleFiles();
  const leaked = files.filter((f) => f.startsWith('test/') || /\.test\./.test(f) || f.includes('_ts-js-loader'));
  assert.deepEqual(leaked, [], `test machinery leaked into the shipped-file scan: ${leaked.join(', ')}`);
});
