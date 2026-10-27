//go:build integration

package bankrec

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
)

// P2-1 (S4's review of PR 632, round 3): an unmatch must reverse the clearing journal the MATCH actually
// posted, by the match's own journal id, not recompute the account from today's rule. A match made while
// main's old arm stood (every hand matched payment credited 1010) must unmatch back through 1010, so the
// undo clears the old credit instead of moving the error between accounts. The payment's method is only
// the fallback for a match that posted no journal (an older match row).
func TestIntegration_FW1_05_R4_UnmatchMirrorsTheMatchsOwnJournal(t *testing.T) {
	pool := intPool(t)
	cleanTables(t, pool)
	acct := rbcAccountID(t, pool)
	branch := aBranch(t, pool)
	ctx := context.Background()

	const year = 2023
	const amount = int64(5200) // $52.00, S4's probe size
	jun01 := time.Date(year, 6, 1, 0, 0, 0, 0, time.UTC)
	jun02 := time.Date(year, 6, 2, 0, 0, 0, 0, time.UTC)
	fw104CleanYear(t, pool, acct, year)
	fw104EnsurePeriods(t, pool, year, jun01)
	t.Cleanup(func() { fw104CleanYear(t, pool, acct, year) })

	glSvc := gl.NewService(gl.NewRepository(pool))
	svc := NewService(NewRepository(pool)).
		WithJournals(glSvc).
		WithJournalPoster(fw104GLPoster{svc: glSvc, clock: func() time.Time { return jun01 }}).
		WithGLBalances(glSvc)

	t.Run("mirrors the journal the match posted", func(t *testing.T) {
		const payID = "87057705-0000-4000-8000-000000000001"
		insertPayment(t, pool, payID, branch, "CHEQUE", "", amount)
		lineID := fw104InsertLineAt(t, pool, acct, jun01, "CHEQUE DEPOSIT R4 MIRROR", "DEP-R4-MIRROR", amount)

		m, err := svc.ManualMatch(ctx, MatchInput{
			Mode: "manual", StatementLineID: lineID, TargetType: TargetPayment, TargetRefID: payID,
			AmountCents: amount, CanAccessBranch: AllBranches,
		}, "")
		if err != nil {
			t.Fatalf("cheque hand match: %v", err)
		}
		// The match booked today's arm: DR 1000 / CR 1020. Pin the premise before the probe.
		assertJournalLegs(t, pool, m.JournalID, map[string][2]int64{
			AcctBank:        {amount, 0},
			AcctUndeposited: {0, amount},
		})

		// Stand in for a match made while main's old arm stood: that arm credited 1010 for every hand
		// matched payment, so the journal's credit line sits on 1010, not on the tender's 1020.
		if _, err := pool.Exec(ctx, `
			UPDATE gl_journal_lines
			   SET account_id = (SELECT id FROM gl_accounts WHERE code = $1)
			 WHERE journal_id = $2::uuid AND credit_cents > 0`, AcctCardClearing, m.JournalID); err != nil {
			t.Fatalf("rebook the match journal's credit line onto the old arm's 1010: %v", err)
		}
		assertJournalLegs(t, pool, m.JournalID, map[string][2]int64{
			AcctBank:         {amount, 0},
			AcctCardClearing: {0, amount},
			AcctUndeposited:  {0, 0},
		})

		if err := svc.UnmatchReach(ctx, m.ID, "", AllBranches); err != nil {
			t.Fatalf("unmatch: %v", err)
		}

		// The reversal must mirror the journal AS BOOKED: DR 1010 gross / CR 1000, so the old credit is
		// cleared and 1020 never moves.
		var reversalID string
		if err := pool.QueryRow(ctx,
			`SELECT id::text FROM gl_journals WHERE idempotency_key = $1`, unmatchKey(m.ID)).Scan(&reversalID); err != nil {
			t.Fatalf("reversal journal lookup: %v", err)
		}
		assertJournalLegs(t, pool, reversalID, map[string][2]int64{
			AcctCardClearing: {amount, 0},
			AcctBank:         {0, amount},
			AcctUndeposited:  {0, 0},
		})

		// And the pair nets zero on every one of the three accounts: 1010 and 1020 are left where they
		// started (the old-arm credit undone, nothing booked into 1020).
		var wrong []string
		for _, code := range []string{AcctBank, AcctCardClearing, AcctUndeposited} {
			var net int64
			if err := pool.QueryRow(ctx, `
				SELECT COALESCE(SUM(l.debit_cents),0) - COALESCE(SUM(l.credit_cents),0)
				FROM gl_journal_lines l
				JOIN gl_accounts a ON a.id = l.account_id
				JOIN gl_journals j ON j.id = l.journal_id
				WHERE j.idempotency_key IN ($1, $2) AND a.code = $3`,
				clearingKey(m.ID), unmatchKey(m.ID), code).Scan(&net); err != nil {
				t.Fatalf("net for %s: %v", code, err)
			}
			if net != 0 {
				wrong = append(wrong, fmt.Sprintf("account %s legs = (net %d), want 0", code, net))
			}
		}
		if len(wrong) > 0 {
			t.Fatalf("P2-1: the unmatch did not mirror the match's own journal: %v", wrong)
		}
	})

	t.Run("falls back to the tender when the match posted none", func(t *testing.T) {
		const payID = "87057705-0000-4000-8000-000000000002"
		insertPayment(t, pool, payID, branch, "CHEQUE", "", amount)
		lineID := fw104InsertLineAt(t, pool, acct, jun02, "CHEQUE DEPOSIT R4 FALLBACK", "DEP-R4-FALLBACK", amount)

		m, err := svc.ManualMatch(ctx, MatchInput{
			Mode: "manual", StatementLineID: lineID, TargetType: TargetPayment, TargetRefID: payID,
			AmountCents: amount, CanAccessBranch: AllBranches,
		}, "")
		if err != nil {
			t.Fatalf("cheque hand match: %v", err)
		}
		// Stand in for an older match row from before the clearing journal existed: journal_id NULL.
		if _, err := pool.Exec(ctx, `UPDATE bank_matches SET journal_id = NULL WHERE id = $1::uuid`, m.ID); err != nil {
			t.Fatalf("clear the match's journal id: %v", err)
		}

		if err := svc.UnmatchReach(ctx, m.ID, "", AllBranches); err != nil {
			t.Fatalf("unmatch: %v", err)
		}

		// With no journal to mirror, the reversal follows the payment's method (a cheque clears 1020).
		var reversalID string
		if err := pool.QueryRow(ctx,
			`SELECT id::text FROM gl_journals WHERE idempotency_key = $1`, unmatchKey(m.ID)).Scan(&reversalID); err != nil {
			t.Fatalf("reversal journal lookup: %v", err)
		}
		assertJournalLegs(t, pool, reversalID, map[string][2]int64{
			AcctUndeposited: {amount, 0},
			AcctBank:        {0, amount},
		})
	})
}

// P2-2 (the ruling on S4's round 3 P2-2): an EFT receipt stays on 1020 at receipt (gl/posting.go books
// every non card receipt to 1020) and becomes a PAYMENT match target booking DR 1000 / CR 1020, the same
// arm as office cash and cheques, so 1000 holds only money the bank has confirmed. Manual match only; the
// auto path stays card only (the candidate seam reads method CARD). The leg: an office EFT receipt matched
// by hand to its bank line moves 1020 to 1000, and the unmatch moves it back.
func TestIntegration_FW1_05_R4_AnEFTReceiptMatchesByHandAndUnmatches(t *testing.T) {
	pool := intPool(t)
	cleanTables(t, pool)
	acct := rbcAccountID(t, pool)
	branch := aBranch(t, pool)
	ctx := context.Background()

	const year = 2023
	const amount = int64(6100) // $61.00
	jun03 := time.Date(year, 6, 3, 0, 0, 0, 0, time.UTC)
	fw104CleanYear(t, pool, acct, year)
	fw104EnsurePeriods(t, pool, year, jun03)
	t.Cleanup(func() { fw104CleanYear(t, pool, acct, year) })

	glSvc := gl.NewService(gl.NewRepository(pool))
	svc := NewService(NewRepository(pool)).
		WithJournals(glSvc).
		WithJournalPoster(fw104GLPoster{svc: glSvc, clock: func() time.Time { return jun03 }}).
		WithGLBalances(glSvc)

	const payID = "87057706-0000-4000-8000-000000000001"
	insertPayment(t, pool, payID, branch, "EFT", "", amount)
	lineID := fw104InsertLineAt(t, pool, acct, jun03, "EFT DEPOSIT R4", "DEP-R4-EFT", amount)

	m, err := svc.ManualMatch(ctx, MatchInput{
		Mode: "manual", StatementLineID: lineID, TargetType: TargetPayment, TargetRefID: payID,
		AmountCents: amount, CanAccessBranch: AllBranches,
	}, "")
	if err != nil {
		t.Fatalf("P2-2: an EFT receipt was refused a hand match to its bank line: %v", err)
	}
	// The match books the office arm: DR 1000 / CR 1020 (the account the receipt debited).
	assertJournalLegs(t, pool, m.JournalID, map[string][2]int64{
		AcctBank:        {amount, 0},
		AcctUndeposited: {0, amount},
	})

	if err := svc.UnmatchReach(ctx, m.ID, "", AllBranches); err != nil {
		t.Fatalf("P2-2: unmatch: %v", err)
	}
	var reversalID string
	if err := pool.QueryRow(ctx,
		`SELECT id::text FROM gl_journals WHERE idempotency_key = $1`, unmatchKey(m.ID)).Scan(&reversalID); err != nil {
		t.Fatalf("P2-2: reversal journal lookup: %v", err)
	}
	assertJournalLegs(t, pool, reversalID, map[string][2]int64{
		AcctUndeposited: {amount, 0},
		AcctBank:        {0, amount},
	})

	// The pair nets zero: the unmatch moves 1020 back.
	var wrong []string
	for _, code := range []string{AcctBank, AcctCardClearing, AcctUndeposited} {
		var net int64
		if err := pool.QueryRow(ctx, `
			SELECT COALESCE(SUM(l.debit_cents),0) - COALESCE(SUM(l.credit_cents),0)
			FROM gl_journal_lines l
			JOIN gl_accounts a ON a.id = l.account_id
			JOIN gl_journals j ON j.id = l.journal_id
			WHERE j.idempotency_key IN ($1, $2) AND a.code = $3`,
			clearingKey(m.ID), unmatchKey(m.ID), code).Scan(&net); err != nil {
			t.Fatalf("P2-2: net for %s: %v", code, err)
		}
		if net != 0 {
			wrong = append(wrong, fmt.Sprintf("account %s legs = (net %d), want 0", code, net))
		}
	}
	if len(wrong) > 0 {
		t.Fatalf("P2-2: the unmatch did not move 1020 back: %v", wrong)
	}
	assertLineStatus(t, pool, lineID, LineUnmatched)
	if st := paymentStatus(t, pool, payID); st != "RECEIVED" {
		t.Fatalf("P2-2: payment status = %s, want RECEIVED", st)
	}
}
