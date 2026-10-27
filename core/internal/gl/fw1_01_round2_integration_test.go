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
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
	"github.com/jackc/pgx/v5/pgxpool"
)

// fw1Delivery answers one fixed delivery stamp.
type fw1Delivery struct{ at time.Time }

func (f fw1Delivery) DeliveredAt(_ context.Context, _ string) (time.Time, bool, error) {
	return f.at, true, nil
}

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

// TestIntegration_FW1_01R2_DepositReleaseDatedByTheDelivery is P1-2's red: a
// deposit finalized 2026-02-10 (February open) and DELIVERED 2026-06-15 posts
// as February revenue today. After the fix the release is dated by the
// delivery (the fact MarkDelivered records) and posts in June.
func TestIntegration_FW1_01R2_DepositReleaseDatedByTheDelivery(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	clockAt := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	svc := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager}, clockAt).
		WithInvoiceReader(seedInvoiceReader{pool: pool}).
		WithDeliveryReader(fw1Delivery{at: clockAt})
	dep := fw1PlantDeposit(t, pool, time.Date(2026, 2, 10, 12, 0, 0, 0, time.UTC))
	var orderID string
	if err := pool.QueryRow(ctx, `SELECT order_id::text FROM invoices WHERE id=$1::uuid`, dep).Scan(&orderID); err != nil {
		t.Fatalf("read the deposit's order: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM gl_journal_lines l USING gl_journals j WHERE l.journal_id=j.id AND j.idempotency_key=$1`, "deposit.released:"+dep)
		_, _ = pool.Exec(ctx, `DELETE FROM gl_journals WHERE idempotency_key=$1`, "deposit.released:"+dep)
	})

	if err := svc.consume(ctx, ev("deposit.released", map[string]any{"invoice_id": dep, "order_id": orderID})); err != nil {
		t.Fatalf("the release consume: %v", err)
	}
	y, m, posted := fw1ReleaseMonth(t, pool, dep)
	if y != 2026 || m != 6 {
		t.Fatalf("the release posts in %04d-%02d, want 2026-06 (the delivery's month, never the deposit finalization's)", y, m)
	}
	if posted == nil || posted.UTC().Format("2006-01-02") != "2026-06-15" {
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
	fw1ScrubYear(t, pool, 2027, 2028, 2029)
	t.Cleanup(func() { fw1ScrubYear(t, pool, 2027, 2028, 2029) })
	clockAt := time.Date(2026, 6, 15, 12, 0, 0, 0, houseday.DefaultZone().Location())
	worker := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager}, clockAt)
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
	} else if res.FiscalYear != 2027 {
		t.Fatalf("the roll forward answers %d, want 2027 (the year after the house year)", res.FiscalYear)
	}
	if months(2027) != 12 {
		t.Fatalf("2027 holds %d periods, want 12", months(2027))
	}
	if _, err := worker.EnsureNextFiscalYear(ctx); err != nil {
		t.Fatalf("the repeat roll forward: %v", err)
	}
	if months(2028) != 0 {
		t.Fatalf("a repeat roll forward created 2028; a repeat is a no op")
	}

	res, err := controller.OpenNextFiscalYear(ctx, "FW1-01 round 2 early open")
	if err != nil {
		t.Fatalf("the controller open: %v", err)
	}
	if res.FiscalYear != 2028 {
		t.Fatalf("the controller open answers %d, want 2028 (one year beyond the roll forward's target)", res.FiscalYear)
	}
	if months(2028) != 12 {
		t.Fatalf("2028 holds %d periods, want 12", months(2028))
	}
	if _, err := controller.OpenNextFiscalYear(ctx, "FW1-01 round 2 repeat"); err != nil {
		t.Fatalf("the repeat controller open: %v", err)
	}
	if months(2029) != 0 {
		t.Fatalf("a repeat controller open created 2029; at most one year beyond the roll forward's target")
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
	fw1ScrubYear(t, pool, 2025)
	t.Cleanup(func() { fw1ScrubYear(t, pool, 2025) })
	clockAt := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	svc := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager}, clockAt)

	id, _, err := svc.store.InsertJournal(ctx, journalDraft{
		Source:     SourceAdjustment,
		Memo:       "FW1-01 round 2 past document",
		Status:     StatusPosted,
		OccurredAt: time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC),
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
	if posted == nil || posted.UTC().Format("2006-01-02") != "2025-06-15" {
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
	svc := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager}, time.Date(2085, 7, 1, 12, 0, 0, 0, time.UTC))
	if _, err := svc.ClosePeriod(ctx, periodID(t, pool, 2085, 5), "round 2 closed May"); err != nil {
		t.Fatalf("close May 2085: %v", err)
	}
	post := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: "FINANCIAL_CONTROLLER", Tier: identity.TierLeadership, Scope: identity.ScopeGlobal}, time.Date(2085, 7, 1, 12, 0, 0, 0, time.UTC)).
		WithInvoiceReader(seedInvoiceReader{pool: pool})
	dep := fw1PlantDeposit(t, pool, time.Date(2085, 5, 10, 12, 0, 0, 0, time.UTC))
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
		return gl
	}
	if got := asOf(time.Date(2085, 5, 31, 23, 59, 59, 0, time.UTC)); got != 0 {
		t.Fatalf("May's as of 1100 holds %d after May closed; a closed month's figures are final", got)
	}
	if got := asOf(time.Date(2085, 6, 30, 23, 59, 59, 0, time.UTC)); got != 0 {
		t.Fatalf("June's as of 1100 holds %d; the journal was filed in June, not dated into May", got)
	}
	if got := asOf(time.Date(2085, 7, 31, 23, 59, 59, 0, time.UTC)); got != 4000 {
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
	fw1ScrubYear(t, pool, 2027, 2028)
	t.Cleanup(func() { fw1ScrubYear(t, pool, 2027, 2028) })
	controller := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: controllerRole, Tier: identity.TierLeadership, Scope: identity.ScopeGlobal}, time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC))

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
	if years(2027) != 0 || years(2028) != 0 {
		t.Fatalf("the refused audit left years behind (%d, %d); the years roll back with their audit", years(2027), years(2028))
	}

	good := &fw1AuditTx{}
	ok := controller.WithAuditTxRecorder(good)
	if _, err := ok.OpenNextFiscalYear(ctx, "FW1-01 round 2 audit proof"); err != nil {
		t.Fatalf("the open with a working audit: %v", err)
	}
	if years(2027) != 12 || years(2028) != 12 {
		t.Fatalf("the open's years hold (%d, %d) periods, want 12 and 12", years(2027), years(2028))
	}
	if len(good.recorded) != 1 {
		t.Fatalf("%d audit rows recorded, want 1", len(good.recorded))
	}
	if good.recorded[0].EntityID != "fiscal_year:2028" {
		t.Fatalf("the audit names %q, want fiscal_year:2028 (the open's target, one beyond the roll forward's)", good.recorded[0].EntityID)
	}
}
