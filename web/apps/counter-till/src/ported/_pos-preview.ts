// The till's pre-ring totals (decision log row 985): the ticket's own subtotal, tax and total as
// the ring will compute them, read from POST /api/v1/pos/sale-preview and never assembled in the
// browser. Pure helpers only (the terminal owns the fetch), unit-tested in _pos-preview.test.mjs and
// read through the browser legs in test/total-split.pw.ts.

import { discountWire } from './_pos-discount.js';
import type { CartDiscount } from '@hardscapehouse/pos-kit/src/ported/pos-api.js';

export interface PreviewLine {
  product_id: string;
  qty_entered: number;
  uom_entered: string;
  /** The line's manual discount and its approver (row 1047 item 3), absent when there is none. */
  discount?: ReturnType<typeof discountWire>;
}

export interface PreviewBody {
  branch_id: string;
  customer_id: string;
  sale_type: string;
  fulfilment?: string;
  lines: PreviewLine[];
  /** The whole ticket's manual discount and its approver, absent when there is none. */
  discount?: ReturnType<typeof discountWire>;
}

/** One payment shape the server says the counter may offer, with line two's words on the deposit shapes. */
export interface PaymentOption {
  shape: string;
  lineTwoWords?: string;
}

/** What the till knows about the ticket's totals: nothing asked, asked and waiting, answered, or unreachable. */
export type PreviewState =
  | { kind: 'idle' }
  | { kind: 'pending' }
  | {
      kind: 'ready';
      subtotalCents: number;
      taxCents: number;
      totalCents: number;
      /** Every manual discount together, and the ticket discount's own share: the server's cents. */
      discountCents: number;
      saleDiscountCents: number;
      /** The subtotal after the line discounts (what a ticket discount is approved against) and the most a ticket discount may take off: the server's figures, never worked out here. */
      ticketBaseCents: number;
      ticketDiscountMaxCents: number;
      /** Per line, only when a discount applies: the net the line charges and what came off. */
      lines: PreviewDiscountLine[];
      /** The shapes the account may be offered (item 9.1), the server's list: empty when it sent none. */
      paymentOptions: PaymentOption[];
    }
  /** The ring refused the ticket in words (a discount past what it comes off, an approver it will not take). */
  | { kind: 'refused'; words: string }
  | { kind: 'unavailable' };

export interface PreviewDiscountLine {
  lineNo: number;
  lineTotalCents: number;
  /** The line's price less its own discount: the figure the receipt prints and the line shows. */
  beforeTicketDiscountCents: number;
  discountCents: number;
  saleDiscountCents: number;
}

/** The preview's request for the ticket, shaped like the ring's own body minus every tender. */
export function previewBody(facts: {
  branchId: string;
  customerId: string;
  saleType: string;
  fulfilment: string;
  lines: readonly { productId: string; qty: number; uom: string; discount?: CartDiscount }[];
  discount?: CartDiscount | null;
}): PreviewBody | null {
  if (facts.lines.length === 0) return null;
  return {
    branch_id: facts.branchId,
    customer_id: facts.customerId,
    sale_type: facts.saleType,
    // The ring sends the fulfilment only on a COD sale; the preview follows it.
    fulfilment: facts.saleType === 'COD' ? facts.fulfilment : undefined,
    lines: facts.lines.map((l) => ({
      product_id: l.productId,
      qty_entered: l.qty,
      uom_entered: l.uom,
      ...(l.discount ? { discount: discountWire(l.discount) } : {}),
    })),
    ...(facts.discount ? { discount: discountWire(facts.discount) } : {}),
  };
}

/** The fingerprint of a request: a change in it is the reason to ask again. '' means nothing to ask. */
export function previewKey(body: PreviewBody | null): string {
  return body === null ? '' : JSON.stringify(body);
}

/**
 * Read an answer; anything that is not three whole-cent figures is not an answer. A 422 carrying
 * the ring's words (status given) is a refusal the total row can say in those words; any other
 * non answer is just unavailable.
 */
export function readPreview(answer: unknown, status = 200): PreviewState {
  if (answer === null || typeof answer !== 'object') return { kind: 'unavailable' };
  const a = answer as Record<string, unknown>;
  if (status === 422) {
    return typeof a.error === 'string' && a.error !== '' ? { kind: 'refused', words: a.error } : { kind: 'unavailable' };
  }
  if (status !== 200) return { kind: 'unavailable' };
  const { subtotal_cents: s, tax_cents: t, total_cents: n } = a;
  if (![s, t, n].every((v) => typeof v === 'number' && Number.isInteger(v))) return { kind: 'unavailable' };
  const whole = (v: unknown): number | null => (v === undefined ? 0 : typeof v === 'number' && Number.isInteger(v) ? v : null);
  const discount = whole(a.discount_cents);
  const saleDiscount = whole(a.sale_discount_cents);
  const base = whole(a.ticket_base_cents);
  const max = whole(a.ticket_discount_max_cents);
  if (discount === null || saleDiscount === null || base === null || max === null) return { kind: 'unavailable' };
  const lines: PreviewDiscountLine[] = [];
  if (Array.isArray(a.lines)) {
    for (const raw of a.lines) {
      const l = (raw ?? {}) as Record<string, unknown>;
      const own = whole(l.discount_cents);
      const share = whole(l.sale_discount_cents);
      if (
        !Number.isInteger(l.line_no) ||
        !Number.isInteger(l.line_total_cents) ||
        !Number.isInteger(l.before_ticket_discount_cents) ||
        own === null ||
        share === null
      ) {
        return { kind: 'unavailable' };
      }
      lines.push({
        lineNo: l.line_no as number,
        lineTotalCents: l.line_total_cents as number,
        beforeTicketDiscountCents: l.before_ticket_discount_cents as number,
        discountCents: own,
        saleDiscountCents: share,
      });
    }
  }
  return {
    kind: 'ready',
    subtotalCents: s as number,
    taxCents: t as number,
    totalCents: n as number,
    discountCents: discount,
    saleDiscountCents: saleDiscount,
    ticketBaseCents: base,
    ticketDiscountMaxCents: max,
    lines,
    paymentOptions: readPaymentOptions(a.payment_options),
  };
}

/** The server's payment_options: entries that are not a shape word are dropped, never guessed. */
export function readPaymentOptions(raw: unknown): PaymentOption[] {
  if (!Array.isArray(raw)) return [];
  const out: PaymentOption[] = [];
  for (const entry of raw) {
    const o = (entry ?? {}) as Record<string, unknown>;
    if (typeof o.shape !== 'string' || o.shape === '') continue;
    out.push(typeof o.line_two_words === 'string' && o.line_two_words !== '' ? { shape: o.shape, lineTwoWords: o.line_two_words } : { shape: o.shape });
  }
  return out;
}

/** The words for the split refusal, from its fields (the server's `split_total_mismatch`). */
export function splitMismatchWords(
  err: { tendered_cents?: number; total_cents?: number; message?: string },
  money: (cents: number) => string,
): string {
  const { tendered_cents: tendered, total_cents: total } = err;
  if (Number.isInteger(tendered) && Number.isInteger(total)) {
    return `The tenders add up to ${money(tendered as number)}, but the sale total is ${money(total as number)}. Change the legs so they match.`;
  }
  return err.message && err.message !== '' ? err.message : 'The tenders do not add up to the sale total. Change the legs so they match.';
}
