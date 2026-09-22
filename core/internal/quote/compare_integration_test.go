//go:build integration

// compare_integration_test.go, DIB-59 comparison against a LIVE Postgres.
//
// The acceptance criterion for this item is negative, the comparison must write NOTHING to any pricing
// table, and a negative is only really provable against a real database. Asserting "we called the
// preview method" would test the code's intention; counting rows before and after tests the fact.
package quote

import (
	"context"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/inventory"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pricing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/product"
	"github.com/jackc/pgx/v5/pgxpool"
)

// pricingTables are every table the pricing module writes (0023/0026/0039). The comparison must leave
// all of them untouched. Enumerated rather than sampled: a rule appended to pricing_rules and a
// BYPASS row appended to pricing_audit_log are different leaks, and only one of them is the obvious one.
var pricingTables = []string{
	"pricing_audit_log",
	"pricing_rules",
	"pricing_category_policy",
	"pricing_list_prices",
	"quantity_break_headers",
	"quantity_break_items",
	"customer_category_overrides",
}

func tableCounts(t *testing.T, pool *pgxpool.Pool) map[string]int64 {
	t.Helper()
	out := map[string]int64{}
	for _, tbl := range pricingTables {
		var n int64
		if err := pool.QueryRow(context.Background(),
			"SELECT COUNT(*) FROM "+tbl).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", tbl, err)
		}
		out[tbl] = n
	}
	return out
}

// TestIntegration_Compare_WritesNothingToPricing is the R6 acceptance test.
//
// It runs the comparison through the REAL pricing service (not the deterministic stub the other tests
// use), that is the whole point: the stub cannot write, so proving the stub writes nothing would prove
// nothing at all. The real waterfall CAN append a BYPASS row to pricing_audit_log when margin
// enforcement is disabled, which is exactly the leak this guards.
func TestIntegration_Compare_WritesNothingToPricing(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	_, quoteID, prod, _ := seedQuote(t, pool, "DRAFT", nil)

	productSvc := productServiceFor(t, pool)
	realPricing := pricing.NewService(pricing.NewRepository(pool)).
		WithCategoryResolver(productSvc).
		WithUOMConverter(productSvc).
		WithMarginGate(pricing.NewIdentityMarginGate())
	invSvc := inventory.NewService(inventory.NewRepository(pool))

	repo := NewRepository(pool).withNow(func() time.Time { return now })
	svc := NewStaffService(repo).
		WithPricePreview(realPricing).
		WithCostBasis(invSvc).
		WithCustomerType(qCustType{}).
		withCaller(qCallerFrom).withNow(func() time.Time { return now })

	// ARM THE TRAP. The BYPASS append only fires when margin enforcement is DISABLED for the product
	// or its category, and nothing in the seed has it disabled, so without this the test would pass
	// even against a comparison that wrote on every call, and would be proving nothing at all.
	// Disabling enforcement for the product under comparison is what makes the assertion below real.
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO pricing_product_policy (product_id, margin_enforcement_disabled)
		VALUES ($1::uuid, true)
		ON CONFLICT (product_id) DO UPDATE SET margin_enforcement_disabled = true`, prod); err != nil {
		t.Fatalf("arm enforcement-disabled: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM pricing_product_policy WHERE product_id = $1`, prod)
	})

	before := tableCounts(t, pool)

	// Score the current line plus several alternates, the shape most likely to stamp phantom rows.
	alts := []Alternate{}
	rows, err := pool.Query(context.Background(),
		`SELECT id::text FROM products WHERE is_active AND id <> $1 ORDER BY sku LIMIT 4`, prod)
	if err != nil {
		t.Fatalf("pick alternates: %v", err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			t.Fatalf("scan alternate: %v", err)
		}
		alts = append(alts, Alternate{ProductID: id})
	}
	rows.Close()
	if len(alts) == 0 {
		t.Skip("no alternate products seeded")
	}

	out, err := svc.CompareLine(qCtxAs(intStaff(intTrenton)), quoteID, 1, alts)
	if err != nil {
		t.Fatalf("compare: %v", err)
	}
	if len(out.Options) < 2 {
		t.Fatalf("want the current line plus alternates, got %d options", len(out.Options))
	}
	if !out.Options[0].IsCurrent {
		t.Error("the current line must be pinned first so the comparison reads against it")
	}

	after := tableCounts(t, pool)
	for _, tbl := range pricingTables {
		if before[tbl] != after[tbl] {
			t.Errorf("R6 VIOLATED: %s changed from %d to %d rows during a comparison, the comparison "+
				"must write nothing to any pricing table", tbl, before[tbl], after[tbl])
		}
	}
}

// TestIntegration_Compare_ScoresThroughTheRealWaterfall proves the comparison is actually consulting
// the engine rather than echoing the stored line back, otherwise the no-write test above would pass
// trivially for a function that does nothing.
func TestIntegration_Compare_ScoresThroughTheRealWaterfall(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	_, quoteID, prod, _ := seedQuote(t, pool, "DRAFT", nil)

	productSvc := productServiceFor(t, pool)
	realPricing := pricing.NewService(pricing.NewRepository(pool)).
		WithCategoryResolver(productSvc).
		WithUOMConverter(productSvc).
		WithMarginGate(pricing.NewIdentityMarginGate())
	repo := NewRepository(pool).withNow(func() time.Time { return now })
	svc := NewStaffService(repo).
		WithPricePreview(realPricing).
		WithCostBasis(inventory.NewService(inventory.NewRepository(pool))).
		WithCustomerType(qCustType{}).
		withCaller(qCallerFrom).withNow(func() time.Time { return now })

	out, err := svc.CompareLine(qCtxAs(intStaff(intTrenton)), quoteID, 1, nil)
	if err != nil {
		t.Fatalf("compare: %v", err)
	}
	cur := out.Options[0]
	if cur.ProductID != prod {
		t.Errorf("current option product = %q, want the line's product %q", cur.ProductID, prod)
	}
	// Shape only, never a specific rate. Pinning a number here would make this test a hostage to
	// whatever the pricing fixtures happen to carry (the DIB-378 lesson).
	if cur.Unavailable == "" {
		if cur.UnitPriceCents <= 0 {
			t.Errorf("a scored option must carry a resolved unit price, got %d", cur.UnitPriceCents)
		}
		if cur.LineTotalCents <= 0 {
			t.Errorf("a scored option must carry a line total, got %d", cur.LineTotalCents)
		}
		if cur.PriceUOM == "" {
			t.Error("a scored option must name the unit its price is in")
		}
		if cur.Source == "" {
			t.Error("a scored option must name which waterfall level answered")
		}
	}
	if cur.DeltaCents != 0 {
		t.Errorf("the current line's delta against itself must be 0, got %d", cur.DeltaCents)
	}
}

func TestIntegration_Compare_BoundsTheRequest(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	_, quoteID, _, _ := seedQuote(t, pool, "DRAFT", nil)
	svc, repo, _ := intSvc(t, pool, now)
	_ = repo
	svc.WithPricePreview(previewStub{})

	too := make([]Alternate, maxAlternates+1)
	for i := range too {
		too[i] = Alternate{ProductID: qProd}
	}
	if _, err := svc.CompareLine(qCtxAs(intStaff(intTrenton)), quoteID, 1, too); err == nil {
		t.Fatalf("more than %d alternates must be refused", maxAlternates)
	}
}

// previewStub satisfies PricePreviewer for the bounds test, where no real pricing is needed.
type previewStub struct{}

func (previewStub) ResolvePricePreview(_ context.Context, in pricing.ResolveInput) (pricing.Breakdown, error) {
	return pricing.Breakdown{FinalCents: 100, PriceUOM: "EA", QtyBase: in.QtyEntered,
		LineTotalCents: int64(in.QtyEntered * 100)}, nil
}

// productServiceFor builds the real product service, which pricing needs as its category resolver and
// UOM converter, the comparison must go through the same conversion seam a real quote line does.
func productServiceFor(t *testing.T, pool *pgxpool.Pool) *product.Service {
	t.Helper()
	return product.NewService(product.NewRepository(pool))
}
