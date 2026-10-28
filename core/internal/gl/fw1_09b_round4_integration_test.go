//go:build integration

// fw1_09b_round4_integration_test.go, FW1-09b round 4's red first legs
// (S3's review of PR 613, FROM-PRINCIPAL 122): the year end posts into its
// own December whatever its status and runs once, the year end runs the pre
// flight for every month it seals, the pre flight reads the month END in the
// dealer's zone and the queue over the month's window, and the recurring
// run's own write is fenced like every write.
package gl

import (
	"context"
	"strings"
	"testing"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
	"github.com/jackc/pgx/v5/pgxpool"
)

// fw1ScrubAfter removes every fiscal year AFTER the threshold, so a leg's
// stray (the bug being proven posts past its year) cannot land in another
// suite's open periods and the earliest open month at or after the year's end
// is the leg's own to control. The seeded 2026 books and every sentinel band
// below the threshold stand.
func fw1ScrubAfter(t *testing.T, pool *pgxpool.Pool, threshold int) {
	t.Helper()
	ctx := context.Background()
	for _, q := range []string{
		`DELETE FROM gl_journal_lines l USING gl_journals j WHERE l.journal_id=j.id AND j.fiscal_year > $1`,
		`DELETE FROM gl_journals WHERE fiscal_year > $1`,
		`DELETE FROM gl_period_overrides o USING fiscal_periods p WHERE o.period_id=p.id AND p.year > $1`,
		`DELETE FROM fiscal_periods WHERE year > $1`,
	} {
		if _, err := pool.Exec(ctx, q, threshold); err != nil {
			t.Fatalf("scrub after %d: %v", threshold, err)
		}
	}
}

// fw1SeedIncome posts one revenue journal for the year (income only, the
// probe's shape).
func fw1SeedIncome(t *testing.T, pool *pgxpool.Pool, year int, cents int64) {
	t.Helper()
	ctx := context.Background()
	svc := svcAt(pool, identity.StaffContext{UserID: aUser(t, pool), RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager, Scope: identity.ScopeGlobal}, time.Date(year, 6, 15, 12, 0, 0, 0, time.UTC))
	if _, err := svc.PostExternal(ctx, ExternalJournal{
		Source: SourceAdjustment, IdempotencyKey: "r4-income",
		Lines: []ExternalLine{
			{AccountCode: "1000", DebitCents: cents, Memo: "bank"},
			{AccountCode: "4000", CreditCents: cents, Memo: "revenue"},
		},
	}); err != nil {
		t.Fatalf("seed the income: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM gl_journal_lines l USING gl_journals j WHERE l.journal_id=j.id AND j.idempotency_key='r4-income'`)
		_, _ = pool.Exec(ctx, `DELETE FROM gl_journals WHERE idempotency_key='r4-income'`)
	})
}

// fw1CloseEveryMonth closes the year's twelve periods through the store (the
// month close, unfenced at the store).
func fw1CloseEveryMonth(t *testing.T, pool *pgxpool.Pool, year int, actor string) {
	t.Helper()
	repo := NewRepository(pool)
	for m := 1; m <= 12; m++ {
		if _, err := repo.ClosePeriod(context.Background(), periodID(t, pool, year, m), actor, "round 4 month close"); err != nil {
			t.Fatalf("close %04d-%02d: %v", year, m, err)
		}
	}
}

// fw1ClosingFacts reads a year's CLOSING journals: how many, and the first
// one's filed period.
func fw1ClosingFacts(t *testing.T, pool *pgxpool.Pool, year int) (int, int, int) {
	t.Helper()
	var n, fy, fm int
	err := pool.QueryRow(context.Background(), `
		SELECT count(*),
		       COALESCE(MAX(p.year) FILTER (WHERE p.year = $1), 0),
		       COALESCE(MAX(p.month) FILTER (WHERE p.year = $1), 0)
		  FROM gl_journals j JOIN fiscal_periods p ON p.id = j.period_id
		 WHERE j.fiscal_year = $1 AND j.source = 'CLOSING'`, year).Scan(&n, &fy, &fm)
	if err != nil {
		t.Fatalf("read the closing journals: %v", err)
	}
	return n, fy, fm
}

// TestIntegration_FW1_09bR4_TheYearEndPostsIntoItsOwnDecemberAndOnceOnly is
// probe 3's red: with every month of 3074 closed by the month close, the year
// end's closing journal lands in 3075-01 today (the late posted rule moving it
// out of its closed December), a second run posts again, and $700.00 of income
// reads $1,400.00 of retained earnings across two years. After the fix the
// year end is the one act allowed to write its own closed December, a second
// run is a no op answering the first, and $700.00 of income gives $700.00 of
// retained earnings in that year.
func TestIntegration_FW1_09bR4_TheYearEndPostsIntoItsOwnDecemberAndOnceOnly(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	ensurePeriods(t, pool, 3074)
	fw1ScrubAfter(t, pool, 3074)
	t.Cleanup(func() { fw1ScrubAfter(t, pool, 3074) })
	fw1SeedIncome(t, pool, 3074, 700)
	fw1CloseEveryMonth(t, pool, 3074, user)
	closer := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: controllerRole, Tier: identity.TierLeadership, Scope: identity.ScopeGlobal}, time.Date(3075, 1, 5, 12, 0, 0, 0, time.UTC))

	first, err := closer.CloseYear(ctx, 3074, "round 4 december proof")
	if err != nil {
		t.Fatalf("the year end: %v", err)
	}
	n, fy, fm := fw1ClosingFacts(t, pool, 3074)
	if n != 1 {
		t.Fatalf("%d closing journals for 3074, want 1", n)
	}
	if fy != 3074 || fm != 12 {
		t.Fatalf("the closing journal sits in %04d-%02d, want 3074-12 (the closed year's own December, whatever its status)", fy, fm)
	}
	var re int64
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(COALESCE(l.credit_cents,0) - COALESCE(l.debit_cents,0)), 0)
		  FROM gl_journal_lines l
		  JOIN gl_journals j ON j.id = l.journal_id
		  JOIN gl_accounts a ON a.id = l.account_id
		 WHERE j.fiscal_year = 3074 AND j.source = 'CLOSING' AND a.code = '3900'`).Scan(&re); err != nil {
		t.Fatal(err)
	}
	if re != 700 {
		t.Fatalf("retained earnings in 3074 = %d, want 700 (the year's income stays in its year)", re)
	}
	var next int64
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM gl_journals j JOIN fiscal_periods p ON p.id = j.period_id
		 WHERE p.year = 3075 AND j.source = 'CLOSING'`).Scan(&next); err != nil {
		t.Fatal(err)
	}
	if next != 0 {
		t.Fatalf("%d closing journals sit in 3075; the next year's P&L must not hold them", next)
	}

	second, err := closer.CloseYear(ctx, 3074, "round 4 repeat")
	if err != nil {
		t.Fatalf("the repeat year end: %v", err)
	}
	if second.NetIncomeCents != first.NetIncomeCents || len(second.ClosingJournalIDs) != len(first.ClosingJournalIDs) {
		t.Fatalf("the repeat answered %+v, want the first run's answer %+v", second, first)
	}
	n2, _, _ := fw1ClosingFacts(t, pool, 3074)
	if n2 != 1 {
		t.Fatalf("%d closing journals after the repeat, want 1 (a second year end is a no op)", n2)
	}
}

// fw1WindowDrawers answers 3 open drawers for exactly one window, and records
// the windows it was asked.
type fw1WindowDrawers struct {
	year, month int
	calls       [][2]time.Time
}

func (f *fw1WindowDrawers) OpenDrawers(_ context.Context, from, to time.Time) (int, error) {
	f.calls = append(f.calls, [2]time.Time{from, to})
	if from.UTC().Year() == f.year && int(from.UTC().Month()) == f.month {
		return 3, nil
	}
	return 0, nil
}

// fw1TieSpy records the asOf instants the ties were read at.
type fw1TieSpy struct {
	asOfs []time.Time
}

func (f *fw1TieSpy) ARBalanceCents(_ context.Context, asOf time.Time, _ string) (int64, error) {
	f.asOfs = append(f.asOfs, asOf)
	return 0, nil
}

// fw1FixedStock answers one fixed stock value.
type fw1FixedStock struct{ cents int64 }

func (f fw1FixedStock) StockValueCents(_ context.Context, _ time.Time, _ string) (int64, error) {
	return f.cents, nil
}

// TestIntegration_FW1_09bR4_TheYearEndRunsThePreFlight is probe 2's red: three
// open drawers in December and the year end still seals the year today. After
// the fix the year end runs the pre flight for every month it will seal and
// refuses in words naming the month, and the stock side says in words that it
// reads the month's end (W2-5's as of read, the basis both sides share).
func TestIntegration_FW1_09bR4_TheYearEndRunsThePreFlight(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	ensurePeriods(t, pool, 3076)
	fw1ScrubAfter(t, pool, 3076)
	t.Cleanup(func() { fw1ScrubAfter(t, pool, 3076) })
	// The stock tie must hold for every month the pre flight walks, so the
	// fake answers the ledger's own 1200 inception to date balance (the same
	// figure at every month end of 3076: this leg plants nothing, and fw1ScrubAfter
	// removed every year after it). A fixed zero fails January first on the
	// shared gate database, before the walk ever reaches December (round 6).
	zone, err := NewRepository(pool).BusinessZone(ctx)
	if err != nil {
		t.Fatal(err)
	}
	stock1200, _, err := NewRepository(pool).AccountBalanceAsOf(ctx, "1200", time.Date(3077, 1, 1, 0, 0, 0, 0, zone.Location()), "")
	if err != nil {
		t.Fatal(err)
	}
	drawers := &fw1WindowDrawers{year: 3076, month: 12}
	closer := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: controllerRole, Tier: identity.TierLeadership, Scope: identity.ScopeGlobal}, time.Date(3077, 1, 5, 12, 0, 0, 0, time.UTC)).
		WithDrawerReader(drawers).
		WithStockReader(fw1FixedStock{cents: stock1200})

	_, err = closer.CloseYear(ctx, 3076, "round 4 pre flight proof")
	if err == nil {
		t.Fatalf("the year end sealed a year with three open drawers in December; it must run the pre flight")
	}
	msg := err.Error()
	for _, want := range []string{"3076-12", "3 till drawers", "the month's end"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("the refusal %q does not name %q", msg, want)
		}
	}
	var open int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM fiscal_periods WHERE year=3076 AND status='OPEN'`).Scan(&open); err != nil {
		t.Fatal(err)
	}
	if open != 12 {
		t.Fatalf("%d periods of 3076 stand OPEN after the refusal, want 12", open)
	}
}

// TestIntegration_FW1_09bR4_ThePreFlightReadsTheMonthEndAndTheQueueWindow is
// probe 1's red over the REAL outbox store: a DEAD payment.received dated in
// the month, and the close succeeding today (the queue window was empty and
// the ties read the month's first instant). After the fix the queue is read
// over [month start, next month start), the ties at the first instant of the
// next month in the dealer's zone, and the fakes hold the pre flight to both.
func TestIntegration_FW1_09bR4_ThePreFlightReadsTheMonthEndAndTheQueueWindow(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	ensurePeriods(t, pool, 3078)
	mid := time.Date(3078, 6, 15, 12, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(ctx,
		`INSERT INTO events_outbox (topic, payload, occurred_at, status, attempts, last_error)
		 VALUES ('payment.received', '{}'::jsonb, $1, 'DEAD', 9, 'round 4 proof')`, mid); err != nil {
		t.Fatalf("plant the dead event: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM events_outbox WHERE topic='payment.received' AND occurred_at=$1`, mid)
	})
	tie := &fw1TieSpy{}
	svc := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: "FINANCIAL_CONTROLLER", Tier: identity.TierLeadership, Scope: identity.ScopeGlobal}, mid).
		WithQueueReader(notify.NewPgOutboxStore(pool)).
		WithARSubledger(tie).
		WithDrawerReader(&fw1WindowDrawers{year: 0, month: 0})

	if _, err := svc.ClosePeriod(ctx, periodID(t, pool, 3078, 6), "round 4 queue proof"); err == nil {
		t.Fatalf("the month closed with a dead posting event in it; the queue window must find it")
	} else if got := err.Error(); !strings.Contains(got, "1 posting event") {
		t.Fatalf("the refusal %q does not name the dead event", got)
	}
	if len(tie.asOfs) == 0 {
		t.Fatalf("the AR tie was never read")
	}
	zone, err := NewRepository(pool).BusinessZone(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(3078, 7, 1, 0, 0, 0, 0, zone.Location())
	for _, got := range tie.asOfs {
		if !got.Equal(want) {
			t.Fatalf("the AR tie read as of %s, want the first instant of the next month in the dealer's zone (%s)", got, want)
		}
	}
}

// TestIntegration_FW1_09bR4_RecurringRunNeedsDealerWide is the lead's add: the
// recurring run's own create is a write like the others, and its journal
// carries no branch (the consolidated books), so a scoped caller is refused.
// A dealer wide caller lands the DRAFT.
func TestIntegration_FW1_09bR4_RecurringRunNeedsDealerWide(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	trenton, _ := fw1TwoBranches(t, pool)
	if _, err := pool.Exec(ctx, `DELETE FROM gl_recurring_templates WHERE name='R4 rent'`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM gl_recurring_templates WHERE name='R4 rent'`)
		_, _ = pool.Exec(ctx, `DELETE FROM gl_journal_lines l USING gl_journals j WHERE l.journal_id=j.id AND j.memo LIKE 'Recurring: R4 rent%'`)
		_, _ = pool.Exec(ctx, `DELETE FROM gl_journals WHERE memo LIKE 'Recurring: R4 rent%'`)
	})
	if _, err := pool.Exec(ctx,
		`INSERT INTO gl_recurring_templates (name, memo, lines) VALUES ('R4 rent', 'R4 rent',
		  '[{"account_code":"6100","debit_cents":120000,"memo":"rent"},{"account_code":"1000","credit_cents":120000,"memo":"bank"}]'::jsonb)`); err != nil {
		t.Fatalf("plant the template: %v", err)
	}
	var tplID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM gl_recurring_templates WHERE name='R4 rent'`).Scan(&tplID); err != nil {
		t.Fatal(err)
	}
	scoped := svcAt(pool, fw1Scoped(user, trenton), time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC))
	if _, err := scoped.RunRecurring(ctx, tplID); err == nil {
		t.Fatalf("a branch scoped manager ran a recurring template into the consolidated books; the write fence must refuse it")
	} else if got := err.Error(); !strings.Contains(got, "does not cover") {
		t.Fatalf("the refusal = %q, want plain words about the books the sign in covers", got)
	}
	var n int64
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM gl_journals WHERE memo LIKE 'Recurring: R4 rent%'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("%d recurring journals committed behind the refused run", n)
	}
	global := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: "GENERAL_MANAGER", Tier: identity.TierLeadership, Scope: identity.ScopeGlobal}, time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC))
	j, err := global.RunRecurring(ctx, tplID)
	if err != nil {
		t.Fatalf("the dealer wide run: %v", err)
	}
	if j.Status != StatusDraft {
		t.Fatalf("the recurring run landed %s, want a DRAFT for review", j.Status)
	}
}
