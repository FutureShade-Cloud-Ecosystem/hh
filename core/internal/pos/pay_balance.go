package pos

// pay_balance.go, item 9.4 (the row 1047 deposits work): PAY BALANCE at the
// counter till. Two routes, staff only, branch fenced:
//
//	find  GET /api/v1/pos/pay-balance?q=...   the orders that owe money, by
//	                                         order number, customer name or phone
//	take  POST /api/v1/pos/pay-balance        the WHOLE balance of one order,
//	                                         by CASH or CARD
//
// No part payments at this door: an amount that is not the whole balance is
// refused in words, and an already paid order is refused in words. The card
// goes through the card charge rail (the attempt store, idempotent by key; a
// replay answers the first result and charges nothing), the money allocates
// to the order's invoice or invoices, and 9.3's hold lifts with the payment
// because it reads the money: the balances this settle moves are the balances
// the hold reads, in one transaction.
//
// THE LOCK ORDER (payments/LOCK-ORDER.md): the take is a writer that works on
// one order, so it takes the order row, then the payments sequence row without
// advancing it, then the order's existing invoice rows FOR NO KEY UPDATE in
// ascending id order, and only then draws the payments feed through the till's
// tender and the invoices feed on the balance moves: the register settle's own
// order with the order row in front, which that order's first step allows.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
	"github.com/jackc/pgx/v5"
)

// PaidInFullWords is the receipt's line, server owned.
const PaidInFullWords = "Paid in full"

// pickHoldSentence is the hold's mandated words (order/pick_hold.go owns the
// sentence; this is the copy this module's answers carry, the same words).
const pickHoldSentence = "This order cannot reach picking until the balance is paid."

// errPayBalanceDrawerRefused answers a non card tender the drawer would not
// take: one fixed sentence, never the till's internal error text.
var errPayBalanceDrawerRefused = clientError{status: http.StatusConflict,
	msg: "The payment could not be taken at the drawer. Check the drawer and try again."}

// errPayBalanceKeyUsedElsewhere refuses a take whose idempotency key already
// holds a payment on ANOTHER order (a ring sale's leg key reused, say): the
// replay fast path answers only this request's own order, fixed words.
var errPayBalanceKeyUsedElsewhere = clientError{status: http.StatusConflict,
	msg: "This idempotency key already took a payment on another order; use a new key for a new payment."}

// ErrPayBalanceOrderNotFound answers an order this door cannot pay: missing,
// or in a branch the caller does not reach (the same 404 for both, no
// existence leak across the branch wall).
var ErrPayBalanceOrderNotFound = clientError{status: http.StatusNotFound, msg: "pos: no order found that owes money for that reference"}

// ErrPayBalanceAlreadyPaid answers a take on an order that owes nothing: the
// desk's own words, so the counter never asks for a balance of $0.00.
var ErrPayBalanceAlreadyPaid = clientError{status: http.StatusConflict, msg: "The order is paid in full."}

// ErrPayBalancePartAmount refuses an amount that is not the whole balance,
// the problem body style split_total_mismatch uses.
type ErrPayBalancePartAmount struct {
	SentCents    int64
	BalanceCents int64
}

func (e ErrPayBalancePartAmount) Error() string {
	return fmt.Sprintf("pos: this door takes the whole balance, %d was offered against %d cents",
		e.SentCents, e.BalanceCents)
}

// payBalancePartMessage is the refusal's sentence in dollars.
func payBalancePartMessage(e ErrPayBalancePartAmount) string {
	return fmt.Sprintf("This door takes the whole balance: %s is owed, %s was entered.",
		splitMismatchDollars(e.BalanceCents), splitMismatchDollars(e.SentCents))
}

// OwingPayment is one payment an order has taken so far: the money trail the
// find answer and the take receipt both print.
type OwingPayment struct {
	PaymentID   string `json:"payment_id"`
	Method      string `json:"method"`
	AmountCents int64  `json:"amount_cents"`
}

// OwingOrder is one order that owes money, the find answer's row: order
// number, customer, job, fulfilment, order total, each payment so far with
// its method and amount, still owed, held or not (9.3's hold).
type OwingOrder struct {
	OrderID         string         `json:"order_id"`
	OrderNo         string         `json:"order_no"`
	CustomerID      string         `json:"customer_id"`
	CustomerName    string         `json:"customer_name"`
	Job             string         `json:"job,omitempty"`
	Fulfilment      string         `json:"fulfilment"`
	OrderTotalCents int64          `json:"order_total_cents"`
	PaidCents       int64          `json:"paid_cents"`
	StillOwedCents  int64          `json:"still_owed_cents"`
	HeldFromPicking bool           `json:"held_from_picking"`
	HoldWords       string         `json:"hold_words,omitempty"`
	Payments        []OwingPayment `json:"payments"`

	// pastPicking is the order module's ruling that the order has already
	// reached or passed picking: if it owes again (an NSF after delivery) the
	// hold words say the balance is due (item 9 Opus P3-4).
	pastPicking bool
}

// PayBalanceTakeInput is the POST body: the whole balance of one order.
type PayBalanceTakeInput struct {
	OrderID        string `json:"order_id"`
	Method         string `json:"method"` // CASH | CARD
	AmountCents    int64  `json:"amount_cents"`
	TillSessionID  string `json:"till_session_id"`
	IdempotencyKey string `json:"idempotency_key"`
	PaymentToken   string `json:"payment_token,omitempty"` // CARD only, client-tokenized
	CardType       string `json:"card_type,omitempty"`
	Offline        bool   `json:"offline,omitempty"`
}

// PayBalanceTaken is the take's answer, what the receipt and the after card
// need: paid in full, released to picking or not, and the money trail.
type PayBalanceTaken struct {
	OrderID            string         `json:"order_id"`
	OrderNo            string         `json:"order_no"`
	CustomerID         string         `json:"customer_id"`
	PaymentID          string         `json:"payment_id"`
	Method             string         `json:"method"`
	AmountCents        int64          `json:"amount_cents"`
	PaidWords          string         `json:"paid_words"`
	ReleasedToPicking  bool           `json:"released_to_picking"`
	StillOwedCents     int64          `json:"still_owed_cents"`
	HeldFromPickingNow bool           `json:"held_from_picking_now"`
	Payments           []OwingPayment `json:"payments"`
	Replayed           bool           `json:"replayed,omitempty"`
}

// payBalanceOrderFacts is the locked read of the order this door pays: its
// own facts, its customer's terms, its open invoices with their balances and
// what its live INVOICE documents have billed, taken in the lock order the
// file header names.
type payBalanceOrderFacts struct {
	OrderID      string
	OrderNo      string
	CustomerID   string
	CustomerName string
	BranchID     string
	Fulfilment   string
	Status       string
	TermsDays    int
	TotalCents   int64
	// InvoicedCents is what the order's live INVOICE documents (neither DRAFT
	// nor VOID) bill, the figure the hold's uninvoiced remainder turns on.
	InvoicedCents int64
	Invoices      []payBalanceInvoice
}

// payBalanceOrderCancelled is the one order status this door refuses on sight:
// a cancelled order is not payable here (its money is unwound by the desk, and
// the raise would refuse it anyway), so every read treats it as not found
// rather than owing.
const payBalanceOrderCancelled = "CANCELLED"

// payBalanceDueWords is the find's hold words for an order already past
// picking that owes again: the picking sentence would speak of a runway the
// order has already used (item 9 Opus P3-4).
const payBalanceDueWords = "Balance due"

// payBalanceInvoice is one open invoice of the order, locked FOR NO KEY UPDATE.
type payBalanceInvoice struct {
	InvoiceID    string
	InvoiceNo    string
	BalanceCents int64
}

// remainderCents is the part of the order's total nothing has invoiced yet:
// under pay before release it is owed too, exactly the hold's own arithmetic.
func (f payBalanceOrderFacts) remainderCents() int64 {
	if rest := f.TotalCents - f.InvoicedCents; rest > 0 {
		return rest
	}
	return 0
}

// balanceCents is ONE NUMBER, the hold's own owed: the open invoice balances
// plus the uninvoiced remainder. The find's still_owed_cents, the pre read's
// early refusals and the take's whole balance all read it, so the hold and
// this door can never disagree about what an order owes.
func (f payBalanceOrderFacts) balanceCents() int64 {
	var n int64
	for _, inv := range f.Invoices {
		n += inv.BalanceCents
	}
	return n + f.remainderCents()
}

// payBalanceHoldView is the hold's own reading, taken inside the take's
// transaction after the money moved: owed from the same sums the hold reads,
// held the same zero day terms rule. The take's answer carries these figures
// rather than hard coding them.
type payBalanceHoldView struct {
	OwedCents int64
	Held      bool
}

// ErrPayBalanceRestUnbillable refuses a take whose whole balance includes a
// part no invoice covers and this door cannot bill: an order partly invoiced
// from its shipment groups is billed to completion by the fulfilment path, and
// its open invoices are paid at the register. Fixed words, never an internal
// error text.
var ErrPayBalanceRestUnbillable = clientError{status: http.StatusConflict,
	msg: "Part of this order is not invoiced yet and cannot be billed at this door; its open invoices are paid at the register, the rest when its invoice exists."}

// ErrPayBalanceRaiseRefused answers a refusal the invoice raise itself
// returned inside the transaction (an order that is cancelled between the pre
// read and the locks, has no lines, or disagrees with its own lines): one
// fixed 409 sentence, never a bare 500 and never the ring's "sale" word (a
// payment is not a sale).
var ErrPayBalanceRaiseRefused = clientError{status: http.StatusConflict,
	msg: "This order cannot be billed at this door right now; ask the desk to check the order and try again."}

// ErrPayBalanceInvoicerUnwired answers a take that would have to raise an
// invoice with no invoicer behind the door: the move is refused, never a take
// that strands the remainder.
var ErrPayBalanceInvoicerUnwired = clientError{status: http.StatusServiceUnavailable,
	msg: "This order's invoice could not be raised at this door, try again in a moment."}

// RaisedOrderInvoice is the document the invoicer seam raised on the caller's
// transaction.
type RaisedOrderInvoice struct {
	InvoiceID    string
	InvoiceNo    string
	TotalCents   int64
	BalanceCents int64
}

// PayBalanceInvoicer is the billing writer the take raises an uninvoiced
// order's invoice through, on the take's own transaction: the same writer the
// desk's order payment raises with (quote/order_payment.go), never a copy of
// it. Consumer declared; the composition root binds billing's InvoiceWriter.
type PayBalanceInvoicer interface {
	RaiseOrderInvoiceTx(ctx context.Context, tx pgx.Tx, orderID string) (RaisedOrderInvoice, error)
}

// WithPayBalanceInvoicer wires (or overrides) the invoicer seam.
func (s *Service) WithPayBalanceInvoicer(i PayBalanceInvoicer) *Service { s.payInvoicer = i; return s }

// payBalancePre is the advisory pool read before any charge: the same facts
// the locks re-derive, for the early refusals a person can act on first.
type payBalancePre struct {
	OrderID      string
	OrderNo      string
	CustomerID   string
	BranchID     string
	TermsDays    int
	BalanceCents int64
}

// PayBalanceFind answers the orders that owe money and match the term (an
// order number, a customer name, a phone), fenced to the caller's reach: a
// branch scoped caller finds only their branches' orders, another branch's
// order is simply not found. truncated is true when a bound stopped the find
// with more orders possibly matching (the till says to search by order number).
func (s *Service) PayBalanceFind(ctx context.Context, term string) ([]OwingOrder, bool, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return nil, false, err
	}
	term = strings.TrimSpace(term)
	if term == "" {
		return nil, false, clientErr(http.StatusBadRequest, "pos: say which order, or whose name or phone")
	}
	out, truncated, err := s.store.FindOwingOrders(ctx, term, callerReachBranches(caller), 20)
	if err != nil {
		return nil, false, err
	}
	if out == nil {
		out = []OwingOrder{}
	}
	for i := range out {
		if out[i].HeldFromPicking {
			out[i].HoldWords = pickHoldSentence
			if out[i].pastPicking {
				out[i].HoldWords = payBalanceDueWords
			}
		}
	}
	return out, truncated, nil
}

// callerReachBranches lists the branches a scoped caller reaches; nil for a
// global caller, the store read's no-filter shape.
func callerReachBranches(caller identity.StaffContext) []string {
	if caller.Scope == identity.ScopeGlobal {
		return nil
	}
	ids := make([]string, 0, len(caller.Branches))
	for b := range caller.Branches {
		ids = append(ids, b)
	}
	return ids
}

// PayBalanceTake takes the WHOLE balance of one order by CASH or CARD. The
// card is captured on the rail before the transaction opens (the ring's own
// loop and failure handling), the settle runs in one transaction with the
// till writing the payment and the drawer, and a replay answers the first
// result having charged and written nothing.
func (s *Service) PayBalanceTake(ctx context.Context, in PayBalanceTakeInput) (PayBalanceTaken, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return PayBalanceTaken{}, err
	}
	in.Method = strings.ToUpper(strings.TrimSpace(in.Method))
	in.OrderID = strings.TrimSpace(in.OrderID)
	switch in.Method {
	case TenderCash, TenderCard:
	default:
		return PayBalanceTaken{}, clientErr(http.StatusBadRequest, "pos: method must be CASH or CARD")
	}
	if in.OrderID == "" {
		return PayBalanceTaken{}, clientErr(http.StatusBadRequest, "pos: order_id is required")
	}
	if in.IdempotencyKey == "" {
		return PayBalanceTaken{}, errMissingKey
	}
	if in.AmountCents <= 0 {
		return PayBalanceTaken{}, clientErr(http.StatusBadRequest, "pos: amount_cents must be greater than zero")
	}
	if in.TillSessionID == "" {
		return PayBalanceTaken{}, ErrNoOpenTill{}
	}
	if in.Method == TenderCard {
		if in.PaymentToken == "" {
			return PayBalanceTaken{}, clientErr(http.StatusBadRequest, "pos: a CARD payment needs its client tokenized payment_token")
		}
		if in.Offline {
			return PayBalanceTaken{}, errCardOffline
		}
	}

	// The payments row this door writes carries the same DERIVED leg key the
	// ring's tenders carry (payments.idempotency_key is UNIQUE, and the raw
	// take key is the client's, reused nowhere else): leg 1 of this key.
	payKey := legIdempotencyKey(in.IdempotencyKey, 1)

	// The replay fast path, before any lock and any charge: the payments row
	// this key already wrote answers the take (the ring's pos_sale replay,
	// with the payments row as the record).
	if past, found, ferr := s.store.FindPayBalancePaymentByKey(ctx, payKey); ferr != nil {
		return PayBalanceTaken{}, ferr
	} else if found {
		// The replay belongs to THIS request's order only (the review's P3-1):
		// the key space is shared with the ring's own leg keys, so a key that
		// took a payment on another order is refused, never answered as this
		// order's replay.
		if past.OrderID != in.OrderID {
			return PayBalanceTaken{}, errPayBalanceKeyUsedElsewhere
		}
		return s.payBalanceReplay(ctx, caller, in, past)
	}

	// The advisory pre-read (pool): the early refusals a person can act on
	// before any card is charged. The transaction re-validates the same words
	// under the locks.
	pre, found, err := s.store.ReadOwingOrder(ctx, in.OrderID)
	if err != nil {
		return PayBalanceTaken{}, err
	}
	if !found {
		return PayBalanceTaken{}, ErrPayBalanceOrderNotFound
	}
	if !caller.CanAccessBranch(pre.BranchID) {
		return PayBalanceTaken{}, ErrPayBalanceOrderNotFound
	}
	// NO CARD IS EVER CAPTURED ON A TAKE THAT CANNOT COMPLETE (the delta
	// review's P2-A): a cancelled order is not this door's (the read excludes
	// it; the guard holds for the shapes a race can still present), and a
	// balance that includes an uninvoiced remainder beside a live invoice is a
	// part this door cannot bill (the raise would refuse it after the charge,
	// and the charge and reversal is exactly what P2-3 removed).
	if pre.Status == payBalanceOrderCancelled {
		return PayBalanceTaken{}, ErrPayBalanceOrderNotFound
	}
	if pre.InvoicedCents > 0 && pre.remainderCents() > 0 {
		return PayBalanceTaken{}, ErrPayBalanceRestUnbillable
	}
	if pre.remainderCents() > 0 && s.payInvoicer == nil {
		return PayBalanceTaken{}, ErrPayBalanceInvoicerUnwired
	}
	if pre.balanceCents() == 0 {
		return PayBalanceTaken{}, ErrPayBalanceAlreadyPaid
	}
	if in.AmountCents != pre.balanceCents() {
		return PayBalanceTaken{}, ErrPayBalancePartAmount{SentCents: in.AmountCents, BalanceCents: pre.balanceCents()}
	}

	// The ring's step 5b (DIB-389), ahead of any capture: the session must
	// exist, be open and belong to the order's branch, in the ring's own 409
	// words, so no card is charged for a take the drawer would refuse anyway
	// (a charge then reversal the customer sees) and no cash lands in another
	// branch's drawer.
	if s.tender != nil {
		sess, serr := s.tender.GetSession(ctx, in.TillSessionID)
		if serr != nil {
			return PayBalanceTaken{}, clientErr(http.StatusConflict, "pos: till session %s unavailable: %s", in.TillSessionID, serr.Error())
		}
		if sess.Status != till.StatusOpen {
			return PayBalanceTaken{}, clientErr(http.StatusConflict, "pos: till session %s is not open", in.TillSessionID)
		}
		if sess.BranchID != pre.BranchID {
			return PayBalanceTaken{}, clientErr(http.StatusConflict, "pos: till session %s belongs to another branch", in.TillSessionID)
		}
	}

	actor := identity.StaffUserIDFromContext(ctx)

	// The card leg: captured on the rail before the transaction, exactly the
	// ring's loop and typed answers, one leg for the whole balance.
	captures := []captureLeg{}
	if in.Method == TenderCard {
		captures = append(captures, captureLeg{
			Seq: 1, Method: TenderCard, AmountCents: pre.balanceCents(),
			IdemKey:      payKey,
			PaymentToken: in.PaymentToken,
			CardType:     in.CardType,
		})
	}
	ring, err := s.captureCardLegs(ctx, SaleInput{
		IdempotencyKey: in.IdempotencyKey, BranchID: pre.BranchID, TillSessionID: in.TillSessionID,
	}, captures, pre.CustomerID, actor)
	if err != nil {
		return PayBalanceTaken{}, err
	}
	if ring.replay != nil {
		return PayBalanceTaken{}, ErrCardChargeInFlight
	}
	if ring.failed() {
		_, ansErr := s.settleFailedCardRing(ctx, ring)
		return PayBalanceTaken{}, ansErr
	}

	var out PayBalanceTaken
	var tenderRes till.TenderResult
	txErr := s.store.RunInTx(ctx, func(tx pgx.Tx) error {
		facts, ferr := s.store.LockPayBalanceOrderTx(ctx, tx, in.OrderID)
		if ferr != nil {
			return ferr
		}
		if !caller.CanAccessBranch(facts.BranchID) {
			return ErrPayBalanceOrderNotFound
		}
		// The pre read's refusals, re-derived under the locks: a cancellation
		// or a part invoice that landed between the read and the locks refuses
		// here rather than charging and reversing.
		if facts.Status == payBalanceOrderCancelled {
			return ErrPayBalanceOrderNotFound
		}
		if facts.InvoicedCents > 0 && facts.remainderCents() > 0 {
			return ErrPayBalanceRestUnbillable
		}
		// An uninvoiced remainder is owed too (the hold's own number), so the
		// whole balance includes it: the take raises the invoice for it inside
		// this transaction, through the same writer the desk's order payment
		// raises with, and then settles the whole. A remainder this door
		// cannot bill (an order partly invoiced from its shipments) refuses
		// in words with nothing taken.
		if rest := facts.remainderCents(); rest > 0 {
			if s.payInvoicer == nil {
				return ErrPayBalanceInvoicerUnwired
			}
			raised, rerr := s.payInvoicer.RaiseOrderInvoiceTx(ctx, tx, facts.OrderID)
			if rerr != nil {
				return rerr
			}
			facts.Invoices = append(facts.Invoices, payBalanceInvoice{
				InvoiceID: raised.InvoiceID, InvoiceNo: raised.InvoiceNo, BalanceCents: raised.BalanceCents,
			})
			// The writer bills the order WHOLE (it refuses an order that
			// already carries a live invoice another way), so from here the
			// order is fully invoiced and the balance is exactly the open
			// balances, the raised document's included: leaving the remainder
			// counted would bill it twice.
			facts.InvoicedCents = facts.TotalCents
		}
		balance := facts.balanceCents()
		if balance == 0 {
			return ErrPayBalanceAlreadyPaid
		}
		if in.AmountCents != balance {
			// The balance moved between the pre-read and the locks (another
			// route took a payment). For a CARD take the captured leg is
			// compensated below, exactly the ring's fault path.
			return ErrPayBalancePartAmount{SentCents: in.AmountCents, BalanceCents: balance}
		}
		capture := till.CardCapture{}
		if len(captures) == 1 {
			capture = till.CardCapture{AttemptID: captures[0].AttemptID, ProcessorRef: captures[0].GatewayRef}
		}
		res, terr := s.tender.TakeTender(ctx, tx, till.TenderInput{
			TillSessionID:  in.TillSessionID,
			Method:         in.Method,
			AmountCents:    balance,
			CustomerID:     facts.CustomerID,
			PaymentToken:   in.PaymentToken,
			CardType:       in.CardType,
			IdempotencyKey: payKey,
			Offline:        in.Offline,
			Capture:        capture,
		})
		if terr != nil {
			if in.Method == TenderCard {
				return terr
			}
			// A non card tender failure is answered with ONE fixed sentence
			// (the review's P3-5): the till's own error taxonomy is its
			// package's, and its internals never reach the cashier.
			return errPayBalanceDrawerRefused
		}
		tenderRes = res
		if !res.Inserted {
			// A concurrent take with the same key won the payments row: this
			// transaction wrote nothing, answer the winner's payment as the
			// replay it is.
			return errPayBalanceLostRace{}
		}
		for _, inv := range facts.Invoices {
			if serr := s.store.SettleInvoiceTx(ctx, tx, settleInvoice{
				PaymentID: res.PaymentID, InvoiceID: inv.InvoiceID, CustomerID: facts.CustomerID,
				AmountCents: inv.BalanceCents, BranchID: facts.BranchID, AllocatedBy: actor,
				GatewayRef: res.GatewayRef, OrderID: facts.OrderID, Method: in.Method,
			}); serr != nil {
				return serr
			}
		}
		// The hold's own reading, on this transaction, after the money moved:
		// the answer's release, still owed and held figures are these, never
		// hard coded.
		holdNow, herr := s.store.PayBalanceHoldTx(ctx, tx, facts.OrderID)
		if herr != nil {
			return herr
		}
		trail, terr2 := s.store.OrderPaymentsTrailTx(ctx, tx, facts.OrderID)
		if terr2 != nil {
			return terr2
		}
		out = PayBalanceTaken{
			OrderID: facts.OrderID, OrderNo: facts.OrderNo, CustomerID: facts.CustomerID,
			PaymentID: res.PaymentID, Method: in.Method, AmountCents: balance,
			PaidWords:          PaidInFullWords,
			ReleasedToPicking:  !holdNow.Held,
			StillOwedCents:     holdNow.OwedCents,
			HeldFromPickingNow: holdNow.Held,
			Payments:           trail,
		}
		return nil
	})
	if txErr != nil {
		if errors.As(txErr, &errPayBalanceLostRace{}) {
			past := payBalanceReplayRow{OrderID: in.OrderID, PaymentID: tenderRes.PaymentID,
				Method: in.Method, AmountCents: in.AmountCents}
			return s.payBalanceReplay(ctx, caller, in, past)
		}
		if len(ring.captured) > 0 {
			ring.rank = cardRankReversed
			_, serr := s.settleFailedCardRing(ctx, ring)
			// The compensation's own failures outrank the refusal's words (a
			// held charge needs a manager); a cleanly reversed card falls
			// through to the words below.
			if serr != nil && !errors.Is(serr, ErrSaleFailedCardReversed) {
				return PayBalanceTaken{}, serr
			}
			// A refusal this door can say in words answers its own words, never
			// the ring's "sale" word: a payment is not a sale, and the cashier
			// reads what to do next, not what the ring would have said.
			var ce clientError
			if errors.As(txErr, &ce) {
				return PayBalanceTaken{}, ce
			}
			return PayBalanceTaken{}, ErrSaleFailedCardReversed
		}
		return PayBalanceTaken{}, txErr
	}

	// Post commit, the ring's own hooks: the till's receipt record and the
	// audit trail.
	if tenderRes.PaymentID != "" {
		s.tender.RecordPaymentReceived(ctx, tenderRes)
	}
	s.record(ctx, audit.SensitiveAccess{
		Action: audit.ActPaymentAllocated, EntityType: "payment", EntityID: out.PaymentID,
		ActorID: actor, BranchID: pre.BranchID, Purpose: "PAY_BALANCE_TAKEN",
		Extra: map[string]any{"order_id": out.OrderID, "amount_cents": out.AmountCents, "method": out.Method},
	})
	return out, nil
}

// errPayBalanceLostRace marks the same-key concurrent take whose payments row
// another request already wrote: the transaction rolls back having written
// nothing, and the caller answers the winner as a replay.
type errPayBalanceLostRace struct{}

func (errPayBalanceLostRace) Error() string {
	return "pos: this payment's key was taken by a concurrent take"
}

// payBalanceReplayRow is the payments row a replay reads back by key.
type payBalanceReplayRow struct {
	OrderID     string
	PaymentID   string
	Method      string
	AmountCents int64
}

// payBalanceReplay answers a take whose key already took a payment: the
// order's own current facts, the money trail as it stands, and Replayed set,
// having charged nothing and written nothing.
func (s *Service) payBalanceReplay(ctx context.Context, caller identity.StaffContext, in PayBalanceTakeInput, past payBalanceReplayRow) (PayBalanceTaken, error) {
	orderID := past.OrderID
	if orderID == "" {
		// The row predates this door's bookkeeping: resolve through the input.
		orderID = in.OrderID
	}
	facts, found, err := s.store.ReadOwingOrder(ctx, orderID)
	if err != nil {
		return PayBalanceTaken{}, err
	}
	if !found {
		return PayBalanceTaken{}, ErrPayBalanceOrderNotFound
	}
	// The same branch wall the find and the take hold (the review's P3-1): a
	// replay of another branch's order answers the door's 404, never that
	// order's number, customer or money trail.
	if !caller.CanAccessBranch(facts.BranchID) {
		return PayBalanceTaken{}, ErrPayBalanceOrderNotFound
	}
	trail, err := s.store.OrderPaymentsTrail(ctx, orderID)
	if err != nil {
		return PayBalanceTaken{}, err
	}
	var paid int64
	for _, p := range trail {
		paid += p.AmountCents
	}
	owed := facts.balanceCents()
	held := facts.TermsDays <= 0 && owed > 0
	return PayBalanceTaken{
		OrderID: orderID, OrderNo: facts.OrderNo, CustomerID: facts.CustomerID,
		PaymentID: past.PaymentID, Method: past.Method, AmountCents: past.AmountCents,
		PaidWords:         PaidInFullWords,
		ReleasedToPicking: !held,
		StillOwedCents:    owed, HeldFromPickingNow: held,
		Payments: trail, Replayed: true,
	}, nil
}
