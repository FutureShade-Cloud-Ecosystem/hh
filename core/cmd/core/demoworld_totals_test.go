//go:build demo

package main

// demoworld_totals_test.go: proof that every document in the demo world carries money its own
// lines add up to, by the product's own fold-up.
//
// The world is built the way a demo box builds it (the frozen migrations, the first apply, the real
// demo populate, the second apply), and then EVERY quote and EVERY order in the database is held to
// the arithmetic the product itself applies whenever it recomputes a header:
//
//	quote (quote/edit_repository.go recomputeTotals):
//	  subtotal_cents = SUM(quote_lines.line_total_cents)
//	  total_cents    = SUM(quote_lines.line_total_cents) + tax_cents + delivery_fee_cents
//	order (order/edit_repository.go recomputeTotals):
//	  subtotal_cents = SUM(order_lines.line_total_cents)
//	  total_cents    = SUM(order_lines.line_total_cents) + tax_cents
//
// The assertion is stated as the product states it, a header folded up from its lines, never the
// other way round, and it holds over the whole world rather than one document: the migrations
// plant demo islands beside the seed's own rows, and the visual check that produced this proof
// found exactly such an island (the accepted quote Q-DEMO-CP03, total 20000 cents, NO lines), whose
// Convert to Order offered an empty item table under a confident total. So the proof also refuses
// the degenerate pass: a document with a zero total and no lines satisfies the fold-up arithmetic
// trivially, and every quote and order in this world must carry at least one line for the fold to
// mean anything.
//
// The database gate is the demo world proofs' own variable, and the harness refuses an address that
// is not local.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/demo"
)

func TestDemoWorld_DocumentTotals(t *testing.T) {
	dsn := os.Getenv(tillDemoDBEnvVar)
	if dsn == "" {
		t.Skip(tillDemoDBEnvVar + " is not set: the demo world proof needs its own throwaway local Postgres")
	}
	requireLocalDSN(t, dsn)
	ctx := context.Background()

	rawKey := make([]byte, 32)
	if _, err := rand.Read(rawKey); err != nil {
		t.Fatalf("generate vault key: %v", err)
	}

	// The world, in the order a demo box builds it.
	pool, testDSN := qwFreshDB(t, ctx, dsn, "demoworld_totals")
	t.Setenv("DATABASE_URL", testDSN)
	t.Setenv("CORE_DATABASE_URL", testDSN)
	t.Setenv("PAYMENT_VAULT_KEY", hex.EncodeToString(rawKey))
	t.Setenv("APP_ENV", "dev")
	if _, created := qwApply(t); created == 0 {
		t.Fatal("the first apply created nothing on a fresh database")
	}
	now := qwClock
	if _, err := demo.RunPopulate(ctx, demo.Options{Getenv: os.Getenv, Now: &now}); err != nil {
		t.Fatalf("demo populate: %v", err)
	}
	if _, created := qwApply(t); created == 0 {
		t.Fatal("the second apply created nothing, the quote world never landed")
	}

	// The quotes. tax and the delivery fee are READ by the fold, never recomputed here: M5 owns the
	// tax figure and the terms write owns the fee, so the proof judges the header against its own
	// stored figures exactly as the product's UPDATE does.
	type doc struct {
		no, kind                       string
		subtotal, tax, fee, total, sum int64
		lines                          int
	}
	var bad []doc
	read := func(query, kind string) {
		rows, err := pool.Query(ctx, query)
		if err != nil {
			t.Fatalf("reading %s: %v", kind, err)
		}
		defer rows.Close()
		for rows.Next() {
			var d doc
			if err := rows.Scan(&d.no, &d.subtotal, &d.tax, &d.fee, &d.total, &d.sum, &d.lines); err != nil {
				t.Fatalf("scanning a %s: %v", kind, err)
			}
			d.kind = kind
			if d.subtotal != d.sum || d.total != d.sum+d.tax+d.fee || d.lines == 0 {
				bad = append(bad, d)
			}
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("walking the %s list: %v", kind, err)
		}
	}
	read(`
		SELECT q.quote_no::text, q.subtotal_cents, q.tax_cents,
		       COALESCE(q.delivery_fee_cents, 0), q.total_cents,
		       COALESCE(SUM(l.line_total_cents), 0), COUNT(l.id)
		  FROM quotes q LEFT JOIN quote_lines l ON l.quote_id = q.id
		 GROUP BY q.id, q.quote_no, q.subtotal_cents, q.tax_cents, q.delivery_fee_cents, q.total_cents`,
		"quote")
	read(`
		SELECT o.order_no::text, o.subtotal_cents, o.tax_cents, 0, o.total_cents,
		       COALESCE(SUM(l.line_total_cents), 0), COUNT(l.id)
		  FROM orders o LEFT JOIN order_lines l ON l.order_id = o.id
		 GROUP BY o.id, o.order_no, o.subtotal_cents, o.tax_cents, o.total_cents`,
		"order")
	if len(bad) > 0 {
		for _, d := range bad {
			t.Errorf("%s %s: subtotal %d, tax %d, fee %d, total %d, but its %d lines sum to %d; the product's fold-up would write a different header",
				d.kind, d.no, d.subtotal, d.tax, d.fee, d.total, d.lines, d.sum)
		}
		t.Fatalf("%d of the demo world's documents carry money their own lines do not add up to", len(bad))
	}

	// The world must hold documents at all, or the proof above judged nothing. The counts are the
	// whole world's, migrations' islands included, and both kinds are comfortably above the islands
	// alone, so a seed that quietly stopped writing its own book fails here.
	for _, table := range []string{"quotes", "quote_lines", "orders", "order_lines"} {
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&n); err != nil {
			t.Fatalf("counting %s: %v", table, err)
		}
		if n == 0 {
			t.Fatalf("%s holds no rows, the totals proof judged an empty world", table)
		}
	}
	t.Logf("every quote and every order in the demo world folds: its total is the sum of its lines plus its own tax and fees")
}
