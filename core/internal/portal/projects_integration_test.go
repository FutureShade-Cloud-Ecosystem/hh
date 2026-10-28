//go:build integration

// projects_integration_test.go, CP-03 / A-3. Proves the Projects/Jobs rollup against the
// live seeded DB: the demo project (0049) links order SO-UAT-0001 (total 24860, invoice
// INV-UAT-0001 total 24860), so its spend rolls up to the invoice total, and a cross-tenant
// read is a 404 (IDOR-safe). Requires migrations 0048/0049 applied + the 0046 UAT fixtures.
// Run: DATABASE_URL=… go test -tags integration ./internal/portal -run TestIntegration_Projects
package portal

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"
)

// randSuffix keeps parallel/re-run inserts from colliding on the UNIQUE account_no/order_no.
func randSuffix() string { return strconv.FormatInt(time.Now().UnixNano(), 36) }

const (
	uatCustomerID = "c7000000-0000-4000-9000-000000000001" // Quinte Hardscapes (0046)
	demoProjectID = "b8000000-0000-4000-9000-0000000000a1" // Maple Glen Patio (0049)
)

// TestIntegration_Projects_SpendRollup proves, against live Postgres, that a project's spend
// equals the sum of its orders' invoice totals, using a SELF-CONTAINED fixture (its own
// customer + project + order + invoice) so the test never depends on whether the optional
// 0046 UAT order was seeded on this DB. It cleans up after itself.
func TestIntegration_Projects_SpendRollup(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	svc := NewProjectsService(NewProjectRepository(pool))

	suffix := randSuffix()
	var custID, projID, orderID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO customers (account_no, name) VALUES ($1, 'CP-03 Rollup Test Co') RETURNING id::text`,
		"CP03-"+suffix).Scan(&custID); err != nil {
		t.Fatalf("insert customer: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM customers WHERE id = $1`, custID) })

	if err := pool.QueryRow(ctx, `
		INSERT INTO projects (customer_id, name, job_address) VALUES ($1, 'Rollup Test Job', '1 Test Rd')
		RETURNING id::text`, custID).Scan(&projID); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	// An order tagged to the project + two invoices via the order (spend = 12000 + 8000).
	branch := "5d1b3f6e-0000-4000-9000-000000000001"
	if err := pool.QueryRow(ctx, `
		INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, requested_date,
		                    subtotal_cents, tax_cents, total_cents, project_id)
		VALUES ($1, $2, $3, 'INVOICED', 'PICKUP', CURRENT_DATE, 20000, 0, 20000, $4)
		RETURNING id::text`, "SO-CP03-"+suffix, custID, branch, projID).Scan(&orderID); err != nil {
		t.Fatalf("insert order: %v", err)
	}
	for i, cents := range []int64{12000, 8000} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO invoices (invoice_no, customer_id, order_id, branch_id, status, issue_date, due_date,
			                      subtotal_cents, tax_cents, total_cents, balance_cents)
			VALUES ($1, $2, $3, $4, 'FINALIZED', (now() AT TIME ZONE 'America/Toronto')::date, (now() AT TIME ZONE 'America/Toronto')::date + 30, $5, 0, $5, $5)`,
			fmt.Sprintf("INV-CP03-%s-%d", suffix, i), custID, orderID, branch, cents); err != nil {
			t.Fatalf("insert invoice %d: %v", i, err)
		}
	}

	d, err := svc.GetProject(ctx, custID, projID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if d.SpendCents != 20000 {
		t.Fatalf("spend = %d, want 20000 (Σ of two invoices 12000+8000)", d.SpendCents)
	}
	if d.OrderCount != 1 {
		t.Fatalf("order_count = %d, want 1", d.OrderCount)
	}
	// The drill-down invoice list total must reconcile with the rolled-up spend.
	var drillSum int64
	for _, iv := range d.Invoices {
		drillSum += iv.TotalCents
	}
	if drillSum != d.SpendCents {
		t.Fatalf("invoice drill total %d != spend %d", drillSum, d.SpendCents)
	}
}

// TestIntegration_Projects_DemoSeed confirms the 0049 demo project seeded and, when the 0046
// UAT order is present on this DB, that it links + rolls up to the invoice total. On a DB
// without the UAT order fixtures the linkage half skips (the seed UPDATE is order-guarded).
func TestIntegration_Projects_DemoSeed(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	svc := NewProjectsService(NewProjectRepository(pool))

	var exists bool
	_ = pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM projects WHERE id = $1)`, demoProjectID).Scan(&exists)
	if !exists {
		t.Skip("demo project 0049 not seeded (UAT customer absent on this DB)")
	}
	d, err := svc.GetProject(ctx, uatCustomerID, demoProjectID)
	if err != nil {
		t.Fatalf("GetProject(demo): %v", err)
	}
	if d.Name != "Maple Glen Patio" {
		t.Fatalf("demo project name = %q, want Maple Glen Patio", d.Name)
	}
	var orderPresent bool
	_ = pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM orders WHERE id = '07000000-0000-4000-9000-0000000000a1')`).Scan(&orderPresent)
	if !orderPresent {
		t.Skip("UAT order SO-UAT-0001 not seeded on this DB; linkage/rollup asserted by TestIntegration_Projects_SpendRollup instead")
	}
	if d.OrderCount < 1 {
		t.Fatalf("demo order_count = %d, want >= 1 (SO-UAT-0001 links to the demo project)", d.OrderCount)
	}
	var invSum int64
	_ = pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(i.total_cents),0) FROM invoices i JOIN orders o ON o.id = i.order_id
		WHERE o.project_id = $1`, demoProjectID).Scan(&invSum)
	if d.SpendCents != invSum {
		t.Fatalf("demo spend %d != Σ invoices %d", d.SpendCents, invSum)
	}
}

// TestIntegration_Projects_SpendExcludesVoidAndDraft is the regression for the MAJOR money bug:
// project spend (and the invoice drill list) must count ONLY money-bearing invoices -
// FINALIZED/PARTIALLY_PAID/PAID, and exclude VOID (reversed money) and DRAFT (not yet a
// receivable). Fixture: one order with a FINALIZED (20000), a VOID (24860) and a DRAFT (5000)
// invoice; spend must be 20000 and the drill list must contain exactly the one FINALIZED row.
func TestIntegration_Projects_SpendExcludesVoidAndDraft(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	svc := NewProjectsService(NewProjectRepository(pool))

	suffix := randSuffix()
	var custID, projID, orderID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO customers (account_no, name) VALUES ($1, 'CP-03 VoidDraft Co') RETURNING id::text`,
		"CP03V-"+suffix).Scan(&custID); err != nil {
		t.Fatalf("insert customer: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM customers WHERE id = $1`, custID) })

	if err := pool.QueryRow(ctx, `
		INSERT INTO projects (customer_id, name, job_address) VALUES ($1, 'VoidDraft Job', '2 Test Rd')
		RETURNING id::text`, custID).Scan(&projID); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	branch := "5d1b3f6e-0000-4000-9000-000000000001"
	if err := pool.QueryRow(ctx, `
		INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, requested_date,
		                    subtotal_cents, tax_cents, total_cents, project_id)
		VALUES ($1, $2, $3, 'INVOICED', 'PICKUP', CURRENT_DATE, 20000, 0, 20000, $4)
		RETURNING id::text`, "SO-CP03V-"+suffix, custID, branch, projID).Scan(&orderID); err != nil {
		t.Fatalf("insert order: %v", err)
	}
	seed := []struct {
		status string
		cents  int64
	}{{"FINALIZED", 20000}, {"VOID", 24860}, {"DRAFT", 5000}}
	for i, s := range seed {
		if _, err := pool.Exec(ctx, `
			INSERT INTO invoices (invoice_no, customer_id, order_id, branch_id, status, issue_date, due_date,
			                      subtotal_cents, tax_cents, total_cents, balance_cents)
			VALUES ($1, $2, $3, $4, $5, (now() AT TIME ZONE 'America/Toronto')::date, (now() AT TIME ZONE 'America/Toronto')::date + 30, $6, 0, $6, $6)`,
			fmt.Sprintf("INV-CP03V-%s-%d", suffix, i), custID, orderID, branch, s.status, s.cents); err != nil {
			t.Fatalf("insert invoice %s: %v", s.status, err)
		}
	}

	d, err := svc.GetProject(ctx, custID, projID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if d.SpendCents != 20000 {
		t.Fatalf("spend = %d, want 20000 (only the FINALIZED invoice; VOID+DRAFT excluded)", d.SpendCents)
	}
	if len(d.Invoices) != 1 || d.Invoices[0].Status != "FINALIZED" {
		t.Fatalf("drill list = %+v, want exactly one FINALIZED invoice (VOID/DRAFT excluded)", d.Invoices)
	}
	// And the list view must agree with the dashboard.
	list, err := svc.GetProjectDashboard(ctx, custID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var found bool
	for _, p := range list {
		if p.ID == projID {
			found = true
			if p.SpendCents != 20000 {
				t.Fatalf("list spend = %d, want 20000", p.SpendCents)
			}
		}
	}
	if !found {
		t.Fatal("project missing from list view")
	}
}

// TestIntegration_Projects_QuoteVarianceDedup is the regression for the BLOCKING money bug:
// a single quote can back MULTIPLE orders on a project (orders.quote_id has no UNIQUE), and the
// quoted-vs-actual variance must count that quote ONCE, not once per order. Fixture: quote Q
// (28000) linked to TWO orders O1/O2 of the same project; QuotedCents must be 28000 (not 56000).
func TestIntegration_Projects_QuoteVarianceDedup(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	svc := NewProjectsService(NewProjectRepository(pool))

	suffix := randSuffix()
	var custID, projID, quoteID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO customers (account_no, name) VALUES ($1, 'CP-03 QuoteDedup Co') RETURNING id::text`,
		"CP03Q-"+suffix).Scan(&custID); err != nil {
		t.Fatalf("insert customer: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM customers WHERE id = $1`, custID) })

	if err := pool.QueryRow(ctx, `
		INSERT INTO projects (customer_id, name, job_address) VALUES ($1, 'QuoteDedup Job', '3 Test Rd')
		RETURNING id::text`, custID).Scan(&projID); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	branch := "5d1b3f6e-0000-4000-9000-000000000001"
	if err := pool.QueryRow(ctx, `
		INSERT INTO quotes (quote_no, customer_id, branch_id, status, subtotal_cents, tax_cents, total_cents)
		VALUES ($1, $2, $3, 'ACCEPTED', 28000, 0, 28000) RETURNING id::text`,
		"Q-CP03Q-"+suffix, custID, branch).Scan(&quoteID); err != nil {
		t.Fatalf("insert quote: %v", err)
	}
	// TWO orders on the same project, both referencing the SAME quote, the fan-out that used
	// to double-count. Each order also gets one FINALIZED invoice (spend = 15000 + 15000).
	for i := 0; i < 2; i++ {
		var orderID string
		if err := pool.QueryRow(ctx, `
			INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, requested_date,
			                    subtotal_cents, tax_cents, total_cents, project_id, quote_id)
			VALUES ($1, $2, $3, 'INVOICED', 'PICKUP', CURRENT_DATE, 15000, 0, 15000, $4, $5)
			RETURNING id::text`,
			fmt.Sprintf("SO-CP03Q-%s-%d", suffix, i), custID, branch, projID, quoteID).Scan(&orderID); err != nil {
			t.Fatalf("insert order %d: %v", i, err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO invoices (invoice_no, customer_id, order_id, branch_id, status, issue_date, due_date,
			                      subtotal_cents, tax_cents, total_cents, balance_cents)
			VALUES ($1, $2, $3, $4, 'FINALIZED', (now() AT TIME ZONE 'America/Toronto')::date, (now() AT TIME ZONE 'America/Toronto')::date + 30, 15000, 0, 15000, 15000)`,
			fmt.Sprintf("INV-CP03Q-%s-%d", suffix, i), custID, orderID, branch); err != nil {
			t.Fatalf("insert invoice %d: %v", i, err)
		}
	}

	d, err := svc.GetProject(ctx, custID, projID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if d.QuotedCents != 28000 {
		t.Fatalf("quoted = %d, want 28000 (the single quote counted ONCE, not once per order)", d.QuotedCents)
	}
	if d.SpendCents != 30000 {
		t.Fatalf("spend = %d, want 30000 (two 15000 invoices)", d.SpendCents)
	}
	if d.VarianceCents != 2000 {
		t.Fatalf("variance = %d, want 2000 (spend 30000 − quoted 28000); a double-counted quote would give -26000", d.VarianceCents)
	}
}

// TestIntegration_Projects_QuoteSplitAcrossProjects is the regression for the CP-03 QuotedCents
// cross-project double-attribution: orders.quote_id is non-unique AND orders.project_id is
// nullable/freely set, so a SINGLE quote can back orders on TWO different projects. The old
// QuotedCents summed the quote's FULL total for every project whose orders referenced it, so a
// quote split across P1/P2 inflated BOTH projects' quoted (and thus variance). The fix attributes
// each quote to a project only by the SHARE of the quote's order totals on that project, so summed
// across the projects it touches it reconstructs the quote total exactly ONCE.
//
// Fixture: quote Q total 30000, two orders, O1 (total 10000) on project P1, O2 (total 20000) on
// project P2, both referencing Q. Correct QuotedCents: P1 = 30000*10000/30000 = 10000; P2 =
// 30000*20000/30000 = 20000 (sum 30000, the quote once). The OLD code gave P1 = 30000 AND
// P2 = 30000 (sum 60000, the quote double-counted, inflating each project's variance).
func TestIntegration_Projects_QuoteSplitAcrossProjects(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	svc := NewProjectsService(NewProjectRepository(pool))

	suffix := randSuffix()
	var custID, projID1, projID2, quoteID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO customers (account_no, name) VALUES ($1, 'CP-03 QuoteSplit Co') RETURNING id::text`,
		"CP03S-"+suffix).Scan(&custID); err != nil {
		t.Fatalf("insert customer: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM customers WHERE id = $1`, custID) })

	if err := pool.QueryRow(ctx, `
		INSERT INTO projects (customer_id, name, job_address) VALUES ($1, 'Split Job A', '10 Split Rd')
		RETURNING id::text`, custID).Scan(&projID1); err != nil {
		t.Fatalf("insert project1: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO projects (customer_id, name, job_address) VALUES ($1, 'Split Job B', '20 Split Rd')
		RETURNING id::text`, custID).Scan(&projID2); err != nil {
		t.Fatalf("insert project2: %v", err)
	}
	branch := "5d1b3f6e-0000-4000-9000-000000000001"
	// One quote, total 30000, backing an order on EACH project.
	if err := pool.QueryRow(ctx, `
		INSERT INTO quotes (quote_no, customer_id, branch_id, status, subtotal_cents, tax_cents, total_cents)
		VALUES ($1, $2, $3, 'ACCEPTED', 30000, 0, 30000) RETURNING id::text`,
		"Q-CP03S-"+suffix, custID, branch).Scan(&quoteID); err != nil {
		t.Fatalf("insert quote: %v", err)
	}
	mkOrder := func(tag, projID string, cents int64) {
		if _, err := pool.Exec(ctx, `
			INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, requested_date,
			                    subtotal_cents, tax_cents, total_cents, project_id, quote_id)
			VALUES ($1, $2, $3, 'INVOICED', 'PICKUP', CURRENT_DATE, $5, 0, $5, $4, $6)`,
			"SO-CP03S-"+suffix+"-"+tag, custID, branch, projID, cents, quoteID); err != nil {
			t.Fatalf("insert order %s: %v", tag, err)
		}
	}
	mkOrder("a", projID1, 10000) // P1's share of the quote's orders
	mkOrder("b", projID2, 20000) // P2's share of the quote's orders

	d1, err := svc.GetProject(ctx, custID, projID1)
	if err != nil {
		t.Fatalf("GetProject P1: %v", err)
	}
	d2, err := svc.GetProject(ctx, custID, projID2)
	if err != nil {
		t.Fatalf("GetProject P2: %v", err)
	}
	if d1.QuotedCents != 10000 {
		t.Fatalf("P1 quoted = %d, want 10000 (its 10000/30000 share of the split quote); "+
			"the old full-total attribution gave 30000", d1.QuotedCents)
	}
	if d2.QuotedCents != 20000 {
		t.Fatalf("P2 quoted = %d, want 20000 (its 20000/30000 share of the split quote); "+
			"the old full-total attribution gave 30000", d2.QuotedCents)
	}
	if got := d1.QuotedCents + d2.QuotedCents; got != 30000 {
		t.Fatalf("split quote reconstructs to %d across both projects, want 30000 (the quote counted "+
			"exactly once, never double-attributed)", got)
	}
}

// TestIntegration_Projects_VarianceQuotedVsQuoted is the regression for the MAJOR money bug
// (projects.go:206): the quoted-vs-actual VARIANCE must compare LIKE-FOR-LIKE, the spend on
// the QUOTED work vs the quoted amount, and must NOT mix all-orders spend (which includes
// ad-hoc, unquoted orders) against a quoted-orders-only quoted total. An ad-hoc order with no
// quote must inflate the SpendCents headline but leave VarianceCents untouched.
//
// Fixture (one project):
//   - Quote Q = 20000, ACCEPTED, backing ONE order O_q (INVOICED) whose FINALIZED invoice = 20000.
//     → the quoted work came in exactly on quote (quoted-spend 20000 vs quoted 20000).
//   - An AD-HOC order O_x (quote_id NULL) with a FINALIZED invoice = 50000, pure add-on spend.
//
// Correct rollup: SpendCents = 70000 (both invoices), QuotedCents = 20000,
// QuotedSpendCents = 20000, VarianceCents = 0 (on quote).
// The OLD buggy code computed VarianceCents = SpendCents − QuotedCents = 70000 − 20000 = +50000,
// a FALSE 50000 overrun manufactured entirely by the ad-hoc order. This test asserts variance
// == 0 and so FAILS against the pre-fix code.
func TestIntegration_Projects_VarianceQuotedVsQuoted(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	svc := NewProjectsService(NewProjectRepository(pool))

	suffix := randSuffix()
	var custID, projID, quoteID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO customers (account_no, name) VALUES ($1, 'CP-03 MixedVariance Co') RETURNING id::text`,
		"CP03M-"+suffix).Scan(&custID); err != nil {
		t.Fatalf("insert customer: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM customers WHERE id = $1`, custID) })

	if err := pool.QueryRow(ctx, `
		INSERT INTO projects (customer_id, name, job_address) VALUES ($1, 'MixedVariance Job', '4 Test Rd')
		RETURNING id::text`, custID).Scan(&projID); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	branch := "5d1b3f6e-0000-4000-9000-000000000001"
	if err := pool.QueryRow(ctx, `
		INSERT INTO quotes (quote_no, customer_id, branch_id, status, subtotal_cents, tax_cents, total_cents)
		VALUES ($1, $2, $3, 'ACCEPTED', 20000, 0, 20000) RETURNING id::text`,
		"Q-CP03M-"+suffix, custID, branch).Scan(&quoteID); err != nil {
		t.Fatalf("insert quote: %v", err)
	}

	// mkOrderWithInvoice inserts one order (quote-linked or ad-hoc) + one FINALIZED invoice.
	mkOrderWithInvoice := func(tag string, quote *string, cents int64) {
		var orderID string
		if err := pool.QueryRow(ctx, `
			INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, requested_date,
			                    subtotal_cents, tax_cents, total_cents, project_id, quote_id)
			VALUES ($1, $2, $3, 'INVOICED', 'PICKUP', CURRENT_DATE, $6, 0, $6, $4, $5)
			RETURNING id::text`,
			"SO-CP03M-"+suffix+"-"+tag, custID, branch, projID, quote, cents).Scan(&orderID); err != nil {
			t.Fatalf("insert order %s: %v", tag, err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO invoices (invoice_no, customer_id, order_id, branch_id, status, issue_date, due_date,
			                      subtotal_cents, tax_cents, total_cents, balance_cents)
			VALUES ($1, $2, $3, $4, 'FINALIZED', CURRENT_DATE, CURRENT_DATE + 30, $5, 0, $5, $5)`,
			"INV-CP03M-"+suffix+"-"+tag, custID, orderID, branch, cents); err != nil {
			t.Fatalf("insert invoice %s: %v", tag, err)
		}
	}
	mkOrderWithInvoice("q", &quoteID, 20000) // quoted work, on quote
	mkOrderWithInvoice("x", nil, 50000)      // ad-hoc add-on, no quote

	d, err := svc.GetProject(ctx, custID, projID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if d.SpendCents != 70000 {
		t.Fatalf("spend = %d, want 70000 (quoted 20000 + ad-hoc 50000, the total headline)", d.SpendCents)
	}
	if d.QuotedCents != 20000 {
		t.Fatalf("quoted = %d, want 20000", d.QuotedCents)
	}
	if d.QuotedSpendCents != 20000 {
		t.Fatalf("quoted-spend = %d, want 20000 (ONLY the quote-originated order's invoice)", d.QuotedSpendCents)
	}
	if d.VarianceCents != 0 {
		t.Fatalf("variance = %d, want 0 (quoted-spend 20000 − quoted 20000). The pre-fix code "+
			"gave +50000: a FALSE overrun manufactured by the ad-hoc unquoted order.", d.VarianceCents)
	}
}

// TestIntegration_Projects_DeliveryCountExcludesDraftAndCancelled is the regression for the
// MAJOR delivery-count bug (projects.go:159/:200): the subquery counted DISTINCT manifests
// touching the project's orders with NO status filter, so DRAFT and CANCELLED manifests were
// counted as deliveries, contradicting the DTO contract ('scheduled/dispatched manifests').
// The fix JOINs manifests and restricts to FINAL/DISPATCHED/COMPLETED (real deliveries).
//
// Fixture (one project, one order): three manifests each with a line on the order -
// one DRAFT, one CANCELLED, one DISPATCHED. delivery_count must be 1 (only the DISPATCHED),
// on BOTH the detail dashboard and the list view. The pre-fix code returned 3.
func TestIntegration_Projects_DeliveryCountExcludesDraftAndCancelled(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	svc := NewProjectsService(NewProjectRepository(pool))

	suffix := randSuffix()
	var custID, projID, orderID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO customers (account_no, name) VALUES ($1, 'CP-03 DeliveryCount Co') RETURNING id::text`,
		"CP03D-"+suffix).Scan(&custID); err != nil {
		t.Fatalf("insert customer: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM customers WHERE id = $1`, custID) })

	if err := pool.QueryRow(ctx, `
		INSERT INTO projects (customer_id, name, job_address) VALUES ($1, 'DeliveryCount Job', '5 Test Rd')
		RETURNING id::text`, custID).Scan(&projID); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	branch := "5d1b3f6e-0000-4000-9000-000000000001"
	if err := pool.QueryRow(ctx, `
		INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, requested_date,
		                    subtotal_cents, tax_cents, total_cents, project_id)
		VALUES ($1, $2, $3, 'INVOICED', 'DELIVERY', CURRENT_DATE, 20000, 0, 20000, $4)
		RETURNING id::text`, "SO-CP03D-"+suffix, custID, branch, projID).Scan(&orderID); err != nil {
		t.Fatalf("insert order: %v", err)
	}

	// Three manifests, each with one line referencing the project's order. Only the DISPATCHED
	// one is a real delivery; DRAFT (not yet) and CANCELLED (never happened) must not count.
	for i, status := range []string{"DRAFT", "CANCELLED", "DISPATCHED"} {
		var manifestID string
		if err := pool.QueryRow(ctx, `
			INSERT INTO manifests (manifest_no, branch_id, status, delivery_date)
			VALUES ($1, $2, $3, CURRENT_DATE) RETURNING id::text`,
			fmt.Sprintf("MF-CP03D-%s-%d", suffix, i), branch, status).Scan(&manifestID); err != nil {
			t.Fatalf("insert manifest %s: %v", status, err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO manifest_lines (manifest_id, line_no, order_id, qty_base)
			VALUES ($1, 1, $2, 1)`, manifestID, orderID); err != nil {
			t.Fatalf("insert manifest_line %s: %v", status, err)
		}
	}

	d, err := svc.GetProject(ctx, custID, projID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if d.DeliveryCount != 1 {
		t.Fatalf("delivery_count = %d, want 1 (only the DISPATCHED manifest; DRAFT+CANCELLED excluded). "+
			"The pre-fix unfiltered count returned 3.", d.DeliveryCount)
	}
	// The list view must agree.
	list, err := svc.GetProjectDashboard(ctx, custID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var found bool
	for _, p := range list {
		if p.ID == projID {
			found = true
			if p.DeliveryCount != 1 {
				t.Fatalf("list delivery_count = %d, want 1 (DRAFT+CANCELLED excluded)", p.DeliveryCount)
			}
		}
	}
	if !found {
		t.Fatal("project missing from list view")
	}
}

// TestIntegration_Projects_QuotedResidualReconciles is the regression for the MINOR QuotedCents
// rounding-drift bug (projects.go:211): a per-project proportional share ROUND()ed independently
// drifts off the quote total when the split is non-divisible. Fixture: quote Q total 100c split
// 1:1:1 across three projects (one order of 1c each, equal shares). Each project's exact share
// is 33.33…c; the old ROUND gave 33+33+33 = 99 (1c lost). The largest-remainder fix floors to
// 33 and distributes the 1 leftover cent to exactly one project, so the shares sum to EXACTLY 100.
func TestIntegration_Projects_QuotedResidualReconciles(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	svc := NewProjectsService(NewProjectRepository(pool))

	suffix := randSuffix()
	var custID, quoteID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO customers (account_no, name) VALUES ($1, 'CP-03 Residual Co') RETURNING id::text`,
		"CP03R-"+suffix).Scan(&custID); err != nil {
		t.Fatalf("insert customer: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM customers WHERE id = $1`, custID) })

	branch := "5d1b3f6e-0000-4000-9000-000000000001"
	// Quote total 100c, split 1:1:1 across three projects (non-divisible: 100/3 = 33.33…).
	if err := pool.QueryRow(ctx, `
		INSERT INTO quotes (quote_no, customer_id, branch_id, status, subtotal_cents, tax_cents, total_cents)
		VALUES ($1, $2, $3, 'ACCEPTED', 100, 0, 100) RETURNING id::text`,
		"Q-CP03R-"+suffix, custID, branch).Scan(&quoteID); err != nil {
		t.Fatalf("insert quote: %v", err)
	}

	projIDs := make([]string, 3)
	for i := 0; i < 3; i++ {
		if err := pool.QueryRow(ctx, `
			INSERT INTO projects (customer_id, name, job_address) VALUES ($1, $2, '6 Test Rd')
			RETURNING id::text`, custID, fmt.Sprintf("Residual Job %d", i)).Scan(&projIDs[i]); err != nil {
			t.Fatalf("insert project %d: %v", i, err)
		}
		// One 1c order per project on the SAME quote → equal 1:1:1 order-total shares.
		if _, err := pool.Exec(ctx, `
			INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, requested_date,
			                    subtotal_cents, tax_cents, total_cents, project_id, quote_id)
			VALUES ($1, $2, $3, 'INVOICED', 'PICKUP', CURRENT_DATE, 1, 0, 1, $4, $5)`,
			fmt.Sprintf("SO-CP03R-%s-%d", suffix, i), custID, branch, projIDs[i], quoteID); err != nil {
			t.Fatalf("insert order %d: %v", i, err)
		}
	}

	var sum int64
	shares := make([]int64, 3)
	for i, pid := range projIDs {
		d, err := svc.GetProject(ctx, custID, pid)
		if err != nil {
			t.Fatalf("GetProject %d: %v", i, err)
		}
		shares[i] = d.QuotedCents
		sum += d.QuotedCents
	}
	if sum != 100 {
		t.Fatalf("split-quote shares %v sum to %d, want EXACTLY 100 (largest-remainder reconciliation). "+
			"The old per-project ROUND(33.33)=33 gave 99.", shares, sum)
	}
	// Sanity: each share is a floor (33) or floor+1 (34); nothing negative or wild.
	for i, s := range shares {
		if s != 33 && s != 34 {
			t.Fatalf("project %d share = %d, want 33 or 34 (floor or floor+residual)", i, s)
		}
	}
}

func TestIntegration_Projects_IDOR_CrossTenant404(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	svc := NewProjectsService(NewProjectRepository(pool))

	var exists bool
	_ = pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM projects WHERE id = $1)`, demoProjectID).Scan(&exists)
	if !exists {
		t.Skip("demo project not seeded")
	}

	// A DIFFERENT customer id asking for the UAT customer's project must get ErrProjectNotFound
	// (never the project's data).
	otherCustomer := "c7000000-0000-4000-9000-0000000000ff"
	if _, err := svc.GetProject(ctx, otherCustomer, demoProjectID); err != ErrProjectNotFound {
		t.Fatalf("cross-tenant read err = %v, want ErrProjectNotFound (IDOR-safe 404)", err)
	}
	// And the other customer's project list must not include it.
	list, err := svc.GetProjectDashboard(ctx, otherCustomer)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, p := range list {
		if p.ID == demoProjectID {
			t.Fatal("cross-tenant list leaked the UAT customer's project")
		}
	}
}
