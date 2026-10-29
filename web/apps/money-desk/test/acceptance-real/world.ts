// THE WORLD'S KNOWN FIGURES. The seam the specs read (W2-4-HARNESS-INTERFACE-r1):
// every month, every cent and every journal number a spec asserts comes from this
// file, never from a spec's own text.
//
// W2-1b's world (the plan's item: the twelve business months ending at the anchor
// month, every month before the anchor closed and the anchor month left open, one
// fiscal year end inside those months and closed) has NOT landed, so every constant
// below reads TODAY'S SEED VALUE: what the FINANCE box's own seed produces.
//
// WHERE TODAY'S SEED'S FIGURES COME FROM. The finance box boots on a fresh database:
// the migrations run first, then core finance-seed applies the Dibbits seed
// (core/cmd/core/dibbits_seed.sql), and the box's worker drains the posting queue the
// migrations left behind. Three sources of the ledger's figures, all read on a finance
// box seeded in October 2026 and all reproduced by a local migrate and finance-seed of
// this tree on a throwaway Postgres:
//
//   - THE JUNE SETUP ENTRIES: migration 0056_bistrack_tieout_demo.sql posts four POSTED
//     journals (entry numbers 1 to 4) into fiscal period 2026-06, and migration 0221
//     (the file 0221_the_setup_entries_behind_no_customer_document_come_back_out.sql)
//     reverses all four of them (entry numbers 5 to 8). The pair nets to zero on every
//     account, so 2026-06 carries the movement twice and every balance in it is 0.
//   - THE AR MOVEMENT: migration 0055_portal_demo_fixtures.sql plants two FINALIZED demo
//     counterparty invoices (INV-DEMO-CP03-Q at 20000 cents and INV-DEMO-CP03-X at 50000
//     cents) dated CURRENT_DATE, and migration 0222 queues their ar.invoice.finalized
//     events, which the box's worker posts as DR 1100 and CR 4000 in the month the box
//     booted. That is the seed's 1100 movement: 70000 cents, a $700.00 balance.
//   - THE FISCAL PERIODS: migration 0007_gl_block.sql plants fiscal 2026, months 1 to 12,
//     and nothing in the seed closes one, so the seed signs off NO month and no year
//     carries the year end marker.
//
// THE TRIAL BALANCE THE SEED CARRIES. The whole fiscal year reads $38,350.00 on both sides
// (the harness's own pinned figure, harness.pw.ts's SEED_WORLD block: 3835000 cents), and
// 1100 Accounts Receivable carries $17,000.00 by $16,300.00 of that movement, a $700.00
// balance. The month the AR movement lands in carries $700.00 on both sides on its own.
//
// THE TWO MONTHS ARE READ AT RUN TIME, NOT WRITTEN DOWN. A month is not a fact about the
// seed: migration 0055 dates the seed's two demo counterparty invoices CURRENT_DATE, so
// the month the seed's AR movement lands in is the month the box was BUILT in, and nobody
// can write that month down in a file. A month literal here is the time bomb class the
// repo already refuses in Go (DIB-427): a box built in a later month makes legs 1 to 3 red
// for nobody's commit.
//
// WHERE THE MONTH COMES FROM, read through the core at run time:
//   - THE SEEDED INVOICE'S OWN ISSUE DATE, the month the box was built in. That date is
//     fixed when the migrations run, so it names the boot month however long after that
//     the spec runs, and it is the month the $700.00 movement is in: the month a trial
//     balance can be read for, and a month that can still post.
//   - THE CORE'S OWN REPORTED DATE, the box's wall clock, for a box whose register does
//     not carry that invoice. It is the fallback, not the first choice: a wall clock
//     drifts with the run, and the movement's own month does not.
//
// THE LINE TO SWITCH, one constant per line: when W2-1b's world lands, the lead
// replaces each value below with the world's own figure and nothing else in the
// specs moves. The world runs the twelve business months ending at its anchor, so
// its month end is the month before the anchor, its open month is the anchor month
// itself, and one of its years carries the year end marker. THE TWO MONTH ANCHORS
// below are where that happens: while an anchor is empty the specs read the box's own
// month at run time, and when an anchor carries a month the specs read that month.


/**
 * The seeded demo invoice whose own issue date names the box's boot month: migration 0055
 * plants it dated CURRENT_DATE, so its issue date IS the month the box was built in.
 */
export const BOOT_MONTH_INVOICE_NO = "INV-DEMO-CP03-Q";

/**
 * The world's month end (YYYY-MM), or the empty string while the world has no fixed month
 * and the specs read the box's own month at run time. When W2-1b's world lands this is
 * where its month end goes: the month before its anchor.
 */
export const MONTH_END_ANCHOR = "";

/**
 * The world's open month (YYYY-MM), or the empty string while the world has no fixed month
 * and the specs read the box's own month at run time. When W2-1b's world lands this is
 * where its open month goes: its anchor month itself.
 */
export const OPEN_MONTH_ANCHOR = "";

/**
 * The month a core's date answer names. A core's dates are RFC 3339 strings whose first
 * seven characters are the month, so the month is read, never assumed.
 */
export function monthOfDate(coreDate: string): string {
  const matched = /^(\d{4})-(\d{2})/.exec(coreDate);
  if (matched === null) {
    throw new Error(`the core's date "${coreDate}" names no month this spec can read`);
  }
  return `${matched[1]}-${matched[2]}`;
}

/** The world's Accounts Receivable (1100) balance at that month end, in cents ($700.00: the seed's two demo counterparty invoices). */
export const MONTH_END_1100_CENTS = 70_000;

/** A month the world has signed off (YYYY-MM), or the empty string while the seed signs off no month at all. */
export const SIGNED_OFF_BANK_MONTH = "";

/** The year that carries the world's year end marker, or 0 when no year does. */
export const YEAR_END_MARKED_YEAR = 0;

/** The world's journal the ledger reads, by its entry number (the register names it JE-0001). */
export const LEDGER_JOURNAL_ENTRY_NO = "JE-0001";

/**
 * The fiscal year that journal belongs to. This is NOT the box's boot month and does not
 * move with it: the ledger's own key is the fiscal year and the entry number together
 * (gl_journals' UNIQUE (fiscal_year, entry_no)), and migration 0056 posts that journal
 * into fiscal period 2026-06 on every box, whatever month the box booted in.
 */
export const LEDGER_JOURNAL_FISCAL_YEAR = 2026;

/** That journal's memo, the words the desk shows beside its number (migration 0056's first journal). */
export const LEDGER_JOURNAL_MEMO = "M2-C1 demo: invoice finalized";

// ---- W2-4 (b): the receivables figures ----------------------------------------------------
//
// W2-4 (b): the receivables figures the real core answers with.
//
// The figures below were read off the core's own API on a real core box
// running today's seed (core migrate, core finance-seed), never off the app.
// The app is the thing under test; a figure it computes is never the expected
// value.
//
// Until W2-1b's world lands, every constant here reads TODAY'S SEED VALUE.
// When the world's driver lands (deploy/finance/world/run.sh, fin2's W2-1b),
// each constant is switched to the world's figure on the line its own comment
// names; the switch is one line per constant and nothing else in the suite
// moves. Parts (c) and (d) add their own constants to this file: the fold
// unions all three sets rather than taking one.
//
// Today's seed is a finance box's world: the seeded INV-2026-0002 carries the
// opening-balance marker, so it has no journal of its own, and account 1100
// holds only the two demo invoices the box's worker posts ($700.00). The tie
// is therefore out by INV-2026-0002's balance, and AR_1100_GL_CENTS says so
// rather than a figure that would read as a tie (settled on the real box at
// the fold).

/** The open receivables total in cents: the aging buckets' sum, and the AR subledger's figure for account 1100. */
export const AR_TOTAL_CENTS = 1_087_000;

/**
 * The GL side of the 1100 tie in cents: what account 1100 holds on the same books, read on the
 * real finance box. The box's worker posts the two demo counterparty invoices (migrations 0055 and
 * 0222, 20000 and 50000 cents) as DR 1100, and the June setup entries net to zero (0056 against
 * 0221); the seeded INV-2026-0002 carries the opening-balance marker and has no journal. The
 * tie-out reads every year to date, so this does not move with the clock. W2-1b's world ties by
 * construction and switches this line.
 */
export const AR_1100_GL_CENTS = 70_000;

/** The GL account code the AR aging total ties to: the receivables control account the tie-out reads. */
export const AR_CONTROL_ACCOUNT_CODE = "1100";

/**
 * The aging cards the landing carries, in the core's order. Only the ids are the world's: which
 * card an invoice sits on moves with the clock (an invoice's due date ages past the run day), so
 * the specs hold each card's figure to the core's own landing and hold their SUM to AR_TOTAL_CENTS,
 * which does not move (FROM-PRINCIPAL 389's clock rule).
 */
export const AR_AGING_CARD_IDS: readonly string[] = ["card.escalation", "card.late", "card.warn", "card.current"];

/** The open invoices the register lists: [invoice number, balance in cents]. */
export const AR_OPEN_INVOICES: readonly (readonly [string, number])[] = [
  ["INV-DEMO-CP03-Q", 20_000],
  ["INV-DEMO-CP03-X", 50_000],
  ["INV-2026-0002", 1_017_000],
];

/** The register's foot: how many invoices it lists, and their total in cents. */
export const AR_REGISTER_COUNT: number = 3;

/** The register's foot total in cents: the server's own sum of the listed rows. */
export const AR_REGISTER_CENTS = 1_087_000;

/** The account a statement is run for, its name as the desk shows it, and its account number. */
export const AR_STATEMENT_CUSTOMER_NAME = "Frontenac Home Builders";

/** The statement account's number. */
export const AR_STATEMENT_ACCOUNT_NO = "CUST-1005";

/** The statement's closing balance in cents: what the account owes after the run. */
export const AR_STATEMENT_CLOSING_CENTS = 1_017_000;

/**
 * The statement's aging strip bands, as the desk labels them, the last being the total due. The
 * split across the bands moves with the clock, so the specs hold each band to the core's own
 * statement and the total due to AR_STATEMENT_CLOSING_CENTS, which does not move.
 */
export const AR_STATEMENT_BANDS: readonly string[] = ["Current", "1 to 30 days", "31 to 60 days", "Over 60 days", "Total due"];

/** The invoice a received payment is applied to, and the payment's amount in cents. */
export const AR_PAYMENT_INVOICE_NO = "INV-2026-0002";

/** The received payment's amount in cents: the invoice's whole balance. */
export const AR_PAYMENT_CENTS = 1_017_000;

/** The world's named staff: the financial controller who reads the book, and the platform id the box grants. */
export const AR_FINANCIAL_CONTROLLER = {
  platformUserId: "fin-console-user-a",
  email: "jordan.blake@dibbits.example",
  name: "Jordan Blake",
  roleName: "Financial Controller",
  tier: 1,
  scope: "GLOBAL",
} as const;

// ---- W2-4 (d): the bank reconciliation figures ----------------------------------------------
//
// (d) was written against W2-1b's world (anchor 2026-09, the month before signed off). Today's
// finance seed signs off no month (SIGNED_OFF_BANK_MONTH above is ""), so the join round moves
// (d)'s legs onto the seed's branch with the anchor switch, as (c)'s ledger legs do. OPEN_MONTH
// below is (d)'s pre-join literal and goes in that round (FROM-PRINCIPAL 389: no month literal).

/** (d)'s open month, pre-join: the W2-1b world's anchor. Replaced by the box's own month in the join round. */
export const OPEN_MONTH = "2026-09";

/** The world's tie: a signed off month's reconciliation is complete at zero difference, in cents. */
export const SIGNED_OFF_VARIANCE_CENTS = 0;
