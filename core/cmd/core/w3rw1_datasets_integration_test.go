//go:build integration

package main

// w3rw1_datasets_integration_test.go, the composition root's leg for the curated data sets (W3-RW1).
// The ties live in internal/reporting's named tests, over the real owner services. This leg stands on
// the OTHER side of the seam: it builds the composition root's own adapter exactly as wire_tops.go
// binds it, and proves the production conversion carries real rows from the owners into the reporting
// service, fenced. If the adapter's field copies drift from the reporting package's tests' copies, or
// an owner's row shape changes underneath it, this is the test that fails.

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/inventory"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/purchasing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/reporting"
)

func TestIntegration_W3RW1_TheCompositionRootWiresTheDatasetSources(t *testing.T) {
	url, set := os.LookupEnv("DATABASE_URL")
	if !set || url == "" {
		t.Skip("DATABASE_URL is unset, the composition root wiring leg needs a migrated + seeded database")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("integration DB unavailable: %v", err)
	}
	// Cleanup is LIFO: the pool closes after every fixture delete registered below has run.
	t.Cleanup(pool.Close)
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("integration DB not reachable: %v", err)
	}

	// The construction wire_tops.go makes, over the same owners.
	dsSources := w3rw1DatasetSources{
		credit:   billing.NewCreditService(billing.NewRepository(pool)).WithZone(newDealerZone(pool)),
		invoices: billing.NewRepository(pool),
		ap:       purchasing.NewService(purchasing.NewRepository(pool)),
		gl:       gl.NewService(gl.NewRepository(pool)),
		stock:    inventory.NewService(inventory.NewRepository(pool)),
	}
	svc := reporting.NewService(reporting.NewRepository(pool)).
		WithDatasetARSource(dsSources).
		WithDatasetAPSource(dsSources).
		WithDatasetGLSource(dsSources).
		WithDatasetSalesSource(dsSources).
		WithDatasetStockSource(dsSources)

	var actor string
	if err := pool.QueryRow(ctx,
		`SELECT u.id::text FROM users u WHERE u.status='ACTIVE' ORDER BY u.created_at LIMIT 1`).Scan(&actor); err != nil {
		t.Fatalf("no seeded user to act as: %v", err)
	}
	global := identity.ContextWithStaff(ctx, identity.StaffContext{
		UserID: actor, RoleCode: "FINANCIAL_CONTROLLER", Tier: identity.TierLeadership,
		Scope: identity.ScopeGlobal, Status: "ACTIVE",
	})
	scopedTRT := identity.ContextWithStaff(ctx, identity.StaffContext{
		UserID: actor, RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager,
		Scope: identity.ScopeBranch, Status: "ACTIVE",
		Branches: map[string]bool{fw1TRT: true},
	})

	// One live sale at Trenton, in a stretch of 1971 nothing else dates: the read through the
	// composition root's adapter carries the line's own cents and branch, and a Kingston scope does
	// not see it. The cleanup is registered before anything else so a failure unwinds it.
	var cust, inv, prod, line string
	del := func(sql string, args ...any) {
		t.Cleanup(func() {
			if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
				t.Errorf("wiring leg cleanup failed: %v\nSQL: %s", err, sql)
			}
		})
	}
	inv = "b7a10000-0000-4000-8000-00000000d001"
	cust = "b7a10000-0000-4000-8000-00000000d002"
	prod = "b7a10000-0000-4000-8000-00000000d003"
	line = "b7a10000-0000-4000-8000-00000000d004"
	// t.Cleanup is LIFO: register parents first so the deletes unwind the foreign keys.
	del(`DELETE FROM customers WHERE id=$1::uuid`, cust)
	del(`DELETE FROM products WHERE id=$1::uuid`, prod)
	del(`DELETE FROM invoices WHERE id=$1::uuid`, inv)
	del(`DELETE FROM invoice_lines WHERE id=$1::uuid`, line)
	if _, err := pool.Exec(ctx, `
		INSERT INTO customers (id, account_no, name, home_branch_id)
		VALUES ($1::uuid, 'W3RW1-DS', 'w3rw1 wiring leg account', $2::uuid)`, cust, fw1TRT); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO products (id, sku, name, base_uom, price_uom, is_active)
		VALUES ($1::uuid, 'W3RW1-DS', 'w3rw1 wiring leg product', 'EA', 'EA', TRUE)`, prod); err != nil {
		t.Fatalf("seed product: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO invoices (id, invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                      subtotal_cents, tax_cents, holdback_cents, total_cents, balance_cents,
		                      source_kind, created_at, finalized_at)
		VALUES ($1::uuid, 'W3RW1-DS-1', $2::uuid, $3::uuid, 'FINALIZED', '1971-03-01', '1971-03-01',
		        12_345, 0, 0, 12_345, 12_345, 'ORDER', '1971-03-01T12:00:00Z', '1971-03-01T12:00:00Z')`,
		inv, cust, fw1TRT); err != nil {
		t.Fatalf("seed invoice: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO invoice_lines (id, invoice_id, line_no, product_id, description, qty_entered,
		                          uom_entered, qty_base, unit_price_cents, price_uom, line_total_cents, tax_cents)
		VALUES ($1::uuid, $2::uuid, 1, $3::uuid, 'w3rw1 wiring leg line', 1, 'EA', 1, 12_345, 'EA', 12_345, 0)`,
		line, inv, prod); err != nil {
		t.Fatalf("seed invoice line: %v", err)
	}

	res, err := svc.RunDataset(global, "sales", reporting.DatasetQuery{
		Columns: []string{"branch_id", "line_total_cents"},
		Filters: []reporting.DatasetFilter{
			{Column: "invoice_date", Op: "ge", Values: []any{"1971-03-01"}},
			{Column: "invoice_date", Op: "le", Values: []any{"1971-03-09"}},
		},
		GroupBy: []string{"branch_id"},
	})
	if err != nil {
		t.Fatalf("the wired sales read: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0][0] != fw1TRT || res.Rows[0][1] != int64(12_345) {
		t.Fatalf("the composition root's adapter must carry the line's cents and branch, got %v", res.Rows)
	}

	scoped, err := svc.RunDataset(scopedTRT, "sales", reporting.DatasetQuery{
		Columns: []string{"branch_id", "line_total_cents"},
		Filters: []reporting.DatasetFilter{
			{Column: "invoice_date", Op: "ge", Values: []any{"1971-03-01"}},
			{Column: "invoice_date", Op: "le", Values: []any{"1971-03-09"}},
		},
		GroupBy: []string{"branch_id"},
	})
	if err != nil {
		t.Fatalf("the scoped wired read: %v", err)
	}
	if len(scoped.Rows) != 1 || scoped.Totals["line_total_cents"] != int64(12_345) {
		t.Fatalf("the Trenton scope keeps the Trenton line, got %v", scoped.Rows)
	}

	scopedKGN := identity.ContextWithStaff(ctx, identity.StaffContext{
		UserID: actor, RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager,
		Scope: identity.ScopeBranch, Status: "ACTIVE",
		Branches: map[string]bool{fw1KGN: true},
	})
	other, err := svc.RunDataset(scopedKGN, "sales", reporting.DatasetQuery{
		Columns: []string{"branch_id", "line_total_cents"},
		Filters: []reporting.DatasetFilter{
			{Column: "invoice_date", Op: "ge", Values: []any{"1971-03-01"}},
			{Column: "invoice_date", Op: "le", Values: []any{"1971-03-09"}},
		},
	})
	if err != nil {
		t.Fatalf("the Kingston wired read: %v", err)
	}
	if len(other.Rows) != 0 {
		t.Fatalf("a Kingston scope sees none of Trenton's lines, got %v", other.Rows)
	}

	// Every wired source answers a read, whichever rows the seed holds.
	runs := []struct {
		set   string
		query reporting.DatasetQuery
	}{
		{set: "ar", query: reporting.DatasetQuery{Columns: []string{"total_cents"}}},
		{set: "ap", query: reporting.DatasetQuery{Columns: []string{"open_cents"}}},
		{set: "gl", query: reporting.DatasetQuery{
			Columns: []string{"debit_cents"},
			Filters: []reporting.DatasetFilter{
				{Column: "fiscal_year", Op: "eq", Values: []any{1971}},
			},
		}},
		{set: "stock", query: reporting.DatasetQuery{Columns: []string{"value_cents"}}},
	}
	for _, run := range runs {
		if _, err := svc.RunDataset(global, run.set, run.query); err != nil {
			t.Fatalf("the wired %s read: %v", run.set, err)
		}
	}
}

func TestIntegration_W3RW1_ARDatasetAsOfAgreesWithOwnerRouteAfterNoonUTC(t *testing.T) {
	url, set := os.LookupEnv("DATABASE_URL")
	if !set || url == "" {
		t.Skip("DATABASE_URL is unset, the AR as_of house day leg needs a migrated + seeded database")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("integration DB unavailable: %v", err)
	}
	t.Cleanup(pool.Close)

	dsSources := w3rw1DatasetSources{
		credit:   billing.NewCreditService(billing.NewRepository(pool)).WithZone(newDealerZone(pool)),
		invoices: billing.NewRepository(pool),
		ap:       purchasing.NewService(purchasing.NewRepository(pool)),
		gl:       gl.NewService(gl.NewRepository(pool)),
		stock:    inventory.NewService(inventory.NewRepository(pool)),
	}
	svc := reporting.NewService(reporting.NewRepository(pool)).
		WithDatasetARSource(dsSources).
		WithDatasetAPSource(dsSources).
		WithDatasetGLSource(dsSources).
		WithDatasetSalesSource(dsSources).
		WithDatasetStockSource(dsSources)

	var actor string
	if err := pool.QueryRow(ctx,
		`SELECT u.id::text FROM users u WHERE u.status='ACTIVE' ORDER BY u.created_at LIMIT 1`).Scan(&actor); err != nil {
		t.Fatalf("no seeded user to act as: %v", err)
	}
	global := identity.ContextWithStaff(ctx, identity.StaffContext{
		UserID: actor, RoleCode: "FINANCIAL_CONTROLLER", Tier: identity.TierLeadership,
		Scope: identity.ScopeGlobal, Status: "ACTIVE",
	})

	const (
		day  = "1971-06-15"
		cust = "b7a10000-0000-4000-8000-00000000d101"
		inv  = "b7a10000-0000-4000-8000-00000000d102"
	)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM invoices WHERE id=$1::uuid`, inv)
		_, _ = pool.Exec(context.Background(), `DELETE FROM customers WHERE id=$1::uuid`, cust)
	})
	if _, err := pool.Exec(ctx, `
		INSERT INTO customers (id, account_no, name, home_branch_id)
		VALUES ($1::uuid, 'W3RW1-AR-NOON', 'w3rw1 ar house day account', $2::uuid)`, cust, fw1TRT); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	// Planted at 15:00Z (11:00 EDT in America/Toronto), after noon UTC on day D.
	if _, err := pool.Exec(ctx, `
		INSERT INTO invoices (id, invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                      subtotal_cents, tax_cents, holdback_cents, total_cents, balance_cents,
		                      source_kind, created_at, finalized_at)
		VALUES ($1::uuid, 'W3RW1-AR-NOON-1', $2::uuid, $3::uuid, 'FINALIZED', $4::date, $4::date,
		        55_600, 0, 0, 55_600, 55_600, 'ORDER', '1971-06-15T15:00:00Z', '1971-06-15T15:00:00Z')`,
		inv, cust, fw1TRT, day); err != nil {
		t.Fatalf("seed invoice after noon UTC: %v", err)
	}

	// Owner route GET /api/v1/ar/aging?as_of=1971-06-15 resolves the day in the dealer zone via ParseAsOfFor.
	ownerCut, err := billing.ParseAsOfFor(global, newDealerZone(pool), day)
	if err != nil {
		t.Fatalf("ParseAsOfFor(%s): %v", day, err)
	}
	ownerRows, err := dsSources.credit.Aging(global, ownerCut, "")
	if err != nil {
		t.Fatalf("owner AR aging: %v", err)
	}
	var ownerTotal int64
	for _, r := range ownerRows {
		if r.CustomerID == cust {
			ownerTotal += r.TotalCents
		}
	}
	if ownerTotal != 55_600 {
		t.Fatalf("owner AR aging at as_of=%s = %d, want 55600", day, ownerTotal)
	}

	res, err := svc.RunDataset(global, "ar", reporting.DatasetQuery{
		Columns: []string{"customer_id", "total_cents"},
		Filters: []reporting.DatasetFilter{
			{Column: "customer_id", Op: "eq", Values: []any{cust}},
		},
		AsOf: day,
	})
	if err != nil {
		t.Fatalf("ar dataset read: %v", err)
	}
	got, _ := res.Totals["total_cents"].(int64)
	if got != ownerTotal {
		t.Fatalf("ar dataset total_cents at as_of=%s = %d, want owner route's %d (invoice finalized at 15:00Z after noon UTC on %s was dropped)",
			day, got, ownerTotal, day)
	}
}

func TestIntegration_W3RW1_APDatasetAsOfAgreesWithOwnerRouteAfterNoonUTC(t *testing.T) {
	url, set := os.LookupEnv("DATABASE_URL")
	if !set || url == "" {
		t.Skip("DATABASE_URL is unset, the AP as_of house day leg needs a migrated + seeded database")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("integration DB unavailable: %v", err)
	}
	t.Cleanup(pool.Close)

	glSvc := gl.NewService(gl.NewRepository(pool))
	apSvc := purchasing.NewService(purchasing.NewRepository(pool)).
		WithGLJournalClock(w4cGLJournalClock{svc: glSvc})
	dsSources := w3rw1DatasetSources{
		credit:   billing.NewCreditService(billing.NewRepository(pool)),
		invoices: billing.NewRepository(pool),
		ap:       apSvc,
		gl:       glSvc,
		stock:    inventory.NewService(inventory.NewRepository(pool)),
	}
	svc := reporting.NewService(reporting.NewRepository(pool)).
		WithDatasetARSource(dsSources).
		WithDatasetAPSource(dsSources).
		WithDatasetGLSource(dsSources).
		WithDatasetSalesSource(dsSources).
		WithDatasetStockSource(dsSources)

	var actor string
	if err := pool.QueryRow(ctx,
		`SELECT u.id::text FROM users u WHERE u.status='ACTIVE' ORDER BY u.created_at LIMIT 1`).Scan(&actor); err != nil {
		t.Fatalf("no seeded user to act as: %v", err)
	}
	global := identity.ContextWithStaff(ctx, identity.StaffContext{
		UserID: actor, RoleCode: "FINANCIAL_CONTROLLER", Tier: identity.TierLeadership,
		Scope: identity.ScopeGlobal, Status: "ACTIVE",
	})

	const (
		day  = "1971-06-15"
		vend = "b7a10000-0000-4000-8000-00000000d201"
		bill = "b7a10000-0000-4000-8000-00000000d202"
		jid  = "b7a10000-0000-4000-8000-00000000d203"
		pid  = "b7a10000-0000-4000-8000-00000000d204"
	)
	var periodID string
	var createdPeriod bool
	if err := pool.QueryRow(ctx, `SELECT id::text FROM fiscal_periods WHERE year=1971 AND month=6`).Scan(&periodID); err != nil {
		periodID = pid
		createdPeriod = true
		if _, err := pool.Exec(ctx, `INSERT INTO fiscal_periods (id, year, month, status) VALUES ($1::uuid, 1971, 6, 'OPEN')`, periodID); err != nil {
			t.Fatalf("seed fiscal period: %v", err)
		}
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM purchasing_bills WHERE id=$1::uuid`, bill)
		_, _ = pool.Exec(context.Background(), `DELETE FROM gl_journals WHERE id=$1::uuid`, jid)
		_, _ = pool.Exec(context.Background(), `DELETE FROM vendors WHERE id=$1::uuid`, vend)
		if createdPeriod {
			_, _ = pool.Exec(context.Background(), `DELETE FROM fiscal_periods WHERE id=$1::uuid`, periodID)
		}
	})
	if _, err := pool.Exec(ctx, `
		INSERT INTO vendors (id, vendor_no, name, payment_terms_days)
		VALUES ($1::uuid, 'W3RW1-AP-NOON', 'w3rw1 ap house day vendor', 30)`, vend); err != nil {
		t.Fatalf("seed vendor: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO gl_journals (id, fiscal_year, entry_no, period_id, source, memo, status, branch_id)
		VALUES ($1::uuid, 1971, 987651, $2::uuid, 'MANUAL', 'w3rw1 ap house day journal', 'DRAFT', $3::uuid)`,
		jid, periodID, fw1TRT); err != nil {
		t.Fatalf("seed draft journal: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO gl_journal_lines (journal_id, line_no, account_id, branch_id, debit_cents, credit_cents)
		VALUES ($1::uuid, 1, (SELECT id FROM gl_accounts WHERE code='5000'), $2::uuid, 44500, NULL),
		       ($1::uuid, 2, (SELECT id FROM gl_accounts WHERE code='2100'), $2::uuid, NULL, 44500)`,
		jid, fw1TRT); err != nil {
		t.Fatalf("seed journal lines: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE gl_journals SET status='POSTED', posted_at='1971-06-15T15:00:00Z' WHERE id=$1::uuid`, jid); err != nil {
		t.Fatalf("post journal after noon UTC: %v", err)
	}
	// Planted at 15:00Z (11:00 EDT in America/Toronto), after noon UTC on day D.
	if _, err := pool.Exec(ctx, `
		INSERT INTO purchasing_bills (id, bill_no, vendor_id, branch_id, status, match_state,
		                              bill_date, due_date, subtotal_cents, tax_cents, total_cents,
		                              gl_journal_id, created_at)
		VALUES ($1::uuid, 'W3RW1-AP-NOON-1', $2::uuid, $3::uuid, 'POSTED', 'MATCHED',
		        $4::date, '1971-07-15'::date, 44_500, 0, 44_500, $5::uuid, '1971-06-15T15:00:00Z')`,
		bill, vend, fw1TRT, day, jid); err != nil {
		t.Fatalf("seed purchasing_bill after noon UTC: %v", err)
	}

	// Owner route GET /api/v1/purchasing/ap-aging?as_of=1971-06-15 calls APAgingForCaller(ctx, "1971-06-15", "").
	ownerPage, err := dsSources.ap.APAgingForCaller(global, day, "")
	if err != nil {
		t.Fatalf("owner AP aging: %v", err)
	}
	var ownerOpen int64
	for _, row := range ownerPage.Items {
		for _, it := range row.OpenItems {
			if it.VendorID == vend {
				ownerOpen += it.OpenCents
			}
		}
	}
	if ownerOpen != 44_500 {
		t.Fatalf("owner AP aging at as_of=%s = %d, want 44500", day, ownerOpen)
	}

	res, err := svc.RunDataset(global, "ap", reporting.DatasetQuery{
		Columns: []string{"vendor_id", "open_cents"},
		Filters: []reporting.DatasetFilter{
			{Column: "vendor_id", Op: "eq", Values: []any{vend}},
		},
		AsOf: day,
	})
	if err != nil {
		t.Fatalf("ap dataset read: %v", err)
	}
	got, _ := res.Totals["open_cents"].(int64)
	if got != ownerOpen {
		t.Fatalf("ap dataset open_cents at as_of=%s = %d, want owner route's %d (bill posted at 15:00Z after noon UTC on %s was dropped)",
			day, got, ownerOpen, day)
	}
}

func TestIntegration_W3RW1_DatasetTiesHaveNoDuplicateAdapterInReporting(t *testing.T) {
	if _, err := os.Stat("../../internal/reporting/datasets_integration_test.go"); err == nil {
		t.Fatalf("internal/reporting/datasets_integration_test.go still exists with a duplicate test-only adapter; all dataset tie legs must run over cmd/core's w3rw1DatasetSources")
	}
}

func TestIntegration_W3RW1_OwnerDatasetReadsFailClosedWithoutStaffContext(t *testing.T) {
	url, set := os.LookupEnv("DATABASE_URL")
	if !set || url == "" {
		t.Skip("DATABASE_URL is unset, the fail-closed owner read leg needs a migrated + seeded database")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("integration DB unavailable: %v", err)
	}
	t.Cleanup(pool.Close)

	stockSvc := inventory.NewService(inventory.NewRepository(pool))
	if rows, err := stockSvc.DatasetStockOnHand(context.Background()); err == nil {
		t.Fatalf("DatasetStockOnHand without a staff context must fail closed, got %d rows and nil error", len(rows))
	}

	billRepo := billing.NewRepository(pool)
	from := w3rw1Instant(w3rw1Day1)
	to := w3rw1Instant(w3rw1Day9)
	if rows, err := billRepo.DatasetInvoiceLines(context.Background(), from, to); err == nil {
		t.Fatalf("DatasetInvoiceLines without a staff context must fail closed, got %d rows and nil error", len(rows))
	}
}

// -- the five named tie legs and the branch fence leg, over w3rw1DatasetSources -----------------------

const (
	w3rw1Day1                 = "1971-03-01"
	w3rw1Day9                 = "1971-03-09"
	w3rw1AsOf                 = "1971-03-05"
	w3rw1Dib396StockDriftLock = int64(3960001)
)

func w3rw1Instant(day string) time.Time {
	t, err := time.Parse("2006-01-02", day)
	if err != nil {
		panic(err)
	}
	return t.Add(12 * time.Hour)
}

func w3rw1Pool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url, set := os.LookupEnv("DATABASE_URL")
	if !set || url == "" {
		t.Skip("DATABASE_URL is unset, dataset tie tests need a migrated + seeded database")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("integration DB unavailable: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		t.Fatalf("integration DB not reachable: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func w3rw1Service(t *testing.T, pool *pgxpool.Pool) *reporting.Service {
	t.Helper()
	src := w3rw1DatasetSources{
		credit:   billing.NewCreditService(billing.NewRepository(pool)).WithZone(newDealerZone(pool)),
		invoices: billing.NewRepository(pool),
		ap:       purchasing.NewService(purchasing.NewRepository(pool)),
		gl:       gl.NewService(gl.NewRepository(pool)),
		stock:    inventory.NewService(inventory.NewRepository(pool)),
	}
	return reporting.NewService(reporting.NewRepository(pool)).
		WithDatasetARSource(src).
		WithDatasetAPSource(src).
		WithDatasetGLSource(src).
		WithDatasetSalesSource(src).
		WithDatasetStockSource(src)
}

func w3rw1Actor(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var actor string
	if err := pool.QueryRow(context.Background(),
		`SELECT u.id::text FROM users u WHERE u.status='ACTIVE' ORDER BY u.created_at LIMIT 1`).Scan(&actor); err != nil {
		t.Fatalf("no seeded user to act as: %v", err)
	}
	return actor
}

func w3rw1GlobalCtx(ctx context.Context, t *testing.T, pool *pgxpool.Pool) context.Context {
	t.Helper()
	return identity.ContextWithStaff(ctx, identity.StaffContext{
		UserID: w3rw1Actor(t, pool), RoleCode: "FINANCIAL_CONTROLLER", Tier: identity.TierLeadership,
		Scope: identity.ScopeGlobal, Status: "ACTIVE",
	})
}

func w3rw1ScopedCtx(ctx context.Context, t *testing.T, pool *pgxpool.Pool, branchID string) context.Context {
	t.Helper()
	return identity.ContextWithStaff(ctx, identity.StaffContext{
		UserID: w3rw1Actor(t, pool), RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager,
		Scope: identity.ScopeBranch, Status: "ACTIVE",
		Branches: map[string]bool{branchID: true},
	})
}

func w3rw1LeaseShared(t *testing.T, pool *pgxpool.Pool, key int64) {
	t.Helper()
	conn, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatalf("DIB-396 lease: acquire connection: %v", err)
	}
	if _, err := conn.Exec(context.Background(), "SELECT pg_advisory_lock_shared($1)", key); err != nil {
		conn.Release()
		t.Fatalf("DIB-396 lease: pg_advisory_lock_shared(%d): %v", key, err)
	}
	t.Cleanup(func() {
		_, _ = conn.Exec(context.Background(), "SELECT pg_advisory_unlock_shared($1)", key)
		conn.Release()
	})
}

type w3rw1Fixture struct {
	t    *testing.T
	pool *pgxpool.Pool
	ctx  context.Context
	tag  string
}

func newW3RW1Fixture(t *testing.T, pool *pgxpool.Pool) *w3rw1Fixture {
	t.Helper()
	return &w3rw1Fixture{
		t:    t,
		pool: pool,
		ctx:  context.Background(),
		tag:  "W3RW1-" + uuid.NewString()[:8],
	}
}

func (f *w3rw1Fixture) newID() string { return uuid.NewString() }

func (f *w3rw1Fixture) exec(sql string, args ...any) {
	f.t.Helper()
	if _, err := f.pool.Exec(f.ctx, sql, args...); err != nil {
		f.t.Fatalf("fixture: %v\nSQL: %s\nargs: %v", err, sql, args)
	}
}

func (f *w3rw1Fixture) del(sql string, args ...any) {
	f.t.Cleanup(func() {
		if _, err := f.pool.Exec(context.Background(), sql, args...); err != nil {
			f.t.Errorf("fixture cleanup failed: %v\nSQL: %s", err, sql)
		}
	})
}

func (f *w3rw1Fixture) customer(name string) string {
	id := f.newID()
	f.del(`DELETE FROM customers WHERE id=$1::uuid`, id)
	f.exec(`INSERT INTO customers (id, account_no, name) VALUES ($1::uuid, $2, $3)`,
		id, f.tag+"-"+name, f.tag+" "+name)
	return id
}

func (f *w3rw1Fixture) homeBranch(customerID, branchID string) {
	f.exec(`UPDATE customers SET home_branch_id=$2::uuid WHERE id=$1::uuid`, customerID, branchID)
}

func (f *w3rw1Fixture) vendor(name string) string {
	id := f.newID()
	f.del(`DELETE FROM vendors WHERE id=$1::uuid`, id)
	f.exec(`INSERT INTO vendors (id, vendor_no, name) VALUES ($1::uuid, $2, $3)`,
		id, f.tag+"-"+name, f.tag+" "+name)
	return id
}

func (f *w3rw1Fixture) product(sku, name, categoryID string) string {
	id := f.newID()
	f.del(`DELETE FROM products WHERE id=$1::uuid`, id)
	f.del(`DELETE FROM invoice_lines WHERE product_id=$1::uuid`, id)
	f.del(`DELETE FROM purchasing_po_lines WHERE product_id=$1::uuid`, id)
	f.exec(`INSERT INTO products (id, sku, name, category_id, base_uom, price_uom, is_active)
	        VALUES ($1::uuid, $2, $3, NULLIF($4,'')::uuid, 'EA', 'EA', TRUE)`,
		id, f.tag+"-"+sku, f.tag+" "+name, categoryID)
	return id
}

func (f *w3rw1Fixture) openInvoice(customerID, branchID, date string, subtotal, tax int64) string {
	id := f.newID()
	f.del(`DELETE FROM invoice_lines WHERE invoice_id=$1::uuid`, id)
	f.del(`DELETE FROM invoices WHERE id=$1::uuid`, id)
	f.exec(`INSERT INTO invoices (id, invoice_no, customer_id, branch_id, status, issue_date, due_date,
	          subtotal_cents, tax_cents, holdback_cents, total_cents, balance_cents, source_kind,
	          created_at, finalized_at)
	        VALUES ($1::uuid, $2, $3::uuid, $4::uuid, 'FINALIZED', $5::date, $5::date,
	                $6, $7, 0, $8, $8, 'ORDER', $9::timestamptz, $9::timestamptz)`,
		id, f.tag+"-DS-"+id[:6], customerID, branchID, date, subtotal, tax, subtotal+tax,
		w3rw1Instant(date))
	return id
}

func (f *w3rw1Fixture) invoice(customerID, branchID, orderID, date string, subtotal, tax int64, status string, balance int64) string {
	id := f.newID()
	f.del(`DELETE FROM invoice_lines WHERE invoice_id=$1::uuid`, id)
	f.del(`DELETE FROM invoices WHERE id=$1::uuid`, id)
	f.exec(`INSERT INTO invoices (id, invoice_no, customer_id, order_id, branch_id, status, issue_date,
	          due_date, subtotal_cents, tax_cents, holdback_cents, total_cents, balance_cents)
	        VALUES ($1::uuid, $2, $3::uuid, NULLIF($4,'')::uuid, $5::uuid, $6, $7::date, $7::date,
	                $8, $9, 0, $10, $11)`,
		id, f.tag+"-"+id[:6], customerID, orderID, branchID, status, date,
		subtotal, tax, subtotal+tax, balance)
	return id
}

func (f *w3rw1Fixture) invoiceLine(invoiceID, productID string, lineNo int, qty float64, unitPriceCents, lineTotalCents int64) string {
	id := f.newID()
	f.exec(`INSERT INTO invoice_lines (id, invoice_id, line_no, product_id, description, qty_entered,
	          uom_entered, qty_base, unit_price_cents, price_uom, line_total_cents, tax_cents)
	        VALUES ($1::uuid, $2::uuid, $3, $4::uuid, $5, $6, 'EA', $6, $7, 'EA', $8, 0)`,
		id, invoiceID, lineNo, productID, f.tag+" line", qty, unitPriceCents, lineTotalCents)
	return id
}

func (f *w3rw1Fixture) postedBill(vendorID, branchID, billDate string, totalCents int64) string {
	id := f.newID()
	f.del(`DELETE FROM purchasing_bills WHERE id=$1::uuid`, id)
	f.exec(`INSERT INTO purchasing_bills (id, bill_no, vendor_id, branch_id, status, match_state,
	          bill_date, due_date, subtotal_cents, tax_cents, total_cents)
	        VALUES ($1::uuid, $2, $3::uuid, $4::uuid, 'POSTED', 'MATCHED',
	                $5::date, $5::date, $6, 0, $6)`,
		id, f.tag+"-DSBILL-"+id[:6], vendorID, branchID, billDate, totalCents)
	return id
}

func (f *w3rw1Fixture) fiscalPeriod(year, month int) string {
	var id string
	if err := f.pool.QueryRow(f.ctx,
		`SELECT id::text FROM fiscal_periods WHERE year=$1 AND month=$2`, year, month).Scan(&id); err == nil {
		return id
	}
	id = f.newID()
	f.del(`DELETE FROM fiscal_periods WHERE id=$1::uuid`, id)
	f.exec(`INSERT INTO fiscal_periods (id, year, month, status) VALUES ($1::uuid, $2, $3, 'OPEN')`,
		id, year, month)
	return id
}

func (f *w3rw1Fixture) journal(periodID, branchID, postedAt string, entryNo int, legs []struct {
	accountCode           string
	debitCents, creditCts int64
}) string {
	jid := f.newID()
	f.del(`DELETE FROM gl_journals WHERE id=$1::uuid`, jid)
	f.exec(`INSERT INTO gl_journals (id, fiscal_year, entry_no, period_id, source, memo, status, branch_id)
	        VALUES ($1::uuid,
	                (SELECT year FROM fiscal_periods WHERE id=$2::uuid),
	                $3, $2::uuid, 'MANUAL', $4, 'DRAFT', NULLIF($5,'')::uuid)`,
		jid, periodID, entryNo, f.tag+" dataset journal", branchID)
	lineNo := 0
	for _, leg := range legs {
		lineNo++
		f.exec(`INSERT INTO gl_journal_lines (journal_id, line_no, account_id, branch_id, debit_cents, credit_cents)
		        VALUES ($1::uuid, $2, (SELECT id FROM gl_accounts WHERE code=$3), NULLIF($4,'')::uuid,
		                NULLIF($5, 0), NULLIF($6, 0))`,
			jid, lineNo, leg.accountCode, branchID, leg.debitCents, leg.creditCts)
	}
	f.exec(`UPDATE gl_journals SET status='POSTED', posted_at=$2::timestamptz WHERE id=$1::uuid`,
		jid, postedAt)
	return jid
}

func (f *w3rw1Fixture) branchLocation(branchID string) string {
	var id string
	if err := f.pool.QueryRow(f.ctx,
		`SELECT id::text FROM locations WHERE branch_id=$1::uuid ORDER BY loc_type, code LIMIT 1`, branchID).
		Scan(&id); err != nil {
		f.t.Fatalf("no location in branch %s: %v", branchID, err)
	}
	return id
}

func (f *w3rw1Fixture) binLocation(branchID, code string) string {
	id := f.newID()
	f.del(`DELETE FROM locations WHERE id=$1::uuid`, id)
	f.exec(`INSERT INTO locations (id, branch_id, parent_id, loc_type, code, name)
	        VALUES ($1::uuid, $2::uuid, $2::uuid, 'BIN', $3, $4)`,
		id, branchID, f.tag+"-"+code, f.tag+" "+code)
	return id
}

func TestIntegration_Dataset_AR_TiesToTheAging(t *testing.T) {
	pool := w3rw1Pool(t)
	f := newW3RW1Fixture(t, pool)
	ctx := w3rw1GlobalCtx(context.Background(), t, pool)
	svc := w3rw1Service(t, pool)

	custA := f.customer("aging north")
	f.homeBranch(custA, fw1TRT)
	custB := f.customer("aging south")
	f.homeBranch(custB, fw1KGN)
	f.openInvoice(custA, fw1TRT, w3rw1Day1, 30_000, 0)
	f.openInvoice(custA, fw1TRT, w3rw1Day1, 12_345, 0)
	f.openInvoice(custB, fw1KGN, w3rw1Day1, 5_000, 0)

	res, err := svc.RunDataset(ctx, "ar", reporting.DatasetQuery{
		Columns: []string{"branch_id", "current_cents", "days_1_30_cents", "total_cents"},
		GroupBy: []string{"branch_id"},
		AsOf:    w3rw1AsOf,
	})
	if err != nil {
		t.Fatalf("ar read: %v", err)
	}
	wantTotal := int64(47_345)
	if got := res.Totals["total_cents"]; got != wantTotal {
		t.Fatalf("the ar set totals %v, the fixture wrote %v", got, wantTotal)
	}
	aging, err := billing.NewCreditService(billing.NewRepository(pool)).WithZone(newDealerZone(pool)).AgingForCaller(ctx, w3rw1AsOf, "")
	if err != nil {
		t.Fatalf("the owner's aging read: %v", err)
	}
	var agingTotal int64
	for _, r := range aging {
		agingTotal += r.TotalCents
	}
	if agingTotal != wantTotal {
		t.Fatalf("the aging itself answers %v, the fixture wrote %v", agingTotal, wantTotal)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("two home branches, two rows, got %v", res.Rows)
	}
	byBranch := map[string]any{}
	for _, row := range res.Rows {
		byBranch[row[0].(string)] = row[2]
	}
	if byBranch[fw1TRT] != int64(42_345) || byBranch[fw1KGN] != int64(5_000) {
		t.Fatalf("the buckets do not sit by home branch: %v", res.Rows)
	}
	if res.Totals["days_1_30_cents"] != wantTotal || res.Totals["current_cents"] != int64(0) {
		t.Fatalf("at %s the balances are four days past due: 1-30 totals %v, current totals %v",
			w3rw1AsOf, res.Totals["days_1_30_cents"], res.Totals["current_cents"])
	}
}

func TestIntegration_Dataset_AP_TiesToTheAPAging(t *testing.T) {
	pool := w3rw1Pool(t)
	f := newW3RW1Fixture(t, pool)
	ctx := w3rw1GlobalCtx(context.Background(), t, pool)
	svc := w3rw1Service(t, pool)

	vendorN := f.vendor("payables north")
	vendorS := f.vendor("payables south")
	f.postedBill(vendorN, fw1TRT, w3rw1Day1, 21_500)
	f.postedBill(vendorS, fw1KGN, w3rw1Day1, 9_750)

	res, err := svc.RunDataset(ctx, "ap", reporting.DatasetQuery{
		Columns: []string{"branch_id", "open_cents"},
		GroupBy: []string{"branch_id"},
		AsOf:    w3rw1AsOf,
	})
	if err != nil {
		t.Fatalf("ap read: %v", err)
	}
	wantTotal := int64(31_250)
	if got := res.Totals["open_cents"]; got != wantTotal {
		t.Fatalf("the ap set totals %v, the fixture wrote %v", got, wantTotal)
	}
	page, err := purchasing.NewService(purchasing.NewRepository(pool)).APAgingForCaller(ctx, w3rw1AsOf, "")
	if err != nil {
		t.Fatalf("the owner's aging read: %v", err)
	}
	if page.TotalCents != wantTotal {
		t.Fatalf("the ap aging itself answers %v, the fixture wrote %v", page.TotalCents, wantTotal)
	}
	byBranch := map[string]any{}
	for _, row := range res.Rows {
		byBranch[row[0].(string)] = row[1]
	}
	if byBranch[fw1TRT] != int64(21_500) || byBranch[fw1KGN] != int64(9_750) {
		t.Fatalf("the open cents do not sit by branch: %v", res.Rows)
	}
}

func TestIntegration_Dataset_GL_TiesToTheTrialBalance(t *testing.T) {
	pool := w3rw1Pool(t)
	f := newW3RW1Fixture(t, pool)
	ctx := w3rw1GlobalCtx(context.Background(), t, pool)
	svc := w3rw1Service(t, pool)

	march := f.fiscalPeriod(1971, 3)
	april := f.fiscalPeriod(1971, 4)
	f.journal(march, fw1TRT, "1971-03-05T12:00:00Z", 990_001, []struct {
		accountCode           string
		debitCents, creditCts int64
	}{
		{"1000", 50_000, 0},
		{"1100", 0, 50_000},
	})
	f.journal(march, fw1KGN, "1971-03-06T12:00:00Z", 990_002, []struct {
		accountCode           string
		debitCents, creditCts int64
	}{
		{"1100", 25_000, 0},
		{"1000", 0, 25_000},
	})
	f.journal(april, fw1TRT, "1971-04-05T12:00:00Z", 990_003, []struct {
		accountCode           string
		debitCents, creditCts int64
	}{
		{"1000", 7_000, 0},
		{"1100", 0, 7_000},
	})
	draft := f.newID()
	f.del(`DELETE FROM gl_journals WHERE id=$1::uuid`, draft)
	f.exec(`INSERT INTO gl_journals (id, fiscal_year, entry_no, period_id, source, memo, status, posted_at, branch_id)
	        VALUES ($1::uuid, 1971, 990004, $2::uuid, 'MANUAL', $3, 'DRAFT', NULL, NULLIF($4,'')::uuid)`,
		draft, march, f.tag+" never posted", fw1TRT)

	res, err := svc.RunDataset(ctx, "gl", reporting.DatasetQuery{
		Columns: []string{"account_code", "debit_cents", "credit_cents"},
		Filters: []reporting.DatasetFilter{
			{Column: "fiscal_year", Op: "eq", Values: []any{1971}},
			{Column: "fiscal_month", Op: "eq", Values: []any{3}},
		},
		GroupBy: []string{"account_code"},
	})
	if err != nil {
		t.Fatalf("gl read: %v", err)
	}
	tb, err := gl.NewService(gl.NewRepository(pool)).TrialBalance(ctx, 1971, 3, "")
	if err != nil {
		t.Fatalf("the owner's trial balance: %v", err)
	}
	if len(tb.Rows) == 0 {
		t.Fatal("the trial balance answers no rows for a period the fixture just posted into")
	}
	byCode := map[string][]any{}
	for _, row := range res.Rows {
		byCode[row[0].(string)] = row
	}
	for _, row := range tb.Rows {
		set, ok := byCode[row.AccountCode]
		if !ok {
			t.Fatalf("account %s is on the trial balance but not in the set", row.AccountCode)
		}
		if set[1] != row.DebitCents || set[2] != row.CreditCents {
			t.Fatalf("account %s: the set answers %v/%v, the trial balance answers %d/%d",
				row.AccountCode, set[1], set[2], row.DebitCents, row.CreditCents)
		}
	}
	if res.Totals["debit_cents"] != int64(75_000) || res.Totals["credit_cents"] != int64(75_000) {
		t.Fatalf("march ties 75000 a side, got %v", res.Totals)
	}
	yearRes, err := svc.RunDataset(ctx, "gl", reporting.DatasetQuery{
		Columns: []string{"debit_cents"},
		Filters: []reporting.DatasetFilter{{Column: "fiscal_year", Op: "eq", Values: []any{1971}}},
	})
	if err != nil {
		t.Fatalf("gl year read: %v", err)
	}
	if yearRes.Totals["debit_cents"] != int64(82_000) {
		t.Fatalf("the year read carries april too, got %v", yearRes.Totals)
	}
}

func TestIntegration_Dataset_Sales_TiesToTheInvoices(t *testing.T) {
	pool := w3rw1Pool(t)
	f := newW3RW1Fixture(t, pool)
	ctx := w3rw1GlobalCtx(context.Background(), t, pool)
	svc := w3rw1Service(t, pool)

	cust := f.customer("sales tie")
	prod := f.product("DSSKU", "dataset tie product", "")
	inA := f.openInvoice(cust, fw1TRT, w3rw1Day1, 30_000, 0)
	f.invoiceLine(inA, prod, 1, 1, 10_000, 10_000)
	f.invoiceLine(inA, prod, 2, 2, 10_000, 20_000)
	inB := f.openInvoice(cust, fw1KGN, w3rw1Day9, 4_500, 0)
	f.invoiceLine(inB, prod, 1, 3, 1_500, 4_500)
	inVoid := f.invoice(cust, fw1TRT, "", w3rw1Day1, 88_888, 0, "VOID", 88_888)
	f.invoiceLine(inVoid, prod, 1, 1, 88_888, 88_888)
	inLate := f.openInvoice(cust, fw1TRT, "1971-04-01", 500, 0)
	f.invoiceLine(inLate, prod, 1, 1, 500, 500)

	res, err := svc.RunDataset(ctx, "sales", reporting.DatasetQuery{
		Columns: []string{"invoice_id", "line_total_cents"},
		Filters: []reporting.DatasetFilter{
			{Column: "invoice_date", Op: "ge", Values: []any{w3rw1Day1}},
			{Column: "invoice_date", Op: "le", Values: []any{w3rw1Day9}},
		},
		GroupBy: []string{"invoice_id"},
	})
	if err != nil {
		t.Fatalf("sales read: %v", err)
	}
	byInvoice := map[string]int64{}
	for _, row := range res.Rows {
		byInvoice[row[0].(string)] = row[1].(int64)
	}
	if len(byInvoice) != 2 {
		t.Fatalf("two live invoices in the window, the set answers %d: %v", len(byInvoice), byInvoice)
	}
	for _, inv := range []struct {
		id       string
		subtotal int64
	}{{
		id:       inA,
		subtotal: 30_000,
	}, {
		id:       inB,
		subtotal: 4_500,
	}} {
		if got := byInvoice[inv.id]; got != inv.subtotal {
			t.Fatalf("invoice %s: its lines answer %d, the header's subtotal is %d", inv.id, got, inv.subtotal)
		}
		var header int64
		if err := pool.QueryRow(context.Background(),
			`SELECT subtotal_cents FROM invoices WHERE id=$1::uuid`, inv.id).Scan(&header); err != nil {
			t.Fatalf("read the header back: %v", err)
		}
		if header != inv.subtotal {
			t.Fatalf("fixture drift: the header says %d, the test wrote %d", header, inv.subtotal)
		}
	}
	if res.Totals["line_total_cents"] != int64(34_500) {
		t.Fatalf("the window's sales total to 34500 cents, got %v", res.Totals)
	}
}

func TestIntegration_Dataset_Stock_TiesToStockValueCents(t *testing.T) {
	pool := w3rw1Pool(t)
	w3rw1LeaseShared(t, pool, w3rw1Dib396StockDriftLock)
	f := newW3RW1Fixture(t, pool)
	ctx := w3rw1GlobalCtx(context.Background(), t, pool)
	svc := w3rw1Service(t, pool)
	inv := inventory.NewService(inventory.NewRepository(pool))

	before, err := inv.StockValueCents(ctx, "")
	if err != nil {
		t.Fatalf("stock value before: %v", err)
	}
	trentonLoc := f.branchLocation(fw1TRT)
	trentonLoc2 := f.binLocation(fw1TRT, "DSBIN")
	kingstonLoc := f.branchLocation(fw1KGN)
	prod := f.product("DSSTOCK", "dataset stock product", "")
	f.del(`DELETE FROM product_branch_costs WHERE product_id=$1::uuid`, prod)
	f.exec(`INSERT INTO product_branch_costs (product_id, branch_id, avg_unit_cost_cents)
	        VALUES ($1::uuid, $2::uuid, 1000), ($1::uuid, $3::uuid, 1000)`,
		prod, fw1TRT, fw1KGN)
	f.del(`DELETE FROM stock_balances WHERE product_id=$1::uuid`, prod)
	f.exec(`INSERT INTO stock_balances (product_id, location_id, branch_id, on_hand_base, allocated_base)
	        VALUES ($1::uuid, $2::uuid, $3::uuid, 3.5, 0), ($1::uuid, $4::uuid, $3::uuid, 2.25, 0)`,
		prod, trentonLoc, fw1TRT, trentonLoc2)
	f.exec(`INSERT INTO stock_balances (product_id, location_id, branch_id, on_hand_base, allocated_base)
	        VALUES ($1::uuid, $2::uuid, $3::uuid, 1, 0)`, prod, kingstonLoc, fw1KGN)

	after, err := inv.StockValueCents(ctx, "")
	if err != nil {
		t.Fatalf("stock value after: %v", err)
	}
	wantDelta := after - before

	res, err := svc.RunDataset(ctx, "stock", reporting.DatasetQuery{
		Columns: []string{"product_id", "branch_id", "on_hand_base", "unit_cost_cents", "value_cents"},
		Filters: []reporting.DatasetFilter{{Column: "product_id", Op: "eq", Values: []any{prod}}},
	})
	if err != nil {
		t.Fatalf("stock read: %v", err)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("two branches carry the product, the set answers %d rows: %v", len(res.Rows), res.Rows)
	}
	var setValue int64
	for _, row := range res.Rows {
		setValue += row[4].(int64)
	}
	if setValue != wantDelta {
		t.Fatalf("the set values the product at %d cents, the valuation moved %d cents", setValue, wantDelta)
	}
	if res.Totals["on_hand_base"] != 6.75 {
		t.Fatalf("on hand sums across the locations to 6.75, got %v", res.Totals["on_hand_base"])
	}
	for _, row := range res.Rows {
		if row[3] != int64(1_000) {
			t.Fatalf("the unit cost column reads the branch's own cost, got %v", row[3])
		}
	}
}

func TestIntegration_Dataset_AScopedCallerSeesOnlyItsBranches(t *testing.T) {
	pool := w3rw1Pool(t)
	w3rw1LeaseShared(t, pool, w3rw1Dib396StockDriftLock)
	f := newW3RW1Fixture(t, pool)
	globalCtx := w3rw1GlobalCtx(context.Background(), t, pool)
	scopedCtx := w3rw1ScopedCtx(context.Background(), t, pool, fw1TRT)
	svc := w3rw1Service(t, pool)

	custN := f.customer("fence north")
	f.homeBranch(custN, fw1TRT)
	custS := f.customer("fence south")
	f.homeBranch(custS, fw1KGN)
	f.openInvoice(custN, fw1TRT, w3rw1Day1, 11_000, 0)
	f.openInvoice(custS, fw1KGN, w3rw1Day1, 13_000, 0)

	vendorN := f.vendor("fence vendor north")
	vendorS := f.vendor("fence vendor south")
	f.postedBill(vendorN, fw1TRT, w3rw1Day1, 2_100)
	f.postedBill(vendorS, fw1KGN, w3rw1Day1, 2_300)

	march := f.fiscalPeriod(1971, 3)
	f.journal(march, fw1TRT, "1971-03-05T12:00:00Z", 990_011, []struct {
		accountCode           string
		debitCents, creditCts int64
	}{
		{"1000", 4_100, 0},
		{"1100", 0, 4_100},
	})
	f.journal(march, fw1KGN, "1971-03-05T12:00:00Z", 990_012, []struct {
		accountCode           string
		debitCents, creditCts int64
	}{
		{"1000", 4_300, 0},
		{"1100", 0, 4_300},
	})

	prod := f.product("DSFENCE", "dataset fence product", "")
	f.del(`DELETE FROM product_branch_costs WHERE product_id=$1::uuid`, prod)
	f.exec(`INSERT INTO product_branch_costs (product_id, branch_id, avg_unit_cost_cents)
	        VALUES ($1::uuid, $2::uuid, 500), ($1::uuid, $3::uuid, 500)`,
		prod, fw1TRT, fw1KGN)
	trentonLoc := f.branchLocation(fw1TRT)
	f.del(`DELETE FROM stock_balances WHERE product_id=$1::uuid`, prod)
	f.exec(`INSERT INTO stock_balances (product_id, location_id, branch_id, on_hand_base, allocated_base)
	        VALUES ($1::uuid, $2::uuid, $3::uuid, 2, 0)`, prod, trentonLoc, fw1TRT)
	kingstonLoc := f.branchLocation(fw1KGN)
	f.exec(`INSERT INTO stock_balances (product_id, location_id, branch_id, on_hand_base, allocated_base)
	        VALUES ($1::uuid, $2::uuid, $3::uuid, 4, 0)`, prod, kingstonLoc, fw1KGN)

	salesIn := f.openInvoice(custN, fw1TRT, w3rw1Day1, 1_700, 0)
	f.invoiceLine(salesIn, prod, 1, 1, 1_700, 1_700)
	salesOut := f.openInvoice(custS, fw1KGN, w3rw1Day1, 1_900, 0)
	f.invoiceLine(salesOut, prod, 1, 1, 1_900, 1_900)

	for _, tc := range []struct {
		set       string
		money     string
		wantTotal any
		query     reporting.DatasetQuery
	}{
		{
			set: "ar", money: "total_cents", wantTotal: int64(12_700),
			query: reporting.DatasetQuery{
				Columns: []string{"branch_id", "total_cents"},
				GroupBy: []string{"branch_id"},
				AsOf:    w3rw1AsOf,
			},
		},
		{
			set: "ap", money: "open_cents", wantTotal: int64(2_100),
			query: reporting.DatasetQuery{
				Columns: []string{"branch_id", "open_cents"},
				GroupBy: []string{"branch_id"},
				AsOf:    w3rw1AsOf,
			},
		},
		{
			set: "gl", money: "debit_cents", wantTotal: int64(4_100),
			query: reporting.DatasetQuery{
				Columns: []string{"branch_id", "debit_cents"},
				Filters: []reporting.DatasetFilter{
					{Column: "fiscal_year", Op: "eq", Values: []any{1971}},
					{Column: "fiscal_month", Op: "eq", Values: []any{3}},
				},
				GroupBy: []string{"branch_id"},
			},
		},
		{
			set: "sales", money: "line_total_cents", wantTotal: int64(1_700),
			query: reporting.DatasetQuery{
				Columns: []string{"branch_id", "line_total_cents"},
				Filters: []reporting.DatasetFilter{
					{Column: "invoice_date", Op: "ge", Values: []any{w3rw1Day1}},
					{Column: "invoice_date", Op: "le", Values: []any{w3rw1Day9}},
				},
				GroupBy: []string{"branch_id"},
			},
		},
		{
			set: "stock", money: "value_cents", wantTotal: int64(1_000),
			query: reporting.DatasetQuery{
				Columns: []string{"branch_id", "value_cents"},
				Filters: []reporting.DatasetFilter{{Column: "product_id", Op: "eq", Values: []any{prod}}},
				GroupBy: []string{"branch_id"},
			},
		},
	} {
		scoped, err := svc.RunDataset(scopedCtx, tc.set, tc.query)
		if err != nil {
			t.Fatalf("%s: scoped read: %v", tc.set, err)
		}
		if len(scoped.Rows) != 1 || scoped.Rows[0][0] != fw1TRT {
			t.Fatalf("%s: the scoped caller must see Trenton alone, got %v", tc.set, scoped.Rows)
		}
		if scoped.Totals[tc.money] != tc.wantTotal {
			t.Fatalf("%s: the scoped totals answer %v, Trenton's slice is %v",
				tc.set, scoped.Totals[tc.money], tc.wantTotal)
		}
		global, err := svc.RunDataset(globalCtx, tc.set, tc.query)
		if err != nil {
			t.Fatalf("%s: global read: %v", tc.set, err)
		}
		var globalTrenton any
		for _, row := range global.Rows {
			if row[0] == fw1TRT {
				globalTrenton = row[1]
			}
		}
		if globalTrenton != tc.wantTotal {
			t.Fatalf("%s: the global answer's Trenton slice is %v, the test wrote %v",
				tc.set, globalTrenton, tc.wantTotal)
		}
	}
}
