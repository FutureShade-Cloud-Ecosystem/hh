//go:build integration

// fw1_09f_open_drawers_at_month_end_integration_test.go, FW1-09f (S5's P3 on PR 647, FROM-PRINCIPAL
// 341 item 1): the close pre flight's drawer check must count every drawer STILL OPEN at the month's
// end, not only the drawers OPENED inside the month.
//
// THE DEFECT. The pre flight asked the till seam for the drawers opened in [month start, next month)
// that were still OPEN. A drawer opened in an EARLIER month and never closed passed the close of
// every later month: the books sealed over a drawer that was still open. "No open drawer" means any
// drawer still open at the month's end.
//
// THE LEGS. One red: a drawer opened in the month BEFORE the month being closed, never closed, and
// the close of that later month must be refused naming the open drawer. Two honesty legs: a drawer
// opened AND closed inside the month does not block, and a drawer opened AFTER the month's end does
// not block (it blocks the month it was opened in, which is the point).
//
// THE WIRING. The drawer seam reads the REAL till repository, the same read cmd/core wires, and the
// closer is the real GL service, so the legs prove the shipped path and not a fake's arithmetic.
//
// THE SENTINEL YEARS. The months sit in past sentinel years (2019, 2021, 2023), never in the seeded
// 2026 books. A past month also keeps the legs honest in the shared gate database: every other leg
// opens its drawer NOW, so no other leg's drawer can sit open at a past month's end.
package till

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5/pgxpool"
)

// fw109fDrawerReader is the wired drawer seam: the GL close pre flight reads the till drawers
// through it, and it reads the REAL repository, the same read cmd/core wires (FW1-09f).
type fw109fDrawerReader struct{ repo *Repository }

func (r fw109fDrawerReader) OpenDrawersAt(ctx context.Context, at time.Time) (int, error) {
	return r.repo.OpenSessionsAt(ctx, at)
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
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM gl_period_overrides o USING fiscal_periods p WHERE p.year = $1`, year)
		_, _ = pool.Exec(ctx, `DELETE FROM fiscal_periods WHERE year = $1`, year)
	})
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

// fw109fDrawer plants one till drawer: opened at openedAt, closed at closedAt when that is set,
// left open otherwise. The read that matters runs through the real repository.
func fw109fDrawer(t *testing.T, pool *pgxpool.Pool, branchID, userID, station string, openedAt time.Time, closedAt *time.Time) {
	t.Helper()
	ctx := context.Background()
	status := "OPEN"
	var closedBy any
	var closedValue any
	if closedAt != nil {
		status = "CLOSED"
		closedBy = userID
		closedValue = *closedAt
	}
	var id string
	if err := pool.QueryRow(ctx,
		`INSERT INTO till_sessions (branch_id, station, status, opened_by, float_cents, opened_at, closed_by, closed_at)
		 VALUES ($1, $2, $3, $4, 0, $5, $6, $7) RETURNING id::text`,
		branchID, station, status, userID, openedAt, closedBy, closedValue).Scan(&id); err != nil {
		t.Fatalf("plant the drawer at %s: %v", station, err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM till_sessions WHERE id = $1`, id)
	})
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

// TestIntegration_FW1_09f_ADrawerOpenFromAnEarlierMonthRefusesTheClose is the red: a drawer opened
// in the month BEFORE the month being closed, never closed. The close of the later month must be
// refused, naming the open drawer, and the month must stand OPEN. Today the close seals the month
// over the drawer that is still open.
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
	for _, want := range []string{"pre flight", "1 till drawer", "still open at the month's end"} {
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
	for _, want := range []string{"pre flight", "1 till drawer", "still open at the month's end"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("the refusal %q does not name %q", msg, want)
		}
	}
	if status := fw109fPeriodStatus(t, pool, year, 8); status != "OPEN" {
		t.Fatalf("the refused close left the period %s, want OPEN", status)
	}
}
