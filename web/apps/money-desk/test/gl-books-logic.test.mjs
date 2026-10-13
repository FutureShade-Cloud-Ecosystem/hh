// The books' plain logic: the amount a person types, the journal form's own checks, the refusal words of
// each write, and the period and check words. No money is computed here; the cases hold the words and the
// parse, and that a lost answer is told apart from a refusal.
import { test } from "node:test";
import assert from "node:assert/strict";
import { parseAmountToCents } from "../src/gl/gl-money.ts";
import {
  accountCodeOf,
  accountTypeWord,
  checkDraft,
  monthWord,
  outcomeUnknown,
  periodName,
  refusalWords,
  suggestedSide,
  tieWords,
  unknownWords,
} from "../src/gl/gl-logic.ts";

test("an amount is read through the decimal string, never a float multiply", () => {
  assert.equal(parseAmountToCents("1.005"), null);
  assert.equal(parseAmountToCents("19.68"), 1968);
  assert.equal(parseAmountToCents("$20,000.00"), 2000000);
  assert.equal(parseAmountToCents("0.1"), 10);
  assert.equal(parseAmountToCents("-5"), null);
  assert.equal(parseAmountToCents("1,23"), null);
  assert.equal(parseAmountToCents(""), null);
  assert.equal(parseAmountToCents("1234567890"), null);
});

test("an account choice names its code before the middot", () => {
  assert.equal(accountCodeOf("1000 · Cash"), "1000");
  assert.equal(accountCodeOf(""), "");
});

const line = (account, debit, credit, memo = "") => ({ account, debit, credit, memo });

test("the form checks a memo, two lines, an account and exactly one amount on each, and sends integer cents", () => {
  assert.match(checkDraft("", [line("1000 · Cash", "1", ""), line("5200 · X", "", "1")], parseAmountToCents).error, /memo/);
  assert.match(checkDraft("m", [line("1000 · Cash", "1", "")], parseAmountToCents).error, /at least two lines/);
  assert.match(checkDraft("m", [line("", "1", ""), line("5200 · X", "", "1")], parseAmountToCents).error, /Line 1: choose an account/);
  assert.match(checkDraft("m", [line("1000 · Cash", "1.234", ""), line("5200 · X", "", "1")], parseAmountToCents).error, /Line 1: the debit is not an amount/);
  assert.match(checkDraft("m", [line("1000 · Cash", "1", "1"), line("5200 · X", "", "1")], parseAmountToCents).error, /exactly one/);
  assert.match(checkDraft("m", [line("1000 · Cash", "0", ""), line("5200 · X", "", "1")], parseAmountToCents).error, /exactly one/);
  const ok = checkDraft(" m ", [line("1000 · Cash", "12.50", "", " a "), line("5200 · X", "", "12.50")], parseAmountToCents);
  assert.equal(ok.ok, true);
  assert.deepEqual(ok.lines, [
    { account_code: "1000", debit_cents: 1250, credit_cents: 0, memo: "a" },
    { account_code: "5200", debit_cents: 0, credit_cents: 1250, memo: "" },
  ]);
});

test("a lost answer is told apart from a refusal, and a refusal loses the module prefix", () => {
  assert.equal(outcomeUnknown(0), true);
  assert.equal(outcomeUnknown(500), true);
  assert.equal(outcomeUnknown(409), false);
  assert.equal(outcomeUnknown(503), false);
  assert.match(refusalWords("post", "", 0), /may or may not have been saved/);
  assert.match(unknownWords("close-period"), /close a period/);
  assert.equal(refusalWords("account", "gl: account code \"6100\" already exists", 409), "Account code \"6100\" already exists.");
});

test("each write's gate refusal names who may, the maker's four eyes refusal says so, and the closed month has its way out", () => {
  assert.match(refusalWords("reopen", "Forbidden", 403), /for the Financial Controller\. Sign out/);
  assert.match(refusalWords("post", "Forbidden", 403), /Financial Controller or the General Manager/);
  assert.match(refusalWords("approve-journal", "gl: an approver must differ from the journal's creator (four-eyes)", 403), /You made this journal/);
  assert.match(refusalWords("post", "gl: fiscal period is CLOSED", 409), /reopen it with a reason/);
  assert.match(refusalWords("post", "gl: journal is unbalanced (x)", 422), /Debits and credits are not equal/);
  assert.doesNotMatch(refusalWords("export", "Forbidden", 403), /Forbidden\./);
});

test("periods, checks and accounts read in words", () => {
  assert.equal(periodName({ year: 2026, month: 9 }), "September 2026");
  assert.equal(monthWord(13), "13");
  assert.equal(tieWords(true, 0), "In tie");
  assert.equal(tieWords(false, 0), "Out of tie");
  assert.equal(tieWords(false, 1), "Out of tie for 1 day");
  assert.equal(accountTypeWord("REVENUE"), "Revenue");
  assert.equal(suggestedSide("EXPENSE"), "DEBIT");
  assert.equal(suggestedSide("EQUITY"), "CREDIT");
});
