// The demo sign in adapter: a contractor persona chosen by its role label, minted by the core's demo routes
// (GET /api/v1/demo/flows carries class and role_code; POST /api/v1/demo/session takes the flow name and
// nothing else), the tenant token adopted into the session's in memory custody. Demo build only.
import { test } from "node:test";
import assert from "node:assert/strict";
import { DemoTenantSignInAdapter, demoTenantAdapter } from "../src/demo-adapter.ts";
import { InMemoryTenantSession } from "../src/session.ts";
import { ROLE_LABELS } from "../src/capabilities.ts";

const NOW = 1_800_000_000_000;

function jwt(expSeconds) {
  const part = (o) => Buffer.from(JSON.stringify(o)).toString("base64url");
  return `${part({ alg: "none" })}.${part({ sub: "x", exp: expSeconds })}.sig`;
}

const FLOWS = {
  flows: [
    { flow: "sales", role_code: "inside_sales", label: "Sales", class: "staff" },
    { flow: "contractor-account-admin", role_code: "account_admin", label: "Contractor owner", class: "tenant" },
    { flow: "contractor-buyer", role_code: "buyer", label: "Contractor buyer", class: "tenant" },
    { flow: "contractor-buyer-no-pay", role_code: "buyer_no_pay", label: "Contractor buyer (no pay)", class: "tenant" },
    { flow: "contractor-ap-finance", role_code: "ap_finance", label: "Contractor AP", class: "tenant" },
    { flow: "contractor-field-crew", role_code: "field_crew", label: "Contractor field crew", class: "tenant" },
  ],
};

function rig({ flows = FLOWS, sessionStatus = 200, sessionBody, flowsStatus = 200 } = {}) {
  const calls = [];
  const fetchLike = async (url, init = {}) => {
    calls.push({ url, method: init.method ?? "GET", body: init.body });
    if (url.endsWith("/api/v1/demo/flows")) return { status: flowsStatus, ok: flowsStatus === 200, json: async () => flows };
    if (url.endsWith("/api/v1/demo/session")) {
      const flow = JSON.parse(init.body).flow;
      const body = sessionBody ?? {
        token: jwt(NOW / 1000 + 3600),
        user: { user_id: "u-" + flow, email: flow + "@hh-demo.invalid", display_name: "Persona", role_code: "buyer", role_name: "Label" },
        flow,
        class: "tenant",
      };
      return { status: sessionStatus, ok: sessionStatus === 200, json: async () => body };
    }
    throw new Error("unexpected " + url);
  };
  return { calls, adapter: demoTenantAdapter({ fetch: fetchLike }) };
}

test("it offers the demo's methods and the five role labels, in the kit's own words", () => {
  const { adapter } = rig();
  assert.deepEqual(adapter.methods(), ["email_link", "google"]);
  assert.deepEqual(adapter.personas(), ["Owner", "Buyer", "Project manager", "Accounts payable", "Field crew"]);
  assert.deepEqual(adapter.personas(), [ROLE_LABELS.account_admin, ROLE_LABELS.buyer, ROLE_LABELS.buyer_no_pay, ROLE_LABELS.ap_finance, ROLE_LABELS.field_crew]);
});

test("choosing a role label mints that role's tenant flow and carries only the flow name", async () => {
  const { adapter, calls } = rig();
  const answer = await adapter.signInWith({ method: "persona", roleLabel: "Accounts payable" });
  assert.equal(answer.ok, true);
  assert.equal(answer.value.user.userId, "u-contractor-ap-finance");
  assert.equal(answer.value.expiresAtMs, NOW + 3_600_000);
  const post = calls.find((c) => c.method === "POST");
  assert.deepEqual(JSON.parse(post.body), { flow: "contractor-ap-finance" });
});

test("each of the five labels reaches its own flow, never a staff flow", async () => {
  const want = { Owner: "contractor-account-admin", Buyer: "contractor-buyer", "Project manager": "contractor-buyer-no-pay", "Accounts payable": "contractor-ap-finance", "Field crew": "contractor-field-crew" };
  for (const [label, flow] of Object.entries(want)) {
    const { adapter, calls } = rig();
    const answer = await adapter.signInWith({ method: "persona", roleLabel: label });
    assert.equal(answer.ok, true, label);
    assert.equal(JSON.parse(calls.find((c) => c.method === "POST").body).flow, flow, label);
  }
});

test("an unknown label, or a flows listing without that contractor flow, mints nothing and says so in plain words", async () => {
  const unknown = rig();
  const a = await unknown.adapter.signInWith({ method: "persona", roleLabel: "Sales" });
  assert.equal(a.ok, false);
  assert.equal(unknown.calls.some((c) => c.method === "POST"), false);
  const missing = rig({ flows: { flows: FLOWS.flows.filter((f) => f.class === "staff") } });
  const b = await missing.adapter.signInWith({ method: "persona", roleLabel: "Owner" });
  assert.equal(b.ok, false);
  assert.equal(missing.calls.some((c) => c.method === "POST"), false);
  assert.doesNotMatch(b.error, /flow|token|session|core|console/i);
});

test("a staff class answer to a contractor ask is refused, nothing adopted", async () => {
  const { adapter } = rig({ sessionBody: { token: jwt(NOW / 1000 + 3600), user: { user_id: "u", email: "e", display_name: "n", role_code: "r", role_name: "l" }, flow: "x", class: "staff" } });
  const answer = await adapter.signInWith({ method: "persona", roleLabel: "Owner" });
  assert.equal(answer.ok, false);
});

test("a token with no readable expiry is refused", async () => {
  const { adapter } = rig({ sessionBody: { token: "not-a-jwt", user: { user_id: "u", email: "e", display_name: "n", role_code: "r", role_name: "l" }, flow: "x", class: "tenant" } });
  assert.equal((await adapter.signInWith({ method: "persona", roleLabel: "Owner" })).ok, false);
});

test("the demo email link takes a persona's address and refuses any other", async () => {
  const good = rig();
  const ok = await good.adapter.signInWith({ method: "email_link", email: "Field_Crew@hh-demo.invalid" });
  assert.equal(ok.ok, true);
  assert.equal(JSON.parse(good.calls.find((c) => c.method === "POST").body).flow, "contractor-field-crew");
  const bad = rig();
  for (const email of ["someone@example.com", "buyer@elsewhere.invalid", "", "sales@hh-demo.invalid"]) {
    const answer = await bad.adapter.signInWith({ method: "email_link", email });
    assert.equal(answer.ok, false, email);
    assert.match(answer.error, /demo role/i);
  }
  assert.equal(bad.calls.some((c) => c.method === "POST"), false);
});

test("Google and the password way are not open on the demo, and point at the role picker", async () => {
  const { adapter } = rig();
  for (const method of ["google", "microsoft", "futurebuild"]) {
    const answer = await adapter.signInWith({ method });
    assert.equal(answer.ok, false);
    assert.match(answer.error, /demo role/i);
  }
  const password = await adapter.signIn("a@b.c", "x");
  assert.equal(password.ok, false);
});

test("a rate refusal and an unreachable box read as plain words", async () => {
  const limited = rig({ sessionStatus: 429 });
  const a = await limited.adapter.signInWith({ method: "persona", roleLabel: "Owner" });
  assert.equal(a.ok, false);
  assert.equal(a.status, 429);
  assert.match(a.error, /too many/i);
  const down = demoTenantAdapter({ fetch: async () => { throw new Error("boom"); } });
  const b = await down.signInWith({ method: "persona", roleLabel: "Owner" });
  assert.equal(b.ok, false);
  assert.match(b.error, /not reachable/i);
});

test("the session adopts the persona's token in memory and reads the role from tenant me", async () => {
  const calls = [];
  const fetchLike = async (url, init = {}) => {
    calls.push(url);
    if (url.endsWith("/api/v1/demo/flows")) return { status: 200, ok: true, json: async () => FLOWS };
    if (url.endsWith("/api/v1/demo/session")) {
      return { status: 200, ok: true, json: async () => ({ token: jwt(NOW / 1000 + 3600), user: { user_id: "u1", email: "ap_finance@hh-demo.invalid", display_name: "Contractor AP", role_code: "ap_finance", role_name: "Contractor AP" }, flow: "contractor-ap-finance", class: "tenant" }) };
    }
    if (url.endsWith("/api/v1/tenant/me")) {
      const all = ["manage_users", "view_billing", "pay_invoices", "manage_payment_methods", "submit_rfq", "create_orders", "view_orders_deliveries", "edit_delivery_instructions", "manage_files"];
      const held = ["view_billing", "pay_invoices", "manage_payment_methods", "view_orders_deliveries", "manage_files"];
      const body = { id: "u1", customer_id: "c", email: "ap_finance@hh-demo.invalid", name: "Contractor AP", role: "ap_finance", status: "active", notification_prefs: {}, created_at: "x", updated_at: "x", capabilities: Object.fromEntries(all.map((c) => [c, held.includes(c)])) };
      return { status: 200, ok: true, text: async () => JSON.stringify(body), arrayBuffer: async () => new ArrayBuffer(0) };
    }
    throw new Error("unexpected " + url);
  };
  const adapter = demoTenantAdapter({ fetch: fetchLike });
  const session = new InMemoryTenantSession({ adapter, fetch: fetchLike, now: () => NOW });
  assert.deepEqual(session.options().methods, ["email_link", "google"]);
  assert.equal(session.options().personas.length, 5);
  const result = await session.signInWith({ method: "persona", roleLabel: "Accounts payable" });
  assert.equal(result.ok, true);
  assert.equal(session.profile().role, "ap_finance");
  assert.equal(session.can("pay_invoices"), true);
  assert.equal(session.can("submit_rfq"), false);
  assert.notEqual(session.current(), null);
  assert.ok(DemoTenantSignInAdapter);
});
