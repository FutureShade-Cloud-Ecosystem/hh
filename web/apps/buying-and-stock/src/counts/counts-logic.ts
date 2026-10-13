// The counts slice's plain logic: the sheet's five states and their words, the
// maker-checker gates as rules a test can hold, and the checks made before a
// request is sent. Nothing here computes money; the server values every
// variance, and this file only says the figures.
//
// Port provenance: the sheet words and the recon verdict follow the original
// app's count-shared.ts and inventory-logic.ts (repository hardscapeos_dibbits,
// commit 87d7941b, read only). TIED and OUT_OF_TIE reuse the trial balance
// strip's own words, the same accounting fact the product already names.
import type { InvCountSheet, InvCountVarianceSummary } from "@hardscapehouse/api-client";
import { formatMoney, formatQty } from "../po/po-money.js";

// ---- the sheet's states -------------------------------------------------------------------------------

const SHEET_STATUS_WORD: Record<string, string> = {
  OPEN: "Open",
  FROZEN: "Frozen",
  COUNTED: "Counted",
  POSTED: "Posted",
  CANCELLED: "Cancelled",
};

export function sheetStatusWord(status: string): string {
  return SHEET_STATUS_WORD[status] ?? status.toLowerCase().replace(/_/g, " ");
}

export type Tone = "good" | "watch" | "stop" | "plain";

/** The pill's tone; the word is always beside it, so colour never carries the meaning alone. */
export function sheetStatusTone(status: string): Tone {
  if (status === "POSTED") return "good";
  if (status === "CANCELLED") return "stop";
  if (status === "COUNTED" || status === "FROZEN" || status === "OPEN") return "watch";
  return "plain";
}

const SHEET_SCOPE_WORD: Record<string, string> = {
  FULL: "Full count",
  AD_HOC: "Ad-hoc",
};

/** CATEGORY:<id> has no word to put in a cell: it degrades raw, which is the honest answer. */
export function sheetScopeWord(scope: string): string {
  return SHEET_SCOPE_WORD[scope] ?? scope.toLowerCase().replace(/_/g, " ");
}

// ---- the entry pane's one state -------------------------------------------------------------------------

/** What the count entry pane is showing: the freeze invitation, the count, or the read only sheet. */
export type EntryState = "freeze" | "entry" | "read";

export function entryState(sheet: Pick<InvCountSheet, "status">): EntryState {
  if (sheet.status === "OPEN") return "freeze";
  if (sheet.status === "FROZEN" || sheet.status === "COUNTED") return "entry";
  return "read";
}

// ---- the maker-checker gates -----------------------------------------------------------------------------

/** A checker who is not the maker can approve a COUNTED sheet. */
export function canApprove(sheet: Pick<InvCountSheet, "status" | "counted_by" | "approved_by">, checkerId: string): boolean {
  if (sheet.status !== "COUNTED") return false;
  if (sheet.approved_by !== undefined && sheet.approved_by !== "") return false;
  return sheet.counted_by !== checkerId;
}

/** An approved COUNTED sheet can post. */
export function canPost(sheet: Pick<InvCountSheet, "status" | "approved_by">): boolean {
  return sheet.status === "COUNTED" && sheet.approved_by !== undefined && sheet.approved_by !== "";
}

// ---- the entry's own checks --------------------------------------------------------------------------------

export type CountsCheck = { ok: true; qty: number } | { ok: false; words: string };

/**
 * A counted quantity as typed: read through the decimal string, never through
 * a float artefact. A blank field is refused here so a half-filled row is
 * never submitted as a zero.
 */
export function countsFromTyped(text: string): CountsCheck {
  const value = text.trim();
  if (value === "") return { ok: false, words: "Write how many you counted." };
  if (/^-\d+(?:\.\d+)?$/.test(value)) return { ok: false, words: "A count is zero or more." };
  if (!/^\d+(?:\.\d+)?$/.test(value)) return { ok: false, words: "Write the counted quantity as a number." };
  const qty = Number(value);
  return Number.isFinite(qty) ? { ok: true, qty } : { ok: false, words: "Write the counted quantity as a number." };
}

/** A counted line whose figure moved needs its reason code before the sheet can be submitted. */
export function varianceNeedsReason(line: { counted_qty_base: number | null; snapshot_qty_base: number }): boolean {
  if (line.counted_qty_base === null) return false;
  return line.counted_qty_base !== line.snapshot_qty_base;
}

// ---- the post confirm ----------------------------------------------------------------------------------------

/** One counted line as the confirm reads it: the product's label and the book and counted figures. */
export interface ConfirmLine {
  readonly product_label: string;
  readonly snapshot_qty_base: number;
  readonly counted_qty_base: number | null;
  readonly variance_base: number;
  readonly variance_value_cents?: number | null;
}

const UNVALUED = "valued at the yard's average cost when it posts";

/**
 * The confirm's words, naming the sheet, the branch and the value, so the
 * press is made with the facts in front of the checker. The core values a
 * variance only inside the post, so before it there is no figure to say: the
 * words say how it will be valued, never a zero. A figure appears only when the
 * server has valued every variance line, and is the server's, formatted.
 */
export function confirmPostWords(
  sheet: { sheet_no: string; branch_name: string },
  summary: { variance_lines: number; net_variance_value_cents?: InvCountVarianceSummary["net_variance_value_cents"] },
  lines: readonly ConfirmLine[] = [],
): string {
  const moved = lines.filter((l) => l.counted_qty_base !== null && l.variance_base !== 0);
  const net = summary.net_variance_value_cents;
  const allValued = typeof net === "number" && net !== 0 && moved.every((l) => typeof l.variance_value_cents === "number");
  const worth = allValued ? `net ${net < 0 ? "-" : "+"}${formatMoney(Math.abs(net))}` : UNVALUED;
  return `Post ${sheet.sheet_no} for ${sheet.branch_name}: ${summary.variance_lines} variance ${summary.variance_lines === 1 ? "line" : "lines"}, ${worth}. This writes the stock moves and the reason-coded journal, and a re-post never doubles it.`;
}

/** Each variance line as product, yard and book to counted quantity. */
export function confirmPostLines(branchName: string, lines: readonly ConfirmLine[]): string[] {
  return lines
    .filter((l) => l.counted_qty_base !== null && l.variance_base !== 0)
    .map((l) => `${l.product_label} at ${branchName}: book ${formatQty(l.snapshot_qty_base, "")} to counted ${formatQty(l.counted_qty_base, "")}`);
}

/** The cancel confirm's words: the sheet, the yard, and what is lost. */
export function confirmCancelWords(sheet: { sheet_no: string; branch_name: string }): string {
  return `Cancel ${sheet.sheet_no} for ${sheet.branch_name}. Nothing posts, the counted work on the sheet is lost, and a cancelled sheet cannot be reopened.`;
}

// ---- the recon ----------------------------------------------------------------------------------------------

const RECON_STATUS_WORD: Record<string, string> = {
  OPEN: "Open",
  TIED: "Tied",
  OUT_OF_TIE: "Out of tie",
};

export function reconStatusWord(status: string): string {
  return RECON_STATUS_WORD[status] ?? status.toLowerCase().replace(/_/g, " ");
}

export function reconStatusTone(status: string): Tone {
  if (status === "TIED") return "good";
  if (status === "OUT_OF_TIE") return "stop";
  return "watch";
}

/** The delta's tone: found money reads good, missing money reads stop, zero reads plain. */
export function signTone(cents: number | null | undefined): Tone {
  if (typeof cents !== "number" || cents === 0) return "plain";
  return cents > 0 ? "good" : "stop";
}

const MONTHS = ["January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"];

/** The period as the card names it: "March 2031". */
export function reconcilePeriodWord(year: number, month: number): string {
  return `${MONTHS[Math.min(Math.max(month, 1), 12) - 1]} ${year}`;
}
