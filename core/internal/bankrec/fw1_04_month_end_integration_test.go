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
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	identityPkg "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/logging"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
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
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`DELETE FROM bank_matches WHERE statement_line_id IN (SELECT id FROM bank_statement_lines WHERE bank_account_id = $1::uuid AND EXTRACT(YEAR FROM txn_date) = $2)`, []any{acct, year}},
		{`DELETE FROM bank_matches WHERE journal_id IN (SELECT id FROM gl_journals WHERE fiscal_year = $1)`, []any{year}},
		{`DELETE FROM bank_matches WHERE target_ref_id IN (SELECT id FROM gl_journals WHERE fiscal_year = $1)`, []any{year}},
		{`DELETE FROM bank_statement_lines WHERE bank_account_id = $1::uuid AND EXTRACT(YEAR FROM txn_date) = $2`, []any{acct, year}},
		{`DELETE FROM bank_reconciliations WHERE bank_account_id = $1::uuid AND year = $2`, []any{acct, year}},
		{`UPDATE gl_journals SET status = 'DRAFT', reverses_journal_id = NULL WHERE fiscal_year = $1 OR reverses_journal_id IN (SELECT id FROM gl_journals WHERE fiscal_year = $1)`, []any{year}},
		{`DELETE FROM gl_journal_lines WHERE journal_id IN (SELECT id FROM gl_journals WHERE fiscal_year = $1)`, []any{year}},
		{`DELETE FROM gl_journals WHERE fiscal_year = $1`, []any{year}},
		{`DELETE FROM gl_period_overrides o USING fiscal_periods p WHERE o.period_id = p.id AND p.year = $1 AND p.year != 2026`, []any{year}},
		{`DELETE FROM fiscal_periods WHERE year = $1 AND year != 2026`, []any{year}},
	} {
		if _, err := pool.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatalf("fw104CleanYear(%d) query %q: %v", year, q.sql, err)
		}
	}
}

func fw104InsertLineAt(t *testing.T, pool *pgxpool.Pool, acct string, txnDate time.Time, desc, ref string, cents int64) string {
	t.Helper()
	return insertLine(t, pool, acct, txnDate, desc, ref, cents)
}

// anyUserID is any seeded user's id: the gl read fences (FW1-10, merged from main) read a stamped caller, and a
// ledger read in a test needs one to hand.
func anyUserID(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var userID string
	if err := pool.QueryRow(context.Background(), `SELECT id::text FROM users LIMIT 1`).Scan(&userID); err != nil {
		t.Fatalf("select user: %v", err)
	}
	return userID
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
	if done.DepositsInTransitCents != 30000 || done.OutstandingChequesCents != 12000 || done.AdjustedBankBalanceCents != 158000 || done.MatchedLinesCount != 1 {
		t.Errorf("Leg 2: report figures = DIT %d OC %d Adjusted %d Matched %d, want 30000 12000 158000 1",
			done.DepositsInTransitCents, done.OutstandingChequesCents, done.AdjustedBankBalanceCents, done.MatchedLinesCount)
	}
	if len(done.DepositsInTransit) != 1 || len(done.OutstandingCheques) != 1 {
		t.Errorf("Leg 2: report lists = %d DIT, %d OC; want 1 and 1", len(done.DepositsInTransit), len(done.OutstandingCheques))
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
	if !strings.Contains(err.Error(), "month end") || !strings.Contains(err.Error(), "2088-01-01") {
		t.Errorf("Leg 3: refusal %q does not mention month end and first sign-off day 2088-01-01", err.Error())
	}

	// Round 2 (item 2.2): on the last day of the month itself (June 30 at noon in Toronto), sign-off must still be
	// refused and must name the first day it may be signed off (2023-07-01).
	const lastDayYear = 2023
	june10 := time.Date(lastDayYear, 6, 10, 0, 0, 0, 0, time.UTC)
	fw104CleanYear(t, pool, acct, lastDayYear)
	fw104EnsurePeriods(t, pool, lastDayYear, june10)
	t.Cleanup(func() { fw104CleanYear(t, pool, acct, lastDayYear) })

	jJune, err := glSvc.PostExternal(ctx, gl.ExternalJournal{
		IdempotencyKey: "fw104-leg3-june-lastday",
		Source:         "ADJUSTMENT",
		Memo:           "June deposit",
		DocumentDate:   june10.Add(12 * time.Hour),
		Lines: []gl.ExternalLine{
			{AccountCode: "1000", DebitCents: 40000, Memo: "bank"},
			{AccountCode: "4950", CreditCents: 40000, Memo: "offset"},
		},
	})
	if err != nil {
		t.Fatalf("post june journal: %v", err)
	}
	lineJune := fw104InsertLineAt(t, pool, acct, june10, "JUN DEPOSIT", "JUN-LAST-1", 40000)

	torontoLoc, err := time.LoadLocation("America/Toronto")
	if err != nil {
		t.Fatalf("load toronto: %v", err)
	}
	june30Noon := time.Date(lastDayYear, 6, 30, 12, 0, 0, 0, torontoLoc)
	svcLastDay := NewService(NewRepository(pool)).
		WithClock(func() time.Time { return june30Noon }).
		WithJournals(glSvc).
		WithJournalPoster(fw104GLPoster{svc: glSvc, clock: func() time.Time { return june10 }}).
		WithGLBalances(glSvc)

	if _, err := svcLastDay.ManualMatch(ctx, MatchInput{
		Mode: "manual", StatementLineID: lineJune, TargetType: TargetJournal, TargetRefID: jJune, AmountCents: 40000, CanAccessBranch: AllBranches,
	}, ""); err != nil {
		t.Fatalf("match june line: %v", err)
	}
	recJune, err := svcLastDay.OpenReconciliation(ctx, ReconInput{BankAccountID: acct, Year: lastDayYear, Month: 6, StatementBalanceCents: 40000})
	if err != nil {
		t.Fatalf("open june: %v", err)
	}
	if _, err := svcLastDay.CompleteReconciliation(ctx, recJune.ID, ""); err == nil || !strings.Contains(err.Error(), "2023-07-01") {
		t.Fatalf("Leg 3 (round 2): sign-off on June 30 (last day of month) must be refused naming 2023-07-01, got %v", err)
	}
}

// TestIntegration_FW1_04_BankReadsRefusedForCounterAndBranchStaff: FROM-PRINCIPAL 111
// proves that counter staff (inside_sales, tier 3 branch) and branch staff
// (branch_manager / sales_manager, tier 2 branch) are refused (403 Forbidden) on all
// five bankrec GET routes (accounts, lines, matches, rules, reconciliations), while
// Money Desk work or above (payables_receivables, general_manager, financial_controller)
// are admitted.
func TestIntegration_FW1_04_BankReadsRefusedForCounterAndBranchStaff(t *testing.T) {
	pool := intPool(t)
	cleanTables(t, pool)
	acct := rbcAccountID(t, pool)
	branch := aBranch(t, pool)
	lineID := fw104InsertLineAt(t, pool, acct, time.Date(2023, 5, 10, 0, 0, 0, 0, time.UTC), "PAYROLL WIRE", "PR-111", -500000)

	svc := NewService(NewRepository(pool))
	h := NewHandler(svc, slog.Default())
	sg := testkit.NewStaffGuards(t)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, sg.Guards)
	bearer := sg.Bearer(t)

	routes := []string{
		"/api/v1/bankrec/accounts",
		"/api/v1/bankrec/lines?bank_account_id=" + acct,
		"/api/v1/bankrec/matches?statement_line_id=" + lineID,
		"/api/v1/bankrec/rules",
		"/api/v1/bankrec/reconciliations?bank_account_id=" + acct,
	}

	probe := func(path string) int {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+bearer)
		req.Header.Set(authz.HeaderXOrg, testkit.OrgNorthSlug)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec.Code
	}

	refusedRoles := []struct {
		name string
		auth identityPkg.StaffAuthority
	}{
		{"counter inside_sales", identityPkg.StaffAuthority{RoleID: "inside_sales", Tier: identityPkg.TierFrontline, Scope: identityPkg.ScopeBranch, Branches: []string{branch}}},
		{"outside_sales", identityPkg.StaffAuthority{RoleID: "outside_sales", Tier: identityPkg.TierFrontline, Scope: identityPkg.ScopeBranch, Branches: []string{branch}}},
		{"branch_manager", identityPkg.StaffAuthority{RoleID: "branch_manager", Tier: identityPkg.TierManager, Scope: identityPkg.ScopeBranch, Branches: []string{branch}}},
		{"sales_manager", identityPkg.StaffAuthority{RoleID: "sales_manager", Tier: identityPkg.TierManager, Scope: identityPkg.ScopeBranch, Branches: []string{branch}}},
		{"hr", identityPkg.StaffAuthority{RoleID: "hr", Tier: identityPkg.TierLeadership, Scope: identityPkg.ScopeGlobal}},
	}
	for _, role := range refusedRoles {
		sg.SetAuthority(role.auth)
		for _, path := range routes {
			if code := probe(path); code != http.StatusForbidden {
				t.Errorf("%s GET %s status = %d, want 403 Forbidden", role.name, path, code)
			}
		}
	}

	admittedRoles := []struct {
		name string
		auth identityPkg.StaffAuthority
	}{
		{"payables_receivables (Money Desk work)", identityPkg.StaffAuthority{RoleID: "payables_receivables", Tier: identityPkg.TierManager, Scope: identityPkg.ScopeGlobal}},
		{"general_manager (Money Desk manage)", identityPkg.StaffAuthority{RoleID: "general_manager", Tier: identityPkg.TierLeadership, Scope: identityPkg.ScopeGlobal}},
		{"financial_controller (Money Desk admin)", identityPkg.StaffAuthority{RoleID: "financial_controller", Tier: identityPkg.TierLeadership, Scope: identityPkg.ScopeGlobal}},
	}
	for _, role := range admittedRoles {
		sg.SetAuthority(role.auth)
		for _, path := range routes {
			if code := probe(path); code != http.StatusOK {
				t.Errorf("%s GET %s status = %d, want 200 OK", role.name, path, code)
			}
		}
	}
}

// Item 2 closed-month rule: a match on a statement line in a closed GL month lands through FW1-01's rule
// (into the earliest open period, keeping the line's date on posted_at, marked late_posted), never refused.
func TestIntegration_FW1_04_MatchInClosedGLMonthLandsInEarliestOpenPeriodMarkedLatePosted(t *testing.T) {
	pool := intPool(t)
	cleanTables(t, pool)
	acct := rbcAccountID(t, pool)
	branch := aBranch(t, pool)
	ctx := context.Background()

	const year = 2023
	may18 := time.Date(year, 5, 18, 0, 0, 0, 0, time.UTC)
	june10 := time.Date(year, 6, 10, 14, 0, 0, 0, time.UTC)
	fw104CleanYear(t, pool, acct, year)
	fw104EnsurePeriods(t, pool, year, may18)
	t.Cleanup(func() { fw104CleanYear(t, pool, acct, year) })

	// Close 2023-01 through 2023-05 in fiscal_periods so 2023-06 is the earliest open period.
	if _, err := pool.Exec(ctx, `UPDATE fiscal_periods SET status = 'CLOSED', closed_at = $1 WHERE year = $2 AND month <= 5`, june10, year); err != nil {
		t.Fatalf("close periods 1..5: %v", err)
	}

	const payID = "87040009-0000-4000-8000-000000000009"
	insertPayment(t, pool, payID, branch, "CARD", "GTW-FW104-CLSM", 42000)
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM payments WHERE id = $1::uuid`, payID) })

	lineID := fw104InsertLineAt(t, pool, acct, may18, "MAY CARD IN CLOSED GL MONTH", "GTW-FW104-CLSM", 42000)
	glSvc := gl.NewService(gl.NewRepository(pool))
	paySeam := &fakePayments{cands: []CandidatePayment{{PaymentID: payID, BranchID: branch, GatewayRef: "GTW-FW104-CLSM", AmountCents: 42000, ReceivedAt: may18}}}
	svc := NewService(NewRepository(pool)).
		WithPayments(paySeam).
		WithJournals(glSvc).
		WithJournalPoster(fw104GLPoster{svc: glSvc, clock: func() time.Time { return june10 }}).
		WithGLBalances(glSvc)

	m, err := svc.ManualMatch(ctx, MatchInput{
		Mode: "manual", StatementLineID: lineID, TargetType: TargetPayment, TargetRefID: payID, AmountCents: 42000, CanAccessBranch: AllBranches,
	}, "")
	if err != nil {
		t.Fatalf("match on statement line in closed GL month was refused: %v", err)
	}
	j, err := glSvc.GetJournal(identity.ContextWithStaff(ctx, identity.StaffContext{UserID: anyUserID(t, pool), Scope: identity.ScopeGlobal}), m.JournalID)
	if err != nil {
		t.Fatalf("get clearing journal: %v", err)
	}
	var filedMonth int
	if err := pool.QueryRow(ctx, `SELECT month FROM fiscal_periods WHERE id = $1::uuid`, j.PeriodID).Scan(&filedMonth); err != nil {
		t.Fatalf("read period month: %v", err)
	}
	if filedMonth != 6 {
		t.Errorf("clearing journal for closed-month May line filed into month %d, want 6", filedMonth)
	}
	if j.PostedAt.UTC().Month() != time.May || j.PostedAt.UTC().Day() != 18 {
		t.Errorf("clearing journal posted_at = %v, want May 18", j.PostedAt.UTC())
	}
	if !j.LatePosted {
		t.Errorf("clearing journal LatePosted = false, want true when May GL period was closed")
	}
}

// Item 4b: Sign-off is refused when no statement lines are loaded in the month for a BANK account.
func TestIntegration_FW1_04_SignOffWithNoStatementLinesIsRefused(t *testing.T) {
	pool := intPool(t)
	cleanTables(t, pool)
	acct := rbcAccountID(t, pool)
	ctx := context.Background()

	const year = 2023
	may10 := time.Date(year, 5, 10, 0, 0, 0, 0, time.UTC)
	fw104CleanYear(t, pool, acct, year)
	fw104EnsurePeriods(t, pool, year, may10)
	t.Cleanup(func() { fw104CleanYear(t, pool, acct, year) })

	glSvc := gl.NewService(gl.NewRepository(pool))
	svc := NewService(NewRepository(pool)).
		WithJournals(glSvc).
		WithJournalPoster(fw104GLPoster{svc: glSvc, clock: func() time.Time { return may10 }}).
		WithGLBalances(glSvc)

	rec, err := svc.OpenReconciliation(ctx, ReconInput{BankAccountID: acct, Year: year, Month: 5, StatementBalanceCents: 0})
	if err != nil {
		t.Fatalf("open empty may: %v", err)
	}
	_, err = svc.CompleteReconciliation(ctx, rec.ID, "")
	if err == nil || !strings.Contains(err.Error(), "no statement lines") {
		t.Fatalf("expected refusal for no statement lines loaded, got %v", err)
	}
}

// Item 4c: Direct reversal of a bankrec clearing journal in the Ledger is refused, and if a clearing journal is
// reversed in the database, sign-off refuses to complete the month.
func TestIntegration_FW1_04_SignOffRefusedWhenClearingJournalReversedAndDirectReversalRefused(t *testing.T) {
	pool := intPool(t)
	cleanTables(t, pool)
	acct := rbcAccountID(t, pool)
	branch := aBranch(t, pool)
	ctx := context.Background()

	const year = 2023
	may15 := time.Date(year, 5, 15, 0, 0, 0, 0, time.UTC)
	fw104CleanYear(t, pool, acct, year)
	fw104EnsurePeriods(t, pool, year, may15)
	t.Cleanup(func() { fw104CleanYear(t, pool, acct, year) })

	const payID = "87040002-0000-4000-8000-000000000002"
	insertPayment(t, pool, payID, branch, "CARD", "GTW-FW104-REV", 75000)
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM payments WHERE id = $1::uuid`, payID) })

	lineID := fw104InsertLineAt(t, pool, acct, may15, "MAY SETTLEMENT REV", "GTW-FW104-REV", 75000)
	glSvc := gl.NewService(gl.NewRepository(pool))
	paySeam := &fakePayments{cands: []CandidatePayment{{PaymentID: payID, BranchID: branch, GatewayRef: "GTW-FW104-REV", AmountCents: 75000, ReceivedAt: may15}}}
	svc := NewService(NewRepository(pool)).
		WithPayments(paySeam).
		WithJournals(glSvc).
		WithJournalPoster(fw104GLPoster{svc: glSvc, clock: func() time.Time { return may15 }}).
		WithGLBalances(glSvc)

	m, err := svc.ManualMatch(ctx, MatchInput{
		Mode: "manual", StatementLineID: lineID, TargetType: TargetPayment, TargetRefID: payID, AmountCents: 75000, CanAccessBranch: AllBranches,
	}, "")
	if err != nil {
		t.Fatalf("manual match: %v", err)
	}
	// Direct reversal through gl.Service.ReverseJournal must be refused because the clearing journal belongs to bankrec.
	var userID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM users LIMIT 1`).Scan(&userID); err != nil {
		t.Fatalf("select user: %v", err)
	}
	// A caller that reaches the journal's books: the merged clearing journal's branch is the consolidated
	// one (no branch), so the sign in must be dealer wide to get past the reach fence and pin the owner
	// refusal ("posted by the bank reconciliation module") and not the reach refusal.
	staffCtx := identity.ContextWithStaff(ctx, identity.StaffContext{UserID: userID, Scope: identity.ScopeGlobal})
	if _, err := glSvc.ReverseJournal(staffCtx, m.JournalID); err == nil || !strings.Contains(err.Error(), "bank reconciliation") {
		t.Fatalf("expected direct reversal of bankrec clearing journal to be refused, got %v", err)
	}
	// If the clearing journal is reversed at the storage layer (another journal links to it via reverses_journal_id), sign-off must refuse.
	rec, err := svc.OpenReconciliation(ctx, ReconInput{BankAccountID: acct, Year: year, Month: 5, StatementBalanceCents: 75000})
	if err != nil {
		t.Fatalf("open may: %v", err)
	}
	revID, err := glSvc.PostExternal(ctx, gl.ExternalJournal{
		IdempotencyKey: "fw104-rev-sim",
		Source:         "ADJUSTMENT",
		Memo:           "simulated reversal",
		DocumentDate:   may15,
		Lines: []gl.ExternalLine{
			{AccountCode: "1010", DebitCents: 75000, Memo: "reverse clearing"},
			{AccountCode: "1000", CreditCents: 75000, Memo: "reverse bank"},
		},
	})
	if err != nil {
		t.Fatalf("post simulated reversal: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE gl_journals SET reverses_journal_id = $1::uuid WHERE id = $2::uuid`, m.JournalID, revID); err != nil {
		t.Fatalf("link simulated reversal: %v", err)
	}
	if _, err := svc.CompleteReconciliation(ctx, rec.ID, ""); err == nil || !strings.Contains(err.Error(), "clearing journal") {
		t.Fatalf("expected sign-off refusal when clearing journal is reversed, got %v", err)
	}
}

// Item 4d: Chronological sign-off order (cannot sign off a later month while an earlier month is OPEN, cannot sign off
// an earlier month after a later month is COMPLETED) and prior-month closing to current-month closing statement
// continuity.
func TestIntegration_FW1_04_ChronologicalSignOffOrderAndStatementContinuity(t *testing.T) {
	pool := intPool(t)
	cleanTables(t, pool)
	acct := rbcAccountID(t, pool)
	ctx := context.Background()

	const year = 2023
	apr10 := time.Date(year, 4, 10, 0, 0, 0, 0, time.UTC)
	may10 := time.Date(year, 5, 10, 0, 0, 0, 0, time.UTC)
	fw104CleanYear(t, pool, acct, year)
	fw104EnsurePeriods(t, pool, year, apr10)
	t.Cleanup(func() { fw104CleanYear(t, pool, acct, year) })

	glSvc := gl.NewService(gl.NewRepository(pool))
	aprJ, err := glSvc.PostExternal(ctx, gl.ExternalJournal{
		IdempotencyKey: "fw104-4d-apr",
		Source:         "ADJUSTMENT",
		Memo:           "April deposit",
		DocumentDate:   apr10.Add(12 * time.Hour),
		Lines: []gl.ExternalLine{
			{AccountCode: "1000", DebitCents: 100000, Memo: "bank"},
			{AccountCode: "4950", CreditCents: 100000, Memo: "offset"},
		},
	})
	if err != nil {
		t.Fatalf("post apr journal: %v", err)
	}
	mayJ, err := glSvc.PostExternal(ctx, gl.ExternalJournal{
		IdempotencyKey: "fw104-4d-may",
		Source:         "ADJUSTMENT",
		Memo:           "May deposit",
		DocumentDate:   may10.Add(12 * time.Hour),
		Lines: []gl.ExternalLine{
			{AccountCode: "1000", DebitCents: 40000, Memo: "bank"},
			{AccountCode: "4950", CreditCents: 40000, Memo: "offset"},
		},
	})
	if err != nil {
		t.Fatalf("post may journal: %v", err)
	}
	aprLine := fw104InsertLineAt(t, pool, acct, apr10, "APR DEPOSIT", "APR-4D", 100000)
	mayLine := fw104InsertLineAt(t, pool, acct, may10, "MAY DEPOSIT", "MAY-4D", 40000)

	svc := NewService(NewRepository(pool)).
		WithJournals(glSvc).
		WithJournalPoster(fw104GLPoster{svc: glSvc, clock: func() time.Time { return may10 }}).
		WithGLBalances(glSvc)

	if _, err := svc.ManualMatch(ctx, MatchInput{
		Mode: "manual", StatementLineID: aprLine, TargetType: TargetJournal, TargetRefID: aprJ, AmountCents: 100000, CanAccessBranch: AllBranches,
	}, ""); err != nil {
		t.Fatalf("match apr: %v", err)
	}
	if _, err := svc.ManualMatch(ctx, MatchInput{
		Mode: "manual", StatementLineID: mayLine, TargetType: TargetJournal, TargetRefID: mayJ, AmountCents: 40000, CanAccessBranch: AllBranches,
	}, ""); err != nil {
		t.Fatalf("match may: %v", err)
	}

	aprRec, err := svc.OpenReconciliation(ctx, ReconInput{BankAccountID: acct, Year: year, Month: 4, StatementBalanceCents: 100000})
	if err != nil {
		t.Fatalf("open apr: %v", err)
	}
	mayRec, err := svc.OpenReconciliation(ctx, ReconInput{BankAccountID: acct, Year: year, Month: 5, StatementBalanceCents: 140000})
	if err != nil {
		t.Fatalf("open may: %v", err)
	}
	// Attempting to complete May while April is still OPEN must be refused.
	if _, err := svc.CompleteReconciliation(ctx, mayRec.ID, ""); err == nil || !strings.Contains(err.Error(), "2023-04") {
		t.Fatalf("expected refusal when completing May while April is still open, got %v", err)
	}
	// Complete April first.
	if _, err := svc.CompleteReconciliation(ctx, aprRec.ID, ""); err != nil {
		t.Fatalf("complete apr: %v", err)
	}
	// If May is opened with a closing balance ($1,500.00) that does not continue from April's closing ($1,000.00) plus May's lines ($400.00 = $1,400.00), sign-off must be refused.
	badMay, err := svc.OpenReconciliation(ctx, ReconInput{BankAccountID: acct, Year: year, Month: 5, StatementBalanceCents: 150000})
	if err != nil {
		t.Fatalf("reopen may with broken continuity: %v", err)
	}
	if _, err := svc.CompleteReconciliation(ctx, badMay.ID, ""); err == nil || !strings.Contains(err.Error(), "prior month") {
		t.Fatalf("expected continuity refusal when May closing balance does not continue from April, got %v", err)
	}
	// Restore May's closing balance to $1,400.00 (140,000 cents): now May carries OpeningBalanceCents = 100000 and completes cleanly.
	mayRec, err = svc.OpenReconciliation(ctx, ReconInput{BankAccountID: acct, Year: year, Month: 5, StatementBalanceCents: 140000})
	if err != nil {
		t.Fatalf("reopen may with valid continuity: %v", err)
	}
	doneMay, err := svc.CompleteReconciliation(ctx, mayRec.ID, "")
	if err != nil {
		t.Fatalf("complete may after apr: %v", err)
	}
	if doneMay.OpeningBalanceCents == nil || *doneMay.OpeningBalanceCents != 100000 {
		t.Errorf("completed May OpeningBalanceCents = %v, want 100000", doneMay.OpeningBalanceCents)
	}
	// Attempting to open and complete an earlier month (March 2023) after April and May are COMPLETED must be refused.
	mar10 := time.Date(year, 3, 10, 0, 0, 0, 0, time.UTC)
	marLine := fw104InsertLineAt(t, pool, acct, mar10, "MAR DEPOSIT", "MAR-4D", 10000)
	marJ, err := glSvc.PostExternal(ctx, gl.ExternalJournal{
		IdempotencyKey: "fw104-4d-mar",
		Source:         "ADJUSTMENT",
		Memo:           "March deposit",
		DocumentDate:   mar10.Add(12 * time.Hour),
		Lines: []gl.ExternalLine{
			{AccountCode: "1000", DebitCents: 10000, Memo: "bank"},
			{AccountCode: "4950", CreditCents: 10000, Memo: "offset"},
		},
	})
	if err != nil {
		t.Fatalf("post mar journal: %v", err)
	}
	if _, err := svc.ManualMatch(ctx, MatchInput{
		Mode: "manual", StatementLineID: marLine, TargetType: TargetJournal, TargetRefID: marJ, AmountCents: 10000, CanAccessBranch: AllBranches,
	}, ""); err != nil {
		t.Fatalf("match mar: %v", err)
	}
	marRec, err := svc.OpenReconciliation(ctx, ReconInput{BankAccountID: acct, Year: year, Month: 3, StatementBalanceCents: 10000})
	if err != nil {
		t.Fatalf("open mar: %v", err)
	}
	if _, err := svc.CompleteReconciliation(ctx, marRec.ID, ""); err == nil || !strings.Contains(err.Error(), "already completed") {
		t.Fatalf("expected refusal when completing March after April/May are completed, got %v", err)
	}
}

// Round 2 (item 2.1, ruled, both halves): a reconciliation starting point per bank account (cut-over).
//  1. S5's probe: GL 1000 opening balance $5,000.00 dated the last day of March 2022 on no statement, an April deposit
//     of $100.00 on the April statement and matched, and April's real closing balance typed ($5,100.00) signs off at
//     zero with the opening balance absent from the outstanding lists.
//  2. Confirmed outstanding list at cut-over: an entry on the confirmed list stays outstanding, and one on or before
//     cut-over not on the list never appears.
//  3. Audited (BANKREC_CUTOVER), Financial Controller only, and refused once a later month has signed off.
func TestIntegration_FW1_04_CutoverStartingPointAndConfirmedOutstanding(t *testing.T) {
	pool := intPool(t)
	cleanTables(t, pool)
	acct := rbcAccountID(t, pool)
	ctx := context.Background()

	const year = 2022
	mar30 := time.Date(year, 3, 30, 12, 0, 0, 0, time.UTC)
	mar31 := time.Date(year, 3, 31, 12, 0, 0, 0, time.UTC)
	apr10 := time.Date(year, 4, 10, 0, 0, 0, 0, time.UTC)

	resetCutover := func() {
		_, _ = pool.Exec(context.Background(), `
			UPDATE bank_accounts
			   SET cutover_date = NULL,
			       cutover_statement_balance_cents = NULL,
			       cutover_outstanding_journal_ids = '{}'::uuid[],
			       cutover_recorded_by = NULL,
			       cutover_recorded_at = NULL
			 WHERE id = $1::uuid`, acct)
	}
	fw104CleanYear(t, pool, acct, year)
	fw104EnsurePeriods(t, pool, year, mar30)
	resetCutover()
	t.Cleanup(func() {
		resetCutover()
		fw104CleanYear(t, pool, acct, year)
	})

	var userID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM users LIMIT 1`).Scan(&userID); err != nil {
		t.Fatalf("select user: %v", err)
	}
	ctrlCtx := identity.ContextWithStaff(ctx, identity.StaffContext{
		UserID:   userID,
		RoleCode: "FINANCIAL_CONTROLLER",
		Tier:     identity.TierLeadership,
		Scope:    identity.ScopeGlobal,
	})
	gmCtx := identity.ContextWithStaff(ctx, identity.StaffContext{
		UserID:   userID,
		RoleCode: "GENERAL_MANAGER",
		Tier:     identity.TierLeadership,
		Scope:    identity.ScopeGlobal,
	})
	payRecCtx := identity.ContextWithStaff(ctx, identity.StaffContext{
		UserID:   userID,
		RoleCode: "PAYABLES_RECEIVABLES",
		Tier:     identity.TierManager,
		Scope:    identity.ScopeGlobal,
	})

	auditSvc := audit.NewService(audit.NewRepository(pool), logging.New("test"))
	glSvc := gl.NewService(gl.NewRepository(pool))
	svc := NewService(NewRepository(pool)).
		WithJournals(glSvc).
		WithJournalPoster(fw104GLPoster{svc: glSvc, clock: func() time.Time { return apr10 }}).
		WithGLBalances(glSvc).
		WithAudit(auditSvc)

	// Guard: non-Controller roles (General Manager, Payables and Receivables) are refused.
	for _, tc := range []struct {
		name string
		c    context.Context
	}{
		{"general_manager", gmCtx},
		{"payables_receivables", payRecCtx},
	} {
		if _, err := svc.RecordCutover(tc.c, CutoverInput{
			BankAccountID:                acct,
			CutoverDate:                  "2022-03-31",
			OpeningStatementBalanceCents: 500000,
		}); err == nil || !strings.Contains(err.Error(), "Controller") {
			t.Fatalf("expected non-Controller (%s) cutover to be refused, got %v", tc.name, err)
		}
	}

	// Half 1 (S5's probe): GL 1000 opening balance $5,000.00 dated 2022-03-31 on no statement, April deposit $100.00
	// matched to the April statement, real closing balance $5,100.00 typed.
	openJID, err := glSvc.PostExternal(ctx, gl.ExternalJournal{
		IdempotencyKey: "fw104-r2-cutover-open",
		Source:         "ADJUSTMENT",
		Memo:           "opening balance at go live",
		DocumentDate:   mar31,
		Lines: []gl.ExternalLine{
			{AccountCode: "1000", DebitCents: 500000, Memo: "opening balance"},
			{AccountCode: "3900", CreditCents: 500000, Memo: "retained earnings"},
		},
	})
	if err != nil {
		t.Fatalf("post opening balance: %v", err)
	}
	aprDepJID, err := glSvc.PostExternal(ctx, gl.ExternalJournal{
		IdempotencyKey: "fw104-r2-cutover-apr-dep",
		Source:         "ADJUSTMENT",
		Memo:           "April customer deposit",
		DocumentDate:   apr10.Add(12 * time.Hour),
		Lines: []gl.ExternalLine{
			{AccountCode: "1000", DebitCents: 10000, Memo: "bank"},
			{AccountCode: "4950", CreditCents: 10000, Memo: "revenue"},
		},
	})
	if err != nil {
		t.Fatalf("post april deposit: %v", err)
	}
	aprLine := fw104InsertLineAt(t, pool, acct, apr10, "APRIL CUSTOMER DEPOSIT", "APR-DEP-1", 10000)
	if _, err := svc.ManualMatch(ctx, MatchInput{
		Mode: "manual", StatementLineID: aprLine, TargetType: TargetJournal, TargetRefID: aprDepJID, AmountCents: 10000, CanAccessBranch: AllBranches,
	}, userID); err != nil {
		t.Fatalf("match april deposit: %v", err)
	}

	var preAudit int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action = 'BANKREC_CUTOVER' AND entity_id = $1`, acct).Scan(&preAudit); err != nil {
		t.Fatalf("count pre audit: %v", err)
	}

	if _, err := svc.RecordCutover(ctrlCtx, CutoverInput{
		BankAccountID:                acct,
		CutoverDate:                  "2022-03-31",
		OpeningStatementBalanceCents: 500000,
	}); err != nil {
		t.Fatalf("record cut-over: %v", err)
	}

	var postAudit int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action = 'BANKREC_CUTOVER' AND entity_id = $1`, acct).Scan(&postAudit); err != nil {
		t.Fatalf("count post audit: %v", err)
	}
	if postAudit != preAudit+1 {
		t.Errorf("BANKREC_CUTOVER audit rows = %d, want %d", postAudit, preAudit+1)
	}

	recApr, err := svc.OpenReconciliation(ctx, ReconInput{
		BankAccountID:         acct,
		Year:                  year,
		Month:                 4,
		StatementBalanceCents: 510000,
	})
	if err != nil {
		t.Fatalf("open april with cutover: %v", err)
	}
	if recApr.OpeningBalanceCents == nil || *recApr.OpeningBalanceCents != 500000 {
		t.Errorf("April OpeningBalanceCents = %v, want 500000", recApr.OpeningBalanceCents)
	}
	if recApr.DepositsInTransitCents != 0 || len(recApr.DepositsInTransit) != 0 {
		t.Errorf("April DepositsInTransit = %d (%+v), want 0 and empty (opening balance %s must not be outstanding)",
			recApr.DepositsInTransitCents, recApr.DepositsInTransit, openJID)
	}
	if recApr.AdjustedBankBalanceCents != 510000 || recApr.GLBalanceCents != 510000 || recApr.VarianceCents != 0 {
		t.Errorf("April figures: Adjusted=%d GL=%d Variance=%d, want 510000 510000 0",
			recApr.AdjustedBankBalanceCents, recApr.GLBalanceCents, recApr.VarianceCents)
	}

	doneApr, err := svc.CompleteReconciliation(ctx, recApr.ID, userID)
	if err != nil {
		t.Fatalf("sign off april with cutover: %v", err)
	}
	if doneApr.Status != ReconCompleted || doneApr.VarianceCents != 0 || len(doneApr.DepositsInTransit) != 0 {
		t.Errorf("completed April = %+v, want COMPLETED with variance 0 and no deposits in transit", doneApr)
	}

	// Guard: once a later month (2022-04) has signed off, recording or changing cut-over at 2022-03-31 is refused.
	// The balance reconciles (the book at the cut over date is $5,000.00), so the conflict is what answers.
	if _, err := svc.RecordCutover(ctrlCtx, CutoverInput{
		BankAccountID:                acct,
		CutoverDate:                  "2022-03-31",
		OpeningStatementBalanceCents: 500000,
	}); err == nil || !strings.Contains(err.Error(), "already completed") {
		t.Fatalf("expected cutover refusal once a later month has signed off, got %v", err)
	}

	// Half 2: confirmed list of outstanding items at cut-over.
	// Reset April's reconciliation row so we can record a cut-over with one confirmed outstanding cheque (-$200.00)
	// dated 2022-03-30 alongside the $5,000.00 opening balance (not on the confirmed list).
	if _, err := pool.Exec(ctx, `DELETE FROM bank_reconciliations WHERE bank_account_id = $1::uuid AND year = $2`, acct, year); err != nil {
		t.Fatalf("delete april recon: %v", err)
	}
	chequeJID, err := glSvc.PostExternal(ctx, gl.ExternalJournal{
		IdempotencyKey: "fw104-r2-cutover-cheque",
		Source:         "ADJUSTMENT",
		Memo:           "March 30 cheque 998 still in flight at cut-over",
		DocumentDate:   mar30,
		Lines: []gl.ExternalLine{
			{AccountCode: "6300", DebitCents: 20000, Memo: "vendor"},
			{AccountCode: "1000", CreditCents: 20000, Memo: "cheque 998"},
		},
	})
	if err != nil {
		t.Fatalf("post cutover cheque: %v", err)
	}
	if _, err := svc.RecordCutover(ctrlCtx, CutoverInput{
		BankAccountID:                acct,
		CutoverDate:                  "2022-03-31",
		OpeningStatementBalanceCents: 500000,
		OutstandingJournalIDs:        []string{chequeJID},
	}); err != nil {
		t.Fatalf("record cutover with confirmed outstanding cheque: %v", err)
	}

	// In April, GL balance is 500,000 - 200,000_0 + 10,000 = 490,000 cents ($4,900.00); statement closing is 510,000 ($5,100.00).
	// chequeJID ($200.00) stays in OutstandingCheques while openJID ($5,000.00) never appears in DepositsInTransit.
	recApr2, err := svc.OpenReconciliation(ctx, ReconInput{
		BankAccountID:         acct,
		Year:                  year,
		Month:                 4,
		StatementBalanceCents: 510000,
	})
	if err != nil {
		t.Fatalf("open april with confirmed outstanding list: %v", err)
	}
	if len(recApr2.DepositsInTransit) != 0 || recApr2.DepositsInTransitCents != 0 {
		t.Errorf("unlisted cutover entry appeared in DepositsInTransit: %+v", recApr2.DepositsInTransit)
	}
	if len(recApr2.OutstandingCheques) != 1 || recApr2.OutstandingCheques[0].JournalID != chequeJID || recApr2.OutstandingChequesCents != 20000 {
		t.Errorf("confirmed cutover cheque missing from OutstandingCheques: got %+v (sum %d), want [%s] (20000)",
			recApr2.OutstandingCheques, recApr2.OutstandingChequesCents, chequeJID)
	}
	if recApr2.AdjustedBankBalanceCents != 490000 || recApr2.GLBalanceCents != 490000 || recApr2.VarianceCents != 0 {
		t.Errorf("April with confirmed cutover cheque: Adjusted=%d GL=%d Variance=%d, want 490000 490000 0",
			recApr2.AdjustedBankBalanceCents, recApr2.GLBalanceCents, recApr2.VarianceCents)
	}
	if _, err := svc.CompleteReconciliation(ctx, recApr2.ID, userID); err != nil {
		t.Fatalf("complete april with confirmed cutover cheque: %v", err)
	}
}

// Round 3 (FROM-PRINCIPAL 169): the Controller's door to the cut over. The route legs walk the real mux:
// the Controller's POST answers 200 with the recorded point on the account read and one BANKREC_CUTOVER audit
// row; a General Manager is refused 403 (the service keeps its Controller check behind the admin gate); and
// once a later month has signed off, recording is refused 409.
func TestIntegration_FW1_04_CutoverRouteLegs(t *testing.T) {
	pool := intPool(t)
	cleanTables(t, pool)
	acct := rbcAccountID(t, pool)
	ctx := context.Background()

	const year = 2022
	apr10 := time.Date(year, 4, 10, 0, 0, 0, 0, time.UTC)

	resetCutover := func() {
		_, _ = pool.Exec(context.Background(), `
			UPDATE bank_accounts
			   SET cutover_date = NULL,
			       cutover_statement_balance_cents = NULL,
			       cutover_outstanding_journal_ids = '{}'::uuid[],
			       cutover_recorded_by = NULL,
			       cutover_recorded_at = NULL
			 WHERE id = $1::uuid`, acct)
	}
	fw104CleanYear(t, pool, acct, year)
	fw104EnsurePeriods(t, pool, year, apr10)
	resetCutover()
	t.Cleanup(func() {
		resetCutover()
		fw104CleanYear(t, pool, acct, year)
	})

	var userID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM users LIMIT 1`).Scan(&userID); err != nil {
		t.Fatalf("select user: %v", err)
	}

	auditSvc := audit.NewService(audit.NewRepository(pool), logging.New("test"))
	glSvc := gl.NewService(gl.NewRepository(pool))
	svc := NewService(NewRepository(pool)).
		WithJournals(glSvc).
		WithJournalPoster(fw104GLPoster{svc: glSvc, clock: func() time.Time { return apr10 }}).
		WithGLBalances(glSvc).
		WithAudit(auditSvc)
	h := NewHandler(svc, slog.Default()).WithUserLinker(func(context.Context, string) (string, bool) { return userID, true })
	sg := testkit.NewStaffGuards(t)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, sg.Guards)
	bearer := sg.Bearer(t)

	postWithBody := func(auth identityPkg.StaffAuthority, body string) (int, string) {
		t.Helper()
		sg.SetAuthority(auth)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/bankrec/accounts/"+acct+"/cutover", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+bearer)
		req.Header.Set(authz.HeaderXOrg, testkit.OrgNorthSlug)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}
	post := func(auth identityPkg.StaffAuthority, body string) int {
		t.Helper()
		code, _ := postWithBody(auth, body)
		return code
	}
	gm := identityPkg.StaffAuthority{RoleID: "general_manager", Tier: identityPkg.TierLeadership, Scope: identityPkg.ScopeGlobal}
	controller := identityPkg.StaffAuthority{RoleID: "financial_controller", Tier: identityPkg.TierLeadership, Scope: identityPkg.ScopeGlobal}
	payRec := identityPkg.StaffAuthority{RoleID: "payables_receivables", Tier: identityPkg.TierManager, Scope: identityPkg.ScopeGlobal}

	// The ledger carries the go live opening from before the cut over date, so the starting point reconciles
	// (round 4): the book at 2022-03-31 is $5,000.00 and the recorded statement balance is the same.
	if _, err := glSvc.PostExternal(ctx, gl.ExternalJournal{
		IdempotencyKey: "fw104-r3-route-open",
		Source:         "ADJUSTMENT",
		Memo:           "opening balance at go live",
		DocumentDate:   time.Date(year, 3, 31, 12, 0, 0, 0, time.UTC),
		Lines: []gl.ExternalLine{
			{AccountCode: "1000", DebitCents: 500000, Memo: "opening balance"},
			{AccountCode: "3900", CreditCents: 500000, Memo: "retained earnings"},
		},
	}); err != nil {
		t.Fatalf("post opening balance: %v", err)
	}

	body := `{"cutover_date":"2022-03-31","opening_statement_balance_cents":500000}`
	// The General Manager and the Money Desk work role are refused at the door.
	if code := post(gm, body); code != http.StatusForbidden {
		t.Errorf("general manager POST cutover status = %d, want 403 Forbidden", code)
	}
	if code := post(payRec, body); code != http.StatusForbidden {
		t.Errorf("payables_receivables POST cutover status = %d, want 403 Forbidden", code)
	}

	// The Controller's POST answers 200 and the account read carries the recorded point.
	var preAudit int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action = 'BANKREC_CUTOVER' AND entity_id = $1`, acct).Scan(&preAudit); err != nil {
		t.Fatalf("count pre audit: %v", err)
	}
	sg.SetAuthority(controller)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/bankrec/accounts/"+acct+"/cutover", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set(authz.HeaderXOrg, testkit.OrgNorthSlug)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("financial controller POST cutover status = %d, want 200 OK (body %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"cutover_date":"2022-03-31"`) || !strings.Contains(rec.Body.String(), `"cutover_statement_balance_cents":500000`) {
		t.Errorf("cutover answer does not carry the recorded point: %s", rec.Body.String())
	}
	var postAudit int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action = 'BANKREC_CUTOVER' AND entity_id = $1`, acct).Scan(&postAudit); err != nil {
		t.Fatalf("count post audit: %v", err)
	}
	if postAudit != preAudit+1 {
		t.Errorf("BANKREC_CUTOVER audit rows = %d, want %d", postAudit, preAudit+1)
	}

	// Once a later month (2022-04) has signed off, recording through the door is refused 409. April's own
	// figures tie against the opening already in the ledger, so it can sign off.
	depJID, err := glSvc.PostExternal(ctx, gl.ExternalJournal{
		IdempotencyKey: "fw104-r3-route-apr-dep",
		Source:         "ADJUSTMENT",
		Memo:           "April customer deposit",
		DocumentDate:   apr10.Add(12 * time.Hour),
		Lines: []gl.ExternalLine{
			{AccountCode: "1000", DebitCents: 10000, Memo: "bank"},
			{AccountCode: "4950", CreditCents: 10000, Memo: "revenue"},
		},
	})
	if err != nil {
		t.Fatalf("post april deposit: %v", err)
	}
	aprLine := fw104InsertLineAt(t, pool, acct, apr10, "APRIL CUSTOMER DEPOSIT", "APR-ROUTE-1", 10000)
	if _, err := svc.ManualMatch(ctx, MatchInput{
		Mode: "manual", StatementLineID: aprLine, TargetType: TargetJournal, TargetRefID: depJID, AmountCents: 10000, CanAccessBranch: AllBranches,
	}, userID); err != nil {
		t.Fatalf("match april deposit: %v", err)
	}
	recApr, err := svc.OpenReconciliation(ctx, ReconInput{BankAccountID: acct, Year: year, Month: 4, StatementBalanceCents: 510000})
	if err != nil {
		t.Fatalf("open april: %v", err)
	}
	if _, err := svc.CompleteReconciliation(ctx, recApr.ID, userID); err != nil {
		t.Fatalf("sign off april: %v", err)
	}
	// Once any month (2022-04) has signed off on the account, any cut-over, whether dated before, in, or
	// after the signed-off month, is refused 409 naming 2022-04 (FROM-PRINCIPAL 183, S5 R3.3).
	if code, rbody := postWithBody(controller, `{"cutover_date":"2022-03-31","opening_statement_balance_cents":500000}`); code != http.StatusConflict || !strings.Contains(rbody, "month 2022-04 is already completed") {
		t.Errorf("controller POST cutover at 2022-03-31 after 2022-04 signed off: status = %d body %s, want 409 Conflict naming 2022-04", code, rbody)
	}
	if code, rbody := postWithBody(controller, `{"cutover_date":"2022-06-30","opening_statement_balance_cents":510000}`); code != http.StatusConflict || !strings.Contains(rbody, "month 2022-04 is already completed") {
		t.Errorf("controller POST cutover at later date 2022-06-30 (reconciling) after 2022-04 signed off: status = %d body %s, want 409 Conflict naming 2022-04", code, rbody)
	}
	if code, rbody := postWithBody(controller, `{"cutover_date":"2022-06-30","opening_statement_balance_cents":1}`); code != http.StatusConflict || !strings.Contains(rbody, "month 2022-04 is already completed") {
		t.Errorf("controller POST cutover at later date 2022-06-30 ($0.01) after 2022-04 signed off: status = %d body %s, want 409 Conflict naming 2022-04", code, rbody)
	}
}

// Round 2 (item 2.3): importing a native RBC export row with a USD$ amount or an empty amount column is refused in
// words and records a FAILED import audit with zero lines inserted.
func TestIntegration_FW1_04_RBCImportRefusesUSDAndEmptyAmount(t *testing.T) {
	pool := intPool(t)
	cleanTables(t, pool)
	acct := rbcAccountID(t, pool)
	ctx := context.Background()

	svc := NewService(NewRepository(pool))

	usdContent := "Account Type,Account Number,Transaction Date,Cheque Number,Description 1,Description 2,CAD$,USD$\n" +
		"Chequing,00123-1234567,6/10/2023,,USD WIRE,US VENDOR,,250.00\n"
	resUSD, err := svc.Import(ctx, ImportInput{
		BankAccountID: acct,
		ProfileCode:   "rbc-default",
		Filename:      "rbc_usd.csv",
		Content:       usdContent,
	}, "")
	if err == nil || !strings.Contains(err.Error(), "USD") {
		t.Fatalf("expected USD row import refusal mentioning USD, got res=%+v err=%v", resUSD, err)
	}
	if resUSD.Status != ImportStatusFailed || resUSD.Inserted != 0 {
		t.Errorf("USD import result = %+v, want FAILED with 0 inserted", resUSD)
	}

	emptyContent := "Account Type,Account Number,Transaction Date,Cheque Number,Description 1,Description 2,CAD$,USD$\n" +
		"Chequing,00123-1234567,6/10/2023,,BLANK ROW,MEMO,,\n"
	resEmpty, err := svc.Import(ctx, ImportInput{
		BankAccountID: acct,
		ProfileCode:   "rbc-default",
		Filename:      "rbc_empty.csv",
		Content:       emptyContent,
	}, "")
	if err == nil || !strings.Contains(err.Error(), "missing amount") {
		t.Fatalf("expected empty amount row import refusal mentioning missing amount, got res=%+v err=%v", resEmpty, err)
	}
	if resEmpty.Status != ImportStatusFailed || resEmpty.Inserted != 0 {
		t.Errorf("empty import result = %+v, want FAILED with 0 inserted", resEmpty)
	}
}

// Round 3: the starting point form's read. The ledger's entries on the account's GL code on or before the cut
// over date, with the book balance their net makes, answered by the core so the desk formats and never computes.
func TestIntegration_FW1_04_CutoverPreviewRouteLegs(t *testing.T) {
	pool := intPool(t)
	cleanTables(t, pool)
	acct := rbcAccountID(t, pool)
	ctx := context.Background()

	const year = 2022
	mar31 := time.Date(year, 3, 31, 12, 0, 0, 0, time.UTC)

	fw104CleanYear(t, pool, acct, year)
	fw104EnsurePeriods(t, pool, year, mar31)
	t.Cleanup(func() { fw104CleanYear(t, pool, acct, year) })

	var userID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM users LIMIT 1`).Scan(&userID); err != nil {
		t.Fatalf("select user: %v", err)
	}
	glSvc := gl.NewService(gl.NewRepository(pool))
	svc := NewService(NewRepository(pool)).
		WithJournals(glSvc).
		WithGLBalances(glSvc)
	h := NewHandler(svc, slog.Default()).WithUserLinker(func(context.Context, string) (string, bool) { return userID, true })
	sg := testkit.NewStaffGuards(t)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, sg.Guards)
	bearer := sg.Bearer(t)

	openJID, err := glSvc.PostExternal(ctx, gl.ExternalJournal{
		IdempotencyKey: "fw104-r3-preview-open",
		Source:         "ADJUSTMENT",
		Memo:           "opening balance at go live",
		DocumentDate:   mar31,
		Lines: []gl.ExternalLine{
			{AccountCode: "1000", DebitCents: 500000, Memo: "opening balance"},
			{AccountCode: "3900", CreditCents: 500000, Memo: "retained earnings"},
		},
	})
	if err != nil {
		t.Fatalf("post opening balance: %v", err)
	}

	get := func(auth identityPkg.StaffAuthority, query string) (int, string) {
		t.Helper()
		sg.SetAuthority(auth)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/bankrec/accounts/"+acct+"/cutover"+query, nil)
		req.Header.Set("Authorization", "Bearer "+bearer)
		req.Header.Set(authz.HeaderXOrg, testkit.OrgNorthSlug)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}
	work := identityPkg.StaffAuthority{RoleID: "payables_receivables", Tier: identityPkg.TierManager, Scope: identityPkg.ScopeGlobal}
	controller := identityPkg.StaffAuthority{RoleID: "financial_controller", Tier: identityPkg.TierLeadership, Scope: identityPkg.ScopeGlobal}

	if code, body := get(work, "?date=not-a-date"); code != http.StatusBadRequest || !strings.Contains(body, "cutover_date must be YYYY-MM-DD") {
		t.Errorf("preview with a bad date: status = %d body %s, want 400 naming the date", code, body)
	}
	if code, body := get(work, ""); code != http.StatusBadRequest {
		t.Errorf("preview with no date: status = %d body %s, want 400", code, body)
	}
	code, body := get(work, "?date=2022-03-31")
	if code != http.StatusOK {
		t.Fatalf("preview status = %d body %s, want 200 OK", code, body)
	}
	if !strings.Contains(body, `"book_balance_cents":500000`) {
		t.Errorf("preview book balance missing: %s", body)
	}
	if !strings.Contains(body, openJID) || !strings.Contains(body, "opening balance at go live") {
		t.Errorf("preview does not list the ledger's opening entry: %s", body)
	}
	// A date before the entry lists nothing, at zero.
	code, body = get(controller, "?date=2022-03-30")
	if code != http.StatusOK {
		t.Fatalf("preview before any entry: status = %d body %s, want 200 OK", code, body)
	}
	if !strings.Contains(body, `"book_balance_cents":0`) || strings.Contains(body, openJID) {
		t.Errorf("preview at 2022-03-30 must be empty at zero: %s", body)
	}
}

// Round 4 (FROM-PRINCIPAL 181, P1 money): a starting point must reconcile. The statement balance equals the
// book balance less ticked outstanding cheques plus ticked deposits in transit; the server refuses the same
// through RecordCutover, in the desk's words, with the difference named.
func TestIntegration_FW1_04_CutoverMustReconcile(t *testing.T) {
	pool := intPool(t)
	cleanTables(t, pool)
	acct := rbcAccountID(t, pool)
	ctx := context.Background()

	const year = 2022
	mar30 := time.Date(year, 3, 30, 12, 0, 0, 0, time.UTC)
	mar31 := time.Date(year, 3, 31, 12, 0, 0, 0, time.UTC)

	resetCutover := func() {
		_, _ = pool.Exec(context.Background(), `
			UPDATE bank_accounts
			   SET cutover_date = NULL, cutover_statement_balance_cents = NULL,
			       cutover_outstanding_journal_ids = '{}'::uuid[], cutover_recorded_by = NULL, cutover_recorded_at = NULL
			 WHERE id = $1::uuid`, acct)
	}
	fw104CleanYear(t, pool, acct, year)
	fw104EnsurePeriods(t, pool, year, mar31)
	resetCutover()
	t.Cleanup(func() {
		resetCutover()
		fw104CleanYear(t, pool, acct, year)
	})

	var userID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM users LIMIT 1`).Scan(&userID); err != nil {
		t.Fatalf("select user: %v", err)
	}
	glSvc := gl.NewService(gl.NewRepository(pool))
	svc := NewService(NewRepository(pool)).WithJournals(glSvc).WithGLBalances(glSvc)
	h := NewHandler(svc, slog.Default()).WithUserLinker(func(context.Context, string) (string, bool) { return userID, true })
	sg := testkit.NewStaffGuards(t)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, sg.Guards)
	bearer := sg.Bearer(t)
	sg.SetAuthority(identityPkg.StaffAuthority{RoleID: "financial_controller", Tier: identityPkg.TierLeadership, Scope: identityPkg.ScopeGlobal})

	// The ledger at cut over: the go live opening on 2022-03-31 and a cheque written 2022-03-30, still in flight.
	openJID, err := glSvc.PostExternal(ctx, gl.ExternalJournal{
		IdempotencyKey: "fw104-r4-open",
		Source:         "ADJUSTMENT",
		Memo:           "opening balance at go live",
		DocumentDate:   mar31,
		Lines: []gl.ExternalLine{
			{AccountCode: "1000", DebitCents: 500000, Memo: "opening balance"},
			{AccountCode: "3900", CreditCents: 500000, Memo: "retained earnings"},
		},
	})
	if err != nil {
		t.Fatalf("post opening balance: %v", err)
	}
	chequeJID, err := glSvc.PostExternal(ctx, gl.ExternalJournal{
		IdempotencyKey: "fw104-r4-cheque",
		Source:         "ADJUSTMENT",
		Memo:           "cheque 998 still in flight at cut over",
		DocumentDate:   mar30,
		Lines: []gl.ExternalLine{
			{AccountCode: "6300", DebitCents: 20000, Memo: "vendor"},
			{AccountCode: "1000", CreditCents: 20000, Memo: "cheque 998"},
		},
	})
	if err != nil {
		t.Fatalf("post cheque: %v", err)
	}

	post := func(body string) (int, string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/bankrec/accounts/"+acct+"/cutover", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+bearer)
		req.Header.Set(authz.HeaderXOrg, testkit.OrgNorthSlug)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}

	// The book at cut over is $4,800.00 (the $5,000.00 opening less the $200.00 cheque), so the statement the
	// book expects with nothing ticked is $4,800.00: a statement of $5,000.00 with nothing ticked is refused
	// in words naming $200.00.
	code, body := post(`{"cutover_date":"2022-03-31","opening_statement_balance_cents":500000}`)
	if code != http.StatusUnprocessableEntity || !strings.Contains(body, "the statement and the book differ by $200.00") || !strings.Contains(body, "tick the items still outstanding at cut over, or check the date and balance") {
		t.Errorf("unreconciled cut-over: status = %d body %s, want 422 naming the difference and the tick", code, body)
	}
	code, body = post(`{"cutover_date":"2022-03-31","opening_statement_balance_cents":490000,"outstanding_journal_ids":["` + chequeJID + `"]}`)
	if code != http.StatusUnprocessableEntity || !strings.Contains(body, "the statement and the book differ by $100.00") {
		t.Errorf("ticked but short cut-over: status = %d body %s, want 422 differing by $100.00", code, body)
	}
	// The reconciling record lands: statement $5,000.00 with the cheque ticked (book $4,800.00 plus the $200.00
	// still outstanding), and again with nothing ticked at a balance the book makes exactly.
	code, body = post(`{"cutover_date":"2022-03-31","opening_statement_balance_cents":500000,"outstanding_journal_ids":["` + chequeJID + `"]}`)
	if code != http.StatusOK {
		t.Errorf("reconciling cut-over with the cheque ticked: status = %d body %s, want 200 OK", code, body)
	}
	resetCutover()
	code, body = post(`{"cutover_date":"2022-03-31","opening_statement_balance_cents":480000}`)
	if code != http.StatusOK {
		t.Errorf("reconciling cut-over at the bare book: status = %d body %s, want 200 OK", code, body)
	}

	// FROM-PRINCIPAL 183 (S5 R3.2): the book figure is the LEDGER's own balance at the cut-over date, never
	// the sum of the preview's uncleared entry list. An older bankrec clearing journal (+$30.00 on 2022-03-15)
	// sits in GL 1000 (making the ledger balance $4,830.00 at 2022-03-31) while BankAccountEntriesAsOf leaves
	// it out of the tick list (whose sum remains $4,800.00).
	resetCutover()
	if _, err := glSvc.PostExternal(ctx, gl.ExternalJournal{
		IdempotencyKey: "bankrec:match:fw104-r4-prior-cleared",
		Source:         "ADJUSTMENT",
		Memo:           "bankrec clearing journal already cleared on an earlier statement",
		DocumentDate:   time.Date(year, 3, 15, 12, 0, 0, 0, time.UTC),
		Lines: []gl.ExternalLine{
			{AccountCode: "1000", DebitCents: 3000, Memo: "bank"},
			{AccountCode: "1020", CreditCents: 3000, Memo: "undeposited funds"},
		},
	}); err != nil {
		t.Fatalf("post prior bankrec clearing journal: %v", err)
	}
	getPreview := func(query string) (int, string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/bankrec/accounts/"+acct+"/cutover"+query, nil)
		req.Header.Set("Authorization", "Bearer "+bearer)
		req.Header.Set(authz.HeaderXOrg, testkit.OrgNorthSlug)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}
	if pCode, pBody := getPreview("?date=2022-03-31&statement_cents=503000&ticked=" + chequeJID); pCode != http.StatusOK || !strings.Contains(pBody, `"book_balance_cents":483000`) || !strings.Contains(pBody, `"reconciles":true`) {
		t.Errorf("preview with bankrec clearing journal in ledger: status = %d body %s, want book_balance_cents 483000 and reconciles true", pCode, pBody)
	}
	// A statement of $5,000.00 with the -$200.00 cheque ticked matches only the entry-list sum ($4,800.00 - (-$200.00)),
	// NOT the ledger's own $4,830.00 balance ($4,830.00 - (-$200.00) = $5,030.00), so RecordCutover refuses it by $30.00.
	code, body = post(`{"cutover_date":"2022-03-31","opening_statement_balance_cents":500000,"outstanding_journal_ids":["` + chequeJID + `"]}`)
	if code != http.StatusUnprocessableEntity || !strings.Contains(body, "the statement and the book differ by $30.00") {
		t.Errorf("cut-over matching entry sum instead of ledger balance: status = %d body %s, want 422 differing by $30.00", code, body)
	}
	code, body = post(`{"cutover_date":"2022-03-31","opening_statement_balance_cents":503000,"outstanding_journal_ids":["` + chequeJID + `"]}`)
	if code != http.StatusOK {
		t.Errorf("cut-over matching ledger balance ($4,830.00 less -$200.00 cheque = $5,030.00): status = %d body %s, want 200 OK", code, body)
	}
	_ = openJID
}
