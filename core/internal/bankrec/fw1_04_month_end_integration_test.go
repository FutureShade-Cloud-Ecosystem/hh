//go:build integration

package bankrec

// fw1_04_month_end_integration_test.go (FW1-04): bank reconciliation month end that can close a real month.
//
// Red-first legs:
// 1. A month matched in the following month cannot sign off today (GLBalanceCents reads one month's movement instead of
//    AccountBalanceAsOf, and clearing journals post on the processing clock instead of the statement line's txn_date).
// 2. A month with one deposit in transit and one outstanding cheque cannot sign off today.
// 3. Sign-off before month end is accepted today.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	"github.com/jackc/pgx/v5/pgxpool"
)

type fw104GLPoster struct {
	svc   *gl.Service
	clock func() time.Time
}

func (p fw104GLPoster) PostJournal(ctx context.Context, ext gl.ExternalJournal) (string, error) {
	if ext.DocumentDate.IsZero() && p.clock != nil {
		ext.DocumentDate = p.clock()
	}
	return p.svc.PostExternal(ctx, ext)
}

func fw104EnsurePeriods(t *testing.T, pool *pgxpool.Pool, year int, at time.Time) {
	t.Helper()
	ctx := context.Background()
	for m := 1; m <= 12; m++ {
		if _, err := pool.Exec(ctx, `
			INSERT INTO fiscal_periods (year, month, status, created_at, updated_at)
			VALUES ($1, $2, 'OPEN', $3, $3)
			ON CONFLICT (year, month) DO UPDATE SET status = 'OPEN', closed_at = NULL, updated_at = $3`,
			year, m, at); err != nil {
			t.Fatalf("ensure period %d-%02d: %v", year, m, err)
		}
	}
}

func fw104CleanYear(t *testing.T, pool *pgxpool.Pool, acct string, year int) {
	t.Helper()
	ctx := context.Background()
	_, _ = pool.Exec(ctx, `DELETE FROM bank_matches WHERE statement_line_id IN (SELECT id FROM bank_statement_lines WHERE bank_account_id = $1::uuid AND EXTRACT(YEAR FROM txn_date) = $2)`, acct, year)
	_, _ = pool.Exec(ctx, `DELETE FROM bank_statement_lines WHERE bank_account_id = $1::uuid AND EXTRACT(YEAR FROM txn_date) = $2`, acct, year)
	_, _ = pool.Exec(ctx, `DELETE FROM bank_reconciliations WHERE bank_account_id = $1::uuid AND year = $2`, acct, year)
	_, _ = pool.Exec(ctx, `DELETE FROM gl_journal_lines WHERE journal_id IN (SELECT id FROM gl_journals WHERE fiscal_year = $1)`, year)
	_, _ = pool.Exec(ctx, `UPDATE gl_journals SET reverses_journal_id = NULL WHERE fiscal_year = $1`, year)
	_, _ = pool.Exec(ctx, `DELETE FROM gl_journals WHERE fiscal_year = $1`, year)
}

func fw104InsertLineAt(t *testing.T, pool *pgxpool.Pool, acct string, txnDate time.Time, desc, ref string, cents int64) string {
	t.Helper()
	return insertLine(t, pool, acct, txnDate, desc, ref, cents)
}

// Leg 1: A month (2023-05) whose statement lines are matched in the following month (2023-06) against an account with
// a prior month's closing balance ($1,000.00 in 2023-04, plus a +$500.00 card settlement in 2023-05 matched on
// 2023-06-05) must post its clearing journal on the statement line's date (in 2023-05), read the closing GL balance as
// of 2023-05 month end ($1,500.00), and sign off 2023-05 at zero variance.
func TestIntegration_FW1_04_AMonthMatchedInTheFollowingMonthCanSignOff(t *testing.T) {
	pool := intPool(t)
	cleanTables(t, pool)
	acct := rbcAccountID(t, pool)
	branch := aBranch(t, pool)
	ctx := context.Background()

	const year = 2023
	aprilDay := time.Date(year, 4, 15, 0, 0, 0, 0, time.UTC)
	mayDay := time.Date(year, 5, 22, 0, 0, 0, 0, time.UTC)
	juneClock := time.Date(year, 6, 5, 14, 0, 0, 0, time.UTC)

	fw104CleanYear(t, pool, acct, year)
	fw104EnsurePeriods(t, pool, year, aprilDay)
	t.Cleanup(func() { fw104CleanYear(t, pool, acct, year) })

	// Prior month (2023-04): $1,000.00 opening deposit in GL 1000, matched and completed at $1,000.00.
	glSvc := gl.NewService(gl.NewRepository(pool))
	aprilJID, err := glSvc.PostExternal(ctx, gl.ExternalJournal{
		IdempotencyKey: "fw104-leg1-april-open",
		Source:         "ADJUSTMENT",
		Memo:           "April opening deposit",
		DocumentDate:   aprilDay.Add(12 * time.Hour),
		Lines: []gl.ExternalLine{
			{AccountCode: "1000", DebitCents: 100000, Memo: "bank"},
			{AccountCode: "4950", CreditCents: 100000, Memo: "offset"},
		},
	})
	if err != nil {
		t.Fatalf("post april journal: %v", err)
	}
	aprilLine := fw104InsertLineAt(t, pool, acct, aprilDay, "APRIL OPENING DEPOSIT", "APR-1", 100000)

	// In June (2023-06-05), the bookkeeper reconciles April and May.
	const payID = "87040001-0000-4000-8000-000000000001"
	insertPayment(t, pool, payID, branch, "CARD", "GTW-FW104-1", 50000)
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM payments WHERE id = $1::uuid`, payID) })

	mayLine := fw104InsertLineAt(t, pool, acct, mayDay, "MAY CARD SETTLEMENT", "GTW-FW104-1", 50000)
	paySeam := &fakePayments{cands: []CandidatePayment{{PaymentID: payID, BranchID: branch, GatewayRef: "GTW-FW104-1", AmountCents: 50000, ReceivedAt: mayDay}}}
	svc := NewService(NewRepository(pool)).
		WithPayments(paySeam).
		WithJournals(glSvc).
		WithJournalPoster(fw104GLPoster{svc: glSvc, clock: func() time.Time { return juneClock }}).
		WithGLBalances(glSvc)

	if _, err := svc.ManualMatch(ctx, MatchInput{
		Mode: "manual", StatementLineID: aprilLine, TargetType: TargetJournal, TargetRefID: aprilJID, AmountCents: 100000, CanAccessBranch: AllBranches,
	}, ""); err != nil {
		t.Fatalf("match april line: %v", err)
	}
	aprilRec, err := svc.OpenReconciliation(ctx, ReconInput{BankAccountID: acct, Year: year, Month: 4, StatementBalanceCents: 100000})
	if err != nil {
		t.Fatalf("open april: %v", err)
	}
	if _, err := svc.CompleteReconciliation(ctx, aprilRec.ID, ""); err != nil {
		t.Fatalf("complete april: %v", err)
	}

	// Match the May statement line on June 5: its clearing journal must carry the May statement line's date.
	mayMatch, err := svc.ManualMatch(ctx, MatchInput{
		Mode: "manual", StatementLineID: mayLine, TargetType: TargetPayment, TargetRefID: payID, AmountCents: 50000, CanAccessBranch: AllBranches,
	}, "")
	if err != nil {
		t.Fatalf("match may line in june: %v", err)
	}
	var clearingMonth int
	if err := pool.QueryRow(ctx, `
		SELECT p.month FROM gl_journals j JOIN fiscal_periods p ON p.id = j.period_id WHERE j.id = $1::uuid`,
		mayMatch.JournalID).Scan(&clearingMonth); err != nil {
		t.Fatalf("read clearing journal period: %v", err)
	}
	if clearingMonth != 5 {
		t.Errorf("Leg 1: May statement line matched in June posted its clearing journal into month %d, want 5", clearingMonth)
	}

	// Open and sign off May 2023 at its closing statement balance of $1,500.00 (150,000 cents).
	mayRec, err := svc.OpenReconciliation(ctx, ReconInput{BankAccountID: acct, Year: year, Month: 5, StatementBalanceCents: 150000})
	if err != nil {
		t.Fatalf("open may: %v", err)
	}
	if mayRec.GLBalanceCents != 150000 || mayRec.VarianceCents != 0 {
		t.Errorf("Leg 1: open May 2023 read GLBalanceCents=%d VarianceCents=%d, want 150000 and 0", mayRec.GLBalanceCents, mayRec.VarianceCents)
	}
	done, err := svc.CompleteReconciliation(ctx, mayRec.ID, "")
	t.Logf("Leg 1 complete May 2023 in June: %v", err)
	if err != nil {
		t.Fatalf("Leg 1: a month matched in the following month could not sign off: %v", err)
	}
	if done.Status != ReconCompleted || done.GLBalanceCents != 150000 || done.VarianceCents != 0 {
		t.Errorf("Leg 1: completed May row = %+v, want COMPLETED with GL 150000 and variance 0", done)
	}
}

// Leg 2: A month (2023-05) with one deposit in transit (+$300.00 booked in GL 1000 on May 30, not yet on the May
// statement) and one outstanding cheque (-$120.00 booked in GL 1000 on May 30, not yet on the May statement) must
// compute adjusted bank balance = statement ($1,400.00) + deposits_in_transit ($300.00) - outstanding_cheques ($120.00)
// = $1,580.00 == GL closing balance ($1,580.00) and sign off at zero variance.
func TestIntegration_FW1_04_AMonthWithOneDepositInTransitAndOneOutstandingChequeCanSignOff(t *testing.T) {
	pool := intPool(t)
	cleanTables(t, pool)
	acct := rbcAccountID(t, pool)
	ctx := context.Background()

	const year = 2023
	may10 := time.Date(year, 5, 10, 0, 0, 0, 0, time.UTC)
	may30 := time.Date(year, 5, 30, 12, 0, 0, 0, time.UTC)
	juneClock := time.Date(year, 6, 5, 14, 0, 0, 0, time.UTC)

	fw104CleanYear(t, pool, acct, year)
	fw104EnsurePeriods(t, pool, year, may10)
	t.Cleanup(func() { fw104CleanYear(t, pool, acct, year) })

	glSvc := gl.NewService(gl.NewRepository(pool))
	// Cleared deposit in May: $1,400.00 (140,000 cents), on the May statement and matched.
	clearedJID, err := glSvc.PostExternal(ctx, gl.ExternalJournal{
		IdempotencyKey: "fw104-leg2-cleared",
		Source:         "ADJUSTMENT",
		Memo:           "May cleared deposit",
		DocumentDate:   may10.Add(12 * time.Hour),
		Lines: []gl.ExternalLine{
			{AccountCode: "1000", DebitCents: 140000, Memo: "bank"},
			{AccountCode: "4950", CreditCents: 140000, Memo: "offset"},
		},
	})
	if err != nil {
		t.Fatalf("post cleared deposit: %v", err)
	}
	clearedLine := fw104InsertLineAt(t, pool, acct, may10, "MAY CLEARED DEPOSIT", "CLR-1", 140000)

	// One deposit in transit (+$300.00 = 30,000 cents) and one outstanding cheque (-$120.00 = 12,000 cents) booked in
	// GL 1000 on May 30, neither on the May statement. GL closing balance at May 31 is 140,000 + 30,000 - 12,000 = 158,000.
	if _, err := glSvc.PostExternal(ctx, gl.ExternalJournal{
		IdempotencyKey: "fw104-leg2-dit",
		Source:         "ADJUSTMENT",
		Memo:           "May 30 deposit in transit",
		DocumentDate:   may30,
		Lines: []gl.ExternalLine{
			{AccountCode: "1000", DebitCents: 30000, Memo: "deposit in transit"},
			{AccountCode: "4950", CreditCents: 30000, Memo: "offset"},
		},
	}); err != nil {
		t.Fatalf("post deposit in transit: %v", err)
	}
	if _, err := glSvc.PostExternal(ctx, gl.ExternalJournal{
		IdempotencyKey: "fw104-leg2-cheque",
		Source:         "ADJUSTMENT",
		Memo:           "May 30 cheque 1044 to vendor",
		DocumentDate:   may30,
		Lines: []gl.ExternalLine{
			{AccountCode: "6300", DebitCents: 12000, Memo: "expense"},
			{AccountCode: "1000", CreditCents: 12000, Memo: "cheque 1044"},
		},
	}); err != nil {
		t.Fatalf("post outstanding cheque: %v", err)
	}

	svc := NewService(NewRepository(pool)).
		WithJournals(glSvc).
		WithJournalPoster(fw104GLPoster{svc: glSvc, clock: func() time.Time { return juneClock }}).
		WithGLBalances(glSvc)

	if _, err := svc.ManualMatch(ctx, MatchInput{
		Mode: "manual", StatementLineID: clearedLine, TargetType: TargetJournal, TargetRefID: clearedJID, AmountCents: 140000, CanAccessBranch: AllBranches,
	}, ""); err != nil {
		t.Fatalf("match cleared line: %v", err)
	}

	rec, err := svc.OpenReconciliation(ctx, ReconInput{BankAccountID: acct, Year: year, Month: 5, StatementBalanceCents: 140000})
	if err != nil {
		t.Fatalf("open may: %v", err)
	}
	if rec.VarianceCents != 0 {
		t.Errorf("Leg 2: open May with 1 deposit in transit and 1 outstanding cheque computed VarianceCents=%d, want 0", rec.VarianceCents)
	}
	done, err := svc.CompleteReconciliation(ctx, rec.ID, "")
	t.Logf("Leg 2 complete May with deposit in transit and outstanding cheque: %v", err)
	if err != nil {
		t.Fatalf("Leg 2: a month with one deposit in transit and one outstanding cheque could not sign off: %v", err)
	}
	if done.Status != ReconCompleted || done.VarianceCents != 0 {
		t.Errorf("Leg 2: completed row = %+v, want COMPLETED with variance 0", done)
	}
}

// Leg 3: Sign-off before month end (e.g. for a month that has not reached month end yet) is accepted today and must be refused.
func TestIntegration_FW1_04_SignOffBeforeMonthEndIsRefused(t *testing.T) {
	pool := intPool(t)
	cleanTables(t, pool)
	acct := rbcAccountID(t, pool)
	ctx := context.Background()

	const year = 2087
	dec10 := time.Date(year, 12, 10, 0, 0, 0, 0, time.UTC)

	fw104CleanYear(t, pool, acct, year)
	fw104EnsurePeriods(t, pool, year, dec10)
	t.Cleanup(func() { fw104CleanYear(t, pool, acct, year) })

	glSvc := gl.NewService(gl.NewRepository(pool))
	jID, err := glSvc.PostExternal(ctx, gl.ExternalJournal{
		IdempotencyKey: "fw104-leg3-midmonth",
		Source:         "ADJUSTMENT",
		Memo:           "December deposit",
		DocumentDate:   dec10.Add(12 * time.Hour),
		Lines: []gl.ExternalLine{
			{AccountCode: "1000", DebitCents: 60000, Memo: "bank"},
			{AccountCode: "4950", CreditCents: 60000, Memo: "offset"},
		},
	})
	if err != nil {
		t.Fatalf("post journal: %v", err)
	}
	lineID := fw104InsertLineAt(t, pool, acct, dec10, "DEC DEPOSIT MIDMONTH", "MID-1", 60000)

	svc := NewService(NewRepository(pool)).
		WithJournals(glSvc).
		WithJournalPoster(fw104GLPoster{svc: glSvc, clock: func() time.Time { return dec10 }}).
		WithGLBalances(glSvc)

	if _, err := svc.ManualMatch(ctx, MatchInput{
		Mode: "manual", StatementLineID: lineID, TargetType: TargetJournal, TargetRefID: jID, AmountCents: 60000, CanAccessBranch: AllBranches,
	}, ""); err != nil {
		t.Fatalf("match line: %v", err)
	}
	rec, err := svc.OpenReconciliation(ctx, ReconInput{BankAccountID: acct, Year: year, Month: 12, StatementBalanceCents: 60000})
	if err != nil {
		t.Fatalf("open dec: %v", err)
	}
	_, err = svc.CompleteReconciliation(ctx, rec.ID, "")
	t.Logf("Leg 3 complete 2087-12 before month end: %v", err)
	if err == nil {
		t.Fatalf("Leg 3: sign off before month end (2087-12) was accepted")
	}
	if !strings.Contains(err.Error(), "month end") {
		t.Errorf("Leg 3: refusal %q does not mention month end", err.Error())
	}
}
