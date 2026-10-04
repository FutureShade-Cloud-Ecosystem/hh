//go:build integration

// qd5_lock_integration_test.go, QD-5 review round 2 (P2-A): a group invoice and a counter invoice of
// one order never both land. Both writers take a row lock on the order as the first statement of
// their transaction and run the existence checks after it, so the two serialise and the second one
// sees the first one's committed document. The race is forced the way the reviewer forced it: one
// side is slowed by a trigger that sleeps inside its transaction, the other is issued in that window.
//
//	Run: DATABASE_URL='postgres://.../scratch?sslmode=disable' \
//	     go test -tags integration ./cmd/core/ -run QD5Lock -count=1 -p 1
package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/quote"
)

// slowInsert makes every insert into table for the customer sleep for the given time inside the
// inserting transaction, which stretches that side's window so the other side can be issued in it.
func (e *qd5Env) slowInsert(table, customerID string, d time.Duration) {
	e.t.Helper()
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx, `
CREATE OR REPLACE FUNCTION qd5_slow() RETURNS trigger LANGUAGE plpgsql AS $f$
BEGIN
  IF to_jsonb(NEW)->>'customer_id' = TG_ARGV[0] THEN PERFORM pg_sleep(TG_ARGV[1]::float8); END IF;
  RETURN NEW;
END $f$`); err != nil {
		e.t.Fatalf("create the slow function: %v", err)
	}
	name := fmt.Sprintf("qd5_slow_%s", table)
	if _, err := e.pool.Exec(ctx, fmt.Sprintf(
		`CREATE TRIGGER %s BEFORE INSERT ON %s FOR EACH ROW EXECUTE FUNCTION qd5_slow('%s', '%g')`,
		name, table, customerID, d.Seconds())); err != nil {
		e.t.Fatalf("install the slow insert on %s: %v", table, err)
	}
	e.t.Cleanup(func() {
		_, _ = e.pool.Exec(context.Background(), fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON %s`, name, table))
	})
}

type qd5Outcome struct {
	doc billing.InvoiceDoc
	err error
}

// TestQD5Lock_ACounterInvoiceInFlightBlocksAGroupInvoiceOfTheSameOrder: the counter's payment insert
// is slowed by 2 seconds; a Money Desk manager invoices a completed group of the same order inside
// that window. Exactly one invoice remains, the counter's, and the group is linked to it.
func TestQD5Lock_ACounterInvoiceInFlightBlocksAGroupInvoiceOfTheSameOrder(t *testing.T) {
	e := qd5Setup(t)
	q := e.seedQuote(1_000_000, 0, qd5Trenton)
	res := e.convert(q)
	g1, _ := e.twoGroups(res.OrderID)
	e.slowInsert("payments", q.CustomerID, 2*time.Second)
	tail := billing.NewInvoiceWriter(e.pool).WithOrderSource(w6cOrderInvoiceSource{svc: e.orderSvc})

	counter := make(chan error, 1)
	go func() {
		_, err := e.svc.TakeOrderPayment(e.counter(), q.QuoteID, pay("CASH", 1000, "k-race-a"))
		counter <- err
	}()
	time.Sleep(700 * time.Millisecond) // inside the counter's window: the invoice is written, the payment is sleeping
	group, gerr := tail.InvoiceOrderGroups(context.Background(), res.OrderID, []string{g1}, "ON_DEMAND_SUBSET")
	if err := <-counter; err != nil {
		t.Fatalf("the counter: %v", err)
	}
	if gerr != nil {
		t.Fatalf("the group writer must answer the counter's document, got %v", gerr)
	}
	docs, total := e.billedTotal(q)
	if docs != 1 || total != q.Total {
		t.Fatalf("billed %d across %d documents (order total %d): exactly one invoice must remain", total, docs, q.Total)
	}
	if !group.Reused || !group.AtCounter {
		t.Fatalf("the group answer is the counter's one document, got %+v", group)
	}
}

// TestQD5Lock_AGroupInvoiceInFlightBlocksTheCountersInvoiceOfTheSameOrder: the other way round. The group
// writer's invoice insert is slowed by 2 seconds; the counter's payment is issued in that window. The
// group's invoice is the one that remains, and the counter answers its refusal, writing nothing.
func TestQD5Lock_AGroupInvoiceInFlightBlocksTheCountersInvoiceOfTheSameOrder(t *testing.T) {
	e := qd5Setup(t)
	q := e.seedQuote(1_000_000, 0, qd5Trenton)
	res := e.convert(q)
	g1, _ := e.twoGroups(res.OrderID)
	e.slowInsert("invoices", q.CustomerID, 2*time.Second)
	tail := billing.NewInvoiceWriter(e.pool).WithOrderSource(w6cOrderInvoiceSource{svc: e.orderSvc})

	group := make(chan qd5Outcome, 1)
	go func() {
		d, err := tail.InvoiceOrderGroups(context.Background(), res.OrderID, []string{g1}, "ON_DEMAND_SUBSET")
		group <- qd5Outcome{d, err}
	}()
	time.Sleep(700 * time.Millisecond) // inside the group writer's window
	_, cerr := e.svc.TakeOrderPayment(e.counter(), q.QuoteID, pay("CASH", 1000, "k-race-b"))
	g := <-group
	if g.err != nil {
		t.Fatalf("the group writer: %v", g.err)
	}
	var conflict *quote.OrderPaymentConflictError
	if !errors.As(cerr, &conflict) {
		t.Fatalf("the counter must answer its refusal, got %v", cerr)
	}
	docs, total := e.billedTotal(q)
	if docs != 1 || total != 3051 {
		t.Fatalf("billed %d across %d documents: exactly the group's one invoice must remain", total, docs)
	}
	if c := e.counts(q); c.Payments != 0 {
		t.Fatalf("a refused deposit posted a payment: %+v", c)
	}
	if !strings.Contains(cerr.Error(), g.doc.InvoiceNo) {
		t.Fatalf("the refusal names the invoice that stands, %s: %v", g.doc.InvoiceNo, cerr)
	}
}
