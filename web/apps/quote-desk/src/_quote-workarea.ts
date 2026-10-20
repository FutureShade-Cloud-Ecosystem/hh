// Ported from hardscapeos_dibbits master 87d7941b app/src/_quote-workarea.ts;
// adapted for HH.
//
// _quote-workarea.ts: the quote work area's pure decision tables (the `_*.ts`
// house convention: DOM-free, unit-tested on the Node runner).
//
// Money is never derived here: line totals, subtotal, delivery, tax and the grand total are the
// SERVER's stored figures, shown as the server answered them.
//
// The workspace geometry delegates to the shared kit's own functions
// (@hardscapehouse/pos-kit's _pos-workspace), reused, not forked.

import type { FlowStep, FlowStepId } from "@hardscapehouse/pos-kit/src/ported/_pos-flow.js";
import { houseDay } from "@hardscapehouse/pos-kit/src/ported/host-facts.js";
import { clampSplit, splitFromPointer, type Arrangement } from "@hardscapehouse/pos-kit/src/ported/_pos-workspace.js";

// ── the tracked (never gated) stepper: Customer, Products, Review and Send ────

type QuoteStepId = Extract<FlowStepId, "customer" | "items" | "payment">;

/**
 * quoteFlowSteps: the same report-not-wizard posture as the counter's flow.
 * Step state is DERIVED from the quote data; a click only scrolls, nothing
 * locks. The ids reuse the shipped FlowStepId vocabulary so the kit's flow
 * steps element renders this stepper verbatim; the labels carry the quote
 * meaning.
 */
export function quoteFlowSteps(view: { hasCustomer: boolean; lineCount: number; sentOrBeyond: boolean }): FlowStep[] {
  const complete: Record<QuoteStepId, boolean> = {
    customer: view.hasCustomer,
    items: view.lineCount > 0,
    payment: view.sentOrBeyond,
  };
  const order: Array<{ id: QuoteStepId; n: 1 | 2 | 3; label: string }> = [
    { id: "customer", n: 1, label: "Customer" },
    { id: "items", n: 2, label: "Products" },
    { id: "payment", n: 3, label: "Review & Send" },
  ];
  const firstOpen = order.find((s) => !complete[s.id]);
  return order.map((s) => ({
    id: s.id,
    n: s.n,
    label: s.label,
    complete: complete[s.id],
    current: firstOpen ? s.id === firstOpen.id : s.id === "payment",
  }));
}

// ── margin display (the server's glance, arranged, never computed) ────────────

export interface GlanceLineIn {
  line_no?: number;
  cost_cents?: number;
  margin_bps?: number;
  est_profit_cents?: number;
}

export interface GlanceIn {
  lines?: GlanceLineIn[];
  margin_bps?: number;
  est_profit_cents?: number;
  partial?: boolean;
}

/**
 * marginPresent: whether the server SENT margin data. Byte absence is the
 * gate: an empty glance means this caller gets nothing, and every margin
 * surface must vanish, an absent surface, never a masked value.
 */
export function marginPresent(g: GlanceIn | null | undefined): boolean {
  return Boolean(g && typeof g.margin_bps === "number");
}

/** "38.6%" from bps; an empty string when absent, so the caller hides the surface. */
export function marginPctLabel(bps: number | undefined): string {
  if (typeof bps !== "number" || !Number.isFinite(bps)) return "";
  return `${(bps / 100).toFixed(1)}%`;
}

/** The per-line margin cell: empty when the line's fields are absent. */
export function lineMarginLabel(g: GlanceIn | null | undefined, lineNo: number): string {
  const row = g?.lines?.find((l) => l.line_no === lineNo);
  return marginPctLabel(row?.margin_bps);
}

export function lineCostCents(g: GlanceIn | null | undefined, lineNo: number): number | null {
  const row = g?.lines?.find((l) => l.line_no === lineNo);
  return typeof row?.cost_cents === "number" ? row.cost_cents : null;
}

// ── the identity card ──────────────────────────────────────────────────────────

/** Initials chip text: first letters of the first two words, uppercased. */
export function initialsOf(name: string): string {
  const words = (name || "").trim().split(/\s+/).filter(Boolean);
  if (words.length === 0) return "?";
  return words
    .slice(0, 2)
    .map((w) => w[0]!.toUpperCase())
    .join("");
}

/**
 * creditLine: the "Account current, Credit available" line, composed from the
 * picker's stored credit facts. Facts only: no outstanding balance means
 * Account current; otherwise the outstanding amount is named, never hidden
 * behind a cheerful word.
 */
export function creditLine(
  c: { credit_limit_cents: number; open_balance_cents: number },
  fmt: (cents: number) => string,
): string {
  const available = c.credit_limit_cents - c.open_balance_cents;
  const availablePart = `Credit available ${fmt(Math.max(available, 0))}`;
  if (c.open_balance_cents <= 0) return `Account current · ${availablePart}`;
  return `Outstanding ${fmt(c.open_balance_cents)} · ${availablePart}`;
}

/** "Contractor" / "Retail" from the stored customer_type. */
export function customerTypeTag(customerType: string): string {
  const t = (customerType || "").trim().toUpperCase();
  if (t === "") return "";
  return t.charAt(0) + t.slice(1).toLowerCase();
}

// ── validity ───────────────────────────────────────────────────────────────────

export const VALIDITY_CHOICES = [7, 14, 30, 60, 90] as const;

/** The YYYY-MM-DD date `days` from an injected now (the injected clock rule). */
export function validUntilFromDays(days: number, now: Date): string {
  // The dealer's own day (the kit's house day), never the UTC clock: after eight in the evening in the dealer's zone UTC has rolled over.
  const today = Date.parse(`${houseDay(now)}T00:00:00Z`);
  return new Date(today + days * 86400000).toISOString().slice(0, 10);
}

/**
 * validityDaysOf: the whole days between an injected today and the stored
 * valid_until. null when the quote never expires.
 */
export function validityDaysOf(validUntil: string | undefined, now: Date): number | null {
  if (!validUntil) return null;
  const target = Date.parse(`${validUntil}T00:00:00Z`);
  if (!Number.isFinite(target)) return null;
  const today = Date.parse(`${houseDay(now)}T00:00:00Z`);
  return Math.round((target - today) / 86400000);
}

// ── the delivery fee ───────────────────────────────────────────────────────────

/**
 * parseDeliveryFee: a typed delivery fee in dollars, as integer cents, or the
 * words that refuse it. Blank is no fee. Anything that is not plain dollars and
 * cents ("$25", "1,250") is refused rather than saved as zero.
 */
export function parseDeliveryFee(raw: string): { cents: number } | { error: string } {
  const text = raw.trim();
  if (text === "") return { cents: 0 };
  const match = /^(\d{1,7})(?:\.(\d{1,2}))?$/.exec(text);
  if (match === null) {
    return { error: `"${text}" is not a dollar amount, so the delivery fee was not changed. Type it as 25 or 25.50.` };
  }
  const cents = Number(match[1]) * 100 + Number((match[2] ?? "").padEnd(2, "0"));
  return { cents };
}

// ── the workspace (geometry delegated to the kit, reused not forked) ──────────

export type QuoteArrangement = "PRIMARY_LEFT" | "PRIMARY_RIGHT";

function toKitArrangement(a: QuoteArrangement): Arrangement {
  return a === "PRIMARY_RIGHT" ? "SALE_LEFT" : "LOOKUP_LEFT";
}

export function quoteClampSplit(pct: number): number {
  return clampSplit(pct);
}

export function quoteSplitFromPointer(
  pointerX: number,
  boxLeft: number,
  boxWidth: number,
  arrangement: QuoteArrangement,
): number | null {
  return splitFromPointer(pointerX, boxLeft, boxWidth, toKitArrangement(arrangement));
}

/** The grid-template column tracks for the two panels and the divider. */
export function quotePanelTracks(
  splitPct: number,
  arrangement: QuoteArrangement,
  primaryCollapsed: boolean,
  secondaryCollapsed: boolean,
): string {
  const s = quoteClampSplit(splitPct);
  const primary = primaryCollapsed ? "44px" : `minmax(320px, ${s}fr)`;
  const secondary = secondaryCollapsed ? "44px" : `minmax(300px, ${100 - s}fr)`;
  return arrangement === "PRIMARY_RIGHT" ? `${secondary} 10px ${primary}` : `${primary} 10px ${secondary}`;
}

/** At least one panel is always open: collapsing the last open one restores the other. */
export function collapseNext(
  which: "primary" | "secondary",
  primaryCollapsed: boolean,
  secondaryCollapsed: boolean,
): { primaryCollapsed: boolean; secondaryCollapsed: boolean } {
  if (which === "primary") {
    const p = !primaryCollapsed;
    return { primaryCollapsed: p, secondaryCollapsed: p ? false : secondaryCollapsed };
  }
  const sc = !secondaryCollapsed;
  return { primaryCollapsed: sc ? false : primaryCollapsed, secondaryCollapsed: sc };
}

// ── send channels ──────────────────────────────────────────────────────────────

export interface SendPolicyIn {
  send_email_enabled?: boolean;
  send_sms_enabled?: boolean;
}

/**
 * Whether Send can text this customer: the dealer policy allows texts and the core reports the customer's
 * permission. The desk cannot email a quote (the core has no email send), so there is no email choice; a quote
 * the desk cannot text is sent by copying the link.
 */
export interface TextAllowance {
  allowed: boolean;
  /** The customer's number on file, masked by the core; empty when none is known. */
  phoneMasked: string;
  /** Why not, in the dealer's words, when not allowed. */
  why: string;
}

export function textAllowance(
  p: SendPolicyIn | null | undefined,
  consent: { consent: boolean; phoneMasked: string } | "unknown" | null,
): TextAllowance {
  if (p?.send_sms_enabled === false) return { allowed: false, phoneMasked: "", why: "texts are switched off for this dealer" };
  if (consent === null || consent === "unknown") return { allowed: false, phoneMasked: "", why: "this customer's text permission could not be checked" };
  if (!consent.consent) return { allowed: false, phoneMasked: consent.phoneMasked, why: "texts are not allowed for this customer" };
  return { allowed: true, phoneMasked: consent.phoneMasked, why: "" };
}

/** The Send button's caption: how Send will reach the customer. */
export function sendCaption(t: TextAllowance): string {
  return t.allowed ? "Sends by text" : "Gives you the link to copy";
}

// ── refusals and confirmations in counter words ───────────────────────────────

/**
 * spokenRefusal: the server's sentence, spoken for a counter person. Every
 * module prefix (the core wraps them, so "quote: pricing: ..." arrives with
 * two) and every internal enum, parenthesised or bare, are stripped. The
 * below-floor sentence's way forward is NOT appended here: that refusal is
 * typed on this surface (floor-refusal.ts speaks it with the approvers
 * named), and a second scrubber guessing at it would hide the typed body.
 * Every other sentence passes through; a sentence that names a file, a table
 * or a constraint in its own prose reaches the screen intact, and the fix
 * for that is at the server.
 */
export function spokenRefusal(msg: string): string {
  let m = (msg || "").trim();
  for (let peeled = true; peeled; ) {
    const next = m.replace(
      /^(quoteassist|quote|order|settings|customer|pricing|inventory|billing|pos|tax|gl|dispatch|purchasing|portal|identity|payment|till|finstmt|reporting):\s*/i,
      "",
    );
    peeled = next !== m;
    m = next;
  }
  m = m.replace(/\s*\(\s*(?:[A-Z][A-Z0-9]*(?:_[A-Z0-9]+)+|Err[A-Z]\w*)\s*\)/g, "");
  m = m.replace(/\s*\b[A-Z][A-Z0-9]*(?:_[A-Z0-9]+)+\b\s*\.?$/, "");
  m = m.replace(/\s{2,}/g, " ").trim();
  return m ? m.charAt(0).toUpperCase() + m.slice(1) : m;
}

/**
 * sentByText: the success words, only for a text the core queued. They name the channel and the number on
 * file (masked by the core), and say what the status and the reminders now are.
 */
export function sentByText(phoneMasked: string): string {
  const where = phoneMasked === "" ? "the customer's number on file" : `the customer's number on file (${phoneMasked})`;
  return `Sent by text to ${where}. The quote is now Sent and its follow-ups have started.`;
}

/**
 * notSentWords: a send that reached no one leads with that. Nothing was marked Sent and no follow-up started.
 * why is a clause such as "texts are not allowed for this customer"; a refused text passes the server's
 * spoken sentence.
 */
export function notSentWords(why: string, draft: boolean): string {
  const clause = why.replace(/[.\s]+$/, "");
  const cap = clause.charAt(0).toUpperCase() + clause.slice(1);
  return `Not sent yet. ${cap}, and the desk cannot email a quote.${draft ? " The quote is still a draft and no follow-ups have started." : ""}`;
}

// ── display formatting ─────────────────────────────────────────────────────────

/** qtyLabel: the stored qty string with trailing zeros trimmed, never "1.0000". */
export function qtyLabel(raw: string | number): string {
  const n = Number(raw);
  if (!Number.isFinite(n)) return String(raw);
  return String(Math.round(n * 10000) / 10000);
}

// ── deposit ────────────────────────────────────────────────────────────────────

/**
 * The deposit line: null when the toggle is off or the percent is 0 (an
 * absent row, never a plausible zero). This is a PRINTED TERM of the order
 * the quote may become, composed for display from two stored figures; no
 * money moves on this surface and the terms write owns the stored percent.
 */
export function depositCents(required: boolean, pct: number, totalCents: number): number | null {
  if (!required || pct <= 0 || totalCents <= 0) return null;
  return Math.round((totalCents * pct) / 100);
}

// ── starting over ──────────────────────────────────────────────────────────────

/**
 * The fields a NEW QUOTE starts from, and only those: the workspace fields,
 * the surface config, the salespeople and the send-channel picks are the
 * OPERATOR's settings, not the quote's, and a new quote that also re-laid-out
 * their screen would be a worse defect than the one this table answers.
 */
export interface FreshQuoteState {
  customer: null;
  /** The customer picked for a quote that does not exist yet: it is made on the first line added. */
  composeCustomer: null;
  term: string;
  qtyDrafts: Record<number, string>;
  discDrafts: Record<number, string>;
  addonQty: Record<string, string>;
  notice: string;
  error: string;
  sendOpen: boolean;
  columnsOpen: boolean;
  helpOpen: boolean;
}

export function freshQuoteState(): FreshQuoteState {
  return {
    customer: null,
    composeCustomer: null,
    term: "",
    qtyDrafts: {},
    discDrafts: {},
    addonQty: {},
    notice: "",
    error: "",
    sendOpen: false,
    columnsOpen: false,
    helpOpen: false,
  };
}
