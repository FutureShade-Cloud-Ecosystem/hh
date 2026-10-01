// Ported from hardscapeos_dibbits master 87d7941b app/src/tender-logic.ts; adapted for HH.

// tender-logic.ts: how money taken at a counter is NAMED, as against how it is stored.
//
// WHY THIS FILE EXISTS AT ALL. The tender vocabulary is not one screen's private business: the same
// two columns are rendered by the payment inspector (payment-history.ts), the past-sale reopen
// (pos-lookup.ts), the drawer Z-report (till-close.ts) and the NSF queue (ar-console.ts). Four
// components, one fact. Before this file each of them printed the stored value straight through, so
// there was no map to disagree with: and no place to put one either. The house rule is that
// lifecycle knowledge lives in the matrix and never branched in a component, so the matrix had to be
// created before the four call sites could import it.
//
// It is a NEW file, and that is a compromise worth stating plainly. The payments/POS module already
// has pos-api.ts and ar-api.ts, and warranty-api.ts is the standing precedent for hanging word maps
// off a module's api file. This session's file ownership put both api files in another hand, so the
// maps landed here instead. If those two ever merge, this is the file that should move into them.

/**
 * TENDER_METHOD_LABEL is what a tender is CALLED, as against what the column stores.
 *
 * It translates FOUR columns that are deliberately the same vocabulary: `payments.method`
 * (migration 0010, CASH|CARD|CHEQUE|EFT|OTHER), `till_tender_totals.method` and
 * `till_movements.method` (0028: "mirrors payments.method", its own words), and the counter's
 * `pos_sale.tender` / `pos_sale_tenders.method` (0029 and 0100, ACCOUNT|CASH|CARD|CHEQUE). The union
 * of the four is the six values below. One map rather than four is the whole point: a fifth surface
 * that renders a tender should not get to invent a fifth spelling of "cheque".
 *
 * WHAT IS WRONG WITH THE STORED WORDS is narrower here than elsewhere and worth being honest about.
 * CASH and CHEQUE are already the counter's own nouns: they are wrong on a screen only because they
 * are SHOUTED, and a row of capitals in a dense table reads as a system talking to itself rather
 * than to the person at the till. ACCOUNT is the one that genuinely mis-speaks: the button that
 * takes it says "Account" (pos-terminal.ts) and the sale it makes is a charge to an account, so the
 * screen that reports it back must say the same word the screen that took it said. EFT keeps its
 * capitals because EFT is the trade's word for the thing, not an enum's: the AR payment control
 * already labels it exactly that way (_ar-invoices.ts TENDERS), and this map reuses that rather than
 * inventing a second name for one state.
 */
export const TENDER_METHOD_LABEL: Record<string, string> = {
  ACCOUNT: 'Account',
  CASH: 'Cash',
  CARD: 'Card',
  CHEQUE: 'Cheque',
  EFT: 'EFT',
  OTHER: 'Other',
};

/**
 * tenderMethodWord renders a tender, degrading HONESTLY: an unmapped value comes back as itself
 * lowercased with its underscores opened out, so a seventh method added to 0010's CHECK: an
 * INTERAC or a STORE_CREDIT: shows up on the screen as strange rather than as a confident lie about
 * which of the six it is. Same rule, and same default arm, as willCallWord (dispatch-logic.ts).
 */
export function tenderMethodWord(method: string): string {
  return TENDER_METHOD_LABEL[method] ?? method.toLowerCase().replace(/_/g, ' ');
}

/**
 * PAYMENT_STATUS_LABEL is what a payment's own state is CALLED.
 *
 * `payments.status` is RECEIVED | DEPOSITED | BOUNCED | REFUNDED | VOID (migration 0010), widened by
 * 0027 to reserve PENDING for an offline card that has been queued but not captured. These are the
 * six a person can be shown.
 *
 * The stored words are close to English and still wrong on a screen, for two reasons. The first is
 * the shouting: this value is printed inline beside a payment number and an amount, and capitals
 * there read as an error message rather than as a fact about the money. The second is VOID, which is
 * an adjective doing a verb's job: the payment was voided, and "Voided" is what the person who
 * voided it would say. BOUNCED keeps the word the AR console already uses out loud in its own
 * notices ("Bounced: reversed …"), because a state that already has a name in the product does not
 * get a second one here.
 */
export const PAYMENT_STATUS_LABEL: Record<string, string> = {
  RECEIVED: 'Received',
  DEPOSITED: 'Deposited',
  BOUNCED: 'Bounced',
  REFUNDED: 'Refunded',
  VOID: 'Voided',
  PENDING: 'Pending',
};

/**
 * paymentStatusWord renders a payment's state, degrading HONESTLY: an unmapped value comes back as
 * itself, lowercased, never as 'Unknown' and never as ''. 0027 already widened this CHECK once; the
 * next widening will be visible on the screen the day it ships instead of hiding behind a word that
 * happens to be wrong.
 */
export function paymentStatusWord(status: string): string {
  return PAYMENT_STATUS_LABEL[status] ?? status.toLowerCase().replace(/_/g, ' ');
}
