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
