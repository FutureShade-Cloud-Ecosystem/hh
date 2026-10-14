// The capability gate: what each contractor role may do, copied from the
// core's matrix (core/internal/portal/rbac.go, and its twin in
// core/internal/identity/portal.go that the route guards read). The core is
// the only security boundary: a capability the server refuses stays refused
// whatever this file says. This copy exists so a screen never offers an action
// the server will answer 403 to, and test/capabilities.test.mjs re-reads the
// core's source on every run, so a capability or role added there and not here
// fails the suite.
//
// The wire spelling is the contract's TenantCapabilities (snake case), the same
// keys GET /api/v1/tenant/me returns, so the gate and the answer share a shape.
import type { TenantCapabilities, TenantRole } from "@hardscapehouse/api-client";

/** The nine capabilities, in the core struct's order. */
export const CAPABILITIES = [
  "manage_users",
  "view_billing",
  "pay_invoices",
  "manage_payment_methods",
  "submit_rfq",
  "create_orders",
  "view_orders_deliveries",
  "edit_delivery_instructions",
  "manage_files",
] as const;

export type Capability = (typeof CAPABILITIES)[number];

/** The five stored roles (the database check set, widened for the split buyer personas). */
export const TENANT_ROLES = ["account_admin", "buyer", "buyer_no_pay", "ap_finance", "field_crew"] as const;

/** The words a person reads for each role. The stored string is never shown. */
export const ROLE_LABELS: Readonly<Record<TenantRole, string>> = {
  account_admin: "Owner",
  buyer: "Buyer",
  buyer_no_pay: "Project manager",
  ap_finance: "Accounts payable",
  field_crew: "Field crew",
};

const ALL: readonly Capability[] = CAPABILITIES;

const MATRIX: Readonly<Record<TenantRole, readonly Capability[]>> = {
  // Owner: full control of the account, its crew, money, procurement and files.
  account_admin: ALL,
  // Buyer: transacts and sees billing across the chain, no team management.
  buyer: [
    "view_billing",
    "pay_invoices",
    "manage_payment_methods",
    "submit_rfq",
    "create_orders",
    "view_orders_deliveries",
    "edit_delivery_instructions",
    "manage_files",
  ],
  // Project manager: Buyer minus the pay pair. Builds the plan, commits the order, cannot move money.
  buyer_no_pay: [
    "view_billing",
    "submit_rfq",
    "create_orders",
    "view_orders_deliveries",
    "edit_delivery_instructions",
    "manage_files",
  ],
  // Accounts payable: Buyer minus the buy trio. Pays and manages stored methods, cannot order.
  ap_finance: ["view_billing", "pay_invoices", "manage_payment_methods", "view_orders_deliveries", "manage_files"],
  // Field crew: orders and deliveries only, no financials, no team, no commits.
  field_crew: ["view_orders_deliveries", "edit_delivery_instructions"],
};

function isRole(role: unknown): role is TenantRole {
  return typeof role === "string" && Object.prototype.hasOwnProperty.call(MATRIX, role);
}

/** Whether a role holds a capability. An unknown role or capability grants nothing. */
export function can(role: string | null | undefined, capability: string): boolean {
  if (!isRole(role)) return false;
  return (MATRIX[role] as readonly string[]).includes(capability);
}

/** A role's capabilities in the wire shape GET /api/v1/tenant/me returns. An unknown role holds none. */
export function capabilitiesFor(role: string | null | undefined): TenantCapabilities {
  const held: Record<string, boolean> = {};
  for (const capability of CAPABILITIES) held[capability] = can(role, capability);
  return held as TenantCapabilities;
}

/** The label a person reads for a role; an unknown role reads as a plain word, never the stored string. */
export function roleLabel(role: string | null | undefined): string {
  return isRole(role) ? ROLE_LABELS[role] : "Contractor";
}
