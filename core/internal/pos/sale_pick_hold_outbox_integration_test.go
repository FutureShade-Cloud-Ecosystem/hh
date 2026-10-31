//go:build integration

package pos

// sale_pick_hold_outbox_integration_test.go, item 9 Opus P2-2: the outbox is
// drained through the REAL consumers after a Cash On Demand deposit ring, the
// shape a counter lives. Before the fix, the lifecycle observer's
// ar.invoice.finalized advance jumped the owing order from CONFIRMED to
// INVOICED within a dispatcher tick, so the order read INVOICED instead of in
// process and its doors answered "illegal status transition" instead of the
// hold sentence. The leg walks the whole ruling: ring, drain, still CONFIRMED
// and held and in process, every door refused with the mandated sentence,
// take the balance, drain again, the doors open.

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
	"github.com/jackc/pgx/v5/pgxpool"
)

// drainTheOutbox drains the dispatcher until the outbox quiesces (bounded),
// mirroring the worker's ticker (order/lifecycle_integration_test.go's shape).
func drainTheOutbox(t *testing.T, d *notify.Dispatcher) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < 16; i++ {
		if d.DrainOnce(ctx) == 0 {
			return
		}
	}
	t.Fatalf("outbox did not quiesce after 16 drain passes")
}

func TestIntegration_PickHold_OutboxDrainLeavesTheOwingOrderInProcess(t *testing.T) {
	pool := intPool(t)
	ctx := globalCtx(t, pool)
	productID, branchID := aPaver(t, pool)

	// A cash on demand account: zero day terms with a credit line, unique per
	// run so the leg is rerunnable.
	var custID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO customers (account_no, name, customer_type, payment_terms_days, credit_limit_cents)
		VALUES ($1, 'Outbox hold proof', 'CONTRACTOR', 0, 5000000)
		RETURNING id::text`,
		fmt.Sprintf("P2B-%s", fixtureTag())).Scan(&custID); err != nil {
		t.Fatalf("plant the cash on demand customer: %v", err)
	}

	svc := realPosSvc(t, pool, stubCharger{})
	billingRepo := testBillingRepo(pool)
	orderSvc := order.NewService(order.NewRepository(pool)).WithPickHoldReader(holdBridge{repo: billingRepo})

	// The dispatcher exactly as the worker wires it: a no-op primary owner
	// stands in for GL on the finance topics (GL owns them in production),
	// and the order module's lifecycle consumer registers as the observer,
	// so the drain drives the real onInvoiceFinalized.
	dispatcher := notify.NewDispatcher(notify.NewPgOutboxStore(pool), nil)
	noop := func(context.Context, notify.Event) error { return nil }
	dispatcher.Register("order.fulfilled", noop)
	dispatcher.Register("ar.invoice.finalized", noop)
	dispatcher.Register("payment.received", noop)
	order.NewLifecycleConsumer(orderSvc, billingRepo, nil).RegisterHandlers(dispatcher)

	// The ring: a deposit as line one, the balance due later, on a will call
	// fulfilment that waits for the money.
	session := openTill(t, pool, branchID)
	ring, err := svc.Sale(ctx, SaleInput{
		IdempotencyKey: key(), BranchID: branchID, CustomerID: custID, TillSessionID: session,
		SaleType: SaleCOD, Fulfilment: "WILL_CALL", Tender: TenderCash,
		DepositCents: 2000, DepositMethod: TenderCash,
		Lines: []LineInput{{ProductID: productID, QtyEntered: 5, UOMEntered: "SF"}},
	})
	if err != nil {
		t.Fatalf("the deposit ring: %v", err)
	}
	balance := ring.TotalCents - 2000
	if balance <= 0 {
		t.Fatalf("the proof needs a balance, total %d", ring.TotalCents)
	}

	// THE DRAIN, through the real consumers.
	drainTheOutbox(t, dispatcher)

	// The order is where the ruling says: still CONFIRMED (not jumped to
	// INVOICED by the invoice event), held, with the deposit counted as paid
	// and the balance owed (the in process shape).
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM orders WHERE id = $1::uuid`, ring.OrderID).Scan(&status); err != nil {
		t.Fatalf("read the order's status: %v", err)
	}
	if status != "CONFIRMED" {
		t.Fatalf("after the drain the order is %s, want CONFIRMED (in process, not jumped past the ladder)", status)
	}
	held, owed, paid, _, err := billingRepo.PickHoldForOrder(ctx, ring.OrderID)
	if err != nil || !held || owed != balance || paid != 2000 {
		t.Fatalf("the facts after the drain (held %v, owed %d, paid %d, err %v), want held, %d, 2000",
			held, owed, paid, err, balance)
	}

	// Every door refuses with the mandated sentence.
	door := func(name string, err error) {
		t.Helper()
		if !errors.Is(err, order.ErrHeldFromPicking) {
			t.Fatalf("%s on the held order: %v (want the hold sentence)", name, err)
		}
	}
	door("SetStatus to PICKING", orderSvc.SetStatus(ctx, "", "u-p2b", ring.OrderID, "PICKING", ""))
	_, err = orderSvc.TransitionManual(ctx, ring.OrderID, order.ActionPicking, "")
	door("the manual picking transition", err)
	_, err = orderSvc.ApplyYardPicks(ctx, order.YardPickInput{
		OrderID: ring.OrderID, Lines: []order.YardPickLineInput{{LineNo: 1, QtyPicked: "5"}},
	})
	door("the yard pick", err)
	door("the event FULFILLED advance", orderSvc.AdvanceFromEvent(ctx, ring.OrderID, "FULFILLED", "evt-p2b-ful"))
	door("the event INVOICED advance", orderSvc.AdvanceFromEvent(ctx, ring.OrderID, "INVOICED", "evt-p2b-inv"))

	// Take the balance at this door, drain again.
	take, err := svc.PayBalanceTake(ctx, PayBalanceTakeInput{
		OrderID: ring.OrderID, Method: TenderCash, AmountCents: balance,
		TillSessionID: session, IdempotencyKey: key(),
	})
	if err != nil {
		t.Fatalf("take the balance: %v", err)
	}
	if take.PaidWords != "Paid in full" || take.StillOwedCents != 0 {
		t.Fatalf("the take's answer: %+v", take)
	}
	drainTheOutbox(t, dispatcher)

	// The doors open.
	if _, err := orderSvc.TransitionManual(ctx, ring.OrderID, order.ActionPicking, ""); err != nil {
		t.Fatalf("the picking transition after the balance: %v", err)
	}
	if got := liveOrderStatus(t, pool, ring.OrderID); got != "PICKING" {
		t.Fatalf("the order is %s after the doors opened, want PICKING", got)
	}
}

func liveOrderStatus(t *testing.T, pool *pgxpool.Pool, orderID string) string {
	t.Helper()
	var s string
	if err := pool.QueryRow(context.Background(), `SELECT status FROM orders WHERE id = $1::uuid`, orderID).Scan(&s); err != nil {
		t.Fatalf("read status: %v", err)
	}
	return s
}
