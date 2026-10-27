//go:build integration

// fw1_01b_emitter_integration_test.go, FW1-01b round 2's emitter legs (S3's review,
// FROM-PRINCIPAL 112, P2): the POS's events carry their document's own date. A return's
// events agree on ONE date, the return's own, and the ring's order and relief events carry
// the ring's date.
package pos

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// fw1b2EventDate reads one event's own instant off the outbox row: found says whether the
// event exists at all, carried says whether it carries a date.
func fw1b2EventDate(t *testing.T, pool *pgxpool.Pool, topic, key, value string) (found bool, carried *time.Time) {
	t.Helper()
	var n int64
	var at *time.Time
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*), MAX((payload->>'occurred_at')::timestamptz) FROM events_outbox
		  WHERE topic = $1 AND payload->>$2 = $3`, topic, key, value).Scan(&n, &at); err != nil {
		t.Fatalf("read the %s event: %v", topic, err)
	}
	return n > 0, at
}

// TestIntegration_FW1_01B_R2_ReturnEventsAgreeOnOneDate is the return's emitter leg: the
// money reversal, the stock reversal and the damage write off of one return all carry the
// return's own date, so the three journals the ledger posts for it land in the same month.
func TestIntegration_FW1_01B_R2_ReturnEventsAgreeOnOneDate(t *testing.T) {
	f := newDispFixture(t)
	oR, lR := f.ring(SaleCollectNow, 5)
	oD, lD := f.ring(SaleCollectNow, 4)
	note, err := f.note(
		ReturnAcrossOrderInput{OrderID: oR, Lines: []ReturnLineInput{{OrderLineID: lR, QtyBase: 5, ReasonCode: "OTHER", Disposition: DispositionRestock}}},
		ReturnAcrossOrderInput{OrderID: oD, Lines: []ReturnLineInput{{OrderLineID: lD, QtyBase: 4, ReasonCode: "DAMAGED", Disposition: DispositionDamaged}}},
	)
	if err != nil {
		t.Fatalf("the note: %v", err)
	}

	var dates []time.Time
	for _, r := range note.Returns {
		// One return, one date: the money reversal, the stock reversal and the damage write off
		// of the SAME return agree. Two returns committed by one note are two documents and may
		// differ by the microseconds the walk took between them.
		var mine []time.Time
		for _, topic := range []string{"pos.return.committed", "order.returned", "pos.return.written_off"} {
			found, carried := fw1b2EventDate(t, f.pool, topic, "return_id", r.ReturnID)
			if !found {
				continue // this child's disposition did not call for that event
			}
			if carried == nil {
				t.Fatalf("the %s event carries no occurred_at; the return's own date must ride it", topic)
			}
			mine = append(mine, *carried)
		}
		if len(mine) == 0 {
			t.Fatalf("the return emitted no dated event at all")
		}
		for _, d := range mine {
			if !d.UTC().Equal(mine[0].UTC()) {
				t.Fatalf("one return's events carry different dates (%v and %v); one return has one date", mine[0].UTC(), d.UTC())
			}
		}
		dates = append(dates, mine...)
	}
	if len(dates) < 3 {
		t.Fatalf("the note emitted %d dated events, want at least the money reversal, the stock reversal and the write off", len(dates))
	}
}

// TestIntegration_FW1_01B_R2_TheRingsEventsCarryTheRingsDate is the ring's emitter leg: the
// order's fulfilment and the relief's assessment, both the ring's own act, carry the ring's
// date.
func TestIntegration_FW1_01B_R2_TheRingsEventsCarryTheRingsDate(t *testing.T) {
	f := newDispFixture(t)
	sale, err := f.svc.Sale(f.ctx, SaleInput{
		IdempotencyKey: key(), BranchID: f.branchID, CustomerID: f.custID,
		SaleType: SaleCollectNow, Tender: TenderAccount,
		Lines:  []LineInput{{ProductID: f.productID, QtyEntered: 4, UOMEntered: "SF"}},
		Relief: &ReliefDecision{ReasonCode: TaxONFNPos8},
	})
	if err != nil {
		t.Fatalf("the ring: %v", err)
	}
	f.orders = append(f.orders, sale.OrderID)

	var dates []time.Time
	for _, topic := range []string{"order.fulfilled", "tax.relief.assessed"} {
		found, carried := fw1b2EventDate(t, f.pool, topic, "order_id", sale.OrderID)
		if !found {
			t.Fatalf("the ring emitted no %s event", topic)
		}
		if carried == nil {
			t.Fatalf("the %s event carries no occurred_at; the ring's own date must ride it", topic)
		}
		dates = append(dates, *carried)
	}
	if !dates[0].UTC().Equal(dates[1].UTC()) {
		t.Fatalf("the ring's events carry different dates (%v and %v); one ring has one date", dates[0].UTC(), dates[1].UTC())
	}
}
