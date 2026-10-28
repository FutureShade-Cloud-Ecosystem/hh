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
// Today's seed is a finance box's world: the seeded customer invoices carry
// the opening-balance marker, so they have no journal of their own, and
// account 1100 holds no matching balance. The tie is therefore out by the
// whole opening balance, and AR_1100_GL_CENTS says so rather than a zero that
// would read as a tie.

/** The open receivables total in cents: the aging buckets' sum, and the AR subledger's figure for account 1100. */
export const AR_TOTAL_CENTS = 1_087_000;

/** The GL side of the 1100 tie in cents: what account 1100 holds on the same books. */
export const AR_1100_GL_CENTS = 0;

/** The GL account code the AR aging total ties to: the receivables control account the tie-out reads. */
export const AR_CONTROL_ACCOUNT_CODE = "1100";

/** The aging cards the landing carries: [card id, total in cents, accounts counted]. */
export const AR_AGING_CARDS: readonly (readonly [string, number, number])[] = [
  ["card.escalation", 0, 0],
  ["card.late", 0, 0],
  ["card.warn", 0, 0],
  ["card.current", 1_087_000, 2],
];

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

/** The statement's aging strip: [band, cents], the last band being the total due. */
export const AR_STATEMENT_STRIP: readonly (readonly [string, number])[] = [
  ["Current", 1_017_000],
  ["1 to 30 days", 0],
  ["31 to 60 days", 0],
  ["Over 60 days", 0],
  ["Total due", 1_017_000],
];

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
