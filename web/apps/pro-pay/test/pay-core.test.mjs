// The money logic of Pro Pay, in plain node with a scripted transport: the fee words, the payable sum, the aging, how a
// pay answer is read, and the sender that keeps one attempt to one key. Nothing here dials a server; the stub legs are in
// pay-stub.test.mjs and the browser legs in pro-pay.pw.ts.
import { test } from "node:test";
import assert from "node:assert/strict";
import { ApiClient } from "@hardscapehouse/api-client";
import {
  payableCents,
  isPayable,
  daysPastDue,
  agingBucket,
  agingTotals,
  feeCents,
  chargeFor,
  classifyPayment,
  PaySender,
  readHostedToken,
} from "../src/pay-core.ts";

const A = "11111111-1111-4111-8111-111111111111";
const B = "22222222-2222-4222-8222-222222222222";
const METHOD = "33333333-3333-4333-8333-333333333333";

// Days are built from today, never written as calendar dates: `day(-9)` is the dealer day nine days ago.
const TODAY = new Date().toISOString().slice(0, 10);
function day(offset) {
  const when = new Date(`${TODAY}T00:00:00Z`);
  when.setUTCDate(when.getUTCDate() + offset);
  return when.toISOString().slice(0, 10);
}

const PAYMENT = { id: "p1", payment_no: "PMT-1", customer_id: "c", method: "CARD", amount_cents: 5000, status: "RECEIVED", gateway_ref: "r", received_at: new Date().toISOString(), allocations: [] };

function reply(status, body) {
  return { status, ok: status >= 200 && status < 300, text: async () => JSON.stringify(body), arrayBuffer: async () => new ArrayBuffer(0) };
}

/** A transport that records every request and answers from a script (a function per call, or a value). */
function transport(script) {
  const calls = [];
  const fetch = async (url, init) => {
    calls.push({ url, method: init.method, body: init.body === undefined ? undefined : JSON.parse(init.body) });
    const step = script[Math.min(calls.length - 1, script.length - 1)];
    return typeof step === "function" ? step(init) : step;
  };
  return { calls, client: new ApiClient({ baseUrl: "http://x.invalid", fetch }) };
}

const body = (extra = {}) => ({ invoiceIds: [A], methodId: METHOD, ...extra });

test("the payable sum leaves out the retained holdback, never below zero", () => {
  assert.equal(payableCents({ balance_cents: 1000, holdback_cents: 200 }), 800);
  assert.equal(payableCents({ balance_cents: 100, holdback_cents: 500 }), 0);
  assert.equal(payableCents({ balance_cents: 100, holdback_cents: 0 }), 100);
});

test("only an open invoice with something payable can be picked", () => {
  assert.equal(isPayable({ status: "FINALIZED", balance_cents: 10, holdback_cents: 0 }), true);
  assert.equal(isPayable({ status: "PARTIALLY_PAID", balance_cents: 10, holdback_cents: 0 }), true);
  assert.equal(isPayable({ status: "PAID", balance_cents: 0, holdback_cents: 0 }), false);
  assert.equal(isPayable({ status: "DRAFT", balance_cents: 10, holdback_cents: 0 }), false);
  assert.equal(isPayable({ status: "VOID", balance_cents: 10, holdback_cents: 0 }), false);
  assert.equal(isPayable({ status: "FINALIZED", balance_cents: 50, holdback_cents: 50 }), false);
});

test("aging counts whole calendar days past the due day and files them in the board's four buckets", () => {
  assert.equal(daysPastDue(`${day(0)}T00:00:00Z`, day(0)), 0);
  assert.equal(daysPastDue(day(10), day(0)), 0);
  assert.equal(daysPastDue(`${day(-9)}T00:00:00Z`, day(0)), 9);
  assert.equal(daysPastDue(day(-31), day(0)), 31);
  assert.equal(agingBucket(0), "current");
  assert.equal(agingBucket(1), "1-30");
  assert.equal(agingBucket(30), "1-30");
  assert.equal(agingBucket(31), "31-60");
  assert.equal(agingBucket(60), "31-60");
  assert.equal(agingBucket(61), "60+");
});

test("the aging tiles sum what is open, by bucket", () => {
  const rows = [
    { status: "FINALIZED", balance_cents: 100, due_date: day(10) },
    { status: "FINALIZED", balance_cents: 200, due_date: day(-9) },
    { status: "PARTIALLY_PAID", balance_cents: 400, due_date: day(-70) },
    { status: "PAID", balance_cents: 0, due_date: day(-200) },
  ];
  const totals = agingTotals(rows, day(0));
  assert.deepEqual(totals.buckets, { current: 100, "1-30": 200, "31-60": 0, "60+": 400 });
  assert.equal(totals.overdueCents, 600);
  assert.equal(totals.overdueCount, 2);
});

test("every method shows its fee before it is chosen, and the core adds none", () => {
  for (const kind of ["CARD", "BANK"]) {
    assert.equal(feeCents(kind, 123456), 0);
    assert.deepEqual(chargeFor(kind, 123456), { feeCents: 0, totalCents: 123456, feeWords: "No fee" });
  }
});

test("a pay answer is read as paid, declined, refused or unknown, and a timeout is never a decline", () => {
  assert.equal(classifyPayment({ ok: true, value: PAYMENT }).kind, "paid");
  assert.equal(classifyPayment({ ok: false, error: "payment declined", status: 402 }).kind, "declined");
  for (const status of [400, 401, 403, 404]) assert.equal(classifyPayment({ ok: false, error: "no", status }).kind, "refused", String(status));
  for (const status of [409, 500, 502, 503]) assert.equal(classifyPayment({ ok: false, error: "x", status }).kind, "unknown", String(status));
  assert.equal(classifyPayment({ ok: false, error: "the request timed out" }).kind, "unknown");
});

test("a retry of an unknown outcome carries the same key and the same body, and never a second key", async () => {
  const t = transport([() => { throw new Error("socket closed"); }, reply(201, PAYMENT)]);
  const sender = new PaySender(t.client, { canPay: () => true });
  const first = await sender.pay(body(), 5000);
  assert.equal(first.kind, "unknown");
  assert.ok(sender.waiting, "the attempt is held as waiting");
  const second = await sender.pay(body(), 5000);
  assert.equal(second.kind, "paid");
  assert.equal(t.calls.length, 2);
  assert.equal(typeof t.calls[0].body.idempotency_key, "string");
  assert.ok(t.calls[0].body.idempotency_key.length >= 16);
  assert.deepEqual(t.calls[1].body, t.calls[0].body);
  assert.equal(sender.waiting, null);
});

test("a held attempt refuses a changed selection, a changed funding source or a changed expected total, with no request", async () => {
  const t = transport([reply(409, { error: "pending" }), reply(201, PAYMENT)]);
  const sender = new PaySender(t.client, { canPay: () => true });
  assert.equal((await sender.pay(body(), 5000)).kind, "unknown");
  for (const [changed, total] of [[body({ invoiceIds: [A, B] }), 9000], [body({ methodId: undefined, nonce: "n-1" }), 5000], [body(), 5100]]) {
    const refused = await sender.pay(changed, total);
    assert.equal(refused.kind, "blocked");
    assert.match(refused.words, /still waiting for an answer/);
  }
  assert.equal(t.calls.length, 1, "no second request went out");
  assert.ok(sender.waiting, "the held attempt is untouched");
});

test("a double press while the send is in flight is one request and the same answer", async () => {
  let release;
  const gate = new Promise((resolve) => (release = resolve));
  const t = transport([async () => { await gate; return reply(201, PAYMENT); }]);
  const sender = new PaySender(t.client, { canPay: () => true });
  const one = sender.pay(body(), 5000);
  const two = sender.pay(body(), 5000);
  const three = sender.pay(body({ invoiceIds: [B] }), 700);
  release();
  const [a, b, c] = await Promise.all([one, two, three]);
  assert.equal(t.calls.length, 1);
  assert.equal(a.kind, "paid");
  assert.equal(b.kind, "paid");
  assert.equal(b.payment.payment_no, a.payment.payment_no);
  assert.equal(c.kind, "blocked", "a different body during a send is refused");
});

test("a settled attempt answers the same press again from its receipt, with no second call", async () => {
  const t = transport([reply(201, PAYMENT)]);
  const sender = new PaySender(t.client, { canPay: () => true });
  assert.equal((await sender.pay(body(), 5000)).kind, "paid");
  const again = await sender.pay(body(), 5000);
  assert.equal(again.kind, "paid");
  assert.equal(again.payment.payment_no, "PMT-1");
  assert.equal(t.calls.length, 1);
});

test("a decline clears the attempt, and trying again is a new attempt with a new key", async () => {
  const t = transport([reply(402, { error: "payment declined" }), reply(201, PAYMENT)]);
  const sender = new PaySender(t.client, { canPay: () => true });
  const declined = await sender.pay(body(), 5000);
  assert.equal(declined.kind, "declined");
  assert.equal(sender.waiting, null);
  assert.equal((await sender.pay(body(), 5000)).kind, "paid");
  assert.notEqual(t.calls[1].body.idempotency_key, t.calls[0].body.idempotency_key);
});

test("a send that never answers ends as unknown at its own deadline, not as a failure", async () => {
  const t = transport([() => new Promise(() => {})]);
  const sender = new PaySender(t.client, { canPay: () => true, timeoutMs: 40 });
  const outcome = await sender.pay(body(), 5000);
  assert.equal(outcome.kind, "unknown");
  assert.match(outcome.words, /may have gone through/);
  assert.ok(sender.waiting);
});

test("a role that lacks the pay capability is refused with no request", async () => {
  const t = transport([reply(201, PAYMENT)]);
  const sender = new PaySender(t.client, { canPay: () => false });
  const outcome = await sender.pay(body(), 5000);
  assert.equal(outcome.kind, "blocked");
  assert.match(outcome.words, /role/);
  assert.equal(t.calls.length, 0);
});

test("a malformed attempt is refused before any request: no invoice, no funding, both fundings, a repeated invoice", async () => {
  const t = transport([reply(201, PAYMENT)]);
  const sender = new PaySender(t.client, { canPay: () => true });
  for (const bad of [{ invoiceIds: [], methodId: METHOD }, { invoiceIds: [A] }, { invoiceIds: [A], methodId: METHOD, nonce: "n" }, { invoiceIds: [A, A], methodId: METHOD }]) {
    assert.equal((await sender.pay(bad, 1)).kind, "blocked");
  }
  assert.equal(t.calls.length, 0);
});

test("the body on the wire carries ids and one funding source and nothing else a card could ride in", async () => {
  const t = transport([reply(201, PAYMENT), reply(201, PAYMENT)]);
  const sender = new PaySender(t.client, { canPay: () => true });
  await sender.pay({ invoiceIds: [B, A], nonce: "tok-1" }, 100);
  assert.deepEqual(Object.keys(t.calls[0].body).sort(), ["idempotency_key", "invoice_ids", "nonce"]);
  assert.deepEqual(t.calls[0].body.invoice_ids, [A, B], "the selection is sent sorted, as the core hashes it");
  await sender.pay(body({ invoiceIds: [A] }), 100);
  assert.deepEqual(Object.keys(t.calls[1].body).sort(), ["idempotency_key", "invoice_ids", "payment_method_id"]);
});

test("a hosted frame's token is accepted by shape, and anything that looks like card digits is refused", () => {
  assert.equal(readHostedToken({ type: "hh-hosted-token", token: "stub-nonce-ok-1" }), "stub-nonce-ok-1");
  assert.equal(readHostedToken({ type: "other", token: "stub-nonce-ok-1" }), null);
  assert.equal(readHostedToken({ type: "hh-hosted-token", token: "" }), null);
  assert.equal(readHostedToken({ type: "hh-hosted-token", token: 42 }), null);
  assert.equal(readHostedToken({ type: "hh-hosted-token", token: "x".repeat(300) }), null);
  assert.equal(readHostedToken({ type: "hh-hosted-token", token: "4242424242424242" }), null);
  assert.equal(readHostedToken({ type: "hh-hosted-token", token: "4242 4242 4242 4242" }), null);
  assert.equal(readHostedToken({ type: "hh-hosted-token", token: "4242-4242-4242-4242" }), null);
  assert.equal(readHostedToken("4242424242424242"), null);
  assert.equal(readHostedToken(null), null);
});
