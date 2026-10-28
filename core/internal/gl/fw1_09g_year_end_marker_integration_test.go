//go:build integration

// fw1_09g_year_end_marker_integration_test.go, FW1-09g's red first legs on the real
// migrated and seeded database: the year end close's OWN marker, gl_year_end_closes.
//
// Today the once only signal for a year end close is the SHAPE of the books: a year that
// holds a closing journal is answered by that journal, and every other year is answered by
// the SEAL (every period of the year already CLOSED). The seal is not a record of the act:
// a year whose twelve months were sealed one by one and whose book holds no P&L at all
// therefore records NOTHING on its year end press, and a year that was never closed at the
// year end looks exactly like it. The marker is the record of the act, and it is the ONE
// once only signal for every data shape: a P&L year, a year with no P&L, a year whose P&L
// nets to zero, and a year sealed month by month.
//
// ISOLATION. Fiscal years 3200, 3201, 3202 and 3203, outside the seeded 2026 periods and
// every band the package's other integration tests use.
//
//	Run: DATABASE_URL='postgres://.../db?sslmode=disable' go test -tags=integration -count=1 -run FW1_09g ./internal/gl/
package gl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// fw1_09gMarker reads the year's marker row: the row the close writes for the year it
// closed. The table is migration 0452's, so a box without that migration answers an error
// here, which is the point: the marker does not exist yet.
func fw1_09gMarker(t *testing.T, pool *pgxpool.Pool, year int) (present bool, closedBy string, closedAt time.Time, reason string) {
	t.Helper()
	ctx := context.Background()
	err := pool.QueryRow(ctx,
		`SELECT true, COALESCE(closed_by::text,''), closed_at, COALESCE(reason,'') FROM gl_year_end_closes WHERE fiscal_year=$1`,
		year).Scan(&present, &closedBy, &closedAt, &reason)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, "", time.Time{}, ""
	}
	if err != nil {
		t.Fatalf("read the year end marker for %d: %v", year, err)
	}
	return present, closedBy, closedAt, reason
}

// fw1_09gField reads one field off the year end result through its JSON shape, so a leg can
// name a field the code does not carry yet WITHOUT failing to compile. A red that does not
// build proves nothing.
func fw1_09gField(t *testing.T, res YearCloseResult, field string) (any, bool) {
	t.Helper()
	body, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("render the year end result: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatalf("read the year end result %s: %v", body, err)
	}
	v, ok := fields[field]
	return v, ok
}

// fw1_09gAlreadyClosed holds the year end result's already_closed field to what the press
// must answer: false on the press that closed the year, true on the press that found it
// closed.
func fw1_09gAlreadyClosed(t *testing.T, res YearCloseResult, want bool, what string) {
	t.Helper()
	v, ok := fw1_09gField(t, res, "already_closed")
	if !ok {
		t.Fatalf("%s carries no already_closed field: the caller cannot tell 'closed now' from 'already closed'", what)
	}
	if v != want {
		t.Fatalf("%s answers already_closed %v, want %v", what, v, want)
	}
}

// fw1_09gDropAuditRows removes this leg's audit rows on cleanup, so the shared scratch
// database carries no trail of these legs into another package's run. It runs BEFORE
// ensurePeriods' own cleanup (Go runs cleanups in reverse order), so the year's periods are
// still there to be named.
func fw1_09gDropAuditRows(t *testing.T, pool *pgxpool.Pool, year int) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM audit_log WHERE action IN ('GL_PERIOD_CLOSE','GL_YEAR_END_CLOSE')
			   AND entity_id IN (SELECT id FROM fiscal_periods WHERE year=$1)`, year)
	})
}

// fw1_09gAuditRows reads the audit rows the close wrote for the year's own periods, the way
// the trail reads them: by the action and by the period the row is keyed to.
func fw1_09gAuditRows(t *testing.T, pool *pgxpool.Pool, year int, action string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE action=$2
		   AND entity_id IN (SELECT id FROM fiscal_periods WHERE year=$1)`, year, action).Scan(&n); err != nil {
		t.Fatalf("read the %s audit rows for %d: %v", action, year, err)
	}
	return n
}

// TestIntegration_FW1_09g_AYearSealedMonthByMonthWithNoPLRecordsItsCloseAndItsMarker is
// FW1-09g's red: a year whose twelve months were sealed ONE BY ONE, before any year end
// press, and whose book holds no P&L at all, records NOTHING on the year end press today.
// The seal (every period already CLOSED) is read as the once only signal, so the press
// answers the first run's result and writes no audit row and no marker.
//
// The year end close is an ACT, and an act leaves a record: one GL_YEAR_END_CLOSE row and
// one marker, naming who closed the year and why. The answer says which press closed it.
func TestIntegration_FW1_09g_AYearSealedMonthByMonthWithNoPLRecordsItsCloseAndItsMarker(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	const year = 3200
	ensurePeriods(t, pool, year)
	fw1_09gDropAuditRows(t, pool, year)
	recorder := audit.NewService(audit.NewRepository(pool), nil)
	controller := fw1_09cController(t, pool, user, year, recorder)

	// The year is sealed MONTH BY MONTH: every period closed on its own, before any year
	// end press, on a book that holds no P&L at all (a quiet year).
	for month := 1; month <= 12; month++ {
		id := periodID(t, pool, year, month)
		if _, err := controller.ClosePeriod(ctx, id, fmt.Sprintf("FW1-09g seal %04d-%02d", year, month)); err != nil {
			t.Fatalf("seal the month %04d-%02d: %v", year, month, err)
		}
	}

	const reason = "FW1-09g the year end close of a year sealed month by month"
	res, err := controller.CloseYear(ctx, year, reason)
	if err != nil {
		t.Fatalf("the year end close of a year sealed month by month: %v", err)
	}

	// THE RED: the press records the close it just did.
	if audits := fw1_09gAuditRows(t, pool, year, "GL_YEAR_END_CLOSE"); audits != 1 {
		t.Fatalf("the year end close of a year sealed month by month recorded %d GL_YEAR_END_CLOSE rows, want exactly 1 (the close is an act and it leaves a record)", audits)
	}

	// THE MARKER: one row for the year, naming who closed it and why.
	present, closedBy, closedAt, markerReason := fw1_09gMarker(t, pool, year)
	if !present {
		t.Fatalf("the year end close of %d left no marker row: a closed year must be marked, and the marker is the once only signal for every data shape", year)
	}
	if closedBy != user {
		t.Fatalf("the marker for %d names closed_by %q, want the caller %q", year, closedBy, user)
	}
	if closedAt.IsZero() {
		t.Fatalf("the marker for %d carries no closed_at", year)
	}
	if markerReason != reason {
		t.Fatalf("the marker for %d carries reason %q, want %q", year, markerReason, reason)
	}

	// THE ANSWER SAYS WHICH: this press closed the year now.
	fw1_09gAlreadyClosed(t, res, false, "the first press")

	// THE SECOND PRESS: the marker answers it. The year is closed once.
	res2, err := controller.CloseYear(ctx, year, "FW1-09g the second press")
	if err != nil {
		t.Fatalf("the second year end press: %v", err)
	}
	fw1_09gAlreadyClosed(t, res2, true, "the second press")
	// ... and it wrote nothing: still one audit row, still one marker.
	if audits := fw1_09gAuditRows(t, pool, year, "GL_YEAR_END_CLOSE"); audits != 1 {
		t.Fatalf("the second press recorded %d GL_YEAR_END_CLOSE rows, want 1 (the press writes nothing)", audits)
	}
	if present, _, _, _ := fw1_09gMarker(t, pool, year); !present {
		t.Fatalf("the second press left no marker row: the first press's marker must stand")
	}
}

// TestIntegration_FW1_09g_TheMarkerIsTheOnceOnlySignalAcrossTheDataShapes is the shape
// leg: the marker answers the repeat press for EVERY shape, the ones that hold a P&L and
// the ones that hold none. The P&L years are answered today by the closing journal and the
// seal; the marker is the ONE signal, so a year of any shape is closed once and says so.
func TestIntegration_FW1_09g_TheMarkerIsTheOnceOnlySignalAcrossTheDataShapes(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	recorder := audit.NewService(audit.NewRepository(pool), nil)

	shapes := []struct {
		name  string
		year  int
		plant func(t *testing.T, svc *Service, year int)
	}{
		{name: "a year with a P&L", year: 3201, plant: func(t *testing.T, svc *Service, year int) {
			fw1_09cPL(t, svc, year)
		}},
		{name: "a year with no P&L at all", year: 3202, plant: func(t *testing.T, svc *Service, year int) {}},
		{name: "a year whose P&L nets to zero", year: 3203, plant: func(t *testing.T, svc *Service, year int) {
			fw1_09cZeroNetPL(t, svc, year, fw1_09cBranch(t, pool))
		}},
	}
	for _, sh := range shapes {
		t.Run(sh.name, func(t *testing.T) {
			ensurePeriods(t, pool, sh.year)
			fw1_09gDropAuditRows(t, pool, sh.year)
			controller := fw1_09cController(t, pool, user, sh.year, recorder)
			sh.plant(t, controller, sh.year)

			res, err := controller.CloseYear(ctx, sh.year, "FW1-09g the first press")
			if err != nil {
				t.Fatalf("the first year end press: %v", err)
			}
			fw1_09gAlreadyClosed(t, res, false, "the first press")
			present, closedBy, _, _ := fw1_09gMarker(t, pool, sh.year)
			if !present {
				t.Fatalf("the first press left no marker row for %d", sh.year)
			}
			if closedBy != user {
				t.Fatalf("the marker for %d names closed_by %q, want the caller %q", sh.year, closedBy, user)
			}

			res2, err := controller.CloseYear(ctx, sh.year, "FW1-09g the repeat press")
			if err != nil {
				t.Fatalf("the repeat year end press: %v", err)
			}
			fw1_09gAlreadyClosed(t, res2, true, "the repeat press")
			if audits := fw1_09gAuditRows(t, pool, sh.year, "GL_YEAR_END_CLOSE"); audits != 1 {
				t.Fatalf("the shape left %d GL_YEAR_END_CLOSE rows after two presses, want exactly 1", audits)
			}
			var markers int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM gl_year_end_closes WHERE fiscal_year=$1`, sh.year).Scan(&markers); err != nil {
				t.Fatalf("count the marker rows for %d: %v", sh.year, err)
			}
			if markers != 1 {
				t.Fatalf("the shape left %d marker rows for %d, want exactly 1", markers, sh.year)
			}
		})
	}
}
