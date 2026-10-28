//go:build integration

// fw1_09g_round2_year_wipe_integration_test.go, FW1-09g round 2's red first legs
// (FROM-PRINCIPAL 350, K3's P2): the year's wipe takes the YEAR END MARKER with the
// year's periods.
//
// The marker (migration 0452's gl_year_end_closes) is the record of the year end close
// and the ONE once only signal for every shape of the books. It has its own primary key
// and no foreign key to fiscal_periods, so deleting the year's periods cannot take it
// with them. The two wipes that empty a year between legs (fw1ScrubAfter, which takes
// every year above a threshold, and fw1NextYear, which takes the year above every period
// in the database) therefore leave the marker behind, and a reused gate box carries that
// row into the next run: the next close answers AlreadyClosed before the leg does any
// work, and the leg sees no closing journal and no new year end audit.
//
// The marker must go with the year's periods: a year whose periods are gone was never
// closed, and its next close must be a NEW close.
//
// ISOLATION. Fiscal years 3210 to 3214, outside the seeded 2026 periods and every band
// the package's other integration tests use.
//
//	Run: DATABASE_URL='postgres://.../db?sslmode=disable' go test -tags=integration -count=1 -run FW1_09g ./internal/gl/
package gl

import (
	"context"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/jackc/pgx/v5/pgxpool"
)

// fw1_09gR2DropAuditRows removes the year's own audit rows on cleanup, by the year the
// row carries, so a row that was keyed to a period the wipe has since replaced still
// goes. The trail must not carry a dead period's row into another leg's run.
func fw1_09gR2DropAuditRows(t *testing.T, pool *pgxpool.Pool, year int) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM audit_log WHERE action IN ('GL_PERIOD_CLOSE','GL_YEAR_END_CLOSE')
			   AND changes->>'fiscal_year' = $1::text`, year)
	})
}

// fw1_09gR2PlantPeriods makes the year's twelve periods and takes them away again on
// cleanup WITHOUT taking the year end marker with them. That is the state an earlier run
// leaves behind on a reused box: the year's periods gone, its marker still standing.
func fw1_09gR2PlantPeriods(t *testing.T, pool *pgxpool.Pool, year int) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx,
		`INSERT INTO fiscal_periods (year, month) SELECT $1, m FROM generate_series(1,12) AS m
		 ON CONFLICT (year, month) DO NOTHING`, year); err != nil {
		t.Fatalf("plant the periods for %d: %v", year, err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM gl_journal_lines l USING gl_journals j WHERE l.journal_id=j.id AND j.fiscal_year=$1`, year)
		_, _ = pool.Exec(ctx, `DELETE FROM gl_journals WHERE fiscal_year=$1`, year)
		_, _ = pool.Exec(ctx, `DELETE FROM gl_period_overrides o USING fiscal_periods p WHERE o.period_id=p.id AND p.year=$1`, year)
		_, _ = pool.Exec(ctx, `DELETE FROM fiscal_periods WHERE year=$1`, year)
	})
}

// fw1_09gR2PlantMarker writes the year end marker for a year by hand: the record of the
// act, standing on a year whose periods are gone. It is what an earlier run's close
// leaves behind, and the box carries it into the next run.
func fw1_09gR2PlantMarker(t *testing.T, pool *pgxpool.Pool, year int, closedBy string) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx,
		`INSERT INTO gl_year_end_closes (fiscal_year, closed_at, closed_by, reason)
		 VALUES ($1, now(), $2::uuid, $3) ON CONFLICT (fiscal_year) DO NOTHING`,
		year, closedBy, "the marker an earlier run left behind"); err != nil {
		t.Fatalf("plant the year end marker for %d: %v", year, err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM gl_year_end_closes WHERE fiscal_year=$1`, year)
	})
}

// fw1_09gR2NextYear answers the year above every period in the database, the way
// fw1NextYear names it, so a leg can plant the marker on that exact year before the wipe
// runs. The band year pins the answer, so the leg's own plant cannot drift.
func fw1_09gR2NextYear(t *testing.T, pool *pgxpool.Pool, band int) int {
	t.Helper()
	var max int
	if err := pool.QueryRow(context.Background(),
		`SELECT COALESCE(MAX(year), 2025) FROM fiscal_periods`).Scan(&max); err != nil {
		t.Fatalf("read the latest fiscal year: %v", err)
	}
	if max < band {
		t.Fatalf("the latest fiscal year in the database is %d, want at least %d", max, band)
	}
	return max + 1
}

// TestIntegration_FW1_09g_R2_TheScrubAboveTheThresholdTakesTheMarker is the first red:
// the scrub that empties every year above a threshold takes the year end marker with the
// year's periods. Today it takes the journals, the overrides and the periods, and leaves
// the marker, so the next close of that year answers AlreadyClosed and writes nothing.
func TestIntegration_FW1_09g_R2_TheScrubAboveTheThresholdTakesTheMarker(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	const year = 3210
	const threshold = 3209
	ensurePeriods(t, pool, year)
	fw1_09gR2DropAuditRows(t, pool, year)
	recorder := audit.NewService(audit.NewRepository(pool), nil)
	controller := fw1_09cController(t, pool, user, year, recorder)

	// PLANT: the year is closed, so it carries the marker that records the act.
	res, err := controller.CloseYear(ctx, year, "FW1-09g round 2 the planting close")
	if err != nil {
		t.Fatalf("the planting year end close of %d: %v", year, err)
	}
	fw1_09gAlreadyClosed(t, res, false, "the planting press")
	if present, _, _, _ := fw1_09gMarker(t, pool, year); !present {
		t.Fatalf("the planting close of %d left no marker row: the marker is the record of the act", year)
	}

	// THE WIPE: every year above the threshold goes, the way a shared gate box is
	// scrubbed between the legs that use it.
	fw1ScrubAfter(t, pool, threshold)

	// THE YEAR IS BACK: the periods are made again, so the year can be closed again.
	ensurePeriods(t, pool, year)

	// THE PRESS AFTER THE WIPE: the year carries no marker (the wipe took it with the
	// year's periods), so this press is a NEW close: it says so, and it leaves the row.
	res2, err := controller.CloseYear(ctx, year, "FW1-09g round 2 the press after the wipe")
	if err != nil {
		t.Fatalf("the year end press after the wipe of %d: %v", year, err)
	}
	audits := fw1_09gAuditRows(t, pool, year, "GL_YEAR_END_CLOSE")
	if res2.AlreadyClosed || audits != 1 {
		t.Fatalf("the press after the wipe answered already_closed %v and recorded %d GL_YEAR_END_CLOSE rows, want false and exactly 1: the year end marker must go with the year's periods, or a scrubbed year can never be closed again", res2.AlreadyClosed, audits)
	}
}

// TestIntegration_FW1_09g_R2_TheNextYearWipeTakesTheMarker is the second red: the wipe
// that takes the year above every period in the database takes the marker with it, on
// both of its halves. The setup half runs the moment the helper is called, and the
// cleanup half runs when the leg that called it ends.
func TestIntegration_FW1_09g_R2_TheNextYearWipeTakesTheMarker(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	const band = 3211
	ensurePeriods(t, pool, band)
	fw1_09gR2DropAuditRows(t, pool, band)
	recorder := audit.NewService(audit.NewRepository(pool), nil)

	// PLANT: the marker an earlier run left behind, on the year the wipe names.
	next := fw1_09gR2NextYear(t, pool, band)
	fw1_09gR2DropAuditRows(t, pool, next)
	fw1_09gR2PlantMarker(t, pool, next, user)

	// THE WIPE, setup half: it runs the moment the helper is called.
	got := fw1NextYear(t, pool)
	if got != next {
		t.Fatalf("the next year wipe answered %d, want the planted year %d", got, next)
	}
	if present, _, _, _ := fw1_09gMarker(t, pool, next); present {
		t.Fatalf("the next year wipe left the year end marker for %d behind: the marker must go with the year's periods, or the next run's close answers already closed before it does any work", next)
	}

	// THE YEAR IS BACK, and the press must be a NEW close: it says so, and it leaves the row.
	ensurePeriods(t, pool, next)
	controller := fw1_09cController(t, pool, user, next, recorder)
	res, err := controller.CloseYear(ctx, next, "FW1-09g round 2 the press after the next year wipe")
	if err != nil {
		t.Fatalf("the year end press after the next year wipe of %d: %v", next, err)
	}
	audits := fw1_09gAuditRows(t, pool, next, "GL_YEAR_END_CLOSE")
	if res.AlreadyClosed || audits != 1 {
		t.Fatalf("the press after the next year wipe answered already_closed %v and recorded %d GL_YEAR_END_CLOSE rows, want false and exactly 1: the year end marker must go with the year's periods", res.AlreadyClosed, audits)
	}
}

// TestIntegration_FW1_09g_R2_TheNextYearWipesCleanupTakesTheMarker is the third red: the
// cleanup half of the next year wipe takes the marker too. It is the half that runs when
// the leg that called the helper ends, and it is the half that leaves the marker behind on
// the box for the NEXT run to find.
//
// The leg runs the earlier run as a subtest, so that the earlier run's own cleanup (the
// helper's cleanup half) has run by the time the marker is read.
func TestIntegration_FW1_09g_R2_TheNextYearWipesCleanupTakesTheMarker(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	const band = 3213
	ensurePeriods(t, pool, band)
	fw1_09gR2DropAuditRows(t, pool, band)
	recorder := audit.NewService(audit.NewRepository(pool), nil)

	var earlier int
	t.Run("an earlier run closes the year", func(t *testing.T) {
		// The earlier run's own wipe: the helper takes the year above every period in the
		// database, and its cleanup takes that year's rows when this subtest ends.
		earlier = fw1NextYear(t, pool)
		// The earlier run's own periods. The leg takes them away again WITHOUT taking the
		// marker, which is the state a reused box carries into the next run.
		fw1_09gR2PlantPeriods(t, pool, earlier)
		controller := fw1_09cController(t, pool, user, earlier, recorder)
		res, err := controller.CloseYear(ctx, earlier, "FW1-09g round 2 the earlier run's close")
		if err != nil {
			t.Fatalf("the earlier run's year end close of %d: %v", earlier, err)
		}
		fw1_09gAlreadyClosed(t, res, false, "the earlier run's press")
		if present, _, _, _ := fw1_09gMarker(t, pool, earlier); !present {
			t.Fatalf("the earlier run's close of %d left no marker row", earlier)
		}
	})
	// The earlier run's cleanup has run: the helper's cleanup half took the year's rows.
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM gl_year_end_closes WHERE fiscal_year=$1`, earlier)
		_, _ = pool.Exec(ctx,
			`DELETE FROM audit_log WHERE action IN ('GL_PERIOD_CLOSE','GL_YEAR_END_CLOSE')
			   AND changes->>'fiscal_year' = $1::text`, earlier)
	})
	if present, _, _, _ := fw1_09gMarker(t, pool, earlier); present {
		t.Fatalf("the next year wipe's cleanup left the year end marker for %d behind: the marker must go with the year's periods, or the next run's close answers already closed before it does any work", earlier)
	}
}
