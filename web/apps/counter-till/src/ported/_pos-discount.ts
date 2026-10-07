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
/** The ticket's rule: the most a ticket discount may take off (the server's figure) and the money words. */
export interface TicketRule {
  maxCents: number;
  money: (cents: number) => string;
}

/** The rule a ticket discount lives under, said in words (the dialog shows it before anything is typed). */
export function ticketDiscountRule(rule: TicketRule): string {
  return `A ticket discount must leave something to pay: the most you can take off is ${rule.money(rule.maxCents)} (the ticket total less one cent).`;
}

export function parseDiscountEntry(mode: DiscountMode, text: string, ticket?: TicketRule): DiscountEntry {
  const trimmed = text.trim();
  if (mode === 'amount') {
    const cents = parseAmountToCents(trimmed);
    if (cents === null || cents <= 0) return { ok: false, words: AMOUNT_WORDS };
    if (ticket && cents > ticket.maxCents) return { ok: false, words: ticketDiscountRule(ticket) };
    return { ok: true, discount: { amountCents: cents } };
  }
  const m = trimmed.match(/^([0-9]{1,3})(?:\.([0-9]{1,2}))?$/);
  if (m === null) return { ok: false, words: PERCENT_WORDS };
  const bps = Number(m[1]) * 100 + Number((m[2] ?? '').padEnd(2, '0'));
  if (bps <= 0 || bps > 10000) return { ok: false, words: PERCENT_WORDS };
  if (ticket && bps >= 10000) return { ok: false, words: ticketDiscountRule(ticket) };
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

/**
 * The contract's DiscountInput for a discount: one of percent or amount, approved_by, and what the
 * yes was given for (approved_qty for a line, approved_base_cents for both), which the ring checks.
 */
export function discountWire(d: CartDiscount | null | undefined):
  | { percent_bps: number; approved_by: string; approved_qty?: number; approved_base_cents?: number }
  | { amount_cents: number; approved_by: string; approved_qty?: number; approved_base_cents?: number }
  | undefined {
  if (!d) return undefined;
  const approval = {
    ...(d.approvedQty !== undefined ? { approved_qty: d.approvedQty } : {}),
    ...(d.approvedBaseCents !== undefined ? { approved_base_cents: d.approvedBaseCents } : {}),
  };
  if (d.percentBps !== undefined) return { percent_bps: d.percentBps, approved_by: d.approvedBy, ...approval };
  return { amount_cents: d.amountCents ?? 0, approved_by: d.approvedBy, ...approval };
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

type StandLine = Pick<CartLine, 'qty' | 'lineTotalCents'> & {
  product: { id: string };
  uom?: string;
  discount?: CartDiscount;
  priceMissing?: boolean;
};

/**
 * The ticket's signature: every line's product, quantity, unit, price and discount. A ticket
 * discount is approved on one ticket; when the signature moves, the yes no longer stands.
 */
export function ticketBasis(cart: readonly StandLine[]): string {
  return cart
    .map((l) =>
      [l.product.id, l.qty, l.uom ?? '', l.lineTotalCents, l.discount ? `${l.discount.percentBps ?? ''}/${l.discount.amountCents ?? ''}/${l.discount.approvedBy}` : ''].join('|'),
    )
    .join(';');
}

export interface Standing<T extends StandLine> {
  cart: T[];
  ticket: CartDiscount | null;
  lineRemoved: boolean;
  ticketRemoved: boolean;
  /** Why a line's discount went: its quantity moved, or its price did. */
  reason: 'quantity' | 'price' | null;
}

/**
 * Which discounts still stand. A line discount stands at the quantity and the line total it was
 * approved at (a line still waiting on its price keeps its yes); a ticket discount stands while
 * the ticket's signature is the one it was approved on. A manager's yes is for the line and
 * quantity it was given: it never grows with the quantity or the ticket (the ring refuses a stale
 * one too, this is the till saying so first, in words).
 */
export function discountsThatStand<T extends StandLine>(cart: readonly T[], ticket: CartDiscount | null): Standing<T> {
  let reason: 'quantity' | 'price' | null = null;
  const next = cart.map((l) => {
    const d = l.discount;
    if (!d) return l;
    if (d.approvedQty !== undefined && d.approvedQty !== l.qty) {
      reason = 'quantity';
      return { ...l, discount: undefined };
    }
    if (!l.priceMissing && d.approvedBaseCents !== undefined && d.approvedBaseCents !== l.lineTotalCents) {
      reason ??= 'price';
      return { ...l, discount: undefined };
    }
    return l;
  });
  const lineRemoved = reason !== null;
  let keep: CartDiscount | null = ticket;
  let ticketRemoved = false;
  if (ticket !== null && ticket.basis !== undefined && !cart.some((l) => l.priceMissing) && ticket.basis !== ticketBasis(next)) {
    keep = null;
    ticketRemoved = true;
  }
  return { cart: lineRemoved ? (next as T[]) : (cart as T[]), ticket: keep, lineRemoved, ticketRemoved, reason };
}

/** The words for a discount that stopped standing, and what to do about it. */
export function discountRemovedWords(reason: 'quantity' | 'price' | null, ticketRemoved: boolean): string {
  const parts: string[] = [];
  if (reason !== null) parts.push(`The discount was removed because the ${reason} changed.`);
  if (ticketRemoved) parts.push('The ticket discount was removed because the ticket changed.');
  parts.push('Ask a manager again.');
  return parts.join(' ');
}
