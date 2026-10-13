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
