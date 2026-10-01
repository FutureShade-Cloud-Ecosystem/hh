// Ported from hardscapeos_dibbits master 87d7941b app/src/_pos-split.ts; adapted for HH.

import { centsAmount } from './_money-format.js';
// _pos-split.ts: pure, DOM-free split-tender words and shapes for the counter (DIB-165).
//
// The server refuses a split whose legs do not sum EXACTLY to the sale total, and migration 0100's
// deferred trigger refuses it again at COMMIT. Neither of those is a good place for a cashier to
// discover they typed $40.00 instead of $400.00: by then the customer has their card out. The
// reference did the same arithmetic locally, live, and simply did not enable the Ring button until
// the numbers agreed.
//
// HH note: whether a tender covers a total, and what is left of one, is the core's arithmetic in
// HH (the money invariant), so the split arithmetic now answers through the host seams in
// ports.ts; this file keeps the leg shapes, the method roster, the display formatter and the wire
// mapping, so the panel reads as it did and decides nothing about money itself.

/** One leg the cashier is composing. amountCents is what they typed, already parsed to cents. */
export interface DraftLeg {
  method: 'CASH' | 'CARD' | 'CHEQUE' | 'ACCOUNT';
  amountCents: number;
}

/** The live state of a split as the cashier types it. */
export interface SplitState {
 /** Σ legs. */
  tenderedCents: number;
 /** total − tendered. Positive = still owed, negative = over-tendered. */
  remainingCents: number;
 /** True only when the legs settle the sale exactly AND the shape is legal. */
  ready: boolean;
 /** Why it is not ready, in words a cashier can act on. '' when ready. */
  problem: string;
}

/** The methods a split leg may use. Mirrors the server's CHECK constraint. */
export const SPLIT_METHODS: DraftLeg['method'][] = ['CASH', 'CARD', 'CHEQUE', 'ACCOUNT'];

/** maxSplitLegs mirrors the server bound so the UI stops adding rows at the same place. */
export const MAX_SPLIT_LEGS = 6;

/**
 * evaluateSplit is the whole split rule set, in one pure function.
 *
 * hasCustomer matters because an ACCOUNT leg charges an account, and there is no account on a
 * walk-in sale: the server would refuse it, but "pick the customer first" is a better thing for the
 * cashier to read than a 4xx after they have already collected the cash leg.
 *
 * HH note: the reference summed and subtracted the leg amounts here, live, and enabled Ring only
 * when the legs settled the sale exactly. Whether a tender covers a total is the core's
 * consequence in HH (the money invariant), so the arithmetic is left out of the port and the seam
 * lives in ports.ts with the reference's signature (CLOSURE.md names it); the cashier still reads
 * the same state, from the host's answer.
 */
export { evaluateSplit } from './ports.js';

/**
 * autoFillRemainder is the ergonomics that makes a split usable at a counter: the cashier types the
 * cash they were handed and the next leg pre-fills with EXACTLY what is left, so the common case
 * needs no mental arithmetic and cannot be off by a cent. Returns 0 (not a negative) when the sale
 * is already covered: a leg is never pre-filled with a number that would over-tender.
 *
 * HH note: the remainder is the total less money already typed, which is the core's arithmetic in
 * HH; the seam lives in ports.ts (CLOSURE.md names it).
 */
export { autoFillRemainder } from './ports.js';

/**
 * parseAmountCents turns what the cashier typed into cents, or NaN when it is not a number.
 *
 * NaN rather than 0 on purpose: an empty box and a deliberate zero are different mistakes, and
 * collapsing them means the "every tender needs an amount" message never fires for a blank row.
 * Currency symbols, spaces and thousands separators are stripped because people paste from
 * invoices; anything else remaining is a refusal.
 *
 * HH note: the reference multiplied the typed decimal through a float; the house rule converts
 * through the decimal string, so the parse is a host seam (ports.ts, named in CLOSURE.md).
 */
export { parseAmountCents } from './ports.js';

/** fmtCents is the local money formatter (same LOCAL STOPGAP posture as pos-api.ts money()). */
export function fmtCents(cents: number): string {
  return `CA$${centsAmount(cents)}`;
}

/**
 * toPostBody maps the draft legs onto the wire shape the sale endpoint takes. A CARD leg carries its
 * own token because a split can legitimately hold two cards, so the token cannot be a sale-level
 * field.
 */
export function toPostBody(legs: DraftLeg[]): { method: string; amount_cents: number; payment_token?: string }[] {
  return legs.map((l) => ({
    method: l.method,
    amount_cents: l.amountCents,
    payment_token: l.method === 'CARD' ? 'tok_simulated_runner' : undefined,
  }));
}
