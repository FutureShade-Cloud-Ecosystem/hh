// The app's own sections and the order surfaces. The frame's selector carries
// the catalogue's three field surfaces (the kit's surfaces.ts: Tracking,
// Deliveries, Pickups); they are this app's order filters. Projects, Team and
// Account are sections of this app the catalogue does not name, so they ride
// this app's own section row under the title; the catalogue entry for them is
// pro-kit's to add, and this file is where this app maps them meanwhile.
import type { Tracking } from "@hardscapehouse/pro-kit";

export type Section = "orders" | "projects" | "team" | "account";

export interface SectionRef {
  id: Section;
  label: string;
}

/** The sections a person is offered, capability first: orders and account for everyone. */
export function sectionsFor(holds: (capability: string) => boolean): SectionRef[] {
  const sections: SectionRef[] = [{ id: "orders", label: "Orders" }];
  if (holds("view_billing")) sections.push({ id: "projects", label: "Projects" });
  if (holds("manage_users")) sections.push({ id: "team", label: "Team" });
  sections.push({ id: "account", label: "Account" });
  return sections;
}

/** The filter a catalogue field surface puts on the orders list, or null for every order. */
export interface OrdersSurface {
  label: string;
  fulfillments: readonly string[] | null;
  emptyWords: string;
}

export const ORDERS_SURFACES: Readonly<Record<"tracking" | "deliveries" | "pickups", OrdersSurface>> = {
  tracking: {
    label: "Tracking",
    fulfillments: null,
    emptyWords: "No orders to track yet. They appear here once the dealer receives one.",
  },
  deliveries: {
    label: "Deliveries",
    fulfillments: ["DELIVERY"],
    emptyWords: "No deliveries on the way. When the dealer books one, it shows here.",
  },
  pickups: {
    label: "Pickups",
    fulfillments: ["PICKUP", "WILL_CALL"],
    emptyWords: "Nothing ready for pickup yet. It shows here once the yard has it ready.",
  },
};

export function ordersSurfaceOf(surfaceId: string): OrdersSurface | undefined {
  if (Object.prototype.hasOwnProperty.call(ORDERS_SURFACES, surfaceId)) {
    return ORDERS_SURFACES[surfaceId as keyof typeof ORDERS_SURFACES];
  }
  return undefined;
}

/** The road step of a flow: the one a delivery stop would speak for. */
export function roadStep(fulfillment: string): Tracking {
  return fulfillment === "DELIVERY" ? "out-for-delivery" : "ready-willcall";
}
