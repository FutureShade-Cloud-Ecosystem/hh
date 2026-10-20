// The session end to end against the real stub process: the stub adapter signs
// in through the stub's own door, the session adopts the bearer, reads tenant
// me for the role and capabilities, and the tenant client reads a gated route.
// Nothing about the person comes from a local table.
import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { tenantAuth, tenantDashboard } from "@hardscapehouse/api-client";
import { InMemoryTenantSession } from "../src/session.ts";
import { stubAdapter } from "../src/stub-adapter.ts";
import { demoTenantAdapter } from "../src/demo-adapter.ts";
import { ROLE_LABELS } from "../src/capabilities.ts";
import { tenantClient } from "../src/client.ts";
import { can } from "../src/capabilities.ts";
import { PERSONAS, STUB_PASSWORD } from "../test-stub/tenant-data.mjs";
import { spawnStub } from "../test-stub/spawn-stub.mjs";

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

const sessionFor = () => new InMemoryTenantSession({ adapter: stubAdapter({ baseUrl: base }), baseUrl: base });

test("each persona signs in and the session holds the role and capabilities the core answers", async () => {
  for (const persona of Object.values(PERSONAS)) {
    const session = sessionFor();
    const result = await session.signIn(persona.email, STUB_PASSWORD);
    assert.equal(result.ok, true, persona.role);
    assert.equal(session.profile().role, persona.role);
    for (const [capability, held] of Object.entries(session.profile().capabilities)) assert.equal(held, can(persona.role, capability));
    await session.signOut();
    assert.equal(session.profile(), null);
  }
});

test("a wrong password is refused in plain words and nothing is held", async () => {
  const session = sessionFor();
  const result = await session.signIn(PERSONAS.owner.email, "wrong");
  assert.equal(result.ok, false);
  assert.equal(result.error, "That email and password do not match.");
  assert.equal(session.current(), null);
});

test("an unreachable server reads as a plain sentence, not a stack", async () => {
  const session = new InMemoryTenantSession({ adapter: stubAdapter({ baseUrl: "http://127.0.0.1:9" }), baseUrl: "http://127.0.0.1:9" });
  const result = await session.signIn(PERSONAS.owner.email, STUB_PASSWORD);
  assert.equal(result.ok, false);
  assert.match(result.error, /^Pro is not reachable right now/);
});

test("the tenant client reads a gated route for a role that holds it and is refused for one that does not", async () => {
  const buyer = sessionFor();
  await buyer.signIn(PERSONAS.buyer.email, STUB_PASSWORD);
  const dashboard = await tenantDashboard.tenantGetDashboard(tenantClient(buyer, { baseUrl: base }));
  assert.equal(dashboard.ok, true);
  assert.equal(dashboard.value.account.name, "Summit Ridge Builders");
  const field = sessionFor();
  await field.signIn(PERSONAS.field.email, STUB_PASSWORD);
  const refused = await tenantDashboard.tenantGetDashboard(tenantClient(field, { baseUrl: base }));
  assert.equal(refused.ok, false);
  assert.equal(refused.status, 403);
  assert.equal(typeof (await tenantAuth.tenantGetMe(tenantClient(field, { baseUrl: base }))).value.name, "string");
});

test("after sign out the old token is not accepted and no bearer is sent", async () => {
  const session = sessionFor();
  await session.signIn(PERSONAS.owner.email, STUB_PASSWORD);
  const client = tenantClient(session, { baseUrl: base });
  await session.signOut();
  const refused = await tenantDashboard.tenantGetDashboard(client);
  assert.equal(refused.ok, false);
  assert.equal(refused.status, 401);
});

test("the demo adapter signs each of the five roles in through the stub's demo routes, role from tenant me", async () => {
  for (const persona of Object.values(PERSONAS)) {
    const session = new InMemoryTenantSession({ adapter: demoTenantAdapter({ baseUrl: base }), baseUrl: base });
    const result = await session.signInWith({ method: "persona", roleLabel: ROLE_LABELS[persona.role] });
    assert.equal(result.ok, true, persona.role);
    assert.equal(session.profile().role, persona.role);
    for (const [capability, held] of Object.entries(session.profile().capabilities)) assert.equal(held, can(persona.role, capability));
    const dashboard = await tenantDashboard.tenantGetDashboard(tenantClient(session, { baseUrl: base }));
    assert.equal(dashboard.ok, can(persona.role, "view_billing"), persona.role);
    if (!dashboard.ok) assert.equal(dashboard.status, 403);
    await session.signOut();
  }
});

test("the demo email link signs a role in by its demo address and words the direct demo sign in", async () => {
  const session = new InMemoryTenantSession({ adapter: demoTenantAdapter({ baseUrl: base }), baseUrl: base });
  const result = await session.signInWith({ method: "email_link", email: "ap_finance@hh-demo.invalid" });
  assert.equal(result.ok, true);
  assert.equal(result.value.notice, "Signed in as the demo Accounts payable. No email is sent in the demo.");
  assert.equal(session.profile().role, "ap_finance");
  assert.equal(session.profile().notice, "Signed in as the demo Accounts payable. No email is sent in the demo.");
});
