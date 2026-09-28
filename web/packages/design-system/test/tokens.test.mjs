// The sheet's own structure, held where the tokens are defined: both halves of
// every theme-capable pair, the invariants held across both halves, the brand
// layers declared where they belong, and the sheet parseable by the gates at
// all. Ported shapes from the ERP's _theme-tokens.test.mjs (sections 2, 3a-ii,
// 3e and 3f): a per-theme pair must be DECLARED in both halves, an invariant
// fill must be re-valued by no block, and an undocumented exception is
// indistinguishable from an accident.

import { test } from 'node:test';
import assert from 'node:assert/strict';

import { contrast, parseDeclarations, palettes, readPkg, resolveColor, sheetBlocks, stripComments } from './_lib/sheet.mjs';

const AA = 4.5;

const css = () => readPkg('src/tokens.css');

test('every theme-capable role is DECLARED in the light half, not inherited', () => {
  // Both halves of a per-theme pair are a design decision each, and the light
  // one must not silently follow a future edit to the dark one. The names below
  // are every role the kits paint per theme.
  const blocks = sheetBlocks();
  const dark = parseDeclarations(blocks.dark);
  const light = parseDeclarations(blocks.light);
  const perTheme = [
    '--hh-page', '--hh-card', '--hh-line', '--hh-text', '--hh-text-dim',
    '--hh-row', '--hh-row-alt',
    '--hh-accent-text', '--hh-focus-ring',
    '--hh-danger', '--hh-danger-fill', '--hh-danger-fill-ink',
    '--hh-info', '--hh-success', '--hh-success-fill',
    '--hh-state-good', '--hh-state-watch', '--hh-state-stop',
    '--hh-disabled-fill', '--hh-disabled-ink', '--hh-disabled-line',
    '--hh-ground', '--hh-widget', '--hh-widget-raised',
    '--hh-hairline', '--hh-track', '--hh-widget-shadow',
    '--hh-scrim', '--hh-elev-1', '--hh-elev-2', '--hh-elev-3',
    '--hh-color-scheme',
    '--brand-on', '--brand-tint',
  ];
  const missing = perTheme.filter((t) => !(t in dark) || !(t in light));
  assert.deepEqual(missing, [], `these roles must be declared in BOTH halves; missing: ${missing.join(', ')}`);
  // And the halves must actually differ where they claim to be a different page.
  for (const t of ['--hh-page', '--hh-card', '--hh-text', '--hh-accent-text']) {
    assert.notEqual(dark[t], light[t], `${t} must differ between halves; a shared value is not a theme`);
  }
});

test('the gold family and the layout invariants are theme-invariant on purpose', () => {
  // The gold FILL does not flip, so neither does its ink: that is the whole
  // rule. The light block re-valuing any of these would reverse it.
  const blocks = sheetBlocks();
  const light = parseDeclarations(blocks.light);
  const invariants = [
    '--hh-accent', '--hh-accent-hover', '--hh-accent-ink',
    '--brand', '--brand-hover', '--brand-fill', '--brand-fill-hover', '--brand-fill-on',
    '--cbrand', '--cbrand-on',
    '--hh-font-ui', '--hh-font-mono',
    '--hh-s-2', '--hh-s-4', '--hh-s-8', '--hh-s-16', '--hh-s-24',
    '--hh-radius-control', '--hh-radius-card', '--hh-radius-pill',
    '--hh-z-nav', '--hh-z-toast',
    '--hh-measure', '--hh-reading-max', '--hh-form-col', '--hh-pane-columns',
  ];
  const moved = invariants.filter((t) => t in light);
  assert.deepEqual(
    moved,
    [],
    `the light half re-declares theme-INVARIANT roles: ${moved.join(', ')}. The gold family is held ` +
      `across themes by design, and the brand fill's ink with it.`,
  );
});

test('a token name written in PROSE cannot corrupt the parsed table', () => {
  // The guard for the strip. The merge header names tokens next to colons
  // constantly; a parser that reads prose as declarations would hand every gate
  // a corrupted table.
  assert.match(css(), /\/\*/, 'tokens.css has no comments at all, so this guard is measuring nothing');
  const blocks = sheetBlocks();
  for (const [name, body] of Object.entries(blocks)) {
    const parsed = parseDeclarations(body);
    assert.ok(Object.keys(parsed).length > 5, `${name} block parsed to almost nothing; the block regex has drifted`);
    for (const [tok, value] of Object.entries(parsed)) {
      assert.ok(value.length < 160, `${tok} in ${name} parsed to ${value.length} characters, so a comment was swallowed`);
      assert.ok(!value.includes('*/') && !value.includes('/*'), `${tok} in ${name} parsed to a value containing a comment delimiter`);
    }
  }
  // And the count is exact: every declaration-shaped line in the block parses,
  // and nothing else does.
  for (const [name, body] of Object.entries(blocks)) {
    const declared = new Set([...stripComments(body).matchAll(/^\s*(--[a-z0-9-]+)\s*:/gm)].map((m) => m[1]));
    const parsed = [...body.matchAll(/^\s*(--[a-z0-9-]+)\s*:/gm)].map((m) => m[1]);
    const extra = parsed.filter((t) => !declared.has(t));
    assert.deepEqual(extra, [], `in ${name}, a declaration-shaped line inside a comment is being counted as a token: ${extra.join(', ')}`);
  }
});

test('the per-theme solid danger and success fills follow the D-077 / DIB-468 shape', () => {
  const all = palettes();
  for (const [fill, inkTok, inkLiteral] of [
    ['--hh-danger-fill', '--hh-danger-fill-ink', '#ffffff'],
    ['--hh-success-fill', '--hh-accent-ink', '#0b1725'],
  ]) {
    const dark = resolveColor(all['dark'][fill], all.dark);
    const light = resolveColor(all['light'][fill], all.light);
    assert.notEqual(dark, light, `${fill} is per-theme by ruling; one value reverses it`);
    // The ink clears AA on BOTH halves, in all four palettes (the fills are
    // brand-invariant, so the four palettes share their values; measured anyway).
    for (const name of ['dark', 'light', 'hh-pro dark', 'hh-pro light']) {
      const v = all[name];
      const ink = resolveColor(v[inkTok], v);
      const value = resolveColor(v[fill], v);
      const ratio = contrast(ink, value);
      assert.ok(ratio >= AA, `${inkTok} on ${fill} in ${name} is ${ratio.toFixed(2)}:1, below AA ${AA}:1`);
    }
    assert.equal(resolveColor(all['dark'][inkTok], all.dark), inkLiteral, `${inkTok} must be the literal ink the fills pin`);
  }
});

test('the accent ink is identical in every palette, and gold-as-ink is the one accent role that flips', () => {
  const all = palettes();
  const values = ['dark', 'light', 'hh-pro dark', 'hh-pro light'].map((n) => resolveColor(all[n]['--hh-accent-ink'], all[n]));
  assert.equal(new Set(values).size, 1, '--hh-accent-ink must not flip; got ' + values.join(', '));
  const textDark = resolveColor(all['dark']['--hh-accent-text'], all.dark);
  const textLight = resolveColor(all['light']['--hh-accent-text'], all.light);
  assert.notEqual(textDark, textLight, 'gold-as-INK must be per-theme: one value is invisible on one page');
  for (const [name, v] of [['dark', all.dark], ['light', all.light]]) {
    for (const surface of ['--hh-page', '--hh-card']) {
      const ratio = contrast(resolveColor(v['--hh-accent-text'], v), resolveColor(v[surface], v));
      assert.ok(ratio >= AA, `accent text on ${surface} in ${name} is ${ratio.toFixed(2)}:1, below AA`);
    }
  }
});

test('the contractor layer is declared once, defers to the dealer brand, and is not consumed by the kits', () => {
  // The public proposal surface ONLY: the kits are shells-agnostic and must not
  // reach for the contractor's accent. The layer is two tokens whose defaults
  // defer through --brand, so they always resolve.
  const blocks = sheetBlocks();
  const dark = parseDeclarations(blocks.dark);
  assert.equal(dark['--cbrand'], 'var(--brand)', '--cbrand must defer to --brand on :root');
  assert.equal(dark['--cbrand-on'], 'var(--brand-on)', '--cbrand-on must defer to --brand-on on :root');
  const light = parseDeclarations(blocks.light);
  assert.ok(!('--cbrand' in light), 'the contractor layer needs no light half: it defers, it does not re-value');
  for (const file of ['src/state.ts', 'src/primitives.ts', 'src/lit.ts',
    'src/kits/scan-and-confirm.ts', 'src/kits/list-detail.ts', 'src/kits/guided-flow.ts',
    'src/kits/dashboard.ts', 'src/kits/inbox.ts']) {
    const src = readPkg(file);
    assert.ok(!src.includes('--cbrand'), `${file} consumes the contractor layer; only the public proposal surface may`);
  }
});
