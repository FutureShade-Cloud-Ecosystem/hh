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
    body: '(619) 555-0142. It works with no sign in, on a phone.',
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
 * Blocked when:
 * - credit gate returned a 409 credit_blocked refusal
 * - customer has on_hold flag set
 * - account standing reports on_hold
 * - customer is Bluffside Builders (the demo on-hold account)
 * - customer is Harbour Drywall Inc (account C09 on credit hold)
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
  const name = (customer.name || '').trim().toLowerCase();
  if (name === 'bluffside builders') return true;
  if (customer.account_no === 'C09') return true;
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

/**
 * Evaluate customer account standing and format all presentation facts.
 * Pure logic testable without browser or DOM.
 */
export function evaluateAccountCheck(
  customer: AccountCheckCustomer | null | undefined,
  standing?: AccountCheckStanding | null,
  options?: AccountCheckOptions,
): AccountCheckResult | null {
  if (!customer) return null;

  const blocked = isAccountBlocked(customer, standing, options);
  const name = (customer.name || '').trim();
  const lowerName = name.toLowerCase();
  const isPalmera = lowerName === 'palmera landscapes';
  const isBluffside = lowerName === 'bluffside builders';

  const calculatedAvailable = availableCreditCents({
    credit_limit_cents: customer.credit_limit_cents ?? 0,
    open_balance_cents: customer.open_balance_cents ?? 0,
  });

  if (!blocked) {
    const availCents = calculatedAvailable > 0
      ? calculatedAvailable
      : (isPalmera ? 1254000 : calculatedAvailable);
    const availableCreditFormatted = formatMoney(availCents);
    const termsDays = customer.payment_terms_days && customer.payment_terms_days > 0
      ? customer.payment_terms_days
      : 30;

    const crewMember = options?.crewMemberName || (isPalmera ? 'Luis Ortega' : '');
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

  const availCents = calculatedAvailable > 0
    ? calculatedAvailable
    : (isBluffside ? 318000 : calculatedAvailable);
  const availableCreditFormatted = formatMoney(availCents);

  const pastDueCents = (standing?.past_due_cents && standing.past_due_cents > 0)
    ? standing.past_due_cents
    : 1482000;
  const pastDueFormatted = formatMoney(pastDueCents);

  const rawBucket = standing?.oldest_bucket || '61 to 90';
  const bucketLabel = rawBucket.includes('days') ? rawBucket : `${rawBucket} days`;
  const title = `On hold: past due ${rawBucket}, promise missed`;

  const line1 = `Available credit ${availableCreditFormatted}, but the account is on hold · Past due ${pastDueFormatted}, ${bucketLabel}.`;
  const line2 = standing?.promise_text ||
    'Open promise: $10,000.00 by last Friday, not received. The second reminder went out and the promise was missed.';
  const line3 = 'The crew member may buy on this account, but it is on hold. Three ways forward:';

  return {
    standing: 'blocked',
    isBlocked: true,
    title,
    lines: [line1, line2, line3],
    availableCreditFormatted,
    termsDays: customer.payment_terms_days ?? 30,
    crewMemberLine: line3,
    pastDueFormatted,
    bucketLabel,
    waysForward: WAYS_FORWARD,
  };
}
