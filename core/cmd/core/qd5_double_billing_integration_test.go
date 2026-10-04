//go:build integration

// qd5_double_billing_integration_test.go, QD-5 review round 1 (P1-2, P2-1): the order is billed once,
// whichever door bills it, and a door's behaviour that predates QD-5 is unchanged. The scenario is the
// reviewer's: a credit order of two lines (a discounted one and a plain one, 5311 with tax) split into
// two shipment groups.
//
//	Run: DATABASE_URL='postgres://.../scratch?sslmode=disable' \
//	     go test -tags integration ./cmd/core/ -run QD5Billing -count=1 -p 1
package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/quote"
)

// twoGroups plants one COMPLETE shipment group per order line and returns their ids, line 1 first.
func (e *qd5Env) twoGroups(orderID string) (g1, g2 string) {
	e.t.Helper()
	// One statement: the partition rule wants every line in exactly one live group at its end.
	if err := e.pool.QueryRow(context.Background(), `
		WITH g AS (
		  INSERT INTO shipment_groups (order_id, group_no, status, completed_at)
		  VALUES ($1, 1, 'COMPLETE', now()), ($1, 2, 'COMPLETE', now()) RETURNING id, group_no),
		l AS (
		  INSERT INTO shipment_group_lines (shipment_group_id, order_line_id, qty_base)
		  SELECT g.id, ol.id, ol.qty_base FROM g JOIN order_lines ol ON ol.order_id = $1 AND ol.line_no = g.group_no RETURNING 1)
		SELECT (SELECT id::text FROM g WHERE group_no = 1), (SELECT id::text FROM g WHERE group_no = 2), (SELECT count(*) FROM l)`,
		orderID).Scan(&g1, &g2, new(int)); err != nil {
		e.t.Fatalf("seed shipment groups: %v", err)
	}
	return g1, g2
}

func (e *qd5Env) billedTotal(q qd5Quote) (docs int, total int64) {
	e.t.Helper()
	if err := e.pool.QueryRow(context.Background(),
		`SELECT count(*), COALESCE(SUM(total_cents),0)::bigint FROM invoices WHERE customer_id = $1 AND doc_type = 'INVOICE'`,
		q.CustomerID).Scan(&docs, &total); err != nil {
		e.t.Fatal(err)
	}
	return docs, total
}

// TestQD5Billing_AGroupBilledOrderPaidAtTheCounterIsNeverBilledTwice (P1-2): group 1 is billed through
// the manager route's writer, then a deposit is taken at the door. The counter refuses with a 409 that
// says why and raises nothing: the invoices of the order never total more than the order.
func TestQD5Billing_AGroupBilledOrderPaidAtTheCounterIsNeverBilledTwice(t *testing.T) {
	e := qd5Setup(t)
	q := e.seedQuote(1_000_000, 0, qd5Trenton)
	res := e.convert(q)
	g1, _ := e.twoGroups(res.OrderID)
	tail := billing.NewInvoiceWriter(e.pool).WithOrderSource(w6cOrderInvoiceSource{svc: e.orderSvc})
	first, err := tail.InvoiceOrderGroups(context.Background(), res.OrderID, []string{g1}, "ON_DEMAND_SUBSET")
	if err != nil {
		t.Fatalf("group 1: %v", err)
	}
	if first.TotalCents != 3051 {
		t.Fatalf("group 1 bills line 1 with its tax, 3051, got %d", first.TotalCents)
	}

	_, err = e.svc.TakeOrderPayment(e.counter(), q.QuoteID, pay("CASH", 1000, "k-deposit-after-group"))
	var conflict *quote.OrderPaymentConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("the counter must refuse an order that already has an invoice raised another way, got %v", err)
	}
	// The words say who can record the payment, not a door the counter roles cannot open (P3-1).
	for _, want := range []string{"Payables and receivables", "General Manager", "Financial controller", first.InvoiceNo} {
		if !strings.Contains(conflict.Msg, want) {
			t.Fatalf("the refusal must say %q, got %q", want, conflict.Msg)
		}
	}
	if strings.Contains(conflict.Msg, "register") {
		t.Fatalf("the refusal sends the clerk to a door they cannot open: %q", conflict.Msg)
	}
	if docs, total := e.billedTotal(q); docs != 1 || total != 3051 || total > q.Total {
		t.Fatalf("the invoices of the order total %d across %d documents (order total %d): never more than the order", total, docs, q.Total)
	}
	if c := e.counts(q); c.Payments != 0 {
		t.Fatalf("a refused deposit posted a payment: %+v", c)
	}
}

// TestQD5Billing_TheSyncDoorsPartialWholeInvoiceStillLeavesALaterGroupBilled (P2-1): the base behaviour
// of an existing caller. The sync door's whole invoice bills only the fulfilled line (3051); the group
// that carries the other line is then billed as a document of its own (2260), so 5311 is billed in
// total, exactly as before QD-5. Only the counter's own document covers a shipment plan's groups.
func TestQD5Billing_TheSyncDoorsPartialWholeInvoiceStillLeavesALaterGroupBilled(t *testing.T) {
	e := qd5Setup(t)
	q := e.seedQuote(1_000_000, 0, qd5Trenton)
	res := e.convert(q)
	_, g2 := e.twoGroups(res.OrderID)
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx,
		`UPDATE order_lines SET fulfilled_base = qty_base WHERE order_id = $1 AND line_no = 1`, res.OrderID); err != nil {
		t.Fatal(err)
	}
	tail := billing.NewInvoiceWriter(e.pool).WithOrderSource(w6cOrderInvoiceSource{svc: e.orderSvc})
	whole, err := tail.InvoiceOrder(ctx, res.OrderID)
	if err != nil {
		t.Fatalf("the sync door's whole invoice: %v", err)
	}
	if whole.TotalCents != 3051 {
		t.Fatalf("the whole invoice bills the fulfilled line only: %d", whole.TotalCents)
	}
	later, err := tail.InvoiceOrderGroups(ctx, res.OrderID, []string{g2}, "ON_DEMAND_SUBSET")
	if err != nil {
		t.Fatalf("the later group: %v", err)
	}
	if later.Reused || later.InvoiceID == whole.InvoiceID || later.TotalCents != 2260 {
		t.Fatalf("the later group is its own document at 2260, got %+v", later)
	}
	if docs, total := e.billedTotal(q); docs != 2 || total != 5311 {
		t.Fatalf("billed %d across %d documents, want 5311 across 2 as on the base", total, docs)
	}
}
