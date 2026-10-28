// THE WORLD'S KNOWN FIGURES. The seam the specs read (W2-4-HARNESS-INTERFACE-r1):
// every month, every cent and every journal number a spec asserts comes from this
// file, never from a spec's own text.
//
// W2-1b's world (the plan's item: the twelve business months ending at the anchor
// month, every month before the anchor closed and the anchor month left open, one
// fiscal year end inside those months and closed) has NOT landed, so every constant
// below reads TODAY'S SEED VALUE: what the core's own seed produces.
//
//   - The fiscal periods: db/migrations/0007_gl_block.sql:57 plants fiscal 2026,
//     months 1 to 12, all OPEN. Nothing closes a period, so the seed signs off no
//     month and the seed's year carries no year end marker.
//   - The journals: cmd/core/demoseed_controller.go plants two DRAFT journals (the
//     insurance premium and the inventory write down) and posts nothing, so the
//     seed's trial balance is empty and every balance is 0.
//   - The clock: the demo world is pinned at 2026-09-01T09:00:00Z
//     (cmd/core/demoseed_main.go:5), so the month it has just traded is 2026-08.
//
// THE LINE TO SWITCH, one constant per line: when W2-1b's world lands, the lead
// replaces each value below with the world's own figure and nothing else in the
// specs moves. The world runs the twelve business months ending at its anchor, so
// its month end is the month before the anchor, its open month is the anchor month
// itself, and one of its years carries the year end marker.

/** The world's month end (YYYY-MM): the fiscal month the ledger closes on. */
export const MONTH_END = "2026-08";

/** The world's Accounts Receivable (1100) balance at that month end, in cents. */
export const MONTH_END_1100_CENTS = 0;

/** A month the world has signed off (YYYY-MM): its bank reconciliation is signed off at zero. */
export const SIGNED_OFF_BANK_MONTH = "2026-08";

/** The world's open month (YYYY-MM): the month a journal can still post into. */
export const OPEN_MONTH = "2026-09";

/** The year that carries the world's year end marker, or 0 when no year does. */
export const YEAR_END_MARKED_YEAR = 0;

/** The world's journal the ledger reads, by its entry number (the register names it JE-0000). */
export const LEDGER_JOURNAL_ENTRY_NO = "JE-0001";

/** That journal's memo, the words the desk shows beside its number. */
export const LEDGER_JOURNAL_MEMO = "Insurance premium paid ahead for the year (demo)";
