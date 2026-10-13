// The pinned rail's contract, over the server render, plus the ported floors:
// the original's _rail-state.test.mjs floors (the posture resolves through the
// pure resolver, the default is collapsed, the toggle still writes '1'/'0')
// and the two unguarded floors unit-rail-pins.test.mjs was written for (one
// name per row: a row that shows its label carries no native title and no
// aria-label on top of it; the empty state is a calm sentence, and it is the
// FIRST thing a new person sees). The rail is the person's own pinned
// surfaces across every micro app they hold, collapsed by default, never a
// navigation tree (row 1520).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

import { renderKit } from './lib/render.mjs';
import { ShellRail } from '../src/shell-rail.ts';
import { parseRules, declsFor, px } from './lib/css.mjs';
import { memoryPinStorage, RAIL_COLLAPSED_KEY } from '../src/shell-state.ts';

const railSrc = readFileSync(fileURLToPath(new URL('../src/shell-rail.ts', import.meta.url)), 'utf8');

const visible = (markup) => markup.replace(/<!--.*?-->/g, '');

const PINS = [
  { key: 'yard-pick/pick-queue', label: 'Pick queue', glyph: 'P' },
  { key: 'drive-run/my-run', label: 'My run', glyph: 'D' },
  { key: 'house-messages/inbox', label: 'House Messages', glyph: 'H' },
];

// ── the collapsed default and the stored posture ─────────────────────────────────────────────────

test('the rail renders collapsed by default: icons, and the labels ride as tooltips', () => {
  const markup = renderKit(ShellRail, { pins: PINS });
  assert.ok(markup.includes('data-collapsed="true"'), 'the collapsed posture is in the markup');
  const rows = markup.match(/<button[^>]*data-action="rail-select"[^>]*>[\s\S]*?<\/button>/g) ?? [];
  assert.equal(rows.length, 3, 'the pins only: one row per pin');
  assert.ok(rows[0].includes('aria-label="Pick queue"'), 'a collapsed row is named by its label');
  assert.ok(markup.includes('Keep your pins open'), 'the expand control says what it does');
});

test('expanded, the labels are the words on screen: no aria-label and no title on a row', () => {
  const markup = renderKit(ShellRail, { pins: PINS, collapsed: false });
  assert.ok(markup.includes('data-collapsed="false"'), 'the expanded posture is in the markup');
  const rows = markup.match(/<button[^>]*data-action="rail-select"[^>]*>[\s\S]*?<\/button>/g) ?? [];
  for (const row of rows) {
    assert.ok(!/\btitle=/.test(row), 'no native title beside a visible label');
    assert.ok(!/\baria-label=/.test(row), 'and no aria-label: the visible words are the name');
    assert.ok(/class="label"/.test(row), 'the label text is rendered');
  }
  assert.ok(markup.includes('Let your pins hide again'), 'the collapse control says what it does');
});

test('the rail resolves its posture through the pure resolver when the shell wires storage', () => {
  const storage = memoryPinStorage();
  const el = new ShellRail();
  el.storage = { read: (key) => (key === RAIL_COLLAPSED_KEY ? '0' : null), write: storage.write };
  el.resolvePosture();
  assert.equal(el.collapsed, false, 'the stored explicit expand wins');
  const second = new ShellRail();
  second.storage = { read: () => null, write: () => {} };
  second.resolvePosture();
  assert.equal(second.collapsed, true, 'silence means collapsed');
});

test('an explicit collapsed attribute stands over storage', () => {
  const el = new ShellRail();
  el.storage = { read: () => '0', write: () => {} };
  el.setAttribute('collapsed', '');
  el.resolvePosture();
  assert.equal(el.collapsed, true, 'what the shell asked for, the shell gets');
});

// ── one name per row, glyphs out of the name, the keyboard ───────────────────────────────────────

test('the glyph is decoration: it never contributes to a row\u2019s name', () => {
  const markup = renderKit(ShellRail, { pins: PINS, collapsed: false });
  assert.ok(/<span class="glyph" aria-hidden="true">/.test(markup), 'the glyph is aria-hidden');
});

test('collapsed, the rows are one roving stop: one row holds the tab, the rest wait for arrows', () => {
  const markup = renderKit(ShellRail, { pins: PINS });
  const rows = markup.match(/<button[^>]*data-action="rail-select"[^>]*>[\s\S]*?<\/button>/g) ?? [];
  const stops = rows.map((r) => /data-tab="(-?\d)"/.exec(r)?.[1]);
  assert.equal(stops.filter((t) => t === '0').length, 1, 'exactly one row tab stop');
  assert.equal(stops.filter((t) => t === '-1').length, 2, 'the other rows wait for the arrows');
  assert.ok(markup.includes('title="Keep your pins open"'), 'the toggle keeps its natural stop');
});

test('expanded, every control is in the tab order', () => {
  const markup = renderKit(ShellRail, { pins: PINS, collapsed: false });
  const rows = markup.match(/<button[^>]*data-action="rail-select"[^>]*>[\s\S]*?<\/button>/g) ?? [];
  const stops = rows.map((r) => /data-tab="(-?\d)"/.exec(r)?.[1]);
  assert.ok(stops.every((t) => t === '0'), 'all tabbable when expanded');
});

test('the active pin is marked in words and geometry, and only when it is real', () => {
  const marked = renderKit(ShellRail, { pins: PINS, collapsed: false, active: 'drive-run/my-run' });
  const row = marked.match(/<button[^>]*data-key="drive-run\/my-run"[^>]*>/);
  assert.ok(row !== null && row[0].includes('aria-current="true"'), 'the active row says so');
  const none = renderKit(ShellRail, { pins: PINS, collapsed: false, active: 'not/pinned' });
  assert.ok(!none.includes('aria-current="true"'), 'no tick parked at the top when nothing is active');
});

// ── the D-107 empty state: the first thing a new person sees ─────────────────────────────────────

test('an empty pin list is the default, and it is a calm sentence rather than a blank nav', () => {
  const markup = visible(renderKit(ShellRail, { pins: [] }));
  const block = /<p class="empty">([\s\S]*?)<\/p>/.exec(markup);
  assert.ok(block !== null, 'the empty state renders');
  const title = /<strong>([^<]*)<\/strong>/.exec(block[1])?.[1]?.replace(/\s+/g, ' ').trim();
  assert.ok(title !== undefined && title.endsWith('.'), `a D-107 title ends: "${String(title)}"`);
  const body = block[1].replace(/<strong>[\s\S]*?<\/strong>/, '').replace(/\s+/g, ' ').trim();
  const words = body.split(/\s+/).filter(Boolean).length;
  assert.ok(words >= 8, `"${body}" is ${words} words: a label, not a sentence`);
  assert.ok(/[.!?]$/.test(body), 'the body ends');
  assert.equal((body.match(/[.!?]\s+\S/g) ?? []).length, 0, 'one calm line, not two sentences');
  assert.ok(/\bpin\b/i.test(body), 'the body names the action');
  assert.ok(/selector/i.test(body), 'and where a pin is made');
  assert.ok(!/role="(alert|status)"/.test(block[0]), 'nothing pinned is not an event to announce');
});

test('the empty state sits inside the nav that names itself', () => {
  const markup = renderKit(ShellRail, { pins: [] });
  const navAt = markup.indexOf('aria-label="Pinned surfaces"');
  const emptyAt = markup.indexOf('class="empty"');
  const navEnd = markup.indexOf('</nav>');
  assert.ok(navAt > 0 && navAt < emptyAt && emptyAt < navEnd, 'the reader is told what the region is first');
});

test('the nav names itself as the person\u2019s pins, never a navigation tree', () => {
  const markup = renderKit(ShellRail, { pins: PINS });
  assert.ok(markup.includes('aria-label="Pinned surfaces"'), 'the nav\u2019s own name');
});

// ── the toggle is one button across postures, so it keeps keyboard focus ─────────────────────────

const toggleOf = (markup) => /<button[^>]*data-action="rail-toggle"[^>]*>[\s\S]*?<\/button>/.exec(markup)?.[0] ?? '';

test('the toggle is one template in both postures: Lit keeps the focused button instead of replacing it', () => {
  const collapsed = renderKit(ShellRail, { pins: PINS });
  const expanded = renderKit(ShellRail, { pins: PINS, collapsed: false });
  // Lit swaps the DOM when the template changes, and the focused button is lost.
  // The server render keeps the template hash in its part comments, so two
  // templates show as two different skeletons once the bound values are blanked.
  const skeleton = (markup) => {
    const head = /<div class="rhead">[\s\S]*?<\/button>/.exec(markup)?.[0] ?? '';
    return head.replace(/="[^"]*"/g, '=""');
  };
  assert.notEqual(skeleton(collapsed), '', 'the header band is found');
  assert.equal(skeleton(expanded), skeleton(collapsed), 'one button, posture bound on its attributes');
  assert.equal(railSrc.match(/data-action="rail-toggle"/g)?.length, 1, 'the source spells the toggle once');
});

test('the toggle carries the posture on aria-expanded and says the words for it', () => {
  const collapsed = toggleOf(renderKit(ShellRail, { pins: PINS }));
  const expanded = toggleOf(renderKit(ShellRail, { pins: PINS, collapsed: false }));
  assert.ok(collapsed.includes('aria-expanded="false"') && collapsed.includes('Keep your pins open'));
  assert.ok(expanded.includes('aria-expanded="true"') && expanded.includes('Let your pins hide again'));
  for (const markup of [collapsed, expanded]) {
    const title = /title="([^"]*)"/.exec(markup)?.[1];
    const label = /aria-label="([^"]*)"/.exec(markup)?.[1];
    assert.ok(title && title === label, 'title and aria-label say the same words, character for character');
  }
});

// ── the actions: one select verb, one toggle verb ────────────────────────────────────────────────

test('every row routes through one action name carrying its key', () => {
  const markup = renderKit(ShellRail, { pins: PINS });
  for (const pin of PINS) {
    assert.ok(markup.includes(`data-key="${pin.key}"`), `the row for ${pin.key} carries its key`);
  }
  assert.ok(!markup.includes('data-action="rail-open"'), 'no second verb: the rail selects and toggles');
});

// ── FLOORS, as source scans in the house split ───────────────────────────────────────────────────

test('FLOOR: the posture resolves THROUGH the pure resolver, and the default without storage is collapsed', () => {
  assert.ok(
    /railCollapsedFromStored\(/.test(railSrc),
    'the rail must delegate to the pure resolver, not re-derive the stored value',
  );
  assert.ok(/this\.collapsed = true;/.test(railSrc), 'the field initializer must be the collapsed default');
  assert.ok(/RAIL_COLLAPSED_KEY/.test(railSrc), 'and under the one agreed key');
});

test('FLOOR: the toggle still writes the stored mark through the one writer', () => {
  assert.ok(
    /railCollapsedToStored\(/.test(railSrc),
    "the toggle writes through railCollapsedToStored: the '0' is what makes an explicit expand stick",
  );
});

test('FLOOR: every title in the rail is matched, character for character, by an aria-label', () => {
  // The toggle's words are bound per posture, so the floor reads the render of both.
  for (const collapsed of [true, false]) {
    const markup = renderKit(ShellRail, { pins: PINS, collapsed });
    const titles = [...markup.matchAll(/\btitle="([^"]*)"/g)].map((m) => m[1]);
    assert.ok(titles.length > 0, 'the icon-only controls still need their tooltips');
    for (const t of titles) {
      assert.ok(markup.includes(`aria-label="${t}"`), `the title "${t}" has no matching aria-label`);
    }
  }
});

test('FLOOR: the rail is drawn in tokens only, no hex colour literals anywhere in the source', () => {
  const hexes = [...railSrc.matchAll(/#[0-9a-fA-F]{3,8}\b/g)].map((m) => m[0]);
  assert.deepEqual(hexes, [], `shell-rail.ts must stay literal-free, found: ${hexes.join(', ')}`);
});

test('FLOOR: every row and the toggle are 44 px targets, at every size, because the rail is the phone rail too', () => {
  const rules = parseRules(ShellRail.styles.map((sheet) => sheet.cssText).join('\n'));
  assert.ok(px(declsFor(rules, '.row')['min-height']) >= 44, 'the rows carry the 44 px floor');
  const toggle = declsFor(rules, '.toggle');
  assert.ok(px(toggle['width']) >= 44 && px(toggle['height']) >= 44, 'the toggle is a 44 px square');
});

