package pos

// pay_balance_test.go, item 9.4 (the row 1047 deposits work): Pay balance at
// the counter, the unit legs over the fake store. The SQL (the find's match,
// the fence, the settle's moves) is proven against the real database in
// pay_balance_integration_test.go.

import (
	"context"
	"errors"
	"strings"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
	"github.com/jackc/pgx/v5"
)

// -- the fake store's Pay balance state --------------------------------------

func (f *fakeStore) FindOwingOrders(_ context.Context, term string, branchIDs []string, limit int) ([]OwingOrder, error) {
	f.payFindTerm = term
	f.payFindBranches = branchIDs
	f.payFindLimit = limit
	return f.owingOrders, nil
}

func (f *fakeStore) ReadOwingOrder(_ context.Context, orderID string) (payBalanceOrderFacts, bool, error) {
	facts, ok := f.owing[orderID]
	return facts, ok, nil
}

func (f *fakeStore) FindPayBalancePaymentByKey(_ context.Context, key string) (payBalanceReplayRow, bool, error) {
	row, ok := f.payByKey[key]
	return row, ok, nil
}

func (f *fakeStore) LockPayBalanceOrderTx(_ context.Context, _ pgx.Tx, orderID string) (payBalanceOrderFacts, error) {
	f.payLocks = append(f.payLocks, orderID)
	facts, ok := f.owing[orderID]
	if !ok {
		return payBalanceOrderFacts{}, ErrPayBalanceOrderNotFound
	}
	return facts, nil
}

func (f *fakeStore) SettleInvoiceTx(_ context.Context, _ pgx.Tx, s settleInvoice) error {
	f.settled = append(f.settled, s)
	f.payTrail = append(f.payTrail, OwingPayment{PaymentID: s.PaymentID, Method: s.Method, AmountCents: s.AmountCents})
	return nil
}

func (f *fakeStore) OrderPaymentsTrailTx(_ context.Context, _ pgx.Tx, _ string) ([]OwingPayment, error) {
	return f.payTrail, nil
}

func (f *fakeStore) OrderPaymentsTrail(_ context.Context, _ string) ([]OwingPayment, error) {
	return f.payTrail, nil
}

func (f *fakeStore) PayBalanceHoldTx(_ context.Context, _ pgx.Tx, orderID string) (payBalanceHoldView, error) {
	if f.holdView == nil {
		return payBalanceHoldView{}, nil
	}
	return f.holdView[orderID], nil
}

// -- the tests ----------------------------------------------------------------

// owingFacts builds one order's facts: one open invoice carrying the balance.
func owingFacts(orderID, customerID, branchID string, terms int, balance int64) payBalanceOrderFacts {
	return payBalanceOrderFacts{
		OrderID: orderID, OrderNo: "#SO-1", CustomerID: customerID, CustomerName: "A customer",
		BranchID: branchID, Fulfilment: "WILL_CALL", TermsDays: terms, TotalCents: 11300,
		// Fully invoiced: the whole total is billed, so the balance is the
		// open invoice alone and no remainder rides beside it.
		InvoicedCents: 11300,
		Invoices:      []payBalanceInvoice{{InvoiceID: "inv-1", InvoiceNo: "INV-1", BalanceCents: balance}},
	}
}

func takeInput() PayBalanceTakeInput {
	return PayBalanceTakeInput{
		OrderID: "ord-owing", Method: TenderCash, AmountCents: 9300,
		TillSessionID: "till-1", IdempotencyKey: "take-key-1",
	}
}

func payBalanceHarness() *harness {
	h := newHarness()
	h.store.owing = map[string]payBalanceOrderFacts{
		"ord-owing": owingFacts("ord-owing", "cust-abc", "branch-trt", 0, 9300),
	}
	return h
}

// The whole balance by cash: one tender at the drawer, the invoice settled,
// the receipt's words and the release, the money trail.
func TestPayBalanceTake_CashTakesTheWholeBalance(t *testing.T) {
	h := payBalanceHarness()
	out, err := h.svc.PayBalanceTake(context.Background(), takeInput())
	if err != nil {
		t.Fatalf("take: %v", err)
	}
	if len(h.tender.takes) != 1 || h.tender.takes[0].AmountCents != 9300 || h.tender.takes[0].Method != TenderCash {
		t.Fatalf("the drawer's tender: %+v", h.tender.takes)
	}
	if len(h.store.settled) != 1 || h.store.settled[0].AmountCents != 9300 || h.store.settled[0].InvoiceID != "inv-1" {
		t.Fatalf("the settle: %+v", h.store.settled)
	}
	if out.PaidWords != PaidInFullWords || !out.ReleasedToPicking || out.StillOwedCents != 0 || out.Replayed {
		t.Fatalf("the answer: %+v", out)
	}
	if len(out.Payments) != 1 || out.Payments[0].AmountCents != 9300 || out.Payments[0].Method != TenderCash {
		t.Fatalf("the money trail: %+v", out.Payments)
	}
}

// The card rides the card charge rail: one leg, captured before the
// transaction, bound to the payments row the tender writes.
func TestPayBalanceTake_CardRidesTheChargeRail(t *testing.T) {
	h := payBalanceHarness()
	h.tender.gatewayRef = "rp_take_1"
	in := takeInput()
	in.Method = TenderCard
	in.PaymentToken = "tok-x"
	out, err := h.svc.PayBalanceTake(context.Background(), in)
	if err != nil {
		t.Fatalf("take: %v", err)
	}
	if len(h.tender.captureCalls) != 1 || h.tender.captureCalls[0].AmountCents != 9300 {
		t.Fatalf("the card leg: %+v", h.tender.captureCalls)
	}
	if h.tender.captureCalls[0].LegKey == takeInput().IdempotencyKey {
		t.Fatalf("the leg key must be derived from the take's key, not be it")
	}
	if len(h.tender.takes) != 1 || h.tender.takes[0].Capture.AttemptID == "" {
		t.Fatalf("the tender did not bind the capture: %+v", h.tender.takes)
	}
	if out.Method != TenderCard {
		t.Fatalf("the answer's method: %s", out.Method)
	}
}

// A declined card writes nothing: no settle, no tender kept.
func TestPayBalanceTake_CardDeclined_WritesNothing(t *testing.T) {
	h := payBalanceHarness()
	h.tender.captureErr = till.ErrCardDeclined
	in := takeInput()
	in.Method = TenderCard
	in.PaymentToken = "tok-x"
	_, err := h.svc.PayBalanceTake(context.Background(), in)
	if !errors.Is(err, ErrCardChargeCardDeclined) {
		t.Fatalf("want the decline answer, got %v", err)
	}
	if len(h.store.settled) != 0 || len(h.tender.takes) != 0 {
		t.Fatalf("a declined take wrote rows: %+v", h.store.settled)
	}
}

// A part amount is refused in words, before anything is charged.
func TestPayBalanceTake_PartAmountRefused(t *testing.T) {
	h := payBalanceHarness()
	in := takeInput()
	in.AmountCents = 5000
	_, err := h.svc.PayBalanceTake(context.Background(), in)
	var e ErrPayBalancePartAmount
	if !errors.As(err, &e) || e.SentCents != 5000 || e.BalanceCents != 9300 {
		t.Fatalf("want ErrPayBalancePartAmount(5000, 9300), got %v", err)
	}
	if msg := payBalancePartMessage(e); msg == "" || containsCentsWord(msg) {
		t.Fatalf("the refusal's message must speak dollars: %q", msg)
	}
	if len(h.tender.takes) != 0 || len(h.store.settled) != 0 {
		t.Fatalf("a refused take wrote rows")
	}
}

// An already paid order is refused in words.
func TestPayBalanceTake_AlreadyPaidRefused(t *testing.T) {
	h := payBalanceHarness()
	h.store.owing["ord-paid"] = owingFacts("ord-paid", "cust-abc", "branch-trt", 0, 0)
	in := takeInput()
	in.OrderID = "ord-paid"
	in.AmountCents = 100
	if _, err := h.svc.PayBalanceTake(context.Background(), in); !errors.Is(err, ErrPayBalanceAlreadyPaid) {
		t.Fatalf("want ErrPayBalanceAlreadyPaid, got %v", err)
	}
}

// A missing order and an order in an unreachable branch are the same 404.
func TestPayBalanceTake_NotFoundAndBranchFenced(t *testing.T) {
	h := payBalanceHarness()
	in := takeInput()
	in.OrderID = "ord-missing"
	if _, err := h.svc.PayBalanceTake(context.Background(), in); !errors.Is(err, ErrPayBalanceOrderNotFound) {
		t.Fatalf("a missing order must 404, got %v", err)
	}
	// A branch scoped caller cannot see another branch's order.
	h.caller.Scope = identity.ScopeBranch // no branch in its reach
	in.OrderID = "ord-owing"
	if _, err := h.svc.PayBalanceTake(context.Background(), in); !errors.Is(err, ErrPayBalanceOrderNotFound) {
		t.Fatalf("a fenced order must 404, got %v", err)
	}
}

// A replay answers the first result and charges nothing.
func TestPayBalanceTake_ReplayChargesNothing(t *testing.T) {
	h := payBalanceHarness()
	// The payments row carries the DERIVED leg key (payments.idempotency_key
	// is the tender's own key, the ring's split-leg derivation).
	h.store.payByKey[legIdempotencyKey("take-key-1", 1)] = payBalanceReplayRow{
		OrderID: "ord-owing", PaymentID: "pay-first", Method: TenderCash, AmountCents: 9300,
	}
	out, err := h.svc.PayBalanceTake(context.Background(), takeInput())
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !out.Replayed || out.PaymentID != "pay-first" || out.PaidWords != PaidInFullWords {
		t.Fatalf("the replay's answer: %+v", out)
	}
	if len(h.tender.captureCalls) != 0 || len(h.tender.takes) != 0 || len(h.store.settled) != 0 {
		t.Fatalf("a replay charged or wrote: %+v", h.store.settled)
	}
}

// The find answers the rows with the hold's words, and the fence is the
// caller's reach.
func TestPayBalanceFind_AnswersRowsWithTheHoldWords(t *testing.T) {
	h := newHarness()
	h.store.owingOrders = []OwingOrder{{
		OrderID: "ord-owing", OrderNo: "#SO-1", CustomerID: "cust-abc", CustomerName: "A customer",
		Fulfilment: "WILL_CALL", OrderTotalCents: 11300, PaidCents: 2000, StillOwedCents: 9300,
		HeldFromPicking: true, Payments: []OwingPayment{{PaymentID: "pay-1", Method: TenderCash, AmountCents: 2000}},
	}}
	out, err := h.svc.PayBalanceFind(context.Background(), " A customer ")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if len(out) != 1 || out[0].HoldWords != pickHoldSentence {
		t.Fatalf("the find's row: %+v", out)
	}
	if h.store.payFindTerm != "A customer" {
		t.Fatalf("the term was not trimmed: %q", h.store.payFindTerm)
	}
	if h.store.payFindBranches != nil {
		t.Fatalf("a global caller must not filter branches: %v", h.store.payFindBranches)
	}
	// An empty term is a bad request, not a whole ledger.
	if _, err := h.svc.PayBalanceFind(context.Background(), "  "); err == nil {
		t.Fatalf("an empty term must be refused")
	}
}

func containsCentsWord(msg string) bool { return strings.Contains(msg, "cents") }

// -- the uninvoiced remainder (the review's P2-2 legs, unit) -----------------

// fakePayInvoicer records the raise the take asks for and answers it.
type fakePayInvoicer struct {
	err   error
	calls []string
}

func (f *fakePayInvoicer) RaiseOrderInvoiceTx(_ context.Context, _ pgx.Tx, orderID string) (RaisedOrderInvoice, error) {
	f.calls = append(f.calls, orderID)
	if f.err != nil {
		return RaisedOrderInvoice{}, f.err
	}
	return RaisedOrderInvoice{InvoiceID: "inv-raised", InvoiceNo: "INV-RAISED", TotalCents: 11300, BalanceCents: 11300}, nil
}

// owingFacts with no invoices and nothing invoiced: the order owes its whole
// total as the uninvoiced remainder, the number the hold reads.
func remainderFacts(orderID string) payBalanceOrderFacts {
	f := owingFacts(orderID, "cust-abc", "branch-trt", 0, 0)
	f.Invoices = nil
	f.InvoicedCents = 0
	return f
}

// The take on an order that owes only its uninvoiced remainder raises the
// invoice inside its own transaction (the desk's writer) and settles it: one
// tender for the whole total, one settle on the raised document.
func TestPayBalanceTake_UninvoicedRemainder_IsRaisedAndSettled(t *testing.T) {
	h := payBalanceHarness()
	h.store.owing["ord-owing"] = remainderFacts("ord-owing")
	h.store.holdView = map[string]payBalanceHoldView{"ord-owing": {OwedCents: 0, Held: false}}
	inv := &fakePayInvoicer{}
	h.svc = h.svc.WithPayBalanceInvoicer(inv)
	in := takeInput()
	in.AmountCents = 11300
	out, err := h.svc.PayBalanceTake(context.Background(), in)
	if err != nil {
		t.Fatalf("take: %v", err)
	}
	if len(inv.calls) != 1 || inv.calls[0] != "ord-owing" {
		t.Fatalf("the raise: %v", inv.calls)
	}
	if len(h.tender.takes) != 1 || h.tender.takes[0].AmountCents != 11300 {
		t.Fatalf("the drawer's tender: %+v", h.tender.takes)
	}
	if len(h.store.settled) != 1 || h.store.settled[0].InvoiceID != "inv-raised" || h.store.settled[0].AmountCents != 11300 {
		t.Fatalf("the settle: %+v", h.store.settled)
	}
	// The answer's hold figures are the hold's own read inside the transaction,
	// never hard coded: a clear read releases, an owed read does not.
	if !out.ReleasedToPicking || out.StillOwedCents != 0 || out.HeldFromPickingNow {
		t.Fatalf("the answer's hold figures: %+v", out)
	}
	h.store.holdView["ord-owing"] = payBalanceHoldView{OwedCents: 1, Held: true}
	again, err := h.svc.PayBalanceTake(context.Background(), in)
	if err != nil {
		t.Fatalf("take with a held read: %v", err)
	}
	if again.ReleasedToPicking || again.StillOwedCents != 1 || !again.HeldFromPickingNow {
		t.Fatalf("the answer follows the hold's read, not a constant: %+v", again)
	}
}

// A remainder the door cannot bill (an order partly invoiced from its
// shipments) refuses in words with nothing taken; an unwired invoicer seam
// refuses too, never a take that stranding the remainder.
func TestPayBalanceTake_UninvoicedRemainder_RefusedWhenItCannotBeBilled(t *testing.T) {
	h := payBalanceHarness()
	h.store.owing["ord-owing"] = remainderFacts("ord-owing")
	inv := &fakePayInvoicer{err: ErrPayBalanceRestUnbillable}
	h.svc = h.svc.WithPayBalanceInvoicer(inv)
	in := takeInput()
	in.AmountCents = 11300
	if _, err := h.svc.PayBalanceTake(context.Background(), in); !errors.Is(err, ErrPayBalanceRestUnbillable) {
		t.Fatalf("want ErrPayBalanceRestUnbillable, got %v", err)
	}
	if len(h.tender.takes) != 0 || len(h.store.settled) != 0 {
		t.Fatalf("a refused raise took money anyway: %+v %+v", h.tender.takes, h.store.settled)
	}
	bare := payBalanceHarness()
	bare.store.owing["ord-owing"] = remainderFacts("ord-owing")
	if _, err := bare.svc.PayBalanceTake(context.Background(), in); !errors.Is(err, ErrPayBalanceInvoicerUnwired) {
		t.Fatalf("want ErrPayBalanceInvoicerUnwired, got %v", err)
	}
	if len(bare.tender.takes) != 0 {
		t.Fatalf("an unwired raise took money anyway: %+v", bare.tender.takes)
	}
}

// The take checks the drawer BEFORE any capture (the review's P2-3, the
// ring's step 5b): a session that is not open, and a session belonging to
// another branch, refuse in the ring's own 409 words with no card charged.
func TestPayBalanceTake_ChecksTheDrawerBeforeAnyCapture(t *testing.T) {
	cases := []struct {
		name string
		set  func(h *harness)
	}{
		{"a closed session", func(h *harness) { h.tender.sessionStatus = "CLOSED" }},
		{"an unreachable session", func(h *harness) { h.tender.sessionErr = errors.New("till: not found") }},
		{"another branch's session", func(h *harness) { h.tender.sessionBranch = "branch-else" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := payBalanceHarness()
			tc.set(h)
			in := takeInput()
			in.Method = TenderCard
			in.PaymentToken = "tok-x"
			if _, err := h.svc.PayBalanceTake(context.Background(), in); err == nil {
				t.Fatalf("the take succeeded on %s", tc.name)
			}
			if len(h.tender.captureCalls) != 0 {
				t.Fatalf("a card was charged before the drawer was checked: %+v", h.tender.captureCalls)
			}
			if len(h.tender.takes) != 0 {
				t.Fatalf("money moved before the drawer was checked: %+v", h.tender.takes)
			}
		})
	}
}

// The replay is fenced and order bound (the review's P3-1): a key that
// already took a payment on ANOTHER order (a ring sale's leg key, say) is
// refused rather than answered as this order's replay, and a replay whose
// order sits in a branch the caller does not reach answers the same 404 the
// find and the take do.
func TestPayBalanceTake_ReplayIsFencedAndOrderBound(t *testing.T) {
	h := payBalanceHarness()
	h.store.payByKey[legIdempotencyKey("take-key-1", 1)] = payBalanceReplayRow{
		OrderID: "ord-elsewhere", PaymentID: "pay-9", Method: TenderCash, AmountCents: 700,
	}
	if _, err := h.svc.PayBalanceTake(context.Background(), takeInput()); err == nil {
		t.Fatal("a key used on another order's payment answered this order's take as a replay")
	} else if !strings.Contains(err.Error(), "another order") {
		t.Fatalf("the cross order replay refusal: %v", err)
	}

	fenced := payBalanceHarness()
	fenced.caller = identity.StaffContext{UserID: "u-harness", RoleCode: "CSR",
		Tier: identity.TierManager, Scope: identity.ScopeBranch, Branches: map[string]bool{"branch-trt": true}}
	fenced.store.owing["ord-elsewhere"] = owingFacts("ord-elsewhere", "cust-abc", "branch-else", 0, 9300)
	elsewhere := takeInput()
	elsewhere.OrderID = "ord-elsewhere"
	fenced.store.payByKey[legIdempotencyKey(elsewhere.IdempotencyKey, 1)] = payBalanceReplayRow{
		OrderID: "ord-elsewhere", PaymentID: "pay-9", Method: TenderCash, AmountCents: 9300,
	}
	if _, err := fenced.svc.PayBalanceTake(context.Background(), elsewhere); !errors.Is(err, ErrPayBalanceOrderNotFound) {
		t.Fatalf("a replay on another branch's order must answer the door's 404, got %v", err)
	}
}
