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
// own facts, its customer's terms, and its open invoices with their balances,
// taken in the lock order the file header names.
type payBalanceOrderFacts struct {
	OrderID      string
	OrderNo      string
	CustomerID   string
	CustomerName string
	BranchID     string
	Fulfilment   string
	TermsDays    int
	TotalCents   int64
	Invoices     []payBalanceInvoice
}

// payBalanceInvoice is one open invoice of the order, locked FOR NO KEY UPDATE.
type payBalanceInvoice struct {
	InvoiceID    string
	InvoiceNo    string
	BalanceCents int64
}

// balanceCents is the whole balance: the sum of the open invoice balances.
func (f payBalanceOrderFacts) balanceCents() int64 {
	var n int64
	for _, inv := range f.Invoices {
		n += inv.BalanceCents
	}
	return n
}

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
// order is simply not found.
func (s *Service) PayBalanceFind(ctx context.Context, term string) ([]OwingOrder, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return nil, err
	}
	term = strings.TrimSpace(term)
	if term == "" {
		return nil, clientErr(http.StatusBadRequest, "pos: say which order, or whose name or phone")
	}
	out, err := s.store.FindOwingOrders(ctx, term, callerReachBranches(caller), 20)
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = []OwingOrder{}
	}
	for i := range out {
		if out[i].HeldFromPicking {
			out[i].HoldWords = pickHoldSentence
		}
	}
	return out, nil
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
		return s.payBalanceReplay(ctx, in, past)
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
	if pre.balanceCents() == 0 {
		return PayBalanceTaken{}, ErrPayBalanceAlreadyPaid
	}
	if in.AmountCents != pre.balanceCents() {
		return PayBalanceTaken{}, ErrPayBalancePartAmount{SentCents: in.AmountCents, BalanceCents: pre.balanceCents()}
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
			return clientErr(http.StatusConflict, "%s", terr.Error())
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
		// The hold's own reading, from the facts this transaction just moved:
		// the whole balance settled, nothing is owed, the hold (if the shape
		// ever carried one) lifts with this commit.
		trail, terr2 := s.store.OrderPaymentsTrailTx(ctx, tx, facts.OrderID)
		if terr2 != nil {
			return terr2
		}
		out = PayBalanceTaken{
			OrderID: facts.OrderID, OrderNo: facts.OrderNo, CustomerID: facts.CustomerID,
			PaymentID: res.PaymentID, Method: in.Method, AmountCents: balance,
			PaidWords:          PaidInFullWords,
			ReleasedToPicking:  true,
			StillOwedCents:     0,
			HeldFromPickingNow: false,
			Payments:           trail,
		}
		return nil
	})
	if txErr != nil {
		if errors.As(txErr, &errPayBalanceLostRace{}) {
			past := payBalanceReplayRow{OrderID: in.OrderID, PaymentID: tenderRes.PaymentID,
				Method: in.Method, AmountCents: in.AmountCents}
			return s.payBalanceReplay(ctx, in, past)
		}
		if len(ring.captured) > 0 {
			ring.rank = cardRankReversed
			if _, serr := s.settleFailedCardRing(ctx, ring); serr != nil {
				return PayBalanceTaken{}, serr
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
func (s *Service) payBalanceReplay(ctx context.Context, in PayBalanceTakeInput, past payBalanceReplayRow) (PayBalanceTaken, error) {
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
