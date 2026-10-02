// Ported from hardscapeos_dibbits master 87d7941b app/src/_customer-picker.test.mjs; adapted for HH.

// Gates for the quote-builder customer picker's decision logic. Run with the suite:
// node --import./src/_ts-js-loader.mjs --test 'src/**/*.test.mjs' (from app/)
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  customerTypeWord,
  recentsKey,
  rememberRecent,
  orderResults,
  nextActive,
  termsLabel,
  pickerStatus,
  isSearchable,
  MAX_RECENTS,
} from './_customer-picker.ts';

test('recently-used accounts are scoped per branch, and "no branch" is its own bucket', () => {
 // Two branches sharing one recency list would push branch A's accounts to the top of branch B's
 // picker: the exact confusion the branch scoping exists to prevent.
  assert.notEqual(recentsKey('branch-a'), recentsKey('branch-b'));
  assert.notEqual(recentsKey(''), recentsKey('branch-a'));
  assert.equal(recentsKey('  branch-a  '), recentsKey('branch-a'), 'whitespace must not fork the key');
});

test('rememberRecent promotes, deduplicates and caps', () => {
  let list = [];
  for (const id of ['a', 'b', 'c']) list = rememberRecent(list, id);
  assert.deepEqual(list, ['c', 'b', 'a'], 'most recent first');

  list = rememberRecent(list, 'a');
  assert.deepEqual(list, ['a', 'c', 'b'], 're-picking must MOVE the entry, not duplicate it');

  let long = [];
  for (let i = 0; i < MAX_RECENTS + 5; i++) long = rememberRecent(long, `id-${i}`);
  assert.equal(long.length, MAX_RECENTS, 'the list is capped');
  assert.equal(long[0], `id-${MAX_RECENTS + 4}`);
});

test('rememberRecent ignores an empty id instead of storing a blank entry', () => {
  assert.deepEqual(rememberRecent(['a'], ''), ['a']);
  assert.deepEqual(rememberRecent(['a'], '   '), ['a']);
});

test('orderResults floats recent accounts without inventing or dropping rows', () => {
  const results = [{ id: 'a' }, { id: 'b' }, { id: 'c' }];
  const ordered = orderResults(results, ['c', 'a']);
  assert.deepEqual(
    ordered.map((r) => r.id),
    ['c', 'a', 'b'],
    'recents in recency order, then the server order',
  );
  assert.equal(ordered.length, results.length, 'no row may be dropped');
});

test('a recent id the server did not return NEVER appears', () => {
 // The recency list is a re-ordering of the server's answer, not a second source of truth. If a
 // stale stored entry could add a row, a deactivated account would reappear at the top of
 // the picker: and the person choosing it would only find out at save time.
  const ordered = orderResults([{ id: 'a' }], ['ghost', 'a']);
  assert.deepEqual(
    ordered.map((r) => r.id),
    ['a'],
    'the ghost id must not materialise a row',
  );
});

test('orderResults preserves the server ordering among non-recent rows', () => {
  const results = [{ id: 'x' }, { id: 'y' }, { id: 'z' }];
  assert.deepEqual(
    orderResults(results, []).map((r) => r.id),
    ['x', 'y', 'z'],
  );
});

test('Down from nothing highlights the FIRST row, not the second', () => {
 // The top row is usually the match the user wants. An off-by-one here silently skips it.
  assert.equal(nextActive(-1, 1, 3), 0);
  assert.equal(nextActive(-1, -1, 3), 2, 'Up from nothing wraps to the last row');
});

test('arrow keys wrap at both ends and never point outside the list', () => {
  assert.equal(nextActive(2, 1, 3), 0);
  assert.equal(nextActive(0, -1, 3), 2);
  assert.equal(nextActive(1, 1, 3), 2);
});

test('an empty list has no active row, so Enter cannot commit one', () => {
  assert.equal(nextActive(-1, 1, 0), -1);
  assert.equal(nextActive(0, 1, 0), -1);
});

test('zero-day terms read as Prepaid, not "Net 0"', () => {
 // "Net 0" reads like a formatting bug; Prepaid is a different commercial conversation and the
 // person building the quote needs to see which one they are in.
  assert.equal(termsLabel(0), 'Prepaid');
  assert.equal(termsLabel(-5), 'Prepaid');
  assert.equal(termsLabel(30), 'Net 30');
  assert.equal(termsLabel(45), 'Net 45');
  assert.equal(termsLabel(Number.NaN), 'Terms unknown');
});

test('the live region says something useful in every state', () => {
 // Silence during a type-ahead is the accessibility failure: the list changes with no announcement.
  assert.match(pickerStatus({ query: '', loading: false, error: '', count: 0 }), /at least two/i);
  assert.match(pickerStatus({ query: 'dib', loading: true, error: '', count: 0 }), /Searching/i);
  assert.match(pickerStatus({ query: 'dib', loading: false, error: '', count: 0 }), /No accounts match/i);
  assert.match(pickerStatus({ query: 'dib', loading: false, error: '', count: 1 }), /1 account match/);
  assert.match(pickerStatus({ query: 'dib', loading: false, error: '', count: 4 }), /4 accounts match/);
  assert.equal(
    pickerStatus({ query: 'dib', loading: false, error: 'Server said no.', count: 0 }),
    'Server said no.',
    'an error must not be swallowed by the count message',
  );
});

test('a one-character query is not sent to the server', () => {
  assert.equal(isSearchable(''), false);
  assert.equal(isSearchable('d'), false);
  assert.equal(isSearchable(' d '), false);
  assert.equal(isSearchable('di'), true);
});

// ── the account-type word (item 3b, session B) ───────────────────────────────────────────────────
// customers.customer_type (migration 0006) CHECK IN
// ('CONTRACTOR','RETAIL','BUILDER','GOVERNMENT','INTERNAL'). Both account pickers print it into the
// sub-line under the name, so the map is pinned against the schema, not against one screen.

const CUSTOMER_TYPES_IN_SCHEMA = ['CONTRACTOR', 'RETAIL', 'BUILDER', 'GOVERNMENT', 'INTERNAL'];

test('every account type the schema defines renders its intended word', () => {
  assert.equal(customerTypeWord('CONTRACTOR'), 'Contractor');
  assert.equal(customerTypeWord('RETAIL'), 'Retail');
  assert.equal(customerTypeWord('BUILDER'), 'Builder');
  assert.equal(customerTypeWord('GOVERNMENT'), 'Government');
  assert.equal(customerTypeWord('INTERNAL'), 'Internal');
});

test('the map covers every value migration 0006 admits', () => {
  for (const t of CUSTOMER_TYPES_IN_SCHEMA) {
    assert.notEqual(customerTypeWord(t), t, `${t} must be translated, not passed through as the enum`);
    assert.match(customerTypeWord(t), /^[A-Z][a-z]+$/, `${t} must render as one sentence-case word`);
  }
});

test('the words match what the rest of the product already calls these accounts', () => {
 // _pos.ts accountTypeTag and _quote-workarea.ts customerTypeTag have been saying these same five
 // words by title-casing the string. The map must not invent a second vocabulary for one column.
  for (const t of CUSTOMER_TYPES_IN_SCHEMA) {
    assert.equal(customerTypeWord(t), t.charAt(0) + t.slice(1).toLowerCase());
  }
});

test('an unmapped account type degrades HONESTLY: strange, never a confident lie', () => {
  assert.equal(customerTypeWord('MUNICIPAL_ACCOUNT'), 'municipal account');
  assert.notEqual(customerTypeWord('MUNICIPAL_ACCOUNT'), 'Unknown');
  assert.notEqual(customerTypeWord('MUNICIPAL_ACCOUNT'), '');
 // And a blank never becomes a confident 'Contractor': the default price list is not a guess the
 // picker is allowed to make on the reader's behalf.
  assert.notEqual(customerTypeWord(''), 'Contractor');
});
