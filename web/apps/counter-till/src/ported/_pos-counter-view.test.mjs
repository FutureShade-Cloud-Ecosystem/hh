// Ported from hardscapeos_dibbits master 87d7941b app/src/_pos-counter-view.test.mjs; adapted for HH.

// _pos-counter-view.test.mjs: WHICH PARTS OF THE COUNTER A PERSON KEEPS ON SCREEN (rulings R1 and R2).
//
// The ruling's hardest half is that two people at ONE dealer want opposite answers. Ryan Bostock,
// asked whether he is hotkey-heavy: "No, I don't even know what that is. So, probably not": and he
// is also the person who wants screen real estate back and said "I wouldn't need it when I'm in my
// back office" about the hot list. Ryan Rood wants BisTrack's keys in his hands. A dealer-wide
// switch has to be wrong for one of them every day, so this is a USER preference on the
// presentation substrate, resolved user-over-dealer-over-default, whole-or-absent per level.
//
// node --import./src/_ts-js-loader.mjs --test src/_pos-counter-view.test.mjs (run from app/)
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import {
  panelView,
  panelWrite,
  asSurfaceItems,
  isCounterPanel,
  COUNTER_PANELS,
  COUNTER_PANEL_DEFAULTS,
  PANEL_KIND,
  PANEL_LEGEND,
  PANEL_HOTLIST,
  PANEL_LABELS,
  POS_COUNTER_SURFACE_ID,
} from './_pos-counter-view.ts';

const code = (t) => t.replace(/\/\*[\s\S]*?\*\//g, '').replace(/^\s*\/\/.*$/gm, '');
const terminalSrc = code(readFileSync(new URL('./pos-terminal.ts', import.meta.url), 'utf8'));
const item = (id, visible, kind = PANEL_KIND) => ({ kind, id, position: 0, visible });

test('counter view: both panels are ON before anybody configures anything', () => {
 // Defaults do the work. A counter person who has never opened a menu sees the keys they can press
 // and the tiles they can tap; the person who wants the screen back says so once.
  assert.deepEqual(panelView([], []), { ...COUNTER_PANEL_DEFAULTS });
  assert.deepEqual(panelView(null, undefined), { [PANEL_LEGEND]: true, [PANEL_HOTLIST]: true });
});

test('counter view: the person overrides the dealer WHOLE, never merged', () => {
 // The 0140/0149 ruling verbatim: a stored set IS the answer at that level.
  const dealer = [item(PANEL_LEGEND, false), item(PANEL_HOTLIST, false)];
  const mine = [item(PANEL_LEGEND, true)];
  const v = panelView(dealer, mine);
  assert.equal(v[PANEL_LEGEND], true, 'my row wins');
  assert.equal(v[PANEL_HOTLIST], true, 'and the dealer set is NOT merged under it');
  assert.equal(panelView(dealer, [])[PANEL_HOTLIST], false, 'with nothing of mine, the dealer answers');
});

test('counter view: a back-office desk turns the hot list off and keeps its keys', () => {
 // Ryan Bostock: "I wouldn't need it when I'm in my back office." Ryan Rood: "It could be turned
 // on." One switch each, and neither switch touches the other.
  const v = panelView([], [item(PANEL_HOTLIST, false), item(PANEL_LEGEND, true)]);
  assert.equal(v[PANEL_HOTLIST], false);
  assert.equal(v[PANEL_LEGEND], true);
});

test('counter view: an unknown id draws nothing and never becomes a phantom switch', () => {
  assert.ok(isCounterPanel(PANEL_LEGEND) && isCounterPanel(PANEL_HOTLIST));
  assert.ok(!isCounterPanel('teleporter'));
  const v = panelView([], [item('teleporter', false), item(PANEL_LEGEND, false)]);
  assert.deepEqual(Object.keys(v).sort(), [...COUNTER_PANELS].sort());
  assert.equal(v[PANEL_LEGEND], false);
});

test('counter view: rows of another KIND cannot switch a panel off', () => {
 // The user override is whole-or-absent PER KIND, which is what keeps a panel write from
 // disturbing the key roster (kind action) or the curated tiles (kind chip).
  const v = panelView([], [item(PANEL_LEGEND, false, 'chip'), item(PANEL_HOTLIST, false, 'action')]);
  assert.deepEqual(v, { ...COUNTER_PANEL_DEFAULTS });
});

test('counter view: the write is the WHOLE set, in registry order, with one flag flipped', () => {
  const rows = panelWrite({ [PANEL_LEGEND]: true, [PANEL_HOTLIST]: true }, PANEL_LEGEND, false);
  assert.deepEqual(
    rows,
    [
      { kind: PANEL_KIND, id: PANEL_LEGEND, position: 0, visible: false },
      { kind: PANEL_KIND, id: PANEL_HOTLIST, position: 1, visible: true },
    ],
    'a partial write would make the stored set mean something it is not',
  );
  assert.deepEqual(panelWrite({}, PANEL_HOTLIST, false)[0].visible, true, 'unmentioned panels stay on');
});

test('counter view: the loader coerces at the boundary and survives a wrong-shaped body', () => {
  assert.deepEqual(asSurfaceItems({ user_items: 'nope' }, 'user_items'), []);
  assert.deepEqual(asSurfaceItems(null, 'role_items'), []);
  assert.deepEqual(asSurfaceItems({ role_items: [{ kind: 'panel', id: 'legend' }] }, 'role_items'), [
    { kind: 'panel', id: 'legend', position: 0, visible: true },
  ]);
});

test('counter view: the labels are chrome: one or two words, never a sentence', () => {
  for (const id of COUNTER_PANELS) {
    const label = PANEL_LABELS[id];
    assert.ok(label, `${id} has a label`);
    assert.ok(label.split(/\s+/).length <= 2, `"${label}" is ${label.split(/\s+/).length} words`);
    assert.ok(!/[.!?]$/.test(label), 'no sentences in chrome');
  }
});

test('counter view: the terminal stores this against the PERSON on the shared substrate', () => {
 // Not a dealer-wide switch and not a new table: surface_user_items under pos.counter, which is
 // the same surface the key roster and the hot-list tiles are already curated through.
  assert.equal(POS_COUNTER_SURFACE_ID, 'pos.counter');
  assert.match(terminalSrc, /\/api\/v1\/surfaces\/\$\{POS_COUNTER_SURFACE_ID\}\/my-items/);
  assert.match(terminalSrc, /method: 'PUT'/);
  assert.ok(!/my-items`, \{ method: 'DELETE' \}/.test(terminalSrc),
    'a reset would clear the roster and tile overrides too: this writes the panel set instead');
});

test('counter view: a failed write puts the switch back rather than lying about what is stored', () => {
  const body = terminalSrc.slice(terminalSrc.indexOf('private async setPanel'), terminalSrc.indexOf('private dismissLegend'));
  assert.match(body, /const before = this\.panels;/);
  assert.match(body, /if \(!res\.ok\) this\.panels = before;/);
  assert.match(body, /catch \{\s*\n?\s*this\.panels = before;/);
});
