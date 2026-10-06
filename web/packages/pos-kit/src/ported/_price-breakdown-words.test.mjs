// The below-floor sentence's table: the standing words, the server's own
// detail, and the two refusals that must paint the same base words (the
// figures withheld from the data, the flag cleared). Pinned here so the
// chip's render cannot drift from the policy gate that feeds it.
import test from 'node:test';
import assert from 'node:assert/strict';
import { belowFloorWords } from './_price-breakdown-words.ts';

const FULL = { detail: '', margin_bps: 8150, floor_bps: 1000 };

test('belowFloorWords: no detail paints the standing sentence', () => {
  assert.equal(
    belowFloorWords(FULL, true),
    'Below the configured margin floor: manager approval required (margin 81.50% < floor 10.00%)',
  );
});

test('belowFloorWords: the server\'s own detail leads when it gives one', () => {
  assert.equal(
    belowFloorWords({ ...FULL, detail: 'Rule price sits under the floor' }, true),
    'Rule price sits under the floor (margin 81.50% < floor 10.00%)',
  );
});

test('belowFloorWords: figures withheld from the data paint the base words alone', () => {
  const withheld = { detail: FULL.detail, margin_bps: undefined, floor_bps: undefined };
  assert.equal(
    belowFloorWords(withheld, true),
    'Below the configured margin floor: manager approval required',
  );
});

test('belowFloorWords: the flag cleared paints the same base words as withheld data', () => {
  assert.equal(belowFloorWords(FULL, false), belowFloorWords(
    { detail: FULL.detail, margin_bps: undefined, floor_bps: undefined },
    true,
  ));
});

test('belowFloorWords: one figure present is still a refusal, never a half pair', () => {
  assert.equal(
    belowFloorWords({ ...FULL, floor_bps: undefined }, true),
    'Below the configured margin floor: manager approval required',
  );
  assert.equal(
    belowFloorWords({ ...FULL, margin_bps: undefined }, true),
    'Below the configured margin floor: manager approval required',
  );
});
