// The per role landing (D48, Q8: "a manager role lands on its own Manager
// Home"; every other role lands on the first app of its catalog list the
// profile gives it). The table the host carries is a copy of the role
// catalog's micro_apps order; scripts/work-landing.test.ts pins the copy to
// docs/analysis/role-catalog.yaml, and this suite pins the rule.
import { test } from "node:test";
import assert from "node:assert/strict";
import { CATALOG_APP_ORDER, isManagerRole, landingFor } from "../src/landing.js";

test("a manager role lands on Manager Home when it holds it", () => {
  for (const role of [
    "general_manager",
    "branch_manager",
    "sales_manager",
    "yard_manager",
    "logistics_manager",
    "procurement_manager",
  ]) {
    assert.equal(isManagerRole(role), true, role);
    assert.equal(landingFor(role, ["counter-till", "dispatch-board", "manager-home", "money-desk"]), "manager-home", role);
  }
});

test("a role that is not a manager role lands on the first app of its catalog list that it holds", () => {
  assert.equal(isManagerRole("inside_sales"), false);
  assert.equal(landingFor("inside_sales", ["quote-desk", "counter-till", "money-desk"]), "counter-till");
  assert.equal(landingFor("outside_sales", ["dispatch-board", "quote-desk"]), "quote-desk");
  assert.equal(landingFor("financial_controller", ["manager-home", "money-desk", "dealer-admin"]), "money-desk");
  assert.equal(landingFor("payables_receivables", ["manager-home", "buying-and-stock", "money-desk"]), "money-desk");
  assert.equal(landingFor("hr", ["dealer-admin"]), "dealer-admin");
});

test("a manager role without Manager Home falls back to its catalog order", () => {
  assert.equal(landingFor("sales_manager", ["counter-till", "quote-desk"]), "quote-desk");
});

test("a role the catalog does not know lands on the first app it holds, and a role with none lands nowhere", () => {
  assert.equal(CATALOG_APP_ORDER["no_such_role"], undefined);
  assert.equal(landingFor("no_such_role", ["money-desk", "quote-desk"]), "money-desk");
  assert.equal(landingFor("driver", []), null);
  assert.equal(landingFor("inside_sales", []), null);
});

test("the landing is always one of the apps handed in, never a catalog app the role does not hold", () => {
  for (const role of Object.keys(CATALOG_APP_ORDER)) {
    for (const held of [["dealer-admin"], ["quote-desk", "money-desk"], ["house-messages"], []]) {
      const landed = landingFor(role, held);
      assert.ok(landed === null || held.includes(landed), `${role} landed on ${landed} outside ${held.join(",")}`);
    }
  }
});

test("the catalog copy holds the sixteen internal roles in the catalog's own order of apps", () => {
  assert.equal(Object.keys(CATALOG_APP_ORDER).length, 16);
  assert.deepEqual(CATALOG_APP_ORDER["inside_sales"], [
    "counter-till",
    "quote-desk",
    "buying-and-stock",
    "money-desk",
    "house-messages",
  ]);
  assert.equal(CATALOG_APP_ORDER["sales_manager"]?.[0], "manager-home");
});
