// The three Pro apps and their surfaces: the catalogue the frame's selector
// lists and the apps' landing choice reads. The names are the board's
// (HH: Pro, shell-selector-phone-light): Pro Procurement holds Board, Catalog,
// Quote studio and Projects; Pro Pay holds Invoices, Payment methods and
// Statements; Pro Field holds Tracking, Deliveries and Pickups.
//
// Each surface names the one capability that opens it, taken from the route it
// reads (core/internal/portal): a role that lacks it does not see the surface in
// the selector, and an app opened without it shows the refusal state, never an
// empty screen. The server stays the boundary; this only keeps the screen from
// offering what the server will refuse.
import type { Capability } from "./capabilities.js";

export type ProAppKey = "procurement" | "pay" | "field";

export interface ProSurface {
  readonly id: string;
  readonly label: string;
  readonly capability: Capability;
}

export interface ProApp {
  readonly key: ProAppKey;
  /** The words a person reads for the app. */
  readonly label: string;
  /** The app's path on the host that serves the three (the front door's prefix). */
  readonly path: string;
  readonly surfaces: readonly ProSurface[];
}

export const PRO_APPS: readonly ProApp[] = [
  {
    key: "procurement",
    label: "Pro Procurement",
    path: "/pro-procurement/",
    surfaces: [
      { id: "board", label: "Board", capability: "submit_rfq" },
      { id: "catalog", label: "Catalog", capability: "submit_rfq" },
      { id: "quote-studio", label: "Quote studio", capability: "submit_rfq" },
      { id: "projects", label: "Projects", capability: "view_billing" },
    ],
  },
  {
    key: "pay",
    label: "Pro Pay",
    path: "/pro-pay/",
    surfaces: [
      { id: "invoices", label: "Invoices", capability: "view_billing" },
      { id: "payment-methods", label: "Payment methods", capability: "manage_payment_methods" },
      { id: "statements", label: "Statements", capability: "view_billing" },
    ],
  },
  {
    key: "field",
    label: "Pro Field",
    path: "/pro-field/",
    surfaces: [
      { id: "tracking", label: "Tracking", capability: "view_orders_deliveries" },
      { id: "deliveries", label: "Deliveries", capability: "view_orders_deliveries" },
      { id: "pickups", label: "Pickups", capability: "view_orders_deliveries" },
    ],
  },
];

export function appOf(key: string): ProApp | undefined {
  return PRO_APPS.find((app) => app.key === key);
}

export function surfaceOf(app: ProAppKey, surfaceId: string): ProSurface | undefined {
  return appOf(app)?.surfaces.find((surface) => surface.id === surfaceId);
}

/** The surfaces of every app that a set of held capabilities opens, in catalogue order. */
export function openSurfaces(held: ReadonlySet<string>): { app: ProApp; surfaces: readonly ProSurface[] }[] {
  return PRO_APPS.map((app) => ({ app, surfaces: app.surfaces.filter((s) => held.has(s.capability)) })).filter((group) => group.surfaces.length > 0);
}
