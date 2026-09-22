//go:build integration

// coverage_integration_test.go, DIB-80 / S1. The calculator's BASIS read against real Postgres.
//
// coverage_test.go drives applyCoverage and the handler with no database in the room, which is the
// right place for the RULES. What it cannot check is the half that only exists as SQL:
//
//   - that the six LEFT JOINs actually left-join, a product with no rounding rule, no SF factor and
//     no pallet row must still be A ROW in the answer. It is the row the calculator has to be honest
//     about, and an inner join anywhere in that chain would turn "we do not know what this covers"
//     into "this product does not exist", silently, for 13 of the 32 seeded products.
//
//   - that the DECIMAL-as-text scan round-trips the seed's real numbers.
//
//   - that the coverage FALLBACK fires on the shipped data. products.coverage_sf_per_unit is NULL
//     for every seeded row, so if the derivation from product_uoms broke, the whole feature would
//     quietly report "no coverage data" for the entire catalogue and every unit test would pass.
//
//     Run: DATABASE_URL=postgres://hardscape:hardscape@localhost:5432/fix80?sslmode=disable \
//     go test -tags integration -count=1 -run Coverage ./internal/product/...
package product

import (
	"context"
	"math"
	"testing"
)

func TestCoverageBasis_LoadsEveryActiveProductIncludingTheOnesWithNoData(t *testing.T) {
	pool := ciPool(t)
	repo := &Repository{pool: pool}
	rows, err := repo.CoverageBasis(context.Background(), nil)
	if err != nil {
		t.Fatalf("CoverageBasis: %v", err)
	}
	if len(rows) < 20 {
		t.Fatalf("got %d basis rows; the seeded catalogue has ~32 active products", len(rows))
	}

	// THE LEFT-JOIN PROOF. Count the rows with no coverage at all. On the demo seed this is a large
	// minority, if it were ZERO, the joins would have quietly dropped exactly the rows this feature
	// promises to be honest about, and the "no coverage data" path would be untestable in the wild.
	var withCoverage, without int
	byKSU := map[string]CoverageBasis{}
	for _, r := range rows {
		byKSU[r.SKU] = r
		if r.CoverageSFPerUnit == nil {
			without++
			if r.CoverageSource != "" {
				t.Errorf("%s: nil coverage but source %q, the two must agree", r.SKU, r.CoverageSource)
			}
		} else {
			withCoverage++
			if r.CoverageSource == "" {
				t.Errorf("%s: coverage %v with no source, a number must say where it came from",
					r.SKU, *r.CoverageSFPerUnit)
			}
			if *r.CoverageSFPerUnit <= 0 {
				t.Errorf("%s: non-positive coverage %v reached the payload", r.SKU, *r.CoverageSFPerUnit)
			}
		}
		if r.SellUOM == "" {
			t.Errorf("%s: empty sell_uom, the fallback chain must never leave it blank", r.SKU)
		}
	}
	if without == 0 {
		t.Fatal("every product reported coverage; the seed has products with none, so a join dropped rows")
	}
	if withCoverage == 0 {
		t.Fatal("no product reported coverage; the product_uoms fallback is not firing")
	}
	t.Logf("basis rows: %d total, %d with coverage, %d without", len(rows), withCoverage, without)

	// - the worked example the whole feature is designed around -
	brussels, ok := byKSU["DBS-UL-BRUSSELS"]
	if !ok {
		t.Fatal("DBS-UL-BRUSSELS is not in the basis; the seed changed")
	}
	if brussels.BaseUOM != "PC" || brussels.BaseDimension != "count" {
		t.Errorf("base = %s/%s, want PC/count", brussels.BaseUOM, brussels.BaseDimension)
	}
	if brussels.SellUOM != "SF" || brussels.SellDimension != "area" {
		t.Errorf("sell = %s/%s, want SF/area", brussels.SellUOM, brussels.SellDimension)
	}
	if brussels.SellUOMFactor == nil || math.Abs(*brussels.SellUOMFactor-4.10) > 1e-9 {
		t.Errorf("sell factor = %v, want 4.10", brussels.SellUOMFactor)
	}
	// The DERIVED coverage, on real data: 1 / 4.10.
	if brussels.CoverageSource != "uom" {
		t.Errorf("source = %q, want %q, the promoted column is NULL on every seeded row",
			brussels.CoverageSource, "uom")
	}
	if brussels.CoverageSFPerUnit == nil || math.Abs(*brussels.CoverageSFPerUnit-1/4.10) > 1e-9 {
		t.Errorf("coverage = %v, want 1/4.10", brussels.CoverageSFPerUnit)
	}
	if brussels.PalletFactor == nil || math.Abs(*brussels.PalletFactor-50) > 1e-9 {
		t.Errorf("pallet factor = %v, want 50", brussels.PalletFactor)
	}
	if math.Abs(brussels.WasteFactorPct-5) > 1e-9 {
		t.Errorf("advised waste = %v, want 5", brussels.WasteFactorPct)
	}

	// - the honest-empty rows, on real data -
	sand, ok := byKSU["DBS-GATOR-SAND"]
	if !ok {
		t.Fatal("DBS-GATOR-SAND is not in the basis, the no-coverage row was dropped by a join")
	}
	if sand.CoverageSFPerUnit != nil {
		t.Errorf("gator sand reported coverage %v; nothing on file should mean nil", *sand.CoverageSFPerUnit)
	}
	if sand.BaseUOM != "BAG" {
		t.Errorf("gator sand base = %q, want BAG", sand.BaseUOM)
	}

	stone, ok := byKSU["DBS-AGG-34CLEAR"]
	if !ok {
		t.Fatal("DBS-AGG-34CLEAR is not in the basis")
	}
	if stone.SellDimension != "weight" {
		t.Errorf("clear stone sell dimension = %q, want weight", stone.SellDimension)
	}
	// No density is recorded anywhere for the seeded aggregates, and the loader must not invent one.
	if stone.DensityTPerCYD != nil {
		t.Errorf("clear stone reported a density %v; the seed records none", *stone.DensityTPerCYD)
	}
}

func TestCoverageBasis_IDFilterAnswersAboutTheProductsItWasAsked(t *testing.T) {
	pool := ciPool(t)
	repo := &Repository{pool: pool}
	ctx := context.Background()

	all, err := repo.CoverageBasis(ctx, nil)
	if err != nil {
		t.Fatalf("CoverageBasis(all): %v", err)
	}
	if len(all) < 3 {
		t.Skip("not enough seeded products to exercise the filter")
	}
	want := []string{all[0].ProductID, all[2].ProductID}
	got, err := repo.CoverageBasis(ctx, want)
	if err != nil {
		t.Fatalf("CoverageBasis(ids): %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("asked for 2 ids, got %d rows", len(got))
	}
	seen := map[string]bool{}
	for _, r := range got {
		seen[r.ProductID] = true
	}
	for _, id := range want {
		if !seen[id] {
			t.Errorf("asked for %s and it was not in the answer", id)
		}
	}
}

func TestCoverageBasis_EmptyIDListIsNotAnEmptyAnswer(t *testing.T) {
	// A guard against the obvious refactor: treating "no ids" as "IN ()" would answer nothing for
	// the unfiltered read, which is the calculator's NORMAL case.
	pool := ciPool(t)
	repo := &Repository{pool: pool}
	rows, err := repo.CoverageBasis(context.Background(), []string{})
	if err != nil {
		t.Fatalf("CoverageBasis(empty): %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("an empty id list must mean EVERY active product, not none")
	}
}
