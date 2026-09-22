package gl

// posting_return_fee_test.go, A-06: pos.return.committed with a restocking fee. The fee reduces the
// refund-rail credit and books to its own fee_revenue line; the journal stays balanced
// (DR revenue + DR HST = CR rail (total − fee) + CR fee_revenue). Zero-fee events post exactly as before.

import (
	"context"
	"testing"
)

func returnRules() map[string]string {
	return map[string]string{
		"revenue": "4000", "hst": "2200", "undeposited": "1020",
		"card_clearing": "1010", "ar": "1100", "fee_revenue": "4210",
	}
}

func TestA06_PosReturnCommitted_FeeBooksToFeeRevenue(t *testing.T) {
	ctx := context.Background()
	f := newFakeStore()
	f.rules["pos.return.committed"] = returnRules()
	svc := newTestService(f)

	// $45.20 ACCOUNT_CREDIT return with a $4.00 restocking fee: DR 4000 40.00 + DR 2200 5.20 /
	// CR 1100 41.20 + CR 4210 4.00.
	e := ev("pos.return.committed", map[string]any{
		"return_id": "ret-1", "refund_method": "ACCOUNT_CREDIT",
		"subtotal_cents": 4000, "tax_cents": 520, "total_cents": 4520, "fee_cents": 400, "branch_id": "b1",
	})
	if err := svc.consume(ctx, e); err != nil {
		t.Fatalf("consume: %v", err)
	}
	d := f.lastInserted()
	wantLeg(t, d, "4000", 4000, 0)
	wantLeg(t, d, "2200", 520, 0)
	wantLeg(t, d, "1100", 0, 4120)
	wantLeg(t, d, "4210", 0, 400)
}

func TestA06_PosReturnCommitted_ZeroFee_Unchanged(t *testing.T) {
	ctx := context.Background()
	f := newFakeStore()
	f.rules["pos.return.committed"] = returnRules()
	svc := newTestService(f)

	// No fee_cents in the payload (older emitters) → the pre-A06 journal, bit-identical.
	e := ev("pos.return.committed", map[string]any{
		"return_id": "ret-2", "refund_method": "CASH",
		"subtotal_cents": 4000, "tax_cents": 520, "total_cents": 4520, "branch_id": "b1",
	})
	if err := svc.consume(ctx, e); err != nil {
		t.Fatalf("consume: %v", err)
	}
	d := f.lastInserted()
	wantLeg(t, d, "4000", 4000, 0)
	wantLeg(t, d, "2200", 520, 0)
	wantLeg(t, d, "1020", 0, 4520)
	if _, ok := leg(d, "4210"); ok {
		t.Fatal("a zero-fee return must not touch the fee account")
	}
}

func TestA06_PosReturnCommitted_MalformedFee_Refused(t *testing.T) {
	ctx := context.Background()
	f := newFakeStore()
	f.rules["pos.return.committed"] = returnRules()
	svc := newTestService(f)

	for _, fee := range []int64{-1, 4521} {
		e := ev("pos.return.committed", map[string]any{
			"return_id": "ret-3", "refund_method": "ACCOUNT_CREDIT",
			"subtotal_cents": 4000, "tax_cents": 520, "total_cents": 4520, "fee_cents": fee, "branch_id": "b1",
		})
		if err := svc.consume(ctx, e); err == nil {
			t.Fatalf("fee_cents %d must refuse to post", fee)
		}
	}
	if len(f.journals) != 0 {
		t.Fatal("a malformed fee must post nothing")
	}
}
