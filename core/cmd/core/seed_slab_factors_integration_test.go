//go:build integration

// seed_slab_factors_integration_test.go, HH-207: the seeded SF conversion factors must be pieces per
// SQUARE FOOT, derived from each product's own dimensions.
//
// products.pieces_per_sf feeds product_uoms('SF').qty_in_base, which is base units (pieces) per ONE
// sell unit (one SF). The slab rows were seeded with pieces per SQUARE METRE (600x600 mm gives 2.78
// per square metre, but 0.258 per square foot), so a piece cost of 14.30 converted to 39.75 per SF
// against a seeded quote price of 5.00 per SF: a margin of -695 percent on the demo quote.
//
// The convention, proved by PR 78 for the flat slabs and held here for the whole catalogue: the sold
// face is length x width of dimensions_lwh for pavers and slabs, and length x HEIGHT for a wall
// (application 'Wall', the width is the depth into the wall). Migration 0360 corrects an already
// seeded database, guarded so a value a person changed is never touched; its legs below plant the
// old world explicitly and hold the migration to it.
//
//	Run: DATABASE_URL='postgres://.../postgres?sslmode=disable' \
//	     go test -tags integration ./cmd/core/ -run 'SlabFactors|Blu60|Magnumstone|Migration0360' -count=1
package main

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/product"
)

const sqMMPerSF = 92903.04

// minHeldFactors is how many face-derived factors the seed must carry: seventeen products the
// dimensions settle (six flat slabs, six pavers, five walls). If the seed drops below this the held
// set has gone empty and the sweep below would pass without judging anything.
const minHeldFactors = 17

// migration0360File is the fix-up migration this lane adds for databases seeded before the fix.
const migration0360File = "0360_correct_seeded_conversion_factors.sql"

// seededFactor is one seeded product with a pieces_per_sf, and the area of its sold face.
type seededFactor struct {
	SKU, Application, Dims string
	Seeded                 float64
	FaceArea               float64 // square mm: length x width, length x height for a wall
}

func seededSlabPool(t *testing.T) *pgxpool.Pool {
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
	return pool
}

func seededDBForSlabFactors(t *testing.T) (*pgxpool.Pool, []seededFactor) {
	t.Helper()
	ctx := context.Background()
	pool := seededSlabPool(t)
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
		if len(parts) < 2 || (f.Application == "Wall" && len(parts) < 3) {
			// A row a migration planted first (DBS-UL-BRUSSELS, 0061) keeps its own columns because the
			// seed is ON CONFLICT DO NOTHING; with no dimensions (or a wall whose height is missing)
			// there is nothing to derive its sold face from.
			t.Logf("%s: no derivable face (dims %q, application %s), skipped", f.SKU, f.Dims, f.Application)
			continue
		}
		l, _ := strconv.ParseFloat(parts[0], 64)
		w, _ := strconv.ParseFloat(parts[1], 64)
		f.FaceArea = l * w
		if f.Application == "Wall" {
			h, _ := strconv.ParseFloat(parts[2], 64)
			f.FaceArea = l * h // the width of a wall block is its depth into the wall, not on the sold face
		}
		out = append(out, f)
	}
	if len(out) == 0 {
		t.Fatal("the seed carries no products with a pieces_per_sf, the test is not testing anything")
	}
	return pool, out
}

// Every seeded product with a derivable face is held to 92903.04 / face within 1 percent (pavers and
// slabs by length x width, walls by length x height), and NO seeded factor is the per-square-metre
// figure 1e6 / face.
func TestIntegration_SeededSlabFactorsArePiecesPerSquareFoot(t *testing.T) {
	_, all := seededDBForSlabFactors(t)
	for _, f := range all {
		perSF, perSQM := sqMMPerSF/f.FaceArea, 1e6/f.FaceArea
		t.Logf("%-18s %-7s %-12s seeded %.4f  face-derived %.4f  (seeded/derived %.2f)",
			f.SKU, f.Application, f.Dims, f.Seeded, perSF, f.Seeded/perSF)
		if math.Abs(f.Seeded-perSQM)/perSQM < 0.01 {
			t.Errorf("%s: pieces_per_sf %.4f is the per SQUARE METRE figure for %s (%.4f); per SF it is %.4f",
				f.SKU, f.Seeded, f.Dims, perSQM, perSF)
		}
		if math.Abs(f.Seeded-perSF)/perSF > 0.01 {
			t.Errorf("%s: pieces_per_sf %.4f, but its %s face %s gives %.4f pieces per SF (tolerance 1 percent)",
				f.SKU, f.Seeded, f.Application, f.Dims, perSF)
		}
	}
	if len(all) < minHeldFactors {
		t.Errorf("held %d seeded factors to their face, the seed must carry at least %d, the held set has gone empty",
			len(all), minHeldFactors)
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

// The seeded Magnumstone invoice line carries the pieces its own SF quantity converts to: 1500 SF at
// the wall face factor (450x200, so 1.0323 at the seed's four decimals) is 1548.45, exactly what the
// live path writes (pieces are not rounded up, the same convention PR 78 proved on Blu 60's
// 247.776). On the old seed the line sat at 3600, which is 1500 x 2.40 and nothing geometric.
func TestIntegration_MagnumstoneInvoiceLineFollowsItsFactor(t *testing.T) {
	ctx := context.Background()
	pool, _ := seededDBForSlabFactors(t)
	const magnum = "a7000000-0000-4000-9000-000000000010"

	var dims, application string
	if err := pool.QueryRow(ctx, `SELECT dimensions_lwh, application FROM products WHERE id = $1`, magnum).
		Scan(&dims, &application); err != nil {
		t.Fatalf("reading Magnumstone's dimensions: %v", err)
	}
	parts := strings.Split(dims, "x")
	if len(parts) != 3 {
		t.Fatalf("Magnumstone dims %q do not give a wall face", dims)
	}
	l, _ := strconv.ParseFloat(parts[0], 64)
	h, _ := strconv.ParseFloat(parts[2], 64) // a wall is held to length x height
	perSF := sqMMPerSF / (l * h)
	seededStyle := math.Round(perSF*10000) / 10000 // the seed writes the factor at four decimals

	var qtyEntered, qtyBase float64
	if err := pool.QueryRow(ctx, `
		SELECT qty_entered::float8, qty_base::float8
		  FROM invoice_lines
		 WHERE invoice_id = '19000000-0000-4000-9000-000000000002' AND line_no = 1`).
		Scan(&qtyEntered, &qtyBase); err != nil {
		t.Fatalf("reading the seeded Magnumstone invoice line: %v", err)
	}
	want := qtyEntered * seededStyle
	if math.Abs(qtyBase-want) > 0.001 {
		t.Errorf("seeded Magnumstone invoice line: %.0f SF carries qty_base %.3f pieces, its wall face %s gives %.0f x %.4f = %.3f",
			qtyEntered, qtyBase, dims, qtyEntered, seededStyle, want)
	}
}

// ── migration 0360: the fix-up for databases seeded before the correction ──────────────────────

// oldFactorValues is what the seed wrote before this lane: seventeen factors a face settles, at
// their pre-correction figures, as (sku, old pieces per SF).
const oldFactorValues = `(VALUES ` +
	`('DBS-BWS-ANTICO',5.10),` +
	`('DBS-BWS-TREVISTA',5.56),` +
	`('DBS-OAKS-PROMA',3.45),` +
	`('DBS-OAKS-VINTAGE',3.55),` +
	`('DBS-PM-BORANJA',2.78),` +
	`('DBS-PM-LAFITT',3.10),` +
	`('DBS-PM-MAGNUMSTONE',2.40),` +
	`('DBS-PM-MELVILLE',4.17),` +
	`('DBS-PORCEA-2424',2.78),` +
	`('DBS-PORCEA-PLANK',2.78),` +
	`('DBS-TB-ABERDEEN',5.14),` +
	`('DBS-TB-BLU60',2.78),` +
	`('DBS-TB-HYDR',3.95),` +
	`('DBS-TB-MINICRETA',6.00),` +
	`('DBS-UL-ILCAMPO',1.55),` +
	`('DBS-UL-SERIES3000',2.07),` +
	`('DBS-UL-THORNBURY',3.57)` +
	`)`

// newFactorValues is the same seventeen at the face-derived figures the seed and the migration
// carry, as (sku, new pieces per SF).
const newFactorValues = `(VALUES ` +
	`('DBS-BWS-ANTICO',3.1600),` +
	`('DBS-BWS-TREVISTA',0.5161),` +
	`('DBS-OAKS-PROMA',2.1357),` +
	`('DBS-OAKS-VINTAGE',2.6400),` +
	`('DBS-PM-BORANJA',0.2581),` +
	`('DBS-PM-LAFITT',1.1613),` +
	`('DBS-PM-MAGNUMSTONE',1.0323),` +
	`('DBS-PM-MELVILLE',0.3871),` +
	`('DBS-PORCEA-2424',0.2581),` +
	`('DBS-PORCEA-PLANK',0.2581),` +
	`('DBS-TB-ABERDEEN',3.3180),` +
	`('DBS-TB-BLU60',0.2581),` +
	`('DBS-TB-HYDR',3.6866),` +
	`('DBS-TB-MINICRETA',4.6452),` +
	`('DBS-UL-ILCAMPO',1.0323),` +
	`('DBS-UL-SERIES3000',2.0645),` +
	`('DBS-UL-THORNBURY',3.3180)` +
	`)`

const (
	bluQuoteID     = `'17000000-0000-4000-9000-000000000001'`
	magnumInvoice  = `'19000000-0000-4000-9000-000000000002'`
	anticoSKU      = `'DBS-BWS-ANTICO'`
	changedUOM     = "3.3333" // a value no seed ever wrote
	changedQtyBase = "999.999"
)

func migration0360Body(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", "db", "migrations", migration0360File))
	if err != nil {
		t.Fatalf("reading migration %s: %v", migration0360File, err)
	}
	return string(body)
}

// applyDibbitsSeed applies the embedded seed to a pool the caller built (the migration legs build
// their own database name, then need the seed the same way the shared helper applies it).
func applyDibbitsSeed(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	body, err := seedFS.ReadFile("dibbits_seed.sql")
	if err != nil {
		t.Fatalf("reading the embedded seed: %v", err)
	}
	if _, err := pool.Exec(context.Background(), string(body)); err != nil {
		t.Fatalf("applying the seed: %v", err)
	}
}

// plantOldWorld re-plants, on a freshly migrated and seeded database, every value this lane
// corrects, as the old seed carried it: the seventeen product factors and their SF uom rows at the
// old figures, the Blu 60 quote line at 2669, the Magnumstone invoice line at 3600, and one
// SEEDED_DEFAULT SF to PLT edge per product at the factor the old uom rows derive. Every plant is
// verified, so a leg cannot pass because the old world failed to take.
func plantOldWorld(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	mustExec(t, ctx, pool, `UPDATE products p SET pieces_per_sf = v.old, updated_at = now()
		FROM `+oldFactorValues+` v(sku, old) WHERE p.sku = v.sku`)
	mustExec(t, ctx, pool, `UPDATE product_uoms pu SET qty_in_base = v.old, updated_at = now()
		FROM products p JOIN `+oldFactorValues+` v(sku, old) ON p.sku = v.sku
		WHERE pu.product_id = p.id AND pu.uom_code = 'SF'`)
	mustExec(t, ctx, pool, `UPDATE quote_lines SET qty_base = 2669
		 WHERE quote_id = `+bluQuoteID+` AND line_no = 1`)
	mustExec(t, ctx, pool, `UPDATE invoice_lines SET qty_base = 3600
		 WHERE invoice_id = `+magnumInvoice+` AND line_no = 1`)
	mustExec(t, ctx, pool, `INSERT INTO pricing_uom_conversions
			(product_id, from_uom, to_uom, factor, round_up_at_pick, source, source_note)
		SELECT p.id, 'SF', 'PLT', ROUND(v.old / plt.qty_in_base, 8), TRUE, 'SEEDED_DEFAULT',
		       'planted at the pre-correction factor by the 0360 integration test'
		  FROM products p
		  JOIN `+oldFactorValues+` v(sku, old) ON p.sku = v.sku
		  JOIN product_uoms plt ON plt.product_id = p.id AND plt.uom_code = 'PLT' AND plt.qty_in_base > 0
		ON CONFLICT DO NOTHING`)

	wantCount(t, pool, "planted old product factors", 17,
		`SELECT count(*) FROM products p JOIN `+oldFactorValues+` v(sku, old) ON p.sku = v.sku WHERE p.pieces_per_sf = v.old`)
	wantCount(t, pool, "planted old SF uom rows", 17,
		`SELECT count(*) FROM product_uoms pu JOIN products p ON p.id = pu.product_id
		  JOIN `+oldFactorValues+` v(sku, old) ON p.sku = v.sku WHERE pu.uom_code = 'SF' AND pu.qty_in_base = v.old`)
	wantCount(t, pool, "planted Blu 60 quote line at 2669", 1,
		`SELECT count(*) FROM quote_lines WHERE quote_id = `+bluQuoteID+` AND line_no = 1 AND qty_base = 2669`)
	wantCount(t, pool, "planted Magnumstone invoice line at 3600", 1,
		`SELECT count(*) FROM invoice_lines WHERE invoice_id = `+magnumInvoice+` AND line_no = 1 AND qty_base = 3600`)
	wantCount(t, pool, "planted old SEEDED_DEFAULT SF to PLT edges", 17,
		`SELECT count(*) FROM pricing_uom_conversions c JOIN products p ON p.id = c.product_id
		  JOIN `+oldFactorValues+` v(sku, old) ON p.sku = v.sku
		 WHERE c.from_uom = 'SF' AND c.to_uom = 'PLT' AND c.source = 'SEEDED_DEFAULT'
		   AND c.factor = ROUND(v.old / (SELECT qty_in_base FROM product_uoms pu WHERE pu.product_id = p.id AND pu.uom_code = 'PLT'), 8)`)
}

func wantCount(t *testing.T, pool *pgxpool.Pool, what string, want int, query string) {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), query).Scan(&n); err != nil {
		t.Fatalf("%s: %v", what, err)
	}
	if n != want {
		t.Fatalf("%s: got %d rows, want %d", what, n, want)
	}
}

// noRowOutside fails when the query (a mismatch hunt) returns anything, naming what it hunts.
func noRowOutside(t *testing.T, pool *pgxpool.Pool, what, query string) {
	t.Helper()
	rows, err := pool.Query(context.Background(), query)
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
	defer rows.Close()
	var bad []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		bad = append(bad, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(bad) > 0 {
		t.Errorf("%s: %s", what, strings.Join(bad, ", "))
	}
}

// productFactorMismatches hunts products whose factor is not the wanted one.
const productFactorMismatches = `SELECT p.sku || '=' || p.pieces_per_sf::text
	   FROM products p JOIN %s v(sku, want) ON p.sku = v.sku WHERE p.pieces_per_sf <> v.want`

// uomFactorMismatches hunts SF uom rows whose qty_in_base is not the wanted one.
const uomFactorMismatches = `SELECT p.sku || '=' || pu.qty_in_base::text
	   FROM product_uoms pu JOIN products p ON p.id = pu.product_id
	   JOIN %s v(sku, want) ON p.sku = v.sku
	  WHERE pu.uom_code = 'SF' AND pu.qty_in_base <> v.want`

// edgeFactorMismatches hunts SEEDED_DEFAULT SF to PLT edges whose factor is not the wanted factor
// recomputed from the (corrected) SF uom row over the pallet count.
const edgeFactorMismatches = `SELECT v.sku || '=' || COALESCE(c.factor::text, 'missing')
	   FROM %s v(sku, want)
	   JOIN products p ON p.sku = v.sku
	   JOIN product_uoms plt ON plt.product_id = p.id AND plt.uom_code = 'PLT' AND plt.qty_in_base > 0
	   LEFT JOIN pricing_uom_conversions c ON c.product_id = p.id AND c.from_uom = 'SF'
	        AND c.to_uom = 'PLT' AND c.source = 'SEEDED_DEFAULT' AND c.is_active
	  WHERE c.id IS NULL OR c.factor <> ROUND(v.want / plt.qty_in_base, 8)`

// The old world, corrected: every seeded value the old factor touched lands at its face-derived
// figure, and nothing the old factor never touched moves (Brussels keeps the factor migration 0061
// gave it).
func TestIntegration_Migration0360CorrectsAnAlreadySeededOldWorld(t *testing.T) {
	ctx := context.Background()
	pool := freshMigratedDB(t, "slabrestfix")
	applyDibbitsSeed(t, pool)
	plantOldWorld(t, pool)
	mustExec(t, ctx, pool, migration0360Body(t))

	noRowOutside(t, pool, "product factors still off their face value",
		fmt.Sprintf(productFactorMismatches, newFactorValues))
	wantCount(t, pool, "corrected product factors", 17,
		`SELECT count(*) FROM products p JOIN `+newFactorValues+` v(sku, want) ON p.sku = v.sku WHERE p.pieces_per_sf = v.want`)
	noRowOutside(t, pool, "SF uom rows still off their face value",
		fmt.Sprintf(uomFactorMismatches, newFactorValues))
	wantCount(t, pool, "corrected SF uom rows", 17,
		`SELECT count(*) FROM product_uoms pu JOIN products p ON p.id = pu.product_id
		  JOIN `+newFactorValues+` v(sku, want) ON p.sku = v.sku WHERE pu.uom_code = 'SF' AND pu.qty_in_base = v.want`)
	noRowOutside(t, pool, "SEEDED_DEFAULT SF to PLT edges still at the old derived factor",
		fmt.Sprintf(edgeFactorMismatches, newFactorValues))
	wantCount(t, pool, "recomputed SF to PLT edges", 17,
		`SELECT count(*) FROM pricing_uom_conversions c JOIN products p ON p.id = c.product_id
		  JOIN `+newFactorValues+` v(sku, want) ON p.sku = v.sku
		  JOIN product_uoms plt ON plt.product_id = p.id AND plt.uom_code = 'PLT'
		 WHERE c.from_uom = 'SF' AND c.to_uom = 'PLT' AND c.source = 'SEEDED_DEFAULT'
		   AND c.factor = ROUND(v.want / plt.qty_in_base, 8)`)

	var bluBase, magnumBase float64
	if err := pool.QueryRow(ctx, `SELECT qty_base::float8 FROM quote_lines WHERE quote_id = `+bluQuoteID+` AND line_no = 1`).
		Scan(&bluBase); err != nil || bluBase != 247.776 {
		t.Errorf("the seeded Blu 60 quote line must land at 247.776, got %v (err %v)", bluBase, err)
	}
	if err := pool.QueryRow(ctx, `SELECT qty_base::float8 FROM invoice_lines WHERE invoice_id = `+magnumInvoice+` AND line_no = 1`).
		Scan(&magnumBase); err != nil || magnumBase != 1548.45 {
		t.Errorf("the seeded Magnumstone invoice line must land at 1548.45, got %v (err %v)", magnumBase, err)
	}
	wantCount(t, pool, "Brussels keeps the factor 0061 gave it", 1,
		`SELECT count(*) FROM products WHERE sku = 'DBS-UL-BRUSSELS' AND pieces_per_sf = 4.10`)
}

// A value a person changed is never touched: three values no seed ever wrote survive 0360 while
// everything else in the old world is corrected around them.
func TestIntegration_Migration0360LeavesAChangedValueAlone(t *testing.T) {
	ctx := context.Background()
	pool := freshMigratedDB(t, "slabrestkeep")
	applyDibbitsSeed(t, pool)
	plantOldWorld(t, pool)
	mustExec(t, ctx, pool, `UPDATE products SET pieces_per_sf = `+changedUOM+`, updated_at = now()
		WHERE sku = `+anticoSKU)
	mustExec(t, ctx, pool, `UPDATE product_uoms pu SET qty_in_base = `+changedUOM+`, updated_at = now()
		FROM products p WHERE pu.product_id = p.id AND p.sku = `+anticoSKU+` AND pu.uom_code = 'SF'`)
	mustExec(t, ctx, pool, `UPDATE quote_lines SET qty_base = `+changedQtyBase+`
		 WHERE quote_id = `+bluQuoteID+` AND line_no = 1`)
	mustExec(t, ctx, pool, migration0360Body(t))

	wantCount(t, pool, "the changed product factor stands", 1,
		`SELECT count(*) FROM products WHERE sku = `+anticoSKU+` AND pieces_per_sf = `+changedUOM)
	wantCount(t, pool, "the changed SF uom row stands", 1,
		`SELECT count(*) FROM product_uoms pu JOIN products p ON p.id = pu.product_id
		 WHERE p.sku = `+anticoSKU+` AND pu.uom_code = 'SF' AND pu.qty_in_base = `+changedUOM)
	wantCount(t, pool, "the changed quote line stands", 1,
		`SELECT count(*) FROM quote_lines WHERE quote_id = `+bluQuoteID+` AND line_no = 1 AND qty_base = `+changedQtyBase)

	noRowOutside(t, pool, "the untouched sixteen: product factors still off",
		fmt.Sprintf(productFactorMismatches, newFactorValues)+` AND v.sku <> `+anticoSKU)
	wantCount(t, pool, "the sixteen untouched products corrected", 16,
		`SELECT count(*) FROM products p JOIN `+newFactorValues+` v(sku, want) ON p.sku = v.sku
		  WHERE p.pieces_per_sf = v.want AND v.sku <> `+anticoSKU)
	noRowOutside(t, pool, "the untouched sixteen: SF uom rows still off",
		fmt.Sprintf(uomFactorMismatches, newFactorValues)+` AND v.sku <> `+anticoSKU)
	wantCount(t, pool, "the sixteen untouched SF uom rows corrected", 16,
		`SELECT count(*) FROM product_uoms pu JOIN products p ON p.id = pu.product_id
		  JOIN `+newFactorValues+` v(sku, want) ON p.sku = v.sku
		 WHERE pu.uom_code = 'SF' AND pu.qty_in_base = v.want AND v.sku <> `+anticoSKU)
	wantCount(t, pool, "the Magnumstone invoice line corrected", 1,
		`SELECT count(*) FROM invoice_lines WHERE invoice_id = `+magnumInvoice+` AND line_no = 1 AND qty_base = 1548.45`)
	noRowOutside(t, pool, "the untouched sixteen: edges still at the old derived factor",
		fmt.Sprintf(edgeFactorMismatches, newFactorValues)+` AND v.sku <> `+anticoSKU)
}

// factorWorldSnapshot is every row the migration may write, as one ordered string list.
func factorWorldSnapshot(t *testing.T, pool *pgxpool.Pool) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT key || '=' || val FROM (
		  SELECT 'product:' || sku AS key, pieces_per_sf::text AS val
		    FROM products WHERE sku LIKE 'DBS-%'
		  UNION ALL
		  SELECT 'uom:' || p.sku || ':' || pu.uom_code, pu.qty_in_base::text
		    FROM product_uoms pu JOIN products p ON p.id = pu.product_id WHERE p.sku LIKE 'DBS-%'
		  UNION ALL
		  SELECT 'quoteline:' || ql.line_no, ql.qty_base::text
		    FROM quote_lines ql WHERE ql.quote_id = `+bluQuoteID+`
		  UNION ALL
		  SELECT 'invline:' || il.line_no, il.qty_base::text
		    FROM invoice_lines il WHERE il.invoice_id = `+magnumInvoice+`
		  UNION ALL
		  SELECT 'edge:' || p.sku, c.factor::text || '/' || c.source
		    FROM pricing_uom_conversions c JOIN products p ON p.id = c.product_id WHERE p.sku LIKE 'DBS-%'
		) s ORDER BY key`)
	if err != nil {
		t.Fatalf("snapshotting the factor world: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(out) < minHeldFactors {
		t.Fatalf("the snapshot carries %d rows, too empty to prove anything", len(out))
	}
	return out
}

// The migration is idempotent: a second run changes nothing.
func TestIntegration_Migration0360TwiceChangesNothing(t *testing.T) {
	ctx := context.Background()
	pool := freshMigratedDB(t, "slabrestidem")
	applyDibbitsSeed(t, pool)
	plantOldWorld(t, pool)
	mustExec(t, ctx, pool, migration0360Body(t))
	before := factorWorldSnapshot(t, pool)
	mustExec(t, ctx, pool, migration0360Body(t))
	after := factorWorldSnapshot(t, pool)

	if !slices.Equal(before, after) {
		for i := range min(len(before), len(after)) {
			if before[i] != after[i] {
				t.Fatalf("the second run moved %s from %s to %s",
					strings.SplitN(after[i], "=", 2)[0], before[i], after[i])
			}
		}
		t.Fatalf("the second run changed the row set: %d rows before, %d after", len(before), len(after))
	}
}
