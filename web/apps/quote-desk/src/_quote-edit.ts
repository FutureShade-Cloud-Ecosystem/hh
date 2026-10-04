// Ported from hardscapeos_dibbits master 87d7941b app/src/_quote-edit.ts;
// adapted for HH.
//
// _quote-edit.ts: the quote edit offer's pure decision tables (the `_*.ts`
// house convention: DOM-free, unit-tested on the Node runner). The server is
// the only authority: editable_fields and can_convert ride every read, and
// these helpers only read them off the wire shape. A missing list means
// "offer nothing", never "offer everything": an older server that does not
// send the field must not unlock the whole surface.

import { centsAmount } from "@hardscapehouse/pos-kit/src/ported/_money-format.js";

/** The edit field vocabulary the server's editable_fields list carries. */
export const EDIT_LINE_QTY = "line_qty";
export const EDIT_LINE_ADD = "line_add";
export const EDIT_LINE_REMOVE = "line_remove";
export const EDIT_VALID_UNTIL = "valid_until";

/** The quote status column's own six states (the contract's enum). */
export const QUOTE_STATUSES = ["DRAFT", "SENT", "ACCEPTED", "CONVERTED", "EXPIRED", "LOST"] as const;

/** The view shape this app reads, field for field the contract's QuoteView. */
export interface QuoteViewLine {
  line_no: number;
  product_id: string;
  description: string;
  qty_entered: string;
  uom_entered: string;
  qty_base: string;
  unit_price_cents: number;
  price_uom: string;
  line_total_cents: number;
  rule_price_below_floor?: boolean;
  floor_cleared_by?: string;
}

export interface QuoteView {
  id: string;
  quote_no: string;
  customer_id: string;
  customer_name: string;
  branch_id: string;
  status: string;
  valid_until?: string;
  expired: boolean;
  editable_fields: string[];
  can_convert: boolean;
  owner_id?: string;
  owner_name?: string;
  lines: QuoteViewLine[];
  subtotal_cents: number;
  tax_cents: number;
  total_cents: number;
  updated_at: string;
}

const QUOTE_STATUS_LABEL: Record<string, string> = {
  DRAFT: "Draft",
  SENT: "Sent",
  ACCEPTED: "Accepted",
  CONVERTED: "Converted",
  EXPIRED: "Expired",
  LOST: "Lost",
};

/**
 * quoteStatusWord renders the chip form, degrading honestly: an unmapped
 * value comes back as itself lowercased rather than as an invented phrase, so
 * a seventh state the column one day carries shows up as strange instead of
 * as a confident lie.
 */
export function quoteStatusWord(status: string): string {
  return QUOTE_STATUS_LABEL[status] ?? status.toLowerCase().replace(/_/g, " ");
}

const QUOTE_STATUS_PHRASE: Record<string, string> = {
  DRAFT: "a draft",
  SENT: "sent",
  ACCEPTED: "accepted",
  CONVERTED: "converted",
  EXPIRED: "expired",
  LOST: "lost",
};

/** The same six states said mid sentence: "this quote is ..." needs an article. */
export function quoteStatusPhrase(status: string): string {
  return QUOTE_STATUS_PHRASE[status] ?? status.toLowerCase().replace(/_/g, " ");
}

/**
 * canEdit reports whether the SERVER offered this edit field. The client
 * never evaluates the edit state itself: rendering from the server's answer
 * is what makes it impossible to offer a control the server would refuse.
 */
export function canEdit(view: QuoteView | null, field: string): boolean {
  return view?.editable_fields?.includes(field) === true;
}

/** True when the caller may perform any edit at all on this quote. */
export function hasAnyEdit(view: QuoteView | null): boolean {
  return (view?.editable_fields?.length ?? 0) > 0;
}

/** The server's own quantity bound, mirrored for an immediate answer. */
export const MAX_QUOTE_QTY = 999999;

export function validateQty(raw: string): string | null {
  const s = raw.trim();
  if (s === "") return "Enter a quantity.";
  const n = Number(s);
  if (!Number.isFinite(n)) return "Quantity must be a number.";
  if (n <= 0) return "Quantity must be more than zero. Remove the line instead.";
  if (n > MAX_QUOTE_QTY) return `Quantity must be ${MAX_QUOTE_QTY.toLocaleString("en-CA")} or less.`;
  return null;
}

/**
 * expiryLabel renders the validity window for display. The empty case is the
 * one that matters: a quote with no valid_until never expires, and it reads
 * as that, explicitly, never as a dash.
 */
export function expiryLabel(view: Pick<QuoteView, "valid_until" | "expired"> | null): string {
  if (!view) return "not set";
  const vu = view.valid_until?.trim();
  if (!vu) return "No expiry";
  return view.expired ? `Expired ${vu}` : `Valid until ${vu}`;
}

/**
 * quoteErrorMessage maps a failed response to what the operator reads. The
 * server's own sentence is preferred wherever it carries one (403 names the
 * branch or the role, 409 names the frozen state), and nothing here invents
 * a manager rung: a refusal on this surface never means "find a manager for
 * a tier you lack", it means what the server said.
 */
export function quoteErrorMessage(status: number | undefined, detail: string, what: string): string {
  const d = detail.trim();
  if (status === 401) return `Not authenticated. Log in to ${what}.`;
  if (status === 403) return d || "This quote belongs to another branch.";
  if (status === 404) return "That quote no longer exists.";
  if (status === 409) return d || "This quote changed since you opened it. Reload.";
  if (status === 400 || status === 422) return d || `Rejected. Invalid values for ${what}.`;
  if (status === 503) return d || `Service unavailable. ${what}.`;
  return d || `Could not ${what}.`;
}

/** canConvert reads the server's own flag, the one true spelling of it. */
export function canConvert(view: QuoteView | null): boolean {
  return view?.can_convert === true;
}

/** The one money word this panel and the history speak: integer cents in, CA$ out, a minus when negative. */
export function moneyWord(cents: number): string {
  return `${cents < 0 ? "-" : ""}CA$${centsAmount(cents)}`;
}

/** validateDate accepts an empty value (which clears the expiry) or a real YYYY-MM-DD date. */
export function validateDate(raw: string): string | null {
  const s = raw.trim();
  if (s === "") return null; // clearing the expiry is deliberate, not an error
  if (!/^\d{4}-\d{2}-\d{2}$/.test(s)) return "Use the date format YYYY-MM-DD.";
  const d = new Date(`${s}T00:00:00Z`);
  if (Number.isNaN(d.getTime())) return "That is not a real date.";
  // The round trip guards a well formed but non-existent day.
  if (d.toISOString().slice(0, 10) !== s) return "That is not a real date.";
  return null;
}

/** The shortest query the catalogue boxes send, named so the announcement and the call cannot drift. */
export const CATALOGUE_MIN_QUERY = 2;

/**
 * catalogHitStatus is what a screen reader hears while typing in a product
 * search box. Silence is the failure mode: a list that changes under the
 * cursor with no announcement is nothing to someone who cannot see it.
 */
export function catalogHitStatus(opts: { query: string; count: number; subject?: string }): string {
  const subject = opts.subject !== undefined && opts.subject.trim() !== "" ? opts.subject.trim() : "products";
  const q = opts.query.trim();
  if (q.length < CATALOGUE_MIN_QUERY) return `Type at least ${CATALOGUE_MIN_QUERY} characters to search ${subject}.`;
  if (opts.count === 0) return `No ${subject} match ${q}.`;
  const one = subject.endsWith("s") ? subject.slice(0, -1) : subject;
  return `${opts.count} ${opts.count === 1 ? one : subject} match. Use the up and down arrows to choose.`;
}

/** The discount arm's own bound, mirrored for an immediate answer; blank clears the discount. */
export function validateDiscPct(raw: string): string | null {
  const s = raw.trim();
  if (s === "") return null;
  const n = Number(s);
  if (!Number.isInteger(n) || n < 0 || n > 100) return "Discount must be a whole percent from 0 to 100.";
  return null;
}

/** Only a quote still being offered can be shared: sharing an accepted or converted one invites an acceptance that can never be honoured. */
export function isShareable(view: Pick<QuoteView, "status" | "expired">): boolean {
  return (view.status === "DRAFT" || view.status === "SENT") && !view.expired;
}

/** Why a quote cannot be shared, or an empty string when it can. */
export function shareSentence(view: Pick<QuoteView, "status" | "expired">): string {
  if (isShareable(view)) return "";
  return view.expired
    ? "This quote has expired and cannot be shared."
    : `This quote is ${quoteStatusPhrase(view.status)} and cannot be shared.`;
}

/**
 * The sentence under a quote the server offers this caller no edit on. A
 * lapsed or settled quote says why it is frozen; an open quote the caller
 * may read but not change says that no changes are offered, and names its
 * owner when the server sent one. The app decides nothing here: it only
 * words what the missing offer means.
 */
export function frozenSentence(
  view: Pick<QuoteView, "status" | "expired" | "valid_until" | "owner_name">,
): string {
  if (view.expired) return `This quote expired on ${view.valid_until ?? ""} and can no longer be changed.`;
  if (view.status === "DRAFT" || view.status === "SENT") {
    const owner = view.owner_name !== undefined && view.owner_name !== "" ? ` It belongs to ${view.owner_name}.` : "";
    return `No changes are offered to you on this quote.${owner}`;
  }
  return `This quote is ${quoteStatusPhrase(view.status)} and can no longer be changed.`;
}

/** A share link row's state word. */
export function shareLinkState(link: { live: boolean; revokedAt: string | null }): "Active" | "Revoked" | "Expired" {
  if (link.live) return "Active";
  return link.revokedAt !== null ? "Revoked" : "Expired";
}

/** The comparison row's difference: none for the current line, else a signed amount. */
export function comparisonDeltaLabel(row: { is_current: boolean; delta_cents: number }): string {
  if (row.is_current) return "none";
  return `${row.delta_cents < 0 ? "-" : "+"}${moneyWord(Math.abs(row.delta_cents))}`;
}

/** The comparison row's basis: the pricing source, or the floor block with its policy. */
export function comparisonBasis(row: { blocked: boolean; source: string; block_policy?: string }): string {
  if (!row.blocked) return row.source;
  return row.block_policy !== undefined && row.block_policy !== "" ? `below floor (${row.block_policy})` : "below floor";
}

/**
 * The fulfilment choices at conversion. The default is the empty option, not
 * Pickup: an unchosen fulfilment says nothing to the server and lets its own
 * default apply. Delivery is deliberately absent: a delivery is a scheduled
 * stop booked in dispatch, not a word typed here.
 */
export const FULFILMENT_CHOICES: ReadonlyArray<{ value: "" | "WILL_CALL"; label: string }> = [
  { value: "", label: "Pickup at the counter (default)" },
  { value: "WILL_CALL", label: "Will-call: hold it on the rack" },
];

/** The convert request body: empty when nothing was chosen, the choice otherwise. */
export function convertBody(choice: string): { fulfillment?: "WILL_CALL" } {
  return choice === "WILL_CALL" ? { fulfillment: "WILL_CALL" } : {};
}
