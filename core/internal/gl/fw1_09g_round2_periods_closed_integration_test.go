//go:build integration

// fw1_09g_round2_periods_closed_integration_test.go, FW1-09g round 2's red first leg
// (FROM-PRINCIPAL 350, S2's P3 1 ruled): the press that answers "already closed" reads
// periods_closed from the year's REAL period count, not from the marker.
//
// The answer carries two facts: WHICH press this was (already_closed) and WHAT the year's
// periods are (periods_closed). The marker answers the first. It cannot answer the second:
// the marker records the act, and an act does not say what the periods did afterwards. So a
// closed year whose December was reopened answers PeriodsClosed true today, while one of
// its twelve months is OPEN.
//
// The count is the truth: every period of the year CLOSED, or not.
//
// WHAT A REOPEN OF A CLOSED YEAR MEANS is FW1-09h's business, after the wave (FROM-PRINCIPAL
// 350). This leg does not change the reopen: it plants the state through the store seam (the
// same seam S2's probe used) and asks what the press ANSWERS in that state.
//
// ISOLATION. Fiscal year 3215, outside the seeded 2026 periods and every band the package's
// other integration tests use.
//
//	Run: DATABASE_URL='postgres://.../db?sslmode=disable' go test -tags=integration -count=1 -run FW1_09g ./internal/gl/
package gl

import (
	"context"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/jackc/pgx/v5/pgxpool"
)

// fw1_09gR2PeriodCounts reads the year's periods the way the close reads them: how many
// there are, and how many of them are CLOSED.
func fw1_09gR2PeriodCounts(t *testing.T, pool *pgxpool.Pool, year int) (total, closed int) {
	t.Helper()
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*), count(*) FILTER (WHERE status = 'CLOSED') FROM fiscal_periods WHERE year = $1`,
		year).Scan(&total, &closed); err != nil {
		t.Fatalf("read the periods of %d: %v", year, err)
	}
	return total, closed
}

// TestIntegration_FW1_09g_R2_TheAlreadyClosedPressAnswersPeriodsClosedFromTheCount is the
// red: a closed year with one month reopened answers PeriodsClosed false. The press knows
// the year is closed (the marker says so, and it answers already closed), and it must also
// say that the year's periods are NOT all closed.
func TestIntegration_FW1_09g_R2_TheAlreadyClosedPressAnswersPeriodsClosedFromTheCount(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	const year = 3215
	ensurePeriods(t, pool, year)
	fw1_09gR2DropAuditRows(t, pool, year)
	recorder := audit.NewService(audit.NewRepository(pool), nil)
	controller := fw1_09cController(t, pool, user, year, recorder)

	// THE CLOSE: the year is sealed and closed, and it carries the marker.
	res, err := controller.CloseYear(ctx, year, "FW1-09g round 2 the close of the year")
	if err != nil {
		t.Fatalf("the year end close of %d: %v", year, err)
	}
	fw1_09gAlreadyClosed(t, res, false, "the first press")
	if present, _, _, _ := fw1_09gMarker(t, pool, year); !present {
		t.Fatalf("the close of %d left no marker row", year)
	}
	if total, closed := fw1_09gR2PeriodCounts(t, pool, year); total != 12 || closed != 12 {
		t.Fatalf("the closed year %d holds %d periods and %d of them CLOSED, want 12 and 12", year, total, closed)
	}

	// ONE MONTH IS OPEN: the December of the closed year is reopened, through the store
	// seam. What a reopen of a closed year means is FW1-09h's business, after the wave.
	if _, err := NewRepository(pool).ReopenPeriod(ctx, periodID(t, pool, year, 12), user,
		"FW1-09g round 2 the reopened month"); err != nil {
		t.Fatalf("reopen the December of the closed year %d: %v", year, err)
	}
	if total, closed := fw1_09gR2PeriodCounts(t, pool, year); total != 12 || closed != 11 {
		t.Fatalf("the reopened year %d holds %d periods and %d of them CLOSED, want 12 and 11", year, total, closed)
	}

	// THE PRESS AFTER THE REOPEN: it answers already closed, and its periods_closed must
	// come from the count.
	res2, err := controller.CloseYear(ctx, year, "FW1-09g round 2 the press after the reopen")
	if err != nil {
		t.Fatalf("the year end press after the reopen of %d: %v", year, err)
	}
	fw1_09gAlreadyClosed(t, res2, true, "the press after the reopen")
	v, ok := fw1_09gField(t, res2, "periods_closed")
	if !ok {
		t.Fatalf("the press after the reopen carries no periods_closed field: the caller cannot tell a sealed year from an open one")
	}
	if v != false {
		t.Fatalf("the press after the reopen answers periods_closed %v, want false: eleven of the year's twelve months are CLOSED and one is OPEN, so the answer must come from the year's real count and not from the marker", v)
	}
}
