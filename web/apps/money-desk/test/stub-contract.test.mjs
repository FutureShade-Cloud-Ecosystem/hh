// The stub against the contract and against the core, field by field.
//
// A stub that drifts from the server makes every browser leg a flattering
// lie, so this test starts the stub, makes every call the Money Desk's
// receivables slice makes, and holds each answer to the generated wire
// descriptors: required fields present, types and vocabularies right, and no
// field the contract does not declare (the descriptors are open objects, so
// the undeclared field check is this file's own). Then each refusal is held to
// the core's words: the status, the envelope, and the sentence itself, which
// must appear in the core's source. A refusal the stub invented fails here.
import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { readdirSync, readFileSync } from "node:fs";
import { createServer } from "node:net";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { houseDayOffset } from "./house-day.mjs";
import {
  T5018_GOODS_SHARE_PREFIX,
  T5018_GOODS_SHARE_SUFFIX,
  T5018_NOTHING_PAID_NOTE,
  T5018_UNDER_THRESHOLD_NOTE,
} from "./stub/purchasing-world.mjs";
import { SCHEMA_DESCRIPTORS } from "../../../packages/api-client/src/testing.ts";
import { validateValue } from "../../../packages/api-client/test/_validate.mjs";

const appDir = join(dirname(fileURLToPath(import.meta.url)), "..");
const coreInternal = join(appDir, "..", "..", "..", "core", "internal");

const coreSource = ["billing", "payments", "purchasing", "till", join("platform", "authz"), join("platform", "httpx")]
  .flatMap((dir) =>
    readdirSync(join(coreInternal, dir))
      .filter((f) => f.endsWith(".go") && !f.endsWith("_test.go"))
      .map((f) => readFileSync(join(coreInternal, dir, f), "utf8")),
  )
  .join("\n");

let child;
let base;

async function freePort() {
  return new Promise((resolve) => {
    const probe = createServer();
    probe.listen(0, "127.0.0.1", () => {
      const { port } = probe.address();
      probe.close(() => resolve(port));
    });
  });
}

before(async () => {
  const port = await freePort();
  base = `http://127.0.0.1:${port}`;
  child = spawn(process.execPath, [join(appDir, "test", "stub", "money-desk-stub.mjs")], {
    env: { ROLE_STUB_PORT: String(port) },
    stdio: "ignore",
  });
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

after(() => {
  child?.kill();
});

async function call(method, path, body, extraHeaders = {}) {
  const headers = { Authorization: "Bearer stub-token", "X-Org": "hh-demo", ...extraHeaders };
  let payload;
  if (body !== undefined) {
    headers["Content-Type"] = "application/json";
    payload = JSON.stringify(body);
  }
  const res = await fetch(`${base}${path}`, { method, headers, body: payload });
  const text = await res.text();
  let json = null;
  try {
    json = JSON.parse(text);
  } catch {
    /* not json */
  }
  return { status: res.status, json };
}

// A token the stub reads as a named role, minted the way the front door's handoff mints it: the
// token's sub carries the user's id and the user's record carries the role the gates judge.
function tokenFor(sub) {
  const part = (value) => Buffer.from(JSON.stringify(value)).toString("base64url");
  const payload = { iss: "hh-demo-stub", sub, aud: "staff", exp: Math.floor(Date.now() / 1000) + 3600 };
  return `${part({ alg: "ES256", typ: "JWT" })}.${part(payload)}.c3R1Yi1zaWduYXR1cmUtYnl0ZXM`;
}

// The Financial Controller: the money desk at manage may pay a vendor bill.
const CONTROLLER_TOKEN = tokenFor("de110000-0000-4000-9000-000000000008");
// The Payables and Receivables clerk: at work on the money desk, so a manage level route there
// turns the role away.
const CLERK_TOKEN = tokenFor("de110000-0000-4000-9000-000000000007");
// The credit change routes' deciders and a requester that may not decide (CC-2).
const BRANCH_MANAGER_TOKEN = tokenFor("de5b2000-0000-4000-9000-000000000003");
const SALES_MANAGER_TOKEN = tokenFor("de110000-0000-4000-9000-000000000002");
const bearer = (token) => ({ Authorization: `Bearer ${token}` });

const reset = () => fetch(`${base}/_stub/reset`, { method: "POST" });

function declared(d) {
  if (d.k === "ref") return declared(SCHEMA_DESCRIPTORS[d.name]);
  if (d.k === "allOf") return Object.assign({}, ...d.of.map(declared));
  if (d.k === "object") return d.props;
  return {};
}

function undeclared(value, d, path = "value", out = []) {
  if (d.k === "ref") return undeclared(value, SCHEMA_DESCRIPTORS[d.name], path, out);
  if (d.k === "allOf") {
    const props = declared(d);
    if (value !== null && typeof value === "object" && !Array.isArray(value)) {
      for (const key of Object.keys(value)) if (props[key] === undefined) out.push(`${path}.${key}`);
      for (const [key, leg] of Object.entries(props)) if (value[key] !== undefined) undeclared(value[key], leg, `${path}.${key}`, out);
    }
    return out;
  }
  if (d.k === "object") {
    if (value === null || typeof value !== "object" || Array.isArray(value)) return out;
    for (const key of Object.keys(value)) if (d.props[key] === undefined) out.push(`${path}.${key}`);
    for (const [key, leg] of Object.entries(d.props)) if (value[key] !== undefined) undeclared(value[key], leg, `${path}.${key}`, out);
    return out;
  }
  if (d.k === "array" && Array.isArray(value)) value.forEach((v, i) => undeclared(v, d.of, `${path}[${i}]`, out));
  if (d.k === "record" && value !== null && typeof value === "object") for (const [k, v] of Object.entries(value)) undeclared(v, d.of, `${path}.${k}`, out);
  if (d.k === "union") {
    const leg = d.of.find((l) => l.k !== "null");
    if (leg !== undefined && value !== null) undeclared(value, leg, path, out);
  }
  return out;
}

function holds(schemaName, value) {
  const descriptor = SCHEMA_DESCRIPTORS[schemaName];
  assert.ok(descriptor !== undefined, `no descriptor named ${schemaName}`);
  assert.deepEqual(validateValue(value, descriptor), [], `${schemaName} does not fit the contract`);
  assert.deepEqual(undeclared(value, descriptor), [], `${schemaName} carries fields the contract does not declare`);
}

function refused(res, status, fragment) {
  assert.equal(res.status, status);
  holds("Error", res.json);
  assert.ok(res.json.error.includes(fragment), `expected "${fragment}" in "${res.json.error}"`);
  assert.ok(coreSource.includes(fragment), `the core's source does not contain the sentence "${fragment}": the stub invented it`);
}

const today = () => new Intl.DateTimeFormat("en-CA", { timeZone: "America/Toronto", year: "numeric", month: "2-digit", day: "2-digit" }).format(new Date());
// The offset counts the house zone's own calendar days, never UTC days: the
// UTC reading drifts by one across the zone's DST change (test/house-day.mjs).
const later = (days) => houseDayOffset(days);

test("the landing, the aging and the register answer in the contract's shapes", async () => {
  await reset();
  const landing = await call("GET", "/api/v1/ar/landing");
  assert.equal(landing.status, 200);
  holds("BillingARLanding", landing.json);
  assert.equal(landing.json.cards.length, 4);

  const aging = await call("GET", "/api/v1/ar/aging");
  assert.equal(aging.status, 200);
  holds("BillingAgingPage", aging.json);
  assert.ok(aging.json.items.length >= 5);

  const worklist = await call("GET", "/api/v1/billing/ar/worklist");
  assert.equal(worklist.status, 200);
  holds("BillingARWorklistResponse", worklist.json);
  assert.deepEqual(worklist.json.summary, {
    total_past_due_cents: 1_149_950,
    over_60_cents: 697_500,
    broken_promises: 1,
    unapplied_credits_cents: 12_500,
  });
  assert.deepEqual(worklist.json.counts, { all_open: 5, action_needed: 5, over_60: 2, credit_hold: 3 });
  const narrowed = await call("GET", "/api/v1/billing/ar/worklist?chip=over_60&q=Oakline");
  holds("BillingARWorklistResponse", narrowed.json);
  assert.equal(narrowed.json.rows.length, 1);
  assert.equal(narrowed.json.rows[0].account_no, "OB-200");
  assert.deepEqual(narrowed.json.summary, worklist.json.summary);

  const register = await call("GET", `/api/v1/ar/register?customer_id=${aging.json.items[0].customer_id}`);
  assert.equal(register.status, 200);
  holds("BillingRegisterPage", register.json);
  assert.ok(register.json.items.every((r) => r.customer_id === aging.json.items[0].customer_id));

  const one = await call("GET", `/api/v1/ar/register/invoices/${register.json.items[0].id}`);
  assert.equal(one.status, 200);
  holds("BillingInvoiceDetail", one.json);
});

test("the register reads its chip as the core does: no chip is every invoice document (drafts and voids too), PAID the paid ones only, unpaid first, an unknown chip reads as none", async () => {
  await reset();
  const aging = await call("GET", "/api/v1/ar/aging");
  const customer = aging.json.items[0].customer_id;
  const every = await call("GET", `/api/v1/ar/register?customer_id=${customer}`);
  const paid = await call("GET", `/api/v1/ar/register?customer_id=${customer}&chip=PAID`);
  const all = await call("GET", `/api/v1/ar/register?customer_id=${customer}&chip=all`);
  assert.ok(paid.json.items.length > 0 && paid.json.items.every((r) => r.stored_status === "PAID"));
  assert.ok(all.json.items.length > 0 && all.json.items.every((r) => r.stored_status === "FINALIZED" || r.stored_status === "PARTIALLY_PAID"));
  const drafts = every.json.items.filter((r) => r.stored_status === "DRAFT");
  const voids = every.json.items.filter((r) => r.stored_status === "VOID");
  assert.equal(drafts.length, 1, "the no chip read carries a draft, as the core's does");
  assert.equal(voids.length, 1, "the no chip read carries a void, as the core's does");
  assert.equal(every.json.items.length, paid.json.items.length + all.json.items.length + 2);
  assert.ok(every.json.items.every((r) => r.doc_type === "INVOICE"), "credit memos are left out without show_credits");
  const withCredits = await call("GET", `/api/v1/ar/register?customer_id=${customer}&show_credits=1`);
  assert.equal(withCredits.json.items.filter((r) => r.doc_type === "CREDIT_MEMO").length, 1);
  const rank = (r) => (r.stored_status === "FINALIZED" || r.stored_status === "PARTIALLY_PAID" ? 0 : 1);
  const order = every.json.items.map(rank);
  assert.deepEqual(order, [...order].sort((x, y) => x - y), "unpaid documents come ahead of the rest");
  const paged = await call("GET", `/api/v1/ar/register?customer_id=${customer}&limit=1&offset=1`);
  assert.equal(paged.json.items.length, 1);
  assert.equal(paged.json.items[0].id, every.json.items[1].id);
  const unknown = await call("GET", `/api/v1/ar/register?customer_id=${customer}&chip=nonsense`);
  assert.equal(unknown.status, 200);
  assert.deepEqual(unknown.json.items.map((r) => r.id), every.json.items.map((r) => r.id));
});

test("a payment applies to an invoice and the answer is the contract's receipt", async () => {
  await reset();
  const register = await call("GET", "/api/v1/ar/register?chip=all");
  const overdue = register.json.items.find((r) => r.status === "OVERDUE");
  const first = await call("POST", `/api/v1/ar/register/invoices/${overdue.id}/payments`, {
    method: "CHEQUE",
    amount_cents: 1000,
    reference: "stub",
    idempotency_key: "k-1",
  });
  assert.equal(first.status, 200);
  holds("BillingReceiptResult", first.json);
  assert.equal(first.json.balance_cents, overdue.balance_cents - 1000);
  assert.equal(first.json.replayed, false);

  const again = await call("POST", `/api/v1/ar/register/invoices/${overdue.id}/payments`, {
    method: "CHEQUE",
    amount_cents: 1000,
    reference: "stub",
    idempotency_key: "k-1",
  });
  assert.equal(again.json.replayed, true);
  assert.equal(again.json.balance_cents, first.json.balance_cents);

  const list = await call("GET", `/api/v1/ar/invoices/${overdue.id}/payments`);
  holds("TillInvoicePayments", list.json);
  assert.equal(list.json.items.length, 1);
});

test("the payment refusals are the core's sentences, in the core's order", async () => {
  await reset();
  const register = await call("GET", "/api/v1/ar/register?chip=all");
  const inv = register.json.items[0];
  const path = `/api/v1/ar/register/invoices/${inv.id}/payments`;
  refused(await call("POST", path, { method: "CASH", amount_cents: 100, reference: "", idempotency_key: "" }), 400, "recording a payment needs an idempotency key");
  refused(await call("POST", path, { method: "CASH", amount_cents: 0, reference: "", idempotency_key: "k" }), 400, "a payment amount must be greater than zero");
  refused(await call("POST", path, { method: "CASH", amount_cents: inv.balance_cents + 1, reference: "", idempotency_key: "k2" }), 400, "this invoice can take at most");
});

test("a promise to pay, and the refusals the core words", async () => {
  await reset();
  const register = await call("GET", "/api/v1/ar/register?chip=all");
  const inv = register.json.items.find((r) => r.promised_date === undefined && !r.disputed);
  const path = `/api/v1/ar/register/invoices/${inv.id}/promise`;
  const good = { promised_cents: 5000, promised_date: later(7), promised_by: "Accounts payable", note: "Cheque in the mail", idempotency_key: "p-1" };
  refused(await call("POST", path, { ...good, idempotency_key: "" }), 400, "recording a promise needs an idempotency key");
  refused(await call("POST", path, { ...good, promised_cents: 0 }), 400, "a promise names an amount greater than zero");
  refused(await call("POST", path, { ...good, promised_date: "soon" }), 400, "the promised date must be a calendar date (YYYY-MM-DD)");
  refused(await call("POST", path, { ...good, promised_date: later(-3) }), 400, "the promised date is already past");
  refused(await call("POST", path, { ...good, promised_cents: inv.balance_cents + 1 }), 400, "a promise cannot exceed the outstanding balance");
  const made = await call("POST", path, good);
  assert.equal(made.status, 200);
  holds("BillingPromiseView", made.json);
  const second = await call("POST", path, { ...good, idempotency_key: "p-2" });
  assert.equal(second.status, 409);
  assert.ok(coreSource.includes("this invoice already carries a live promise"));
  assert.ok(second.json.error.includes("this invoice already carries a live promise"));
  holds("BillingPromiseView", second.json.promise);
  assert.equal(today() <= good.promised_date, true);
});

test("the bounced queue, marking a payment bounced, and its replay", async () => {
  await reset();
  const queue = await call("GET", "/api/v1/ar/bounced");
  assert.equal(queue.status, 200);
  holds("TillBouncedPaymentPage", queue.json);
  assert.equal(queue.json.items.length, 1);

  const register = await call("GET", "/api/v1/ar/register?chip=all");
  const inv = register.json.items.find((r) => r.status === "OVERDUE" && r.account_no !== "GE-100");
  const paid = await call("POST", `/api/v1/ar/register/invoices/${inv.id}/payments`, { method: "CHEQUE", amount_cents: 2500, reference: "r", idempotency_key: "n-1" });
  const marked = await call("POST", `/api/v1/ar/payments/${paid.json.payment_id}/nsf`, { reason: "Cheque returned NSF" });
  assert.equal(marked.status, 200);
  holds("TillNSFResult", marked.json);
  assert.equal(marked.json.reversed_cents, 2500);

  const after = await call("GET", "/api/v1/ar/bounced");
  assert.equal(after.json.items.length, 2);
  const history = await call("GET", `/api/v1/ar/payments/${paid.json.payment_id}/history`);
  holds("TillPaymentHistory", history.json);

  refused(await call("POST", `/api/v1/ar/payments/${paid.json.payment_id}/nsf`, { reason: "again" }), 409, "payment already marked BOUNCED");
});

test("credit holds read, and the release is refused with the gate's shared body", async () => {
  await reset();
  const list = await call("GET", "/api/v1/ar/holds?status=ACTIVE");
  assert.equal(list.status, 200);
  holds("BillingCreditHoldPage", list.json);
  assert.equal(list.json.items.length, 3);
  const release = await call("POST", `/api/v1/ar/holds/${list.json.items[0].id}/ar-release`, { reason: "paid" });
  assert.equal(release.status, 403);
  holds("Error", release.json);
  assert.equal(release.json.error, "Forbidden");
  assert.ok(coreSource.includes('{"error":"Forbidden"}'));
});

test("a statement is run for an account and read back", async () => {
  await reset();
  const aging = await call("GET", "/api/v1/ar/aging");
  const account = aging.json.items[0];
  const none = await call("GET", `/api/v1/ar/customers/${account.customer_id}/statement`);
  assert.equal(none.status, 404);
  const register = await call("GET", `/api/v1/ar/register?customer_id=${account.customer_id}&chip=all`);
  const run = await call("POST", "/api/v1/ar/register/statements", { invoice_ids: register.json.items.map((r) => r.id), idempotency_key: "s-1" });
  assert.equal(run.status, 200);
  holds("BillingStatementBatch", run.json);
  const got = await call("GET", `/api/v1/ar/customers/${account.customer_id}/statement`);
  assert.equal(got.status, 200);
  holds("BillingStatement", got.json);
  assert.equal(got.json.closing_balance_cents, account.total_cents);
});

const asController = async (method, path, body) => {
  const part = (v) => Buffer.from(JSON.stringify(v)).toString("base64url");
  const token = `${part({ alg: "ES256" })}.${part({ sub: "de110000-0000-4000-9000-000000000008" })}.c2ln`;
  const res = await fetch(`${base}${path}`, { method, headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" }, body: body === undefined ? undefined : JSON.stringify(body) });
  return { status: res.status, json: await res.json() };
};

test("the controller lifts or overrides a hold and runs the sweep; every other role is refused with the shared body", async () => {
  await reset();
  const list = await call("GET", "/api/v1/ar/holds");
  const [first, second] = list.json.items;
  for (const path of [`/api/v1/ar/holds/${first.id}/override`, "/api/v1/ar/credit-sweep"]) {
    const refusedRes = await call("POST", path, { reason: "x" });
    assert.equal(refusedRes.status, 403);
    assert.equal(refusedRes.json.error, "Forbidden");
  }
  const over = await asController("POST", `/api/v1/ar/holds/${first.id}/override`, { reason: "Deposit is coming" });
  assert.equal(over.status, 200);
  holds("BillingHoldOverrideResult", over.json);
  const lifted = await asController("POST", `/api/v1/ar/holds/${second.id}/ar-release`, { reason: "Paid" });
  assert.equal(lifted.status, 200);
  const active = await call("GET", "/api/v1/ar/holds?status=ACTIVE");
  assert.equal(active.json.items.length, 2);
  const sweep = await asController("POST", "/api/v1/ar/credit-sweep");
  holds("BillingEvaluationResult", sweep.json);
  const everyHold = await call("GET", "/api/v1/ar/holds");
  holds("BillingCreditHoldPage", everyHold.json);
});

test("branches, the register selection, reminders, statement runs, the export and the landing's paper path", async () => {
  await reset();
  const branches = await call("GET", "/api/v1/branches");
  assert.equal(branches.status, 200);
  holds("Branch", branches.json[0]);
  const register = await call("GET", "/api/v1/ar/register?chip=all");
  const ids = register.json.items.slice(0, 2).map((r) => r.id);
  const selection = await call("POST", "/api/v1/ar/register/selection", { invoice_ids: ids });
  holds("BillingRegisterSelectionResult", selection.json);
  assert.equal(selection.json.count, 2);
  const remind = await call("POST", "/api/v1/ar/register/reminders", { invoice_ids: ids, tone: "FRIENDLY", idempotency_key: "r-1" });
  holds("BillingReminderResult", remind.json);
  refused(await call("POST", "/api/v1/ar/register/reminders", { invoice_ids: [], tone: "FRIENDLY", idempotency_key: "r-2" }), 400, "choose at least one invoice to chase");
  const run = await call("POST", "/api/v1/ar/statement-runs", { mode: "MASS" });
  holds("BillingStatementRunResult", run.json);
  assert.equal(run.json.run.generated_count, 5);
  const csv = await fetch(`${base}/api/v1/ar/register/export`, { headers: { Authorization: "Bearer x" } });
  assert.equal(csv.status, 200);
  assert.ok((await csv.text()).startsWith("Invoice,"));
  const landing = await call("GET", "/api/v1/ar/landing");
  assert.equal(landing.json.paper_path.length, 1);
  const disputed = register.json.items.find((r) => r.disputed);
  const one = await call("GET", `/api/v1/ar/register/invoices/${disputed.id}`);
  holds("BillingInvoiceDetail", one.json);
  assert.equal(one.json.dispute.status, "OPEN");
});

// The received payment route (POST /api/v1/ar/payments/apply, operation paymentsApplyReceived), held to the
// generated descriptors and to the sentences of the core's payments and billing packages. The route's rules, read
// at source: every refusal is a 422 in words except an invoice that is not the account's, outside the caller's
// reach or missing, which is a 404 "no such invoice: <id>"; the split is the server's, oldest first (issue date,
// due date, invoice number, id) whatever order the list came in; the answer carries one line per named invoice,
// oldest first; unapplied is always zero; a preview never reads the key; a replayed key answers the first result
// when the body is the same and is refused in words when it is not.
const APPLY = "/api/v1/ar/payments/apply";

async function graniteInvoices() {
  const register = await call("GET", "/api/v1/ar/register?chip=all");
  const mine = register.json.items.filter((r) => r.account_no === "GE-100");
  return mine.sort((a, b) => a.issue_date.localeCompare(b.issue_date) || a.due_date.localeCompare(b.due_date) || a.invoice_no.localeCompare(b.invoice_no));
}

test("apply route: the preview splits oldest first whatever the list order and writes nothing", async () => {
  await reset();
  const mine = await graniteInvoices();
  const oldest = mine[0];
  const youngest = mine[mine.length - 1];
  const body = { customer_id: oldest.customer_id, method: "CHEQUE", amount_cents: 500000, reference: "c", invoice_ids: [youngest.id, oldest.id], idempotency_key: "p-1", preview: true };
  const preview = await call("POST", APPLY, body);
  assert.equal(preview.status, 200);
  holds("BillingApplyPaymentAnswer", preview.json);
  assert.equal(preview.json.preview, true);
  assert.equal(preview.json.payment_no, undefined);
  assert.equal(preview.json.unapplied_cents, 0);
  assert.equal(preview.json.on_account_cents, 0);
  assert.deepEqual(preview.json.lines.map((l) => l.invoice_id), [oldest.id, youngest.id], "the server's split is oldest first, not the list's order");
  assert.deepEqual(preview.json.lines.map((l) => l.outcome), ["CLOSES", "PART"]);
  assert.equal(preview.json.lines[0].applied_cents, oldest.balance_cents);
  const untouched = await call("GET", `/api/v1/ar/register/invoices/${oldest.id}`);
  assert.equal(untouched.json.totals.balance_cents, oldest.balance_cents);
  // A preview never reads the key: the same key still applies for real afterwards.
  const posted = await call("POST", APPLY, { ...body, preview: false });
  assert.equal(posted.status, 200);
  assert.equal(posted.json.replayed, false);
  // And a preview under a SPENT key answers as a preview, never as the key's replay: the
  // final route's preview path returns before the replay fast path is ever reached, so the
  // key is never read and the split is judged on the live balances whatever body it rode in
  // on (the core's APreviewUnderASpentKeyAnswersAsAPreview leg holds the same walk there).
  const part = { customer_id: youngest.customer_id, method: "CHEQUE", amount_cents: 10000, reference: "spent", invoice_ids: [youngest.id], idempotency_key: "p-2", preview: false };
  const spent = await call("POST", APPLY, part);
  assert.equal(spent.status, 200);
  assert.equal(spent.json.lines[0].outcome, "PART", "the real apply leaves the invoice open for the previews below");
  const spentPreview = await call("POST", APPLY, { ...part, preview: true });
  assert.equal(spentPreview.status, 200);
  assert.equal(spentPreview.json.preview, true);
  assert.equal(spentPreview.json.replayed, false);
  assert.equal(spentPreview.json.payment_no, undefined);
  assert.equal(spentPreview.json.lines[0].applied_cents, 10000, "the same body previews its own split on the live balance");
  const otherPreview = await call("POST", APPLY, { ...part, amount_cents: 5000, method: "EFT", preview: true });
  assert.equal(otherPreview.status, 200);
  assert.equal(otherPreview.json.preview, true);
  assert.equal(otherPreview.json.replayed, false);
  assert.equal(otherPreview.json.lines[0].applied_cents, 5000, "another body under the spent key previews its own split too");
});

test("apply route: the post is one payment, a retry with the same body answers the first result and posts once", async () => {
  await reset();
  const mine = await graniteInvoices();
  const ids = mine.map((r) => r.id);
  const body = { customer_id: mine[0].customer_id, method: "CHEQUE", amount_cents: 500000, reference: "c", invoice_ids: ids, idempotency_key: "a-1", preview: false };
  const posted = await call("POST", APPLY, body);
  assert.equal(posted.status, 200);
  holds("BillingApplyPaymentAnswer", posted.json);
  assert.ok(posted.json.payment_no);
  assert.equal(posted.json.replayed, false);
  const again = await call("POST", APPLY, { ...body, invoice_ids: [...ids].reverse() });
  assert.equal(again.status, 200);
  holds("BillingApplyPaymentAnswer", again.json);
  assert.equal(again.json.replayed, true);
  assert.equal(again.json.payment_no, posted.json.payment_no);
  const one = await call("GET", `/api/v1/ar/invoices/${ids[1]}/payments`);
  assert.equal(one.json.items.filter((p) => p.payment_no === posted.json.payment_no).length, 1);
  const after = await call("GET", `/api/v1/ar/register/invoices/${mine[0].id}`);
  assert.equal(after.json.totals.balance_cents, 0);
});

test("apply route: a replayed key is judged against its own body", async () => {
  await reset();
  const mine = await graniteInvoices();
  const other = (await call("GET", "/api/v1/ar/register?chip=all")).json.items.find((r) => r.account_no === "OB-200");
  const body = { customer_id: mine[0].customer_id, method: "CHEQUE", amount_cents: 1000, reference: "c", invoice_ids: [mine[0].id], idempotency_key: "r-1", preview: false };
  assert.equal((await call("POST", APPLY, body)).status, 200);
  refused(await call("POST", APPLY, { ...body, amount_cents: 2000 }), 409, "that idempotency key already recorded a different payment; use a new key");
  refused(await call("POST", APPLY, { ...body, reference: "d" }), 409, "that idempotency key already recorded a different payment; use a new key");
  refused(await call("POST", APPLY, { ...body, method: "CASH" }), 409, "that idempotency key already recorded a different payment; use a new key");
  refused(await call("POST", APPLY, { ...body, customer_id: other.customer_id, invoice_ids: [other.id] }), 409, "that idempotency key already recorded a payment for another account; use a new key");
  // The replay judgement runs before the body validation (FP 186 and 187): a body validateApply
  // would refuse on a fresh key hears the spent key's 409 instead.
  refused(await call("POST", APPLY, { ...body, amount_cents: 0 }), 409, "that idempotency key already recorded a different payment; use a new key");
  refused(await call("POST", APPLY, { ...body, invoice_ids: [] }), 409, "that idempotency key already recorded a different payment; use a new key");
  const still = await call("GET", `/api/v1/ar/register/invoices/${mine[0].id}`);
  assert.equal(still.json.totals.balance_cents, mine[0].balance_cents - 1000);
});

test("apply route: the split subtracts the retained holdback and answers any excess as on_account_cents, as the core's walk does", async () => {
  await reset();
  const register = await call("GET", "/api/v1/ar/register?chip=all");
  const held = register.json.items.find((r) => r.invoice_no === "INV-1008");
  assert.equal(held.holdback_cents, 30000, "the world plants a retained holdback on this invoice");
  const detail = await call("GET", `/api/v1/ar/register/invoices/${held.id}`);
  assert.equal(detail.json.totals.holdback_cents, 30000);
  assert.equal(detail.json.totals.payable_cents, held.balance_cents - 30000, "payable is the balance less the retained holdback");
  const payable = held.balance_cents - 30000;
  const body = { customer_id: held.customer_id, method: "CHEQUE", reference: "c", invoice_ids: [held.id], idempotency_key: "h-1", preview: false };
  // The walk counts the payable, never the balance, and leaves any excess on the account as a credit.
  const overPreview = await call("POST", APPLY, { ...body, amount_cents: payable + 1, preview: true });
  assert.equal(overPreview.status, 200);
  holds("BillingApplyPaymentAnswer", overPreview.json);
  assert.equal(overPreview.json.applied_cents, payable);
  assert.equal(overPreview.json.unapplied_cents, 1);
  assert.equal(overPreview.json.on_account_cents, 1);
  // The full payable leaves the retained holdback owing: the line stays PART, with nothing further payable on the invoice, and any excess goes to on_account_cents.
  const posted = await call("POST", APPLY, { ...body, amount_cents: payable + 2500 });
  assert.equal(posted.status, 200);
  holds("BillingApplyPaymentAnswer", posted.json);
  assert.equal(posted.json.applied_cents, payable);
  assert.equal(posted.json.unapplied_cents, 2500);
  assert.equal(posted.json.on_account_cents, 2500);
  const line = posted.json.lines[0];
  assert.equal(line.applied_cents, payable);
  assert.equal(line.outcome, "PART");
  assert.equal(line.balance_after_cents, 30000);
  assert.equal(line.stays_open_cents, 0);
  const spent = await call("POST", APPLY, { ...body, idempotency_key: "h-2", amount_cents: 100 });
  assert.equal(spent.status, 200);
  holds("BillingApplyPaymentAnswer", spent.json);
  assert.equal(spent.json.applied_cents, 0);
  assert.equal(spent.json.unapplied_cents, 100);
  assert.equal(spent.json.on_account_cents, 100);
  assert.equal(spent.json.lines[0].applied_cents, 0);
  assert.equal(spent.json.lines[0].outcome, "NONE");
  assert.equal(spent.json.lines[0].balance_after_cents, 30000);
  assert.equal(spent.json.lines[0].stays_open_cents, 0);
});

test("apply route: refusals are the core's words and leave nothing applied", async () => {
  await reset();
  const mine = await graniteInvoices();
  const register = await call("GET", "/api/v1/ar/register?chip=all");
  const other = register.json.items.find((r) => r.account_no === "OB-200");
  const base = { customer_id: mine[0].customer_id, method: "CASH", reference: "c", idempotency_key: "x-1", preview: false, amount_cents: 1000, invoice_ids: [mine[0].id] };
  refused(await call("POST", APPLY, { ...base, idempotency_key: "" }), 422, "recording a payment needs an idempotency key");
  refused(await call("POST", APPLY, { ...base, amount_cents: 0 }), 422, "a payment amount must be greater than zero");
  refused(await call("POST", APPLY, { ...base, invoice_ids: [] }), 422, "a payment must be applied to at least one invoice");
  const tender = await call("POST", APPLY, { ...base, method: "BARTER" });
  refused(tender, 422, "tender must be one of ");
  assert.equal(tender.json.error, "tender must be one of EFT, CHEQUE, CARD, CASH");
  refused(await call("POST", APPLY, { ...base, customer_id: "nope" }), 422, "customer id must be a uuid");
  refused(await call("POST", APPLY, { ...base, invoice_ids: ["nope"] }), 422, "invoice ids must be uuids");
  refused(await call("POST", APPLY, { ...base, invoice_ids: [mine[0].id, mine[0].id] }), 422, "the same invoice is named twice: ");
  const foreign = await call("POST", APPLY, { ...base, invoice_ids: [mine[0].id, other.id] });
  refused(foreign, 404, "no such invoice: ");
  assert.ok(foreign.json.error.endsWith(other.id));
  const after = await call("GET", `/api/v1/ar/register/invoices/${mine[0].id}`);
  assert.equal(after.json.totals.balance_cents, mine[0].balance_cents);
  // An overpayment closes the invoice and answers the excess as on_account_cents, with no refusal.
  const over = await call("POST", APPLY, { ...base, amount_cents: mine[0].balance_cents + 4000 });
  assert.equal(over.status, 200);
  holds("BillingApplyPaymentAnswer", over.json);
  assert.equal(over.json.applied_cents, mine[0].balance_cents);
  assert.equal(over.json.unapplied_cents, 4000);
  assert.equal(over.json.on_account_cents, 4000);
  // A paid invoice is not open.
  const closed = await call("POST", APPLY, { ...base, idempotency_key: "x-2" });
  refused(closed, 422, "is not open; a payment applies to a finalized or partially paid invoice");
  assert.ok(closed.json.error.startsWith(`invoice ${mine[0].invoice_no} (paid) is not open`));
});

test("a body that is not JSON is the core's 400 on every route that reads one", async () => {
  await reset();
  for (const path of ["/api/v1/ar/payments/apply", "/api/v1/ar/register/statements"]) {
    const res = await fetch(`${base}${path}`, { method: "POST", headers: { Authorization: "Bearer stub-token", "X-Org": "hh-demo", "Content-Type": "application/json" }, body: "{not json" });
    assert.equal(res.status, 400, path);
    const json = await res.json();
    assert.ok(JSON.stringify(json).includes("the request body must be JSON"), path);
  }
  assert.ok(coreSource.includes("the request body must be JSON"));
});

test("the T5018 report and its CSV answer in the contract's shape and match the core's CSV columns, header line and route answer", async () => {
  await reset();
  const t5018Go = readFileSync(join(coreInternal, "purchasing", "t5018_report.go"), "utf8");
  const colBlock = t5018Go.match(/t5018CSVColumns\s*=\s*\[\]string\{([^}]+)\}/);
  assert.ok(colBlock !== null, "the core's t5018_report.go must declare t5018CSVColumns");
  const coreCols = [...colBlock[1].matchAll(/"([^"]+)"/g)].map((m) => m[1]);
  assert.ok(coreCols.includes("box_22_cents") && coreCols.includes("services_subtotal_cents") && coreCols.includes("under_threshold") && coreCols.includes("threshold_note") && coreCols.includes("unmarked_lines_note"));

  assert.ok(t5018Go.includes("out[idx].PaidCents += paidCents"), "the core must accumulate every payment into PaidCents");
  assert.ok(t5018Go.includes("report.TotalCents += row.PaidCents"), "the core must sum PaidCents into TotalCents");
  assert.ok(!t5018Go.includes("row.PaidCents = row.Box22Cents"), "the core must not overwrite PaidCents with Box22Cents");

  const coreConst = (name) => {
    const m = t5018Go.match(new RegExp(`const\\s+${name}\\s*=\\s*"([^"]+)"`));
    assert.ok(m !== null, `the core's t5018_report.go must declare ${name}`);
    return m[1];
  };
  assert.equal(T5018_NOTHING_PAID_NOTE, coreConst("T5018NothingPaidNote"), "the stub's nothing-paid words must match the core's T5018NothingPaidNote");
  assert.equal(T5018_UNDER_THRESHOLD_NOTE, coreConst("T5018UnderThresholdNote"), "the stub's under-threshold tail must match the core's T5018UnderThresholdNote");
  assert.equal(T5018_GOODS_SHARE_PREFIX, coreConst("T5018GoodsSharePrefix"), "the stub's goods-share prefix must match the core's T5018GoodsSharePrefix");
  assert.equal(T5018_GOODS_SHARE_SUFFIX, coreConst("T5018GoodsShareSuffix"), "the stub's goods-share suffix must match the core's T5018GoodsShareSuffix");
  assert.ok(
    t5018Go.includes('fmt.Sprintf("Services before tax %s: %s", t5018FormatDollars(servicesSubtotalCents), T5018UnderThresholdNote)'),
    "the core must format the under-threshold note with Services before tax and the pre-tax figure",
  );
  assert.ok(
    t5018Go.includes('fmt.Sprintf("%s %s %s", T5018GoodsSharePrefix, t5018FormatDollars(goodsShareCents), T5018GoodsShareSuffix)'),
    "the core must format the mixed-bill goods share note with T5018GoodsSharePrefix, the goods figure and T5018GoodsShareSuffix",
  );

  const res = await call("GET", "/api/v1/purchasing/t5018?year=2026");
  assert.equal(res.status, 200);
  holds("PurchasingT5018Report", res.json);
  assert.ok(t5018Go.includes(`"${res.json.rule_note}"`), "the report's rule_note must come from the core's source");
  assert.equal(res.json.items.length, 3);
  const mixed = res.json.items.find((r) => r.paid_cents !== r.box_22_cents);
  assert.ok(mixed !== undefined, "the stub's mixed subcontractor row shows Paid in <year> and Box 22 apart");
  assert.equal(mixed.paid_cents - mixed.box_22_cents, 20_950, "Paid and Box 22 on the mixed bill differ by the goods share");
  assert.equal(
    mixed.threshold_note,
    "Box 22 counts services only; $209.50 of goods is not counted.",
    "Meadowlark's threshold_note names the $209.50 goods share excluded from Box 22",
  );
  assert.equal(res.json.total_cents - res.json.box_22_total_cents, 20_950);
  const under = res.json.items.find((r) => r.under_threshold);
  assert.ok(under !== undefined, "the stub's report includes a vendor under the $500 pre-tax threshold");
  assert.equal(
    under.threshold_note,
    "Services before tax $480.00: under the $500 slip threshold, no slip.",
    "the under-threshold vendor's threshold_note names the $480.00 pre-tax services figure and the $500 slip threshold",
  );

  const csvRes = await fetch(`${base}/api/v1/purchasing/t5018.csv?year=2026`, { headers: { Authorization: "Bearer stub-token", "X-Org": "hh-demo" } });
  assert.equal(csvRes.status, 200);
  const text = await csvRes.text();
  const lines = text.trimEnd().split("\n");
  const csvQuote = (v) => {
    const s = String(v);
    return /[",\n]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s;
  };
  assert.equal(lines[0], csvQuote(res.json.rule_note), "the CSV opens with the core's construction income sentence as a header line");
  assert.equal(lines[1], coreCols.join(","), "the CSV column header matches the core's t5018CSVColumns");
  res.json.items.forEach((row, i) => {
    const expectedRow = [
      row.vendor_no,
      row.name,
      row.business_number_missing ? "(missing)" : row.business_number,
      String(row.paid_cents),
      String(row.box_22_cents),
      String(row.services_subtotal_cents),
      row.under_threshold ? "true" : "false",
      row.threshold_note ?? "",
      String(row.unmarked_line_count),
      row.unmarked_lines_note ?? "",
    ].map(csvQuote).join(",");
    assert.equal(lines[2 + i], expectedRow, `CSV row ${i} matches the route answer`);
  });
  assert.equal(lines[2 + res.json.items.length], "");
  assert.equal(
    lines[3 + res.json.items.length],
    ["Total", "", "", String(res.json.total_cents), String(res.json.box_22_total_cents), "", "", "", "", ""].join(","),
  );
});

// The open AP the Payables board reads, and the payment that lowers it: the core's purchasing
// routes. The stub answers them in the contract's shapes, and every refusal is the core's own
// sentence.
const PAY_BILL = "/api/v1/purchasing/bills/b0000000-0000-4000-8000-000000000006/payments";
const PAY_BILL_OTHER = "/api/v1/purchasing/bills/b0000000-0000-4000-8000-000000000007/payments";

test("the open AP and the bill payment answer in the contract's shapes, and the refusals are the core's own sentences", async () => {
  await reset();
  const aging = await call("GET", "/api/v1/purchasing/ap-aging");
  assert.equal(aging.status, 200);
  holds("PurchasingAPAgingPage", aging.json);

  // The payment: the key rides the Idempotency-Key header, one key per press.
  const body = { amount_cents: 10_000, paid_at: `${today()}T12:00:00Z`, note: "cheque 41" };
  const paid = await call("POST", PAY_BILL, body, { Authorization: `Bearer ${CONTROLLER_TOKEN}`, "Idempotency-Key": "contract-leg-key" });
  assert.equal(paid.status, 200);
  holds("PurchasingBillDetail", paid.json);
  // The books moved by what was paid, and by nothing else.
  const after = await call("GET", "/api/v1/purchasing/ap-aging");
  assert.equal(after.json.total_cents, aging.json.total_cents - 10_000);

  // The same key again, the way a retry after a lost answer sends it: the first payment is
  // answered and nothing is written.
  const replay = await call("POST", PAY_BILL, body, { Authorization: `Bearer ${CONTROLLER_TOKEN}`, "Idempotency-Key": "contract-leg-key" });
  assert.equal(replay.status, 200);
  holds("PurchasingBillDetail", replay.json);
  const again = await call("GET", "/api/v1/purchasing/ap-aging");
  assert.equal(again.json.total_cents, after.json.total_cents);

  // A press with no key at all is refused in the core's words.
  refused(await call("POST", PAY_BILL, body, { Authorization: `Bearer ${CONTROLLER_TOKEN}` }), 400, "idempotency_key is required on a bill payment");
  // An amount of nothing is refused in the core's words.
  refused(await call("POST", PAY_BILL, { ...body, amount_cents: 0 }, { Authorization: `Bearer ${CONTROLLER_TOKEN}`, "Idempotency-Key": "nothing-key" }), 400, "payment amount_cents must be positive");
  // A key spent on another bill is a conflict.
  refused(await call("POST", PAY_BILL_OTHER, body, { Authorization: `Bearer ${CONTROLLER_TOKEN}`, "Idempotency-Key": "contract-leg-key" }), 409, "idempotency key already used for another bill's payment");
  // An amount above the open balance is refused in the core's words.
  refused(await call("POST", PAY_BILL, { ...body, amount_cents: 99_999_999 }, { Authorization: `Bearer ${CONTROLLER_TOKEN}`, "Idempotency-Key": "over-key" }), 409, "exceeds open balance");
  // The core judges a payment against the sum of the bill's PAID instalments, every one of them,
  // whatever day the instalment carries: a payment dated ahead of today takes the room it needs
  // now, even though the board, read as of today, still shows the bill open until that day.
  const dated = await call("POST", PAY_BILL_OTHER, { ...body, amount_cents: 20_000, paid_at: `${later(1)}T12:00:00Z` }, { Authorization: `Bearer ${CONTROLLER_TOKEN}`, "Idempotency-Key": "dated-key" });
  assert.equal(dated.status, 200);
  const datedOver = await call("POST", PAY_BILL_OTHER, { ...body, amount_cents: 20_000, paid_at: `${later(1)}T12:00:00Z` }, { Authorization: `Bearer ${CONTROLLER_TOKEN}`, "Idempotency-Key": "dated-second-key" });
  assert.equal(datedOver.status, 409);
  assert.equal(datedOver.json.error, "purchasing: payment of 20000 cents exceeds open balance 11500 cents on bill B-4108");
  // The role gate is the core's own: the route is a manage level route on the money desk and the
  // Payables and Receivables role is at work on that surface.
  const clerk = await call("POST", PAY_BILL, body, { Authorization: `Bearer ${CLERK_TOKEN}`, "Idempotency-Key": "clerk-key" });
  assert.equal(clerk.status, 403);
});

test("the credit change routes answer in the contract's shapes and the refusals are the core's own sentences", async () => {
  await reset();
  const aging = await call("GET", "/api/v1/ar/aging");
  const customer = aging.json.items[0].customer_id;

  // The gate is the manager tiers; a caller the stub does not know is turned away with the shared body.
  const unknown = await call("GET", "/api/v1/ar/credit-changes", undefined, bearer(tokenFor("00000000-0000-4000-9000-000000000000")));
  refused(unknown, 403, "Forbidden");

  const waiting = await call("GET", "/api/v1/ar/credit-changes?status=PENDING", undefined, bearer(CONTROLLER_TOKEN));
  assert.equal(waiting.status, 200);
  holds("BillingCreditChangePage", waiting.json);
  assert.ok(waiting.json.items.length >= 2);
  assert.ok(waiting.json.items.every((r) => r.status === "PENDING"));

  // A decrease applies at once; an increase waits, and the answer says which tier it needs.
  const down = await call("POST", "/api/v1/ar/credit-changes", { customer_id: customer, new_limit_cents: 0, reason: "Past due" }, bearer(SALES_MANAGER_TOKEN));
  assert.equal(down.status, 201);
  holds("BillingCreditChangeRequest", down.json);
  assert.equal(down.json.status, "APPLIED");
  const up = await call("POST", "/api/v1/ar/credit-changes", { customer_id: customer, new_limit_cents: 2_000_000, reason: "Winter work" }, bearer(SALES_MANAGER_TOKEN));
  assert.equal(up.status, 201);
  holds("BillingCreditChangeRequest", up.json);
  assert.equal(up.json.status, "PENDING");
  assert.equal(up.json.required_approvals, 1);
  const noReason = await call("POST", "/api/v1/ar/credit-changes", { customer_id: customer, new_limit_cents: 10, reason: " " }, bearer(SALES_MANAGER_TOKEN));
  refused(noReason, 400, "credit change: a reason is required");

  // The refusals of a decision, in the core's order and words.
  refused(await call("POST", `/api/v1/ar/credit-changes/${up.json.id}/approve`, {}, bearer(SALES_MANAGER_TOKEN)), 403, "credit change: requester cannot approve their own request");
  const other = await call("POST", "/api/v1/ar/credit-changes", { customer_id: customer, new_limit_cents: 2_200_000, reason: "More room" }, bearer(BRANCH_MANAGER_TOKEN));
  refused(await call("POST", `/api/v1/ar/credit-changes/${other.json.id}/approve`, {}, bearer(SALES_MANAGER_TOKEN)), 403, "credit change: caller role is not permitted to approve this request");
  const approved = await call("POST", `/api/v1/ar/credit-changes/${up.json.id}/approve`, {}, bearer(CONTROLLER_TOKEN));
  assert.equal(approved.status, 200);
  holds("BillingCreditChangeRequest", approved.json);
  assert.equal(approved.json.status, "APPLIED");
  refused(await call("POST", `/api/v1/ar/credit-changes/${up.json.id}/approve`, {}, bearer(CONTROLLER_TOKEN)), 409, "credit change: request is not pending");

  // The first of two approvals moves nothing; the same person never approves twice.
  const big = await call("POST", "/api/v1/ar/credit-changes", { customer_id: customer, new_limit_cents: 9_000_000, reason: "Two large jobs" }, bearer(SALES_MANAGER_TOKEN));
  assert.equal(big.json.required_approvals, 2);
  const first = await call("POST", `/api/v1/ar/credit-changes/${big.json.id}/approve`, {}, bearer(CONTROLLER_TOKEN));
  holds("BillingCreditChangeRequest", first.json);
  assert.equal(first.json.status, "PENDING");
  refused(await call("POST", `/api/v1/ar/credit-changes/${big.json.id}/approve`, {}, bearer(CONTROLLER_TOKEN)), 403, "credit change: the same person cannot approve twice");
  refused(await call("POST", `/api/v1/ar/credit-changes/${big.json.id}/approve`, {}, bearer(BRANCH_MANAGER_TOKEN)), 403, "credit change: caller role is not permitted to approve this request");

  // A rejection needs its reason and settles the request.
  refused(await call("POST", `/api/v1/ar/credit-changes/${big.json.id}/reject`, { reason: "" }, bearer(BRANCH_MANAGER_TOKEN)), 400, "credit change: a reason is required");
  const rejected = await call("POST", `/api/v1/ar/credit-changes/${big.json.id}/reject`, { reason: "Balance is past due" }, bearer(BRANCH_MANAGER_TOKEN));
  assert.equal(rejected.status, 200);
  holds("BillingCreditChangeRequest", rejected.json);
  assert.equal(rejected.json.status, "REJECTED");
  assert.equal(rejected.json.rejection_reason, "Balance is past due");

  const history = await call("GET", `/api/v1/ar/credit-changes?customer_id=${customer}`, undefined, bearer(CONTROLLER_TOKEN));
  holds("BillingCreditChangePage", history.json);
  assert.ok(history.json.items.every((r) => r.customer_id === customer));
});
