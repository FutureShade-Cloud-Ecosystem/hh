// The admin-rest surfaces' pure half, on its own: the assistant's budget and
// spend words (ported from the original app's assist-model.ts), the decimal
// string rule for every amount the server sets, and the refusal words. No
// browser, no network. Red first: this file landed before admin-logic.ts
// existed, and the run below it failed on the missing module (see the report).
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  assistBudgetInputWords,
  assistRefusalWords,
  budgetSentence,
  formatTokens,
  overBudget,
  spendSummary,
  spendTotal,
  usedFraction,
  wholeAmount,
} from "../src/admin-logic.ts";

// -- the amounts: counts or cents set by the server, parsed as decimal strings --

test("a budget is read off the decimal string, never through float arithmetic", () => {
  assert.equal(wholeAmount("60000"), 60000);
  assert.equal(wholeAmount(" 0 "), 0);
  assert.equal(wholeAmount("1200000"), 1200000);
  // Anything that is not a whole count of tokens is refused before a call.
  assert.equal(wholeAmount("60 000"), null);
  assert.equal(wholeAmount("1e6"), null);
  assert.equal(wholeAmount("12.5"), null);
  assert.equal(wholeAmount("-1"), null);
  assert.equal(wholeAmount(""), null);
});

test("the budget box's own refusal names the whole number rule", () => {
  assert.match(assistBudgetInputWords(), /whole number/);
});

// -- the ported budget and spend words (the original's assist-model.ts) ---------------------

test("token counts are grouped the way a person reads them", () => {
  assert.equal(formatTokens(60000), "60,000");
  assert.equal(formatTokens(0), "0");
  assert.equal(formatTokens(1234567), "1,234,567");
});

test("spend totals both directions, because both are billed", () => {
  assert.equal(spendTotal({ tokens_in: 300, tokens_out: 200 }), 500);
  assert.equal(spendTotal(null), 0);
});

test("the boundary is spent reaching the budget, the server's own operator", () => {
  assert.equal(overBudget(60000, 60000), true);
  assert.equal(overBudget(59999, 60000), false);
});

test("a meter's fill clamps, and a zero budget reads full, not empty", () => {
  assert.equal(usedFraction(30000, 60000), 0.5);
  assert.equal(usedFraction(90000, 60000), 1);
  assert.equal(usedFraction(0, 60000), 0);
  assert.equal(usedFraction(0, 0), 1);
});

test("the budget sentence takes its source from the server, never inferred", () => {
  assert.equal(budgetSentence({ tokens: 500000, source: "user" }), "500,000, their own");
  assert.equal(budgetSentence({ tokens: 300000, source: "role" }, "SALES_MANAGER"), "300,000, from SALES_MANAGER");
  assert.equal(budgetSentence({ tokens: 300000, source: "role" }), "300,000, from their role");
  assert.equal(budgetSentence({ tokens: 60000, source: "default" }), "60,000, the default");
  assert.equal(budgetSentence(null), "No budget resolved");
});

test("the spend line leads with the questions, the stub costs zero tokens", () => {
  assert.equal(spendSummary({ exchanges: 0, tokens_in: 0, tokens_out: 0 }), "No questions yet");
  assert.equal(spendSummary({ exchanges: 4, tokens_in: 0, tokens_out: 0 }), "4 questions, no AI tokens used");
  assert.equal(spendSummary({ exchanges: 1, tokens_in: 0, tokens_out: 0 }), "1 question, no AI tokens used");
  assert.equal(spendSummary({ exchanges: 4, tokens_in: 300, tokens_out: 200 }), "4 questions, 500 tokens");
});

// -- the refusals: the core's gates, named -----------------------------------------------

test("the assist admin refusal names leadership, and other statuses pass the server's words", () => {
  const words = assistRefusalWords("Forbidden", 403);
  assert.match(words, /leadership/);
  assert.match(words, /assistant/);
  assert.equal(assistRefusalWords("not found", 404), "not found");
  assert.equal(assistRefusalWords("", undefined), "Try again in a moment.");
});
