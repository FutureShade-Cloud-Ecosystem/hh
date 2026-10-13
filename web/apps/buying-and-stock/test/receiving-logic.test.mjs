// The receiving area's plain logic: the check made before goods are received (it mirrors the
// core's own, in its words, so a person reads the sentence once beside the field), the confirm
// steps that name what each press will do, the body of a landed cost attached to a receipt, and the
// words for a send, which is a successful request whose delivery can still have failed.
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  attachBody,
  attachConfirmWords,
  costsHidden,
  documentFileName,
  fallbackAsk,
  receiveBody,
  receiveConfirmWords,
  receivable,
  sendOutcome,
  sendConfirmWords,
  worklistRows,
} from "../src/receiving/receiving-logic.ts";
import { refusalWords } from "../src/po/po-logic.ts";

const PO = {
  id: "c1",
  po_no: "PO-PLM-136",
  vendor_name: "Alcli (demo)",
  status: "ORDERED",
  lines: [
    { id: "L1", line_no: 1, sku: "ALC-STEP-48", description: "Alcli Step 48in", qty_ordered_base: 10, qty_received_base: 4, qty_outstanding_base: 6, uom: "pc" },
    { id: "L2", line_no: 2, sku: "DBS-AGG-34CLEAR", description: "3/4 Clear Stone", qty_ordered_base: 4, qty_received_base: 0, qty_outstanding_base: 4, uom: "T" },
  ],
};

test("the worklist the core answers as null reads as nothing to receive", () => {
  assert.deepEqual(worklistRows({ items: null }), []);
  assert.deepEqual(worklistRows({ items: [{ po_id: "1" }] }), [{ po_id: "1" }]);
});

test("only an order that is placed or part received can be received against, in the core's words", () => {
  assert.equal(receivable("ORDERED"), "");
  assert.equal(receivable("PARTIAL"), "");
  assert.equal(receivable("DRAFT"), "Cannot receive against a draft purchase order: place the order first.");
  assert.equal(receivable("RECEIVED"), "This purchase order is already fully received.");
  assert.equal(receivable("CLOSED"), "This purchase order is closed.");
  assert.equal(receivable("CANCELLED"), "This purchase order was cancelled.");
});

test("a receipt takes the lines that were counted, blank lines are left alone", () => {
  const made = receiveBody({ L1: "6", L2: "" }, " one pallet short ", PO);
  assert.deepEqual(made, { ok: true, body: { lines: [{ po_line_id: "L1", qty_base: 6 }], notes: "one pallet short" }, picked: [{ line: PO.lines[0], qty: 6 }] });
  const two = receiveBody({ L1: "1.5", L2: "4" }, "", PO);
  assert.equal(two.ok, true);
  assert.deepEqual(two.body.lines, [{ po_line_id: "L1", qty_base: 1.5 }, { po_line_id: "L2", qty_base: 4 }]);
  assert.equal(two.body.notes, undefined);
});

test("a receipt that cannot be right is refused in words before anything is sent", () => {
  assert.match(receiveBody({ L1: "", L2: "" }, "", PO).words, /Write how many arrived on at least one line/);
  assert.match(receiveBody({ L1: "abc" }, "", PO).words, /how many ALC-STEP-48 arrived/);
  assert.match(receiveBody({ L1: "0" }, "", PO).words, /how many ALC-STEP-48 arrived/);
  assert.match(receiveBody({ L1: "-2" }, "", PO).words, /how many ALC-STEP-48 arrived/);
  // The core's own over receipt sentence, with both figures at four decimals.
  assert.equal(receiveBody({ L1: "6.0001" }, "", PO).words, "Over-receipt on line 1: 6.0001 requested but only 6.0000 outstanding.");
  assert.equal(receiveBody({ L1: "7" }, "", PO).words, "Over-receipt on line 1: 7.0000 requested but only 6.0000 outstanding.");
  assert.equal(receiveBody({ L1: "6" }, "", PO).ok, true);
});

test("the confirm step names every line, the order and what it does to stock", () => {
  const picked = receiveBody({ L1: "6", L2: "4" }, "", PO).picked;
  const words = receiveConfirmWords(PO, picked);
  assert.match(words, /Receive 6 Alcli Step 48in and 4 T 3\/4 Clear Stone against PO-PLM-136/);
  assert.match(words, /moves the goods into stock/i);
  assert.match(words, /cannot be undone here/i);
  assert.match(receiveConfirmWords(PO, picked.slice(0, 1)), /^Receive 6 Alcli Step 48in against PO-PLM-136/);
});

test("a landed cost attached to a receipt needs an amount and carries the rate only when it is USD", () => {
  const form = { freight: "480.00", duty: "", handling: "35.5", currency: "CAD", fx: "", basis: "WEIGHT" };
  assert.deepEqual(attachBody(form), { ok: true, body: { freight_cents: 48000, duty_cents: 0, handling_cents: 3550, allocation_basis: "WEIGHT", currency: "CAD" } });
  assert.deepEqual(attachBody({ ...form, currency: "USD", fx: "1.352" }).body, { freight_cents: 48000, duty_cents: 0, handling_cents: 3550, allocation_basis: "WEIGHT", currency: "USD", fx_rate: 1.352 });
  assert.match(attachBody({ ...form, currency: "USD", fx: "" }).words, /exchange rate/);
  assert.match(attachBody({ ...form, freight: "1.234" }).words, /freight as dollars and cents/);
  assert.match(attachBody({ ...form, freight: "", handling: "" }).words, /at least one of freight, duty or handling/);
});

test("the attach confirm step names the receipt and each amount, and does not add them up", () => {
  const body = attachBody({ freight: "480.00", duty: "12", handling: "", currency: "CAD", fx: "", basis: "VALUE" }).body;
  const words = attachConfirmWords("PO-PLM-136/R1", body);
  assert.match(words, /PO-PLM-136\/R1/);
  assert.match(words, /freight \$480\.00/);
  assert.match(words, /duty \$12\.00/);
  assert.doesNotMatch(words, /handling/);
  assert.match(words, /applied at once/i);
  assert.match(words, /by value/i);
});

test("the fallback is asked for only when the core says the untagged address needs a confirmation", () => {
  assert.equal(fallbackAsk("nobody is tagged for ordering at this vendor, confirm the fallback recipient or tag a contact"), true);
  assert.equal(fallbackAsk("no ordering contact is tagged for this vendor, tag one and the order goes to the right desk"), false);
  assert.equal(fallbackAsk("this vendor has no email on file, tag an ordering contact to send anything at all"), false);
});

test("the send confirm step names the desk it goes to, or says nobody is tagged", () => {
  const tagged = { tagged: true, contact: { contact_name: "Orders desk", email: "po@alcli.test", phone: "" } };
  assert.match(sendConfirmWords(PO, tagged), /Send PO-PLM-136 to Orders desk, po@alcli\.test/);
  assert.match(sendConfirmWords(PO, { tagged: false, contact: { contact_name: "", email: "", phone: "" } }), /Nobody is tagged for ordering at Alcli \(demo\)/);
  assert.match(sendConfirmWords(PO, null), /Send PO-PLM-136 to Alcli \(demo\)/);
});

test("a send is a successful request whose delivery is read from its status", () => {
  assert.equal(sendOutcome({ status: "DELIVERED", recipient: "po@alcli.test", detail: "" }).tone, "good");
  assert.equal(sendOutcome({ status: "DELIVERED", recipient: "po@alcli.test", detail: "" }).words, "Sent to po@alcli.test.");
  assert.equal(sendOutcome({ status: "QUEUED", recipient: "po@alcli.test", detail: "" }).tone, "watch");
  const failed = sendOutcome({ status: "FAILED", recipient: "po@alcli.test", detail: "no mail transport is wired" });
  assert.equal(failed.tone, "stop");
  assert.equal(failed.words, "Not delivered to po@alcli.test: no mail transport is wired. The attempt is on the record as failed.");
});

test("the cost payload the core withholds is read as hidden, not as zero", () => {
  assert.equal(costsHidden({}), true);
  assert.equal(costsHidden({ subtotal_cents: 0 }), false);
  assert.equal(costsHidden({ subtotal_cad_cents: 5 }), false);
});

test("a document downloads under the order's own name", () => {
  assert.equal(documentFileName("PO-PLM-136", "csv"), "PO-PLM-136.csv");
  assert.equal(documentFileName("PO/PLM 136", "html"), "PO-PLM-136.html");
});

test("a wall refusal names who may receive goods", () => {
  assert.match(refusalWords("receive", "Forbidden", 403), /Receiving goods is for the Yard Manager/);
  assert.equal(refusalWords("receive", "over-receipt on line 1, 7.0000 requested but only 6.0000 outstanding", 409), "Over-receipt on line 1, 7.0000 requested but only 6.0000 outstanding.");
});
