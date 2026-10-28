//go:build integration

// fw1_08b_part_b_round2_integration_test.go is the P3-3 red (FW1-08b part B round 2,
// FROM-PRINCIPAL 309): the return leg's own guard counted the WHOLE box's branchless journal
// lines. Migration 0446's CHECK refuses a branchless line before it lands, so on a migrated box
// that count is zero whatever happened: the guard can never fire, and it would name another
// package's row rather than the return's if it ever did.
//
// The guard must count the return's OWN journal lines that do not carry the branch the return was
// rung at. This file holds the guard, so the return leg and this leg count the same thing.
package pos

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// fw1ReturnBranchGuard answers how many of the given journals' own journal lines carry a branch
// other than the one the transaction was rung at.
//
// FW1-08b part B round 2 (P3-3): on the head this was the box's branchless line count, which
// migration 0446's CHECK has made unable to fire. The count is scoped to the journals it is
// about, and it asks for the lines that do not carry the rung branch, because after 0446 a line
// with no branch cannot exist at all: a NULL scoped count would be dead too.
//
// The scope and the rung branch are carried in from the start, so the fix changes the shape of
// the count and not its call sites.
func fw1ReturnBranchGuard(ctx context.Context, pool *pgxpool.Pool, journalIDs []string, rungBranchID string) (int, error) {
	if len(journalIDs) == 0 {
		return 0, nil
	}
	var n int
	err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM gl_journal_lines
		  WHERE journal_id::text = ANY($1::text[])
		    AND branch_id IS DISTINCT FROM $2::uuid`,
		journalIDs, rungBranchID).Scan(&n)
	return n, err
}

// TestIntegration_FW1_08b_PartB_Round2_TheReturnLegGuardCountsItsOwnJournalLines is the P3-3 red:
// a journal line stamped at the WRONG branch is exactly the defect the guard was written for, and
// the box wide count cannot see it. The guard must see it, so the count must be scoped to the
// return's own journals.
func TestIntegration_FW1_08b_PartB_Round2_TheReturnLegGuardCountsItsOwnJournalLines(t *testing.T) {
	s := newTrentonStack(t)
	ctx := context.Background()

	// A journal the return would have written, with both of its lines stamped at the WRONG
	// branch: the header names the branch the return was rung at, the lines name another one.
	var periodID string
	var fiscalYear int
	if err := s.pool.QueryRow(ctx,
		`SELECT id::text, year FROM fiscal_periods WHERE status = 'OPEN' ORDER BY year, month LIMIT 1`,
	).Scan(&periodID, &fiscalYear); err != nil {
		t.Fatalf("the seeded fiscal period: %v", err)
	}
	var jid string
	if err := s.pool.QueryRow(ctx, `
		INSERT INTO gl_journals (fiscal_year, entry_no, period_id, source, memo, status, posted_at, idempotency_key, branch_id)
		VALUES ($1,
		        (SELECT COALESCE(MAX(entry_no), 0) + 1 FROM gl_journals WHERE fiscal_year = $1),
		        $2::uuid, 'MANUAL', 'fw1 08b part b round 2: a journal stamped at the wrong branch',
		        'DRAFT', now(), 'fw108pb-r2-wrongbranch-' || gen_random_uuid()::text, $3::uuid)
		RETURNING id::text`, fiscalYear, periodID, kingstonBranchID).Scan(&jid); err != nil {
		t.Fatalf("plant the mis-stamped journal: %v", err)
	}
	t.Cleanup(func() {
		_, _ = s.pool.Exec(context.Background(), `DELETE FROM gl_journal_lines WHERE journal_id = $1::uuid`, jid)
		_, _ = s.pool.Exec(context.Background(), `DELETE FROM gl_journals WHERE id = $1::uuid`, jid)
	})
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO gl_journal_lines (journal_id, line_no, account_id, debit_cents, branch_id, memo)
		VALUES ($1::uuid, 9001, (SELECT id FROM gl_accounts WHERE is_active ORDER BY code LIMIT 1), 100, $2::uuid, 'stamped at the wrong branch')`,
		jid, trentonBranchID); err != nil {
		t.Fatalf("plant the mis-stamped debit line: %v", err)
	}
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO gl_journal_lines (journal_id, line_no, account_id, credit_cents, branch_id, memo)
		VALUES ($1::uuid, 9002, (SELECT id FROM gl_accounts WHERE is_active ORDER BY code LIMIT 1), 100, $2::uuid, 'stamped at the wrong branch')`,
		jid, trentonBranchID); err != nil {
		t.Fatalf("plant the mis-stamped credit line: %v", err)
	}

	n, err := fw1ReturnBranchGuard(ctx, s.pool, []string{jid}, kingstonBranchID)
	if err != nil {
		t.Fatalf("the return leg's guard: %v", err)
	}
	if n != 2 {
		t.Fatalf("the return leg's guard counted %d of the journal's own mis-stamped line(s), want 2: the guard must count the return's own journal lines, not the whole box", n)
	}
}
