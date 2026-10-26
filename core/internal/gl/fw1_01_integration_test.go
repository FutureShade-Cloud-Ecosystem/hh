//go:build integration

// fw1_01_integration_test.go, FW1-01's red first legs on the real migrated
// and seeded database: the fiscal year rolls forward (a posting on the first
// day after the seeded years is refused today), and a system journal carries
// its own document date (a deposit released for a document dated last month
// lands on the processing day today, and is refused outright when that month
// is closed). The manual journal into a closed month stays refused.
//
// ISOLATION. Fiscal years 2085 and 2088, outside the seeded 2026 periods and
// every band the package's other integration tests use; the year roll leg
// posts into MAX(year)+1 of whatever the shared database holds and scrubs
// only that year.
//
//	Run: DATABASE_URL='postgres://.../db?sslmode=disable' go test -tags=integration -count=1 -run FW1_01 ./internal/gl/
package gl

import (
	"context"
	"fmt"
	"testing"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5/pgxpool"
)

// fw1NextYear scrubs and names the first year after the latest the database
// holds, so the leg posts on the exact boundary the roll forward must cover
// whatever bands earlier tests left behind.
func fw1NextYear(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	ctx := context.Background()
	var max int
	if err := pool.QueryRow(ctx, `SELECT COALESCE(MAX(year), 2025) FROM fiscal_periods`).Scan(&max); err != nil {
		t.Fatalf("read the latest fiscal year: %v", err)
	}
	next := max + 1
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM gl_journal_lines l USING gl_journals j WHERE l.journal_id=j.id AND j.fiscal_year=$1`, next)
		_, _ = pool.Exec(c, `DELETE FROM gl_journals WHERE fiscal_year=$1`, next)
		_, _ = pool.Exec(c, `DELETE FROM gl_period_overrides o USING fiscal_periods p WHERE o.period_id=p.id AND p.year=$1`, next)
		_, _ = pool.Exec(c, `DELETE FROM fiscal_periods WHERE year=$1`, next)
	})
	_, _ = pool.Exec(ctx, `DELETE FROM gl_journal_lines l USING gl_journals j WHERE l.journal_id=j.id AND j.fiscal_year=$1`, next)
	_, _ = pool.Exec(ctx, `DELETE FROM gl_journals WHERE fiscal_year=$1`, next)
	_, _ = pool.Exec(ctx, `DELETE FROM gl_period_overrides o USING fiscal_periods p WHERE o.period_id=p.id AND p.year=$1`, next)
	_, _ = pool.Exec(ctx, `DELETE FROM fiscal_periods WHERE year=$1`, next)
	return next
}

// TestIntegration_FW1_01_YearRollForward_PostsOnTheFirstDayAfterTheSeededYear
// is the year leg's red: today the GL refuses a posting dated the first day
// after the latest seeded year with 422 "no fiscal period"; after the fix the
// GL has created that following year idempotently and the journal posts into
// its January.
func TestIntegration_FW1_01_YearRollForward_PostsOnTheFirstDayAfterTheSeededYear(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	next := fw1NextYear(t, pool)
	at := time.Date(next, 1, 1, 12, 0, 0, 0, time.UTC)
	svc := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager}, at)

	id, _, err := svc.store.InsertJournal(ctx, journalDraft{
		Source:     SourceAdjustment,
		Memo:       "FW1-01 year roll",
		Status:     StatusPosted,
		OccurredAt: at,
		Lines: []draftLine{
			{AccountCode: "1000", DebitCents: 1200, Memo: "bank"},
			{AccountCode: "1020", CreditCents: 1200, Memo: "undeposited"},
		},
	})
	if err != nil {
		t.Fatalf("posting on %04d-01-01: %v (the next fiscal year must exist ahead of need)", next, err)
	}
	var fy int
	if err := pool.QueryRow(ctx, `SELECT fiscal_year FROM gl_journals WHERE id=$1::uuid`, id).Scan(&fy); err != nil {
		t.Fatalf("read the journal back: %v", err)
	}
	if fy != next {
		t.Fatalf("the journal posts in fiscal year %d, want %d", fy, next)
	}
	var months int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM fiscal_periods WHERE year=$1`, next).Scan(&months); err != nil {
		t.Fatalf("count the new year's periods: %v", err)
	}
	if months != 12 {
		t.Fatalf("the rolled forward year holds %d periods, want 12", months)
	}
}

// fw1PlantDeposit plants one FINALIZED supplier deposit document dated docAt
// and returns its id, mirroring the deposit release proof's fixture.
func fw1PlantDeposit(t *testing.T, pool *pgxpool.Pool, docAt time.Time) string {
	t.Helper()
	ctx := context.Background()
	suffix := time.Now().UnixNano()
	var cust, branch string
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name, customer_type, payment_terms_days, credit_limit_cents)
		 VALUES ($1, 'FW1-01 posting date proof', 'CONTRACTOR', 30, 5000000) RETURNING id::text`,
		fmt.Sprintf("F1-%d", suffix)).Scan(&cust); err != nil {
		t.Fatalf("plant the customer: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations LIMIT 1`).Scan(&branch); err != nil {
		t.Skipf("no seeded branch: %v", err)
	}
	var ord, inv string
	if err := pool.QueryRow(ctx,
		`INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, subtotal_cents, total_cents)
		 VALUES ($1, $2, $3, 'CONFIRMED', 'PICKUP', 4000, 4000) RETURNING id::text`,
		fmt.Sprintf("#F1-%d", suffix), cust, branch).Scan(&ord); err != nil {
		t.Fatalf("plant the order: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO invoices (invoice_no, customer_id, order_id, branch_id, status, due_date,
		                       subtotal_cents, tax_cents, total_cents, balance_cents, source_kind, finalized_at)
		 VALUES ($1, $2, $3, $4, 'FINALIZED', CURRENT_DATE, 4000, 0, 4000, 4000, 'SUPPLIER_DEPOSIT', $5)
		 RETURNING id::text`,
		fmt.Sprintf("INV-F1-%d", suffix), cust, ord, branch, docAt).Scan(&inv); err != nil {
		t.Fatalf("plant the deposit document: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM invoices WHERE id = $1`, inv)
		_, _ = pool.Exec(c, `DELETE FROM orders WHERE id = $1`, ord)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1`, cust)
	})
	return inv
}

// fw1ReleaseJournalFor reads one keyed journal's period month and posted_at.
func fw1ReleaseJournalFor(t *testing.T, pool *pgxpool.Pool, key string) (int, *time.Time) {
	t.Helper()
	var pmonth int
	var posted *time.Time
	if err := pool.QueryRow(context.Background(), `
		SELECT p.month, j.posted_at
		  FROM gl_journals j JOIN fiscal_periods p ON p.id = j.period_id
		 WHERE j.idempotency_key = $1`, key).Scan(&pmonth, &posted); err != nil {
		t.Fatalf("read the keyed journal: %v", err)
	}
	return pmonth, posted
}

// fw1ReleaseJournal reads the release journal's period month and posted_at.
func fw1ReleaseJournal(t *testing.T, pool *pgxpool.Pool, invoiceID string) (periodMonth int, postedAt *time.Time) {
	t.Helper()
	var pmonth int
	var posted *time.Time
	if err := pool.QueryRow(context.Background(), `
		SELECT p.month, j.posted_at
		  FROM gl_journals j JOIN fiscal_periods p ON p.id = j.period_id
		 WHERE j.idempotency_key = $1`, "deposit.released:"+invoiceID).Scan(&pmonth, &posted); err != nil {
		t.Fatalf("read the release journal: %v", err)
	}
	return pmonth, posted
}

// TestIntegration_FW1_01_SystemJournalTakesItsDocumentsDate is the posting
// date leg's red on the open month: a deposit released while the clock stands
// in June for a document finalized in May (May still OPEN) must post in May
// on the document's date; today it lands on the processing day, June.
func TestIntegration_FW1_01_SystemJournalTakesItsDocumentsDate(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	ensurePeriods(t, pool, 2085)
	docAt := time.Date(2085, 5, 10, 12, 0, 0, 0, time.UTC)
	clockAt := time.Date(2085, 6, 15, 12, 0, 0, 0, time.UTC)
	svc := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager}, clockAt).
		WithInvoiceReader(seedInvoiceReader{pool: pool})
	dep := fw1PlantDeposit(t, pool, docAt)

	if err := svc.consume(ctx, ev("deposit.released", map[string]any{"invoice_id": dep})); err != nil {
		t.Fatalf("the release consume: %v", err)
	}
	pmonth, posted := fw1ReleaseJournal(t, pool, dep)
	if pmonth != 5 {
		t.Fatalf("the release journal posts in period month %d, want 5 (the document's own month)", pmonth)
	}
	if posted == nil || posted.UTC().Format("2006-01-02") != "2085-05-10" {
		t.Fatalf("the release journal carries posted_at %v, want the document date 2085-05-10", posted)
	}
}

// TestIntegration_FW1_01_SystemJournalForAClosedMonthIsNeverRefused is the
// posting date leg's red on the closed month: May is CLOSED, the document is
// dated May, the clock stands in open June; the release must post into the
// earliest OPEN month (June), keep the document date on the journal, and be
// listed as late posted. Today the consume is refused outright.
func TestIntegration_FW1_01_SystemJournalForAClosedMonthIsNeverRefused(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	ensurePeriods(t, pool, 2088)
	svc := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager}, time.Date(2088, 6, 15, 12, 0, 0, 0, time.UTC))
	if _, err := svc.ClosePeriod(ctx, periodID(t, pool, 2088, 5), "FW1-01 closed month proof"); err != nil {
		t.Fatalf("close May 2088: %v", err)
	}
	docAt := time.Date(2088, 5, 10, 12, 0, 0, 0, time.UTC)
	clockAt := time.Date(2088, 6, 15, 12, 0, 0, 0, time.UTC)
	// The earliest OPEN period the database holds (the seeded 2026 months stand open in this
	// database, so the earliest open is not necessarily June: the ruling names the earliest open
	// month, whichever it is).
	var wantMonth, wantYear int
	if err := pool.QueryRow(ctx,
		`SELECT month, year FROM fiscal_periods WHERE status='OPEN' ORDER BY year, month LIMIT 1`).Scan(&wantMonth, &wantYear); err != nil {
		t.Fatalf("read the earliest open period: %v", err)
	}
	post := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: "FINANCIAL_CONTROLLER", Tier: identity.TierLeadership, Scope: identity.ScopeGlobal}, clockAt).
		WithInvoiceReader(seedInvoiceReader{pool: pool})
	dep := fw1PlantDeposit(t, pool, docAt)

	// The document's own finalization is the builder that already dates by
	// the document: today it is the one refused outright for the closed May.
	if err := post.consume(ctx, ev("ar.invoice.finalized", map[string]any{"invoice_id": dep})); err != nil {
		t.Fatalf("the finalization consume for a closed month: %v (a system journal is never refused for a closed month)", err)
	}
	var finMonth int
	var finPosted *time.Time
	if err := pool.QueryRow(ctx, `
		SELECT p.month, j.posted_at
		  FROM gl_journals j JOIN fiscal_periods p ON p.id = j.period_id
		 WHERE j.idempotency_key = $1`, "ar.invoice.finalized:"+dep).Scan(&finMonth, &finPosted); err != nil {
		t.Fatalf("read the finalization journal: %v", err)
	}
	if finMonth != wantMonth {
		t.Fatalf("the finalization journal posts in period month %d, want %d (the earliest OPEN month)", finMonth, wantMonth)
	}
	if finPosted == nil || finPosted.UTC().Format("2006-01-02") != "2088-05-10" {
		t.Fatalf("the finalization journal carries posted_at %v, want the document date 2088-05-10 kept", finPosted)
	}

	if err := post.consume(ctx, ev("deposit.released", map[string]any{"invoice_id": dep})); err != nil {
		t.Fatalf("the release consume for a closed month: %v (a system journal is never refused for a closed month)", err)
	}
	pmonth, posted := fw1ReleaseJournal(t, pool, dep)
	if pmonth != wantMonth {
		t.Fatalf("the release journal posts in period month %d, want %d (the earliest OPEN month)", pmonth, wantMonth)
	}
	if posted == nil || posted.UTC().Format("2006-01-02") != "2088-05-10" {
		t.Fatalf("the release journal carries posted_at %v, want the document date 2088-05-10 kept", posted)
	}

	// The Controller's late posted list: both closed-month journals read back
	// marked, and the filter names exactly them.
	var depBranch string
	if err := pool.QueryRow(ctx,
		`SELECT COALESCE(branch_id::text,'') FROM gl_journals WHERE idempotency_key=$1`, "deposit.released:"+dep).Scan(&depBranch); err != nil {
		t.Fatalf("read the release journal's branch: %v", err)
	}
	late := true
	js, err := post.ListJournals(ctx, JournalFilter{LatePosted: &late, BranchID: depBranch, Limit: 100})
	if err != nil {
		t.Fatalf("the late posted list: %v", err)
	}
	keys := map[string]bool{}
	for _, j := range js {
		if !j.LatePosted {
			t.Fatalf("journal %s came through the late posted filter unmarked", j.IdempotencyKey)
		}
		keys[j.IdempotencyKey] = true
	}
	if !keys["ar.invoice.finalized:"+dep] || !keys["deposit.released:"+dep] {
		t.Fatalf("the late posted list = %v, wants both the finalization and the release", keys)
	}
}

// TestIntegration_FW1_01_ManualJournalIntoAClosedMonthIsStillRefused is the
// rule's guard: both May and June 2088 are CLOSED and the manual journal,
// which carries no document date, is refused as before.
func TestIntegration_FW1_01_ManualJournalIntoAClosedMonthIsStillRefused(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	ensurePeriods(t, pool, 2088)
	clockAt := time.Date(2088, 6, 15, 12, 0, 0, 0, time.UTC)
	svc := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager}, clockAt)
	if _, err := svc.ClosePeriod(ctx, periodID(t, pool, 2088, 5), "FW1-01 closed month proof"); err != nil {
		t.Fatalf("close May 2088: %v", err)
	}
	if _, err := svc.ClosePeriod(ctx, periodID(t, pool, 2088, 6), "FW1-01 closed month proof"); err != nil {
		t.Fatalf("close June 2088: %v", err)
	}
	_, err := svc.CreateJournal(ctx, CreateJournalInput{
		Memo: "FW1-01 manual into closed month",
		Lines: []JournalLineInput{
			{AccountCode: "1000", DebitCents: 900, Memo: "bank"},
			{AccountCode: "1020", CreditCents: 900, Memo: "undeposited"},
		},
	})
	if err == nil {
		t.Fatalf("the manual journal into the closed month posted; it must be refused")
	}
	if got := err.Error(); got != errClosedPeriod.msg {
		t.Fatalf("the manual refusal = %q, want %q", got, errClosedPeriod.msg)
	}
}

// TestIntegration_FW1_01_ControllerOpensTheNextFiscalYearEarly covers the
// Controller action: a manager is refused, a Controller without a reason is
// refused, and a Controller with a reason creates the following year's twelve
// months; the act is idempotent in that it always leaves exactly one more
// year than existed (a repeat answers the year after that).
func TestIntegration_FW1_01_ControllerOpensTheNextFiscalYearEarly(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	next := fw1NextYear(t, pool)
	following := next + 1
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM gl_period_overrides o USING fiscal_periods p WHERE o.period_id=p.id AND p.year=$1`, following)
		_, _ = pool.Exec(c, `DELETE FROM fiscal_periods WHERE year=$1`, following)
	})

	manager := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager}, time.Date(next, 1, 15, 12, 0, 0, 0, time.UTC))
	if _, err := manager.OpenNextFiscalYear(ctx, "early open"); err == nil {
		t.Fatalf("a manager opened the next fiscal year; it is Controller only")
	}
	controller := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: controllerRole, Tier: identity.TierLeadership}, time.Date(next, 1, 15, 12, 0, 0, 0, time.UTC))
	if _, err := controller.OpenNextFiscalYear(ctx, "   "); err == nil {
		t.Fatalf("the Controller opened the year without a reason; a reason is required")
	}
	res, err := controller.OpenNextFiscalYear(ctx, "FW1-01 early open proof")
	if err != nil {
		t.Fatalf("the Controller's early open: %v", err)
	}
	if res.FiscalYear != next {
		t.Fatalf("the early open made year %d, want %d", res.FiscalYear, next)
	}
	var months int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM fiscal_periods WHERE year=$1`, next).Scan(&months); err != nil {
		t.Fatalf("count the opened year's periods: %v", err)
	}
	if months != 12 {
		t.Fatalf("the opened year holds %d periods, want 12", months)
	}
	res2, err := controller.OpenNextFiscalYear(ctx, "FW1-01 one more year ahead")
	if err != nil {
		t.Fatalf("the repeat open: %v", err)
	}
	if res2.FiscalYear != following {
		t.Fatalf("the repeat open made year %d, want %d (always one year ahead of the latest)", res2.FiscalYear, following)
	}
}

// TestIntegration_FW1_01_LateJournalNeverLandsBeforeItsDocumentsMonth is
// round 1b (FROM-PRINCIPAL 96): a stray EARLIER open month (January left
// open) must not catch a journal whose own month (May, closed) posts late;
// it lands in the earliest open month AT OR AFTER its document's month (June).
func TestIntegration_FW1_01_LateJournalNeverLandsBeforeItsDocumentsMonth(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	ensurePeriods(t, pool, 2096)
	svc := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: "FINANCIAL_CONTROLLER", Tier: identity.TierLeadership, Scope: identity.ScopeGlobal}, time.Date(2096, 6, 15, 12, 0, 0, 0, time.UTC))
	if _, err := svc.ClosePeriod(ctx, periodID(t, pool, 2096, 5), "round 1b closed May"); err != nil {
		t.Fatalf("close May 2096: %v", err)
	}
	docAt := time.Date(2096, 5, 10, 12, 0, 0, 0, time.UTC)
	clockAt := time.Date(2096, 6, 15, 12, 0, 0, 0, time.UTC)
	post := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: "FINANCIAL_CONTROLLER", Tier: identity.TierLeadership, Scope: identity.ScopeGlobal}, clockAt).
		WithInvoiceReader(seedInvoiceReader{pool: pool})
	dep := fw1PlantDeposit(t, pool, docAt)

	if err := post.consume(ctx, ev("ar.invoice.finalized", map[string]any{"invoice_id": dep})); err != nil {
		t.Fatalf("the finalization consume with a stray earlier open month: %v", err)
	}
	pmonth, _ := fw1ReleaseJournalFor(t, pool, "ar.invoice.finalized:"+dep)
	if pmonth != 6 {
		t.Fatalf("the late journal landed in month %d, want 6 (the earliest open month AT OR AFTER the document's; never a stray earlier open month)", pmonth)
	}
}
