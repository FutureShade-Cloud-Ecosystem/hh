// The counter's manual discount (row 1047 item 3), the DOM-free half. At the counter a salesperson
// gives a discount only after a manager says yes in person; the till keeps that verbal and records
// the yes: every discount carries the manager who approved it. This module holds what the person
// typed, the words for it, and the wire shape. The cents a discount takes off are NEVER computed
// here: the sale preview answers them (money is computed by the core), this only reads what was
// typed through the decimal string, as the house invariant asks.

import { parseAmountToCents } from '../money.js';
import type { CartDiscount, CartLine } from '@hardscapehouse/pos-kit/src/ported/pos-api.js';

export type DiscountMode = 'percent' | 'amount';

export type DiscountEntry =
  | { ok: true; discount: Pick<CartDiscount, 'percentBps' | 'amountCents'> }
  | { ok: false; words: string };

const PERCENT_WORDS = 'A percent discount is more than 0 and no more than 100, up to two decimals (for example 10 or 12.5).';
const AMOUNT_WORDS = 'A dollar discount is more than $0.00, up to two decimals (for example 25 or 19.68).';

/**
 * Read what the person typed. A percent becomes basis points through the decimal string (never a
 * float multiply), so 12.5 is 1250 exactly; dollars become cents through the app's one money
 * parser. Refused in words, nothing sent: zero, a sign, more decimals than the cent allows, any
 * non digit. Whether the discount is more than the line or the ticket is the core's to say.
 */
export function parseDiscountEntry(mode: DiscountMode, text: string): DiscountEntry {
  const trimmed = text.trim();
  if (mode === 'amount') {
    const cents = parseAmountToCents(trimmed);
    if (cents === null || cents <= 0) return { ok: false, words: AMOUNT_WORDS };
    return { ok: true, discount: { amountCents: cents } };
  }
  const m = trimmed.match(/^([0-9]{1,3})(?:\.([0-9]{1,2}))?$/);
  if (m === null) return { ok: false, words: PERCENT_WORDS };
  const bps = Number(m[1]) * 100 + Number((m[2] ?? '').padEnd(2, '0'));
  if (bps <= 0 || bps > 10000) return { ok: false, words: PERCENT_WORDS };
  return { ok: true, discount: { percentBps: bps } };
}

/** "12.5" for 1250 basis points: the percent as typed, integer steps, no trailing zeros. */
export function percentText(bps: number): string {
  const whole = Math.floor(bps / 100);
  const frac = bps % 100;
  if (frac === 0) return String(whole);
  return `${whole}.${String(frac).padStart(2, '0').replace(/0$/, '')}`;
}

/**
 * The words for a discount on the ticket: what was entered, the server's cents where the preview
 * has answered, and who approved it ("10% off ($20.00), approved by Sales manager").
 */
export function discountWords(
  d: CartDiscount,
  offCents: number | undefined,
  money: (cents: number) => string,
): string {
  const entered = d.percentBps !== undefined ? `${percentText(d.percentBps)}% off` : `${money(d.amountCents ?? 0)} off`;
  const cents = d.percentBps !== undefined && offCents !== undefined ? ` (${money(offCents)})` : '';
  return `${entered}${cents}, approved by ${d.approvedByName}`;
}

/** The contract's DiscountInput for a discount: one of percent or amount, and approved_by. */
export function discountWire(d: CartDiscount | null | undefined):
  | { percent_bps: number; approved_by: string }
  | { amount_cents: number; approved_by: string }
  | undefined {
  if (!d) return undefined;
  if (d.percentBps !== undefined) return { percent_bps: d.percentBps, approved_by: d.approvedBy };
  return { amount_cents: d.amountCents ?? 0, approved_by: d.approvedBy };
}

/** A line's own discount, if it has one. */
export function discountFromLine(line: { discount?: CartDiscount }): CartDiscount | undefined {
  return line.discount;
}

/**
 * Whether the ticket carries any manual discount, on a line or on the ticket. A parked ticket
 * stores only product, quantity and unit, so a discount would not travel with it: the terminal
 * refuses to park one in words rather than lose a manager's yes silently.
 */
export function ticketCarriesDiscount(
  cart: readonly Pick<CartLine, 'discount'>[],
  ticket: CartDiscount | null,
): boolean {
  return ticket !== null || cart.some((l) => l.discount !== undefined);
}
