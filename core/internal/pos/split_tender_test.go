package pos

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
	"github.com/jackc/pgx/v5"
)

// -- DIB-165 split tender -------------------------------------------------------------------------
//
// The harness prices one line at 10000 cents; flat 13% HST makes the sale total 11300. Every split
// below is expressed against that number, so a test that stops adding up is a test that noticed the
// composer changed the total, which is exactly what these are for.
const harnessTotal = 11300

func splitInput(legs ...TenderLegInput) SaleInput {
	in := baseInput()
	in.Tender = "" // a split derives the primary leg; the scalar field is ignored
	in.Tenders = legs
	return in
}

// -- THE GUARD: the legs must settle the sale exactly ---------------------------------------------

// TestSale_SplitTender_SumMustEqualTotal is the named guard test for the split-tender sum rule.
//
// MUTATION TARGET: checkTenderSum's `sum != totalCents` comparison (split_tender.go). Loosen it to
// any slack, abs(sum-total) <= 1, >= instead of !=, a no-op return, and the under-tender and
// over-tender cases below stop erroring. The DB half of the same guard (0100's deferred constraint
// trigger) is mutation-checked separately in split_tender_integration_test.go, because a Go-only
// check would still let a psql session or a future writer produce a sale that does not add up.
func TestSale_SplitTender_SumMustEqualTotal(t *testing.T) {
	cases := []struct {
		name string
		legs []TenderLegInput
		want string // substring the cashier must see
	}{
		{"under-tender by one cent", []TenderLegInput{
			{Method: TenderCash, AmountCents: 4000}, {Method: TenderCard, AmountCents: 7299},
		}, "11299"},
		{"over-tender by one cent", []TenderLegInput{
			{Method: TenderCash, AmountCents: 4000}, {Method: TenderCard, AmountCents: 7301},
		}, "11301"},
		{"wildly short", []TenderLegInput{
			{Method: TenderCash, AmountCents: 100}, {Method: TenderCard, AmountCents: 100},
		}, "200"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness()
			_, err := h.svc.Sale(context.Background(), splitInput(tc.legs...))
			if err == nil {
				t.Fatal("a split whose legs do not sum to the total must be refused")
			}
			// Both numbers reach the cashier, "the tenders do not add up" without saying by how much
			// is a message that sends someone to a calculator.
			if !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "11300") {
				t.Fatalf("error must name the tendered sum AND the sale total, got %q", err.Error())
			}
			// Refused BEFORE the tx: no order, no invoice, and crucially no tender taken. A guard that
			// fires after the first leg is captured has already put money in the drawer for a sale that
			// will not exist.
			if len(h.store.orders) != 0 || len(h.store.invoices) != 0 || h.tender.called {
				t.Fatalf("the sum guard must refuse before any write: orders=%d invoices=%d tenderCalled=%v",
					len(h.store.orders), len(h.store.invoices), h.tender.called)
			}
		})
	}
}

// TestSale_SplitTender_SumRefusalIsAReadableProblemBody pins the refusal's
// problem body (the row 985 item): the typed refusal carries the two
// amounts as integer cents fields, and the sentence speaks dollars, never
// cents. The handler writes the body from these fields (the stable word
// split_total_mismatch beside them); the integration leg at Trenton reads
// the served body end to end.
func TestSale_SplitTender_SumRefusalIsAReadableProblemBody(t *testing.T) {
	err := checkTenderSum([]TenderLeg{
		{Seq: 1, Method: TenderCash, AmountCents: 10000},
		{Seq: 2, Method: TenderCash, AmountCents: 16600},
	}, 30058)
	var sm ErrSplitTotalMismatch
	if !errors.As(err, &sm) {
		t.Fatalf("the sum refusal must be the typed mismatch, got %T (%v)", err, err)
	}
	if sm.TenderedCents != 26600 || sm.TotalCents != 30058 {
		t.Fatalf("the refusal carries both amounts in cents: %+v", sm)
	}
	if got, want := splitMismatchMessage(sm.TenderedCents, sm.TotalCents),
		"The tenders add up to $266.00, but the sale total is $300.58."; got != want {
		t.Fatalf("the message is the dollar sentence:\n  got  %q\n  want %q", got, want)
	}
	if strings.Contains(splitMismatchMessage(sm.TenderedCents, sm.TotalCents), "cents") {
		t.Fatal("no message ever prints cents")
	}
	// The thousands shape a bigger ticket takes.
	if got, want := splitMismatchDollars(126600), "$1,266.00"; got != want {
		t.Fatalf("a four-figure amount keeps its comma: got %q, want %q", got, want)
	}
}

// TestSale_SplitTender_ExactSumCommits is the positive arm of the same guard: the boundary value
// passes. Without it, a mutation that refuses EVERY split would still pass the test above.
func TestSale_SplitTender_ExactSumCommits(t *testing.T) {
	h := newHarness()
	out, err := h.svc.Sale(context.Background(), splitInput(
		TenderLegInput{Method: TenderCash, AmountCents: 4000},
		TenderLegInput{Method: TenderCard, AmountCents: 7300},
	))
	if err != nil {
		t.Fatalf("an exactly-settled split must commit: %v", err)
	}
	if out.TotalCents != harnessTotal {
		t.Fatalf("total = %d, want %d", out.TotalCents, harnessTotal)
	}
}

// -- the settlement shape -------------------------------------------------------------------------

func TestSale_SplitTender_TwoCapturedLegs_ComposesPaidInvoice(t *testing.T) {
	h := newHarness()
	out, err := h.svc.Sale(context.Background(), splitInput(
		TenderLegInput{Method: TenderCash, AmountCents: 4000},
		TenderLegInput{Method: TenderCard, AmountCents: 7300, PaymentToken: "tok_visa"},
	))
	if err != nil {
		t.Fatalf("split sale: %v", err)
	}

	// One tender call per leg, in submission order, each for its own amount.
	if len(h.tender.takes) != 2 {
		t.Fatalf("want 2 TakeTender calls (one per leg), got %d: %+v", len(h.tender.takes), h.tender.takes)
	}
	if h.tender.takes[0].Method != TenderCash || h.tender.takes[0].AmountCents != 4000 {
		t.Fatalf("leg 1 = %s/%d, want CASH/4000", h.tender.takes[0].Method, h.tender.takes[0].AmountCents)
	}
	if h.tender.takes[1].Method != TenderCard || h.tender.takes[1].AmountCents != 7300 {
		t.Fatalf("leg 2 = %s/%d, want CARD/7300", h.tender.takes[1].Method, h.tender.takes[1].AmountCents)
	}
	// The card token rides its OWN leg (a split can carry two cards, so the token cannot be a
	// sale-level field).
	if h.tender.takes[1].PaymentToken != "tok_visa" {
		t.Fatalf("the CARD leg must carry its own token, got %q", h.tender.takes[1].PaymentToken)
	}

	// Distinct payment idempotency keys, the whole reason legs 2..n do not collapse onto leg 1.
	if h.tender.takes[0].IdempotencyKey == h.tender.takes[1].IdempotencyKey {
		t.Fatalf("both legs used idempotency key %q, payments.idempotency_key is UNIQUE, so the second "+
			"leg would replay onto the first row and the drawer would be short", h.tender.takes[0].IdempotencyKey)
	}

	// Fully captured → the invoice books PAID with no balance and AR is untouched.
	if len(h.store.invoices) != 1 || h.store.invoices[0].Status != "PAID" || h.store.invoices[0].BalanceCents != 0 {
		t.Fatalf("fully-captured split must book a PAID, zero-balance invoice: %+v", h.store.invoices)
	}
	// One allocation per captured leg, summing to the total.
	if len(h.store.allocations) != 2 {
		t.Fatalf("want one allocation per leg, got %d: %+v", len(h.store.allocations), h.store.allocations)
	}
	var allocated int64
	for _, a := range h.store.allocations {
		allocated += a.amount
	}
	if allocated != harnessTotal {
		t.Fatalf("allocations sum to %d, want the sale total %d", allocated, harnessTotal)
	}
	// The subledger follows the journal (Ruling 128 P1-3): INVOICE (+total) then one PAYMENT (-leg) per capture, netting to 0.
	if len(h.store.custTxns) != 3 || h.store.custTxns[0].TxnType != "INVOICE" || h.store.custTxns[1].TxnType != "PAYMENT" || h.store.custTxns[2].TxnType != "PAYMENT" {
		t.Fatalf("a fully-captured split must book INVOICE then two PAYMENT rows: %+v", h.store.custTxns)
	}
	// One payment.received per leg, the GL sees N ordinary tenders and needs no new posting rule.
	received := 0
	for _, e := range h.store.events {
		if e == "payment.received" {
			received++
		}
	}
	if received != 2 {
		t.Fatalf("want one payment.received per captured leg, got %d (%v)", received, h.store.events)
	}

	// The persisted legs.
	if len(h.store.saleTenders) != 2 {
		t.Fatalf("want 2 pos_sale_tenders rows, got %d", len(h.store.saleTenders))
	}
	if h.store.saleTenders[0].Seq != 1 || h.store.saleTenders[1].Seq != 2 {
		t.Fatalf("legs must carry submission order: %+v", h.store.saleTenders)
	}
	if !h.store.posSales[0].SplitTender {
		t.Fatal("pos_sale.split_tender must be TRUE so a reader knows to look for the legs")
	}
	// Primary = largest leg. 7300 (CARD) beats 4000 (CASH), so every existing reader of
	// pos_sale.tender sees CARD.
	if out.Tender != TenderCard || h.store.posSales[0].Tender != TenderCard {
		t.Fatalf("primary tender = %s / %s, want CARD (the largest leg)", out.Tender, h.store.posSales[0].Tender)
	}
	if !out.SplitTender || len(out.Tenders) != 2 {
		t.Fatalf("the response must carry the legs for the receipt: %+v", out)
	}
	// A post-commit receipt record per captured leg (the drawer trail shows two tenders, not one).
	if len(h.tender.receiptRecords) != 2 {
		t.Fatalf("want one post-commit PAYMENT_RECEIVED per leg, got %d", len(h.tender.receiptRecords))
	}
}

func TestSale_SplitTender_AccountLeg_IsPartiallyPaidAndCreditGatedOnTheRemainder(t *testing.T) {
	h := newHarness()
	h.store.customers["cust-abc"] = customerRec{ID: "cust-abc", CustomerType: "CONTRACTOR",
		OpenBalanceCents: 50_000, CreditLimitCents: 500_000, PaymentTermsDays: 30}

	out, err := h.svc.Sale(context.Background(), splitInput(
		TenderLegInput{Method: TenderCash, AmountCents: 3300},
		TenderLegInput{Method: TenderAccount, AmountCents: 8000},
	))
	if err != nil {
		t.Fatalf("split with account leg: %v", err)
	}

	// Only the CASH leg is captured; the account leg takes no tender.
	if len(h.tender.takes) != 1 || h.tender.takes[0].Method != TenderCash {
		t.Fatalf("the ACCOUNT leg must capture nothing: %+v", h.tender.takes)
	}
	// The credit gate sees the UNCAPTURED remainder, not the whole sale, a customer with $80 of
	// headroom can still split a $113 sale.
	if h.credit.lastAmount != 8000 {
		t.Fatalf("credit gate exposure = %d, want the account leg's 8000 (not the sale total)", h.credit.lastAmount)
	}
	// Invoice is PARTIALLY_PAID with the account leg as its balance.
	inv := h.store.invoices[0]
	if inv.Status != "PARTIALLY_PAID" || inv.BalanceCents != 8000 {
		t.Fatalf("invoice = %s balance %d, want PARTIALLY_PAID/8000", inv.Status, inv.BalanceCents)
	}
	// AR subledger: INVOICE for the full total, then the capture nets it down to open + account leg.
	if len(h.store.custTxns) != 2 {
		t.Fatalf("want an INVOICE then a PAYMENT row, got %+v", h.store.custTxns)
	}
	if h.store.custTxns[0].TxnType != "INVOICE" || h.store.custTxns[0].BalanceAfter != 50_000+harnessTotal {
		t.Fatalf("INVOICE row = %+v, want balance_after %d", h.store.custTxns[0], 50_000+harnessTotal)
	}
	if h.store.custTxns[1].TxnType != "PAYMENT" || h.store.custTxns[1].BalanceAfter != 50_000+8000 {
		t.Fatalf("PAYMENT row = %+v, want the running balance to land at open+account = %d",
			h.store.custTxns[1], 50_000+8000)
	}
	// Primary = the ACCOUNT leg (8000 > 3300): the largest slice, reported honestly.
	if out.Tender != TenderAccount {
		t.Fatalf("primary tender = %s, want ACCOUNT (the largest leg)", out.Tender)
	}
	// pos_sale.payment_id stays empty because the primary leg captured nothing, not the cash leg's
	// payment, which would misattribute the sale's headline tender.
	if out.PaymentID != "" {
		t.Fatalf("payment_id = %q, want empty (the primary leg is ACCOUNT)", out.PaymentID)
	}
	// The account leg is persisted with no payment id.
	var acct *saleTenderRow
	for i := range h.store.saleTenders {
		if h.store.saleTenders[i].Method == TenderAccount {
			acct = &h.store.saleTenders[i]
		}
	}
	if acct == nil || acct.PaymentID != "" {
		t.Fatalf("the ACCOUNT leg must persist with no payment: %+v", h.store.saleTenders)
	}
}

// -- shape refusals -------------------------------------------------------------------------------

func TestSale_SplitTender_ShapeRefusals(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*SaleInput)
		want error
	}{
		{"a single leg is not a split", func(in *SaleInput) {
			in.Tenders = []TenderLegInput{{Method: TenderCash, AmountCents: harnessTotal}}
		}, errSplitTooFewLegs},
		{"unknown method", func(in *SaleInput) {
			in.Tenders = []TenderLegInput{{Method: "BITCOIN", AmountCents: 4000}, {Method: TenderCash, AmountCents: 7300}}
		}, errSplitBadMethod},
		{"zero-amount leg", func(in *SaleInput) {
			in.Tenders = []TenderLegInput{{Method: TenderCash, AmountCents: 0}, {Method: TenderCard, AmountCents: harnessTotal}}
		}, errSplitBadAmount},
		{"negative leg", func(in *SaleInput) {
			in.Tenders = []TenderLegInput{{Method: TenderCash, AmountCents: -100}, {Method: TenderCard, AmountCents: harnessTotal + 100}}
		}, errSplitBadAmount},
		{"two account legs post the remainder to AR twice", func(in *SaleInput) {
			in.Tenders = []TenderLegInput{
				{Method: TenderAccount, AmountCents: 4000},
				{Method: TenderAccount, AmountCents: 4000},
				{Method: TenderCash, AmountCents: 3300},
			}
		}, errSplitTwoAccounts},
		{"a split plus a deposit is two answers to one question", func(in *SaleInput) {
			in.Tenders = []TenderLegInput{{Method: TenderCash, AmountCents: 4000}, {Method: TenderCard, AmountCents: 7300}}
			in.DepositCents = 1000
			in.DepositMethod = TenderCash
		}, errSplitWithDeposit},
		{"COD is unpaid at ring and cannot be split", func(in *SaleInput) {
			in.Tenders = []TenderLegInput{{Method: TenderCash, AmountCents: 4000}, {Method: TenderCard, AmountCents: 7300}}
			in.SaleType = SaleCOD
		}, errSplitWithCOD},
		{"more legs than any counter needs", func(in *SaleInput) {
			for i := 0; i < 7; i++ {
				in.Tenders = append(in.Tenders, TenderLegInput{Method: TenderCash, AmountCents: 100})
			}
		}, errSplitTooManyLegs},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness()
			in := baseInput()
			in.Tender = ""
			tc.mut(&in)
			_, err := h.svc.Sale(context.Background(), in)
			if err == nil {
				t.Fatal("want a refusal")
			}
			if err.Error() != tc.want.Error() {
				t.Fatalf("err = %q, want %q", err.Error(), tc.want.Error())
			}
			if len(h.store.orders) != 0 || h.tender.called {
				t.Fatal("a shape refusal must open no tx and take no tender")
			}
		})
	}
}

// -- offline + rollback ---------------------------------------------------------------------------

func TestSale_SplitTender_CardLegBlocksOffline(t *testing.T) {
	h := newHarness()
	in := splitInput(
		TenderLegInput{Method: TenderCash, AmountCents: 7300},
		TenderLegInput{Method: TenderCard, AmountCents: 4000}, // NOT the primary leg
	)
	in.Offline = true
	_, err := h.svc.Sale(context.Background(), in)
	if err == nil || err.Error() != errCardOffline.Error() {
		t.Fatalf("a CARD leg must block an offline ring even when it is not the primary tender, got %v", err)
	}

	// And a split with no card leg stays offline-capable, cash and cheque are first-class offline.
	h2 := newHarness()
	in2 := splitInput(
		TenderLegInput{Method: TenderCash, AmountCents: 7300},
		TenderLegInput{Method: TenderCheque, AmountCents: 4000},
	)
	in2.Offline = true
	if _, err := h2.svc.Sale(context.Background(), in2); err != nil {
		t.Fatalf("a cash+cheque split must still ring offline: %v", err)
	}
}

func TestSale_SplitTender_DeclinedCardLegNeverOpensTheTransaction(t *testing.T) {
	h := newHarness()
	// The card leg is declined at its CAPTURE, before the transaction opens: the cash leg was
	// never taken either, nothing persists, and the ring answers the decline word.
	h.tender.captureErr = fmt.Errorf("%w: gateway refused", till.ErrCardDeclined)
	_, err := h.svc.Sale(context.Background(), splitInput(
		TenderLegInput{Method: TenderCash, AmountCents: 4000},
		TenderLegInput{Method: TenderCard, AmountCents: 7300},
	))
	if !errors.Is(err, ErrCardChargeCardDeclined) {
		t.Fatalf("a declined card leg must read as the decline answer, got %v", err)
	}
	if len(h.store.orders) != 0 || len(h.store.invoices) != 0 || len(h.store.saleTenders) != 0 {
		t.Fatalf("a declined leg must leave nothing behind: orders=%d invoices=%d legs=%d",
			len(h.store.orders), len(h.store.invoices), len(h.store.saleTenders))
	}
	if len(h.tender.takes) != 0 {
		t.Fatalf("a declined capture must take no tender at all; takes=%d", len(h.tender.takes))
	}
}

// A failing leg INSIDE the transaction (the bind failing, the drawer refusing) still rolls the
// whole split back, and under the capture seam its captured card leg is compensated before the
// ring answers: the split-decline machine's other half.
func TestSale_SplitTender_TxFailingLegRollsBackAndReversesTheCapture(t *testing.T) {
	h := newHarness()
	h.tender.err = nil
	declineOnSecond := &declineNthTender{inner: h.tender, n: 2}
	h.svc.tender = declineOnSecond

	_, err := h.svc.Sale(context.Background(), splitInput(
		TenderLegInput{Method: TenderCash, AmountCents: 4000},
		TenderLegInput{Method: TenderCard, AmountCents: 7300},
	))
	if !errors.Is(err, ErrSaleFailedCardReversed) {
		t.Fatalf("a failing leg after the capture must answer the reversed word, got %v", err)
	}
	if len(h.store.orders) != 0 || len(h.store.invoices) != 0 || len(h.store.saleTenders) != 0 {
		t.Fatalf("a failing leg must roll the whole split back: orders=%d invoices=%d legs=%d",
			len(h.store.orders), len(h.store.invoices), len(h.store.saleTenders))
	}
	if len(h.tender.receiptRecords) != 0 {
		t.Fatal("a rolled-back split must record no PAYMENT_RECEIVED for the leg that did succeed")
	}
	// The card leg's capture was reversed on the way out.
	if len(h.tender.compCalls) != 1 {
		t.Fatalf("the captured card leg must be compensated once, got %d compensation calls", len(h.tender.compCalls))
	}
}

// declineNthTender wraps the fake till and declines the nth TakeTender call, so the rollback test
// exercises a partially-captured split rather than an all-or-nothing one.
type declineNthTender struct {
	inner *fakeTender
	n     int
	calls int
}

func (d *declineNthTender) TakeTender(ctx context.Context, tx pgx.Tx, in till.TenderInput) (till.TenderResult, error) {
	d.calls++
	if d.calls == d.n {
		return till.TenderResult{}, fmt.Errorf("%w: gateway refused", till.ErrCardDeclined)
	}
	return d.inner.TakeTender(ctx, tx, in)
}

func (d *declineNthTender) RefundTender(ctx context.Context, tx pgx.Tx, in till.RefundInput) (till.RefundResult, error) {
	return d.inner.RefundTender(ctx, tx, in)
}
func (d *declineNthTender) CaptureCard(ctx context.Context, in till.CardChargeInput) (till.CardCapture, error) {
	return d.inner.CaptureCard(ctx, in)
}
func (d *declineNthTender) CompensateCard(ctx context.Context, attemptID string) (till.CardCompensation, error) {
	return d.inner.CompensateCard(ctx, attemptID)
}
func (d *declineNthTender) RecordPaymentReceived(ctx context.Context, res till.TenderResult) {
	d.inner.RecordPaymentReceived(ctx, res)
}
func (d *declineNthTender) GetSession(ctx context.Context, id string) (till.SessionDetail, error) {
	return d.inner.GetSession(ctx, id)
}

// -- derived keys ---------------------------------------------------------------------------------

func TestLegIdempotencyKey_DeterministicAndDistinct(t *testing.T) {
	const saleKey = "11111111-1111-4111-8111-111111111111"
	k1, k2 := legIdempotencyKey(saleKey, 1), legIdempotencyKey(saleKey, 2)
	if k1 == k2 {
		t.Fatal("legs must not share a payment idempotency key")
	}
	// Deterministic: the SAME sale replayed after a network timeout must reuse each leg's key so the
	// till replays onto the existing payments rows instead of double-charging.
	if legIdempotencyKey(saleKey, 1) != k1 {
		t.Fatal("leg keys must be deterministic across calls")
	}
	if k1 == saleKey {
		t.Fatal("a leg key must differ from the sale key (which the pos_sale row already occupies)")
	}
	// Different sales never collide.
	if legIdempotencyKey("22222222-2222-4222-8222-222222222222", 1) == k1 {
		t.Fatal("leg keys must be scoped to their sale")
	}
}

func TestPrimaryLeg_LargestWinsTiesGoToSubmissionOrder(t *testing.T) {
	cases := []struct {
		name string
		legs []TenderLeg
		want string
	}{
		{"largest wins", []TenderLeg{
			{Seq: 1, Method: TenderCash, AmountCents: 100},
			{Seq: 2, Method: TenderCard, AmountCents: 900},
		}, TenderCard},
		{"tie goes to the first submitted", []TenderLeg{
			{Seq: 1, Method: TenderCheque, AmountCents: 500},
			{Seq: 2, Method: TenderCash, AmountCents: 500},
		}, TenderCheque},
		{"account can be primary", []TenderLeg{
			{Seq: 1, Method: TenderCash, AmountCents: 100},
			{Seq: 2, Method: TenderAccount, AmountCents: 900},
		}, TenderAccount},
	}
	for _, tc := range cases {
		if got := primaryLeg(tc.legs); got != tc.want {
			t.Fatalf("%s: primaryLeg = %s, want %s", tc.name, got, tc.want)
		}
	}
}

// -- the single-tender path is untouched ----------------------------------------------------------

// TestSale_SingleTender_WritesNoSplitRows pins the behavior freeze: generalizing the composer onto a
// capture LIST must leave an ordinary sale bit-identical, writing no legs and flagging nothing.
func TestSale_SingleTender_WritesNoSplitRows(t *testing.T) {
	h := newHarness()
	out, err := h.svc.Sale(context.Background(), baseInput())
	if err != nil {
		t.Fatalf("sale: %v", err)
	}
	if len(h.store.saleTenders) != 0 {
		t.Fatalf("a single-tender sale must write no pos_sale_tenders rows, got %+v", h.store.saleTenders)
	}
	if out.SplitTender || len(out.Tenders) != 0 || h.store.posSales[0].SplitTender {
		t.Fatalf("a single-tender sale must not be flagged as split: %+v", out)
	}
}

// -- past-sale lookup: the branch fence -----------------------------------------------------------

func TestLookupSales_BranchFence(t *testing.T) {
	scoped := identity.StaffContext{UserID: "u-trt", RoleCode: "COUNTER_SALES", Tier: identity.TierFrontline,
		Scope: identity.ScopeBranch, Branches: map[string]bool{"branch-trt": true}}
	global := identity.StaffContext{UserID: "u-glob", RoleCode: "SUPER_ADMIN", Tier: identity.TierLeadership,
		Scope: identity.ScopeGlobal}

	t.Run("a scoped caller may not sweep every branch", func(t *testing.T) {
		h := newHarness()
		h.caller = scoped
		_, err := h.svc.LookupSales(context.Background(), SaleLookupQuery{})
		if err == nil || err.Error() != errLookupForbidden.Error() {
			t.Fatalf("an unnamed branch from a scoped caller must be 403, got %v", err)
		}
	})
	t.Run("a scoped caller may not search another branch", func(t *testing.T) {
		h := newHarness()
		h.caller = scoped
		_, err := h.svc.LookupSales(context.Background(), SaleLookupQuery{BranchID: "branch-kgn"})
		if err == nil || err.Error() != errLookupForbidden.Error() {
			t.Fatalf("an unreachable branch must be 403, got %v", err)
		}
	})
	t.Run("a scoped caller searches its own branch", func(t *testing.T) {
		h := newHarness()
		h.caller = scoped
		if _, err := h.svc.LookupSales(context.Background(), SaleLookupQuery{BranchID: "branch-trt"}); err != nil {
			t.Fatalf("own branch must be allowed: %v", err)
		}
		// The fence reaches the store as a real filter, not just an authorization decision, an
		// allowed-but-unfiltered query would return the other branch's rows anyway.
		if h.store.lookupQuery.BranchID != "branch-trt" {
			t.Fatalf("the store must receive the branch filter, got %+v", h.store.lookupQuery)
		}
	})
	t.Run("a GLOBAL caller may omit the branch", func(t *testing.T) {
		h := newHarness()
		h.caller = global
		if _, err := h.svc.LookupSales(context.Background(), SaleLookupQuery{}); err != nil {
			t.Fatalf("a GLOBAL caller may search every branch: %v", err)
		}
	})
	t.Run("the limit is clamped so no caller pages the whole history", func(t *testing.T) {
		h := newHarness()
		h.caller = global
		if _, err := h.svc.LookupSales(context.Background(), SaleLookupQuery{Limit: 100_000}); err != nil {
			t.Fatalf("lookup: %v", err)
		}
		if h.store.lookupQuery.Limit != maxSaleLookupLimit {
			t.Fatalf("limit = %d, want it clamped to %d", h.store.lookupQuery.Limit, maxSaleLookupLimit)
		}
	})
}

// TestGetSale_BranchFence covers the IDOR the past-sale search made reachable: the reopen read was
// never branch-fenced, so any staff caller with a sale id could read another branch's counter
// history. MUTATION TARGET: the CanAccessBranch check in Service.GetSale.
func TestGetSale_BranchFence(t *testing.T) {
	const saleID = "aaaaaaaa-0000-4000-8000-000000000001"
	newSvc := func(caller identity.StaffContext) *harness {
		h := newHarness()
		h.caller = caller
		h.store.views[saleID] = SaleView{OrderID: "order-1", BranchID: "branch-kgn", TotalCents: 11300}
		return h
	}

	h := newSvc(identity.StaffContext{UserID: "u-trt", Scope: identity.ScopeBranch,
		Branches: map[string]bool{"branch-trt": true}})
	_, err := h.svc.GetSale(context.Background(), saleID)
	if err == nil {
		t.Fatal("a scoped caller must not reopen another branch's sale")
	}
	if st, ok := ClientStatus(err); !ok || st != 403 {
		t.Fatalf("want 403 for an existing sale in an unreachable branch, got status=%d ok=%v (%v)", st, ok, err)
	}

	// Its own branch still reads.
	h2 := newSvc(identity.StaffContext{UserID: "u-kgn", Scope: identity.ScopeBranch,
		Branches: map[string]bool{"branch-kgn": true}})
	if _, err := h2.svc.GetSale(context.Background(), saleID); err != nil {
		t.Fatalf("own branch reopen must succeed: %v", err)
	}

	// A MISSING sale stays 404 for everyone, the fence must not turn "no such sale" into "forbidden",
	// which would tell a prober that ids they cannot reach exist.
	h3 := newSvc(identity.StaffContext{Scope: identity.ScopeGlobal, UserID: "u-glob"})
	_, err = h3.svc.GetSale(context.Background(), "bbbbbbbb-0000-4000-8000-000000000009")
	if st, ok := ClientStatus(err); !ok || st != 404 {
		t.Fatalf("a missing sale must stay 404, got status=%d (%v)", st, err)
	}
}
