//go:build integration

// qd1f_reprice_unit_integration_test.go, the entered unit under the lock
// (review round 4, P3-6): the reprice priced the line for one product in one
// unit, and a delete and re-add of the SAME product in another unit inside the
// pricing window passed the product pin, leaving an entered quantity and unit
// that disagree with qty_base and the price. The unit is pinned beside the
// product on every reprice form, discount 0 included.
package quote

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pricing"
)

func TestQD1F_RepriceRefusesARowReaddedInAnotherUnitWhilePriced(t *testing.T) {
	for _, tc := range []struct {
		name string
		disc int
	}{{"discount 0", 0}, {"discount 10", 10}} {
		t.Run(tc.name, func(t *testing.T) {
			pool := intPool(t)
			now := time.Now().UTC()
			seller := qd1User(t, pool, "seller-unit")
			_, quoteID, prod, uom := qd1SeedOwnedQuote(t, pool, "DRAFT", seller, true)
			var otherUOM string
			if err := pool.QueryRow(context.Background(),
				`SELECT code::text FROM uoms WHERE code <> $1 ORDER BY code LIMIT 1`, uom).Scan(&otherUOM); err != nil {
				t.Fatalf("pick another unit: %v", err)
			}
			if tc.disc > 0 {
				qd1SetDisc(t, pool, quoteID, 1, tc.disc)
			}
			arb := &qd1Arbiter{marginBps: 5000, floorBps: 1200}
			arb.records = pricing.NewService(pricing.NewRepository(pool))
			svc := qd1SvcArbiter(t, pool, now, arb)
			pr := &qd1SwapPricer{}
			pr.swap = func() { qd1SwapRow(t, pool, quoteID, prod, otherUOM, tc.disc) }
			svc.WithPricing(pr)
			ctx := qd1WithRole(qCtxAs(intStaff(intTrenton)), "INSIDE_SALES", intTrenton, seller)

			_, err := svc.EditLineQty(ctx, quoteID, 1, "50")
			if !errors.Is(err, errFloorStateChanged) {
				t.Fatalf("a reprice must refuse a row re-added in another unit while priced, got %T: %v", err, err)
			}
			got := liveLine(t, pool, quoteID, 1)
			if got.ProductID != prod || got.QtyEntered != "2.0000" || got.UnitPriceCents != 1000 {
				t.Fatalf("the re-added row must be untouched, got %+v", got)
			}
		})
	}
}
