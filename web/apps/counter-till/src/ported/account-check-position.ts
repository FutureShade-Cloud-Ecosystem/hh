// The account check under the dealer's account position policy (UX-97).
//
// When the policy withholds the credit limit and balance from the caller, the customer carries no
// figures and evaluateAccountCheck would read their absence as a credit line of zero: "No credit
// available", a confident wrong sentence the server never said. This wrapper keeps the figures path
// exactly as it was (it calls evaluateAccountCheck unchanged) and, for a withheld account, says so
// in words and names no figure. The server keeps deciding whether a charge is allowed: the hold
// gate and the credit gate are the enforcement, and a refusal at the ring arrives in the server's
// own words.

import {
  evaluateAccountCheck,
  isAccountBlocked,
  WAYS_FORWARD,
  type AccountCheckCustomer,
  type AccountCheckOptions,
  type AccountCheckResult,
  type AccountCheckStanding,
} from './account-check-logic.js';
import { formatMoney } from "../money.js";

/** What stands where the credit figure would be for a caller the policy refuses. */
export const POSITION_WITHHELD_WORDS = 'Credit position not shown for your role';

/** True when the customer carries no account position at all (absent, not zero). */
export function positionWithheld(customer: AccountCheckCustomer): boolean {
  return (
    customer.credit_limit_cents === undefined &&
    customer.open_balance_cents === undefined &&
    (customer as { available_credit_cents?: number }).available_credit_cents === undefined
  );
}

export function evaluateAccountCheckUnderPolicy(
  customer: AccountCheckCustomer | null | undefined,
  standing?: AccountCheckStanding | null,
  options?: AccountCheckOptions,
): AccountCheckResult | null {
  if (!customer) return null;
  if (!positionWithheld(customer)) return evaluateAccountCheck(customer, standing, options);

  const name = (customer.name || '').trim();
  const termsDays = customer.payment_terms_days && customer.payment_terms_days > 0 ? customer.payment_terms_days : 0;
  const termsWords = termsDays > 0 ? `Terms ${termsDays} days` : 'No terms on file';

  if (!isAccountBlocked(customer, standing, options)) {
    const crewMemberLine = `${name} has no hold. The server decides a charge to this account when the sale rings.`;
    return {
      standing: 'good_standing',
      isBlocked: false,
      title: 'No hold on this account',
      lines: [`${POSITION_WITHHELD_WORDS} · ${termsWords}.`, crewMemberLine],
      availableCreditFormatted: POSITION_WITHHELD_WORDS,
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
  const pastDuePart = pastDueFormatted
    ? ` · Past due ${pastDueFormatted}${bucketLabel ? `, ${bucketLabel}` : ''}.`
    : '.';
  const crewLine = options?.crewMemberName
    ? `${options.crewMemberName} is a listed crew member of ${name}, but the account is on hold, so a charge to it will be refused.`
    : 'The account is on hold. Three ways forward:';
  const lines = [`${POSITION_WITHHELD_WORDS}, and the account is on hold${pastDuePart}`];
  if (standing?.promise_text) lines.push(standing.promise_text);
  lines.push(crewLine);
  return {
    standing: 'blocked',
    isBlocked: true,
    title,
    lines,
    availableCreditFormatted: POSITION_WITHHELD_WORDS,
    termsDays,
    crewMemberLine: crewLine,
    pastDueFormatted,
    bucketLabel,
    waysForward: WAYS_FORWARD,
  };
}
