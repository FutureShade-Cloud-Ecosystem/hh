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

// -- the rules tab --------------------------------------------------------------------------------

const RULES = [
  {
    id: "9a11e111-0000-4000-9000-000000000001",
    scope: "TIER_CATEGORY",
    category_id: "ca7-1",
    customer_id: "",
    customer_type: "",
    product_id: "",
    branch_id: "",
    calc_type: "PCT_OFF_LIST",
    price_cents: null,
    discount_bps: 1000,
    markup_bps: null,
    price_uom: "SF",
    priority: 10,
    valid_from: null,
    valid_to: null,
    detail_text: "Trade discount",
    is_active: true,
    version: 3,
  },
];

function fakeRulesDoor(over = {}) {
  const door = fakeDoor({
    rules: async () => {
      door.calls.push("rules");
      return { ok: true, value: RULES };
    },
    rule: async (id) => {
      door.calls.push(["rule", id]);
      return { ok: true, value: RULES[0] };
    },
    createRule: async (body) => {
      door.calls.push(["createRule", body]);
      return { ok: true, value: { id: "9a11e111-0000-4000-9000-000000000009", version: 1 } };
    },
    updateRule: async (id, body) => {
      door.calls.push(["updateRule", id, body]);
      return { ok: true, value: { ...RULES[0], ...body, version: body.version + 1 } };
    },
    deleteRule: async (id, opts) => {
      door.calls.push(["deleteRule", id, opts]);
      return { ok: true, value: { status: "retired" } };
    },
    previewFloor: async (body) => {
      door.calls.push(["previewFloor", body]);
      if (door.floorAnswer) return { ok: true, value: door.floorAnswer };
      return { ok: true, value: { items: [], evaluated: 40, not_graded: 0, no_cost: 0, truncated: false } };
    },
    ...over,
  });
  return door;
}

test("opening the rules tab loads the rules", async () => {
  const door = fakeRulesDoor();
  const c = run(door);
  await c.open();
  await c.click("pr-set-tab", { tab: "rules" });
  assert.equal(c.model.rules.phase, "ready");
  assert.equal(c.model.rules.rows.length, 1);
});

test("a rule edit re-reads the rule for its current version, and the save rides the floor", async () => {
  const door = fakeRulesDoor();
  const c = run(door);
  await c.open();
  await c.click("pr-set-tab", { tab: "rules" });
  await c.click("pr-rule-edit", { id: RULES[0].id });
  assert.deepEqual(door.calls.find((x) => Array.isArray(x) && x[0] === "rule"), ["rule", RULES[0].id]);
  c.input("rule-discount", "12");
  await c.click("pr-rule-save", {});
  const preview = door.calls.filter((x) => Array.isArray(x) && x[0] === "previewFloor").pop();
  assert.equal(preview[1].kind, "rule");
  assert.equal(preview[1].id, RULES[0].id);
  assert.equal(preview[1].rule.discount_bps, 1200);
  const update = door.calls.find((x) => Array.isArray(x) && x[0] === "updateRule");
  assert.ok(update, "a clean grading saves");
  assert.equal(update[2].version, 3, "the patch carries the version the read brought");
  assert.equal(c.model.rules.notice, "Saved.");
});

test("a create sends the whole write with version zero", async () => {
  const door = fakeRulesDoor();
  const c = run(door);
  await c.open();
  await c.click("pr-set-tab", { tab: "rules" });
  c.click("pr-rule-edit", {});
  c.change("rule-scope", "TIER_DEFAULT");
  c.change("rule-calc", "PCT_OFF_LIST");
  c.input("rule-discount", "15");
  c.input("rule-priority", "10");
  await c.click("pr-rule-save", {});
  const create = door.calls.find((x) => Array.isArray(x) && x[0] === "createRule");
  assert.ok(create);
  assert.equal(create[1].version, 0);
  assert.equal(create[1].scope, "TIER_DEFAULT");
  assert.equal(create[1].discount_bps, 1500);
  assert.equal(create[1].customer_id, "");
});

test("a refused rule save shows the server's words and keeps the editor", async () => {
  const door = fakeRulesDoor({
    updateRule: async (id, body) => {
      door.calls.push(["updateRule", id, body]);
      return { ok: false, error: "scope and dims disagree", status: 422 };
    },
  });
  const c = run(door);
  await c.open();
  await c.click("pr-set-tab", { tab: "rules" });
  await c.click("pr-rule-edit", { id: RULES[0].id });
  await c.click("pr-rule-save", {});
  assert.match(c.model.rules.formError, /scope and dims disagree/);
  assert.ok(c.model.rules.editing);
  assert.equal(door.calls.filter((x) => Array.isArray(x) && x[0] === "updateRule").length, 1, "one write, no queue");
});

test("retire is two taps, and the second carries the version the row showed", async () => {
  const door = fakeRulesDoor();
  const c = run(door);
  await c.open();
  await c.click("pr-set-tab", { tab: "rules" });
  c.click("pr-rule-retire", { id: RULES[0].id });
  assert.equal(c.model.rules.confirmingId, RULES[0].id, "the first tap asks");
  assert.equal(door.calls.some((x) => Array.isArray(x) && x[0] === "deleteRule"), false, "nothing deletes on the first tap");
  await c.click("pr-rule-retire", { id: RULES[0].id });
  const del = door.calls.find((x) => Array.isArray(x) && x[0] === "deleteRule");
  assert.deepEqual(del[2], { version: 3 });
  assert.match(c.model.rules.notice, /Retired/);
});

// -- the customer prices tab --------------------------------------------------------------------------

const CUST_ID = "cu110000-0000-4000-9000-0000000000a1";
const OVERRIDES = [
  {
    id: "8a110000-0000-4000-9000-0000000000a1",
    customer_id: CUST_ID,
    category_id: "ca7-2",
    calc_type: "PCT_OFF_LIST",
    price_cents: null,
    discount_bps: 800,
    markup_bps: null,
    price_uom: "SF",
    notes: "Yard account discount",
    version: 2,
  },
];

function fakeOverridesDoor(over = {}) {
  const door = fakeRulesDoor({
    overrides: async (customerId) => {
      door.calls.push(["overrides", customerId]);
      return { ok: true, value: OVERRIDES };
    },
    createOverride: async (customerId, body) => {
      door.calls.push(["createOverride", customerId, body]);
      return { ok: true, value: { id: "8a110000-0000-4000-9000-0000000000a9", version: 1 } };
    },
    updateOverride: async (id, body) => {
      door.calls.push(["updateOverride", id, body]);
      return { ok: true, value: { ...OVERRIDES[0], ...body, version: body.version + 1 } };
    },
    deleteOverride: async (id, opts) => {
      door.calls.push(["deleteOverride", id, opts]);
      return { ok: true, value: { status: "deleted" } };
    },
    ...over,
  });
  return door;
}

test("the overrides tab loads one customer's prices by the typed id", async () => {
  const door = fakeOverridesDoor();
  const c = run(door);
  await c.open();
  await c.click("pr-set-tab", { tab: "customers" });
  c.input("cust-id", CUST_ID);
  await c.click("pr-cust-load", {});
  assert.equal(c.model.customers.phase, "ready");
  assert.equal(c.model.customers.loadedFor, CUST_ID);
  assert.equal(c.model.customers.rows.length, 1);
});

test("an override add rides the floor with the customer named, then creates", async () => {
  const door = fakeOverridesDoor();
  const c = run(door);
  await c.open();
  await c.click("pr-set-tab", { tab: "customers" });
  c.input("cust-id", CUST_ID);
  await c.click("pr-cust-load", {});
  c.click("pr-cust-edit", {});
  c.change("cust-category", "Retaining Walls");
  c.change("cust-calc", "PCT_OFF_LIST");
  c.input("cust-discount", "9");
  await c.click("pr-cust-save", {});
  const preview = door.calls.filter((x) => Array.isArray(x) && x[0] === "previewFloor").pop();
  assert.equal(preview[1].kind, "override");
  assert.equal(preview[1].customer_id, CUST_ID);
  const create = door.calls.find((x) => Array.isArray(x) && x[0] === "createOverride");
  assert.ok(create);
  assert.equal(create[1], CUST_ID);
  assert.equal(create[2].discount_bps, 900);
  assert.equal(create[2].version, 0);
  assert.equal(c.model.customers.notice, "Saved.");
});

test("an override edit patches with the row's version; delete is two taps with the version", async () => {
  const door = fakeOverridesDoor();
  const c = run(door);
  await c.open();
  await c.click("pr-set-tab", { tab: "customers" });
  c.input("cust-id", CUST_ID);
  await c.click("pr-cust-load", {});
  c.click("pr-cust-edit", { id: OVERRIDES[0].id });
  c.input("cust-discount", "10");
  await c.click("pr-cust-save", {});
  const update = door.calls.find((x) => Array.isArray(x) && x[0] === "updateOverride");
  assert.ok(update);
  assert.equal(update[1], OVERRIDES[0].id);
  assert.equal(update[2].version, 2);

  c.click("pr-cust-del", { id: OVERRIDES[0].id });
  assert.equal(c.model.customers.confirmingId, OVERRIDES[0].id);
  await c.click("pr-cust-del", { id: OVERRIDES[0].id });
  const del = door.calls.find((x) => Array.isArray(x) && x[0] === "deleteOverride");
  assert.deepEqual(del[2], { version: 2 });
  assert.match(c.model.customers.notice, /Deleted/);
});

test("a refused overrides read lands refused, naming the gate", async () => {
  const door = fakeOverridesDoor({
    overrides: async (customerId) => {
      door.calls.push(["overrides", customerId]);
      return { ok: false, error: "Forbidden", status: 403 };
    },
  });
  const c = run(door);
  await c.open();
  await c.click("pr-set-tab", { tab: "customers" });
  c.input("cust-id", CUST_ID);
  await c.click("pr-cust-load", {});
  assert.equal(c.model.customers.phase, "refused");
  assert.match(c.model.customers.error, /staff/);
});
