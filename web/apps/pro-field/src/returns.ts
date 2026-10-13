// The returns words and the one piece of return math that is not money: what a
// person may ask to send back. The dealer's money never moves here and nothing
// is computed about prices; the quantities are checked against what the
// dealer's eligibility answer says is left, and the request itself is the
// server's to accept.
import type { TenantReturnableLine, TenantReturnRequestLineInput } from "@hardscapehouse/api-client";

export const RETURN_STATUS_WORDS: Readonly<Record<string, string>> = {
  PENDING: "Waiting",
  APPROVED: "Approved",
  REJECTED: "Declined",
};

export const RETURN_REASON_WORDS: Readonly<Record<string, string>> = {
  DAMAGED: "Arrived damaged",
  DEFECT: "Faulty",
  WRONG_ITEM: "Wrong item",
  NO_LONGER_NEEDED: "No longer needed",
  OTHER: "Something else",
};

export type ChipTone = "neutral" | "good" | "watch" | "stop";

const STATUS_TONES: Readonly<Record<string, ChipTone>> = { PENDING: "watch", APPROVED: "good", REJECTED: "stop" };

export function returnChipTone(status: string): ChipTone {
  return Object.prototype.hasOwnProperty.call(STATUS_TONES, status) ? STATUS_TONES[status] : "neutral";
}

export function returnReasonWords(reason: string): string {
  return Object.prototype.hasOwnProperty.call(RETURN_REASON_WORDS, reason) ? RETURN_REASON_WORDS[reason] : "Something else";
}

/** One eligible line as the words a person checks: what came, what went back, what is left. */
export function returnLineWords(line: TenantReturnableLine): string {
  const left = Math.max(0, line.qty_sold - line.qty_returned);
  return `${line.description}: ${left} of ${line.qty_sold} left to return`;
}

export type ReturnLinesResult = { ok: true; lines: TenantReturnRequestLineInput[] } | { ok: false; words: string };

/**
* The request's lines from the typed quantities: a whole number per line, at
* least one line asked for, never more than the dealer says is left. This is a
* count check only; every price and every decision stays the server's.
*/
export function returnRequestLines(typed: ReadonlyMap<string, string>, eligible: readonly TenantReturnableLine[]): ReturnLinesResult {
  const lines: TenantReturnRequestLineInput[] = [];
  for (const line of eligible) {
    const raw = typed.get(line.order_line_id) ?? "";
    if (raw.trim() === "") continue;
    if (!/^\d+$/.test(raw.trim())) return { ok: false, words: "Say how many to return as a whole number." };
    const qty = Number(raw.trim());
    const left = Math.max(0, line.qty_sold - line.qty_returned);
    if (qty > left) return { ok: false, words: `No more than ${left} of ${line.description} is left to return.` };
    if (qty > 0) lines.push({ order_line_id: line.order_line_id, qty_base: qty });
  }
  if (lines.length === 0) return { ok: false, words: "Say how many of at least one line to return." };
  return { ok: true, lines };
}
