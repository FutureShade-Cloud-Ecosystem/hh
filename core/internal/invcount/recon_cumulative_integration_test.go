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
			INSERT INTO gl_journal_lines (journal_id, line_no, account_id, debit_cents, credit_cents, memo)
			SELECT $1::uuid, (SELECT COALESCE(MAX(line_no),0)+1 FROM gl_journal_lines WHERE journal_id=$1::uuid),
			       a.id, NULLIF($2,0), NULLIF($3,0), $4 FROM gl_accounts a WHERE a.code = $5`,
			jid, l.debit, l.credit, "FW11", l.code); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatalf("plant line %s: %v", l.code, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit journal: %v", err)
	}
}

// mutableStock is the fake stock seam with values the test can move between two runs. It embeds the
// file's fakeStockSeam for the transactional and posting halves it never exercises.
type mutableStock struct {
	fakeStockSeam
	cost   int64
	onHand float64
}

func (s *mutableStock) SnapshotOnHand(_ context.Context, _ string) ([]Snapshot, error) {
	return []Snapshot{{ProductID: prodP1, LocationID: branchTRT, OnHandBase: s.onHand, AvgCostCents: s.cost}}, nil
}
func (s *mutableStock) WeightedAvgCostCents(_ context.Context, _, _ string) (int64, error) {
	return s.cost, nil
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

// The red leg: after a month with a receipt (and no 1200 movement in the reconciled month itself), the
// tie-out reports one month's movement (0) against the whole stock value, a false out of tie.
func TestIntegration_FW1_11_ReconReads1200ThroughThePeriod(t *testing.T) {
	pool := intPool(t)
	fw11Ensure2091(t, pool)
	fw11PlantReceipt(t, pool, 100000)

	stock := &mutableStock{cost: 1000, onHand: 100} // valued 100000
	svc := NewService(NewRepository(pool)).
		WithStockCounter(stock).
		WithGLBalanceReader(fw11GLBal{pool: pool})
	caller := ctxAsAt(userMaker, branchTRT)

	if _, err := svc.ReconcileMonth(caller, 2091, 5, branchTRT); err != nil {
		t.Fatalf("reconcile 2091-05: %v", err)
	}
	runRow, _, err := svc.ReconRunFor(caller, 2091, 5, branchTRT)
	if err != nil {
		t.Fatalf("recon run: %v", err)
	}
	// FW1-11 red: the GL side is the month's movement (nothing in month 5), not the balance through
	// the period (100000): the tie reads 100000 out, wrongly.
	if runRow.GL1200Cents != 100000 || runRow.DeltaCents != 0 {
		t.Errorf("FW1-11 red: GL 1200 reads %d for 2091-05 with a 2091-04 receipt of 100000, delta %d, want the balance through the period (100000, delta 0)", runRow.GL1200Cents, runRow.DeltaCents)
	}
}

// The second red leg: the run freezes on its first run. A correction in the month never shows.
func TestIntegration_FW1_11_ReconReRunsAPeriod(t *testing.T) {
	pool := intPool(t)
	fw11Ensure2091(t, pool)
	fw11PlantReceipt(t, pool, 100000)

	stock := &mutableStock{cost: 1000, onHand: 100}
	svc := NewService(NewRepository(pool)).
		WithStockCounter(stock).
		WithGLBalanceReader(fw11GLBal{pool: pool})
	caller := ctxAsAt(userMaker, branchTRT)

	if _, err := svc.ReconcileMonth(caller, 2091, 5, branchTRT); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	// A correction lands mid month: stock is recounted (and the GL posts another 5000 of receipts).
	stock.onHand = 105
	fw11PlantReceipt(t, pool, 5000)

	if _, err := svc.ReconcileMonth(caller, 2091, 5, branchTRT); err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	runRow, _, err := svc.ReconRunFor(caller, 2091, 5, branchTRT)
	if err != nil {
		t.Fatalf("recon run: %v", err)
	}
	// FW1-11 red: the second run still answers the first row (subledger 100000, GL 0).
	if runRow.SubledgerCents != 105000 || runRow.GL1200Cents != 105000 {
		t.Errorf("FW1-11 red: the re-run answers the frozen first row (subledger %d, GL %d), want the corrected figures (subledger 105000, GL 105000)", runRow.SubledgerCents, runRow.GL1200Cents)
	}
}
