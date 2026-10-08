//go:build integration

// deposit_release_integration_test.go, the deposit release against the real
// database: the whole life of a supplier deposit, from its document's own
// finalization (DR 1100 / CR 2300, the 0373 rule) through the rest's invoice
// (the ordinary revenue and tax legs) to the release when the special order
// is fulfilled (DR 2300 / CR 4000). After all three journals the books
// balance, 2300 Customer Deposits has returned to zero, and the revenue
// recognised for the sale equals its pre-tax total.
//
// ISOLATION. Fiscal year 2086, outside the seeded 2026 periods and every band
// the package's other integration tests use.
//
//	Run: DATABASE_URL='postgres://.../db?sslmode=disable' go test -tags=integration -count=1 -run DepositRelease ./internal/gl/
package gl

import (
	"context"
	"fmt"
	"testing"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5/pgxpool"
)

const depositReleaseYear = 2086

// seedInvoiceReader reads the invoice facts straight off the pool, the same
// projection billing's InvoiceForGL answers (gl_seam.go). gl's own tests
// cannot import billing's repository for the wiring (billing imports this
// package, the cycle is real), and the live seam is proven end to end by the
// order package's release leg, which wires the real billing repository.
type seedInvoiceReader struct{ pool *pgxpool.Pool }

func (r seedInvoiceReader) InvoiceForGL(ctx context.Context, invoiceID string) (InvoiceAmounts, error) {
	var a InvoiceAmounts
	err := r.pool.QueryRow(ctx,
		`SELECT subtotal_cents, tax_cents, total_cents, holdback_cents,
		        branch_id::text, customer_id::text, invoice_no::text,
		        COALESCE(finalized_at, created_at), source_kind
		 FROM invoices WHERE id = $1`, invoiceID,
	).Scan(&a.SubtotalCents, &a.TaxCents, &a.TotalCents, &a.HoldbackCents,
		&a.BranchID, &a.CustomerID, &a.InvoiceNo, &a.FinalizedAt, &a.SourceKind)
	if err != nil {
		return InvoiceAmounts{}, fmt.Errorf("seed reader: invoice %s: %w", invoiceID, err)
	}
	return a, nil
}

// accountNet sums debit minus credit for one account over the year's journals.
// A leg carries exactly one of debit/credit non-null (the 0007 CHECK), so the
// sum coalesces each side or every row would read as nothing.
func accountNet(t *testing.T, pool *pgxpool.Pool, year int, code string) int64 {
	t.Helper()
	var net int64
	if err := pool.QueryRow(context.Background(), `
		SELECT COALESCE(SUM(COALESCE(l.debit_cents,0) - COALESCE(l.credit_cents,0)), 0)
		  FROM gl_journal_lines l
		  JOIN gl_journals j ON j.id = l.journal_id
		  JOIN gl_accounts a ON a.id = l.account_id
		 WHERE j.fiscal_year = $1 AND a.code = $2`, year, code).Scan(&net); err != nil {
		t.Fatalf("account net %s: %v", code, err)
	}
	return net
}

func TestIntegration_DepositRelease_TheWholeLifeOfADeposit(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	ensurePeriods(t, pool, depositReleaseYear)
	at := time.Date(depositReleaseYear, 6, 15, 12, 0, 0, 0, time.UTC)
	svc := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager}, at).
		WithInvoiceReader(seedInvoiceReader{pool: pool})

	// The sale's own shape: a 4000 deposit (its own FINALIZED document, the
	// SUPPLIER_DEPOSIT source kind) and the rest (7300 pre-tax + 949 HST),
	// the two documents the ring writes for a credit customer's special order.
	suffix := time.Now().UnixNano()
	var cust, branch string
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name, customer_type, payment_terms_days, credit_limit_cents)
		 VALUES ($1, 'Deposit release proof', 'CONTRACTOR', 30, 5000000) RETURNING id::text`,
		fmt.Sprintf("DR-%d", suffix)).Scan(&cust); err != nil {
		t.Fatalf("plant the customer: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations LIMIT 1`).Scan(&branch); err != nil {
		t.Skipf("no seeded branch: %v", err)
	}
	var ord string
	if err := pool.QueryRow(ctx,
		`INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, subtotal_cents, total_cents)
		 VALUES ($1, $2, $3, 'CONFIRMED', 'PICKUP', 11300, 12249) RETURNING id::text`,
		fmt.Sprintf("#DR-%d", suffix), cust, branch).Scan(&ord); err != nil {
		t.Fatalf("plant the order: %v", err)
	}
	var dep, rest string
	if err := pool.QueryRow(ctx,
		`INSERT INTO invoices (invoice_no, customer_id, order_id, branch_id, status, due_date,
		                       subtotal_cents, tax_cents, total_cents, balance_cents, source_kind, finalized_at)
		 VALUES ($1, $2, $3, $4, 'FINALIZED', CURRENT_DATE, 4000, 0, 4000, 4000, 'SUPPLIER_DEPOSIT', $5)
		 RETURNING id::text`,
		fmt.Sprintf("INV-DRD-%d", suffix), cust, ord, branch, at).Scan(&dep); err != nil {
		t.Fatalf("plant the deposit document: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO invoices (invoice_no, customer_id, order_id, branch_id, status, due_date,
		                       subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at)
		 VALUES ($1, $2, $3, $4, 'FINALIZED', CURRENT_DATE, 7300, 949, 8249, 8249, $5)
		 RETURNING id::text`,
		fmt.Sprintf("INV-DRR-%d", suffix), cust, ord, branch, at).Scan(&rest); err != nil {
		t.Fatalf("plant the rest's document: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM invoices WHERE id = ANY($1::uuid[])`, []string{dep, rest})
		_, _ = pool.Exec(c, `DELETE FROM orders WHERE id = $1`, ord)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1`, cust)
	})

	// The ring's two events: the deposit's document finalises into the
	// liability, the rest's into revenue and tax.
	if err := svc.consume(ctx, ev("ar.invoice.finalized", map[string]any{"invoice_id": dep})); err != nil {
		t.Fatalf("the deposit's finalization consume: %v", err)
	}
	if err := svc.consume(ctx, ev("ar.invoice.finalized", map[string]any{"invoice_id": rest})); err != nil {
		t.Fatalf("the rest's finalization consume: %v", err)
	}
	if got := accountNet(t, pool, depositReleaseYear, "2300"); got != -4000 {
		t.Fatalf("before the release 2300 nets %d, want the -4000 liability", got)
	}

	// The fulfilment: the order's special order lands, the deposit releases.
	if err := svc.consume(ctx, ev("deposit.released", map[string]any{"invoice_id": dep})); err != nil {
		t.Fatalf("the release consume: %v", err)
	}

	// THE BOOKS BALANCE, per account, after the whole life:
	//   1100 AR                 4000 + 8249         = 12249 net debit
	//   2300 Customer Deposits  -4000 + 4000        = 0, the liability returned
	//   4000 Revenue            -(7300 + 4000)      = -11300, the sale's pre-tax total
	//   2200 HST                -949
	if got := accountNet(t, pool, depositReleaseYear, "1100"); got != 12249 {
		t.Errorf("1100 AR nets %d, want 12249", got)
	}
	if got := accountNet(t, pool, depositReleaseYear, "2300"); got != 0 {
		t.Errorf("2300 Customer Deposits nets %d, want 0 (the deposit released)", got)
	}
	if got := accountNet(t, pool, depositReleaseYear, "4000"); got != -11300 {
		t.Errorf("4000 Revenue nets %d, want -11300 (the sale's pre-tax total)", got)
	}
	if got := accountNet(t, pool, depositReleaseYear, "2200"); got != -949 {
		t.Errorf("2200 HST nets %d, want -949", got)
	}
	var dr, cr int64
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(COALESCE(l.debit_cents,0)),0), COALESCE(SUM(COALESCE(l.credit_cents,0)),0)
		  FROM gl_journal_lines l JOIN gl_journals j ON j.id = l.journal_id
		 WHERE j.fiscal_year = $1`, depositReleaseYear).Scan(&dr, &cr); err != nil {
		t.Fatalf("the year's legs: %v", err)
	}
	// The three journals together: the two documents' 12249 of AR debits plus
	// the release's 4000 on each side, balanced.
	if dr != cr || dr != 16249 {
		t.Errorf("the books do not balance: dr=%d cr=%d, want 16249 both", dr, cr)
	}

	// The release journal itself: exactly one journal under the release key,
	// its two legs DR 2300 4000 / CR 4000 4000.
	releaseLegs := func() map[string][2]int64 {
		t.Helper()
		rows, err := pool.Query(ctx, `
			SELECT a.code, COALESCE(l.debit_cents,0), COALESCE(l.credit_cents,0)
			  FROM gl_journal_lines l
			  JOIN gl_journals j ON j.id = l.journal_id
			  JOIN gl_accounts a ON a.id = l.account_id
			 WHERE j.idempotency_key = $1`, "deposit.released:"+dep)
		if err != nil {
			t.Fatalf("the release journal's legs: %v", err)
		}
		defer rows.Close()
		out := map[string][2]int64{}
		for rows.Next() {
			var code string
			var dr, cr int64
			if err := rows.Scan(&code, &dr, &cr); err != nil {
				t.Fatalf("scan release leg: %v", err)
			}
			out[code] = [2]int64{dr, cr}
		}
		return out
	}
	legs := releaseLegs()
	if len(legs) != 2 {
		t.Fatalf("the release journal carries %d legs (%v), want the two", len(legs), legs)
	}
	if legs["2300"] != [2]int64{4000, 0} || legs["4000"] != [2]int64{0, 4000} {
		t.Fatalf("the release legs = %v, want DR 2300 4000 / CR 4000 4000", legs)
	}

	// A redelivered release posts nothing new.
	if err := svc.consume(ctx, ev("deposit.released", map[string]any{"invoice_id": dep})); err != nil {
		t.Fatalf("the redelivered release consume: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM gl_journals WHERE idempotency_key = $1`,
		"deposit.released:"+dep).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("a redelivered release left %d journals, want the one", n)
	}
	if got := accountNet(t, pool, depositReleaseYear, "2300"); got != 0 {
		t.Fatalf("after the redelivery 2300 nets %d, want 0", got)
	}
}
