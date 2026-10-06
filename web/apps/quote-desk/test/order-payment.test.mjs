// The counter payment panel's pure logic, unit tested: the amount parse
// (dollars by digits to integer cents, never multiplied), the idempotency
// key's lifecycle (minted once per attempt, kept across retries, new only for
// a new deliberate payment), the receipt's wording and the refusal and
// unknown outcome words. DOM-free, so the Node runner drives it.
import assert from "node:assert/strict";
import { test } from "node:test";
import {
  AttemptBook,
  COMMA_WORDS,
  OVERSAFE_WORDS,
  TENDER_METHODS,
  convertedNote,
  figuresLine,
  isDifferentPaymentRefusal,
  isUnknownOutcome,
  mintPaymentKey,
  parseTypedAmount,
  paymentRefusalWords,
  paymentTargetBalance,
  receivedByWord,
  receiptRows,
  replayedNote,
  tenderWord,
  typedCents,
  unknownOutcomeNote,
} from "../src/_order-payment.js";

const doc = (kind, status, totalCents, id = `${kind}-1`) => ({ kind, id, number: `${kind}-1`, status, date: "", totalCents });

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

test("the book reuses one minter without crypto.randomUUID and never repeats", () => {
  let calls = 0;
  const book = new AttemptBook(() => `fallback-${(calls += 1)}`);
  const first = book.open("q-1", { amountRaw: "1.00", amountCents: 100, tender: "CASH", reference: "", orderId: "" });
  assert.equal(first.key, "fallback-1");
  book.clear("q-1");
  const second = book.open("q-1", { amountRaw: "1.00", amountCents: 100, tender: "CASH", reference: "", orderId: "" });
  assert.equal(second.key, "fallback-2");
});

test("an attempt is minted once per quote and kept across its retries", () => {
  let mints = 0;
  const mint = () => `key-${(mints += 1)}`;
  const book = new AttemptBook(mint);
  const figures = { amountRaw: "10.00", amountCents: 1000, tender: "CARD", reference: "AUTH 9", orderId: "o-1" };
  const first = book.open("q-1", figures);
  assert.equal(mints, 1);
  assert.equal(first.phase, "in-flight");
  // A refusal: the corrected retry is the same attempt, the same key.
  book.mark("q-1", "kept");
  const corrected = book.open("q-1", { ...figures, amountRaw: "37.06", amountCents: 3706 });
  assert.equal(corrected.key, first.key);
  assert.equal(corrected.amountCents, 3706);
  assert.equal(corrected.phase, "in-flight");
  assert.equal(mints, 1);
  // An unknown outcome keeps the attempt and the key too.
  book.mark("q-1", "not-known");
  const unknown = book.attempt("q-1");
  assert.equal(unknown.key, first.key);
  assert.equal(unknown.phase, "not-known");
  // The key that took a different payment is held, not dropped.
  book.mark("q-1", "held");
  assert.equal(book.attempt("q-1").phase, "held");
  // A payment the server took ends the attempt: the next deliberate payment mints fresh.
  book.clear("q-1");
  assert.equal(book.attempt("q-1"), null);
  const next = book.open("q-1", figures);
  assert.equal(next.key, "key-2");
  assert.equal(mints, 2);
});

test("a different quote is a different payment: each quote holds its own attempt", () => {
  const book = new AttemptBook();
  const one = book.open("q-1", { amountRaw: "10.00", amountCents: 1000, tender: "CASH", reference: "", orderId: "" });
  const two = book.open("q-2", { amountRaw: "5.00", amountCents: 500, tender: "EFT", reference: "TR 1", orderId: "" });
  assert.notEqual(one.key, two.key);
  book.clear("q-1");
  assert.equal(book.attempt("q-1"), null);
  assert.equal(book.attempt("q-2").key, two.key);
  // The session ends: nothing of one session's payments is another's.
  book.clearAll();
  assert.equal(book.attempt("q-2"), null);
});

test("the book never re-arms an attempt that is still out", () => {
  const book = new AttemptBook();
  const first = book.open("q-1", { amountRaw: "10.00", amountCents: 1000, tender: "CASH", reference: "", orderId: "" });
  const again = book.open("q-1", { amountRaw: "99.00", amountCents: 9900, tender: "EFT", reference: "X", orderId: "" });
  assert.equal(again, first);
});

test("the server's own words tell a key that took a different payment apart from any other refusal", () => {
  assert.equal(isDifferentPaymentRefusal("this idempotency key took a different payment; use a new key for a new payment"), true);
  assert.equal(isDifferentPaymentRefusal("the payment is more than the balance of $37.06"), false);
  assert.equal(isDifferentPaymentRefusal(""), false);
});

test("the balance a payment is bounded by comes from the reads, never from arithmetic", () => {
  const order = { totalCents: 3706 };
  // No invoice yet: the agreed invoice the door raises opens at the agreed total.
  assert.equal(paymentTargetBalance(order, null, null), 3706);
  assert.equal(paymentTargetBalance(null, null, null), null);
  // An open invoice owes its whole total; a paid one owes nothing.
  assert.equal(paymentTargetBalance(order, doc("INVOICE", "FINALIZED", 3706), null), 3706);
  assert.equal(paymentTargetBalance(order, doc("INVOICE", "PAID", 3706), null), 0);
  // A part-paid invoice: the live figure only when THIS session's receipt answered it, for this invoice.
  const partPaid = doc("INVOICE", "PARTIALLY_PAID", 3706, "inv-9");
  assert.equal(paymentTargetBalance(order, partPaid, null), null);
  assert.equal(paymentTargetBalance(order, partPaid, { invoiceId: "inv-8", balanceCents: 2706 }), null);
  assert.equal(paymentTargetBalance(order, partPaid, { invoiceId: "inv-9", balanceCents: 2706 }), 2706);
  // Any other invoice state is not guessed at.
  assert.equal(paymentTargetBalance(order, doc("INVOICE", "DRAFT", 3706), null), null);
});

test("a decimal comma or a loose space is refused with the words that teach the point, and valid grouping passes", () => {
  // The reviewer's table, verbatim: each of these once left the desk as a different amount.
  for (const bad of ["0,50", "12,50", "1,5", "1 5", "12,50", "0 50", "1234,567", "1.234,50"]) {
    assert.deepEqual(parseTypedAmount(bad), { cents: null, problem: COMMA_WORDS }, bad);
  }
  // Valid thousands grouping, comma or space, only left of the point, is what people paste.
  assert.deepEqual(parseTypedAmount("$1,234.50"), { cents: 123450, problem: "" });
  assert.deepEqual(parseTypedAmount("1 234.50"), { cents: 123450, problem: "" });
  assert.deepEqual(parseTypedAmount("1,234,567.89"), { cents: 123456789, problem: "" });
  assert.deepEqual(parseTypedAmount("1 234"), { cents: 123400, problem: "" });
  // The parse's own refusals keep their words.
  assert.deepEqual(parseTypedAmount("1.005").problem, "The amount is not a number this desk can send: type dollars and cents, like 40.00.");
  assert.equal(parseTypedAmount("abc").cents, null);
  assert.equal(parseTypedAmount("").cents, null);
  assert.deepEqual(parseTypedAmount("40.00"), { cents: 4000, problem: "" });
});

test("lowercase letters and any non digit, non separator input are refused in the desk's own words, never sent", () => {
  // The desk's own words for what is not a number at all: letters, signs, a second point, unicode
  // digits, any junk, wherever it sits (in the fraction, in the whole, or after valid grouping).
  const words = "The amount is not a number this desk can send: type dollars and cents, like 40.00.";
  for (const bad of ["1,000.50abc", "5a", "1e3", "+5", "1.0.0", "1,000.5x", "40.00abc", "1a,000.00", "5;00", "１２３", "1 234a.00"]) {
    assert.deepEqual(parseTypedAmount(bad), { cents: null, problem: words }, bad);
  }
  // A decimal comma or space in the fraction keeps the words that teach the point.
  assert.deepEqual(parseTypedAmount("1.234,50"), { cents: null, problem: COMMA_WORDS });
});

test("an amount above Number.MAX_SAFE_INTEGER cents is refused, never rounded through a float", () => {
  assert.deepEqual(parseTypedAmount("90071992547409.93"), { cents: null, problem: OVERSAFE_WORDS });
  assert.deepEqual(parseTypedAmount("99999999999999999999"), { cents: null, problem: OVERSAFE_WORDS });
  assert.deepEqual(parseTypedAmount("90071992547409.91"), { cents: 9007199254740991, problem: "" });
});

test("the figures line names the documents in their own words and the balance when the reads carry it", () => {
  const quote = doc("QUOTE", "ACCEPTED", 6328);
  // Before any order: the agreed total is the bound.
  assert.equal(figuresLine(quote, null, [], 6328, 0), "Quote QUOTE-1 is Accepted at CA$63.28, the agreed total · balance CA$63.28");
  assert.equal(figuresLine(quote, null, [], null, 0), "Quote QUOTE-1 is Accepted at CA$63.28, the agreed total");
  // An order with no invoice yet.
  const order = doc("ORDER", "DRAFT", 3706);
  assert.equal(figuresLine(null, order, [], 3706, 1), "Order ORDER-1 is Draft at CA$37.06 · balance CA$37.06");
  // An invoice, part paid, with the receipt's live balance.
  const invoice = doc("INVOICE", "PARTIALLY_PAID", 3706, "inv-9");
  assert.equal(
    figuresLine(null, order, [invoice], 2706, 1),
    "Order ORDER-1 is Draft at CA$37.06 · Invoice INVOICE-1 is Part paid at CA$37.06 · balance CA$27.06",
  );
  // The same invoice without the receipt: the state is carried, the figure is not.
  assert.equal(
    figuresLine(null, order, [invoice], null, 1),
    "Order ORDER-1 is Draft at CA$37.06 · Invoice INVOICE-1 is Part paid at CA$37.06 · the balance it still owes is not carried by this read, the server holds it",
  );
  // More than one order: the server says which one to pay, and no balance is named.
  assert.equal(
    figuresLine(quote, order, [], null, 2),
    "Order ORDER-1 is Draft at CA$37.06 · the server holds 2 orders for this quote and will say which one to pay",
  );
  assert.equal(figuresLine(null, null, [], null, 0), "");
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
  const clerk = paymentRefusalWords(
    409,
    "this order is already invoiced as INV-3001 by another route, so the counter cannot bill it again; hand the customer to Payables and receivables, or to the General Manager or Financial controller, who record the payment against that invoice",
  );
  assert.ok(clerk.startsWith("This order is already invoiced as INV-3001"));
  assert.ok(clerk.includes("who record the payment against that invoice"));
});

test("a payment's role refusal names what to do when the gate's body carries no words of its own", () => {
  const words = "Taking a payment on this order is not open to your login. Ask a manager or the counter.";
  assert.equal(paymentRefusalWords(403, ""), words);
  assert.equal(paymentRefusalWords(403, "Forbidden"), words);
  // When the core sends words of its own (this door's role rule names the role and who to ask),
  // the server's sentence is the message.
  assert.equal(
    paymentRefusalWords(403, "quote: converting a quote to an order commits the customer's credit and is limited to sales roles, YARD cannot do it; ask inside sales or a manager"),
    "Converting a quote to an order commits the customer's credit and is limited to sales roles, YARD cannot do it; ask inside sales or a manager",
  );
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
