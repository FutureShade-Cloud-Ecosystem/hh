// Ported from hardscapeos_dibbits master 87d7941b app/src/_order-depth.ts (the
// availability chip helpers; the ERP has no separate pure file for this
// panel); adapted for HH.
//
// _quote-availability.ts: the availability chips' pure table (DOM-free, unit
// tested on the Node runner). The server grades each quote line against what
// is AVAILABLE at the quote's branch (on hand minus hard holds): GREEN covers
// the line, THIN covers some of it, NONE covers none, UNKNOWN means the seam
// is not reporting. The app only WORDS the grade. UNKNOWN is "not reported",
// never "none": a chip saying none would turn away a sale because a wire was
// loose, and a chip saying "not reported" must never be read as stock. Every
// grade carries a word as well as a look, so colour is never the only signal.
import { moneyWord } from "./_quote-edit.js";

export interface BranchStock {
  product_id: string;
  branch_id: string;
  branch_code: string;
  branch_name: string;
  on_hand_base: number;
  reserved_base: number;
  in_transit_base: number;
  available_base: number;
  soft_held_base: number;
  /** Rides only for a caller on the cost list. */
  avg_cost_cents?: number;
}

export interface LineAvailability {
  line_no: number;
  product_id: string;
  qty_base: number;
  grade: string;
  branch: BranchStock;
}

const CHIP: Record<string, string> = {
  GREEN: "chip green",
  THIN: "chip amber",
  NONE: "chip red",
  UNKNOWN: "chip",
};

/** The chip's look for a grade; a grade this app does not know reads as UNKNOWN, the neutral one. */
export function availabilityChip(grade: string): string {
  return CHIP[grade] ?? "chip";
}

/** formatQty trims the trailing zeros a base quantity carries. */
export function formatQty(q: number): string {
  if (!Number.isFinite(q)) return "0";
  return String(Math.round(q * 10000) / 10000);
}

/** The chip's words. An unreported or unknown grade prints no number at all, not even a zero. */
export function availabilityLabel(a: LineAvailability | null | undefined): string {
  if (!a) return "not reported";
  switch (a.grade) {
    case "GREEN":
      return `${formatQty(a.branch.available_base)} available`;
    case "THIN":
      return `only ${formatQty(a.branch.available_base)} of ${formatQty(a.qty_base)}`;
    case "NONE":
      return "none available";
    default:
      return "not reported";
  }
}

/** The peek is offered only behind a short line: a button on every row is noise that trains people to ignore it. */
export function shouldOfferPeek(a: LineAvailability | null | undefined): boolean {
  if (!a || !a.product_id) return false;
  return a.grade === "THIN" || a.grade === "NONE";
}

/** What other quotes soft hold at this branch, said once and only when there is some. */
export function softHeldNote(a: LineAvailability): string {
  return a.branch.soft_held_base > 0 ? `(${formatQty(a.branch.soft_held_base)} on other quotes, not held against you)` : "";
}

/**
 * One branch row of the cross branch peek. Every row the server returns is
 * shown (a missing branch reads as a bug, not an answer), this branch's own
 * marked. The average cost is appended only when the server sent one: an
 * absent value is absent, never a zero.
 */
export function branchRowLabel(b: BranchStock, currentBranchId: string): string {
  const here = b.branch_id === currentBranchId ? " (this branch)" : "";
  const stock = b.available_base > 0 ? `${formatQty(b.available_base)} available` : "none available";
  const inbound = b.in_transit_base > 0 ? ` (+${formatQty(b.in_transit_base)} inbound)` : "";
  const cost = b.avg_cost_cents === undefined ? "" : ` · avg cost ${moneyWord(b.avg_cost_cents)}`;
  return `${b.branch_code}${here}: ${stock}${inbound}${cost}`;
}
