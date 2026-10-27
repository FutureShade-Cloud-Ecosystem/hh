//go:build integration

package main

// P1-A (S5's review of PR 632, round 2): item 2's one transaction path is live in the served core only when
// the PRODUCTION poster carries the transaction arm. This leg composes bankrec over wireOperations' own
// glPosterAdapter (no test poster) and refuses the clearing journal at the GL (1010 inactive): the match and
// its journal must roll back together, so no match row is left, the statement line is not MATCHED, and the
// payment is neither claimed nor DEPOSITED.

import (
	"context"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/bankrec"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The production poster carries bankrec's transaction arm (P1-A): the compile pin that keeps
// the one transaction path live in the served core. Without it the leg below still refuses,
// but on the historical two step order, which commits the match before the journal.
var _ bankrec.JournalPosterTx = glPosterAdapter{}

func TestIntegration_FW1_05_R2_ARefusedClearingJournalLeavesTheLineUnmatchedThroughTheWiredPoster(t *testing.T) {
	ctx := context.Background()
	pool := freshMigratedDB(t, "fw105r2tx")

	// The fiscal year the leg posts into, OPEN (the census leg ensures the same calendar).
	mustExec(t, ctx, pool, `
		INSERT INTO fiscal_periods (year, month, status)
		SELECT 2026, m, 'OPEN' FROM generate_series(1, 12) AS m
		ON CONFLICT (year, month) DO UPDATE SET status = 'OPEN', closed_at = NULL`)

	var branchID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations WHERE loc_type = 'BRANCH' LIMIT 1`).Scan(&branchID); err != nil {
		t.Fatalf("a seeded branch: %v", err)
	}
	var bankAcctID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM bank_accounts WHERE gl_account_code = '1000'`).Scan(&bankAcctID); err != nil {
		t.Fatalf("the RBC operating bank account: %v", err)
	}

	const payID = "87057701-0000-4000-8000-000000000001"
	mustExec(t, ctx, pool, `
		INSERT INTO payments (id, payment_no, method, amount_cents, status, branch_id, gateway_ref)
		VALUES ($1::uuid, 'PAY-FW105R2TX', 'CARD', 60000, 'RECEIVED', $2::uuid, 'GTW-FW105R2-TX')`, payID, branchID)
	t.Cleanup(func() {
		mustExec(t, ctx, pool, `DELETE FROM payments WHERE id = $1::uuid`, payID)
	})

	txnDay := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	var lineID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO bank_statement_lines (bank_account_id, txn_date, description, reference, amount_cents, dedupe_hash, status)
		VALUES ($1, $2, 'CARD SETTLEMENT TX WIRING', 'GTW-FW105R2-TX', 60000, $3, 'UNMATCHED') RETURNING id::text`,
		bankAcctID, txnDay, bankrec.DedupeHash(txnDay, 60000, "CARD SETTLEMENT TX WIRING", "GTW-FW105R2-TX")).Scan(&lineID); err != nil {
		t.Fatalf("the statement line: %v", err)
	}
	t.Cleanup(func() {
		mustExec(t, ctx, pool, `DELETE FROM bank_matches WHERE statement_line_id = $1::uuid`, lineID)
		mustExec(t, ctx, pool, `DELETE FROM bank_statement_lines WHERE id = $1::uuid`, lineID)
	})

	// The served wiring's own adapters (wire_operations.go), no test poster.
	glSvc := gl.NewService(gl.NewRepository(pool))
	svc := bankrec.NewService(bankrec.NewRepository(pool)).
		WithJournalPoster(glPosterAdapter{svc: glSvc}).
		WithGLBalances(glBalanceAdapter{svc: glSvc}).
		WithJournals(glSvc)

	// The GL refuses the clearing journal: 1010 is inactive.
	mustExec(t, ctx, pool, `UPDATE gl_accounts SET is_active = false WHERE code = '1010'`)
	t.Cleanup(func() {
		mustExec(t, ctx, pool, `UPDATE gl_accounts SET is_active = true WHERE code = '1010'`)
	})

	if _, err := svc.ManualMatch(ctx, bankrec.MatchInput{
		Mode: "manual", StatementLineID: lineID, TargetType: bankrec.TargetPayment, TargetRefID: payID,
		AmountCents: 60000, CanAccessBranch: bankrec.AllBranches,
	}, ""); err == nil {
		t.Fatalf("P1-A: the hand match was accepted while the GL refuses the clearing journal")
	}

	// Because the match and its journal share one transaction through the production poster, nothing is left:
	// the line stays UNMATCHED, the payment stays RECEIVED (never claimed, never DEPOSITED), and no live match
	// row and no 1010 leg exists.
	assertFw105R2LineStatus(t, pool, lineID, "UNMATCHED")
	assertFw105R2PaymentStatus(t, pool, payID, "RECEIVED")
	var liveMatches int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM bank_matches WHERE statement_line_id = $1::uuid AND unmatched_at IS NULL`, lineID).Scan(&liveMatches); err != nil {
		t.Fatalf("count live matches: %v", err)
	}
	if liveMatches != 0 {
		t.Fatalf("P1-A: %d live bank_matches row(s) left behind a refused journal, want 0", liveMatches)
	}
	var clearingLegs int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM gl_journal_lines l JOIN gl_accounts a ON a.id = l.account_id WHERE a.code = '1010'`).Scan(&clearingLegs); err != nil {
		t.Fatalf("count 1010 legs: %v", err)
	}
	if clearingLegs != 0 {
		t.Fatalf("P1-A: %d gl_journal_lines leg(s) on 1010 left behind a refused journal, want 0", clearingLegs)
	}
}

func assertFw105R2LineStatus(t *testing.T, pool *pgxpool.Pool, lineID, want string) {
	t.Helper()
	var got string
	if err := pool.QueryRow(context.Background(),
		`SELECT status FROM bank_statement_lines WHERE id = $1::uuid`, lineID).Scan(&got); err != nil {
		t.Fatalf("line status: %v", err)
	}
	if got != want {
		t.Fatalf("P1-A: statement line status = %s, want %s", got, want)
	}
}

func assertFw105R2PaymentStatus(t *testing.T, pool *pgxpool.Pool, payID, want string) {
	t.Helper()
	var got string
	if err := pool.QueryRow(context.Background(),
		`SELECT status FROM payments WHERE id = $1::uuid`, payID).Scan(&got); err != nil {
		t.Fatalf("payment status: %v", err)
	}
	if got != want {
		t.Fatalf("P1-A: payment status = %s, want %s", got, want)
	}
}
