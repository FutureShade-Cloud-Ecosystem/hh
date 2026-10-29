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
				{Column: "fiscal_year", Op: "eq", Values: []any{time.Now().Year()}},
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
