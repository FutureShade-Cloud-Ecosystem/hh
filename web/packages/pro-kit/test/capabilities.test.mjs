// The capability gate, pinned to the core. The matrix is copied from the
// core's own source and this suite re-reads that source on every run: a
// capability or a role added in core and not here fails these cases, so the
// copy cannot drift quietly. Three sources are held against the copy: the
// portal module's Capabilities struct and CapabilitiesFor (rbac.go), the
// identity module's twin that the route guards actually read (portal.go), and
// the generated TenantCapabilities and TenantRole wire types.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { CAPABILITIES, TENANT_ROLES, ROLE_LABELS, can, capabilitiesFor } from "../src/capabilities.ts";

const REPO = fileURLToPath(new URL("../../../../", import.meta.url));
const read = (rel) => readFileSync(`${REPO}${rel}`, "utf8");

// The Capabilities struct's fields in order, with the json tag each carries.
function structFields(source) {
  const body = /type Capabilities struct \{([\s\S]*?)\n\}/.exec(source);
  assert.ok(body, "the Capabilities struct was not found in the core source");
  const fields = [];
  for (const line of body[1].split("\n")) {
    const m = /^\s*(\w+)\s+bool(?:\s+`json:"(\w+)"`)?/.exec(line);
    if (m) fields.push({ name: m[1], json: m[2] ?? null });
  }
  return fields;
}

// The set of true fields per role constant, from the CapabilitiesFor switch.
function matrix(source, roleConst) {
  const fn = /func CapabilitiesFor\([\s\S]*?\n\}\n/.exec(source);
  assert.ok(fn, "CapabilitiesFor was not found in the core source");
  const out = new Map();
  const blocks = fn[0].split(/\n\tcase /).slice(1);
  for (const block of blocks) {
    const head = /^(\w+(?:\.\w+)?)\s*:/.exec(block);
    assert.ok(head, `an unreadable case in CapabilitiesFor: ${block.slice(0, 40)}`);
    const trues = [...block.matchAll(/(\w+):\s+true/g)].map((m) => m[1]);
    out.set(head[1], trues);
  }
  return out;
}

const snake = (name) => name.replace(/([a-z0-9])([A-Z])/g, "$1_$2").toLowerCase();

test("there are nine capabilities and five roles, as the core counts them", () => {
  assert.equal(CAPABILITIES.length, 9);
  assert.equal(TENANT_ROLES.length, 5);
  assert.deepEqual(Object.keys(ROLE_LABELS).sort(), [...TENANT_ROLES].sort());
});

test("the capability list equals the core portal struct's json tags, in order", () => {
  const fields = structFields(read("core/internal/portal/rbac.go"));
  assert.deepEqual(fields.map((f) => f.json), [...CAPABILITIES]);
});

test("the capability list equals the identity twin's fields and the generated wire type", () => {
  const twin = structFields(read("core/internal/identity/portal.go"));
  assert.deepEqual(twin.map((f) => snake(f.name)), [...CAPABILITIES]);
  const types = read("web/packages/api-client/src/gen/types.ts");
  const wire = /export type TenantCapabilities = \{([\s\S]*?)\};/.exec(types);
  assert.ok(wire, "TenantCapabilities was not found in the generated types");
  const keys = [...wire[1].matchAll(/^\s*(\w+):\s*boolean;/gm)].map((m) => m[1]);
  assert.deepEqual(keys, [...CAPABILITIES]);
});

test("the role list equals the generated TenantRole union", () => {
  const types = read("web/packages/api-client/src/gen/types.ts");
  const union = /export type TenantRole = ([^;]+);/.exec(types);
  assert.ok(union, "TenantRole was not found in the generated types");
  const roles = [...union[1].matchAll(/"(\w+)"/g)].map((m) => m[1]);
  assert.deepEqual(roles, [...TENANT_ROLES]);
});

for (const [file, label] of [
  ["core/internal/portal/rbac.go", "the portal module"],
  ["core/internal/identity/portal.go", "the identity module the guards read"],
]) {
  test(`every role's capabilities equal ${label}'s CapabilitiesFor`, () => {
    const source = read(file);
    const fields = structFields(source);
    const byName = new Map(fields.map((f) => [f.name, f.json ?? snake(f.name)]));
    const cases = matrix(source);
    // Role constants resolve to their string through the const block.
    const consts = new Map(
      [...source.matchAll(/(?:Role|PortalRole)\w*\s+(?:Role\s+)?=\s+"(\w+)"/g)].map((m) => [m[0].split(/\s+/)[0], m[1]]),
    );
    assert.equal(cases.size, TENANT_ROLES.length, "the core switch holds a different number of roles");
    for (const [constName, trues] of cases) {
      const role = consts.get(constName);
      assert.ok(role, `the role constant ${constName} did not resolve to a string`);
      const expected = trues.map((t) => byName.get(t)).sort();
      const held = CAPABILITIES.filter((c) => can(role, c)).sort();
      assert.deepEqual(held, expected, `${role} differs from the core`);
    }
  });
}

test("an unknown role, an empty role and a null role hold nothing", () => {
  for (const role of ["", "owner", "ACCOUNT_ADMIN", null, undefined]) {
    for (const capability of CAPABILITIES) assert.equal(can(role, capability), false);
  }
  assert.equal(can("account_admin", "mint_money"), false, "an unknown capability is never granted");
});

test("capabilitiesFor returns the wire shape: every capability key, a boolean each", () => {
  const owner = capabilitiesFor("account_admin");
  assert.deepEqual(Object.keys(owner), [...CAPABILITIES]);
  assert.ok(Object.values(owner).every((v) => v === true));
  const field = capabilitiesFor("field_crew");
  assert.equal(field.view_orders_deliveries, true);
  assert.equal(field.view_billing, false);
  assert.deepEqual(capabilitiesFor("nobody"), Object.fromEntries(CAPABILITIES.map((c) => [c, false])));
});

test("the split buyer roles each lose exactly their pair", () => {
  assert.equal(can("buyer_no_pay", "pay_invoices"), false);
  assert.equal(can("buyer_no_pay", "manage_payment_methods"), false);
  assert.equal(can("buyer_no_pay", "create_orders"), true);
  assert.equal(can("ap_finance", "create_orders"), false);
  assert.equal(can("ap_finance", "submit_rfq"), false);
  assert.equal(can("ap_finance", "pay_invoices"), true);
  assert.equal(can("buyer", "manage_users"), false);
});
