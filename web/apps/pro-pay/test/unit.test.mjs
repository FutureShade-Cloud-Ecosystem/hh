// Unit leg of Pro Pay: its landing surface against the catalogue, against the shared tenant stub (the
// route is held by the capability the surface names: allowed roles read it, the others are refused by the
// server with the shared 403) and the words its rows carry.
import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { createServer } from "node:net";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { landing } from "../src/landing.ts";
import { InMemoryTenantSession, tenantClient, stubAdapter, surfaceOf, can, TENANT_ROLES } from "@hardscapehouse/pro-kit";
import { PERSONAS, STUB_PASSWORD } from "@hardscapehouse/pro-kit/test-stub/tenant-data.mjs";

const stubFile = fileURLToPath(new URL("../../../packages/pro-kit/test-stub/tenant-stub.mjs", import.meta.url));
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
  const port = await freePort(56822, 4);
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

// Every string and value of a template, joined: what a person would read, close enough to assert words on.
function words(template) {
  if (template === null || template === undefined || template === false) return "";
  if (Array.isArray(template)) return template.map(words).join(" ");
  if (typeof template === "object" && "strings" in template) {
    return template.strings.map((s, i) => s + (i < template.values.length ? words(template.values[i]) : "")).join("");
  }
  return String(template);
}

test("the landing surface is a surface of this app and needs the capability the catalogue names", () => {
  const surface = surfaceOf("pay", landing.surface);
  assert.ok(surface, "the landing surface is not in the catalogue");
  assert.equal(surface.id, "invoices");
  assert.equal(surface.label, "Invoices");
  assert.equal(landing.capability, surface.capability);
  assert.equal(landing.capability, "view_billing");
});

test("the route behind the landing is held by exactly the roles that hold the capability", async () => {
  for (const [key, persona] of Object.entries(PERSONAS)) {
    const session = new InMemoryTenantSession({ adapter: stubAdapter({ baseUrl: base }), baseUrl: base });
    assert.equal((await session.signIn(persona.email, STUB_PASSWORD)).ok, true, key);
    const answer = await landing.load(tenantClient(session, { baseUrl: base }));
    if (can(persona.role, landing.capability)) {
      assert.equal(answer.ok, true, `${persona.role} should read it: ${answer.error ?? ""}`);
      assert.ok(Array.isArray(answer.value) && answer.value.length > 0, `${persona.role} read nothing`);
    } else {
      assert.equal(answer.ok, false, `${persona.role} should be refused`);
      assert.equal(answer.status, 403);
    }
  }
  assert.ok(TENANT_ROLES.length === 5);
});

test("a row carries the words a person reads, never a stored string", async () => {
  const session = new InMemoryTenantSession({ adapter: stubAdapter({ baseUrl: base }), baseUrl: base });
  await session.signIn(PERSONAS.ap.email, STUB_PASSWORD);
  const answer = await landing.load(tenantClient(session, { baseUrl: base }));
  // Markup is stripped: attributes may carry the stored string (data-status), the text a person reads must not.
  const text = answer.value.map((item) => words(landing.row(item)).replace(/<[^>]*>/g, " ")).join(" | ");
  assert.ok(text.includes("INV-"), `the rows do not show INV-: ${text}`);
  assert.ok(text.includes("Open"), `the rows do not show a Open chip: ${text}`);
  assert.doesNotMatch(text, /\b[A-Z]{2,}_[A-Z]{2,}\b/, "a stored status string reached a screen");
  assert.doesNotMatch(text, /undefined|null|NaN/);
});

test("the empty words are this app's own, sentence case, with no exclamation", () => {
  assert.ok(landing.emptyWords.startsWith("No invoices yet"));
  assert.doesNotMatch(landing.emptyWords, /!/);
});
