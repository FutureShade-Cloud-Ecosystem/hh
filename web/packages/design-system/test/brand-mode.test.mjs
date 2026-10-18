// BRAND MODE, the dark half's values (decision log row 1763, palette B charcoal, reversing
// row 1119 option A navy). Charcoal is the dark ground the board build kit (tillkit.py) pairs
// with earthy accents (moss, ochre, clay). The token NAMES are the dark set's, so only the
// values move. This file pins the charcoal set as the design system's "brand mode" board
// records it, and the contrast of every new pair against the sheet's own bars: 4.5:1 for text,
// 3:1 for a boundary judged as a shape. The generic matrices in contrast.test.mjs run over
// the same palette and catch a pair that slips; this file names the pairs the board measured,
// so a re-value fails naming the pair.

import { test } from 'node:test';
import assert from 'node:assert/strict';

import { contrast, palette, resolveColor } from './_lib/sheet.mjs';

const AA = 4.5;
const NON_TEXT = 3.0;

const BRAND = {
  // Carried from the charcoal kit verbatim: the named kit set
  '--hh-page': '#16191D',
  '--hh-card': '#1F2328',
  '--hh-line': '#3A4046',
  '--hh-field': '#121517',
  '--hh-control-line': '#666D76',
  '--hh-text': '#F2F1EC',
  '--hh-text-dim': '#A9A59C',
  '--hh-disabled-fill': '#2A2F33',
  '--hh-disabled-ink': '#9F9A91', // kit #8A857C clears 3.69:1 only; lifted one minimal step to 4.83:1, matching navy
  '--hh-disabled-line': '#3A4046', // hairline; same as --hh-line
  // Proposed for unnamed widgets (one step in the charcoal family)
  '--hh-ground': '#0F1216',
  '--hh-widget': '#191C20',
  '--hh-widget-raised': '#1F2328', // held AT the card so the boundary ink clears 3:1
  '--hh-scrim': 'rgba(0, 0, 0, 0.78)',
  // The gold family held invariant between halves; accent-ink stays #0B1725 (the
  // historical near-black, NOT the kit ink #16191D which equals the charcoal page)
  '--hh-accent': '#FFC313',
  '--hh-accent-ink': '#0B1725',
  '--hh-accent-text': '#FFC313',
  // Quote: indigo stays the family's role; on charcoal we add a brighter text-ink indigo that clears 4.5:1
  '--hh-quote': '#86A3C9',         // the Quote Desk boards' dusty quote blue (q4, q6 charcoal)
  '--hh-quote-fill': '#1D4ED8',    // theme-invariant (carried rule)
  '--hh-quote-fill-on': '#FFFFFF',
  '--hh-quote-link': '#9DB4CC',    // the boards' link blue (the kit's info)
  '--hh-quote-tint': '#1D242C',    // mix(#86A3C9, black 78%) = #1D242C
  // Status inks: lifted from kit's earthy palette to clear AA on charcoal
  '--hh-danger': '#D9826E',
  '--hh-info': '#9DB4CC',
  '--hh-success': '#8FA67B',
  '--hh-danger-fill': '#C9364C',
  '--hh-success-fill': '#7A9D62',
  // State colours: kit values verbatim
  '--hh-state-good': '#8FA67B',
  '--hh-state-watch': '#D2A858',
  '--hh-state-stop': '#D9826E',
};

const norm = (v) => v.replace(/\s+/g, '').toUpperCase();

test('brand mode: the dark half carries palette B charcoal\'s token set', () => {
  const dark = palette('dark');
  const wrong = Object.entries(BRAND)
    .filter(([name, want]) => norm(dark[name] ?? '') !== norm(want))
    .map(([name, want]) => `${name}: want ${want}, have ${dark[name]}`);
  assert.deepEqual(wrong, [], `the dark half is not brand mode B charcoal:\n  ${wrong.join('\n  ')}`);
});

test('brand mode: the page is the kit charcoal #16191D and no value of the old navy survives', () => {
  const dark = palette('dark');
  // The pre-charcoal navy set, except --hh-accent-ink #0B1725 (the invariant near-black the design system uses on the gold fill in both halves, NOT the page color).
  const old = ['#0F2944', '#173550', '#2C4763', '#0C2138', '#142E49', '#193653', '#1A3856',
               '#142C46', '#28455F', '#8499AD', '#6B86A3', '#A9BCD0', '#FF8F9F',
               '#A3BCFF', '#242938', '#D73A51', '#58D69B', '#5CC48A', '#F0A93A', '#F17E6E'];
  const left = Object.entries(dark).filter(([, v]) => old.includes(v.toUpperCase()));
  assert.deepEqual(left, [], 'a pre-charcoal dark value is still declared');
});

const TEXT_PAIRS = [
  // bone text
  ['--hh-text', '--hh-page', 15.59],
  ['--hh-text', '--hh-card', 13.97],
  ['--hh-text', '--hh-field', 16.21],
  // warm dim grey
  ['--hh-text-dim', '--hh-page', 7.18],
  ['--hh-text-dim', '--hh-card', 6.43],
  ['--hh-text-dim', '--hh-field', 7.46],
  // gold-as-ink
  ['--hh-accent-text', '--hh-page', 10.97],
  ['--hh-accent-text', '--hh-card', 9.83],
  // gold family: --hh-accent-ink is invariant near-black #0B1725
  ['--hh-accent-ink', '--hh-accent', 11.23],
  ['--hh-accent-ink', '--hh-accent-hover', 12.74],
  // quote indigo (proposed, kit value failed AA)
  ['--hh-quote', '--hh-page', 6.80],
  ['--hh-quote', '--hh-card', 6.09],
  ['--hh-quote-link', '--hh-page', 8.26],
  ['--hh-quote-fill-on', '--hh-quote-fill', 6.70],
  ['--hh-quote', '--hh-quote-tint', 6.04],
  // status text
  ['--hh-danger', '--hh-page', 6.19],
  ['--hh-info', '--hh-page', 8.26],
  ['--hh-success', '--hh-page', 6.64],
  // fills under their inks
  ['--hh-danger-fill-ink', '--hh-danger-fill', 5.10],
  ['--hh-accent-ink', '--hh-success-fill', 5.87],
  // state marks on page (kit values)
  ['--hh-state-good', '--hh-page', 6.64],
  ['--hh-state-watch', '--hh-page', 7.96],
  ['--hh-state-stop', '--hh-page', 6.19],
  // disabled: lifted to 4.83 to match navy
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
    '--hh-state-good', '--hh-state-watch', '--hh-state-stop', '--hh-quote'];
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
  ['--hh-control-line', '--hh-page', 3.37],
  ['--hh-control-line', '--hh-card', 3.02],
  ['--hh-danger-fill', '--hh-page', 3.46],
  ['--hh-success-fill', '--hh-page', 5.73],
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
