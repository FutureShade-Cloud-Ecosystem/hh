package gl

// deposit_cancel_test.go, DB-free unit tests for the deposit cancel posting
// (row 1193): when a special order whose supplier deposit secures it is
// cancelled, the deposit document's liability in 2300 Customer Deposits
// leaves the books under the cancel's disposition. Credit back: DR 2300 /
// CR the customer's receivable, the credit on the account. Kept: DR 2300 /
// CR other income, the dealer keeps the deposit.

import (
	"context"
	"testing"
)

func cancelRules() map[string]map[string]string {
	r := defaultRules()
	r["deposit.cancelled"] = map[string]string{
		"customer_deposit": "2300", "ar": "1100", "other_income": "4950",
	}
	return r
}

// Credit back posts the liability into the customer's receivable for the
// deposit's whole total, balanced, keyed per deposit document.
func TestDepositCancelledCreditBackPostsTheLiabilityIntoAR(t *testing.T) {
	ctx := context.Background()
	f := newFakeStore()
	f.rules = cancelRules()
	svc := newTestService(f).WithInvoiceReader(fakeInvoiceReader{amt: InvoiceAmounts{
		TotalCents: 4000, BranchID: "b1", InvoiceNo: "INV-SD",
		SourceKind: "SUPPLIER_DEPOSIT",
	}})
	if err := svc.consume(ctx, ev("deposit.cancelled", map[string]any{
		"invoice_id": "isd1", "order_id": "o1", "disposition": "CREDIT_BACK",
	})); err != nil {
		t.Fatalf("deposit cancel consume: %v", err)
	}
	d := f.lastInserted()
	wantLeg(t, d, "2300", 4000, 0)
	wantLeg(t, d, "1100", 0, 4000)
	for _, code := range []string{"4000", "4950", "2200"} {
		if _, has := leg(d, code); has {
			t.Fatalf("the credit back must post no %s leg: %v", code, d.Lines)
		}
	}
	if !d.balanced() {
		t.Fatalf("credit back journal unbalanced: dr=%d cr=%d", d.debitTotal(), d.creditTotal())
	}
	if d.IdempotencyKey != "deposit.cancelled:isd1" {
		t.Fatalf("idempotency key = %q, want deposit.cancelled:isd1", d.IdempotencyKey)
	}
	if d.BranchID != "b1" {
		t.Fatalf("cancel journal branch = %q, want the deposit document's b1", d.BranchID)
	}
}

// Kept books only what was PAID into other income (row 1228): the 4000 / 2500
// case, 4950 takes 2500 and the unpaid 1500 is closed against the receivable,
// one entry, balanced, 2300 cleared in full.
func TestDepositCancelledKeptBooksThePaidPartAndClosesTheRest(t *testing.T) {
	ctx := context.Background()
	f := newFakeStore()
	f.rules = cancelRules()
	svc := newTestService(f).WithInvoiceReader(fakeInvoiceReader{amt: InvoiceAmounts{
		TotalCents: 4000, BranchID: "b1", InvoiceNo: "INV-SD",
		SourceKind: "SUPPLIER_DEPOSIT",
	}})
	if err := svc.consume(ctx, ev("deposit.cancelled", map[string]any{
		"invoice_id": "isd1", "order_id": "o1", "disposition": "KEPT",
		"paid_cents": 2500, "closed_cents": 1500,
	})); err != nil {
		t.Fatalf("deposit cancel consume: %v", err)
	}
	d := f.lastInserted()
	wantLeg(t, d, "2300", 4000, 0)
	wantLeg(t, d, "4950", 0, 2500)
	wantLeg(t, d, "1100", 0, 1500)
	if _, has := leg(d, "4000"); has {
		t.Fatalf("the kept booking must post no revenue leg: %v", d.Lines)
	}
	if !d.balanced() {
		t.Fatalf("kept journal unbalanced: dr=%d cr=%d", d.debitTotal(), d.creditTotal())
	}
	if d.IdempotencyKey != "deposit.cancelled:isd1" {
		t.Fatalf("idempotency key = %q, want deposit.cancelled:isd1", d.IdempotencyKey)
	}
}

// Kept on an entirely unpaid deposit books nothing to other income: the whole
// deposit is closed against the receivable (4000 / 0).
func TestDepositCancelledKeptUnpaidBooksNothingToOtherIncome(t *testing.T) {
	ctx := context.Background()
	f := newFakeStore()
	f.rules = cancelRules()
	svc := newTestService(f).WithInvoiceReader(fakeInvoiceReader{amt: InvoiceAmounts{
		TotalCents: 4000, BranchID: "b1", InvoiceNo: "INV-SD", SourceKind: "SUPPLIER_DEPOSIT",
	}})
	if err := svc.consume(ctx, ev("deposit.cancelled", map[string]any{
		"invoice_id": "isd1", "order_id": "o1", "disposition": "KEPT",
		"paid_cents": 0, "closed_cents": 4000,
	})); err != nil {
		t.Fatalf("deposit cancel consume: %v", err)
	}
	d := f.lastInserted()
	wantLeg(t, d, "2300", 4000, 0)
	wantLeg(t, d, "1100", 0, 4000)
	if _, has := leg(d, "4950"); has {
		t.Fatalf("an unpaid kept deposit must book nothing to 4950: %v", d.Lines)
	}
	if !d.balanced() {
		t.Fatalf("kept journal unbalanced: dr=%d cr=%d", d.debitTotal(), d.creditTotal())
	}
}

// A kept event whose amounts do not add up to the document is malformed money
// and refused loudly, never posted.
func TestDepositCancelledKeptRefusesAmountsThatDoNotAddUp(t *testing.T) {
	ctx := context.Background()
	f := newFakeStore()
	f.rules = cancelRules()
	svc := newTestService(f).WithInvoiceReader(fakeInvoiceReader{amt: InvoiceAmounts{
		TotalCents: 4000, BranchID: "b1", SourceKind: "SUPPLIER_DEPOSIT",
	}})
	for _, amts := range []map[string]any{
		{"paid_cents": 2500, "closed_cents": 1000},
		{"paid_cents": -1, "closed_cents": 4001},
		{},
	} {
		p := map[string]any{"invoice_id": "isd1", "order_id": "o1", "disposition": "KEPT"}
		for k, v := range amts {
			p[k] = v
		}
		if err := svc.consume(ctx, ev("deposit.cancelled", p)); err == nil {
			t.Fatalf("amounts %v were accepted", amts)
		}
	}
	if len(f.journals) != 0 {
		t.Fatalf("a refused event posted %d journals", len(f.journals))
	}
}

// A redelivered cancel event posts nothing new: the journal key dedupes, the
// outbox is at least once and the ledger is exactly once, both dispositions
// under the one key.
func TestDepositCancelledRedeliveryPostsNothing(t *testing.T) {
	ctx := context.Background()
	f := newFakeStore()
	f.rules = cancelRules()
	svc := newTestService(f).WithInvoiceReader(fakeInvoiceReader{amt: InvoiceAmounts{
		TotalCents: 4000, BranchID: "b1", SourceKind: "SUPPLIER_DEPOSIT",
	}})
	e := ev("deposit.cancelled", map[string]any{
		"invoice_id": "isd1", "order_id": "o1", "disposition": "CREDIT_BACK",
	})
	if err := svc.consume(ctx, e); err != nil {
		t.Fatalf("first consume: %v", err)
	}
	if err := svc.consume(ctx, e); err != nil {
		t.Fatalf("second consume (replay): %v", err)
	}
	// Even a replay naming the other disposition posts nothing: the document
	// cancels exactly once, whichever disposition the first event carried.
	other := ev("deposit.cancelled", map[string]any{
		"invoice_id": "isd1", "order_id": "o1", "disposition": "KEPT",
		"paid_cents": 0, "closed_cents": 4000,
	})
	if err := svc.consume(ctx, other); err != nil {
		t.Fatalf("replay under the other disposition: %v", err)
	}
	if len(f.journals) != 1 {
		t.Fatalf("a redelivered cancel must post exactly one journal, got %d", len(f.journals))
	}
}

// A cancel naming a document that is not a supplier deposit is refused
// loudly, and an unknown disposition with it: malformed money, never a
// silent no-op.
func TestDepositCancelledRefusesAnOrdinaryInvoiceAndUnknownDispositions(t *testing.T) {
	ctx := context.Background()
	f := newFakeStore()
	f.rules = cancelRules()
	svc := newTestService(f).WithInvoiceReader(fakeInvoiceReader{amt: InvoiceAmounts{
		SubtotalCents: 7300, TaxCents: 949, TotalCents: 8249, BranchID: "b1", InvoiceNo: "INV-REST",
	}})
	if err := svc.consume(ctx, ev("deposit.cancelled", map[string]any{
		"invoice_id": "i2", "order_id": "o1", "disposition": "CREDIT_BACK",
	})); err == nil {
		t.Fatal("a cancel on an ordinary invoice must be refused, not posted")
	}
	if len(f.journals) != 0 {
		t.Fatalf("the refused cancel posted %d journal(s)", len(f.journals))
	}
	// A deposit document under an unknown disposition is refused too.
	svc2 := newTestService(f).WithInvoiceReader(fakeInvoiceReader{amt: InvoiceAmounts{
		TotalCents: 4000, BranchID: "b1", SourceKind: "SUPPLIER_DEPOSIT",
	}})
	if err := svc2.consume(ctx, ev("deposit.cancelled", map[string]any{
		"invoice_id": "isd1", "order_id": "o1", "disposition": "DONATED",
	})); err == nil {
		t.Fatal("an unknown disposition must be refused, not posted")
	}
	if err := svc2.consume(ctx, ev("deposit.cancelled", map[string]any{
		"invoice_id": "isd1", "order_id": "o1",
	})); err == nil {
		t.Fatal("a missing disposition must be refused, not posted")
	}
	if len(f.journals) != 0 {
		t.Fatalf("the refused cancels posted %d journal(s)", len(f.journals))
	}
}

// A deposit document with a zero total books nothing (a nothing deposit is
// no liability), the zero-leg no-op posture.
func TestDepositCancelledZeroTotalPostsNothing(t *testing.T) {
	ctx := context.Background()
	f := newFakeStore()
	f.rules = cancelRules()
	svc := newTestService(f).WithInvoiceReader(fakeInvoiceReader{amt: InvoiceAmounts{
		TotalCents: 0, BranchID: "b1", SourceKind: "SUPPLIER_DEPOSIT",
	}})
	if err := svc.consume(ctx, ev("deposit.cancelled", map[string]any{
		"invoice_id": "isd0", "order_id": "o1", "disposition": "KEPT",
	})); err != nil {
		t.Fatalf("zero deposit consume: %v", err)
	}
	if len(f.journals) != 0 {
		t.Fatalf("a zero deposit must post nothing, got %d journal(s)", len(f.journals))
	}
}
