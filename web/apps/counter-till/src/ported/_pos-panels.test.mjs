// Ported from hardscapeos_dibbits master 87d7941b app/src/_pos-panels.test.mjs; adapted for HH.

// _pos-panels.test.mjs: 0220: where the counter's four panels are ALLOWED to be.
//
// Every assertion here is about the same failure: a panel nobody can get back. A box off the left
// edge, a header dragged above the top, a resize that walks the origin while the width sits pinned
// at its floor: each one ends with a clerk unable to reach Payment with a customer in front of
// them. That is why this arithmetic is tested against invented areas rather than discovered at a
// till.
// node --test src/_pos-panels.test.mjs (run from app/, via the TS loader)
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  PANEL_ORDER,
  PANEL_MIN_W,
  PANEL_MIN_H,
  PANEL_HEAD_H,
  PANEL_KEEP_X,
  defaultLayout,
  clampBox,
  moveBox,
  resizeBox,
  normalizeLayout,
  normalizeOrder,
  bringToFront,
} from './_pos-panels.ts';

const AREA = { w: 1400, h: 820 };
const NARROW = { w: 520, h: 900 };

// ── the default tiling ───────────────────────────────────────────────────────────────────────────
test('the default is a 2x2 in the order the sale happens, with no panel overlapping another', () => {
  const l = defaultLayout(AREA);
  assert.deepEqual(Object.keys(l).sort(), [...PANEL_ORDER].sort());
  const [c, i, p, s] = PANEL_ORDER.map((id) => l[id]);
  assert.equal(c.y, i.y, 'Customer and Items share the top row');
  assert.equal(p.y, s.y, 'Payment and Summary share the second row');
  assert.ok(c.x < i.x, 'Customer sits left of Items');
  assert.ok(c.y < p.y, 'the top row sits above the second');
  assert.ok(c.x + c.w <= i.x, 'the top row does not overlap itself');
  assert.ok(c.y + c.h <= p.y, 'the rows do not overlap each other');
});

test('every default panel is at least the usable minimum', () => {
  for (const area of [AREA, NARROW, { w: 300, h: 300 }]) {
    for (const id of PANEL_ORDER) {
      const b = defaultLayout(area)[id];
      assert.ok(b.w >= PANEL_MIN_W, `${id} width ${b.w} at ${area.w}`);
      assert.ok(b.h >= PANEL_MIN_H, `${id} height ${b.h} at ${area.w}`);
    }
  }
});

test('a narrow area tiles into ONE column rather than two that overlap', () => {
  const l = defaultLayout(NARROW);
  const xs = new Set(PANEL_ORDER.map((id) => l[id].x));
  assert.equal(xs.size, 1, 'every panel shares one x');
  const ys = PANEL_ORDER.map((id) => l[id].y);
  assert.deepEqual(ys, [...ys].sort((a, b) => a - b), 'and they stack in flow order');
});

// ── clamping: the rule the file exists for ───────────────────────────────────────────────────────
test('a panel can never be dropped past the left edge with nothing left to grab', () => {
  const b = clampBox({ x: -5000, y: 40, w: 400, h: 300 }, AREA);
  assert.ok(b.x + b.w >= PANEL_KEEP_X, `${PANEL_KEEP_X}px must stay in, got right edge ${b.x + b.w}`);
});

test('a panel can never be dropped past the right edge of the area', () => {
  const b = clampBox({ x: 99999, y: 40, w: 400, h: 300 }, AREA);
  assert.ok(b.x <= AREA.w - PANEL_KEEP_X, `got x=${b.x} in a ${AREA.w}px area`);
});

test('the header band never goes above the top, because the header is the handle', () => {
  assert.equal(clampBox({ x: 100, y: -400, w: 400, h: 300 }, AREA).y, 0);
  const low = clampBox({ x: 100, y: 99999, w: 400, h: 300 }, AREA);
  assert.ok(low.y <= AREA.h - PANEL_HEAD_H, `a header dragged off the bottom: y=${low.y}`);
});

test('size is floored before position is clamped, so the answer is for a box that exists', () => {
  const b = clampBox({ x: 10, y: 10, w: 10, h: 10 }, AREA);
  assert.equal(b.w, PANEL_MIN_W);
  assert.equal(b.h, PANEL_MIN_H);
  assert.equal(b.x, 10);
});

test('clamping is idempotent: a clamped box does not drift on the next read', () => {
  const once = clampBox({ x: -5000, y: 99999, w: 20, h: 20 }, AREA);
  assert.deepEqual(clampBox(once, AREA), once);
});

// ── moving ───────────────────────────────────────────────────────────────────────────────────────
test('a move is the pointer travel, applied to where the box was when the gesture started', () => {
  const from = { x: 100, y: 100, w: 400, h: 300 };
  assert.deepEqual(moveBox(from, 60, -40, AREA), { x: 160, y: 60, w: 400, h: 300 });
});

test('a move that would lose the panel is clamped, not refused', () => {
  const from = { x: 100, y: 100, w: 400, h: 300 };
  const b = moveBox(from, -9999, -9999, AREA);
  assert.equal(b.y, 0);
  assert.ok(b.x + b.w >= PANEL_KEEP_X);
  assert.equal(b.w, 400, 'a move never changes the size');
});

// ── resizing ─────────────────────────────────────────────────────────────────────────────────────
test('the right and bottom edges grow and shrink the size alone', () => {
  const from = { x: 100, y: 100, w: 400, h: 300 };
  assert.deepEqual(resizeBox(from, 50, 70, { right: true, bottom: true }, AREA), { x: 100, y: 100, w: 450, h: 370 });
});

test('the left edge moves the origin and the size together', () => {
  const from = { x: 300, y: 100, w: 400, h: 300 };
  const b = resizeBox(from, 60, 0, { left: true }, AREA);
  assert.equal(b.x, 360);
  assert.equal(b.w, 340, 'the right edge stayed where it was');
  assert.equal(b.x + b.w, from.x + from.w);
});

test('pulling a left edge PAST the minimum stops the edge: it does not walk the panel', () => {
  const from = { x: 300, y: 100, w: 400, h: 300 };
  const b = resizeBox(from, 5000, 0, { left: true }, AREA);
  assert.equal(b.w, PANEL_MIN_W);
  assert.equal(b.x + b.w, from.x + from.w, 'the far edge is still where it was');
  assert.equal(b.x, from.x + from.w - PANEL_MIN_W);
});

test('pulling a top edge past the minimum does the same thing vertically', () => {
  const from = { x: 100, y: 200, w: 400, h: 300 };
  const b = resizeBox(from, 0, 5000, { top: true }, AREA);
  assert.equal(b.h, PANEL_MIN_H);
  assert.equal(b.y + b.h, from.y + from.h);
});

test('a corner pull moves both axes at once', () => {
  const from = { x: 300, y: 200, w: 400, h: 300 };
  const b = resizeBox(from, -50, -60, { left: true, top: true }, AREA);
  assert.equal(b.x, 250);
  assert.equal(b.w, 450);
  assert.equal(b.y, 140);
  assert.equal(b.h, 360);
});

// ── a stored layout, read into a different screen ────────────────────────────────────────────────
test('a layout stored on a big till is clamped into a small laptop, not lost off the edge', () => {
  const big = defaultLayout({ w: 2400, h: 1300 });
  const small = normalizeLayout(big, { w: 900, h: 600 });
  for (const id of PANEL_ORDER) {
    assert.ok(small[id].x <= 900 - PANEL_KEEP_X, `${id} off the right at 900px: x=${small[id].x}`);
    assert.ok(small[id].y <= 600 - PANEL_HEAD_H, `${id} header off the bottom at 600px: y=${small[id].y}`);
  }
});

test('a panel missing from storage takes its box from the default tiling, never from nothing', () => {
  const l = normalizeLayout({ customer: { x: 5, y: 5, w: 500, h: 400 } }, AREA);
  assert.deepEqual(Object.keys(l).sort(), [...PANEL_ORDER].sort());
  assert.deepEqual(l.customer, { x: 5, y: 5, w: 500, h: 400 });
  assert.deepEqual(l.payment, defaultLayout(AREA).payment);
});

test('garbage in storage is the default tiling, not a crash and not NaN', () => {
  for (const junk of [null, undefined, {}, { items: 'wide' }, { items: { x: NaN, y: 0, w: 1, h: 1 } }]) {
    const l = normalizeLayout(junk, AREA);
    for (const id of PANEL_ORDER) {
      assert.ok(Number.isFinite(l[id].x) && Number.isFinite(l[id].w), `${id} from ${JSON.stringify(junk)}`);
    }
  }
});

// ── the stack ────────────────────────────────────────────────────────────────────────────────────
test('bringing a panel to the front puts it last and disturbs nothing else', () => {
  assert.deepEqual(bringToFront(['customer', 'items', 'payment', 'summary'], 'items'), [
    'customer',
    'payment',
    'summary',
    'items',
  ]);
});

test('bringing the front panel to the front again changes nothing', () => {
  const o = ['customer', 'items', 'payment', 'summary'];
  assert.deepEqual(bringToFront(o, 'summary'), o);
});

test('a stored order is made total and deduplicated rather than trusted', () => {
  assert.deepEqual(normalizeOrder(['summary', 'summary', 'nope', 7]), ['summary', 'customer', 'items', 'payment']);
  assert.deepEqual(normalizeOrder(null), [...PANEL_ORDER]);
  assert.deepEqual(normalizeOrder('items'), [...PANEL_ORDER]);
});
