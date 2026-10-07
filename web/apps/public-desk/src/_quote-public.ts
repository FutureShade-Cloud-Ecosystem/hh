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

/** The money a quote (or an acceptance echo) carries. Both validated refs expose these three fields. */
export interface QuoteMoney {
  subtotalCents: number;
  taxCents: number;
  totalCents: number;
}

/** One rendered row of the totals block. */
export interface TotalsRow {
  /** Row text, already money formatted, e.g. "HST $12.57". */
  label: string;
  /** True for the emphasised bottom line (the grand row). Exactly one row is grand. */
  grand: boolean;
}

export interface TotalsBlock {
  rows: TotalsRow[];
  /**
   * The "HST is applied when this quote is invoiced." footnote, or null when
   * it must be SUPPRESSED: printing it under a total that already contains
   * HST is the defect this module exists to prevent.
   */
  note: string | null;
  /** True when this quote's figures are pre-tax. Drives the before-HST wording everywhere. */
  preTax: boolean;
}

export const INVOICE_TAX_NOTE = "HST is applied when this quote is invoiced.";

/**
 * carriesTax is the single predicate the whole page branches on: a finite,
 * positive tax figure means the total already includes HST.
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
        { label: `HST ${formatMoney(m.taxCents)}`, grand: false },
        { label: `Total ${formatMoney(m.totalCents)}`, grand: true },
      ],
      note: null,
    };
  }
  return {
    preTax: true,
    rows: [
      { label: `Subtotal (before HST) ${formatMoney(m.subtotalCents)}`, grand: false },
      { label: `Amount before HST ${formatMoney(m.totalCents)}`, grand: true },
    ],
    note: INVOICE_TAX_NOTE,
  };
}

/**
 * acceptanceAmountPhrase is the amount clause of the acceptance checkbox and
 * of any sentence that restates what the customer is agreeing to pay. Taxed
 * quotes name the HST inside the total rather than just dropping the tail:
 * the customer is being asked to tick a box, and the box should say what the
 * number contains.
 */
export function acceptanceAmountPhrase(m: QuoteMoney): string {
  if (carriesTax(m)) {
    return `${formatMoney(m.totalCents)}, including ${formatMoney(m.taxCents)} HST,`;
  }
  return `${formatMoney(m.totalCents)} before HST`;
}

/**
 * acceptanceRefusal is the gate the accept button runs before anything is
 * sent: a blank (or white space only) name is refused, an unticked
 * attestation is refused, and an honest form passes as null. The refusal
 * words are the page's own inline copy; the server holds the real gate.
 */
export function acceptanceRefusal(typedName: string, affirmed: boolean): string | null {
  if (!typedName.trim()) return "Please type your full name.";
  if (!affirmed) return "Please tick the box to confirm you accept this quote.";
  return null;
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
