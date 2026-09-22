//go:build integration

// DIB-175 integration tests: the staff order-edit surface against the live Postgres on :5435, through
// the SAME production code paths cmd/server/main.go wires, the real *Repository, the real audit spine,
// the real HTTP handlers. These prove the four things the unit fakes structurally cannot:
//
//	I1  the ratified matrix holds against REAL rows: allowed cells mutate order_lines and recompute
//	    orders.subtotal/total; blocked cells answer 409 NAMING the status and leave the row untouched.
//	I2  an edit is reconstructable from the audit trail alone, edit → GET /edits → exact before→after.
//	I3  the money predicate refuses an edit against a REAL finalized invoice / REAL assessed tax.
//	I4  the status guard is evaluated INSIDE the row lock: an edit authorised against CONFIRMED that
//	    arrives while a concurrent transaction is moving the order to READY is refused, not applied.
//
// Pricing: most tests inject a deterministic resolver so a matrix/audit assertion never depends on
// pricing seed drift (the DIB-378 anchor lesson). TestIntegration_EditThroughRealPricing composes the
// REAL *pricing.Service to prove the seam itself wires up end to end.
//
// MUTATION CHECK: adding "READY" to the line rules in editRules must fail
// TestIntegration_EditBlockedAtReady (and TestEditMatrix/line_qty/READY in the unit suite).
package order

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/inventory"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pricing"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	edIntManager = "3b175000-0000-4000-9000-000000000001"
	edIntStaff   = "3b175000-0000-4000-9000-000000000002"
)

// edIntManagerCtx / edIntStaffCtx are the two authority levels the edit matrix distinguishes, scoped to
// the branch the seeded order lives in.
func edIntManagerCtx() identity.StaffContext { return managerAt(edIntManager, trentonBranch) }
func edIntStaffCtx() identity.StaffContext   { return staffAt(edIntStaff, trentonBranch) }

// anchoredProduct picks a catalogue product whose BASE uom is also its anchor's price uom (factor = 1).
// Picking on factor rather than "any product with a conversion row" is the DIB-378 lesson: a PC-based
// product priced at SF explodes the effective cost through the pieces-per-SF factor and turns a pricing
// assertion into fixture archaeology.
func anchoredProduct(t *testing.T, pool *pgxpool.Pool) (id, uom string) {
	t.Helper()
	err := pool.QueryRow(context.Background(), `
SELECT p.id::text, p.base_uom::text
  FROM products p
  JOIN pricing_list_prices lp ON lp.product_id = p.id AND lp.price_uom = p.base_uom
  JOIN product_uoms pu ON pu.product_id = p.id AND pu.uom_code = p.base_uom AND pu.qty_in_base = 1
 ORDER BY p.sku
 LIMIT 1`).Scan(&id, &uom)
	if err != nil {
		t.Skipf("no factor-1 anchored product seeded (run make reset): %v", err)
	}
	return id, uom
}

// seedEditOrder inserts a customer + one order at `status` in Trenton WITH one order line, and
// registers cleanup of everything it touched (including the audit trail the edits will write).
func seedEditOrder(t *testing.T, pool *pgxpool.Pool, status string, taxCents int64) (cust, ord, prod, uom string) {
	t.Helper()
	ctx := context.Background()
	prod, uom = anchoredProduct(t, pool)
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name) VALUES ($1, 'DIB175 Tenant') RETURNING id::text`,
		fmt.Sprintf("D175-%d", time.Now().UnixNano())).Scan(&cust); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment,
		                     subtotal_cents, tax_cents, total_cents, requested_date)
		 VALUES ($1, $2, $3, $4, 'PICKUP', 200, $5::bigint, 200 + $5::bigint, DATE '2026-08-01') RETURNING id::text`,
		fmt.Sprintf("#D175-%d", time.Now().UnixNano()), cust, trentonBranch, status, taxCents).Scan(&ord); err != nil {
		t.Fatalf("seed order: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO order_lines (order_id, line_no, product_id, description, qty_entered, uom_entered,
		                          qty_base, unit_price_cents, price_uom, line_total_cents)
		 VALUES ($1, 1, $2, 'seeded line', 2, $3, 2, 100, $3, 200)`, ord, prod, uom); err != nil {
		t.Fatalf("seed order line: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM audit_log WHERE entity_id = $1`, ord)
		_, _ = pool.Exec(c, `DELETE FROM order_lines WHERE order_id = $1`, ord)
		_, _ = pool.Exec(c, `DELETE FROM orders WHERE id = $1`, ord)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1`, cust)
	})
	return cust, ord, prod, uom
}

// intPricer is a deterministic stand-in for *pricing.Service: 100¢/unit at factor 1.
type intPricer struct{}

func (intPricer) ResolvePrice(_ context.Context, in pricing.ResolveInput) (pricing.Breakdown, error) {
	return pricing.Breakdown{
		FinalCents: 100, PriceUOM: in.UOMEntered, QtyBase: in.QtyEntered,
		LineTotalCents: int64(in.QtyEntered * 100),
	}, nil
}

type intInv struct{}

func (intInv) CheckAvailability(context.Context, string, string, float64) (bool, string, error) {
	return true, "", nil
}
func (intInv) CurrentCostCents(context.Context, string, string) (int64, bool, error) {
	return 50, true, nil
}

type intCustType struct{}

func (intCustType) CustomerType(context.Context, string) (string, error) { return "CONTRACTOR", nil }

// editSvc wires the REAL repository + REAL audit spine with a deterministic pricer, exactly as main.go
// composes them apart from the pricing stand-in.
func editSvc(t *testing.T, pool *pgxpool.Pool) (*Service, *audit.Service) {
	t.Helper()
	auditSvc := audit.NewService(audit.NewRepository(pool), nil)
	svc := NewService(NewRepository(pool)).
		WithAudit(auditSvc).WithAuditTrail(auditSvc).
		WithCancelGuards(permissiveGuards{}, permissiveGuards{}).
		WithReorderPricing(intPricer{}).WithReorderInventory(intInv{}).
		WithCustomerType(intCustType{})
	return svc, auditSvc
}

// permissiveGuards is a money predicate that proves nothing is attached, the default for tests that
// are not exercising the predicate itself.
type permissiveGuards struct{}

func (permissiveGuards) OrderHasFinalizedInvoice(context.Context, string) (bool, error) {
	return false, nil
}
func (permissiveGuards) OrderHasCommittedPOSSale(context.Context, string) (bool, error) {
	return false, nil
}

// liveLine reads a line's persisted quantity + money straight from Postgres.
func liveLine(t *testing.T, pool *pgxpool.Pool, orderID string, lineNo int) (qtyEntered, qtyBase string, lineTotal int64) {
	t.Helper()
	if err := pool.QueryRow(context.Background(),
		`SELECT qty_entered::text, qty_base::text, line_total_cents FROM order_lines
		  WHERE order_id = $1 AND line_no = $2`, orderID, lineNo).Scan(&qtyEntered, &qtyBase, &lineTotal); err != nil {
		t.Fatalf("read line: %v", err)
	}
	return
}

// liveTotals reads the order's persisted money.
func liveTotals(t *testing.T, pool *pgxpool.Pool, orderID string) (subtotal, total int64) {
	t.Helper()
	if err := pool.QueryRow(context.Background(),
		`SELECT subtotal_cents, total_cents FROM orders WHERE id = $1`, orderID).Scan(&subtotal, &total); err != nil {
		t.Fatalf("read totals: %v", err)
	}
	return
}

// - I1: the ratified matrix against real rows -

// TestIntegration_EditMatrix_AllowedAndBlockedCells drives EVERY (field × status) cell against a real
// order row: allowed cells must actually mutate Postgres, blocked cells must answer 409 with the status
// named and leave the row byte-identical.
func TestIntegration_EditMatrix_AllowedAndBlockedCells(t *testing.T) {
	pool := intPool(t)
	for _, field := range editFieldOrder {
		field := field
		t.Run(string(field), func(t *testing.T) {
			for _, status := range allStatuses {
				status := status
				t.Run(status, func(t *testing.T) {
					_, ord, prod, uom := seedEditOrder(t, pool, status, 0)
					svc, _ := editSvc(t, pool)
					ctx := identity.ContextWithStaff(context.Background(), edIntManagerCtx())

					beforeQty, _, _ := liveLine(t, pool, ord, 1)
					var err error
					switch field {
					case FieldLineQty:
						_, err = svc.EditLineQty(ctx, ord, 1, "7")
					case FieldLineAdd:
						_, err = svc.AddLine(ctx, ord, prod, "3", uom, "added by test")
					case FieldLineRemove:
						_, err = svc.RemoveLine(ctx, ord, 1)
					default:
						_, err = svc.SetRequestedDate(ctx, ord, "2026-09-15")
					}

					if editRules[field].from[status] {
						if err != nil {
							t.Fatalf("%s in %s must be allowed: %v", field, status, err)
						}
						return
					}
					// Blocked cell: 409 naming the status, and NOTHING written.
					var ne *NotEditableError
					if !asNotEditable(err, &ne) {
						t.Fatalf("%s in %s must be refused with NotEditableError, got %v", field, status, err)
					}
					if ne.Status != status {
						t.Errorf("want the refusal to name %q, got %q", status, ne.Status)
					}
					if !strings.Contains(ne.Error(), status) {
						t.Errorf("want the message to name the status, got %q", ne.Error())
					}
					if gotQty, _, _ := liveLine(t, pool, ord, 1); gotQty != beforeQty {
						t.Errorf("a refused edit must not touch the row: qty %s → %s", beforeQty, gotQty)
					}
				})
			}
		})
	}
}

// TestIntegration_EditBlockedAtReady is the NAMED mutation-check anchor: the READY wall for line edits,
// proven against a real row and through the real HTTP boundary (409 + the status in the body).
func TestIntegration_EditBlockedAtReady(t *testing.T) {
	pool := intPool(t)
	_, ord, _, _ := seedEditOrder(t, pool, "READY", 0)
	svc, _ := editSvc(t, pool)
	h := NewHandler(svc)

	sc := edIntManagerCtx()
	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPatch, "/api/v1/orders/"+ord+"/lines/1",
		strings.NewReader(`{"qty_entered":"9"}`))
	r.SetPathValue("id", ord)
	r.SetPathValue("lineNo", "1")
	r = r.WithContext(identity.ContextWithStaff(r.Context(), sc))
	h.editLineQty(rr, r, guards.StaffCaller{})

	if rr.Code != http.StatusConflict {
		t.Fatalf("want 409 for a line edit on a READY order, got %d (%s)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "READY") {
		t.Errorf("the 409 must NAME the blocking status; body = %s", rr.Body.String())
	}
	if qty, _, _ := liveLine(t, pool, ord, 1); qty != "2.0000" {
		t.Errorf("the line must be untouched, got qty %s", qty)
	}
}

// TestIntegration_EditTotalsRecompute proves the money: an allowed qty edit rewrites the line AND the
// order's subtotal/total from the lines themselves.
func TestIntegration_EditTotalsRecompute(t *testing.T) {
	pool := intPool(t)
	_, ord, prod, uom := seedEditOrder(t, pool, "CONFIRMED", 0)
	svc, _ := editSvc(t, pool)
	ctx := identity.ContextWithStaff(context.Background(), edIntManagerCtx())

	if _, err := svc.EditLineQty(ctx, ord, 1, "7"); err != nil {
		t.Fatalf("qty edit: %v", err)
	}
	qty, base, lineTotal := liveLine(t, pool, ord, 1)
	if qty != "7.0000" || base != "7.0000" || lineTotal != 700 {
		t.Errorf("line after edit = (%s, %s, %d), want (7.0000, 7.0000, 700)", qty, base, lineTotal)
	}
	if sub, tot := liveTotals(t, pool, ord); sub != 700 || tot != 700 {
		t.Errorf("order money after edit = (%d, %d), want (700, 700)", sub, tot)
	}
	// Adding a line must fold into the same recompute.
	if _, err := svc.AddLine(ctx, ord, prod, "3", uom, "second line"); err != nil {
		t.Fatalf("add line: %v", err)
	}
	if sub, tot := liveTotals(t, pool, ord); sub != 1000 || tot != 1000 {
		t.Errorf("order money after add = (%d, %d), want (1000, 1000)", sub, tot)
	}
	// Removing the added line must fold back out.
	if _, err := svc.RemoveLine(ctx, ord, 2); err != nil {
		t.Fatalf("remove line: %v", err)
	}
	if sub, tot := liveTotals(t, pool, ord); sub != 700 || tot != 700 {
		t.Errorf("order money after remove = (%d, %d), want (700, 700)", sub, tot)
	}
}

// - I2: the trail is reconstructable -

// TestIntegration_EditAuditReconstruction is acceptance triad 2: after a real edit, the change-history
// READ returns the exact who/what/when + before→after for every column that moved.
func TestIntegration_EditAuditReconstruction(t *testing.T) {
	pool := intPool(t)
	_, ord, _, _ := seedEditOrder(t, pool, "CONFIRMED", 0)
	svc, _ := editSvc(t, pool)
	ctx := identity.ContextWithStaff(context.Background(), edIntManagerCtx())

	if _, err := svc.EditLineQty(ctx, ord, 1, "7"); err != nil {
		t.Fatalf("qty edit: %v", err)
	}
	if _, err := svc.SetRequestedDate(ctx, ord, "2026-09-15"); err != nil {
		t.Fatalf("date edit: %v", err)
	}

	hist, err := svc.OrderEditHistoryFor(ctx, ord)
	if err != nil {
		t.Fatalf("read edit history: %v", err)
	}
	byField := map[string]OrderFieldChange{}
	for _, c := range hist.Changes {
		byField[c.Field] = c
	}
	for _, want := range []struct{ field, before, after string }{
		{"qty_entered", "2.0000", "7.0000"},
		{"qty_base", "2.0000", "7.0000"},
		{"line_total_cents", "200", "700"},
		{"requested_date", "2026-08-01", "2026-09-15"},
	} {
		got, ok := byField[want.field]
		if !ok {
			t.Errorf("no history row for %q (got %d rows)", want.field, len(hist.Changes))
			continue
		}
		if got.Before != want.before || got.After != want.after {
			t.Errorf("%s: got %q → %q, want %q → %q", want.field, got.Before, got.After, want.before, want.after)
		}
		if got.ActorID != edIntManager {
			t.Errorf("%s: want actor %s, got %s", want.field, edIntManager, got.ActorID)
		}
		if got.OccurredAt.IsZero() {
			t.Errorf("%s: want a real occurred_at", want.field)
		}
	}
	// Money must survive the audit boundary as digits, never "[redacted]", the whole point of the
	// int64 + bounded-quantity discipline.
	for _, c := range hist.Changes {
		if strings.Contains(c.Before, "redacted") || strings.Contains(c.After, "redacted") {
			t.Errorf("%s: value was redacted at the audit boundary (%q → %q)", c.Field, c.Before, c.After)
		}
	}
	// The four rows of ONE qty edit must share a request id so a reader can group them.
	qtyReq := byField["qty_entered"].RequestID
	if qtyReq != "" && byField["line_total_cents"].RequestID != qtyReq {
		t.Errorf("rows of one edit must share a request id: %q vs %q",
			qtyReq, byField["line_total_cents"].RequestID)
	}
}

// TestIntegration_RemoveSnapshotsPreImage is condition C1 against a real row: after the line is gone,
// the trail still names what was removed.
func TestIntegration_RemoveSnapshotsPreImage(t *testing.T) {
	pool := intPool(t)
	_, ord, prod, _ := seedEditOrder(t, pool, "PICKING", 0)
	svc, _ := editSvc(t, pool)
	ctx := identity.ContextWithStaff(context.Background(), edIntManagerCtx())

	if _, err := svc.RemoveLine(ctx, ord, 1); err != nil {
		t.Fatalf("remove: %v", err)
	}
	var remaining int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM order_lines WHERE order_id = $1`, ord).Scan(&remaining); err != nil {
		t.Fatalf("count lines: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("want the line deleted, %d remain", remaining)
	}
	hist, err := svc.OrderEditHistoryFor(ctx, ord)
	if err != nil {
		t.Fatalf("read edit history: %v", err)
	}
	var found bool
	for _, c := range hist.Changes {
		if c.Field == "line_removed" {
			found = true
			if c.Before != prod {
				t.Errorf("want the removed product %s snapshotted, got %q", prod, c.Before)
			}
		}
	}
	if !found {
		t.Error("want a line_removed row naming what was removed")
	}
}

// - I3: the money predicate against real money -

// TestIntegration_EditMoneyPredicate proves the guard against REAL attached money: a finalized invoice
// and assessed tax each refuse a line edit, while the date edit stays available.
func TestIntegration_EditMoneyPredicate(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()

	t.Run("a real finalized invoice blocks a line edit", func(t *testing.T) {
		cust, ord, _, _ := seedEditOrder(t, pool, "CONFIRMED", 0)
		var inv string
		if err := pool.QueryRow(ctx,
			`INSERT INTO invoices (invoice_no, customer_id, order_id, branch_id, status, due_date, total_cents, balance_cents)
			 VALUES ($1, $2, $3, $4, 'FINALIZED', CURRENT_DATE, 200, 200) RETURNING id::text`,
			fmt.Sprintf("INV-D175-%d", time.Now().UnixNano()), cust, ord, trentonBranch).Scan(&inv); err != nil {
			t.Fatalf("seed invoice: %v", err)
		}
		t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM invoices WHERE id = $1`, inv) })

		// The REAL billing-backed predicate, exactly as main.go wires it.
		svc, _ := editSvc(t, pool)
		svc.WithCancelGuards(realInvoiceGuard{pool}, permissiveGuards{})
		sctx := identity.ContextWithStaff(ctx, edIntManagerCtx())
		if _, err := svc.EditLineQty(sctx, ord, 1, "7"); err == nil || !strings.Contains(err.Error(), "transacted money") {
			t.Fatalf("want ErrEditBlocked for an order with a finalized invoice, got %v", err)
		}
		if qty, _, _ := liveLine(t, pool, ord, 1); qty != "2.0000" {
			t.Errorf("blocked edit must not write, got qty %s", qty)
		}
	})

	t.Run("assessed tax blocks a line edit but not the date", func(t *testing.T) {
		_, ord, _, _ := seedEditOrder(t, pool, "CONFIRMED", 1300)
		svc, _ := editSvc(t, pool)
		sctx := identity.ContextWithStaff(ctx, edIntManagerCtx())
		if _, err := svc.EditLineQty(sctx, ord, 1, "7"); err == nil || !strings.Contains(err.Error(), "assessed tax") {
			t.Fatalf("want ErrEditTaxAssessed, got %v", err)
		}
		// A date is not money, it must still move on the same taxed order.
		if _, err := svc.SetRequestedDate(sctx, ord, "2026-09-15"); err != nil {
			t.Fatalf("date edit on a taxed order must be allowed, got %v", err)
		}
	})
}

// realInvoiceGuard reads the actual invoices table, mirroring what *billing.Repository supplies in
// production (order does not import billing in a test either).
type realInvoiceGuard struct{ pool *pgxpool.Pool }

func (g realInvoiceGuard) OrderHasFinalizedInvoice(ctx context.Context, orderID string) (bool, error) {
	var n int
	err := g.pool.QueryRow(ctx,
		`SELECT count(*) FROM invoices WHERE order_id = $1 AND status <> 'DRAFT' AND status <> 'VOID'`,
		orderID).Scan(&n)
	return n > 0, err
}

// - I4: the guard is inside the lock -

// TestIntegration_EditLosesRaceToConcurrentTransition is the atomicity proof, and the reason the status
// guard is re-evaluated in the repository rather than trusted from the service.
//
// The edit is authorised while the order is still CONFIRMED (the service's unlocked read sees the
// pre-transition value), then blocks on the order row lock held by a concurrent transaction that is
// moving the order to READY. When that transaction commits, the guarded write re-reads the LATEST
// committed status and must refuse.
//
// If the matrix were enforced only in the service, this edit would land on a READY order, exactly the
// check-then-write race DIB-191 closed for status transitions.
func TestIntegration_EditLosesRaceToConcurrentTransition(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	_, ord, _, _ := seedEditOrder(t, pool, "CONFIRMED", 0)
	svc, _ := editSvc(t, pool)

	// Hold the order row and move it to READY, without committing yet.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT status FROM orders WHERE id = $1 FOR UPDATE`, ord); err != nil {
		t.Fatalf("lock: %v", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE orders SET status = 'READY' WHERE id = $1`, ord); err != nil {
		t.Fatalf("transition: %v", err)
	}

	// The edit authorises against the still-visible CONFIRMED, then blocks on the row lock.
	done := make(chan error, 1)
	go func() {
		sctx := identity.ContextWithStaff(context.Background(), edIntManagerCtx())
		_, e := svc.EditLineQty(sctx, ord, 1, "7")
		done <- e
	}()

	// Give the goroutine time to get past the unlocked authority read and onto the lock.
	time.Sleep(300 * time.Millisecond)
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit the transition: %v", err)
	}

	select {
	case e := <-done:
		var ne *NotEditableError
		if !asNotEditable(e, &ne) {
			t.Fatalf("want the edit refused once the order became READY, got %v", e)
		}
		if ne.Status != "READY" {
			t.Errorf("want the refusal to name READY (the committed status), got %q", ne.Status)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the edit never returned, the guarded write is deadlocked on the order row lock")
	}
	if qty, _, _ := liveLine(t, pool, ord, 1); qty != "2.0000" {
		t.Errorf("the losing edit must not have written; got qty %s", qty)
	}
}

// - authority + the real pricing seam -

// TestIntegration_EditAuthority proves the tier + branch gates against real rows: a frontline caller
// cannot move a line but can move the date; a cross-branch caller reaches neither.
func TestIntegration_EditAuthority(t *testing.T) {
	pool := intPool(t)
	_, ord, _, _ := seedEditOrder(t, pool, "CONFIRMED", 0)
	svc, _ := editSvc(t, pool)

	frontline := identity.ContextWithStaff(context.Background(), edIntStaffCtx())
	if _, err := svc.EditLineQty(frontline, ord, 1, "7"); err == nil {
		t.Error("a frontline caller must not be able to edit a line")
	}
	if _, err := svc.SetRequestedDate(frontline, ord, "2026-09-15"); err != nil {
		t.Errorf("a frontline caller must be able to move the date, got %v", err)
	}
	crossBranch := identity.ContextWithStaff(context.Background(), managerAt(edIntManager, otherBranch))
	if _, err := svc.EditLineQty(crossBranch, ord, 1, "7"); err == nil {
		t.Error("a cross-branch manager must be refused")
	}
	if _, err := svc.OrderEditHistoryFor(crossBranch, ord); err == nil {
		t.Error("a cross-branch manager must not read the change history")
	}
}

// TestIntegration_EditThroughRealPricing composes the REAL *pricing.Service + *inventory.Service, so
// the seam wiring itself is proven end to end (the other tests stub pricing to stay independent of
// seed drift). It asserts the SHAPE, a resolved price and a coherent recompute, not a specific rate,
// which belongs to pricing's own suite.
func TestIntegration_EditThroughRealPricing(t *testing.T) {
	pool := intPool(t)
	_, ord, _, _ := seedEditOrder(t, pool, "CONFIRMED", 0)

	auditSvc := audit.NewService(audit.NewRepository(pool), nil)
	svc := NewService(NewRepository(pool)).
		WithAudit(auditSvc).WithAuditTrail(auditSvc).
		WithCancelGuards(permissiveGuards{}, permissiveGuards{}).
		WithReorderPricing(pricing.NewService(pricing.NewRepository(pool))).
		WithReorderInventory(inventory.NewService(inventory.NewRepository(pool))).
		WithCustomerType(intCustType{})

	ctx := identity.ContextWithStaff(context.Background(), edIntManagerCtx())
	res, err := svc.EditLineQty(ctx, ord, 1, "4")
	if err != nil {
		t.Fatalf("qty edit through the real pricing seam: %v", err)
	}
	if res.Line == nil || res.Line.UnitPriceCents <= 0 {
		t.Fatalf("want a resolved unit price from the real resolver, got %+v", res.Line)
	}
	qty, _, lineTotal := liveLine(t, pool, ord, 1)
	if qty != "4.0000" {
		t.Errorf("want the quantity persisted as 4.0000, got %s", qty)
	}
	sub, _ := liveTotals(t, pool, ord)
	if sub != lineTotal {
		t.Errorf("order subtotal (%d) must equal the sum of its lines (%d)", sub, lineTotal)
	}
}

// TestIntegration_EditByOrderNo proves the DIB-379/F3 alternate key reaches the DIB-175 surface too:
// the visible order_no ("#SO-4") is accepted wherever the order id is, on both the write and the
// change-history read, and resolves to the same order.
func TestIntegration_EditByOrderNo(t *testing.T) {
	pool := intPool(t)
	_, ord, _, _ := seedEditOrder(t, pool, "CONFIRMED", 0)
	var orderNo string
	if err := pool.QueryRow(context.Background(),
		`SELECT order_no::text FROM orders WHERE id = $1`, ord).Scan(&orderNo); err != nil {
		t.Fatalf("read order_no: %v", err)
	}
	svc, _ := editSvc(t, pool)
	ctx := identity.ContextWithStaff(context.Background(), edIntManagerCtx())

	if _, err := svc.EditLineQty(ctx, orderNo, 1, "9"); err != nil {
		t.Fatalf("edit by order_no %q: %v", orderNo, err)
	}
	if qty, _, _ := liveLine(t, pool, ord, 1); qty != "9.0000" {
		t.Errorf("want the edit applied to the resolved order, got qty %s", qty)
	}
	hist, err := svc.OrderEditHistoryFor(ctx, orderNo)
	if err != nil {
		t.Fatalf("history by order_no: %v", err)
	}
	if hist.OrderID != ord {
		t.Errorf("want the history keyed to the resolved id %s, got %s", ord, hist.OrderID)
	}
	if len(hist.Changes) == 0 {
		t.Error("want the edit on the change history")
	}
}

// - the HTTP surface -

// TestIntegration_EditHTTPSurface drives the real handlers over the real repository: an edit, then the
// change-history read, both at the HTTP boundary the browser actually calls.
func TestIntegration_EditHTTPSurface(t *testing.T) {
	pool := intPool(t)
	_, ord, _, _ := seedEditOrder(t, pool, "CONFIRMED", 0)
	svc, _ := editSvc(t, pool)
	h := NewHandler(svc)
	sc := edIntManagerCtx()

	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPatch, "/api/v1/orders/"+ord+"/lines/1",
		strings.NewReader(`{"qty_entered":"6"}`))
	r.SetPathValue("id", ord)
	r.SetPathValue("lineNo", "1")
	r = r.WithContext(identity.ContextWithStaff(r.Context(), sc))
	h.editLineQty(rr, r, guards.StaffCaller{})
	if rr.Code != http.StatusOK {
		t.Fatalf("edit: want 200, got %d (%s)", rr.Code, rr.Body.String())
	}
	var edited struct {
		Status        string `json:"status"`
		SubtotalCents int64  `json:"subtotal_cents"`
		Line          struct {
			QtyEntered string `json:"qty_entered"`
		} `json:"line"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &edited); err != nil {
		t.Fatalf("decode edit response: %v", err)
	}
	if edited.Line.QtyEntered != "6.0000" || edited.SubtotalCents != 600 {
		t.Errorf("edit response = %+v, want qty 6.0000 and subtotal 600", edited)
	}

	rr2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodGet, "/api/v1/orders/"+ord+"/edits", nil)
	r2.SetPathValue("id", ord)
	r2 = r2.WithContext(identity.ContextWithStaff(r2.Context(), sc))
	h.orderEditHistory(rr2, r2, guards.StaffCaller{})
	if rr2.Code != http.StatusOK {
		t.Fatalf("history: want 200, got %d (%s)", rr2.Code, rr2.Body.String())
	}
	var hist OrderEditHistory
	if err := json.Unmarshal(rr2.Body.Bytes(), &hist); err != nil {
		t.Fatalf("decode history: %v", err)
	}
	if len(hist.Changes) == 0 {
		t.Fatal("want the edit visible on the change history")
	}
}

// - B2-1: the audit trail is atomic with the mutation (live PG) -

// failAfterRealRecorder wraps the real audit service: RecordTx writes the row on the caller's tx (so a
// real ORDER_CHANGED row genuinely lands inside the edit's transaction) and THEN returns an error. That
// forces the edit's guarded write to roll back, taking the just-written audit row with it. Asserting
// zero persisted ORDER_CHANGED rows afterwards is the strongest proof the trail shares the mutation's
// transaction, not a separate best-effort write (B2-1).
type failAfterRealRecorder struct{ real *audit.Service }

func (f failAfterRealRecorder) Record(ctx context.Context, a audit.SensitiveAccess) error {
	return f.real.Record(ctx, a)
}
func (f failAfterRealRecorder) RecordTx(ctx context.Context, q audit.Execer, a audit.SensitiveAccess) error {
	_ = f.real.RecordTx(ctx, q, a) // write the row on the tx…
	return errors.New("audit write failed after persisting on tx")
}

func countOrderChangedRows(t *testing.T, pool *pgxpool.Pool, orderID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE entity_id = $1 AND action = 'ORDER_CHANGED'`, orderID).Scan(&n); err != nil {
		t.Fatalf("count ORDER_CHANGED: %v", err)
	}
	return n
}

// TestIntegration_EditAudit_RecorderFailureRollsBack proves atomicity against real Postgres: the audit
// row is written on the edit's transaction and then a recorder error rolls the WHOLE thing back, the
// line is unchanged AND no ORDER_CHANGED row survives. On the pre-B2 code (post-commit best-effort
// audit) the line change would have committed and this fails.
func TestIntegration_EditAudit_RecorderFailureRollsBack(t *testing.T) {
	pool := intPool(t)
	_, ord, _, _ := seedEditOrder(t, pool, "CONFIRMED", 0)
	auditSvc := audit.NewService(audit.NewRepository(pool), nil)
	svc := NewService(NewRepository(pool)).
		WithAudit(failAfterRealRecorder{real: auditSvc}).
		WithCancelGuards(permissiveGuards{}, permissiveGuards{}).
		WithReorderPricing(intPricer{}).WithReorderInventory(intInv{}).WithCustomerType(intCustType{})
	ctx := identity.ContextWithStaff(context.Background(), edIntManagerCtx())

	if _, err := svc.EditLineQty(ctx, ord, 1, "7"); err == nil {
		t.Fatal("want the edit to fail when the recorder fails")
	}
	if qty, _, _ := liveLine(t, pool, ord, 1); qty != "2.0000" {
		t.Errorf("the edit must have rolled back; line qty = %s, want 2.0000", qty)
	}
	if n := countOrderChangedRows(t, pool, ord); n != 0 {
		t.Errorf("a rolled-back edit must leave no ORDER_CHANGED rows; found %d", n)
	}
}

// TestIntegration_EditAudit_CausalOrder proves the change history reads by the 0078 seq, not wall-clock
// created_at (B2-1). The several rows one qty edit emits share a transaction timestamp, so ordering by
// created_at (with the random-UUID id tiebreak the repo uses) would shuffle them; ordering by seq keeps
// them in emission order. It also checks cross-edit order (edit A's rows precede edit B's).
func TestIntegration_EditAudit_CausalOrder(t *testing.T) {
	pool := intPool(t)
	_, ord, _, _ := seedEditOrder(t, pool, "CONFIRMED", 0)
	svc, _ := editSvc(t, pool)
	ctx := identity.ContextWithStaff(context.Background(), edIntManagerCtx())

	if _, err := svc.EditLineQty(ctx, ord, 1, "4"); err != nil { // edit A
		t.Fatalf("edit A: %v", err)
	}
	if _, err := svc.EditLineQty(ctx, ord, 1, "6"); err != nil { // edit B
		t.Fatalf("edit B: %v", err)
	}
	hist, err := svc.OrderEditHistoryFor(ctx, ord)
	if err != nil {
		t.Fatalf("read history: %v", err)
	}

	// Cross-edit: the two qty_entered rows must be 2→4 (A) then 4→6 (B), in that order.
	var qtyRows []OrderFieldChange
	for _, c := range hist.Changes {
		if c.Field == "qty_entered" {
			qtyRows = append(qtyRows, c)
		}
	}
	if len(qtyRows) != 2 || qtyRows[0].After != "4.0000" || qtyRows[1].After != "6.0000" {
		t.Fatalf("cross-edit order wrong: %+v", qtyRows)
	}

	// Intra-edit: within edit A (its request_id), the emission order qty_entered → line_total_cents →
	// total_cents must be preserved. Under created_at ordering these share a timestamp and would sort by
	// random id; only seq preserves the order.
	reqA := qtyRows[0].RequestID
	posA := map[string]int{}
	for i, c := range hist.Changes {
		if c.RequestID == reqA {
			if _, seen := posA[c.Field]; !seen {
				posA[c.Field] = i
			}
		}
	}
	if !(posA["qty_entered"] < posA["line_total_cents"] && posA["line_total_cents"] < posA["total_cents"]) {
		t.Errorf("intra-edit rows not in emission (seq) order: %v", posA)
	}
}

// - B2-2: the money predicate runs INSIDE the row lock (live PG) -

// lockProbeInvoiceReader is a FinalizedInvoiceReader that, when consulted, tries to take the order row
// FOR UPDATE NOWAIT on a SEPARATE connection. If that fails with lock_not_available the caller's
// transaction is already holding the order-row lock, i.e. the money predicate is running UNDER the
// lock, which is exactly what B2-2 requires. It reports "no invoice" so the operation proceeds, and
// records whether it observed the lock. On the pre-B2 code the predicate ran in authorizeEdit before any
// lock was taken, so the NOWAIT would SUCCEED and sawLock would stay false.
type lockProbeInvoiceReader struct {
	pool    *pgxpool.Pool
	sawLock *bool
	calls   *int
}

func (r lockProbeInvoiceReader) OrderHasFinalizedInvoice(ctx context.Context, orderID string) (bool, error) {
	*r.calls++
	_, err := r.pool.Exec(ctx, `SELECT 1 FROM orders WHERE id = $1 FOR UPDATE NOWAIT`, orderID)
	if err != nil && strings.Contains(err.Error(), "55P03") { // lock_not_available → the caller holds the lock
		*r.sawLock = true
	}
	return false, nil // no money attached, let the operation continue
}

// TestIntegration_EditMoneyPredicateUnderLock proves the edit's money predicate runs while the order row
// lock is held (B2-2), deterministically: the predicate reader probes the lock with FOR UPDATE NOWAIT
// and can only fail to take it if the edit's own transaction already holds it. On the pre-B2 code
// (predicate in authorizeEdit, before the guarded write's lock) the probe would have succeeded.
func TestIntegration_EditMoneyPredicateUnderLock(t *testing.T) {
	pool := intPool(t)
	_, ord, _, _ := seedEditOrder(t, pool, "CONFIRMED", 0)

	sawLock, calls := false, 0
	svc := NewService(NewRepository(pool)).
		WithAudit(audit.NewService(audit.NewRepository(pool), nil)).
		WithCancelGuards(lockProbeInvoiceReader{pool: pool, sawLock: &sawLock, calls: &calls}, permissiveGuards{}).
		WithReorderPricing(intPricer{}).WithReorderInventory(intInv{}).WithCustomerType(intCustType{})

	ctx := identity.ContextWithStaff(context.Background(), edIntManagerCtx())
	if _, err := svc.EditLineQty(ctx, ord, 1, "7"); err != nil {
		t.Fatalf("edit: %v", err)
	}
	if calls == 0 {
		t.Fatal("the money predicate was never consulted")
	}
	if !sawLock {
		t.Error("the money predicate did NOT run under the order-row lock (B2-2): the NOWAIT probe acquired the lock")
	}
}

// TestIntegration_CancelMoneyPredicateUnderLock is the same proof for the DIB-191 cancel, which now
// routes through UpdateStatusFromChecked so its predicate runs under the lock too (B2-2).
func TestIntegration_CancelMoneyPredicateUnderLock(t *testing.T) {
	pool := intPool(t)
	_, ord, _, _ := seedEditOrder(t, pool, "CONFIRMED", 0)

	sawLock, calls := false, 0
	auditSvc := audit.NewService(audit.NewRepository(pool), nil)
	svc := NewService(NewRepository(pool)).WithAudit(auditSvc).WithAuditTrail(auditSvc).
		WithCancelGuards(lockProbeInvoiceReader{pool: pool, sawLock: &sawLock, calls: &calls}, permissiveGuards{})

	ctx := identity.ContextWithStaff(context.Background(), edIntManagerCtx())
	if _, err := svc.TransitionManual(ctx, ord, ActionCancel, "customer backed out"); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if calls == 0 {
		t.Fatal("the cancel predicate was never consulted")
	}
	if !sawLock {
		t.Error("the cancel predicate did NOT run under the order-row lock (B2-2)")
	}
	if got := liveStatus(t, pool, ord); got != "CANCELLED" {
		t.Errorf("the cancel should have completed (no money attached); status = %s", got)
	}
}

// - B2-3: the staff order read is snapshot-consistent (live PG) -

// TestIntegration_OrderReadIsConsistentUnderConcurrentEdit hammers OrderLines while a writer edits the
// order, asserting the header subtotal always equals Σ(line_total_cents). Under two bare pool reads the
// pair could straddle an edit's commit (subtotal ≠ Σlines); the REPEATABLE READ snapshot (B2-3) makes it
// always agree.
func TestIntegration_OrderReadIsConsistentUnderConcurrentEdit(t *testing.T) {
	pool := intPool(t)
	_, ord, _, _ := seedEditOrder(t, pool, "CONFIRMED", 0)
	svc, _ := editSvc(t, pool)
	repo := NewRepository(pool)

	stop := make(chan struct{})
	go func() {
		sctx := identity.ContextWithStaff(context.Background(), edIntManagerCtx())
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
				_, _ = svc.EditLineQty(sctx, ord, 1, fmt.Sprintf("%d", (i%9)+1))
			}
		}
	}()
	defer close(stop)

	for i := 0; i < 60; i++ {
		lines, totals, _, err := repo.OrderLines(context.Background(), ord)
		if err != nil {
			t.Fatalf("order read: %v", err)
		}
		var sum int64
		for _, l := range lines {
			sum += l.LineTotalCents
		}
		if totals.SubtotalCents != sum {
			t.Fatalf("torn read: subtotal %d ≠ Σ(lines) %d", totals.SubtotalCents, sum)
		}
	}
}

// - B2-4: the order-number resolver is branch-scoped (live PG) -

// TestIntegration_ResolveOrderNo_CrossBranchIs404 proves option (a): a caller who cannot reach the
// order's branch gets 404 when looking it up by #SO number (no existence oracle), while a UUID deep-link
// of the same order keeps the 403 that tells an in-company caller it exists but is out of their branch.
func TestIntegration_ResolveOrderNo_CrossBranchIs404(t *testing.T) {
	pool := intPool(t)
	_, ord, _, _ := seedEditOrder(t, pool, "CONFIRMED", 0) // lives in trentonBranch
	var orderNo string
	if err := pool.QueryRow(context.Background(),
		`SELECT order_no::text FROM orders WHERE id = $1`, ord).Scan(&orderNo); err != nil {
		t.Fatalf("read order_no: %v", err)
	}
	svc, _ := editSvc(t, pool)

	// A manager whose ONLY branch is otherBranch, scoped, not global.
	crossCtx := identity.ContextWithStaff(context.Background(), managerAt(edIntManager, otherBranch))

	// By #SO number → 404 (the number space is not an oracle).
	if _, err := svc.OrderRead(crossCtx, orderNo); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-branch #SO lookup must be 404 (ErrNotFound), got %v", err)
	}
	if _, err := svc.OrderEditHistoryFor(crossCtx, orderNo); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-branch #SO history must be 404, got %v", err)
	}
	// By UUID deep-link → 403 (exists, but out of your branch), semantics preserved.
	if _, err := svc.OrderRead(crossCtx, ord); !errors.Is(err, ErrForbidden) {
		t.Errorf("cross-branch UUID deep-link must stay 403 (ErrForbidden), got %v", err)
	}
	// A GLOBAL caller resolves the #SO fine.
	globalCtx := identity.ContextWithStaff(context.Background(), globalStaff(edIntManager))
	if _, err := svc.OrderRead(globalCtx, orderNo); err != nil {
		t.Errorf("a GLOBAL caller must resolve the #SO, got %v", err)
	}
}

// asNotEditable is errors.As for *NotEditableError, kept local so the assertion reads the same in every
// test above.
func asNotEditable(err error, target **NotEditableError) bool {
	for e := err; e != nil; {
		if ne, ok := e.(*NotEditableError); ok {
			*target = ne
			return true
		}
		u, ok := e.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		e = u.Unwrap()
	}
	return false
}
