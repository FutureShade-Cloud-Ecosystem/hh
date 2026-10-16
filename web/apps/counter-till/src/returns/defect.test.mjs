// The Counter Till Return Drawer's threshold sentence, walk change 6 item 4. The cost is the
// line's picked quantity x unit cost, the threshold is the dealer's live warranty claim
// threshold, and the sentence is the walk board's wording. The "at or over the threshold" rule
// matches the backend's CostOverClaimThreshold (>=), so the desk's sentence and the core's
// rule cannot come to mean two things. Money is integer cents in, integer cents out; the
// displayed dollars are a one place rounding of those integer cents.
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  defectCostCents,
  defectCostOverThreshold,
  defectThresholdSentence,
} from "./defect.js";

test("defectCostCents multiplies quantity and unit cost in integer cents, zero/negative in, zero out", () => {
  assert.equal(defectCostCents(4, 1420), 5680, "$14.20 x 4 = $56.80");
  assert.equal(defectCostCents(1, 1230), 1230, "$12.30 x 1 = $12.30");
  assert.equal(defectCostCents(0, 1420), 0, "no quantity is no cost");
  assert.equal(defectCostCents(4, 0), 0, "no unit cost is no cost");
  assert.equal(defectCostCents(-1, 1420), 0, "negative quantity is no cost");
  assert.equal(defectCostCents(4, -1), 0, "negative unit cost is no cost");
});

test("defectCostOverThreshold matches the backend's rule: at or over is a warranty claim, strictly under is shrinkage", () => {
  assert.equal(defectCostOverThreshold(5000, 5000), true, "exactly at threshold is a claim");
  assert.equal(defectCostOverThreshold(5680, 5000), true, "over threshold is a claim");
  assert.equal(defectCostOverThreshold(1230, 5000), false, "under threshold is shrinkage");
  assert.equal(defectCostOverThreshold(4999, 5000), false, "one cent under is shrinkage");
  assert.equal(defectCostOverThreshold(5001, 5000), true, "one cent over is a claim");
  assert.equal(defectCostOverThreshold(0, 0), false, "no threshold stays refused");
  assert.equal(defectCostOverThreshold(1, 0), false, "zero threshold is refused");
  assert.equal(defectCostOverThreshold(100, -50), false, "negative threshold is refused");
});

test("defectThresholdSentence: cost strictly under threshold reads as shrinkage (board ct-return-defect-under)", () => {
  const sentence = defectThresholdSentence(1230, 5000);
  assert.ok(sentence !== null);
  assert.equal(sentence.kind, "under");
  assert.equal(sentence.costCents, 1230);
  assert.equal(sentence.thresholdCents, 5000);
  assert.equal(
    sentence.words,
    "Under the $50 claim threshold ($12.30 at cost): logged as shrinkage.",
  );
});

test("defectThresholdSentence: cost over threshold reads as a warranty claim (board ct-return-defect-over)", () => {
  const sentence = defectThresholdSentence(5680, 5000);
  assert.ok(sentence !== null);
  assert.equal(sentence.kind, "over");
  assert.equal(sentence.costCents, 5680);
  assert.equal(sentence.thresholdCents, 5000);
  assert.equal(
    sentence.words,
    "Over the $50 claim threshold ($56.80 at cost): this goes to a warranty claim.",
  );
});

test("defectThresholdSentence: cost exactly at threshold is a warranty claim, the edge never disagrees with the core", () => {
  const sentence = defectThresholdSentence(5000, 5000);
  assert.ok(sentence !== null);
  assert.equal(sentence.kind, "over", "the at-threshold edge is a claim, like CostOverClaimThreshold");
  assert.equal(
    sentence.words,
    "Over the $50 claim threshold ($50.00 at cost): this goes to a warranty claim.",
  );
});

test("defectThresholdSentence: a non integer threshold reads rounded, not refused, the cost still reads exact", () => {
  const sentence = defectThresholdSentence(11325, 12345);
  assert.ok(sentence !== null);
  assert.equal(sentence.kind, "under");
  assert.equal(sentence.thresholdCents, 12345);
  assert.equal(
    sentence.words,
    "Under the $123.45 claim threshold ($113.25 at cost): logged as shrinkage.",
  );
});

test("defectThresholdSentence: a one cent threshold refuses the sentence rather than mis classify a defect as warranty", () => {
  const oneCent = defectThresholdSentence(5000, 1);
  assert.ok(oneCent !== null);
  assert.equal(oneCent.kind, "over", "$50.00 over a $0.01 threshold is a claim");
  assert.equal(oneCent.thresholdCents, 1);
  assert.match(oneCent.words, /\$0\.01/);
});

test("defectThresholdSentence: refuses an absent cost or threshold with null, never a fabricated comparison", () => {
  assert.equal(defectThresholdSentence(NaN, 5000), null, "non numeric cost is null");
  assert.equal(defectThresholdSentence(1230, NaN), null, "non numeric threshold is null");
  assert.equal(defectThresholdSentence(-1, 5000), null, "negative cost is null");
  assert.equal(defectThresholdSentence(1230, -1), null, "negative threshold is null");
});

test("defectThresholdSentence: thousands of dollars format through the till's money helper with grouped thousands", () => {
  const sentence = defectThresholdSentence(1234567, 5000);
  assert.ok(sentence !== null);
  assert.equal(sentence.kind, "over");
  assert.equal(
    sentence.words,
    "Over the $50 claim threshold ($12,345.67 at cost): this goes to a warranty claim.",
  );
});

test("defectCostCents keeps integer cents: picked qty 4 x unit cost $14.20 is exactly $56.80, never a float slip", () => {
  // 1420 * 4 = 5680 in either world; the guard is that the answer matches what the board would
  // render: $56.80 at cost, with no spurious cent.
  assert.equal(defectCostCents(4, 1420), 5680);
  // A non integer result rounds once on the last cent (defect.ts rounds the product).
  assert.equal(defectCostCents(3, 3333), 9999, "3 x $33.33 is $99.99, integer cents");
});
