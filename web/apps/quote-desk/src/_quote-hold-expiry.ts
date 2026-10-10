// Ported from hardscapeos_dibbits master 87d7941b app/src/_quote-hold-expiry.ts;
// adapted for HH.
//
// _quote-hold-expiry.ts: the decisions behind the expired soft holds status
// (DOM-free, unit tested on the Node runner). Three of them decide whether a
// rep is told the truth:
//
//   WHETHER THE STATUS EXISTS AT ALL: keyed on any_expired and on nothing else
//   THE SENTENCE: "N of M lines are no longer held", with M counted right
//   WHICH LINES ARE NAMED: product and quantity, because a count sends a rep
//   to compare two screens and a name lets them pick up the phone
//
// An expired hold is a state, not a fault: nothing failed, the stock went back
// to available on schedule. So the panel is a polite status region, never an
// alert, and it is silent when there is nothing to say (no holds, every hold
// standing, or a seam nobody wired). HH change: the ERP's panel took a
// can-rehold flag from the surface; here the app holds no such rule. The
// offer is the status's own and the server decides, refusing in its own words
// (a 409 on an expired quote), which the panel shows verbatim.

export interface ExpiredHold {
  reservation_id: string;
  product_id: string;
  product_name: string;
  sku: string;
  branch_id: string;
  qty_base: number;
  /** EXPIRED (the sweep timed it out) or RELEASED (something gave it back early). */
  status: string;
  expires_at?: string;
  ended_at?: string;
}

export interface SoftHoldExpiry {
  quote_id: string;
  active_count: number;
  expired: ExpiredHold[];
  any_expired: boolean;
}

/** What the panel renders. null means render nothing. */
export interface ExpiryBanner {
  headline: string;
  /** One per lapsed line: "12 · Clear Stone". */
  lines: string[];
  /** The offer is the status's own: the server decides, and refuses in words. */
  offerRehold: boolean;
}

export const REHOLD_LABEL = "Re-hold these lines";

/**
 * expiryHeadline is the sentence. M is the lines that WERE held: the ones
 * still standing plus the ones that lapsed, not the quote's line count (a
 * free text line never had a hold). The singular reads "1 of 3 lines is no
 * longer held": correct verb, so the screen reads as written, not generated.
 */
export function expiryHeadline(st: SoftHoldExpiry): string {
  const n = st.expired.length;
  const m = n + Math.max(0, st.active_count);
  if (n === 1) return `A stock hold on this quote has expired: 1 of ${m} lines is no longer held.`;
  return `Stock holds on this quote have expired: ${n} of ${m} lines are no longer held.`;
}

/** formatQty trims the trailing zeros a base quantity carries. */
export function formatQty(q: number): string {
  if (!Number.isFinite(q)) return "0";
  return String(Math.round(q * 10000) / 10000);
}

/**
 * expiredLineLabel names one lapsed line the way a rep says it aloud: how
 * much, of what, and when it lapsed when the server said. The product falls
 * back to its SKU and then to "an unnamed product", never a blank or an id. A
 * hold RELEASED early says so, quietly: the two answers to "why did this come
 * back" differ. `whenLabel` words a timestamp; the caller owns the locale.
 */
export function expiredLineLabel(h: ExpiredHold, whenLabel?: (iso: string) => string): string {
  const name = h.product_name?.trim() || h.sku?.trim() || "an unnamed product";
  const qty = formatQty(h.qty_base);
  const suffix = h.status === "RELEASED" ? " · released early" : "";
  const at = h.expires_at ?? h.ended_at;
  const when = whenLabel !== undefined && at !== undefined && at !== "" ? ` · lapsed ${whenLabel(at)}` : "";
  return `${qty} · ${name}${suffix}${when}`;
}

/**
 * expiryBanner is the whole decision: what to render, or nothing at all.
 * Keyed on any_expired, the one boolean the server derives from the list it
 * sends. An unwired seam renders nothing: a banner drawn from an unasked
 * question is noise. any_expired with an empty list is a contradiction the
 * panel must not paint a headline over.
 */
export function expiryBanner(
  st: SoftHoldExpiry | null,
  opts: { seamWired?: boolean; whenLabel?: (iso: string) => string } = {},
): ExpiryBanner | null {
  if (!st || opts.seamWired === false) return null;
  if (!st.any_expired) return null;
  const expired = st.expired ?? [];
  if (expired.length === 0) return null;
  return {
    headline: expiryHeadline({ ...st, expired }),
    lines: expired.map((h) => expiredLineLabel(h, opts.whenLabel)),
    offerRehold: true,
  };
}

/** The refusal of a re-hold: the server's own words verbatim, a fallback only when it sent none. */
export function reholdRefusal(_status: number | undefined, detail: string): string {
  const d = detail.trim();
  return d !== "" ? d : "The holds could not be re-held. Try again from the quote once it is editable.";
}

/** reholdResultNote reports what the re-hold actually did. */
export function reholdResultNote(applied: number): string {
  if (applied <= 0) return "Nothing could be re-held: this quote has no lines with stock to hold.";
  if (applied === 1) return "Re-held 1 line.";
  return `Re-held ${applied} lines.`;
}
