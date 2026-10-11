// The receivables slice's plain logic: how an amount typed is read, how a refusal
// reads out loud, what each aging card is called. No money is computed here;
// the cases hold the display words and the one parse at the boundary.
import { test } from "node:test";
import assert from "node:assert/strict";
import { formatMoney, parseAmountToCents, centsToField } from "../src/ar/ar-money.ts";
import { canLiftHolds, lineWords, cardRange, cardTitle, holdKindWord, holdMarks, paymentOutcome, refusalWords, relativeDay, rowMarks, statusTone, statusWord, validatePromise } from "../src/ar/ar-logic.ts";

test("an amount typed is read through the decimal string and never a float", () => {
  assert.equal(parseAmountToCents("19.68"), 1968);
  assert.equal(parseAmountToCents("50"), 5000);
  assert.equal(parseAmountToCents("0.1"), 10);
  assert.equal(parseAmountToCents("20.005"), null);
  assert.equal(parseAmountToCents("-5"), null);
  assert.equal(parseAmountToCents(""), null);
});

test("cents read as dollars, and a missing figure is never a plausible zero", () => {
  assert.equal(formatMoney(96000), "$960.00");
  assert.equal(formatMoney(undefined), "(no figure)");
  assert.equal(centsToField(96000), "960.00");
  assert.equal(centsToField(5), "0.05");
});

test("the four cards carry the words the landing draws", () => {
  assert.equal(cardTitle("ESCALATION"), "Escalation");
  assert.equal(cardTitle("LATE"), "Late");
  assert.equal(cardTitle("WARN"), "Overdue");
  assert.equal(cardTitle("CURRENT"), "Current");
  assert.equal(cardRange({ bucket: "ESCALATION", from_days: 90 }), "90 days and over");
  assert.equal(cardRange({ bucket: "LATE", from_days: 61, to_days: 89 }), "61 to 89 days");
  assert.equal(cardRange({ bucket: "WARN", from_days: 1, to_days: 60 }), "1 to 60 days");
  assert.equal(cardRange({ bucket: "CURRENT", from_days: 0 }), "Not yet due");
});

test("a status reads as a word, and a tone that never carries the meaning alone", () => {
  assert.equal(statusWord("OVERDUE"), "Overdue");
  assert.equal(statusWord("DUE_SOON"), "Due soon");
  assert.equal(statusWord("CREDIT_HOLD"), "Credit hold");
  assert.equal(statusTone("OVERDUE"), "stop");
  assert.equal(statusTone("PAID"), "good");
  assert.equal(statusTone("OPEN"), "plain");
});

test("a refused release and a refused journal approval name who may do it", () => {
  for (const action of ["release-hold", "approve-journal"]) {
    const words = refusalWords(action, "Forbidden", 403);
    assert.ok(words.includes("Financial Controller"), words);
    assert.ok(words.includes("General Manager"), words);
    assert.ok(!words.includes("Forbidden"), words);
  }
  assert.ok(refusalWords("release-hold", "Forbidden", 403).includes("credit hold"));
  assert.ok(refusalWords("approve-journal", "Forbidden", 403).includes("journal"));
});

test("the core's own sentence is kept, and a figure in cents reads as dollars", () => {
  assert.equal(refusalWords("promise", "a promise cannot exceed the outstanding balance", 400), "A promise cannot exceed the outstanding balance.");
  const payment = refusalWords("payment", "this invoice can take at most 96000 cents right now; refresh and retry", 400);
  assert.equal(payment, "This invoice can take at most $960.00 right now; refresh and retry.");
  assert.ok(!payment.includes("cents"));
  assert.equal(refusalWords("payment", "", 401), "Your sign in has ended. Sign in again.");
  assert.equal(refusalWords("bounce", "payment already marked BOUNCED", 409), "Payment already marked BOUNCED.");
  assert.equal(refusalWords("split", "payments: transport error: x", undefined), "The server could not be reached. Nothing was changed. Try again.");
});

test("a payment whose answer was lost or came back 5xx says the outcome is unknown and tells the person to check the account", () => {
  for (const status of [undefined, 500, 502, 503, 504]) {
    const outcome = paymentOutcome("payments: transport error: x", status);
    assert.equal(outcome.kind, "unknown", String(status));
    assert.ok(outcome.words.includes("may or may not have been recorded"), outcome.words);
    assert.ok(outcome.words.includes("Check the account"), outcome.words);
    assert.ok(!outcome.words.includes("Nothing was changed"), outcome.words);
  }
});

test("a worded 4xx is a refusal in the route's own words, and says the payment was not recorded", () => {
  const over = paymentOutcome("the selected invoices can take at most 96000 cents right now; refresh and retry", 422);
  assert.equal(over.kind, "refused");
  assert.equal(over.words, "The payment was not recorded. The selected invoices can take at most $960.00 right now; refresh and retry.");
  const missing = paymentOutcome("no such invoice: 0b1c2d3e", 404);
  assert.equal(missing.kind, "refused");
  assert.equal(missing.words, "The payment was not recorded. No such invoice: 0b1c2d3e.");
  assert.equal(paymentOutcome("Forbidden", 403).kind, "refused");
  assert.ok(paymentOutcome("Forbidden", 403).words.startsWith("The payment was not recorded."));
});

test("the route's replay refusal reads as the earlier payment being recorded, in both its wordings", () => {
  const different = paymentOutcome("that idempotency key already recorded a different payment; use a new key", 422);
  assert.equal(different.kind, "recorded");
  assert.ok(different.words.includes("already recorded by the earlier press"), different.words);
  assert.ok(!different.words.includes("was not recorded"), different.words);
  const otherAccount = paymentOutcome("that idempotency key already recorded a payment for another account; use a new key", 400);
  assert.equal(otherAccount.kind, "recorded");
});

test("a promise needs an amount, a date, and who promised it", () => {
  const good = { amountText: "50.00", date: "2999-01-01", by: "Accounts payable", note: "", balanceCents: 96000 };
  assert.deepEqual(validatePromise(good), { ok: true, promisedCents: 5000 });
  assert.equal(validatePromise({ ...good, amountText: "" }).ok, false);
  assert.equal(validatePromise({ ...good, date: "" }).words, "Pick the date the customer promised to pay by.");
  assert.equal(validatePromise({ ...good, by: "" }).words, "Enter who made the promise.");
  assert.equal(validatePromise({ ...good, amountText: "960.01" }).words, "That is more than the invoice balance of $960.00.");
});

test("a day reads as a count from the server's own day, never a calendar literal", () => {
  assert.equal(relativeDay("2030-06-01", "2030-06-30"), "29 days ago");
  assert.equal(relativeDay("2030-06-29", "2030-06-30"), "1 day ago");
  assert.equal(relativeDay("2030-06-30", "2030-06-30"), "today");
  assert.equal(relativeDay("2030-07-01", "2030-06-30"), "in 1 day");
  assert.equal(relativeDay("2030-07-12", "2030-06-30"), "in 12 days");
});

test("a register row's marks: the day count is the status word, a promise or a dispute rides as a second mark", () => {
  const base = { status: "OVERDUE", days_overdue: 122, disputed: false };
  assert.deepEqual(rowMarks(base), [{ word: "122d overdue", tone: "stop", dashed: false }]);
  assert.deepEqual(rowMarks({ ...base, promised_date: "2030-01-01" }), [
    { word: "122d overdue", tone: "stop", dashed: false },
    { word: "promise open", tone: "watch", dashed: true },
  ]);
  assert.deepEqual(rowMarks({ ...base, status: "DISPUTED", disputed: true }), [
    { word: "122d overdue", tone: "stop", dashed: false },
    { word: "disputed", tone: "info", dashed: false },
  ]);
  assert.deepEqual(rowMarks({ status: "DUE_SOON", days_overdue: 0, disputed: false }), [{ word: "due soon", tone: "watch", dashed: false }]);
  assert.deepEqual(rowMarks({ status: "OPEN", days_overdue: 0, disputed: false }), [{ word: "open", tone: "plain", dashed: false }]);
  assert.deepEqual(rowMarks({ status: "PAID", days_overdue: 0, disputed: false }), [{ word: "paid", tone: "good", dashed: false }]);
});

test("what the server's split says about an invoice, in the references' words", () => {
  assert.equal(lineWords({ outcome: "CLOSES", stays_open_cents: 0 }), "closes");
  assert.equal(lineWords({ outcome: "PART", stays_open_cents: 747400 }), "part, $7,474.00 stays open");
  assert.equal(lineWords({ outcome: "NONE", stays_open_cents: 100 }), "untouched");
});

test("an amount typed as money reads back, and a malformed one is still refused", () => {
  assert.equal(parseAmountToCents("$20,000.00"), 2000000);
  assert.equal(parseAmountToCents("$ 5"), null);
  assert.equal(parseAmountToCents("1,200"), 120000);
  assert.equal(parseAmountToCents("1,20"), null);
  assert.equal(parseAmountToCents("12,34,567"), null);
  assert.equal(parseAmountToCents("5000.00"), 500000);
});

test("a refused read says who may open the desk, not the words for a refused action", () => {
  const words = refusalWords("load", "Forbidden", 403);
  assert.ok(words.includes("open"), words);
  assert.ok(words.includes("General Manager"), words);
});

test("a hold reads as the references draw it: Aged AR and auto, Returned payment, Over limit and override in force", () => {
  assert.equal(holdKindWord("AR_AGING"), "Aged AR");
  assert.equal(holdKindWord("NSF"), "Returned payment");
  assert.equal(holdKindWord("CREDIT_LIMIT"), "Over limit");
  assert.equal(holdKindWord("COD"), "Pay before release");
  assert.equal(holdKindWord("MANUAL"), "Manual");
  const now = Date.parse("2030-06-30T12:00:00Z");
  assert.deepEqual(holdMarks({ auto_placed: true, override_until: null }, now), ["auto"]);
  assert.deepEqual(holdMarks({ auto_placed: false, override_until: "2030-07-02T00:00:00Z" }, now), ["override in force"]);
  assert.deepEqual(holdMarks({ auto_placed: false, override_until: "2030-06-01T00:00:00Z" }, now), []);
});

test("lifting and overriding a hold are the controller's and the general manager's, never payables and receivables", () => {
  assert.equal(canLiftHolds("financial_controller"), true);
  assert.equal(canLiftHolds("general_manager"), true);
  assert.equal(canLiftHolds("payables_receivables"), false);
});

test("a 4xx that is not the payment route's own refusal keeps a held key: the outcome stays unknown", () => {
  // The route's own refusals all answer 422 in its words (the AR register maps its bad request errors
  // there); a 404 is its no existence leak answer, which its replay walk can also give for a payment
  // that landed, and every other 4xx (408, 413, 429, a proxy's 400) is not the route's answer at all.
  for (const status of [400, 404, 405, 408, 409, 413, 429]) {
    const held = paymentOutcome("not found", status, true);
    assert.equal(held.kind, "unknown", String(status));
    assert.ok(held.words.startsWith("The earlier press may or may not have been recorded."), held.words);
    assert.ok(held.words.includes("sends the same key"), held.words);
    assert.ok(!held.words.includes("was not recorded"), held.words);
  }
  // The route's own worded refusal still ends the key while one is held, and with no key held the
  // same statuses keep their long standing reading.
  assert.equal(paymentOutcome("the selected invoices can take at most 96000 cents right now; refresh and retry", 422, true).kind, "refused");
  assert.equal(paymentOutcome("no such invoice: 0b1c2d3e", 404, false).kind, "refused");
});

test("a 401 or 403 on a press while the account holds a key is the gate's answer: unknown, never not recorded", () => {
  for (const status of [401, 403]) {
    const held = paymentOutcome("unauthorized", status, true);
    assert.equal(held.kind, "unknown", String(status));
    assert.ok(held.words.startsWith("The earlier press may or may not have been recorded."), held.words);
    assert.ok(held.words.includes(refusalWords("payment", "unauthorized", status)), held.words);
    assert.ok(held.words.includes("sends the same key"), held.words);
    assert.ok(!held.words.includes("was not recorded"), held.words);
    // With no key held, nothing earlier can have landed: the gate's refusal is a known answer.
    assert.equal(paymentOutcome("unauthorized", status, false).kind, "refused", String(status));
    assert.equal(paymentOutcome("unauthorized", status).kind, "refused", String(status));
  }
});
