// The inventory slice's plain logic: the words the surfaces say, the rules the
// buttons follow, and the check made before a request is sent. Nothing here
// computes money; the server decides every figure and every refusal, and this
// file only says them.
//
// Port provenance: the vocabulary and the transfer rules follow the original
// app's inventory-logic.ts and inventory-stock-logic.ts (repository
// hardscapeos_dibbits, commit 87d7941b, read only), gathered into one matrix
// the same way the original gathered them: lifecycle knowledge lives in one
// map, never branched inside a component. Every accessor degrades honestly: an
// unmapped value comes back as itself, lowercased with the underscores opened
// out, so a new state added to the schema shows up strange (a bug report)
// rather than confident and wrong (a lie nobody catches).

import { houseDay, stampDay } from "../po/po-logic.js";
import { formatQty } from "../po/po-money.js";

// ---- the ledger's words ---------------------------------------------------------------------------

const MOVE_TYPE_WORD: Record<string, string> = {
  RECEIPT: "Receipt",
  TRANSFER: "Transfer",
  PICK: "Pick",
  SHIP: "Shipment",
  ADJUST: "Adjustment",
  COUNT: "Count",
  BREAK_PALLET: "Pallet break",
  RETURN: "Return",
};

export function moveTypeWord(moveType: string): string {
  return MOVE_TYPE_WORD[moveType] ?? moveType.toLowerCase().replace(/_/g, " ");
}

const MOVE_DIRECTION_WORD: Record<string, string> = { IN: "In", OUT: "Out" };

export function moveDirectionWord(direction: string): string {
  return MOVE_DIRECTION_WORD[direction] ?? direction.toLowerCase().replace(/_/g, " ");
}

// ---- transfers --------------------------------------------------------------------------------------

const TRANSFER_STATUS_WORD: Record<string, string> = {
  DRAFT: "Draft",
  IN_TRANSIT: "In transit",
  RECEIVED: "Received",
  CANCELLED: "Cancelled",
};

export function transferStatusWord(status: string): string {
  return TRANSFER_STATUS_WORD[status] ?? status.toLowerCase().replace(/_/g, " ");
}

/** A transfer line's units still on the truck: the shipped less the received. */
export function inTransitBase(line: { qty_shipped_base: number; qty_received_base: number }): number {
  return line.qty_shipped_base - line.qty_received_base;
}

/**
 * Which buttons a transfer offers, from its status alone. There is no cancel
 * on a shipped transfer, deliberately: the stock has physically moved, and the
 * correction is a transfer back, not an erasure of the one that happened.
 */
export function transferActions(status: string): { ship: boolean; receive: boolean; cancel: boolean } {
  if (status === "DRAFT") return { ship: true, receive: false, cancel: true };
  if (status === "IN_TRANSIT") return { ship: false, receive: true, cancel: false };
  return { ship: false, receive: false, cancel: false };
}

/** One configured route, as the transit-days read answers it. */
export interface TransitPair {
  from_branch_id?: string;
  to_branch_id?: string;
  transit_days?: number;
  /** false means no row for this route: it promises nothing, which is not the same as 0 days. */
  configured?: boolean;
}

/**
 * The configured days for one ordered pair, or null when the route has no row.
 * Null, not 0: "nobody has said how long this run takes" and "this run arrives
 * the same day" are different answers.
 */
export function transitDaysFor(pairs: readonly TransitPair[] | undefined, fromBranchId: string, toBranchId: string): number | null {
  for (const p of pairs ?? []) {
    if (p.from_branch_id !== fromBranchId || p.to_branch_id !== toBranchId) continue;
    if (p.configured === false) return null;
    const days = p.transit_days;
    if (typeof days !== "number" || !Number.isFinite(days) || days < 0) return null;
    return Math.trunc(days);
  }
  return null;
}

/** The pair's configured time as the row's words: "1 day", "3 days", "not set". */
export function configWord(pair: TransitPair): string {
  if (pair.configured === false) return "not set";
  const days = pair.transit_days;
  if (typeof days !== "number" || !Number.isFinite(days) || days < 0) return "not set";
  return `${Math.trunc(days)} ${Math.trunc(days) === 1 ? "day" : "days"}`;
}

function dayNumber(date: string): number | null {
  const m = stampDay(date).match(/^(\d{4})-(\d{2})-(\d{2})$/);
  return m === null ? null : Math.floor(Date.UTC(Number(m[1]), Number(m[2]) - 1, Number(m[3])) / 86_400_000);
}

function dayOfNumber(number: number): string {
  const at = new Date(number * 86_400_000);
  const mm = String(at.getUTCMonth() + 1).padStart(2, "0");
  const dd = String(at.getUTCDate()).padStart(2, "0");
  return `${at.getUTCFullYear()}-${mm}-${dd}`;
}

/**
 * The ship box's pre-fill: the ship day plus the route's configured days, both
 * in the house zone. An unconfigured route pre-fills nothing, and nothing here
 * invents a date.
 */
export function defaultShipETA(pairs: readonly TransitPair[] | undefined, fromBranchId: string, toBranchId: string, today: string = houseDay()): string {
  const days = transitDaysFor(pairs, fromBranchId, toBranchId);
  if (days === null) return "";
  const base = dayNumber(today);
  return base === null ? "" : dayOfNumber(base + days);
}

// ---- holds and sales ---------------------------------------------------------------------------------

const HOLD_MODE_WORD: Record<string, string> = {
  HARD: "Hard hold",
  SOFT: "Soft hold",
  NONE: "No hold",
};

export function holdModeWord(mode: string): string {
  return HOLD_MODE_WORD[mode] ?? mode.toLowerCase().replace(/_/g, " ");
}

const RESERVATION_STATUS_WORD: Record<string, string> = {
  ACTIVE: "Active",
  RELEASED: "Released",
  CONSUMED: "Consumed",
  EXPIRED: "Expired",
};

export function reservationStatusWord(status: string): string {
  return RESERVATION_STATUS_WORD[status] ?? status.toLowerCase().replace(/_/g, " ");
}

const SALE_TYPE_WORD: Record<string, string> = {
  COLLECT_NOW: "Collect now",
  COLLECT_LATER: "Collect later",
  DELIVERED: "Delivered",
};

export function saleTypeWord(saleType: string): string {
  return SALE_TYPE_WORD[saleType] ?? saleType.toLowerCase().replace(/_/g, " ");
}

// ---- the check made before a request is sent ----------------------------------------------------------

export type QtyCheck = { ok: true; qty: number } | { ok: false; words: string };

/**
 * A counted or ordered quantity as typed: read through the decimal string the
 * way the money fields read their dollars, never through a float artefact.
 * A blank field is the caller's own "nothing typed".
 */
export function qtyFromTyped(text: string): QtyCheck {
  const value = text.trim();
  if (value === "") return { ok: false, words: "Write how many you counted." };
  if (/^-\d+(?:\.\d+)?$/.test(value)) return { ok: false, words: "A count is zero or more." };
  if (!/^\d+(?:\.\d+)?$/.test(value)) return { ok: false, words: "Write the quantity as a number." };
  const qty = Number(value);
  return Number.isFinite(qty) ? { ok: true, qty } : { ok: false, words: "Write the quantity as a number." };
}

export type ReorderCheck = { ok: true; point: number; reorderQty: number } | { ok: false; words: string };

/**
 * The reorder point and order quantity as typed. A blank field is refused in
 * words: an empty box read as a number is a zero, and a zero point saved by
 * accident turns the yard's reorder line off.
 */
export function reorderFieldsFromTyped(pointText: string, qtyText: string): ReorderCheck {
  const point = pointText.trim();
  const qty = qtyText.trim();
  if (point === "" || qty === "") return { ok: false, words: "A blank field is not a zero. Write the point and the order quantity, zero or more." };
  const number = /^\d+(?:\.\d+)?$/;
  if (!number.test(point) || !number.test(qty)) return { ok: false, words: "Write the point and the order quantity as numbers, zero or more." };
  return { ok: true, point: Number(point), reorderQty: Number(qty) };
}

// ---- the confirms for the presses that cannot be taken back ---------------------------------------------

/** What a confirm says: a title, the facts beneath it, and the label of the button that goes ahead. */
export interface ConfirmWords {
  readonly title: string;
  readonly lines: readonly string[];
  readonly go: string;
}

export function receiveTransferConfirm(
  t: { transfer_no: string; lines: readonly { product_name: string; uom: string; qty_shipped_base: number; qty_received_base: number }[] },
  toName: string,
): ConfirmWords {
  return {
    title: `Receive ${t.transfer_no} into ${toName}.`,
    lines: [
      ...t.lines.map((l) => `${l.product_name}: ${formatQty(inTransitBase(l), l.uom)} in transit`),
      `The truck is unloaded at ${toName} and the stock lands there at the cost it travelled with. Nothing moves until you confirm, and there is no undo short of a transfer back.`,
    ],
    go: "Confirm receive",
  };
}

export function cancelTransferConfirm(t: { transfer_no: string }): ConfirmWords {
  return {
    title: `Cancel ${t.transfer_no}.`,
    lines: ["Nothing moves. The draft is closed for good; raise a new one to try again."],
    go: "Confirm cancel",
  };
}

export function releaseConfirm(hold: { product: string; yard: string; qty: number }): ConfirmWords {
  return {
    title: `Release the hold on ${hold.product} at ${hold.yard}.`,
    lines: [`${formatQty(hold.qty, "")} goes back on the floor for anyone to sell. A hold can be placed again.`],
    go: "Confirm release",
  };
}

export function expireConfirm(): ConfirmWords {
  return {
    title: "Run the expiry sweep.",
    lines: ["This expires every expired hold at once. A hold still inside its time is left alone."],
    go: "Confirm sweep",
  };
}

export function bootstrapConfirm(yard: string): ConfirmWords {
  return {
    title: `Open a reorder line for every stocked product at ${yard}.`,
    lines: ["Each line starts at half of today's on hand. A line a person tuned is never overwritten."],
    go: "Confirm open lines",
  };
}

/** The seat a role code holds on this surface, per the role matrix: the Procurement Manager manages, the Yard Manager works, the rest read. */
export function seatFor(roleCode: string): "manage" | "work" | "read" {
  if (roleCode === "procurement_manager") return "manage";
  if (roleCode === "yard_manager") return "work";
  return "read";
}

// ---- words for a refusal ------------------------------------------------------------------------------

/**
 * What each walled move is, and who may do it. The walls are the role matrix
 * and the contract's x-access for this surface: the Procurement Manager holds
 * manage, the Yard Manager work (receives and counts), Payables and
 * Receivables reads; the po_writers list walls the ordering; the transit-days
 * write is the tech admin's tier alone.
 */
const WHO_MAY: Record<string, string> = {
  write:
    "Writing here is for the seats the role matrix holds at work or manage: the Procurement Manager and the Yard Manager among the demo roles. A read-only role can look but not touch.",
  reserve:
    "Holding stock, moving it between branches and setting the allocation policy are manage seats' moves on this surface: the Procurement Manager's among the demo roles.",
  manage:
    "Moving stock between branches, holding it, and setting points and policy are manage seats' moves on this surface: the Procurement Manager's among the demo roles.",
  receive:
    "Receiving an order into stock is a work move: the Yard Manager's and the Procurement Manager's among the demo roles.",
  "count-approve":
    "Approving a count and posting its variance are manager seats' moves: the Procurement Manager's among the demo roles. The maker never approves their own count.",
  "transit-days": "Setting a route's transit time is the tech admin's; no demo role on this box holds it.",
};

const SERVER_PREFIX = /^[a-z][a-z0-9]*: /;

function sentence(text: string): string {
  const bare = text.trim().replace(SERVER_PREFIX, "");
  if (bare === "") return "";
  const cased = bare.charAt(0).toUpperCase() + bare.slice(1);
  return /[.!?]$/.test(cased) ? cased : `${cased}.`;
}

/**
 * A refusal or failure as the words a person reads. A 403 from the role gate
 * names who may do the move; a 403 that carries the server's own sentence (the
 * branch fence's words) reads as it is, capitalised and ended.
 */
export function refusalWords(action: string, error: string, status: number | undefined): string {
  if (status === 403) {
    const words = sentence(error);
    if (words !== "" && words !== "Forbidden.") return words;
    return WHO_MAY[action] ?? "Your role may not do that. Ask the General Manager.";
  }
  if (status === 401) return "Your sign in has ended. Sign in again.";
  if (status === undefined) return "The server could not be reached. Nothing was changed. Try again.";
  if (status >= 500) return "The server hit a problem. Nothing was changed. Try again.";
  const words = sentence(error);
  return words === "" ? "The server refused that." : words;
}
