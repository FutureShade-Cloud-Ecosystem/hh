// Ported from hardscapeos_dibbits master 87d7941b app/src/_date.test.mjs; adapted for HH.

// DIB-200 compact-date-entry regression. Locks parseCompactDate: the counter clerk types the
// requested date as one 8-digit run (YYYYMMDD) instead of tabbing year→month→day, and it lands
// as the ISO YYYY-MM-DD the sale body carries. Runs on the Node built-in test runner (no deps),
// importing the.ts source through the loader:
// node --import./src/_ts-js-loader.mjs --test 'src/**/*.test.mjs' (run from app/)
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { parseCompactDate } from './_date.ts';

test('compact 8-digit YYYYMMDD → ISO (the DIB-200 ask): 20311021 → 2031-10-21', () => {
  assert.equal(parseCompactDate('20311021'), '2031-10-21');
  assert.equal(parseCompactDate('20310101'), '2031-01-01');
  assert.equal(parseCompactDate('20311231'), '2031-12-31');
});

test('already-ISO passes through unchanged (the native date picker emits ISO)', () => {
  assert.equal(parseCompactDate('2031-10-21'), '2031-10-21');
  assert.equal(parseCompactDate('2032-02-29'), '2032-02-29'); // real leap day
});

test('surrounding whitespace and typed separators are tolerated', () => {
  assert.equal(parseCompactDate('  20311021  '), '2031-10-21');
  assert.equal(parseCompactDate('2031/10/21'), '2031-10-21');
  assert.equal(parseCompactDate(' 2031-10-21 '), '2031-10-21');
});

test('impossible calendar dates → null (compact and ISO)', () => {
  assert.equal(parseCompactDate('20311301'), null); // month 13
  assert.equal(parseCompactDate('20310230'), null); // Feb 30
  assert.equal(parseCompactDate('20250229'), null); // 2025 is not a leap year
  assert.equal(parseCompactDate('2031-13-01'), null);
  assert.equal(parseCompactDate('2031-02-30'), null);
  assert.equal(parseCompactDate('20310000'), null); // month 0 / day 0
});

test('wrong length / non-numeric / empty → null', () => {
  assert.equal(parseCompactDate('1021'), null); // too short
  assert.equal(parseCompactDate('203110211'), null); // 9 digits
  assert.equal(parseCompactDate('2031102'), null); // 7 digits
  assert.equal(parseCompactDate('hello'), null);
  assert.equal(parseCompactDate(''), null);
  assert.equal(parseCompactDate('   '), null);
});
