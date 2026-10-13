// The session end to end against the real stub process: the stub adapter signs
// in through the stub's own door, the session adopts the bearer, reads tenant
// me for the role and capabilities, and the tenant client reads a gated route.
// Nothing about the person comes from a local table.
import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { createServer } from "node:net";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { tenantAuth, tenantDashboard } from "@hardscapehouse/api-client";
import { InMemoryTenantSession } from "../src/session.ts";
import { stubAdapter } from "../src/stub-adapter.ts";
import { tenantClient } from "../src/client.ts";
import { can } from "../src/capabilities.ts";
import { PERSONAS, STUB_PASSWORD } from "../test-stub/tenant-data.mjs";

const pkgDir = join(fileURLToPath(new URL(".", import.meta.url)), "..");
let child;
let base;

// A free port inside the Pro cycle's own range (the kit's block is 56800 to 56809), never an ephemeral one.
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
  const port = await freePort(56802, 2);
  base = `http://127.0.0.1:${port}`;
  child = spawn(process.execPath, [join(pkgDir, "test-stub", "tenant-stub.mjs")], { env: { ROLE_STUB_PORT: String(port), ROLE_STUB_IDLE_EXIT_MS: "120000" }, stdio: "ignore" });
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
