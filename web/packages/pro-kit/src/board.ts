// The contractor's board vocabulary, derived from the dealer's order state the
// way the contract's OrderCard x-state-map says (core/api/openapi.yaml). There
// is no board route; the board is this projection over the tenant routes'
// orders, projects and quotes, and test/board.test.mjs re-reads the contract's
// map on every run.
//
// Where the contract is silent this file is silent too: a state it does not
// map has no stage and no tracking (null), never a guess. In particular the
// out-for-delivery state comes from a delivery stop that is en route, and no
// tenant route serves stops, so it is not derivable here.
import type { Stage } from "./chips.js";

const ORDER_STAGE: Readonly<Record<string, Stage>> = {
  DRAFT: "plan",
  CONFIRMED: "order",
  PICKING: "order",
  READY: "order",
  PARTIALLY_FULFILLED: "order",
  FULFILLED: "order",
  INVOICED: "invoice",
  CLOSED: "invoice",
  CANCELLED: "plan",
};

/** The board column of a dealer order state, or null for a state this table does not know. */
export function stageOfOrder(status: string): Stage | null {
  return Object.prototype.hasOwnProperty.call(ORDER_STAGE, status) ? ORDER_STAGE[status] : null;
}

/** A planning project is in Plan (no dealer order exists yet); the contract maps no other project state. */
export function stageOfProject(status: string): Stage | null {
  return status === "PLANNING" ? "plan" : null;
}

/** A draft or sent dealer quote is at the quote desk, in Quote; the contract maps no other quote state. */
export function stageOfQuote(status: string): Stage | null {
  return status === "DRAFT" || status === "SENT" ? "quote" : null;
}

export const TRACKING_STATES = [
  "submitted",
  "confirmed",
  "picking",
  "ready-willcall",
  "out-for-delivery",
  "delivered",
  "invoiced",
  "cancelled",
] as const;

export type Tracking = (typeof TRACKING_STATES)[number];

export const TRACKING_WORDS: Readonly<Record<Tracking, string>> = {
  submitted: "Submitted",
  confirmed: "Confirmed",
  picking: "Picking",
  "ready-willcall": "Ready for pickup",
  "out-for-delivery": "Out for delivery",
  delivered: "Delivered",
  invoiced: "Invoiced",
  cancelled: "Cancelled",
};

/** The tracking state of an order from its state and fulfilment, or null where the contract leaves it to a delivery stop. */
export function trackingOf(status: string, fulfillment: string | null | undefined): Tracking | null {
  switch (status) {
    case "DRAFT":
      return "submitted";
    case "CONFIRMED":
      return "confirmed";
    case "PICKING":
      return "picking";
    case "READY":
      return fulfillment === "PICKUP" || fulfillment === "WILL_CALL" ? "ready-willcall" : null;
    case "FULFILLED":
      return "delivered";
    case "INVOICED":
    case "CLOSED":
      return "invoiced";
    case "CANCELLED":
      return "cancelled";
    default:
      return null;
  }
}
