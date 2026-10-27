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

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	identityPkg "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
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
	if !strings.Contains(err.Error(), "month end") {
		t.Errorf("Leg 3: refusal %q does not mention month end", err.Error())
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
	j, err := glSvc.GetJournal(ctx, m.JournalID)
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

// Item 3 (FROM-PRINCIPAL 117): The Lodgement Rule, testing the new lodgement case (a lodgement in 1000 not yet on a
// statement is a deposit in transit, and a new lodgement matched on a statement clears without booking 1020->1000 a
// second time) beside the older lodgement case (a lodgement from before this change has no lodgement journal, so it
// keeps today's match posting 1020->1000).
func TestIntegration_FW1_04_LodgementRuleNewInTransitAndClearedBesideOlderLodgement(t *testing.T) {
	pool := intPool(t)
	cleanTables(t, pool)
	acct := rbcAccountID(t, pool)
	branch := aBranch(t, pool)
	ctx := context.Background()

	const year = 2023
	may10 := time.Date(year, 5, 10, 0, 0, 0, 0, time.UTC)
	may20 := time.Date(year, 5, 20, 0, 0, 0, 0, time.UTC)
	may30 := time.Date(year, 5, 30, 12, 0, 0, 0, time.UTC)
	june05 := time.Date(year, 6, 5, 14, 0, 0, 0, time.UTC)
	fw104CleanYear(t, pool, acct, year)
	fw104EnsurePeriods(t, pool, year, may10)
	t.Cleanup(func() { fw104CleanYear(t, pool, acct, year) })

	const (
		oldMoveID        = "87040011-0000-4000-8000-000000000011" // Older lodgement: no journal in 1000 before match ($250.00)
		newClearedMoveID = "87040012-0000-4000-8000-000000000012" // New lodgement: DR 1000 / CR 1020 at lodgement ($400.00), on May statement
		newTransitMoveID = "87040013-0000-4000-8000-000000000013" // New lodgement: DR 1000 / CR 1020 at lodgement ($300.00), in transit at May end
	)

	glSvc := gl.NewService(gl.NewRepository(pool))
	// Book the two new lodgements at lodgement time (DR 1000 / CR 1020, SourceRefID = movementID).
	if _, err := glSvc.PostExternal(ctx, gl.ExternalJournal{
		IdempotencyKey: "till.movement.posted:" + newClearedMoveID,
		Source:         "ADJUSTMENT",
		SourceRefID:    newClearedMoveID,
		Memo:           "Till lodgement " + newClearedMoveID[:8],
		BranchID:       branch,
		DocumentDate:   may20.Add(12 * time.Hour),
		Lines: []gl.ExternalLine{
			{AccountCode: "1000", DebitCents: 40000, Memo: "bank lodgement"},
			{AccountCode: "1020", CreditCents: 40000, Memo: "undeposited funds"},
		},
	}); err != nil {
		t.Fatalf("post new cleared lodgement journal: %v", err)
	}
	if _, err := glSvc.PostExternal(ctx, gl.ExternalJournal{
		IdempotencyKey: "till.movement.posted:" + newTransitMoveID,
		Source:         "ADJUSTMENT",
		SourceRefID:    newTransitMoveID,
		Memo:           "Till lodgement in transit " + newTransitMoveID[:8],
		BranchID:       branch,
		DocumentDate:   may30,
		Lines: []gl.ExternalLine{
			{AccountCode: "1000", DebitCents: 30000, Memo: "bank lodgement in transit"},
			{AccountCode: "1020", CreditCents: 30000, Memo: "undeposited funds"},
		},
	}); err != nil {
		t.Fatalf("post new transit lodgement journal: %v", err)
	}

	// May bank statement has two deposit lines: $250.00 for oldMoveID and $400.00 for newClearedMoveID.
	// newTransitMoveID ($300.00) is NOT on the May statement (deposit in transit).
	oldLine := fw104InsertLineAt(t, pool, acct, may10, "MAY OLD LODGEMENT DEPOSIT", "LODG-OLD", 25000)
	newLine := fw104InsertLineAt(t, pool, acct, may20, "MAY NEW LODGEMENT DEPOSIT", "LODG-NEW", 40000)

	lodgSeam := &fakeLodgements{
		open: []Lodgement{
			{MovementID: oldMoveID, BranchID: branch, AmountCents: 25000, Reference: "LODG-OLD", OccurredAt: may10},
			{MovementID: newClearedMoveID, BranchID: branch, AmountCents: 40000, Reference: "LODG-NEW", OccurredAt: may20},
			{MovementID: newTransitMoveID, BranchID: branch, AmountCents: 30000, Reference: "LODG-TRN", OccurredAt: may30},
		},
	}
	svc := NewService(NewRepository(pool)).
		WithLodgements(lodgSeam).
		WithJournals(glSvc).
		WithJournalPoster(fw104GLPoster{svc: glSvc, clock: func() time.Time { return june05 }}).
		WithGLBalances(glSvc)

	// Match the older lodgement ($250.00): it has no lodgement journal, so it must post DR 1000 / CR 1020 once.
	oldMatch, err := svc.ManualMatch(ctx, MatchInput{
		Mode: "manual", StatementLineID: oldLine, TargetType: TargetLodgement, TargetRefID: oldMoveID, AmountCents: 25000, CanAccessBranch: AllBranches,
	}, "")
	if err != nil {
		t.Fatalf("match older lodgement: %v", err)
	}
	if oldMatch.JournalID == "" {
		t.Errorf("older lodgement match posted no clearing journal")
	}

	// Match the new lodgement ($400.00): it already has its DR 1000 / CR 1020 journal, so matching it must NOT book
	// a second 1020 -> 1000 journal, and must mark the 1000 entry cleared.
	if _, err := svc.ManualMatch(ctx, MatchInput{
		Mode: "manual", StatementLineID: newLine, TargetType: TargetLodgement, TargetRefID: newClearedMoveID, AmountCents: 40000, CanAccessBranch: AllBranches,
	}, ""); err != nil {
		t.Fatalf("match new cleared lodgement: %v", err)
	}

	// May statement closing balance is $650.00 ($250.00 + $400.00).
	// Deposits in transit at May 31 must be $300.00 (only newTransitMoveID).
	// GL 1000 closing balance at May 31 must be $950.00 ($250.00 + $400.00 + $300.00), nothing booked twice.
	mayRec, err := svc.OpenReconciliation(ctx, ReconInput{BankAccountID: acct, Year: year, Month: 5, StatementBalanceCents: 65000})
	if err != nil {
		t.Fatalf("open may: %v", err)
	}
	if mayRec.DepositsInTransitCents != 30000 || len(mayRec.DepositsInTransit) != 1 {
		t.Errorf("May DepositsInTransitCents = %d (count %d), want 30000 (count 1 for newTransitMoveID)",
			mayRec.DepositsInTransitCents, len(mayRec.DepositsInTransit))
	}
	if mayRec.GLBalanceCents != 95000 || mayRec.AdjustedBankBalanceCents != 95000 || mayRec.VarianceCents != 0 {
		t.Errorf("May GL=%d Adjusted=%d Variance=%d, want 95000, 95000, 0",
			mayRec.GLBalanceCents, mayRec.AdjustedBankBalanceCents, mayRec.VarianceCents)
	}
	doneMay, err := svc.CompleteReconciliation(ctx, mayRec.ID, "")
	if err != nil {
		t.Fatalf("complete may with older + new cleared + new in-transit lodgements: %v", err)
	}
	if doneMay.Status != ReconCompleted || doneMay.VarianceCents != 0 {
		t.Errorf("completed May = %+v, want COMPLETED with variance 0", doneMay)
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
	staffCtx := identity.ContextWithStaff(ctx, identity.StaffContext{UserID: userID})
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
