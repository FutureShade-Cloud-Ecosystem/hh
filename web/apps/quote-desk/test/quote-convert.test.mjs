// The guided Convert to Order surface's pure decisions, on the Node runner.
import { test } from "node:test";
import assert from "node:assert/strict";

import {
  awayNotice,
  codRefusalWords,
  convertRouteQuoteId,
  isCodRefusal,
  paymentKeyFor,
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

test("the standing states the server's facts and does no arithmetic of its own", () => {
  const unknown = standingOf(null, null);
  assert.equal(unknown.terms, "UNKNOWN");
  assert.match(unknown.gateText, /server/);
  const cod = standingOf({ name: "A", paymentTermsDays: 0, creditLimitCents: 0, openBalanceCents: 0 }, null);
  assert.equal(cod.terms, "COD");
  assert.equal(cod.termsText, "Cash On Demand: nothing is released until it is paid.");
  assert.doesNotMatch(cod.termsText + cod.gateText, /cash on delivery|due on release|no credit check/i);
  const account = standingOf({ name: "A", paymentTermsDays: 30, creditLimitCents: 500000, openBalanceCents: 125000 }, null);
  assert.equal(account.terms, "ACCOUNT");
  assert.match(account.termsText, /30 days/);
  assert.match(account.gateText, /CA\$5,000\.00/);
  assert.match(account.gateText, /CA\$1,250\.00/);
  assert.doesNotMatch(account.gateText, /within|over by/);
  // An account at its limit is stated, never judged: the server decides at confirm.
  const full = standingOf({ name: "A", paymentTermsDays: 30, creditLimitCents: 100000, openBalanceCents: 95000 }, null);
  assert.doesNotMatch(full.gateText, /within|over by/);
  const held = standingOf({ name: "A", paymentTermsDays: 30, creditLimitCents: 100000, openBalanceCents: 0 }, { onHold: true, pastDueCents: 12000, oldestBucket: "60" });
  assert.equal(held.gate, "hold");
  assert.match(held.gateText, /credit hold/);
  assert.match(held.gateText, /CA\$120\.00/);
  // No hold on file is not a promise that there is none: the read cannot tell the two apart.
  const quiet = standingOf({ name: "A", paymentTermsDays: 30, creditLimitCents: 100000, openBalanceCents: 0 }, { onHold: false, pastDueCents: null, oldestBucket: null });
  assert.equal(quiet.gate, "server");
  assert.doesNotMatch(quiet.gateText, /no hold|clear/i);
});

test("a Cash On Demand refusal is said in the server's words with no credit advice", () => {
  const door = codRefusalWords(409, "quote: this quote is cancelled; only a converted quote's order takes a payment");
  assert.match(door, /only a converted quote's order takes a payment/);
  assert.match(door, /Nothing was created and nothing was taken/);
  assert.doesNotMatch(door, /credit hold|settle the account|manager release/i);
  assert.match(codRefusalWords(400, "a customer with no credit account pays the whole agreed total of $10.00"), /whole agreed total/);
  // No answer at all: the action may or may not have happened, and trying again is safe.
  const lost = codRefusalWords(undefined, "network down");
  assert.match(lost, /could not confirm/i);
  assert.match(lost, /same payment/i);
  assert.doesNotMatch(lost, /Nothing was created/);
});

test("the real gate's COD refusal is told apart from a credit one", () => {
  assert.equal(isCodRefusal("this customer cannot buy on account right now (account has no credit; cash or card only (COD)), the quote is unchanged"), true);
  assert.equal(isCodRefusal("this order is $9.00 against a $5.00 credit limit with $0.00 already owing"), false);
  assert.equal(isCodRefusal(""), false);
});

test("the idempotency key follows the intended payment", () => {
  let n = 0;
  const mint = () => `key-${++n}`;
  const fifty = { method: "CASH", amountCents: 5000, reference: "till 2" };
  const first = paymentKeyFor(null, fifty, mint);
  assert.equal(first.key, "key-1");
  // The same payment again keeps its key, so a replay posts nothing twice.
  assert.equal(paymentKeyFor(first, { ...fifty }, mint).key, "key-1");
  // Any change of what is intended is a new payment with a new key.
  assert.equal(paymentKeyFor(first, { ...fifty, method: "CARD" }, mint).key, "key-2");
  assert.equal(paymentKeyFor(first, { ...fifty, amountCents: 6000 }, mint).key, "key-3");
  assert.equal(paymentKeyFor(first, { ...fifty, reference: "" }, mint).key, "key-4");
});

test("a payment settled after the person left is said in plain words", () => {
  const taken = awayNotice({ orderNo: "SO-1", quoteNo: "Q-7", amountCents: 5000, paid: { paymentNo: "PAY-3" } });
  assert.match(taken, /SO-1/);
  assert.match(taken, /payment of CA\$50\.00 was taken/);
  const not = awayNotice({ orderNo: "SO-1", quoteNo: "Q-7", amountCents: 5000, paid: null, said: "the payments desk is closed" });
  assert.match(not, /was not taken/);
  assert.match(not, /Nothing was charged/);
  assert.match(not, /the payments desk is closed/);
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

test("a Cash On Demand refusal says to reload when the quote may have changed", () => {
  assert.match(codRefusalWords(400, "a customer with no credit account pays the whole agreed total of $10.00"), /reload/i);
  assert.match(codRefusalWords(409, "convert this quote to an order first"), /reload/i);
});
