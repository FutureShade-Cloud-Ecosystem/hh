// The guided Convert to Order surface's pure decisions, on the Node runner.
import { test } from "node:test";
import assert from "node:assert/strict";

import {
  convertRouteQuoteId,
  parsePayAmount,
  refusalWords,
  standingOf,
  stepAfter,
  stepBefore,
  stockWords,
} from "../src/_quote-convert.ts";

test("the route names a quote only on the convert address", () => {
  assert.equal(convertRouteQuoteId("#/quotes/q-1003/convert"), "q-1003");
  assert.equal(convertRouteQuoteId("#/quotes/Q-1003/convert/"), "Q-1003");
  assert.equal(convertRouteQuoteId("#/quotes"), "");
  assert.equal(convertRouteQuoteId("#/quotes/q-1003"), "");
  assert.equal(convertRouteQuoteId("#/quotes/a/b/convert"), "");
  assert.equal(convertRouteQuoteId(""), "");
});

test("the steps walk forward and back and stop at the ends", () => {
  assert.equal(stepAfter("review"), "fulfilment");
  assert.equal(stepAfter("confirm"), "confirm");
  assert.equal(stepBefore("review"), "review");
  assert.equal(stepBefore("payment"), "standing");
});

test("a short line warns that the order waits; an unreported grade is never none", () => {
  assert.equal(stockWords({ grade: "GREEN", qtyBase: 3, availableBase: 9 }, "Trenton").kind, "ok");
  const thin = stockWords({ grade: "THIN", qtyBase: 3, availableBase: 2 }, "Trenton");
  assert.equal(thin.kind, "short");
  assert.match(thin.text, /wait on stock/);
  assert.equal(stockWords({ grade: "NONE", qtyBase: 3, availableBase: 0 }, "").kind, "short");
  assert.equal(stockWords({ grade: "UNKNOWN", qtyBase: 3, availableBase: 0 }, "Trenton").kind, "unknown");
  assert.equal(stockWords(null, "Trenton").kind, "unknown");
});

test("the standing reads terms and whether the credit gate will pass", () => {
  assert.equal(standingOf(null, 100).gate, "unknown");
  const cod = standingOf({ name: "A", paymentTermsDays: 0, creditLimitCents: 0, openBalanceCents: 0 }, 5000);
  assert.equal(cod.terms, "COD");
  assert.equal(cod.gate, "none");
  assert.match(cod.termsText, /due on release/);
  const within = standingOf({ name: "A", paymentTermsDays: 30, creditLimitCents: 500000, openBalanceCents: 125000 }, 10000);
  assert.equal(within.gate, "within");
  assert.match(within.termsText, /30 days/);
  const over = standingOf({ name: "A", paymentTermsDays: 30, creditLimitCents: 100000, openBalanceCents: 95000 }, 10000);
  assert.equal(over.gate, "over");
  assert.match(over.gateText, /over by CA\$/);
  // Landing exactly on the limit is within it.
  assert.equal(standingOf({ name: "A", paymentTermsDays: 30, creditLimitCents: 100000, openBalanceCents: 90000 }, 10000).gate, "within");
});

test("a refusal is plain words with what to do next", () => {
  const credit = refusalWords(409, "quote: credit policy: this account is over its credit limit");
  assert.match(credit, /credit limit/);
  assert.match(credit, /Nothing was created/);
  assert.doesNotMatch(credit, /^quote:/i);
  assert.match(refusalWords(409, "quote: this quote is sent"), /check its status/);
  assert.match(refusalWords(403, ""), /Ask a manager/);
  assert.match(refusalWords(500, ""), /Nothing was created/);
});

test("the payment amount goes through the decimal string to integer cents", () => {
  assert.deepEqual(parsePayAmount("50"), { cents: 5000 });
  assert.deepEqual(parsePayAmount(" 50.5 "), { cents: 5050 });
  assert.deepEqual(parsePayAmount("1.005".slice(0, 4)), { cents: 100 });
  assert.ok("error" in parsePayAmount("fifty"));
  assert.ok("error" in parsePayAmount("1,250"));
  assert.ok("error" in parsePayAmount("0"));
  assert.ok("error" in parsePayAmount("-5"));
  assert.ok("error" in parsePayAmount("1.005"));
});
