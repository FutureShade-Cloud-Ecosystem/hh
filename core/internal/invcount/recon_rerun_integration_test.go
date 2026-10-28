//go:build integration

package invcount

// recon_rerun_integration_test.go, FW1-11 Round 2 (P2-1). The current open month's inventory
// tie-out re-runs so a correction in the month updates the period's row; a past period's row stays
// frozen once recorded so looking at an older month never overwrites its subledger figure with
// today's live shelf stock.

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// fw11PlantReceiptInPeriod posts a receipt's 1200 debit in (year, month) and cleans it up after the test.
func fw11PlantReceiptInPeriod(t *testing.T, pool *pgxpool.Pool, year, month int, cents int64) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx,
		`INSERT INTO fiscal_periods (year, month) VALUES ($1, $2) ON CONFLICT (year, month) DO NOTHING`,
		year, month); err != nil {
		t.Fatalf("ensure period %d-%02d: %v", year, month, err)
	}
	var periodID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM fiscal_periods WHERE year=$1 AND month=$2`, year, month).Scan(&periodID); err != nil {
		t.Fatalf("period %d-%02d: %v", year, month, err)
	}
	var jid string
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO gl_journals (fiscal_year, entry_no, period_id, source, memo, status, posted_at, branch_id)
		VALUES ($1, (SELECT COALESCE(MAX(entry_no),0)+1 FROM gl_journals WHERE fiscal_year=$1),
		        $2::uuid, 'PO_RECEIPT', 'FW11 receipt', 'POSTED', now(), $3::uuid)
		RETURNING id::text`,
		year, periodID, branchTRT).Scan(&jid); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("plant receipt journal: %v", err)
	}
	for _, l := range []struct {
		code          string
		debit, credit int64
	}{
		{"1200", cents, 0},
		{"2100", 0, cents},
	} {
		if _, err := tx.Exec(ctx, `
			INSERT INTO gl_journal_lines (journal_id, line_no, account_id, debit_cents, credit_cents, branch_id, memo)
			SELECT $1::uuid, (SELECT COALESCE(MAX(line_no),0)+1 FROM gl_journal_lines WHERE journal_id=$1::uuid),
			       a.id, NULLIF($2,0), NULLIF($3,0), $6::uuid, $4 FROM gl_accounts a WHERE a.code = $5`,
			jid, l.debit, l.credit, "FW11", l.code, branchTRT); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatalf("plant line %s: %v", l.code, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit journal: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM gl_journal_lines WHERE journal_id=$1::uuid`, jid)
		_, _ = pool.Exec(bg, `DELETE FROM gl_journals WHERE id=$1::uuid`, jid)
	})
}

// The second red leg: the current open month re-runs when a mid-month correction lands.
func TestIntegration_FW1_11_ReconReRunsAPeriod(t *testing.T) {
	pool := intPool(t)
	curYear, curMonth := currentYearMonth(time.Now())
	ctx := context.Background()
	if _, err := pool.Exec(ctx,
		`DELETE FROM inventory_recon_runs WHERE fiscal_year=$1 AND month=$2 AND branch_id=$3::uuid`,
		curYear, curMonth, branchTRT); err != nil {
		t.Fatalf("clear current recon run: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM inventory_recon_runs WHERE fiscal_year=$1 AND month=$2 AND branch_id=$3::uuid`,
			curYear, curMonth, branchTRT)
	})

	gb := fw11GLBal{pool: pool}
	baseline, err := gb.GLBalanceThroughCents(ctx, "1200", curYear, curMonth, branchTRT)
	if err != nil {
		t.Fatalf("baseline: %v", err)
	}
	fw11PlantReceiptInPeriod(t, pool, curYear, curMonth, 100000)

	stock := &mutableStock{value: baseline + 100000}
	svc := NewService(NewRepository(pool)).
		WithStockCounter(stock).
		WithGLBalanceReader(gb)
	caller := ctxAsAt(userMaker, branchTRT)

	if _, err := svc.ReconcileMonth(caller, curYear, curMonth, branchTRT); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	// A correction lands mid month: stock is recounted (and the GL posts another 5000 of receipts).
	stock.value = baseline + 105000
	fw11PlantReceiptInPeriod(t, pool, curYear, curMonth, 5000)

	if _, err := svc.ReconcileMonth(caller, curYear, curMonth, branchTRT); err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	runRow, _, err := svc.ReconRunFor(caller, curYear, curMonth, branchTRT)
	if err != nil {
		t.Fatalf("recon run: %v", err)
	}
	// FW1-11: the re-run in the current open month answers the corrected figures, the shelves and the
	// ledger both moved by this test's second receipt, never the first row's frozen ones.
	if runRow.SubledgerCents != baseline+105000 || runRow.GL1200Cents != baseline+105000 || runRow.DeltaCents != 0 {
		t.Errorf("FW1-11: the re-run answers subledger %d, GL %d, delta %d, want both arms at the corrected figures (baseline %d plus this test's 105000 of receipts), delta 0", runRow.SubledgerCents, runRow.GL1200Cents, runRow.DeltaCents, baseline)
	}
}

// Round 2 (P2-1): a past period's inventory tie-out row stays frozen; a re-run is allowed only for the
// current open month, so looking at an older period never overwrites its recorded subledger figure with
// today's live shelf stock.
func TestIntegration_FW1_11_PastPeriodReconRowStaysFrozen(t *testing.T) {
	pool := intPool(t)
	curYear, _ := currentYearMonth(time.Now())
	pastYear, pastMonth := curYear-1, 5
	ctx := context.Background()
	if _, err := pool.Exec(ctx,
		`DELETE FROM inventory_recon_runs WHERE fiscal_year=$1 AND month=$2 AND branch_id=$3::uuid`,
		pastYear, pastMonth, branchTRT); err != nil {
		t.Fatalf("clear past recon run: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM inventory_recon_runs WHERE fiscal_year=$1 AND month=$2 AND branch_id=$3::uuid`,
			pastYear, pastMonth, branchTRT)
	})

	gb := fw11GLBal{pool: pool}
	baseline, err := gb.GLBalanceThroughCents(ctx, "1200", pastYear, pastMonth, branchTRT)
	if err != nil {
		t.Fatalf("baseline: %v", err)
	}
	stock := &mutableStock{value: baseline}
	svc := NewService(NewRepository(pool)).
		WithStockCounter(stock).
		WithGLBalanceReader(gb)
	caller := ctxAsAt(userMaker, branchTRT)

	// First run records the period's tie-out row when the shelves matched the period's ledger.
	if _, err := svc.ReconcileMonth(caller, pastYear, pastMonth, branchTRT); err != nil {
		t.Fatalf("first reconcile of past period: %v", err)
	}
	// Months later, today's shelf stock moves by 50000; looking at the past period again must keep its
	// recorded row frozen rather than overwriting it with today's shelves.
	stock.value = baseline + 50000
	delta, err := svc.ReconcileMonth(caller, pastYear, pastMonth, branchTRT)
	if err != nil {
		t.Fatalf("second reconcile of past period: %v", err)
	}
	runRow, _, err := svc.ReconRunFor(caller, pastYear, pastMonth, branchTRT)
	if err != nil {
		t.Fatalf("recon run: %v", err)
	}
	if delta != 0 || runRow.SubledgerCents != baseline || runRow.GL1200Cents != baseline || runRow.DeltaCents != 0 {
		t.Errorf("FW1-11 P2-1 red: re-running past period %d-%02d overwrote its frozen row with today's shelf stock (subledger %d, GL %d, delta %d), want the frozen row (subledger %d, GL %d, delta 0)",
			pastYear, pastMonth, runRow.SubledgerCents, runRow.GL1200Cents, runRow.DeltaCents, baseline, baseline)
	}
}
