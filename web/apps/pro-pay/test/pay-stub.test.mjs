// Pro Pay's money routes against the shared tenant stub, with the real sender: each role meets the route its capability
// holds (the server is the boundary, whatever the screen offers), the sandbox verdicts reach the sender as the right
// outcomes, and a second sender holding the same key gets the original payment back, never a second one.
import { test, before, after, beforeEach } from "node:test";
import assert from "node:assert/strict";
import { tenantBilling, tenantDashboard, tenantPayments } from "@hardscapehouse/api-client";
import { InMemoryTenantSession, tenantClient, stubAdapter, surfaceOf, can, TENANT_ROLES } from "@hardscapehouse/pro-kit";
import { PERSONAS, STUB_PASSWORD } from "@hardscapehouse/pro-kit/test-stub/tenant-data.mjs";
import { PaySender } from "../src/pay-core.ts";
import { spawnStub } from "@hardscapehouse/pro-kit/test-stub/spawn-stub.mjs";

let stub;
let base;

before(async () => {
  stub = await spawnStub();
  base = stub.base;
});

// End the stub by its own door and await its exit: no signal is ever sent to a process.
after(async () => {
  await stub.stop();
});

beforeEach(async () => {
  await fetch(`${base}/_stub/reset`, { method: "POST" });
});

async function signedIn(who) {
  const session = new InMemoryTenantSession({ adapter: stubAdapter({ baseUrl: base }), baseUrl: base });
  assert.equal((await session.signIn(PERSONAS[who].email, STUB_PASSWORD)).ok, true, who);
  return { session, client: tenantClient(session, { baseUrl: base }) };
}

async function openInvoiceIds(client) {
  const page = await tenantBilling.tenantListInvoices(client, { limit: 200 });
  assert.equal(page.ok, true);
  return page.value.items.filter((i) => ["FINALIZED", "PARTIALLY_PAID"].includes(i.status) && i.balance_cents > 0).map((i) => i.id);
}

test("Pro Pay's three surfaces are in the catalogue with the capability of the route each reads", () => {
  assert.equal(surfaceOf("pay", "invoices")?.capability, "view_billing");
  assert.equal(surfaceOf("pay", "payment-methods")?.capability, "manage_payment_methods");
  assert.equal(surfaceOf("pay", "statements")?.capability, "view_billing");
});

test("every route Pro Pay reads is held by exactly the roles that hold its capability", async () => {
  const reads = [
    ["view_billing", (c) => tenantBilling.tenantListInvoices(c, { limit: 200 })],
    ["view_billing", (c) => tenantDashboard.tenantGetBillingSummary(c)],
    ["view_billing", (c) => tenantPayments.tenantListPayments(c)],
    ["manage_payment_methods", (c) => tenantPayments.tenantListPaymentMethods(c)],
  ];
  for (const [key, persona] of Object.entries(PERSONAS)) {
    const { client } = await signedIn(key);
    for (const [capability, read] of reads) {
      const answer = await read(client);
      if (can(persona.role, capability)) assert.equal(answer.ok, true, `${persona.role} should read under ${capability}`);
      else {
        assert.equal(answer.ok, false, `${persona.role} should be refused under ${capability}`);
        assert.equal(answer.status, 403);
      }
    }
  }
  assert.equal(TENANT_ROLES.length, 5);
});

test("the server refuses a pay from a role without the capability even when the screen is forced past its own gate", async () => {
  for (const [key, persona] of Object.entries(PERSONAS)) {
    if (can(persona.role, "pay_invoices") || !can(persona.role, "view_billing")) continue;
    const { client } = await signedIn(key);
    const ids = await openInvoiceIds(client);
    const forced = new PaySender(client, { canPay: () => true });
    const outcome = await forced.pay({ invoiceIds: [ids[0]], nonce: "stub-nonce-ok-1" }, 1);
    assert.equal(outcome.kind, "refused", persona.role);
    assert.equal(outcome.status, 403);
    assert.equal(forced.waiting, null);
  }
});

test("a role that holds the capability pays once, and the payment is the core's figure for the selection", async () => {
  const { client } = await signedIn("ap");
  const ids = await openInvoiceIds(client);
  const sender = new PaySender(client, { canPay: () => true });
  const methods = await tenantPayments.tenantListPaymentMethods(client);
  const outcome = await sender.pay({ invoiceIds: ids, methodId: methods.value[0].id }, 0);
  assert.equal(outcome.kind, "paid");
  assert.equal(outcome.payment.allocations.length, ids.length);
  assert.equal(outcome.differs, true, "the shown figure (0 here) is flagged when the books settle another");
  const after = await tenantPayments.tenantListPayments(client);
  assert.equal(after.value.length, 3);
});

test("a second sender holding the same key gets the original payment back and no second payment is made", async () => {
  const { client } = await signedIn("ap");
  const ids = await openInvoiceIds(client);
  const shown = await shownSum(client, ids);
  const first = new PaySender(client, { canPay: () => true, newKey: () => "fixed-key-for-this-attempt-0001" });
  const paid = await first.pay({ invoiceIds: ids, nonce: "stub-nonce-ok-7" }, shown);
  assert.equal(paid.kind, "paid");
  // A reload: a new sender, the same key and body (as a retried unknown outcome carries).
  const second = new PaySender(client, { canPay: () => true, newKey: () => "fixed-key-for-this-attempt-0001" });
  const again = await second.pay({ invoiceIds: ids, nonce: "stub-nonce-ok-7" }, shown);
  assert.equal(again.kind, "paid");
  assert.equal(again.payment.payment_no, paid.payment.payment_no);
  const held = await tenantPayments.tenantListPayments(client);
  assert.equal(held.value.length, 3, "two seed payments and one new, not two new");
});

test("the sandbox verdicts reach the sender: a decline is declined, a pending hold is unknown and stays held on the same key", async () => {
  const { client } = await signedIn("ap");
  const ids = await openInvoiceIds(client);
  const declined = await new PaySender(client, { canPay: () => true }).pay({ invoiceIds: ids, nonce: "stub-nonce-declined" }, 100);
  assert.equal(declined.kind, "declined");
  const sender = new PaySender(client, { canPay: () => true });
  const pending = await sender.pay({ invoiceIds: ids, nonce: "stub-nonce-pending" }, 100);
  assert.equal(pending.kind, "unknown");
  assert.ok(sender.waiting);
  const retried = await sender.pay({ invoiceIds: ids, nonce: "stub-nonce-pending" }, 100);
  assert.equal(retried.kind, "unknown");
  const none = await tenantPayments.tenantListPayments(client);
  assert.equal(none.value.length, 2, "neither verdict moved money");
});

test("a stored method's listing carries display metadata only, never a card number or a vault token", async () => {
  const { client } = await signedIn("ap");
  const methods = await tenantPayments.tenantListPaymentMethods(client);
  for (const m of methods.value) {
    assert.deepEqual(Object.keys(m).sort(), ["brand", "created_at", "customer_id", "exp_month", "exp_year", "gateway", "id", "is_default", "last4", "method_type", "status", "updated_at"]);
    assert.match(m.last4, /^\d{4}$/);
  }
});

// ---- the payoff (TM-2b): the stub quotes and charges as the core does -------------------------------------------

async function termsOn(invoiceNo, days) {
  const r = await fetch(`${base}/_stub/control`, { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify({ discount: { invoice_no: invoiceNo, bps: 200, days } }) });
  assert.equal(r.status, 200);
}

// The amount the Pay button would show for these invoices: the sum of the listed payoffs.
async function shownSum(client, ids) {
  const page = await tenantBilling.tenantListInvoices(client, { limit: 200 });
  return page.value.items.filter((i) => ids.includes(i.id)).reduce((sum, i) => sum + i.payoff_cents, 0);
}

async function listed(client, invoiceNo) {
  const page = await tenantBilling.tenantListInvoices(client, { limit: 200 });
  return page.value.items.find((i) => i.invoice_no === invoiceNo);
}

test("an open invoice inside its discount window lists the payoff, the discount and its last day; the pay charges the payoff", async () => {
  const { client } = await signedIn("ap");
  const before = await listed(client, "INV-20402");
  await termsOn("INV-20402", 3);
  const row = await listed(client, "INV-20402");
  const discount = Math.round(before.balance_cents * 0.02);
  assert.equal(row.discount_cents, discount);
  assert.equal(row.payoff_cents, before.balance_cents - discount);
  assert.match(row.discount_by, /^\d{4}-\d{2}-\d{2}$/);
  const sender = new PaySender(client, { canPay: () => true });
  const outcome = await sender.pay({ invoiceIds: [row.id], nonce: "stub-nonce-ok-2" }, row.payoff_cents);
  assert.equal(outcome.kind, "paid");
  assert.equal(outcome.payment.amount_cents, row.payoff_cents);
  assert.equal(outcome.differs, false, "the figure shown is the figure charged");
  const after = await listed(client, "INV-20402");
  assert.equal(after.status, "PAID");
  assert.equal(after.balance_cents, 0, "the money and the discount close the invoice");
});

test("outside the window, or without terms, the payoff is the open amount and no discount is quoted", async () => {
  const { client } = await signedIn("ap");
  await termsOn("INV-20402", -1);
  const late = await listed(client, "INV-20402");
  assert.equal(late.payoff_cents, late.balance_cents);
  assert.equal("discount_cents" in late, false);
  assert.equal("discount_by" in late, false);
  const plain = await listed(client, "INV-20403");
  assert.equal(plain.payoff_cents, plain.balance_cents);
  const paid = await listed(client, "INV-20401");
  assert.equal("payoff_cents" in paid, false, "a paid invoice is not quoted");
});

test("the pay body carries the shown amount: a stale figure is refused as changed, nothing moves, and the new amount pays", async () => {
  const { client } = await signedIn("ap");
  await termsOn("INV-20402", 3);
  const row = await listed(client, "INV-20402");
  const before = (await tenantPayments.tenantListPayments(client)).value.length;
  const sender = new PaySender(client, { canPay: () => true });
  const stale = await sender.pay({ invoiceIds: [row.id], nonce: "stub-nonce-ok-3" }, row.payoff_cents + row.discount_cents);
  assert.equal(stale.kind, "changed");
  assert.match(stale.words, /amount to pay has changed/i);
  assert.match(stale.words, /No money moved/);
  assert.equal(sender.waiting, null, "a refused-before-claim answer holds nothing");
  assert.equal((await tenantPayments.tenantListPayments(client)).value.length, before, "no payment was made");
  assert.equal((await listed(client, "INV-20402")).balance_cents, row.balance_cents, "the invoice is untouched");
  const paid = await sender.pay({ invoiceIds: [row.id], nonce: "stub-nonce-ok-3" }, row.payoff_cents);
  assert.equal(paid.kind, "paid");
  assert.equal(paid.differs, false);
});

// ---- the stub judges the house day and holds the running cap, as the core does (TM-2b P3-3) -------------------------

const HOUSE_ZONE = "America/Toronto";
const houseParts = (ms) => Object.fromEntries(new Intl.DateTimeFormat("sv-SE", { timeZone: HOUSE_ZONE, year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", hourCycle: "h23", minute: "2-digit" }).formatToParts(new Date(ms)).map((p) => [p.type, p.value]));
const houseDayOf = (ms) => { const p = houseParts(ms); return `${p.year}-${p.month}-${p.day}`; };

/** The next instant, at least an hour from now, at 23:30 in the dealer's zone: the evening, when the UTC day is already tomorrow. */
function houseEvening() {
  let ms = Math.floor(Date.now() / 60_000) * 60_000;
  for (let i = 0; i < 3 * 24 * 60; i++, ms += 60_000) {
    const p = houseParts(ms);
    if (p.hour === "23" && p.minute === "30") return ms;
  }
  throw new Error("no house evening found");
}

async function control(body) {
  const r = await fetch(`${base}/_stub/control`, { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify(body) });
  assert.equal(r.status, 200);
}

test("the window is judged on the dealer's house day: in the house evening a last day of today still quotes the discount", async () => {
  const { client } = await signedIn("ap");
  const evening = houseEvening();
  try {
    await control({ clock: new Date(evening).toISOString() });
    await termsOn("INV-20402", 0);
    const row = await listed(client, "INV-20402");
    assert.equal(row.discount_by, houseDayOf(evening), "today is the house day, not the UTC day");
    assert.ok(row.discount_cents > 0, "the last day is today, so the discount still quotes");
    await control({ clock: new Date(evening + 3_600_000).toISOString() });
    const after = await listed(client, "INV-20402");
    assert.equal("discount_cents" in after, false, "past the house midnight the window is closed");
  } finally {
    await control({ clock: null });
  }
});

test("the running cap holds: the discount is the terms' percent of the open amount, but never past the room earlier receipts left", async () => {
  const { client } = await signedIn("ap");
  await termsOn("INV-20402", 3);
  const free = await listed(client, "INV-20402");
  const cap = Math.round(((free.total_cents - free.holdback_cents) * 200) / 10_000);
  await control({ discount: { invoice_no: "INV-20402", bps: 200, days: 3, taken_cents: cap - 50 } });
  const capped = await listed(client, "INV-20402");
  assert.equal(capped.discount_cents, 50, "only 50 cents of room is left");
  assert.equal(capped.payoff_cents, free.balance_cents - free.holdback_cents - 50);
  await control({ discount: { invoice_no: "INV-20402", bps: 200, days: 3, taken_cents: cap } });
  const spent = await listed(client, "INV-20402");
  assert.equal("discount_cents" in spent, false, "no room left, no discount quoted");
  assert.equal(spent.payoff_cents, free.balance_cents - free.holdback_cents);
});
