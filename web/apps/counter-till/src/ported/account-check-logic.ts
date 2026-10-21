// Ported counter account check logic for Change 8. Pure, DOM-free helpers.

import { formatMoney } from "../money.js";
import { availableCreditCents } from "@hardscapehouse/pos-kit/src/ported/ports.js";

export type AccountStandingType = 'good_standing' | 'blocked';

export interface AccountCheckCustomer {
  id?: string;
  name?: string;
  account_no?: string;
  customer_type?: string;
  payment_terms_days?: number;
  credit_limit_cents?: number;
  open_balance_cents?: number;
  on_hold?: boolean;
}

export interface AccountCheckStanding {
  on_hold?: boolean;
  past_due_cents?: number;
  oldest_bucket?: string;
  promise_cents?: number;
  promise_text?: string;
}

export interface AccountCheckOptions {
  isCreditBlockedRefusal?: boolean;
  crewMemberName?: string;
}

export interface WayForward {
  id: 'payment_now' | 'send_paylink' | 'ask_override';
  title: string;
  subtitle: string;
  body: string;
  testId: string;
}

export const WAYS_FORWARD: readonly WayForward[] = [
  {
    id: 'payment_now',
    title: 'Take a payment now',
    subtitle: 'ACH first, no fee',
    body: 'Card adds a 2.9% surcharge, shown as its own line before you confirm. The hold lifts when it lands.',
    testId: 'pos-way-payment-now',
  },
  {
    id: 'send_paylink',
    title: 'Send a paylink',
    subtitle: 'to the owner phone',
    body: 'The link goes to the owner phone on file. It works with no sign in, on a phone.',
    testId: 'pos-way-send-paylink',
  },
  {
    id: 'ask_override',
    title: 'Ask the credit manager',
    subtitle: 'to override',
    body: 'Sends the request to the controller with this order.',
    testId: 'pos-way-ask-override',
  },
];

/**
 * Check whether an account is blocked / on hold.
 * Blocked only from what the server says:
 * - credit gate returned a 409 credit_blocked refusal
 * - customer has on_hold flag set
 * - account standing reports on_hold
 * Never from a customer name or an account number.
 */
export function isAccountBlocked(
  customer: AccountCheckCustomer | null | undefined,
  standing?: AccountCheckStanding | null,
  options?: AccountCheckOptions,
): boolean {
  if (!customer) return false;
  if (options?.isCreditBlockedRefusal === true) return true;
  if (customer.on_hold === true) return true;
  if (standing?.on_hold === true) return true;
  return false;
}

export interface AccountCheckResult {
  standing: AccountStandingType;
  isBlocked: boolean;
  title: string;
  lines: string[];
  availableCreditFormatted: string;
  termsDays: number;
  crewMemberLine: string;
  pastDueFormatted: string | null;
  bucketLabel: string | null;
  waysForward: readonly WayForward[];
}

/** The available credit in words: the server's figure when positive, else a plain statement. */
function availableCreditWords(availableCents: number): string {
  return availableCents > 0 ? formatMoney(availableCents) : 'none (the credit line is used up or not set)';
}

/**
 * Evaluate customer account standing and format all presentation facts.
 * Pure logic testable without browser or DOM. Every figure and name comes from the arguments.
 */
export function evaluateAccountCheck(
  customer: AccountCheckCustomer | null | undefined,
  standing?: AccountCheckStanding | null,
  options?: AccountCheckOptions,
): AccountCheckResult | null {
  if (!customer) return null;

  const blocked = isAccountBlocked(customer, standing, options);
  const name = (customer.name || '').trim();

  const calculatedAvailable = availableCreditCents({
    credit_limit_cents: customer.credit_limit_cents ?? 0,
    open_balance_cents: customer.open_balance_cents ?? 0,
  });
  const availableCreditFormatted = availableCreditWords(calculatedAvailable);

  if (!blocked) {
    const termsDays = customer.payment_terms_days && customer.payment_terms_days > 0
      ? customer.payment_terms_days
      : 30;

    const crewMember = options?.crewMemberName || '';
    const crewMemberLine = crewMember
      ? `${crewMember} is a listed crew member of ${name} and may buy on this account. Every ticket needs a job and PO.`
      : `${name} may buy on this account. Every ticket needs a job and PO.`;

    const line1 = `Available credit ${availableCreditFormatted} · Terms ${termsDays} days · No open promise.`;

    return {
      standing: 'good_standing',
      isBlocked: false,
      title: 'In good standing',
      lines: [line1, crewMemberLine],
      availableCreditFormatted,
      termsDays,
      crewMemberLine,
      pastDueFormatted: null,
      bucketLabel: null,
      waysForward: [],
    };
  }

  const pastDueCents = standing?.past_due_cents && standing.past_due_cents > 0 ? standing.past_due_cents : 0;
  const pastDueFormatted = pastDueCents > 0 ? formatMoney(pastDueCents) : null;

  const rawBucket = standing?.oldest_bucket || '';
  const bucketLabel = rawBucket === '' ? null : rawBucket.includes('days') ? rawBucket : `${rawBucket} days`;
  const title = rawBucket === '' ? 'On hold' : `On hold: past due ${rawBucket}`;

  const pastDuePart = pastDueFormatted
    ? ` · Past due ${pastDueFormatted}${bucketLabel ? `, ${bucketLabel}` : ''}.`
    : '.';
  const line1 = `Available credit ${availableCreditFormatted}, but the account is on hold${pastDuePart}`;
  const crewLine = options?.crewMemberName
    ? `${options.crewMemberName} may buy on this account, but it is on hold. Three ways forward:`
    : 'The account is on hold. Three ways forward:';
  const lines = [line1];
  if (standing?.promise_text) lines.push(standing.promise_text);
  lines.push(crewLine);

  return {
    standing: 'blocked',
    isBlocked: true,
    title,
    lines,
    availableCreditFormatted,
    termsDays: customer.payment_terms_days && customer.payment_terms_days > 0 ? customer.payment_terms_days : 30,
    crewMemberLine: crewLine,
    pastDueFormatted,
    bucketLabel,
    waysForward: WAYS_FORWARD,
  };
}
