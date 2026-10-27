//go:build integration

package bankrec

// FW1-05b (FROM-PRINCIPAL 232, fail closed): a journal poster without the JournalPosterTx arm makes the
// write fail closed, refused in words with nothing written, instead of posting in two steps. The two step
// order (the match commits, then the journal posts outside) is how an orphan match comes back: a refused
// journal left a MATCHED line and a DEPOSITED payment with no book entry, unnoticed. No production path
// uses the fallback (the served poster at wireOperations carries the arm), so the fallback is a refusal.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
)

// armlessPoster is a test poster with ONLY the historical PostJournal: exactly the shape that must now
// fail closed, kept armless on purpose because this file's legs expect the refusal.
type armlessPoster struct{ svc *gl.Service }

func (p armlessPoster) PostJournal(ctx context.Context, in gl.ExternalJournal) (string, error) {
	return p.svc.PostExternal(ctx, in)
}

// TestIntegration_FW1_05b_MatchWithoutTheTxArmFailsClosed: a hand match served by an armless poster is
// refused in words and leaves no match, no claim, the line UNMATCHED.
func TestIntegration_FW1_05b_MatchWithoutTheTxArmFailsClosed(t *testing.T) {
	pool := intPool(t)
	cleanTables(t, pool)
	acct := rbcAccountID(t, pool)
	branch := aBranch(t, pool)
	ctx := context.Background()

	const year = 2023
	may24 := time.Date(year, 5, 24, 0, 0, 0, 0, time.UTC)
	fw104CleanYear(t, pool, acct, year)
	fw104EnsurePeriods(t, pool, year, may24)
	t.Cleanup(func() { fw104CleanYear(t, pool, acct, year) })

	glSvc := gl.NewService(gl.NewRepository(pool))
	svc := NewService(NewRepository(pool)).
		WithJournals(glSvc).
		WithJournalPoster(armlessPoster{svc: glSvc}).
		WithGLBalances(glSvc)

	const amount = int64(5200)
	payID := "87057704-0000-4000-8000-000000055561"
	insertPayment(t, pool, payID, branch, "CARD", "", amount)
	lineID := fw104InsertLineAt(t, pool, acct, may24, "CARD DEPOSIT 05B ARM", "DEP-05B-ARM", amount)

	_, err := svc.ManualMatch(ctx, MatchInput{
		Mode: "manual", StatementLineID: lineID, TargetType: TargetPayment, TargetRefID: payID,
		AmountCents: amount, CanAccessBranch: AllBranches,
	}, "")
	if err == nil {
		t.Fatalf("an armless poster matched in two steps: the match committed with its journal outside the transaction")
	}
	if !strings.Contains(err.Error(), "inside the match's transaction") {
		t.Fatalf("refusal does not name the fail closed rule: %v", err)
	}
	if got := paymentStatus(t, pool, payID); got != "RECEIVED" {
		t.Fatalf("payment moved to %s, want RECEIVED (nothing written)", got)
	}
	var lineStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM bank_statement_lines WHERE id::text = $1`, lineID).Scan(&lineStatus); err != nil {
		t.Fatalf("line status: %v", err)
	}
	if lineStatus != "UNMATCHED" {
		t.Fatalf("line is %s, want UNMATCHED (nothing written)", lineStatus)
	}
	var matches int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM bank_matches WHERE statement_line_id::text = $1`, lineID).Scan(&matches); err != nil {
		t.Fatalf("match rows: %v", err)
	}
	if matches != 0 {
		t.Fatalf("the refused match left %d match rows, want none", matches)
	}
}

// TestIntegration_FW1_05b_UnmatchWithoutTheTxArmFailsClosed says why the unmatch expects the same
// refusal: a reversal posted after the unmatch's commit would free the payment while the original
// clearing journal stands, so an armless poster refuses the undo with nothing written.
func TestIntegration_FW1_05b_UnmatchWithoutTheTxArmFailsClosed(t *testing.T) {
	pool := intPool(t)
	cleanTables(t, pool)
	acct := rbcAccountID(t, pool)
	branch := aBranch(t, pool)
	ctx := context.Background()

	const year = 2023
	may24 := time.Date(year, 5, 24, 0, 0, 0, 0, time.UTC)
	fw104CleanYear(t, pool, acct, year)
	fw104EnsurePeriods(t, pool, year, may24)
	t.Cleanup(func() { fw104CleanYear(t, pool, acct, year) })

	glSvc := gl.NewService(gl.NewRepository(pool))
	armed := NewService(NewRepository(pool)).
		WithJournals(glSvc).
		WithJournalPoster(fw104GLPoster{svc: glSvc, clock: func() time.Time { return may24 }}).
		WithGLBalances(glSvc)
	armless := NewService(NewRepository(pool)).
		WithJournals(glSvc).
		WithJournalPoster(armlessPoster{svc: glSvc}).
		WithGLBalances(glSvc)

	const amount = int64(5300)
	payID := "87057704-0000-4000-8000-000000055562"
	insertPayment(t, pool, payID, branch, "CARD", "", amount)
	lineID := fw104InsertLineAt(t, pool, acct, may24, "CARD DEPOSIT 05B UNDO", "DEP-05B-UNDO", amount)

	m, err := armed.ManualMatch(ctx, MatchInput{
		Mode: "manual", StatementLineID: lineID, TargetType: TargetPayment, TargetRefID: payID,
		AmountCents: amount, CanAccessBranch: AllBranches,
	}, "")
	if err != nil {
		t.Fatalf("armed hand match: %v", err)
	}

	if err := armless.UnmatchReach(ctx, m.ID, "", AllBranches); err == nil {
		t.Fatalf("an armless poster unmatched in two steps: the payment was freed while the reversal posted outside the transaction")
	}
	if got := paymentStatus(t, pool, payID); got != "DEPOSITED" {
		t.Fatalf("payment moved to %s, want DEPOSITED (nothing written)", got)
	}
	var live int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM bank_matches WHERE id::text = $1 AND unmatched_at IS NULL`, m.ID).Scan(&live); err != nil {
		t.Fatalf("live match: %v", err)
	}
	if live != 1 {
		t.Fatalf("the refused unmatch left %d live matches, want the original still live", live)
	}
	var reversals int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM gl_journals WHERE idempotency_key = $1`, unmatchKey(m.ID)).Scan(&reversals); err != nil {
		t.Fatalf("reversal lookup: %v", err)
	}
	if reversals != 0 {
		t.Fatalf("the refused unmatch left %d reversal journals, want none", reversals)
	}
}
