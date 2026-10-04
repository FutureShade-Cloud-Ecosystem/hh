// Ported from hardscapeos_dibbits master 87d7941b app/src/_quote-edit.ts;
// adapted for HH.
//
// _quote-edit.ts: the quote edit offer's pure decision tables (the `_*.ts`
// house convention: DOM-free, unit-tested on the Node runner). The server is
// the only authority: editable_fields and can_convert ride every read, and
// these helpers only read them off the wire shape. A missing list means
// "offer nothing", never "offer everything": an older server that does not
// send the field must not unlock the whole surface.

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
