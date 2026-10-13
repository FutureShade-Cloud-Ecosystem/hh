// The order's journey as a contractor reads it: which steps happened, which
// one is live, what is still to come. Ported from the original HH Pro demo's
// tracking selector and timeline (ref-hh-pro src/core/selectors/tracking.ts,
// src/ui/components/order/OrderTimeline.tsx), derived here from the order's
// own state through the contract's state map (the kit's board.ts).
//
// The map is the truth and this file keeps its silences: a READY delivery's
// road step and a part delivered order's remainder are a delivery stop's to
// speak for, and no tenant route serves stops, so those steps never read as
// current here and the order's own status chip carries the words instead. A
// cancelled order stops rather than promising steps.
//
// Proof and the truck: the 52 operations carry no proof of delivery and no
// truck route, so the journey's delivered step and the fulfillment word are
// what the contract gives, and this file invents nothing past them.
import type { TenantOrder } from "@hardscapehouse/api-client";
import { TRACKING_WORDS, calendarDay, houseDay, trackingOf, type Tracking } from "@hardscapehouse/pro-kit";
import { roadStep } from "./sections.js";

export type StepState = "done" | "current" | "upcoming";

export interface StepView {
  tracking: Tracking;
  words: string;
  state: StepState;
  /** The day the step is known to have happened, where the order carries one. */
  day?: string;
}

/** The journey's steps for a fulfillment: a delivery goes out on a truck, a pickup waits at the yard. */
export function flowFor(fulfillment: string): Tracking[] {
  return fulfillment === "DELIVERY"
    ? ["submitted", "confirmed", "picking", "out-for-delivery", "delivered"]
    : ["submitted", "confirmed", "picking", "ready-willcall", "delivered"];
}

export function timelineSteps(order: Pick<TenantOrder, "status" | "fulfillment"> & { created_at?: string }): StepView[] {
  const flow = flowFor(order.fulfillment);
  const tracking = trackingOf(order.status, order.fulfillment);
  if (tracking === "cancelled") {
    return flow.map((step) => ({ tracking: step, words: TRACKING_WORDS[step], state: "upcoming" as const }));
  }
  let currentIndex = -1;
  let doneRank: number;
  if (tracking !== null && flow.includes(tracking)) {
    currentIndex = flow.indexOf(tracking);
    doneRank = currentIndex;
  } else if (tracking === "invoiced") {
    doneRank = flow.length;
  } else if (order.status === "READY") {
    // Ready at the yard on a delivery: picking is done, the road is a stop's word.
    doneRank = flow.indexOf("picking") + 1;
  } else {
    // Part delivered: a truck went out; what is still to come is not known here.
    doneRank = flow.indexOf(roadStep(order.fulfillment)) + 1;
  }
  return flow.map((step, index) => ({
    tracking: step,
    words: TRACKING_WORDS[step],
    state: index < doneRank ? ("done" as const) : index === currentIndex ? ("current" as const) : ("upcoming" as const),
    day: step === "submitted" && typeof order.created_at === "string" ? houseDayOf(order.created_at) : undefined,
  }));
}

// The submitted step's day: created_at is an instant, so it reads as the dealer's own day.
function houseDayOf(iso: string): string | undefined {
  const words = houseDay(iso);
  return words === "n/a" ? undefined : words;
}

const FULFILLMENT_WORDS: Readonly<Record<string, string>> = { PICKUP: "Pickup", DELIVERY: "Delivery", WILL_CALL: "Will call" };

/** The fulfillment as the words a person uses; a string the contract does not ship reads as nothing. */
export function fulfillmentWords(fulfillment: string): string {
  return Object.prototype.hasOwnProperty.call(FULFILLMENT_WORDS, fulfillment) ? FULFILLMENT_WORDS[fulfillment] : "";
}

export interface PromisedWords {
  label: string;
  day: string;
}

/** The asked-for day, the journey's headline answer: promised for a delivery, ready by for a pickup. */
export function promisedWords(order: Pick<TenantOrder, "fulfillment" | "requested_date">): PromisedWords {
  return {
    label: order.fulfillment === "DELIVERY" ? "Promised for" : "Ready by",
    day: calendarDay(order.requested_date),
  };
}

/**
* Whether the order is past its asked-for day and still live, told in words.
* The dealer's day comes from the caller (houseToday), so this stays a pure
* comparison of bare days in the dealer's zone.
*/
export function lateWords(order: Pick<TenantOrder, "status" | "fulfillment" | "requested_date">, today: string): string {
  const live = ["CONFIRMED", "PICKING", "READY", "PARTIALLY_FULFILLED"];
  if (!live.includes(order.status)) return "";
  if (typeof order.requested_date !== "string" || !/^\d{4}-\d{2}-\d{2}$/.test(order.requested_date)) return "";
  if (order.requested_date >= today) return "";
  return order.fulfillment === "DELIVERY"
    ? "Past the requested day and not delivered yet. Ask the dealer where it stands."
    : "Past the requested day and not collected yet. Ask the dealer where it stands.";
}

/** The dealer's own today as a bare day (YYYY-MM-DD), from the clock, never written down anywhere. */
export function houseToday(now: Date = new Date()): string {
  return new Intl.DateTimeFormat("en-CA", { timeZone: "America/Los_Angeles", year: "numeric", month: "2-digit", day: "2-digit" }).format(now);
}

// ---- the delivery instructions ------------------------------------------------------------------
// The core calls the write "only meaningful for a DELIVERY order that is not
// yet fulfilled" (core/internal/order/service.go), so the form follows that:
// live deliveries take notes, everything else reads them.

const DISPATCHED: readonly string[] = ["FULFILLED", "INVOICED", "CLOSED"];

/** Whether this person, on this order, sees the notes form rather than the notes. */
export function instructionsEditable(order: Pick<TenantOrder, "status" | "fulfillment">, mayEdit: boolean): boolean {
  if (!mayEdit) return false;
  if (order.fulfillment !== "DELIVERY") return false;
  return !DISPATCHED.includes(order.status) && order.status !== "CANCELLED";
}

/** The one line that says why the notes read only, when they do. */
export function instructionsNote(order: Pick<TenantOrder, "status" | "fulfillment">, mayEdit: boolean): string {
  if (!mayEdit) return "Your role reads these notes; changing them is another role's to do.";
  if (order.fulfillment !== "DELIVERY") return "This order is not a delivery, so its notes are set by the dealer.";
  if (DISPATCHED.includes(order.status)) return "The truck already left with these notes.";
  return "";
}
