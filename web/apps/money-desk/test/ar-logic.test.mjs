// The receivables slice's plain logic: how an amount typed is read, how a refusal
// reads out loud, what each aging card is called. No money is computed here;
// the cases hold the display words and the one parse at the boundary.
import { test } from "node:test";
import assert from "node:assert/strict";
import { formatMoney, parseAmountToCents, centsToField } from "../src/ar/ar-money.ts";
import { allocateOldestFirst, canLiftHolds, cardRange, cardTitle, holdKindWord, holdMarks, refusalWords, relativeDay, rowMarks, statusTone, statusWord, validatePayment, validatePromise } from "../src/ar/ar-logic.ts";

test("an amount typed is read through the decimal string and never a float", () => {
  assert.equal(parseAmountToCents("19.68"), 1968);
  assert.equal(parseAmountToCents("50"), 5000);
  assert.equal(parseAmountToCents("0.1"), 10);
  assert.equal(parseAmountToCents("20.005"), null);
  assert.equal(parseAmountToCents("-5"), null);
  assert.equal(parseAmountToCents("1,200"), null);
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
  assert.equal(refusalWords("payment", "payments: transport error: x", undefined), "The server could not be reached. Nothing was changed. Try again.");
});

test("a payment is checked before it is sent, in words", () => {
  const ok = validatePayment({ amountText: "100.00", reference: "Cheque 1042", balanceCents: 96000 });
  assert.deepEqual(ok, { ok: true, amountCents: 10000 });
  assert.deepEqual(validatePayment({ amountText: "", reference: "r", balanceCents: 96000 }), { ok: false, words: "Enter the amount received, such as 100.00." });
  assert.deepEqual(validatePayment({ amountText: "0", reference: "r", balanceCents: 96000 }), { ok: false, words: "The amount must be more than zero." });
  assert.deepEqual(validatePayment({ amountText: "960.01", reference: "r", balanceCents: 96000 }), { ok: false, words: "That is more than the invoice balance of $960.00." });
  assert.deepEqual(validatePayment({ amountText: "5", reference: " ", balanceCents: 96000 }), { ok: false, words: "Enter a reference, such as a cheque number." });
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

test("the oldest invoice is filled first, to the cent, and what is left over stays unapplied", () => {
  assert.deepEqual(allocateOldestFirst([1425000, 1322400, 0], 2000000), [1425000, 575000, 0]);
  assert.deepEqual(allocateOldestFirst([100, 200], 50), [50, 0]);
  assert.deepEqual(allocateOldestFirst([100, 200], 1000), [100, 200]);
  assert.deepEqual(allocateOldestFirst([100], 0), [0]);
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
