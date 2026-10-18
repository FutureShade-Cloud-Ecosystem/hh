// THE QUOTE ACCENT (decision log rows 1047 item 7 and 1063): quotes and their documents read in an
// indigo quote blue, orders and the till keep gold. This file pins the five tokens as the board
// records them, each measured pair against its board ratio, the per theme fill, and the gold
// family's values byte unchanged. The generic matrices in contrast.test.mjs run over the same
// tokens; this file names the pairs, so a re-value fails naming the pair. Brand mode is charcoal
// (row 1763, palette B): the charcoal ink and link are the Quote Desk boards' dusty blues
// (#86A3C9, 6.80 on the page and 6.09 on the card; #9DB4CC links, 8.26 and 7.40), and the quote
// button follows the boards too: #86A3C9 carrying #0B1725 (6.96:1), a shape by itself. Light keeps
// the D-077 blue #1D4ED8 carrying white (6.70:1).

import { test } from 'node:test';
import assert from 'node:assert/strict';

import { PALETTE_NAMES, contrast, palette, parseDeclarations, resolveColor, sheetBlocks } from './_lib/sheet.mjs';

const AA = 4.5;
const NON_TEXT = 3.0;

const norm = (v) => v.replace(/\s+/g, '').toUpperCase();

// [token, light, brand mode]
const QUOTE = [
  ['--hh-quote', '#1C45B8', '#86A3C9'],
  ['--hh-quote-fill', '#1D4ED8', '#86A3C9'],
  ['--hh-quote-fill-on', '#FFFFFF', '#0B1725'],
  ['--hh-quote-link', '#1C45B8', '#9DB4CC'],
  ['--hh-quote-tint', '#E8EDFB', '#1D242C'],
];

test('the five quote tokens carry the board values in brand mode and in light', () => {
  const wrong = [];
  for (const [tok, light, dark] of QUOTE) {
    if (norm(palette('light')[tok] ?? '') !== norm(light)) wrong.push(`${tok} light: want ${light}, have ${palette('light')[tok]}`);
    if (norm(palette('dark')[tok] ?? '') !== norm(dark)) wrong.push(`${tok} brand: want ${dark}, have ${palette('dark')[tok]}`);
  }
  assert.deepEqual(wrong, []);
});

test('every quote token is per theme: declared in :root and in the light half, and re-valued by no hh-pro block', () => {
  const blocks = sheetBlocks();
  const dark = parseDeclarations(blocks.dark);
  for (const tok of ['--hh-quote-fill', '--hh-quote-fill-on']) {
    assert.ok(tok in dark, `${tok} is declared in :root`);
    for (const half of ['hhproDark', 'hhproLight']) {
      assert.ok(!(tok in parseDeclarations(blocks[half])), `${tok} keeps the house values; ${half} re-values it`);
    }
  }
  for (const tok of ['--hh-quote', '--hh-quote-link', '--hh-quote-tint', '--hh-quote-fill', '--hh-quote-fill-on']) {
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
  ['dark', '--hh-quote', '--hh-page', 6.80],
  ['dark', '--hh-quote', '--hh-card', 6.09],
  ['dark', '--hh-quote-link', '--hh-page', 8.26],
  ['light', '--hh-quote-fill-on', '--hh-quote-fill', 6.70],
  ['dark', '--hh-quote-fill-on', '--hh-quote-fill', 6.96],
  ['light', '--hh-quote', '--hh-quote-tint', 6.92],
  ['dark', '--hh-quote', '--hh-quote-tint', 6.04],
];

test('every pair on the board measures what the board records', () => {
  for (const [name, ink, ground, board] of MEASURED) {
    const r = ratio(palette(name), ink, ground);
    assert.ok(r >= AA, `${ink} on ${ground} in ${name}: ${r.toFixed(2)}:1 is below ${AA}:1`);
    assert.ok(Math.abs(r - board) < 0.02, `${ink} on ${ground} in ${name}: ${r.toFixed(2)}:1, the board records ${board}`);
  }
});

test('the fill is a shape by itself: 3:1 on the card and the page in light and in brand mode', () => {
  const light = palette('light');
  assert.ok(Math.abs(ratio(light, '--hh-quote-fill', '--hh-card') - 6.70) < 0.02, 'fill on the light card');
  assert.ok(Math.abs(ratio(light, '--hh-quote-fill', '--hh-page') - 5.64) < 0.02, 'fill on the light page');
  const dark = palette('dark');
  assert.ok(Math.abs(ratio(dark, '--hh-quote-fill', '--hh-page') - 6.80) < 0.02, 'fill on the charcoal page');
  assert.ok(Math.abs(ratio(dark, '--hh-quote-fill', '--hh-card') - 6.09) < 0.02, 'fill on the charcoal card');
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
