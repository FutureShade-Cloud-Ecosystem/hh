// Item 9.6 (row 1047 item 6): the deposit at the payment step, as pure helpers. The server names
// the shapes the counter may offer and the words for line two (the preview's payment_options);
// this module only reads them, words the screen around them and shapes the ring's facts. The till
// decides no money rule: whether the account fits a shape is the server's answer, and a refusal
// is the server's message (or, failing that, words made from its fields), never a code or cents.

import type { PaymentOption } from './_pos-preview.js';

export const SHAPE_DEPOSIT_ON_ACCOUNT = 'DEPOSIT_NOW_REST_ON_ACCOUNT';
export const SHAPE_DEPOSIT_DUE_LATER = 'DEPOSIT_NOW_REST_DUE_LATER';

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

/** The sentence under the two lines: what line two means for this account type. */
export function depositSentence(shape: string): string {
  return shape === SHAPE_DEPOSIT_ON_ACCOUNT
    ? 'A credit customer: the rest rides their account and is paid from the statement.'
    : 'Cash On Demand means pay before release, never cash on delivery: the deposit puts the order in process, and picking waits for paid in full.';
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
 * The words for the two deposit refusals (422 problem bodies with integer cents fields and a
 * message in dollars), or null when the error is not one of them. The server's message wins; the
 * fields word it only when the message is missing.
 */
export function depositRefusalWords(
  err: { status: number; error: string; message?: string; total_cents?: number; amount_cents?: number },
  money: (cents: number) => string,
): string | null {
  if (err.error === 'cod_not_for_terms_account') {
    // The ruling's refusal carries no figures: its message is the whole answer.
    return err.message && err.message !== ''
      ? err.message
      : 'This account pays on terms, so its sale is not pay before release. Ring it on account or paid in full.';
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
