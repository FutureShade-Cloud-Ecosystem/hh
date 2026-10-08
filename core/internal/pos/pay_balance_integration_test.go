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

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
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
