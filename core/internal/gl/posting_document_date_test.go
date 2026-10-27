package gl

// posting_document_date_test.go, FW1-01b's unit coverage that EVERY builder dates its journal by the
// document it books and not by the processing day. The three integration legs
// (fw1_01b_integration_test.go) prove it end to end on the real database for the receipt, the till session
// and the return; this file covers every other builder on the DB-free fakes, so no builder is left dated
// by the clock by accident.
//
// The date reaches a builder one of two ways (the contract in posting.go's header): through the reader
// seam the builder already uses, when the document's date is a column of the row that seam reads, or
// through the event payload's "occurred_at", which the module that owns the document stamps on it.

import (
	"context"
	"testing"
	"time"
)

// fw1bDocDate is the document's own date in this file: the last day of a month, far from the service
// clock, so a journal dated by the clock cannot pass for one dated by the document.
var fw1bDocDate = time.Date(2087, 1, 31, 18, 0, 0, 0, time.UTC)

// fw1bEarlierDate is an EARLIER document's date: the deposit's finalization, a month before the
// cancel and before the delivery. A builder that dated its journal by it would fail the legs.
var fw1bEarlierDate = time.Date(2086, 12, 15, 12, 0, 0, 0, time.UTC)

// fw1bClockDate is the processing instant: the day after the document's day, which is what the clock
// says while the consumer reads the outbox.
var fw1bClockDate = time.Date(2087, 2, 1, 5, 0, 0, 0, time.UTC)

// fakeDelivery answers one fixed delivery stamp: the fact MarkDelivered records on an order, which
// is the document the deposit release dates its journal by (612 round 2, S2's P1-2).
type fakeDelivery struct{ at time.Time }

func (f fakeDelivery) DeliveredAt(_ context.Context, _ string) (time.Time, bool, error) {
	return f.at, true, nil
}

// TestFW1_01B_EveryBuilderTakesItsDocumentsDate is the whole set in one table: every posting topic's
// builder must put the document's date on the journal it posts.
func TestFW1_01B_EveryBuilderTakesItsDocumentsDate(t *testing.T) {
	ctx := context.Background()
	docAt := fw1bDocDate

	cases := []struct {
		name    string
		topic   string
		payload map[string]any
		rules   map[string]string // the fake store's rules for this topic, when the shared fixture has none
		want    time.Time         // the date the journal must carry; zero means the document's own date
		wire    func(*Service) *Service
	}{
		{
			name:    "the receipt takes the payment's received_at through the payment seam",
			topic:   "payment.received",
			payload: map[string]any{"payment_id": "p1"},
			wire: func(s *Service) *Service {
				return s.WithPaymentReader(fakePaymentReader{facts: PaymentFacts{
					BranchID: "b1", Method: "CASH", Status: "RECEIVED", AmountCents: 5000, ReceivedAt: docAt,
				}})
			},
		},
		{
			name:    "the tax relief takes the assessment instant off the event",
			topic:   "tax.relief.assessed",
			payload: map[string]any{"order_id": "o1", "occurred_at": docAt},
			wire: func(s *Service) *Service {
				return s.WithTaxSplitReader(fakeTaxSplit{full: 1300, onCredit: 800, net: 500, found: true})
			},
		},
		{
			name:  "the bounce takes the NSF record's instant off the event",
			topic: "payment.bounced",
			payload: map[string]any{
				"payment_id": "p1", "branch_id": "b1", "method": "CASH", "amount_cents": 5000, "occurred_at": docAt,
			},
		},
		{
			name:  "the reversal takes the reversal's instant off the event",
			topic: "payment.reversed",
			payload: map[string]any{
				"payment_id": "rev1", "reverses_payment_id": "p1", "amount_cents": 5000,
				"method": "CASH", "branch_id": "b1", "occurred_at": docAt,
			},
		},
		{
			name:  "the re-application takes the batch's instant off the event",
			topic: "payment.reapplied",
			payload: map[string]any{
				"batch_id": "b1", "payment_id": "p1", "amount_cents": 5000,
				"method": "CASH", "branch_id": "b1", "occurred_at": docAt,
			},
		},
		{
			name:  "the fulfilment takes the order's date off the event",
			topic: "order.fulfilled",
			payload: map[string]any{
				"order_id": "o1", "branch_id": "b1", "occurred_at": docAt,
				"lines": []map[string]any{{"product_id": "prod-1", "qty_base": 1.0}},
			},
			wire: func(s *Service) *Service {
				return s.WithCostBasis(fakeCostBasis{have: map[string]bool{"prod-1": true}, costCents: 4200})
			},
		},
		{
			name:  "the return takes the return's own date off the event",
			topic: "pos.return.committed",
			payload: map[string]any{
				"return_id": "r1", "order_id": "o1", "invoice_id": "i1", "refund_method": "CASH",
				"subtotal_cents": 4000, "tax_cents": 520, "total_cents": 4520, "branch_id": "b1", "occurred_at": docAt,
			},
		},
		{
			name:  "the stock reversal takes the return's own date off the event",
			topic: "order.returned",
			payload: map[string]any{
				"return_id": "r1", "order_id": "o1", "branch_id": "b1", "occurred_at": docAt,
				"lines": []map[string]any{{"product_id": "prod-1", "qty_base": 1.0}},
			},
			wire: func(s *Service) *Service {
				return s.WithCostBasis(fakeCostBasis{have: map[string]bool{"prod-1": true}, costCents: 4200})
			},
		},
		{
			name:  "the damage write off takes the return's own date off the event",
			topic: "pos.return.written_off",
			payload: map[string]any{
				"return_id": "r1", "order_id": "o1", "branch_id": "b1", "occurred_at": docAt,
				"lines": []map[string]any{{"product_id": "prod-1", "qty_base": 1.0}},
			},
			wire: func(s *Service) *Service {
				return s.WithCostBasis(fakeCostBasis{have: map[string]bool{"prod-1": true}, costCents: 4200})
			},
		},
		{
			name:  "the till session takes its business day off the event",
			topic: "till.session.completed",
			payload: map[string]any{
				"session_id": "s1", "branch_id": "b1", "over_short_cents": -500, "occurred_at": docAt,
			},
		},
		{
			name:  "the deposit cancellation takes the cancel's own date off the event",
			topic: "deposit.cancelled",
			payload: map[string]any{
				"invoice_id": "i1", "order_id": "o1", "disposition": "CREDIT_BACK",
				"occurred_at": docAt,
			},
			rules: map[string]string{"customer_deposit": "2300", "ar": "1100", "other_income": "4950"},
			wire: func(s *Service) *Service {
				return s.WithInvoiceReader(fakeInvoiceReader{amt: InvoiceAmounts{
					TotalCents: 4000, BranchID: "b1", InvoiceNo: "INV-SD", SourceKind: "SUPPLIER_DEPOSIT",
					// The deposit was finalized EARLIER: that date never dates this journal.
					FinalizedAt: fw1bEarlierDate,
				}})
			},
		},
		{
			name:    "the deposit release takes the delivery's date through the delivery seam",
			topic:   "deposit.released",
			payload: map[string]any{"invoice_id": "i1", "order_id": "o1"},
			wire: func(s *Service) *Service {
				return s.WithInvoiceReader(fakeInvoiceReader{amt: InvoiceAmounts{
					TotalCents: 4000, BranchID: "b1", InvoiceNo: "INV-SD", SourceKind: "SUPPLIER_DEPOSIT", FinalizedAt: fw1bEarlierDate,
				}}).WithDeliveryReader(fakeDelivery{at: docAt})
			},
		},
		{
			name:    "the deposit release with no delivery falls back to the clock",
			topic:   "deposit.released",
			payload: map[string]any{"invoice_id": "i1", "order_id": "o1"},
			want:    fw1bClockDate,
			wire: func(s *Service) *Service {
				return s.WithInvoiceReader(fakeInvoiceReader{amt: InvoiceAmounts{
					TotalCents: 4000, BranchID: "b1", InvoiceNo: "INV-SD", SourceKind: "SUPPLIER_DEPOSIT", FinalizedAt: fw1bEarlierDate,
				}})
			},
		},
		{
			name:    "the invoice finalization takes the document's date through the invoice seam",
			topic:   "ar.invoice.finalized",
			payload: map[string]any{"invoice_id": "i1"},
			wire: func(s *Service) *Service {
				return s.WithInvoiceReader(fakeInvoiceReader{amt: InvoiceAmounts{
					SubtotalCents: 4000, TaxCents: 520, TotalCents: 4520, BranchID: "b1", InvoiceNo: "INV-1", FinalizedAt: docAt,
				}})
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFakeStore()
			if c.rules != nil {
				f.rules[c.topic] = c.rules
			}
			svc := newTestService(f).withClock(func() time.Time { return fw1bClockDate })
			if c.wire != nil {
				svc = c.wire(svc)
			}
			if err := svc.consume(ctx, ev(c.topic, c.payload)); err != nil {
				t.Fatalf("consume %s: %v", c.topic, err)
			}
			d := f.lastInserted()
			if d == nil {
				t.Fatalf("the %s consume posted no journal", c.topic)
			}
			want := c.want
			if want.IsZero() {
				want = docAt
			}
			if !d.OccurredAt.Equal(want) {
				t.Fatalf("the %s journal carries the date %v, want %v", c.topic, d.OccurredAt.UTC(), want.UTC())
			}
		})
	}
}

// TestFW1_01B_AnEventWithoutADateFallsBackToTheClock is the other half of the contract: an event written
// before the rule existed (no date on the payload, no date from the seam) still posts, on the clock, and
// is never refused for want of a date.
func TestFW1_01B_AnEventWithoutADateFallsBackToTheClock(t *testing.T) {
	ctx := context.Background()
	f := newFakeStore()
	svc := newTestService(f).withClock(func() time.Time { return fw1bClockDate })

	if err := svc.consume(ctx, ev("till.session.completed", map[string]any{
		"session_id": "s1", "branch_id": "b1", "over_short_cents": -500,
	})); err != nil {
		t.Fatalf("consume till.session.completed: %v", err)
	}
	d := f.lastInserted()
	if d == nil {
		t.Fatalf("the consume posted no journal")
	}
	if !d.OccurredAt.Equal(fw1bClockDate) {
		t.Fatalf("the journal carries the date %v, want the clock's %v", d.OccurredAt.UTC(), fw1bClockDate.UTC())
	}
}
