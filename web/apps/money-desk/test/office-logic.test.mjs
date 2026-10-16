// The receivables office's plain logic: the checks made before a request is sent, the words for what the server
// answered, and the rule that no address is ever drawn on a screen. Nothing here computes money.
import { test } from "node:test";
import assert from "node:assert/strict";
import { relativeStamp, contactLine, deliveryWord, runModeWord, runNoticeWords, statementHow, STATEMENT_ROUTE_LINE, SAVED_NOTICE, validateApContact } from "../src/ar/office-logic.ts";

test("an AP contact is checked before it is sent, in the core's limits", () => {
  assert.deepEqual(validateApContact({ name: "Accounts payable desk", email: "ap@gls-demo.invalid", phone: "" }), { ok: true, contact: { contact_name: "Accounts payable desk", email: "ap@gls-demo.invalid", phone: "" } });
  assert.deepEqual(validateApContact({ name: " Desk ", email: "", phone: " 5550100 " }), { ok: true, contact: { contact_name: "Desk", email: "", phone: "5550100" } });
  const none = validateApContact({ name: "Desk", email: "", phone: "" });
  assert.equal(none.ok, false);
  assert.match(none.words, /needs an email or a phone/);
  assert.equal(validateApContact({ name: "", email: "no at sign", phone: "" }).ok, false);
  assert.match(validateApContact({ name: "", email: "a b@x.invalid", phone: "" }).words, /not an address/);
  assert.match(validateApContact({ name: "x".repeat(121), email: "a@x.invalid", phone: "" }).words, /at most 120 characters/);
  assert.match(validateApContact({ name: "", email: `${"a".repeat(250)}@x.invalid`, phone: "" }).words, /at most 254 characters/);
  assert.match(validateApContact({ name: "", email: "", phone: "5".repeat(33) }).words, /at most 32/);
});

test("a contact reads as what is on file and never as an address or a number", () => {
  const line = contactLine({ tagged: true, contact: { customer_id: "c", contact_name: "Accounts payable desk", email: "ap@gls-demo.invalid", phone: "5550100" } });
  assert.match(line, /Accounts payable desk/);
  assert.match(line, /email on file/);
  assert.match(line, /phone on file/);
  assert.ok(!/@|gls-demo|5550100/.test(line), line);
  assert.ok(!/Statements/.test(SAVED_NOTICE), "the saved notice does not claim statements or invoices");
  assert.equal(SAVED_NOTICE, "The AP contact is saved. Reminders for this account go to it through the messaging sink.");
  assert.equal(contactLine({ tagged: false }), "No AP contact is tagged on this account.");
  assert.match(contactLine({ tagged: true, contact: { customer_id: "c", contact_name: "", email: "x@y.invalid", phone: "" } }), /^AP contact on file: email on file, no phone\.$/);
});

test("a statement run reads in the words of what it did", () => {
  assert.equal(runModeWord("MASS"), "Month end, every account");
  assert.equal(runModeWord("INTERIM"), "One account");
  assert.equal(runNoticeWords({ notices_enabled: true, notice_queued_count: 3, notice_suppressed_count: 1 }), "3 statements went to the messaging sink; 1 had no AP contact or was held back.");
  assert.equal(runNoticeWords({ notices_enabled: true, notice_queued_count: 1, notice_suppressed_count: 0 }), "1 statement went to the messaging sink.");
  assert.equal(runNoticeWords({ notices_enabled: false, notice_queued_count: 0, notice_suppressed_count: 4 }), "Statement notices are switched off for this dealer, so nothing was queued.");
  assert.equal(deliveryWord("QUEUED"), "Queued");
  assert.equal(deliveryWord("NOT_QUEUED"), "Not queued");
  assert.equal(deliveryWord("SENT"), "Sent");
  assert.equal(deliveryWord("FAILED"), "Failed");
});

test("how a statement goes reads from its delivery word, never from the AP contact", () => {
  assert.equal(statementHow("QUEUED"), "Queued as a text to the account's consented number");
  assert.equal(statementHow("SENT"), "Sent as a text to the account's consented number");
  assert.equal(statementHow("NOT_QUEUED"), "Not queued");
  assert.equal(statementHow("FAILED"), "Did not send");
  assert.equal(STATEMENT_ROUTE_LINE, "A statement with a balance is queued as a text to the account's consented number when notices are on.");
});

// ---- reminders and the paper path ----------------------------------------------------------------------------------

import { channelWord, paperResolveCheck, recipientWords, reminderPreview, reminderResultWords, sendStatusWord } from "../src/ar/office-logic.ts";

const POLICY = { aging_warn_days: 60, aging_escalate_days: 90, reminder_cadence_days: 7, reminder_max_per_cycle: 3, black_hole_window_days: 45, black_hole_min_sends: 3, reminder_email_enabled: true, reminder_sms_enabled: false, untagged_fallback: "CONFIRM", ar_tier: "PRO" };
const TAGGED = { tagged: true, contact: { customer_id: "c", contact_name: "Accounts payable desk", email: "ap@x.invalid", phone: "" } };

test("a reminder says before it is sent what would go and to whom, by role label", () => {
  assert.deepEqual(reminderPreview({ policy: POLICY, channel: "EMAIL", contact: TAGGED, confirm: false }), { blocked: "", lines: ["Goes to the AP contact (Accounts payable desk), by email, through the messaging sink."] });
  assert.equal(reminderPreview({ policy: POLICY, channel: "EMAIL", contact: null, confirm: false }).lines[0], "Checking the contact.");
  assert.equal(reminderPreview({ policy: { ...POLICY, ar_tier: "BASE" }, channel: "EMAIL", contact: TAGGED, confirm: false }).blocked, "Reminders are part of accelerated AR, and this dealer is on the base plan.");
  assert.equal(reminderPreview({ policy: { ...POLICY, reminder_email_enabled: false }, channel: "EMAIL", contact: TAGGED, confirm: false }).blocked, "Email reminders are switched off for this dealer.");
  assert.equal(reminderPreview({ policy: POLICY, channel: "SMS", contact: TAGGED, confirm: false }).blocked, "Text reminders are switched off for this dealer.");
  const smsOn = { ...POLICY, reminder_sms_enabled: true };
  assert.match(reminderPreview({ policy: smsOn, channel: "SMS", contact: TAGGED, confirm: false }).lines.join(" "), /No AP contact is reachable by text/);
  assert.equal(reminderPreview({ policy: { ...smsOn, untagged_fallback: "REFUSE" }, channel: "SMS", contact: TAGGED, confirm: false }).blocked, "No AP contact is reachable by text, and this dealer does not fall back to the account's own address.");
  const none = reminderPreview({ policy: POLICY, channel: "EMAIL", contact: { tagged: false }, confirm: false });
  assert.match(none.lines.join(" "), /nobody is tagged for AP/i);
  assert.match(reminderPreview({ policy: POLICY, channel: "EMAIL", contact: { tagged: false }, confirm: true }).lines.join(" "), /confirmed/);
});

test("a sent reminder reads by the recipient's kind, never the address", () => {
  const send = { id: "s", customer_id: "c", channel: "EMAIL", recipient: "ap@x.invalid", recipient_kind: "AP_CONTACT", bucket: "LATE", resend: false, status: "DELIVERED", provider: "stub", sent_at: "2026-01-01T00:00:00Z", open_count: 0, click_count: 0 };
  assert.equal(recipientWords(send), "the AP contact");
  assert.equal(recipientWords({ ...send, recipient_kind: "FALLBACK" }), "the account's own address, which you confirmed");
  assert.equal(channelWord("SMS"), "text");
  assert.equal(channelWord("EMAIL"), "email");
  assert.equal(sendStatusWord("DELIVERED"), "Went to the messaging sink");
  assert.equal(sendStatusWord("QUEUED"), "Queued");
  assert.equal(sendStatusWord("FAILED"), "Failed: nothing left the building");
  const words = reminderResultWords(send, "INV-1001");
  assert.equal(words, "Reminder for INV-1001 went to the AP contact by email, through the messaging sink.");
  assert.ok(!/@|ap@x/.test(words));
  assert.match(reminderResultWords({ ...send, status: "FAILED" }, "INV-1001"), /did not go: nothing left the building/);
  assert.match(reminderResultWords({ ...send, resend: true, watermark: "PAST DUE, 75 DAYS" }, "INV-1001"), /stamped PAST DUE, 75 DAYS/);
});

test("closing a paper path flag needs a decision and a note, said before it is sent", () => {
  assert.deepEqual(paperResolveCheck("MOVED_TO_PAPER", " Phoned the desk "), { ok: true, status: "MOVED_TO_PAPER", note: "Phoned the desk" });
  assert.equal(paperResolveCheck("DISMISSED", "").ok, false);
  assert.match(paperResolveCheck("DISMISSED", "  ").words, /Say why in a few words/);
  assert.match(paperResolveCheck("DISMISSED", "x".repeat(301)).words, /longer than 300 characters/);
  assert.equal(paperResolveCheck("OPEN", "note").ok, false);
});

test("a moment reads as the days since it, from elapsed time and never a guessed calendar day", () => {
  const now = Date.parse("2030-06-15T12:00:00Z");
  assert.equal(relativeStamp("2030-06-15T11:59:00Z", now), "today");
  assert.equal(relativeStamp("2030-06-15T13:00:00Z", now), "today", "a moment ahead of the clock reads today, never a negative");
  assert.equal(relativeStamp("2030-06-14T11:00:00Z", now), "1 day ago");
  assert.equal(relativeStamp("2030-06-10T12:00:00Z", now), "5 days ago");
  assert.equal(relativeStamp("not a time", now), "");
});

// ---- an invoice: send, dispute, a payment reversal -----------------------------------------------------------------

import { disputeCheck, disputeOutcomeWord, disputeReasonWord, reversalCheck, reversalWords, sendInvoiceWords } from "../src/ar/office-logic.ts";

test("a dispute is checked before it is sent: a reason, an amount in dollars, never above the balance", () => {
  assert.deepEqual(disputeCheck({ reason: "DAMAGED", amountText: "100.00", note: " Cracked pallets ", balanceCents: 96_000 }), { ok: true, reason: "DAMAGED", amountCents: 10_000, note: "Cracked pallets" });
  assert.match(disputeCheck({ reason: "", amountText: "100.00", note: "", balanceCents: 96_000 }).words, /Choose why/);
  assert.match(disputeCheck({ reason: "DAMAGED", amountText: "", note: "", balanceCents: 96_000 }).words, /Enter the disputed amount/);
  assert.match(disputeCheck({ reason: "DAMAGED", amountText: "12.345", note: "", balanceCents: 96_000 }).words, /dollars and cents/);
  assert.match(disputeCheck({ reason: "DAMAGED", amountText: "0", note: "", balanceCents: 96_000 }).words, /more than zero/);
  assert.equal(disputeCheck({ reason: "DAMAGED", amountText: "960.01", note: "", balanceCents: 96_000 }).words, "That is more than the invoice balance of $960.00.");
  assert.equal(disputeCheck({ reason: "DAMAGED", amountText: "960.00", note: "", balanceCents: 96_000 }).ok, true);
  assert.equal(disputeReasonWord("SHORT_DELIVERY"), "Short delivery");
  assert.equal(disputeReasonWord("NOT_ORDERED"), "Not ordered");
  assert.equal(disputeOutcomeWord("WRITTEN_OFF"), "Written off");
  assert.equal(disputeOutcomeWord("PAID"), "Paid");
});

test("a reversal needs a reason, and says what it undid in dollars the server named", () => {
  assert.deepEqual(reversalCheck("  Keyed on the wrong account "), { ok: true, reason: "Keyed on the wrong account" });
  assert.match(reversalCheck("  ").words, /Enter why the payment is being reversed/);
  const done = { label: "VOID", original_payment_id: "p", reversal_payment_id: "r", reversal_no: "REV-0101", amount_cents: 2500, reason: "x", same_day: true, reopened_invoices: ["a", "b"] };
  assert.equal(reversalWords(done, "PAY-0101"), "Payment PAY-0101 is reversed as a void, the same day. $25.00 is owing again on 2 invoices.");
  assert.equal(reversalWords({ ...done, label: "REVERSAL", same_day: false, reopened_invoices: ["a"] }, "PAY-0101"), "Payment PAY-0101 is reversed as a reversal. $25.00 is owing again on 1 invoice.");
});

test("a sent invoice says it went through the messaging sink, and a replay says nothing new went", () => {
  assert.equal(sendInvoiceWords({ sent: 1, replayed: 0, tone: "" }), "The invoice went to the account's contact through the messaging sink.");
  assert.equal(sendInvoiceWords({ sent: 0, replayed: 1, tone: "" }), "That invoice was already sent on this key, so nothing new went.");
  assert.match(sendInvoiceWords({ sent: 1, replayed: 0, tone: "", skipped: { "INV-1": "recorded, but delivery failed: x" } }), /was recorded, but did not go: nothing left the building/);
});

// ---- settings and the extra hold reads -------------------------------------------------------------------------------

import { autoHoldLines, dispatchWords, landingPolicyCheck, nsfPolicyCheck, policyFieldWord, policyValueWord, registerPolicyCheck } from "../src/ar/office-logic.ts";

const FORM = { warn: "60", escalate: "90", cadence: "7", maxPerCycle: "3", window: "45", minSends: "3", email: true, sms: false, fallback: "CONFIRM", tier: "PRO" };

test("the receivables rules are checked before they are sent, in the core's ranges and order", () => {
  assert.deepEqual(landingPolicyCheck(FORM), {
    ok: true,
    policy: { aging_warn_days: 60, aging_escalate_days: 90, reminder_cadence_days: 7, reminder_max_per_cycle: 3, black_hole_window_days: 45, black_hole_min_sends: 3, reminder_email_enabled: true, reminder_sms_enabled: false, untagged_fallback: "CONFIRM", ar_tier: "PRO" },
  });
  assert.equal(landingPolicyCheck({ ...FORM, warn: "0" }).words, "Overdue warning must be between 1 and 364 days.");
  assert.equal(landingPolicyCheck({ ...FORM, escalate: "366" }).words, "Escalation must be between 2 and 365 days.");
  assert.equal(landingPolicyCheck({ ...FORM, cadence: "91" }).words, "Days between reminders must be between 1 and 90.");
  assert.equal(landingPolicyCheck({ ...FORM, maxPerCycle: "21" }).words, "Reminders per cycle must be between 1 and 20.");
  assert.equal(landingPolicyCheck({ ...FORM, window: "6" }).words, "The paper path window must be between 7 and 365 days.");
  assert.equal(landingPolicyCheck({ ...FORM, minSends: "0" }).words, "Reminders before the paper path flag must be between 1 and 20.");
  assert.equal(landingPolicyCheck({ ...FORM, warn: "x" }).words, "Enter Overdue warning as a whole number of days.");
  assert.equal(landingPolicyCheck({ ...FORM, warn: "1.5" }).ok, false);
  assert.equal(landingPolicyCheck({ ...FORM, warn: "95", escalate: "90" }).words, "Overdue warning (95) must be fewer than escalation (90).");
  assert.equal(landingPolicyCheck({ ...FORM, fallback: "SHRUG" }).words, "Choose what happens when no AP contact is tagged.");
  assert.equal(landingPolicyCheck({ ...FORM, tier: "GOLD" }).words, "Choose the plan.");
});

test("the register settings are checked in the core's ranges", () => {
  const f = { visibility: "MANAGER_UP", dueSoon: "7", cooldown: "24", tones: ["FINAL", "FRIENDLY"], showCredits: false };
  assert.deepEqual(registerPolicyCheck(f), { ok: true, values: { account_position_visibility: "MANAGER_UP", due_soon_days: 7, reminder_cooldown_hours: 24, reminder_tones: ["FINAL", "FRIENDLY"], show_credits_default: false } });
  assert.equal(registerPolicyCheck({ ...f, dueSoon: "0" }).words, "The due soon window must be between 1 and 90 days.");
  assert.equal(registerPolicyCheck({ ...f, cooldown: "721" }).words, "The reminder cooldown must be between 0 and 720 hours.");
  assert.equal(registerPolicyCheck({ ...f, cooldown: "" }).words, "Enter the reminder cooldown as a whole number of hours.");
  assert.equal(registerPolicyCheck({ ...f, visibility: "EVERYONE" }).words, "Choose who sees an account's position.");
});

test("the returned payment fee is entered in dollars and sent in cents", () => {
  assert.deepEqual(nsfPolicyCheck({ fee: "35.00", taxable: false, code: " 4200 ", autoHold: true, notes: " Per the schedule " }), { ok: true, policy: { fee_cents: 3500, fee_taxable: false, fee_account_code: "4200", auto_hold: true, notes: "Per the schedule" } });
  assert.equal(nsfPolicyCheck({ fee: "0", taxable: false, code: "", autoHold: true, notes: "" }).policy.fee_cents, 0);
  assert.equal(nsfPolicyCheck({ fee: "", taxable: false, code: "", autoHold: true, notes: "" }).words, "Enter the fee, such as 35.00. Zero means no fee.");
  assert.equal(nsfPolicyCheck({ fee: "-5", taxable: false, code: "", autoHold: true, notes: "" }).words, "Enter the fee as dollars and cents, such as 35.00.");
  assert.equal(nsfPolicyCheck({ fee: "35.001", taxable: false, code: "", autoHold: true, notes: "" }).ok, false);
});

test("a settings change reads as words in the history", () => {
  assert.equal(policyFieldWord("reminder_email_enabled"), "Email reminders");
  assert.equal(policyFieldWord("ar_tier"), "Plan");
  assert.equal(policyFieldWord("something_new"), "something_new");
  assert.equal(policyValueWord("reminder_email_enabled", "true"), "On");
  assert.equal(policyValueWord("reminder_sms_enabled", "false"), "Off");
  assert.equal(policyValueWord("ar_tier", "PRO"), "Accelerated AR");
  assert.equal(policyValueWord("untagged_fallback", "CONFIRM"), "Ask to confirm the account's address");
  assert.equal(policyValueWord("aging_warn_days", "60"), "60");
});

test("the automatic hold switch is read as what it would apply, and what it cannot do", () => {
  const s = { auto_hold_enabled: false, scope: "ORG", aging_bucket_days: 30, aging_threshold_cents: 500_000, aging_hold_enabled: true, limit_hold_enabled: true, limit_tolerance_cents: 0, cod_enforced: true, thresholds_editable: false, switch_wired: true };
  const lines = autoHoldLines(s);
  assert.equal(lines[0], "Automatic holds are off. The credit sweep reports what it would hold and places nothing by itself.");
  assert.ok(lines.includes("Aged receivables: held past 30 days overdue once $5,000.00 or more is in that age."));
  assert.ok(lines.includes("Over the credit limit: held when a balance goes over the limit."));
  assert.ok(lines.includes("Pay before release accounts are held until the balance is paid."));
  assert.equal(autoHoldLines({ ...s, auto_hold_enabled: true })[0], "Automatic holds are on. The credit sweep places holds by itself, and each one can be lifted or overridden here.");
  assert.equal(autoHoldLines({ ...s, switch_wired: false }).at(-1), "The switch is not wired on this server, so it is shown but cannot be changed.");
  assert.ok(autoHoldLines(s).includes("The thresholds are the dealer's policy and are read only here."));
});

test("the dispatch gate reads as a yes or the server's reason", () => {
  assert.equal(dispatchWords({ allowed: true, reason: "" }, "SO-2001"), "Order SO-2001 may go out: nothing on the account stops it.");
  assert.equal(dispatchWords({ allowed: false, reason: "ACCOUNT ON HOLD, a payment was returned; the office must clear this before delivery" }, "SO-2001"), "Order SO-2001 is stopped: ACCOUNT ON HOLD, a payment was returned; the office must clear this before delivery.");
  assert.equal(dispatchWords({ allowed: false, reason: "AR check unavailable: order not found" }, "SO-9"), "Order SO-9 is stopped: AR check unavailable: order not found.");
});

// ---- the credit sweep ------------------------------------------------------------------------------------------------

import { sweepWords } from "../src/ar/office-logic.ts";

test("the credit sweep says what it did, and when it only reported", () => {
  assert.equal(sweepWords({ evaluated: 5, placed: 1, would_hold: 0, dry_run: false }), "The sweep checked 5 accounts and placed 1 hold.");
  assert.equal(sweepWords({ evaluated: 5, placed: 2, would_hold: 0, dry_run: false }), "The sweep checked 5 accounts and placed 2 holds.");
  assert.equal(sweepWords({ evaluated: 1, placed: 0, would_hold: 0, dry_run: false }), "The sweep checked 1 account and placed 0 holds.");
  assert.equal(sweepWords({ evaluated: 5, placed: 0, would_hold: 1, dry_run: true }), "The sweep checked 5 accounts. It would hold 1 and placed none, because automatic holds are off.");
  assert.equal(sweepWords({ evaluated: 5, placed: 0, would_hold: 3, dry_run: true }), "The sweep checked 5 accounts. It would hold 3 and placed none, because automatic holds are off.");
  assert.equal(sweepWords({ evaluated: 5, placed: 0, would_hold: 0, dry_run: true }), "The sweep checked 5 accounts. It would hold none and placed none, because automatic holds are off.");
});
