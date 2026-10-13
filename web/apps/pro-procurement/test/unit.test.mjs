// Unit leg of Pro Procurement Board: its landing surface against the catalogue, against the shared tenant stub (the
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

before(async () => {
  const port = await new Promise((resolve) => {
    const probe = createServer();
    probe.listen(0, "127.0.0.1", () => {
      const { port: p } = probe.address();
      probe.close(() => resolve(p));
    });
  });
  base = `http://127.0.0.1:${port}`;
  child = spawn(process.execPath, [stubFile], { env: { ROLE_STUB_PORT: String(port) }, stdio: "ignore" });
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

after(() => child?.kill());

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
  const surface = surfaceOf("procurement", landing.surface);
  assert.ok(surface, "the landing surface is not in the catalogue");
  assert.equal(surface.id, "board");
  assert.equal(surface.label, "Board");
  assert.equal(landing.capability, surface.capability);
  assert.equal(landing.capability, "submit_rfq");
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
  await session.signIn(PERSONAS.owner.email, STUB_PASSWORD);
  const answer = await landing.load(tenantClient(session, { baseUrl: base }));
  // Markup is stripped: attributes may carry the stored string (data-status), the text a person reads must not.
  const text = answer.value.map((item) => words(landing.row(item)).replace(/<[^>]*>/g, " ")).join(" | ");
  assert.ok(text.includes("Point Loma retaining wall"), `the rows do not show Point Loma retaining wall: ${text}`);
  assert.ok(text.includes("Planning"), `the rows do not show a Planning chip: ${text}`);
  assert.doesNotMatch(text, /\b[A-Z]{2,}_[A-Z]{2,}\b/, "a stored status string reached a screen");
  assert.doesNotMatch(text, /undefined|null|NaN/);
});

test("the empty words are this app's own, sentence case, with no exclamation", () => {
  assert.ok(landing.emptyWords.startsWith("No projects yet"));
  assert.doesNotMatch(landing.emptyWords, /!/);
});
