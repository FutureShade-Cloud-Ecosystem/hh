// The counter payment panel's pure logic, unit tested: the amount parse
// (dollars by digits to integer cents, never multiplied), the idempotency
// key's lifecycle (minted once per attempt, kept across retries, new only for
// a new deliberate payment), the receipt's wording and the refusal and
// unknown outcome words. DOM-free, so the Node runner drives it.
import assert from "node:assert/strict";
import { test } from "node:test";
import {
  PaymentKeyRing,
  TENDER_METHODS,
  convertedNote,
  isUnknownOutcome,
  mintPaymentKey,
  paymentRefusalWords,
  receivedByWord,
  receiptRows,
  replayedNote,
  tenderWord,
  typedCents,
  unknownOutcomeNote,
} from "../src/_order-payment.js";

test("the tender roster is the contract's four, in its order", () => {
  assert.deepEqual([...TENDER_METHODS], ["CASH", "CARD", "CHEQUE", "EFT"]);
  assert.equal(tenderWord("CASH"), "Cash");
  assert.equal(tenderWord("CARD"), "Card");
  assert.equal(tenderWord("CHEQUE"), "Cheque");
  assert.equal(tenderWord("EFT"), "EFT");
});

test("typed dollars become integer cents through the digits, never a multiply", () => {
  assert.equal(typedCents("40.00"), 4000);
  assert.equal(typedCents("40"), 4000);
  assert.equal(typedCents("0.05"), 5);
  assert.equal(typedCents("$1,234.50"), 123450);
  assert.equal(typedCents("0.1"), 10);
  // The half cent parse: 1.005 through a float multiply is the wrong cent; through the digits it is refused.
  assert.equal(typedCents("1.005"), null);
  assert.equal(typedCents(""), null);
  assert.equal(typedCents("abc"), null);
  assert.equal(typedCents("-5"), null);
});

test("a minted key is a UUID shape from the platform", () => {
  const key = mintPaymentKey();
  assert.match(key, /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$|^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
  assert.notEqual(mintPaymentKey(), mintPaymentKey());
});

test("when the platform offers no randomUUID the in memory fallback mints keys that never repeat", () => {
  const original = globalThis.crypto;
  Object.defineProperty(globalThis, "crypto", { value: { randomUUID: undefined }, configurable: true });
  try {
    const first = mintPaymentKey();
    const second = mintPaymentKey();
    assert.notEqual(first, "");
    assert.notEqual(second, "");
    assert.notEqual(first, second);
  } finally {
    Object.defineProperty(globalThis, "crypto", { value: original, configurable: true });
  }
});

test("the fallback minter works without crypto.randomUUID and never repeats", () => {
  let calls = 0;
  const ring = new PaymentKeyRing(() => `fallback-${(calls += 1)}`);
  assert.equal(ring.current(), "fallback-1");
  assert.equal(ring.current(), "fallback-1");
  ring.settled(true);
  assert.equal(ring.current(), "fallback-2");
});

test("the key is minted once per attempt and kept across its retries", () => {
  let mints = 0;
  const mint = () => `key-${(mints += 1)}`;
  const ring = new PaymentKeyRing(mint);
  const first = ring.current();
  assert.equal(mints, 1);
  // A refusal: the retry is the same attempt, the same key.
  ring.settled(false);
  assert.equal(ring.current(), first);
  assert.equal(mints, 1);
  // An unknown outcome keeps the key too (settled(false) is the same answer).
  ring.settled(false);
  assert.equal(ring.current(), first);
  // A payment the server took ends the attempt: the next deliberate payment mints fresh.
  ring.settled(true);
  assert.equal(ring.current(), "key-2");
  assert.equal(mints, 2);
  assert.equal(ring.held(), true);
});

test("a different quote is a different payment: the ring resets", () => {
  const ring = new PaymentKeyRing();
  const first = ring.current();
  ring.reset();
  assert.notEqual(ring.current(), first);
});

test("the receipt rows are the server's figures in the order a counter reads them", () => {
  const rows = receiptRows(
    {
      orderNo: "SO-2001",
      invoiceNo: "INV-3001",
      paymentNo: "P-4001",
      method: "CHEQUE",
      amountCents: 4000,
      balanceCents: 3068,
      receivedBy: "demo-account-sales",
      reference: "CHQ 2211",
    },
    "demo-account-sales",
  );
  assert.deepEqual(
    rows.map((r) => `${r.label}=${r.value}`),
    [
      "Invoice=INV-3001",
      "Order=SO-2001",
      "Payment=P-4001",
      "Tender=Cheque",
      "Amount=CA$40.00",
      "Balance on the invoice=CA$30.68",
      "Received by=you",
      "Reference as recorded=CHQ 2211",
    ],
  );
});

test("received by names the caller as you and any other receiver as the id stored", () => {
  assert.equal(receivedByWord("u-1", "u-1"), "you");
  assert.equal(receivedByWord("u-2", "u-1"), "u-2");
  assert.equal(receivedByWord("u-1", ""), "u-1");
});

test("a stored empty reference is said as none given, never invented", () => {
  const rows = receiptRows(
    { orderNo: "SO-1", invoiceNo: "INV-1", paymentNo: "P-1", method: "CASH", amountCents: 100, balanceCents: 0, receivedBy: "u-1", reference: "" },
    "u-1",
  );
  assert.equal(rows.find((r) => r.label === "Reference as recorded")?.value, "none given");
});

test("the refusal words keep the server's sentence and only peel its prefixes", () => {
  assert.equal(paymentRefusalWords(400, "the payment is more than the balance of $30.68"), "The payment is more than the balance of $30.68");
  assert.equal(paymentRefusalWords(403, "Forbidden"), "Forbidden");
  const clerk = paymentRefusalWords(
    409,
    "this order is already invoiced as INV-3001 by another route, so the counter cannot bill it again; hand the customer to Payables and receivables, or to the General Manager or Financial controller, who record the payment against that invoice",
  );
  assert.ok(clerk.startsWith("This order is already invoiced as INV-3001"));
  assert.ok(clerk.includes("who record the payment against that invoice"));
});

test("an unknown outcome is no status or a 5xx, and its note says the retry is safe", () => {
  assert.equal(isUnknownOutcome(undefined), true);
  assert.equal(isUnknownOutcome(500), true);
  assert.equal(isUnknownOutcome(503), true);
  assert.equal(isUnknownOutcome(400), false);
  assert.equal(isUnknownOutcome(409), false);
  assert.equal(isUnknownOutcome(403), false);
  const note = unknownOutcomeNote();
  assert.ok(note.startsWith("The outcome of that payment is not known"));
  assert.ok(note.includes("Trying again with the same amount and method is safe"));
});

test("the replay and converted notes say what the server did", () => {
  assert.ok(replayedNote().includes("nothing was posted a second time"));
  assert.ok(convertedNote().includes("the order never existed unpaid"));
});
