// The surface catalogue: the board's names, each surface gated by one real
// capability, the three apps' paths in the Pro range of the front door.
import { test } from "node:test";
import assert from "node:assert/strict";
import { CAPABILITIES, can, TENANT_ROLES } from "../src/capabilities.ts";
import { PRO_APPS, appOf, surfaceOf, openSurfaces } from "../src/surfaces.ts";

test("the three apps hold the surfaces the board names, in the board's order", () => {
  assert.deepEqual(PRO_APPS.map((a) => a.label), ["Pro Procurement", "Pro Pay", "Pro Field"]);
  assert.deepEqual(appOf("procurement").surfaces.map((s) => s.label), ["Board", "Catalog", "Quote studio", "Projects"]);
  assert.deepEqual(appOf("pay").surfaces.map((s) => s.label), ["Invoices", "Payment methods", "Statements"]);
  assert.deepEqual(appOf("field").surfaces.map((s) => s.label), ["Tracking", "Deliveries", "Pickups"]);
});

test("every surface is gated by a capability the core really has, and ids are unique per app", () => {
  for (const app of PRO_APPS) {
    const ids = app.surfaces.map((s) => s.id);
    assert.equal(new Set(ids).size, ids.length, `${app.key} repeats a surface id`);
    for (const surface of app.surfaces) assert.ok(CAPABILITIES.includes(surface.capability), `${surface.id} names an unknown capability`);
    assert.match(app.path, /^\/pro-[a-z]+\/$/);
  }
});

test("no sentence a person reads carries a stored string or a stray capital", () => {
  for (const app of PRO_APPS) {
    for (const surface of app.surfaces) assert.doesNotMatch(surface.label, /[_]|^[a-z]/, surface.label);
  }
});

test("each role sees exactly the surfaces its capabilities open", () => {
  for (const role of TENANT_ROLES) {
    const held = new Set(CAPABILITIES.filter((c) => can(role, c)));
    const open = openSurfaces(held);
    for (const { app, surfaces } of open) for (const s of surfaces) assert.equal(can(role, s.capability), true, `${role} sees ${app.key} ${s.id}`);
    const total = open.reduce((n, g) => n + g.surfaces.length, 0);
    const expected = PRO_APPS.flatMap((a) => a.surfaces).filter((s) => can(role, s.capability)).length;
    assert.equal(total, expected, role);
  }
  // The field crew opens Field and nothing in Pay or Procurement.
  const field = openSurfaces(new Set(CAPABILITIES.filter((c) => can("field_crew", c))));
  assert.deepEqual(field.map((g) => g.app.key), ["field"]);
  // AP opens Pay and Field's read surfaces, never the board.
  const ap = openSurfaces(new Set(CAPABILITIES.filter((c) => can("ap_finance", c))));
  assert.ok(ap.some((g) => g.app.key === "pay"));
  assert.equal(surfaceOf("procurement", "board").capability, "submit_rfq");
  assert.equal(ap.find((g) => g.app.key === "procurement")?.surfaces.some((s) => s.id === "board") ?? false, false);
});
