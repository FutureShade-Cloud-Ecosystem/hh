// Ported from hardscapeos_dibbits master 87d7941b app/src/_pos-searchdrop.ts; adapted for HH.

// _pos-searchdrop.ts: the counter search RESULT DROPDOWN's pure decisions.
//
// WHY A DROPDOWN AND NOT A GRID. The back-office lead, on the counter build: "the name of the game is
// getting people in and out really quickly." The grid-first lookup spent the screen on browsing:
// a mode the counter is almost never in. What a counter person actually does is type three
// characters of a name they already know and take the first answer. So the default result surface
// is five rows under the field, Enter commits the top one, and BROWSING IS THE EXCEPTION PATH
// behind its own disclosure.
//
// AND WHY A SCAN NEVER TOUCHES IT. A scanner burst resolves exactly (barcode, then exact SKU) and
// adds in one gesture: it never consults this list, never waits for it, and clears it on the way
// out. That is not an optimisation, it is the contract: a list that a scan could pick from is a
// list that could pick WRONG at 200 characters a second.
//
// DOM-free per the `_*.ts` house convention: every decision below is exercised on the Node runner.

import type { CounterHit } from './pos-api.js';

/**
 * How many rows the dropdown shows. The client said "about five", and five is also where the list
 * stops being a glance: a counter person reading a sixth row has already lost to typing one more
 * character. Anything beyond the cap is reported as a count, never as more rows.
 */
export const DROP_ROWS = 5;

/** The minimum term length the server answers at all: a shorter one is not a query yet. */
export const MIN_TERM = 2;

/** What the dropdown renders for a given answer. */
export interface DropView {
  /** The rows actually shown (never more than `cap`). */
  rows: CounterHit[];
  /** How many matches were left off the end. 0 renders nothing. */
  overflow: number;
  /** The hit Enter commits. Null when there is nothing to commit. */
  top: CounterHit | null;
}

/**
 * dropView: the whole visible answer, from the server's hit list.
 *
 * The server already ranks (EXACT → SKU_PREFIX → NAME_PREFIX → … → FUZZY), so the top hit is
 * simply the first: re-ranking here would mean the row a person is looking at and the row Enter
 * takes could disagree, which is the one thing a commit-on-Enter list may never do.
 */
export function dropView(hits: readonly CounterHit[] | null | undefined, cap = DROP_ROWS): DropView {
  const list = Array.isArray(hits) ? hits.filter((h) => h && typeof h.product_id === 'string') : [];
  const rows = list.slice(0, Math.max(0, cap));
  return { rows, overflow: Math.max(0, list.length - rows.length), top: rows[0] ?? null };
}

/**
 * shouldQuery: is this term worth a round trip?
 *
 * Trimmed, and at least MIN_TERM characters. The server clamps too; asking anyway would be a
 * request whose answer is known to be empty, and on a busy counter that is a request that delays
 * the one that matters.
 */
export function shouldQuery(term: string): boolean {
  return term.trim().length >= MIN_TERM;
}

/**
 * rowStep: arrow navigation inside the dropdown, as one Tab stop (the house roving rule).
 *
 * ArrowDown from the FIELD enters the list at row 0; ArrowUp from row 0 leaves it again (returning
 * -1), because a list you cannot walk back out of traps the caret. No wrap-around: a counter person
 * pressing ArrowDown four times expects to be at the bottom, not back at the top.
 */
export function rowStep(i: number, key: string, len: number): number | null {
  if (len <= 0) return null;
  switch (key) {
    case 'ArrowDown':
      return Math.min(len - 1, i + 1);
    case 'ArrowUp':
      return i - 1; // -1 means "back to the field": the caller reads it that way
    case 'Home':
      return 0;
    case 'End':
      return len - 1;
    default:
      return null;
  }
}

// ── the customer-aware price picture ────────────────────────────────────────────────────────────

/**
 * One product's price picture as the SERVER decided it (pricing.CounterQuote). Every cent here was
 * computed by the pricing waterfall: the same one the ring prices through. The counter formats
 * these numbers; it never derives one.
 */
export interface CounterQuote {
  product_id: string;
  price_uom: string;
  retail_cents: number;
  customer_cents: number;
  saving_cents?: number;
  saving_bps?: number;
  basis?: string;
  next_break_qty?: number;
  next_break_uom?: string;
  next_break_unit_cents?: number;
  next_break_save_cents?: number;
  unpriceable?: boolean;
}

/** What a price cell may show. Every flag is a fact about the SERVER's answer, never a computation. */
export interface PricePicture {
 /** Nothing can be said: render no price at all (never CA$0.00). */
  unpriceable: boolean;
  /** The customer's own price. */
  customerCents: number;
  /** Retail, shown STRUCK only when the customer is actually paying less than it. */
  retailCents: number;
  showRetail: boolean;
 /** The saving against retail: the number customers always ask for, per the counter lead's ask. */
  savingCents: number;
  savingBps: number;
  showSaving: boolean;
  /** Which rule produced the saving, in the server's word (CONTRACT / BULK / PROMO / …). */
  basis: string;
 /** A bulk break the customer has NOT reached: an offer to make, never a price already given. */
  showBreak: boolean;
  breakQty: number;
  breakUom: string;
  breakUnitCents: number;
  breakSaveCents: number;
}

/**
 * pricePicture: what a row (or a tile, or a ticket line) may claim about one product's price.
 *
 * THE SHAPE OF THE CLIENT'S ASK, EXACTLY: "should display retail + the account specific pricing
 * depending on the customer type: should also display savings over retail for special pricing and
 * bulk pricing". Three parts, and each one is suppressed the moment it would be noise or a lie:
 *
 * retail struck through only when the customer is genuinely paying less. A walk-in paying
 * retail must not see their own price crossed out beside itself.
 * saving only when the server sent one. There is no "you save CA$0.00" state, because a
 * zero saving is not a saving and a badge that says so trains people to ignore badges.
 * bulk only when a break exists that BEATS what they are getting now, and it is worded by
 * the caller as an opportunity: it is a quantity they have not bought yet.
 *
 * A missing quote is UNPRICEABLE, not zero. The counter renders no price rather than a number
 * somebody might sell at.
 */
export function pricePicture(q: CounterQuote | null | undefined): PricePicture {
  const empty: PricePicture = {
    unpriceable: true,
    customerCents: 0,
    retailCents: 0,
    showRetail: false,
    savingCents: 0,
    savingBps: 0,
    showSaving: false,
    basis: '',
    showBreak: false,
    breakQty: 0,
    breakUom: '',
    breakUnitCents: 0,
    breakSaveCents: 0,
  };
  if (!q || q.unpriceable) return empty;
  const customer = num(q.customer_cents);
  const retail = num(q.retail_cents);
  if (customer <= 0 && retail <= 0) return empty;
  const saving = num(q.saving_cents);
  const breakUnit = num(q.next_break_unit_cents);
  const breakSave = num(q.next_break_save_cents);
  const breakQty = num(q.next_break_qty);
  return {
    unpriceable: false,
    customerCents: customer,
    retailCents: retail,
    showRetail: retail > customer,
    savingCents: saving,
    savingBps: num(q.saving_bps),
    showSaving: saving > 0,
    basis: typeof q.basis === 'string' ? q.basis : '',
    showBreak: breakQty > 0 && breakUnit > 0 && breakSave > 0,
    breakQty,
    breakUom: typeof q.next_break_uom === 'string' ? q.next_break_uom : '',
    breakUnitCents: breakUnit,
    breakSaveCents: breakSave,
  };
}

function num(v: unknown): number {
  return typeof v === 'number' && Number.isFinite(v) ? v : 0;
}

/**
 * basisWord: the server's basis enum in counter language, for the saving chip.
 *
 * One or two words, per the chrome budget. An unknown basis falls back to the empty string rather
 * than printing a raw enum: a counter surface never wears the wire's vocabulary (the same rule
 * that retired COMMITTED from the sale pill).
 */
export function basisWord(basis: string): string {
  switch ((basis || '').toUpperCase()) {
    case 'CONTRACT':
      return 'Contract';
    case 'ACCOUNT':
      return 'Account';
    case 'BULK':
      return 'Bulk';
    case 'PROMO':
      return 'Promo';
    default:
      return '';
  }
}
