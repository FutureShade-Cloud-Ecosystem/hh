//go:build integration

package tax

import (
	"context"
	"testing"
)

// A California dealer against the real schema: its rows are bound through the repository, the neutral
// standard reason is accepted by the widened snapshot CHECK (0389), the snapshot carries the standard rate
// it was written under, and the recompute reads that rate back instead of the dealer's current one.
// The rows planted here are torn down by defer (the pool closes before t.Cleanup runs, DIB-384).
func TestIntegration_Jurisdiction_CaliforniaBindsAndRecomputesAtItsOwnStandard(t *testing.T) {
	pool := intPool(t)
	if pool == nil {
		return
	}
	defer pool.Close()
	ctx := context.Background()
	const notes = "int-test california rows"
	svc := intSvc(t, pool).WithDealerRegion(fakeRegion{region: "CA"})

	if _, err := svc.CreateRate(ctx, Rate{ReasonCode: ReasonStdSales, RateBps: 775, EffectiveFrom: "2000-01-01", Notes: notes}); err != nil {
		t.Fatal(err)
	}
	orderID := seededOrderID(t, pool)
	defer func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM tax_rates WHERE jurisdiction = 'CA' AND notes = $1`, notes); err != nil {
			t.Errorf("teardown of the california rows failed: %v", err)
		}
		if _, err := pool.Exec(context.Background(), `DELETE FROM tax_decision_snapshot WHERE order_id=$1::uuid`, orderID); err != nil {
			t.Errorf("teardown of the california snapshot failed: %v", err)
		}
	}()

	q, err := svc.AssessSale(ctx, SaleAssessment{SubtotalCents: 100_000})
	if err != nil {
		t.Fatal(err)
	}
	if q.TaxCents != 7_750 || q.RateBps != 775 || q.StandardRateBps != 775 || q.ReasonCode != ReasonStdSales || q.Jurisdiction != "CA" {
		t.Fatalf("california ring: %+v", q)
	}

	// The Ontario dealer on the same database is untouched by the California rows.
	on := intSvc(t, pool)
	if oq, err := on.AssessSale(ctx, SaleAssessment{SubtotalCents: 100_000}); err != nil || oq.RateBps != 1300 || oq.ReasonCode != ReasonStdON13 {
		t.Fatalf("ontario beside california: %+v %v", oq, err)
	}

	// The neutral reason is lawful at the snapshot, and the standard rate is stored and read back.
	if _, err := pool.Exec(ctx, `DELETE FROM tax_decision_snapshot WHERE order_id=$1::uuid`, orderID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO tax_decision_snapshot (order_id, tax_reason_code, rule_version, rate_bps, full_hst_cents, net_collected_cents, automated, standard_rate_bps, created_at)
		VALUES ($1::uuid,'STD_SALES_TAX','W4-flat-13',775,775,775,false,775, TIMESTAMPTZ '2088-05-01 12:00:00+00')`, orderID); err != nil {
		t.Fatalf("the neutral standard reason was refused by the snapshot CHECK: %v", err)
	}
	res, err := svc.RecomputeReport(ctx, "2088-01-01", "2088-12-31")
	if err != nil {
		t.Fatal(err)
	}
	if res.Scanned != 1 || res.Inserted != 1 {
		t.Fatalf("recompute over the isolated 2088 window: scanned=%d inserted=%d, want 1/1", res.Scanned, res.Inserted)
	}
	var reason string
	var rate, std int
	var full int64
	if err := pool.QueryRow(ctx, `
		SELECT tax_reason_code, rate_bps, standard_rate_bps, full_hst_cents FROM tax_decision_snapshot
		WHERE order_id=$1::uuid AND rule_version='M5-recompute' ORDER BY created_at DESC, id DESC LIMIT 1`, orderID).Scan(&reason, &rate, &std, &full); err != nil {
		t.Fatal(err)
	}
	if reason != ReasonStdSales || rate != 775 || std != 775 || full != 775 {
		t.Fatalf("recomputed row: %s rate=%d std=%d full=%d", reason, rate, std, full)
	}

	// A row written the old way (no standard rate named) is an Ontario row at 13 percent.
	var legacy int
	if err := pool.QueryRow(ctx, `SELECT column_default::int FROM information_schema.columns WHERE table_name='tax_decision_snapshot' AND column_name='standard_rate_bps'`).Scan(&legacy); err != nil || legacy != 1300 {
		t.Fatalf("the legacy default is %d (%v), want 1300", legacy, err)
	}
}
