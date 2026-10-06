package pos

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// -- DIB-165: split tender ------------------------------------------------------------------------
//
// One sale, settled across several tenders. The counter case that forced it: a contractor pays $400
// cash out of the truck float, puts $600 on the company card and leaves the rest on account. Before
// this the cashier had to ring three sales, which produced three orders, three invoices and three
// receipts for one pile of stone, and a return against it had no single sale to reverse.
//
// THE SHAPE. Each leg is (method, amount). The legs sum to the sale total, exactly, no rounding
// slack, no "close enough" (see checkTenderSum). Every non-ACCOUNT leg is captured through the SAME
// till.TakeTender seam a single-tender sale uses, so each leg gets its own payments row, its own
// drawer movement and its own payment.received event: the GL sees three ordinary tenders, not one
// exotic one. The ACCOUNT leg captures nothing and rides the invoice balance, exactly as a
// single-tender ACCOUNT sale does.
//
// WHY AT MOST ONE ACCOUNT LEG. Two account legs would post the same remainder to AR twice. Enforced
// here for the message and by a partial unique index in 0101 for the truth.

// TenderLegInput is one leg of a split settlement, as the client submits it.
type TenderLegInput struct {
	Method      string `json:"method"`       // ACCOUNT | CASH | CARD | CHEQUE
	AmountCents int64  `json:"amount_cents"` // > 0
	// CARD-only, per leg: a split with two card legs needs two tokens.
	PaymentToken string `json:"payment_token,omitempty"`
	CardType     string `json:"card_type,omitempty"`
}

// TenderLeg is a validated, sequenced leg (1-based seq = submission order).
type TenderLeg struct {
	Seq          int
	Method       string
	AmountCents  int64
	PaymentToken string
	CardType     string
}

// TenderLegResult is one settled leg on the Sale response, what the receipt prints.
type TenderLegResult struct {
	Seq         int    `json:"seq"`
	Method      string `json:"method"`
	AmountCents int64  `json:"amount_cents"`
	PaymentID   string `json:"payment_id,omitempty"`  // "" for the ACCOUNT leg (nothing captured)
	GatewayRef  string `json:"gateway_ref,omitempty"` // CARD only
}

var (
	errSplitTooFewLegs = clientError{status: http.StatusBadRequest,
		msg: "pos: a split tender needs at least two legs, ring a single tender instead"}
	errSplitBadMethod = clientError{status: http.StatusBadRequest,
		msg: "pos: every split tender leg method must be ACCOUNT, CASH, CARD or CHEQUE"}
	errSplitBadAmount = clientError{status: http.StatusUnprocessableEntity,
		msg: "pos: every split tender leg amount must be greater than zero"}
	errSplitTwoAccounts = clientError{status: http.StatusUnprocessableEntity,
		msg: "pos: a split tender may carry at most one ACCOUNT leg"}
	errSplitWithDeposit = clientError{status: http.StatusUnprocessableEntity,
		msg: "pos: a split tender settles the sale itself, do not also send a deposit"}
	errSplitWithCOD = clientError{status: http.StatusUnprocessableEntity,
		msg: "pos: a cash-on-demand sale is unpaid at ring and cannot be split-tendered"}
	errSplitTooManyLegs = clientError{status: http.StatusUnprocessableEntity,
		msg: "pos: a split tender may carry at most 6 legs"}
)

// maxSplitLegs bounds the fan-out: six is more than any counter has ever needed and keeps the number
// of gateway round-trips inside one transaction sane.
const maxSplitLegs = 6

// normalizeSplitLegs validates the submitted legs and stamps their submission order. It returns nil
// (no error) when the sale is not a split, so the caller can branch on len()==0.
//
// It deliberately does NOT check the sum: the sale total is not known until the pricing engine has
// run every line, and a leg list that is well-formed but does not add up must surface as the sum
// error (which can name both numbers) rather than as a shape error.
func normalizeSplitLegs(in SaleInput) ([]TenderLeg, error) {
	if len(in.Tenders) == 0 {
		return nil, nil
	}
	if len(in.Tenders) < 2 {
		return nil, errSplitTooFewLegs
	}
	if len(in.Tenders) > maxSplitLegs {
		return nil, errSplitTooManyLegs
	}
	if in.DepositCents != 0 {
		return nil, errSplitWithDeposit
	}
	if in.SaleType == SaleCOD {
		return nil, errSplitWithCOD
	}
	legs := make([]TenderLeg, 0, len(in.Tenders))
	accounts := 0
	for i, l := range in.Tenders {
		switch l.Method {
		case TenderAccount:
			accounts++
		case TenderCash, TenderCard, TenderCheque:
		default:
			return nil, errSplitBadMethod
		}
		if l.AmountCents <= 0 {
			return nil, errSplitBadAmount
		}
		legs = append(legs, TenderLeg{
			Seq: i + 1, Method: l.Method, AmountCents: l.AmountCents,
			PaymentToken: l.PaymentToken, CardType: l.CardType,
		})
	}
	if accounts > 1 {
		return nil, errSplitTwoAccounts
	}
	return legs, nil
}

// ErrSplitTotalMismatch is the typed shape of the split-tender sum
// refusal: the two amounts the cashier needs, as integer cents. The
// handler answers the problem body from these fields (the stable word
// split_total_mismatch beside them, and a message in dollars), so the
// till renders from the fields; the Go-side Error string keeps the
// module-prefixed cents sentence for the logs, greppable as before.
type ErrSplitTotalMismatch struct {
	TenderedCents int64
	TotalCents    int64
}

func (e ErrSplitTotalMismatch) Error() string {
	return fmt.Sprintf("pos: split tenders total %d cents but the sale total is %d cents, the tenders must sum exactly to the total",
		e.TenderedCents, e.TotalCents)
}

// splitMismatchDollars speaks an amount of integer cents the way the
// counter reads money: 26600 becomes $266.00, 126600 becomes $1,266.00.
// Integer arithmetic only, the amounts are always positive here.
func splitMismatchDollars(cents int64) string {
	dollars, frac := cents/100, cents%100
	d := strconv.FormatInt(dollars, 10)
	var groups []string
	for len(d) > 3 {
		groups = append([]string{d[len(d)-3:]}, groups...)
		d = d[:len(d)-3]
	}
	groups = append([]string{d}, groups...)
	return "$" + strings.Join(groups, ",") + "." + fmt.Sprintf("%02d", frac)
}

// splitMismatchMessage is the refusal's sentence in dollars and plain
// words, the one a person reads at the counter. No message ever prints
// cents.
func splitMismatchMessage(tenderedCents, totalCents int64) string {
	return fmt.Sprintf("The tenders add up to %s, but the sale total is %s.",
		splitMismatchDollars(tenderedCents), splitMismatchDollars(totalCents))
}

// checkTenderSum is the tx-level half of the split-tender guard: the legs must settle the sale
// EXACTLY. The DB half is the deferred constraint trigger in migration 0100, which re-derives the
// same assertion from orders.total_cents at COMMIT so no writer, this service, a migration, a
// psql session, can produce a sale whose legs do not add up.
//
// Exact, not "within a cent". A one-cent slack rule is how drawers end a week $4 short: the slack is
// consumed silently on every sale and nothing ever reconciles. If the arithmetic is off, the client
// computed it wrong and the cashier needs to see the two numbers.
func checkTenderSum(legs []TenderLeg, totalCents int64) error {
	var sum int64
	for _, l := range legs {
		sum += l.AmountCents
	}
	if sum != totalCents {
		return ErrSplitTotalMismatch{TenderedCents: sum, TotalCents: totalCents}
	}
	return nil
}

// primaryLeg picks the leg written to pos_sale.tender: the largest amount, ties broken by submission
// order. Every pre-existing reader of that column (the return path's card_refundable, the reopen
// view, the receipt header) keeps seeing a real tender rather than a synthetic "SPLIT" value it has
// no case for; pos_sale.split_tender is the flag that tells a reader to go look at the legs.
func primaryLeg(legs []TenderLeg) string {
	if len(legs) == 0 {
		return ""
	}
	ordered := make([]TenderLeg, len(legs))
	copy(ordered, legs)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].AmountCents > ordered[j].AmountCents })
	return ordered[0].Method
}

// splitLegKeyNamespace is a fixed UUID namespace for deriving each leg's payment idempotency key.
// It is a constant, not a secret: the derivation must be reproducible across processes and restarts.
var splitLegKeyNamespace = uuid.MustParse("165a1e00-0000-4000-8000-000000000165")

// legIdempotencyKey derives a per-leg payment key from the sale's client key. payments.idempotency_key
// is UNIQUE (0010), so submitting the sale's own key for every leg would make leg 2 a replay of leg 1 -
// one payments row for three tenders, and a drawer that is short by the other two. The derivation is
// deterministic (UUIDv5 over "<sale key>:<seq>"), so retrying the whole sale after a network timeout
// replays each leg onto its own existing row instead of double-charging any of them.
func legIdempotencyKey(saleKey string, seq int) string {
	return uuid.NewSHA1(splitLegKeyNamespace, []byte(fmt.Sprintf("%s:%d", saleKey, seq))).String()
}

// captureLeg is one leg the ring actually captures money for (every method except ACCOUNT). A
// single-tender sale produces zero or one of these; a split produces one per non-ACCOUNT leg. The
// invoice/allocation/subledger/event code downstream iterates this slice and therefore has exactly
// one shape for both cases.
type captureLeg struct {
	Seq          int
	Method       string
	AmountCents  int64
	IdemKey      string
	PaymentToken string
	CardType     string
	// AttemptID is the card charge attempt the pre-transaction capture loop claimed for this leg
	// (CARD only): the sale transaction binds it to the payments row it writes, and the
	// compensator reverses it when the transaction fails.
	AttemptID string
	// filled in by the tx as each leg is captured
	PaymentID  string
	GatewayRef string
}

// captureLegsForSplit projects the validated legs onto the capture list (ACCOUNT captures nothing).
func captureLegsForSplit(legs []TenderLeg, saleKey string) []captureLeg {
	out := make([]captureLeg, 0, len(legs))
	for _, l := range legs {
		if l.Method == TenderAccount {
			continue
		}
		out = append(out, captureLeg{
			Seq: l.Seq, Method: l.Method, AmountCents: l.AmountCents,
			IdemKey: legIdempotencyKey(saleKey, l.Seq), PaymentToken: l.PaymentToken, CardType: l.CardType,
		})
	}
	return out
}

// sumCaptured totals what the ring takes at the counter.
func sumCaptured(caps []captureLeg) int64 {
	var n int64
	for _, c := range caps {
		n += c.AmountCents
	}
	return n
}

// hasCardLeg reports whether any leg charges a card at ring, the offline-block predicate.
func hasCardLeg(caps []captureLeg) bool {
	for _, c := range caps {
		if c.Method == TenderCard {
			return true
		}
	}
	return false
}

// saleTenderRow is one pos_sale_tenders insert (0100).
type saleTenderRow struct {
	Seq           int
	Method        string
	AmountCents   int64
	PaymentID     string
	TillSessionID string
	GatewayRef    string
}
