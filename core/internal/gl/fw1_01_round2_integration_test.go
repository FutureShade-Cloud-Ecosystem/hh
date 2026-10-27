//go:build integration

// fw1_01_round2_integration_test.go, PR 612 round 2's red first legs (S2's
// review, FROM-PRINCIPAL 105): the deposit release is dated by the delivery
// (P1-2); the roll forward is a no op on a repeat and the Controller may open
// at most one year beyond its target (P2-1); the posting path never creates a
// past year, a document before the first period files late (P2-2); as of
// reads use the period a late posted journal was filed in, not its document
// date (P2-3); and the Controller's open year records its audit inside the
// year's transaction (P2-4).
package gl

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
	"github.com/jackc/pgx/v5/pgxpool"
)

// fw1AuditTx captures in-transaction audit rows, and can be made to fail.
type fw1AuditTx struct {
	fail     bool
	recorded []audit.SensitiveAccess
}

func (f *fw1AuditTx) RecordTx(_ context.Context, _ audit.Execer, a audit.SensitiveAccess) error {
	if f.fail {
		return fmt.Errorf("fw1: audit write refused")
	}
	f.recorded = append(f.recorded, a)
	return nil
}

// fw1ScrubYear removes a fiscal year's rows before and after a leg.
func fw1ScrubYear(t *testing.T, pool *pgxpool.Pool, years ...int) {
	t.Helper()
	ctx := context.Background()
	for _, y := range years {
		for _, q := range []string{
			`DELETE FROM gl_journal_lines l USING gl_journals j WHERE l.journal_id=j.id AND j.fiscal_year=$1`,
			`DELETE FROM gl_journals WHERE fiscal_year=$1`,
			`DELETE FROM gl_period_overrides o USING fiscal_periods p WHERE o.period_id=p.id AND p.year=$1`,
			`DELETE FROM fiscal_periods WHERE year=$1`,
		} {
			if _, err := pool.Exec(ctx, q, y); err != nil {
				t.Fatalf("scrub year %d: %v", y, err)
			}
		}
	}
}

// fw1EnsureHouseYear makes sure a leg's house year holds all twelve OPEN months.
func fw1EnsureHouseYear(t *testing.T, pool *pgxpool.Pool, year int) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO fiscal_periods (year, month, status)
		SELECT $1, m, 'OPEN' FROM generate_series(1, 12) AS m
		ON CONFLICT (year, month) DO UPDATE SET status = 'OPEN', closed_at = NULL, closed_by = NULL`, year); err != nil {
		t.Fatalf("ensure house year %d: %v", year, err)
	}
}

// fw1PlantFuturePeriod plants one far future period row (the shape finstmt's
// integration suite leaves in the shared CI database) and scrubs it on cleanup.
func fw1PlantFuturePeriod(t *testing.T, pool *pgxpool.Pool, year int) {
	t.Helper()
	fw1ScrubYear(t, pool, year)
	t.Cleanup(func() { fw1ScrubYear(t, pool, year) })
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO fiscal_periods (year, month, status) VALUES ($1, 1, 'OPEN') ON CONFLICT (year, month) DO NOTHING`, year); err != nil {
		t.Fatalf("plant future period %d: %v", year, err)
	}
}

// fw1ReleaseMonth reads the release journal's filed period (year, month) and posted_at.
func fw1ReleaseMonth(t *testing.T, pool *pgxpool.Pool, invoiceID string) (int, int, *time.Time) {
	t.Helper()
	var y, m int
	var posted *time.Time
	if err := pool.QueryRow(context.Background(), `
		SELECT p.year, p.month, j.posted_at
		  FROM gl_journals j JOIN fiscal_periods p ON p.id = j.period_id
		 WHERE j.idempotency_key = $1`, "deposit.released:"+invoiceID).Scan(&y, &m, &posted); err != nil {
		t.Fatalf("read the release journal: %v", err)
	}
	return y, m, posted
}

// TestIntegration_FW1_01R2_DepositReleaseDatedByTheDelivery is P1-2's and
// P2-A's red: a deposit finalized 2026-02-10 (February open), DELIVERED
// 2026-06-15, and swept when the service clock stands in July (2026-07-01)
// must post in June (the delivery's month) through the real order.Repository
// DeliveryReader, never in February (finalization) or July (processing time).
func TestIntegration_FW1_01R2_DepositReleaseDatedByTheDelivery(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	fw1EnsureHouseYear(t, pool, 2026)
	loc := houseday.DefaultZone().Location()
	clockAt := time.Date(2026, 7, 1, 12, 0, 0, 0, loc)
	svc := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager, Scope: identity.ScopeGlobal}, clockAt).
		WithInvoiceReader(seedInvoiceReader{pool: pool}).
		WithDeliveryReader(order.NewRepository(pool))
	dep := fw1PlantDeposit(t, pool, time.Date(2026, 2, 10, 12, 0, 0, 0, loc))
	var orderID string
	if err := pool.QueryRow(ctx, `SELECT order_id::text FROM invoices WHERE id=$1::uuid`, dep).Scan(&orderID); err != nil {
		t.Fatalf("read the deposit's order: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM gl_journal_lines l USING gl_journals j WHERE l.journal_id=j.id AND j.idempotency_key=$1`, "deposit.released:"+dep)
		_, _ = pool.Exec(ctx, `DELETE FROM gl_journals WHERE idempotency_key=$1`, "deposit.released:"+dep)
	})

	// Stamp the delivery on the real order row in the Postgres now()::text form
	// for 2026-06-15, while the service clock stands at 2026-07-01.
	if _, err := pool.Exec(ctx,
		`UPDATE orders SET metadata = jsonb_build_object('delivered_at', '2026-06-15 16:00:00.123456+00') WHERE id = $1`, orderID); err != nil {
		t.Fatalf("plant delivered_at stamp: %v", err)
	}
	if err := svc.consume(ctx, ev("deposit.released", map[string]any{"invoice_id": dep, "order_id": orderID})); err != nil {
		t.Fatalf("the release consume: %v", err)
	}
	y, m, posted := fw1ReleaseMonth(t, pool, dep)
	if y != 2026 || m != 6 {
		t.Fatalf("the release posts in %04d-%02d, want 2026-06 (the delivery's month, never the deposit finalization's or the processing clock's)", y, m)
	}
	if posted == nil || posted.In(loc).Format("2006-01-02") != "2026-06-15" {
		t.Fatalf("the release carries posted_at %v, want the delivery date 2026-06-15", posted)
	}
}

// TestIntegration_FW1_01R2_RollForwardIsANoOpAndTheControllerOpensOneMore is
// P2-1's red: the automatic roll forward ensures the year after the CURRENT
// HOUSE YEAR and a repeat adds nothing; the Controller's early open reaches at
// most one year beyond that, and its repeat adds nothing either.
func TestIntegration_FW1_01R2_RollForwardIsANoOpAndTheControllerOpensOneMore(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	// The band is the database's own frontier: the house year IS the latest year that exists, so the
	// roll forward's target and the Controller's one-beyond are the two years after it, whichever
	// numbers they are (the legs must not assume 2026; other suites hold far future bands here).
	house := fw1NextYear(t, pool) - 1
	target, beyond := house+1, house+2
	fw1ScrubYear(t, pool, target, beyond)
	t.Cleanup(func() { fw1ScrubYear(t, pool, target, beyond) })
	clockAt := time.Date(house, 6, 15, 12, 0, 0, 0, houseday.DefaultZone().Location())
	worker := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager, Scope: identity.ScopeGlobal}, clockAt)
	controller := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: controllerRole, Tier: identity.TierLeadership, Scope: identity.ScopeGlobal}, clockAt)

	months := func(year int) int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM fiscal_periods WHERE year=$1`, year).Scan(&n); err != nil {
			t.Fatalf("count %d: %v", year, err)
		}
		return n
	}

	if res, err := worker.EnsureNextFiscalYear(ctx); err != nil {
		t.Fatalf("the roll forward: %v", err)
	} else if res.FiscalYear != target {
		t.Fatalf("the roll forward answers %d, want %d (the year after the house year)", res.FiscalYear, target)
	}
	if months(target) != 12 {
		t.Fatalf("%d holds %d periods, want 12", target, months(target))
	}
	if _, err := worker.EnsureNextFiscalYear(ctx); err != nil {
		t.Fatalf("the repeat roll forward: %v", err)
	}
	if months(beyond) != 0 {
		t.Fatalf("a repeat roll forward created %d; a repeat is a no op", beyond)
	}

	res, err := controller.OpenNextFiscalYear(ctx, "FW1-01 round 2 early open")
	if err != nil {
		t.Fatalf("the controller open: %v", err)
	}
	if res.FiscalYear != beyond {
		t.Fatalf("the controller open answers %d, want %d (one year beyond the roll forward's target)", res.FiscalYear, beyond)
	}
	if months(beyond) != 12 {
		t.Fatalf("%d holds %d periods, want 12", beyond, months(beyond))
	}
	if _, err := controller.OpenNextFiscalYear(ctx, "FW1-01 round 2 repeat"); err != nil {
		t.Fatalf("the repeat controller open: %v", err)
	}
	if months(beyond+1) != 0 {
		t.Fatalf("a repeat controller open created %d; at most one year beyond the roll forward's target", beyond+1)
	}

	// The guards carried from round 1: a manager is refused, a blank reason is refused.
	if _, err := worker.OpenNextFiscalYear(ctx, "nope"); err == nil {
		t.Fatalf("a manager opened the next fiscal year; it is Controller only")
	}
	if _, err := controller.OpenNextFiscalYear(ctx, "   "); err == nil {
		t.Fatalf("the Controller opened the year without a reason; a reason is required")
	}
}

// TestIntegration_FW1_01R2_NeverCreatesAPastYear is P2-2's red: a system
// journal whose document is dated 2025-06-15, before the books' first year,
// creates 2025 OPEN and posts into it today. After the fix no past year is
// created: the journal files late into the earliest open month at or after its
// document's (the first period), keeping its document date.
func TestIntegration_FW1_01R2_NeverCreatesAPastYear(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	fw1EnsureHouseYear(t, pool, 2026)
	fw1ScrubYear(t, pool, 2025)
	t.Cleanup(func() { fw1ScrubYear(t, pool, 2025) })
	loc := houseday.DefaultZone().Location()
	clockAt := time.Date(2026, 6, 15, 12, 0, 0, 0, loc)
	svc := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager, Scope: identity.ScopeGlobal}, clockAt)

	id, _, err := svc.store.InsertJournal(ctx, journalDraft{
		Source:     SourceAdjustment,
		Memo:       "FW1-01 round 2 past document",
		Status:     StatusPosted,
		OccurredAt: time.Date(2025, 6, 15, 12, 0, 0, 0, loc),
		Lines: []draftLine{
			{AccountCode: "1000", DebitCents: 700, Memo: "bank"},
			{AccountCode: "1020", CreditCents: 700, Memo: "undeposited"},
		},
	})
	if err != nil {
		t.Fatalf("the past dated posting: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM gl_journal_lines l USING gl_journals j WHERE l.journal_id=j.id AND j.id=$1::uuid`, id)
		_, _ = pool.Exec(ctx, `DELETE FROM gl_journals WHERE id=$1::uuid`, id)
	})
	var past int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM fiscal_periods WHERE year=2025`).Scan(&past); err != nil {
		t.Fatal(err)
	}
	if past != 0 {
		t.Fatalf("the posting created %d periods in 2025; the books' past is never created", past)
	}
	var fy, fm int
	var posted *time.Time
	if err := pool.QueryRow(ctx, `
		SELECT p.year, p.month, j.posted_at
		  FROM gl_journals j JOIN fiscal_periods p ON p.id = j.period_id
		 WHERE j.id=$1::uuid`, id).Scan(&fy, &fm, &posted); err != nil {
		t.Fatalf("read the journal back: %v", err)
	}
	if fy != 2026 || fm != 1 {
		t.Fatalf("the past dated journal filed in %04d-%02d, want 2026-01 (the earliest open month at or after its document's)", fy, fm)
	}
	if posted == nil || posted.In(loc).Format("2006-01-02") != "2025-06-15" {
		t.Fatalf("the journal carries posted_at %v, want its document date 2025-06-15 kept", posted)
	}
}

// TestIntegration_FW1_01R2_AsOfReadsUseTheFiledPeriod is P2-3's red: a late
// posted journal (document dated in the closed May, filed in open June) still
// moves May's as of balance today, because AccountBalanceAsOf reads
// posted_at. After the fix the as of reads use the period the journal was
// filed in: May's figure is final at May's end, the journal appears from
// June's end on.
func TestIntegration_FW1_01R2_AsOfReadsUseTheFiledPeriod(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	ensurePeriods(t, pool, 2085)
	loc := houseday.DefaultZone().Location()
	svc := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager, Scope: identity.ScopeGlobal}, time.Date(2085, 7, 1, 12, 0, 0, 0, loc))
	if _, err := svc.ClosePeriod(ctx, periodID(t, pool, 2085, 5), "round 2 closed May"); err != nil {
		t.Fatalf("close May 2085: %v", err)
	}
	post := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: "FINANCIAL_CONTROLLER", Tier: identity.TierLeadership, Scope: identity.ScopeGlobal}, time.Date(2085, 7, 1, 12, 0, 0, 0, loc)).
		WithInvoiceReader(seedInvoiceReader{pool: pool})
	base, _, err := post.store.AccountBalanceAsOf(ctx, "1100", time.Date(2085, 5, 1, 0, 0, 0, 0, loc), "")
	if err != nil {
		t.Fatalf("baseline 1100: %v", err)
	}
	dep := fw1PlantDeposit(t, pool, time.Date(2085, 5, 10, 12, 0, 0, 0, loc))
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM gl_journal_lines l USING gl_journals j WHERE l.journal_id=j.id AND j.idempotency_key=$1`, "ar.invoice.finalized:"+dep)
		_, _ = pool.Exec(ctx, `DELETE FROM gl_journals WHERE idempotency_key=$1`, "ar.invoice.finalized:"+dep)
	})
	if err := post.consume(ctx, ev("ar.invoice.finalized", map[string]any{"invoice_id": dep})); err != nil {
		t.Fatalf("the late finalization consume: %v", err)
	}
	var fy, fm int
	if err := pool.QueryRow(ctx, `
		SELECT p.year, p.month FROM gl_journals j JOIN fiscal_periods p ON p.id=j.period_id
		 WHERE j.idempotency_key=$1`, "ar.invoice.finalized:"+dep).Scan(&fy, &fm); err != nil {
		t.Fatalf("read the late journal: %v", err)
	}
	if fy != 2085 || fm != 6 {
		t.Fatalf("the late journal filed in %04d-%02d, want 2085-06 (the leg's premise)", fy, fm)
	}

	asOf := func(at time.Time) int64 {
		t.Helper()
		gl, _, err := post.store.AccountBalanceAsOf(ctx, "1100", at, "")
		if err != nil {
			t.Fatalf("as of %s: %v", at, err)
		}
		return gl - base
	}
	if got := asOf(time.Date(2085, 5, 31, 23, 59, 59, 0, loc)); got != 0 {
		t.Fatalf("May's as of 1100 holds %d after May closed; a closed month's figures are final", got)
	}
	if got := asOf(time.Date(2085, 6, 30, 23, 59, 59, 0, loc)); got != 0 {
		t.Fatalf("June's as of 1100 holds %d; the journal was filed in June, not dated into May", got)
	}
	if got := asOf(time.Date(2085, 7, 31, 23, 59, 59, 0, loc)); got != 4000 {
		t.Fatalf("July's as of 1100 holds %d, want 4000 (the late journal shows from its filed period's end on)", got)
	}
}

// TestIntegration_FW1_01R2_OpenYearAuditInTheYearsTransaction is P2-4's red:
// when the audit write fails, the Controller's open still commits the year
// today. After the fix the audit row is recorded inside the year's
// transaction, so a failed audit rolls the year back, and a clean open leaves
// the audit row and the year together.
func TestIntegration_FW1_01R2_OpenYearAuditInTheYearsTransaction(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	house := fw1NextYear(t, pool) - 1
	target, beyond := house+1, house+2
	fw1ScrubYear(t, pool, target, beyond)
	t.Cleanup(func() { fw1ScrubYear(t, pool, target, beyond) })
	controller := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: controllerRole, Tier: identity.TierLeadership, Scope: identity.ScopeGlobal}, time.Date(house, 6, 15, 12, 0, 0, 0, houseday.DefaultZone().Location()))

	years := func(year int) int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM fiscal_periods WHERE year=$1`, year).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	failing := &fw1AuditTx{fail: true}
	bad := controller.WithAuditTxRecorder(failing)
	if _, err := bad.OpenNextFiscalYear(ctx, "FW1-01 round 2 audit proof"); err == nil {
		t.Fatalf("the open succeeded with the audit write refused; the audit must hold the year")
	}
	if years(target) != 0 || years(beyond) != 0 {
		t.Fatalf("the refused audit left years behind (%d, %d); the years roll back with their audit", years(target), years(beyond))
	}

	good := &fw1AuditTx{}
	ok := controller.WithAuditTxRecorder(good)
	if _, err := ok.OpenNextFiscalYear(ctx, "FW1-01 round 2 audit proof"); err != nil {
		t.Fatalf("the open with a working audit: %v", err)
	}
	if years(target) != 12 || years(beyond) != 12 {
		t.Fatalf("the open's years hold (%d, %d) periods, want 12 and 12", years(target), years(beyond))
	}
	if len(good.recorded) != 1 {
		t.Fatalf("%d audit rows recorded, want 1", len(good.recorded))
	}
	if good.recorded[0].EntityID != fmt.Sprintf("fiscal_year:%d", beyond) {
		t.Fatalf("the audit names %q, want fiscal_year:%d (the open's target, one beyond the roll forward's)", good.recorded[0].EntityID, beyond)
	}
}
