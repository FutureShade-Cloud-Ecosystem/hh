//go:build integration

package pos

// sale_pick_hold_integration_test.go, item 9.3 (the row 1047 deposits work):
// the pay before release hold against the real database, end to end. A cash on
// demand sale with a deposit is held from every path that releases goods; the
// hold reads the money, so the last dollar lifts it, whichever route took it.
//
// THE ROUTES THAT LIFT, and what this file proves about each:
//   - the AR register: the real billing.RegisterService.RecordPayment, the
//     exact code the register route drives.
//   - the desk's order payment: it settles through the payments module's own
//     settle (quote/order_payment.go's CounterPayer is payments.RecordReceiptTx),
//     so the lift is proven through that settle directly, the engine both the
//     desk door and the register ride.
//   - the till: its balance paying route is item 9.4's take, and that file's
//     integration test carries the lift leg for it. The deposit itself (the
//     till's line one money) is what this file's ring plants.
//
// Concurrency: two whole balance payments racing lift the hold once and count
// the money once.

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
)

// holdBridge adapts billing's facts onto the order module's seam (the
// composition root's pickHoldAdapter, in the test).
type holdBridge struct{ repo *billing.Repository }

func (b holdBridge) PickHoldFacts(ctx context.Context, orderID string) (order.PickHoldFacts, error) {
	held, owed, paid, total, err := b.repo.PickHoldForOrder(ctx, orderID)
	if err != nil {
		return order.PickHoldFacts{}, err
	}
	out := order.PickHoldFacts{Held: held, OrderTotalCents: total, PaidCents: paid, OwedCents: owed}
	if held {
		out.Words = order.PickHoldWords
	}
	if paid > 0 && owed > 0 {
		out.InProcessWords = order.PickInProcessWords
	}
	return out, nil
}

// The full walk: a deposit ring on a cash on demand account, held at every
// releasing path, lifted by the AR register's own service.
func TestIntegration_PickHold_CashOnDemandDepositHoldsEveryReleasingPath(t *testing.T) {
	pool := intPool(t)
	ctx := globalCtx(t, pool)
	productID, branchID := aPaver(t, pool)

	// A cash on demand account: zero day terms with a credit line (the demo
	// world's C10 shape), unique per run so the leg is rerunnable.
	var custID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO customers (account_no, name, customer_type, payment_terms_days, credit_limit_cents)
		VALUES ($1, 'Pick hold proof', 'CONTRACTOR', 0, 5000000)
		RETURNING id::text`,
		fmt.Sprintf("PH-%s", fixtureTag())).Scan(&custID); err != nil {
		t.Fatalf("plant the cash on demand customer: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM customers WHERE id = $1::uuid`, custID)
	})

	svc := realPosSvc(t, pool, stubCharger{})
	billingRepo := testBillingRepo(pool)
	orderSvc := order.NewService(order.NewRepository(pool)).WithPickHoldReader(holdBridge{repo: billingRepo})

	// The ring: a deposit as line one, the balance due later, on a fulfilment
	// that waits.
	session := openTill(t, pool, branchID)
	out, err := svc.Sale(ctx, SaleInput{
		IdempotencyKey: key(), BranchID: branchID, CustomerID: custID, TillSessionID: session,
		SaleType: SaleCOD, Fulfilment: "WILL_CALL", Tender: TenderCash,
		DepositCents: 2000, DepositMethod: TenderCash,
		Lines: []LineInput{{ProductID: productID, QtyEntered: 5, UOMEntered: "SF"}},
	})
	if err != nil {
		t.Fatalf("the deposit ring: %v", err)
	}
	balance := out.TotalCents - 2000
	if balance <= 0 {
		t.Fatalf("the proof needs a balance, total %d", out.TotalCents)
	}

	// The facts: held, with the deposit counted as paid and the in process
	// chip, read from the money.
	held, owed, paid, total, err := billingRepo.PickHoldForOrder(ctx, out.OrderID)
	if err != nil || !held || owed != balance || paid != 2000 || total != out.TotalCents {
		t.Fatalf("the facts read (held %v, owed %d, paid %d, total %d, err %v), want held, %d, 2000, %d",
			held, owed, paid, total, err, balance, out.TotalCents)
	}

	// Held at every releasing path.
	if err := orderSvc.SetStatus(ctx, "", "u-hold", out.OrderID, "PICKING", ""); !errors.Is(err, order.ErrHeldFromPicking) {
		t.Fatalf("SetStatus to PICKING on a held order: %v", err)
	}
	if _, err := orderSvc.TransitionManual(ctx, out.OrderID, order.ActionPicking, ""); !errors.Is(err, order.ErrHeldFromPicking) {
		t.Fatalf("the manual picking transition on a held order: %v", err)
	}
	if _, err := orderSvc.ApplyYardPicks(ctx, order.YardPickInput{
		OrderID: out.OrderID, Lines: []order.YardPickLineInput{{LineNo: 1, QtyPicked: "5"}},
	}); !errors.Is(err, order.ErrHeldFromPicking) {
		t.Fatalf("a yard pick on a held order: %v", err)
	}
	// The shipment group's own ladder into PICKING (and so READY) refuses the
	// same hold: a group moves through none of the order's own status writers.
	shipPlan, err := orderSvc.PlanShipment(ctx, out.OrderID, []order.ShipmentGroupInput{{
		Label: "One truck", Lines: []order.ShipmentGroupLineInput{{LineNo: 1, QtyBase: 5}},
	}})
	if err != nil {
		t.Fatalf("plan the held order's shipment: %v", err)
	}
	if len(shipPlan.Groups) != 1 {
		t.Fatalf("the plan carries %d groups, want one", len(shipPlan.Groups))
	}
	if _, err := orderSvc.TransitionGroup(ctx, shipPlan.Groups[0].ID, order.GroupPicking); !errors.Is(err, order.ErrHeldFromPicking) {
		t.Fatalf("a shipment group move to PICKING on a held order: %v", err)
	}
	var groupStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM shipment_groups WHERE id=$1`, shipPlan.Groups[0].ID).
		Scan(&groupStatus); err != nil || groupStatus != "PLANNED" {
		t.Fatalf("the held order's group sits at %s (%v), want PLANNED", groupStatus, err)
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM orders WHERE id=$1`, out.OrderID).Scan(&status); err != nil || status != "CONFIRMED" {
		t.Fatalf("a held order sits at %s (%v), want CONFIRMED", status, err)
	}

	// THE AR REGISTER LIFTS IT: the real register service, the whole balance.
	register := billing.NewRegisterService(billingRepo).
		WithMoney(billing.NewPaymentsReceiptWriter(payments.NewRepository(pool)))
	if _, err := register.RecordPayment(ctx, billing.ReceiptInput{
		InvoiceID: out.InvoiceID, Method: "CASH", AmountCents: balance,
		IdempotencyKey: key(),
	}); err != nil {
		t.Fatalf("the register payment: %v", err)
	}
	held, owed, paid, _, err = billingRepo.PickHoldForOrder(ctx, out.OrderID)
	if err != nil || held || owed != 0 || paid != out.TotalCents {
		t.Fatalf("after the register payment the facts read (held %v, owed %d, paid %d, err %v), want lifted",
			held, owed, paid, err)
	}
	// The same transition that was refused now moves.
	if _, err := orderSvc.TransitionManual(ctx, out.OrderID, order.ActionPicking, ""); err != nil {
		t.Fatalf("the picking transition after payment: %v", err)
	}
}

// A converted cash on demand order owes its whole total before its first
// invoice exists, and the hold reads that (the uninvoiced remainder counts).
func TestIntegration_PickHold_ConvertedCashOnDemandOrderOwesBeforeInvoicing(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	_, branchID := aPaver(t, pool)
	var custID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO customers (account_no, name, customer_type, payment_terms_days, credit_limit_cents)
		VALUES ($1, 'Pick hold converted proof', 'CONTRACTOR', 0, 5000000)
		RETURNING id::text`,
		fmt.Sprintf("PHC-%s", fixtureTag())).Scan(&custID); err != nil {
		t.Fatalf("plant the customer: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM customers WHERE id = $1::uuid`, custID)
	})
	// The order a converted quote becomes: CONFIRMED, nothing invoiced.
	var orderID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, subtotal_cents, tax_cents, total_cents)
		VALUES ($1, $2::uuid, $3::uuid, 'CONFIRMED', 'WILL_CALL', 10000, 1300, 11300)
		RETURNING id::text`,
		fmt.Sprintf("SO-PHC-%s", fixtureTag()), custID, branchID).Scan(&orderID); err != nil {
		t.Fatalf("plant the converted order: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM orders WHERE id = $1::uuid`, orderID)
	})
	billingRepo := testBillingRepo(pool)
	held, owed, paid, total, err := billingRepo.PickHoldForOrder(ctx, orderID)
	if err != nil || !held || owed != 11300 || paid != 0 || total != 11300 {
		t.Fatalf("the uninvoiced cash on demand order reads (held %v, owed %d, paid %d, total %d, err %v), want held owing its whole total",
			held, owed, paid, total, err)
	}
}

// A credit customer's on account order is never held by this rule: it owes on
// terms, which is what terms are.
func TestIntegration_PickHold_CreditCustomerOnAccountNeverHeld(t *testing.T) {
	pool := intPool(t)
	ctx := globalCtx(t, pool)
	productID, branchID := aPaver(t, pool)
	var custID string
	if err := pool.QueryRow(ctx,
		`SELECT id::text FROM customers WHERE customer_type='CONTRACTOR' AND payment_terms_days > 0 AND credit_limit_cents > 1000000 LIMIT 1`).
		Scan(&custID); err != nil {
		t.Skipf("no credit terms CONTRACTOR seeded: %v", err)
	}
	svc := realPosSvc(t, pool, stubCharger{})
	out, err := svc.Sale(ctx, SaleInput{
		IdempotencyKey: key(), BranchID: branchID, CustomerID: custID,
		SaleType: SaleCollectLater, Tender: TenderAccount,
		Lines: []LineInput{{ProductID: productID, QtyEntered: 5, UOMEntered: "SF"}},
	})
	if err != nil {
		t.Fatalf("the on account ring: %v", err)
	}
	billingRepo := testBillingRepo(pool)
	held, owed, _, _, err := billingRepo.PickHoldForOrder(ctx, out.OrderID)
	if err != nil || held || owed != out.TotalCents {
		t.Fatalf("a credit customer's on account order reads (held %v, owed %d, err %v), want owing on terms but never held",
			held, owed, err)
	}
	orderSvc := order.NewService(order.NewRepository(pool)).WithPickHoldReader(holdBridge{repo: billingRepo})
	if _, err := orderSvc.TransitionManual(ctx, out.OrderID, order.ActionPicking, ""); err != nil {
		t.Fatalf("a credit customer's on account order must reach picking: %v", err)
	}
}

// The desk's order payment settles through the payments module's settle; the
// lift is proven through that engine. Two whole balance settles racing lift
// the hold once and count the money once (the concurrency leg).
func TestIntegration_PickHold_PaymentsSettleLiftsOnce_UnderConcurrency(t *testing.T) {
	pool := intPool(t)
	ctx := globalCtx(t, pool)
	productID, branchID := aPaver(t, pool)
	var custID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO customers (account_no, name, customer_type, payment_terms_days, credit_limit_cents)
		VALUES ($1, 'Pick hold race proof', 'CONTRACTOR', 0, 5000000)
		RETURNING id::text`,
		fmt.Sprintf("PHR-%s", fixtureTag())).Scan(&custID); err != nil {
		t.Fatalf("plant the customer: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM customers WHERE id = $1::uuid`, custID)
	})
	svc := realPosSvc(t, pool, stubCharger{})
	session := openTill(t, pool, branchID)
	out, err := svc.Sale(ctx, SaleInput{
		IdempotencyKey: key(), BranchID: branchID, CustomerID: custID, TillSessionID: session,
		SaleType: SaleCOD, Fulfilment: "WILL_CALL", Tender: TenderCash,
		Lines: []LineInput{{ProductID: productID, QtyEntered: 5, UOMEntered: "SF"}},
	})
	if err != nil {
		t.Fatalf("the unpaid ring: %v", err)
	}
	billingRepo := testBillingRepo(pool)
	if held, _, _, _, err := billingRepo.PickHoldForOrder(ctx, out.OrderID); err != nil || !held {
		t.Fatalf("the unpaid order reads (held %v, err %v), want held", held, err)
	}

	// Two whole balance settles, different keys, racing.
	var userID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM users LIMIT 1`).Scan(&userID); err != nil {
		t.Skipf("no seeded user: %v", err)
	}
	settle := func(k string) error {
		_, err := payments.NewRepository(pool).RecordReceipt(ctx, payments.ReceiptRequest{
			CustomerID: custID, Method: "CASH", IdempotencyKey: k, ReceivedBy: userID, BranchID: branchID,
			Allocations: []payments.Allocation{{InvoiceID: out.InvoiceID, AmountCents: out.TotalCents}},
		})
		return err
	}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = settle(key())
		}(i)
	}
	wg.Wait()
	if errs[0] == nil && errs[1] == nil {
		t.Fatalf("two whole balance settles both committed, the money was counted twice")
	}
	if errs[0] != nil && errs[1] != nil {
		t.Fatalf("neither settle committed: %v and %v", errs[0], errs[1])
	}
	held, owed, paid, _, err := billingRepo.PickHoldForOrder(ctx, out.OrderID)
	if err != nil || held || owed != 0 || paid != out.TotalCents {
		t.Fatalf("after the race the facts read (held %v, owed %d, paid %d, err %v), want lifted once",
			held, owed, paid, err)
	}
	var paymentCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM payments p JOIN payment_allocations a ON a.payment_id = p.id WHERE a.invoice_id = $1`,
		out.InvoiceID).Scan(&paymentCount); err != nil || paymentCount != 1 {
		t.Fatalf("the invoice carries %d payments (%v), want exactly 1", paymentCount, err)
	}
}
