// Item 9.8: the held order card's words and the hold slip, pure. The hold is the server's fact
// (the staff order read's pay_hold and the pay balance find's held_from_picking); this module
// reads it, holds the board's sentence, and composes the slip's document with every value escaped.

/** The line under the panel's title, board 03, word for word. */
export const HOLD_PANEL_LINE =
  "Cash On Demand means pay before release, never cash on delivery. The deposit put it in process; the hold lifts on its own when the last dollar lands.";

/** The panel's title when the server's own words are not on hand (it sends them whenever it holds). */
export const HOLD_TITLE_FALLBACK = "This order cannot reach picking until the balance is paid.";

/** What the staff order read says about the pay hold. */
export interface PayHold {
  held: boolean;
  words: string | null;
  inProcessWords: string | null;
  orderTotalCents: number;
  paidCents: number;
  owedCents: number;
}

const whole = (v: unknown): v is number => typeof v === "number" && Number.isSafeInteger(v);

/** pay_hold, whole or not at all: a half readable hold is not shown as a hold. */
export function readPayHold(raw: unknown): PayHold | null {
  if (raw === null || typeof raw !== "object" || Array.isArray(raw)) return null;
  const r = raw as Record<string, unknown>;
  if (typeof r["held_from_picking"] !== "boolean") return null;
  if (!whole(r["order_total_cents"]) || !whole(r["paid_cents"]) || !whole(r["owed_cents"])) return null;
  return {
    held: r["held_from_picking"],
    words: typeof r["words"] === "string" && r["words"] !== "" ? r["words"] : null,
    inProcessWords: typeof r["in_process_words"] === "string" && r["in_process_words"] !== "" ? r["in_process_words"] : null,
    orderTotalCents: r["order_total_cents"],
    paidCents: r["paid_cents"],
    owedCents: r["owed_cents"],
  };
}

/** The facts the held card shows. */
export interface HeldOrderCard {
  orderNo: string;
  customerName: string;
  /** The account type words under the title ("Cash On Demand account"), when known. */
  accountWords: string | null;
  inProcessWords: string | null;
  holdWords: string;
  orderTotalCents: number;
  /** The deposit paid and its method word ("Cash"); null when nothing is paid yet. */
  deposit: { methodWord: string; cents: number } | null;
  balanceDueCents: number;
  /** "On the order": what was sold, when the host has the lines. */
  lines: { description: string; qty: string; lineTotalCents: number | null }[];
}

export interface HoldSlip {
  orderNo: string;
  customerName: string;
  balanceDueCents: number;
  holdWords: string;
}

const ESCAPES: Record<string, string> = { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" };
const escapeText = (text: string): string => text.replace(/[&<>"']/g, (c) => ESCAPES[c] ?? c);

/**
 * The hold slip's document for the till's print seam (the same carriage the receipt prints through):
 * the order number, the customer, the balance due and the hold sentence. Every value is escaped, and
 * money arrives through the caller's one formatter, never as cents.
 */
export function holdSlipHtml(slip: HoldSlip, money: (cents: number) => string): string {
  return [
    "<!doctype html>",
    '<html lang="en"><head><meta charset="utf-8"><title>Hold slip</title>',
    "<style>body{font:14px/1.4 sans-serif;margin:16px;max-width:80mm}h1{font-size:16px;margin:0 0 8px}",
    "p{margin:0 0 6px}.big{font-size:20px;font-weight:700}</style></head><body>",
    "<h1>Held from picking</h1>",
    `<p>Order ${escapeText(slip.orderNo)}</p>`,
    `<p>${escapeText(slip.customerName)}</p>`,
    `<p class="big">Balance due ${escapeText(money(slip.balanceDueCents))}</p>`,
    `<p>${escapeText(slip.holdWords)}</p>`,
    "</body></html>",
  ].join("");
}

/** The held card for an order the Pay balance find answered, with the order read's words when it came. */
export function heldCardFromOwing(
  order: {
    orderNo: string;
    customerName: string;
    orderTotalCents: number;
    paidCents: number;
    stillOwedCents: number;
    holdWords: string | null;
    payments: { method: string }[];
  },
  hold: PayHold | null,
  methodWord: (method: string) => string,
): HeldOrderCard {
  return {
    orderNo: order.orderNo,
    customerName: order.customerName,
    accountWords: null,
    inProcessWords: hold?.inProcessWords ?? null,
    holdWords: hold?.words ?? order.holdWords ?? "",
    orderTotalCents: order.orderTotalCents,
    deposit: order.paidCents > 0 ? { methodWord: methodWord(order.payments[0]?.method ?? ""), cents: order.paidCents } : null,
    balanceDueCents: order.stillOwedCents,
    lines: [],
  };
}
