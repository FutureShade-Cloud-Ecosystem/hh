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
  available_credit_cents?: number;
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
  /** The account pays before release (zero day terms with a credit line, and no on account shape offered). */
  cashOnDemand?: boolean;
}

export interface WayForward {
  id: 'payment_now' | 'send_paylink' | 'ask_override';
  title: string;
  subtitle: string;
  body: string;
  testId: string;
}

// What the clerk can do and where it is done. None of these cards sends anything: the paylink and
// the override are not wired to this card, so the words name the door and make no claim of a send.
export const WAYS_FORWARD: readonly WayForward[] = [
  {
    id: 'payment_now',
    title: 'Take a payment now',
    subtitle: 'ask the account to pay',
    body: 'Take the payment at the counter or the Money Desk. A hold is released by hand once the account is clear.',
    testId: 'pos-way-payment-now',
  },
  {
    id: 'send_paylink',
    title: 'Send a paylink',
    subtitle: 'from the Money Desk',
    body: 'Ask the account to pay; a paylink is sent from the Money Desk.',
    testId: 'pos-way-send-paylink',
  },
  {
    id: 'ask_override',
    title: 'Ask the credit manager',
    subtitle: 'to override',
    body: 'Ask the credit manager to review the hold; an override is made on their desk.',
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

/** What stands where the credit figure would be for a caller the dealer's account position policy refuses (UX-97). */
export const POSITION_WITHHELD_WORDS = 'Credit position not shown for your role';

/**
 * True when the customer carries no account position at all: the three figures are absent, not zero,
 * for a caller the policy refuses. The check then says so in words and never reads the absence as a
 * credit line of zero ("No credit available" is a sentence the server never said).
 */
export function positionWithheld(customer: AccountCheckCustomer): boolean {
  return (
    customer.credit_limit_cents === undefined &&
    customer.open_balance_cents === undefined &&
    customer.available_credit_cents === undefined
  );
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

  const withheld = positionWithheld(customer);
  const calculatedAvailable = availableCreditCents({
    credit_limit_cents: customer.credit_limit_cents ?? 0,
    open_balance_cents: customer.open_balance_cents ?? 0,
  });
  const availableCreditFormatted = withheld ? POSITION_WITHHELD_WORDS : availableCreditWords(calculatedAvailable);

  const termsDays = customer.payment_terms_days && customer.payment_terms_days > 0 ? customer.payment_terms_days : 0;
  const termsWords = termsDays > 0 ? `Terms ${termsDays} days` : 'No terms on file';

  if (!blocked) {
    const crewMember = options?.crewMemberName || '';
    let title = 'In good standing';
    let lines: string[];
    let crewMemberLine: string;
    if (options?.cashOnDemand === true) {
      crewMemberLine = `${name} pays before release: the sale is paid before the goods go out.`;
      lines = ['Cash on demand: pay before release.', crewMemberLine];
    } else if (withheld) {
      title = 'No hold on this account';
      crewMemberLine = `${name} has no hold. The charge is checked when the sale rings.`;
      lines = [`${POSITION_WITHHELD_WORDS} · ${termsWords}.`, crewMemberLine];
    } else if (calculatedAvailable <= 0) {
      title = 'No credit available';
      crewMemberLine = 'No credit is available, so a charge to this account will be refused.';
      lines = [`Available credit ${availableCreditFormatted} · ${termsWords}.`, crewMemberLine];
    } else {
      crewMemberLine = crewMember
        ? `${crewMember} is a listed crew member of ${name} and may buy on this account. Every ticket needs a job and PO.`
        : `${name} may buy on this account. Every ticket needs a job and PO.`;
      lines = [`Available credit ${availableCreditFormatted} · ${termsWords}.`, crewMemberLine];
    }

    return {
      standing: 'good_standing',
      isBlocked: false,
      title,
      lines,
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
  const title = bucketLabel === null ? 'On hold' : `On hold: past due ${bucketLabel}`;

  // The past due figure is a credit figure like the limit, the balance and the available: a caller
  // the position withholds gets the hold words without it (round 3, visual review P1 3). The till
  // renders no past due row at all for such a caller, word or figure.
  const pastDuePart = !withheld && pastDueFormatted
    ? ` · Past due ${pastDueFormatted}${bucketLabel ? `, ${bucketLabel}` : ''}.`
    : '.';
  const line1 = withheld
    ? `${POSITION_WITHHELD_WORDS}, and the account is on hold${pastDuePart}`
    : `Available credit ${availableCreditFormatted}, but the account is on hold${pastDuePart}`;
  // line1 already says the account is on hold, so the way forward line does not say it again.
  const crewLine = options?.crewMemberName
    ? `${options.crewMemberName} is a listed crew member of ${name}, so a charge to it will be refused.`
    : 'Three ways forward:';
  const lines = [line1];
  if (standing?.promise_text) lines.push(standing.promise_text);
  lines.push(crewLine);

  return {
    standing: 'blocked',
    isBlocked: true,
    title,
    lines,
    availableCreditFormatted,
    termsDays,
    crewMemberLine: crewLine,
    pastDueFormatted,
    bucketLabel,
    waysForward: WAYS_FORWARD,
  };
}
