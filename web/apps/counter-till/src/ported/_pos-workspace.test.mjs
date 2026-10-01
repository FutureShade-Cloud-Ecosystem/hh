// Ported from hardscapeos_dibbits master 87d7941b app/src/_pos-workspace.test.mjs; adapted for HH.

// DIB-488 rev 2: the two-panel POS workspace geometry + rules (_pos-workspace.ts), and the
// budget+floor source-scan pair over the terminal's divider semantics. Node built-in runner:
// node --import./src/_ts-js-loader.mjs --test 'src/**/*.test.mjs' (run from app/)
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import {
  SPLIT_MIN,
  SPLIT_MAX,
  SPLIT_DEFAULT,
  clampSplit,
  defaultView,
  resolveWorkspaceView,
  workspacePayload,
  leftPanel,
  splitFromPointer,
  nudgeSplit,
  splitAria,
  toggleCollapse,
  toggleMaximize,
  resolveLayout,
  RAIL_PX,
  workspaceSaveBodyV9,
  mergeWorkspaceForSave,
  changedWorkspaceFields,
  WORKSPACE_FIELDS,
} from './_pos-workspace.ts';

const view = (over = {}) => ({ ...defaultView(), ...over });

// ── geometry ────────────────────────────────────────────────────────────────────────────────────

test('clampSplit holds the 20-80 bounds and rounds; garbage degrades to the default', () => {
  assert.equal(clampSplit(10), SPLIT_MIN);
  assert.equal(clampSplit(95), SPLIT_MAX);
  assert.equal(clampSplit(54.6), 55);
  assert.equal(clampSplit(NaN), SPLIT_DEFAULT);
  assert.equal(clampSplit(Infinity), SPLIT_DEFAULT);
});

test('splitFromPointer: the divider lands AT the pointer (lookup on the left)', () => {
 // Box 1000px wide starting at x=100; pointer at 700 → left share 60%.
  assert.equal(splitFromPointer(700, 100, 1000, 'LOOKUP_LEFT'), 60);
 // Past the edges the clamp holds: a fast drag off the panel never wedges a 0% panel.
  assert.equal(splitFromPointer(50, 100, 1000, 'LOOKUP_LEFT'), SPLIT_MIN);
  assert.equal(splitFromPointer(1900, 100, 1000, 'LOOKUP_LEFT'), SPLIT_MAX);
});

test('splitFromPointer honours the SWAPPED arrangement: the left share belongs to the SALE panel', () => {
 // Same pointer, sale panel on the left: raw left 60% is the SALE share → lookup is 40%.
  assert.equal(splitFromPointer(700, 100, 1000, 'SALE_LEFT'), 40);
});

test('splitFromPointer refuses a degenerate box (mid-layout width 0) with null, not a wild number', () => {
  assert.equal(splitFromPointer(700, 100, 0, 'LOOKUP_LEFT'), null);
  assert.equal(splitFromPointer(700, 100, -5, 'LOOKUP_LEFT'), null);
});

test('nudgeSplit is PHYSICAL: ArrowLeft shrinks whichever panel is on the left', () => {
 // Lookup on the left: left arrow shrinks the lookup share.
  assert.equal(nudgeSplit(view({ split: 50 }), -1), 48);
  assert.equal(nudgeSplit(view({ split: 50 }), 1), 52);
 // Swapped (sale on the left): left arrow shrinks the SALE share, so the lookup share GROWS.
  assert.equal(nudgeSplit(view({ split: 50, arrangement: 'SALE_LEFT' }), -1), 52);
 // Coarse step (PageUp/Down) and the clamp at the rail.
  assert.equal(nudgeSplit(view({ split: 50 }), 1, true), 60);
  assert.equal(nudgeSplit(view({ split: SPLIT_MAX }), 1), SPLIT_MAX);
});

test('splitAria speaks the divider position from the LEFT, and names both panels with their shares', () => {
  const a = splitAria(view({ split: 60 }));
  assert.equal(a.now, 60);
  const swapped = splitAria(view({ split: 60, arrangement: 'SALE_LEFT' }));
  assert.equal(swapped.now, 40); // sale (left) holds 40 of the width when lookup holds 60
  assert.match(swapped.text, /lookup 60%/i);
  assert.match(swapped.text, /Sale 40%/);
});

// ── collapse / maximize rules ───────────────────────────────────────────────────────────────────

test('at least one panel is always open: collapsing the last open panel restores the other', () => {
  const oneRail = toggleCollapse(view(), 'lookup');
  assert.equal(oneRail.lookupCollapsed, true);
  const flipped = toggleCollapse(oneRail, 'sale');
  assert.equal(flipped.saleCollapsed, true);
  assert.equal(flipped.lookupCollapsed, false, 'collapsing the only open panel must restore the other');
});

test('collapsing a maximized panel surrenders the maximize first', () => {
  const maxed = toggleMaximize(view(), 'sale');
  const collapsed = toggleCollapse(maxed, 'sale');
  assert.equal(collapsed.maximized, '');
  assert.equal(collapsed.saleCollapsed, true);
});

test('maximizing a collapsed panel restores it: a maximized rail is a contradiction', () => {
  const railed = toggleCollapse(view(), 'lookup');
  const maxed = toggleMaximize(railed, 'lookup');
  assert.equal(maxed.maximized, 'lookup');
  assert.equal(maxed.lookupCollapsed, false);
 // …and un-maximizing hands the space back without inventing new collapse state.
  const back = toggleMaximize(maxed, 'lookup');
  assert.equal(back.maximized, '');
  assert.equal(back.lookupCollapsed, false);
});

test('maximize leaves the OTHER panel’s collapse preference untouched underneath', () => {
  const saleRailed = toggleCollapse(view(), 'sale');
  const maxed = toggleMaximize(saleRailed, 'lookup');
  assert.equal(maxed.saleCollapsed, true, 'maximize is a moment, not a rewrite of the layout');
});

// ── layout resolution ───────────────────────────────────────────────────────────────────────────

test('resolveLayout: both open → three tracks with the divider between, shares from the split', () => {
  const l = resolveLayout(view({ split: 60 }));
  assert.deepEqual(l.order, ['lookup', 'sale']);
  assert.equal(l.divider, true);
  assert.equal(l.gridColumns, 'minmax(240px, 60fr) auto minmax(240px, 40fr)');
});

test('resolveLayout: the swap reorders the panels without renumbering the split', () => {
  const l = resolveLayout(view({ split: 60, arrangement: 'SALE_LEFT' }));
  assert.deepEqual(l.order, ['sale', 'lookup']);
  assert.equal(l.gridColumns, 'minmax(240px, 40fr) auto minmax(240px, 60fr)');
});

test('resolveLayout: a collapsed panel is a fixed rail, the divider stands down', () => {
  const l = resolveLayout(view({ lookupCollapsed: true }));
  assert.equal(l.lookup, 'rail');
  assert.equal(l.divider, false);
  assert.equal(l.gridColumns, `${RAIL_PX}px minmax(0, 1fr)`);
});

test('resolveLayout: maximize wins over everything: one panel, whole width, other hidden', () => {
  const l = resolveLayout(view({ maximized: 'sale', lookupCollapsed: true }));
  assert.equal(l.sale, 'open');
  assert.equal(l.lookup, 'hidden');
  assert.equal(l.divider, false);
  assert.equal(l.gridColumns, 'minmax(0, 1fr)');
});

// ── resolve + payload (the persistence edge) ────────────────────────────────────────────────────

test('resolveWorkspaceView hardens the wire: absent/garbage degrades to defaults, never an error', () => {
  assert.deepEqual(resolveWorkspaceView(null), defaultView());
  assert.deepEqual(resolveWorkspaceView({}), defaultView());
  const v = resolveWorkspaceView({ split_pct: 400, arrangement: 'DIAGONAL', lookup_collapsed: 'yes' });
  assert.equal(v.split, SPLIT_MAX); // clamped, not trusted
  assert.equal(v.arrangement, 'LOOKUP_LEFT'); // unknown arrangement → default
  assert.equal(v.lookupCollapsed, false); // only a literal true collapses anything
  assert.equal(v.maximized, '', 'maximize is never restored from persistence');
});

test('resolveWorkspaceView refuses the both-collapsed row in favour of the sale panel', () => {
  const v = resolveWorkspaceView({ lookup_collapsed: true, sale_collapsed: true });
  assert.equal(v.lookupCollapsed, true);
  assert.equal(v.saleCollapsed, false);
});

test('workspacePayload carries exactly the persisted ergonomics: and NOT maximize', () => {
  const p = workspacePayload(view({ split: 33, arrangement: 'SALE_LEFT', saleCollapsed: true, maximized: 'lookup' }));
  assert.deepEqual(p, { split_pct: 33, arrangement: 'SALE_LEFT', lookup_collapsed: false, sale_collapsed: true });
  assert.equal('maximized' in p, false);
});

test('leftPanel names the physical order for both arrangements', () => {
  assert.equal(leftPanel(view()), 'lookup');
  assert.equal(leftPanel(view({ arrangement: 'SALE_LEFT' })), 'sale');
});

// ── budget + floor source-scan pair (house rule): the divider must SPEAK, gold-ink stays capped ──

const terminalSrc = readFileSync(fileURLToPath(new URL('./pos-terminal.ts', import.meta.url)), 'utf8');
// HH note: the divider's accessibility floor read the reference's quote work area, the one
// surface that still shipped the two-panel workspace there. That screen is not ported, so the
// floor stayed with the reference (CLOSURE.md); this app owes the gate back if it ever ships a
// splitter.

test('POS restructure: POS retired the two-panel workspace UI: and kept the shared machinery', () => {
 // RETIRED (POS only): the divider, the collapse rails, the swap/maximize controls and the
 // per-panel config popovers. None of them may quietly come back to the counter.
  for (const gone of ['class="wsdivider"', 'data-testid="ws-divider"', 'data-testid="ws-swap"', 'renderPanelControls', 'renderLookupPanel', 'renderSalePanel']) {
    assert.ok(!terminalSrc.includes(gone), `${gone} retired from the counter`);
  }
 // KEPT: this module, unchanged, because the quote builder and the settings kit resolve layouts
 // through it: the retirement was of POS's USE, not of the machinery.
  assert.equal(typeof resolveLayout, 'function', 'the shared layout resolver stands');
  // HH note: the companion assertion read the reference's quote work area; unported, so that
  // half stayed with the reference (CLOSURE.md).
 // KEPT on POS: the two per-user ergonomics that still mean something in one column. They ride the
 // same pos_user_workspace merge-save they always did: nobody's saved preferences were dropped.
  assert.match(terminalSrc, /data-testid="lookup-view-cards"/, 'the results Cards ⇄ Rows flip survived, re-homed');
  assert.match(terminalSrc, /data-testid="margin-eye"/, 'the margin quick-hide survived, re-homed');
 // 0220 moved this marker from V8 to V9 when the counter layout joined the same whole-or-absent
 // row. The CLAIM is unchanged and is what this line is for: the Cards/Rows flip and the margin
 // quick-hide still persist through the one merge-save, not through a second path invented for
 // them. Each composer is additive and the earlier ones are still exported and still pinned above.
  assert.match(terminalSrc, /workspaceSaveBodyV9\(body\)/, 'through the one merge-save contract');
});

test('BUDGET: hardcoded hex colour literals in pos-terminal.ts may only FALL (ratchet ≤ 2)', () => {
 // Two pre-existing #0b1725 gold-ink literals predate this lane (flagged for a tokens sweep).
 // Everything DIB-488 added uses --hos-* roles only; a third hex literal is a regression here.
  const hexes = terminalSrc.match(/#[0-9a-fA-F]{3,8}\b/g) ?? [];
  assert.ok(
    hexes.length <= 2,
    `pos-terminal.ts hex colour budget is 2 (the pre-existing gold inks); found ${hexes.length}: ${hexes.join(', ')}`,
  );
});

// ── 0220 · the counter layout rides the same whole-or-absent row ─────────────────────────────────
//
// The ruling being pinned: the four-step single column is the shipped default at every level, and
// the panels layout is something one person opts into. Everything below is that sentence, in
// assertions.

test('0220: the shipped default is the standard four-step counter', () => {
  assert.equal(defaultView().counterLayout, 'STEPPER');
  assert.equal(resolveWorkspaceView(null).counterLayout, 'STEPPER');
  assert.equal(resolveWorkspaceView({}).counterLayout, 'STEPPER');
});

test('0220: only the literal PANELS leaves the standard counter', () => {
  assert.equal(resolveWorkspaceView({ counter_layout: 'PANELS' }).counterLayout, 'PANELS');
 // A pre-0220 row, a partial read, a typo and a future value all keep the column the person is
 // already looking at: a layout nobody chose is not a layout to put somebody in.
  for (const junk of ['', 'panels', 'TILED', 7, null, undefined, {}]) {
    assert.equal(resolveWorkspaceView({ counter_layout: junk }).counterLayout, 'STEPPER', String(junk));
  }
});

test('0220: the save body carries the layout, and still carries everything it carried before', () => {
  const body = workspaceSaveBodyV9(view({ counterLayout: 'PANELS', split: 42, marginHidden: true }));
  assert.equal(body.counter_layout, 'PANELS');
  assert.equal(body.split_pct, 42);
  assert.equal(body.margin_hidden, true);
  assert.equal(body.lookup_presentation, 'CARDS');
  assert.ok(!('maximized' in body), 'maximize is excluded by construction and stays excluded');
});

test('0220: the layout is a field the merge-save knows about, so one till cannot flatten another', () => {
  assert.ok(WORKSPACE_FIELDS.includes('counterLayout'));
  const before = view({ counterLayout: 'STEPPER' });
  const after = view({ counterLayout: 'PANELS' });
  assert.deepEqual(changedWorkspaceFields(before, after), ['counterLayout']);

 // This till changed only the layout; the stored row has a split this till never touched. The
 // stored split must survive the save.
  const merged = mergeWorkspaceForSave({ split_pct: 31, counter_layout: 'STEPPER' }, after, ['counterLayout']);
  assert.equal(merged.counterLayout, 'PANELS', 'the gesture this surface made is applied');
  assert.equal(merged.split, 31, "and the other till's split is not flattened by it");
});

test('0220: a save that did NOT touch the layout leaves the stored layout alone', () => {
  const local = view({ counterLayout: 'STEPPER', split: 70 });
  const merged = mergeWorkspaceForSave({ split_pct: 55, counter_layout: 'PANELS' }, local, ['split']);
  assert.equal(merged.counterLayout, 'PANELS', 'the stored choice stands: this surface did not change it');
  assert.equal(merged.split, 70);
});

// ── 0220 · what the counter DECLARES about the two layouts (source scan, house convention) ───────

test('0220: the terminal offers both layouts and defaults to the standard counter', () => {
  assert.match(terminalSrc, /counterLayout: CounterLayout = 'STEPPER'/, 'the field ships as STEPPER');
  assert.match(terminalSrc, /data-testid="pos-layout-stepper"/, 'Steps is offered');
  assert.match(terminalSrc, /data-testid="pos-layout-panels"/, 'Panels is offered');
  assert.match(terminalSrc, /role="radiogroup"/, 'as two answers to one question, not two switches');
});

test('0220: a step body is still rendered by exactly ONE switch, in both layouts', () => {
 // The accordion's own rule. Both paths call renderStepBody; nothing else does.
  const calls = [...terminalSrc.matchAll(/this\.renderStepBody\(/g)].length;
  assert.equal(calls, 2, `renderStepBody must be called by the two layouts and nobody else, found ${calls}`);
  assert.match(terminalSrc, /data-testid="pos-flow-panels"/);
  assert.match(terminalSrc, /data-testid="pos-flow-column"/, 'and the single column is still there');
});
