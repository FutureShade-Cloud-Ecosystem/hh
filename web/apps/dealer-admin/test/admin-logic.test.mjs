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
  keysWaitWords,
  keysValueWords,
  listPriceWords,
  messagesEmptyWords,
  messagesRefusalWords,
  messageKindWords,
  overBudget,
  printDocLabel,
  printEmptyWords,
  printStatusWords,
  printTargetWords,
  productChangedPatch,
  productDraftFrom,
  productRefusalWords,
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

// -- the print queue: one document's history --------------------------------------------------

test("the print doc types read as the papers they are, and degrade honestly", () => {
  assert.equal(printDocLabel("RECEIPT"), "Receipt");
  assert.equal(printDocLabel("LABEL_ZPL"), "Label (ZPL)");
  assert.equal(printDocLabel("DELIVERY_DOC"), "Delivery document");
  assert.equal(printDocLabel("STRANGE_TYPE"), "strange type");
});

test("a job's status and render target read as words, never a stored enum", () => {
  assert.equal(printStatusWords("QUEUED"), "Queued");
  assert.equal(printStatusWords("PRINTED"), "Printed");
  assert.equal(printStatusWords("FAILED"), "Failed");
  assert.equal(printStatusWords("ELSE"), "else");
  assert.equal(printTargetWords("BROWSER"), "Browser");
  assert.equal(printTargetWords("ZPL"), "ZPL");
  assert.equal(printTargetWords(""), "");
});

test("an empty print history says the document has printed nothing yet, not that nothing happened", () => {
  const words = printEmptyWords();
  assert.match(words, /No print jobs/);
  assert.match(words, /prints/);
});

// -- the exports: one call, one file, no queue ------------------------------------------------

test("a message's kind reads as a word, and the export's empty is a real answer", () => {
  assert.equal(messageKindWords("TEXT"), "Text");
  assert.equal(messageKindWords("EVENT"), "Event");
  assert.equal(messageKindWords("APPROVAL"), "Approval");
  assert.equal(messageKindWords("ELSE"), "else");
  const words = messagesEmptyWords();
  assert.match(words, /No messages/);
  assert.match(words, /export/);
});

test("the messages export refusal names leadership; other statuses pass through", () => {
  const words = messagesRefusalWords("Forbidden", 403);
  assert.match(words, /leadership/);
  assert.match(words, /messages/);
  assert.equal(messagesRefusalWords("until must be an RFC3339 timestamp", 400), "until must be an RFC3339 timestamp");
});

// -- the catalog: the product's words and its picture ---------------------------------------------

const PRODUCT = {
  id: "3a000000-0000-4000-9000-000000000001",
  sku: "PAV-60-CH",
  name: "Charcoal 60 paver",
  description: "A 60 mm paver in charcoal.",
  manufacturer: "Techo-Bloc",
  collection: "Blu",
  color: "Charcoal",
  color_family: "Grey",
  finish: "smooth",
  application: "patios and walkways",
  texture: "",
  product_size: "60 mm",
  sold_by: "SF",
  base_uom: "SF",
  dimensions_lwh: "",
  is_active: true,
  minimum_order_qty: "120",
  list_price_cents: 488,
  list_price_uom: "SF",
};

test("the editor's draft carries the words the row holds, empties for absent ones", () => {
  const draft = productDraftFrom(PRODUCT);
  assert.equal(draft.name, "Charcoal 60 paver");
  assert.equal(draft.manufacturer, "Techo-Bloc");
  assert.equal(draft.texture, "");
});

test("the save sends only what changed, as the contract's partial map", () => {
  const draft = { ...productDraftFrom(PRODUCT), description: "A 60 mm paver, charcoal, for patios.", color: "" };
  const patch = productChangedPatch(draft, PRODUCT);
  assert.deepEqual(Object.keys(patch).sort(), ["color", "description"]);
  assert.equal(patch.description, "A 60 mm paver, charcoal, for patios.");
  assert.equal(patch.color, "");
});

test("an untouched draft sends nothing at all", () => {
  const patch = productChangedPatch(productDraftFrom(PRODUCT), PRODUCT);
  assert.deepEqual(patch, {});
});

test("the list price reads through the shared formatter, and absence is not zero", () => {
  assert.equal(listPriceWords(488, "SF"), "$4.88 per SF");
  assert.equal(listPriceWords(123400, ""), "$1,234.00");
  assert.equal(listPriceWords(null, "SF"), "No list price set");
  assert.equal(listPriceWords(undefined, ""), "No list price set");
  // A zero that the server actually sent is a zero, not a missing price.
  assert.equal(listPriceWords(0, "EA"), "$0.00 per EA");
});

test("the product write's refusal names the managers; the column refusal passes through", () => {
  assert.match(productRefusalWords("Forbidden", 403), /managers and above/);
  assert.equal(
    productRefusalWords('update product: column "bogus" is not editable', 400),
    'update product: column "bogus" is not editable',
  );
});

// -- the platform keys: the read only roster --------------------------------------------------

test("a key's floor and override are the server's own data, said as it came", () => {
  assert.equal(keysValueWords({ launcher: true }), '{"launcher":true}');
  assert.equal(keysValueWords(null), "not set");
  assert.equal(keysValueWords(undefined), "not set");
});

test("the waits words name what is missing and never a word of the build", () => {
  const words = keysWaitWords();
  assert.match(words, /not served here yet/);
  assert.match(words, /operator tools/);
  assert.doesNotMatch(words, /lane|ledger|parity|auth/i);
});
