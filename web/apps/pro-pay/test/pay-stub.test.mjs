// Pro Pay's money routes against the shared tenant stub, with the real sender: each role meets the route its capability
// holds (the server is the boundary, whatever the screen offers), the sandbox verdicts reach the sender as the right
// outcomes, and a second sender holding the same key gets the original payment back, never a second one.
import { test, before, after, beforeEach } from "node:test";
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { createServer } from "node:net";
import { fileURLToPath } from "node:url";
import { tenantBilling, tenantDashboard, tenantPayments } from "@hardscapehouse/api-client";
import { InMemoryTenantSession, tenantClient, stubAdapter, surfaceOf, can, TENANT_ROLES } from "@hardscapehouse/pro-kit";
import { PERSONAS, STUB_PASSWORD } from "@hardscapehouse/pro-kit/test-stub/tenant-data.mjs";
import { PaySender } from "../src/pay-core.ts";

const stubFile = fileURLToPath(new URL("../../../packages/pro-kit/test-stub/tenant-stub.mjs", import.meta.url));
let child;
let base;

// A free port inside the Pro cycle's own range (this file's block is 56826 to 56829), never an ephemeral one.
async function freePort(start, count) {
  for (let port = start; port < start + count; port += 1) {
    const free = await new Promise((resolve) => {
      const probe = createServer();
      probe.once("error", () => resolve(false));
      probe.listen(port, "127.0.0.1", () => probe.close(() => resolve(true)));
    });
    if (free) return port;
  }
  throw new Error(`no free port in ${start} to ${start + count - 1}`);
}

before(async () => {
  const port = await freePort(56826, 4);
  base = `http://127.0.0.1:${port}`;
  child = spawn(process.execPath, [stubFile], { env: { ROLE_STUB_PORT: String(port), ROLE_STUB_IDLE_EXIT_MS: "120000" }, stdio: "ignore" });
  for (let i = 0; i < 100; i += 1) {
    try {
      if ((await fetch(`${base}/healthz`)).ok) return;
    } catch {
      /* not up yet */
    }
    await new Promise((r) => setTimeout(r, 50));
  }
  throw new Error("the stub did not start");
});

// End the stub by its own door: no signal is ever sent to a process.
after(async () => {
  await fetch(`${base}/_stub/exit`, { method: "POST" }).catch(() => {});
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
  const first = new PaySender(client, { canPay: () => true, newKey: () => "fixed-key-for-this-attempt-0001" });
  const paid = await first.pay({ invoiceIds: ids, nonce: "stub-nonce-ok-7" }, 100);
  assert.equal(paid.kind, "paid");
  // A reload: a new sender, the same key and body (as a retried unknown outcome carries).
  const second = new PaySender(client, { canPay: () => true, newKey: () => "fixed-key-for-this-attempt-0001" });
  const again = await second.pay({ invoiceIds: ids, nonce: "stub-nonce-ok-7" }, 100);
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
