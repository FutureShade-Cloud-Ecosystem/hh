// The world's KNOWN figures, as the world's own build states them. A spec never hard codes a figure: it reads this
// module. Until W2-1b's world lands, each constant reads today's seed value, and the header comment beside it names
// the line that switches it to the world's figure.
//
// W2-4 (d), the bank's month. The world closes every month before its anchor and leaves the anchor month open
// (FIN-WAVE2-PLAN-r2.md, "Months"), and its tie at the end says "each closed month's bank reconciliation complete
// at zero difference". The world is built at seed 1, anchor 2026-09, dealer "Quarryfen Hardscape Supply" (the wave
// 2 round 2 notes), so the months below are already the world's months, and the zero difference is already the
// world's tie.
//
// THE SWITCH LINES, for the fold: today's seed plants no bank statement and no reconciliation at all
// (core/internal/demo has no bankrec row), so there is no seed value for the bank's months to read. The figures
// only the world knows, the month's statement balance, its ledger balance and its line count, are not here either:
// the spec reads those from the core's own answer and holds the screen to that, so no figure of the spec waits on
// a number this module cannot hold. When the world's dump lands, the fold checks the world against the core's
// answer once, and the constants below are the ones the dump carries.

/** The world's anchor month, the month the world leaves open: YYYY-MM. */
export const WORLD_ANCHOR_MONTH = "2026-09";

/** The month before the anchor, the one the world closed and signed off at zero difference: YYYY-MM. */
export const SIGNED_OFF_BANK_MONTH = "2026-08";

/** The world's open month, the anchor itself, whose reconciliation is never signed off: YYYY-MM. */
export const OPEN_MONTH = WORLD_ANCHOR_MONTH;

/** The world's tie: a closed month's reconciliation is complete at zero difference, in cents. */
export const SIGNED_OFF_VARIANCE_CENTS = 0;
