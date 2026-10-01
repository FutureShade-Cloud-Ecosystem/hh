// Ported from hardscapeos_dibbits master 87d7941b app/src/_customer-picker.ts; adapted for HH.

// _customer-picker.ts: the decision logic behind the quote builder's customer field.
//
// THE PROBLEM: the quote builder asked for a "Customer id" and a "Branch id" as raw text. Nobody
// knows a uuid. In practice that meant copying one out of another screen, and a typo produced a
// 422 from the server with no indication of which of the two fields was wrong. The replacement is
// a type-ahead over the accounts the caller can actually see.
//
// House convention (_pricing-audit.ts, _quote-edit.ts): the logic lives here, DOM-free and
// unit-tested; the Lit component renders it and owns the network call.

/** The account projection GET /api/v1/customers/search returns. */
export interface PickerCustomer {
  id: string;
  name: string;
  account_no: string;
  customer_type: string;
  payment_terms_days: number;
  credit_limit_cents: number;
  open_balance_cents: number;
}

/** How many recently-used accounts we keep, per branch. */
export const MAX_RECENTS = 8;

/**
 * CUSTOMER_TYPE_LABEL is what `customers.customer_type` (migration 0006) is CALLED.
 *
 * The column is CONTRACTOR | RETAIL | BUILDER | GOVERNMENT | INTERNAL, and BOTH account pickers:
 * the quote builder's type-ahead and the till's: printed it into the sub-line under the account
 * name exactly as stored: "ACCT-1042 · CONTRACTOR". That line is read at a counter with somebody
 * waiting, and it was shouting a CHECK constraint at them.
 *
 * ONE map, imported by both pickers, because it is ONE column. The words are not new: the identity
 * card (_pos.ts accountTypeTag) and the quote work area (_quote-workarea.ts customerTypeTag) have
 * been saying "Contractor" and "Retail" for these same five values all along: they just said it by
 * title-casing the string, which is a cosmetic mask rather than a translation. It writes no words,
 * it cannot be read to find out what the product calls a state, and it dresses a value it does not
 * recognise so convincingly that a new one would look deliberate. This map is where the words live.
 *
 * It sits in the picker's own logic module rather than in either component, per the house rule that
 * lifecycle knowledge lives in the matrix and never in a component: a component that knew this map
 * would be the second place the same fact lived, and there are two components.
 */
export const CUSTOMER_TYPE_LABEL: Record<string, string> = {
  CONTRACTOR: 'Contractor',
  RETAIL: 'Retail',
  BUILDER: 'Builder',
  GOVERNMENT: 'Government',
  INTERNAL: 'Internal',
};

/**
 * customerTypeWord renders an account's type, degrading HONESTLY: an unmapped value comes back as
 * itself, lowercased with underscores turned to spaces, so a sixth type added to 0006's CHECK reads
 * as STRANGE rather than as a confident lie about which price list an account is on. Never
 * 'Unknown' and never '': a missing account type is a thing to notice, not a thing to hide.
 */
export function customerTypeWord(customerType: string): string {
  return CUSTOMER_TYPE_LABEL[customerType] ?? String(customerType ?? '').toLowerCase().replace(/_/g, ' ');
}

/**
 * recentsKey scopes the recently-used list to a BRANCH.
 *
 * This is the honest half of "branch-scoped results". The SERVER cannot scope the result set:
 * GET /api/v1/customers/search (internal/customer/repository.go:SearchAccounts) neither accepts a
 * branch filter nor returns customers.home_branch_id, and internal/customer is outside this
 * bundle's fence: so a genuinely branch-filtered search is a backend change, parked and named in
 * REPORT-W2-U.md rather than faked here by hiding rows the API said were valid.
 *
 * What IS branch-scoped is recency, which is where branch-scoping actually earns its keep day to
 * day: the counter at one branch should not have the other branch's accounts pushed to the top of
 * their list. An empty branch id gets its own bucket rather than silently sharing branch A's.
 */
export function recentsKey(branchId: string): string {
  const b = (branchId ?? '').trim();
  return `hos-quote-customer-recents:${b === '' ? 'unset' : b}`;
}

/**
 * rememberRecent returns the list with `id` promoted to the front, deduplicated and capped.
 * Pure: the caller owns persistence: so the ordering rule is testable without a DOM.
 */
export function rememberRecent(
  list: readonly string[],
  id: string,
  max: number = MAX_RECENTS,
): string[] {
  const clean = (id ?? '').trim();
  if (clean === '') return [...list];
  const cap = Number.isFinite(max) && max > 0 ? Math.trunc(max) : MAX_RECENTS;
  return [clean, ...list.filter((x) => x !== clean)].slice(0, cap);
}

/**
 * orderResults sorts the server's results so recently-used accounts come first, in recency order,
 * and everything else keeps the server's own ordering (name ASC).
 *
 * It NEVER adds a row the server did not return and never removes one. A recent account that does
 * not match the current query simply is not in `results` and does not appear: the recency list is
 * a re-ordering of the truth, not a second source of it. That distinction is what stops a stale
 * localStorage entry from resurrecting a deactivated account in front of the user.
 */
export function orderResults<T extends { id: string }>(
  results: readonly T[],
  recentIds: readonly string[],
): T[] {
  const rank = new Map<string, number>();
  recentIds.forEach((id, i) => {
    if (!rank.has(id)) rank.set(id, i);
  });
  return [...results].sort((a, b) => {
    const ra = rank.has(a.id) ? rank.get(a.id)! : Number.POSITIVE_INFINITY;
    const rb = rank.has(b.id) ? rank.get(b.id)! : Number.POSITIVE_INFINITY;
    if (ra !== rb) return ra - rb;
    return 0; // Array.prototype.sort is stable, so non-recent rows keep the server's name order.
  });
}

/**
 * nextActive moves the keyboard highlight, wrapping at both ends.
 *
 * -1 means "nothing highlighted" (the state right after typing). Pressing Down from there lands on
 * the FIRST row rather than the second: an off-by-one here is the difference between a picker that
 * feels right and one that quietly skips the best match, which is usually the one at the top.
 * An empty list stays at -1 so Enter cannot commit a row that is not there.
 */
export function nextActive(current: number, delta: number, len: number): number {
  if (!Number.isFinite(len) || len <= 0) return -1;
  if (current < 0) return delta > 0 ? 0 : len - 1;
  return (((current + delta) % len) + len) % len;
}

/**
 * termsLabel renders payment_terms_days the way the counter says it out loud.
 *
 * 0 days is PREPAID, not "Net 0": the account pays before the goods move, which is a materially
 * different conversation from "Net 30" and must not read as a formatting quirk.
 */
export function termsLabel(days: number): string {
  if (!Number.isFinite(days)) return 'Terms unknown';
  const d = Math.trunc(days);
  if (d <= 0) return 'Prepaid';
  return `Net ${d}`;
}

/**
 * pickerStatus is the text for the combobox's aria-live region: what a screen-reader user hears
 * as they type. Silence during a type-ahead is the accessibility failure mode: the list changes
 * under them with no announcement at all.
 */
export function pickerStatus(opts: {
  query: string;
  loading: boolean;
  error: string;
  count: number;
}): string {
  if (opts.error) return opts.error;
  if (opts.loading) return 'Searching accounts…';
  if (opts.query.trim() === '') return 'Type at least two characters to search accounts.';
  if (opts.count === 0) return `No accounts match ${opts.query.trim()}.`;
  return `${opts.count} account${opts.count === 1 ? '' : 's'} match. Use the up and down arrows to choose.`;
}

/** The shortest query worth a round trip. One character matches most of the book. */
export const MIN_QUERY = 2;

/** True when `q` is worth sending to the server. */
export function isSearchable(q: string): boolean {
  return (q ?? '').trim().length >= MIN_QUERY;
}
