//go:build integration

// fw1_09c_year_end_close_integration_test.go, FW1-09c's red first legs on the real
// migrated and seeded database (FROM-PRINCIPAL 222): the year end close is the
// Financial Controller's, so a GM's close is refused in words BEFORE any write; and
// the Controller's close leaves exactly one GL_YEAR_END_CLOSE audit row inside the
// close's own transaction, keyed to the closed year's January period id, so a refused
// audit leaves no close behind.
//
// ISOLATION. Fiscal years 3080, 3081 and 3082, outside the seeded 2026 periods and
// every band the package's other integration tests use.
//
//	Run: DATABASE_URL='postgres://.../db?sslmode=disable' go test -tags=integration -count=1 -run FW1_09c ./internal/gl/
package gl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5/pgxpool"
)

// fw1_09cPL posts one revenue and one expense into the year, so a close that IS
// allowed to run has a net income to sweep and a closing journal to write. The
// document dates file the postings into the year's own periods.
func fw1_09cPL(t *testing.T, svc *Service, year int) {
	t.Helper()
	ctx := context.Background()
	if _, err := svc.PostExternal(ctx, ExternalJournal{
		Source: SourceAdjustment, IdempotencyKey: fmt.Sprintf("fw1-09c-rev-%d", rand.Uint64()),
		DocumentDate: time.Date(year, 6, 10, 12, 0, 0, 0, time.UTC),
		Lines:        []ExternalLine{{AccountCode: "1000", DebitCents: 100000}, {AccountCode: "4000", CreditCents: 100000}},
	}); err != nil {
		t.Fatalf("post the year's revenue: %v", err)
	}
	if _, err := svc.PostExternal(ctx, ExternalJournal{
		Source: SourceAdjustment, IdempotencyKey: fmt.Sprintf("fw1-09c-exp-%d", rand.Uint64()),
		DocumentDate: time.Date(year, 6, 20, 12, 0, 0, 0, time.UTC),
		Lines:        []ExternalLine{{AccountCode: "6100", DebitCents: 40000}, {AccountCode: "1000", CreditCents: 40000}},
	}); err != nil {
		t.Fatalf("post the year's expense: %v", err)
	}
}

// fw1_09cWrites reads what the year's close left behind: the closing journals, the
// override rows, the sealed periods and the audit rows keyed to the year's own
// January period (the row's entity, so one leg never counts another's).
func fw1_09cWrites(t *testing.T, pool *pgxpool.Pool, year int, january string) (closings, overrides, sealed, audits int) {
	t.Helper()
	ctx := context.Background()
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM gl_journals WHERE fiscal_year=$1 AND source='CLOSING'`, year).Scan(&closings); err != nil {
		t.Fatalf("read the closing journals: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM gl_period_overrides o JOIN fiscal_periods p ON p.id=o.period_id WHERE p.year=$1`, year).Scan(&overrides); err != nil {
		t.Fatalf("read the override rows: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM fiscal_periods WHERE year=$1 AND status='CLOSED'`, year).Scan(&sealed); err != nil {
		t.Fatalf("read the sealed periods: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE action='GL_YEAR_END_CLOSE' AND entity_id=$1::uuid`, january).Scan(&audits); err != nil {
		t.Fatalf("read the year end audit rows: %v", err)
	}
	return closings, overrides, sealed, audits
}

// fw1_09cDropAudit removes a leg's audit rows on cleanup, so the shared scratch
// database carries no trail of these legs into another package's run.
func fw1_09cDropAudit(t *testing.T, pool *pgxpool.Pool, january string) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM audit_log WHERE action='GL_YEAR_END_CLOSE' AND entity_id=$1::uuid`, january)
	})
}

// TestIntegration_FW1_09c_GMYearEndCloseIsRefusedAndWritesNothing is item 1's
// red: the close-year route is RequireAdmin, so a GM (Tier 1, GLOBAL) reaches
// CloseYear today and seals the year. The year end close is the Controller's, the
// way the period reopen and the early open are: the refusal comes in words, before
// any write, so nothing of the year moves.
func TestIntegration_FW1_09c_GMYearEndCloseIsRefusedAndWritesNothing(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	const year = 3080
	ensurePeriods(t, pool, year)
	// The clock stands AFTER the year's last day, so the refusal can only be the role
	// rule: the year has ended and the pre flight is quiet on a year with no activity.
	gm := svcAt(pool, identity.StaffContext{
		UserID: user, RoleCode: "GENERAL_MANAGER", Tier: identity.TierLeadership, Scope: identity.ScopeGlobal,
	}, time.Date(year+1, 1, 5, 12, 0, 0, 0, time.UTC))
	fw1_09cPL(t, gm, year)

	_, err := gm.CloseYear(ctx, year, "FW1-09c the GM's year end close")
	if err == nil {
		t.Fatalf("the GM's year end close succeeded; the year end close is the Financial Controller's")
	}
	var ce clientError
	if !errors.As(err, &ce) || ce.status != http.StatusForbidden {
		t.Fatalf("the GM's year end close answered %v, want a 403 in words", err)
	}
	if !strings.Contains(err.Error(), "Financial Controller") {
		t.Fatalf("the refusal does not name the Financial Controller: %v", err)
	}
	// The refusal comes before any write: no closing journal, no override row, no sealed
	// period, no audit row.
	january := periodID(t, pool, year, 1)
	closings, overrides, sealed, audits := fw1_09cWrites(t, pool, year, january)
	if closings != 0 || overrides != 0 || sealed != 0 || audits != 0 {
		t.Fatalf("the refused GM close wrote anyway: %d closing journals, %d override rows, %d sealed periods, %d audit rows; a refused close writes nothing",
			closings, overrides, sealed, audits)
	}
}

// TestIntegration_FW1_09c_YearEndCloseLeavesOneAuditRowThroughTheRealRecorder is
// item 2's red: the Controller's close records GL_YEAR_END_CLOSE inside the close's
// own transaction, through the REAL recorder (audit.NewService, as wire_money.go
// wires it), keyed to the closed year's January period id (audit_log.entity_id is a
// uuid column), with the year and the reason riding the row.
func TestIntegration_FW1_09c_YearEndCloseLeavesOneAuditRowThroughTheRealRecorder(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	const year = 3081
	ensurePeriods(t, pool, year)
	recorder := audit.NewService(audit.NewRepository(pool), nil)
	controller := svcAt(pool, identity.StaffContext{
		UserID: user, RoleCode: controllerRole, Tier: identity.TierLeadership, Scope: identity.ScopeGlobal,
	}, time.Date(year+1, 1, 5, 12, 0, 0, 0, time.UTC)).WithAuditTxRecorder(recorder)
	fw1_09cPL(t, controller, year)

	const reason = "FW1-09c year end close through the real recorder"
	res, err := controller.CloseYear(ctx, year, reason)
	if err != nil {
		t.Fatalf("the year end close through the real recorder: %v", err)
	}
	if res.ClosingJournalID == "" || !res.PeriodsClosed {
		t.Fatalf("the year end must post a closing journal and seal the periods, got %+v", res)
	}

	// The row is keyed to the closed year's January period (a uuid column, so not the year).
	january := periodID(t, pool, year, 1)
	fw1_09cDropAudit(t, pool, january)

	var rows []struct {
		entityID string
		changes  []byte
	}
	collected, err := pool.Query(ctx,
		`SELECT COALESCE(entity_id::text,''), changes FROM audit_log
		  WHERE action='GL_YEAR_END_CLOSE' AND entity_id=$1::uuid ORDER BY created_at`, january)
	if err != nil {
		t.Fatalf("read the year end audit rows: %v", err)
	}
	defer collected.Close()
	for collected.Next() {
		var r struct {
			entityID string
			changes  []byte
		}
		if err := collected.Scan(&r.entityID, &r.changes); err != nil {
			t.Fatalf("scan the year end audit row: %v", err)
		}
		rows = append(rows, r)
	}
	if err := collected.Err(); err != nil {
		t.Fatalf("read the year end audit rows: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("the year end close left %d GL_YEAR_END_CLOSE audit rows, want exactly 1", len(rows))
	}
	// The row is keyed to the closed year's January period (a uuid column, so not the year).
	if rows[0].entityID != january {
		t.Fatalf("the audit names %q, want the closed year's January period id %q", rows[0].entityID, january)
	}
	// The year and the reason ride the row.
	var changes map[string]any
	if err := json.Unmarshal(rows[0].changes, &changes); err != nil {
		t.Fatalf("read the audit row's changes %s: %v", rows[0].changes, err)
	}
	if got, ok := changes["fiscal_year"].(float64); !ok || int(got) != year {
		t.Fatalf("the audit row's changes carry fiscal_year %v, want %d (%s)", changes["fiscal_year"], year, rows[0].changes)
	}
	if got, _ := changes["reason"].(string); got != reason {
		t.Fatalf("the audit row's changes carry reason %q, want %q (%s)", got, reason, rows[0].changes)
	}
}

// TestIntegration_FW1_09c_YearEndAuditFailureRollsTheCloseBack is item 2's second
// red: the audit row rides the close's OWN transaction, so a recorder that refuses
// holds the close: no closing journal, no sealed period, no override row. The open
// year's rule (FW1-01 round 2), the close's rule now.
func TestIntegration_FW1_09c_YearEndAuditFailureRollsTheCloseBack(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	const year = 3082
	ensurePeriods(t, pool, year)
	controller := svcAt(pool, identity.StaffContext{
		UserID: user, RoleCode: controllerRole, Tier: identity.TierLeadership, Scope: identity.ScopeGlobal,
	}, time.Date(year+1, 1, 5, 12, 0, 0, 0, time.UTC)).WithAuditTxRecorder(&fw1AuditTx{fail: true})
	fw1_09cPL(t, controller, year)

	if _, err := controller.CloseYear(ctx, year, "FW1-09c the refusing recorder"); err == nil {
		t.Fatalf("the year end close succeeded with the audit write refused; the audit must hold the close")
	}
	closings, overrides, sealed, audits := fw1_09cWrites(t, pool, year, periodID(t, pool, year, 1))
	if closings != 0 || overrides != 0 || sealed != 0 {
		t.Fatalf("the refused audit left the close behind: %d closing journals, %d override rows, %d sealed periods; the close rolls back with its audit",
			closings, overrides, sealed)
	}
	if audits != 0 {
		t.Fatalf("the refused audit left %d audit rows behind", audits)
	}
}
