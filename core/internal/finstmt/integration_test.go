//go:build integration

// Integration tests, the real pgx Repository + Service against a PRIVATE migrated+seeded Postgres
// (hos_w5_finstmt), reading the 0034 POSTED-filtered views. The gl module is not posting in this worktree,
// so each test seeds gl_journals/gl_journal_lines (and, for aging, customer_transactions) DIRECTLY to
// produce POSTED books, then asserts the A-04 acceptance criteria:
//
//	AC1 POSTED-only views · AC2 balance-sheet invariant incl. 2500/2510/2520 loans/leases ·
//	AC3 branch-sum == consolidated · AC4 comparatives incl. empty history · AC5 drill-down ties ·
//	AC6 aging buckets + subledger tie to billing.ARBalanceCents · AC8 perf smoke · AC9 schedules CRUD.
//
// Run: DATABASE_URL=…hos_w5_finstmt… go test -tags=integration ./internal/finstmt/... -count=1 -p 1
package finstmt

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	trenton  = "5d1b3f6e-0000-4000-9000-000000000001"
	kingston = "5d1b3f6e-0000-4000-9000-000000000002"
)

func intPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url, set := os.LookupEnv("DATABASE_URL")
	if url == "" {
		url = "postgres://hardscape:hardscape@localhost:5435/hardscapeos?sslmode=disable"
	}
	fail := func(format string, a ...any) {
		if set {
			t.Fatalf(format, a...)
		}
		t.Skipf(format, a...)
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		fail("integration DB unavailable: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		fail("integration DB not reachable: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// -- seeding helpers (direct POSTED books; gl isn't posting in this worktree) --

type jline struct {
	code   string
	debit  int64
	credit int64
}

func ensurePeriod(t *testing.T, pool *pgxpool.Pool, year, month int) string {
	t.Helper()
	ctx := context.Background()
	_, err := pool.Exec(ctx,
		`INSERT INTO fiscal_periods (year, month) VALUES ($1, $2) ON CONFLICT (year, month) DO NOTHING`,
		year, month)
	if err != nil {
		t.Fatalf("ensure period: %v", err)
	}
	var id string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM fiscal_periods WHERE year=$1 AND month=$2`, year, month).Scan(&id); err != nil {
		t.Fatalf("period id: %v", err)
	}
	return id
}

func accountID(t *testing.T, pool *pgxpool.Pool, code string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), `SELECT id::text FROM gl_accounts WHERE code=$1`, code).Scan(&id); err != nil {
		t.Fatalf("account %s: %v", code, err)
	}
	return id
}

func branchArg(b string) any {
	if b == "" {
		return nil
	}
	return b
}

// seedJournal inserts one journal + its lines. status POSTED stamps posted_at; anything else (DRAFT)
// stays out of the views. entry_no must be unique within a fiscal year, the caller manages it.
func seedJournal(t *testing.T, pool *pgxpool.Pool, year, month, entryNo int, status, branch string, lines []jline) {
	t.Helper()
	ctx := context.Background()
	period := ensurePeriod(t, pool, year, month)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)

	var postedAt any
	if status == "POSTED" {
		postedAt = time.Date(year, time.Month(month), 15, 12, 0, 0, 0, time.UTC)
	}
	var journalID string
	err = tx.QueryRow(ctx,
		`INSERT INTO gl_journals (fiscal_year, entry_no, period_id, source, memo, status, posted_at, branch_id)
		 VALUES ($1, $2, $3, 'MANUAL', 'finstmt integration', $4, $5, $6) RETURNING id::text`,
		year, entryNo, period, status, postedAt, branchArg(branch)).Scan(&journalID)
	if err != nil {
		t.Fatalf("insert journal: %v", err)
	}
	for i, l := range lines {
		var debit, credit any
		if l.debit > 0 {
			debit = l.debit
		}
		if l.credit > 0 {
			credit = l.credit
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO gl_journal_lines (journal_id, line_no, account_id, debit_cents, credit_cents, branch_id)
			 VALUES ($1, $2, $3, $4, $5, $6)`,
			journalID, i+1, accountID(t, pool, l.code), debit, credit, branchArg(branch)); err != nil {
			t.Fatalf("insert line %s: %v", l.code, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// resetYear makes a test re-runnable: it removes any journals this test seeded for its private fiscal
// year (lines cascade) so re-seeding never collides with the (fiscal_year, entry_no) unique key.
func resetYear(t *testing.T, pool *pgxpool.Pool, year int) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `DELETE FROM gl_journals WHERE fiscal_year = $1`, year); err != nil {
		t.Fatalf("reset year %d: %v", year, err)
	}
}

// realSvc composes the real repository behind a GLOBAL caller, the Financial-Controller posture these
// AC tests have always described (they read the CONSOLIDATED books, which DIB-391 rules a GLOBAL-scope
// capability). The branch fence itself is exercised in finstmt_fence_integration_test.go.
func realSvc(pool *pgxpool.Pool) *Service {
	return NewService(NewRepository(pool)).withCaller(func(context.Context) (identity.StaffContext, bool) {
		return identity.StaffContext{UserID: "it-global", Scope: identity.ScopeGlobal, Tier: identity.TierLeadership}, true
	})
}

func amount(rows []StatementRow, code string) int64 {
	for _, r := range rows {
		if r.AccountCode == code {
			return r.AmountCents
		}
	}
	return 0
}

// -- AC1 POSTED-only views ----------------------------------------------------

func TestIntegration_AC1_PostedOnly(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	svc := realSvc(pool)
	const yr = 2101
	resetYear(t, pool, yr)

	seedJournal(t, pool, yr, 3, 1, "POSTED", trenton, []jline{
		{code: "1000", debit: 100_000}, {code: "4000", credit: 100_000},
	})
	seedJournal(t, pool, yr, 3, 2, "DRAFT", trenton, []jline{
		{code: "1000", debit: 999_999}, {code: "4000", credit: 999_999},
	})

	is, err := svc.IncomeStatement(ctx, yr, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := amount(is, "4000"); got != 100_000 {
		t.Fatalf("revenue 4000 = %d, want 100000 (DRAFT must be excluded)", got)
	}
	// the DRAFT journal must not appear in gl detail either
	detail, err := svc.GLDetail(ctx, GLDetailFilter{Year: yr, AccountCode: "4000"})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range detail {
		if d.CreditCents == 999_999 {
			t.Fatal("DRAFT line leaked into v_gl_detail")
		}
	}
}

// -- AC2 balance-sheet invariant incl. loans & leases -------------------------

func TestIntegration_AC2_BalanceSheetInvariant(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	svc := realSvc(pool)
	const yr = 2102
	resetYear(t, pool, yr)
	// Round 5 (S3's P1): the year's periods exist BEFORE the baseline is read, so the carried
	// history (0439) stands behind both reads equally and the proof stays the fixture's own delta.
	ensureStmtPeriods(t, pool, yr)

	// 0439: the balance sheet carries history forward, so the year's totals stand on every year behind
	// it, the seeded books included. The baseline is read BEFORE the fixture, and the proof is the
	// DELTA the fixture's own movements make, on a database whose carried history may hold a seeded
	// year nobody closed (its net income sits in its own P&L, so the absolute report need not
	// balance; the fixture's movements must, and do).
	base, err := svc.BalanceSheetReportFor(ctx, yr, "")
	if err != nil {
		t.Fatal(err)
	}
	baseLiab := func(code string) int64 { return amount(base.Liabilities, code) }

	// A balanced set of POSTED journals touching assets, three loan/lease liabilities, equity, rev, exp.
	seedJournal(t, pool, yr, 1, 1, "POSTED", trenton, []jline{{code: "1000", debit: 1_000_000}, {code: "3000", credit: 1_000_000}})
	seedJournal(t, pool, yr, 1, 2, "POSTED", trenton, []jline{{code: "1000", debit: 200_000}, {code: "2500", credit: 200_000}})
	seedJournal(t, pool, yr, 1, 3, "POSTED", trenton, []jline{{code: "1500", debit: 150_000}, {code: "2510", credit: 150_000}})
	seedJournal(t, pool, yr, 1, 4, "POSTED", trenton, []jline{{code: "1500", debit: 100_000}, {code: "2520", credit: 100_000}})
	seedJournal(t, pool, yr, 2, 5, "POSTED", trenton, []jline{{code: "1100", debit: 500_000}, {code: "4000", credit: 500_000}})
	seedJournal(t, pool, yr, 2, 6, "POSTED", trenton, []jline{{code: "5000", debit: 300_000}, {code: "1000", credit: 300_000}})

	rep, err := svc.BalanceSheetReportFor(ctx, yr, "")
	if err != nil {
		t.Fatal(err)
	}
	// The absolute identity holds with earnings to date (round 4): assets == liabilities + equity
	// (the carried equity plus the unclosed years' retained earnings to date) + this year's net income.
	if !rep.Balanced {
		t.Fatalf("not balanced: A=%d L=%d E=%d NI=%d",
			rep.TotalAssetsCents, rep.TotalLiabilitiesCents, rep.TotalEquityCents, rep.NetIncomeCents)
	}
	// The fixture's own movements, per bucket, over the baseline.
	dA := rep.TotalAssetsCents - base.TotalAssetsCents
	dL := rep.TotalLiabilitiesCents - base.TotalLiabilitiesCents
	dE := rep.TotalEquityCents - base.TotalEquityCents
	dNI := rep.NetIncomeCents - base.NetIncomeCents
	if dA != 1_650_000 || dL != 450_000 || dE != 1_000_000 || dNI != 200_000 {
		t.Fatalf("unexpected deltas over the baseline: base=%+v rep=%+v", base, rep)
	}
	// the loan/lease liabilities must be present and carry their balances
	if amount(rep.Liabilities, "2500")-baseLiab("2500") != 200_000 ||
		amount(rep.Liabilities, "2510")-baseLiab("2510") != 150_000 ||
		amount(rep.Liabilities, "2520")-baseLiab("2520") != 100_000 {
		t.Fatalf("loan/lease liabilities wrong: %+v", rep.Liabilities)
	}
}

// -- AC3 branch-sum == consolidated -------------------------------------------

func TestIntegration_AC3_BranchSumEqualsConsolidated(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	svc := realSvc(pool)
	const yr = 2103
	resetYear(t, pool, yr)

	seedJournal(t, pool, yr, 4, 1, "POSTED", trenton, []jline{{code: "1100", debit: 600_000}, {code: "4000", credit: 600_000}})
	seedJournal(t, pool, yr, 4, 2, "POSTED", trenton, []jline{{code: "5000", debit: 200_000}, {code: "1100", credit: 200_000}})
	seedJournal(t, pool, yr, 4, 3, "POSTED", kingston, []jline{{code: "1100", debit: 400_000}, {code: "4000", credit: 400_000}})
	seedJournal(t, pool, yr, 4, 4, "POSTED", kingston, []jline{{code: "5000", debit: 100_000}, {code: "1100", credit: 100_000}})

	trt, err := svc.IncomeStatementReportFor(ctx, yr, 0, trenton)
	if err != nil {
		t.Fatal(err)
	}
	kgn, err := svc.IncomeStatementReportFor(ctx, yr, 0, kingston)
	if err != nil {
		t.Fatal(err)
	}
	con, err := svc.IncomeStatementReportFor(ctx, yr, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if trt.NetIncomeCents+kgn.NetIncomeCents != con.NetIncomeCents {
		t.Fatalf("branch sum %d != consolidated %d", trt.NetIncomeCents+kgn.NetIncomeCents, con.NetIncomeCents)
	}
	if con.RevenueCents != 1_000_000 || con.ExpenseCents != 300_000 {
		t.Fatalf("consolidated rev/exp wrong: rev=%d exp=%d", con.RevenueCents, con.ExpenseCents)
	}
	// per-account additivity too
	if amount(trt.Revenue, "4000")+amount(kgn.Revenue, "4000") != amount(con.Revenue, "4000") {
		t.Fatal("per-account branch sum != consolidated for 4000")
	}
	// balance sheet consolidation likewise
	bTrt, _ := svc.BalanceSheet(ctx, yr, trenton)
	bKgn, _ := svc.BalanceSheet(ctx, yr, kingston)
	bCon, _ := svc.BalanceSheet(ctx, yr, "")
	if amount(bTrt, "1100")+amount(bKgn, "1100") != amount(bCon, "1100") {
		t.Fatal("balance sheet 1100 branch sum != consolidated")
	}
}

// -- AC4 comparatives incl. empty history -------------------------------------

func TestIntegration_AC4_ComparativesEmptyHistory(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	svc := realSvc(pool)
	const yr = 2105 // prior year 2104 is intentionally empty
	resetYear(t, pool, yr)

	seedJournal(t, pool, yr, 6, 1, "POSTED", trenton, []jline{{code: "1000", debit: 700_000}, {code: "4000", credit: 700_000}})

	rows, err := svc.IncomeStatementComparison(ctx, yr, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 {
		t.Fatal("empty comparison, current rows dropped")
	}
	var found bool
	for _, r := range rows {
		if r.AccountCode == "4000" {
			found = true
			if r.CurrentCents != 700_000 || r.PriorCents != 0 || r.DeltaCents != 700_000 {
				t.Fatalf("4000 comparative wrong (empty prior): %+v", r)
			}
		}
	}
	if !found {
		t.Fatal("current-year revenue account missing from comparison")
	}
}

// -- AC5 drill-down ties ------------------------------------------------------

func TestIntegration_AC5_DrillDownTies(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	svc := realSvc(pool)
	const yr = 2106
	resetYear(t, pool, yr)

	// two POSTED revenue postings + a small contra debit → the statement figure must equal Σ detail
	seedJournal(t, pool, yr, 5, 1, "POSTED", trenton, []jline{{code: "1000", debit: 300_000}, {code: "4000", credit: 300_000}})
	seedJournal(t, pool, yr, 5, 2, "POSTED", trenton, []jline{{code: "1000", debit: 120_000}, {code: "4000", credit: 120_000}})
	seedJournal(t, pool, yr, 5, 3, "POSTED", trenton, []jline{{code: "4000", debit: 20_000}, {code: "1000", credit: 20_000}}) // contra

	is, err := svc.IncomeStatementReportFor(ctx, yr, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	stmtFig := amount(is.Revenue, "4000")
	if stmtFig != 400_000 { // 300k + 120k - 20k
		t.Fatalf("statement figure 4000 = %d, want 400000", stmtFig)
	}
	detail, tie, err := svc.DrillDown(ctx, yr, 0, "4000", "REVENUE", "")
	if err != nil {
		t.Fatal(err)
	}
	if tie != stmtFig {
		t.Fatalf("drill-down tie %d != statement figure %d", tie, stmtFig)
	}
	if len(detail) != 3 {
		t.Fatalf("expected 3 detail lines behind 4000, got %d", len(detail))
	}
}

// -- AC6 aging buckets + subledger tie to billing.ARBalanceCents --------------

func TestIntegration_AC6_AgingTiesToSubledger(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	svc := realSvc(pool)

	// A non-invoice AR movement (NSF fee) must be counted by the aging view, insert one and prove the
	// grand total still ties to billing's ARBalanceCents (both union the same two sources).
	cust := "c7000000-0000-4000-9000-000000000001"
	// re-runnable: drop any NSF row this test previously seeded
	if _, err := pool.Exec(ctx, `DELETE FROM customer_transactions WHERE memo = 'finstmt aging test'`); err != nil {
		t.Fatalf("reset aging txn: %v", err)
	}
	_, err := pool.Exec(ctx,
		`INSERT INTO customer_transactions (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, memo)
		 VALUES ($1, 'NSF', 4500, 4500, 'nsf', gen_random_uuid(), $2, 'finstmt aging test')`,
		cust, trenton)
	if err != nil {
		t.Fatalf("seed NSF txn: %v", err)
	}

	sum, err := svc.AgingSummaryFor(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	// bucket totals must re-sum to the grand total
	var bucketSum int64
	for _, b := range sum.Buckets {
		bucketSum += b.Cents
	}
	if bucketSum != sum.GrandTotalCents {
		t.Fatalf("Σbuckets %d != grand total %d", bucketSum, sum.GrandTotalCents)
	}
	// a non-invoice source_type must be present among the rows
	var sawNonInvoice bool
	for _, r := range sum.Rows {
		if r.SourceType != "invoice" {
			sawNonInvoice = true
		}
	}
	if !sawNonInvoice {
		t.Fatal("no non-invoice AR row in aging, customer_transactions union missing")
	}
	// Subledger tie: aging grand total == billing.ARBalanceCents. v_ar_aging carries NO effective date
	// cutoff (every open invoice + non-invoice AR row is present), so the tie is the subledger balance
	// "as of end of time", a far-future asOf makes the comparison exact and deterministic. (An asOf of
	// today's now()+1h is unreliable here: in this environment billing's asOf binds date-only, so rows
	// created earlier today fall outside a same-day cutoff, see the worker report deviation note.)
	farFuture := time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)
	arBal, err := billing.NewRepository(pool).ARBalanceCents(ctx, farFuture, "")
	if err != nil {
		t.Fatal(err)
	}
	if sum.GrandTotalCents != arBal {
		t.Fatalf("aging grand total %d != ARBalanceCents %d", sum.GrandTotalCents, arBal)
	}
}

// -- AC8 perf smoke -----------------------------------------------------------

func TestIntegration_AC8_PerfSmoke(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	svc := realSvc(pool)
	const yr = 2108
	resetYear(t, pool, yr)

	// seed a few hundred balanced POSTED lines, then time the statement queries
	for n := 0; n < 300; n++ {
		seedJournal(t, pool, yr, 1, n+1, "POSTED", trenton, []jline{
			{code: "1000", debit: 1_000}, {code: "4000", credit: 1_000},
		})
	}
	start := time.Now()
	if _, err := svc.IncomeStatementReportFor(ctx, yr, 0, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.BalanceSheetReportFor(ctx, yr, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.TrialBalanceReportFor(ctx, yr); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AgingSummaryFor(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("statement smoke too slow: %s", elapsed)
	}
}

// -- AC9 schedules CRUD -------------------------------------------------------

func TestIntegration_AC9_SchedulesCRUD(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	svc := realSvc(pool)

	// reject invalid payloads (validated in-app before Postgres CHECK)
	if _, err := svc.CreateSchedule(ctx, ScheduleInput{Report: "BOGUS", Recipients: "a@b.com", Cadence: "MONTHLY"}, ""); err == nil {
		t.Fatal("bad report accepted")
	}
	if _, err := svc.CreateSchedule(ctx, ScheduleInput{Report: "AR_AGING", Recipients: "nope", Cadence: "MONTHLY"}, ""); err == nil {
		t.Fatal("bad recipient accepted")
	}

	created, err := svc.CreateSchedule(ctx, ScheduleInput{
		Report: "income_statement", Recipients: " ctrl@dibbits.example ", Cadence: "monthly", BranchID: trenton, IsActive: true,
	}, "")
	if err != nil {
		t.Fatalf("create valid: %v", err)
	}
	if created.Report != "INCOME_STATEMENT" || created.Cadence != "MONTHLY" || created.Recipients != "ctrl@dibbits.example" {
		t.Fatalf("create normalize wrong: %+v", created)
	}

	list, err := svc.ListSchedules(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSchedule(list, created.ID) {
		t.Fatal("created schedule missing from list")
	}

	updated, err := svc.UpdateSchedule(ctx, created.ID, ScheduleInput{
		Report: "AR_AGING", Recipients: "ctrl@dibbits.example,ap@dibbits.example", Cadence: "WEEKLY", IsActive: false,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Report != "AR_AGING" || updated.Cadence != "WEEKLY" || updated.IsActive {
		t.Fatalf("update wrong: %+v", updated)
	}

	if err := svc.DeleteSchedule(ctx, created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := svc.GetSchedule(ctx, created.ID); err != errNotFound {
		t.Fatalf("expected errNotFound after delete, got %v", err)
	}
	// deleting again is a not-found
	if err := svc.DeleteSchedule(ctx, created.ID); err != errNotFound {
		t.Fatalf("expected errNotFound on second delete, got %v", err)
	}
}

func containsSchedule(list []Schedule, id string) bool {
	for _, s := range list {
		if s.ID == id {
			return true
		}
	}
	return false
}

func tbRow(rows []TrialBalanceRow, code string) (TrialBalanceRow, bool) {
	for _, r := range rows {
		if r.AccountCode == code {
			return r, true
		}
	}
	return TrialBalanceRow{}, false
}

// -- AC7 trial balance is in balance (Σdebit == Σcredit), POSTED-only ---------

func TestIntegration_AC7_TrialBalanceInBalance(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	svc := realSvc(pool)
	const yr = 2107
	resetYear(t, pool, yr)
	// Round 5 (S3's P1): the year's periods exist BEFORE the baseline is read, so the carried
	// history (0439) stands behind both reads equally and the proof stays the fixture's own delta.
	ensureStmtPeriods(t, pool, yr)

	// Round 4 (S3's P2): the trial balance's BALANCE accounts read inception to date, so the proof is
	// the DELTA over the baseline this database already held (the seeded books and any earlier band's
	// history stand behind the year), the P&L accounts staying the year's own movement.
	base, err := svc.TrialBalanceReportFor(ctx, yr)
	if err != nil {
		t.Fatal(err)
	}
	baseBy := func(code string) (int64, int64, bool) {
		r, ok := tbRow(base.Rows, code)
		return r.TotalDebitCents, r.TotalCreditCents, ok
	}
	baseTotals := [2]int64{base.TotalDebitCents, base.TotalCreditCents}

	// Several balanced POSTED journals touching assets/liabilities/equity/revenue/expense.
	// Each journal balances by construction (debits == credits), so Σall-debits == Σall-credits.
	// The seeded line amounts are the ground truth; we independently accumulate the expected
	// per-account Σdebit/Σcredit here and reconcile them against the real v_trial_balance view.
	posted := []struct {
		entry int
		month int
		lines []jline
	}{
		{1, 1, []jline{{code: "1000", debit: 1_000_000}, {code: "3000", credit: 1_000_000}}}, // owner equity into cash
		{2, 1, []jline{{code: "1100", debit: 400_000}, {code: "2500", credit: 400_000}}},     // borrow (liability)
		{3, 2, []jline{{code: "1000", debit: 600_000}, {code: "4000", credit: 600_000}}},     // revenue collected to cash
		{4, 2, []jline{{code: "5000", debit: 250_000}, {code: "1000", credit: 250_000}}},     // expense paid from cash
	}

	// Expected totals + per-account tallies derived from the fixtures above.
	var expDebit, expCredit int64
	debitBy := map[string]int64{}
	creditBy := map[string]int64{}
	for _, j := range posted {
		for _, l := range j.lines {
			expDebit += l.debit
			expCredit += l.credit
			debitBy[l.code] += l.debit
			creditBy[l.code] += l.credit
		}
		seedJournal(t, pool, yr, j.month, j.entry, "POSTED", trenton, j.lines)
	}
	// A DRAFT journal that must NOT move any total (POSTED-only view).
	seedJournal(t, pool, yr, 2, 5, "DRAFT", trenton, []jline{
		{code: "1000", debit: 777_777}, {code: "4000", credit: 777_777},
	})

	rep, err := svc.TrialBalanceReportFor(ctx, yr)
	if err != nil {
		t.Fatal(err)
	}

	// (1) the double-entry tie holds
	if !rep.InBalance {
		t.Fatalf("trial balance not in balance: debit=%d credit=%d", rep.TotalDebitCents, rep.TotalCreditCents)
	}
	// (2) totals are equal
	if rep.TotalDebitCents != rep.TotalCreditCents {
		t.Fatalf("Σdebit %d != Σcredit %d", rep.TotalDebitCents, rep.TotalCreditCents)
	}
	// (2b) round 5 (S3's section 3): every row's balance is debit minus credit, the computed 3901
	// line included, so the whole report's signed balances sum to zero.
	var balanceSum int64
	for _, r := range rep.Rows {
		balanceSum += r.BalanceCents
	}
	if balanceSum != 0 {
		t.Fatalf("Σbalance %d != 0 (the 3901 line's sign is off by twice its amount)", balanceSum)
	}
	// (3) the DELTAS equal the exact Σ of seeded (balanced) amounts, DRAFT excluded
	if rep.TotalDebitCents-baseTotals[0] != expDebit {
		t.Fatalf("Σdebit delta %d != seeded Σdebit %d (DRAFT must be excluded)", rep.TotalDebitCents-baseTotals[0], expDebit)
	}
	if rep.TotalCreditCents-baseTotals[1] != expCredit {
		t.Fatalf("Σcredit delta %d != seeded Σcredit %d (DRAFT must be excluded)", rep.TotalCreditCents-baseTotals[1], expCredit)
	}

	// (4) one account's BalanceCents delta == its Σdebit − Σcredit from the fixtures (1000 is a BALANCE
	// account, inception to date since 0439 round 4: the fixture's delta over the carried baseline).
	// 1000 (cash): debit 1_000_000 + 600_000, credit 250_000 → delta 1_350_000.
	const cashCode = "1000"
	row, ok := tbRow(rep.Rows, cashCode)
	if !ok {
		t.Fatalf("account %s missing from trial balance", cashCode)
	}
	wantCashBal := debitBy[cashCode] - creditBy[cashCode]
	bd, bc, had := baseBy(cashCode)
	if row.BalanceCents-(bd-bc) != wantCashBal {
		t.Fatalf("%s balance delta %d != Σdebit−Σcredit %d", cashCode, row.BalanceCents-(bd-bc), wantCashBal)
	}
	if had && (row.TotalDebitCents-bd != debitBy[cashCode] || row.TotalCreditCents-bc != creditBy[cashCode]) {
		t.Fatalf("%s debit/credit columns wrong: got d=%d c=%d want d=%d c=%d",
			cashCode, row.TotalDebitCents-bd, row.TotalCreditCents-bc, debitBy[cashCode], creditBy[cashCode])
	}
	// the DRAFT credit of 777_777 must not have leaked into revenue 4000's credit column
	if r4000, ok := tbRow(rep.Rows, "4000"); ok && r4000.TotalCreditCents != creditBy["4000"] {
		t.Fatalf("4000 credit %d != seeded %d (DRAFT leaked)", r4000.TotalCreditCents, creditBy["4000"])
	}
}
