// THE QUOTE ACCENT (decision log rows 1047 item 7 and 1063): quotes and their documents read in an
// indigo quote blue, orders and the till keep gold. This file pins the five tokens as the board
// records them, each measured pair against its board ratio, the one-value fill, and the gold
// family's values byte unchanged. The generic matrices in contrast.test.mjs run over the same
// tokens; this file names the pairs, so a re-value fails naming the pair.

import { test } from 'node:test';
import assert from 'node:assert/strict';

import { PALETTE_NAMES, contrast, palette, parseDeclarations, resolveColor, sheetBlocks } from './_lib/sheet.mjs';

const AA = 4.5;
const NON_TEXT = 3.0;

const norm = (v) => v.replace(/\s+/g, '').toUpperCase();

// [token, light, brand mode]
const QUOTE = [
  ['--hh-quote', '#1C45B8', '#A3BCFF'],
  ['--hh-quote-fill', '#1D4ED8', '#1D4ED8'],
  ['--hh-quote-fill-on', '#FFFFFF', '#FFFFFF'],
  ['--hh-quote-link', '#1C45B8', '#A3BCFF'],
  ['--hh-quote-tint', '#E8EDFB', '#242938'],
];

test('the five quote tokens carry the board values in brand mode and in light', () => {
  const wrong = [];
  for (const [tok, light, dark] of QUOTE) {
    if (norm(palette('light')[tok] ?? '') !== norm(light)) wrong.push(`${tok} light: want ${light}, have ${palette('light')[tok]}`);
    if (norm(palette('dark')[tok] ?? '') !== norm(dark)) wrong.push(`${tok} brand: want ${dark}, have ${palette('dark')[tok]}`);
  }
  assert.deepEqual(wrong, []);
});

test('the fill, its ink are declared once on :root and re-valued by no light or hh-pro block', () => {
  const blocks = sheetBlocks();
  const dark = parseDeclarations(blocks.dark);
  for (const tok of ['--hh-quote-fill', '--hh-quote-fill-on']) {
    assert.ok(tok in dark, `${tok} is declared in :root`);
    for (const half of ['light', 'hhproDark', 'hhproLight']) {
      assert.ok(!(tok in parseDeclarations(blocks[half])), `${tok} is one value in both modes; ${half} re-values it`);
    }
  }
  for (const tok of ['--hh-quote', '--hh-quote-link', '--hh-quote-tint']) {
    assert.ok(tok in parseDeclarations(blocks.light), `${tok} is per-theme and must be declared in the light half`);
  }
});

test('hh-pro keeps the board values: the quote blue is the same in every palette', () => {
  for (const [tok, light, dark] of QUOTE) {
    assert.equal(norm(palette('hh-pro dark')[tok]), norm(dark), `${tok} in hh-pro dark`);
    assert.equal(norm(palette('hh-pro light')[tok]), norm(light), `${tok} in hh-pro light`);
  }
});

const ratio = (v, a, b) => contrast(resolveColor(v[a], v), resolveColor(v[b], v));

// [palette, ink, ground, board ratio]
const MEASURED = [
  ['light', '--hh-quote', '--hh-card', 8.10],
  ['light', '--hh-quote', '--hh-page', 6.81],
  ['light', '--hh-quote-link', '--hh-card', 8.10],
  ['dark', '--hh-quote', '--hh-page', 7.88],
  ['dark', '--hh-quote', '--hh-card', 6.74],
  ['dark', '--hh-quote-link', '--hh-page', 7.88],
  ['light', '--hh-quote-fill-on', '--hh-quote-fill', 6.70],
  ['dark', '--hh-quote-fill-on', '--hh-quote-fill', 6.70],
  ['light', '--hh-quote', '--hh-quote-tint', 6.92],
  ['dark', '--hh-quote', '--hh-quote-tint', 7.72],
];

test('every pair on the board measures what the board records', () => {
  for (const [name, ink, ground, board] of MEASURED) {
    const r = ratio(palette(name), ink, ground);
    assert.ok(r >= AA, `${ink} on ${ground} in ${name}: ${r.toFixed(2)}:1 is below ${AA}:1`);
    assert.ok(Math.abs(r - board) < 0.02, `${ink} on ${ground} in ${name}: ${r.toFixed(2)}:1, the board records ${board}`);
  }
});

test('the fill is a shape: 3:1 on the card and the page in light, and by its fill-on border on navy', () => {
  const light = palette('light');
  assert.ok(Math.abs(ratio(light, '--hh-quote-fill', '--hh-card') - 6.70) < 0.02, 'fill on the light card');
  assert.ok(Math.abs(ratio(light, '--hh-quote-fill', '--hh-page') - 5.64) < 0.02, 'fill on the light page');
  for (const name of PALETTE_NAMES) {
    const v = palette(name);
    for (const surface of ['--hh-page', '--hh-card']) {
      const best = Math.max(ratio(v, '--hh-quote-fill', surface), ratio(v, '--hh-quote-fill-on', surface));
      assert.ok(best >= NON_TEXT, `the quote control's boundary on ${surface} in ${name}: ${best.toFixed(2)}:1`);
    }
  }
});

test('the gold family is byte unchanged and never aliased to the quote tokens', () => {
  const dark = palette('dark');
  assert.equal(dark['--hh-accent'], '#FFC313');
  assert.equal(dark['--hh-accent-hover'], '#FFD45B');
  assert.equal(dark['--hh-accent-ink'], '#0B1725');
  assert.equal(dark['--hh-accent-text'], '#FFC313');
  assert.equal(palette('light')['--hh-accent-text'], '#896400');
  for (const name of PALETTE_NAMES) {
    for (const [tok, value] of Object.entries(palette(name))) {
      if (tok.startsWith('--hh-accent') || tok === '--hh-focus-ring') {
        assert.ok(!/quote/.test(value), `${tok} in ${name} points at the quote family: ${value}`);
      }
    }
  }
});
