//go:build integration

package bankrec

// FW1-05b (S4's review of sync stack 1, P2): a LODGEMENT or PAYMENT target matches only a BANK account
// whose GL code is the lodgement journal's debit account (1000). The till journals DR 1000 / CR 1020 at
// lodgement, so only that account's lines explain a payment or a lodgement; a CREDIT_CARD line (2110 to
// 2115) matched to a lodgement would book DR 2110 / CR 1020, crediting 1020 twice while 1000 keeps the
// deposit. Refused in words, nothing written. The same fence holds the auto match: its candidates never
// pair a line on a non bank account.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestIntegration_FW1_05b_CreditCardLodgementMatchRefused is S4's $77.00 case: a $77.00 line on 2110
// (a CREDIT_CARD account) hand matched to a $77.00 lodgement is refused in words, with nothing written.
func TestIntegration_FW1_05b_CreditCardLodgementMatchRefused(t *testing.T) {
	pool := intPool(t)
	cleanTables(t, pool)
	cardAcct := fw105bAccountByCode(t, pool, "2110")
	branch := aBranch(t, pool)
	ctx := context.Background()

	const year = 2023
	may24 := time.Date(year, 5, 24, 0, 0, 0, 0, time.UTC)
	fw104CleanYear(t, pool, cardAcct, year)
	fw104EnsurePeriods(t, pool, year, may24)
	t.Cleanup(func() { fw104CleanYear(t, pool, cardAcct, year) })

	glSvc := gl.NewService(gl.NewRepository(pool))
	lodgements := &fakeLodgements{open: []Lodgement{{
		MovementID: "55555555-5555-4555-8555-555555555501", BranchID: branch, AmountCents: 7700,
	}}}
	svc := NewService(NewRepository(pool)).
		WithJournals(glSvc).
		WithJournalPoster(fw104GLPoster{svc: glSvc, clock: func() time.Time { return may24 }}).
		WithGLBalances(glSvc).
		WithLodgements(lodgements)

	lineID := fw104InsertLineAt(t, pool, cardAcct, may24, "VISA DEPOSIT 05B", "DEP-05B-VISA", 7700)

	// The package's other legs post bankrec journals of their own on the shared database, so the leg
	// asserts on the DELTA: the refused match must write no new bankrec journal.
	var bankrecJournalsBefore int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM gl_journals WHERE idempotency_key LIKE 'bankrec:%'`).Scan(&bankrecJournalsBefore); err != nil {
		t.Fatalf("bankrec journals before: %v", err)
	}

	_, err := svc.ManualMatch(ctx, MatchInput{
		Mode: "manual", StatementLineID: lineID, TargetType: TargetLodgement,
		TargetRefID: "55555555-5555-4555-8555-555555555501",
		AmountCents: 7700, CanAccessBranch: AllBranches,
	}, "")
	if err == nil {
		t.Fatalf("a CREDIT_CARD line was hand matched to a lodgement: 2110 would debit and 1020 credit a second time")
	}
	if !strings.Contains(err.Error(), "1000") {
		t.Fatalf("refusal does not name the bank account the lodgement journals into: %v", err)
	}
	var lineStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM bank_statement_lines WHERE id::text = $1`, lineID).Scan(&lineStatus); err != nil {
		t.Fatalf("line status: %v", err)
	}
	if lineStatus != "UNMATCHED" {
		t.Fatalf("line is %s, want UNMATCHED (nothing written)", lineStatus)
	}
	var matches, journals int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM bank_matches WHERE statement_line_id::text = $1`, lineID).Scan(&matches); err != nil {
		t.Fatalf("match rows: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM gl_journals WHERE idempotency_key LIKE 'bankrec:%'`).Scan(&journals); err != nil {
		t.Fatalf("bankrec journals: %v", err)
	}
	if matches != 0 || journals != bankrecJournalsBefore {
		t.Fatalf("the refused match wrote %d match rows and %d new bankrec journals, want none", matches, journals-bankrecJournalsBefore)
	}
}

// TestIntegration_FW1_05b_AutoCandidatesNeverPairANonBankAccount holds the same fence on the auto
// match: an approved gateway rule and a waiting card payment candidate cannot pair a line that sits
// on 2110, and the run writes nothing.
func TestIntegration_FW1_05b_AutoCandidatesNeverPairANonBankAccount(t *testing.T) {
	pool := intPool(t)
	cleanTables(t, pool)
	cardAcct := fw105bAccountByCode(t, pool, "2110")
	branch := aBranch(t, pool)
	ctx := context.Background()

	const year = 2023
	may24 := time.Date(year, 5, 24, 0, 0, 0, 0, time.UTC)
	fw104CleanYear(t, pool, cardAcct, year)
	fw104EnsurePeriods(t, pool, year, may24)
	t.Cleanup(func() { fw104CleanYear(t, pool, cardAcct, year) })

	glSvc := gl.NewService(gl.NewRepository(pool))
	svc := NewService(NewRepository(pool)).
		WithJournals(glSvc).
		WithJournalPoster(fw104GLPoster{svc: glSvc, clock: func() time.Time { return may24 }}).
		WithGLBalances(glSvc).
		WithPayments(&fakePayments{cands: []CandidatePayment{{
			PaymentID: "66666666-6666-4666-8666-666666666601", BranchID: branch, AmountCents: 7700,
			GatewayRef: "VISA-05B-AUTO", ReceivedAt: may24,
		}}})

	lineID := fw104InsertLineAt(t, pool, cardAcct, may24, "VISA BATCH 05B", "VISA-05B-AUTO", 7700)
	// The candidate stands for a real RECEIVED card payment row: the auto claim runs against the real
	// payments table, so the row must exist for today's path to pair at all.
	insertPayment(t, pool, "66666666-6666-4666-8666-666666666601", branch, "CARD", "VISA-05B-AUTO", 7700)

	summary, err := svc.AutoMatchReach(ctx, cardAcct, may24.AddDate(0, 0, -1), may24.AddDate(0, 0, 2), "", AllBranches)
	if err != nil {
		t.Fatalf("auto match on the card account: %v", err)
	}
	if summary.Matched != 0 {
		t.Fatalf("the auto match paired %d line(s) on a CREDIT_CARD account, want none", summary.Matched)
	}
	var lineStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM bank_statement_lines WHERE id::text = $1`, lineID).Scan(&lineStatus); err != nil {
		t.Fatalf("line status: %v", err)
	}
	if lineStatus != "UNMATCHED" {
		t.Fatalf("line is %s, want UNMATCHED (nothing written)", lineStatus)
	}
}

// fw105bAccountByCode reads one seeded bank account by its GL code.
func fw105bAccountByCode(t *testing.T, pool *pgxpool.Pool, code string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(),
		`SELECT id::text FROM bank_accounts WHERE gl_account_code = $1`, code).Scan(&id); err != nil {
		t.Fatalf("bank account %s: %v", code, err)
	}
	return id
}
