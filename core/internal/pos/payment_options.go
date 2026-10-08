package pos

import "context"

// payment_options.go, item 9.1 (the row 1047 deposits work): WHICH PAYMENT
// SHAPES THE COUNTER MAY OFFER, answered by the server so no screen decides a
// money rule.
//
// The shapes, one per account type, in the item's own words (the pay before
// release option added by the row 1214 ruling):
//
//	CREDIT customer (terms above zero AND a credit line):
//	  PAY_IN_FULL_NOW, ALL_ON_ACCOUNT, DEPOSIT_NOW_REST_ON_ACCOUNT
//	CASH ON DEMAND customer (zero day terms WITH a credit line):
//	  PAY_IN_FULL_NOW, PAY_BEFORE_RELEASE, DEPOSIT_NOW_REST_DUE_LATER
//	WALK IN, any NO CREDIT account, a held credit account:
//	  PAY_IN_FULL_NOW, DEPOSIT_NOW_REST_DUE_LATER
//
// Never on account for a walk in or a no credit account, and a credit customer
// on hold loses on account (the existing hold rule, till/holds.go, the same
// fact the hold aware credit gate denies an ACCOUNT tender on): a held credit
// account is offered exactly what a pay before release account is, cash or
// card only, which are the hold rule's own words.
//
// WHERE IT RIDES. On the sale preview, the smaller honest change: the preview
// is already where the till reads its customer (the walk in resolution), its
// branch wall and its priced totals at the payment step, so the shapes ride
// the same answer instead of a second read the till would have to keep beside
// it. The preview still writes nothing.
//
// THE WORDS FOR LINE TWO come from the server too ("On account" or "Balance
// due later"), because they are money words: which of them the counter shows
// is decided by the same facts that decide what the ring will accept.
//
// WHAT THIS IS NOT. It is not a gate. The ring keeps its own refusal (the
// credit gate, the deposit bound, the shape refusal item 9.2 adds); this read
// tells the till what may be OFFERED so the offer and the refusal cannot
// disagree by construction, both being the server's. The ring still runs on
// the amount, a credit line that fits the shape can still be over its limit
// for a particular total, and that answer belongs to the gate, not the menu.

// The payment shape codes. A closed set, so a client can switch on them.
const (
	// PaymentShapePayInFull: pay the whole ticket now (CASH, CARD or CHEQUE).
	PaymentShapePayInFull = "PAY_IN_FULL_NOW"
	// PaymentShapeAllOnAccount: line two is the whole ticket, on the account's terms.
	PaymentShapeAllOnAccount = "ALL_ON_ACCOUNT"
	// PaymentShapeDepositOnAccount: line one a deposit now, line two the rest on account.
	PaymentShapeDepositOnAccount = "DEPOSIT_NOW_REST_ON_ACCOUNT"
	// PaymentShapeDepositDueLater: line one a deposit now, line two the rest due later,
	// paid before release (the pay before release posture).
	PaymentShapeDepositDueLater = "DEPOSIT_NOW_REST_DUE_LATER"
	// PaymentShapePayBeforeRelease: the whole ticket unpaid at ring, no deposit: the
	// invoice number issues now, is due today, and fulfilment waits for paid in full
	// (any fulfilment). The counter's former COD fulfilment tile rang exactly this
	// sale; the ruling (row 1214) moved the choice here, where the account, not the
	// tile, decides. Offered to the Cash On Demand account alone.
	PaymentShapePayBeforeRelease = "PAY_BEFORE_RELEASE"
)

// The words the till shows for line two of a deposit sale. Server owned, the
// same words the receipt and the hold print.
const (
	LineTwoOnAccount = "On account"
	LineTwoDueLater  = "Balance due later"
)

// The sentence under the two lines, by reason. Server owned like the line two
// words, so a screen never words a money rule: a credit customer's rest rides
// the account; a Cash On Demand account (zero day terms with a credit line) is
// told what its posture means; every other pay before release account (a no
// credit retail account, a walk in, a credit customer on hold) is told what it
// is, in the words of the on account refusal (on_account_not_available).
const (
	SentenceOnAccount    = "A credit customer: the rest rides their account and is paid from the statement."
	SentenceCashOnDemand = "Cash On Demand means pay before release, never cash on delivery: the deposit puts the order in process, and picking waits for paid in full."
	SentenceCannotCarry  = "This account cannot carry this sale on account: pay in full now, or take a deposit with the balance due later. The deposit puts the order in process, and picking waits for paid in full."
)

// PaymentOption is one shape the counter may offer, with the words for line
// two when the shape has one (the deposit shapes; the paid and on account
// shapes carry no line two words of their own).
type PaymentOption struct {
	Shape string `json:"shape"`
	// LineTwoWords is the server's wording for the shape's line two, present
	// only on the deposit shapes.
	LineTwoWords string `json:"line_two_words,omitempty"`
	// LineTwoSentence is the one sentence the counter shows under the two
	// lines, present only on the deposit shapes and chosen by the reason the
	// account is offered that shape.
	LineTwoSentence string `json:"line_two_sentence,omitempty"`
}

// paymentOptionsFor derives the shapes from the account's own facts. Pure, so
// the rule can be argued with without a database:
//
//   - a credit customer is terms above zero AND a credit line, either missing
//     and the account pays before release (a zero day terms account with a
//     line is the cash on demand account, a terms account with no line is a
//     no credit account, and the walk in row is both at once);
//   - a hold removes on account for a credit customer, the hold rule's own
//     posture (cash or card only), so the deposit's line two becomes the
//     balance due later.
func paymentOptionsFor(termsDays int, creditLimitCents int64, held bool) []PaymentOption {
	if termsDays > 0 && creditLimitCents > 0 && !held {
		return []PaymentOption{
			{Shape: PaymentShapePayInFull},
			{Shape: PaymentShapeAllOnAccount},
			{Shape: PaymentShapeDepositOnAccount, LineTwoWords: LineTwoOnAccount, LineTwoSentence: SentenceOnAccount},
		}
	}
	sentence := SentenceCannotCarry
	options := []PaymentOption{{Shape: PaymentShapePayInFull}}
	// The no deposit pay before release ring is the Cash On Demand account's
	// alone (row 1214, the posture of row 999): zero day terms WITH a line, the
	// same fact that chooses the sentence below. A terms account is refused the
	// ring outright (ruling 11), a no credit account and the walk in have no
	// line to draw the unpaid exposure against (the credit gate's own COD
	// constraint), and a held credit account is back on cash or card only: none
	// of them may be offered a shape the ring would refuse. The option carries
	// no line two (there is no deposit) and no sentence of its own.
	if termsDays <= 0 && creditLimitCents > 0 {
		sentence = SentenceCashOnDemand
		options = append(options, PaymentOption{Shape: PaymentShapePayBeforeRelease})
	}
	return append(options,
		PaymentOption{Shape: PaymentShapeDepositDueLater, LineTwoWords: LineTwoDueLater, LineTwoSentence: sentence})
}

// previewPaymentOptions reads the hold fact only when the account could offer
// on account at all (a pay before release account loses nothing by a hold),
// and FAILS CLOSED on a hold read that cannot be made or a seam that is not
// wired: the preview keeps answering its totals, but it never offers credit it
// could not prove is clear. The production wiring (wire_tops.go) hands the
// preview the same hold repository the standing read and the credit gate read,
// so the unwired shape is a composition gap, not a state a dealer meets.
//
// Item 9 Opus P2-3: the due later deposit rides the COD sale type, so the
// branch's pos_policy.cod_enabled gates it too, and the pay before release
// option rides the same sale type and the same switch. With Cash On Demand off
// (or a policy read that cannot be made), the pay before release accounts are
// offered pay in full now only, the same fact the ring's own errCODDisabled
// refuses on: the menu never promises a shape the ring will not take. A credit
// customer's shapes ride on account, not the COD sale type, and are untouched.
func (s *Service) previewPaymentOptions(ctx context.Context, branchID string, cust customerRec) []PaymentOption {
	if cust.PaymentTermsDays <= 0 || cust.CreditLimitCents <= 0 {
		if !s.codEnabledAtBranch(ctx, branchID) {
			return []PaymentOption{{Shape: PaymentShapePayInFull}}
		}
		return paymentOptionsFor(cust.PaymentTermsDays, cust.CreditLimitCents, false)
	}
	held := true
	if s.holds != nil {
		ok, err := s.holds.HasActiveHold(ctx, cust.ID)
		if err == nil {
			held = ok
		}
		// An errored read keeps held=true: the fail closed posture above.
	}
	if held && !s.codEnabledAtBranch(ctx, branchID) {
		// A held credit account is offered what a pay before release account
		// is, and the due later deposit is off with the switch: cash or card
		// only, the hold rule's own words.
		return []PaymentOption{{Shape: PaymentShapePayInFull}}
	}
	return paymentOptionsFor(cust.PaymentTermsDays, cust.CreditLimitCents, held)
}

// codEnabledAtBranch answers the branch's Cash On Demand switch, failing
// closed (the branch row overrides the org default, the ring's own gate
// shape). A false or unreadable switch drops the due later deposit from the
// menu; the ring's refusal stays the enforcement either way.
func (s *Service) codEnabledAtBranch(ctx context.Context, branchID string) bool {
	policy, err := s.store.ReadPosPolicy(ctx, branchID)
	if err != nil {
		return false
	}
	return policy.CODEnabled
}
