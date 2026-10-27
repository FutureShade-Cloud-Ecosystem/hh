//go:build integration

// deposit_cancel_integration_test.go, the cancelled deposit against the real
// database: a supplier deposit document books its liability at finalization
// (DR 1100 / CR 2300, the 0373 rule) and leaves the books under the cancel's
// disposition (the 0375 rules): credit back DR 2300 / CR the receivable, kept
// DR 2300 / CR 4950 Other Income. After either, 2300 for the deposit is zero
// and the books balance; a redelivery posts nothing.
//
// ISOLATION. Fiscal year 2087, outside the seeded 2026 periods and the 2086
// band the release proof uses.
//
//	Run: DATABASE_URL='postgres://.../db?sslmode=disable' go test -tags=integration -count=1 -run DepositCancel ./internal/gl/
package gl

import (
	"context"
	"fmt"
	"testing"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5/pgxpool"
)

const depositCancelYear = 2087

// seedOneDeposit plants one customer, one CONFIRMED order and one FINALIZED
// deposit document, the two rows the cancel proof needs.
func seedOneDeposit(t *testing.T, pool *pgxpool.Pool, tag string, at time.Time) (cust, ord, dep string) {
	t.Helper()
	ctx := context.Background()
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name, customer_type, payment_terms_days, credit_limit_cents)
		 VALUES ($1, 'Deposit cancel proof', 'CONTRACTOR', 30, 5000000) RETURNING id::text`,
		fmt.Sprintf("DC-%s", tag)).Scan(&cust); err != nil {
		t.Fatalf("plant the customer: %v", err)
	}
	var branch string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations LIMIT 1`).Scan(&branch); err != nil {
		t.Skipf("no seeded branch: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, subtotal_cents, total_cents)
		 VALUES ($1, $2, $3, 'CONFIRMED', 'PICKUP', 4000, 4000) RETURNING id::text`,
		fmt.Sprintf("#DC-%s", tag), cust, branch).Scan(&ord); err != nil {
		t.Fatalf("plant the order: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO invoices (invoice_no, customer_id, order_id, branch_id, status, due_date,
		                       subtotal_cents, tax_cents, total_cents, balance_cents, source_kind, finalized_at)
		 VALUES ($1, $2, $3, $4, 'FINALIZED', CURRENT_DATE, 4000, 0, 4000, 4000, 'SUPPLIER_DEPOSIT', $5)
		 RETURNING id::text`,
		fmt.Sprintf("INV-DCD-%s", tag), cust, ord, branch, at).Scan(&dep); err != nil {
		t.Fatalf("plant the deposit document: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM invoices WHERE id = $1`, dep)
		_, _ = pool.Exec(c, `DELETE FROM orders WHERE id = $1`, ord)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1`, cust)
	})
	return cust, ord, dep
}

func TestIntegration_DepositCancel_BothDispositionsClear2300AndBalance(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	ensurePeriods(t, pool, depositCancelYear)
	at := time.Date(depositCancelYear, 6, 15, 12, 0, 0, 0, time.UTC)
	svc := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager, Scope: identity.ScopeGlobal}, at).
		WithInvoiceReader(seedInvoiceReader{pool: pool})
	suffix := time.Now().UnixNano()

	// CREDIT BACK. The document books the liability, the cancel credits it
	// back to the account.
	_, _, depCB := seedOneDeposit(t, pool, fmt.Sprintf("CB-%d", suffix), at)
	if err := svc.consume(ctx, ev("ar.invoice.finalized", map[string]any{"invoice_id": depCB})); err != nil {
		t.Fatalf("the credit-back deposit's finalization: %v", err)
	}
	if err := svc.consume(ctx, ev("deposit.cancelled", map[string]any{
		"invoice_id": depCB, "disposition": "CREDIT_BACK",
	})); err != nil {
		t.Fatalf("the credit-back cancel consume: %v", err)
	}

	// KEPT. Same life, the dealer keeps what was paid: 2500 of the 4000 was
	// paid, the unpaid 1500 is closed against the receivable (row 1228).
	_, _, depK := seedOneDeposit(t, pool, fmt.Sprintf("K-%d", suffix), at)
	if err := svc.consume(ctx, ev("ar.invoice.finalized", map[string]any{"invoice_id": depK})); err != nil {
		t.Fatalf("the kept deposit's finalization: %v", err)
	}
	if err := svc.consume(ctx, ev("deposit.cancelled", map[string]any{
		"invoice_id": depK, "disposition": "KEPT", "paid_cents": 2500, "closed_cents": 1500,
	})); err != nil {
		t.Fatalf("the kept cancel consume: %v", err)
	}

	// 2300 AT ZERO for both deposits: each booked +4000 of liability at its
	// finalization and released it at its cancel.
	if got := accountNet(t, pool, depositCancelYear, "2300"); got != 0 {
		t.Errorf("2300 Customer Deposits nets %d, want 0 (both deposits left the books)", got)
	}
	// Credit back returned the deposit to the account: 1100 nets ZERO (the
	// finalize's DR and the cancel's CR). Kept closes only the unpaid 1500, so
	// 4000 less 1500 stands: the 2500 the customer paid is cleared by the
	// payment's own journal, which this proof does not post.
	if got := accountNet(t, pool, depositCancelYear, "1100"); got != 2500 {
		t.Errorf("1100 AR nets %d, want 2500 (the finalize less the closed unpaid rest)", got)
	}
	// Other income carries what the customer paid of the kept deposit, and
	// only that.
	if got := accountNet(t, pool, depositCancelYear, "4950"); got != -2500 {
		t.Errorf("4950 Other Income nets %d, want -2500 (the paid part kept)", got)
	}
	// Revenue carries nothing of either: a cancelled special order's deposit
	// is never revenue.
	if got := accountNet(t, pool, depositCancelYear, "4000"); got != 0 {
		t.Errorf("4000 Revenue nets %d, want 0 (no cancelled deposit is revenue)", got)
	}

	// THE BOOKS BALANCE across the year's four journals.
	var dr, cr int64
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(COALESCE(l.debit_cents,0)),0), COALESCE(SUM(COALESCE(l.credit_cents,0)),0)
		  FROM gl_journal_lines l JOIN gl_journals j ON j.id = l.journal_id
		 WHERE j.fiscal_year = $1`, depositCancelYear).Scan(&dr, &cr); err != nil {
		t.Fatalf("the year's legs: %v", err)
	}
	// The two finalizations post 4000 each side; each cancel 4000 each side:
	// 16000 both sides, four journals.
	if dr != cr || dr != 16000 {
		t.Errorf("the books do not balance: dr=%d cr=%d, want 16000 both", dr, cr)
	}

	// The two journals' own legs.
	for _, tc := range []struct {
		key  string
		want map[string][2]int64
	}{
		{"deposit.cancelled:" + depCB, map[string][2]int64{"2300": {4000, 0}, "1100": {0, 4000}}},
		{"deposit.cancelled:" + depK, map[string][2]int64{"2300": {4000, 0}, "4950": {0, 2500}, "1100": {0, 1500}}},
	} {
		rows, err := pool.Query(ctx, `
			SELECT a.code, COALESCE(l.debit_cents,0), COALESCE(l.credit_cents,0)
			  FROM gl_journal_lines l
			  JOIN gl_journals j ON j.id = l.journal_id
			  JOIN gl_accounts a ON a.id = l.account_id
			 WHERE j.idempotency_key = $1`, tc.key)
		if err != nil {
			t.Fatalf("the cancel journal's legs (%s): %v", tc.key, err)
		}
		got := map[string][2]int64{}
		for rows.Next() {
			var code string
			var d, c int64
			if err := rows.Scan(&code, &d, &c); err != nil {
				t.Fatal(err)
			}
			got[code] = [2]int64{d, c}
		}
		rows.Close()
		if len(got) != len(tc.want) || got["2300"] != tc.want["2300"] {
			t.Fatalf("the journal %s legs = %v, want %v", tc.key, got, tc.want)
		}
		for code, want := range tc.want {
			if got[code] != want {
				t.Fatalf("the journal %s leg %s = %v, want %v", tc.key, code, got[code], want)
			}
		}
	}

	// A redelivery of either event posts nothing, under either disposition's
	// spelling: the document cancels exactly once.
	for _, e := range []struct {
		invoice     string
		disposition string
	}{
		{depCB, "CREDIT_BACK"}, {depCB, "KEPT"},
		{depK, "CREDIT_BACK"}, {depK, "KEPT"},
	} {
		if err := svc.consume(ctx, ev("deposit.cancelled", map[string]any{
			"invoice_id": e.invoice, "disposition": e.disposition, "paid_cents": 0, "closed_cents": 4000,
		})); err != nil {
			t.Fatalf("the redelivered cancel consume: %v", err)
		}
	}
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM gl_journals WHERE idempotency_key LIKE 'deposit.cancelled:%' AND fiscal_year = $1`,
		depositCancelYear).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("after the redeliveries the year holds %d cancel journals, want the two", n)
	}
	if got := accountNet(t, pool, depositCancelYear, "2300"); got != 0 {
		t.Fatalf("after the redeliveries 2300 nets %d, want 0", got)
	}
}
