package gl

import (
	"context"
	"testing"
)

// Sub item 10.3 (the Q4 ruling): goods that come back damaged are written off to 5200 at the current
// average cost, the cost leaving COGS (DR 5200 / CR 5000), keyed per return, skipped quietly when no
// cost row exists, and the topic is one the posting consumer owns.
func TestPosReturnWrittenOff_ExpensesTheCostToInventoryAdjustments(t *testing.T) {
	ctx := context.Background()
	f := newFakeStore()
	svc := newTestService(f).WithCostBasis(fakeCostBasis{have: map[string]bool{"prod-costed": true}, costCents: 4200})

	e := ev("pos.return.written_off", map[string]any{
		"order_id": "ord-1", "return_id": "ret-1", "branch_id": "b1",
		"lines": []map[string]any{
			{"product_id": "prod-costed", "qty_base": 2.0},
			{"product_id": "prod-costed", "qty_base": 1.0},
			{"product_id": "prod-nocost", "qty_base": 3.0},
		},
	})
	if err := svc.consume(ctx, e); err != nil {
		t.Fatalf("consume pos.return.written_off: %v", err)
	}
	if len(f.inserted) != 1 {
		t.Fatalf("want exactly one write off journal, got %d", len(f.inserted))
	}
	d := &f.inserted[0]
	wantLeg(t, d, "5200", 8400, 0)
	wantLeg(t, d, "5000", 0, 8400)
	if d.IdempotencyKey != "pos.return.written_off:ret-1" {
		t.Fatalf("the journal is keyed per return, got %q", d.IdempotencyKey)
	}

	// The same event again posts nothing new (the ledger dedupes on the key).
	if err := svc.consume(ctx, e); err != nil {
		t.Fatalf("redelivery: %v", err)
	}
	if len(f.journals) != 1 {
		t.Fatalf("a redelivered write off must not post twice, got %d journals", len(f.journals))
	}

	// No cost row on any line: nothing to write off, no journal, no error.
	f2 := newFakeStore()
	svc2 := newTestService(f2).WithCostBasis(fakeCostBasis{have: map[string]bool{}})
	if err := svc2.consume(ctx, ev("pos.return.written_off", map[string]any{
		"order_id": "ord-2", "return_id": "ret-2", "branch_id": "b1",
		"lines": []map[string]any{{"product_id": "prod-nocost", "qty_base": 1.0}},
	})); err != nil {
		t.Fatalf("an uncosted write off must skip quietly: %v", err)
	}
	if len(f2.inserted) != 0 {
		t.Fatalf("an uncosted write off posted %d journals", len(f2.inserted))
	}

	// A malformed event (no return id) is an error, never a silent ack.
	if err := svc.consume(ctx, ev("pos.return.written_off", map[string]any{"order_id": "ord-3"})); err == nil {
		t.Fatal("a write off with no return_id must be refused")
	}
}

func TestPosReturnWrittenOff_IsAPostedTopicWithRequiredRules(t *testing.T) {
	found := false
	for _, topic := range PostedTopics {
		if topic == "pos.return.written_off" {
			found = true
		}
	}
	if !found {
		t.Fatal("pos.return.written_off must be a topic the posting consumer owns")
	}
	f := newFakeStore()
	svc := newTestService(f)
	f.activeRules = healthyRules()
	if err := svc.ValidatePostingRules(context.Background()); err != nil {
		t.Fatalf("healthy rules incl. the write off pair should validate: %v", err)
	}
	var without []RuleRef
	for _, r := range healthyRules() {
		if r.EventType == "pos.return.written_off" && r.LineRole == "expense" {
			continue
		}
		without = append(without, r)
	}
	f.activeRules = without
	if err := svc.ValidatePostingRules(context.Background()); err == nil {
		t.Fatal("a missing write off expense rule must fail the boot validation")
	}
}

// Item 10.5: a swap child's applied credit leaves the refund rail and credits the receivable, the
// new invoice it settles debits it; the journal stays balanced and the rail moves only the leftover.
func TestPosReturnCommitted_AppliedCreditCreditsTheReceivableNotTheRail(t *testing.T) {
	ctx := context.Background()
	f := newFakeStore()
	svc := newTestService(f)
	if err := svc.consume(ctx, ev("pos.return.committed", map[string]any{
		"return_id": "ret-s1", "order_id": "o1", "invoice_id": "i1", "refund_method": "CARD",
		"subtotal_cents": 10000, "tax_cents": 1300, "total_cents": 11300, "fee_cents": 0, "applied_cents": 7000, "branch_id": "b1",
	})); err != nil {
		t.Fatalf("consume: %v", err)
	}
	d := &f.inserted[0]
	wantLeg(t, d, "4000", 10000, 0)
	wantLeg(t, d, "2200", 1300, 0)
	wantLeg(t, d, "1010", 0, 4300)
	wantLeg(t, d, "1100", 0, 7000)
	// Applied beyond the total is a malformed event.
	if err := svc.consume(ctx, ev("pos.return.committed", map[string]any{
		"return_id": "ret-s2", "order_id": "o1", "invoice_id": "i1", "refund_method": "CASH",
		"subtotal_cents": 100, "tax_cents": 13, "total_cents": 113, "applied_cents": 200, "branch_id": "b1",
	})); err == nil {
		t.Fatal("applied credit above the total must be refused")
	}
}

// The principal's split for an order not fully paid: the part that came off the open invoice credits
// the receivable, the rail moves only what was paid.
func TestPosReturnCommitted_InvoiceOffsetCreditsTheReceivable(t *testing.T) {
	ctx := context.Background()
	f := newFakeStore()
	svc := newTestService(f)
	if err := svc.consume(ctx, ev("pos.return.committed", map[string]any{
		"return_id": "ret-o1", "order_id": "o1", "invoice_id": "i1", "refund_method": "CASH",
		"subtotal_cents": 6000, "tax_cents": 780, "total_cents": 6780, "fee_cents": 0, "invoice_offset_cents": 3000, "branch_id": "b1",
	})); err != nil {
		t.Fatalf("consume: %v", err)
	}
	d := &f.inserted[0]
	wantLeg(t, d, "1020", 0, 3780)
	wantLeg(t, d, "1100", 0, 3000)
	wantLeg(t, d, "4000", 6000, 0)
	if err := svc.consume(ctx, ev("pos.return.committed", map[string]any{
		"return_id": "ret-o2", "order_id": "o1", "invoice_id": "i1", "refund_method": "CASH",
		"subtotal_cents": 100, "tax_cents": 13, "total_cents": 113, "invoice_offset_cents": 200, "branch_id": "b1",
	})); err == nil {
		t.Fatal("an offset above the total must be refused")
	}
}
