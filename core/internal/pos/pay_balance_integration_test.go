//go:build integration

package pos

// pay_balance_integration_test.go, item 9.4 (the row 1047 deposits work): Pay
// balance at the counter against the real database: the find by each key and
// its fence, the take in full by cash and by card, the declines and refusals,
// the replay, and 9.3's hold lifting through the till's own route.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/quote"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// seedOwingWorld plants a cash on demand customer with a phone and rings a
// deposit sale on a waiting fulfilment: one order owing its balance, held
// from picking, with the deposit as its money trail.
func seedOwingWorld(t *testing.T, pool *pgxpool.Pool, charger till.CardCharger, salt string) (custID, phone, orderID, invoiceID, session, branchID string, balance int64) {
	t.Helper()
	ctx := globalCtx(t, pool)
	productID, branchID := aPaver(t, pool)
	phone = fmt.Sprintf("613-555-%04d", tillRunSalt%10000)
	if err := pool.QueryRow(ctx, `
		INSERT INTO customers (account_no, name, customer_type, payment_terms_days, credit_limit_cents)
		VALUES ($1, 'Pay balance proof', 'CONTRACTOR', 0, 5000000)
		RETURNING id::text`,
		fmt.Sprintf("PB-%s-%d", salt, tillRunSalt)).Scan(&custID); err != nil {
		t.Fatalf("plant the customer: %v", err)
	}
	// The phone the find matches lives on the customer's contact row.
	if _, err := pool.Exec(ctx, `
		INSERT INTO customer_contacts (customer_id, name, phone, is_primary) VALUES ($1::uuid, 'Site contact', $2, true)`,
		custID, phone); err != nil {
		t.Fatalf("plant the contact: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM customers WHERE id = $1::uuid`, custID)
	})
	svc := realPosSvc(t, pool, charger)
	session = openTill(t, pool, branchID)
	out, err := svc.Sale(ctx, SaleInput{
		IdempotencyKey: key(), BranchID: branchID, CustomerID: custID, TillSessionID: session,
		SaleType: SaleCOD, Fulfilment: "WILL_CALL", Tender: TenderCash,
		DepositCents: 2000, DepositMethod: TenderCash,
		Lines: []LineInput{{ProductID: productID, QtyEntered: 5, UOMEntered: "SF"}},
	})
	if err != nil {
		t.Fatalf("the deposit ring: %v", err)
	}
	return custID, phone, out.OrderID, out.InvoiceID, session, branchID, out.TotalCents - 2000
}

// otherBranchID picks a branch that is not the one named.
func otherBranchID(t *testing.T, pool *pgxpool.Pool, not string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(),
		`SELECT id::text FROM locations WHERE id::text <> $1 LIMIT 1`, not).Scan(&id); err != nil {
		t.Skipf("no second branch seeded: %v", err)
	}
	return id
}

func TestIntegration_PayBalance_FindByEveryKey(t *testing.T) {
	pool := intPool(t)
	ctx := globalCtx(t, pool)
	custID, phone, orderID, _, _, _, balance := seedOwingWorld(t, pool, stubCharger{}, "find")
	var orderNo string
	if err := pool.QueryRow(ctx, `SELECT order_no::text FROM orders WHERE id=$1`, orderID).Scan(&orderNo); err != nil {
		t.Fatalf("the order number: %v", err)
	}
	svc := realPosSvc(t, pool, stubCharger{})

	for _, tc := range []struct{ name, term string }{
		{"by order number", orderNo},
		{"by customer name", "Pay balance proof"},
		{"by phone", phone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows, _, err := svc.PayBalanceFind(ctx, tc.term)
			if err != nil {
				t.Fatalf("find: %v", err)
			}
			var found bool
			for _, r := range rows {
				if r.OrderID != orderID {
					continue
				}
				found = true
				if r.PaidCents != 2000 || r.StillOwedCents != balance || !r.HeldFromPicking ||
					r.CustomerID != custID || r.Fulfilment != "WILL_CALL" {
					t.Fatalf("the row: %+v", r)
				}
				if len(r.Payments) != 1 || r.Payments[0].Method != TenderCash || r.Payments[0].AmountCents != 2000 {
					t.Fatalf("the money trail: %+v", r.Payments)
				}
				if r.HoldWords != "This order cannot reach picking until the balance is paid." {
					t.Fatalf("the hold's words: %q", r.HoldWords)
				}
			}
			if !found {
				t.Fatalf("the term %q did not find the owing order, %d rows", tc.term, len(rows))
			}
		})
	}
}

func TestIntegration_PayBalance_FindAndTakeFencedToTheCallersBranch(t *testing.T) {
	pool := intPool(t)
	_, _, orderID, _, _, branchID, _ := seedOwingWorld(t, pool, stubCharger{}, "fence")
	svc := realPosSvc(t, pool, stubCharger{})
	// A caller scoped to a branch that is not the order's finds nothing...
	other := otherBranchID(t, pool, branchID)
	scoped := staffCtx(t, pool, identity.ScopeBranch, other)
	rows, _, err := svc.PayBalanceFind(scoped, "Pay balance proof")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	for _, r := range rows {
		if r.OrderID == orderID {
			t.Fatalf("a branch scoped caller found another branch's order")
		}
	}
	// ...and the take answers the same 404, never a leak.
	if _, err := svc.PayBalanceTake(scoped, PayBalanceTakeInput{
		OrderID: orderID, Method: TenderCash, AmountCents: 1,
		TillSessionID: "none", IdempotencyKey: key(),
	}); !errors.Is(err, ErrPayBalanceOrderNotFound) {
		t.Fatalf("a fenced take must 404, got %v", err)
	}
	// The same caller scoped TO the order's branch finds it.
	own := staffCtx(t, pool, identity.ScopeBranch, branchID)
	rows, _, err = svc.PayBalanceFind(own, "Pay balance proof")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	var found bool
	for _, r := range rows {
		if r.OrderID == orderID {
			found = true
		}
	}
	if !found {
		t.Fatalf("the order's own branch did not find it")
	}
}

func TestIntegration_PayBalance_TakeInFullByCash_LiftsTheHold_AndReplays(t *testing.T) {
	pool := intPool(t)
	ctx := globalCtx(t, pool)
	_, _, orderID, invoiceID, session, _, balance := seedOwingWorld(t, pool, stubCharger{}, "cash")
	svc := realPosSvc(t, pool, stubCharger{})
	billingRepo := testBillingRepo(pool)
	if held, _, _, _, err := billingRepo.PickHoldForOrder(ctx, orderID); err != nil || !held {
		t.Fatalf("the order does not start held: (%v, %v)", held, err)
	}

	takeKey := key()
	out, err := svc.PayBalanceTake(ctx, PayBalanceTakeInput{
		OrderID: orderID, Method: TenderCash, AmountCents: balance,
		TillSessionID: session, IdempotencyKey: takeKey,
	})
	if err != nil {
		t.Fatalf("take: %v", err)
	}
	if out.PaidWords != "Paid in full" || !out.ReleasedToPicking || out.StillOwedCents != 0 || out.Replayed {
		t.Fatalf("the answer: %+v", out)
	}
	var invStatus string
	var invBalance int64
	if err := pool.QueryRow(ctx, `SELECT status, balance_cents FROM invoices WHERE id=$1`, invoiceID).
		Scan(&invStatus, &invBalance); err != nil || invStatus != "PAID" || invBalance != 0 {
		t.Fatalf("the invoice after the take: %s %d (%v)", invStatus, invBalance, err)
	}
	// THE TILL ROUTE'S LIFT (item 9.3's routes): the hold reads the money this
	// settle moved, in the same transaction.
	if held, owed, _, _, err := billingRepo.PickHoldForOrder(ctx, orderID); err != nil || held || owed != 0 {
		t.Fatalf("after the take the facts read (held %v, owed %d, err %v), want lifted", held, owed, err)
	}
	orderSvc := order.NewService(order.NewRepository(pool)).WithPickHoldReader(holdBridge{repo: billingRepo})
	if _, err := orderSvc.TransitionManual(ctx, orderID, order.ActionPicking, ""); err != nil {
		t.Fatalf("the picking transition after the take: %v", err)
	}

	// The replay: the same key answers the first result, charges nothing,
	// writes nothing.
	var paymentsBefore int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM payments p JOIN payment_allocations a ON a.payment_id = p.id WHERE a.invoice_id=$1`,
		invoiceID).Scan(&paymentsBefore); err != nil {
		t.Fatalf("count payments: %v", err)
	}
	again, err := svc.PayBalanceTake(ctx, PayBalanceTakeInput{
		OrderID: orderID, Method: TenderCash, AmountCents: balance,
		TillSessionID: session, IdempotencyKey: takeKey,
	})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !again.Replayed || again.PaymentID != out.PaymentID || again.PaidWords != "Paid in full" {
		t.Fatalf("the replay's answer: %+v", again)
	}
	var paymentsAfter int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM payments p JOIN payment_allocations a ON a.payment_id = p.id WHERE a.invoice_id=$1`,
		invoiceID).Scan(&paymentsAfter); err != nil || paymentsAfter != paymentsBefore {
		t.Fatalf("the replay changed the payment count: %d then %d (%v)", paymentsBefore, paymentsAfter, err)
	}
}

func TestIntegration_PayBalance_TakeByCard_ItsDeclineAndTheRefusals(t *testing.T) {
	pool := intPool(t)
	ctx := globalCtx(t, pool)
	// A world whose card declines: nothing is written.
	_, _, orderID, invoiceID, session, _, balance := seedOwingWorld(t, pool, stubCharger{fail: true}, "decline")
	svc := realPosSvc(t, pool, stubCharger{fail: true})
	if _, err := svc.PayBalanceTake(ctx, PayBalanceTakeInput{
		OrderID: orderID, Method: TenderCard, AmountCents: balance, PaymentToken: "tok-x",
		TillSessionID: session, IdempotencyKey: key(),
	}); !errors.Is(err, ErrCardChargeCardDeclined) {
		t.Fatalf("want the decline answer, got %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM payment_allocations WHERE invoice_id=$1`, invoiceID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("a declined take added allocations: the deposit's own 1 expected, got %d (%v)", n, err)
	}
	var orders int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM orders WHERE id=$1 AND status='CONFIRMED'`, orderID).Scan(&orders); err != nil || orders != 1 {
		t.Fatalf("the declined take disturbed the order: %d (%v)", orders, err)
	}

	// A fresh world with a working card: the take binds the capture, the
	// invoice lands PAID, the part amount and the paid order refuse in words.
	_, _, orderID2, invoiceID2, session2, _, balance2 := seedOwingWorld(t, pool, stubCharger{}, "card")
	svc2 := realPosSvc(t, pool, stubCharger{})
	cardKey := key()
	out, err := svc2.PayBalanceTake(ctx, PayBalanceTakeInput{
		OrderID: orderID2, Method: TenderCard, AmountCents: balance2, PaymentToken: "tok-x",
		TillSessionID: session2, IdempotencyKey: cardKey,
	})
	if err != nil {
		t.Fatalf("the card take: %v", err)
	}
	if out.Method != TenderCard || out.AmountCents != balance2 || out.Replayed {
		t.Fatalf("the card answer: %+v", out)
	}
	var invStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM invoices WHERE id=$1`, invoiceID2).Scan(&invStatus); err != nil || invStatus != "PAID" {
		t.Fatalf("the invoice after the card take: %s (%v)", invStatus, err)
	}
	// The card replay: same key, charges nothing.
	again, err := svc2.PayBalanceTake(ctx, PayBalanceTakeInput{
		OrderID: orderID2, Method: TenderCard, AmountCents: balance2, PaymentToken: "tok-x",
		TillSessionID: session2, IdempotencyKey: cardKey,
	})
	if err != nil || !again.Replayed || again.PaymentID != out.PaymentID {
		t.Fatalf("the card replay: (%+v, %v)", again, err)
	}

	_, _, orderID3, _, session3, _, _ := seedOwingWorld(t, pool, stubCharger{}, "part")
	var e ErrPayBalancePartAmount
	if _, err := svc2.PayBalanceTake(ctx, PayBalanceTakeInput{
		OrderID: orderID3, Method: TenderCash, AmountCents: 7,
		TillSessionID: session3, IdempotencyKey: key(),
	}); !errors.As(err, &e) || e.SentCents != 7 {
		t.Fatalf("the part refusal: %v", err)
	}
	if _, err := svc2.PayBalanceTake(ctx, PayBalanceTakeInput{
		OrderID: orderID2, Method: TenderCash, AmountCents: balance2,
		TillSessionID: session3, IdempotencyKey: key(),
	}); !errors.Is(err, ErrPayBalanceAlreadyPaid) {
		t.Fatalf("the paid order refusal: %v", err)
	}
}

// The find counts a voided invoice as unbilled (the review's void leg): the
// order's only document is VOID, so nothing has billed its total and the find
// must not drop the order as paid, and answers the whole uninvoiced total as
// what it owes, held (one number with the hold).
func TestIntegration_PayBalance_FindCountsAVoidedInvoiceAsUnbilled(t *testing.T) {
	pool := intPool(t)
	ctx := globalCtx(t, pool)
	suffix := fmt.Sprintf("void-%d", time.Now().UnixNano()%1_000_000_000)
	var custID, branchID, orderID, orderNo string
	if err := pool.QueryRow(ctx, `
		WITH c AS (INSERT INTO customers (account_no, name, customer_type, payment_terms_days, credit_limit_cents)
		           VALUES ('PBV-'||$1, 'Void invoice proof', 'CONTRACTOR', 0, 5000000) RETURNING id)
		INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, subtotal_cents, total_cents)
		SELECT '#SO-PBV-'||$1, c.id, (SELECT id FROM locations WHERE loc_type='BRANCH' LIMIT 1),
		       'CONFIRMED', 'WILL_CALL', 10000, 10000
		  FROM c RETURNING id::text, order_no`, suffix).Scan(&orderID, &orderNo); err != nil {
		t.Fatalf("plant the order: %v", err)
	}
	defer pool.Exec(context.Background(), `DELETE FROM orders WHERE id=$1`, orderID)
	if err := pool.QueryRow(ctx, `SELECT customer_id::text, branch_id::text FROM orders WHERE order_no=$1`, orderNo).
		Scan(&custID, &branchID); err != nil {
		t.Fatalf("read the planted order: %v", err)
	}
	defer pool.Exec(context.Background(), `DELETE FROM customers WHERE id=$1`, custID)
	if _, err := pool.Exec(ctx, `
INSERT INTO invoices (invoice_no, customer_id, order_id, branch_id, doc_type, status,
                      subtotal_cents, tax_cents, total_cents, balance_cents, due_date, finalized_at)
VALUES ('#INV-PBV-'||nextval('invoice_no_seq'), $1::uuid, $2::uuid, $3::uuid, 'INVOICE', 'VOID',
        10000, 0, 10000, 10000, current_date, now())`, custID, orderID, branchID); err != nil {
		t.Fatalf("plant the voided invoice: %v", err)
	}
	svc := realPosSvc(t, pool, stubCharger{})
	rows, _, err := svc.PayBalanceFind(ctx, "#SO-PBV-"+suffix)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	var found *OwingOrder
	for i := range rows {
		if rows[i].OrderNo == orderNo {
			found = &rows[i]
		}
	}
	if found == nil {
		t.Fatalf("the find dropped an order whose only invoice is VOID, want it owing its whole total")
	}
	if found.StillOwedCents != 10000 || found.PaidCents != 0 || !found.HeldFromPicking {
		t.Fatalf("the void invoice's order reads owed %d paid %d held %v, want 10000, 0, held: the void billed nothing",
			found.StillOwedCents, found.PaidCents, found.HeldFromPicking)
	}
}

// -- the uninvoiced remainder: one number, raised and settled (the review's P2-2) --

// payDoorOrderSource binds billing's invoice writer to the order module's
// facts, the test-side twin of the composition root's quoteAgreedOrderSource.
type payDoorOrderSource struct{ svc *order.Service }

func payDoorFacts(f order.InvoiceFacts, err error) (billing.OrderInvoiceFacts, error) {
	if err != nil {
		return billing.OrderInvoiceFacts{}, err
	}
	out := billing.OrderInvoiceFacts{
		OrderID: f.OrderID, OrderNo: f.OrderNo, CustomerID: f.CustomerID, BranchID: f.BranchID,
		TermsDays: f.TermsDays, TaxReasonCode: f.TaxReasonCode, ClosesOrder: true,
		FreightCents: f.FreightCents, AgreedTaxCents: f.AgreedTaxCents,
	}
	for _, l := range f.Lines {
		out.Lines = append(out.Lines, billing.OrderInvoiceLine{
			LineNo: l.LineNo, OrderLineID: l.OrderLineID, ProductID: l.ProductID,
			Description: l.Description, QtyEntered: l.QtyEntered, UOMEntered: l.UOMEntered,
			QtyBase: l.QtyBase, UnitPriceCents: l.UnitPriceCents, PriceUOM: l.PriceUOM,
			LineTotalCents: l.LineTotalCents,
		})
	}
	return out, nil
}

func (a payDoorOrderSource) InvoiceableOrder(ctx context.Context, orderID string, groupIDs []string) (billing.OrderInvoiceFacts, error) {
	f, err := a.svc.InvoiceableOrder(ctx, orderID, groupIDs)
	return payDoorFacts(f, err)
}

func (a payDoorOrderSource) MarkGroupsInvoiced(ctx context.Context, groupIDs []string, invoiceID string, at time.Time) error {
	return a.svc.MarkGroupsInvoiced(ctx, groupIDs, invoiceID, at)
}

func (a payDoorOrderSource) AgreedInvoiceableOrder(ctx context.Context, orderID string) (billing.OrderInvoiceFacts, error) {
	f, err := a.svc.AgreedInvoiceableOrder(ctx, orderID)
	return payDoorFacts(f, err)
}

func (a payDoorOrderSource) AgreedInvoiceableOrderTx(ctx context.Context, tx pgx.Tx, orderID string) (billing.OrderInvoiceFacts, error) {
	f, err := a.svc.AgreedInvoiceableOrderTx(ctx, tx, orderID)
	return payDoorFacts(f, err)
}

// A zero terms order with no invoice is found owing its whole total, taken in
// full (the invoice raised inside the take's transaction, then settled), and
// released: the counter door the hold could strand no longer can.
func TestIntegration_PayBalance_UninvoicedOrder_FoundTakenReleased(t *testing.T) {
	pool := intPool(t)
	ctx := globalCtx(t, pool)
	productID, branchID := aPaver(t, pool)
	suffix := fmt.Sprintf("rest-%d", time.Now().UnixNano()%1_000_000_000)
	var custID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO customers (account_no, name, customer_type, payment_terms_days, credit_limit_cents)
		VALUES ('PBR-'||$1, 'Uninvoiced remainder proof', 'CONTRACTOR', 0, 5000000) RETURNING id::text`,
		suffix).Scan(&custID); err != nil {
		t.Fatalf("plant the customer: %v", err)
	}
	var orderID, orderNo string
	if err := pool.QueryRow(ctx, `
		INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, subtotal_cents, total_cents)
		VALUES ('#SO-PBR-'||$1, $2::uuid, $3::uuid, 'CONFIRMED', 'WILL_CALL', 10000, 10000) RETURNING id::text, order_no`,
		suffix, custID, branchID).Scan(&orderID, &orderNo); err != nil {
		t.Fatalf("plant the order: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO order_lines (order_id, line_no, product_id, description, qty_entered, uom_entered,
		                         qty_base, unit_price_cents, price_uom, line_total_cents)
		VALUES ($1::uuid, 1, $2::uuid, 'PBR line', 5, 'SF', 5, 2000, 'SF', 10000)`, orderID, productID); err != nil {
		t.Fatalf("plant the order line: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM invoices WHERE order_id = $1`, orderID)
		_, _ = pool.Exec(c, `DELETE FROM order_lines WHERE order_id = $1`, orderID)
		_, _ = pool.Exec(c, `DELETE FROM orders WHERE id = $1`, orderID)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1`, custID)
	})

	billingRepo := testBillingRepo(pool)
	orderSvc := order.NewService(order.NewRepository(pool)).WithPickHoldReader(holdBridge{repo: billingRepo})
	svc := realPosSvc(t, pool, stubCharger{}).WithPayBalanceInvoicer(
		raiseAdapter{w: billing.NewInvoiceWriter(pool).WithOrderSource(payDoorOrderSource{svc: orderSvc})})

	// HELD BEFORE ITS FIRST INVOICE: the hold counts the uninvoiced remainder.
	if held, owed, _, _, err := billingRepo.PickHoldForOrder(ctx, orderID); err != nil || !held || owed != 10000 {
		t.Fatalf("the uninvoiced order's hold reads (held %v, owed %d, err %v), want held, 10000", held, owed, err)
	}
	// The find answers the same number (the review's one number).
	rows, _, err := svc.PayBalanceFind(ctx, "#SO-PBR-"+suffix)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	var row *OwingOrder
	for i := range rows {
		if rows[i].OrderID == orderID {
			row = &rows[i]
		}
	}
	if row == nil {
		t.Fatalf("the find did not answer the uninvoiced order")
	}
	if row.StillOwedCents != 10000 || !row.HeldFromPicking || row.HoldWords == "" {
		t.Fatalf("the find's row: owed %d held %v words %q, want 10000 held with words",
			row.StillOwedCents, row.HeldFromPicking, row.HoldWords)
	}

	// THE TAKE: the whole total, cash; the invoice is raised inside the take's
	// transaction and settled with it.
	session := openTill(t, pool, branchID)
	out, err := svc.PayBalanceTake(ctx, PayBalanceTakeInput{
		OrderID: orderID, Method: TenderCash, AmountCents: 10000,
		TillSessionID: session, IdempotencyKey: key(),
	})
	if err != nil {
		t.Fatalf("take: %v", err)
	}
	if out.AmountCents != 10000 || !out.ReleasedToPicking || out.StillOwedCents != 0 || out.HeldFromPickingNow {
		t.Fatalf("the take's answer: %+v", out)
	}
	var invStatus, invDoc string
	var invBalance int64
	if err := pool.QueryRow(ctx,
		`SELECT status::text, doc_type, balance_cents FROM invoices WHERE order_id=$1`, orderID).
		Scan(&invStatus, &invDoc, &invBalance); err != nil || invStatus != "PAID" || invDoc != "INVOICE" || invBalance != 0 {
		t.Fatalf("the raised invoice reads %s %s %d (%v), want a PAID INVOICE at 0", invStatus, invDoc, invBalance, err)
	}
	var allocated int64
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(a.amount_cents), 0) FROM payment_allocations a
		  JOIN invoices i ON i.id = a.invoice_id WHERE i.order_id = $1`, orderID).Scan(&allocated); err != nil || allocated != 10000 {
		t.Fatalf("the allocations sum to %d (%v), want 10000 counted once", allocated, err)
	}
	// The subledger: the raised document's INVOICE row and the take's PAYMENT
	// row, both written in the take's one transaction (their occurred_at is
	// the same transaction time, so the assertion is order insensitive).
	var ledger []struct {
		T string
		A int64
	}
	lrows, err := pool.Query(ctx, `
		SELECT txn_type, amount_cents FROM customer_transactions
		 WHERE source_ref_id IN (SELECT id FROM invoices WHERE order_id=$1) OR source_ref_id IN
		       (SELECT a.payment_id FROM payment_allocations a JOIN invoices i ON i.id=a.invoice_id WHERE i.order_id=$1)`, orderID)
	if err != nil {
		t.Fatalf("the subledger read: %v", err)
	}
	for lrows.Next() {
		var r struct {
			T string
			A int64
		}
		if err := lrows.Scan(&r.T, &r.A); err != nil {
			t.Fatalf("scan ledger: %v", err)
		}
		ledger = append(ledger, r)
	}
	lrows.Close()
	var invoiceRow, paymentRow bool
	for _, r := range ledger {
		if r.T == "INVOICE" && r.A == 10000 {
			invoiceRow = true
		}
		if r.T == "PAYMENT" && r.A == -10000 {
			paymentRow = true
		}
	}
	if len(ledger) != 2 || !invoiceRow || !paymentRow {
		t.Fatalf("the subledger rows: %+v, want the raised INVOICE at 10000 and the PAYMENT at -10000", ledger)
	}
	// RELEASED: the picking door that the hold guarded now moves.
	if _, err := orderSvc.TransitionManual(ctx, orderID, order.ActionPicking, ""); err != nil {
		t.Fatalf("the picking transition after the take: %v", err)
	}
}

// raiseAdapter shapes the writer's answer into the take's seam (the
// composition root's posPayBalanceInvoicer, test-side, refusal mapping
// included: every refusal the raise can return crosses as the door's own
// fixed 409 sentence, exactly as payBalanceRaiseRefusal maps them).
type raiseAdapter struct{ w *billing.InvoiceWriter }

func (a raiseAdapter) RaiseOrderInvoiceTx(ctx context.Context, tx pgx.Tx, orderID string) (RaisedOrderInvoice, error) {
	doc, err := a.w.InvoiceOrderAtAgreedTx(ctx, tx, orderID)
	if err != nil {
		var already *billing.OrderAlreadyInvoicedError
		var conflict *quote.OrderPaymentConflictError
		var refusal *order.AgreedBillingRefusal
		switch {
		case errors.As(err, &already):
			return RaisedOrderInvoice{}, ErrPayBalanceRestUnbillable
		case errors.As(err, &conflict), errors.As(err, &refusal), errors.Is(err, billing.ErrNothingToInvoice):
			return RaisedOrderInvoice{}, ErrPayBalanceRaiseRefused
		}
		return RaisedOrderInvoice{}, err
	}
	return RaisedOrderInvoice{InvoiceID: doc.InvoiceID, InvoiceNo: doc.InvoiceNo,
		TotalCents: doc.TotalCents, BalanceCents: doc.BalanceCents}, nil
}

// The advisory pre read and the locked read use the SAME predicate (the
// review's P3-2 pin): an order carrying a credit memo beside its open invoice
// is the shape the two reads used to disagree on (the pre read summed the
// memo's negative balance into the take's amount, the locked read skipped it,
// and a card charged on the first figure was reversed by the second). One
// predicate feeds both now: the open balances of live INVOICE documents, plus
// the uninvoiced remainder, and the hold reads the same number third.
func TestIntegration_PayBalance_PreReadAndLockedReadAgree(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	suffix := fmt.Sprintf("agree-%d", time.Now().UnixNano()%1_000_000_000)
	var custID, orderID string
	if err := pool.QueryRow(ctx, `
		WITH c AS (INSERT INTO customers (account_no, name, customer_type, payment_terms_days, credit_limit_cents)
		           VALUES ('PBA-'||$1, 'Pre and locked proof', 'CONTRACTOR', 0, 5000000) RETURNING id)
		INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, subtotal_cents, total_cents)
		SELECT '#SO-PBA-'||$1, c.id, (SELECT id FROM locations WHERE loc_type='BRANCH' LIMIT 1),
		       'CONFIRMED', 'WILL_CALL', 10000, 10000
		  FROM c RETURNING id::text, customer_id::text`, suffix).Scan(&orderID, &custID); err != nil {
		t.Fatalf("plant the order: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM invoices WHERE order_id = $1`, orderID)
		_, _ = pool.Exec(c, `DELETE FROM orders WHERE id = $1`, orderID)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1`, custID)
	})
	var branchID string
	if err := pool.QueryRow(ctx, `SELECT branch_id::text FROM orders WHERE id=$1`, orderID).Scan(&branchID); err != nil {
		t.Fatalf("read the order: %v", err)
	}
	for _, doc := range []struct {
		docType, status string
		total, balance  int64
	}{
		{"INVOICE", "PARTIALLY_PAID", 10000, 7000},
		{"CREDIT_MEMO", "FINALIZED", -2000, -2000},
		{"INVOICE", "PAID", 0, 0},
	} {
		if _, err := pool.Exec(ctx, `
INSERT INTO invoices (invoice_no, customer_id, order_id, branch_id, doc_type, status,
                      subtotal_cents, tax_cents, total_cents, balance_cents, due_date, finalized_at)
VALUES ('#INV-PBA-'||nextval('invoice_no_seq'), $1::uuid, $2::uuid, $3::uuid, $4, $5, $6, 0, $6, $7, current_date, now())`,
			custID, orderID, branchID, doc.docType, doc.status, doc.total, doc.balance); err != nil {
			t.Fatalf("plant %s %s: %v", doc.docType, doc.status, err)
		}
	}
	repo := testPosRepo(pool)
	pre, found, err := repo.ReadOwingOrder(ctx, orderID)
	if err != nil || !found {
		t.Fatalf("the pre read: (%v, %v)", found, err)
	}
	var locked payBalanceOrderFacts
	txErr := repo.RunInTx(ctx, func(tx pgx.Tx) error {
		var err error
		locked, err = repo.LockPayBalanceOrderTx(ctx, tx, orderID)
		return err
	})
	if txErr != nil {
		t.Fatalf("the locked read: %v", txErr)
	}
	_, owedHold, _, _, err := testBillingRepo(pool).PickHoldForOrder(ctx, orderID)
	if err != nil {
		t.Fatalf("the hold read: %v", err)
	}
	if pre.balanceCents() != 7000 || locked.balanceCents() != 7000 || owedHold != 7000 {
		t.Fatalf("the three reads answer %d, %d and %d, want 7000 at all three: the pre read, the locked read and the hold are one number",
			pre.balanceCents(), locked.balanceCents(), owedHold)
	}
}

// The find's term is user text matched with LIKE: the wildcards in it are
// LITERAL (the review's P3-3), so a term of "%" or "_" matches the orders
// whose own text carries that character, never every owing order in reach.
func TestIntegration_PayBalance_FindEscapesTheWildcards(t *testing.T) {
	pool := intPool(t)
	ctx := globalCtx(t, pool)
	suffix := fmt.Sprintf("esc-%d", time.Now().UnixNano()%1_000_000_000)
	plant := func(name string) string {
		var custID, orderID, orderNo string
		if err := pool.QueryRow(ctx, `
			WITH c AS (INSERT INTO customers (account_no, name, customer_type, payment_terms_days, credit_limit_cents)
			           VALUES ('PBE-'||$1, $2, 'CONTRACTOR', 0, 5000000) RETURNING id)
			INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, subtotal_cents, total_cents)
			SELECT '#SO-PBE-'||$1, c.id, (SELECT id FROM locations WHERE loc_type='BRANCH' LIMIT 1),
			       'CONFIRMED', 'WILL_CALL', 10000, 10000
			  FROM c RETURNING id::text, order_no`, suffix+name, name).Scan(&orderID, &orderNo); err != nil {
			t.Fatalf("plant %s: %v", name, err)
		}
		if err := pool.QueryRow(ctx, `SELECT customer_id::text FROM orders WHERE id=$1`, orderID).Scan(&custID); err != nil {
			t.Fatalf("read the customer: %v", err)
		}
		t.Cleanup(func() {
			c := context.Background()
			_, _ = pool.Exec(c, `DELETE FROM orders WHERE id = $1`, orderID)
			_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1`, custID)
		})
		// One open invoice, so both orders owe and both sit in the find's reach.
		if _, err := pool.Exec(ctx, `
INSERT INTO invoices (invoice_no, customer_id, order_id, branch_id, doc_type, status,
                      subtotal_cents, tax_cents, total_cents, balance_cents, due_date, finalized_at)
VALUES ('#INV-PBE-'||nextval('invoice_no_seq'), $1::uuid, $2::uuid,
        (SELECT branch_id FROM orders WHERE id = $2::uuid), 'INVOICE', 'FINALIZED',
        10000, 0, 10000, 10000, current_date, now())`, custID, orderID); err != nil {
			t.Fatalf("plant %s's invoice: %v", name, err)
		}
		return orderNo
	}
	percentNo := plant("EscPE 50% Co" + suffix)
	underscoreNo := plant("Esc_US " + suffix)
	svc := realPosSvc(t, pool, stubCharger{})
	// The bare wildcards are the sharpest terms: unescaped, each would match
	// every owing order in reach (both planted ones at least); escaped, each
	// matches exactly the one order whose own text carries the character.
	rows, _, err := svc.PayBalanceFind(ctx, "%")
	if err != nil {
		t.Fatalf("find the percent term: %v", err)
	}
	if len(rows) != 1 || rows[0].OrderNo != percentNo {
		t.Fatalf("the term %% answered %d rows (%v), want exactly the one order whose name carries a literal percent", len(rows), orderNos(rows))
	}
	rows, _, err = svc.PayBalanceFind(ctx, "_US")
	if err != nil {
		t.Fatalf("find the underscore term: %v", err)
	}
	if len(rows) != 1 || rows[0].OrderNo != underscoreNo {
		t.Fatalf("the term _US answered %d rows (%v), want exactly the one order whose name carries a literal underscore", len(rows), orderNos(rows))
	}
}

func orderNos(rows []OwingOrder) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.OrderNo)
	}
	return out
}

// -- the review's P3-8 legs: multi invoice allocations, and two takes racing --

// plantOwingOrder plants one zero day terms customer, one order and the open
// invoices named, and unwinds all of it. It answers the order id, the
// customer, the branch and the whole balance.
func plantOwingOrder(t *testing.T, pool *pgxpool.Pool, invoices []struct {
	total, balance int64
}) (orderID, custID, branchID string, balance int64) {
	t.Helper()
	ctx := context.Background()
	suffix := fmt.Sprintf("multi-%d", time.Now().UnixNano()%1_000_000_000)
	if err := pool.QueryRow(ctx, `
		WITH c AS (INSERT INTO customers (account_no, name, customer_type, payment_terms_days, credit_limit_cents)
		           VALUES ('PBM-'||$1, 'Multi invoice proof', 'CONTRACTOR', 0, 5000000) RETURNING id)
		INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, subtotal_cents, total_cents)
		SELECT '#SO-PBM-'||$1, c.id, (SELECT id FROM locations WHERE loc_type='BRANCH' LIMIT 1),
		       'CONFIRMED', 'WILL_CALL', 15000, 15000
		  FROM c RETURNING id::text, customer_id::text, branch_id::text`, suffix).Scan(&orderID, &custID, &branchID); err != nil {
		t.Fatalf("plant the order: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM invoices WHERE order_id = $1`, orderID)
		_, _ = pool.Exec(c, `DELETE FROM orders WHERE id = $1`, orderID)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1`, custID)
	})
	for _, inv := range invoices {
		if _, err := pool.Exec(ctx, `
INSERT INTO invoices (invoice_no, customer_id, order_id, branch_id, doc_type, status,
                      subtotal_cents, tax_cents, total_cents, balance_cents, due_date, finalized_at)
VALUES ('#INV-PBM-'||nextval('invoice_no_seq'), $1::uuid, $2::uuid, $3::uuid, 'INVOICE', 'FINALIZED',
        $4, 0, $4, $5, current_date, now())`, custID, orderID, branchID, inv.total, inv.balance); err != nil {
			t.Fatalf("plant an invoice: %v", err)
		}
		balance += inv.balance
	}
	return orderID, custID, branchID, balance
}

// A multi invoice order's take allocates once per invoice, at each invoice's
// own balance, and the subledger's PAYMENT rows sum to the whole take.
func TestIntegration_PayBalance_MultiInvoiceTakeAllocatesPerInvoice(t *testing.T) {
	pool := intPool(t)
	ctx := globalCtx(t, pool)
	orderID, _, branchID, balance := plantOwingOrder(t, pool, []struct {
		total, balance int64
	}{{10000, 10000}, {5000, 5000}})
	if balance != 15000 {
		t.Fatalf("the planted balance is %d, want 15000", balance)
	}
	svc := realPosSvc(t, pool, stubCharger{})
	session := openTill(t, pool, branchID)
	out, err := svc.PayBalanceTake(ctx, PayBalanceTakeInput{
		OrderID: orderID, Method: TenderCash, AmountCents: balance,
		TillSessionID: session, IdempotencyKey: key(),
	})
	if err != nil {
		t.Fatalf("take: %v", err)
	}
	if out.AmountCents != 15000 {
		t.Fatalf("the take's amount is %d, want the whole 15000", out.AmountCents)
	}
	// One allocation per invoice, at that invoice's own balance.
	arows, err := pool.Query(ctx, `
		SELECT i.total_cents, a.amount_cents, i.status::text FROM payment_allocations a
		  JOIN invoices i ON i.id = a.invoice_id WHERE i.order_id = $1 ORDER BY i.total_cents DESC`, orderID)
	if err != nil {
		t.Fatalf("read the allocations: %v", err)
	}
	defer arows.Close()
	var count int
	var allocated int64
	for arows.Next() {
		var total, amount int64
		var status string
		if err := arows.Scan(&total, &amount, &status); err != nil {
			t.Fatalf("scan an allocation: %v", err)
		}
		count++
		if amount != total || status != "PAID" {
			t.Fatalf("the %d cent invoice took a %d cent allocation at %s, want its whole balance and PAID",
				total, amount, status)
		}
		allocated += amount
	}
	if count != 2 || allocated != 15000 {
		t.Fatalf("%d allocations summing %d, want one per invoice summing 15000", count, allocated)
	}
	// The subledger's PAYMENT rows sum to the whole take, once.
	var ledgerSum int64
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount_cents), 0) FROM customer_transactions t
		 WHERE t.txn_type = 'PAYMENT' AND t.source_ref_id IN
		       (SELECT a.payment_id FROM payment_allocations a JOIN invoices i ON i.id = a.invoice_id WHERE i.order_id = $1)`,
		orderID).Scan(&ledgerSum); err != nil || ledgerSum != -15000 {
		t.Fatalf("the subledger's PAYMENT rows sum to %d (%v), want -15000 counted once", ledgerSum, err)
	}
}

// Two takes racing on one order with one key: exactly one commits, the other
// answers the winner as a replay (or the paid order's own refusal), and the
// money counts once.
func TestIntegration_PayBalance_TwoTakesRacingCountTheMoneyOnce(t *testing.T) {
	pool := intPool(t)
	ctx := globalCtx(t, pool)
	orderID, _, branchID, balance := plantOwingOrder(t, pool, []struct {
		total, balance int64
	}{{15000, 15000}})
	svc := realPosSvc(t, pool, stubCharger{})
	session := openTill(t, pool, branchID)
	raceKey := key()
	const racers = 2
	results := make([]PayBalanceTaken, racers)
	errs := make([]error, racers)
	var start sync.WaitGroup
	start.Add(1)
	var wg sync.WaitGroup
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			start.Wait()
			results[i], errs[i] = svc.PayBalanceTake(ctx, PayBalanceTakeInput{
				OrderID: orderID, Method: TenderCash, AmountCents: balance,
				TillSessionID: session, IdempotencyKey: raceKey,
			})
		}(i)
	}
	start.Done()
	wg.Wait()

	var committed, replayed, refused int
	for i := 0; i < racers; i++ {
		switch {
		case errs[i] != nil:
			if !errors.Is(errs[i], ErrPayBalanceAlreadyPaid) {
				t.Fatalf("racer %d errored beyond the paid refusal: %v", i, errs[i])
			}
			refused++
		case results[i].Replayed:
			replayed++
		default:
			committed++
		}
	}
	if committed != 1 || committed+replayed+refused != racers {
		t.Fatalf("the race answered %d committed, %d replayed, %d refused; want exactly one commit",
			committed, replayed, refused)
	}
	var payments, allocated int64
	if err := pool.QueryRow(ctx, `
		SELECT count(DISTINCT a.payment_id), COALESCE(SUM(a.amount_cents), 0)
		  FROM payment_allocations a JOIN invoices i ON i.id = a.invoice_id
		 WHERE i.order_id = $1 AND EXISTS (SELECT 1 FROM payments p WHERE p.id = a.payment_id AND p.status = 'RECEIVED')`,
		orderID).Scan(&payments, &allocated); err != nil || payments != 1 || allocated != balance {
		t.Fatalf("after the race %d payments allocated %d, want one payment allocating %d once",
			payments, allocated, balance)
	}
	var invStatus string
	if err := pool.QueryRow(ctx, `SELECT status::text FROM invoices WHERE order_id=$1`, orderID).Scan(&invStatus); err != nil || invStatus != "PAID" {
		t.Fatalf("the invoice is %s (%v), want PAID", invStatus, err)
	}
}

// -- the delta review's P2-A legs: no card is captured on a take that cannot complete --

// countingCharger wraps the stub charger and counts the captures and
// compensations it took, so a leg can assert ZERO captures (the refusal came
// before the rail).
type countingCharger struct {
	stubCharger
	count int
	comps []string
}

func (c *countingCharger) Capture(ctx context.Context, in till.CardChargeInput) (till.CardCapture, error) {
	capture, err := c.stubCharger.Capture(ctx, in)
	if err != nil {
		return till.CardCapture{}, err
	}
	c.count++
	return capture, nil
}

func (c *countingCharger) Compensate(ctx context.Context, attemptID string) (till.CardCompensation, error) {
	c.comps = append(c.comps, attemptID)
	return c.stubCharger.Compensate(ctx, attemptID)
}

// A zero terms order PARTLY invoiced from its shipment groups (invoiced above
// zero, remainder above zero) is the unbillable shape: the whole balance
// includes a part no invoice covers and this door cannot bill, so the take
// refuses BEFORE any capture, and no card is charged and reversed for it.
func TestIntegration_PayBalance_PartlyInvoicedOrder_RefusedBeforeAnyCapture(t *testing.T) {
	pool := intPool(t)
	ctx := globalCtx(t, pool)
	orderID, _, branchID, _ := plantOwingOrder(t, pool, []struct {
		total, balance int64
	}{{10000, 10000}})
	// The planted order's total is 15000 with one live invoice of 10000: the
	// remainder (5000) is the unbillable part.
	charger := &countingCharger{}
	svc := realPosSvc(t, pool, charger)
	session := openTill(t, pool, branchID)
	if _, err := svc.PayBalanceTake(ctx, PayBalanceTakeInput{
		OrderID: orderID, Method: TenderCard, AmountCents: 15000, PaymentToken: "tok-x",
		TillSessionID: session, IdempotencyKey: key(),
	}); !errors.Is(err, ErrPayBalanceRestUnbillable) {
		t.Fatalf("want the unbillable rest refusal, got %v", err)
	}
	if charger.count != 0 {
		t.Fatalf("a card was captured for a take that cannot complete: %d captures", charger.count)
	}
	var payments int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM payments p JOIN payment_allocations a ON a.payment_id = p.id
		  JOIN invoices i ON i.id = a.invoice_id WHERE i.order_id = $1`, orderID).Scan(&payments); err != nil || payments != 0 {
		t.Fatalf("the refused take wrote payments: %d (%v)", payments, err)
	}
}

// A cancelled order that still carries an open invoice is NOT this door's to
// pay: the find does not answer it and the take answers the door's 404, before
// any capture.
func TestIntegration_PayBalance_CancelledOrder_NotFound(t *testing.T) {
	pool := intPool(t)
	ctx := globalCtx(t, pool)
	orderID, _, branchID, _ := plantOwingOrder(t, pool, []struct {
		total, balance int64
	}{{15000, 15000}})
	var orderNo string
	if err := pool.QueryRow(ctx, `SELECT order_no FROM orders WHERE id=$1`, orderID).Scan(&orderNo); err != nil {
		t.Fatalf("read the order number: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE orders SET status='CANCELLED' WHERE id=$1`, orderID); err != nil {
		t.Fatalf("cancel the order: %v", err)
	}
	charger := &countingCharger{}
	svc := realPosSvc(t, pool, charger)
	rows, _, err := svc.PayBalanceFind(ctx, orderNo)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	for _, r := range rows {
		if r.OrderID == orderID {
			t.Fatalf("the find answered a cancelled order as owing money")
		}
	}
	session := openTill(t, pool, branchID)
	if _, err := svc.PayBalanceTake(ctx, PayBalanceTakeInput{
		OrderID: orderID, Method: TenderCard, AmountCents: 15000, PaymentToken: "tok-x",
		TillSessionID: session, IdempotencyKey: key(),
	}); !errors.Is(err, ErrPayBalanceOrderNotFound) {
		t.Fatalf("a cancelled order must answer the door's 404, got %v", err)
	}
	if charger.count != 0 {
		t.Fatalf("a card was captured for a cancelled order: %d captures", charger.count)
	}
}

// An order whose own header disagrees with its lines is refused by the raise
// inside the transaction (a disagreement is something to look at, never a
// document to bill): the refusal crosses as the door's own fixed 409 sentence,
// never a bare 500, and nothing is taken.
func TestIntegration_PayBalance_BrokenOrderRaiseRefusal_IsAFixedSentence(t *testing.T) {
	pool := intPool(t)
	ctx := globalCtx(t, pool)
	productID, branchID := aPaver(t, pool)
	suffix := fmt.Sprintf("brk-%d", time.Now().UnixNano()%1_000_000_000)
	var custID, orderID string
	if err := pool.QueryRow(ctx, `
		WITH c AS (INSERT INTO customers (account_no, name, customer_type, payment_terms_days, credit_limit_cents)
		           VALUES ('PBK-'||$1, 'Broken arithmetic proof', 'CONTRACTOR', 0, 5000000) RETURNING id)
		INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, subtotal_cents, total_cents)
		SELECT '#SO-PBK-'||$1, c.id, $2::uuid, 'CONFIRMED', 'WILL_CALL', 10000, 10000
		  FROM c RETURNING id::text, customer_id::text`, suffix, branchID).Scan(&orderID, &custID); err != nil {
		t.Fatalf("plant the order: %v", err)
	}
	// The line sums to 9000 while the header says 10000: the order does not
	// agree with its own lines.
	if _, err := pool.Exec(ctx, `
		INSERT INTO order_lines (order_id, line_no, product_id, description, qty_entered, uom_entered,
		                         qty_base, unit_price_cents, price_uom, line_total_cents)
		VALUES ($1::uuid, 1, $2::uuid, 'PBK line', 5, 'SF', 5, 1800, 'SF', 9000)`, orderID, productID); err != nil {
		t.Fatalf("plant the order line: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM invoices WHERE order_id = $1`, orderID)
		_, _ = pool.Exec(c, `DELETE FROM order_lines WHERE order_id = $1`, orderID)
		_, _ = pool.Exec(c, `DELETE FROM orders WHERE id = $1`, orderID)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1`, custID)
	})
	billingRepo := testBillingRepo(pool)
	orderSvc := order.NewService(order.NewRepository(pool)).WithPickHoldReader(holdBridge{repo: billingRepo})
	svc := realPosSvc(t, pool, stubCharger{}).WithPayBalanceInvoicer(
		raiseAdapter{w: billing.NewInvoiceWriter(pool).WithOrderSource(payDoorOrderSource{svc: orderSvc})})
	session := openTill(t, pool, branchID)
	if _, err := svc.PayBalanceTake(ctx, PayBalanceTakeInput{
		OrderID: orderID, Method: TenderCash, AmountCents: 10000,
		TillSessionID: session, IdempotencyKey: key(),
	}); !errors.Is(err, ErrPayBalanceRaiseRefused) {
		t.Fatalf("the broken order's refusal must be the door's fixed sentence, got %v", err)
	}
	var payments, invoices int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM payments p JOIN payment_allocations a ON a.payment_id = p.id
		  JOIN invoices i ON i.id = a.invoice_id WHERE i.order_id = $1`, orderID).Scan(&payments); err != nil || payments != 0 {
		t.Fatalf("the refused take wrote payments: %d (%v)", payments, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM invoices WHERE order_id = $1`, orderID).Scan(&invoices); err != nil || invoices != 0 {
		t.Fatalf("the refused take wrote an invoice: %d (%v)", invoices, err)
	}
}

// -- item 9 F review N5: the find never silently drops an owing order --

// plantFindCustomer plants one zero day terms customer and returns its name
// (the find term), id and branch, unwinding every order and invoice planted
// under it.
func plantFindCustomer(t *testing.T, pool *pgxpool.Pool, tag string) (name, custID, branchID string) {
	t.Helper()
	ctx := context.Background()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000_000)
	name = "Findbound " + tag + " " + suffix
	if err := pool.QueryRow(ctx, `
		INSERT INTO customers (account_no, name, customer_type, payment_terms_days, credit_limit_cents)
		VALUES ('FB-'||$1, $2, 'CONTRACTOR', 0, 5000000) RETURNING id::text,
		       (SELECT id::text FROM locations WHERE loc_type='BRANCH' ORDER BY id LIMIT 1)`, suffix, name).Scan(&custID, &branchID); err != nil {
		t.Fatalf("plant the customer: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM invoices WHERE customer_id = $1::uuid`, custID)
		_, _ = pool.Exec(c, `DELETE FROM orders WHERE customer_id = $1::uuid`, custID)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1::uuid`, custID)
	})
	return name, custID, branchID
}

// findSuffix is the unique tail plantFindCustomer put on the name.
func findSuffix(name string) string { return name[strings.LastIndex(name, " ")+1:] }

// plantPaidOrders plants n orders billed in full and paid, a day old.
func plantPaidOrders(t *testing.T, pool *pgxpool.Pool, custID, branchID, suffix string, n int) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, subtotal_cents, total_cents, created_at)
		SELECT '#SO-FBP-'||$1||'-'||g, $2::uuid, $3::uuid, 'CONFIRMED', 'WILL_CALL', 10000, 10000, now() - interval '1 day'
		  FROM generate_series(1, $4::int) g`, suffix, custID, branchID, n); err != nil {
		t.Fatalf("plant the paid orders: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO invoices (invoice_no, customer_id, order_id, branch_id, doc_type, status,
		                      subtotal_cents, tax_cents, total_cents, balance_cents, due_date, finalized_at)
		SELECT '#INV-FBP-'||nextval('invoice_no_seq'), $1::uuid, o.id, $2::uuid, 'INVOICE', 'PAID',
		       10000, 0, 10000, 0, current_date, now()
		  FROM orders o WHERE o.customer_id = $1::uuid AND o.order_no LIKE '#SO-FBP-'||$3||'-%'`, custID, branchID, suffix); err != nil {
		t.Fatalf("plant the paid invoices: %v", err)
	}
}

// plantFindOrder plants one order at the given age (days back) with one
// invoice of the given total, status and balance; invoiceTotal 0 plants no
// invoice. It answers the order number.
func plantFindOrder(t *testing.T, pool *pgxpool.Pool, custID, branchID, no string, daysBack int, orderTotal, invoiceTotal, balance int64, status string) string {
	t.Helper()
	ctx := context.Background()
	var orderID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, subtotal_cents, total_cents, created_at)
		VALUES ($1, $2::uuid, $3::uuid, 'CONFIRMED', 'WILL_CALL', $4, $4, now() - ($5 || ' days')::interval)
		RETURNING id::text`, no, custID, branchID, orderTotal, fmt.Sprint(daysBack)).Scan(&orderID); err != nil {
		t.Fatalf("plant order %s: %v", no, err)
	}
	if invoiceTotal > 0 {
		if _, err := pool.Exec(ctx, `
			INSERT INTO invoices (invoice_no, customer_id, order_id, branch_id, doc_type, status,
			                      subtotal_cents, tax_cents, total_cents, balance_cents, due_date, finalized_at)
			VALUES ('#INV-FB-'||nextval('invoice_no_seq'), $1::uuid, $2::uuid, $3::uuid, 'INVOICE', $4,
			        $5, 0, $5, $6, current_date, now())`, custID, orderID, branchID, status, invoiceTotal, balance); err != nil {
			t.Fatalf("plant the invoice of %s: %v", no, err)
		}
	}
	return no
}

// Five hundred and twenty paid orders (past the old find's ten pages of fifty),
// all newer, and older orders that still owe, for the same customer: the find answers the owing one (and the order billed short,
// and the order nothing was billed on), and says nothing was cut off.
func TestIntegration_PayBalance_FindReachesAnOwingOrderBehindManyPaidOnes(t *testing.T) {
	pool := intPool(t)
	ctx := globalCtx(t, pool)
	name, custID, branchID := plantFindCustomer(t, pool, "paid60")
	suffix := findSuffix(name)
	// More paid orders than the old find's whole walk (ten pages of fifty), all
	// newer than the owing ones.
	plantPaidOrders(t, pool, custID, branchID, suffix, 520)
	owing := plantFindOrder(t, pool, custID, branchID, "#SO-FBO-"+suffix, 30, 10000, 10000, 10000, "FINALIZED")
	billedShort := plantFindOrder(t, pool, custID, branchID, "#SO-FBS-"+suffix, 31, 15000, 10000, 0, "PAID")
	uninvoiced := plantFindOrder(t, pool, custID, branchID, "#SO-FBU-"+suffix, 32, 7000, 0, 0, "")

	svc := realPosSvc(t, pool, stubCharger{})
	rows, truncated, err := svc.PayBalanceFind(ctx, name)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	got := map[string]int64{}
	for _, r := range rows {
		got[r.OrderNo] = r.StillOwedCents
	}
	want := map[string]int64{owing: 10000, billedShort: 5000, uninvoiced: 7000}
	if len(got) != len(want) {
		t.Fatalf("the find answered %v, want exactly the three owing orders %v (not the paid ones)", orderNos(rows), want)
	}
	for no, cents := range want {
		if got[no] != cents {
			t.Fatalf("order %s owes %d in the find, want %d (answered %v)", no, got[no], cents, got)
		}
	}
	if truncated {
		t.Fatal("nothing was cut off, yet the find says it was")
	}
}

// More owing orders than the answer holds: the answer is full and says so.
func TestIntegration_PayBalance_FindSaysWhenMoreOrdersMatch(t *testing.T) {
	pool := intPool(t)
	ctx := globalCtx(t, pool)
	name, custID, branchID := plantFindCustomer(t, pool, "many")
	suffix := findSuffix(name)
	for i := 0; i < 23; i++ {
		plantFindOrder(t, pool, custID, branchID, fmt.Sprintf("#SO-FBM-%s-%02d", suffix, i), i+1, 10000, 10000, 10000, "FINALIZED")
	}
	svc := realPosSvc(t, pool, stubCharger{})
	rows, truncated, err := svc.PayBalanceFind(ctx, name)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if len(rows) != 20 {
		t.Fatalf("the find answered %d rows, want the full 20", len(rows))
	}
	if !truncated {
		t.Fatal("twenty three orders owe and twenty were answered, yet the find did not say more match")
	}
}
