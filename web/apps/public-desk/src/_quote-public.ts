// Pure totals and acceptance logic for the customer's quote page, ported from
// the older product's module of the same role.
//
// House convention: the page component stays a render function; the DECISION
// it leans on lives here and is unit-tested (test/unit.test.mjs).
//
// THE QUESTION THIS CLOSES. A quote either carries stored tax or it does not,
// and the page's framing is a claim about the DATA, so it is made from the
// data. When the quote carries tax we show classic Subtotal / HST / Total and
// say nothing about future invoicing: the tax is already in the total, and
// printing "HST is applied when this quote is invoiced" under it invites the
// customer to add it a second time. When the quote carries no tax, the
// before-HST framing is kept, the figures are labelled as pre-tax, and the
// note says when HST would arrive. A defensive edge holds both sides: a
// missing or unreadable tax figure from an older server is treated as
// untaxed (which is what such a server actually served), and a negative
// value, which no quote should ever carry, is likewise not allowed to claim
// "tax included".
//
// One module drives every place the claim is made: the totals block, the
// acceptance checkbox label, and the confirmation echo. The failure mode the
// older product repaired here was precisely that the same sentence had been
// written out three times and each copy had drifted.
//
// Field names follow the client's validated refs (quote-client.ts), so the
// page hands a validated quote or acceptance straight over.

import { formatMoney } from "./format.js";

/**
 * The money a quote (or an acceptance echo) carries. Both validated refs expose these fields. taxLabel is
 * the name of the dealer's tax ("HST", "Sales tax"), the server's own word: the page renders it and never
 * guesses it from a rate or a country.
 */
export interface QuoteMoney {
  subtotalCents: number;
  taxCents: number;
  totalCents: number;
  taxLabel: string;
}

/**
 * midSentence is the tax word as it reads inside a sentence: an acronym ("HST", "GST") stays as it is,
 * a phrase ("Sales tax") drops its capital ("before sales tax").
 */
export function midSentence(label: string): string {
  return label === label.toUpperCase() ? label : label.charAt(0).toLowerCase() + label.slice(1);
}

/** One rendered row of the totals block. */
export interface TotalsRow {
  /** Row text, already money formatted, e.g. "HST $12.57" or "Sales tax $7.75". */
  label: string;
  /** True for the emphasised bottom line (the grand row). Exactly one row is grand. */
  grand: boolean;
}

export interface TotalsBlock {
  rows: TotalsRow[];
  /**
   * The "HST is applied when this quote is invoiced." footnote (the dealer's own
   * tax word), or null when it must be SUPPRESSED: printing it under a total
   * that already contains the tax is the defect this module exists to prevent.
   */
  note: string | null;
  /** True when this quote's figures are pre-tax. Drives the before-tax wording everywhere. */
  preTax: boolean;
}

/** invoiceTaxNote is the pre-tax footnote, naming the dealer's tax. */
export function invoiceTaxNote(label: string): string {
  return `${label} is applied when this quote is invoiced.`;
}

/**
 * carriesTax is the single predicate the whole page branches on: a finite,
 * positive tax figure means the total already includes the tax.
 */
export function carriesTax(m: Partial<QuoteMoney> | null | undefined): boolean {
  const tax = m?.taxCents;
  return typeof tax === "number" && Number.isFinite(tax) && tax > 0;
}

/**
 * totalsBlock builds the rows for the totals area of the quote and of the
 * acceptance confirmation, which must agree with each other by construction.
 */
export function totalsBlock(m: QuoteMoney): TotalsBlock {
  if (carriesTax(m)) {
    return {
      preTax: false,
      rows: [
        { label: `Subtotal ${formatMoney(m.subtotalCents)}`, grand: false },
        { label: `${m.taxLabel} ${formatMoney(m.taxCents)}`, grand: false },
        { label: `Total ${formatMoney(m.totalCents)}`, grand: true },
      ],
      note: null,
    };
  }
  return {
    preTax: true,
    rows: [
      { label: `Subtotal (before ${midSentence(m.taxLabel)}) ${formatMoney(m.subtotalCents)}`, grand: false },
      { label: `Amount before ${midSentence(m.taxLabel)} ${formatMoney(m.totalCents)}`, grand: true },
    ],
    note: invoiceTaxNote(m.taxLabel),
  };
}

/**
 * acceptanceAmountPhrase is the amount clause of the acceptance checkbox and
 * of any sentence that restates what the customer is agreeing to pay. Taxed
 * quotes name the tax inside the total rather than just dropping the tail:
 * the customer is being asked to tick a box, and the box should say what the
 * number contains.
 */
export function acceptanceAmountPhrase(m: QuoteMoney): string {
  if (carriesTax(m)) {
    return `${formatMoney(m.totalCents)}, including ${formatMoney(m.taxCents)} ${m.taxLabel},`;
  }
  return `${formatMoney(m.totalCents)} before ${midSentence(m.taxLabel)}`;
}

/**
 * acceptanceRefusal is the gate the accept runs before anything is sent, in
 * the order the form reads: the quantities box above the name, a blank (or
 * white space only) name, then the attestation. An honest form passes as
 * null. The refusal words are the page's own inline copy; the server holds
 * the real gate.
 */
export function acceptanceRefusal(typedName: string, affirmed: boolean, quantitiesConfirmed: boolean): string | null {
  if (!quantitiesConfirmed) return "Please tick the box to confirm you have reviewed the quantities on this quote.";
  if (!typedName.trim()) return "Please type your full name.";
  if (!affirmed) return "Please tick the box to confirm you accept this quote.";
  return null;
}

/** acceptReady: the Accept button is enabled only when the form passes every part of the gate. */
export function acceptReady(typedName: string, affirmed: boolean, quantitiesConfirmed: boolean): boolean {
  return acceptanceRefusal(typedName, affirmed, quantitiesConfirmed) === null;
}

/**
 * acceptHint is what the disabled Accept button says is still missing, in the order the form reads, or
 * null when nothing is. A greyed button with no reason is a dead end for a keyboard or screen reader
 * customer, so the page shows this beside the button and ties it to the button as its description.
 */
export function acceptHint(typedName: string, affirmed: boolean, quantitiesConfirmed: boolean): string | null {
  const missing: string[] = [];
  if (!quantitiesConfirmed) missing.push("tick the quantities box");
  if (!typedName.trim()) missing.push("type your full name");
  if (!affirmed) missing.push("tick the confirmation");
  if (missing.length === 0) return null;
  const last = missing.pop() as string;
  return `To accept, ${missing.length > 0 ? `${missing.join(", ")} and ${last}` : last}.`;
}

/**
 * showsImages: thumbnails render only when the dealer chose to include
 * product images on this quote AND at least one line carries a reference.
 * Both halves are facts from the server; neither is inferred. The structural
 * arguments are the validated quote's own fields.
 */
export function showsImages(q: { includeImages?: boolean | null; lines: Array<{ imageRef?: string | null }> }): boolean {
  return Boolean(q.includeImages) && q.lines.some((l) => Boolean(l.imageRef));
}

/**
 * Status badge label: the customer-facing words on the status chip beside the
 * quote number. SENT reads as "Awaiting acceptance", because a customer only
 * sees this page while their quote is awaiting their own action; the same
 * state in a staff surface would name the moment differently. CONVERTED reads
 * as "Order created" because the document is the order now. EXPIRED reads
 * the calendar fact. LOST reads as "Withdrawn" because that is what a dealer
 * told the customer when they emailed back to cancel. An unmapped state
 * degrades honestly, mirroring quoteStatusPhrase's mid-sentence form: a new
 * state reads in its own words rather than silently reading as "Unknown".
 */
export function statusBadgeLabel(status: string): string {
  const labels: Record<string, string> = {
    DRAFT: "Draft",
    SENT: "Awaiting acceptance",
    ACCEPTED: "Accepted",
    CONVERTED: "Order created",
    EXPIRED: "Expired",
    LOST: "Withdrawn",
  };
  return labels[status] ?? status.charAt(0).toUpperCase() + status.slice(1).toLowerCase().replace(/_/g, " ");
}

/**
 * The kind word the status chip uses to pick its colour: "info" for an open
 * quote, "success" for an acceptance, "accent" for the order band, "danger"
 * for an expired or withdrawn quote. The badge paints in a fixed token the
 * pair was measured for, so a state with no token drops to "info" rather than
 * guessing; the chips.test.mjs and tokens.test.mjs pair matrices catch a chip
 * pair that slips below its bar.
 */
export type StatusBadgeKind = "info" | "success" | "accent" | "danger";

export function statusBadgeKind(status: string): StatusBadgeKind {
  if (status === "ACCEPTED") return "success";
  if (status === "CONVERTED") return "accent";
  if (status === "EXPIRED" || status === "LOST") return "danger";
  return "info";
}

/**
 * Recompute a quote's money when the customer excludes one or more optional
 * add-on lines. Tax is recomputed proportionally (the dealer's tax rate
 * applied to the remaining subtotal); the page never rounds tax to zero on
 * an exclusion (a $0.00 tax line on a non-zero subtotal would be a confident
 * wrong number, which the house rule forbids). The total is then the sum.
 *
 * The function is pure: it returns a fresh QuoteMoney with the dealer's tax
 * label carried across, and never reads or writes external state. Excluding
 * zero lines is an identity: the same numbers come back. An optional line
 * that is NOT in the excluded set is still included, so the customer's view
 * matches the quote's own words.
 */
export function quoteMoneyExcludingOptional(
  m: QuoteMoney,
  excludedOptionalLineCents: ReadonlyArray<number>,
): QuoteMoney {
  const excludedTotal = excludedOptionalLineCents.reduce((sum, cents) => sum + (Number.isFinite(cents) ? cents : 0), 0);
  if (excludedTotal <= 0 || m.subtotalCents <= 0) return m;
  const remainingSubtotal = Math.max(0, m.subtotalCents - excludedTotal);
  if (remainingSubtotal === m.subtotalCents) return m;
  const taxRate = m.taxCents / m.subtotalCents;
  const remainingTax = Math.round(remainingSubtotal * taxRate);
  const remainingTotal = remainingSubtotal + remainingTax;
  return {
    subtotalCents: remainingSubtotal,
    taxCents: remainingTax,
    totalCents: remainingTotal,
    taxLabel: m.taxLabel,
  };
}

/**
 * The optional lines a quote carries, by line number, in the server's own
 * order. The page reads this to know which lines deserve a checkbox column;
 * the customer's toggle state is then "which of these are EXCLUDED", and the
 * exclusion list is what the totals see. A quote with no optional lines
 * comes back as an empty array; the page then renders nothing extra.
 */
export function optionalLineNos(q: { lines: ReadonlyArray<{ lineNo: number; optional?: boolean | null }> }): number[] {
  return q.lines.filter((l) => l.optional === true).map((l) => l.lineNo);
}

/**
 * The cents a card surcharge adds, given the card surcharge percentage and
 * the customer's selected payment base. A null percentage means the dealer
 * does not accept cards with a surcharge (the page then offers ACH only); a
 * percentage above zero adds a surcharge, rounded to the nearest cent so the
 * page never shows a confident wrong number.
 */
export function cardSurchargeCents(totalCents: number, surchargePct: number | null): number {
  if (surchargePct === null || !Number.isFinite(surchargePct) || surchargePct <= 0) return 0;
  if (!Number.isFinite(totalCents) || totalCents <= 0) return 0;
  return Math.round((totalCents * surchargePct) / 100);
}

/**
 * True when a rep card should render. A rep card needs at least a NAME to
 * render at all; an email alone (no name, no phone) does not earn a card,
 * because the customer has no person to address.
 */
export function repHasContact(rep: { repName?: string | null; repEmail?: string | null; repPhone?: string | null }): boolean {
  return typeof rep.repName === "string" && rep.repName.trim() !== "";
}

/**
 * The mailto: URL the "Ask a question" link opens. The customer's default
 * mail client receives the message; the page never sends mail on a customer's
 * behalf. The subject names the quote so the rep's inbox can route it, and
 * the body is empty so the customer writes their own words. The email
 * address, when absent, drops to a bare "?" so a misconfigured dealer does
 * not silently route the customer nowhere.
 */
export function repMailtoHref(quoteNo: string, repEmail: string | null): string {
  const subject = encodeURIComponent(`Question about quote ${quoteNo}`);
  const to = typeof repEmail === "string" && repEmail.trim() !== "" ? repEmail.trim() : "";
  return `mailto:${to}?subject=${subject}`;
}

/**
 * Whether a quote offers a card payment option. A dealer without a
 * configured card surcharge shows ACH only; the card radio is then absent,
 * not selected, so a customer never lands on a payment the dealer cannot
 * accept. A null surcharge percentage is the honest "no card option".
 */
export function offersCardPayment(q: { cardSurchargePct?: number | null }): boolean {
  const pct = q.cardSurchargePct;
  return typeof pct === "number" && Number.isFinite(pct) && pct > 0;
}
