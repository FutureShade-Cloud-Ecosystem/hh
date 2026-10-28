//go:build integration

// fw1_09g_period_close_audit_integration_test.go, FW1-09g's red first legs on the real
// migrated and seeded database: the PERIOD close's own audit row (S2's P3 on 614).
//
// The period close is the routine monthly act, and today it leaves no trail of its own: it
// seals the period and writes the gl_period_overrides row, and that is all. The year end
// close writes its GL_YEAR_END_CLOSE row inside its own transaction, so a refused audit
// holds the close; the period close has no such row, so nothing holds it and the seal is
// the only trace that a month was closed and by whom.
//
// The row's shape: action GL_PERIOD_CLOSE, entity gl.period, keyed to the period id, the
// actor the caller, the reason the top level Reason (a reason is FREE TEXT a person types,
// so it never rides Extra), and the year in Extra (the audit boundary's Extra path is
// deny-by-default, and "fiscal_year" is on its allowlist). The month does NOT ride Extra:
// the allowlist does not admit it, and the rule is not to widen the allowlist for it.
//
// ISOLATION. Fiscal years 3204 and 3205, outside the seeded 2026 periods and every band the
// package's other integration tests use.
//
//	Run: DATABASE_URL='postgres://.../db?sslmode=disable' go test -tags=integration -count=1 -run FW1_09g ./internal/gl/
package gl

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5/pgxpool"
)

// fw1_09gPeriodAudit reads the audit rows the close wrote for the given period, the way the
// trail reads them: by the action and by the period the row is keyed to.
func fw1_09gPeriodAudit(t *testing.T, pool *pgxpool.Pool, periodID, action string) []struct {
	entityType string
	entityID   string
	actorID    string
	changes    []byte
} {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT entity_type, entity_id::text, COALESCE(actor_id::text,''), changes
		   FROM audit_log WHERE action=$2 AND entity_id=$1::uuid ORDER BY created_at`, periodID, action)
	if err != nil {
		t.Fatalf("read the %s audit rows for the period: %v", action, err)
	}
	defer rows.Close()
	var out []struct {
		entityType string
		entityID   string
		actorID    string
		changes    []byte
	}
	for rows.Next() {
		var r struct {
			entityType string
			entityID   string
			actorID    string
			changes    []byte
		}
		if err := rows.Scan(&r.entityType, &r.entityID, &r.actorID, &r.changes); err != nil {
			t.Fatalf("scan the %s audit row: %v", action, err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read the %s audit rows for the period: %v", action, err)
	}
	return out
}

// TestIntegration_FW1_09g_PeriodCloseLeavesItsAuditRowThroughTheRealRecorder is S2's P3
// on 614, as a red: the period close writes NO audit row today, so the month was sealed
// and nobody can say who sealed it or why. The close writes GL_PERIOD_CLOSE inside its OWN
// transaction, through the REAL recorder (audit.NewService, as wire_money.go wires it).
func TestIntegration_FW1_09g_PeriodCloseLeavesItsAuditRowThroughTheRealRecorder(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	const year = 3204
	ensurePeriods(t, pool, year)
	fw1_09gDropAuditRows(t, pool, year)
	recorder := audit.NewService(audit.NewRepository(pool), nil)
	manager := svcAt(pool, identity.StaffContext{
		UserID: user, RoleCode: "GENERAL_MANAGER", Tier: identity.TierLeadership, Scope: identity.ScopeGlobal,
	}, time.Date(year, 6, 30, 12, 0, 0, 0, time.UTC)).WithAuditTxRecorder(recorder)

	id := periodID(t, pool, year, 3)
	const reason = "FW1-09g the June close"
	closed, err := manager.ClosePeriod(ctx, id, reason)
	if err != nil {
		t.Fatalf("the period close: %v", err)
	}
	if closed.Status != "CLOSED" {
		t.Fatalf("the period close answered %q, want CLOSED", closed.Status)
	}

	rows := fw1_09gPeriodAudit(t, pool, id, "GL_PERIOD_CLOSE")
	if len(rows) != 1 {
		t.Fatalf("the period close wrote %d GL_PERIOD_CLOSE rows, want exactly 1 (the close is an act and it leaves a record)", len(rows))
	}
	if rows[0].entityType != "gl.period" {
		t.Fatalf("the row names entity_type %q, want gl.period", rows[0].entityType)
	}
	if rows[0].entityID != id {
		t.Fatalf("the row is keyed to %q, want the closed period %q", rows[0].entityID, id)
	}
	if rows[0].actorID != user {
		t.Fatalf("the row names actor %q, want the caller %q", rows[0].actorID, user)
	}
	// The year rides Extra (the allowlist admits it) and the reason rides the top level
	// Reason field (a reason is free text and never rides Extra).
	var changes map[string]any
	if err := json.Unmarshal(rows[0].changes, &changes); err != nil {
		t.Fatalf("read the row's changes %s: %v", rows[0].changes, err)
	}
	if got, ok := changes["fiscal_year"].(float64); !ok || int(got) != year {
		t.Fatalf("the row's changes carry fiscal_year %v, want %d (%s)", changes["fiscal_year"], year, rows[0].changes)
	}
	if got, _ := changes["reason"].(string); got != reason {
		t.Fatalf("the row's changes carry reason %q, want %q (%s)", got, reason, rows[0].changes)
	}
	// The month does NOT ride Extra: the allowlist does not admit it and the rule is not
	// to widen the allowlist for it.
	if _, present := changes["month"]; present {
		t.Fatalf("the row's changes carry a month key (%s): the Extra allowlist does not admit it and must not be widened for it", rows[0].changes)
	}
}

// TestIntegration_FW1_09g_ARefusedPeriodAuditRollsTheCloseBack is S2's P3 on 614's second
// half: the row rides the close's OWN transaction, so a recorder that refuses holds the
// close. The year end close's rule (FW1-09c), the period close's rule now.
func TestIntegration_FW1_09g_ARefusedPeriodAuditRollsTheCloseBack(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	const year = 3205
	ensurePeriods(t, pool, year)
	fw1_09gDropAuditRows(t, pool, year)
	manager := svcAt(pool, identity.StaffContext{
		UserID: user, RoleCode: "GENERAL_MANAGER", Tier: identity.TierLeadership, Scope: identity.ScopeGlobal,
	}, time.Date(year, 6, 30, 12, 0, 0, 0, time.UTC)).WithAuditTxRecorder(&fw1AuditTx{fail: true})

	id := periodID(t, pool, year, 4)
	if _, err := manager.ClosePeriod(ctx, id, "FW1-09g the refusing recorder"); err == nil {
		t.Fatalf("the period close succeeded with the audit write refused; the audit must hold the close")
	}
	// Nothing of the close survives: the period is OPEN, there is no override row and no
	// audit row.
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM fiscal_periods WHERE id=$1::uuid`, id).Scan(&status); err != nil {
		t.Fatalf("read the period: %v", err)
	}
	if status != "OPEN" {
		t.Fatalf("the refused audit left the period %s; the close rolls back with its audit", status)
	}
	var overrides int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM gl_period_overrides WHERE period_id=$1::uuid`, id).Scan(&overrides); err != nil {
		t.Fatalf("read the override rows: %v", err)
	}
	if overrides != 0 {
		t.Fatalf("the refused audit left %d override rows behind", overrides)
	}
	if rows := fw1_09gPeriodAudit(t, pool, id, "GL_PERIOD_CLOSE"); len(rows) != 0 {
		t.Fatalf("the refused audit left %d audit rows behind", len(rows))
	}
}
