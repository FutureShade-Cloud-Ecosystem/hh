// Unit leg of the Today surface against the shared tenant stub: the loader
// reads the dashboard route (tenantGetDashboard, gated view_billing; the roles
// without it are refused by the server with the shared 403) and the view words
// a person reads (the account, the credit line, the recent orders with their
// status and tracking words, never a stored status string, never arithmetic).
import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { todayLoad, todayView, TODAY_EMPTY } from "../src/today.ts";
import { InMemoryTenantSession, tenantClient, stubAdapter, money, can } from "@hardscapehouse/pro-kit";
import { PERSONAS, STUB_PASSWORD } from "@hardscapehouse/pro-kit/test-stub/tenant-data.mjs";
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

async function pageFor(who) {
  const session = new InMemoryTenantSession({ adapter: stubAdapter({ baseUrl: base }), baseUrl: base });
  const signed = await session.signIn(PERSONAS[who].email, STUB_PASSWORD);
  assert.equal(signed.ok, true, who);
  return {
    session,
    client: tenantClient(session, { baseUrl: base }),
    route: { surface: "today" },
    busy: false,
    field: () => null,
    goto: () => {},
    refresh: () => {},
    say: () => {},
    hold: () => {},
    openModal: () => {},
    closeModal: () => {},
  };
}

// Every string and value of a template, joined: what a person would read, close enough to assert words on.
function words(template) {
  if (template === null || template === undefined || template === false) return "";
  if (Array.isArray(template)) return template.map(words).join(" ");
  if (typeof template === "object" && "strings" in template) {
    return template.strings.map((s, i) => s + (i < template.values.length ? words(template.values[i]) : "")).join("");
  }
  return String(template);
}

test("the loader reads the dashboard for exactly the roles that hold view_billing", async () => {
  for (const [key, persona] of Object.entries(PERSONAS)) {
    const page = await pageFor(key);
    const answer = await todayLoad(page);
    if (can(persona.role, "view_billing")) {
      assert.equal(answer.kind, "ready", `${persona.role} should read the dashboard: ${JSON.stringify(answer)}`);
      if (answer.kind === "ready") {
        assert.equal(answer.data.account.name, "Summit Ridge Builders");
        assert.ok(Array.isArray(answer.data.recent_orders));
      }
    } else {
      assert.equal(answer.kind, "refusal", `${persona.role} should be refused`);
    }
  }
});

test("the view carries the account, the credit line as the core returned it, and the recent orders", async () => {
  const page = await pageFor("owner");
  const answer = await todayLoad(page);
  assert.equal(answer.kind, "ready");
  if (answer.kind !== "ready") return;
  const text = words(todayView(answer.data, page)).replace(/<[^>]*>/g, " ");
  assert.ok(text.includes("Summit Ridge Builders"), text);
  assert.ok(text.includes("Open balance"), text);
  assert.ok(text.includes("Available credit"), text);
  assert.ok(text.includes("Net 30"), text);
  assert.ok(text.includes("SO-30506"), "the newest order is not on Today");
  assert.ok(text.includes("Confirmed"), "the order status word is missing");
  assert.ok(text.includes(money(answer.data.open_balance_cents)), "the balance figure is missing");
  assert.doesNotMatch(text, /\b[A-Z]{2,}_[A-Z]{2,}\b/, "a stored status string reached the screen");
  assert.doesNotMatch(text, /undefined|null|NaN|!/);
});

test("a tracking word rides the order where the contract maps one, and the status chip stands in where it does not", async () => {
  const page = await pageFor("buyer");
  const answer = await todayLoad(page);
  assert.equal(answer.kind, "ready");
  if (answer.kind !== "ready") return;
  const text = words(todayView(answer.data, page)).replace(/<[^>]*>/g, " ");
  assert.ok(text.includes("Ready for pickup"), "the will call order is not tracked in words");
  assert.ok(text.includes("Out for delivery") === false, "out for delivery is not derivable and must not be guessed");
});

test("the empty words are Today's own, sentence case, with no exclamation", () => {
  assert.match(TODAY_EMPTY, /^No orders yet/);
  assert.doesNotMatch(TODAY_EMPTY, /!/);
});
