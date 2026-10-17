// P2-2 of the 2D review: a landing link opens a live app only for a role the
// access matrix gives something other than none on that app's surface. The
// table is closed and copied from core/internal/roleconfig/default.json.
import { test } from "node:test";
import assert from "node:assert/strict";
import { existsSync, readFileSync } from "node:fs";
import { appsFor } from "../src/role-apps.ts";
import { needsLink, widgetLink } from "../src/landing-links.ts";
import { needsRowTarget } from "../src/manager-logic.ts";

const MONEY = "Money Desk";
const BUYING = "Buying and Stock";
const TILL = "Counter Till";
const YARD = "Yard Pick";

const EXPECTED = {
  "manager.yard": [MONEY, BUYING, YARD],
  "manager.logistics": [],
  "manager.branch": [MONEY, BUYING, TILL],
  "manager.sales": [MONEY, BUYING, TILL],
  "manager.procurement": [MONEY, BUYING],
  "manager.payables": [MONEY, BUYING],
  "manager.controller": [MONEY, BUYING],
  "manager.general": [MONEY, BUYING, TILL],
};

for (const [profile, apps] of Object.entries(EXPECTED)) {
  test(`${profile} may open exactly ${apps.length === 0 ? "no live app" : apps.join(", ")}`, () => {
    assert.deepEqual([...appsFor(profile)].sort(), [...apps].sort());
  });
}

test("a profile the table does not know is not narrowed", () => {
  assert.equal(appsFor("manager.future"), null);
  assert.equal(appsFor(""), null);
});

test("the table is the matrix's non none cells", () => {
  const path = new URL("../../../../core/internal/roleconfig/default.json", import.meta.url);
  if (!existsSync(path)) return;
  const cells = JSON.parse(readFileSync(path, "utf8")).cells;
  const ROLE = {
    "manager.yard": "yard_manager", "manager.logistics": "logistics_manager", "manager.branch": "branch_manager",
    "manager.sales": "sales_manager", "manager.procurement": "procurement_manager", "manager.payables": "payables_receivables",
    "manager.controller": "financial_controller", "manager.general": "general_manager",
  };
  const SURFACE = { "money-desk": MONEY, "buying-and-stock": BUYING, "counter-till": TILL, "yard-pick": YARD };
  for (const [profile, role] of Object.entries(ROLE)) {
    const open = cells.filter((c) => c.role === role && c.surface in SURFACE && c.level !== "none").map((c) => SURFACE[c.surface]);
    assert.deepEqual([...appsFor(profile)].sort(), open.sort(), profile);
  }
});

test("a needs source or a card whose app the role may not open keeps the coming words", () => {
  assert.equal(needsLink("pos.parked", "manager.controller"), null);
  assert.equal(needsLink("tasks.stale_picks", "manager.logistics"), null);
  assert.equal(needsLink("weights.pending", "manager.logistics"), null);
  assert.deepEqual(needsLink("pos.parked", "manager.branch"), { app: TILL, href: "/till/" });
  assert.deepEqual(widgetLink({ module: "pos" }, "manager.payables"), { kind: "coming" });
  assert.deepEqual(widgetLink({ module: "tasks" }, "manager.general"), { kind: "coming" });
  assert.deepEqual(widgetLink({ module: "tasks" }, "manager.yard"), { kind: "app", app: YARD, href: "/yard/" });
  assert.deepEqual(widgetLink({ module: "billing" }, "manager.logistics"), { kind: "coming" });
  assert.deepEqual(widgetLink({ module: "warranty" }, "manager.logistics"), { kind: "view", view: "warranty" });
  const row = { source_key: "pos.parked", what: "", count: 1, age: "", severity: "good", module: "", unit: "", screen: "Parked tickets", view: "", action: "" };
  assert.equal("href" in needsRowTarget(row, "manager.controller"), false);
  assert.equal("href" in needsRowTarget(row, "manager.sales"), true);
});
