// Ported from hardscapeos_dibbits master 87d7941b app/src/quick-action-guard.test.mjs; adapted for HH.

// quick-action-guard.test.mjs: DIB-434. THE IN-PROGRESS GUARD, held to its promise.
//
// The ruling's hard requirement is one sentence: a quick "+ Sale" from anywhere in the OS must
// NEVER clobber a ticket that is already live on the till. Every test below is a way of that
// sentence being false.
//
// node --import./src/_ts-js-loader.mjs --test src/quick-action-guard.test.mjs (run from app/)
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  quickStartMode,
  quickStartNeedsGuard,
  canPark,
  guardMessage,
} from './quick-action-guard.ts';

const empty = { lineCount: 0, hasCustomer: false, hasOtherWork: false };

// ── THE NAMED GUARD TEST ───────────────────────────────────────────────────────────────────────
// This is the one the mutation is aimed at. If quickStartMode is ever changed to return 'fresh'
// for a ticket with lines, this fails by name.
test('DIB-434 GUARD: a ticket with lines is NEVER started over silently', () => {
  for (const lineCount of [1, 2, 17]) {
    const live = { ...empty, lineCount };
    assert.equal(
      quickStartMode(live),
      'park-or-continue',
      `${lineCount} line(s) on the till must offer Park or Continue: never a silent reset`,
    );
    assert.equal(quickStartNeedsGuard(live), true);
    assert.equal(canPark(live), true, 'a ticket with lines is exactly what the parked rail holds');
  }
});

test('DIB-434 GUARD: work with no lines still stops the action, with an honest reason', () => {
 // A selected customer, or a typed note / requested date, is real work. It cannot be PARKED (a
 // parked ticket with no lines is not a ticket), so the guard offers Continue or a named clear:
 // and never claims it can park something it cannot.
  for (const live of [
    { ...empty, hasCustomer: true },
    { ...empty, hasOtherWork: true },
    { ...empty, hasCustomer: true, hasOtherWork: true },
  ]) {
    assert.equal(quickStartMode(live), 'confirm-or-continue');
    assert.equal(quickStartNeedsGuard(live), true);
    assert.equal(canPark(live), false, 'the dialog must not offer Park for a ticket with no lines');
    assert.match(
      guardMessage(live),
      /no lines to park/i,
      'the sentence has to say WHY it is not offering Park',
    );
  }
});

test('DIB-434 GUARD: an empty terminal starts fresh with no prompt', () => {
 // The other half of a good guard: it must not nag. A prompt on an empty till is the prompt people
 // learn to dismiss without reading, which is how the ones that matter stop working.
  assert.equal(quickStartMode(empty), 'fresh');
  assert.equal(quickStartNeedsGuard(empty), false);
  assert.equal(guardMessage(empty), '');
});

test('DIB-434 GUARD: lines win over everything: the park path is offered whenever it is possible', () => {
 // A ticket with lines AND a customer AND notes is still 'park-or-continue': the richest ticket is
 // the one it would be worst to lose, and it is exactly the one the rail can keep.
  const rich = { lineCount: 3, hasCustomer: true, hasOtherWork: true };
  assert.equal(quickStartMode(rich), 'park-or-continue');
});

test('DIB-434 GUARD: the guard sentence counts the lines it is protecting', () => {
  assert.match(guardMessage({ ...empty, lineCount: 1 }), /1 line\b/);
  assert.match(guardMessage({ ...empty, lineCount: 4 }), /4 lines\b/);
});

test('DIB-434 GUARD: no input combination produces a silent reset of a non-empty ticket', () => {
 // Exhaustive over the decision's whole input space (line counts 0..3 × the two flags), because
 // "never" is a claim about every input, not about the three a reviewer thought of.
  for (const lineCount of [0, 1, 2, 3]) {
    for (const hasCustomer of [false, true]) {
      for (const hasOtherWork of [false, true]) {
        const live = { lineCount, hasCustomer, hasOtherWork };
        const isEmpty = lineCount === 0 && !hasCustomer && !hasOtherWork;
        assert.equal(
          quickStartMode(live) === 'fresh',
          isEmpty,
          `a reset with no prompt is allowed ONLY on a completely empty till: ${JSON.stringify(live)}`,
        );
      }
    }
  }
});
