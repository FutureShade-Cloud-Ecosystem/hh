//go:build integration

// qd5_order_payment_integration_test.go, QD-5: the counter's payment on a converted quote's order,
// proven on real rows through the real modules and the composition root's own adapters
// (quoteMoneyDoor), not doubles: the quote service converts, the order module answers the agreed
// facts, billing's writer raises the document, payments' settle records the money.
//
//	Run: DATABASE_URL='postgres://.../scratch?sslmode=disable' \
//	     go test -tags integration ./cmd/core/ -run QD5 -count=1 -p 1
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

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/quote"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
)

const (
	qd5Trenton  = "5d1b3f6e-0000-4000-9000-000000000001"
	qd5Kingston = "5d1b3f6e-0000-4000-9000-000000000002"
)

type qd5Env struct {
	t        *testing.T
	pool     *pgxpool.Pool
	svc      *quote.StaffService
	orderSvc *order.Service
	userID   string
	payer    *qd5FlakyPayer
}

// qd5FlakyPayer wraps the real payer so a test can make the settle fail once, the infrastructure
// fault between two commits.
type qd5FlakyPayer struct {
	real quote.CounterPayer
	mu   sync.Mutex
	fail bool
}

func (p *qd5FlakyPayer) TakePayment(ctx context.Context, in quote.CounterReceiptInput) (quote.CounterReceipt, error) {
	p.mu.Lock()
	f := p.fail
	p.mu.Unlock()
	if f {
		return quote.CounterReceipt{}, errors.New("settle: connection reset")
	}
	return p.real.TakePayment(ctx, in)
}

func (p *qd5FlakyPayer) failNext(v bool) { p.mu.Lock(); p.fail = v; p.mu.Unlock() }

func qd5Setup(t *testing.T) *qd5Env {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL is not set; the QD-5 money path needs the scratch database")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	var userID string
	if err := pool.QueryRow(context.Background(),
		`SELECT id::text FROM users WHERE status='ACTIVE' ORDER BY created_at LIMIT 1`).Scan(&userID); err != nil {
		t.Fatalf("no seeded staff: %v", err)
	}
	orderSvc := order.NewService(order.NewRepository(pool))
	repo := quote.NewRepository(pool).WithCreditGate(till.NewHoldAwareCreditGate(till.NewRepository(pool)))
	aud := audit.NewService(audit.NewRepository(pool), nil)
	inv, realPayer := quoteMoneyDoor(pool, orderSvc)
	flaky := &qd5FlakyPayer{real: realPayer}
	svc := quote.NewStaffService(repo).WithAudit(aud).WithAuditTrail(aud).
		WithOrderInvoicer(inv).WithCounterPayer(flaky)
	return &qd5Env{t: t, pool: pool, svc: svc, orderSvc: orderSvc, userID: userID, payer: flaky}
}

func (e *qd5Env) as(role, branch string) context.Context {
	return identity.ContextWithStaff(context.Background(), identity.StaffContext{
		UserID: e.userID, RoleCode: role, Tier: identity.TierFrontline, Scope: identity.ScopeBranch,
		Status: identity.StatusActive, Branches: map[string]bool{branch: true},
	})
}

func (e *qd5Env) counter() context.Context { return e.as("INSIDE_SALES", qd5Trenton) }

// qd5Quote is one seeded ACCEPTED quote: a discounted line (3 at 10.00 less 10 percent) and a plain
// line (2 at 10.00), with an optional delivery fee taxed with the lines at 13 percent half up.
type qd5Quote struct {
	QuoteID, QuoteNo, CustomerID string
	Subtotal, Fee, Tax, Total    int64
}

func (e *qd5Env) seedQuote(creditLimit, fee int64, branch string) qd5Quote {
	e.t.Helper()
	ctx := context.Background()
	var prod, uom string
	if err := e.pool.QueryRow(ctx, `
		SELECT p.id::text, pu.uom_code::text FROM products p
		  JOIN product_uoms pu ON pu.product_id = p.id AND pu.qty_in_base = 1
		 WHERE p.is_active ORDER BY p.sku LIMIT 1`).Scan(&prod, &uom); err != nil {
		e.t.Skipf("no factor-1 anchored product seeded: %v", err)
	}
	tag := fmt.Sprintf("QD5-%d", time.Now().UnixNano())
	var q qd5Quote
	if err := e.pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name, customer_type, credit_limit_cents) VALUES ($1,$3,'CONTRACTOR',$2) RETURNING id::text`,
		tag, creditLimit, tag).Scan(&q.CustomerID); err != nil {
		e.t.Fatalf("seed customer: %v", err)
	}
	q.Subtotal = 2700 + 2000
	q.Fee = fee
	q.Tax = ((q.Subtotal+fee)*1300 + 5000) / 10000
	q.Total = q.Subtotal + fee + q.Tax
	fulfil := "PICKUP"
	if fee > 0 {
		fulfil = "DELIVERY"
	}
	q.QuoteNo = "#Q-" + tag
	if err := e.pool.QueryRow(ctx, `
		INSERT INTO quotes (quote_no, customer_id, branch_id, status, subtotal_cents, tax_cents, total_cents,
		                    delivery_fee_cents, fulfillment, created_by)
		VALUES ($1, $2::uuid, $3::uuid, 'ACCEPTED', $4, $5, $6, $7, $8, $9::uuid) RETURNING id::text`,
		q.QuoteNo, q.CustomerID, branch, q.Subtotal, q.Tax, q.Total, fee, fulfil, e.userID).Scan(&q.QuoteID); err != nil {
		e.t.Fatalf("seed quote: %v", err)
	}
	if _, err := e.pool.Exec(ctx, `
		INSERT INTO quote_lines (quote_id, line_no, product_id, description, qty_entered, uom_entered, qty_base,
		                         unit_price_cents, price_uom, line_total_cents, disc_pct, pre_discount_total_cents)
		VALUES ($1::uuid, 1, $2::uuid, 'discounted line', 3, $3, 3, 1000, $3, 2700, 10, 3000),
		       ($1::uuid, 2, $2::uuid, 'plain line', 2, $3, 2, 1000, $3, 2000, 0, 0)`,
		q.QuoteID, prod, uom); err != nil {
		e.t.Fatalf("seed quote lines: %v", err)
	}
	e.t.Cleanup(func() { e.cleanup(q) })
	return q
}

func (e *qd5Env) cleanup(q qd5Quote) {
	c := context.Background()
	orders := `SELECT id FROM orders WHERE quote_id = $1`
	invoices := `SELECT id FROM invoices WHERE customer_id = $1`
	for _, s := range []string{
		`DELETE FROM payment_allocations WHERE invoice_id IN (` + invoices + `)`,
		`DELETE FROM payments WHERE customer_id = $1`,
		`DELETE FROM customer_transactions WHERE customer_id = $1`,
		`DELETE FROM invoice_lines WHERE invoice_id IN (` + invoices + `)`,
		`DELETE FROM invoices WHERE customer_id = $1`,
		`UPDATE shipment_groups SET invoice_id = NULL WHERE order_id IN (` + orders + `)`,
	} {
		arg := q.CustomerID
		if strings.Contains(s, "shipment_groups") {
			arg = q.QuoteID
		}
		_, _ = e.pool.Exec(c, s, arg)
	}
	for _, s := range []string{
		`DELETE FROM shipment_group_lines WHERE shipment_group_id IN (SELECT id FROM shipment_groups WHERE order_id IN (` + orders + `))`,
		`DELETE FROM shipment_groups WHERE order_id IN (` + orders + `)`,
		`DELETE FROM audit_log WHERE entity_id IN (` + orders + `)`,
		`DELETE FROM quote_accept_provenance WHERE quote_id = $1`,
		`DELETE FROM order_lines WHERE order_id IN (` + orders + `)`,
		`DELETE FROM orders WHERE quote_id = $1`,
		`DELETE FROM audit_log WHERE entity_id = $1`,
		`DELETE FROM quote_lines WHERE quote_id = $1`,
		`DELETE FROM quotes WHERE id = $1`,
	} {
		_, _ = e.pool.Exec(c, s, q.QuoteID)
	}
	_, _ = e.pool.Exec(c, `DELETE FROM customers WHERE id = $1`, q.CustomerID)
}

// - reads straight off the rows -

type qd5Counts struct{ Orders, Invoices, Payments int }

func (e *qd5Env) counts(q qd5Quote) qd5Counts {
	e.t.Helper()
	var c qd5Counts
	ctx := context.Background()
	for _, r := range []struct {
		sql string
		arg string
		dst *int
	}{
		{`SELECT count(*) FROM orders WHERE quote_id = $1`, q.QuoteID, &c.Orders},
		{`SELECT count(*) FROM invoices WHERE customer_id = $1`, q.CustomerID, &c.Invoices},
		{`SELECT count(*) FROM payments WHERE customer_id = $1`, q.CustomerID, &c.Payments},
	} {
		if err := e.pool.QueryRow(ctx, r.sql, r.arg).Scan(r.dst); err != nil {
			e.t.Fatalf("count: %v", err)
		}
	}
	return c
}

func (e *qd5Env) quoteStatus(q qd5Quote) string {
	var s string
	if err := e.pool.QueryRow(context.Background(), `SELECT status FROM quotes WHERE id = $1`, q.QuoteID).Scan(&s); err != nil {
		e.t.Fatalf("quote status: %v", err)
	}
	return s
}

func (e *qd5Env) convert(q qd5Quote) quote.ConvertResult {
	e.t.Helper()
	res, err := e.svc.ConvertQuote(e.counter(), q.QuoteID, quote.ConvertOptions{})
	if err != nil {
		e.t.Fatalf("convert: %v", err)
	}
	return res
}

func pay(method string, cents int64, key string) quote.OrderPaymentInput {
	return quote.OrderPaymentInput{Method: method, AmountCents: cents, Reference: "till 1", IdempotencyKey: key}
}

func wantBadRequest(t *testing.T, what string, err error) {
	t.Helper()
	var br httpx.BadRequestError
	if !errors.As(err, &br) {
		t.Fatalf("%s: want a 400 refusal, got %T: %v", what, err, err)
	}
}

// - 1: an accepted quote with a discounted line converts, the order equals the quote, the discount shows -

func TestQD5_DiscountedLineConvertsAtTheQuoteTotalsAndShowsTheDiscount(t *testing.T) {
	e := qd5Setup(t)
	q := e.seedQuote(1_000_000, 0, qd5Trenton)
	res := e.convert(q)

	var sub, tax, total int64
	if err := e.pool.QueryRow(context.Background(),
		`SELECT subtotal_cents, tax_cents, total_cents FROM orders WHERE id = $1`, res.OrderID).Scan(&sub, &tax, &total); err != nil {
		t.Fatal(err)
	}
	if sub != q.Subtotal || tax != q.Tax || total != q.Total {
		t.Fatalf("order %d/%d/%d, quote %d/%d/%d: the order must equal the quote", sub, tax, total, q.Subtotal, q.Tax, q.Total)
	}
	var desc string
	var unit, lineTotal int64
	if err := e.pool.QueryRow(context.Background(),
		`SELECT description, unit_price_cents, line_total_cents FROM order_lines WHERE order_id = $1 AND line_no = 1`,
		res.OrderID).Scan(&desc, &unit, &lineTotal); err != nil {
		t.Fatal(err)
	}
	if lineTotal != 2700 {
		t.Fatalf("the discounted line's total moved: %d, want 2700", lineTotal)
	}
	if !strings.Contains(desc, "10% off") {
		t.Fatalf("a discounted line must show its discount, description %q", desc)
	}
	var plain string
	if err := e.pool.QueryRow(context.Background(),
		`SELECT description FROM order_lines WHERE order_id = $1 AND line_no = 2`, res.OrderID).Scan(&plain); err != nil {
		t.Fatal(err)
	}
	if plain != "plain line" {
		t.Fatalf("an undiscounted line must carry its description unchanged, got %q", plain)
	}
	// The same words reach the invoice line, through the order's own facts.
	if _, err := e.svc.TakeOrderPayment(e.counter(), q.QuoteID, pay("CASH", q.Total, "k-disc")); err != nil {
		t.Fatalf("take payment: %v", err)
	}
	var invDesc string
	var invLineTotal int64
	if err := e.pool.QueryRow(context.Background(), `
		SELECT il.description, il.line_total_cents FROM invoice_lines il JOIN invoices i ON i.id = il.invoice_id
		 WHERE i.customer_id = $1 AND il.line_no = 1`, q.CustomerID).Scan(&invDesc, &invLineTotal); err != nil {
		t.Fatal(err)
	}
	if invDesc != desc || invLineTotal != 2700 {
		t.Fatalf("the invoice line must say what the order line says: %q/%d vs %q", invDesc, invLineTotal, desc)
	}
}

// - 2: the invoice equals the order (pickup) and the quote's agreed total (delivery) -

func TestQD5_PickupInvoiceTotalEqualsTheOrderTotal(t *testing.T) {
	e := qd5Setup(t)
	q := e.seedQuote(1_000_000, 0, qd5Trenton)
	e.convert(q)
	out, err := e.svc.TakeOrderPayment(e.counter(), q.QuoteID, pay("CASH", q.Total, "k-pickup"))
	if err != nil {
		t.Fatalf("take payment: %v", err)
	}
	var orderTotal, invTotal, invSub, invTax int64
	if err := e.pool.QueryRow(context.Background(),
		`SELECT total_cents FROM orders WHERE quote_id = $1`, q.QuoteID).Scan(&orderTotal); err != nil {
		t.Fatal(err)
	}
	if err := e.pool.QueryRow(context.Background(),
		`SELECT total_cents, subtotal_cents, tax_cents FROM invoices WHERE customer_id = $1`, q.CustomerID).Scan(&invTotal, &invSub, &invTax); err != nil {
		t.Fatal(err)
	}
	if invTotal != orderTotal || invTotal != q.Total || invSub != q.Subtotal || invTax != q.Tax {
		t.Fatalf("invoice %d/%d/%d, order total %d, quote %d/%d/%d", invTotal, invSub, invTax, orderTotal, q.Subtotal, q.Tax, q.Total)
	}
	if out.InvoiceTotalCents != q.Total || out.AmountCents != q.Total || out.BalanceCents != 0 {
		t.Fatalf("the answer must carry the agreed figures and a zero balance: %+v", out)
	}
	if out.InvoiceNo == "" || out.PaymentNo == "" || out.OrderNo == "" || out.Method != "CASH" || out.ReceivedAt.IsZero() {
		t.Fatalf("the receipt needs the invoice, payment and order numbers, the tender and the time: %+v", out)
	}
	var by string
	if err := e.pool.QueryRow(context.Background(),
		`SELECT COALESCE(received_by::text,'') FROM payments WHERE customer_id = $1`, q.CustomerID).Scan(&by); err != nil {
		t.Fatal(err)
	}
	if by != e.userID {
		t.Fatalf("the caller must be stamped as the receiver, got %q want %q", by, e.userID)
	}
	var status string
	_ = e.pool.QueryRow(context.Background(), `SELECT status FROM invoices WHERE customer_id = $1`, q.CustomerID).Scan(&status)
	if status != "PAID" {
		t.Fatalf("a fully paid invoice is PAID, got %q", status)
	}
}

func TestQD5_DeliveryInvoiceTotalEqualsTheQuotesAgreedTotalWithFeeAndTax(t *testing.T) {
	e := qd5Setup(t)
	q := e.seedQuote(1_000_000, 5_000, qd5Trenton)
	e.convert(q)
	out, err := e.svc.TakeOrderPayment(e.counter(), q.QuoteID, pay("CARD", q.Total, "k-delivery"))
	if err != nil {
		t.Fatalf("take payment: %v", err)
	}
	var invTotal, invSub, invTax int64
	if err := e.pool.QueryRow(context.Background(),
		`SELECT total_cents, subtotal_cents, tax_cents FROM invoices WHERE customer_id = $1`, q.CustomerID).Scan(&invTotal, &invSub, &invTax); err != nil {
		t.Fatal(err)
	}
	if invTotal != q.Total || invTax != q.Tax || invSub != q.Subtotal+q.Fee {
		t.Fatalf("the delivery invoice must equal the quote's agreed total: invoice %d (sub %d tax %d), quote total %d (sub+fee %d tax %d)",
			invTotal, invSub, invTax, q.Total, q.Subtotal+q.Fee, q.Tax)
	}
	var freight, lines int64
	_ = e.pool.QueryRow(context.Background(), `
		SELECT COALESCE(SUM(il.line_total_cents) FILTER (WHERE il.order_line_id IS NULL),0), COUNT(*)
		  FROM invoice_lines il JOIN invoices i ON i.id = il.invoice_id WHERE i.customer_id = $1`, q.CustomerID).Scan(&freight, &lines)
	if freight != q.Fee || lines != 3 {
		t.Fatalf("the fee must be its own line: freight line %d (want %d), %d lines (want 3)", freight, q.Fee, lines)
	}
	var sumLineTax int64
	_ = e.pool.QueryRow(context.Background(), `
		SELECT COALESCE(SUM(il.tax_cents),0) FROM invoice_lines il JOIN invoices i ON i.id = il.invoice_id WHERE i.customer_id = $1`,
		q.CustomerID).Scan(&sumLineTax)
	if sumLineTax != q.Tax {
		t.Fatalf("the line taxes must sum to the header tax: %d vs %d", sumLineTax, q.Tax)
	}
	if out.InvoiceTotalCents != q.Total || out.BalanceCents != 0 {
		t.Fatalf("answer %+v", out)
	}
}

// - 3 and 9: replay posts nothing, the numbers never move, also under concurrency -

func TestQD5_ReplayPostsNothingAndNothingMoves(t *testing.T) {
	e := qd5Setup(t)
	q := e.seedQuote(1_000_000, 0, qd5Trenton)
	e.convert(q)
	first, err := e.svc.TakeOrderPayment(e.counter(), q.QuoteID, pay("CASH", 1000, "k-replay"))
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	before := e.counts(q)
	// Another customer's invoice raised in between must not move this one's numbers.
	other := e.seedQuote(1_000_000, 0, qd5Trenton)
	e.convert(other)
	if _, err := e.svc.TakeOrderPayment(e.counter(), other.QuoteID, pay("CASH", 500, "k-other")); err != nil {
		t.Fatalf("other: %v", err)
	}
	second, err := e.svc.TakeOrderPayment(e.counter(), q.QuoteID, pay("CASH", 1000, "k-replay"))
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !second.Replayed || first.Replayed {
		t.Fatalf("replayed flags: first %v second %v", first.Replayed, second.Replayed)
	}
	if second.PaymentNo != first.PaymentNo || second.PaymentID != first.PaymentID ||
		second.InvoiceNo != first.InvoiceNo || second.InvoiceTotalCents != first.InvoiceTotalCents ||
		second.AmountCents != first.AmountCents || second.BalanceCents != first.BalanceCents {
		t.Fatalf("a replay answers the first result: first %+v, replay %+v", first, second)
	}
	if after := e.counts(q); after != before {
		t.Fatalf("a replay posted something: before %+v after %+v", before, after)
	}
	var bal int64
	_ = e.pool.QueryRow(context.Background(), `SELECT balance_cents FROM invoices WHERE customer_id = $1`, q.CustomerID).Scan(&bal)
	if bal != q.Total-1000 {
		t.Fatalf("the balance moved on a replay: %d, want %d", bal, q.Total-1000)
	}
}

func TestQD5_SameKeyDifferentPaymentIsRefusedAndAnotherQuoteKeepsItsOwn(t *testing.T) {
	e := qd5Setup(t)
	q := e.seedQuote(1_000_000, 0, qd5Trenton)
	e.convert(q)
	if _, err := e.svc.TakeOrderPayment(e.counter(), q.QuoteID, pay("CASH", 1000, "k-same")); err != nil {
		t.Fatal(err)
	}
	for _, in := range []quote.OrderPaymentInput{pay("CASH", 2000, "k-same"), pay("CARD", 1000, "k-same")} {
		_, err := e.svc.TakeOrderPayment(e.counter(), q.QuoteID, in)
		var c *quote.OrderPaymentConflictError
		if !errors.As(err, &c) {
			t.Fatalf("the same key for a different payment must be a 409, got %v", err)
		}
	}
	if c := e.counts(q); c.Payments != 1 {
		t.Fatalf("payments %d, want 1", c.Payments)
	}
	// The same key on another quote is that quote's own payment, never the first quote's replayed.
	o := e.seedQuote(1_000_000, 0, qd5Trenton)
	e.convert(o)
	out, err := e.svc.TakeOrderPayment(e.counter(), o.QuoteID, pay("CASH", 1000, "k-same"))
	if err != nil {
		t.Fatalf("same key, another quote: %v", err)
	}
	if out.Replayed {
		t.Fatal("a key must never replay another quote's payment")
	}
	if c := e.counts(o); c.Payments != 1 || c.Invoices != 1 {
		t.Fatalf("the other quote's own payment: %+v", c)
	}
}

func TestQD5_EightParallelCallsWithOneKeyLeaveOnePayment(t *testing.T) {
	e := qd5Setup(t)
	q := e.seedQuote(1_000_000, 0, qd5Trenton)
	e.convert(q)
	var wg sync.WaitGroup
	results := make([]quote.OrderPaymentResult, 8)
	errs := make([]error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = e.svc.TakeOrderPayment(e.counter(), q.QuoteID, pay("CASH", q.Total, "k-parallel"))
		}(i)
	}
	wg.Wait()
	replays := 0
	for i := range results {
		if errs[i] != nil {
			t.Fatalf("call %d: %v", i, errs[i])
		}
		if results[i].PaymentNo != results[0].PaymentNo || results[i].InvoiceNo != results[0].InvoiceNo {
			t.Fatalf("call %d answered a different document: %+v vs %+v", i, results[i], results[0])
		}
		if results[i].Replayed {
			replays++
		}
	}
	if replays != 7 {
		t.Fatalf("exactly one call does the work and seven replay it, got %d replays", replays)
	}
	if c := e.counts(q); c.Payments != 1 || c.Invoices != 1 {
		t.Fatalf("eight parallel same key calls left %+v, want one payment and one invoice", c)
	}
	var allocs int
	_ = e.pool.QueryRow(context.Background(), `
		SELECT count(*) FROM payment_allocations pa JOIN payments p ON p.id = pa.payment_id WHERE p.customer_id = $1`,
		q.CustomerID).Scan(&allocs)
	if allocs != 1 {
		t.Fatalf("allocations %d, want 1", allocs)
	}
}

// - 4: the role rule at the service: branch reach, and the conversion half's own rule (row 14) -

func TestQD5_CounterRolesAdmittedAtTheirOwnBranchAndRefusedAtAnothers(t *testing.T) {
	e := qd5Setup(t)
	for _, role := range []string{"INSIDE_SALES", "SALES_MANAGER", "YARD_MANAGER", "BRANCH_MANAGER"} {
		t.Run(role, func(t *testing.T) {
			q := e.seedQuote(1_000_000, 0, qd5Trenton)
			e.convert(q)
			// Another branch's caller: refused, nothing raised.
			_, err := e.svc.TakeOrderPayment(e.as(role, qd5Kingston), q.QuoteID, pay("CASH", 1000, "k-"+role+"-away"))
			if !errors.Is(err, quote.ErrNotFound) && !errors.Is(err, quote.ErrForbidden) {
				t.Fatalf("%s at another branch must be refused, got %v", role, err)
			}
			if c := e.counts(q); c.Invoices != 0 || c.Payments != 0 {
				t.Fatalf("a refused caller raised something: %+v", c)
			}
			// Its own branch: admitted, and stamped as the receiver.
			if _, err := e.svc.TakeOrderPayment(e.as(role, qd5Trenton), q.QuoteID, pay("CASH", 1000, "k-"+role)); err != nil {
				t.Fatalf("%s at its own branch: %v", role, err)
			}
		})
	}
}

func TestQD5_Row14_YardManagerCannotConvertEitherWay(t *testing.T) {
	e := qd5Setup(t)
	yard := e.as("YARD_MANAGER", qd5Trenton)
	q := e.seedQuote(1_000_000, 0, qd5Trenton)
	var ns *quote.NotSellingRoleError
	if _, err := e.svc.ConvertQuote(yard, q.QuoteID, quote.ConvertOptions{}); !errors.As(err, &ns) {
		t.Fatalf("row 14: the Yard manager converting must be refused naming the role, got %v", err)
	}
	// The cash customer's one action converts too, so it keeps the same rule: nothing is written.
	cash := e.seedQuote(0, 0, qd5Trenton)
	if _, err := e.svc.TakeOrderPayment(yard, cash.QuoteID, pay("CASH", cash.Total, "k-yard-cash")); !errors.As(err, &ns) {
		t.Fatalf("the Yard manager must not convert through the payment door either, got %v", err)
	}
	if c := e.counts(cash); c != (qd5Counts{}) || e.quoteStatus(cash) != "ACCEPTED" {
		t.Fatalf("a refused cash action wrote rows: %+v status %s", c, e.quoteStatus(cash))
	}
}

// - 5: fulfilling afterwards, and a later shipment plan, bill nothing twice -

func TestQD5_FulfilmentAndAShipmentPlanBillNothingTwice(t *testing.T) {
	e := qd5Setup(t)
	q := e.seedQuote(1_000_000, 0, qd5Trenton)
	res := e.convert(q)
	out, err := e.svc.TakeOrderPayment(e.counter(), q.QuoteID, pay("CASH", q.Total, "k-fulfil"))
	if err != nil {
		t.Fatal(err)
	}
	tail := billing.NewInvoiceWriter(e.pool).WithOrderSource(w6cOrderInvoiceSource{svc: e.orderSvc})

	// The order leaves the yard: its lines are fulfilled and the fulfilment path bills the order.
	if _, err := e.pool.Exec(context.Background(),
		`UPDATE order_lines SET fulfilled_base = qty_base WHERE order_id = $1`, res.OrderID); err != nil {
		t.Fatal(err)
	}
	doc, err := tail.InvoiceOrder(context.Background(), res.OrderID)
	if err != nil {
		t.Fatalf("billing the fulfilled order: %v", err)
	}
	if !doc.Reused || doc.InvoiceNo != out.InvoiceNo || doc.TotalCents != q.Total {
		t.Fatalf("fulfilment must return the counter's document, got %+v (counter %s)", doc, out.InvoiceNo)
	}
	if c := e.counts(q); c.Invoices != 1 {
		t.Fatalf("invoices after fulfilment: %d, want 1", c.Invoices)
	}

	// A shipment plan gained later: the plan's own group key bills nothing either.
	var groupID string
	if err := e.pool.QueryRow(context.Background(),
		`INSERT INTO shipment_groups (order_id, group_no, status) VALUES ($1, 1, 'COMPLETE') RETURNING id::text`,
		res.OrderID).Scan(&groupID); err != nil {
		t.Fatalf("seed shipment group: %v", err)
	}
	if _, err := e.pool.Exec(context.Background(), `
		INSERT INTO shipment_group_lines (shipment_group_id, order_line_id, qty_base)
		SELECT $1::uuid, id, qty_base FROM order_lines WHERE order_id = $2`, groupID, res.OrderID); err != nil {
		t.Fatalf("seed shipment group lines: %v", err)
	}
	gdoc, err := tail.InvoiceOrderGroups(context.Background(), res.OrderID, []string{groupID}, "ON_ALL_COMPLETE")
	if err != nil {
		t.Fatalf("billing the plan: %v", err)
	}
	if !gdoc.Reused || gdoc.InvoiceNo != out.InvoiceNo {
		t.Fatalf("the plan must answer the counter's document, got %+v", gdoc)
	}
	if c := e.counts(q); c.Invoices != 1 {
		t.Fatalf("invoices after the plan: %d, want 1", c.Invoices)
	}
}

// - 7: the customer with no credit -

func TestQD5_CashCustomerConvertsAndPaysAsOneAction(t *testing.T) {
	e := qd5Setup(t)
	q := e.seedQuote(0, 0, qd5Trenton)
	// Today's door still refuses a cash customer: that is the gap, and it stays for the plain door.
	if _, err := e.svc.ConvertQuote(e.counter(), q.QuoteID, quote.ConvertOptions{}); err == nil {
		t.Fatal("a customer with no credit must not convert through the plain conversion door")
	}
	out, err := e.svc.TakeOrderPayment(e.counter(), q.QuoteID, pay("CASH", q.Total, "k-cash"))
	if err != nil {
		t.Fatalf("convert and pay: %v", err)
	}
	if !out.Converted || out.BalanceCents != 0 || out.InvoiceTotalCents != q.Total || out.OrderNo == "" {
		t.Fatalf("one action: %+v", out)
	}
	if c := e.counts(q); c != (qd5Counts{Orders: 1, Invoices: 1, Payments: 1}) || e.quoteStatus(q) != "CONVERTED" {
		t.Fatalf("after: %+v status %s", c, e.quoteStatus(q))
	}
	// A replay posts nothing and is not a second conversion.
	again, err := e.svc.TakeOrderPayment(e.counter(), q.QuoteID, pay("CASH", q.Total, "k-cash"))
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !again.Replayed || again.PaymentNo != out.PaymentNo || again.OrderNo != out.OrderNo {
		t.Fatalf("replay %+v vs first %+v", again, out)
	}
	if c := e.counts(q); c != (qd5Counts{Orders: 1, Invoices: 1, Payments: 1}) {
		t.Fatalf("a replay posted something: %+v", c)
	}
}

func TestQD5_CashCustomerShortOverOrBadPaymentLeavesNoOrderAndNoInvoice(t *testing.T) {
	e := qd5Setup(t)
	q := e.seedQuote(0, 0, qd5Trenton)
	cases := map[string]quote.OrderPaymentInput{
		"short":          pay("CASH", q.Total-1, "k-short"),
		"deposit":        pay("CASH", q.Total/2, "k-deposit"),
		"over":           pay("CASH", q.Total+1, "k-over"),
		"zero":           pay("CASH", 0, "k-zero"),
		"negative":       pay("CASH", -500, "k-neg"),
		"unknown method": pay("OTHER", q.Total, "k-method"),
		"charge account": pay("ACCOUNT", q.Total, "k-acct"),
		"missing key":    pay("CASH", q.Total, ""),
	}
	for name, in := range cases {
		_, err := e.svc.TakeOrderPayment(e.counter(), q.QuoteID, in)
		wantBadRequest(t, name, err)
		if c := e.counts(q); c != (qd5Counts{}) || e.quoteStatus(q) != "ACCEPTED" {
			t.Fatalf("%s left rows behind: %+v status %s", name, c, e.quoteStatus(q))
		}
	}
}

func TestQD5_CashCustomerActiveHoldDoesNotBlockACashAction(t *testing.T) {
	e := qd5Setup(t)
	q := e.seedQuote(0, 0, qd5Trenton)
	if _, err := e.pool.Exec(context.Background(),
		`INSERT INTO credit_holds (customer_id, status, reason) VALUES ($1, 'ACTIVE', 'QD-5 test')`, q.CustomerID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = e.pool.Exec(context.Background(), `DELETE FROM credit_holds WHERE customer_id = $1`, q.CustomerID)
	})
	if _, err := e.svc.TakeOrderPayment(e.counter(), q.QuoteID, pay("CASH", q.Total, "k-hold")); err != nil {
		t.Fatalf("a cash sale draws on no credit, a hold must not block it: %v", err)
	}
}

func TestQD5_CustomerWithCreditConvertsFirstThroughTheExistingDoor(t *testing.T) {
	e := qd5Setup(t)
	q := e.seedQuote(1_000_000, 0, qd5Trenton)
	_, err := e.svc.TakeOrderPayment(e.counter(), q.QuoteID, pay("CASH", 1000, "k-first"))
	var c *quote.OrderPaymentConflictError
	if !errors.As(err, &c) {
		t.Fatalf("an accepted quote of a customer with credit converts through today's door first, got %v", err)
	}
	if got := e.counts(q); got != (qd5Counts{}) {
		t.Fatalf("a refusal wrote rows: %+v", got)
	}
}

func TestQD5_AFaultBetweenTheCommitsIsFinishedByTheSameRequest(t *testing.T) {
	e := qd5Setup(t)
	q := e.seedQuote(0, 0, qd5Trenton)
	e.payer.failNext(true)
	_, err := e.svc.TakeOrderPayment(e.counter(), q.QuoteID, pay("CASH", q.Total, "k-fault"))
	var nr *quote.PaymentNotRecordedError
	if !errors.As(err, &nr) {
		t.Fatalf("a fault in the settle must say what exists, got %v", err)
	}
	if nr.OrderNo == "" || nr.InvoiceNo == "" {
		t.Fatalf("the refusal must name the order and the invoice: %+v", nr)
	}
	e.payer.failNext(false)
	out, err := e.svc.TakeOrderPayment(e.counter(), q.QuoteID, pay("CASH", q.Total, "k-fault"))
	if err != nil {
		t.Fatalf("the repeat must finish the action: %v", err)
	}
	if out.BalanceCents != 0 || out.InvoiceNo != nr.InvoiceNo || out.OrderNo != nr.OrderNo {
		t.Fatalf("the repeat finishes the same order and invoice: %+v vs %+v", out, nr)
	}
	if c := e.counts(q); c != (qd5Counts{Orders: 1, Invoices: 1, Payments: 1}) {
		t.Fatalf("after the repeat: %+v", c)
	}
}

// - 8: amounts -

func TestQD5_RefusedAmountsRaiseNoInvoice(t *testing.T) {
	e := qd5Setup(t)
	q := e.seedQuote(1_000_000, 0, qd5Trenton)
	e.convert(q)
	cases := map[string]quote.OrderPaymentInput{
		"above the total": pay("CASH", q.Total+1, "k-a1"),
		"zero":            pay("CASH", 0, "k-a2"),
		"negative":        pay("CASH", -1, "k-a3"),
		"unknown method":  pay("BITCOIN", 100, "k-a4"),
		"missing key":     pay("CASH", 100, ""),
		"key too long":    pay("CASH", 100, strings.Repeat("k", 65)),
		"key charset":     pay("CASH", 100, "bad key\n"),
	}
	for name, in := range cases {
		_, err := e.svc.TakeOrderPayment(e.counter(), q.QuoteID, in)
		wantBadRequest(t, name, err)
		if c := e.counts(q); c.Invoices != 0 || c.Payments != 0 {
			t.Fatalf("%s: a refused payment raised an invoice or a payment: %+v", name, c)
		}
	}
}

func TestQD5_DepositForACreditCustomerLeavesTheRightBalance(t *testing.T) {
	e := qd5Setup(t)
	q := e.seedQuote(1_000_000, 0, qd5Trenton)
	e.convert(q)
	dep, err := e.svc.TakeOrderPayment(e.counter(), q.QuoteID, pay("CHEQUE", 1500, "k-dep"))
	if err != nil {
		t.Fatalf("deposit: %v", err)
	}
	if dep.BalanceCents != q.Total-1500 || dep.AmountCents != 1500 {
		t.Fatalf("deposit answer %+v, want balance %d", dep, q.Total-1500)
	}
	var status string
	_ = e.pool.QueryRow(context.Background(), `SELECT status FROM invoices WHERE customer_id = $1`, q.CustomerID).Scan(&status)
	if status != "PARTIALLY_PAID" {
		t.Fatalf("a deposit leaves the invoice PARTIALLY_PAID, got %q", status)
	}
	// More than what is left is refused by the settle, and posts nothing.
	_, err = e.svc.TakeOrderPayment(e.counter(), q.QuoteID, pay("CASH", q.Total, "k-too-much"))
	wantBadRequest(t, "above the balance", err)
	if c := e.counts(q); c.Payments != 1 {
		t.Fatalf("payments %d, want 1", c.Payments)
	}
	rest, err := e.svc.TakeOrderPayment(e.counter(), q.QuoteID, pay("CASH", q.Total-1500, "k-rest"))
	if err != nil {
		t.Fatalf("the rest: %v", err)
	}
	if rest.BalanceCents != 0 || rest.InvoiceNo != dep.InvoiceNo {
		t.Fatalf("the rest settles the same invoice: %+v", rest)
	}
	if c := e.counts(q); c.Invoices != 1 || c.Payments != 2 {
		t.Fatalf("counts %+v", c)
	}
}

func TestQD5_QuoteInAnyOtherStateTakesNoPayment(t *testing.T) {
	e := qd5Setup(t)
	q := e.seedQuote(1_000_000, 0, qd5Trenton)
	if _, err := e.pool.Exec(context.Background(), `UPDATE quotes SET status = 'SENT' WHERE id = $1`, q.QuoteID); err != nil {
		t.Fatal(err)
	}
	_, err := e.svc.TakeOrderPayment(e.counter(), q.QuoteID, pay("CASH", 100, "k-sent"))
	var c *quote.OrderPaymentConflictError
	if !errors.As(err, &c) {
		t.Fatalf("a quote that is not converted takes no payment, got %v", err)
	}
}
