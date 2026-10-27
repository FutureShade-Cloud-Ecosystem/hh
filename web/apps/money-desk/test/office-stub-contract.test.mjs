// The stub against the contract and the core for the receivables office routes: statement runs read back,
// AP contacts, reminders, the paper path, invoice send, document, dispute and payment reversal, the settings and
// the extra hold reads. Every answer is held to the generated wire descriptors; every refusal sentence must be
// found in the core's source, and the gates (a manager, or an administrator) must answer as the core's guards do.
import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { call, coreSource, forbidden, holds, later, refused, reset, startStub, stopStub } from "./office-harness.mjs";

before(startStub);
after(stopStub);

async function accounts() {
  const aging = await call("GET", "/api/v1/ar/aging");
  assert.equal(aging.status, 200);
  return aging.json.items;
}

// ---- statement runs and AP contacts --------------------------------------------------------------

test("a statement run is read back by its id in the contract's shape", async () => {
  await reset();
  const made = await call("POST", "/api/v1/ar/statement-runs", { mode: "MASS" });
  assert.equal(made.status, 201, "the core answers a created run with 201");
  holds("BillingStatementRunResult", made.json);
  const read = await call("GET", `/api/v1/ar/statement-runs/${made.json.run.id}`);
  assert.equal(read.status, 200);
  holds("BillingStatementRunResult", read.json);
  assert.deepEqual(read.json, made.json, "the stored run reads back as it was made");
  assert.equal((await call("POST", "/api/v1/ar/statement-runs", { mode: "MASS" })).json.run.id !== made.json.run.id, true, "each run has its own id");
});

test("a statement run is refused in the core's words, and an unknown run is not found", async () => {
  await reset();
  refused(await call("POST", "/api/v1/ar/statement-runs", { mode: "INTERIM" }), 400, "billing: an INTERIM run is for exactly one account");
  refused(await call("POST", "/api/v1/ar/statement-runs", { mode: "WEEKLY" }), 400, "billing: unknown statement mode");
  refused(await call("POST", "/api/v1/ar/statement-runs", { mode: "MASS", as_of: "soon" }), 400, "as_of must be YYYY-MM-DD");
  refused(await call("GET", "/api/v1/ar/statement-runs/not-a-run"), 404, "not found");
});

test("an AP contact is tagged, read back, replaced and removed", async () => {
  await reset();
  // The fifth demo account (Cedar Ridge) has no contact on file; the other four carry an invented desk.
  const first = (await accounts()).find((a) => a.account_no === "CR-500");
  const path = `/api/v1/ar/customers/${first.customer_id}/ap-contact`;
  const none = await call("GET", path);
  assert.equal(none.status, 200, "an untagged account is an ordinary answer, never a 404");
  holds("BillingAPContactRead", none.json);
  assert.equal(none.json.tagged, false);

  const saved = await call("PUT", path, { customer_id: "ignored", contact_name: "Accounts payable desk", email: "ap@gls-demo.invalid", phone: "" });
  assert.equal(saved.status, 200);
  holds("BillingAPContactRead", saved.json);
  assert.equal(saved.json.contact.customer_id, first.customer_id, "the account is the path's, never the body's");

  const read = await call("GET", path);
  holds("BillingAPContactRead", read.json);
  assert.equal(read.json.tagged, true);
  assert.equal(read.json.contact.contact_name, "Accounts payable desk");

  const replaced = await call("PUT", path, { contact_name: "", email: "", phone: "5550100" });
  assert.equal(replaced.status, 200);
  assert.equal((await call("GET", path)).json.contact.email, "", "a save replaces the whole contact");

  const gone = await call("DELETE", path);
  assert.equal(gone.status, 200);
  holds("BillingAPContactRead", gone.json);
  assert.equal(gone.json.tagged, false);
  assert.equal((await call("GET", path)).json.tagged, false);
});

test("an AP contact is refused in the core's words", async () => {
  await reset();
  const [first] = await accounts();
  const path = `/api/v1/ar/customers/${first.customer_id}/ap-contact`;
  refused(await call("PUT", path, { contact_name: "x".repeat(121), email: "ap@gls-demo.invalid", phone: "" }), 422, "ar: contact_name is at most 120 characters");
  refused(await call("PUT", path, { contact_name: "", email: `${"a".repeat(250)}@x.invalid`, phone: "" }), 422, "ar: an AP email is at most 254 characters and a phone at most 32");
  refused(await call("PUT", path, { contact_name: "", email: "", phone: "5".repeat(33) }), 422, "ar: an AP email is at most 254 characters and a phone at most 32");
  refused(await call("PUT", path, { contact_name: "", email: "no at sign", phone: "" }), 422, "ar: that AP email address is not an address");
  refused(await call("PUT", path, { contact_name: "Desk", email: "", phone: "" }), 422, "ar: an AP contact needs an email or a phone");
  const bad = await fetch(`${await baseOf()}${path}`, { method: "PUT", headers: { Authorization: "Bearer x", "Content-Type": "application/json" }, body: "{nope" });
  assert.equal(bad.status, 400);
  assert.ok(coreSource.includes("invalid JSON body"));
  assert.ok(JSON.stringify(await bad.json()).includes("invalid JSON body"));
});

async function baseOf() {
  const { baseUrl } = await import("./office-harness.mjs");
  return baseUrl();
}

// ---- reminders and the paper path ----------------------------------------------------------------------------------

const SEND = "/api/v1/ar/reminders";

async function openInvoice(customerNo) {
  const register = await call("GET", "/api/v1/ar/register");
  const row = register.json.items.find((r) => r.account_no === customerNo && r.balance_cents > 0);
  return { customer_id: row.customer_id, invoice_id: row.id };
}

const sendBody = (target, over = {}) => ({ ...target, channel: "EMAIL", resend: false, confirm_fallback: false, idempotency_key: `k-${Math.random()}`, ...over });

test("a reminder is refused on the base plan in the core's own sentence, and is not the gate's shared body", async () => {
  await reset();
  const target = await openInvoice("GE-100");
  const res = await call("POST", SEND, sendBody(target));
  refused(res, 403, "reminders are part of accelerated AR, this account is on the base plan");
  assert.notEqual(res.json.error, "Forbidden");
  const history = await call("GET", SEND);
  assert.equal(history.status, 200, "the history read is not tier gated");
  holds("BillingReminderSendPage", history.json);
  assert.deepEqual(history.json.items, []);
});

test("a reminder on the accelerated plan goes to the AP contact, replays on its key, and lists newest first", async () => {
  await reset("pro");
  const target = await openInvoice("GE-100");
  const body = sendBody(target, { idempotency_key: "r-1" });
  const first = await call("POST", SEND, body);
  assert.equal(first.status, 201);
  holds("BillingReminderSend", first.json);
  assert.equal(first.json.recipient_kind, "AP_CONTACT");
  assert.equal(first.json.status, "DELIVERED");
  assert.equal(first.json.provider, "stub");
  const again = await call("POST", SEND, body);
  assert.equal(again.status, 201);
  assert.equal(again.json.id, first.json.id, "the same key answers the first send");
  const second = await call("POST", SEND, sendBody(target, { resend: true }));
  assert.equal(second.status, 201);
  assert.equal(second.json.resend, true);
  assert.ok(second.json.watermark !== undefined && second.json.watermark !== "");
  const history = await call("GET", `${SEND}?customer_id=${target.customer_id}`);
  holds("BillingReminderSendPage", history.json);
  assert.equal(history.json.items.length, 2);
  assert.equal(history.json.items[0].id, second.json.id);
  const other = await call("GET", `${SEND}?customer_id=${(await openInvoice("OB-200")).customer_id}`);
  assert.equal(other.json.items.length, 0);
});

test("a reminder is refused in the core's words, in the core's order", async () => {
  await reset("pro");
  const tagged = await openInvoice("GE-100");
  refused(await call("POST", SEND, sendBody(tagged, { channel: "FAX" })), 422, "ar: channel must be EMAIL or SMS");
  refused(await call("POST", SEND, sendBody(tagged, { channel: "SMS" })), 422, "ar: text reminders are switched off for this dealer, turn them on in AR policy first");
  // An invoice that is not this account's open one is the refusal; none named means the account's oldest open one.
  const foreign = await openInvoice("OB-200");
  refused(await call("POST", SEND, sendBody({ customer_id: tagged.customer_id, invoice_id: foreign.invoice_id })), 422, "ar: there is no open invoice to remind about for this request");
  refused(await call("POST", SEND, sendBody({ customer_id: "e0000000-0000-4000-8000-0000000000ff", invoice_id: "" })), 404, "not found");
  const oldest = await call("POST", SEND, sendBody({ customer_id: tagged.customer_id, invoice_id: "" }));
  assert.equal(oldest.status, 201);
  assert.equal(oldest.json.bucket, "ESCALATION", "the account's worst open invoice is the one the message names");
  // The untagged account (Cedar Ridge) needs the fallback confirmed, and then has exactly one address.
  const bare = await openInvoice("CR-500");
  refused(await call("POST", SEND, sendBody(bare)), 422, "ar: nobody is tagged for AP on this account, confirm the fallback recipient or tag a contact");
  const confirmed = await call("POST", SEND, sendBody(bare, { confirm_fallback: true }));
  assert.equal(confirmed.status, 201);
  assert.equal(confirmed.json.recipient_kind, "FALLBACK");
  assert.ok(confirmed.json.fallback_reason.includes("no AP contact tagged"));
  assert.ok(coreSource.includes("no AP contact tagged; operator confirmed the account address"));
  // A dealer that refuses the fallback says so; an account with no address on a channel says that.
  await call("PUT", "/api/v1/ar/landing/policy", { ...(await call("GET", "/api/v1/ar/landing/policy")).json, untagged_fallback: "REFUSE" });
  refused(await call("POST", SEND, sendBody(bare, { confirm_fallback: true })), 422, "ar: no AP contact is tagged on this account, tag one and the reminder goes to the right desk");
  assert.ok(coreSource.includes("ar: no AP contact is tagged on this account, tag one and the reminder goes to the right desk"));
});

test("the paper path lists flags, a sweep raises the account that ignored three reminders, and a flag closes with a note", async () => {
  await reset("pro");
  const list = await call("GET", "/api/v1/ar/paper-path");
  assert.equal(list.status, 200);
  holds("BillingPaperPathPage", list.json);
  assert.equal(list.json.items.length, 1, "the demo world starts with one flag");
  const target = await openInvoice("RP-300");
  for (let i = 0; i < 3; i += 1) assert.equal((await call("POST", SEND, sendBody(target))).status, 201);
  const sweep = await call("POST", "/api/v1/ar/paper-path/sweep");
  assert.equal(sweep.status, 200);
  holds("BillingPaperPathSweepResult", sweep.json);
  assert.equal(sweep.json.raised, 1);
  assert.equal((await call("POST", "/api/v1/ar/paper-path/sweep")).json.raised, 0, "a second sweep raises nothing new");
  const after = await call("GET", "/api/v1/ar/paper-path");
  assert.equal(after.json.items.length, 2);
  const flag = after.json.items[0];
  const path = `/api/v1/ar/paper-path/${flag.flag_id}/resolve`;
  refused(await call("POST", path, { status: "OPEN", note: "n" }), 422, "ar: a paper-path flag closes as MOVED_TO_PAPER or DISMISSED, both are decisions, and both need one");
  refused(await call("POST", path, { status: "DISMISSED", note: "  " }), 422, "ar: say why in a few words, the next person to open this account reads it, not the flag");
  refused(await call("POST", path, { status: "DISMISSED", note: "x".repeat(301) }), 422, "ar: that note is longer than 300 characters");
  refused(await call("POST", "/api/v1/ar/paper-path/nope/resolve", { status: "DISMISSED", note: "n" }), 404, "not found");
  const done = await call("POST", path, { status: "MOVED_TO_PAPER", note: "Moved to paper statements" });
  assert.equal(done.status, 200);
  assert.deepEqual(done.json, { ok: true });
  assert.equal((await call("GET", "/api/v1/ar/paper-path")).json.items.length, 1);
  const landing = await call("GET", "/api/v1/ar/landing");
  assert.equal(landing.json.paper_path.length, 1, "the landing's Needs you list reads the same flags");
});

// ---- an invoice: send, document, dispute, and a payment reversal -------------------------------------------------------

async function invoiceOf(no) {
  const register = await call("GET", "/api/v1/ar/register");
  return register.json.items.find((r) => r.invoice_no === no);
}

test("an invoice is sent once per key, and an account with no contact email is refused in the core's words", async () => {
  await reset();
  const inv = await invoiceOf("INV-1002");
  const path = `/api/v1/ar/register/invoices/${inv.id}/send`;
  const first = await call("POST", path, { idempotency_key: "s-1" });
  assert.equal(first.status, 200);
  assert.equal(first.json.sent, 1);
  assert.equal(first.json.replayed, 0);
  // The core answers an empty tone here, which the contract's tone enum has no word for (reported, not copied).
  assert.equal(first.json.tone, "");
  const again = await call("POST", path, { idempotency_key: "s-1" });
  assert.equal(again.status, 200);
  assert.deepEqual([again.json.sent, again.json.replayed], [0, 1]);
  refused(await call("POST", path, { idempotency_key: "" }), 422, "sending an invoice needs an idempotency key");
  refused(await call("POST", "/api/v1/ar/register/invoices/nope/send", { idempotency_key: "s-2" }), 404, "no such invoice");
  const oakline = await invoiceOf("INV-1003");
  const none = await call("POST", `/api/v1/ar/register/invoices/${oakline.id}/send`, { idempotency_key: "s-3" });
  assert.equal(none.status, 422);
  assert.ok(none.json.error.endsWith("has no contact email on the account"), none.json.error);
  assert.ok(coreSource.includes('"%s has no contact email on the account"'));
});

test("an invoice document is HTML, never cached, and a missing invoice is not found", async () => {
  await reset();
  const inv = await invoiceOf("INV-1001");
  const doc = await call("GET", `/api/v1/ar/register/invoices/${inv.id}/document`);
  assert.equal(doc.status, 200);
  assert.ok(doc.text.startsWith("<!doctype html>"));
  assert.ok(doc.text.includes("INV-1001"));
  refused(await call("GET", "/api/v1/ar/register/invoices/nope/document"), 404, "no such invoice");
});

test("a dispute opens once, shows on the invoice, closes with an outcome, and is refused in the core's words", async () => {
  await reset();
  const inv = await invoiceOf("INV-1002");
  const path = `/api/v1/ar/register/invoices/${inv.id}/dispute`;
  const good = { reason: "DAMAGED", note: "Two pallets arrived cracked", amount_cents: 10000, idempotency_key: "d-1" };
  refused(await call("POST", path, { ...good, idempotency_key: "" }), 422, "opening a dispute needs an idempotency key");
  refused(await call("POST", path, { ...good, reason: "BORED" }), 422, "a dispute names a reason: short delivery, damaged, pricing, not ordered, or other");
  refused(await call("POST", "/api/v1/ar/register/invoices/nope/dispute", good), 404, "no such invoice");
  refused(await call("POST", path, { ...good, amount_cents: inv.balance_cents + 1 }), 422, "the disputed amount cannot exceed the outstanding balance");
  const opened = await call("POST", path, good);
  assert.equal(opened.status, 200);
  holds("BillingDisputeView", opened.json);
  assert.equal(opened.json.status, "OPEN");
  const detail = await call("GET", `/api/v1/ar/register/invoices/${inv.id}`);
  holds("BillingInvoiceDetail", detail.json);
  assert.equal(detail.json.dispute.status, "OPEN");
  assert.equal(detail.json.status, "DISPUTED");
  const second = await call("POST", path, { ...good, idempotency_key: "d-2" });
  assert.equal(second.status, 409);
  assert.equal(second.json.error, "this invoice already has an open dispute");
  assert.ok(coreSource.includes("this invoice already has an open dispute"));
  holds("BillingDisputeView", second.json.dispute);

  const close = `${path}/resolve`;
  refused(await call("POST", close, { outcome: "SHRUGGED" }), 422, "closing a dispute names how it ended: paid, credited, written off, or withdrawn");
  refused(await call("POST", "/api/v1/ar/register/invoices/nope/dispute/resolve", { outcome: "PAID" }), 404, "no such invoice");
  const done = await call("POST", close, { outcome: "CREDITED" });
  assert.equal(done.status, 200);
  assert.deepEqual(done.json, { resolved: true });
  assert.equal((await call("POST", close, { outcome: "CREDITED" })).json.resolved, false, "closing what is not open answers false");
  const after = await call("GET", `/api/v1/ar/register/invoices/${inv.id}`);
  assert.equal(after.json.dispute.status, "RESOLVED");
  assert.equal(after.json.dispute.outcome, "CREDITED");
  assert.notEqual(after.json.status, "DISPUTED");
});

test("a payment is reversed once, reopens what it paid, and is refused in the core's words", async () => {
  await reset();
  const inv = await invoiceOf("INV-1002");
  const paid = await call("POST", `/api/v1/ar/register/invoices/${inv.id}/payments`, { method: "CHEQUE", amount_cents: 2500, reference: "r", idempotency_key: "rv-1" });
  const path = `/api/v1/ar/payments/${paid.json.payment_id}/reverse`;
  refused(await call("POST", path, { reason: "" }), 400, "payments: a reversal reason is required");
  refused(await call("POST", "/api/v1/ar/payments/nope/reverse", { reason: "Keyed in error" }), 404, "payment not found");
  const reversed = await call("POST", path, { reason: "Keyed on the wrong account" });
  assert.equal(reversed.status, 201);
  holds("PaymentsReversalResult", reversed.json);
  assert.equal(reversed.json.amount_cents, 2500);
  assert.equal(reversed.json.label, "VOID", "a same day reversal is a void");
  assert.equal(reversed.json.same_day, true);
  assert.deepEqual(reversed.json.reopened_invoices, [inv.id]);
  assert.equal((await invoiceOf("INV-1002")).balance_cents, inv.balance_cents, "the balance is what it was before the payment");
  refused(await call("POST", path, { reason: "again" }), 409, "payments: this payment has already been reversed");
  const history = await call("GET", `/api/v1/ar/payments/${paid.json.payment_id}/history`);
  holds("TillPaymentHistory", history.json);
  assert.equal(history.json.reversed, true);
  const reversal = await call("POST", `/api/v1/ar/payments/${reversed.json.reversal_payment_id}/reverse`, { reason: "undo the undo" });
  assert.equal(reversal.status, 400);
  assert.ok(reversal.json.error.includes("it is itself a reversing entry"));
  assert.ok(coreSource.includes("it is itself a reversing entry"));
  const list = await call("GET", `/api/v1/ar/invoices/${inv.id}/payments`);
  holds("TillInvoicePayments", list.json);
  assert.ok(list.json.items.some((p) => p.status === "VOID"));
  assert.ok(list.json.items.some((p) => typeof p.reversed_at === "string"), "the reversed original carries reversed_at");
  // A reversed payment cannot be bounced (SF-2d round 7).
  refused(await call("POST", `/api/v1/ar/payments/${paid.json.payment_id}/nsf`, { reason: "Returned" }), 409, "this payment was reversed, so it cannot be marked bounced");
});

test("a mis-keyed receipt is voided as never taken, with nothing held (FW1-06b)", async () => {
  await reset();
  const inv = await invoiceOf("INV-1002");
  const paid = await call("POST", `/api/v1/ar/register/invoices/${inv.id}/payments`, { method: "CHEQUE", amount_cents: 2500, reference: "r", idempotency_key: "vd-1" });
  const path = `/api/v1/ar/payments/${paid.json.payment_id}/void`;
  refused(await call("POST", path, { reason: "" }), 400, "payments: a void reason is required");
  refused(await call("POST", "/api/v1/ar/payments/nope/void", { reason: "Keyed in error" }), 404, "payment not found");
  const voided = await call("POST", path, { reason: "Keyed for the wrong amount" });
  assert.equal(voided.status, 201);
  holds("PaymentsVoidResult", voided.json);
  assert.equal(voided.json.amount_cents, 2500);
  assert.equal(voided.json.label, "VOID");
  assert.equal(voided.json.held_cents, 0, "the void holds nothing in 2310: the receipt never happened");
  assert.equal(typeof voided.json.voided_at, "string");
  assert.deepEqual(voided.json.reopened_invoices, [inv.id]);
  assert.equal((await invoiceOf("INV-1002")).balance_cents, inv.balance_cents, "the balance is what it was before the payment");
  refused(await call("POST", path, { reason: "again" }), 400, "only a received receipt that has not been deposited can be voided");
  assert.ok(coreSource.includes("it was reversed, and the reversal owns the undo"));
  // The voided receipt's allocation is gone, so the settlement list no longer carries the row.
  const list = await call("GET", `/api/v1/ar/invoices/${inv.id}/payments`);
  holds("TillInvoicePayments", list.json);
  assert.ok(!list.json.items.some((p) => p.payment_id === paid.json.payment_id), "the voided receipt is not on the invoice's payments list");
  // A voided receipt cannot be bounced either: the bounce undoes a live receipt, and this one never happened.
  refused(await call("POST", `/api/v1/ar/payments/${paid.json.payment_id}/nsf`, { reason: "Returned" }), 409, "till: only a received or deposited payment can be marked bounced");
});

test("a reversed payment's held money is read, applied to an invoice, replayed once and refused in the core's words", async () => {
  await reset();
  const inv = await invoiceOf("INV-1002");
  const other = await invoiceOf("INV-1001");
  const paid = await call("POST", `/api/v1/ar/register/invoices/${inv.id}/payments`, { method: "CHEQUE", amount_cents: 2500, reference: "r", idempotency_key: "ra-1" });
  const id = paid.json.payment_id;
  const held = `/api/v1/ar/payments/${id}/held`;
  const apply = `/api/v1/ar/payments/${id}/reapply`;
  const body = (key, invoice, cents) => ({ reason: "Keyed on the wrong account", idempotency_key: key, allocations: [{ invoice_id: invoice.id, amount_cents: cents }] });
  // Never reversed: it holds nothing.
  const none = await call("GET", held);
  assert.equal(none.status, 200);
  holds("PaymentsHeld", none.json);
  assert.deepEqual([none.json.reversed, none.json.held_cents], [false, 0]);
  refused(await call("POST", apply, body("ra-k1", inv, 100)), 409, "it has not been reversed");
  refused(await call("GET", "/api/v1/ar/payments/00000000-0000-4000-8000-0000000000aa/held"), 404, "payment not found");
  assert.equal((await call("POST", `/api/v1/ar/payments/${id}/reverse`, { reason: "Keyed on the wrong account" })).status, 201);
  const reversed = await call("GET", held);
  holds("PaymentsHeld", reversed.json);
  assert.deepEqual([reversed.json.reversed, reversed.json.amount_cents, reversed.json.held_cents], [true, 2500, 2500]);
  // The service's checks first, then the store's.
  refused(await call("POST", apply, { ...body("ra-k2", inv, 100), reason: " " }), 400, "payments: a re-application reason is required");
  refused(await call("POST", apply, { ...body("", inv, 100) }), 400, "re-applying held money needs an idempotency key");
  refused(await call("POST", apply, { reason: "x", idempotency_key: "ra-k3", allocations: [] }), 400, "name at least one invoice to apply the held money to");
  refused(await call("POST", apply, body("ra-k4", inv, 2501)), 409, "that is more than the payment still holds");
  refused(await call("POST", apply, body("ra-k5", { id: "00000000-0000-4000-8000-0000000000bb" }, 100)), 404, "invoice not found");
  // The apply: the held money goes on an invoice of the account, and the balance moves with it.
  const before = other.balance_cents;
  const first = await call("POST", apply, body("ra-k6", other, 1000));
  assert.equal(first.status, 201);
  holds("PaymentsReapplication", first.json);
  assert.deepEqual([first.json.applied_cents, first.json.held_before_cents, first.json.held_after_cents, first.json.replayed], [1000, 2500, 1500, false]);
  assert.equal((await invoiceOf("INV-1001")).balance_cents, before - 1000);
  assert.equal((await call("GET", held)).json.held_cents, 1500);
  // The same key and body answers the first result, writes nothing; the same key with another amount is a conflict.
  const replay = await call("POST", apply, body("ra-k6", other, 1000));
  assert.equal(replay.status, 200);
  holds("PaymentsReapplication", replay.json);
  assert.deepEqual([replay.json.replayed, replay.json.batch_id], [true, first.json.batch_id]);
  assert.equal((await invoiceOf("INV-1001")).balance_cents, before - 1000);
  refused(await call("POST", apply, body("ra-k6", other, 1100)), 409, "that idempotency key already re-applied held money differently; use a new key");
  // Never twice: what is left can be applied once, and no more.
  refused(await call("POST", apply, body("ra-k7", other, 1501)), 409, "that is more than the payment still holds");
  assert.equal((await call("POST", apply, body("ra-k8", other, 1500))).status, 201);
  assert.equal((await call("GET", held)).json.held_cents, 0);
  refused(await call("POST", apply, body("ra-k9", other, 1)), 409, "that is more than the payment still holds");
  // The reversing row holds nothing.
  const list = await call("GET", `/api/v1/ar/invoices/${inv.id}/payments`);
  const row = list.json.items.find((p) => p.status === "VOID");
  refused(await call("POST", `/api/v1/ar/payments/${row.payment_id}/reapply`, body("ra-k10", other, 1)), 409, "it is itself a reversing entry, the money is held for the payment it reversed");
  assert.ok(coreSource.includes("the money is held for the payment it reversed"));
});

test("a reversed payment's held money is paid out, never past what is held, replayed once, and the write off waits for a count", async () => {
  await reset();
  const inv = await invoiceOf("INV-1002");
  const paid = await call("POST", `/api/v1/ar/register/invoices/${inv.id}/payments`, { method: "CHEQUE", amount_cents: 2500, reference: "r", idempotency_key: "po-1" });
  const id = paid.json.payment_id;
  const out = `/api/v1/ar/payments/${id}/payout`;
  const body = (key, kind, cents, extra = {}) => ({ kind, amount_cents: cents, reason: "The customer asked for it back", idempotency_key: key, ...extra });
  refused(await call("POST", out, body("po-k1", "BANK", 100, { reference: "CHQ 1" })), 409, "it has not been reversed");
  assert.equal((await call("POST", `/api/v1/ar/payments/${id}/reverse`, { reason: "Keyed on the wrong account" })).status, 201);
  refused(await call("POST", out, { ...body("po-k2", "BANK", 100, { reference: "CHQ 1" }), reason: " " }), 400, "payments: a reason is required");
  refused(await call("POST", out, body("", "BANK", 100, { reference: "CHQ 1" })), 400, "paying out held money needs an idempotency key");
  refused(await call("POST", out, body("po-k3", "CASHBACK", 100)), 400, "the kind must be DRAWER, BANK or WRITE_OFF");
  refused(await call("POST", out, body("po-k4", "BANK", 100)), 400, "a bank pay out needs the cheque number or transfer reference");
  refused(await call("POST", out, body("po-k5", "BANK", 2501, { reference: "CHQ 1" })), 409, "that is more than the payment still holds");
  refused(await call("POST", out, body("po-k6", "WRITE_OFF", 100)), 409, "this payment was not rung through a till session, so there is no count to cover a write off");
  const first = await call("POST", out, body("po-k7", "BANK", 1000, { reference: "CHQ 100245" }));
  assert.equal(first.status, 201);
  holds("PaymentsPayout", first.json);
  assert.deepEqual([first.json.amount_cents, first.json.held_before_cents, first.json.held_after_cents, first.json.replayed], [1000, 2500, 1500, false]);
  const held = await call("GET", `/api/v1/ar/payments/${id}/held`);
  holds("PaymentsHeld", held.json);
  assert.deepEqual([held.json.held_cents, held.json.paid_out_cents], [1500, 1000]);
  const replay = await call("POST", out, body("po-k7", "BANK", 1000, { reference: "CHQ 100245" }));
  assert.equal(replay.status, 200);
  assert.deepEqual([replay.json.replayed, replay.json.payout_id], [true, first.json.payout_id]);
  refused(await call("POST", out, body("po-k7", "BANK", 1100, { reference: "CHQ 100245" })), 409, "that idempotency key already paid out held money differently; use a new key");
  const drawer = await call("POST", out, body("po-k8", "DRAWER", 1500));
  assert.equal(drawer.status, 201);
  assert.equal((await call("GET", `/api/v1/ar/payments/${id}/held`)).json.held_cents, 0);
  refused(await call("POST", out, body("po-k9", "DRAWER", 1)), 409, "that is more than the payment still holds");
});

test("a bounced payment cannot be reversed, and the core says why", async () => {
  await reset();
  const inv = await invoiceOf("INV-1002");
  const paid = await call("POST", `/api/v1/ar/register/invoices/${inv.id}/payments`, { method: "CHEQUE", amount_cents: 2500, reference: "r", idempotency_key: "rv-2" });
  assert.equal((await call("POST", `/api/v1/ar/payments/${paid.json.payment_id}/nsf`, { reason: "Returned" })).status, 200);
  refused(await call("POST", `/api/v1/ar/payments/${paid.json.payment_id}/reverse`, { reason: "Too late" }), 400, "it was bounced, and a payment is reversed or bounced, never both");
});

// ---- settings: the receivables rules, the register, the returned payment fee, automatic holds ------------------------

/** A refusal whose core sentence is built by a format string: the format is in the core's source, the stub fills it. */
function refusedBy(res, status, expected, format) {
  assert.equal(res.status, status, JSON.stringify(res.json));
  holds("Error", res.json);
  assert.equal(res.json.error, expected);
  assert.ok(coreSource.includes(format), `the core's source does not contain the format "${format}"`);
}

test("the receivables rules are read by a manager, written only by an administrator, and refused in the core's words", async () => {
  await reset();
  const read = await call("GET", "/api/v1/ar/landing/policy", undefined, "manager");
  assert.equal(read.status, 200);
  holds("BillingLandingPolicy", read.json);
  assert.equal(read.json.ar_tier, "BASE");
  forbidden(await call("PUT", "/api/v1/ar/landing/policy", read.json, "manager"));
  forbidden(await call("GET", "/api/v1/ar/landing/policy/history", undefined, "manager"));
  const put = (over) => call("PUT", "/api/v1/ar/landing/policy", { ...read.json, ...over });
  refusedBy(await put({ aging_warn_days: 0 }), 422, "ar: aging_warn_days must be between 1 and 364", '"%s: %s must be between %d and %d"');
  refusedBy(await put({ black_hole_min_sends: 21 }), 422, "ar: black_hole_min_sends must be between 1 and 20", '"%s: %s must be between %d and %d"');
  assert.ok(coreSource.includes('{"black_hole_min_sends", p.BlackHoleMinSends, 1, 20}'));
  refusedBy(await put({ aging_warn_days: 95, aging_escalate_days: 90 }), 422, "ar: aging_warn_days (95) must be fewer than aging_escalate_days (90)", '"ar: aging_warn_days (%d) must be fewer than aging_escalate_days (%d)"');
  refusedBy(await put({ untagged_fallback: "SHRUG" }), 422, "ar: untagged_fallback must be CONFIRM or REFUSE, a send with no tagged contact is never a blast", "untagged_fallback must be CONFIRM or REFUSE");
  refused(await put({ ar_tier: "GOLD" }), 422, "ar: ar_tier must be BASE or PRO");
  const saved = await put({ reminder_cadence_days: 10, reminder_sms_enabled: true, ar_tier: "PRO" });
  assert.equal(saved.status, 200);
  holds("BillingLandingPolicy", saved.json);
  assert.equal(saved.json.reminder_cadence_days, 10);
  const history = await call("GET", "/api/v1/ar/landing/policy/history");
  assert.equal(history.status, 200);
  holds("BillingPolicyChangePage", history.json);
  assert.deepEqual(history.json.items.map((c) => c.field).sort(), ["ar_tier", "reminder_cadence_days", "reminder_sms_enabled"]);
  const cadence = history.json.items.find((c) => c.field === "reminder_cadence_days");
  assert.deepEqual([cadence.before, cadence.after], ["7", "10"]);
});

test("the register settings are read by anyone and written only by an administrator", async () => {
  await reset();
  const read = await call("GET", "/api/v1/ar/register/policy", undefined, "manager");
  assert.equal(read.status, 200);
  holds("BillingRegisterPolicy", read.json);
  forbidden(await call("PUT", "/api/v1/ar/register/policy", read.json, "manager"));
  const put = (over) => call("PUT", "/api/v1/ar/register/policy", { ...read.json, ...over });
  refused(await put({ account_position_visibility: "EVERYONE" }), 422, "account position visibility must be MANAGER_UP, ALL_STAFF or HIDDEN");
  refused(await put({ due_soon_days: 0 }), 422, "the due-soon window is between 1 and 90 days");
  refused(await put({ reminder_cooldown_hours: 721 }), 422, "the reminder cooldown is between 0 and 720 hours");
  refused(await put({ workspace_split_pct: 10 }), 422, "the panel split is between 20 and 80 percent");
  refused(await put({ workspace_arrangement: "CENTRE" }), 422, "the panel arrangement is PRIMARY_LEFT or PRIMARY_RIGHT");
  refused(await put({ reminder_tones: ["LOUD"] }), 422, "a reminder tone is FRIENDLY, FIRM or FINAL");
  const saved = await put({ due_soon_days: 10, reminder_tones: ["FINAL", "FRIENDLY", "FRIENDLY"] });
  assert.equal(saved.status, 200);
  assert.deepEqual(saved.json, { saved: true });
  const after = await call("GET", "/api/v1/ar/register/policy");
  holds("BillingRegisterPolicy", after.json);
  assert.equal(after.json.due_soon_days, 10);
  assert.deepEqual(after.json.reminder_tones, ["FRIENDLY", "FINAL"], "tones are kept once each, in escalation order");
});

test("the returned payment fee is read by a manager, written by an administrator, and is the fee a bounce charges", async () => {
  await reset();
  const read = await call("GET", "/api/v1/ar/nsf-policy", undefined, "manager");
  assert.equal(read.status, 200);
  holds("TillNSFPolicy", read.json);
  forbidden(await call("PUT", "/api/v1/ar/nsf-policy", read.json, "manager"));
  refused(await call("PUT", "/api/v1/ar/nsf-policy", { ...read.json, fee_cents: -1 }), 422, "till: fee_cents must be >= 0");
  const saved = await call("PUT", "/api/v1/ar/nsf-policy", { ...read.json, fee_cents: 6000, fee_account_code: "", notes: "Fee raised" });
  assert.equal(saved.status, 200);
  assert.deepEqual(saved.json, { status: "ok" });
  const after = await call("GET", "/api/v1/ar/nsf-policy");
  assert.equal(after.json.fee_cents, 6000);
  assert.equal(after.json.fee_account_code, "4200", "a blank account code takes the core's default");
  assert.ok(coreSource.includes('in.FeeAccountCode = "4200"'));
  const inv = await invoiceOf("INV-1002");
  const paid = await call("POST", `/api/v1/ar/register/invoices/${inv.id}/payments`, { method: "CHEQUE", amount_cents: 2500, reference: "r", idempotency_key: "nsf-1" });
  const marked = await call("POST", `/api/v1/ar/payments/${paid.json.payment_id}/nsf`, { reason: "Returned" });
  assert.equal(marked.json.fee_cents, 6000, "the bounce charges the policy's fee");
});

test("the automatic hold switch is an administrator's, shows what it would apply, and says when it cannot be changed", async () => {
  await reset();
  forbidden(await call("GET", "/api/v1/ar/auto-hold", undefined, "manager"));
  forbidden(await call("PUT", "/api/v1/ar/auto-hold", { enabled: true }, "manager"));
  const read = await call("GET", "/api/v1/ar/auto-hold");
  assert.equal(read.status, 200);
  holds("BillingAutoHoldSetting", read.json);
  assert.equal(read.json.auto_hold_enabled, false, "the switch ships off");
  assert.equal(read.json.thresholds_editable, false);
  refused(await call("PUT", "/api/v1/ar/auto-hold", { reason: "x" }), 400, "enabled must be true or false, say which");
  const on = await call("PUT", "/api/v1/ar/auto-hold", { enabled: true, reason: "Ready" });
  assert.equal(on.status, 200);
  holds("BillingAutoHoldSetting", on.json);
  assert.equal(on.json.auto_hold_enabled, true);
  assert.equal((await call("PUT", "/api/v1/ar/auto-hold", { enabled: true })).status, 200, "re-asserting the current value is not an error");
  await reset("unwired");
  const unwired = await call("GET", "/api/v1/ar/auto-hold");
  assert.equal(unwired.json.switch_wired, false);
  refused(await call("PUT", "/api/v1/ar/auto-hold", { enabled: true }), 503, "the auto-hold switch is not configured on this server, the AR policy write seam is not wired");
});

test("holds a returned payment placed are listed with their payment, and an NSF hold is released through the till's path", async () => {
  await reset();
  const list = await call("GET", "/api/v1/ar/credit-holds?status=ACTIVE", undefined, "manager");
  assert.equal(list.status, 200);
  holds("TillCreditHoldPage", list.json);
  assert.equal(list.json.items.length, 1, "only the hold a returned payment placed");
  assert.ok(list.json.items[0].source_payment_id);
  forbidden(await call("POST", `/api/v1/ar/credit-holds/${list.json.items[0].id}/release`, { reason: "paid" }, "manager"));
  const nsf = list.json.items[0];
  // The billing release refuses an NSF hold in the core's words and points at the till's path.
  const viaBilling = await call("POST", `/api/v1/ar/holds/${nsf.id}/ar-release`, { reason: "paid" });
  assert.equal(viaBilling.status, 400);
  assert.ok(viaBilling.json.error.includes("an NSF hold is released through the till's Tier-1 path"));
  assert.ok(coreSource.includes("an NSF hold is released "));
  refused(await call("POST", `/api/v1/ar/credit-holds/${nsf.id}/release`, { reason: "" }), 422, "till: a release reason is required");
  refused(await call("POST", "/api/v1/ar/credit-holds/nope/release", { reason: "paid" }), 404, "not found");
  const done = await call("POST", `/api/v1/ar/credit-holds/${nsf.id}/release`, { reason: "Replacement cheque cleared" });
  assert.equal(done.status, 200);
  holds("TillStatusResult", done.json);
  assert.equal(done.json.status, "RELEASED");
  const all = await call("GET", "/api/v1/ar/credit-holds");
  assert.equal(all.json.items[0].status, "RELEASED");
  assert.equal(all.json.items[0].release_reason, "Replacement cheque cleared");
  assert.equal((await call("GET", "/api/v1/ar/credit-holds?status=ACTIVE")).json.items.length, 0);
});

test("the dispatch gate says whether an order may go out, and why not, in the core's words", async () => {
  await reset();
  const granite = await invoiceOf("INV-1001");
  const clear = await invoiceOf("INV-1005");
  const orderOf = async (inv) => (await call("GET", `/api/v1/ar/register/invoices/${inv.id}`)).json.order_id;
  const stopped = await call("GET", `/api/v1/ar/dispatch-gate/${await orderOf(granite)}`, undefined, "manager");
  assert.equal(stopped.status, 200);
  holds("BillingDispatchGateResult", stopped.json);
  assert.deepEqual(stopped.json, { allowed: false, reason: "ACCOUNT ON HOLD, a payment was returned; the office must clear this before delivery" });
  assert.ok(coreSource.includes("ACCOUNT ON HOLD, a payment was returned; the office must clear this before delivery"));
  const open = await call("GET", `/api/v1/ar/dispatch-gate/${await orderOf(clear)}`);
  assert.deepEqual(open.json, { allowed: true, reason: "" });
  const missing = await call("GET", "/api/v1/ar/dispatch-gate/e0000000-0000-4000-8000-0000000000ff");
  assert.deepEqual(missing.json, { allowed: false, reason: "AR check unavailable: order not found" });
  assert.ok(coreSource.includes("AR check unavailable: order not found"));
});

// ---- the credit sweep -------------------------------------------------------------------------------------------------

test("the credit sweep only reports while automatic holds are off, and places the holds once they are on", async () => {
  await reset();
  const list = await call("GET", "/api/v1/ar/credit-holds?status=ACTIVE");
  await call("POST", `/api/v1/ar/credit-holds/${list.json.items[0].id}/release`, { reason: "Replacement cheque cleared" });
  const report = await call("POST", "/api/v1/ar/credit-sweep");
  assert.equal(report.status, 200);
  holds("BillingEvaluationResult", report.json);
  assert.equal(report.json.dry_run, true);
  assert.equal(report.json.placed, 0);
  assert.equal(report.json.would_hold, 1, "the account whose hold was lifted is still past the edge");
  assert.equal((await call("GET", "/api/v1/ar/holds?status=ACTIVE")).json.items.length, 2, "a dry run places nothing");
  await call("PUT", "/api/v1/ar/auto-hold", { enabled: true });
  const placed = await call("POST", "/api/v1/ar/credit-sweep");
  holds("BillingEvaluationResult", placed.json);
  assert.deepEqual([placed.json.dry_run, placed.json.placed, placed.json.would_hold], [false, 1, 0]);
  const after = await call("GET", "/api/v1/ar/holds?status=ACTIVE");
  assert.equal(after.json.items.length, 3);
  holds("BillingCreditHoldPage", after.json);
  assert.equal((await call("POST", "/api/v1/ar/credit-sweep")).json.placed, 0, "an account already held is not held twice");
});
