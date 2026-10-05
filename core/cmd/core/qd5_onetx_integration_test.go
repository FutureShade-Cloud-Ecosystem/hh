//go:build integration

// qd5_onetx_integration_test.go, QD-5 review round 1 (P2-2, P1-3): the counter's action is ONE
// transaction on ONE connection. A fault injected right after each write, and before the commit,
// leaves nothing behind; and a pool of any size serves any number of parallel callers, because each
// call holds exactly one connection and the per quote lock is taken inside that transaction.
//
//	Run: DATABASE_URL='postgres://.../scratch?sslmode=disable' \
//	     go test -tags integration ./cmd/core/ -run QD5OneTx -count=1 -p 1
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/inventory"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/quote"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
)

// qd5SetupSized builds the money door over a pool of exactly maxConns connections (0 keeps pgx's
// default), with the real stock reserver so a conversion raises its hard holds, and one product with
// stock at the branch so a hold can be raised on it.
func qd5SetupSized(t *testing.T, maxConns int32) *qd5Env { return qd5SetupWith(t, maxConns, true) }

// qd5SetupWith is qd5SetupSized with the stock reserver optional: inventory's reserve reads its
// allocation policy through the pool while the conversion holds the action's connection, a nested read
// no pool of one can serve, so the one connection probe runs without holds.
func qd5SetupWith(t *testing.T, maxConns int32, withStock bool) *qd5Env {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL is not set; the QD-5 money path needs the scratch database")
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("pool config: %v", err)
	}
	if maxConns > 0 {
		cfg.MaxConns = maxConns
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	qd5KeepSequences(t, pool)
	var userID string
	if err := pool.QueryRow(context.Background(),
		`SELECT id::text FROM users WHERE status='ACTIVE' ORDER BY created_at LIMIT 1`).Scan(&userID); err != nil {
		t.Fatalf("no seeded staff: %v", err)
	}
	orderSvc := order.NewService(order.NewRepository(pool))
	inv := inventory.NewService(inventory.NewRepository(pool))
	repo := quote.NewRepository(pool).WithCreditGate(till.NewHoldAwareCreditGate(till.NewRepository(pool)))
	if withStock {
		repo.WithStockReserver(w5aQuoteStockReserver{svc: inv})
	}
	aud := audit.NewService(audit.NewRepository(pool), nil)
	invoicer, payer := quoteMoneyDoor(pool, orderSvc)
	svc := quote.NewStaffService(repo).WithAudit(aud).WithAuditTrail(aud).
		WithOrderInvoicer(invoicer).WithCounterPayer(payer)
	if withStock {
		svc.WithStockReserver(w5aQuoteStockReserver{svc: inv})
	}
	return &qd5Env{t: t, pool: pool, svc: svc, orderSvc: orderSvc, userID: userID}
}

// stockFor makes sure the product the seeded quote lines use has stock at the branch, so a hold can
// be raised against it.
func (e *qd5Env) stockFor(branch string) {
	e.t.Helper()
	var prod string
	if err := e.pool.QueryRow(context.Background(), `
		SELECT p.id::text FROM products p
		  JOIN product_uoms pu ON pu.product_id = p.id AND pu.qty_in_base = 1
		 WHERE p.is_active ORDER BY p.sku LIMIT 1`).Scan(&prod); err != nil {
		e.t.Skipf("no factor-1 anchored product seeded: %v", err)
	}
	qd5KeepStockRow(e.t, e.pool, prod) // the row goes back exactly as it was when the test ends
	if _, err := e.pool.Exec(context.Background(), `
		INSERT INTO stock_balances (product_id, location_id, branch_id, on_hand_base, allocated_base)
		SELECT p.id, $1::uuid, $1::uuid, 100000, 0 FROM products p
		  JOIN product_uoms pu ON pu.product_id = p.id AND pu.qty_in_base = 1
		 WHERE p.is_active ORDER BY p.sku LIMIT 1
		ON CONFLICT (product_id, location_id) DO UPDATE SET on_hand_base = GREATEST(stock_balances.on_hand_base, 100000)`,
		branch); err != nil {
		e.t.Skipf("no stock row could be made: %v", err)
	}
}

// qd5Boom is the injected fault: a trigger function that raises right after a row is written for the
// test's own customer, which is the instant after that write and before the commit. The customer is
// the trigger's argument; the tables that do not carry it are resolved through their parent row.
const qd5Boom = `
CREATE OR REPLACE FUNCTION qd5_boom() RETURNS trigger LANGUAGE plpgsql AS $f$
DECLARE
  cust text := TG_ARGV[0];
  hit  boolean := false;
  j    jsonb := to_jsonb(NEW);
BEGIN
  IF TG_TABLE_NAME IN ('orders', 'invoices', 'payments', 'customer_transactions', 'quotes') THEN
    hit := j->>'customer_id' = cust;
    IF TG_ARGV[1] <> '' THEN hit := hit AND (j->>'txn_type' = TG_ARGV[1]); END IF;
  ELSIF TG_TABLE_NAME IN ('invoice_lines', 'payment_allocations') THEN
    hit := EXISTS (SELECT 1 FROM invoices i WHERE i.id = (j->>'invoice_id')::uuid AND i.customer_id::text = cust);
  ELSIF TG_TABLE_NAME = 'order_lines' THEN
    hit := EXISTS (SELECT 1 FROM orders o WHERE o.id = (j->>'order_id')::uuid AND o.customer_id::text = cust);
  ELSIF TG_TABLE_NAME = 'quote_accept_provenance' THEN
    hit := EXISTS (SELECT 1 FROM quotes q WHERE q.id = (j->>'quote_id')::uuid AND q.customer_id::text = cust);
  ELSIF TG_TABLE_NAME = 'stock_reservations' THEN
    hit := j->>'res_type' = 'ORDER' AND EXISTS (SELECT 1 FROM orders o WHERE o.id = (j->>'source_ref_id')::uuid AND o.customer_id::text = cust);
  ELSIF TG_TABLE_NAME = 'audit_log' THEN
    hit := j->>'entity_type' = 'order' AND EXISTS (SELECT 1 FROM orders o WHERE o.id = (j->>'entity_id')::uuid AND o.customer_id::text = cust);
  ELSIF TG_TABLE_NAME = 'events_outbox' THEN
    hit := j->'payload'->>'customer_id' = cust AND j->>'topic' = TG_ARGV[1];
  END IF;
  IF hit THEN
    RAISE EXCEPTION 'qd5 injected fault after % on %', TG_OP, TG_TABLE_NAME;
  END IF;
  RETURN NEW;
END $f$`

var qd5FaultSeq int

// injectFaultAfter installs the fault trigger on table for op, scoped to q's customer and (for the
// two tables that need it) to a second argument: the subledger row type, or the outbox topic. The
// trigger is dropped with the test.
func (e *qd5Env) injectFaultAfter(q qd5Quote, table, op, arg string, deferred bool) {
	e.t.Helper()
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx, qd5Boom); err != nil {
		e.t.Fatalf("create the fault function: %v", err)
	}
	qd5FaultSeq++
	name := fmt.Sprintf("qd5_fault_%d", qd5FaultSeq)
	kind := "TRIGGER"
	timing := ""
	if deferred {
		// A deferred constraint trigger runs when the transaction commits, after every statement of the
		// action has been written: the last place a fault can land.
		kind, timing = "CONSTRAINT TRIGGER", " DEFERRABLE INITIALLY DEFERRED"
	}
	if _, err := e.pool.Exec(ctx, fmt.Sprintf(
		`CREATE %s %s AFTER %s ON %s%s FOR EACH ROW EXECUTE FUNCTION qd5_boom('%s', '%s')`,
		kind, name, op, table, timing, q.CustomerID, arg)); err != nil {
		e.t.Fatalf("install the fault on %s %s: %v", op, table, err)
	}
	e.t.Cleanup(func() {
		_, _ = e.pool.Exec(context.Background(), fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON %s`, name, table))
	})
}

// failingInvoicer wraps the real invoicer and fails right after a chosen call has read or written, the
// way a fault after the order facts were read (or after the preview) would.
type failingInvoicer struct {
	real          quote.OrderInvoicer
	afterAgreed   int // fail after the n-th AgreedInvoiceTx (1 based), 0 never
	agreedCalls   int
	afterRaisedTx bool
}

func (f *failingInvoicer) AgreedInvoiceTx(ctx context.Context, tx pgx.Tx, orderID string) (quote.AgreedInvoice, error) {
	out, err := f.real.AgreedInvoiceTx(ctx, tx, orderID)
	f.agreedCalls++
	if err == nil && f.afterAgreed == f.agreedCalls {
		return quote.AgreedInvoice{}, errors.New("qd5 injected fault after the order facts were read")
	}
	return out, err
}

func (f *failingInvoicer) RaiseAgreedInvoiceTx(ctx context.Context, tx pgx.Tx, orderID string) (quote.AgreedInvoice, error) {
	out, err := f.real.RaiseAgreedInvoiceTx(ctx, tx, orderID)
	if err == nil && f.afterRaisedTx {
		return quote.AgreedInvoice{}, errors.New("qd5 injected fault after the invoice was raised")
	}
	return out, err
}

// qd5Footprint is everything the action can leave: rows of every table it writes.
type qd5Footprint struct {
	Orders, OrderLines, Invoices, InvoiceLines, Payments, Allocations, Subledger, Outbox int
	Holds                                                                                map[string]int
	QuoteStatus                                                                          string
}

func (e *qd5Env) footprint(q qd5Quote) qd5Footprint {
	e.t.Helper()
	ctx := context.Background()
	var f qd5Footprint
	for _, r := range []struct {
		sql string
		arg string
		dst *int
	}{
		{`SELECT count(*) FROM orders WHERE quote_id = $1`, q.QuoteID, &f.Orders},
		{`SELECT count(*) FROM order_lines WHERE order_id IN (SELECT id FROM orders WHERE quote_id = $1)`, q.QuoteID, &f.OrderLines},
		{`SELECT count(*) FROM invoices WHERE customer_id = $1`, q.CustomerID, &f.Invoices},
		{`SELECT count(*) FROM invoice_lines WHERE invoice_id IN (SELECT id FROM invoices WHERE customer_id = $1)`, q.CustomerID, &f.InvoiceLines},
		{`SELECT count(*) FROM payments WHERE customer_id = $1`, q.CustomerID, &f.Payments},
		{`SELECT count(*) FROM payment_allocations WHERE invoice_id IN (SELECT id FROM invoices WHERE customer_id = $1)`, q.CustomerID, &f.Allocations},
		{`SELECT count(*) FROM customer_transactions WHERE customer_id = $1`, q.CustomerID, &f.Subledger},
		{`SELECT count(*) FROM events_outbox WHERE payload->>'customer_id' = $1 AND topic IN ('ar.invoice.finalized','payment.received')`, q.CustomerID, &f.Outbox},
	} {
		if err := e.pool.QueryRow(ctx, r.sql, r.arg).Scan(r.dst); err != nil {
			e.t.Fatalf("footprint: %v", err)
		}
	}
	f.Holds = map[string]int{}
	rows, err := e.pool.Query(ctx, `
		SELECT res_type || '/' || status, count(*)::int FROM stock_reservations
		 WHERE source_ref_id = $1::uuid OR source_ref_id IN (SELECT id FROM orders WHERE quote_id = $1)
		 GROUP BY 1`, q.QuoteID)
	if err != nil {
		e.t.Fatalf("footprint holds: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			e.t.Fatal(err)
		}
		f.Holds[k] = n
	}
	f.QuoteStatus = e.quoteStatus(q)
	return f
}

func sameHolds(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// seedSoftHold gives the quote its own soft hold the way a live quote has one: through the quote
// service's own hold sync (res_type QUOTE, source_type 'quote', raised by inventory's reserve), not a
// row shaped by hand, so the conversion finds and consumes it exactly as it does a real one. A fault
// must leave it exactly as it was.
func (e *qd5Env) seedSoftHold(q qd5Quote) {
	e.t.Helper()
	if n, err := e.svc.ResyncQuoteHolds(e.counter(), q.QuoteID); err != nil || n == 0 {
		e.t.Fatalf("the quote's own soft hold could not be raised (applied %d): %v", n, err)
	}
	var mode, source string
	if err := e.pool.QueryRow(context.Background(), `
		SELECT hold_mode, source_type FROM stock_reservations
		 WHERE source_ref_id = $1::uuid AND res_type = 'QUOTE' AND status = 'ACTIVE'`, q.QuoteID).Scan(&mode, &source); err != nil {
		e.t.Fatalf("read the quote's soft hold: %v", err)
	}
	if mode != "SOFT" || source != "quote" {
		e.t.Fatalf("the planted hold is shaped %s/%s, a real quote hold is SOFT/quote", mode, source)
	}
}

// qd5Fault is one injection point: where the fault lands, and which actions pass it (a conversion
// point is passed only by the cash customer's one action).
type qd5Fault struct {
	name       string
	table, op  string // a trigger fault, after this write
	deferred   bool   // the trigger is a deferred constraint trigger: it fires at commit
	arg        string
	afterFacts int  // a seam fault, after the n-th read of the agreed facts
	afterRaise bool // a seam fault, after the invoice was raised
	converts   bool // only the cash customer's action converts
}

var qd5Faults = []qd5Fault{
	{name: "convert: the order header", table: "orders", op: "INSERT", converts: true},
	{name: "convert: the order lines", table: "order_lines", op: "INSERT", converts: true},
	{name: "convert: the quote marked converted", table: "quotes", op: "UPDATE", converts: true},
	{name: "convert: the acceptance provenance", table: "quote_accept_provenance", op: "INSERT", converts: true},
	{name: "convert: the hard holds", table: "stock_reservations", op: "INSERT", converts: true},
	{name: "audit: the order's history row", table: "audit_log", op: "INSERT", converts: true},
	{name: "order facts: read for the preview", afterFacts: 1},
	{name: "invoice: after it was raised", afterRaise: true},
	{name: "invoice header", table: "invoices", op: "INSERT"},
	{name: "invoice lines", table: "invoice_lines", op: "INSERT"},
	{name: "invoice: the AR subledger row", table: "customer_transactions", op: "INSERT", arg: "INVOICE"},
	{name: "invoice: the finalized event", table: "events_outbox", op: "INSERT", arg: "ar.invoice.finalized"},
	{name: "receipt: the payment header", table: "payments", op: "INSERT"},
	{name: "receipt: the allocation", table: "payment_allocations", op: "INSERT"},
	{name: "balance: the invoice balance moved", table: "invoices", op: "UPDATE"},
	{name: "balance: the AR subledger payment row", table: "customer_transactions", op: "INSERT", arg: "PAYMENT"},
	{name: "receipt: the payment event", table: "events_outbox", op: "INSERT", arg: "payment.received"},
	{name: "commit: a deferred constraint raises when the transaction commits", table: "payments", op: "INSERT", deferred: true},
}

func (e *qd5Env) withFailingInvoicer(f qd5Fault) {
	if f.table != "" {
		return
	}
	inv, _ := quoteMoneyDoor(e.pool, e.orderSvc)
	e.svc.WithOrderInvoicer(&failingInvoicer{real: inv, afterAgreed: f.afterFacts, afterRaisedTx: f.afterRaise})
}

func (e *qd5Env) injectFault(q qd5Quote, f qd5Fault) {
	e.t.Helper()
	if f.table != "" {
		e.injectFaultAfter(q, f.table, f.op, f.arg, f.deferred)
		return
	}
	e.withFailingInvoicer(f)
}

func (e *qd5Env) restoreInvoicer() {
	inv, _ := quoteMoneyDoor(e.pool, e.orderSvc)
	e.svc.WithOrderInvoicer(inv)
}

// TestQD5OneTx_AFaultAfterEachWriteOfTheCashCustomersActionLeavesNothingBehind: the cash customer's
// convert, invoice and payment is one action. A fault injected after each write, before the commit,
// leaves no order, no invoice, no balance, no payment and no event, the quote ACCEPTED and its soft
// hold as it was; and the same request repeated afterwards does the whole action once.
func TestQD5OneTx_AFaultAfterEachWriteOfTheCashCustomersActionLeavesNothingBehind(t *testing.T) {
	for _, f := range qd5Faults {
		t.Run(f.name, func(t *testing.T) {
			e := qd5SetupSized(t, 0)
			e.stockFor(qd5Trenton)
			q := e.seedQuote(0, 0, qd5Trenton)
			e.seedSoftHold(q)
			before := e.footprint(q)
			e.injectFault(q, f)

			_, err := e.svc.TakeOrderPayment(e.counter(), q.QuoteID, pay("CASH", q.Total, "k-fault"))
			if err == nil || !strings.Contains(err.Error(), "injected fault") {
				t.Fatalf("the fault must reach the caller as the action's failure, got %v", err)
			}
			if before.Holds["QUOTE/ACTIVE"] == 0 {
				t.Fatalf("the quote's own soft hold must be there before the fault: %v", before.Holds)
			}
			after := e.footprint(q)
			if after.Orders != 0 || after.OrderLines != 0 || after.Invoices != 0 || after.InvoiceLines != 0 ||
				after.Payments != 0 || after.Allocations != 0 || after.Subledger != 0 || after.Outbox != 0 {
				t.Fatalf("a fault left rows behind: %+v", after)
			}
			if after.QuoteStatus != "ACCEPTED" || !sameHolds(before.Holds, after.Holds) {
				t.Fatalf("the quote and its holds must be untouched: status %s holds %v (before %v)",
					after.QuoteStatus, after.Holds, before.Holds)
			}

			// The repeat, with the fault gone, is the whole action, once.
			e.restoreInvoicer()
			e.dropFaults()
			out, err := e.svc.TakeOrderPayment(e.counter(), q.QuoteID, pay("CASH", q.Total, "k-fault"))
			if err != nil {
				t.Fatalf("the repeat: %v", err)
			}
			if out.Replayed || !out.Converted || out.BalanceCents != 0 {
				t.Fatalf("the repeat is the first success, not a replay: %+v", out)
			}
			if c := e.counts(q); c != (qd5Counts{Orders: 1, Invoices: 1, Payments: 1}) {
				t.Fatalf("after the repeat: %+v", c)
			}
			// The successful repeat consumes the quote's soft hold and raises the order's hard ones.
			if h := e.footprint(q).Holds; h["QUOTE/ACTIVE"] != 0 || h["ORDER/ACTIVE"] == 0 {
				t.Fatalf("the repeat must consume the quote's soft hold and hold the order's stock: %v", h)
			}
		})
	}
}

// TestQD5OneTx_AFaultAfterEachWriteOfTheCreditCustomersPaymentLeavesTheConvertedOrderAsItWas: the credit
// customer's order is already converted and its hard holds are active. A fault after each write of the
// invoice and the payment leaves no invoice, no balance, no payment, the order and its holds as they were.
func TestQD5OneTx_AFaultAfterEachWriteOfTheCreditCustomersPaymentLeavesTheConvertedOrderAsItWas(t *testing.T) {
	for _, f := range qd5Faults {
		if f.converts {
			continue
		}
		t.Run(f.name, func(t *testing.T) {
			e := qd5SetupSized(t, 0)
			e.stockFor(qd5Trenton)
			q := e.seedQuote(1_000_000, 0, qd5Trenton)
			e.seedSoftHold(q)
			e.convert(q)
			before := e.footprint(q)
			e.injectFault(q, f)

			_, err := e.svc.TakeOrderPayment(e.counter(), q.QuoteID, pay("CASH", 1000, "k-fault"))
			if err == nil || !strings.Contains(err.Error(), "injected fault") {
				t.Fatalf("the fault must reach the caller as the action's failure, got %v", err)
			}
			after := e.footprint(q)
			if after.Invoices != 0 || after.InvoiceLines != 0 || after.Payments != 0 || after.Allocations != 0 ||
				after.Subledger != 0 || after.Outbox != 0 {
				t.Fatalf("a fault left rows behind: %+v", after)
			}
			if after.Orders != 1 || after.QuoteStatus != "CONVERTED" || !sameHolds(before.Holds, after.Holds) {
				t.Fatalf("the converted order and its holds must be as they were: %+v (before %+v)", after, before)
			}

			e.restoreInvoicer()
			e.dropFaults()
			out, err := e.svc.TakeOrderPayment(e.counter(), q.QuoteID, pay("CASH", 1000, "k-fault"))
			if err != nil || out.Replayed || out.BalanceCents != q.Total-1000 {
				t.Fatalf("the repeat: %+v %v", out, err)
			}
		})
	}
}

// dropFaults removes every fault trigger so the repeat runs clean.
func (e *qd5Env) dropFaults() {
	e.t.Helper()
	rows, err := e.pool.Query(context.Background(),
		`SELECT tgname, c.relname FROM pg_trigger t JOIN pg_class c ON c.oid = t.tgrelid WHERE tgname LIKE 'qd5_fault_%'`)
	if err != nil {
		e.t.Fatal(err)
	}
	type trig struct{ name, table string }
	var all []trig
	for rows.Next() {
		var x trig
		if err := rows.Scan(&x.name, &x.table); err != nil {
			rows.Close()
			e.t.Fatal(err)
		}
		all = append(all, x)
	}
	rows.Close()
	for _, x := range all {
		if _, err := e.pool.Exec(context.Background(), fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON %s`, x.name, x.table)); err != nil {
			e.t.Fatal(err)
		}
	}
}

// - P1-3: one connection per call, whatever the pool -

// parallel runs n calls at once and reports whether they all finished within the limit.
func parallel(t *testing.T, n int, limit time.Duration, call func(i int) error) []error {
	t.Helper()
	errs := make([]error, n)
	var wg sync.WaitGroup
	done := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = call(i)
		}(i)
	}
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(limit):
		t.Fatalf("%d parallel calls did not finish within %s: the pool is starved", n, limit)
	}
	return errs
}

// TestQD5OneTx_EightParallelCallsOnAPoolOfFourOrOneComplete: the reviewer's probe, with the pool size set
// explicitly so it fails on any CPU count. Eight calls with one key on one quote, eight different
// quotes, and eight different keys on one quote, on a pool of four and on a pool of ONE connection: a
// call that needed a second connection would starve here, so each call holds exactly one.
func TestQD5OneTx_EightParallelCallsOnAPoolOfFourOrOneComplete(t *testing.T) {
	for _, size := range []int32{4, 1} {
		t.Run(fmt.Sprintf("pool %d, one key on one quote", size), func(t *testing.T) {
			e, setup := qd5SetupSized(t, size), qd5SetupSized(t, 0)
			q := setup.seedQuote(1_000_000, 0, qd5Trenton)
			setup.convert(q) // the plain conversion door needs a second connection for its hold lookup: not the action under test
			errs := parallel(t, 8, 40*time.Second, func(int) error {
				_, err := e.svc.TakeOrderPayment(e.counter(), q.QuoteID, pay("CASH", 1000, "k-one"))
				return err
			})
			for i, err := range errs {
				if err != nil {
					t.Fatalf("call %d: %v", i, err)
				}
			}
			if c := e.counts(q); c.Payments != 1 || c.Invoices != 1 {
				t.Fatalf("eight same key calls left %+v, want one payment and one invoice", c)
			}
		})
		t.Run(fmt.Sprintf("pool %d, eight different quotes", size), func(t *testing.T) {
			e, setup := qd5SetupWith(t, size, size > 1), qd5SetupSized(t, 0)
			quotes := make([]qd5Quote, 8)
			for i := range quotes {
				quotes[i] = setup.seedQuote(0, 0, qd5Trenton)
			}
			errs := parallel(t, 8, 40*time.Second, func(i int) error {
				_, err := e.svc.TakeOrderPayment(e.counter(), quotes[i].QuoteID, pay("CASH", quotes[i].Total, "k-many"))
				return err
			})
			for i, err := range errs {
				if err != nil {
					t.Fatalf("call %d: %v", i, err)
				}
			}
			for i, q := range quotes {
				if c := e.counts(q); c != (qd5Counts{Orders: 1, Invoices: 1, Payments: 1}) {
					t.Fatalf("quote %d left %+v", i, c)
				}
			}
		})
		t.Run(fmt.Sprintf("pool %d, eight keys on one quote", size), func(t *testing.T) {
			e, setup := qd5SetupSized(t, size), qd5SetupSized(t, 0)
			q := setup.seedQuote(1_000_000, 0, qd5Trenton)
			setup.convert(q) // the plain conversion door needs a second connection for its hold lookup: not the action under test
			errs := parallel(t, 8, 40*time.Second, func(i int) error {
				_, err := e.svc.TakeOrderPayment(e.counter(), q.QuoteID, pay("CASH", 100, fmt.Sprintf("k-key-%d", i)))
				return err
			})
			for i, err := range errs {
				if err != nil {
					t.Fatalf("call %d: %v", i, err)
				}
			}
			var bal int64
			_ = e.pool.QueryRow(context.Background(), `SELECT balance_cents FROM invoices WHERE customer_id = $1`, q.CustomerID).Scan(&bal)
			if c := e.counts(q); c.Payments != 8 || c.Invoices != 1 || bal != q.Total-800 {
				t.Fatalf("eight keys left %+v balance %d, want 8 payments, one invoice, balance %d", c, bal, q.Total-800)
			}
		})
	}
}
