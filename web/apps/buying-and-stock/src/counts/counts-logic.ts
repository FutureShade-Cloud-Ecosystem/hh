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
  if (!/^-?\d+(?:\.\d+)?$/.test(value)) return { ok: false, words: "Write the counted quantity as a number." };
  const qty = Number(value);
  return Number.isFinite(qty) ? { ok: true, qty } : { ok: false, words: "Write the counted quantity as a number." };
}

/** A counted line whose figure moved needs its reason code before the sheet can be submitted. */
export function varianceNeedsReason(line: { counted_qty_base: number | null; snapshot_qty_base: number }): boolean {
  if (line.counted_qty_base === null) return false;
  return line.counted_qty_base !== line.snapshot_qty_base;
}

// ---- the post confirm ----------------------------------------------------------------------------------------

/**
 * The confirm's words, naming the sheet, the branch and the value, so the
 * press is made with the facts in front of the checker. The money figure is
 * the server's summary, formatted here and computed nowhere.
 */
export function confirmPostWords(
  sheet: { sheet_no: string; branch_name: string },
  summary: Pick<InvCountVarianceSummary, "variance_lines" | "net_variance_value_cents">,
): string {
  const net = summary.net_variance_value_cents ?? 0;
  const dollars = (Math.abs(net) / 100).toLocaleString("en-CA", { minimumFractionDigits: 2, maximumFractionDigits: 2 });
  const sign = net < 0 ? "-" : net > 0 ? "+" : "";
  return `Post ${sheet.sheet_no} for ${sheet.branch_name}: ${summary.variance_lines} variance ${summary.variance_lines === 1 ? "line" : "lines"}, net ${sign}$${dollars}. This writes the stock moves and the reason-coded journal, and a re-post never doubles it.`;
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
