package pos

// sale_deposit.go, item 9.2 (the row 1047 deposits work): the deposit as line
// one, the rest as line two, and the refusals in the shape the counter reads.
//
// THE BOOKING is the ring's own, unchanged arithmetic: the deposit is one
// captured leg through the till (a CARD deposit rides the card charge rail
// like any card leg), the invoice lands PARTIALLY_PAID with the rest as its
// balance, on account for a credit customer (its due date from the terms) and
// due later for a pay before release sale (no terms date, pay before release).
// What this file adds are the two refusals the item names, both in the
// problem body style split_total_mismatch uses (DIB-165): a stable error word,
// the money as integer cents fields, and a message in dollars, never cents in
// words.

import "fmt"

// ErrAccountShapeNotAvailable refuses an on account booking the account does
// not fit: zero day terms (a cash on demand account, the walk in), no credit
// line, or a pay before release sale, whose line two is due later by
// definition. The ring refuses the SHAPE before the credit gate runs on the
// amount: a cash on demand account with a healthy line still cannot put the
// rest on account, because on account is not a shape that account was offered.
type ErrAccountShapeNotAvailable struct {
	// AmountCents is what would have gone on account (the whole total for an
	// ACCOUNT tender, the account leg's amount for a split).
	AmountCents int64
	// CreditLimitCents is the account's credit line, the fact the refusal
	// names for the counter (zero day terms with a line reads as the line it
	// is; terms with no line reads as the empty line it is).
	CreditLimitCents int64
	// ZeroDayTerms reports whether the refusal came from the terms (zero day
	// or a pay before release sale) rather than the line.
	ZeroDayTerms bool
}

func (e ErrAccountShapeNotAvailable) Error() string {
	return fmt.Sprintf(
		"pos: this account cannot put %d cents on account (zero day terms: %v, credit line %d cents)",
		e.AmountCents, e.ZeroDayTerms, e.CreditLimitCents)
}

// accountShapeMessage is the refusal's sentence in dollars and plain words,
// the one a person reads at the counter. No message ever prints cents.
func accountShapeMessage(e ErrAccountShapeNotAvailable) string {
	return fmt.Sprintf(
		"This account cannot carry %s on account. Pay in full now, or take a deposit with the balance due later.",
		splitMismatchDollars(e.AmountCents))
}

// ErrCODNotForTermsAccount refuses a pay before release (COD) sale type for
// an account that pays on terms (the principal's ruling: Cash On Demand is
// the ACCOUNT's posture; the COD sale type is its guard, and a terms account
// is not in it, whatever the tender). The hold stays keyed on the account's
// own terms, which this refusal leaves no sale to disagree with.
type ErrCODNotForTermsAccount struct{}

func (e ErrCODNotForTermsAccount) Error() string {
	return "pos: a pay before release sale is not for an account that pays on terms"
}

// codNotForTermsMessage is the refusal's sentence, plain words with no figures.
func codNotForTermsMessage() string {
	return "This account pays on terms, so its sale is not pay before release. Ring it on account or paid in full."
}

// ErrDepositOutOfRange refuses a deposit outside its money bound (0 < deposit
// < the sale total): the problem body style the other deposit refusals use,
// with the deposit and the total as integer cents fields and a message in
// dollars. The bound is judged once the engine has priced the lines, so the
// body names the real total; a deposit equal to the total is just a paid sale
// (ring it paid) and a negative one is no deposit at all.
type ErrDepositOutOfRange struct {
	DepositCents int64
	TotalCents   int64
}

func (e ErrDepositOutOfRange) Error() string {
	return fmt.Sprintf(
		"pos: the deposit %d is outside the bound 0 < deposit < the sale total %d cents",
		e.DepositCents, e.TotalCents)
}

// depositOutOfRangeMessage is the refusal's sentence in dollars.
func depositOutOfRangeMessage(e ErrDepositOutOfRange) string {
	return fmt.Sprintf("The deposit must be more than %s and less than the sale total of %s.",
		splitMismatchDollars(0), splitMismatchDollars(e.TotalCents))
}

// ErrDepositNotCollectNow refuses a deposit on a collect now sale: the goods
// do not leave on a part payment. The item allowed refusing in words or
// moving the sale to the matching held shape; the ring REFUSES, keeping the
// customer's ask intact and naming the two honest paths (pay in full now, or
// a fulfilment that waits: will call, delivery, or a pay before release sale),
// rather than silently rebooking a collect now as something the customer did
// not ask for.
type ErrDepositNotCollectNow struct {
	TotalCents   int64
	DepositCents int64
}

func (e ErrDepositNotCollectNow) Error() string {
	return fmt.Sprintf(
		"pos: a collect now sale cannot take a deposit (%d of %d cents), the goods do not leave on a part payment",
		e.DepositCents, e.TotalCents)
}

// depositNotCollectNowMessage is the refusal's sentence in dollars.
func depositNotCollectNowMessage(depositCents, totalCents int64) string {
	return fmt.Sprintf(
		"A collect now sale is paid in full, the goods do not leave on a deposit. Take the whole %s now, or choose will call, delivery or a pay before release sale and leave a deposit.",
		splitMismatchDollars(totalCents))
}

// onAccountCents is what this sale would book on account: for a single
// ACCOUNT tender THE REST, the total less the deposit already taken as line
// one (a deposit sale's line two is the rest, and the rest is what the
// refusal offers to carry); the one ACCOUNT leg a split may carry, stated in
// its own amount; nothing for any other shape. The caller holds it against
// the account's own facts.
func onAccountCents(tender string, legs []TenderLeg, isSplit bool, totalCents, depositCents int64) int64 {
	if isSplit {
		for _, l := range legs {
			if l.Method == TenderAccount {
				return l.AmountCents
			}
		}
		return 0
	}
	if tender == TenderAccount {
		return totalCents - depositCents
	}
	return 0
}
