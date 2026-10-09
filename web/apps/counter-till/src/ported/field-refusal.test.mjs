// Ported from hardscapeos_dibbits master 87d7941b app/src/field-refusal.test.mjs; adapted for HH.

// field-refusal.test.mjs: item 3 (session A): the server's 422 sentence lands at the control it is
// about, and a sentence with nowhere to go is never swallowed.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { fieldRefusal, formRefusal, refusalFor, whenWord } from './field-refusal.ts';

// Every validator in this codebase writes the knob's own name as the FIRST WORD, because that is how
// a sentence about a knob reads out loud. The field identity is therefore recoverable without
// changing a single server message.
test('a refusal names the control it is about, and the words are the server\'s own', () => {
  const r = fieldRefusal('commit_prompt_hours must be between 0 and 336');
  assert.equal(r.field, 'commit_prompt_hours');
  assert.equal(r.sentence, 'commit_prompt_hours must be between 0 and 336', 'verbatim: never paraphrased');

  assert.equal(fieldRefusal('reopen_policy must be ANYONE, MANAGER or NOBODY').field, 'reopen_policy');
  assert.equal(fieldRefusal('working_view_days must be between 1 and 90').field, 'working_view_days');
 // A parenthesised value does not hide the knob.
  assert.equal(fieldRefusal('aging_warn_days (95) must be fewer than aging_escalate_days (90)').field, 'aging_warn_days');
});

// Defence in depth. The marshal layer already strips this (item 5e), so a current server never sends
// one: but this function's whole job is to find the FIRST WORD, and a stale server would otherwise
// offer "dispatch" as the field name.
test('a module prefix is stripped rather than mistaken for the field', () => {
  const r = fieldRefusal('dispatch: commit_prompt_hours must be between 0 and 336');
  assert.equal(r.field, 'commit_prompt_hours');
  assert.equal(r.sentence, 'commit_prompt_hours must be between 0 and 336');
  assert.doesNotMatch(r.sentence, /dispatch:/, 'internal engineering vocabulary must never reach a screen');
});

// THE ONE REAL MISMATCH, and it is not hypothetical: the settings kit's SHARED visibility validator
// says cost_visibility because it is shared, while the dispatch policy's column: and the control on
// screen: is load_value_visibility. Without the alias the refusal floats to the top of the page as an
// unattached banner, which is the behaviour this whole file exists to replace.
test('the shared visibility validator anchors to the column the screen actually renders', () => {
  const r = fieldRefusal('cost_visibility must be MANAGER_UP, ALL_STAFF or HIDDEN');
  assert.equal(r.field, 'load_value_visibility');
  assert.equal(r.sentence, 'cost_visibility must be MANAGER_UP, ALL_STAFF or HIDDEN', 'the words are still the server\'s');
});

// The shape test is what stops an ordinary sentence from being mistaken for a field name and
// anchoring a message under an unrelated input, where nobody would find it.
test('a sentence that names no knob names no field', () => {
  for (const msg of [
    'Nothing was saved because the request could not be read',
    'changing dealer screen settings is limited to the General Manager, Financial Controller, HR or Platform Administrator',
    'internal error',
    'branch_id is required',
    '',
  ]) {
    const r = fieldRefusal(msg);
    if (msg === 'branch_id is required') {
      assert.equal(r.field, 'branch_id', 'a real column name IS a field, even on a request-level refusal');
      continue;
    }
    assert.equal(r.field, '', `"${msg}" names no control`);
  }
 // A single lowercase word is not a column name: one underscore at least.
  assert.equal(fieldRefusal('saved').field, '');
});

test('refusalFor hands the sentence to exactly one control', () => {
  const r = fieldRefusal('working_view_days must be between 1 and 90');
  assert.equal(refusalFor(r, 'working_view_days'), 'working_view_days must be between 1 and 90');
  assert.equal(refusalFor(r, 'commit_horizon_weeks'), '', 'and to no other');
  assert.equal(refusalFor(null, 'working_view_days'), '');
  assert.equal(refusalFor(fieldRefusal('internal error'), 'anything'), '');
});

// THE LOAD-BEARING CASE. If a validator gains a knob the screen has not caught up with, anchoring by
// field alone would SILENTLY SWALLOW the message: the save fails and the screen says nothing. A
// sentence with nowhere to go goes to the top.
test('a refusal for a control this screen does not render still reaches the reader', () => {
  const rendered = ['commit_prompt_hours', 'reopen_policy'];
  assert.equal(formRefusal(fieldRefusal('commit_prompt_hours must be between 0 and 336'), rendered), '',
    'a refusal with a home is shown at home, not twice');
  assert.equal(
    formRefusal(fieldRefusal('freight_split_mode must be EVEN or MANUAL'), rendered),
    'freight_split_mode must be EVEN or MANUAL',
    'a knob this screen does not draw must not be swallowed',
  );
  assert.equal(
    formRefusal(fieldRefusal('changing dealer screen settings is limited to the General Manager, Financial Controller, HR or Platform Administrator'), rendered),
    'changing dealer screen settings is limited to the General Manager, Financial Controller, HR or Platform Administrator',
    'and a refusal that names no knob at all belongs at the top',
  );
  assert.equal(formRefusal(null, rendered), '');
});

// A history row that says "Invalid Date" looks broken in a way that sends somebody to check a bug,
// while the row's real content: what changed, from what, by whom: is still worth showing.
test('a recorded instant reads as a date, and an unreadable one reads as nothing', () => {
  const out = whenWord('2026-08-31T19:39:56.424134Z');
  assert.match(out, /2026/);
  assert.doesNotMatch(out, /T\d\d:/, 'an ISO stamp is not how anybody reads a history');
  assert.equal(whenWord('not a date'), '');
  assert.equal(whenWord(undefined), '');
  assert.equal(whenWord(''), '');
});
