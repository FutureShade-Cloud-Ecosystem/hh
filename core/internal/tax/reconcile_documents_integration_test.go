//go:build integration

package tax

// reconcile_documents_integration_test.go, FW1-11. The reconcile's collected side reads
// tax_decision_snapshot rows, which only relief sales write; the GL side reads one month's movement on
// 2200, which also carries the vendor bills' input tax credits. An ordinary month, a counter sale and a
// posted vendor bill, reads a false variance (the audit's LEDGER-TIEOUT finding). The rebuild reads the
// month's documents: sales HST on the invoices (credit memos subtract), input tax credits on the posted
// vendor bills, shown apart, netted, against 2200's month movement.

import (
	"context"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	gl "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/purchasing"
	"github.com/jackc/pgx/v5/pgxpool"
)

const fw11TRT = "5d1b3f6e-0000-4000-9000-000000000001"

// fw11Ensure2091 opens fiscal year 2091 for the GL legs and cleans its journals after.
func fw11Ensure2091(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx,
		`INSERT INTO fiscal_periods (year, month) SELECT 2091, m FROM generate_series(1,12) AS m
		 ON CONFLICT (year, month) DO NOTHING`); err != nil {
		t.Fatalf("ensure periods 2091: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM gl_journal_lines l USING gl_journals j WHERE l.journal_id=j.id AND j.fiscal_year=2091`)
		_, _ = pool.Exec(bg, `DELETE FROM gl_journals WHERE fiscal_year=2091`)
		_, _ = pool.Exec(bg, `DELETE FROM fiscal_periods WHERE year=2091`)
	})
}

// fw11Line is one planted leg: account code and its side.
type fw11Line struct {
	code          string
	debit, credit int64
}

// fw11Post plants one POSTED 2091-05 journal with the given legs, the way the real writers post them
// (the deferred balance trigger asserts at COMMIT).
func fw11Post(t *testing.T, pool *pgxpool.Pool, memo string, lines []fw11Line) {
	t.Helper()
	ctx := context.Background()
	var periodID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM fiscal_periods WHERE year=2091 AND month=5`).Scan(&periodID); err != nil {
		t.Fatalf("period 2091-05: %v", err)
	}
	var jid string
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO gl_journals (fiscal_year, entry_no, period_id, source, memo, status, posted_at, branch_id)
		VALUES (2091, (SELECT COALESCE(MAX(entry_no),0)+1 FROM gl_journals WHERE fiscal_year=2091),
		        $1::uuid, 'ADJUSTMENT', $2, 'POSTED', $3, $4::uuid)
		RETURNING id::text`,
		periodID, memo, time.Date(2091, 5, 20, 12, 0, 0, 0, time.UTC), fw11TRT).Scan(&jid); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("plant journal: %v", err)
	}
	for _, l := range lines {
		if _, err := tx.Exec(ctx, `
			INSERT INTO gl_journal_lines (journal_id, line_no, account_id, debit_cents, credit_cents, memo)
			SELECT $1::uuid, (SELECT COALESCE(MAX(line_no),0)+1 FROM gl_journal_lines WHERE journal_id=$1::uuid),
			       a.id, NULLIF($2,0), NULLIF($3,0), $4 FROM gl_accounts a WHERE a.code = $5`,
			jid, l.debit, l.credit, memo, l.code); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatalf("plant line %s: %v", l.code, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit journal: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM gl_journal_lines WHERE journal_id=$1`, jid)
		_, _ = pool.Exec(bg, `DELETE FROM gl_journals WHERE id=$1`, jid)
	})
}

// fw11GLBal reads the GL side through the real gl service, the provider seam the wiring uses.
type fw11GLBal struct{ svc *gl.Service }

func (a fw11GLBal) GLBalanceCents(ctx context.Context, code string, year, month int, branchID string) (int64, error) {
	return a.svc.GLBalanceCents(ctx, code, year, month, branchID)
}

// The red leg: an ordinary month with a counter sale's invoice and one posted vendor bill shows a false
// variance today, because the collected side reads relief snapshots (none here) and the GL side nets the
// bill's input tax credit against the sale's collected tax.
func TestIntegration_FW1_11_ReconcileReadsTheMonthsDocuments(t *testing.T) {
	pool := intPool(t)
	fw11Ensure2091(t, pool)
	// The reconcile reads its document sides through the owning modules' seams (billing's invoices,
	// purchasing's posted bills), wired here over the real repositories the way main.go wires them.
	svc := intSvc(t, pool).
		WithSalesHSTs(billing.NewRepository(pool)).
		WithBillITCs(purchasing.NewRepository(pool)).
		WithGLBalances(fw11GLBal{svc: gl.NewService(gl.NewRepository(pool))})
	ctx := context.Background()

	var customerID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM customers LIMIT 1`).Scan(&customerID); err != nil {
		t.Skipf("no seeded customer: %v", err)
	}
	// The sale: a 100000 + 13000 tax invoice finalized in the month, its journal posted (CR 2200).
	var inv string
	if err := pool.QueryRow(ctx, `
		INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                      subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, doc_type)
		VALUES ('FW11-SALE', $1::uuid, $2::uuid, 'FINALIZED', '2091-05-10', '2091-05-10',
		        100000, 13000, 113000, 113000, '2091-05-10', 'INVOICE')
		RETURNING id::text`,
		customerID, fw11TRT).Scan(&inv); err != nil {
		t.Fatalf("plant invoice: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM invoices WHERE id=$1`, inv)
	})
	// The sale's journal the way ar.invoice.finalized posts it: DR 1100, CR 4000 revenue, CR 2200 tax.
	fw11Post(t, pool, "FW11 sale collected", []fw11Line{
		{"1100", 113000, 0},
		{"4000", 0, 100000},
		{"2200", 0, 13000},
	})

	// The vendor bill: 30000 + 3900 input tax, posted in the month (DR 2200).
	var vendorID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM vendors LIMIT 1`).Scan(&vendorID); err != nil {
		t.Skipf("no seeded vendor: %v", err)
	}
	var bill string
	if err := pool.QueryRow(ctx, `
		INSERT INTO purchasing_bills (bill_no, vendor_id, branch_id, status, match_state, bill_date,
		                              subtotal_cents, tax_cents, total_cents, posted_at)
		VALUES ('FW11-BILL', $1::uuid, $2::uuid, 'POSTED', 'MATCHED', '2091-05-20', 30000, 3900, 33900, $3)
		RETURNING id::text`,
		vendorID, fw11TRT, time.Date(2091, 5, 21, 12, 0, 0, 0, time.UTC)).Scan(&bill); err != nil {
		t.Fatalf("plant bill: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM purchasing_bills WHERE id=$1`, bill)
	})
	// The vendor bill's journal the way PostBill posts it: DR the cost, DR 2200 input tax, CR 2100 payable.
	fw11Post(t, pool, "FW11 bill input tax", []fw11Line{
		{"5250", 30000, 0},
		{"2200", 3900, 0},
		{"2100", 0, 33900},
	})

	rep, err := svc.ReconcileReport(ctx, 2091, 5, "")
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	// The collected side is the month's documents, not the relief snapshots: 13000 on the sale, the
	// bill's 3900 shown apart as input tax credits.
	if rep.CollectedCents != 13000 {
		t.Errorf("FW1-11: the reconcile reads collected %d for an ordinary month's documents, want 13000 (it used to read relief snapshots, which never wrote this month)", rep.CollectedCents)
	}
	if rep.InputTaxCreditsCents != 3900 {
		t.Errorf("FW1-11: the reconcile reads input tax credits %d, want 3900 on the posted bill", rep.InputTaxCreditsCents)
	}
	// The month ties: 2200's movement (13000 credit less 3900 debit = 9100) against the documents net
	// (13000 less 3900 of input tax).
	if rep.VarianceCents != 0 || !rep.TiedOut {
		t.Errorf("FW1-11: an ordinary month shows a variance of %d (GL %d against %d collected less %d of input tax), want a tie", rep.VarianceCents, rep.GLPayableCents, rep.CollectedCents, rep.InputTaxCreditsCents)
	}
}
