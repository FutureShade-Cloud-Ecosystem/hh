//go:build demo

package main

// demoworld_tax_agree_test.go, the demo dealer's stored documents, the recompute and the rates a
// person reads all agree at 7.75 percent (FROM-PRINCIPAL 48 b). Built the way a demo box builds, then:
//
//  1. every stored invoice, order and quote with a subtotal carries exactly the half up 7.75 percent
//     of that subtotal, and none carries the 13 percent figure; the only untaxed documents are the
//     contractor island's named ones (CP03), so a stray zero cannot hide behind "tax_cents <> 0";
//  2. the two deposit sales carry the 775 total, and their invoices owe total less the deposit;
//  3. every accepted quote's accepted tax equals the quote's tax;
//  4. a recompute over a W4 snapshot of every taxed order, written the way the ring writes one
//     (full tax at the standard rate, the rate stored beside it), reconstructs a base, binds 775 on
//     the dealer's own reason and lands on the order's stored tax to the cent;
//  5. the rates a person reads (the engine's standard, the quote header figure, the receipt header
//     figure) are 775.
//
// Counts are asserted so the legs cannot pass over an empty world.

import (
	"context"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/printing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/quote"
)

func TestDemoWorld_StoredDocumentsAndRecomputeAgreeAt775(t *testing.T) {
	ctx := context.Background()
	pool := glsBootWorld(t, ctx, "demoworld_tax_agree")

	const at775 = `(subtotal_cents * 775 + 5000) / 10000`
	const at1300 = `(subtotal_cents * 1300 + 5000) / 10000`

	// 1. Every stored document: the 775 figure, never the 13 percent one.
	for _, table := range []string{"invoices", "orders", "quotes"} {
		total := qwCount(t, ctx, pool, `SELECT count(*) FROM `+table+` WHERE subtotal_cents > 0`)
		if min := map[string]int{"invoices": 30, "orders": 45, "quotes": 5}[table]; total < min {
			t.Fatalf("%s: only %d documents with a subtotal, the world is not the world this leg proves", table, total)
		}
		off := qwCount(t, ctx, pool, `SELECT count(*) FROM `+table+` WHERE subtotal_cents > 0 AND tax_cents <> `+at775+` AND tax_cents <> 0`)
		if off != 0 {
			t.Errorf("%s: %d document(s) carry a tax that is not 7.75 percent of the subtotal", table, off)
		}
		at13 := qwCount(t, ctx, pool, `SELECT count(*) FROM `+table+` WHERE subtotal_cents > 100 AND tax_cents = `+at1300)
		if at13 != 0 {
			t.Errorf("%s: %d document(s) still carry the 13 percent figure", table, at13)
		}
		// The untaxed ones are named: the contractor island only.
		var stray int
		numberCol := map[string]string{"invoices": "invoice_no", "orders": "order_no", "quotes": "quote_no"}[table]
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE subtotal_cents > 0 AND tax_cents = 0 AND `+numberCol+` NOT LIKE '%CP03%'`).Scan(&stray); err != nil {
			t.Fatalf("%s untaxed: %v", table, err)
		}
		if stray != 0 {
			t.Errorf("%s: %d untaxed document(s) outside the contractor island", table, stray)
		}
		t.Logf("%s: %d documents with a subtotal, all at 7.75 percent (the contractor island's untaxed ones named apart)", table, total)
	}

	// 2. The deposit sales: the order and the invoice carry the 775 total, and the invoice owes total less the deposit.
	deposits := map[string]int64{"D01": 50_000, "D02": 2_000}
	for key, deposit := range deposits {
		var subtotal, taxCents, orderTotal, invTotal, balance int64
		if err := pool.QueryRow(ctx, `
			SELECT o.subtotal_cents, o.tax_cents, o.total_cents, i.total_cents, i.balance_cents
			  FROM orders o JOIN invoices i ON i.order_id = o.id
			 WHERE o.order_no LIKE '%-'||$1`, key).Scan(&subtotal, &taxCents, &orderTotal, &invTotal, &balance); err != nil {
			t.Fatalf("deposit sale %s: %v", key, err)
		}
		want := (subtotal*775 + 5000) / 10000
		if taxCents != want || orderTotal != subtotal+want || invTotal != orderTotal || balance != orderTotal-deposit {
			t.Errorf("deposit sale %s: tax %d (want %d), order total %d, invoice total %d, balance %d (want %d)",
				key, taxCents, want, orderTotal, invTotal, balance, orderTotal-deposit)
		}
	}

	// 3. An accepted quote's accepted tax is the quote's tax.
	if n := qwCount(t, ctx, pool, `SELECT count(*) FROM quote_acceptances a JOIN quotes q ON q.id = a.quote_id WHERE a.accepted_tax_cents <> q.tax_cents`); n != 0 {
		t.Errorf("%d accepted quote(s) accepted a tax other than the quote's", n)
	}

	// 4. The recompute: a W4 snapshot per taxed order, as the ring writes one, recomputed by the real engine.
	const window = "2087"
	if _, err := pool.Exec(ctx, `
		INSERT INTO tax_decision_snapshot (order_id, tax_reason_code, rule_version, rate_bps, full_hst_cents, net_collected_cents, automated, standard_rate_bps, created_at)
		SELECT id, 'STD_SALES_TAX', 'W4-flat-13', 775, tax_cents, tax_cents, false, 775, TIMESTAMPTZ '`+window+`-05-01 12:00:00+00'
		  FROM orders WHERE subtotal_cents > 0 AND tax_cents > 0`); err != nil {
		t.Fatalf("planting the W4 snapshots: %v", err)
	}
	planted := qwCount(t, ctx, pool, `SELECT count(*) FROM tax_decision_snapshot WHERE created_at >= TIMESTAMPTZ '`+window+`-01-01 00:00:00+00'`)
	if planted < 20 {
		t.Fatalf("only %d snapshots planted", planted)
	}
	engine := newTaxEngine(pool)
	res, err := engine.RecomputeReport(ctx, window+"-01-01", window+"-12-31")
	if err != nil {
		t.Fatalf("the recompute: %v", err)
	}
	if res.Scanned != planted || res.Inserted != planted {
		t.Fatalf("recompute scanned %d inserted %d, want %d of each", res.Scanned, res.Inserted, planted)
	}
	// Every recomputed row binds 775 on the dealer's reason, stores 775 as its standard, and lands on the order's tax.
	if bad := qwCount(t, ctx, pool, `
		SELECT count(*) FROM tax_decision_snapshot m JOIN orders o ON o.id = m.order_id
		 WHERE m.rule_version = 'M5-recompute' AND m.created_at >= now() - interval '1 hour'
		   AND (m.rate_bps <> 775 OR m.standard_rate_bps <> 775 OR m.tax_reason_code <> 'STD_SALES_TAX'
		        OR m.net_collected_cents <> o.tax_cents OR m.full_hst_cents <> o.tax_cents)`); bad != 0 {
		t.Errorf("%d recomputed row(s) disagree with the order's stored tax at 775", bad)
	}
	if n := qwCount(t, ctx, pool, `SELECT count(*) FROM tax_decision_snapshot WHERE rule_version = 'M5-recompute' AND created_at >= now() - interval '1 hour'`); n != planted {
		t.Errorf("%d recomputed rows, want %d", n, planted)
	}
	// And the recompute's own base reconstruction: a row written at 775 is not divided by 1300.
	if _, err := pool.Exec(ctx, `DELETE FROM tax_decision_snapshot WHERE created_at >= TIMESTAMPTZ '`+window+`-01-01 00:00:00+00' OR (rule_version = 'M5-recompute' AND created_at >= now() - interval '1 hour')`); err != nil {
		t.Fatalf("cleaning the snapshots: %v", err)
	}

	// 5. The rates a person reads.
	if got, err := engine.StandardRateBps(ctx, ""); err != nil || got != 775 {
		t.Errorf("the engine's standard rate is %d (err %v), want 775", got, err)
	}
	if got := quote.NewRepository(pool).ReadHSTRateBps(ctx); got != 775 {
		t.Errorf("the quote header's rate is %d, want 775", got)
	}
	head, err := printing.NewRepository(pool).ReadBranchHeader(ctx, trentonBranchID)
	if err != nil || head.HSTRateBps != 775 {
		t.Errorf("the receipt header's rate is %d (err %v), want 775", head.HSTRateBps, err)
	}
}
