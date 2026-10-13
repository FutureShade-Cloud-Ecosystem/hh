// The receivables office's plain logic: the checks made before a request is sent, the words for what the server
// answered, and the rule that no address is ever drawn on a screen. Nothing here computes money.
import { test } from "node:test";
import assert from "node:assert/strict";
import { contactLine, deliveryWord, runModeWord, runNoticeWords, statementGoes, validateApContact } from "../src/ar/office-logic.ts";

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
