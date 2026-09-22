//go:build integration

package reporting

// fixture_integration_test.go, the harness the 22 named per-template tests share.
//
// -- HOW THESE TESTS GET EXACT NUMBERS OUT OF A SHARED DATABASE ---------------------------------------
// Every report here aggregates: "sales by day" sums whatever invoices exist on that day. Asserting an
// exact figure against a database that other packages are writing to at the same time is therefore
// impossible, UNLESS the test owns its slice of the data.
//
// It does, by owning a DATE WINDOW nobody else uses. Each test takes a private ten-day window in a
// randomly-chosen stretch of the early 1970s, writes its fixture there, and runs the report over
// exactly that window. Nothing else in the system dates anything to 1971, so the report's rows are the
// fixture's rows and the totals are arithmetic the test wrote down in advance.
//
// The window base is randomised ONCE PER PROCESS rather than fixed, so a run that dies before its
// cleanup cannot poison the next run's assertions, the next run is simply somewhere else.
//
// Reports with no date parameter (inventory valuation, AR aging, below-reorder, PO status) cannot use a
// window. Those assert on their OWN FIXTURE ROW inside the result rather than on the grand total, which
// is the same discipline stated differently: never assert a whole-database number.
//
// -- FIXTURES RESTORE WHAT THEY TOUCH -----------------------------------------------------------------
// Every insert registers its own delete, and cleanup runs in reverse insertion order so foreign keys
// unwind cleanly. This is not tidiness: several of these tables feed live operational surfaces (AR
// aging, the reorder report, the recovery board), and a suite that left rows behind would put phantom
// money on a screen a manager is meant to act on.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"sync/atomic"
	"testing"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	trentonBranch  = "5d1b3f6e-0000-4000-9000-000000000001"
	kingstonBranch = "5d1b3f6e-0000-4000-9000-000000000002"
)

// windowBase is this process's private stretch of history. Chosen once, in the early 1970s, far from
// any real business date and far from any other test's fixtures.
var windowBase = time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC).
	AddDate(0, 0, rand.Intn(1200)) //nolint:gosec // test-only spread, not a security decision

var windowSeq atomic.Int64

// window hands out the next private ten-day window as (from, to) date strings.
func window() (string, string) {
	n := windowSeq.Add(1)
	from := windowBase.AddDate(0, 0, int(n)*10)
	return from.Format(DateLayout), from.AddDate(0, 0, 9).Format(DateLayout)
}

// intPool opens the integration database, or SKIPS when DATABASE_URL is unset (and FAILS when it is set
// but unreachable, a silent skip on a configured URL is how a broken suite passes CI).
func intPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url, set := os.LookupEnv("DATABASE_URL")
	fail := func(format string, a ...any) {
		if set {
			t.Fatalf(format, a...)
		}
		t.Skipf(format, a...)
	}
	if url == "" {
		fail("DATABASE_URL is unset, reporting integration tests need a migrated + seeded database")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		fail("integration DB unavailable: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		fail("integration DB not reachable: %v", err)
	}
	var ok bool
	if err := pool.QueryRow(context.Background(),
		`SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_name='report_templates')`).
		Scan(&ok); err != nil || !ok {
		pool.Close()
		fail("integration DB missing the reporting schema, run cmd/migrate first: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// -- DIB-396 lease (test-only) ------------------------------------------------
// Copied verbatim in behaviour from internal/gate/dib396_lease_test.go. The inventory fixtures here
// write stock_moves and stock_balances together, and although they are written CONSISTENTLY (both
// sides, same quantity), the two statements are not one transaction from the point of view of another
// package's whole-DB drift gate. Taking the shared lease for the length of those tests keeps four
// consecutive zero-flake waves at four.

const dib396StockDriftLease = int64(3960001)

func dib396LeaseShared(t *testing.T, pool *pgxpool.Pool, key int64) {
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

// -- the service under test ---------------------------------------------------

// globalSvc builds the real Service with a GLOBAL (leadership) caller, which is what a controller
// running a consolidated report is.
func globalSvc(t *testing.T, pool *pgxpool.Pool) *Service {
	t.Helper()
	return NewService(NewRepository(pool)).withCaller(func(context.Context) (identity.StaffContext, bool) {
		return identity.StaffContext{
			UserID: fixtureActor(t, pool), RoleCode: "CONTROLLER",
			Tier: identity.TierLeadership, Scope: identity.ScopeGlobal, Status: "ACTIVE",
		}, true
	})
}

// scopedSvc builds a Service whose caller can reach exactly one branch.
func scopedSvc(t *testing.T, pool *pgxpool.Pool, branchID string) *Service {
	t.Helper()
	return NewService(NewRepository(pool)).withCaller(func(context.Context) (identity.StaffContext, bool) {
		return identity.StaffContext{
			UserID: fixtureActor(t, pool), RoleCode: "BRANCH_MANAGER",
			Tier: identity.TierManager, Scope: identity.ScopeBranch, Status: "ACTIVE",
			Branches: map[string]bool{branchID: true},
		}, true
	})
}

// fixtureActor returns a real users.id so FK-bearing writes (print jobs, runs) are honest.
func fixtureActor(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(),
		`SELECT id::text FROM users WHERE status='ACTIVE' ORDER BY created_at LIMIT 1`).Scan(&id); err != nil {
		t.Fatalf("no seeded user to act as: %v", err)
	}
	return id
}

// -- the fixture builder ------------------------------------------------------

type fixture struct {
	t    *testing.T
	pool *pgxpool.Pool
	ctx  context.Context
	tag  string
}

// newFixture starts an island of data that cleans itself up.
func newFixture(t *testing.T, pool *pgxpool.Pool) *fixture {
	t.Helper()
	return &fixture{t: t, pool: pool, ctx: context.Background(),
		tag: "W6B-" + uuid.NewString()[:8]}
}

// exec runs one statement, failing the test with the statement on error.
func (f *fixture) exec(sql string, args ...any) {
	f.t.Helper()
	if _, err := f.pool.Exec(f.ctx, sql, args...); err != nil {
		f.t.Fatalf("fixture: %v\nSQL: %s\nargs: %v", err, sql, args)
	}
}

// del registers a cleanup delete, run in reverse order (t.Cleanup is LIFO), so FKs unwind.
func (f *fixture) del(sql string, args ...any) {
	f.t.Cleanup(func() {
		if _, err := f.pool.Exec(context.Background(), sql, args...); err != nil {
			f.t.Errorf("fixture cleanup failed, this leaves rows on a live surface: %v\nSQL: %s", err, sql)
		}
	})
}

func (f *fixture) newID() string { return uuid.NewString() }

// category creates a product category.
func (f *fixture) category(name string) string {
	id := f.newID()
	f.del(`DELETE FROM product_categories WHERE id=$1::uuid`, id)
	f.exec(`INSERT INTO product_categories (id, code, name) VALUES ($1::uuid, $2, $3)`,
		id, f.tag+"-"+name, f.tag+" "+name)
	return id
}

// product creates a product in a category.
func (f *fixture) product(sku, name, categoryID string) string {
	id := f.newID()
	// Two deletes, and the ORDER matters: cleanup is LIFO, so the invoice_lines delete registered here
	// runs AFTER the products delete would have... which is exactly wrong. Registering the child delete
	// FIRST means it runs LAST, so instead both are registered against this product and the child one is
	// registered SECOND, see below. The practical rule this encodes: a fixture must not depend on the
	// order its callers happen to create things in, because a test that creates a product after an
	// invoice would otherwise fail its own cleanup.
	f.del(`DELETE FROM products WHERE id=$1::uuid`, id)
	f.del(`DELETE FROM invoice_lines WHERE product_id=$1::uuid`, id)
	f.del(`DELETE FROM purchasing_po_lines WHERE product_id=$1::uuid`, id)
	f.exec(`INSERT INTO products (id, sku, name, category_id, base_uom, price_uom, is_active)
	        VALUES ($1::uuid, $2, $3, NULLIF($4,'')::uuid, 'EA', 'EA', TRUE)`,
		id, f.tag+"-"+sku, f.tag+" "+name, categoryID)
	return id
}

// customer creates a customer.
func (f *fixture) customer(name string) string {
	id := f.newID()
	f.del(`DELETE FROM customers WHERE id=$1::uuid`, id)
	f.exec(`INSERT INTO customers (id, account_no, name) VALUES ($1::uuid, $2, $3)`,
		id, f.tag+"-"+name, f.tag+" "+name)
	return id
}

// vendor creates a vendor.
func (f *fixture) vendor(name string) string {
	id := f.newID()
	f.del(`DELETE FROM vendors WHERE id=$1::uuid`, id)
	f.exec(`INSERT INTO vendors (id, vendor_no, name) VALUES ($1::uuid, $2, $3)`,
		id, f.tag+"-"+name, f.tag+" "+name)
	return id
}

// order creates an order (needed wherever an invoice must hang off one for rep attribution).
func (f *fixture) order(customerID, branchID, date string) string {
	id := f.newID()
	f.del(`DELETE FROM orders WHERE id=$1::uuid`, id)
	f.exec(`INSERT INTO orders (id, order_no, customer_id, branch_id, status, fulfillment, requested_date,
	          subtotal_cents, tax_cents, total_cents)
	        VALUES ($1::uuid, $2, $3::uuid, $4::uuid, 'INVOICED', 'PICKUP', $5::date, 0, 0, 0)`,
		id, f.tag+"-"+id[:6], customerID, branchID, date)
	return id
}

// invoicePaid creates a PAID invoice with a zero balance. PAID-with-no-balance is deliberate for every
// test that is not about AR: a FINALIZED invoice with an open balance would show up in the LIVE aged-debt
// view for the length of the test, which is a number somebody is meant to act on.
func (f *fixture) invoicePaid(customerID, branchID, orderID, date string, subtotal, tax int64) string {
	return f.invoice(customerID, branchID, orderID, date, subtotal, tax, "PAID", 0)
}

func (f *fixture) invoice(customerID, branchID, orderID, date string, subtotal, tax int64, status string, balance int64) string {
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

// invoiceLine adds a line. qty is base units; unitPrice and lineTotal are integer cents.
func (f *fixture) invoiceLine(invoiceID, productID string, lineNo int, qty float64, unitPriceCents, lineTotalCents int64) string {
	id := f.newID()
	f.exec(`INSERT INTO invoice_lines (id, invoice_id, line_no, product_id, description, qty_entered,
	          uom_entered, qty_base, unit_price_cents, price_uom, line_total_cents, tax_cents)
	        VALUES ($1::uuid, $2::uuid, $3, $4::uuid, $5, $6, 'EA', $6, $7, 'EA', $8, 0)`,
		id, invoiceID, lineNo, productID, f.tag+" line", qty, unitPriceCents, lineTotalCents)
	return id
}

// repAssignment writes a customer-level (orderID "") or per-order rep assignment.
func (f *fixture) repAssignment(customerID, orderID, repUserID string) string {
	id := f.newID()
	f.del(`DELETE FROM rep_assignments WHERE id=$1::uuid`, id)
	f.exec(`INSERT INTO rep_assignments (id, customer_id, order_id, rep_user_id, source)
	        VALUES ($1::uuid, $2::uuid, NULLIF($3,'')::uuid, $4::uuid, 'MANUAL')`,
		id, customerID, orderID, repUserID)
	return id
}

// stockReceipt writes a RECEIPT move AND the matching balance, so the pair is internally consistent and
// the weighted-average cost view has something to average.
func (f *fixture) stockReceipt(productID, branchID, locationID, occurredAt string, qty float64, unitCostCents int64) {
	id := f.newID()
	f.del(`DELETE FROM stock_moves WHERE id=$1::uuid`, id)
	f.del(`DELETE FROM stock_balances WHERE product_id=$1::uuid AND location_id=$2::uuid`, productID, locationID)
	f.exec(`INSERT INTO stock_moves (id, product_id, to_location_id, qty_base, qty_entered, uom_entered,
	          move_type, unit_cost_cents, value_cents, source_type, branch_id, occurred_at)
	        VALUES ($1::uuid, $2::uuid, $3::uuid, $4::numeric, $4::numeric, 'EA', 'RECEIPT', $5::bigint,
	                round($4::numeric * $5::bigint)::bigint,
	                'fixture', $6::uuid, $7::timestamptz)`,
		id, productID, locationID, qty, unitCostCents, branchID, occurredAt)
	f.exec(`INSERT INTO stock_balances (product_id, location_id, branch_id, on_hand_base, allocated_base)
	        VALUES ($1::uuid, $2::uuid, $3::uuid, $4, 0)
	        ON CONFLICT (product_id, location_id) DO UPDATE SET on_hand_base = stock_balances.on_hand_base + EXCLUDED.on_hand_base`,
		productID, locationID, branchID, qty)
}

// branchLocation returns a yard location inside a branch (any location whose branch_id matches).
func (f *fixture) branchLocation(branchID string) string {
	var id string
	if err := f.pool.QueryRow(f.ctx,
		`SELECT id::text FROM locations WHERE branch_id=$1::uuid ORDER BY loc_type, code LIMIT 1`, branchID).
		Scan(&id); err != nil {
		f.t.Fatalf("no location in branch %s: %v", branchID, err)
	}
	return id
}

// -- result helpers -----------------------------------------------------------

// cell reads a value by column NAME from a result row, positional indexing in a test is how an
// assertion silently starts checking the wrong column after a catalog edit.
func cell(t *testing.T, res Result, rowIdx int, name string) any {
	t.Helper()
	for i, c := range res.Columns {
		if c.Name == name {
			if rowIdx >= len(res.Rows) {
				t.Fatalf("row %d requested but the report returned %d rows", rowIdx, len(res.Rows))
			}
			return res.Rows[rowIdx][i]
		}
	}
	t.Fatalf("column %q is not in %s's allowlist (%v)", name, res.Code, columnNames(res))
	return nil
}

// findRow returns the index of the first row whose named column equals want, or -1.
func findRow(res Result, name string, want any) int {
	idx := -1
	for i, c := range res.Columns {
		if c.Name == name {
			idx = i
		}
	}
	if idx < 0 {
		return -1
	}
	for r, row := range res.Rows {
		if fmt.Sprint(row[idx]) == fmt.Sprint(want) {
			return r
		}
	}
	return -1
}

func columnNames(res Result) []string {
	out := make([]string, 0, len(res.Columns))
	for _, c := range res.Columns {
		out = append(out, c.Name)
	}
	return out
}

// wantCents asserts a money cell.
func wantCents(t *testing.T, res Result, row int, name string, want int64) {
	t.Helper()
	got, ok := cell(t, res, row, name).(int64)
	if !ok {
		t.Fatalf("%s.%s is %T, want int64, money must never become a float", res.Code, name, cell(t, res, row, name))
	}
	if got != want {
		t.Errorf("%s row %d %s = %d cents, want %d", res.Code, row, name, got, want)
	}
}

// wantInt asserts a count cell.
func wantInt(t *testing.T, res Result, row int, name string, want int64) {
	t.Helper()
	got, _ := cell(t, res, row, name).(int64)
	if got != want {
		t.Errorf("%s row %d %s = %d, want %d", res.Code, row, name, got, want)
	}
}

// wantText asserts a text cell.
func wantText(t *testing.T, res Result, row int, name, want string) {
	t.Helper()
	got, _ := cell(t, res, row, name).(string)
	if got != want {
		t.Errorf("%s row %d %s = %q, want %q", res.Code, row, name, got, want)
	}
}

// wantQty asserts a quantity cell within a tolerance that is finer than any quantity the substrate can
// store (NUMERIC(18,4)).
func wantQty(t *testing.T, res Result, row int, name string, want float64) {
	t.Helper()
	got, _ := cell(t, res, row, name).(float64)
	if diff := got - want; diff > 0.00005 || diff < -0.00005 {
		t.Errorf("%s row %d %s = %v, want %v", res.Code, row, name, got, want)
	}
}

// wantTotal asserts a totals-row figure.
func wantTotalCents(t *testing.T, res Result, name string, want int64) {
	t.Helper()
	got, ok := res.Totals[name].(int64)
	if !ok {
		t.Fatalf("%s: totals[%s] is %#v, want an int64", res.Code, name, res.Totals[name])
	}
	if got != want {
		t.Errorf("%s: totals[%s] = %d cents, want %d", res.Code, name, got, want)
	}
}

// run executes a template and fails on error.
func run(t *testing.T, svc *Service, code string, params map[string]string) Result {
	t.Helper()
	res, err := svc.Run(context.Background(), code, params)
	if err != nil {
		t.Fatalf("run %s(%v): %v", code, params, err)
	}
	return res
}

// -- the rest of the fixture vocabulary ---------------------------------------

// statementRun writes an ar_statement_runs row with an explicit created_at inside the test's window.
func (f *fixture) statementRun(mode, branchID, date, status string, generated, queued int) string {
	id := f.newID()
	f.del(`DELETE FROM ar_statement_runs WHERE id=$1::uuid`, id)
	f.exec(`INSERT INTO ar_statement_runs (id, mode, branch_id, as_of_date, status, generated_count,
	          queued_count, requested_by, created_at)
	        VALUES ($1::uuid, $2, NULLIF($3,'')::uuid, $4::date, $5, $6, $7, $8::uuid, $4::date)`,
		id, mode, branchID, date, status, generated, queued, fixtureActor(f.t, f.pool))
	return id
}

// stockMove writes a bare move (no balance side) for movement-report fixtures.
//
// QUANTITY IS ALWAYS POSITIVE. stock_moves CHECKs qty_base > 0 and expresses DIRECTION through which
// location column is set, from_location_id for stock leaving, to_location_id for stock arriving. That
// is the substrate's model and the fixture honours it rather than working around it, which is also why
// inventory_movement reports "quantity MOVED" rather than a net change.
//
// The balance is deliberately NOT touched here: this move is a HISTORY row for a report, and
// inventory_movement reads stock_moves alone.
func (f *fixture) stockMove(productID, branchID, locationID, occurredAt, moveType string, qty float64, unitCostCents int64, outbound bool) {
	id := f.newID()
	f.del(`DELETE FROM stock_moves WHERE id=$1::uuid`, id)
	col := "to_location_id"
	if outbound {
		col = "from_location_id"
	}
	f.exec(`INSERT INTO stock_moves (id, product_id, `+col+`, qty_base, qty_entered, uom_entered,
	          move_type, unit_cost_cents, value_cents, source_type, branch_id, occurred_at)
	        VALUES ($1::uuid, $2::uuid, $3::uuid, $4::numeric, $4::numeric, 'EA', $5, $6::bigint,
	                round($4::numeric * $6::bigint)::bigint,
	                'fixture', $7::uuid, $8::timestamptz)`,
		id, productID, locationID, qty, moveType, unitCostCents, branchID, occurredAt)
}

// reorderPoint sets a branch reorder policy for a product.
func (f *fixture) reorderPoint(productID, branchID string, point, qty float64, vendorID string) {
	f.del(`DELETE FROM product_reorder_points WHERE product_id=$1::uuid AND branch_id=$2::uuid`, productID, branchID)
	f.exec(`INSERT INTO product_reorder_points (product_id, branch_id, reorder_point_base, reorder_qty_base,
	          preferred_vendor_id, is_active)
	        VALUES ($1::uuid, $2::uuid, $3, $4, NULLIF($5,'')::uuid, TRUE)`,
		productID, branchID, point, qty, vendorID)
}

func (f *fixture) purchaseOrder(vendorID, branchID, status string) string {
	id := f.newID()
	f.del(`DELETE FROM purchasing_pos WHERE id=$1::uuid`, id)
	f.exec(`INSERT INTO purchasing_pos (id, po_no, vendor_id, branch_id, status)
	        VALUES ($1::uuid, $2, $3::uuid, $4::uuid, $5)`,
		id, f.tag+"-PO-"+id[:6], vendorID, branchID, status)
	return id
}

func (f *fixture) poLine(poID string, lineNo int, productID string, ordered float64, unitCostCents int64, received float64) string {
	id := f.newID()
	f.del(`DELETE FROM purchasing_po_lines WHERE id=$1::uuid`, id)
	f.exec(`INSERT INTO purchasing_po_lines (id, po_id, line_no, product_id, description,
	          qty_ordered_base, uom, unit_cost_cents, qty_received_base)
	        VALUES ($1::uuid, $2::uuid, $3, $4::uuid, $5, $6, 'EA', $7, $8)`,
		id, poID, lineNo, productID, f.tag+" po line", ordered, unitCostCents, received)
	return id
}

func (f *fixture) receipt(poID string, seq int, receivedAt string) string {
	id := f.newID()
	f.del(`DELETE FROM purchasing_receipt_lines WHERE receipt_id=$1::uuid`, id)
	f.del(`DELETE FROM purchasing_receipts WHERE id=$1::uuid`, id)
	f.exec(`INSERT INTO purchasing_receipts (id, po_id, seq, grn_no, received_by, received_at)
	        VALUES ($1::uuid, $2::uuid, $3, $4, $5::uuid, $6::timestamptz)`,
		id, poID, seq, f.tag+"-GRN-"+id[:6], fixtureActor(f.t, f.pool), receivedAt)
	return id
}

func (f *fixture) receiptLine(receiptID, poLineID string, qty float64, unitCostCents, valueCents int64) {
	f.exec(`INSERT INTO purchasing_receipt_lines (receipt_id, po_line_id, qty_base, unit_cost_cents, value_cents)
	        VALUES ($1::uuid, $2::uuid, $3, $4, $5)`, receiptID, poLineID, qty, unitCostCents, valueCents)
}

func (f *fixture) bill(vendorID, branchID, billDate, matchState string) string {
	id := f.newID()
	f.del(`DELETE FROM purchasing_bill_lines WHERE bill_id=$1::uuid`, id)
	f.del(`DELETE FROM purchasing_bills WHERE id=$1::uuid`, id)
	f.exec(`INSERT INTO purchasing_bills (id, bill_no, vendor_id, branch_id, status, match_state, bill_date)
	        VALUES ($1::uuid, $2, $3::uuid, $4::uuid, 'DRAFT', $5, $6::date)`,
		id, f.tag+"-BILL-"+id[:6], vendorID, branchID, matchState, billDate)
	return id
}

func (f *fixture) billLine(billID string, lineNo int, description, matchState string,
	qtyVariance float64, priceVarianceCents, lineTotalCents int64) {
	f.exec(`INSERT INTO purchasing_bill_lines (bill_id, line_no, description, qty_billed_base,
	          unit_cost_cents, line_total_cents, match_state, qty_variance_base, price_variance_cents)
	        VALUES ($1::uuid, $2, $3, 1, $4, $4, $5, $6, $7)`,
		billID, lineNo, description, lineTotalCents, matchState, qtyVariance, priceVarianceCents)
}

func (f *fixture) payment(customerID, branchID, method, status, receivedAt string, amountCents, surchargeCents int64) string {
	id := f.newID()
	f.del(`DELETE FROM payments WHERE id=$1::uuid`, id)
	f.exec(`INSERT INTO payments (id, payment_no, customer_id, method, amount_cents, status, received_at,
	          branch_id, surcharge_cents)
	        VALUES ($1::uuid, $2, $3::uuid, $4, $5, $6, $7::timestamptz, $8::uuid, $9)`,
		id, f.tag+"-PAY-"+id[:6], customerID, method, amountCents, status, receivedAt, branchID, surchargeCents)
	return id
}

func (f *fixture) creditHold(customerID, kind, status, reason, createdAt string, auto bool, actor string) string {
	id := f.newID()
	f.del(`DELETE FROM credit_holds WHERE id=$1::uuid`, id)
	released := "NULL"
	_ = released
	f.exec(`INSERT INTO credit_holds (id, customer_id, status, reason, hold_kind, auto_placed, placed_by,
	          created_at, released_at, released_by)
	        VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7::uuid, $8::timestamptz,
	                CASE WHEN $3 = 'RELEASED' THEN $8::timestamptz ELSE NULL END,
	                CASE WHEN $3 = 'RELEASED' THEN $7::uuid ELSE NULL END)`,
		id, customerID, status, reason, kind, auto, actor, createdAt)
	return id
}

func (f *fixture) deliveryStop(branchID, orderID, scheduledDate, status, deliveredAt, failureReason string) string {
	id := f.newID()
	f.del(`DELETE FROM delivery_stops WHERE id=$1::uuid`, id)
	// The substrate requires a signature + pod_at on a DELIVERED stop and a reason on a FAILED one
	// (0-era CHECKs). The fixture honours both rather than working around them.
	f.exec(`INSERT INTO delivery_stops (id, branch_id, order_id, stop_no, scheduled_date, status,
	          address_line, city, postal_code, failure_reason, pod_signature_ref, pod_at, delivered_at)
	        VALUES ($1::uuid, $2::uuid, $3::uuid, 1, $4::date, $5, '1 Test Rd', 'Trenton', 'K8V 1A1',
	                $6,
	                CASE WHEN $5 = 'DELIVERED' THEN 'fixture-sig' ELSE '' END,
	                CASE WHEN $5 = 'DELIVERED' THEN NULLIF($7,'')::timestamptz ELSE NULL END,
	                NULLIF($7,'')::timestamptz)`,
		id, branchID, orderID, scheduledDate, status, failureReason, deliveredAt)
	return id
}

func (f *fixture) warrantyClaim(customerID, branchID, vendorID, phase, outcome, openedAt string,
	expectedCents, realizedCents int64) string {
	id := f.newID()
	f.del(`DELETE FROM warranty_claims WHERE id=$1::uuid`, id)
	f.exec(`INSERT INTO warranty_claims (id, claim_no, customer_id, branch_id, vendor_id, phase, source,
	          outcome, expected_cents, realized_cents, opened_at, closed_at)
	        VALUES ($1::uuid, $2, $3::uuid, $4::uuid, NULLIF($5,'')::uuid, $6, 'INTAKE',
	                NULLIF($7,''), $8, $9, $10::timestamptz,
	                CASE WHEN $6 IN ('CLOSED','REJECTED','ABANDONED') THEN $10::timestamptz ELSE NULL END)`,
		id, f.tag+"-WTY-"+id[:6], customerID, branchID, vendorID, phase, outcome,
		expectedCents, realizedCents, openedAt)
	return id
}

// -- small test utilities -----------------------------------------------------

// addDays shifts a YYYY-MM-DD string.
func addDays(date string, n int) string {
	d, err := time.Parse(DateLayout, date)
	if err != nil {
		panic("addDays: " + err.Error())
	}
	return d.AddDate(0, 0, n).Format(DateLayout)
}

// twoUsers returns two distinct seeded user ids to stand in for two reps.
func twoUsers(t *testing.T, pool *pgxpool.Pool) (string, string) {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT id::text FROM users WHERE status='ACTIVE' ORDER BY created_at LIMIT 2`)
	if err != nil {
		t.Fatalf("reading seeded users: %v", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if len(ids) < 2 {
		t.Skip("need two seeded ACTIVE users to model two reps")
	}
	return ids[0], ids[1]
}

// isColumnDrift reports whether an error is the allowlist-drift failure.
func isColumnDrift(err error) bool { return errors.Is(err, ErrColumnDrift) }

// commissionRule writes a rule and returns its id.
func (f *fixture) commissionRule(repUserID, basis string, rateBps int, categoryID, branchID, effectiveFrom string) string {
	id := f.newID()
	f.del(`DELETE FROM commission_run_lines WHERE rule_id=$1::uuid`, id)
	f.del(`DELETE FROM commission_rules WHERE id=$1::uuid`, id)
	f.exec(`INSERT INTO commission_rules (id, rep_user_id, basis, rate_bps, category_id, branch_id, effective_from)
	        VALUES ($1::uuid, $2::uuid, $3, $4, NULLIF($5,'')::uuid, NULLIF($6,'')::uuid, $7::date)`,
		id, repUserID, basis, rateBps, categoryID, branchID, effectiveFrom)
	return id
}

// mustJSON serialises a value the way the handler would, for tests that assert on what reaches a client.
func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
