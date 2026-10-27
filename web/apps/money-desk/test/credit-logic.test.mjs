// The credit memo form's own words (FW1-12, round 2): what the credit does to the account or to
// the invoice, and what the approval rail will do to it.
//
// These are the sentences a manager reads BEFORE the press. The visual read found that the form
// said only "On the account" and left the two field labels to a screen reader, so nobody could
// tell what the credit would do to a balance, or that a credit at or over the approval threshold
// moves no money until it is approved.
//
// No money is computed here. The cents come in already read, and the words go out.
import { test } from "node:test";
import assert from "node:assert/strict";
import { creditApprovalWords, creditTargetWords } from "../src/ar/ar-logic.ts";

test("the account credit says what it does to the account, and names no invoice", () => {
  const words = creditTargetWords(null, null);
  assert.match(words, /On the account/);
  assert.match(words, /lowers what this customer owes/);
  assert.doesNotMatch(words, /INV-/);
});

test("an invoice credit names that invoice and the ROOM it has left, before tax and with tax", () => {
  // The room is the core's: what the invoice has left to credit, before tax, and the same room with
  // tax. The balance with tax ($4,825.00) is what is OWED, not what may be credited: it was the
  // figure the desk used to show, and on a paid invoice it was $0.00 while the invoice still had room.
  const words = creditTargetWords(
    { invoice_no: "INV-1001", balance_cents: 482500 },
    { remainingSubtotalCents: 426991, remainingTotalCents: 482500 },
  );
  assert.match(words, /Against invoice INV-1001/);
  assert.match(words, /up to \$4,269\.91 before tax/);
  assert.match(words, /\$4,825\.00 with tax/);
  assert.doesNotMatch(words, /up to \$4,825\.00\b/);
});

test("an invoice credit whose room the core has not answered names no figure", () => {
  // The desk never guesses a cap: an unanswered quote says no figure rather than a wrong one.
  const words = creditTargetWords({ invoice_no: "INV-1001" }, null);
  assert.match(words, /Against invoice INV-1001/);
  assert.doesNotMatch(words, /up to \$/);
});

test("a credit at or over the threshold says it waits for a second pair of eyes and moves no money", () => {
  const words = creditApprovalWords(500000, 600000);
  assert.match(words, /at or over \$5,000\.00/);
  assert.match(words, /second pair of eyes/);
  assert.match(words, /moves no money/);
});

test("a credit under the threshold still says what the threshold is, and that tax counts", () => {
  const words = creditApprovalWords(500000, 10000);
  assert.match(words, /\$5,000\.00 or more/);
  assert.match(words, /tax included/);
  assert.match(words, /moves no money/);
});

test("an unknown threshold, and no rail at all, say nothing about approval", () => {
  // A threshold the desk could not read is not a threshold: the desk says nothing rather than guess.
  assert.equal(creditApprovalWords(null, 600000), "");
  // Zero is gl's "no rail": every credit posts, so there is nothing to wait for.
  assert.equal(creditApprovalWords(0, 600000), "");
});
