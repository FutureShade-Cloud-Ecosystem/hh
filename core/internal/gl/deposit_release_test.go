package gl

// deposit_release_test.go, DB-free unit tests for the deposit release posting
// (the item after 9.5): when the special order a supplier deposit secures is
// fulfilled, the deposit document's liability in 2300 Customer Deposits
// releases into revenue, DR 2300 / CR 4000, no AR and no tax legs (the
// deposit document booked no tax when it finalised; the sale's own invoice
// booked the sale's whole tax on the rest).

import (
	"context"
	"testing"
)

// The release posts the liability into revenue for the deposit's whole total,
// balanced, keyed per deposit document so a redelivered event cannot post twice.
func TestDepositReleasedPostsTheLiabilityIntoRevenue(t *testing.T) {
	ctx := context.Background()
	f := newFakeStore()
	svc := newTestService(f).WithInvoiceReader(fakeInvoiceReader{amt: InvoiceAmounts{
		SubtotalCents: 4000, TaxCents: 0, TotalCents: 4000, BranchID: "b1", InvoiceNo: "INV-SD",
		SourceKind: "SUPPLIER_DEPOSIT",
	}})
	if err := svc.consume(ctx, ev("deposit.released", map[string]any{"invoice_id": "isd1"})); err != nil {
		t.Fatalf("deposit release consume: %v", err)
	}
	d := f.lastInserted()
	wantLeg(t, d, "2300", 4000, 0)
	wantLeg(t, d, "4000", 0, 4000)
	for _, code := range []string{"1100", "2200"} {
		if _, has := leg(d, code); has {
			t.Fatalf("the release must post no %s leg: %v", code, d.Lines)
		}
	}
	if !d.balanced() {
		t.Fatalf("release journal unbalanced: dr=%d cr=%d", d.debitTotal(), d.creditTotal())
	}
	if d.IdempotencyKey != "deposit.released:isd1" {
		t.Fatalf("idempotency key = %q, want deposit.released:isd1", d.IdempotencyKey)
	}
	if d.BranchID != "b1" {
		t.Fatalf("release journal branch = %q, want the deposit document's b1", d.BranchID)
	}
}

// A redelivered release event posts nothing new: the journal key dedupes, the
// outbox is at least once and the ledger is exactly once.
func TestDepositReleasedRedeliveryPostsNothing(t *testing.T) {
	ctx := context.Background()
	f := newFakeStore()
	svc := newTestService(f).WithInvoiceReader(fakeInvoiceReader{amt: InvoiceAmounts{
		SubtotalCents: 4000, TotalCents: 4000, BranchID: "b1", InvoiceNo: "INV-SD",
		SourceKind: "SUPPLIER_DEPOSIT",
	}})
	e := ev("deposit.released", map[string]any{"invoice_id": "isd1"})
	if err := svc.consume(ctx, e); err != nil {
		t.Fatalf("first consume: %v", err)
	}
	if err := svc.consume(ctx, e); err != nil {
		t.Fatalf("second consume (replay): %v", err)
	}
	if len(f.journals) != 1 {
		t.Fatalf("a redelivered release must post exactly one journal, got %d", len(f.journals))
	}
}

// A release naming a document that is not a supplier deposit is refused
// loudly: the event is malformed money, never a silent no-op.
func TestDepositReleasedRefusesAnOrdinaryInvoice(t *testing.T) {
	ctx := context.Background()
	f := newFakeStore()
	svc := newTestService(f).WithInvoiceReader(fakeInvoiceReader{amt: InvoiceAmounts{
		SubtotalCents: 7300, TaxCents: 949, TotalCents: 8249, BranchID: "b1", InvoiceNo: "INV-REST",
	}})
	if err := svc.consume(ctx, ev("deposit.released", map[string]any{"invoice_id": "i2"})); err == nil {
		t.Fatal("a release on an ordinary invoice must be refused, not posted")
	}
	if len(f.journals) != 0 {
		t.Fatalf("the refused release posted %d journal(s)", len(f.journals))
	}
}

// A deposit document with a zero total books nothing (a nothing deposit is no
// liability), the zero-leg no-op posture the other builders use.
func TestDepositReleasedZeroTotalPostsNothing(t *testing.T) {
	ctx := context.Background()
	f := newFakeStore()
	svc := newTestService(f).WithInvoiceReader(fakeInvoiceReader{amt: InvoiceAmounts{
		TotalCents: 0, BranchID: "b1", SourceKind: "SUPPLIER_DEPOSIT",
	}})
	if err := svc.consume(ctx, ev("deposit.released", map[string]any{"invoice_id": "isd0"})); err != nil {
		t.Fatalf("zero deposit consume: %v", err)
	}
	if len(f.journals) != 0 {
		t.Fatalf("a zero deposit must post nothing, got %d journal(s)", len(f.journals))
	}
}
