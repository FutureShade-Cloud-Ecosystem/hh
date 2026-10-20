// The receivables slice's plain logic: how an amount typed is read, how a refusal
// reads out loud, what each aging card is called. No money is computed here;
// the cases hold the display words and the one parse at the boundary.
import { test } from "node:test";
import assert from "node:assert/strict";
import { formatMoney, parseAmountToCents, centsToField } from "../src/ar/ar-money.ts";
import { STATEMENT_STRIP_LABELS, cardShown, bucketLabelFor, bucketWord, canLiftHolds, lineWords, cardCovers, cardTitleFor, holdKindWord, holdMarks, MAX_APPLY_INVOICES, overpaymentPreview, paymentOutcome, paymentReferenceRequired, pickCeilingWords, refusalWords, relativeDay, rowMarks, statusTone, statusWord, validatePromise, worklistActionWord } from "../src/ar/ar-logic.ts";
import { houseDay, resetHouseZone, serveHouseZoneFrom } from "@hardscapehouse/pos-kit/src/ported/host-facts.ts";

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
  assert.equal(formatMoney(undefined), "Not available");
  assert.equal(centsToField(96000), "960.00");
  assert.equal(centsToField(5), "0.05");
});

test("each card is titled by the days its tier covers, from the days the core sends", () => {
  assert.equal(cardTitleFor({ bucket: "ESCALATION", from_days: 90 }), "90 days and over");
  assert.equal(cardTitleFor({ bucket: "LATE", from_days: 61, to_days: 89 }), "61 to 89 days");
  assert.equal(cardTitleFor({ bucket: "WARN", from_days: 1, to_days: 60 }), "1 to 60 days");
  assert.equal(cardTitleFor({ bucket: "CURRENT", from_days: 0 }), "Current");
  // A dealer who retunes the tiers sees their own ranges.
  assert.equal(cardTitleFor({ bucket: "ESCALATION", from_days: 75 }), "75 days and over");
  assert.equal(cardTitleFor({ bucket: "LATE", from_days: 31, to_days: 74 }), "31 to 74 days");
  assert.equal(cardTitleFor({ bucket: "WARN", from_days: 1, to_days: 30 }), "1 to 30 days");
  assert.equal(cardCovers({ bucket: "WARN", from_days: 1, to_days: 30 }), "Past due");
  assert.equal(cardCovers({ bucket: "CURRENT", from_days: 0 }), "Not yet due");
});

test("every day lands in exactly one register band, in one vocabulary, and the boundaries meet", () => {
  const at = (n) => bucketLabelFor(n);
  assert.deepEqual(at(0), { label: "Current", key: "CURRENT" });
  assert.deepEqual(at(30), { label: "1 to 30 days", key: "1_30" });
  assert.deepEqual(at(31), { label: "31 to 60 days", key: "31_60" });
  assert.deepEqual(at(60), { label: "31 to 60 days", key: "31_60" });
  assert.deepEqual(at(61), { label: "61 to 90 days", key: "61_90" });
  assert.deepEqual(at(90), { label: "61 to 90 days", key: "61_90" });
  assert.deepEqual(at(91), { label: "Over 90 days", key: "90_PLUS" });
  // The core's key names the band; the core's older label never reaches the screen.
  assert.equal(bucketWord("90_PLUS", "91 and over"), "Over 90 days");
  assert.equal(bucketWord("1_30", "1 to 30"), "1 to 30 days");
  assert.equal(bucketWord(undefined, "61 to 90"), "61 to 90 days");
  assert.equal(bucketWord(undefined, "91 and over"), "Over 90 days");
});

test("a status reads as a word, and a tone that never carries the meaning alone", () => {
  assert.equal(statusWord("OVERDUE"), "Past due");
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
    // The promise is only what is true: pressing again sends the same payment under the same key.
    assert.ok(outcome.words.includes("to send the same payment"), outcome.words);
    assert.ok(!outcome.words.includes("whatever the form now says"), outcome.words);
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

test("the pick ceiling words are the route's own sentence, said by the browser before any body is sent", () => {
  // The browser refuses a pick over the ceiling locally before sending, in the route's own words,
  // while the server answers the replay judgement ahead of validateApply.
  assert.equal(MAX_APPLY_INVOICES, 100);
  const words = pickCeilingWords(101);
  assert.ok(words.startsWith("A received payment may name at most 100 invoices; record it as more than one payment."), words);
  assert.ok(words.endsWith("101 invoices are picked."), words);
});

test("the replay conflict is read by its 409 alone, never by matching the error's words", () => {
  const different = paymentOutcome("that idempotency key already recorded a different payment; use a new key", 409, true);
  assert.equal(different.kind, "recorded-other");
  assert.ok(different.words.startsWith("This payment was not recorded."), different.words);
  assert.ok(different.words.includes("The earlier press was recorded as a different payment."), different.words);
  assert.ok(different.words.includes("press Record payment to record this one"), different.words);
  // The status alone decides: any error text under a 409 is the spent key's conflict.
  const blank = paymentOutcome("server said no", 409, true);
  assert.equal(blank.kind, "recorded-other");
  // The old wordings under a 422 are the route's pre-write refusal of a body it read: no substring
  // of the error decides the outcome any more.
  assert.equal(paymentOutcome("that idempotency key already recorded a different payment; use a new key", 422, true).kind, "refused");
  assert.equal(paymentOutcome("that idempotency key already recorded a payment for another account; use a new key", 422, true).kind, "refused");
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
  assert.deepEqual(rowMarks(base), [{ word: "122d past due", tone: "stop", dashed: false }]);
  assert.deepEqual(rowMarks({ ...base, promised_date: "2030-01-01" }), [
    { word: "122d past due", tone: "stop", dashed: false },
    { word: "promise open", tone: "watch", dashed: true },
  ]);
  assert.deepEqual(rowMarks({ ...base, status: "DISPUTED", disputed: true }), [
    { word: "122d past due", tone: "stop", dashed: false },
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
  // The route's own refusals of a body it read all answer 422 in its words (the AR register maps its
  // bad request errors there), and its replay conflicts answer 409 (handled above); a 404 is its no
  // existence leak answer, which its replay walk can also give for a payment that landed, and every
  // other 4xx (400, 408, 413, 429, a proxy's answer) is not the route's answer at all. A body
  // mangled in flight answers 400 before any key is read, so it says nothing about the held key.
  for (const status of [400, 404, 405, 408, 413, 429]) {
    const held = paymentOutcome("not found", status, true);
    assert.equal(held.kind, "unknown", String(status));
    assert.ok(held.words.startsWith("The earlier press may or may not have been recorded."), held.words);
    assert.ok(held.words.includes("sends the same key"), held.words);
    assert.ok(!held.words.includes("was not recorded"), held.words);
  }
  const mangled = paymentOutcome("the request body must be JSON", 400, true);
  assert.equal(mangled.kind, "unknown");
  assert.ok(mangled.words.startsWith("The earlier press may or may not have been recorded."), mangled.words);
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

test("the held key's gate words say when to press: after signing in for a 401, only with a role that may for a 403", () => {
  const signIn = paymentOutcome("unauthorized", 401, true);
  assert.ok(signIn.words.includes("Sign in again. Then press Record payment"), signIn.words);
  const role = paymentOutcome("forbidden", 403, true);
  // The 403 names who may record the payment and never tells the same role to press again as is.
  assert.ok(role.words.includes("Your role may not do that. Ask the General Manager or the Financial Controller."), role.words);
  assert.ok(role.words.includes("Press Record payment again only once you are signed in with a role that can record it"), role.words);
  assert.ok(!role.words.includes("Financial Controller. Then press"), role.words);
});

test("check, ACH, and wire need a reference, while card and cash do not", () => {
  for (const method of ["CHEQUE", "EFT", "WIRE"]) assert.equal(paymentReferenceRequired(method), true, method);
  for (const method of ["CARD", "CASH"]) assert.equal(paymentReferenceRequired(method), false, method);
});

test("the worklist has one plain action label per server recommendation", () => {
  assert.equal(worklistActionWord("record_payment"), "Record payment");
  assert.equal(worklistActionWord("review_promise"), "Review promise");
  assert.equal(worklistActionWord("review_credit_hold"), "Review credit hold");
  assert.equal(worklistActionWord("apply_credit"), "Review credit");
  assert.equal(worklistActionWord("open_account"), "Open account");
});

test("an overpayment banner comes from the server preview, never browser money math", () => {
  assert.equal(overpaymentPreview(null, "The selected invoices can take at most 96000 cents right now; refresh and retry"), true);
  assert.equal(overpaymentPreview({ unapplied_cents: 1 }, ""), true);
  assert.equal(overpaymentPreview({ unapplied_cents: 0 }, ""), false);
  assert.equal(overpaymentPreview(null, "The server could not be reached"), false);
});

test("a one day range says 1 day, and a card whose tier range runs backwards is not shown", () => {
  // warn = 1: the first card covers one day.
  assert.equal(cardTitleFor({ bucket: "WARN", from_days: 1, to_days: 1 }), "1 day");
  assert.equal(cardTitleFor({ bucket: "LATE", from_days: 5, to_days: 5 }), "5 days");
  // escalate = warn + 1: the middle card would read 61 to 60 and can hold nothing.
  assert.equal(cardShown({ bucket: "LATE", from_days: 61, to_days: 60 }), false);
  assert.equal(cardShown({ bucket: "LATE", from_days: 61, to_days: 89 }), true);
  assert.equal(cardShown({ bucket: "ESCALATION", from_days: 61 }), true);
  assert.equal(cardShown({ bucket: "CURRENT", from_days: 0 }), true);
  assert.equal(cardShown({ bucket: "WARN", from_days: 1, to_days: 1 }), true);
});

test("the statement strip names its bands in the screen's one vocabulary; its last band is over 60, as the core sums it", () => {
  assert.deepEqual(STATEMENT_STRIP_LABELS, ["Current", "1 to 30 days", "31 to 60 days", "Over 60 days", "Total due"]);
});

// A Pacific dealer at 21:30 on the second of a month: already the third in UTC and in Toronto. The
// instant is built from numbers so no committed line reads as a calendar date.
const PACIFIC_EVENING = new Date(Date.UTC(2026, 6, 3, 4, 30));

test("a timestamp made this Pacific evening reads today, never tomorrow, in any machine zone", () => {
  serveHouseZoneFrom({ business_zone: "America/Los_Angeles" });
  try {
    const asOf = houseDay(PACIFIC_EVENING);
    assert.equal(asOf, "2026-07-02");
    assert.equal(relativeDay(PACIFIC_EVENING.toISOString(), asOf), "today");
    assert.equal(relativeDay("2026-07-01T20:00:00-07:00", asOf), "1 day ago");
    assert.equal(relativeDay("2026-07-02", asOf), "today");
  } finally {
    resetHouseZone();
  }
});

test("the money desk surface config read serves the dealer's zone for the day words", async () => {
  const { ApiClient } = await import("@hardscapehouse/api-client");
  const { GlApi } = await import("../src/gl/gl-api.ts");
  const fetch = async () => new Response(JSON.stringify({ surface_id: "money-desk", business_zone: "America/Los_Angeles" }), { status: 200, headers: { "content-type": "application/json" } });
  try {
    await new GlApi(new ApiClient({ baseUrl: "http://core.test", fetch })).legacyImportSource();
    assert.equal(houseDay(PACIFIC_EVENING), "2026-07-02");
  } finally {
    resetHouseZone();
  }
});
