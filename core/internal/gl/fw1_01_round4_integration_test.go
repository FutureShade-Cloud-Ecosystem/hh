//go:build integration

// fw1_01_round4_integration_test.go, PR 614 round 4's legs (S2's round 3
// read): every audited gl action driven through the REAL audit recorder
// (audit.NewService writing audit_log, the way wire_money wires production),
// where every earlier leg used a fake, so the one defect between the fake and
// production was invisible. The open year's audit named "fiscal_year:2028" in
// audit_log.entity_id, a uuid column (0013), so through the real recorder the
// insert failed with "invalid input syntax for type uuid" and the year's
// transaction rolled back: in production the Controller could never open a
// year.
package gl

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
	"github.com/jackc/pgx/v5/pgxpool"
)

// fw1R4Controller builds a Financial Controller's service at the round's pinned clock in 2026.
func fw1R4Controller(pool *pgxpool.Pool, user string) *Service {
	return svcAt(pool, identity.StaffContext{UserID: user, RoleCode: controllerRole, Tier: identity.TierLeadership, Scope: identity.ScopeGlobal}, time.Date(2026, 6, 15, 12, 0, 0, 0, houseday.DefaultZone().Location()))
}

// TestIntegration_FW1_01R4_OpenYearAuditRowThroughTheRealRecorder is the
// round 4 P1 red: the Controller's open through the real recorder must
// succeed AND leave exactly one GL_FISCAL_YEAR_OPEN row in audit_log, keyed
// to an entity id that is a uuid.
func TestIntegration_FW1_01R4_OpenYearAuditRowThroughTheRealRecorder(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	fw1EnsureHouseYear(t, pool, 2026)
	fw1ScrubYear(t, pool, 2027, 2028)
	t.Cleanup(func() { fw1ScrubYear(t, pool, 2027, 2028) })
	fw1PlantFuturePeriod(t, pool, 2109)
	// The REAL recorder, not a fake: the whole defect lives between the two.
	recorder := audit.NewService(audit.NewRepository(pool), nil)
	controller := fw1R4Controller(pool, user).WithAuditTxRecorder(recorder)

	var before int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action = 'GL_FISCAL_YEAR_OPEN'`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	res, err := controller.OpenNextFiscalYear(ctx, "FW1-01 round 4 real recorder proof")
	if err != nil {
		t.Fatalf("the Controller's open through the real audit recorder: %v", err)
	}
	if res.FiscalYear != 2028 {
		t.Fatalf("the open answered %d, want 2028", res.FiscalYear)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action = 'GL_FISCAL_YEAR_OPEN'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != before+1 {
		t.Fatalf("audit_log holds %d GL_FISCAL_YEAR_OPEN rows after the open (before %d), want exactly one more", n, before)
	}
	var entityID, yearWords, actor string
	if err := pool.QueryRow(ctx,
		`SELECT COALESCE(entity_id::text,''), COALESCE(changes->>'fiscal_year',''), COALESCE(actor_id::text,'')
		 FROM audit_log WHERE action = 'GL_FISCAL_YEAR_OPEN' ORDER BY seq DESC LIMIT 1`).Scan(&entityID, &yearWords, &actor); err != nil {
		t.Fatal(err)
	}
	var januaryID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM fiscal_periods WHERE year = 2028 AND month = 1`).Scan(&januaryID); err != nil {
		t.Fatalf("read the opened year's January period: %v", err)
	}
	if entityID != januaryID {
		t.Fatalf("the audit's entity_id is %q, want the opened year's January period id %q (audit_log.entity_id is a uuid column)", entityID, januaryID)
	}
	if yearWords != "2028" {
		t.Fatalf("the audit's changes carry fiscal_year %q, want 2028 (the year rides Extra once the entity is the period)", yearWords)
	}
	if actor != user {
		t.Fatalf("the audit's actor is %q, want the Controller %q", actor, user)
	}
}

// TestIntegration_FW1_01R4_PeriodCloseLeavesItsOverrideRow is the period
// close leg through the real recorder: the close's audit trail is the
// gl_period_overrides row (CLOSE, the actor, the reason), which the store
// writes inside the close's transaction. The close writes no audit_log row by
// design (the reopen is the privileged, reason-bearing act that does); the
// recorder is wired anyway, the way production wires it.
func TestIntegration_FW1_01R4_PeriodCloseLeavesItsOverrideRow(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	fw1ScrubYear(t, pool, 2027)
	fw1EnsureHouseYear(t, pool, 2027)
	t.Cleanup(func() { fw1ScrubYear(t, pool, 2027) })
	recorder := audit.NewService(audit.NewRepository(pool), nil)
	controller := fw1R4Controller(pool, user).WithAudit(recorder)

	var periodID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM fiscal_periods WHERE year = 2027 AND month = 2`).Scan(&periodID); err != nil {
		t.Fatal(err)
	}
	var before int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM gl_period_overrides WHERE period_id = $1`, periodID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.ClosePeriod(ctx, periodID, "FW1-01 round 4 close through the real recorder"); err != nil {
		t.Fatalf("the period close through the real audit recorder: %v", err)
	}
	var n int
	var action, actor, reason string
	if err := pool.QueryRow(ctx,
		`SELECT count(*), COALESCE(max(action),''), COALESCE(max(actor_id::text),''), COALESCE(max(reason),'')
		 FROM gl_period_overrides WHERE period_id = $1`, periodID).Scan(&n, &action, &actor, &reason); err != nil {
		t.Fatal(err)
	}
	if n != before+1 {
		t.Fatalf("the close left %d override rows (before %d), want exactly one more", n, before)
	}
	if action != "CLOSE" {
		t.Fatalf("the close's override action is %q, want CLOSE", action)
	}
	if actor != user {
		t.Fatalf("the close's override actor is %q, want the caller %q", actor, user)
	}
	if reason != "FW1-01 round 4 close through the real recorder" {
		t.Fatalf("the close's override reason is %q, want the close's reason", reason)
	}
}

// TestIntegration_FW1_01R4_ReopenAuditRowThroughTheRealRecorder is the reopen
// leg through the real recorder: the reopen is the privileged act whose
// recorded compensating control is the GL_PERIOD_REOPEN sensitive-access row,
// keyed to the period's uuid, beside the override row.
func TestIntegration_FW1_01R4_ReopenAuditRowThroughTheRealRecorder(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	fw1ScrubYear(t, pool, 2027)
	fw1EnsureHouseYear(t, pool, 2027)
	t.Cleanup(func() { fw1ScrubYear(t, pool, 2027) })
	recorder := audit.NewService(audit.NewRepository(pool), nil)
	controller := fw1R4Controller(pool, user).WithAudit(recorder)

	var periodID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM fiscal_periods WHERE year = 2027 AND month = 3`).Scan(&periodID); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.ClosePeriod(ctx, periodID, "round 4 reopen setup"); err != nil {
		t.Fatalf("setup close: %v", err)
	}
	var before int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action = 'GL_PERIOD_REOPEN'`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.ReopenPeriod(ctx, periodID, "FW1-01 round 4 reopen through the real recorder"); err != nil {
		t.Fatalf("the reopen through the real audit recorder: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action = 'GL_PERIOD_REOPEN'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != before+1 {
		t.Fatalf("audit_log holds %d GL_PERIOD_REOPEN rows after the reopen (before %d), want exactly one more", n, before)
	}
	var entityID, actor string
	if err := pool.QueryRow(ctx,
		`SELECT COALESCE(entity_id::text,''), COALESCE(actor_id::text,'')
		 FROM audit_log WHERE action = 'GL_PERIOD_REOPEN' ORDER BY seq DESC LIMIT 1`).Scan(&entityID, &actor); err != nil {
		t.Fatal(err)
	}
	if entityID != periodID {
		t.Fatalf("the reopen audit's entity_id is %q, want the period %q", entityID, periodID)
	}
	if actor != user {
		t.Fatalf("the reopen audit's actor is %q, want the Controller %q", actor, user)
	}
	var overrides int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM gl_period_overrides WHERE period_id = $1 AND action = 'REOPEN'`, periodID).Scan(&overrides); err != nil {
		t.Fatal(err)
	}
	if overrides != 1 {
		t.Fatalf("the reopen left %d REOPEN override rows, want 1", overrides)
	}
}

// TestIntegration_FW1_01R4_YearEndLeavesItsOverrideRows is the year end leg
// through the real recorder: the close's audit trail is one CLOSE override
// row per sealed period (the year-end close seals them through
// ClosePeriodsForYear), beside the closing journal.
func TestIntegration_FW1_01R4_YearEndLeavesItsOverrideRows(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	ensurePeriods(t, pool, 2091)
	recorder := audit.NewService(audit.NewRepository(pool), nil)
	// The leg's clock stands inside 2091, so the seeded P&L journals file into 2091 (a posting files
	// by the clock's own period, the AC13 seed's rule).
	controller := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: controllerRole, Tier: identity.TierLeadership, Scope: identity.ScopeGlobal}, time.Date(2091, 6, 15, 12, 0, 0, 0, time.UTC)).WithAudit(recorder)

	// Post a revenue and an expense so the year end has a net income to sweep (the AC13 seed).
	if _, err := controller.PostExternal(ctx, ExternalJournal{
		Source: SourceAdjustment, IdempotencyKey: fmt.Sprintf("r4-rev-%d", time.Now().UnixNano()),
		Lines: []ExternalLine{{AccountCode: "1000", DebitCents: 100000}, {AccountCode: "4000", CreditCents: 100000}},
	}); err != nil {
		t.Fatalf("post revenue: %v", err)
	}
	if _, err := controller.PostExternal(ctx, ExternalJournal{
		Source: SourceAdjustment, IdempotencyKey: fmt.Sprintf("r4-exp-%d", time.Now().UnixNano()),
		Lines: []ExternalLine{{AccountCode: "6100", DebitCents: 40000}, {AccountCode: "1000", CreditCents: 40000}},
	}); err != nil {
		t.Fatalf("post expense: %v", err)
	}

	res, err := controller.CloseYear(ctx, 2091, "FW1-01 round 4 year end through the real recorder")
	if err != nil {
		t.Fatalf("the year end through the real audit recorder: %v", err)
	}
	if res.ClosingJournalID == "" || !res.PeriodsClosed {
		t.Fatalf("the year end must post a closing journal and seal the periods, got %+v", res)
	}
	var periods, overrides int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM fiscal_periods WHERE year = 2091 AND status = 'CLOSED'`).Scan(&periods); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM gl_period_overrides o JOIN fiscal_periods p ON p.id = o.period_id
		 WHERE p.year = 2091 AND o.action = 'CLOSE'`).Scan(&overrides); err != nil {
		t.Fatal(err)
	}
	if periods == 0 {
		t.Fatal("the year end sealed no period of 2091")
	}
	if overrides != periods {
		t.Fatalf("the year end left %d CLOSE override rows for %d sealed periods, want one per sealed period", overrides, periods)
	}
}

// TestIntegration_FW1_01R4_HeadOfficeSetAuditRowThroughTheRealRecorder is the
// head office set leg through the real recorder: the Controller's change
// stamps GL_HEAD_OFFICE_BRANCH_SET inside the policy write's transaction,
// keyed to the branch's uuid.
func TestIntegration_FW1_01R4_HeadOfficeSetAuditRowThroughTheRealRecorder(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	recorder := audit.NewService(audit.NewRepository(pool), nil)
	controller := fw1R4Controller(pool, user).WithAuditTxRecorder(recorder)

	var branchID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations LIMIT 1`).Scan(&branchID); err != nil {
		t.Skipf("no seeded branch: %v", err)
	}
	var before int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action = 'GL_HEAD_OFFICE_BRANCH_SET'`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := controller.SetHeadOfficeBranch(ctx, branchID, "FW1-01 round 4 head office set through the real recorder"); err != nil {
		t.Fatalf("the head office set through the real audit recorder: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `UPDATE gl_policy SET head_office_branch_id = NULL WHERE branch_id IS NULL`)
	})

	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action = 'GL_HEAD_OFFICE_BRANCH_SET'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != before+1 {
		t.Fatalf("audit_log holds %d GL_HEAD_OFFICE_BRANCH_SET rows after the set (before %d), want exactly one more", n, before)
	}
	var entityID, actor string
	if err := pool.QueryRow(ctx,
		`SELECT COALESCE(entity_id::text,''), COALESCE(actor_id::text,'')
		 FROM audit_log WHERE action = 'GL_HEAD_OFFICE_BRANCH_SET' ORDER BY seq DESC LIMIT 1`).Scan(&entityID, &actor); err != nil {
		t.Fatal(err)
	}
	if entityID != branchID {
		t.Fatalf("the head office audit's entity_id is %q, want the branch %q", entityID, branchID)
	}
	if actor != user {
		t.Fatalf("the head office audit's actor is %q, want the Controller %q", actor, user)
	}
}
