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
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pos"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/quote"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/settings"
)

func TestDemoWorld_SalesTaxIsSanDiegoCounty(t *testing.T) {
	ctx := context.Background()
	pool := glsBootWorld(t, ctx, "demoworld_tax")

	// The dealer's own configuration, read through the settings seam every module reads it by.
	cfg, err := settings.NewDealerConfigReader(pool).DealerConfig(ctx)
	if err != nil {
		t.Fatalf("the dealer configuration: %v", err)
	}
	want := settings.DealerConfig{Currency: "USD", TaxRegion: "CA", BusinessZone: "America/Los_Angeles", HSTRateBps: 775, Country: "US"}
	if cfg != want {
		t.Errorf("the dealer configuration reads %+v, want %+v", cfg, want)
	}

	// The rate rows: the California standard and manual review rows at 775, on their own reason codes,
	// and the Ontario rows left exactly as the migrations plant them (inert for this dealer, never edited).
	for _, row := range []struct{ reason string }{{"STD_SALES_TAX"}, {"MANUAL_TAX_REVIEW"}} {
		if n := qwCount(t, ctx, pool, `SELECT count(*) FROM tax_rates WHERE jurisdiction = 'CA' AND reason_code = $1 AND rate_bps = 775 AND effective_to IS NULL`, row.reason); n != 1 {
			t.Errorf("tax_rates CA %s: %d row(s) at 775, want 1", row.reason, n)
		}
	}
	for _, reason := range []string{"STD_ON_HST_13", "MANUAL_TAX_REVIEW"} {
		if n := qwCount(t, ctx, pool, `SELECT count(*) FROM tax_rates WHERE jurisdiction = 'ON' AND reason_code = $1 AND rate_bps = 1300`, reason); n != 1 {
			t.Errorf("tax_rates ON %s: %d row(s) at 1300, want the migration's row untouched", reason, n)
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

	// The real engine, the same assessor adapters the composition wires, bound to the dealer's region.
	taxSvc := newTaxEngine(pool)
	ring, err := posTaxAdapter{svc: taxSvc}.AssessSale(ctx, pos.TaxQuoteInput{BranchID: trentonBranchID, SubtotalCents: 10000})
	if err != nil || ring.RateBps != 775 || ring.TaxCents != 775 || ring.ReasonCode != "STD_SALES_TAX" || ring.Jurisdiction != "CA" || ring.StandardRateBps != 775 {
		t.Errorf("a standard sale rung through the engine: %+v (err %v), want 775 bps, 775 cents on 10000, STD_SALES_TAX in CA", ring, err)
	}
	inv, err := w6cInvoiceTaxAdapter{svc: taxSvc}.AssessSale(ctx, billing.TaxQuoteInput{BranchID: trentonBranchID, SubtotalCents: 100})
	if err != nil || inv.RateBps != 775 || inv.TaxCents != 8 {
		t.Errorf("an invoice through the engine: %+v (err %v), want 775 bps and 8 cents on 100", inv, err)
	}
	if got := quote.NewRepository(pool).ReadHSTRateBps(ctx); got != 775 {
		t.Errorf("a quote header's settings figure is %d bps, want 775", got)
	}
	if got, err := taxSvc.StandardRateBps(ctx, ""); err != nil || got != 775 {
		t.Errorf("the dealer's standard rate through the engine is %d (err %v), want 775", got, err)
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
		`UPDATE tax_rates SET rate_bps = 800 WHERE jurisdiction = 'CA' AND reason_code = 'STD_SALES_TAX'`,
		`UPDATE system_settings SET value = '800'::jsonb WHERE key = 'hst_rate_bps'`,
		`UPDATE system_settings SET value = '"CAD"'::jsonb WHERE key = 'currency'`,
		`UPDATE gl_accounts SET name = 'HST Payable' WHERE code = '2200'`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("a tester's change %q: %v", q, err)
		}
	}
	qwApply(t) // the boot's next apply on a built box
	if n := qwCount(t, ctx, pool, `SELECT count(*) FROM tax_rates WHERE jurisdiction = 'CA' AND reason_code = 'STD_SALES_TAX' AND rate_bps = 800`); n != 1 {
		t.Errorf("the apply moved a tester's standard rate")
	}
	if n := qwCount(t, ctx, pool, `SELECT count(*) FROM system_settings WHERE (key = 'hst_rate_bps' AND value = '800'::jsonb) OR (key = 'currency' AND value = '"CAD"'::jsonb)`); n != 2 {
		t.Errorf("the apply moved a tester's settings")
	}
	if n := qwCount(t, ctx, pool, `SELECT count(*) FROM gl_accounts WHERE code = '2200' AND name = 'HST Payable'`); n != 1 {
		t.Errorf("the apply renamed a tester's ledger account")
	}
}
