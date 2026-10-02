//go:build integration

// seed_slab_factors_integration_test.go, HH-207: the seeded SF conversion factors must be pieces per
// SQUARE FOOT, derived from each product's own dimensions.
//
// products.pieces_per_sf feeds product_uoms('SF').qty_in_base, which is base units (pieces) per ONE
// sell unit (one SF). The slab rows were seeded with pieces per SQUARE METRE (600x600 mm gives 2.78
// per square metre, but 0.258 per square foot), so a piece cost of 14.30 converted to 39.75 per SF
// against a seeded quote price of 5.00 per SF: a margin of -695 percent on the demo quote.
//
//	Run: DATABASE_URL='postgres://.../postgres?sslmode=disable' \
//	     go test -tags integration ./cmd/core/ -run SlabFactors -count=1
package main

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/product"
)

const sqMMPerSF = 92903.04

// seededFactor is one seeded product with a pieces_per_sf, and the face area its own dimensions give.
type seededFactor struct {
	SKU, Application, Dims string
	Seeded                 float64
	FaceArea               float64 // square mm, length x width
}

// dimensionDetermined are the products whose factor the dimensions settle: flat slabs, patio stones
// and porcelain sold by the face. Every other seeded factor (pavers and walls sold by laid face with
// joint, running-bond or multi-piece packs) is NOT determined by length x width and is reported in
// the table the test logs, not judged here.
var dimensionDetermined = map[string]bool{
	"DBS-TB-BLU60": true, "DBS-PM-MELVILLE": true, "DBS-BWS-TREVISTA": true,
	"DBS-PORCEA-2424": true, "DBS-PORCEA-PLANK": true, "DBS-PM-BORANJA": true,
}

func seededDBForSlabFactors(t *testing.T) (*pgxpool.Pool, []seededFactor) {
	t.Helper()
	ctx := context.Background()
	pool := freshMigratedDB(t, "slab207")
	body, err := seedFS.ReadFile("dibbits_seed.sql")
	if err != nil {
		t.Fatalf("reading the embedded seed: %v", err)
	}
	if _, err := pool.Exec(ctx, string(body)); err != nil {
		t.Fatalf("applying the seed: %v", err)
	}
	rows, err := pool.Query(ctx, `SELECT sku, application, dimensions_lwh, pieces_per_sf::text
	                                FROM products WHERE sku LIKE 'DBS-%' AND pieces_per_sf IS NOT NULL ORDER BY sku`)
	if err != nil {
		t.Fatalf("reading seeded products: %v", err)
	}
	defer rows.Close()
	var out []seededFactor
	for rows.Next() {
		var f seededFactor
		var v string
		if err := rows.Scan(&f.SKU, &f.Application, &f.Dims, &v); err != nil {
			t.Fatal(err)
		}
		f.Seeded, _ = strconv.ParseFloat(v, 64)
		parts := strings.Split(f.Dims, "x")
		if len(parts) < 2 {
			// A row a migration planted first (DBS-UL-BRUSSELS, 0061) keeps its own columns because the
			// seed is ON CONFLICT DO NOTHING; with no dimensions there is nothing to derive from.
			t.Logf("%s: no dimensions_lwh (%q), skipped", f.SKU, f.Dims)
			continue
		}
		l, _ := strconv.ParseFloat(parts[0], 64)
		w, _ := strconv.ParseFloat(parts[1], 64)
		f.FaceArea = l * w
		out = append(out, f)
	}
	if len(out) == 0 {
		t.Fatal("the seed carries no products with a pieces_per_sf, the test is not testing anything")
	}
	return pool, out
}

// Every dimension-determined slab factor agrees with 92903.04 / (L x W) within 1 percent, and NO
// seeded factor, determined or not, is the per-square-metre figure 1e6 / (L x W).
func TestIntegration_SeededSlabFactorsArePiecesPerSquareFoot(t *testing.T) {
	_, all := seededDBForSlabFactors(t)
	seen := 0
	for _, f := range all {
		perSF, perSQM := sqMMPerSF/f.FaceArea, 1e6/f.FaceArea
		t.Logf("%-18s %-7s %-12s seeded %.4f  face-derived %.4f  (seeded/derived %.2f)",
			f.SKU, f.Application, f.Dims, f.Seeded, perSF, f.Seeded/perSF)
		if math.Abs(f.Seeded-perSQM)/perSQM < 0.01 {
			t.Errorf("%s: pieces_per_sf %.4f is the per SQUARE METRE figure for %s (%.4f); per SF it is %.4f",
				f.SKU, f.Seeded, f.Dims, perSQM, perSF)
		}
		if dimensionDetermined[f.SKU] {
			seen++
			if math.Abs(f.Seeded-perSF)/perSF > 0.01 {
				t.Errorf("%s: pieces_per_sf %.4f, but %s gives %.4f pieces per SF (tolerance 1 percent)",
					f.SKU, f.Seeded, f.Dims, perSF)
			}
		}
	}
	if seen != len(dimensionDetermined) {
		t.Errorf("checked %d of the %d dimension-determined products, the list and the seed have drifted", seen, len(dimensionDetermined))
	}
}

// The direction proof, through the real product conversion: 100 SF of the 600x600 Blu 60 is about
// 26 pieces (each piece covers 3.875 SF), not 278. And the seeded quote line for it is sane: its
// qty_base is the pieces its own SF quantity converts to, and the seeded piece cost converted to the
// price unit leaves a positive margin against the seeded price.
func TestIntegration_Blu60QuoteLineConvertsAndMarginsSanely(t *testing.T) {
	ctx := context.Background()
	pool, _ := seededDBForSlabFactors(t)
	const blu = "a7000000-0000-4000-9000-000000000001"

	f, err := product.NewRepository(pool).ConvertUOM(ctx, blu, "SF")
	if err != nil {
		t.Fatalf("ConvertUOM(SF): %v", err)
	}
	pieces := 100 * f
	wantPieces := 100 * sqMMPerSF / (600 * 600)
	if math.Abs(pieces-wantPieces) > 0.5 {
		t.Errorf("100 SF of a 600x600 slab converts to %.2f pieces, want about %.2f", pieces, wantPieces)
	}

	var qtyEntered, qtyBase float64
	var price, cost int64
	var priceUOM string
	err = pool.QueryRow(ctx, `
		SELECT ql.qty_entered::float8, ql.qty_base::float8, ql.unit_price_cents, ql.price_uom, pbc.avg_unit_cost_cents
		  FROM quote_lines ql
		  JOIN product_branch_costs pbc ON pbc.product_id = ql.product_id
		       AND pbc.branch_id = '5d1b3f6e-0000-4000-9000-000000000001'
		 WHERE ql.product_id = $1 AND ql.price_uom = 'SF'`, blu).Scan(&qtyEntered, &qtyBase, &price, &priceUOM, &cost)
	if err != nil {
		t.Fatalf("reading the seeded Blu 60 quote line and cost: %v", err)
	}
	if math.Abs(qtyBase-qtyEntered*f) > 0.5 {
		t.Errorf("seeded quote line: %.0f SF has qty_base %.3f pieces, its own factor %.4f gives %.3f",
			qtyEntered, qtyBase, f, qtyEntered*f)
	}
	costPerSF := math.Round(float64(cost) * f) // pricing: roundCents(cost_base x factor(price_uom))
	margin := (float64(price) - costPerSF) / float64(price) * 100
	t.Logf("price %d per %s, piece cost %d, cost per SF %.0f, margin %.1f percent", price, priceUOM, cost, costPerSF, margin)
	if margin <= 0 || margin > 80 {
		t.Errorf("seeded Blu 60 quote margin is %.1f percent (%s), want a sane positive figure", margin, fmt.Sprint("price ", price, " cost/SF ", costPerSF))
	}
}
