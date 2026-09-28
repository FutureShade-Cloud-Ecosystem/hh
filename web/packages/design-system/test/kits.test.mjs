// The kits' conduct gates: the two carried rules and the coverage rule, as
// structural assertions over what the kits actually render.
//
//   COLOUR NEVER ENCODES STATE ALONE: every designed state's indicator pairs
//   its colour with an icon AND a word AND a pattern change (data-pattern), so
//   the state survives with the colour removed. Asserted structurally: the
//   rendered markup must carry all three, and no two states of a kit may share
//   an indicator shape.
//
//   DISABLED CHANGES COLOUR, NOT OPACITY: held controls are the
//   --hh-disabled-* token swap, stated once in primitives.ts. No shipped source
//   may declare an opacity or a filter, and the held-action states must render
//   real disabled buttons.
//
//   COVERAGE: every kit renders every designed state (the five from the source
//   brief) and the ready state beside them.

import { test } from 'node:test';
import assert from 'node:assert/strict';

import { renderTemplate } from '../src/lit.js';
import { KIT_STATES, STATE_META, kitShell, stateIndicator } from '../src/state.js';
import { baseStyles } from '../src/primitives.js';
import { STATE_ICONS, icon } from '../src/state-icons.js';
import { parseDeclarations, readPkg, shippedStyleFiles, sheetBlocks, stripComments } from './_lib/sheet.mjs';

import { ScanConfirmKit } from '../src/kits/scan-and-confirm.js';
import { ListDetailKit } from '../src/kits/list-detail.js';
import { GuidedFlowKit } from '../src/kits/guided-flow.js';
import { DashboardKit } from '../src/kits/dashboard.js';
import { InboxKit } from '../src/kits/inbox.js';

const KITS = [
  ['hh-scan-and-confirm', ScanConfirmKit],
  ['hh-list-detail', ListDetailKit],
  ['hh-guided-flow', GuidedFlowKit],
  ['hh-dashboard', DashboardKit],
  ['hh-inbox', InboxKit],
];

const render = (Kit, state) => Kit.renderToString({ state });

const attr = (markup, name) => {
  const m = markup.match(new RegExp(`${name}="([^"]*)"`));
  return m ? m[1] : null;
};

// ── coverage ────────────────────────────────────────────────────────────────────

test('the state system declares exactly the five designed states from the brief', () => {
  assert.deepEqual(
    [...KIT_STATES].sort(),
    ['conflict', 'empty', 'error', 'loading', 'offline'],
    'the designed states are fixed: empty, loading, offline, conflict, error',
  );
  for (const state of KIT_STATES) {
    const meta = STATE_META[state];
    assert.ok(meta.label.length > 0, `${state} needs a word`);
    assert.ok(meta.pattern.length > 0, `${state} needs a pattern marker`);
    assert.ok(STATE_ICONS[meta.icon].paths.length > 0, `${state} needs an icon`);
  }
});

// One test per matrix, loops inside: bun (CI's drift gate) refuses the second
// and later test() calls made from a top-level for-of (oven-sh/bun#5090).
test('kit coverage: every kit renders its every designed state and its ready state', () => {
  const failures = [];
  for (const [tag, Kit] of KITS) {
    for (const state of KIT_STATES) {
      try {
        const markup = render(Kit, state);
        assert.equal(attr(markup, 'data-kit'), tag, 'the root must name the kit');
        assert.equal(attr(markup, 'data-state'), state, 'the root must name the state');
        assert.equal(attr(markup, 'data-pattern'), STATE_META[state].pattern, 'the root must carry the state pattern');
        assert.ok(markup.includes('state-label') || markup.includes('banner-text'), `${tag}/${state} lost its word`);
        assert.ok(markup.includes('<svg'), `${tag}/${state} lost its icon`);
      } catch (e) {
        failures.push(`${tag}/${state}: ${e.message}`);
      }
    }
    try {
      const markup = render(Kit, 'ready');
      assert.equal(attr(markup, 'data-state'), 'ready');
      assert.equal(attr(markup, 'data-pattern'), 'ready');
      assert.ok(markup.includes('data-live="true"'), 'ready actions must be live');
    } catch (e) {
      failures.push(`${tag}/ready: ${e.message}`);
    }
  }
  assert.deepEqual(failures, [], `kit coverage failures:\n  ${failures.join('\n  ')}`);
});

// ── colour never encodes state alone ─────────────────────────────────────────────

test(`state indicators survive with colour removed: every kit`, () => {
  for (const [tag, Kit] of KITS) {
      const shapes = new Set();
      for (const state of KIT_STATES) {
        const markup = render(Kit, state);
        // The word, with content.
        const label = markup.match(/class="[^"]*(state-label|banner-text|review-title)[^"]*"\s*>([^<]+)</);
        assert.ok(label && label[2].trim().length > 0, `${tag}/${state}: no word beside the colour`);
        // The icon, with geometry (a path, not a glyph and not an image).
        const svg = markup.match(/<svg[^>]*>[\s\S]*?<\/svg>/);
        assert.ok(svg, `${tag}/${state}: no icon beside the colour`);
        assert.match(svg[0], /<path d="/, `${tag}/${state}: the icon is not path geometry`);
        assert.ok(!svg[0].includes('data:image'), `${tag}/${state}: the icon must not be an embedded image`);
        // The pattern change, machine-checkable.
        const pattern = attr(markup, 'data-pattern');
        assert.ok(pattern && pattern !== 'ready', `${tag}/${state}: no pattern change marker`);
        shapes.add(`${pattern}|${label[2]}`);
      }
      // No two states of a kit may present the same shape: five distinct
      // pattern+word pairs, so the states are tellable apart with colour gone.
      assert.equal(
        shapes.size,
        KIT_STATES.length,
        `${tag}: two states present the same indicator shape (${[...shapes].join(' ; ')}), which means they are colour-only apart`,
      );
  }
});

test('the shared indicator helper is the one implementation every kit gets', () => {
  // Designed ONCE: every indicator on the page is this module's output, byte
  // for byte, not a per-kit re-derivation.
  const once = renderTemplate(stateIndicator('offline'));
  assert.match(once, /data-state="offline"/);
  assert.match(once, /data-pattern="cached"/);
  for (const state of KIT_STATES) {
    assert.match(renderTemplate(stateIndicator(state)), new RegExp(`data-pattern="${STATE_META[state].pattern}"`));
  }
  for (const file of ['src/kits/scan-and-confirm.ts', 'src/kits/list-detail.ts', 'src/kits/guided-flow.ts', 'src/kits/dashboard.ts', 'src/kits/inbox.ts']) {
    const src = readPkg(file);
    assert.ok(src.includes('kitShell'), `${file} must build on the shared kitShell scaffold`);
    assert.ok(!src.includes('STATE_META'), `${file} must not re-derive state metadata; the scaffold owns it`);
  }
});

test('offline and conflict are pattern changes, not recolourings: the body stays, the actions change', () => {
  // The offline pattern shows the cached body with held actions; the conflict
  // pattern shows the local body under a review card with the review live. That
  // is a change a reader can see with colour removed.
  const kit = InboxKit;
  const offline = render(kit, 'offline');
  assert.ok(offline.includes('banner-offline'), 'offline must carry the banner pattern');
  assert.ok(offline.includes('msg-main'), 'offline must show the cached body');
  assert.ok(offline.includes('data-live="false"'), 'offline must hold the actions');
  assert.match(offline, /<button[^>]*disabled[^>]*>/, 'offline must render really disabled buttons');

  const conflict = render(kit, 'conflict');
  assert.ok(conflict.includes('review-card'), 'conflict must carry the review pattern');
  assert.ok(conflict.includes('msg-main'), 'conflict must show the local body');
  const buttons = conflict.match(/<button[^>]*>[^<]*</g) ?? [];
  const review = buttons.find((b) => b.includes('data-action="review"'));
  assert.ok(review && !review.includes('disabled'), 'conflict must leave the scaffold review action live');
  assert.ok(buttons.filter((b) => b.includes('disabled')).length >= 2, 'conflict must hold the kit actions');

  const ready = render(kit, 'ready');
  for (const b of ready.match(/<button[^>]*>/g) ?? []) {
    assert.ok(!b.includes('disabled'), 'ready must hold nothing');
  }
});

// ── disabled changes colour, not opacity ────────────────────────────────────────

test('the disabled rule is a token swap, stated once in the primitives', () => {
  const styles = baseStyles;
  const rule = styles.match(/button:disabled[\s\S]*?\}/);
  assert.ok(rule, 'the primitives must carry a button:disabled rule');
  assert.match(rule[0], /var\(--hh-disabled-fill\)/, 'the swap must set the disabled fill token');
  assert.match(rule[0], /var\(--hh-disabled-ink\)/, 'the swap must set the disabled ink token');
  assert.match(rule[0], /var\(--hh-disabled-line\)/, 'the swap must set the disabled line token');
});

test('no shipped source fades a control: no opacity declaration, no filter, anywhere', () => {
  // The counterexample that earned this rule was a raw "Accept and sign"
  // button at reduced opacity on the one screen where someone decides to spend
  // money. The rule is package-wide and the scan is comment-stripped, so a
  // comment that QUOTES the word is not a violation but a declaration is.
  const offenders = [];
  for (const file of shippedStyleFiles()) {
    const src = stripComments(readPkg(file));
    for (const m of src.matchAll(/(?:^|[\s;{])(opacity|filter)\s*:/gm)) {
      offenders.push(`${file}: ${m[1]} declaration`);
    }
  }
  assert.deepEqual(
    offenders.sort(),
    [],
    `a shipped source declares opacity or filter. Disabled controls change COLOUR through the ` +
      `--hh-disabled-* swap; a fade is not a state:\n  ${offenders.join('\n  ')}`,
  );
});

test('the disabled pairs the swap produces clear AA in both halves', () => {
  // WCAG exempts disabled controls; this package does not. The gate in
  // contrast.test.mjs holds the painted pair; this holds the sheet's halves to
  // declaring the family in BOTH, each half its own decision.
  const blocks = sheetBlocks();
  for (const [name, body] of [['dark half', blocks.dark], ['light half', blocks.light]]) {
    const parsed = parseDeclarations(body);
    for (const t of ['--hh-disabled-fill', '--hh-disabled-ink', '--hh-disabled-line']) {
      assert.ok(t in parsed, `${t} must be declared in the ${name}`);
    }
  }
});

// ── the icon geometry gate, held locally until the registry lands ────────────────

test('state-icons.ts is path geometry only: the non-geometry reject list', () => {
  // The registry format this package will adopt (src/icons/README.md) rejects
  // anything that is not path data. The five marks here are held to the same
  // list now: no script, no event attribute, no style, no external reference,
  // no embedded image, no animation element.
  const src = readPkg('src/state-icons.ts');
  for (const banned of [
    /<script/i,
    /\son[a-z]+\s*=/i,
    /<style/i,
    /(href|xlink:href|url\()/i,
    /data:image/i,
    /<(animate|animateTransform|animateMotion|set)\b/i,
  ]) {
    assert.ok(!banned.test(src), `state-icons.ts matched the non-geometry reject list: ${banned}`);
  }
  // Every quoted string inside a paths array is path data starting with a
  // moveto; strings outside the arrays are not judged.
  const arrays = src.match(/paths:\s*\[[^\]]*\]/g) ?? [];
  assert.ok(arrays.length >= 5, 'expected the five state marks to declare paths');
  for (const arr of arrays) {
    for (const d of arr.matchAll(/'([^']+)'/g)) {
      assert.match(d[1], /^[Mm]/, `a geometry string does not start as path data: ${d[1].slice(0, 30)}`);
    }
  }
});

test('every icon renders as currentColor stroke geometry with a text alternative available', () => {
  for (const name of Object.keys(STATE_ICONS)) {
    const markup = renderTemplate(icon(name, `label for ${name}`));
    assert.match(markup, /stroke="currentColor"/, `${name} must draw in currentColor`);
    assert.ok(!/#[0-9a-f]{3,8}/i.test(markup), `${name} must carry no colour of its own`);
    assert.match(markup, /<title>/, `${name} rendered with a label must carry a text alternative`);
    const bare = renderTemplate(icon(name));
    assert.match(bare, /aria-hidden="true"/, `${name} rendered bare must be hidden from assistive tech`);
  }
});
