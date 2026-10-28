//go:build integration

package invcount

// recon_cumulative_integration_test.go, FW1-11. The tie-out compared the whole stock value with ONE
// month's movement on 1200 and froze on its first run (the audit's LEDGER-TIEOUT finding). The rebuild
// reads 1200's balance through the period (cumulative by period, until 612's late posted filing rule
// lands) and re-runs a period, so a correction in the month is never hidden behind the first row.

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

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
		_, _ = pool.Exec(bg, `DELETE FROM inventory_recon_runs WHERE fiscal_year=2091`)
		_, _ = pool.Exec(bg, `DELETE FROM gl_journal_lines l USING gl_journals j WHERE l.journal_id=j.id AND j.fiscal_year=2091`)
		_, _ = pool.Exec(bg, `DELETE FROM gl_journals WHERE fiscal_year=2091`)
		_, _ = pool.Exec(bg, `DELETE FROM fiscal_periods WHERE year=2091`)
	})
}

// fw11PlantReceipt posts a receipt's 1200 debit in 2091-04, the month BEFORE the reconciled one.
func fw11PlantReceipt(t *testing.T, pool *pgxpool.Pool, cents int64) {
	t.Helper()
	ctx := context.Background()
	var periodID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM fiscal_periods WHERE year=2091 AND month=4`).Scan(&periodID); err != nil {
		t.Fatalf("period 2091-04: %v", err)
	}
	var jid string
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO gl_journals (fiscal_year, entry_no, period_id, source, memo, status, posted_at, branch_id)
		VALUES (2091, (SELECT COALESCE(MAX(entry_no),0)+1 FROM gl_journals WHERE fiscal_year=2091),
		        $1::uuid, 'PO_RECEIPT', 'FW11 receipt', 'POSTED', $2, $3::uuid)
		RETURNING id::text`,
		periodID, time.Date(2091, 4, 20, 12, 0, 0, 0, time.UTC), branchTRT).Scan(&jid); err != nil {
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
}

// mutableStock is the fake stock seam with values the test can move between two runs. It embeds the
// file's fakeStockSeam for the transactional and posting halves it never exercises. value is the
// subledger figure the recon's stock arm reads; the seeded books carry 1200 negative until FW1-07
// posts the missing cost of goods sold, so the legs value the shelves at the real baseline plus
// their own receipts and assert the two arms meet.
type mutableStock struct {
	fakeStockSeam
	value int64
}

func (s *mutableStock) SnapshotOnHand(_ context.Context, _ string) ([]Snapshot, error) {
	return []Snapshot{{ProductID: prodP1, LocationID: branchTRT, OnHandBase: 1, AvgCostCents: s.value}}, nil
}
func (s *mutableStock) WeightedAvgCostCents(_ context.Context, _, _ string) (int64, error) {
	return s.value, nil
}

// fw11GLBal mimics today's real GL balance adapter: one month's movement on the account, natural
// orientation (1200 is debit normal, its movement reads positive).
type fw11GLBal struct{ pool *pgxpool.Pool }

func (b fw11GLBal) GLBalanceCents(_ context.Context, code string, year, month int, branchID string) (int64, error) {
	var netDebit int64
	err := b.pool.QueryRow(context.Background(), `
		SELECT COALESCE(SUM(net_debit_cents),0) FROM v_gl_account_balances
		 WHERE account_code = $1 AND fiscal_year = $2 AND month = $3
		   AND ($4 = '' OR branch_id = $4::uuid)`, code, year, month, branchID).Scan(&netDebit)
	return netDebit, err
}

// GLBalanceThroughCents is the read the rebuilt tie-out takes: every period up to and including the
// asked one, cumulative.
func (b fw11GLBal) GLBalanceThroughCents(_ context.Context, code string, year, month int, branchID string) (int64, error) {
	var netDebit int64
	err := b.pool.QueryRow(context.Background(), `
		SELECT COALESCE(SUM(net_debit_cents),0) FROM v_gl_account_balances
		 WHERE account_code = $1
		   AND (fiscal_year < $2 OR (fiscal_year = $2 AND month <= $3))
		   AND ($4 = '' OR branch_id = $4::uuid)`, code, year, month, branchID).Scan(&netDebit)
	return netDebit, err
}

// The red leg: after a month with a receipt (and no 1200 movement in the reconciled month itself), the
// tie-out reports one month's movement (0) against the whole stock value, a false out of tie.
func TestIntegration_FW1_11_ReconReads1200ThroughThePeriod(t *testing.T) {
	pool := intPool(t)
	fw11Ensure2091(t, pool)
	fw11PlantReceipt(t, pool, 100000)

	// The seeded years' 1200 rows are part of the real balance, so the legs are self relative: the
	// baseline is the through-period balance before this test's receipt, and every expectation adds
	// this test's own postings to it.
	gb := fw11GLBal{pool: pool}
	baseline, err := gb.GLBalanceThroughCents(context.Background(), "1200", 2091, 3, branchTRT)
	if err != nil {
		t.Fatalf("baseline: %v", err)
	}

	stock := &mutableStock{value: baseline + 100000}
	svc := NewService(NewRepository(pool)).
		WithStockCounter(stock).
		WithGLBalanceReader(gb)
	caller := ctxAsAt(userMaker, branchTRT)

	if _, err := svc.ReconcileMonth(caller, 2091, 5, branchTRT); err != nil {
		t.Fatalf("reconcile 2091-05: %v", err)
	}
	runRow, _, err := svc.ReconRunFor(caller, 2091, 5, branchTRT)
	if err != nil {
		t.Fatalf("recon run: %v", err)
	}
	// FW1-11: the GL side is the balance THROUGH the period (baseline plus this test's 100000
	// receipt, nothing posted in month 5 itself), so the tie reads zero against the same valuation.
	if runRow.GL1200Cents != baseline+100000 || runRow.DeltaCents != 0 {
		t.Errorf("FW1-11: GL 1200 reads %d for 2091-05, want the balance through the period (baseline %d plus the 100000 receipt), delta %d, want 0", runRow.GL1200Cents, baseline, runRow.DeltaCents)
	}
}
