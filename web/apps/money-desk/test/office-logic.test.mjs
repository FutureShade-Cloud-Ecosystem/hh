// The receivables office's plain logic: the checks made before a request is sent, the words for what the server
// answered, and the rule that no address is ever drawn on a screen. Nothing here computes money.
import { test } from "node:test";
import assert from "node:assert/strict";
import { relativeStamp, contactLine, deliveryWord, runModeWord, runNoticeWords, statementGoes, validateApContact } from "../src/ar/office-logic.ts";

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

test("where a statement would go is the AP contact, by its role label, or nowhere", () => {
  const tagged = { tagged: true, contact: { customer_id: "c", contact_name: "Accounts payable desk", email: "ap@x.invalid", phone: "" } };
  assert.equal(statementGoes(tagged, "QUEUED"), "To the AP contact (Accounts payable desk), by email");
  assert.equal(statementGoes({ tagged: false }, "NOT_QUEUED"), "Nowhere: no AP contact is tagged");
  assert.equal(statementGoes(null, "QUEUED"), "Checking the contact");
  assert.equal(statementGoes({ tagged: true, contact: { customer_id: "c", contact_name: "", email: "", phone: "5550100" } }, "QUEUED"), "To the AP contact, by phone");
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
