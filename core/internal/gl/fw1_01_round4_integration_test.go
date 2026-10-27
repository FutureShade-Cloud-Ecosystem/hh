//go:build integration

// fw1_01_round4_integration_test.go, PR 614 round 4's red first leg (S2's
// round 3 read, the P1): every earlier leg recorded the open year's audit
// through a fake, so the one defect between the fake and production was
// invisible. Through the REAL recorder (audit.NewService writing audit_log,
// the way wire_money wires the box) the open audits "fiscal_year:2028" into
// audit_log.entity_id, a uuid column (0013), the insert fails with "invalid
// input syntax for type uuid", and the year's transaction rolls back: in
// production the Controller can never open a year.
package gl

import (
	"context"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
)

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
	controller := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: controllerRole, Tier: identity.TierLeadership, Scope: identity.ScopeGlobal}, time.Date(2026, 6, 15, 12, 0, 0, 0, houseday.DefaultZone().Location())).
		WithAuditTxRecorder(recorder)

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
	if n != 1 {
		t.Fatalf("audit_log holds %d GL_FISCAL_YEAR_OPEN rows after the open, want 1", n)
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
