//go:build integration

// fw1_09f_open_drawers_at_month_end_integration_test.go, FW1-09f (S5's P3 on PR 647, FROM-PRINCIPAL
// 341 item 1): the close pre flight's drawer check must count every drawer the month is still
// waiting on, not only the drawers OPENED inside the month.
//
// THE DEFECT. The pre flight asked the till seam for the drawers opened in [month start, next month)
// that were still OPEN. A drawer opened in an EARLIER month and never closed passed the close of
// every later month: the books sealed over a drawer that was still open. "No open drawer" means any
// drawer the month is still waiting on.
//
// ROUND 2, THE RULE IS "NOT COMPLETED AT THE CLOSE" (S5's P2-1, FROM-PRINCIPAL 346). Round 1 read
// the drawer as it stood at the month's END, a past instant: a drawer opened on the month's last
// evening and completed after midnight counted for ever, because CLOSED is final and no product
// action clears it. The month's books are whole once the drawer is completed, because the
// completion posts the over or short into the business day the drawer was OPENED for. So the read
// counts the drawers opened by the month's end that are NOT YET COMPLETED, and a drawer completed
// at any time clears the month.
//
// THE LEGS. One red: a drawer opened in the month BEFORE the month being closed, never closed, and
// the close of that later month must be refused naming the drawer that is not yet completed. Round
// 2's two reds: a drawer completed after midnight, and a drawer closed exactly at midnight, both in
// the house zone, must CLOSE the month. Two honesty legs: a drawer opened AND closed inside the
// month does not block, and a drawer opened AFTER the month's end does not block (it blocks the
// month it was opened in, which is the point). Round 2's fourth leg: the legs' own cleanup must
// remove the override rows of its own sentinel year only, never another year's.
//
// THE WIRING. The drawer seam reads the REAL till repository, the same read cmd/core wires, and the
// closer is the real GL service, so the legs prove the shipped path and not a fake's arithmetic.
//
// THE SENTINEL YEARS. The months sit in past sentinel years (2019, 2021, 2023, 2017, 2015, 2013),
// never in the seeded 2026 books. A past month also keeps the legs honest in the shared gate
// database: every other leg opens its drawer NOW, so no other leg's drawer can sit open at a past
// month's end.
package till

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// fw109fDrawerReader is the wired drawer seam: the GL close pre flight reads the till drawers
// through it, and it reads the REAL repository, the same read cmd/core wires (FW1-09f).
type fw109fDrawerReader struct{ repo *Repository }

func (r fw109fDrawerReader) UncompletedDrawersOpenedBefore(ctx context.Context, before time.Time) (int, error) {
	return r.repo.UncompletedSessionsOpenedBefore(ctx, before)
}

// fw109fPeriods creates the sentinel year's twelve fiscal periods and removes them again when the
// leg ends, so the shared gate database keeps no period of the leg's own.
func fw109fPeriods(t *testing.T, pool *pgxpool.Pool, year int) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx,
		`INSERT INTO fiscal_periods (year, month) SELECT $1, m FROM generate_series(1,12) AS m
		 ON CONFLICT (year, month) DO NOTHING`, year); err != nil {
		t.Fatalf("ensure the %d periods: %v", year, err)
	}
	t.Cleanup(func() { fw109fCleanupPeriods(t, pool, year) })
}

// fw109fCleanupPeriods removes the sentinel year's periods and the override rows that hang on
// them, so the shared gate database keeps no trace of the leg.
//
// The override delete joins the periods on the override's OWN period, exactly as gl's ensurePeriods
// does: joining on the year alone removed every override row in the shared gate database, other
// packages' included.
func fw109fCleanupPeriods(t *testing.T, pool *pgxpool.Pool, year int) {
	t.Helper()
	ctx := context.Background()
	_, _ = pool.Exec(ctx, `DELETE FROM gl_period_overrides o USING fiscal_periods p WHERE o.period_id = p.id AND p.year = $1`, year)
	_, _ = pool.Exec(ctx, `DELETE FROM fiscal_periods WHERE year = $1`, year)
}

// fw109fPeriodID answers the month's period id.
func fw109fPeriodID(t *testing.T, pool *pgxpool.Pool, year, month int) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(),
		`SELECT id::text FROM fiscal_periods WHERE year = $1 AND month = $2`, year, month).Scan(&id); err != nil {
		t.Fatalf("the %d-%02d period: %v", year, month, err)
	}
	return id
}

// fw109fPeriodCount counts one month's periods, the number a leg's cleanup must not move when it
// did not plant the row.
func fw109fPeriodCount(t *testing.T, pool *pgxpool.Pool, year, month int) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM fiscal_periods WHERE year = $1 AND month = $2`, year, month).Scan(&n); err != nil {
		t.Fatalf("read the %d-%02d periods: %v", year, month, err)
	}
	return n
}

// fw109fOtherYearPeriod plants the other year's period and answers whether this call planted it.
//
// The plant is ON CONFLICT DO NOTHING, so on a reused box the row can already be there: the
// answer tells the caller whether the row is the leg's own to remove. A row that came back from
// the insert is the leg's own; a row that was already there is not, and the leg must leave it
// exactly as it found it (S5's P3, FROM-PRINCIPAL 349).
func fw109fOtherYearPeriod(t *testing.T, pool *pgxpool.Pool, year, month int) (string, bool) {
	t.Helper()
	ctx := context.Background()
	var id string
	err := pool.QueryRow(ctx,
		`INSERT INTO fiscal_periods (year, month) VALUES ($1, $2) ON CONFLICT (year, month) DO NOTHING RETURNING id::text`,
		year, month).Scan(&id)
	if err == nil {
		return id, true
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("ensure the %d-%02d period: %v", year, month, err)
	}
	// The row was already there: the leg planted nothing.
	return fw109fPeriodID(t, pool, year, month), false
}

// fw109fOtherYearCleanup removes the other year's override row and its period.
//
// The period is removed only when the leg planted it (S5's P3, FROM-PRINCIPAL 349): the plant is
// ON CONFLICT DO NOTHING, so on a reused box the row can already be there, and a leg that removed
// it anyway took a row it did not create out of the shared gate database. The override row is
// removed first, because gl_period_overrides holds the period ON DELETE RESTRICT.
func fw109fOtherYearCleanup(t *testing.T, pool *pgxpool.Pool, periodID string, planted bool) {
	t.Helper()
	ctx := context.Background()
	_, _ = pool.Exec(ctx, `DELETE FROM gl_period_overrides WHERE period_id = $1::uuid`, periodID)
	if !planted {
		return
	}
	_, _ = pool.Exec(ctx, `DELETE FROM fiscal_periods WHERE id = $1::uuid`, periodID)
}

// fw109fDrawer plants one till drawer: opened at openedAt, closed at closedAt when that is set,
// left open otherwise. The read that matters runs through the real repository.
//
// A CLOSED drawer is planted in the EXACT shape SetCompleted writes (till/repository.go): the
// session is CLOSED, the completion carries its own instant, and the over or short sits on the
// row. The leg plants the row rather than running the till service, because the service's
// completion posts a GL journal into the drawer's own business day, and a journal landed in a
// shared period would move the balances other legs read.
func fw109fDrawer(t *testing.T, pool *pgxpool.Pool, branchID, userID, station string, openedAt time.Time, closedAt *time.Time) {
	t.Helper()
	ctx := context.Background()
	var id string
	if closedAt == nil {
		if err := pool.QueryRow(ctx,
			`INSERT INTO till_sessions (branch_id, station, status, opened_by, float_cents, opened_at)
			 VALUES ($1, $2, 'OPEN', $3, 0, $4) RETURNING id::text`,
			branchID, station, userID, openedAt).Scan(&id); err != nil {
			t.Fatalf("plant the drawer at %s: %v", station, err)
		}
	} else {
		if err := pool.QueryRow(ctx,
			`INSERT INTO till_sessions (branch_id, station, status, opened_by, float_cents, opened_at,
			    completed_by, completed_at, closed_by, closed_at, counted_cents, over_short_cents)
			 VALUES ($1, $2, 'CLOSED', $3, 0, $4, $3, $5, $3, $5, 0, 0) RETURNING id::text`,
			branchID, station, userID, openedAt, *closedAt).Scan(&id); err != nil {
			t.Fatalf("plant the closed drawer at %s: %v", station, err)
		}
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM till_sessions WHERE id = $1`, id)
	})
}

// fw109fHouseZone is the dealer's business zone, the one the close pre flight reads its
// instants in (the store's own BusinessZone, never a hard coded default).
func fw109fHouseZone(t *testing.T, pool *pgxpool.Pool) *time.Location {
	t.Helper()
	zone, err := gl.NewRepository(pool).BusinessZone(context.Background())
	if err != nil {
		t.Fatalf("the dealer's business zone: %v", err)
	}
	return zone.Location()
}

// fw109fOverrideCount counts the override rows in the whole gate database, the number a leg's
// cleanup must not move beyond its own year's.
func fw109fOverrideCount(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM gl_period_overrides`).Scan(&n); err != nil {
		t.Fatalf("read the override rows: %v", err)
	}
	return n
}

// fw109fOverrideCountOn counts the override rows on one period.
func fw109fOverrideCountOn(t *testing.T, pool *pgxpool.Pool, periodID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM gl_period_overrides WHERE period_id = $1::uuid`, periodID).Scan(&n); err != nil {
		t.Fatalf("read the override rows of %s: %v", periodID, err)
	}
	return n
}

// fw109fCloser is the GL service the way cmd/core wires it, the drawer seam reading the real till
// repository.
func fw109fCloser(t *testing.T, pool *pgxpool.Pool) *gl.Service {
	t.Helper()
	return gl.NewService(gl.NewRepository(pool)).WithDrawerReader(fw109fDrawerReader{repo: NewRepository(pool)})
}

// fw109fCtx stamps the Controller caller the close runs as: a period is company wide, its close a
// dealer wide act.
func fw109fCtx(ctx context.Context, userID string) context.Context {
	return identity.ContextWithStaff(ctx, identity.StaffContext{
		UserID: userID, RoleCode: "FINANCIAL_CONTROLLER", Tier: identity.TierLeadership,
		Scope: identity.ScopeGlobal, Status: "ACTIVE",
	})
}

// fw109fPeriodStatus reads the month's own status.
func fw109fPeriodStatus(t *testing.T, pool *pgxpool.Pool, year, month int) string {
	t.Helper()
	var status string
	if err := pool.QueryRow(context.Background(),
		`SELECT status FROM fiscal_periods WHERE year = $1 AND month = $2`, year, month).Scan(&status); err != nil {
		t.Fatalf("read the %d-%02d period: %v", year, month, err)
	}
	return status
}

// TestIntegration_FW1_09f_ADrawerOpenFromAnEarlierMonthRefusesTheClose is round 1's red, kept:
// a drawer opened in the month BEFORE the month being closed, never closed. The close of the later
// month must be refused, naming the drawer that is not yet completed, and the month must stand
// OPEN. The refusal's words say what is true: the drawer was opened in or before the month and is
// not yet completed.
func TestIntegration_FW1_09f_ADrawerOpenFromAnEarlierMonthRefusesTheClose(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	branchID, userID := seedBranchUser(t, pool)
	const year = 2019
	fw109fPeriods(t, pool, year)
	fw109fDrawer(t, pool, branchID, userID, "FW1-09F-EARLIER",
		time.Date(year, 5, 15, 12, 0, 0, 0, time.UTC), nil)

	svc := fw109fCloser(t, pool)
	_, err := svc.ClosePeriod(fw109fCtx(ctx, userID), fw109fPeriodID(t, pool, year, 6), "FW1-09f earlier month drawer")
	if err == nil {
		t.Fatalf("the month closed with a drawer open from the earlier month; the pre flight must refuse")
	}
	msg := err.Error()
	for _, want := range []string{"pre flight", "1 till drawer opened in or before the month is not yet completed"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("the refusal %q does not name %q", msg, want)
		}
	}
	if status := fw109fPeriodStatus(t, pool, year, 6); status != "OPEN" {
		t.Fatalf("the refused close left the period %s, want OPEN", status)
	}
}

// TestIntegration_FW1_09f_ADrawerOpenedAndClosedInsideTheMonthDoesNotBlock is the first honesty leg:
// a drawer opened AND closed inside the month being closed was not open at that month's end, so the
// close must seal the month.
func TestIntegration_FW1_09f_ADrawerOpenedAndClosedInsideTheMonthDoesNotBlock(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	branchID, userID := seedBranchUser(t, pool)
	const year = 2021
	fw109fPeriods(t, pool, year)
	opened := time.Date(year, 6, 10, 12, 0, 0, 0, time.UTC)
	closed := time.Date(year, 6, 20, 12, 0, 0, 0, time.UTC)
	fw109fDrawer(t, pool, branchID, userID, "FW1-09F-INSIDE", opened, &closed)

	svc := fw109fCloser(t, pool)
	if _, err := svc.ClosePeriod(fw109fCtx(ctx, userID), fw109fPeriodID(t, pool, year, 6), "FW1-09f closed inside the month"); err != nil {
		t.Fatalf("the month did not close with a drawer closed inside it: %v", err)
	}
	if status := fw109fPeriodStatus(t, pool, year, 6); status != "CLOSED" {
		t.Fatalf("the closed month reads %s, want CLOSED", status)
	}
}

// TestIntegration_FW1_09f_ADrawerOpenedAfterTheMonthEndDoesNotBlock is the second honesty leg: a
// drawer opened AFTER the month being closed ended did not exist at that month's end, so it does not
// block that month. It blocks the month it was opened in, which is the same rule read the other way
// round.
func TestIntegration_FW1_09f_ADrawerOpenedAfterTheMonthEndDoesNotBlock(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	branchID, userID := seedBranchUser(t, pool)
	const year = 2023
	fw109fPeriods(t, pool, year)
	fw109fDrawer(t, pool, branchID, userID, "FW1-09F-LATER",
		time.Date(year, 8, 5, 12, 0, 0, 0, time.UTC), nil)

	svc := fw109fCloser(t, pool)
	// July closed: the drawer opens in August, after July's end, so July's close is clean.
	if _, err := svc.ClosePeriod(fw109fCtx(ctx, userID), fw109fPeriodID(t, pool, year, 7), "FW1-09f drawer opens later"); err != nil {
		t.Fatalf("the month did not close with a drawer that opens after its end: %v", err)
	}
	if status := fw109fPeriodStatus(t, pool, year, 7); status != "CLOSED" {
		t.Fatalf("the closed month reads %s, want CLOSED", status)
	}
	// August refused: the same drawer is open at August's end, and it names the drawer.
	_, err := svc.ClosePeriod(fw109fCtx(ctx, userID), fw109fPeriodID(t, pool, year, 8), "FW1-09f drawer opens in the month")
	if err == nil {
		t.Fatalf("the month closed with a drawer open inside it; the pre flight must refuse")
	}
	msg := err.Error()
	for _, want := range []string{"pre flight", "1 till drawer opened in or before the month is not yet completed"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("the refusal %q does not name %q", msg, want)
		}
	}
	if status := fw109fPeriodStatus(t, pool, year, 8); status != "OPEN" {
		t.Fatalf("the refused close left the period %s, want OPEN", status)
	}
}

// TestIntegration_FW1_09f_ADrawerCompletedAfterMidnightDoesNotBlockTheMonth is round 2's first red
// (S5's P2-1, FROM-PRINCIPAL 346): a drawer opened on the month's last evening and completed after
// midnight must not block that month for ever.
//
// CLOSED is final: the completion is the one write that ends a drawer's life, so a pre flight that
// counted the drawer would refuse the close with no product action left to clear it. And the books
// are whole once it is done, because the completion posts the over or short into the business day
// the drawer was OPENED for, which is the month being closed.
func TestIntegration_FW1_09f_ADrawerCompletedAfterMidnightDoesNotBlockTheMonth(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	branchID, userID := seedBranchUser(t, pool)
	const year = 2017
	fw109fPeriods(t, pool, year)
	house := fw109fHouseZone(t, pool)
	// The drawer's own evening, and the count the next morning after midnight, both in the
	// dealer's house zone, so the leg lands where the house zone matters.
	opened := time.Date(year, 6, 30, 20, 0, 0, 0, house)
	completed := time.Date(year, 7, 1, 8, 0, 0, 0, house)
	fw109fDrawer(t, pool, branchID, userID, "FW1-09F-OVERNIGHT", opened, &completed)

	svc := fw109fCloser(t, pool)
	if _, err := svc.ClosePeriod(fw109fCtx(ctx, userID), fw109fPeriodID(t, pool, year, 6), "FW1-09f drawer completed after midnight"); err != nil {
		t.Fatalf("the month did not close with a drawer completed after midnight: %v", err)
	}
	if status := fw109fPeriodStatus(t, pool, year, 6); status != "CLOSED" {
		t.Fatalf("the closed month reads %s, want CLOSED", status)
	}
}

// TestIntegration_FW1_09f_ADrawerClosedExactlyAtMidnightDoesNotBlockTheMonth is round 2's second
// red: a drawer closed EXACTLY at midnight in the house zone, the first instant of the next month,
// is the month's end itself. It is completed, so it must not block the month.
//
// The boundary is the one the zone decides: midnight in the house zone is not midnight in UTC, so
// the leg reads the dealer's own zone instead of a fixed one.
func TestIntegration_FW1_09f_ADrawerClosedExactlyAtMidnightDoesNotBlockTheMonth(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	branchID, userID := seedBranchUser(t, pool)
	const year = 2015
	fw109fPeriods(t, pool, year)
	house := fw109fHouseZone(t, pool)
	opened := time.Date(year, 6, 30, 20, 0, 0, 0, house)
	// Exactly the month's end: the first instant of the next month in the house zone.
	closed := time.Date(year, 7, 1, 0, 0, 0, 0, house)
	fw109fDrawer(t, pool, branchID, userID, "FW1-09F-MIDNIGHT", opened, &closed)

	svc := fw109fCloser(t, pool)
	if _, err := svc.ClosePeriod(fw109fCtx(ctx, userID), fw109fPeriodID(t, pool, year, 6), "FW1-09f drawer closed at midnight"); err != nil {
		t.Fatalf("the month did not close with a drawer closed exactly at midnight: %v", err)
	}
	if status := fw109fPeriodStatus(t, pool, year, 6); status != "CLOSED" {
		t.Fatalf("the closed month reads %s, want CLOSED", status)
	}
}

// TestIntegration_FW1_09f_ThePeriodCleanupLeavesAnotherYearsOverrideAlone is round 2's third red
// (S5's P2-2): the legs' own cleanup must remove the override rows of ITS OWN sentinel year only.
//
// The cleanup joined the periods on the year alone, so it removed EVERY override row in the shared
// gate database: other packages' legs read those rows by period, and the override row is the
// recorded compensating control for a reopen. A gate shared across packages must be left as the leg
// found it.
func TestIntegration_FW1_09f_ThePeriodCleanupLeavesAnotherYearsOverrideAlone(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	const ownYear = 2013
	const otherYear = 2009

	// The leg's own sentinel year, through the leg's own helper, so the cleanup under test is
	// the one every leg runs.
	fw109fPeriods(t, pool, ownYear)
	ownPeriod := fw109fPeriodID(t, pool, ownYear, 4)
	if _, err := pool.Exec(ctx,
		`INSERT INTO gl_period_overrides (period_id, action, reason) VALUES ($1::uuid, 'CLOSE', 'fw109f own year')`,
		ownPeriod); err != nil {
		t.Fatalf("plant the leg's own override: %v", err)
	}

	// Another year's period, with an override row on it the leg never touched.
	otherPeriod, otherPlanted := fw109fOtherYearPeriod(t, pool, otherYear, 3)
	if _, err := pool.Exec(ctx,
		`INSERT INTO gl_period_overrides (period_id, action, reason) VALUES ($1::uuid, 'REOPEN', 'fw109f other year sentinel')`,
		otherPeriod); err != nil {
		t.Fatalf("plant the other year's override: %v", err)
	}
	t.Cleanup(func() {
		fw109fOtherYearCleanup(t, pool, otherPeriod, otherPlanted)
	})

	before := fw109fOverrideCount(t, pool)
	otherBefore := fw109fOverrideCountOn(t, pool, otherPeriod)

	// The cleanup under test: the one every leg runs when its sentinel year is done.
	fw109fCleanupPeriods(t, pool, ownYear)

	after := fw109fOverrideCount(t, pool)
	otherAfter := fw109fOverrideCountOn(t, pool, otherPeriod)
	t.Logf("fw109f sentinel: overrides before|%d after|%d, the other year's override before|%d after|%d",
		before, after, otherBefore, otherAfter)

	if otherAfter != 1 {
		t.Fatalf("the cleanup left %d of the other year's override rows, want 1; it must delete only its own year's overrides", otherAfter)
	}
	if got := fw109fOverrideCountOn(t, pool, ownPeriod); got != 0 {
		t.Fatalf("the leg's own period still holds %d override row(s), want 0", got)
	}
}

// TestIntegration_FW1_09f_TheYearCleanupLeavesAPeriodItDidNotPlantAlone is the year helper's own
// S5's P3 red (FROM-PRINCIPAL 349): a leg leaves the shared gate database exactly as it found it.
//
// fw109fPeriods plants the sentinel year's twelve periods with ON CONFLICT DO NOTHING, so on a
// reused box periods of the year can already be there, planted by another leg or left by an
// earlier run. The year cleanup deleted EVERY period of the year anyway: it took periods the leg
// did not plant out of the shared gate database, and the override rows hanging on them with it.
//
// The leg pre plants one period of its sentinel year with a row that references it (the
// gl_period_overrides row holds the period ON DELETE RESTRICT, so the period can only go when the
// row hanging on it goes first), runs the leg under test's own year plant and its own year
// cleanup, and fails when the pre planted period is gone. Its own cleanup removes what it
// planted.
func TestIntegration_FW1_09f_TheYearCleanupLeavesAPeriodItDidNotPlantAlone(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	const year = 2011
	const month = 2

	// The period another leg planted in this leg's own sentinel year: the year cleanup must
	// leave it.
	prePeriod, prePlanted := fw109fOtherYearPeriod(t, pool, year, month)
	// A row that references it, so the delete is visible: gl_period_overrides holds the period
	// ON DELETE RESTRICT, so the period can only go when the row hanging on it goes first.
	if _, err := pool.Exec(ctx,
		`INSERT INTO gl_period_overrides (period_id, action, reason) VALUES ($1::uuid, 'REOPEN', 'fw109f pre planted year period sentinel')`,
		prePeriod); err != nil {
		t.Fatalf("plant the pre planted period's override: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM gl_period_overrides WHERE period_id = $1::uuid AND reason = 'fw109f pre planted year period sentinel'`, prePeriod)
		if prePlanted {
			_, _ = pool.Exec(ctx, `DELETE FROM fiscal_periods WHERE id = $1::uuid`, prePeriod)
		}
	})

	before := fw109fPeriodCount(t, pool, year, month)
	overridesBefore := fw109fOverrideCountOn(t, pool, prePeriod)

	// The leg under test: its own year plant, which plants the year's missing periods, and its
	// own year cleanup, the one every leg runs when its sentinel year is done.
	fw109fPeriods(t, pool, year)
	fw109fCleanupPeriods(t, pool, year)

	after := fw109fPeriodCount(t, pool, year, month)
	overridesAfter := fw109fOverrideCountOn(t, pool, prePeriod)
	t.Logf("fw109f year cleanup: the %d-%02d periods before|%d after|%d, the override rows on it before|%d after|%d",
		year, month, before, after, overridesBefore, overridesAfter)
	if after != before {
		t.Fatalf("the year cleanup removed the %d-%02d period it did not plant (periods before|%d after|%d, the override rows on it before|%d after|%d): it must delete only the periods it planted",
			year, month, before, after, overridesBefore, overridesAfter)
	}
}

// TestIntegration_FW1_09f_ThePeriodCleanupLeavesAPeriodItDidNotPlantAlone is S5's P3 red
// (FROM-PRINCIPAL 349): a leg leaves the shared gate database exactly as it found it.
//
// The other year's period is planted with ON CONFLICT DO NOTHING, so on a reused box the row can
// already be there, and the leg's cleanup deleted it anyway: the leg took a row it did not create
// out of the shared gate database. The cleanup must remove the period only when the leg planted
// it.
//
// The leg pre plants the period with a row that references it, runs the leg under test's own
// plant (which finds the row and plants nothing) and its own cleanup, and fails when the pre
// planted period is gone. Its own safety net removes what it planted.
func TestIntegration_FW1_09f_ThePeriodCleanupLeavesAPeriodItDidNotPlantAlone(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	const year = 2009
	const month = 3

	// The period another leg (or an earlier run) left behind: the leg under test must leave it.
	prePeriod, prePlanted := fw109fOtherYearPeriod(t, pool, year, month)
	// A row that references it, so the delete is visible: gl_period_overrides holds the period
	// ON DELETE RESTRICT, so the period can only go when the row hanging on it goes first.
	if _, err := pool.Exec(ctx,
		`INSERT INTO gl_period_overrides (period_id, action, reason) VALUES ($1::uuid, 'REOPEN', 'fw109f pre planted period sentinel')`,
		prePeriod); err != nil {
		t.Fatalf("plant the pre planted period's override: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM gl_period_overrides WHERE period_id = $1::uuid`, prePeriod)
		if prePlanted {
			_, _ = pool.Exec(ctx, `DELETE FROM fiscal_periods WHERE id = $1::uuid`, prePeriod)
		}
	})

	before := fw109fPeriodCount(t, pool, year, month)

	// The leg under test: its own plant, which finds the pre planted row and plants nothing, and
	// its own cleanup.
	legPeriod, legPlanted := fw109fOtherYearPeriod(t, pool, year, month)
	if legPeriod != prePeriod {
		t.Fatalf("the leg's plant answered the period %s, want the pre planted %s", legPeriod, prePeriod)
	}
	fw109fOtherYearCleanup(t, pool, legPeriod, legPlanted)

	after := fw109fPeriodCount(t, pool, year, month)
	t.Logf("fw109f period cleanup: the %d-%02d periods before|%d after|%d, the leg planted|%t",
		year, month, before, after, legPlanted)
	if after != before {
		t.Fatalf("the leg's cleanup removed the %d-%02d period it did not plant (periods before|%d after|%d): it must delete a period only when it planted it",
			year, month, before, after)
	}
}
