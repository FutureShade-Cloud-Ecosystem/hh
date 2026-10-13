//go:build demo

package main

// demoworld_tax_test.go, the demo world's money configuration on a booted box: US dollars, the
// San Diego County combined sales tax at 7.75 percent, and no Canadian tax word in the rows a
// person reads. Built the way a demo box builds (marker, migrate, apply, populate, apply), then
// held to the rows and to the real engine: a standard sale, an invoice and a quote all bind 775,
// and every seeded document carries the tax the engine would have written.

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pos"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/quote"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/tax"
)

func TestDemoWorld_SalesTaxIsSanDiegoCounty(t *testing.T) {
	ctx := context.Background()
	pool := glsBootWorld(t, ctx, "demoworld_tax")

	setting := func(key string) string {
		t.Helper()
		var v string
		if err := pool.QueryRow(ctx, `SELECT value::text FROM system_settings WHERE key = $1`, key).Scan(&v); err != nil {
			t.Fatalf("system_settings %s: %v", key, err)
		}
		return strings.Trim(v, `"`)
	}
	if got := setting("currency"); got != "USD" {
		t.Errorf("system_settings currency reads %q, want USD", got)
	}
	if got := setting("tax_region"); got != "CA" {
		t.Errorf("system_settings tax_region reads %q, want CA", got)
	}
	if got := setting("hst_rate_bps"); got != "775" {
		t.Errorf("system_settings hst_rate_bps reads %q, want 775", got)
	}

	// The rate rows: the standard and the manual review row at 775, the relief rows untouched.
	for _, code := range []string{"STD_ON_HST_13", "MANUAL_TAX_REVIEW"} {
		if n := qwCount(t, ctx, pool, `SELECT count(*) FROM tax_rates WHERE reason_code = $1 AND rate_bps = 775`, code); n != 1 {
			t.Errorf("tax_rates %s: %d row(s) at 775, want 1", code, n)
		}
		if n := qwCount(t, ctx, pool, `SELECT count(*) FROM tax_rates WHERE reason_code = $1 AND rate_bps <> 775`, code); n != 0 {
			t.Errorf("tax_rates %s: %d row(s) not at 775", code, n)
		}
	}

	// No Canadian tax word in the rows a person reads on the tax admin and the ledger screens.
	canadian := regexp.MustCompile(`(?i)\bHST\b|\bGST\b|\bPST\b|ontario|on-reserve|reserve|federal status|first nations|\bCAD\b|\bON\b|\bFN\b`)
	for _, q := range []struct{ what, sql string }{
		{"tax_status_card_profiles", `SELECT name || ' ' || notes FROM tax_status_card_profiles`},
		{"gl_accounts", `SELECT name FROM gl_accounts`},
		{"tax_rates notes", `SELECT notes FROM tax_rates`},
	} {
		rows, err := pool.Query(ctx, q.sql)
		if err != nil {
			t.Fatalf("%s: %v", q.what, err)
		}
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				t.Fatalf("%s: %v", q.what, err)
			}
			if canadian.MatchString(s) {
				t.Errorf("%s still reads a Canadian tax word: %q", q.what, s)
			}
		}
		rows.Close()
	}
	var payable string
	if err := pool.QueryRow(ctx, `SELECT name FROM gl_accounts WHERE code = '2200'`).Scan(&payable); err != nil || payable != "Sales tax payable" {
		t.Errorf("gl account 2200 reads %q (err %v), want Sales tax payable", payable, err)
	}

	// The real engine, the same assessor adapters the composition wires.
	taxSvc := tax.NewService(tax.NewRepository(pool))
	ring, err := posTaxAdapter{svc: taxSvc}.AssessSale(ctx, pos.TaxQuoteInput{BranchID: trentonBranchID, SubtotalCents: 10000})
	if err != nil || ring.RateBps != 775 || ring.TaxCents != 775 {
		t.Errorf("a standard sale rung through the engine: %+v (err %v), want 775 bps and 775 cents on 10000", ring, err)
	}
	inv, err := w6cInvoiceTaxAdapter{svc: taxSvc}.AssessSale(ctx, billing.TaxQuoteInput{BranchID: trentonBranchID, SubtotalCents: 100})
	if err != nil || inv.RateBps != 775 || inv.TaxCents != 8 {
		t.Errorf("an invoice through the engine: %+v (err %v), want 775 bps and 8 cents on 100", inv, err)
	}
	if got := quote.NewRepository(pool).ReadHSTRateBps(ctx); got != 775 {
		t.Errorf("a quote header binds %d bps, want 775", got)
	}

	// Every taxed document in the world carries the tax the engine writes at 775, half up once. The
	// contractor island (CP03) is planted untaxed, which is no rate's figure, and stays as it is.
	for _, table := range []string{"invoices", "orders", "quotes"} {
		q := `SELECT count(*) FROM ` + table + ` WHERE tax_cents <> 0 AND tax_cents <> (subtotal_cents * 775 + 5000) / 10000`
		if n := qwCount(t, ctx, pool, q); n != 0 {
			t.Errorf("%d %s row(s) carry tax that is not 7.75 percent of their subtotal", n, table)
		}
	}
}

// TestDemoWorld_BuiltBoxKeepsATestersTaxRows: the convergence runs while the world is being built
// and never on a built box. A tester who changes the rate or the settings keeps the change across
// the boot's next apply.
func TestDemoWorld_BuiltBoxKeepsATestersTaxRows(t *testing.T) {
	ctx := context.Background()
	pool := glsBootWorld(t, ctx, "demoworld_tax_tester")
	for _, q := range []string{
		`UPDATE tax_rates SET rate_bps = 800 WHERE reason_code = 'STD_ON_HST_13'`,
		`UPDATE system_settings SET value = '800'::jsonb WHERE key = 'hst_rate_bps'`,
		`UPDATE system_settings SET value = '"CAD"'::jsonb WHERE key = 'currency'`,
		`UPDATE gl_accounts SET name = 'HST Payable' WHERE code = '2200'`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("a tester's change %q: %v", q, err)
		}
	}
	qwApply(t) // the boot's next apply on a built box
	if n := qwCount(t, ctx, pool, `SELECT count(*) FROM tax_rates WHERE reason_code = 'STD_ON_HST_13' AND rate_bps = 800`); n != 1 {
		t.Errorf("the apply moved a tester's standard rate")
	}
	if n := qwCount(t, ctx, pool, `SELECT count(*) FROM system_settings WHERE (key = 'hst_rate_bps' AND value = '800'::jsonb) OR (key = 'currency' AND value = '"CAD"'::jsonb)`); n != 2 {
		t.Errorf("the apply moved a tester's settings")
	}
	if n := qwCount(t, ctx, pool, `SELECT count(*) FROM gl_accounts WHERE code = '2200' AND name = 'HST Payable'`); n != 1 {
		t.Errorf("the apply renamed a tester's ledger account")
	}
}
