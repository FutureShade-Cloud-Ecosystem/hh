package gl

// consumer_test.go, DB-free unit tests for the transactional-outbox posting consumer + boot validation.
// Covers A-01 AC2 (idempotency), AC3 (posting matrix + PaymentReader 1010/1020 leg choice + invoice legs),
// AC7 (FN 13/8/5 split), AC8 (boot posting-rule validation), AC14 (till over/short journal).

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
)

func ev(topic string, payload any) notify.Event {
	b, _ := json.Marshal(payload)
	return notify.Event{Topic: topic, Payload: b, EventID: "evt-1"}
}

func leg(d *journalDraft, code string) (draftLine, bool) {
	for _, l := range d.Lines {
		if l.AccountCode == code {
			return l, true
		}
	}
	return draftLine{}, false
}

func wantLeg(t *testing.T, d *journalDraft, code string, debit, credit int64) {
	t.Helper()
	l, ok := leg(d, code)
	if !ok {
		t.Fatalf("expected a leg on account %s; lines=%v", code, d.Lines)
	}
	if l.DebitCents != debit || l.CreditCents != credit {
		t.Fatalf("account %s: want dr=%d cr=%d, got dr=%d cr=%d", code, debit, credit, l.DebitCents, l.CreditCents)
	}
}

// AC2, a redelivered outbox event posts exactly one journal (idempotent on the deterministic key).
func TestAC2_OutboxIdempotency(t *testing.T) {
	ctx := context.Background()
	f := newFakeStore()
	svc := newTestService(f).WithPaymentReader(fakePaymentReader{facts: PaymentFacts{Method: "CASH", Status: "RECEIVED", AmountCents: 5000}})

	e := ev("payment.received", map[string]any{"payment_id": "p1", "method": "CASH"})
	if err := svc.consume(ctx, e); err != nil {
		t.Fatalf("first consume: %v", err)
	}
	if err := svc.consume(ctx, e); err != nil {
		t.Fatalf("second consume (replay): %v", err)
	}
	if len(f.journals) != 1 {
		t.Fatalf("a redelivered event must post exactly one journal, got %d", len(f.journals))
	}
}

// AC3, the posting matrix + the PaymentReader-driven 1010/1020 leg choice, plus the invoice-finalized legs.
func TestAC3_PostingMatrix(t *testing.T) {
	ctx := context.Background()

	// Cash receipt → DR 1020 Undeposited Funds / CR 1100 AR (1020 doctrine).
	fCash := newFakeStore()
	cashSvc := newTestService(fCash).WithPaymentReader(fakePaymentReader{facts: PaymentFacts{BranchID: "b1", Method: "CASH", AmountCents: 5000}})
	if err := cashSvc.consume(ctx, ev("payment.received", map[string]any{"payment_id": "p1", "method": "CASH"})); err != nil {
		t.Fatalf("cash consume: %v", err)
	}
	d := fCash.lastInserted()
	wantLeg(t, d, "1020", 5000, 0)
	wantLeg(t, d, "1100", 0, 5000)

	// Card receipt → DR 1010 Card Settlement Clearing / CR 1100 AR.
	fCard := newFakeStore()
	cardSvc := newTestService(fCard).WithPaymentReader(fakePaymentReader{facts: PaymentFacts{BranchID: "b1", Method: "CARD", AmountCents: 8000}})
	if err := cardSvc.consume(ctx, ev("payment.received", map[string]any{"payment_id": "p2", "method": "CARD"})); err != nil {
		t.Fatalf("card consume: %v", err)
	}
	dc := fCard.lastInserted()
	wantLeg(t, dc, "1010", 8000, 0)
	wantLeg(t, dc, "1100", 0, 8000)
	if dc.BranchID != "b1" {
		t.Fatalf("payment journal must be branch-stamped from PaymentReader facts, got %q", dc.BranchID)
	}

	// Invoice finalized → DR 1100 (total−holdback) / DR 1150 holdback / CR 4000 subtotal / CR 2200 tax.
	fInv := newFakeStore()
	invSvc := newTestService(fInv).WithInvoiceReader(fakeInvoiceReader{amt: InvoiceAmounts{
		SubtotalCents: 10000, TaxCents: 1300, TotalCents: 11300, HoldbackCents: 1000, BranchID: "b1", InvoiceNo: "INV-1",
	}})
	if err := invSvc.consume(ctx, ev("ar.invoice.finalized", map[string]any{"invoice_id": "i1"})); err != nil {
		t.Fatalf("invoice consume: %v", err)
	}
	di := fInv.lastInserted()
	wantLeg(t, di, "1100", 10300, 0) // total 11300 − holdback 1000
	wantLeg(t, di, "1150", 1000, 0)
	wantLeg(t, di, "4000", 0, 10000)
	wantLeg(t, di, "2200", 0, 1300)
	if !di.balanced() {
		t.Fatalf("invoice journal unbalanced: dr=%d cr=%d", di.debitTotal(), di.creditTotal())
	}
}

// Sub item 9.5: a supplier deposit document (source_kind SUPPLIER_DEPOSIT,
// the invoice a supplier's demanded deposit bills to a credit account) posts
// the LIABILITY, not revenue: DR 1100 AR / CR 2300 Customer Deposits for its
// whole total, and no revenue or tax legs (the goods it secures have not
// booked; their own invoice posts its own revenue).
func TestSupplierDepositInvoicePostsToCustomerDeposits(t *testing.T) {
	ctx := context.Background()
	f := newFakeStore()
	svc := newTestService(f).WithInvoiceReader(fakeInvoiceReader{amt: InvoiceAmounts{
		SubtotalCents: 4000, TaxCents: 0, TotalCents: 4000, BranchID: "b1", InvoiceNo: "INV-SD",
		SourceKind: "SUPPLIER_DEPOSIT",
	}})
	if err := svc.consume(ctx, ev("ar.invoice.finalized", map[string]any{"invoice_id": "isd1"})); err != nil {
		t.Fatalf("deposit invoice consume: %v", err)
	}
	d := f.lastInserted()
	wantLeg(t, d, "1100", 4000, 0)
	wantLeg(t, d, "2300", 0, 4000)
	if _, has := leg(d, "4000"); has {
		t.Fatalf("the deposit document must post no revenue leg: %v", d.Lines)
	}
	if _, has := leg(d, "2200"); has {
		t.Fatalf("the deposit document must post no tax leg: %v", d.Lines)
	}
	if !d.balanced() {
		t.Fatalf("deposit journal unbalanced: dr=%d cr=%d", d.debitTotal(), d.creditTotal())
	}

	// The ordinary invoice beside it still posts revenue and tax: the marker,
	// not the event, decides.
	f2 := newFakeStore()
	plain := newTestService(f2).WithInvoiceReader(fakeInvoiceReader{amt: InvoiceAmounts{
		SubtotalCents: 7300, TaxCents: 949, TotalCents: 8249, BranchID: "b1", InvoiceNo: "INV-REST",
	}})
	if err := plain.consume(ctx, ev("ar.invoice.finalized", map[string]any{"invoice_id": "i2"})); err != nil {
		t.Fatalf("rest invoice consume: %v", err)
	}
	d2 := f2.lastInserted()
	wantLeg(t, d2, "4000", 0, 7300)
	wantLeg(t, d2, "2200", 0, 949)
}

// AC7, the FN 13/8/5 split: invoice.finalized CREDITED 2200 the full 13%; the relief REDUCES that liability
// to the 5% net federal by DEBITING 2200 for the 8% ON portion, offsetting the FN relief recoverable (1165):
// DR 2200 8% / CR 1165 8%. A standard-rate sale (found=false) books nothing.
func TestAC7_FNReliefSplit(t *testing.T) {
	ctx := context.Background()

	f := newFakeStore()
	svc := newTestService(f).WithTaxSplitReader(fakeTaxSplit{full: 1300, onCredit: 800, net: 500, found: true})
	if err := svc.consume(ctx, ev("tax.relief.assessed", map[string]any{"order_id": "o1"})); err != nil {
		t.Fatalf("relief consume: %v", err)
	}
	d := f.lastInserted()
	wantLeg(t, d, "2200", 800, 0) // DEBIT HST payable by the 8% ON portion → 13% nets to 5%
	wantLeg(t, d, "1165", 0, 800) // offset the FN relief recoverable
	if !d.balanced() {
		t.Fatal("relief journal unbalanced")
	}

	// Standard-rate sale → no relief journal.
	f2 := newFakeStore()
	svc2 := newTestService(f2).WithTaxSplitReader(fakeTaxSplit{found: false})
	if err := svc2.consume(ctx, ev("tax.relief.assessed", map[string]any{"order_id": "o2"})); err != nil {
		t.Fatalf("no-relief consume: %v", err)
	}
	if len(f2.journals) != 0 {
		t.Fatalf("a standard-rate sale must book no relief journal, got %d", len(f2.journals))
	}
}

// NSF bounce legs, the reversal-credit leg is picked from method AND status, and a taxable fee splits the
// gross into 4200 income + 2200 HST rather than booking $0 tax.
func TestNSFBounceLegs(t *testing.T) {
	ctx := context.Background()

	// Un-deposited CARD bounce → reverse 1010 (card clearing), NOT 1020. AR reinstated for amount+fee.
	fCard := newFakeStore()
	cardSvc := newTestService(fCard)
	if err := cardSvc.consume(ctx, ev("payment.bounced", map[string]any{
		"payment_id": "p1", "branch_id": "b1", "method": "CARD", "amount_cents": 10000, "fee_cents": 4500, "cleared": false,
	})); err != nil {
		t.Fatalf("card bounce consume: %v", err)
	}
	d := fCard.lastInserted()
	wantLeg(t, d, "1100", 14500, 0) // AR reinstated: reversal 10000 + fee 4500
	wantLeg(t, d, "1010", 0, 10000) // reverse the still-clearing card receipt (not 1020)
	wantLeg(t, d, "4200", 0, 4500)  // fee income (non-taxable)
	if !d.balanced() {
		t.Fatalf("card bounce journal unbalanced: dr=%d cr=%d", d.debitTotal(), d.creditTotal())
	}

	// DEPOSITED (cleared) CARD bounce → reverse 1000 bank.
	fBank := newFakeStore()
	if err := newTestService(fBank).consume(ctx, ev("payment.bounced", map[string]any{
		"payment_id": "p2", "branch_id": "b1", "method": "CARD", "amount_cents": 8000, "cleared": true,
	})); err != nil {
		t.Fatalf("cleared bounce consume: %v", err)
	}
	wantLeg(t, fBank.lastInserted(), "1000", 0, 8000)

	// Cash bounce (undeposited) → reverse 1020.
	fCash := newFakeStore()
	if err := newTestService(fCash).consume(ctx, ev("payment.bounced", map[string]any{
		"payment_id": "p3", "branch_id": "b1", "method": "CASH", "amount_cents": 5000, "cleared": false,
	})); err != nil {
		t.Fatalf("cash bounce consume: %v", err)
	}
	wantLeg(t, fCash.lastInserted(), "1020", 0, 5000)

	// Taxable fee → the gross 4500 splits into net income (4200) + embedded 13% HST (2200).
	fTax := newFakeStore()
	if err := newTestService(fTax).consume(ctx, ev("payment.bounced", map[string]any{
		"payment_id": "p4", "branch_id": "b1", "method": "CHEQUE", "amount_cents": 6000, "fee_cents": 4500, "fee_taxable": true,
	})); err != nil {
		t.Fatalf("taxable fee consume: %v", err)
	}
	dt := fTax.lastInserted()
	wantLeg(t, dt, "1100", 10500, 0) // AR: 6000 reversal + 4500 gross fee
	wantLeg(t, dt, "1020", 0, 6000)  // cheque reverses undeposited
	wantLeg(t, dt, "4200", 0, 3982)  // net fee income = 4500·10000/11300
	wantLeg(t, dt, "2200", 0, 518)   // embedded HST = 4500 − 3982
	if !dt.balanced() {
		t.Fatalf("taxable fee journal unbalanced: dr=%d cr=%d", dt.debitTotal(), dt.creditTotal())
	}
}

// AC8, boot posting-rule validation flags a rule that resolves to a missing/inactive account and passes a
// complete, healthy rule set.
func TestAC8_ValidatePostingRules(t *testing.T) {
	ctx := context.Background()
	f := newFakeStore()
	f.activeRules = healthyRules()
	svc := newTestService(f)
	if err := svc.ValidatePostingRules(ctx); err != nil {
		t.Fatalf("healthy rule set should validate, got %v", err)
	}

	// Break one rule → an inactive/missing account.
	broken := healthyRules()
	broken[0].AccountOK = false
	f.activeRules = broken
	if err := svc.ValidatePostingRules(ctx); err == nil {
		t.Fatal("a dangling posting rule must fail validation")
	}

	// Drop a required role → validation must catch the gap.
	missing := healthyRules()[:len(healthyRules())-1] // drop the last (order.fulfilled/inventory)
	f.activeRules = missing
	if err := svc.ValidatePostingRules(ctx); err == nil {
		t.Fatal("a missing required posting role must fail validation")
	}
}

func healthyRules() []RuleRef {
	set := map[string][]string{
		"invoice.finalized":      {"ar", "revenue", "tax"},
		"payment.received":       {"cash", "ar", "card_clearing"},
		"tax.relief.assessed":    {"hst_full", "fn_relief"},
		"payment.bounced":        {"ar", "bank", "undeposited", "card_clearing", "fee_income", "hst"},
		"payment.reversed":       {"ar", "refund_payable"},
		"payment.reapplied":      {"ar", "refund_payable"},
		"till.session.completed": {"over_short", "undeposited"},
		"order.fulfilled":        {"cogs", "inventory"},
		"pos.return.committed":   {"revenue", "hst", "undeposited", "card_clearing", "ar", "fee_revenue"},
		"order.returned":         {"inventory", "cogs"},
		"deposit.released":       {"customer_deposit", "revenue"},
		"deposit.cancelled":      {"customer_deposit", "ar", "other_income"},
		"pos.return.written_off": {"expense", "cogs"},
	}
	var out []RuleRef
	for et, roles := range set {
		for _, r := range roles {
			out = append(out, RuleRef{EventType: et, LineRole: r, AccountCode: "x", AccountOK: true})
		}
	}
	return out
}

// AC14, the till over/short journal: a short drawer expenses to 6350; an over drawer credits 6350.
func TestAC14_TillOverShort(t *testing.T) {
	ctx := context.Background()

	// Short $25 → DR 6350 / CR 1020.
	fShort := newFakeStore()
	shortSvc := newTestService(fShort)
	if err := shortSvc.consume(ctx, ev("till.session.completed", map[string]any{"session_id": "s1", "branch_id": "b1", "over_short_cents": -2500})); err != nil {
		t.Fatalf("short consume: %v", err)
	}
	ds := fShort.lastInserted()
	wantLeg(t, ds, "6350", 2500, 0)
	wantLeg(t, ds, "1020", 0, 2500)

	// Over $15 → DR 1020 / CR 6350.
	fOver := newFakeStore()
	overSvc := newTestService(fOver)
	if err := overSvc.consume(ctx, ev("till.session.completed", map[string]any{"session_id": "s2", "branch_id": "b1", "over_short_cents": 1500})); err != nil {
		t.Fatalf("over consume: %v", err)
	}
	do := fOver.lastInserted()
	wantLeg(t, do, "1020", 1500, 0)
	wantLeg(t, do, "6350", 0, 1500)

	// Balanced drawer → no journal.
	fZero := newFakeStore()
	zeroSvc := newTestService(fZero)
	if err := zeroSvc.consume(ctx, ev("till.session.completed", map[string]any{"session_id": "s3", "over_short_cents": 0})); err != nil {
		t.Fatalf("zero consume: %v", err)
	}
	if len(fZero.journals) != 0 {
		t.Fatalf("a balanced drawer must book nothing, got %d journals", len(fZero.journals))
	}
}

// SF-2d round 4 (ruled): every payment.reversed is Dr AR / Cr 2310 refund_payable, the full amount, whatever the
// tender, the session, the register or the deposit status; one journal however often the event is delivered; and the
// account never depends on a status the payload or the payment reader carries.
func TestPaymentReversedLegs(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name, method, status string
		payload              map[string]any
	}{
		{"cheque undeposited", "CHEQUE", "RECEIVED", map[string]any{}},
		{"card in clearing", "CARD", "RECEIVED", map[string]any{}},
		{"card deposited", "CARD", "DEPOSITED", map[string]any{"original_cleared": true, "original_status": "DEPOSITED"}},
		{"cheque deposited", "CHEQUE", "DEPOSITED", map[string]any{"original_cleared": true}},
		{"cash", "CASH", "RECEIVED", map[string]any{"original_cleared": false}},
	}
	for _, c := range cases {
		f := newFakeStore()
		svc := newTestService(f).WithPaymentReader(fakePaymentReader{facts: PaymentFacts{BranchID: "b1", Method: c.method, Status: c.status, AmountCents: 12000}})
		payload := map[string]any{"payment_id": "rev1", "reverses_payment_id": "orig1", "amount_cents": 12000, "method": c.method, "branch_id": "b1"}
		for k, v := range c.payload {
			payload[k] = v
		}
		for i := 0; i < 2; i++ { // delivered twice
			if err := svc.consume(ctx, ev("payment.reversed", payload)); err != nil {
				t.Fatalf("%s: consume: %v", c.name, err)
			}
		}
		d := f.lastInserted()
		wantLeg(t, d, "1100", 12000, 0)
		wantLeg(t, d, "2310", 0, 12000)
		if !d.balanced() {
			t.Fatalf("%s: unbalanced", c.name)
		}
		if d.IdempotencyKey != "payment.reversed:rev1" {
			t.Fatalf("%s: key %q", c.name, d.IdempotencyKey)
		}
	}
	// An old payload (no branch, no status facts) posts the same way: the account never read the status.
	fOld := newFakeStore()
	svcOld := newTestService(fOld).WithPaymentReader(fakePaymentReader{facts: PaymentFacts{BranchID: "b1", Method: "CARD", Status: "DEPOSITED", AmountCents: 500}})
	if err := svcOld.consume(ctx, ev("payment.reversed", map[string]any{"payment_id": "rev9", "reverses_payment_id": "orig9", "amount_cents": 500})); err != nil {
		t.Fatalf("old payload: %v", err)
	}
	wantLeg(t, fOld.lastInserted(), "2310", 0, 500)
	// A complete payload needs no payment reader at all.
	fNo := newFakeStore()
	if err := newTestService(fNo).consume(ctx, ev("payment.reversed", map[string]any{"payment_id": "rev8", "reverses_payment_id": "orig8", "amount_cents": 700, "method": "CHEQUE", "branch_id": "b1"})); err != nil {
		t.Fatalf("a complete payload with no reader: %v", err)
	}
	wantLeg(t, fNo.lastInserted(), "2310", 0, 700)
	// A malformed event is an error, never a silent ack.
	svc := newTestService(newFakeStore()).WithPaymentReader(fakePaymentReader{})
	if err := svc.consume(ctx, ev("payment.reversed", map[string]any{"payment_id": "rev1"})); err == nil {
		t.Fatal("a reversal naming no original must be refused")
	}
}

// The topic is posted by the consumer and its two roles are required at boot.
func TestPaymentReversed_IsAPostedTopicWithRequiredRules(t *testing.T) {
	found := false
	for _, topic := range PostedTopics {
		if topic == "payment.reversed" {
			found = true
		}
	}
	if !found {
		t.Fatal("payment.reversed must be a topic the posting consumer owns")
	}
	for _, role := range []string{"ar", "refund_payable"} {
		f := newFakeStore()
		svc := newTestService(f)
		var without []RuleRef
		for _, r := range healthyRules() {
			if r.EventType == "payment.reversed" && r.LineRole == role {
				continue
			}
			without = append(without, r)
		}
		f.activeRules = without
		if err := svc.ValidatePostingRules(context.Background()); err == nil {
			t.Fatalf("a missing payment.reversed %s rule must fail the boot validation", role)
		}
	}
}

// SF-2l (ruled 49.3 and 52): a re-application of the money a reversal holds is Dr 2310 refund_payable / Cr AR for the
// total, one journal per request however often the event is delivered, on the branch the payload carries.
func TestPaymentReappliedLegs(t *testing.T) {
	ctx := context.Background()
	f := newFakeStore()
	svc := newTestService(f)
	payload := map[string]any{"batch_id": "batch1", "payment_id": "orig1", "amount_cents": 9000, "method": "CHEQUE", "branch_id": "b1"}
	for i := 0; i < 2; i++ { // delivered twice
		if err := svc.consume(ctx, ev("payment.reapplied", payload)); err != nil {
			t.Fatalf("consume: %v", err)
		}
	}
	d := f.lastInserted()
	wantLeg(t, d, "2310", 9000, 0)
	wantLeg(t, d, "1100", 0, 9000)
	if !d.balanced() {
		t.Fatal("unbalanced")
	}
	if d.IdempotencyKey != "payment.reapplied:batch1" || d.BranchID != "b1" || d.SourceRefID != "orig1" {
		t.Fatalf("key %q branch %q source ref %q", d.IdempotencyKey, d.BranchID, d.SourceRefID)
	}
	if len(f.journals) != 1 {
		t.Fatalf("delivered twice, %d journals", len(f.journals))
	}
	for _, bad := range []map[string]any{
		{"payment_id": "orig1", "amount_cents": 100},
		{"batch_id": "b", "amount_cents": 100},
		{"batch_id": "b", "payment_id": "orig1"},
		{"batch_id": "b", "payment_id": "orig1", "amount_cents": -5},
	} {
		if err := newTestService(newFakeStore()).consume(ctx, ev("payment.reapplied", bad)); err == nil {
			t.Fatalf("a malformed re-application %v was acked", bad)
		}
	}
}

// The topic is posted by the consumer and its two roles are required at boot.
func TestPaymentReapplied_IsAPostedTopicWithRequiredRules(t *testing.T) {
	found := false
	for _, topic := range PostedTopics {
		if topic == "payment.reapplied" {
			found = true
		}
	}
	if !found {
		t.Fatal("payment.reapplied must be a topic the posting consumer owns")
	}
	for _, role := range []string{"ar", "refund_payable"} {
		f := newFakeStore()
		svc := newTestService(f)
		var without []RuleRef
		for _, r := range healthyRules() {
			if r.EventType == "payment.reapplied" && r.LineRole == role {
				continue
			}
			without = append(without, r)
		}
		f.activeRules = without
		if err := svc.ValidatePostingRules(context.Background()); err == nil {
			t.Fatalf("a missing payment.reapplied/%s rule must fail validation", role)
		}
	}
}
