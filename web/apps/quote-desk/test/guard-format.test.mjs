// The Check Quote form's pure words, unit tested on the Node runner: basis
// points spoken as percent, the typed decimal read to integer cents without a
// float, the verdict sentences and the 403 block reader. Ported from the ERP's
// _quote-guard-format test; the local margin arithmetic is not ported, because
// the app never computes a margin (the server answers it).
import { test } from "node:test";
import assert from "node:assert/strict";

import { blockFromText, bpsToPercent, dollarsToCents, guardVerdictWords, humanizeBps, mentionsBps, sentenceBlock } from "../src/_guard-format.js";

test("bpsToPercent speaks basis points the way the counter says them", () => {
  assert.equal(bpsToPercent(1200), "12%");
  assert.equal(bpsToPercent(2250), "22.5%");
  assert.equal(bpsToPercent(625), "6.25%");
  assert.equal(bpsToPercent(-444), "-4.44%");
  assert.equal(bpsToPercent(0), "0%");
  assert.equal(bpsToPercent(Number.NaN), "");
});

test("humanizeBps rewrites every bps quantity and leaves other sentences alone", () => {
  assert.equal(humanizeBps("margin 600 bps is below the 1200 bps floor"), "margin 6% is below the 12% floor");
  assert.equal(humanizeBps("account has no credit; cash or card only (COD)"), "account has no credit; cash or card only (COD)");
  assert.equal(humanizeBps("margin 6% is below the 12% floor"), "margin 6% is below the 12% floor");
  assert.equal(humanizeBps(""), "");
  assert.equal(humanizeBps("code ABbps stays"), "code ABbps stays");
});

test("mentionsBps finds a surviving basis point quantity", () => {
  assert.equal(mentionsBps("margin 600 bps"), true);
  assert.equal(mentionsBps("margin 6%"), false);
  assert.equal(mentionsBps("margin 600 bps"), true, "the pattern holds no state between calls");
});

test("dollarsToCents reads the typed decimal through the string, never a float", () => {
  assert.equal(dollarsToCents("100.00"), 10000);
  assert.equal(dollarsToCents("94"), 9400);
  assert.equal(dollarsToCents("1.005"), null, "a half cent is not a price");
  assert.equal(dollarsToCents("1.1"), 110);
  assert.equal(dollarsToCents("0.07"), 7);
  assert.equal(dollarsToCents(" 12.50 "), 1250);
  assert.equal(dollarsToCents(""), null);
  assert.equal(dollarsToCents("abc"), null);
  assert.equal(dollarsToCents("-3"), null);
  assert.equal(dollarsToCents("1e3"), null);
  assert.equal(dollarsToCents("19.99"), 1999);
});

test("guardVerdictWords says what the server answered, in percent", () => {
  assert.equal(guardVerdictWords({ allowed: true, margin_bps: 2400, override: false }), "Approved. The margin is 24%.");
  assert.equal(
    guardVerdictWords({ allowed: true, margin_bps: 600, override: true }),
    "Approved by an override. The margin is 6%.",
  );
  assert.equal(guardVerdictWords({ allowed: false, margin_bps: -444, override: false }), "Blocked. The margin is -4.44%, below the margin policy.");
});

test("blockFromText reads the block an envelope carries and refuses anything else", () => {
  const body = JSON.stringify({ error: "identity: blocked", block: { policy_type: "MARGIN_BELOW_MINIMUM", detail: "margin 600 bps is below the 1200 bps floor", branch_id: "b1" } });
  assert.deepEqual(blockFromText(body), { policyType: "MARGIN_BELOW_MINIMUM", detail: "margin 6% is below the 12% floor" });
  assert.equal(blockFromText("not json"), null);
  assert.equal(blockFromText(JSON.stringify({ error: "nope" })), null);
  assert.equal(blockFromText(JSON.stringify({ block: { detail: "x" } })), null);
});

test("sentenceBlock reads only the core's margin sentence as a block", () => {
  assert.deepEqual(sentenceBlock("margin -444 bps is below the 1200 bps floor", "MARGIN_BELOW_MINIMUM"), {
    policyType: "MARGIN_BELOW_MINIMUM",
    detail: "margin -4.44% is below the 12% floor",
  });
  // The spoken form the panel hands it is capitalised.
  assert.notEqual(sentenceBlock("Margin 1000 bps is below the 1200 bps floor", "MARGIN_BELOW_MINIMUM"), null);
  // Any other refusal is not a margin block: access, silence, other words.
  assert.equal(sentenceBlock("that branch is outside your grants", "MARGIN_BELOW_MINIMUM"), null);
  assert.equal(sentenceBlock("Forbidden", "MARGIN_BELOW_MINIMUM"), null);
  assert.equal(sentenceBlock("", "MARGIN_BELOW_MINIMUM"), null);
  assert.equal(sentenceBlock("   ", "MARGIN_BELOW_MINIMUM"), null);
});
