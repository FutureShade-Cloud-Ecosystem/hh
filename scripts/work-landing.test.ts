// work-landing.test.ts: the Work shell host's per role landing must not be
// able to disagree with docs/analysis/role-catalog.yaml (the catalog is the
// source: each role's micro_apps list is in landing order) or with the Work
// profile and launch map the shell ships. The host carries a copy of the
// catalog's orders (web/apps/work-shell/src/landing.ts); this test pins the
// copy, the manager rule (D48, Q8: a manager role lands on its own Manager
// Home) and the resulting table. Run from anywhere:
//   bun test scripts/work-landing.test.ts

import { describe, expect, test } from "bun:test";
import { YAML } from "bun";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { REPO_ROOT, PROFILE_FILES } from "./generate-field-profiles";
import { CATALOG_APP_ORDER, isManagerRole, landingFor } from "../web/apps/work-shell/src/landing";
import { launchableApps, switcherFor } from "../web/apps/work-shell/src/switcher";

interface CatalogRole {
  id: string;
  audience: string;
  micro_apps?: string[];
}

const catalog = YAML.parse(readFileSync(resolve(REPO_ROOT, "docs/analysis/role-catalog.yaml"), "utf8")) as {
  roles: CatalogRole[];
};
const internal = catalog.roles.filter((r) => r.audience === "internal");

const profile = JSON.parse(readFileSync(PROFILE_FILES.work, "utf8")) as {
  apps: { id: string; name: string; roles: string[] }[];
};
const script = readFileSync(resolve(REPO_ROOT, "tauri/hh-work/assemble-dist.sh"), "utf8");
const launch: Record<string, string> = {};
for (const m of (script.match(/LAUNCH_MAP=\(\n([\s\S]*?)\n\)/)?.[1] ?? "").matchAll(
  /"([a-z-]+) ([a-z0-9-]+) (\/[a-z0-9-]+\/)"/g,
)) {
  launch[m[1]] = m[3];
}
const apps = launchableApps(profile.apps, launch);

describe("the landing table against the role catalog", () => {
  test("the host's copy holds exactly the catalog's internal roles", () => {
    expect(Object.keys(CATALOG_APP_ORDER).sort()).toEqual(internal.map((r) => r.id).sort());
  });

  test("each role's order is the catalog's micro_apps order, verbatim", () => {
    for (const role of internal) {
      expect([...(CATALOG_APP_ORDER[role.id] ?? [])]).toEqual(role.micro_apps ?? []);
    }
  });

  test("a manager role is a catalog role whose id ends in _manager", () => {
    const managers = internal.filter((r) => isManagerRole(r.id)).map((r) => r.id);
    expect(managers.sort()).toEqual(
      internal
        .map((r) => r.id)
        .filter((id) => id.endsWith("_manager"))
        .sort(),
    );
    expect(managers.sort()).toEqual([
      "branch_manager",
      "general_manager",
      "logistics_manager",
      "procurement_manager",
      "sales_manager",
      "yard_manager",
    ]);
  });
});

describe("the landing every internal role gets in the Work shell", () => {
  // Written out by hand from the catalog orders, the profile's role lists and
  // the launch map, so a change to any of them is a reviewed change here.
  const EXPECTED: Record<string, string | null> = {
    general_manager: "manager-home",
    financial_controller: "money-desk",
    hr: "dealer-admin",
    branch_manager: "manager-home",
    sales_manager: "manager-home",
    yard_manager: "manager-home",
    logistics_manager: "manager-home",
    procurement_manager: "manager-home",
    payables_receivables: "money-desk",
    inside_sales: "counter-till",
    outside_sales: "quote-desk",
    yard_team: "buying-and-stock",
    driver: null,
    super_admin: "dealer-admin",
    dealer_admin: "dealer-admin",
    fb_operator: null,
  };

  test("the table names every internal role, no more", () => {
    expect(Object.keys(EXPECTED).sort()).toEqual(internal.map((r) => r.id).sort());
  });

  test.each(Object.entries(EXPECTED))("%s lands on %s", (role, landing) => {
    const held = switcherFor(apps, role).map((a) => a.id);
    expect(landingFor(role, held)).toBe(landing);
    if (landing !== null) expect(held).toContain(landing);
  });

  test("the landing of the two live office personas the brief names", () => {
    const held = (role: string) => switcherFor(apps, role).map((a) => a.id);
    expect(landingFor("inside_sales", held("inside_sales"))).toBe("counter-till");
    expect(landingFor("sales_manager", held("sales_manager"))).toBe("manager-home");
  });
});
