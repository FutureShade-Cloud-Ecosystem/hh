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
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
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
			rows, err := svc.PayBalanceFind(ctx, tc.term)
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
	rows, err := svc.PayBalanceFind(scoped, "Pay balance proof")
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
	rows, err = svc.PayBalanceFind(own, "Pay balance proof")
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
	billingRepo := billing.NewRepository(pool)
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
	rows, err := svc.PayBalanceFind(ctx, "#SO-PBV-"+suffix)
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

	billingRepo := billing.NewRepository(pool)
	orderSvc := order.NewService(order.NewRepository(pool)).WithPickHoldReader(holdBridge{repo: billingRepo})
	svc := realPosSvc(t, pool, stubCharger{}).WithPayBalanceInvoicer(
		raiseAdapter{w: billing.NewInvoiceWriter(pool).WithOrderSource(payDoorOrderSource{svc: orderSvc})})

	// HELD BEFORE ITS FIRST INVOICE: the hold counts the uninvoiced remainder.
	if held, owed, _, _, err := billingRepo.PickHoldForOrder(ctx, orderID); err != nil || !held || owed != 10000 {
		t.Fatalf("the uninvoiced order's hold reads (held %v, owed %d, err %v), want held, 10000", held, owed, err)
	}
	// The find answers the same number (the review's one number).
	rows, err := svc.PayBalanceFind(ctx, "#SO-PBR-"+suffix)
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
// composition root's posPayBalanceInvoicer, test-side).
type raiseAdapter struct{ w *billing.InvoiceWriter }

func (a raiseAdapter) RaiseOrderInvoiceTx(ctx context.Context, tx pgx.Tx, orderID string) (RaisedOrderInvoice, error) {
	doc, err := a.w.InvoiceOrderAtAgreedTx(ctx, tx, orderID)
	if err != nil {
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
	repo := NewRepository(pool)
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
	_, owedHold, _, _, err := billing.NewRepository(pool).PickHoldForOrder(ctx, orderID)
	if err != nil {
		t.Fatalf("the hold read: %v", err)
	}
	if pre.balanceCents() != 7000 || locked.balanceCents() != 7000 || owedHold != 7000 {
		t.Fatalf("the three reads answer %d, %d and %d, want 7000 at all three: the pre read, the locked read and the hold are one number",
			pre.balanceCents(), locked.balanceCents(), owedHold)
	}
}
