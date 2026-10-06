//go:build integration

// hh204_terms_tax_integration_test.go, the terms write's flat-HST stamp against a LIVE Postgres.
//
// WHAT ONLY A DATABASE CAN PROVE HERE: the stamp's arithmetic must be INTEGER end to end. In
// Postgres SUM(bigint) returns numeric, so without a cast the (+ 5000 half-up guard) / 10000 is a
// NUMERIC division whose fraction the store into the bigint column rounds a SECOND time: one cent
// high whenever the true tax carries a fraction below a half, whole-cent bases included, because
// the guard itself supplies the spurious half. The fake-store proofs of the terms write never ran
// this SQL; these do.
//
// The shapes are the staging audit's observed subtotals plus the rounding-rule control: 3600 and
// 9000 (whole-cent tax), 34195 (fractional tax, half-up stays down), 3650 (an exact half, half-up
// rounds up: the RULE itself pinned, not just the defect), and 3600 + 900 delivery (the fee rides
// the tax base and the total fold-up).
//
// Run: DATABASE_URL=... go test -tags=integration ./internal/quote/ -count=1 -p 1
package quote

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// seedTermsQuote seeds a DRAFT quote in Trenton with ONE line of exactly totalCents, registered for
// cleanup, so the assertions below are about the stamp's arithmetic and not fixture archaeology.
func seedTermsQuote(t *testing.T, pool *pgxpool.Pool, totalCents int64) string {
	t.Helper()
	ctx := context.Background()
	prod, uom := anchoredProduct(t, pool)
	tag := fmt.Sprintf("HH204-%d", time.Now().UnixNano())

	var cust string
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name, customer_type) VALUES ($1,$2,'CONTRACTOR') RETURNING id::text`,
		tag, tag).Scan(&cust); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	var quoteID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO quotes (quote_no, customer_id, branch_id, status, valid_until,
		                    subtotal_cents, tax_cents, total_cents, created_by, salesperson_id)
		VALUES ($1, $2::uuid, $3::uuid, 'DRAFT', NULL, $4, 0, $4, $5, $5) RETURNING id::text`,
		"#Q-"+tag, cust, intTrenton, totalCents, seedUserID).Scan(&quoteID); err != nil {
		t.Fatalf("seed quote: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO quote_lines (quote_id, line_no, product_id, description, qty_entered, uom_entered,
		                         qty_base, unit_price_cents, price_uom, line_total_cents)
		VALUES ($1::uuid, 1, $2::uuid, 'HH-204 tax shape', 1, $3, 1, $4, $3, $4)`,
		quoteID, prod, uom, totalCents); err != nil {
		t.Fatalf("seed quote line: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM audit_log WHERE entity_id = $1`, quoteID)
		_, _ = pool.Exec(c, `DELETE FROM quote_lines WHERE quote_id = $1`, quoteID)
		_, _ = pool.Exec(c, `DELETE FROM quotes WHERE id = $1`, quoteID)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1`, cust)
	})
	return quoteID
}

// TestIntegration_TermsTaxRoundsHalfUpOnce stamps terms through the real service path (real guarded
// write, rate read from system_settings) and asserts the STORED row: exactly one rounding, the
// integer half-up, may ever touch the tax.
func TestIntegration_TermsTaxRoundsHalfUpOnce(t *testing.T) {
	pool := intPool(t)
	// The expected cents below are the arithmetic at the seeded rate; a dealer who retuned it owns
	// different numbers, so say so rather than fail mysteriously.
	var rateBps int64
	if err := pool.QueryRow(context.Background(),
		`SELECT value::bigint FROM system_settings WHERE key = 'hst_rate_bps'`).Scan(&rateBps); err != nil {
		t.Skipf("no hst_rate_bps seeded, these shapes assert its arithmetic: %v", err)
	}
	if rateBps != 1300 {
		t.Skipf("hst_rate_bps = %d, these shapes assert the arithmetic at the seeded 1300", rateBps)
	}

	// The service clock stays UNPINNED: this test asserts money and never a date (valid_until is
	// NULL, so expiry cannot arm), and a file that pins a calendar instant beside the real clock
	// that mints its row tags is the dated time bomb the testaudit tripwire refuses (DIB-427).
	now := time.Now()
	svc, _, _ := intSvc(t, pool, now)
	ctx := qCtxAs(intStaff(intTrenton))

	cases := []struct {
		name          string
		lineTotal     int64
		deliveryCents int64
		wantTax       int64
	}{
		{"whole-cent base 3600, the guard's half must not round it up", 3600, 0, 468},
		{"whole-cent base 9000, the guard's half must not round it up", 9000, 0, 1170},
		{"fractional base 34195, half-up stays down", 34195, 0, 4445},
		{"exact-half base 3650, half-up rounds up", 3650, 0, 475},
		{"delivery rides the base, 3600 plus a 900 fee (whole-cent tax 585)", 3600, 900, 585},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			quoteID := seedTermsQuote(t, pool, tc.lineTotal)

			in := TermsInput{DeliveryFeeCents: tc.deliveryCents}
			if tc.deliveryCents > 0 {
				in.Fulfillment = "DELIVERY" // a PICKUP strip clears the fee in validation
			}
			out, err := svc.UpdateTerms(ctx, quoteID, in)
			if err != nil {
				t.Fatalf("terms write: %v", err)
			}

			var sub, tax, total int64
			if err := pool.QueryRow(context.Background(),
				`SELECT subtotal_cents, tax_cents, total_cents FROM quotes WHERE id = $1`, quoteID).
				Scan(&sub, &tax, &total); err != nil {
				t.Fatalf("read stored totals: %v", err)
			}
			if sub != tc.lineTotal {
				t.Errorf("subtotal_cents = %d, want %d", sub, tc.lineTotal)
			}
			if tax != tc.wantTax {
				t.Errorf("tax_cents = %d, want %d: one integer half-up, never a second rounding",
					tax, tc.wantTax)
			}
			wantTotal := tc.lineTotal + tc.deliveryCents + tc.wantTax
			if total != wantTotal {
				t.Errorf("total_cents = %d, want %d (subtotal + delivery + tax)", total, wantTotal)
			}
			if out.TaxCents != tax || out.TotalCents != total {
				t.Errorf("returned terms (tax %d, total %d) disagree with the stored row (tax %d, total %d)",
					out.TaxCents, out.TotalCents, tax, total)
			}
		})
	}
}
