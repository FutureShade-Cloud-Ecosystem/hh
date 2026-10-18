// The role configuration surface's decisions, with no DOM and no network:
// the grid's axes (the core's own five job shapes on the core's own ten
// surfaces), the seeded role to shape catalog, the cell summary words, the
// item set's shape checks in the core's own words, and the door's calls.
import { test } from "node:test";
import assert from "node:assert/strict";
import { ApiClient } from "@hardscapehouse/api-client";
import { ConfigApi } from "../src/config-api.js";
import {
  CONFIG_SURFACES,
  JOB_SHAPES,
  cellWords,
  configRefusalWords,
  itemRowError,
  itemSetError,
  profileForRoleCode,
  rolesForShape,
  workspaceSplitError,
  newWorkspaceKey,
  inPool,
} from "../src/config-logic.js";

test("the grid's rows are the thirteen profiles the core's validProfiles holds: five base shapes and eight manager refinements", () => {
  assert.deepEqual(
    JOB_SHAPES.map((s) => s.profile),
    [
      "counter", "yard", "driver", "manager", "admin",
      "manager.branch", "manager.sales", "manager.yard", "manager.logistics",
      "manager.procurement", "manager.payables", "manager.controller", "manager.general",
    ],
  );
  assert.match(JOB_SHAPES.find((s) => s.profile === "manager").what, /manager role with no refinement row/);
});

test("the grid's columns are the core's ten curated surfaces, each with its own words", () => {
  assert.deepEqual(
    CONFIG_SURFACES.map((s) => s.id),
    [
      "counter-till",
      "quote-desk",
      "yard-pick",
      "drive-run",
      "dispatch-board",
      "buying-and-stock",
      "money-desk",
      "manager-home",
      "house-messages",
      "dealer-admin",
    ],
  );
  for (const s of CONFIG_SURFACES) {
    assert.match(s.word, /\s/);
  }
});

test("a role code resolves to the profile the core resolves today (0140 as amended by 0381); an unknown role falls back to the counter shape", () => {
  assert.equal(profileForRoleCode("INSIDE_SALES"), "counter");
  assert.equal(profileForRoleCode("OUTSIDE_SALES"), "counter");
  assert.equal(profileForRoleCode("YARD_TEAM"), "yard");
  assert.equal(profileForRoleCode("DRIVERS"), "driver");
  assert.equal(profileForRoleCode("BRANCH_MANAGER"), "manager.branch");
  assert.equal(profileForRoleCode("SALES_MANAGER"), "manager.sales");
  assert.equal(profileForRoleCode("YARD_MANAGER"), "manager.yard");
  assert.equal(profileForRoleCode("LOGISTICS_MANAGER"), "manager.logistics");
  assert.equal(profileForRoleCode("PROCUREMENT_MANAGER"), "manager.procurement");
  assert.equal(profileForRoleCode("PAYABLES_RECEIVABLES"), "manager.payables");
  assert.equal(profileForRoleCode("FINANCIAL_CONTROLLER"), "manager.controller");
  assert.equal(profileForRoleCode("GENERAL_MANAGER"), "manager.general");
  assert.equal(profileForRoleCode("HR"), "admin");
  assert.equal(profileForRoleCode("SUPER_ADMIN"), "admin");
  assert.equal(profileForRoleCode("SOMETHING_ELSE"), "counter");
});

test("the job shape catalog groups the dealer's role names under their profiles", () => {
  const roles = [
    { code: "GENERAL_MANAGER", name: "General Manager", tier: 1, scope: "GLOBAL" },
    { code: "SUPER_ADMIN", name: "Super Admin", tier: 1, scope: "GLOBAL" },
    { code: "YARD_TEAM", name: "Yard Team", tier: 3, scope: "BRANCH" },
    { code: "YARD_MANAGER", name: "Yard Manager", tier: 2, scope: "BRANCH" },
    { code: "DRIVERS", name: "Drivers", tier: 3, scope: "BRANCH" },
  ];
  assert.deepEqual(rolesForShape("manager.general", roles), ["General Manager"]);
  assert.deepEqual(rolesForShape("admin", roles), ["Super Admin"]);
  assert.deepEqual(rolesForShape("yard", roles), ["Yard Team"]);
  assert.deepEqual(rolesForShape("manager.yard", roles), ["Yard Manager"]);
  assert.deepEqual(rolesForShape("driver", roles), ["Drivers"]);
  assert.deepEqual(rolesForShape("manager", roles), []);
  assert.deepEqual(rolesForShape("counter", roles), []);
});

test("a cell with no rows says the surface shows its own registry default; a curated cell counts its rows by kind", () => {
  assert.equal(cellWords([]), "Registry default");
  assert.equal(
    cellWords([
      { kind: "column", id: "a", position: 0, visible: true },
      { kind: "column", id: "b", position: 1, visible: false },
      { kind: "action", id: "c", position: 2, visible: true },
    ]),
    "2 columns, 1 action",
  );
  assert.equal(cellWords([{ kind: "panel", id: "a", position: 0, visible: true }]), "1 panel");
});

test("an item row needs one of the four kinds, an id in the registry alphabet, and a position that is not negative", () => {
  assert.equal(itemRowError({ kind: "column", id: "cost", position: 0, visible: true }), "");
  assert.equal(
    itemRowError({ kind: "sort", id: "cost", position: 0, visible: true }),
    "Item kind must be panel, column, chip or action.",
  );
  assert.equal(
    itemRowError({ kind: "column", id: "Cost!", position: 0, visible: true }),
    "Item id \u201cCost!\u201d must be 1 to 64 lowercase letters, digits, dots, dashes or underscores.",
  );
  assert.equal(
    itemRowError({ kind: "column", id: "cost", position: -1, visible: true }),
    "Item positions cannot be negative.",
  );
});

test("a duplicate row is refused in the core's words, and an empty set names DELETE as the reset", () => {
  assert.equal(
    itemSetError([
      { kind: "column", id: "cost", position: 0, visible: true },
      { kind: "column", id: "cost", position: 1, visible: true },
    ]),
    "Item column/cost appears twice.",
  );
  assert.equal(
    itemSetError([]),
    "The item set is empty; to reset, use the kind's own reset.",
  );
  const many = Array.from({ length: 129 }, (_, i) => ({ kind: "column", id: `c${i}`, position: i, visible: true }));
  assert.equal(itemSetError(many), "At most 128 items per write.");
});

test("a workspace split outside 20 to 80 is refused in the core's words", () => {
  assert.equal(workspaceSplitError(58), "");
  assert.equal(workspaceSplitError(20), "");
  assert.equal(workspaceSplitError(80), "");
  assert.equal(workspaceSplitError(19), "Workspace split must be between 20 and 80.");
  assert.equal(workspaceSplitError(81), "Workspace split must be between 20 and 80.");
});

test("a refused read carries the server's own sentence whole; a bare word becomes plain words", () => {
  assert.equal(
    configRefusalWords(
      "settings: only the General Manager, the Financial Controller, HR or the Platform Administrator see another profile's dealer screen settings",
      403,
    ),
    "settings: only the General Manager, the Financial Controller, HR or the Platform Administrator see another profile's dealer screen settings",
  );
  assert.equal(configRefusalWords("", undefined), "Try again in a moment.");
});

test("each workspace save carries a fresh idempotency key", () => {
  const a = newWorkspaceKey();
  const b = newWorkspaceKey();
  assert.notEqual(a, b);
  assert.match(a, /^[0-9a-f-]{36}$/);
});

// -- the door ----------------------------------------------------------------------------------------

function apiAnswering(answer) {
  const calls = [];
  const fetch = async (url, init) => {
    calls.push({ url: String(url), init: init ?? {} });
    return new Response(JSON.stringify(answer.body), {
      status: answer.status ?? 200,
      headers: { "content-type": "application/json" },
    });
  };
  return { api: new ConfigApi(new ApiClient({ baseUrl: "http://core.test", fetch })), calls };
}

test("the cell read asks the core for one surface and one job shape", async () => {
  const { api, calls } = apiAnswering({
    body: {
      surface_id: "counter-till",
      profile: "counter",
      items_profile: "counter",
      role_items: [{ kind: "column", id: "cost", position: 0, visible: false }],
      user_items: [],
      workspace: { split_pct: 58, arrangement: "PRIMARY_LEFT", primary_collapsed: false, secondary_collapsed: false, cost_hidden: false, source: "dealer" },
    },
  });
  const result = await api.surfaceConfig("counter-till", "yard");
  assert.equal(result.ok, true);
  assert.equal(result.value.role_items.length, 1);
  assert.equal(result.value.role_items[0].id, "cost");
  assert.equal(calls.length, 1);
  assert.match(calls[0].url, /\/api\/v1\/surfaces\/counter-till\/config\?profile=yard$/);
});

test("a refused cell read answers the refusal, never a throw", async () => {
  const { api } = apiAnswering({
    status: 403,
    body: { error: "settings: only the General Manager, the Financial Controller, HR or the Platform Administrator see another profile's dealer screen settings" },
  });
  const result = await api.surfaceConfig("money-desk", "manager");
  assert.equal(result.ok, false);
  assert.equal(result.status, 403);
});

test("the role items write PUTs the whole set with the profile in the body", async () => {
  const { api, calls } = apiAnswering({ body: { saved: true } });
  const items = [{ kind: "column", id: "cost", position: 0, visible: false }];
  const result = await api.putRoleItems("counter-till", "yard", items);
  assert.equal(result.ok, true);
  assert.equal(calls[0].init.method, "PUT");
  assert.match(calls[0].url, /\/api\/v1\/surfaces\/counter-till\/items$/);
  assert.deepEqual(JSON.parse(calls[0].init.body), { profile: "yard", items });
});

test("the kind reset DELETEs with the profile and the kind in the query, never a body", async () => {
  const { api, calls } = apiAnswering({ body: { reset: true } });
  const result = await api.resetRoleItems("counter-till", "yard", "column");
  assert.equal(result.ok, true);
  assert.equal(calls[0].init.method, "DELETE");
  assert.match(calls[0].url, /\/api\/v1\/surfaces\/counter-till\/items\?profile=yard&kind=column$/);
});

test("my own items write and reset ride the my-items routes", async () => {
  const { api, calls } = apiAnswering({ body: { saved: true } });
  const items = [{ kind: "chip", id: "pavers", position: 0, visible: true }];
  assert.equal((await api.putMyItems("counter-till", items)).ok, true);
  assert.match(calls[0].url, /\/api\/v1\/surfaces\/counter-till\/my-items$/);
  assert.deepEqual(JSON.parse(calls[0].init.body), { items });
  assert.equal((await api.resetMyItems("counter-till")).ok, true);
  assert.equal(calls[1].init.method, "DELETE");
  assert.match(calls[1].url, /\/api\/v1\/surfaces\/counter-till\/my-items$/);
});

test("my workspace save PUTs with the idempotency key and reset DELETEs the row", async () => {
  const { api, calls } = apiAnswering({
    body: { split_pct: 58, arrangement: "PRIMARY_LEFT", primary_collapsed: false, secondary_collapsed: false, cost_hidden: false, source: "user" },
  });
  const save = {
    split_pct: 58,
    arrangement: "PRIMARY_LEFT",
    primary_collapsed: false,
    secondary_collapsed: false,
    cost_hidden: false,
    open_ref: "",
    idempotency_key: newWorkspaceKey(),
  };
  assert.equal((await api.putMyWorkspace("counter-till", save)).ok, true);
  assert.equal(calls[0].init.method, "PUT");
  assert.match(calls[0].url, /\/api\/v1\/surfaces\/counter-till\/my-workspace$/);
  assert.deepEqual(JSON.parse(calls[0].init.body), save);
  assert.equal((await api.resetMyWorkspace("counter-till")).ok, true);
  assert.equal(calls[1].init.method, "DELETE");
  assert.match(calls[1].url, /\/api\/v1\/surfaces\/counter-till\/my-workspace$/);
});

test("a bounded pool runs every task and never more than the limit at once", async () => {
  let running = 0;
  let peak = 0;
  const done = [];
  await inPool(Array.from({ length: 20 }, (_, i) => i), 4, async (n) => {
    running += 1;
    peak = Math.max(peak, running);
    await new Promise((r) => setTimeout(r, 1));
    running -= 1;
    done.push(n);
  });
  assert.equal(done.length, 20);
  assert.equal(peak, 4);
});
