// The TenantSession seam: sign in, sign out, the live token and its expiry,
// the role and capabilities exactly as GET /api/v1/tenant/me answers them.
// The adapter is a double the test owns; the session talks to a recording
// fetch. Nothing reaches web storage, the URL or a log.
import { test } from "node:test";
import assert from "node:assert/strict";
import { inspect } from "node:util";
import { InMemoryTenantSession, memoryStorage } from "../src/session.ts";
import { tenantClient } from "../src/client.ts";
import { realExchangeAdapter } from "../src/real-exchange.ts";

const NOW = 1_000_000_000_000;
const SECRET_TOKEN = "tok-secret-0123456789";

function me(role, over = {}) {
  const all = ["manage_users", "view_billing", "pay_invoices", "manage_payment_methods", "submit_rfq", "create_orders", "view_orders_deliveries", "edit_delivery_instructions", "manage_files"];
  const held = { account_admin: all, field_crew: ["view_orders_deliveries", "edit_delivery_instructions"], ap_finance: ["view_billing", "pay_invoices", "manage_payment_methods", "view_orders_deliveries", "manage_files"] }[role] ?? [];
  return {
    id: "u-1",
    customer_id: "c-1",
    email: "owner@hh-demo.invalid",
    name: "Contractor owner",
    role,
    status: "active",
    notification_prefs: {},
    created_at: new Date(NOW).toISOString(),
    updated_at: new Date(NOW).toISOString(),
    capabilities: Object.fromEntries(all.map((c) => [c, held.includes(c)])),
    ...over,
  };
}

function rig({ role = "account_admin", meStatus = 200, adapterAnswer, now = () => NOW } = {}) {
  const calls = [];
  const fetchLike = async (url, init) => {
    calls.push({ url, headers: init.headers, method: init.method });
    const body = meStatus === 200 ? me(role) : { error: "Missing or invalid credentials" };
    return { status: meStatus, ok: meStatus === 200, text: async () => JSON.stringify(body), arrayBuffer: async () => new ArrayBuffer(0) };
  };
  const adapterCalls = { signIn: [], signOut: 0 };
  const adapter = {
    async signIn(email, password) {
      adapterCalls.signIn.push([email, password]);
      if (adapterAnswer) return adapterAnswer;
      return { ok: true, value: { token: SECRET_TOKEN, expiresAtMs: NOW + 3_600_000, user: { userId: "u-1", email, name: "Contractor owner" } } };
    },
    async signOut() {
      adapterCalls.signOut += 1;
    },
  };
  const session = new InMemoryTenantSession({ adapter, fetch: fetchLike, now });
  return { session, calls, adapterCalls };
}

test("a new session is signed out: no token, no profile, nothing permitted", () => {
  const { session } = rig();
  assert.equal(session.current(), null);
  assert.equal(session.profile(), null);
  assert.equal(session.can("view_billing"), false);
});

test("sign in adopts the token, then reads the role and capabilities from tenant me", async () => {
  const { session, calls, adapterCalls } = rig({ role: "ap_finance" });
  const result = await session.signIn("ap@hh-demo.invalid", "pw");
  assert.equal(result.ok, true);
  assert.deepEqual(adapterCalls.signIn, [["ap@hh-demo.invalid", "pw"]]);
  assert.equal(calls.length, 1);
  assert.match(calls[0].url, /\/api\/v1\/tenant\/me$/);
  assert.equal(calls[0].headers.Authorization, `Bearer ${SECRET_TOKEN}`);
  assert.equal("X-Org" in calls[0].headers, false, "a tenant call never carries X-Org");
  const profile = session.profile();
  assert.equal(profile.role, "ap_finance");
  assert.equal(profile.name, "Contractor owner");
  assert.equal(session.can("pay_invoices"), true);
  assert.equal(session.can("create_orders"), false);
  assert.deepEqual(session.current(), { token: SECRET_TOKEN, expiresAtMs: NOW + 3_600_000 });
});

test("the role and capabilities are the core's answer, never derived locally", async () => {
  // The core says this buyer holds nothing; the local matrix would say otherwise.
  const calls = [];
  const fetchLike = async (url, init) => {
    calls.push(url);
    const body = me("buyer", { capabilities: Object.fromEntries(Object.keys(me("buyer").capabilities).map((k) => [k, false])) });
    return { status: 200, ok: true, text: async () => JSON.stringify(body), arrayBuffer: async () => new ArrayBuffer(0) };
  };
  const adapter = {
    signIn: async (email) => ({ ok: true, value: { token: SECRET_TOKEN, expiresAtMs: NOW + 60_000, user: { userId: "u-1", email, name: "n" } } }),
    signOut: async () => {},
  };
  const session = new InMemoryTenantSession({ adapter, fetch: fetchLike, now: () => NOW });
  await session.signIn("b@hh-demo.invalid", "pw");
  assert.equal(session.can("create_orders"), false);
});

test("a refused sign in stays signed out, keeps no token and says why in plain words", async () => {
  const { session, calls } = rig({ adapterAnswer: { ok: false, error: "That email and password do not match.", status: 401 } });
  const result = await session.signIn("x@hh-demo.invalid", "bad");
  assert.equal(result.ok, false);
  assert.equal(result.error, "That email and password do not match.");
  assert.equal(session.current(), null);
  assert.equal(calls.length, 0, "no tenant call is made without a token");
});

test("a tenant me refusal after the token lands signs the person back out", async () => {
  const { session, adapterCalls } = rig({ meStatus: 401 });
  const result = await session.signIn("o@hh-demo.invalid", "pw");
  assert.equal(result.ok, false);
  assert.equal(result.status, 401);
  assert.equal(session.current(), null, "the token is dropped when the account cannot be read");
  assert.equal(session.profile(), null);
  assert.equal(adapterCalls.signOut, 1);
});

test("sign out clears the token and the profile and tells the adapter", async () => {
  const { session, adapterCalls } = rig();
  await session.signIn("o@hh-demo.invalid", "pw");
  await session.signOut();
  assert.equal(session.current(), null);
  assert.equal(session.profile(), null);
  assert.equal(session.can("view_billing"), false);
  assert.equal(adapterCalls.signOut, 1);
});

test("an expired token reads as signed out", async () => {
  let clock = NOW;
  const { session } = rig({ now: () => clock });
  await session.signIn("o@hh-demo.invalid", "pw");
  assert.notEqual(session.current(), null);
  clock = NOW + 3_600_001;
  assert.equal(session.current(), null);
});

test("listeners hear sign in and sign out, and stop when they unsubscribe", async () => {
  const { session } = rig();
  const heard = [];
  const stop = session.subscribe(() => heard.push(session.profile() === null ? "out" : "in"));
  await session.signIn("o@hh-demo.invalid", "pw");
  await session.signOut();
  stop();
  await session.signIn("o@hh-demo.invalid", "pw");
  assert.deepEqual(heard, ["in", "out"]);
});

test("nothing is written to web storage, whatever the global holds", async () => {
  const writes = [];
  const trap = { getItem: () => null, setItem: (k) => writes.push(k), removeItem: () => {}, clear: () => {}, key: () => null, length: 0 };
  globalThis.localStorage = trap;
  globalThis.sessionStorage = trap;
  try {
    const { session } = rig();
    await session.signIn("o@hh-demo.invalid", "pw");
    await session.signOut();
    assert.deepEqual(writes, []);
  } finally {
    delete globalThis.localStorage;
    delete globalThis.sessionStorage;
  }
});

test("the token never shows in JSON, inspect or string forms of the session", async () => {
  const { session } = rig();
  await session.signIn("o@hh-demo.invalid", "pw");
  for (const form of [JSON.stringify(session), inspect(session, { depth: 6 }), String(session), JSON.stringify(session.profile())]) {
    assert.equal(form.includes(SECRET_TOKEN), false);
  }
});

test("the in memory storage double holds values and forgets them", () => {
  const storage = memoryStorage();
  storage.setItem("k", "v");
  assert.equal(storage.getItem("k"), "v");
  storage.removeItem("k");
  assert.equal(storage.getItem("k"), null);
});

test("tenantClient sends the live bearer and never X-Org", async () => {
  const { session, calls } = rig();
  await session.signIn("o@hh-demo.invalid", "pw");
  const client = tenantClient(session, { fetch: async (url, init) => {
    calls.push({ url, headers: init.headers });
    return { status: 200, ok: true, text: async () => "{}", arrayBuffer: async () => new ArrayBuffer(0) };
  } });
  await client.request({ method: "GET", path: "/api/v1/tenant/dashboard" });
  const last = calls.at(-1);
  assert.equal(last.headers.Authorization, `Bearer ${SECRET_TOKEN}`);
  assert.equal("X-Org" in last.headers, false);
  await session.signOut();
  await client.request({ method: "GET", path: "/api/v1/tenant/dashboard" });
  assert.equal("Authorization" in calls.at(-1).headers, false, "after sign out no bearer is sent");
});

test("the real exchange seam is named, and refuses in plain words until the auth lead wires it", async () => {
  const adapter = realExchangeAdapter();
  const answer = await adapter.signIn("o@hh-demo.invalid", "pw");
  assert.equal(answer.ok, false);
  assert.doesNotMatch(answer.error, /console|core|token|session|exchange|keychain/i);
  await adapter.signOut();
});
