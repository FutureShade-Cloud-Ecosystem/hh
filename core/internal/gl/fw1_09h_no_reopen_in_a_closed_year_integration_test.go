//go:build integration

// fw1_09h_no_reopen_in_a_closed_year_integration_test.go, FW1-09h's red first legs on the
// real migrated and seeded database: a month of a CLOSED fiscal year cannot be reopened
// (FROM-PRINCIPAL 350, S2's P3 1 ruled).
//
// The year end close is an act and it leaves a marker (gl_year_end_closes, migration 0452).
// A closed year's P&L never moves without a new close, so the reopen of one of its months
// is refused in words: "fiscal year N is closed; post the correction in the open year", 409,
// before any write and before the audit row.
//
// The legs:
//  1. the red: a year closed through CloseYear (so the marker is written) refuses the reopen
//     of one of its months, and the month stays sealed and no audit row is written;
//  2. a month of a year that carries NO marker still reopens, exactly as before;
//  3. the route answers 409 with the core's own words.
//
// ISOLATION. Fiscal years 3216, 3217 and 3218, outside the seeded 2026 periods and every
// band the package's other integration tests use.
//
//	Run: DATABASE_URL='postgres://.../db?sslmode=disable' go test -tags=integration -count=1 -run FW1_09h ./internal/gl/
package gl

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"github.com/jackc/pgx/v5/pgxpool"
)

// fw1_09hDropReopenAuditRows removes this leg's own reopen audit rows on cleanup, so the
// shared scratch database carries no trail of these legs into another package's run. It
// runs BEFORE ensurePeriods' own cleanup (Go runs cleanups in reverse order), so the year's
// periods are still there to be named.
func fw1_09hDropReopenAuditRows(t *testing.T, pool *pgxpool.Pool, year int) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM audit_log WHERE action = 'GL_PERIOD_REOPEN'
			   AND entity_id IN (SELECT id FROM fiscal_periods WHERE year=$1)`, year)
	})
}

// fw1_09hReopenAuditRows reads the reopen's audit rows for the year, the way the trail
// reads them: by the action and by the period the row is keyed to.
func fw1_09hReopenAuditRows(t *testing.T, pool *pgxpool.Pool, year int) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE action = 'GL_PERIOD_REOPEN'
		   AND entity_id IN (SELECT id FROM fiscal_periods WHERE year=$1)`, year).Scan(&n); err != nil {
		t.Fatalf("read the GL_PERIOD_REOPEN audit rows for %d: %v", year, err)
	}
	return n
}

// fw1_09hReopenOverrides reads the override rows the reopen writes, the way the ledger
// reads them: by the action and by the period.
func fw1_09hReopenOverrides(t *testing.T, pool *pgxpool.Pool, year int) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM gl_period_overrides o JOIN fiscal_periods p ON p.id = o.period_id
		  WHERE p.year = $1 AND o.action = 'REOPEN'`, year).Scan(&n); err != nil {
		t.Fatalf("read the REOPEN override rows for %d: %v", year, err)
	}
	return n
}

// fw1_09hPeriodStatus reads one period's status, the way the month reads.
func fw1_09hPeriodStatus(t *testing.T, pool *pgxpool.Pool, year, month int) string {
	t.Helper()
	var status string
	if err := pool.QueryRow(context.Background(),
		`SELECT status FROM fiscal_periods WHERE year=$1 AND month=$2`, year, month).Scan(&status); err != nil {
		t.Fatalf("read the status of the period %d-%02d: %v", year, month, err)
	}
	return status
}

// fw1_09hExpectClosedYearRefusal holds the refusal to what the rule says it is: the
// year end close is an act, and a closed year's P&L never moves without a new close. The
// refusal carries the status, the core's own sentence naming the period's year, and it
// leaves nothing behind: the month stays sealed, no override row, no audit row.
func fw1_09hExpectClosedYearRefusal(t *testing.T, err error, pool *pgxpool.Pool, year, month int) {
	t.Helper()
	if err == nil {
		t.Fatalf("the reopen of the month %d-%02d of the CLOSED year %d was allowed: a closed year's P&L must never move without a new close", year, month, year)
	}
	var ce clientError
	if !errors.As(err, &ce) {
		t.Fatalf("the reopen of the month %d-%02d of the CLOSED year %d answered %v, want the core's own refusal", year, month, year, err)
	}
	if ce.status != http.StatusConflict {
		t.Fatalf("the reopen of the month %d-%02d of the CLOSED year %d answered %d, want 409", year, month, year, ce.status)
	}
	want := fmt.Sprintf("fiscal year %d is closed; post the correction in the open year", year)
	if !strings.Contains(ce.msg, want) {
		t.Fatalf("the reopen of the month %d-%02d of the CLOSED year %d answered %q, want the sentence %q", year, month, year, ce.msg, want)
	}
	// NOTHING WAS WRITTEN: the month is still sealed, the override row was never written,
	// and the audit row was never written.
	if status := fw1_09hPeriodStatus(t, pool, year, month); status != "CLOSED" {
		t.Fatalf("the refused reopen left the month %d-%02d in status %s, want CLOSED: the refusal comes before any write", year, month, status)
	}
	if n := fw1_09hReopenOverrides(t, pool, year); n != 0 {
		t.Fatalf("the refused reopen wrote %d REOPEN override rows, want none: the refusal comes before any write", n)
	}
	if n := fw1_09hReopenAuditRows(t, pool, year); n != 0 {
		t.Fatalf("the refused reopen wrote %d GL_PERIOD_REOPEN audit rows, want none: the refusal comes before the audit row", n)
	}
}

// TestIntegration_FW1_09h_AMonthOfAClosedYearCannotBeReopened is the red: a year closed
// at the year end (so the marker is written) refuses the reopen of one of its months, as
// the Financial Controller, in words and before any write.
func TestIntegration_FW1_09h_AMonthOfAClosedYearCannotBeReopened(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	const year = 3216
	ensurePeriods(t, pool, year)
	fw1_09gDropAuditRows(t, pool, year)
	fw1_09hDropReopenAuditRows(t, pool, year)
	recorder := audit.NewService(audit.NewRepository(pool), nil)
	controller := fw1_09cController(t, pool, user, year, recorder).WithAudit(recorder)

	// THE CLOSE: the year is closed at the year end, and it carries the marker.
	if _, err := controller.CloseYear(ctx, year, "FW1-09h the close of the year"); err != nil {
		t.Fatalf("the year end close of %d: %v", year, err)
	}
	if present, _, _, _ := fw1_09gMarker(t, pool, year); !present {
		t.Fatalf("the close of %d left no marker row: the marker is what says the year is closed", year)
	}
	if status := fw1_09hPeriodStatus(t, pool, year, 12); status != "CLOSED" {
		t.Fatalf("the close of %d left the month 12 in status %s, want CLOSED", year, status)
	}

	// THE RED: the Controller asks for the December of the closed year back.
	_, err := controller.ReopenPeriod(ctx, periodID(t, pool, year, 12), "FW1-09h the late correction")
	fw1_09hExpectClosedYearRefusal(t, err, pool, year, 12)
}

// TestIntegration_FW1_09h_AMonthOfAYearWithoutTheMarkerStillReopens is the second leg: a
// month of a year that carries NO marker still reopens, exactly as before. The rule is
// about a CLOSED fiscal year, and a year whose twelve months were sealed one by one with
// no year end close carries no marker: it is not a closed year, and a correction in it is
// still the way a book is corrected.
func TestIntegration_FW1_09h_AMonthOfAYearWithoutTheMarkerStillReopens(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	const year = 3217
	ensurePeriods(t, pool, year)
	fw1_09gDropAuditRows(t, pool, year)
	fw1_09hDropReopenAuditRows(t, pool, year)
	recorder := audit.NewService(audit.NewRepository(pool), nil)
	controller := fw1_09cController(t, pool, user, year, recorder).WithAudit(recorder)

	// The year is sealed MONTH BY MONTH, and no year end press ever ran, so the year
	// carries no marker.
	for month := 1; month <= 12; month++ {
		if _, err := controller.ClosePeriod(ctx, periodID(t, pool, year, month),
			fmt.Sprintf("FW1-09h seal %04d-%02d", year, month)); err != nil {
			t.Fatalf("seal the month %04d-%02d: %v", year, month, err)
		}
	}
	if present, _, _, _ := fw1_09gMarker(t, pool, year); present {
		t.Fatalf("the year %d carries a marker without a year end press: the leg's shape is wrong", year)
	}

	// THE REOPEN: still allowed, the month is unsealed and the trail records it.
	p, err := controller.ReopenPeriod(ctx, periodID(t, pool, year, 12), "FW1-09h the late correction")
	if err != nil {
		t.Fatalf("the reopen of a month of the year %d that carries no marker was refused: %v", year, err)
	}
	if p.Status != "OPEN" {
		t.Fatalf("the reopen of the month %d-12 of the year %d that carries no marker answered status %s, want OPEN", year, year, p.Status)
	}
	if status := fw1_09hPeriodStatus(t, pool, year, 12); status != "OPEN" {
		t.Fatalf("the reopen of the month %d-12 left the month in status %s, want OPEN", year, status)
	}
	if n := fw1_09hReopenOverrides(t, pool, year); n != 1 {
		t.Fatalf("the reopen of a month of the year %d that carries no marker left %d REOPEN override rows, want 1", year, n)
	}
	if n := fw1_09hReopenAuditRows(t, pool, year); n != 1 {
		t.Fatalf("the reopen of a month of the year %d that carries no marker left %d GL_PERIOD_REOPEN audit rows, want 1", year, n)
	}
}

// TestIntegration_FW1_09h_TheReopenRouteAnswers409WithTheCoreWords is the HTTP leg: the
// route answers 409 with the core's own sentence, the words the core's source holds.
func TestIntegration_FW1_09h_TheReopenRouteAnswers409WithTheCoreWords(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	const year = 3218
	ensurePeriods(t, pool, year)
	fw1_09gDropAuditRows(t, pool, year)
	fw1_09hDropReopenAuditRows(t, pool, year)
	recorder := audit.NewService(audit.NewRepository(pool), nil)
	controller := fw1_09cController(t, pool, user, year, recorder).WithAudit(recorder)

	if _, err := controller.CloseYear(ctx, year, "FW1-09h the close of the year"); err != nil {
		t.Fatalf("the year end close of %d: %v", year, err)
	}
	h := NewHandler(controller, nil)

	rec := httptest.NewRequest(http.MethodPost, "/api/v1/gl/periods/{id}/reopen",
		strings.NewReader(`{"reason":"a late invoice"}`))
	rec.SetPathValue("id", periodID(t, pool, year, 12))
	rsp := httptest.NewRecorder()
	h.reopenPeriod(rsp, rec, guards.StaffCaller{})

	if rsp.Code != http.StatusConflict {
		t.Fatalf("the reopen route answered %d %s, want 409: the core refuses a month of a closed year", rsp.Code, rsp.Body.String())
	}
	want := fmt.Sprintf("fiscal year %d is closed; post the correction in the open year", year)
	if !strings.Contains(rsp.Body.String(), want) {
		t.Fatalf("the reopen route answered %s, want the core's own sentence %q", rsp.Body.String(), want)
	}
	if status := fw1_09hPeriodStatus(t, pool, year, 12); status != "CLOSED" {
		t.Fatalf("the refused reopen left the month %d-12 in status %s, want CLOSED", year, status)
	}
}
