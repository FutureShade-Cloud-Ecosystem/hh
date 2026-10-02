// The margin surface's policy gate (marginVisibleToCaller in _pos-margin-visibility.ts),
// P3-10 of the comprehensive train review. The principal's ruling: the surface follows the
// policy word. /pos/policy answers margin_visibility and the till obeys it: no margin column,
// no footer margin or profit segments, and no margin eye for a caller the word refuses, all
// three for a caller it allows, even while the glance itself keeps answering (the glance's own
// serving rule is the core's cost and margin configuration, the hardening lane's half).
//
// The whole table is pinned here, refusals first, because the gate's whole job is refusing:
// tier numbers are the core resolver's (core/internal/identity/authority.go, read only):
// 1 leadership, 2 manager, 3 frontline.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { marginVisibleToCaller } from './_pos-margin-visibility.ts';

test('MANAGER_UP admits the resolver tiers 1 and 2 and refuses the frontline tier 3', () => {
  assert.equal(marginVisibleToCaller('MANAGER_UP', 1), true);
  assert.equal(marginVisibleToCaller('MANAGER_UP', 2), true);
  assert.equal(marginVisibleToCaller('MANAGER_UP', 3), false);
});

test('MANAGER_UP fails closed on a session with no tier, or one outside the resolver range', () => {
  assert.equal(marginVisibleToCaller('MANAGER_UP', undefined), false);
  assert.equal(marginVisibleToCaller('MANAGER_UP', 0), false);
  assert.equal(marginVisibleToCaller('MANAGER_UP', 4), false);
});

test('ALL_STAFF admits every staff caller, with a tier or without one', () => {
  assert.equal(marginVisibleToCaller('ALL_STAFF', 1), true);
  assert.equal(marginVisibleToCaller('ALL_STAFF', 2), true);
  assert.equal(marginVisibleToCaller('ALL_STAFF', 3), true);
  // The word itself demands no tier proof, so a session that carried none is
  // still a staff caller as far as this word can see.
  assert.equal(marginVisibleToCaller('ALL_STAFF', undefined), true);
});

test('HIDDEN admits nobody at any tier', () => {
  assert.equal(marginVisibleToCaller('HIDDEN', 1), false);
  assert.equal(marginVisibleToCaller('HIDDEN', 2), false);
  assert.equal(marginVisibleToCaller('HIDDEN', 3), false);
  assert.equal(marginVisibleToCaller('HIDDEN', undefined), false);
});

test('an absent word fails closed at every tier', () => {
  assert.equal(marginVisibleToCaller(undefined, 1), false);
  assert.equal(marginVisibleToCaller(undefined, 2), false);
  assert.equal(marginVisibleToCaller(undefined, 3), false);
  assert.equal(marginVisibleToCaller(undefined, undefined), false);
});

test('an unknown word fails closed: the contract enum is matched exactly', () => {
  // Wrong case, padding, and words that were never in the enum all refuse:
  // a near miss is still a word the contract does not carry.
  assert.equal(marginVisibleToCaller('manager_up', 1), false);
  assert.equal(marginVisibleToCaller(' all_staff ', 1), false);
  assert.equal(marginVisibleToCaller('MANAGERS_ONLY', 1), false);
  assert.equal(marginVisibleToCaller('', 1), false);
});
