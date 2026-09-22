//go:build integration

// Integration tests run the real pgx Repository + Service against the private DB that `make reset` (or the
// worker's hos_w5_gl provisioning) sets up, exercising the 0007+0021+0032+0034 schema. Run with:
//
//	DATABASE_URL='postgres://hardscape:hardscape@localhost:5435/hos_w5_gl?sslmode=disable' \
//	  go test -tags=integration ./internal/gl/... -count=1 -p 1
//
// Covers A-01 AC1 (ClosedPeriodBlocked), AC2 (OutboxReplay_SingleJournal), AC5 (Controller-only reopen +
// ReopenAudit), AC6 (TB tie-out with age), AC8 (boot posting-rule validation on seed), AC9 (GP parity),
// AC12 (recurring /run), AC13 (year-end close → 3900), plus the PostExternal + GLBalanceCents provider path
// bankrec depends on. Test years 209x are used so the suite never disturbs the seeded 2026 periods.
package gl

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/logging"
	"github.com/jackc/pgx/v5/pgxpool"
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

func aUser(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), `SELECT id::text FROM users LIMIT 1`).Scan(&id); err != nil {
		t.Skipf("no seeded user: %v", err)
	}
	return id
}

// ensurePeriods creates the 12 fiscal periods for a test year (idempotent) so posting/closing there never
// touches the seeded 2026 periods.
func ensurePeriods(t *testing.T, pool *pgxpool.Pool, year int) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx,
		`INSERT INTO fiscal_periods (year, month) SELECT $1, m FROM generate_series(1,12) AS m
		 ON CONFLICT (year, month) DO NOTHING`, year); err != nil {
		t.Fatalf("ensure periods %d: %v", year, err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM gl_journal_lines l USING gl_journals j WHERE l.journal_id=j.id AND j.fiscal_year=$1`, year)
		_, _ = pool.Exec(ctx, `DELETE FROM gl_journals WHERE fiscal_year=$1`, year)
		_, _ = pool.Exec(ctx, `DELETE FROM gl_period_overrides o USING fiscal_periods p WHERE o.period_id=p.id AND p.year=$1`, year)
		_, _ = pool.Exec(ctx, `DELETE FROM fiscal_periods WHERE year=$1`, year)
	})
}

func periodID(t *testing.T, pool *pgxpool.Pool, year, month int) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), `SELECT id::text FROM fiscal_periods WHERE year=$1 AND month=$2`, year, month).Scan(&id); err != nil {
		t.Fatalf("period %d-%d: %v", year, month, err)
	}
	return id
}

func svcAt(pool *pgxpool.Pool, caller identity.StaffContext, at time.Time) *Service {
	return NewService(NewRepository(pool)).
		withCaller(func(_ context.Context) (identity.StaffContext, bool) { return caller, caller.UserID != "" }).
		withClock(func() time.Time { return at })
}

// AC2 + provider, PostExternal posts a balanced journal, is idempotent on the key (a replay returns the same
// id and writes no second journal), and GLBalanceCents reads it back correctly (bankrec's dependency).
func TestIntegration_AC2_PostExternalReplayAndBalance(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	ensurePeriods(t, pool, 2098)
	at := time.Date(2098, 6, 15, 12, 0, 0, 0, time.UTC)
	svc := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager}, at)

	key := fmt.Sprintf("itest-%d", time.Now().UnixNano())
	in := ExternalJournal{
		Source: SourceAdjustment, IdempotencyKey: key, Memo: "bankrec clearing test",
		Lines: []ExternalLine{
			{AccountCode: "1000", DebitCents: 25000, Memo: "bank"},
			{AccountCode: "1020", CreditCents: 25000, Memo: "undeposited"},
		},
	}
	id1, err := svc.PostExternal(ctx, in)
	if err != nil {
		t.Fatalf("post external: %v", err)
	}
	id2, err := svc.PostExternal(ctx, in)
	if err != nil {
		t.Fatalf("replay post external: %v", err)
	}
	if id1 != id2 {
		t.Fatalf("idempotent replay must return the same journal id, got %s vs %s", id1, id2)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM gl_journals WHERE idempotency_key=$1`, key).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("replay wrote %d journals, want 1", n)
	}

	// GLBalanceCents: 1000 (debit-normal asset) should read +25000 for the period.
	bal, err := svc.GLBalanceCents(ctx, "1000", 2098, 6, "")
	if err != nil {
		t.Fatalf("gl balance: %v", err)
	}
	if bal != 25000 {
		t.Fatalf("GL 1000 balance = %d, want 25000", bal)
	}
	// 1020 (also debit-normal) should read −25000 (credited).
	bal2, err := svc.GLBalanceCents(ctx, "1020", 2098, 6, "")
	if err != nil {
		t.Fatalf("gl balance 1020: %v", err)
	}
	if bal2 != -25000 {
		t.Fatalf("GL 1020 balance = %d, want -25000", bal2)
	}
}

// AC1, a journal cannot post into a CLOSED period.
func TestIntegration_AC1_ClosedPeriodBlocked(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	ensurePeriods(t, pool, 2097)
	pid := periodID(t, pool, 2097, 6)
	repo := NewRepository(pool)
	if _, err := repo.ClosePeriod(ctx, pid, user, "test close"); err != nil {
		t.Fatalf("close period: %v", err)
	}
	at := time.Date(2097, 6, 15, 12, 0, 0, 0, time.UTC)
	svc := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager}, at)
	_, err := svc.PostExternal(ctx, ExternalJournal{
		Source: SourceAdjustment, IdempotencyKey: fmt.Sprintf("closed-%d", time.Now().UnixNano()),
		Lines: []ExternalLine{{AccountCode: "1000", DebitCents: 100}, {AccountCode: "1020", CreditCents: 100}},
	})
	if err == nil || !strings.Contains(err.Error(), "CLOSED") {
		t.Fatalf("posting into a closed period must fail with a CLOSED error, got %v", err)
	}
}

// AC5, reopen is Financial-Controller-only, requires a reason, and writes a sensitive-access audit record.
func TestIntegration_AC5_ControllerReopenWithAudit(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	ensurePeriods(t, pool, 2096)
	pid := periodID(t, pool, 2096, 3)

	// Close it first (as a manager).
	mgr := identity.StaffContext{UserID: user, RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager}
	if _, err := svcAt(pool, mgr, time.Now()).ClosePeriod(ctx, pid, ""); err != nil {
		t.Fatalf("close: %v", err)
	}

	auditSvc := audit.NewService(audit.NewRepository(pool), logging.New("test"))

	// A non-controller (even a Tier-1 GM) must be refused by the service.
	gm := identity.StaffContext{UserID: user, RoleCode: "GENERAL_MANAGER", Tier: identity.TierLeadership, Scope: identity.ScopeGlobal}
	if _, err := svcAt(pool, gm, time.Now()).WithAudit(auditSvc).ReopenPeriod(ctx, pid, "trying"); err == nil || !strings.Contains(err.Error(), "Controller") {
		t.Fatalf("non-controller reopen must be refused, got %v", err)
	}

	// A missing reason is rejected.
	ctrl := identity.StaffContext{UserID: user, RoleCode: controllerRole, Tier: identity.TierLeadership, Scope: identity.ScopeGlobal}
	if _, err := svcAt(pool, ctrl, time.Now()).WithAudit(auditSvc).ReopenPeriod(ctx, pid, "   "); err == nil {
		t.Fatal("reopen without a reason must be rejected")
	}

	// The Controller reopens with a reason → OPEN + an audit row + a REOPEN override.
	reason := "external accountant year-end adjustment"
	p, err := svcAt(pool, ctrl, time.Now()).WithAudit(auditSvc).ReopenPeriod(ctx, pid, reason)
	if err != nil {
		t.Fatalf("controller reopen: %v", err)
	}
	if p.Status != "OPEN" {
		t.Fatalf("period should be OPEN after reopen, got %s", p.Status)
	}
	var overrides int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM gl_period_overrides WHERE period_id=$1 AND action='REOPEN' AND reason=$2`, pid, reason).Scan(&overrides); err != nil {
		t.Fatal(err)
	}
	if overrides != 1 {
		t.Fatalf("expected 1 REOPEN override with the reason, got %d", overrides)
	}
	var auditRows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action=$1 AND entity_id=$2`, actGLPeriodReopen, pid).Scan(&auditRows); err != nil {
		t.Fatal(err)
	}
	if auditRows < 1 {
		t.Fatalf("expected a GL_PERIOD_REOPEN audit row, got %d", auditRows)
	}
}

// AC6, the AR subledger tie-out persists the FIRST-seen discrepancy age across checks, and clears it on tie.
func TestIntegration_AC6_TieOutAge(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	// Clean any prior AR/1100 tie-out state so the first-seen timestamp is deterministic.
	_, _ = pool.Exec(ctx, `DELETE FROM gl_tieout_state WHERE subledger='AR' AND account_code='1100' AND branch_id IS NULL`)

	mgr := identity.StaffContext{UserID: user, RoleCode: "PAYABLES_RECEIVABLES", Tier: identity.TierManager}
	// The tie-out GL side is now INCEPTION-TO-DATE (all years), so we anchor the fake subledger to the real
	// as-of GL 1100 balance and perturb it, rather than assuming a far year is empty.
	asOf := time.Date(2095, 6, 15, 0, 0, 0, 0, time.UTC)
	glBase, _, err := NewRepository(pool).AccountBalanceAsOf(ctx, "1100", asOf, "")
	if err != nil {
		t.Fatalf("gl base: %v", err)
	}

	// Out of tie: subledger is GL + 99900 → a non-zero delta of 99900.
	svc := svcAt(pool, mgr, asOf.Add(12*time.Hour)).WithARSubledger(fixedAR{v: glBase + 99900})
	states, err := svc.TieOut(ctx, asOf, "")
	if err != nil {
		t.Fatalf("tie-out: %v", err)
	}
	if len(states) != 1 || states[0].InTie || states[0].DeltaCents != 99900 {
		t.Fatalf("expected one out-of-tie AR state with delta 99900, got %+v", states)
	}
	if states[0].OutOfTieSince == nil {
		t.Fatal("out-of-tie state must carry a first-seen timestamp")
	}
	first := *states[0].OutOfTieSince

	// A later re-check while still out of tie must KEEP the original first-seen timestamp (the age accrues).
	svc2 := svcAt(pool, mgr, time.Date(2095, 6, 20, 12, 0, 0, 0, time.UTC)).WithARSubledger(fixedAR{v: glBase + 99900})
	states2, err := svc2.TieOut(ctx, time.Date(2095, 6, 20, 0, 0, 0, 0, time.UTC), "")
	if err != nil {
		t.Fatalf("tie-out 2: %v", err)
	}
	if states2[0].OutOfTieSince == nil || !states2[0].OutOfTieSince.Equal(first) {
		t.Fatalf("first-seen age must persist across checks: was %v now %v", first, states2[0].OutOfTieSince)
	}

	// Back in tie (subledger == GL 1100 inception-to-date) → the discrepancy clears.
	svc3 := svcAt(pool, mgr, time.Now()).WithARSubledger(fixedAR{v: glBase})
	states3, err := svc3.TieOut(ctx, time.Date(2095, 6, 25, 0, 0, 0, 0, time.UTC), "")
	if err != nil {
		t.Fatalf("tie-out 3: %v", err)
	}
	if !states3[0].InTie || states3[0].OutOfTieSince != nil {
		t.Fatalf("returning to tie must clear the discrepancy age, got %+v", states3[0])
	}
	_, _ = pool.Exec(ctx, `DELETE FROM gl_tieout_state WHERE subledger='AR' AND account_code='1100' AND branch_id IS NULL`)
}

// AC6b, an AR balance that spans a YEAR BOUNDARY must NOT read as a discrepancy. The GL 1100 side is
// inception-to-date (all fiscal years through asOf), matching the inception-to-date subledger; the prior-FY
// open invoice's 1100 debit is carried forward, so a same-amount subledger ties out with ZERO delta. Before
// the fix the GL side summed only the current fiscal year's 1100 movement (0 in the new year) → a false delta.
func TestIntegration_AC6b_TieOutYearBoundary(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	ensurePeriods(t, pool, 2094)
	ensurePeriods(t, pool, 2095)
	_, _ = pool.Exec(ctx, `DELETE FROM gl_tieout_state WHERE subledger='AR' AND account_code='1100' AND branch_id IS NULL`)

	mgr := identity.StaffContext{UserID: user, RoleCode: "PAYABLES_RECEIVABLES", Tier: identity.TierManager}
	repo := NewRepository(pool)
	asOf := time.Date(2095, 6, 15, 0, 0, 0, 0, time.UTC)

	// The inception-to-date GL 1100 as-of 2095, BEFORE the prior-FY posting.
	glBefore, _, err := repo.AccountBalanceAsOf(ctx, "1100", asOf, "")
	if err != nil {
		t.Fatalf("gl before: %v", err)
	}

	// Post a prior-FY (2094-12) AR debit of 50000 (DR 1100 / CR 4000), an invoice finalized before year-end.
	poster := svcAt(pool, mgr, time.Date(2094, 12, 20, 12, 0, 0, 0, time.UTC))
	if _, err := poster.PostExternal(ctx, ExternalJournal{
		Source: SourceAdjustment, IdempotencyKey: fmt.Sprintf("tieout-yb-%d", time.Now().UnixNano()),
		Memo: "prior-FY open AR", Lines: []ExternalLine{
			{AccountCode: "1100", DebitCents: 50000, Memo: "AR"},
			{AccountCode: "4000", CreditCents: 50000, Memo: "revenue"},
		},
	}); err != nil {
		t.Fatalf("post prior-FY AR: %v", err)
	}

	// The inception-to-date as-of 2095 MUST now include the 2094 debit (+50000), the year-boundary carry-fwd.
	glAfter, _, err := repo.AccountBalanceAsOf(ctx, "1100", asOf, "")
	if err != nil {
		t.Fatalf("gl after: %v", err)
	}
	if glAfter-glBefore != 50000 {
		t.Fatalf("inception-to-date GL 1100 must carry the prior-FY 50000; moved by %d", glAfter-glBefore)
	}
	// And the OLD (broken) basis, the CURRENT fiscal year's movement only, must NOT see the 2094 debit,
	// which is exactly why a year-boundary AR read as a false discrepancy before the fix.
	glYearOnly, _, err := repo.AccountBalance(ctx, "1100", 2095, 0, "")
	if err != nil {
		t.Fatalf("gl year-only: %v", err)
	}
	if glYearOnly == glAfter {
		t.Fatal("the fiscal-year-scoped balance should exclude the prior-FY carry-forward (test setup invalid)")
	}

	// In the NEW fiscal year the invoice is still open: subledger inception-to-date AR = glAfter. The tie-out
	// asOf 2095-06 must find the prior-year 1100 carry-forward and report ZERO delta (in tie).
	svc := svcAt(pool, mgr, asOf.Add(12*time.Hour)).WithARSubledger(fixedAR{v: glAfter})
	states, err := svc.TieOut(ctx, asOf, "")
	if err != nil {
		t.Fatalf("tie-out: %v", err)
	}
	if len(states) != 1 {
		t.Fatalf("expected one AR tie-out state, got %+v", states)
	}
	if !states[0].InTie || states[0].DeltaCents != 0 {
		t.Fatalf("a prior-FY open invoice must NOT create a spurious delta; got delta=%d inTie=%v gl=%d sub=%d",
			states[0].DeltaCents, states[0].InTie, states[0].GLCents, states[0].SubledgerCents)
	}
	_, _ = pool.Exec(ctx, `DELETE FROM gl_tieout_state WHERE subledger='AR' AND account_code='1100' AND branch_id IS NULL`)
}

// AC8, the seeded posting rules pass boot validation (every rule resolves to an active account and every
// required role is present, incl. the 1020 doctrine).
func TestIntegration_AC8_ValidatePostingRulesOnSeed(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	svc := NewService(NewRepository(pool))
	if err := svc.ValidatePostingRules(ctx); err != nil {
		t.Fatalf("seeded posting rules must validate at boot: %v", err)
	}
}

// AC9, the GP-parity export carries the PARITY DRAFT banner, records a batch, and renders the posted rows.
func TestIntegration_AC9_GPParityExport(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	ensurePeriods(t, pool, 2094)
	at := time.Date(2094, 5, 10, 12, 0, 0, 0, time.UTC)
	svc := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager}, at)
	if _, err := svc.PostExternal(ctx, ExternalJournal{
		Source: SourceAdjustment, IdempotencyKey: fmt.Sprintf("exp-%d", time.Now().UnixNano()), Memo: "export-src",
		Lines: []ExternalLine{{AccountCode: "1000", DebitCents: 12345}, {AccountCode: "4000", CreditCents: 12345}},
	}); err != nil {
		t.Fatalf("seed export journal: %v", err)
	}
	res, err := svc.Export(ctx, 2094, 5, "")
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if !res.IsDraft || res.Format != "GP_PARITY" {
		t.Fatalf("export should be a GP_PARITY draft, got %+v", res)
	}
	if !strings.Contains(res.Content, "PARITY DRAFT") {
		t.Fatal("export content must carry the PARITY DRAFT banner")
	}
	if !strings.Contains(res.Content, "123.45") {
		t.Fatalf("export content must render the posted amount as dollars; got:\n%s", res.Content)
	}
	if res.BatchID == "" {
		t.Fatal("export must record a batch id")
	}
}

// AC12, recurring /run materializes a DRAFT journal from a template's JSONB lines.
func TestIntegration_AC12_RecurringRun(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	ensurePeriods(t, pool, 2093)
	var tplID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO gl_recurring_templates (name, memo, lines, schedule)
		 VALUES ('Monthly rent accrual', 'rent', $1::jsonb, 'MONTHLY') RETURNING id::text`,
		`[{"account_code":"6100","debit_cents":250000},{"account_code":"2100","credit_cents":250000}]`).Scan(&tplID); err != nil {
		t.Fatalf("insert template: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM gl_recurring_templates WHERE id=$1`, tplID) })

	at := time.Date(2093, 4, 1, 12, 0, 0, 0, time.UTC)
	svc := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager}, at)
	j, err := svc.RunRecurring(ctx, tplID)
	if err != nil {
		t.Fatalf("run recurring: %v", err)
	}
	if j.Status != StatusDraft {
		t.Fatalf("recurring run should materialize a DRAFT, got %s", j.Status)
	}
	if len(j.Lines) != 2 || j.TotalCents() != 250000 {
		t.Fatalf("materialized journal has wrong lines: %+v", j.Lines)
	}
}

// AC13, year-end close sweeps net income into 3900 Retained Earnings; afterwards the income statement nets
// to zero and the whole-year trial balance still balances (BS balances day-1).
func TestIntegration_AC13_YearEndCloseTo3900(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	ensurePeriods(t, pool, 2092)
	at := time.Date(2092, 6, 15, 12, 0, 0, 0, time.UTC)
	svc := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: controllerRole, Tier: identity.TierLeadership, Scope: identity.ScopeGlobal}, at)

	// Post a revenue and an expense so there is a net income to close: revenue 100_000, expense 40_000.
	if _, err := svc.PostExternal(ctx, ExternalJournal{
		Source: SourceAdjustment, IdempotencyKey: fmt.Sprintf("rev-%d", time.Now().UnixNano()),
		Lines: []ExternalLine{{AccountCode: "1000", DebitCents: 100000}, {AccountCode: "4000", CreditCents: 100000}},
	}); err != nil {
		t.Fatalf("post revenue: %v", err)
	}
	if _, err := svc.PostExternal(ctx, ExternalJournal{
		Source: SourceAdjustment, IdempotencyKey: fmt.Sprintf("exp-%d", time.Now().UnixNano()),
		Lines: []ExternalLine{{AccountCode: "6100", DebitCents: 40000}, {AccountCode: "1000", CreditCents: 40000}},
	}); err != nil {
		t.Fatalf("post expense: %v", err)
	}

	res, err := svc.CloseYear(ctx, 2092, "year-end")
	if err != nil {
		t.Fatalf("close year: %v", err)
	}
	if res.NetIncomeCents != 60000 {
		t.Fatalf("net income = %d, want 60000", res.NetIncomeCents)
	}
	if res.ClosingJournalID == "" || !res.PeriodsClosed {
		t.Fatalf("close-year must post a closing journal and seal the periods, got %+v", res)
	}

	// After close: the P&L nets to zero for the year (income statement empty), and 3900 carries the net income.
	var plNet int64
	if err := pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(net_debit_cents),0) FROM v_gl_account_balances
		 WHERE fiscal_year=2092 AND account_type IN ('REVENUE','EXPENSE')`).Scan(&plNet); err != nil {
		t.Fatal(err)
	}
	if plNet != 0 {
		t.Fatalf("P&L must net to zero after year-end close, got %d", plNet)
	}
	re, err := svc.GLBalanceCents(ctx, RetainedEarningsCode, 2092, 0, "")
	if err != nil {
		t.Fatalf("retained earnings balance: %v", err)
	}
	if re != 60000 {
		t.Fatalf("3900 Retained Earnings should carry the 60000 net income, got %d", re)
	}
	// Whole-year trial balance still balances (Σ net debit == 0).
	tb, err := svc.TrialBalance(ctx, 2092, 0, "")
	if err != nil {
		t.Fatalf("trial balance: %v", err)
	}
	if tb.BalanceCents != 0 {
		t.Fatalf("year trial balance must net to zero, got %d", tb.BalanceCents)
	}
}

// A-01 (DIB-30), the manual maker-facing Service.CreateJournal path against the real DB: a balanced entry
// BELOW the seeded $5,000 approval threshold posts immediately (POSTED, no second signer), and the trial
// balance rollup for the isolated year reconciles to the exact legs just posted (15000 DR 1000 / CR 4000),
// cross-checked against v_gl_account_balances via GLBalanceCents. Isolated FY 2094 month 6 (no other test or
// seed row touches 2094-06) so the seeded 2026 ledger cannot perturb the totals.
func TestIntegration_A01_CreateJournalTrialBalanceReconciles(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	ensurePeriods(t, pool, 2094)
	at := time.Date(2094, 6, 15, 12, 0, 0, 0, time.UTC)
	svc := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager}, at)

	// $150 (15000c) is well below the seeded $5,000 (500000c) threshold → posts directly, no maker-checker park.
	j, err := svc.CreateJournal(ctx, CreateJournalInput{
		Memo: "A01 manual JE reconcile",
		Lines: []JournalLineInput{
			{AccountCode: "1000", DebitCents: 15000, Memo: "cash"},
			{AccountCode: "4000", CreditCents: 15000, Memo: "revenue"},
		},
	})
	if err != nil {
		t.Fatalf("create journal: %v", err)
	}
	if j.Status != StatusPosted {
		t.Fatalf("a sub-threshold manual JE must post immediately, got status=%s derived=%s", j.Status, j.DerivedStatus)
	}

	tb, err := svc.TrialBalance(ctx, 2094, 6, "")
	if err != nil {
		t.Fatalf("trial balance: %v", err)
	}
	// (a) Grand totals reconcile to the single posted entry and net to zero (double-entry invariant).
	if tb.TotalDebit != 15000 || tb.TotalCredit != 15000 || tb.BalanceCents != 0 {
		t.Fatalf("TB totals = debit %d credit %d balance %d, want 15000/15000/0", tb.TotalDebit, tb.TotalCredit, tb.BalanceCents)
	}
	// (b)+(c) The per-account rows carry the exact legs: 1000 debit +15000, 4000 credit −15000 (net debit).
	row1000 := tbRow(t, tb, "1000")
	if row1000.DebitCents != 15000 || row1000.BalanceCents != 15000 {
		t.Fatalf("account 1000 row = debit %d balance %d, want 15000/+15000", row1000.DebitCents, row1000.BalanceCents)
	}
	row4000 := tbRow(t, tb, "4000")
	if row4000.CreditCents != 15000 || row4000.BalanceCents != -15000 {
		t.Fatalf("account 4000 row = credit %d balance %d, want 15000/-15000", row4000.CreditCents, row4000.BalanceCents)
	}
	// (d) Independent cross-check against v_gl_account_balances via the provider path.
	bal, err := svc.GLBalanceCents(ctx, "1000", 2094, 6, "")
	if err != nil {
		t.Fatalf("gl balance 1000: %v", err)
	}
	if bal != 15000 {
		t.Fatalf("GLBalanceCents(1000, 2094-06) = %d, want 15000", bal)
	}
}

// tbRow returns the trial-balance row for a code (fails the test if absent).
func tbRow(t *testing.T, tb TrialBalanceResult, code string) TrialBalanceRow {
	t.Helper()
	for _, r := range tb.Rows {
		if r.AccountCode == code {
			return r
		}
	}
	t.Fatalf("trial balance has no row for account %s (rows: %d)", code, len(tb.Rows))
	return TrialBalanceRow{}
}

// A-01 (DIB-30), chart-of-accounts CRUD against the real Repository + Service: a unique test code is created
// and shows up in ListAccounts; a duplicate code and an invalid account_type are both rejected; and the
// DeactivateAccount guard returns the 409 errAccountInUse while a DRAFT journal still references the account,
// then succeeds once nothing DRAFT references it. Reconciles to the seeded gl_accounts constraints (unique
// code, account_type/normal_balance checks) and the DraftLineCountForAccountCode guard. Isolated code 9911
// (outside the seeded chart) + FY 2091, fully cleaned up.
func TestIntegration_A01_ChartOfAccountsCRUD(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	const code = "9911"

	// Defensive pre-clean + registered teardown: drop any lines referencing the test account, then the account
	// itself. Registered BEFORE ensurePeriods so (LIFO) the period cleanup wipes the FY-2091 draft's lines first,
	// leaving this to remove any stragglers and the account, FK-safe either way.
	cleanAcct := func() {
		_, _ = pool.Exec(ctx, `DELETE FROM gl_journal_lines l USING gl_accounts a WHERE l.account_id = a.id AND a.code = $1`, code)
		_, _ = pool.Exec(ctx, `DELETE FROM gl_accounts WHERE code = $1`, code)
	}
	cleanAcct()
	t.Cleanup(cleanAcct)

	ensurePeriods(t, pool, 2091)

	repo := NewRepository(pool)
	at := time.Date(2091, 6, 15, 12, 0, 0, 0, time.UTC)
	svc := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager}, at)

	// Create the test account and confirm it lands in ListAccounts with the values we set.
	acct, err := svc.CreateAccount(ctx, CreateAccountInput{Code: code, Name: "A01 Test Expense", AccountType: "EXPENSE", NormalBalance: NormalDebit})
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	if acct.Code != code || acct.AccountType != "EXPENSE" || acct.NormalBalance != NormalDebit || !acct.IsActive {
		t.Fatalf("created account = %+v, want code=%s EXPENSE/DEBIT active", acct, code)
	}
	accounts, err := svc.ListAccounts(ctx)
	if err != nil {
		t.Fatalf("list accounts: %v", err)
	}
	if !containsAccount(accounts, code) {
		t.Fatalf("ListAccounts must include the freshly-created %s", code)
	}

	// A duplicate code is rejected (unique constraint → 409).
	if _, err := svc.CreateAccount(ctx, CreateAccountInput{Code: code, Name: "dupe", AccountType: "EXPENSE", NormalBalance: NormalDebit}); err == nil {
		t.Fatal("creating a duplicate account code must fail")
	}
	// An invalid account_type is rejected at the service layer (no DB write).
	if _, err := svc.CreateAccount(ctx, CreateAccountInput{Code: "9912", Name: "bad type", AccountType: "BOGUS", NormalBalance: NormalDebit}); err == nil {
		t.Fatal("an invalid account_type must be rejected at the service layer")
	}

	// Park a DRAFT journal referencing 9911: $6,000 (600000c) is at/above the seeded $5,000 threshold, so the
	// manual create parks as DRAFT (maker-checker) rather than posting, exactly the state the guard protects.
	dj, err := svc.CreateJournal(ctx, CreateJournalInput{
		Memo: "A01 draft referencing test account",
		Lines: []JournalLineInput{
			{AccountCode: code, DebitCents: 600000, Memo: "expense"},
			{AccountCode: "4000", CreditCents: 600000, Memo: "revenue"},
		},
	})
	if err != nil {
		t.Fatalf("create draft journal: %v", err)
	}
	if dj.Status != StatusDraft {
		t.Fatalf("an at/above-threshold manual JE must park as DRAFT, got %s", dj.Status)
	}

	// While the DRAFT references it, deactivation is refused with the 409 errAccountInUse.
	if _, err := svc.DeactivateAccount(ctx, code); !errors.Is(err, errAccountInUse) {
		t.Fatalf("deactivating an account referenced by a DRAFT journal must return errAccountInUse, got %v", err)
	}

	// Void the DRAFT (DRAFT → VOID); no DRAFT now references the account, so deactivation succeeds.
	if _, err := svc.VoidJournal(ctx, dj.ID); err != nil {
		t.Fatalf("void draft: %v", err)
	}
	deact, err := svc.DeactivateAccount(ctx, code)
	if err != nil {
		t.Fatalf("deactivate account once no DRAFT references it: %v", err)
	}
	if deact.IsActive {
		t.Fatalf("account %s must be inactive after DeactivateAccount, got active", code)
	}
	// Cross-check the persisted flag directly.
	if a, found, err := repo.AccountByCode(ctx, code); err != nil || !found || a.IsActive {
		t.Fatalf("AccountByCode(%s) after deactivate: found=%v active=%v err=%v (want found, inactive)", code, found, a.IsActive, err)
	}
}

// containsAccount reports whether the chart contains an account with the given code.
func containsAccount(accounts []Account, code string) bool {
	for _, a := range accounts {
		if a.Code == code {
			return true
		}
	}
	return false
}

// fixedAR is a settable ARSubledger for the tie-out test.
type fixedAR struct{ v int64 }

func (f fixedAR) ARBalanceCents(_ context.Context, _ time.Time, _ string) (int64, error) {
	return f.v, nil
}
