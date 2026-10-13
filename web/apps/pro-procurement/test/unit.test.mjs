// Unit leg of Pro Procurement Board: the surface registry (the surfaces the
// cycle brief names, each gated by the one capability its routes answer to),
// the mapping from the Pro kit catalogue's ids to this app's surfaces (the
// frame's selector reads the kit catalogue; this app answers it), and the
// shared view helpers. Each surface's data leg lives in its own test file
// beside it; the app root's behaviour is the browser legs'.
import { test } from "node:test";
import assert from "node:assert/strict";
import { SURFACES, LANDING, TOP_SURFACES, isSurfaceId, surfaceRouteFromCatalogue, frameSurfaceOf } from "../src/registry.ts";
import { can, CAPABILITIES } from "@hardscapehouse/pro-kit";

test("the app carries the surfaces the brief names, each gated by a capability the core stores", () => {
  for (const id of ["today", "board", "projects", "catalog", "quotes", "scope", "product"]) {
    const surface = SURFACES[id];
    assert.ok(surface, `the ${id} surface is missing from the registry`);
    assert.ok(CAPABILITIES.includes(surface.capability), `${id} names a capability the core does not store`);
    assert.match(surface.label, /^[A-Z]/, `${id} label is not sentence case`);
    assert.doesNotMatch(surface.label, /!/);
  }
});

test("the landing is Today, held by view_billing, and every other surface answers to submit_rfq", () => {
  assert.equal(LANDING, "today");
  assert.equal(SURFACES.today.capability, "view_billing");
  for (const id of ["board", "projects", "catalog", "quotes", "scope", "product"]) {
    assert.equal(SURFACES[id].capability, "submit_rfq", `${id} should answer to submit_rfq`);
  }
  assert.deepEqual([...TOP_SURFACES].sort(), ["board", "catalog", "projects", "quotes", "today"]);
});

test("a role without the capability is refused at the gate, and one with it passes", () => {
  assert.equal(can("field_crew", SURFACES.today.capability), false, "field crew should not open Today");
  assert.equal(can("account_admin", SURFACES.today.capability), true);
  assert.equal(can("ap_finance", SURFACES.board.capability), false, "accounts payable should not open the Board");
  assert.equal(can("buyer_no_pay", SURFACES.board.capability), true);
  assert.equal(can("buyer", SURFACES.quotes.capability), true);
});

test("the kit catalogue's ids for this app land on this app's surfaces", () => {
  assert.equal(surfaceRouteFromCatalogue("board")?.surface, "board");
  assert.equal(surfaceRouteFromCatalogue("projects")?.surface, "projects");
  assert.equal(surfaceRouteFromCatalogue("catalog")?.surface, "catalog");
  assert.equal(surfaceRouteFromCatalogue("today")?.surface, "today");
  assert.equal(surfaceRouteFromCatalogue("quotes")?.surface, "quotes");
  assert.equal(surfaceRouteFromCatalogue("quote-studio"), null);
  assert.equal(surfaceRouteFromCatalogue("no-such-surface"), null);
});

test("isSurfaceId and frameSurfaceOf keep the frame's selector honest about where the person is", () => {
  assert.equal(isSurfaceId("today"), true);
  assert.equal(isSurfaceId("calendar"), false);
  // A drill-down keeps its parent's selector entry marked, so the selector never lies.
  assert.equal(frameSurfaceOf({ surface: "catalog", productId: "a1" }), "catalog");
  assert.equal(frameSurfaceOf({ surface: "product", productId: "a1" }), "catalog");
  assert.equal(frameSurfaceOf({ surface: "scope", projectId: "9a1", from: "projects" }), "projects");
  assert.equal(frameSurfaceOf({ surface: "scope", projectId: "9a1", from: "board" }), "board");
  assert.equal(frameSurfaceOf({ surface: "scope", projectId: "9a1" }), "board", "order scope defaults to the board as its parent");
  assert.equal(frameSurfaceOf({ surface: "quotes" }), "quotes");
});

test("no surface word carries an exclamation mark or a stored status string", () => {
  for (const [id, surface] of Object.entries(SURFACES)) {
    assert.doesNotMatch(surface.label, /!/, `${id}`);
    assert.doesNotMatch(surface.label, /\b[A-Z]{2,}_[A-Z]{2,}\b/, `${id}`);
  }
});
