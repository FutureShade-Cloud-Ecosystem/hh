//go:build integration

package billing

// pick_hold_integration_test.go, the item 9 review's void and credit memo
// legs: the pay before release hold counts only live INVOICE documents, the
// same predicate order_seam.go and invoice_writer.go hold (doc_type INVOICE,
// status not DRAFT and not VOID). A voided invoice is not money billed, and a
// credit memo is its own document, never a skew on the order's invoiced or
// paid figures.

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customer"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
)

// pickHoldRig plants one zero day terms customer, one order worth totalCents,
// and whatever invoice rows a test names, and unwinds all of it.
type pickHoldRig struct {
	pool       *pgxpool.Pool
	customerID string
	orderID    string
}

func newPickHoldRig(t *testing.T, totalCents int64) *pickHoldRig {
	t.Helper()
	ctx := context.Background()
	pool := intPool(t)
	var branchID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations WHERE loc_type='BRANCH' LIMIT 1`).Scan(&branchID); err != nil {
		t.Skipf("no seeded BRANCH: %v", err)
	}
	suffix := fmt.Sprintf("PH-%s-%d", t.Name(), time.Now().UnixNano()%1_000_000_000)
	var customerID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name, customer_type, home_branch_id, payment_terms_days, credit_limit_cents)
		 VALUES ('PHO-'||$1, 'Pick hold proof '||$1, 'CONTRACTOR', $2::uuid, 0, 5000000) RETURNING id::text`,
		suffix, branchID).Scan(&customerID); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	var orderID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, subtotal_cents, total_cents)
		 VALUES ('ORD-PHO-'||$1, $2::uuid, $3::uuid, 'CONFIRMED', 'WILL_CALL', $4, $4) RETURNING id::text`,
		suffix, customerID, branchID, totalCents).Scan(&orderID); err != nil {
		t.Fatalf("seed order: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM invoices WHERE order_id = $1`, orderID)
		_, _ = pool.Exec(c, `DELETE FROM orders WHERE id = $1`, orderID)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1`, customerID)
	})
	return &pickHoldRig{pool: pool, customerID: customerID, orderID: orderID}
}

// plantInvoice writes one document row on the rig's order, at the shape named.
func (r *pickHoldRig) plantInvoice(t *testing.T, docType, status string, totalCents, balanceCents int64) {
	t.Helper()
	if _, err := r.pool.Exec(context.Background(), `
INSERT INTO invoices (invoice_no, customer_id, order_id, branch_id, doc_type, status,
                      subtotal_cents, tax_cents, total_cents, balance_cents, due_date, finalized_at)
VALUES ('#INV-PHO-'||nextval('invoice_no_seq'), $1::uuid, $2::uuid,
        (SELECT branch_id FROM orders WHERE id = $2::uuid), $3, $4, $5, 0, $5, $6, current_date, now())`,
		r.customerID, r.orderID, docType, status, totalCents, balanceCents); err != nil {
		t.Fatalf("plant %s %s: %v", docType, status, err)
	}
}

// A voided invoice keeps the order held: the void billed nothing, so the whole
// total is the uninvoiced remainder and the hold reads it as owed.
func TestIntegration_PickHold_AVoidedInvoiceKeepsTheOrderHeld(t *testing.T) {
	rig := newPickHoldRig(t, 10000)
	rig.plantInvoice(t, "INVOICE", "VOID", 10000, 10000)
	held, owed, paid, total, err := pickHoldRepo(rig.pool).PickHoldForOrder(context.Background(), rig.orderID)
	if err != nil {
		t.Fatalf("hold read: %v", err)
	}
	if !held || owed != 10000 || paid != 0 || total != 10000 {
		t.Fatalf("with a voided invoice the facts read (held %v, owed %d, paid %d, total %d), want held, 10000, 0, 10000: the void billed nothing",
			held, owed, paid, total)
	}
}

// A credit memo does not skew owed or paid: the order's live invoice carries
// its own figures and the memo (here fully applied, the shape that skews the
// most) is its own document, never part of the order's invoiced or paid sums.
func TestIntegration_PickHold_ACreditMemoDoesNotSkewOwedOrPaid(t *testing.T) {
	rig := newPickHoldRig(t, 10000)
	rig.plantInvoice(t, "INVOICE", "PARTIALLY_PAID", 10000, 7000)
	rig.plantInvoice(t, "CREDIT_MEMO", "FINALIZED", -2000, 0)
	held, owed, paid, total, err := pickHoldRepo(rig.pool).PickHoldForOrder(context.Background(), rig.orderID)
	if err != nil {
		t.Fatalf("hold read: %v", err)
	}
	if !held || owed != 7000 || paid != 3000 || total != 10000 {
		t.Fatalf("with a credit memo beside a live invoice the facts read (held %v, owed %d, paid %d, total %d), want held, 7000, 3000, 10000: the memo is not the invoice",
			held, owed, paid, total)
	}
}

// pickHoldRepo is a billing repository with the hold's order and customer
// seams bound, as the composition root binds them.
func pickHoldRepo(pool *pgxpool.Pool) *Repository {
	return NewRepository(pool).WithPickHoldSources(order.NewRepository(pool), customer.NewRepository(pool))
}
