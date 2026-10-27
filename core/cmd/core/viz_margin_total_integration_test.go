//go:build integration

// viz_margin_total_integration_test.go, PR 608 round 5 (FROM-PRINCIPAL 132): the all branches
// gross margin is the WEIGHTED ratio, total margin cents over total sales cents across the
// branches, never a sum and never a mean of the branch rates. The leg seeds two branches with
// different sales and the same product costs, asks the composed chart read for the margin by
// branch, and pins the tile's total:
//
//	Trenton: revenue 100000, cost 80000 -> 20.00% (2000 bps)
//	Kingston: revenue  10000, cost  5000 -> 50.00% (5000 bps)
//	weighted: 25000 / 110000            -> 22.72% (2272 bps)
//	sum: 7000, mean: 3500 - both wrong, both different from the answer
//
// Red on main: the service totals a series with TotalOf, a plain sum, so the tile answered 7000.
package main

import (
	"context"
	"fmt"
	"math"
	"os"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/dashboard"
	"github.com/jackc/pgx/v5/pgxpool"
)

func vizIntPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		url = "postgres://hardscape:hardscape@localhost:5435/hardscapeos?sslmode=disable"
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("integration DB unavailable: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		t.Fatalf("integration DB not reachable: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestIntegration_VizMarginTotal_IsTheWeightedRatio(t *testing.T) {
	ctx := context.Background()
	pool := vizIntPool(t)

	// Two PURPOSE-MADE branches, so the series sees exactly this leg's orders and no seeded
	// yard's history: the arithmetic under assertion is the point, not the demo world's.
	big, small := "b5e10000-0000-4000-8000-0000000000a1", "b5e10000-0000-4000-8000-0000000000a2"
	for i, b := range []struct{ id, code string }{{big, "R5A"}, {small, "R5B"}} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO locations (id, branch_id, parent_id, loc_type, code, name)
			 VALUES ($1::uuid, $1::uuid, NULL, 'BRANCH', $2, $3)`,
			b.id, b.code, fmt.Sprintf("Round 5 margin branch %d", i+1)); err != nil {
			t.Fatalf("seed branch %s: %v", b.code, err)
		}
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM locations WHERE id IN ($1::uuid, $2::uuid)`, big, small)
	})

	var product string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM products ORDER BY id LIMIT 1`).Scan(&product); err != nil {
		t.Fatalf("pick a seeded product: %v", err)
	}
	var customer string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM customers ORDER BY id LIMIT 1`).Scan(&customer); err != nil {
		t.Fatalf("pick a seeded customer: %v", err)
	}
	stamp := time.Now().UnixNano()

	seed := func(branch string, qty int, unit, cost int64) string {
		t.Helper()
		var ord string
		if err := pool.QueryRow(ctx,
			`INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, subtotal_cents, total_cents)
			 VALUES ($1, $2::uuid, $3::uuid, 'CONFIRMED', 'PICKUP', $4, $4)
			 RETURNING id::text`,
			fmt.Sprintf("#R5M-%d-%s", stamp, branch), customer, branch, int64(qty)*unit).Scan(&ord); err != nil {
			t.Fatalf("seed order in %s: %v", branch, err)
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO order_lines (order_id, line_no, product_id, description, qty_entered, uom_entered,
			                          qty_base, unit_price_cents, price_uom, line_total_cents)
			 SELECT $1::uuid, 1, $2::uuid, 'R5 margin line', $3, p.base_uom, $3, $4, p.base_uom, $5
			   FROM products p WHERE p.id = $2::uuid`, ord, product, qty, unit, int64(qty)*unit); err != nil {
			t.Fatalf("seed line in %s: %v", branch, err)
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO product_branch_costs (product_id, branch_id, avg_unit_cost_cents)
			 VALUES ($1::uuid, $2::uuid, $3)
			 ON CONFLICT (product_id, branch_id) DO UPDATE SET avg_unit_cost_cents = EXCLUDED.avg_unit_cost_cents`,
			product, branch, cost); err != nil {
			t.Fatalf("seed cost in %s: %v", branch, err)
		}
		return ord
	}
	trt := seed(big, 100, 1000, 800)   // revenue 100000, cost 80000 -> 20.00%
	kgn := seed(small, 10, 1000, 500)  // revenue  10000, cost  5000 -> 50.00%
	t.Cleanup(func() {
		c := context.Background()
		for _, ord := range []string{trt, kgn} {
			_, _ = pool.Exec(c, `DELETE FROM order_lines WHERE order_id = $1::uuid`, ord)
			_, _ = pool.Exec(c, `DELETE FROM orders WHERE id = $1::uuid`, ord)
		}
		_, _ = pool.Exec(c, `DELETE FROM product_branch_costs WHERE product_id = $1::uuid AND branch_id IN ($2::uuid, $3::uuid)`,
			product, big, small)
	})

	svc := dashboard.NewService(dashboard.NewRepository(pool), nil)
	if err := svc.RegisterSeries(dashboard.MetricGrossMargin, vizGrossMargin(pool)); err != nil {
		t.Fatalf("register the margin series: %v", err)
	}
	tiles := svc.ResolveViz(ctx, dashboard.Scope{BranchIDs: []string{big, small}, CostAllowed: true},
		[]string{"viz.margin.gross_bps-branch-list"})
	if len(tiles) != 1 {
		t.Fatalf("the margin tile is absent: %+v", tiles)
	}
	tt := tiles[0]
	if tt.State != dashboard.StateOK {
		t.Fatalf("the margin tile did not measure: %s %s", tt.State, tt.Note)
	}
	if len(tt.Points) != 2 {
		t.Fatalf("two branch points, got %+v", tt.Points)
	}
	if tt.Total == nil {
		t.Fatalf("the margin tile carries no total at all: %+v", tt)
	}
	// The weighted ratio: 25000 margin cents on 110000 sales cents, x10000 -> 2272.727, rounded
	// to 2273 by the same float division the per branch arms run. The sum (7000) and the mean
	// (3500) are both wrong and both different.
	want := int64(math.Round(25000 * 10000.0 / 110000))
	if *tt.Total != want {
		t.Fatalf("the all branches gross margin is the weighted ratio %d bps, got %d (points %+v)", want, *tt.Total, tt.Points)
	}
	if *tt.Total == 7000 || *tt.Total == 3500 {
		t.Fatalf("the total must never be the sum or the mean of the branch rates: %d", *tt.Total)
	}
}

// TestIntegration_VizARAgingBranch_IsPastDue pins the receivables card's branch rows: the ar.aging
// branch arm is PAST DUE per branch, the same cut the bucket arm's non-Current bands make, so the
// rows tie with the card's own headline arithmetic (every bucket but Current).
func TestIntegration_VizARAgingBranch_IsPastDue(t *testing.T) {
	ctx := context.Background()
	pool := vizIntPool(t)

	big, small := "b5e10000-0000-4000-8000-0000000000b1", "b5e10000-0000-4000-8000-0000000000b2"
	for i, b := range []struct{ id, code string }{{big, "R5C"}, {small, "R5D"}} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO locations (id, branch_id, parent_id, loc_type, code, name)
			 VALUES ($1::uuid, $1::uuid, NULL, 'BRANCH', $2, $3)`,
			b.id, b.code, fmt.Sprintf("Round 5 aging branch %d", i+1)); err != nil {
			t.Fatalf("seed branch %s: %v", b.code, err)
		}
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM locations WHERE id IN ($1::uuid, $2::uuid)`, big, small)
	})
	var customer string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM customers ORDER BY id LIMIT 1`).Scan(&customer); err != nil {
		t.Fatalf("pick a seeded customer: %v", err)
	}
	stamp := time.Now().UnixNano()
	seed := func(branch, due string, cents int64) string {
		t.Helper()
		var inv string
		if err := pool.QueryRow(ctx,
			`INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
			                       subtotal_cents, tax_cents, total_cents, balance_cents, source_kind)
			 VALUES ($1, $2::uuid, $3::uuid, 'FINALIZED', CURRENT_DATE, $4::date, $5, 0, $5, $5, 'ORDER')
			 RETURNING id::text`,
			fmt.Sprintf("R5AG-%d-%s", stamp, branch), customer, branch, due, cents).Scan(&inv); err != nil {
			t.Fatalf("seed invoice %s: %v", due, err)
		}
		return inv
	}
	a := seed(big, "2020-01-01", 30000)  // long past due
	b := seed(small, "2099-01-01", 40000) // current, excluded
	t.Cleanup(func() {
		c := context.Background()
		for _, inv := range []string{a, b} {
			_, _ = pool.Exec(c, `DELETE FROM invoice_lines WHERE invoice_id = $1::uuid`, inv)
			_, _ = pool.Exec(c, `DELETE FROM invoices WHERE id = $1::uuid`, inv)
		}
	})

	svc := dashboard.NewService(dashboard.NewRepository(pool), nil)
	if err := svc.RegisterSeries(dashboard.MetricARBalance, vizARAging(pool)); err != nil {
		t.Fatalf("register the aging series: %v", err)
	}
	tiles := svc.ResolveViz(ctx, dashboard.Scope{BranchIDs: []string{big, small}},
		[]string{"viz.ar.aging-branch-split"})
	if len(tiles) != 1 || tiles[0].State != dashboard.StateOK {
		t.Fatalf("the aging branch tile did not measure: %+v", tiles)
	}
	got := map[string]int64{}
	for _, p := range tiles[0].Points {
		got[p.Label] = p.Number
	}
	if got["R5C"] != 30000 {
		t.Fatalf("branch R5C past due is 30000, got %+v", got)
	}
	if v, ok := got["R5D"]; ok && v != 0 {
		t.Fatalf("branch R5D holds only a current invoice and must show 0 past due, got %d", v)
	}
}
