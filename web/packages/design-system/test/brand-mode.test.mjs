// BRAND MODE, the dark half's values (decision log row 1047 item 10, ruling 1119, option A).
// The dark option is the HH badge's navy, calmer than the rich blue it replaced; the token
// NAMES are the dark set's, so only the values move. This file pins the set as the design
// system's "brand mode" board (option A) records it, and the contrast of every new pair against
// the sheet's own bars: 4.5:1 for text, 3:1 for a boundary judged as a shape. The generic
// matrices in contrast.test.mjs run over the same palette and catch a pair that slips; this
// file names the pairs the board measured, so a re-value fails naming the pair.

import { test } from 'node:test';
import assert from 'node:assert/strict';

import { contrast, palette, resolveColor } from './_lib/sheet.mjs';

const AA = 4.5;
const NON_TEXT = 3.0;

const BRAND = {
  '--hh-page': '#0F2944',
  '--hh-card': '#173550',
  '--hh-line': '#2C4763',
  '--hh-field': '#0F2944',
  '--hh-control-line': '#6B86A3',
  '--hh-text': '#FFFFFF',
  '--hh-text-dim': '#A9BCD0',
  '--hh-disabled-fill': '#142C46',
  '--hh-disabled-ink': '#8499AD',
  '--hh-disabled-line': '#28455F',
  '--hh-ground': '#0C2138',
  '--hh-widget': '#142E49',
  '--hh-widget-raised': '#193653', // the board's #1A3856 leaves the hh-pro identity at 4.42:1 on it
  '--hh-scrim': 'rgba(4, 10, 20, 0.72)',
  // carried from the dark set unchanged
  '--hh-accent': '#FFC313',
  '--hh-accent-ink': '#0B1725',
  '--hh-accent-text': '#FFC313',
  '--hh-danger': '#FF8F9F',
  '--hh-info': '#86BDF6',
  '--hh-success': '#58D69B',
  '--hh-danger-fill': '#D73A51',
  '--hh-success-fill': '#58D69B',
  '--hh-state-good': '#5CC48A',
  '--hh-state-watch': '#F0A93A',
  '--hh-state-stop': '#F17E6E', // one step lighter than the board's #F07C6C: that is 4.48:1 on widget-raised
};

const norm = (v) => v.replace(/\s+/g, '').toUpperCase();

test('brand mode: the dark half carries option A\'s token set', () => {
  const dark = palette('dark');
  const wrong = Object.entries(BRAND)
    .filter(([name, want]) => norm(dark[name] ?? '') !== norm(want))
    .map(([name, want]) => `${name}: want ${want}, have ${dark[name]}`);
  assert.deepEqual(wrong, [], `the dark half is not brand mode A:\n  ${wrong.join('\n  ')}`);
});

test('brand mode: the page is the badge navy and no value of the old rich blue survives', () => {
  const dark = palette('dark');
  const old = ['#071827', '#0B2338', '#253441', '#0D1420', '#152033', '#1A2740', '#1A3856', '#AEBDCA', '#5A7086'];
  const left = Object.entries(dark).filter(([, v]) => old.includes(v.toUpperCase()));
  assert.deepEqual(left, [], 'a pre-brand-mode dark value is still declared');
});

const TEXT_PAIRS = [
  ['--hh-text', '--hh-page', 14.78],
  ['--hh-text', '--hh-card', 12.64],
  ['--hh-text-dim', '--hh-page', 7.59],
  ['--hh-text-dim', '--hh-card', 6.49],
  ['--hh-accent-text', '--hh-page', 9.2],
  ['--hh-accent-text', '--hh-card', 7.86],
  ['--hh-accent-ink', '--hh-accent', 11.23],
  ['--hh-accent-ink', '--hh-accent-hover', 12.74],
  ['--hh-danger', '--hh-page', 6.81],
  ['--hh-info', '--hh-page', 7.48],
  ['--hh-success', '--hh-page', 8.1],
  ['--hh-danger-fill-ink', '--hh-danger-fill', 4.55],
  ['--hh-accent-ink', '--hh-success-fill', 9.89],
  ['--hh-state-good', '--hh-page', 6.84],
  ['--hh-state-watch', '--hh-page', 7.35],
  ['--hh-state-stop', '--hh-page', 5.59],
  ['--hh-disabled-ink', '--hh-disabled-fill', 4.83],
];

test('brand mode: every text pair on the board clears AA and measures what the board says', () => {
  const v = palette('dark');
  for (const [ink, ground, board] of TEXT_PAIRS) {
    const ratio = contrast(resolveColor(v[ink], v), resolveColor(v[ground], v));
    assert.ok(ratio >= AA, `${ink} on ${ground}: ${ratio.toFixed(2)}:1 is below ${AA}:1`);
    assert.ok(Math.abs(ratio - board) < 0.02, `${ink} on ${ground}: ${ratio.toFixed(2)}:1, the board records ${board}`);
  }
});

test('brand mode: every text ink clears AA on the ground and the widget surfaces too', () => {
  const v = palette('dark');
  const inks = ['--hh-text', '--hh-text-dim', '--hh-accent-text', '--hh-danger', '--hh-info', '--hh-success',
    '--hh-state-good', '--hh-state-watch', '--hh-state-stop'];
  const surfaces = ['--hh-page', '--hh-card', '--hh-ground', '--hh-widget', '--hh-widget-raised'];
  const failures = [];
  for (const ink of inks) for (const s of surfaces) {
    const ratio = contrast(resolveColor(v[ink], v), resolveColor(v[s], v));
    if (ratio < AA) failures.push(`${ink} on ${s}: ${ratio.toFixed(2)}:1`);
  }
  assert.deepEqual(failures, []);
});

test('brand mode: the hh-pro identity blue clears AA on every dark surface it can land on', () => {
  const v = palette('hh-pro dark');
  const failures = [];
  for (const s of ['--hh-page', '--hh-card', '--hh-ground', '--hh-widget', '--hh-widget-raised']) {
    const ratio = contrast(resolveColor(v['--brand'], v), resolveColor(v[s], v));
    if (ratio < AA) failures.push(`--brand on ${s}: ${ratio.toFixed(2)}:1`);
  }
  assert.deepEqual(failures, []);
});

const BOUNDARY_PAIRS = [
  ['--hh-control-line', '--hh-page', 3.92],
  ['--hh-control-line', '--hh-card', 3.35],
  ['--hh-danger-fill', '--hh-page', 3.25],
  ['--hh-success-fill', '--hh-page', 8.1],
];

test('brand mode: every boundary on the board clears 3:1 and measures what the board says', () => {
  const v = palette('dark');
  for (const [a, b, board] of BOUNDARY_PAIRS) {
    const ratio = contrast(resolveColor(v[a], v), resolveColor(v[b], v));
    assert.ok(ratio >= NON_TEXT, `${a} on ${b}: ${ratio.toFixed(2)}:1 is below ${NON_TEXT}:1`);
    assert.ok(Math.abs(ratio - board) < 0.02, `${a} on ${b}: ${ratio.toFixed(2)}:1, the board records ${board}`);
  }
});

test('brand mode: the control boundary also clears 3:1 on the widget layer it can sit on', () => {
  const v = palette('dark');
  for (const s of ['--hh-ground', '--hh-widget', '--hh-widget-raised']) {
    const ratio = contrast(resolveColor(v['--hh-control-line'], v), resolveColor(v[s], v));
    assert.ok(ratio >= NON_TEXT, `--hh-control-line on ${s}: ${ratio.toFixed(2)}:1 is below ${NON_TEXT}:1`);
  }
});

test('brand mode: the danger button edge is white in brand mode and transparent in light', () => {
  assert.equal(palette('dark')['--hh-danger-fill-edge'], '#FFFFFF');
  assert.equal(palette('light')['--hh-danger-fill-edge'], 'transparent');
});

test('brand mode: light stays the sheet\'s own light, untouched by the dark re-value', () => {
  const light = palette('light');
  assert.equal(light['--hh-page'], '#E7ECF1');
  assert.equal(light['--hh-card'], '#FFFFFF');
  assert.equal(light['--hh-text'], '#0B1725');
});
