// The margin surface's policy gate (P3-10, the comprehensive train review): the
// till shows the margin column, the footer's margin and profit segments, and
// the margin eye exactly when the server's own policy word allows the
// signed-in caller. The principal's ruling: the surface follows the word
// /pos/policy answers, even while the glance itself keeps answering (the
// glance's serving rule is the core's cost and margin configuration, and that
// half is the hardening lane's). Pure and DOM-free: the terminal holds the
// policy read and the session, and asks this helper for the answer.
//
// The word is the contract's own enum (pos-api.ts, PosPolicy), matched
// exactly; the tier numbers are the core resolver's
// (core/internal/identity/authority.go, read only): 1 leadership, 2 manager,
// 3 frontline. An absent or unknown word fails closed, the core's own
// posture, and so does a caller whose session carries no tier when the word
// demands a manager.
export function marginVisibleToCaller(
  marginVisibility: string | undefined,
  tier: number | undefined,
): boolean {
  if (marginVisibility === 'MANAGER_UP') return tier === 1 || tier === 2;
  if (marginVisibility === 'ALL_STAFF') return true;
  return false; // HIDDEN, absent, unknown: fail closed
}
