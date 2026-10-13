// The Pricing surface's state machine, on its own: the loads, the refusals,
// the margin editor's floor walk (preview, confirm, save, log) and the fact
// that no write ever queues. The door is a fake that records its calls; no
// browser, no network. Red first: this file landed before
// pricing-controller.ts existed and failed on the missing module (see the
// report).
import { test } from "node:test";
import assert from "node:assert/strict";
import { PricingController } from "../src/pricing-controller.ts";

const CATS = [
  { id: "ca7-1", code: "PAV", name: "Pavers", is_active: true },
  { id: "ca7-2", code: "WAL", name: "Retaining Walls", is_active: true },
];

const POLICIES = [{ category_id: "ca7-1", margin_target_bps: 3500, margin_floor_bps: 2000, margin_enforcement_disabled: false }];

function fakeDoor(over = {}) {
  const calls = [];
  const door = {
    calls,
    categories: async () => {
      calls.push("categories");
      return { ok: true, value: CATS };
    },
    policies: async () => {
      calls.push("policies");
      return { ok: true, value: POLICIES };
    },
    previewFloor: async (body) => {
      calls.push(["previewFloor", body]);
      return { ok: true, value: { items: [], evaluated: 40, not_graded: 0, no_cost: 0, truncated: false } };
    },
    upsertPolicy: async (categoryId, body) => {
      calls.push(["upsertPolicy", categoryId, body]);
      return { ok: true, value: { category_id: categoryId, ...body } };
    },
    confirmFloor: async (body) => {
      calls.push(["confirmFloor", body]);
      return { ok: true, value: { status: "logged" } };
    },
    ...over,
  };
  return door;
}

function run(door) {
  const views = [];
  const c = new PricingController(door, () => views.push(1));
  return c;
}

test("opening the surface loads the margin tab: the categories and the policies, ready", async () => {
  const door = fakeDoor();
  const c = run(door);
  await c.open();
  assert.equal(c.model.tab, "margin");
  assert.equal(c.model.margin.phase, "ready");
  assert.equal(c.model.margin.rows.length, 1);
  assert.equal(c.model.categories.length, 2);
  assert.deepEqual(door.calls, ["categories", "policies"]);
});

test("a refused read lands refused, in the words naming who may act, and no row renders", async () => {
  const door = fakeDoor({
    policies: async () => {
      door.calls.push("policies");
      return { ok: false, error: "Forbidden", status: 403 };
    },
  });
  const c = run(door);
  await c.open();
  assert.equal(c.model.margin.phase, "refused");
  assert.match(c.model.margin.error, /Forbidden/);
  assert.match(c.model.margin.error, /staff/);
});

test("an errored read lands error with the server's own words", async () => {
  const door = fakeDoor({
    policies: async () => {
      door.calls.push("policies");
      return { ok: false, error: "the pricing read is not wired on this deployment", status: 503 };
    },
  });
  const c = run(door);
  await c.open();
  assert.equal(c.model.margin.phase, "error");
  assert.match(c.model.margin.error, /not wired on this deployment/);
});

test("saving a clean policy previews the floor, writes, and never queues", async () => {
  const door = fakeDoor();
  const c = run(door);
  await c.open();
  c.click("pr-policy-edit", { category: "ca7-2" });
  c.input("policy-target", "35");
  c.input("policy-floor", "20");
  await c.click("pr-policy-save", {});
  const preview = door.calls.find((x) => Array.isArray(x) && x[0] === "previewFloor");
  assert.ok(preview, "the save asks the floor first");
  assert.equal(preview[1].kind, "category_policy");
  assert.equal(preview[1].category_id, "ca7-2");
  assert.equal(preview[1].policy.margin_target_bps, 3500);
  assert.equal(preview[1].policy.margin_floor_bps, 2000);
  const upsert = door.calls.find((x) => Array.isArray(x) && x[0] === "upsertPolicy");
  assert.ok(upsert, "a clean grading saves straight away");
  assert.equal(c.model.margin.floor, null, "no warning panel for a clean grading");
  assert.equal(c.model.margin.notice, "Saved.", "the success state is said");
  assert.equal(c.model.margin.editing, null, "the editor closes on a save");
  const confirm = door.calls.find((x) => Array.isArray(x) && x[0] === "confirmFloor");
  assert.equal(confirm, undefined, "nothing is logged when nothing was warned");
});

test("a warning grading opens the panel with the server's own items; confirming writes then logs", async () => {
  const door = fakeDoor({
    previewFloor: async () => ({
      ok: true,
      value: {
        items: [
          {
            product_id: "p1",
            sku: "PAV-1",
            name: "Paver one",
            final_cents: 420,
            margin_bps: 1500,
            floor_bps: 2000,
            previous_final_cents: 480,
            previous_margin_bps: 2600,
            already_under_floor: false,
          },
        ],
        evaluated: 40,
        not_graded: 0,
        no_cost: 0,
        truncated: false,
      },
    }),
  });
  const c = run(door);
  await c.open();
  c.click("pr-policy-edit", { category: "ca7-1" });
  c.input("policy-target", "30");
  c.input("policy-floor", "10");
  await c.click("pr-policy-save", {});
  assert.ok(c.model.margin.floor, "the warning panel is open");
  assert.equal(c.model.margin.floor.items.length, 1);
  assert.equal(c.model.margin.floor.items[0].sku, "PAV-1");
  assert.equal(door.calls.some((x) => Array.isArray(x) && x[0] === "upsertPolicy"), false, "nothing writes past an open warning");

  await c.click("pr-policy-confirm", {});
  const upsert = door.calls.find((x) => Array.isArray(x) && x[0] === "upsertPolicy");
  assert.ok(upsert, "confirming runs the save");
  const confirm = door.calls.find((x) => Array.isArray(x) && x[0] === "confirmFloor");
  assert.ok(confirm, "the confirmation is logged after the save");
  assert.equal(confirm[1].entity_type, "category_policy");
  assert.equal(confirm[1].entity_id, "ca7-1");
  assert.deepEqual(confirm[1].product_ids, ["p1"]);
  assert.match(c.model.margin.notice, /logged/, "the verdict is the server's own word");
});

test("a refused write shows the server's words inline and writes nothing else", async () => {
  const door = fakeDoor({
    upsertPolicy: async (categoryId, body) => {
      door.calls.push(["upsertPolicy", categoryId, body]);
      return { ok: false, error: "Forbidden", status: 403 };
    },
  });
  const c = run(door);
  await c.open();
  c.click("pr-policy-edit", { category: "ca7-1" });
  c.input("policy-target", "35");
  c.input("policy-floor", "20");
  await c.click("pr-policy-save", {});
  assert.match(c.model.margin.formError, /Forbidden/);
  assert.match(c.model.margin.formError, /manager/);
  assert.ok(c.model.margin.editing, "the editor stays open with the person's words");
  assert.equal(door.calls.filter((x) => Array.isArray(x) && x[0] === "upsertPolicy").length, 1, "one write, no retry queue");
});

test("an unparsable percent is refused at the form, before any call", async () => {
  const door = fakeDoor();
  const c = run(door);
  await c.open();
  c.click("pr-policy-edit", { category: "ca7-1" });
  c.input("policy-target", "abc");
  await c.click("pr-policy-save", {});
  assert.match(c.model.margin.formError, /percent/);
  assert.equal(door.calls.some((x) => Array.isArray(x) && x[0] === "previewFloor"), false);
});

test("cancel closes the editor and drops any open warning", () => {
  const door = fakeDoor();
  const c = run(door);
  c.click("pr-policy-edit", { category: "ca7-1" });
  assert.ok(c.model.margin.editing);
  c.click("pr-policy-cancel", {});
  assert.equal(c.model.margin.editing, null);
  assert.equal(c.model.margin.floor, null);
});
