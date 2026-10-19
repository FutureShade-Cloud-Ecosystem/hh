// The Warranty surface's pure decisions, on their own: the words the cards
// say (the kinds and periods the boards name), the threshold's dollars
// parse, the fixed allowance's percent parse, the kind switch's payload
// defaults, and the refusal words. No browser, no network. Red first: this
// file landed before warranty-logic.ts existed, and the run below it failed
// on the missing module (see the report).
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  AGREEMENT_KINDS,
  allowanceFault,
  allowanceWords,
  kindWord,
  nextAgreementFor,
  parseAllowancePct,
  parseThresholdDollars,
  periodWord,
  thresholdFault,
  warrantyRefusalWords,
} from "../src/warranty-logic.ts";

test("the three kinds carry the boards' words, and an unknown degrades to itself", () => {
  assert.deepEqual([...AGREEMENT_KINDS], ["claim_by_claim", "periodic_credit_note", "fixed_allowance"]);
  assert.equal(kindWord("claim_by_claim"), "Claim by claim");
  assert.equal(kindWord("periodic_credit_note"), "Periodic credit note");
  assert.equal(kindWord("fixed_allowance"), "Fixed allowance");
  assert.equal(kindWord("yearly_cheque"), "yearly_cheque");
});

test("the periods carry the boards' words", () => {
  assert.equal(periodWord("monthly"), "Monthly");
  assert.equal(periodWord("quarterly"), "Quarterly");
});

test("the threshold parses the dollars a person typed, the dollar sign included", () => {
  assert.equal(parseThresholdDollars("$50.00"), 5000);
  assert.equal(parseThresholdDollars("50"), 5000);
  assert.equal(parseThresholdDollars("$50"), 5000);
  assert.equal(parseThresholdDollars("49.99"), 4999);
  assert.equal(parseThresholdDollars("0"), 0);
  assert.equal(parseThresholdDollars(""), null);
  assert.equal(parseThresholdDollars("-1"), null);
  assert.equal(parseThresholdDollars("1.005"), null);
  assert.equal(parseThresholdDollars("1e3"), null);
  assert.equal(parseThresholdDollars("abc"), null);
  assert.equal(parseThresholdDollars("US$5"), null);
});

test("the threshold's fault names the rule before the write", () => {
  assert.equal(thresholdFault(5000), "");
  assert.equal(thresholdFault(0), "");
  assert.match(thresholdFault(-1), /zero or more/);
});

test("the fixed allowance reads as a percent, and parses back to basis points", () => {
  assert.equal(allowanceWords(100), "1%");
  assert.equal(allowanceWords(125), "1.25%");
  assert.equal(allowanceWords(0), "0%");
  assert.equal(allowanceWords(10000), "100%");
  assert.equal(parseAllowancePct("1%"), 100);
  assert.equal(parseAllowancePct("1"), 100);
  assert.equal(parseAllowancePct("1.25%"), 125);
  assert.equal(parseAllowancePct("0.5"), 50);
  assert.equal(parseAllowancePct("100%"), 10000);
  assert.equal(parseAllowancePct(""), null);
  assert.equal(parseAllowancePct("-1"), null);
  assert.equal(parseAllowancePct("1.234%"), null);
  assert.equal(parseAllowancePct("1 of purchases"), null);
});

test("the allowance's fault names the cap before the write", () => {
  assert.equal(allowanceFault(100), "");
  assert.match(allowanceFault(10001), /10000/);
  assert.match(allowanceFault(-5), /zero or more/);
});

test("switching kinds lands on a sane payload without a second click", () => {
  const row = { agreement_kind: "claim_by_claim", period: undefined, allowance_bp: 0 };
  assert.deepEqual(nextAgreementFor(row, "periodic_credit_note"), {
    agreement_kind: "periodic_credit_note",
    period: "monthly",
    allowance_bp: 0,
  });
  assert.deepEqual(nextAgreementFor(row, "fixed_allowance"), {
    agreement_kind: "fixed_allowance",
    period: "quarterly",
    allowance_bp: 100,
  });
  assert.deepEqual(nextAgreementFor(row, "claim_by_claim"), {
    agreement_kind: "claim_by_claim",
    period: undefined,
    allowance_bp: 0,
  });
  // A row that already carries a period keeps it across a kind change.
  const quarterly = { agreement_kind: "periodic_credit_note", period: "quarterly", allowance_bp: 0 };
  assert.equal(nextAgreementFor(quarterly, "fixed_allowance").period, "quarterly");
  // A fixed allowance row's percent survives a visit to periodic and back.
  const fixed = { agreement_kind: "fixed_allowance", period: "monthly", allowance_bp: 150 };
  assert.equal(nextAgreementFor(fixed, "fixed_allowance").allowance_bp, 150);
});

test("a refusal names who may act, and the core's own sentence passes whole", () => {
  assert.match(warrantyRefusalWords("Forbidden", 403), /dealer's administrators/);
  assert.equal(warrantyRefusalWords('warranty: unknown vendor "x"', 422), 'warranty: unknown vendor "x"');
  assert.equal(warrantyRefusalWords("", undefined), "Try again in a moment.");
});
