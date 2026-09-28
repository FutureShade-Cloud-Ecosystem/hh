// THE MEASURED-CONTRAST GATE, ported from both source repositories:
// hardscapeos_dibbits app/scripts/contrast-check.mjs (the palette-wide report)
// and app/src/_theme-tokens.test.mjs section 3b (the binding per-declared-rule
// assertions), plus HH Pro's scripts/contrast-probe.mjs (the bar lives in the
// row, never inferred from the group heading).
//
//   /snap/bin/node --import ./src/_ts-js-loader.mjs --test 'test/*.test.mjs'
//
// The bars are the ones the ERP sheet's header documents for each class of
// pair: 4.5:1 (WCAG AA) for anything read as text, 3.0:1 (WCAG 1.4.11) for a
// boundary judged as a shape. This gate does NOT resolve a cascade and measures
// no rendered pixel: it parses the token blocks (comments stripped first; the
// ERP script's header records the NaN:1 lesson), resolves every ink-on-surface
// pair the kits actually paint, and does the WCAG 2.1 arithmetic. The measured
// pairs cover the base vocabulary AND each brand override AND each kit's state
// combinations. A pair under its bar fails the suite NAMING THE PAIR AND ITS
// RATIO; a helper that cannot do the arithmetic fails its own pin first.
//
// Like the ERP's file, this one asserts a FAILURE on purpose for the gold rule:
// white on the brand gold must stay illegible, or someone has reversed a
// ratified rule and re-opened it, not edited a stylesheet.

import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  PALETTE_NAMES,
  contrast,
  declaredPairs,
  palette,
  palettes,
  resolveColor,
} from './_lib/sheet.mjs';
import { hexToOklch, oklchToHex, parseOklch, relativeLuminance } from './_lib/color.mjs';

const AA = 4.5;
const NON_TEXT = 3.0;

const fail = (name, fgHex, bgHex, ratio, min) =>
  `${name}: ${ratio.toFixed(2)}:1 (${fgHex} on ${bgHex}), below the ${min}:1 bar`;

// ── the helpers are pinned before anything trusts them ────────────────────────

test('the contrast helper agrees with the WCAG reference values', () => {
  assert.ok(Math.abs(contrast('#000000', '#ffffff') - 21) < 0.01, 'black on white must be 21:1');
  assert.ok(Math.abs(contrast('#ffffff', '#ffffff') - 1) < 0.01, 'white on white must be 1:1');
  assert.ok(Math.abs(relativeLuminance('#000000')) < 1e-6);
});

test('the oklch converter reproduces the published reference points', () => {
  const red = hexToOklch('#ff0000');
  assert.ok(Math.abs(red[0] - 0.627955) < 1e-3, `red L ${red[0]}`);
  assert.ok(Math.abs(red[1] - 0.257683) < 1e-3, `red C ${red[1]}`);
  assert.ok(Math.abs(red[2] - 29.2338) < 1e-2, `red H ${red[2]}`);
  const blue = hexToOklch('#0000ff');
  assert.ok(Math.abs(blue[0] - 0.452013) < 1e-3, `blue L ${blue[0]}`);
  assert.ok(Math.abs(blue[2] - 264.052) < 1e-2, `blue H ${blue[2]}`);
  assert.equal(oklchToHex(hexToOklch('#FFC313')), '#ffc313', 'gold round-trips');
});

test('palettes() answers each palette from its own blocks and refuses an unknown one', () => {
  const all = palettes();
  // The hh-pro override must actually change the identity, in both halves, or
  // the four-palette matrix below is one palette measured four times.
  assert.notEqual(all['dark']['--brand'], all['hh-pro dark']['--brand'], 'hh-pro must re-value the identity in dark');
  assert.notEqual(all['light']['--brand'], all['hh-pro light']['--brand'], 'hh-pro must re-value the identity in light');
  assert.notEqual(all['dark']['--hh-page'], all['light']['--hh-page'], 'the two halves are different pages');
  assert.throws(() => palette('calm'), /unknown palette/);
});

// ── the palette-wide matrix: every ink the kits paint on every surface it can land on ──

const INKS = [
  '--hh-text',
  '--hh-text-dim',
  '--hh-danger',
  '--hh-info',
  '--hh-success',
  '--hh-accent-text',
  '--hh-state-good',
  '--hh-state-watch',
  '--hh-state-stop',
  '--brand',
];

const SURFACES = ['--hh-page', '--hh-card', '--hh-ground', '--hh-widget', '--hh-widget-raised'];

test(`every state, text and identity ink clears AA on every surface it can land on: every palette`, () => {
  for (const name of PALETTE_NAMES) {
      const v = palette(name);
      const failures = [];
      for (const ink of INKS) {
        const fg = resolveColor(v[ink], v);
        assert.ok(fg, `${ink} must resolve to a flat colour in ${name} (got ${v[ink]})`);
        for (const surface of SURFACES) {
          const bg = resolveColor(v[surface], v);
          const ratio = contrast(fg, bg);
        if (ratio < AA) failures.push(fail(`${ink} on ${surface}`, fg, bg, ratio, AA));
        }
      }
      assert.deepEqual(
        failures.sort(),
        [],
        `inks below AA ${AA}:1 in the ${name} palette. Darken the ink or, if the role is a FILL and not a ` +
          `text ink, split the role the way D-077 did for red and DIB-468 for green:\n  ${failures.join('\n  ')}`,
      );
  }
});

test('every ink resolves in every palette (guards the matrix above)', () => {
  for (const name of PALETTE_NAMES) {
    const v = palette(name);
    for (const ink of INKS) {
      assert.ok(v[ink], `${ink} missing from the ${name} palette`);
      assert.ok(resolveColor(v[ink], v), `${ink} does not resolve in ${name}: ${v[ink]}`);
    }
    for (const surface of SURFACES) {
      assert.ok(v[surface], `${surface} missing from the ${name} palette`);
    }
  }
});

// ── solid fills: judged as shapes at the non-text bar, against where they sit ──

const FILL_PAIRS = [
  { fill: '--hh-danger-fill', surfaces: ['--hh-page', '--hh-card', '--hh-ground'] },
  { fill: '--hh-success-fill', surfaces: ['--hh-page', '--hh-card'] },
];

test(`solid fills stay visible against the surfaces they sit on (non-text 3:1): every palette`, () => {
  for (const name of PALETTE_NAMES) {
      const v = palette(name);
      const failures = [];
      for (const { fill, surfaces } of FILL_PAIRS) {
        const fg = resolveColor(v[fill], v);
        for (const surface of surfaces) {
          const bg = resolveColor(v[surface], v);
          const ratio = contrast(fg, bg);
          if (ratio < NON_TEXT) failures.push(fail(`${fill} vs ${surface}`, fg, bg, ratio, NON_TEXT));
        }
      }
      assert.deepEqual(failures.sort(), [], `fills under the ${NON_TEXT}:1 non-text bar in ${name}:\n  ${failures.join('\n  ')}`);
  }
});

// ── the gold rule, in both directions, in every palette ─────────────────────────

test(`D-078 gold rule: the gold fill carries only its near-black ink, and white on gold stays forbidden: every palette`, () => {
  for (const name of PALETTE_NAMES) {
      const v = palette(name);
      const gold = resolveColor(v['--hh-accent'], v);
      const hover = resolveColor(v['--hh-accent-hover'], v);
      const ink = resolveColor(v['--hh-accent-ink'], v);
  
      const onGold = contrast(ink, gold);
      assert.ok(onGold >= AA, fail('--hh-accent-ink on --hh-accent', ink, gold, onGold, AA));
      const onHover = contrast(ink, hover);
      assert.ok(onHover >= AA, fail('--hh-accent-ink on --hh-accent-hover', ink, hover, onHover, AA));
  
      // The forbidden direction, ON PURPOSE. If white ever clears AA on the gold,
      // the brand mark has changed and the rule that forbids it must be re-opened
      // where it was ratified, not quietly edited here.
      const whiteOnGold = contrast('#ffffff', gold);
      assert.ok(
        whiteOnGold < AA,
        `white ink on the brand gold now measures ${whiteOnGold.toFixed(2)}:1 in ${name}. THE GOLD RULE: ` +
          `gold is a surface and carries near-black, never white. Re-open the ruling, do not soften this test.`,
      );
  
      // The action fill pins the invariant ink WHENEVER the action fill IS the
      // gold (the base vocabulary, both halves: the fill does not flip, so its
      // ink must not either). A shell whose dealer layer re-valued the action to
      // its own colour (hh-pro) is judged by the AA pair test instead, because a
      // fill that is not gold is not who the gold rule speaks to.
      const actionFill = resolveColor(v['--brand-fill'], v);
      const fillOn = resolveColor(v['--brand-fill-on'], v);
      if (actionFill === gold) {
        assert.equal(
          fillOn,
          ink,
          `--brand-fill-on must be the invariant accent ink in ${name}; got ${fillOn}. The gold FILL does not flip, so its ink must not either.`,
        );
      } else {
        const ratio = contrast(fillOn, actionFill);
        assert.ok(ratio >= AA, fail('--brand-fill-on on --brand-fill', fillOn, actionFill, ratio, AA));
      }
  }
});

// ── brand pairs: the identity as text, the action fill under its ink ────────────

test(`brand identity and action pairs clear their bars: every palette`, () => {
  for (const name of PALETTE_NAMES) {
      const v = palette(name);
      const failures = [];
      // The pairs the kit layer actually paints. --brand-on is NOT measured
      // against --brand-fill in the base vocabulary, because there the identity
      // (gold-as-ink, per theme) and the action fill (the invariant gold) are two
      // roles by the gold rule, and no component paints one on the other; the
      // hh-pro override collapses them onto one colour and the action row covers
      // the pair there.
      const rows = [
        ['--brand-fill-on on --brand-fill (the action fill)', '--brand-fill-on', '--brand-fill', AA],
        ['--cbrand-on on --cbrand (the contractor identity)', '--cbrand-on', '--cbrand', AA],
        ['--brand-chrome-on on --brand-chrome (the frame)', '--brand-chrome-on', '--brand-chrome', AA],
        ['--brand-chrome-muted on --brand-chrome-2', '--brand-chrome-muted', '--brand-chrome-2', AA],
      ];
      for (const [label, fgTok, bgTok, min] of rows) {
        const fg = resolveColor(v[fgTok], v);
        const bg = resolveColor(v[bgTok], v);
        assert.ok(fg && bg, `${label}: ${fgTok}/${bgTok} must resolve in ${name}`);
        const ratio = contrast(fg, bg);
        if (ratio < min) failures.push(fail(label, fg, bg, ratio, min));
      }
      assert.deepEqual(failures.sort(), [], `brand pairs below their bar in ${name}:\n  ${failures.join('\n  ')}`);
  }
});

test(`the primary action's boundary is tellable from the page in every palette`, () => {
  // The composite the design actually makes: a filled primary button reads its
  // edge from the FILL where the fill contrasts with the page (dark), and from
  // its INK border where the fill cannot (the gold on a light page is 1.50:1,
  // which is why the button carries the accent-ink border). The pair passes
  // when the control's own marks make its boundary tellable, and fails when
  // neither does.
  const failures = [];
  for (const name of PALETTE_NAMES) {
    const v = palette(name);
    for (const surface of ['--hh-page', '--hh-card']) {
      const fill = resolveColor(v['--brand-fill'], v);
      const border = resolveColor(v['--brand-fill-on'], v);
      const bg = resolveColor(v[surface], v);
      const best = Math.max(contrast(fill, bg), contrast(border, bg));
      if (best < NON_TEXT) failures.push(fail(`primary action boundary vs ${surface}`, `${fill}/${border}`, bg, best, NON_TEXT));
    }
  }
  assert.deepEqual(failures.sort(), [], `primary action boundaries under the ${NON_TEXT}:1 bar:\n  ${failures.join('\n  ')}`);
});

// ── per-kit, per-state pairs: the state combinations the kits actually paint ────

// Every kit paints the same state system (src/state.ts), so the state pairs are
// designed once and named once here; the kit loop holds every kit to them. The
// indicator sits on the state block (a card surface); the banner and review
// card sit on cards; the spinner arc and the disabled pair are their own rows.
const KIT_TAGS = ['hh-scan-and-confirm', 'hh-list-detail', 'hh-guided-flow', 'hh-dashboard', 'hh-inbox'];

// state -> the ink its indicator, banner or review card is read in.
const STATE_INK = {
  empty: '--hh-state-watch',
  loading: '--hh-info',
  offline: '--hh-info',
  conflict: '--hh-state-watch',
  error: '--hh-danger',
};

for (const name of PALETTE_NAMES) {
  for (const tag of KIT_TAGS) {
    for (const state of Object.keys(STATE_INK)) {
      test(`kit pair clears AA: ${tag} / ${state} / ${name}`, () => {
        const v = palette(name);
        const failures = [];

        // The state's indicator ink on the card it is painted on.
        const inkTok = STATE_INK[state];
        const fg = resolveColor(v[inkTok], v);
        const card = resolveColor(v['--hh-card'], v);
        const inkRatio = contrast(fg, card);
        if (inkRatio < AA) failures.push(fail(`${state} indicator (${inkTok}) on --hh-card`, fg, card, inkRatio, AA));

        if (state === 'loading') {
          // The spinner arc, a boundary: gold-as-ink on the card, non-text bar.
          const arc = resolveColor(v['--hh-accent-text'], v);
          const ratio = contrast(arc, card);
          if (ratio < NON_TEXT) failures.push(fail('spinner arc (--hh-accent-text) on --hh-card', arc, card, ratio, NON_TEXT));
        }

        if (state === 'offline') {
          // Held actions are the disabled pair, which clears AA in both halves.
          const dInk = resolveColor(v['--hh-disabled-ink'], v);
          const dFill = resolveColor(v['--hh-disabled-fill'], v);
          const ratio = contrast(dInk, dFill);
          if (ratio < AA) failures.push(fail('--hh-disabled-ink on --hh-disabled-fill', dInk, dFill, ratio, AA));
        }

        assert.deepEqual(
          failures.sort(),
          [],
          `${tag} in its ${state} state paints a pair under its bar in the ${name} palette:\n  ${failures.join('\n  ')}`,
        );
      });
    }
  }
}

// ── the declared-pair gate: every rule block that states a pair, in every palette ──

test('the declared-pair scanner actually finds pairs (guards the gate below)', () => {
  const pairs = declaredPairs();
  assert.ok(pairs.length >= 10, `expected the kits to declare colour/background pairs; found ${pairs.length}`);
  const all = palettes();
  const resolvable = pairs.filter((p) => resolveColor(p.color, all.dark) && resolveColor(p.background, all.dark));
  assert.ok(resolvable.length >= 6, `expected many RESOLVABLE declared pairs; only ${resolvable.length} resolved`);
  const texts = pairs.map((p) => `${p.color} ${p.background}`).join(' ');
  assert.match(texts, /--hh-disabled-ink/, 'the scanner must see the disabled pair (its rule is load-bearing)');
  assert.match(texts, /--brand-fill/, 'the scanner must see the brand action pair');
});

test(`every declared colour-on-background pair clears AA in shipped sources: every palette`, () => {
  for (const name of PALETTE_NAMES) {
      const vars = palette(name);
      const failures = [];
      for (const p of declaredPairs()) {
        const fg = resolveColor(p.color, vars);
        const bg = resolveColor(p.background, vars);
        if (!fg || !bg) continue; // tints and translucent layers: Chromium's problem, not a text fact
        const ratio = contrast(fg, bg);
        if (ratio < AA) {
          failures.push(
            `${p.file}:${p.line}: ${p.color} on ${p.background} is ${ratio.toFixed(2)}:1 (${fg} on ${bg}) in ${name}, below AA ${AA}:1`,
          );
        }
      }
      assert.deepEqual(
        failures.sort(),
        [],
        `declared text/background pairs below AA ${AA}:1 in the ${name} palette. Text on a gold fill must be ` +
          `the invariant near-black; text on a flipping surface must flip with it:\n  ${failures.sort().join('\n  ')}`,
      );
  }
});

// ── the hh-pro override says what HH Pro's sheet says ────────────────────────────

test('the hh-pro override carries HH Pro oklch values verbatim, and its pairs clear AA here', () => {
  const all = palettes();
  const expected = {
    'hh-pro dark': {
      '--brand': 'oklch(70% 0.17 255)',
      '--brand-hover': 'oklch(76% 0.17 255)',
      '--brand-on': 'oklch(17% 0.012 265)',
      '--brand-fill': 'var(--brand)',
      '--brand-fill-hover': 'var(--brand-hover)',
      '--brand-fill-on': 'var(--brand-on)',
    },
    'hh-pro light': {
      '--brand': 'oklch(52% 0.19 255)',
      '--brand-hover': 'oklch(46% 0.19 255)',
      '--brand-on': 'oklch(100% 0 0)',
      '--brand-fill': 'var(--brand)',
      '--brand-fill-hover': 'var(--brand-hover)',
      '--brand-fill-on': 'var(--brand-on)',
    },
  };
  for (const [name, toks] of Object.entries(expected)) {
    for (const [tok, value] of Object.entries(toks)) {
      assert.equal(all[name][tok], value, `${tok} in the ${name} override must be HH Pro's value or default, verbatim`);
    }
  }
  // And the carried values clear the bars on THIS package's canvases, measured:
  for (const name of Object.keys(expected)) {
    const v = palette(name);
    const identity = resolveColor(v['--brand'], v);
    for (const surface of ['--hh-page', '--hh-card']) {
      const bg = resolveColor(v[surface], v);
      const ratio = contrast(identity, bg);
      assert.ok(ratio >= AA, `hh-pro identity on ${surface} in ${name} is ${ratio.toFixed(2)}:1, below AA`);
    }
    const fill = resolveColor(v['--brand-fill'], v);
    const on = resolveColor(v['--brand-on'], v);
    const onFill = contrast(on, fill);
    assert.ok(onFill >= AA, `hh-pro on-ink on its fill in ${name} is ${onFill.toFixed(2)}:1, below AA`);
  }
});
