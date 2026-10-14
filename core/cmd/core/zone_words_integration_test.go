//go:build integration

// zone_words_integration_test.go, the dealer's tax word and the dealer's business day zone hold together
// on the composition root's own writer and credit memo poster: a California dealer's credit memo is
// filed on the Pacific day AND its reversing tax leg reads "Sales tax reversed".
package main

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/settings"
)

type zoneWordsFixedTax struct{}

func (zoneWordsFixedTax) AssessSale(context.Context, billing.TaxQuoteInput) (billing.TaxQuoteResult, error) {
	return billing.TaxQuoteResult{TaxCents: 194, RateBps: 775}, nil
}

func TestIntegration_CaliforniaCreditMemoCarriesTheWordAndThePacificDay(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)

	reader := settings.NewDealerConfigReader(pool)
	orig, err := reader.DealerConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cal := orig
	cal.Country, cal.TaxRegion, cal.Currency, cal.BusinessZone = "US", "CA", "USD", "America/Los_Angeles"
	if err := reader.WriteDealerConfig(ctx, cal); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.WriteDealerConfig(context.Background(), orig) })

	const branch = "5d1b3f6e-0000-4000-9000-000000000001"
	tag := fmt.Sprintf("zw-%d", rand.Int63n(1_000_000_000))
	var cust string
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name, customer_type, home_branch_id, credit_limit_cents)
		 VALUES ($1, $2, 'CONTRACTOR', $3::uuid, 0) RETURNING id::text`,
		"ZW-"+tag, "Zone Words "+tag, branch).Scan(&cust); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM customer_transactions WHERE customer_id = $1`, cust)
		_, _ = pool.Exec(c, `DELETE FROM invoice_lines WHERE invoice_id IN (SELECT id FROM invoices WHERE customer_id = $1)`, cust)
		_, _ = pool.Exec(c, `DELETE FROM invoices WHERE customer_id = $1`, cust)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1`, cust)
	})

	// 05:00 UTC on the 27th is still the evening of the 26th in Los Angeles.
	at := time.Date(2026, 8, 27, 5, 0, 0, 0, time.UTC)
	glSvc := gl.NewService(gl.NewRepository(pool).WithZone(newDealerZone(pool))).WithTaxLabel(newTaxWords(pool))
	w := newInvoiceWriter(pool).
		WithTaxAssessor(zoneWordsFixedTax{}).
		WithCreditMemoPoster(w6cCreditMemoPoster{svc: glSvc, rules: gl.NewRepository(pool), words: newTaxWords(pool)}).
		WithClock(func() time.Time { return at })
	doc, err := w.IssueCreditMemo(ctx, billing.CreditMemoInput{
		CustomerID: cust, BranchID: branch, AmountCents: 2_500, Memo: "zone words",
		IdempotencyKey: "zone-words-" + tag,
	})
	if err != nil {
		t.Fatalf("raise the credit memo: %v", err)
	}
	var issue string
	if err := pool.QueryRow(ctx, `SELECT issue_date::text FROM invoices WHERE id = $1::uuid`, doc.InvoiceID).Scan(&issue); err != nil {
		t.Fatal(err)
	}
	if issue != "2026-08-26" {
		t.Fatalf("issue date = %s, want the Pacific day 2026-08-26", issue)
	}
	rows, err := pool.Query(ctx,
		`SELECT l.memo FROM gl_journals j JOIN gl_journal_lines l ON l.journal_id = j.id
		  WHERE j.source_ref_id = $1::uuid`, doc.InvoiceID)
	if err != nil {
		t.Fatalf("read the reversing legs: %v", err)
	}
	defer rows.Close()
	var sawWord, sawHST bool
	for rows.Next() {
		var memo string
		if err := rows.Scan(&memo); err != nil {
			t.Fatal(err)
		}
		sawWord = sawWord || memo == "Sales tax reversed"
		sawHST = sawHST || memo == "HST reversed"
	}
	if !sawWord || sawHST {
		t.Fatalf("tax leg memo: Sales tax reversed seen %v, HST reversed seen %v", sawWord, sawHST)
	}
}
