// Item 9.6 (row 1047 item 6): the deposit at the payment step, as pure helpers. The server names
// the shapes the counter may offer and the words for line two (the preview's payment_options);
// this module only reads them, words the screen around them and shapes the ring's facts. The till
// decides no money rule: whether the account fits a shape is the server's answer, and a refusal
// is the server's message (or, failing that, words made from its fields), never a code or cents.

import type { PaymentOption } from './_pos-preview.js';

export const SHAPE_DEPOSIT_ON_ACCOUNT = 'DEPOSIT_NOW_REST_ON_ACCOUNT';
export const SHAPE_DEPOSIT_DUE_LATER = 'DEPOSIT_NOW_REST_DUE_LATER';
export const SHAPE_PAY_BEFORE_RELEASE = 'PAY_BEFORE_RELEASE';

/** Whichever deposit shape the server offered for this account, or null when it offered none. */
export function depositOption(options: readonly PaymentOption[]): PaymentOption | null {
  return options.find((o) => o.shape === SHAPE_DEPOSIT_ON_ACCOUNT || o.shape === SHAPE_DEPOSIT_DUE_LATER) ?? null;
}

/** A deposit rides only a fulfilment that waits (will call, delivery, a pay before release sale), where the shape fits. */
export function depositOffered(saleType: string, options: readonly PaymentOption[]): boolean {
  const option = depositOption(options);
  if (option === null || saleType === 'COLLECT_NOW') return false;
  // A COD sale is pay before release by definition: its line two is never "on account".
  return !(saleType === 'COD' && option.shape === SHAPE_DEPOSIT_ON_ACCOUNT);
}

/**
 * The row 1214 ruling: whether the server offered the no deposit pay before release shape, the
 * former COD fulfilment tile's ring. The server alone decides (the Cash On Demand account's own
 * option, dropped when the branch's switch is off); the till reads the answer and computes nothing.
 */
export function payBeforeReleaseOffered(options: readonly PaymentOption[]): boolean {
  return options.some((o) => o.shape === SHAPE_PAY_BEFORE_RELEASE);
}

/**
 * The ring's facts for the pay before release shape: the COD sale type on the fulfilment of the
 * chosen tile (any fulfilment; the goods never leave at ring), no deposit. The tender is not the
 * shape's to choose: it stays the settlement method the counter picked, and nothing captures at
 * ring.
 */
export function payBeforeReleaseRingFacts(
  saleType: string,
): { saleType: 'COD'; fulfilment: 'PICKUP' | 'WILL_CALL' | 'DELIVERY' } {
  const fulfilment = saleType === 'DELIVERED' ? 'DELIVERY' : saleType === 'COLLECT_LATER' ? 'WILL_CALL' : 'PICKUP';
  return { saleType: 'COD', fulfilment };
}

/** The sentence under the two lines: the server's words for why this account has this shape, or none. */
export function depositSentence(option: PaymentOption | null): string {
  return option?.lineTwoSentence ?? '';
}

/**
 * Round 16 (FROM-PRINCIPAL 37 ii): the account's posture line for the pay step's customer card,
 * in place of the credit figure the card used to draw for any account with a line. The demo
 * world's Cash On Demand account (zero day terms with a credit line, the facts the core's own
 * sentence choice keys on) has no credit to be available: its sales pay before release, so the
 * figure was a confident number with no meaning. The posture is read against the server's
 * payment options (one source of truth): an on account shape offered means the account transacts
 * as credit and keeps its figure, held or not (the hold tag carries the hold); a zero day account
 * with a line is the Cash On Demand account whatever the branch's deposit switch answers, and
 * before the preview answers too (the posture is the account's, not the sale's). No line is not
 * this posture: the card's own "No credit line" line stands. The till decides no money rule; the
 * words name what the server's sentence names.
 */
export function cashOnDemandPosture(
  options: readonly PaymentOption[],
  termsDays: number,
  creditLimitCents: number,
): string | null {
  if (termsDays > 0 || creditLimitCents <= 0) return null;
  if (options.some((o) => o.shape === 'ALL_ON_ACCOUNT')) return null;
  return 'Cash On Demand: pay before release';
}

/** The one commit button's words: the deposit in dollars, or what is missing. */
export function depositButtonWords(depositCents: number, money: (cents: number) => string): string {
  return depositCents > 0 ? `Ring sale with ${money(depositCents)} deposit` : 'Enter the deposit';
}

/**
 * The ring's facts for a deposit shape: the credit shape rides the ACCOUNT tender on the sale type
 * the counter chose; the pay before release shape rides the COD sale type with the fulfilment the
 * chosen tile means (will call, delivery), the deposit method standing as the tender.
 */
export function depositRingFacts(
  shape: string,
  saleType: string,
  fulfilment: string,
): { tender: 'ACCOUNT' | 'CASH'; saleType: string; fulfilment: string | undefined } {
  if (shape === SHAPE_DEPOSIT_ON_ACCOUNT) return { tender: 'ACCOUNT', saleType, fulfilment: undefined };
  const waiting = saleType === 'DELIVERED' ? 'DELIVERY' : saleType === 'COLLECT_LATER' ? 'WILL_CALL' : fulfilment;
  return { tender: 'CASH', saleType: 'COD', fulfilment: waiting };
}

/**
 * The words for the deposit refusals (422 problem bodies with integer cents fields and a message in
 * dollars), or null when the error is not one of them. The server's message wins; the fields word
 * it only when the message is missing.
 */
export function depositRefusalWords(
  err: {
    status: number;
    error: string;
    message?: string;
    deposit_cents?: number;
    total_cents?: number;
    amount_cents?: number;
  },
  money: (cents: number) => string,
): string | null {
  if (err.error === 'cod_not_for_terms_account') {
    // The ruling's refusal carries no figures: its message is the whole answer.
    return err.message && err.message !== ''
      ? err.message
      : 'This account pays on terms, so its sale is not pay before release. Ring it on account or paid in full.';
  }
  if (err.error === 'deposit_out_of_range') {
    // The money bound (0 < deposit < the sale total): the message names the total in dollars.
    if (err.message && err.message !== '') return err.message;
    return Number.isInteger(err.total_cents)
      ? `The deposit must be more than ${money(0)} and less than the sale total of ${money(err.total_cents as number)}.`
      : 'The deposit must be more than zero and less than the sale total.';
  }
  if (err.error !== 'deposit_not_collect_now' && err.error !== 'on_account_not_available') return null;
  if (err.message && err.message !== '') return err.message;
  if (err.error === 'deposit_not_collect_now') {
    return Number.isInteger(err.total_cents)
      ? `A collect now sale is paid in full: take the whole ${money(err.total_cents as number)} now, or choose will call or delivery to leave a deposit.`
      : 'A collect now sale is paid in full: take the whole amount now, or choose will call or delivery to leave a deposit.';
  }
  return Number.isInteger(err.amount_cents)
    ? `This account cannot carry ${money(err.amount_cents as number)} on account. Pay in full now, or take a deposit with the balance due later.`
    : 'This account cannot carry that on account. Pay in full now, or take a deposit with the balance due later.';
}

// -- item 9.5: the supplier's demanded deposit, billed to the account ----------

/** The control's label, board wording: the rep marks what the supplier asked for. */
export const SUPPLIER_DEPOSIT_LABEL = 'Supplier asked for a deposit';

/** The Summary and receipt line for the billed deposit: the server's words, the amount in dollars. */
export function supplierDepositLine(cents: number, money: (c: number) => string): string {
  return `Supplier deposit, billed to the account: ${money(cents)}`;
}

/**
 * Whether the payment step shows the supplier deposit control: the server's own allowance, and a
 * ring the deposit may ride (the credit ACCOUNT tender, no counter deposit shape in force). The
 * till decides no money rule; this reads the two facts the server already answered.
 */
export function supplierDepositShown(
  allowance: { allowed: boolean },
  tender: string,
  depositShapeActive: boolean,
): boolean {
  return allowance.allowed && tender === 'ACCOUNT' && !depositShapeActive;
}

/**
 * The words for the supplier deposit refusals (422 problem bodies), or null when the error is not
 * one of them. The server's message wins; the fixed fallbacks word the codes.
 */
export function supplierDepositRefusalWords(err: {
  status: number;
  error: string;
  message?: string;
  deposit_cents?: number;
  subtotal_cents?: number;
}): string | null {
  if (err.error === 'supplier_deposit_not_credit') {
    return err.message && err.message !== ''
      ? err.message
      : 'A supplier deposit is billed to a credit account, on account. This sale is not that shape.';
  }
  if (err.error === 'supplier_deposit_over_pretax') {
    if (err.message && err.message !== '') return err.message;
    return 'The supplier deposit is more than the sale pre-tax amount.';
  }
  return null;
}
