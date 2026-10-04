// Ported from hardscapeos_dibbits master 87d7941b app/src/_response-shape.test.mjs; adapted for HH.

// _response-shape.test.mjs: DIB-471, the pure half: what a loader does with a body it did not expect.
//
// The rule under test is one sentence: on a 2xx, a body that PARSES but is not the shape this
// surface expected carries NO ROWS, and no rows is the EMPTY state. It is never a fault, because
// nothing failed: the request was answered.
//
// The exact body is the one the W6-D empty-state sweep serves to every GET. It is pinned here
// verbatim rather than described, because the whole class of defect is "the shape was not what the
// caller assumed" and a paraphrase of the shape is exactly the assumption that broke.
//
// node --import./src/_ts-js-loader.mjs --test src/_response-shape.test.mjs (run from app/)
import { test } from 'node:test';
import assert from 'node:assert/strict';

import { asRows, ENVELOPE_KEYS } from './_response-shape.ts';

// The W6-D sweep's synthetic body, character for character (scripts/walkthrough-w6d-states.cjs).
const SYNTHETIC = {
  items: [], results: [], rows: [], data: [], lines: [], entries: [], total: 0, count: 0,
};

test('DIB-471: the synthetic sweep body normalises to NO ROWS, not to a throw', () => {
 // Every one of the five surfaces' key lists, against the body that broke them.
  for (const keys of [
    ['staff', ...ENVELOPE_KEYS],
    ['roles', ...ENVELOPE_KEYS],
    ['products', ...ENVELOPE_KEYS],
    ['facets', ...ENVELOPE_KEYS],
    ['accounts', ...ENVELOPE_KEYS],
    ENVELOPE_KEYS,
  ]) {
    const rows = asRows(SYNTHETIC, keys);
    assert.ok(Array.isArray(rows), `asRows must always return an array (keys: ${keys[0]})`);
    assert.equal(rows.length, 0);
 // The failure mode this whole ticket is about: the value reaching a template must support the
 // array protocol. `.map` on an object is the literal TypeError admin/staff threw.
    assert.doesNotThrow(() => rows.map((r) => r));
  }
});

test('DIB-471: a shape it cannot read is EMPTY, never a signal to raise an error', () => {
 // Everything a 2xx can plausibly carry that is not a collection. All of it is "no rows".
  for (const body of [null, undefined, 0, 1, '', 'ok', true, false, {}, { total: 0 }, { items: 3 }, NaN]) {
    const rows = asRows(body);
    assert.deepEqual(rows, [], `expected [] for ${JSON.stringify(body) ?? String(body)}`);
  }
});

test('DIB-471: a real payload survives normalisation unchanged: a bare array', () => {
 // The staff and products endpoints answer with a bare array. Normalisation must be a no-op on the
 // shape that actually ships, or it would be trading a crash for silent data loss.
  const staff = [{ user_id: 'a', display_name: 'Alex' }, { user_id: 'b', display_name: 'Bo' }];
  assert.equal(asRows(staff, ['staff', ...ENVELOPE_KEYS]), staff);
  assert.equal(asRows(staff).length, 2);
});

test('DIB-471: a real payload survives normalisation unchanged: a paged envelope', () => {
 // gl-api.ts Page<T>: { items, total, limit, offset }. The chart of accounts ships this.
  const page = { items: [{ code: '1000' }, { code: '2000' }], total: 2, limit: 50, offset: 0 };
  assert.deepEqual(asRows(page, ['accounts', ...ENVELOPE_KEYS]), page.items);
});

test('DIB-471: the surface-specific key is preferred over the generic envelope', () => {
 // A body carrying BOTH a named collection and a generic one must resolve to the named one: the
 // caller knows its own envelope, and the defaults are a fallback, not an override.
  const body = { staff: [{ user_id: 'a' }], items: [] };
  assert.equal(asRows(body, ['staff', ...ENVELOPE_KEYS]).length, 1);
  assert.equal(asRows(body).length, 0, 'without the named key the generic envelope still wins');
});

test('DIB-471: a non-array value under an envelope key is not mistaken for rows', () => {
 // `{ items: { … } }` is a shape too, and casting it would reintroduce exactly the defect: a
 // non-array reaching a `.map`.
  const rows = asRows({ items: { nested: true }, rows: 'nope', data: [{ ok: true }] });
  assert.deepEqual(rows, [{ ok: true }], 'it must skip past the unusable keys to a real array');
  assert.doesNotThrow(() => asRows({ items: { nested: true } }).map((r) => r));
});
