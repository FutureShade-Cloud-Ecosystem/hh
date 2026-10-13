// The per role landing. Two sources, both recorded:
//
//   - docs/analysis/role-catalog.yaml: each internal role's micro_apps list is
//     in landing order (the first app a role opens). CATALOG_APP_ORDER below
//     is a copy of those lists, verbatim; scripts/work-landing.test.ts pins
//     the copy to the catalog, so the table cannot drift from it.
//   - DECISIONS.md D48 (Q8 narrowed): "a manager role lands on its own
//     Manager Home". A manager role is a catalog role whose id ends in
//     `_manager`.
//
// So a manager role holding Manager Home lands on it; every other role lands
// on the first app of its catalog list that its switcher holds (the catalog
// lists apps of both shells: a Field app, or one the profile does not give
// the role, is skipped, never offered). Whether the landing becomes a served
// row in place of this table stays open at HH-6 (Q8); the switcher default
// seeded from the catalog is the plan's own default.
//
// This module imports nothing, so the repository's bun gate can load it.

export const CATALOG_APP_ORDER: Readonly<Record<string, readonly string[]>> = {
  general_manager: [
    "manager-home",
    "money-desk",
    "dealer-admin",
    "buying-and-stock",
    "counter-till",
    "dispatch-board",
    "quote-desk",
    "house-messages",
  ],
  financial_controller: ["money-desk", "manager-home", "dealer-admin", "buying-and-stock", "house-messages"],
  hr: ["dealer-admin", "house-messages"],
  branch_manager: [
    "manager-home",
    "dispatch-board",
    "counter-till",
    "buying-and-stock",
    "drive-run",
    "money-desk",
    "quote-desk",
    "house-messages",
  ],
  sales_manager: [
    "manager-home",
    "quote-desk",
    "counter-till",
    "dealer-admin",
    "buying-and-stock",
    "dispatch-board",
    "house-messages",
    "money-desk",
  ],
  yard_manager: [
    "yard-pick",
    "dispatch-board",
    "buying-and-stock",
    "manager-home",
    "drive-run",
    "money-desk",
    "house-messages",
  ],
  logistics_manager: ["dispatch-board", "manager-home", "drive-run", "house-messages"],
  procurement_manager: ["buying-and-stock", "manager-home", "money-desk", "house-messages"],
  payables_receivables: ["money-desk", "buying-and-stock", "manager-home", "house-messages"],
  inside_sales: ["counter-till", "quote-desk", "buying-and-stock", "money-desk", "house-messages"],
  outside_sales: ["quote-desk", "buying-and-stock", "dispatch-board", "money-desk", "house-messages"],
  yard_team: ["yard-pick", "buying-and-stock", "dispatch-board", "house-messages"],
  driver: ["drive-run", "house-messages"],
  super_admin: ["dealer-admin", "manager-home", "operator-console"],
  dealer_admin: ["dealer-admin", "operator-console"],
  fb_operator: ["operator-console"],
};

const MANAGER_HOME = "manager-home";

/** A manager role: a catalog role whose id ends in `_manager` (D48, Q8). */
export function isManagerRole(roleCode: string): boolean {
  return roleCode.endsWith("_manager");
}

/**
 * The app a role lands on, from the apps its switcher holds (ids, in the
 * switcher's order). Never an app outside `held`. A role the catalog does not
 * know lands on the first app it holds; a role that holds none lands nowhere
 * (null), and the host says so plainly instead of showing a blank frame.
 */
export function landingFor(roleCode: string, held: readonly string[]): string | null {
  if (held.length === 0) return null;
  if (isManagerRole(roleCode) && held.includes(MANAGER_HOME)) return MANAGER_HOME;
  const order = Object.hasOwn(CATALOG_APP_ORDER, roleCode) ? (CATALOG_APP_ORDER[roleCode] as readonly string[]) : [];
  for (const id of order) {
    if (held.includes(id)) return id;
  }
  return held[0] ?? null;
}
