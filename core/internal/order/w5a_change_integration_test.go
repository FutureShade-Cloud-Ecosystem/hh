//go:build integration

// w5a_change_integration_test.go, SCOPE:F10, the gates and the atomic apply, against the live DB.
//
// The three acceptance claims, each of which needs a real database because each is enforced by a
// LOCK or a CONSTRAINT rather than by an if-statement:
//
//	yard ACK required  , apply from PENDING_YARD is refused (guarded UPDATE, not a Go check)
//	$500 threshold     , a >= $500 delta cannot reach APPROVED on a yard ACK alone
//	atomic apply       , lines, totals AND reservations move together, or none of them do
//
// plus the one-open-change rule, which is a partial unique index and therefore cannot be raced.
package order

import (
	"context"
	"errors"
	"sync"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pricing"
	"github.com/jackc/pgx/v5/pgxpool"
)

// - fixtures -

// w5aFixedPricer prices every line at a FIXED unit price, so a test can construct a delta of an
// exact size and the $500 boundary is a property of the test, not of today's price list.
//
// It is a fake of the PRICING seam, not of the change-order logic: the change path still resolves
// every price through the seam (never a caller-supplied number), which is the property that keeps a
// change from smuggling a hand-set rate past the margin gate. What is faked is what the seam
// ANSWERS, so the arithmetic under test is deterministic.
type w5aFixedPricer struct{ unitCents int64 }

func (p w5aFixedPricer) ResolvePrice(_ context.Context, in pricing.ResolveInput) (pricing.Breakdown, error) {
	qty := in.QtyEntered
	return pricing.Breakdown{
		FinalCents:     p.unitCents,
		PriceUOM:       in.UOMEntered,
		QtyBase:        qty,
		LineTotalCents: int64(qty) * p.unitCents,
	}, nil
}

// w5aNoCostBasis satisfies the inventory half of the edit pricing seams.
type w5aNoCostBasis struct{}

func (w5aNoCostBasis) CheckAvailability(context.Context, string, string, float64) (bool, string, error) {
	return true, "", nil
}
func (w5aNoCostBasis) CurrentCostCents(context.Context, string, string) (int64, bool, error) {
	return 0, false, nil
}

// w5aRetailCustomer satisfies the pricing-tier seam.
type w5aRetailCustomer struct{}

func (w5aRetailCustomer) CustomerType(context.Context, string) (string, error) { return "RETAIL", nil }

// w5aPickStarted is the tasks seam saying "the yard has it", what makes a change POST-pick.
type w5aPickStarted struct{ started bool }

func (p w5aPickStarted) PickStarted(context.Context, string) (bool, string, error) {
	return p.started, "", nil
}

// w5aApprover is the gl approvals seam. err non-nil = "you are not a named approver here".
type w5aApprover struct{ err error }

func (a w5aApprover) CheckApprover(context.Context, string, string, string) error { return a.err }

// w5aChangeService builds the order service with every seam a change order needs, wired the way
// main.go wires them (pricing + inventory + customer type + cancel guards + stock + pick + finance).
func w5aChangeService(t *testing.T, unitCents int64, pickStarted bool) *Service {
	t.Helper()
	return w5aChangeServiceOn(t, intPool(t), unitCents, pickStarted)
}

// w5aChangeServiceOn is w5aChangeService over a CALLER-SUPPLIED pool, so the concurrency test can
// hand it one wide enough for four simultaneous applies (see that test for why).
func w5aChangeServiceOn(t *testing.T, pool *pgxpool.Pool, unitCents int64, pickStarted bool) *Service {
	t.Helper()
	svc := w5aService(t, pool)
	svc.WithReorderPricing(w5aFixedPricer{unitCents: unitCents}).
		WithReorderInventory(w5aNoCostBasis{}).
		WithCustomerType(w5aRetailCustomer{}).
		WithCancelGuards(w5aNoInvoices{}, w5aNoSales{}).
		WithPickState(w5aPickStarted{started: pickStarted}).
		WithFinanceApprover(w5aApprover{})
	return svc
}

// w5aRaise raises a change with one QTY delta on line 1.
func w5aRaise(t *testing.T, svc *Service, ctx context.Context, ord string, newQty string) (ChangeOrder, error) {
	t.Helper()
	return svc.RaiseChange(ctx, ord, ChangeRequestInput{
		Reason: "customer asked for more on site",
		Lines:  []ChangeLineInput{{Op: ChangeOpQty, LineNo: 1, QtyEntered: newQty}},
	})
}

// - the gates -

// TestIntegration_W5A_ChangeRefusedWhenThePickHasNotStarted. The whole feature is POST-pick; before
// that a change is an ordinary line edit and the refusal says so, naming the path to use instead.
func TestIntegration_W5A_ChangeRefusedWhenThePickHasNotStarted(t *testing.T) {
	pool := intPool(t)
	_, ord := w5aSeedOrderWithLine(t, pool, "CONFIRMED", 10)
	svc := w5aChangeService(t, 1000, false) // tasks says no pick, and CONFIRMED is not post-pick
	ctx := identity.ContextWithStaff(context.Background(), w5aStaff(true))

	_, err := w5aRaise(t, svc, ctx, ord, "12")
	if !errors.Is(err, ErrChangeNotPostPick) {
		t.Fatalf("raise on a pre-pick order = %v, want ErrChangeNotPostPick, a change order is not a "+
			"second, unguarded line-edit path", err)
	}
}

// TestIntegration_W5A_PickingOrderIsPostPickEvenWithoutATask. The status ladder is the fallback for
// a will-call pulled straight off the rack, which has no task row.
func TestIntegration_W5A_PickingOrderIsPostPickEvenWithoutATask(t *testing.T) {
	pool := intPool(t)
	_, ord := w5aSeedOrderWithLine(t, pool, "PICKING", 10)
	svc := w5aChangeService(t, 1000, false) // tasks is SILENT
	ctx := identity.ContextWithStaff(context.Background(), w5aStaff(true))

	co, err := w5aRaise(t, svc, ctx, ord, "12")
	if err != nil {
		t.Fatalf("a PICKING order must accept a change even with no task row: %v", err)
	}
	if co.PickStartedAtRequest {
		t.Error("pick_started_at_request must record what TASKS said (false), not what the fallback decided")
	}
	if co.OrderStatusAtRequest != "PICKING" {
		t.Errorf("order_status_at_request = %q, want PICKING, the evidence must be stamped", co.OrderStatusAtRequest)
	}
}

// TestIntegration_W5A_ApplyRefusedWithoutTheYardAck is the FIRST acceptance gate. Ruling 3: the ACK
// is always required, whatever the delta is worth.
func TestIntegration_W5A_ApplyRefusedWithoutTheYardAck(t *testing.T) {
	pool := intPool(t)
	_, ord := w5aSeedOrderWithPricedLine(t, pool, "PICKING", 10, 100)
	svc := w5aChangeService(t, 100, true) // $1.00/unit → a $2 delta, far under the threshold
	ctx := identity.ContextWithStaff(context.Background(), w5aStaff(true))

	co, err := w5aRaise(t, svc, ctx, ord, "12")
	if err != nil {
		t.Fatalf("raise: %v", err)
	}
	if co.Status != ChangeStatusPendingYard {
		t.Fatalf("a fresh change is %q, want PENDING_YARD", co.Status)
	}
	if co.FinanceRequired {
		t.Fatalf("a $%0.2f delta must not need finance", float64(co.DeltaCents)/100)
	}
	if _, err := svc.ApplyChange(ctx, co.ID); !errors.Is(err, ErrChangeYardAckRequired) {
		t.Fatalf("apply before the ACK = %v, want ErrChangeYardAckRequired, an immaterial change must "+
			"still not rewrite a staged load behind the yard's back", err)
	}
	// The order's lines must be untouched by the refused apply.
	if got := w5aLineQty(t, pool, ord, 1); got != 10 {
		t.Fatalf("line qty = %v after a refused apply, want the original 10", got)
	}
}

// TestIntegration_W5A_YardAckAloneApprovesAnImmaterialChange, the other half of the same gate.
func TestIntegration_W5A_YardAckAloneApprovesAnImmaterialChange(t *testing.T) {
	pool := intPool(t)
	_, ord := w5aSeedOrderWithPricedLine(t, pool, "PICKING", 10, 100)
	svc := w5aChangeService(t, 100, true)
	ctx := identity.ContextWithStaff(context.Background(), w5aStaff(true))

	co, _ := w5aRaise(t, svc, ctx, ord, "12")
	acked, err := svc.YardAck(ctx, co.ID, "we can still get it on")
	if err != nil {
		t.Fatalf("yard ack: %v", err)
	}
	if acked.Status != ChangeStatusApproved {
		t.Fatalf("status after ACK = %q, want APPROVED for an under-threshold delta", acked.Status)
	}
	if acked.YardAckedAt == "" || acked.YardAckedBy == "" {
		t.Error("the ACK must be attributed and stamped, an unsigned acknowledgement is not one")
	}
}

// TestIntegration_W5A_OverFiveHundredNeedsFinance is the SECOND acceptance gate: the $500 threshold.
func TestIntegration_W5A_OverFiveHundredNeedsFinance(t *testing.T) {
	pool := intPool(t)
	// $60.00/unit: 10 → 20 units is a +$600.00 delta, over the $500 threshold. The SEEDED line must
	// already be priced at that rate, or the delta measures the fixture instead of the change.
	_, ord := w5aSeedOrderWithPricedLine(t, pool, "PICKING", 10, 6000)
	svc := w5aChangeService(t, 6000, true)
	ctx := identity.ContextWithStaff(context.Background(), w5aStaff(true))

	co, err := w5aRaise(t, svc, ctx, ord, "20")
	if err != nil {
		t.Fatalf("raise: %v", err)
	}
	if co.DeltaCents != 60000 {
		t.Fatalf("delta = %d cents, want 60000 (the fixture's arithmetic must be exact for the "+
			"threshold assertion to mean anything)", co.DeltaCents)
	}
	if !co.FinanceRequired {
		t.Fatal("a $600 delta must require finance")
	}
	if co.FinanceThresholdCents != 50000 {
		t.Fatalf("threshold stamped = %d, want 50000, the threshold in force must ride on the row",
			co.FinanceThresholdCents)
	}

	acked, err := svc.YardAck(ctx, co.ID, "")
	if err != nil {
		t.Fatalf("yard ack: %v", err)
	}
	if acked.Status != ChangeStatusPendingFinance {
		t.Fatalf("status after ACK on a material delta = %q, want PENDING_FINANCE, the yard's "+
			"acknowledgement is not a financial approval", acked.Status)
	}
	if _, err := svc.ApplyChange(ctx, co.ID); !errors.Is(err, ErrChangeFinanceRequired) {
		t.Fatalf("apply after ACK but before the signature = %v, want ErrChangeFinanceRequired", err)
	}

	// A NON-manager cannot sign.
	staffCtx := identity.ContextWithStaff(context.Background(), w5aStaff(false))
	if _, err := svc.FinanceApprove(staffCtx, co.ID, ""); !errors.Is(err, ErrForbidden) {
		t.Fatalf("a non-manager signing = %v, want ErrForbidden", err)
	}
	// A manager who is not a NAMED approver for this branch cannot sign either (DIB-361 narrowing).
	svc.WithFinanceApprover(w5aApprover{err: errors.New("not a configured approver")})
	if _, err := svc.FinanceApprove(ctx, co.ID, ""); !errors.Is(err, ErrForbidden) {
		t.Fatalf("a non-named manager signing = %v, want ErrForbidden, the approvals substrate must "+
			"be able to NARROW the manager set", err)
	}
	// A named approver can.
	svc.WithFinanceApprover(w5aApprover{})
	approved, err := svc.FinanceApprove(ctx, co.ID, "ok, bill it")
	if err != nil {
		t.Fatalf("finance approve: %v", err)
	}
	if approved.Status != ChangeStatusApproved || approved.FinanceApprovedAt == "" {
		t.Fatalf("after signing: status=%q at=%q, want APPROVED with a stamp",
			approved.Status, approved.FinanceApprovedAt)
	}
}

// TestIntegration_W5A_ApplyIsAtomicAndReRunsReservations is the THIRD acceptance gate: the delta
// applies atomically AND the holds follow it. Reservations moving with the lines is the property
// that makes a change order safe to run against a confirmed order at all.
func TestIntegration_W5A_ApplyIsAtomicAndReRunsReservations(t *testing.T) {
	pool := intPool(t)
	_, ord := w5aSeedOrderWithPricedLine(t, pool, "DRAFT", 10, 100)
	svc := w5aChangeService(t, 100, true)
	ctx := identity.ContextWithStaff(context.Background(), w5aStaff(true))

	baseReserved := w5aReserved(t, pool)
	if _, err := svc.TransitionManual(ctx, ord, ActionConfirm, ""); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	// Walk it into the pick so the change is genuinely post-pick.
	if _, err := svc.TransitionManual(ctx, ord, ActionPicking, ""); err != nil {
		t.Fatalf("picking: %v", err)
	}
	if _, qty, _ := w5aHolds(t, pool, ord); qty != 10 {
		t.Fatalf("held before the change = %v, want 10", qty)
	}

	co, err := w5aRaise(t, svc, ctx, ord, "25")
	if err != nil {
		t.Fatalf("raise: %v", err)
	}
	if _, err := svc.YardAck(ctx, co.ID, ""); err != nil {
		t.Fatalf("yard ack: %v", err)
	}
	applied, err := svc.ApplyChange(ctx, co.ID)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if applied.Status != ChangeStatusApplied || applied.AppliedAt == "" {
		t.Fatalf("after apply: status=%q at=%q, want APPLIED with a stamp", applied.Status, applied.AppliedAt)
	}

	// 1 · the LINE moved
	if got := w5aLineQty(t, pool, ord, 1); got != 25 {
		t.Fatalf("line qty after apply = %v, want 25", got)
	}
	// 2 · the TOTALS were recomputed from the lines
	var subtotal, total int64
	if err := pool.QueryRow(context.Background(),
		`SELECT subtotal_cents, total_cents FROM orders WHERE id = $1`, ord).Scan(&subtotal, &total); err != nil {
		t.Fatal(err)
	}
	if subtotal != 2500 || total != 2500 {
		t.Fatalf("order money after apply = %d/%d, want 2500/2500 (Σ line_total_cents, never a delta "+
			"increment)", subtotal, total)
	}
	// 3 · the RESERVATIONS followed
	n, held, mode := w5aHolds(t, pool, ord)
	if n != 1 || held != 25 || mode != "HARD" {
		t.Fatalf("holds after apply: %d row(s) for %v (%s), want exactly 1 HARD hold for 25, a change "+
			"that moves the line but not the hold re-opens the oversell it was supposed to control",
			n, held, mode)
	}
	if got := w5aReserved(t, pool) - baseReserved; got != 25 {
		t.Fatalf("qty_reserved moved by %v, want 25 (not 35, the old 10-unit hold must be gone)", got)
	}

	// 4 · RE-ENTRANCY: a second apply is refused, so a double click cannot double-apply.
	if _, err := svc.ApplyChange(ctx, co.ID); !errors.Is(err, ErrChangeTerminal) {
		t.Fatalf("a second apply = %v, want ErrChangeTerminal", err)
	}
	if got := w5aLineQty(t, pool, ord, 1); got != 25 {
		t.Fatalf("line qty after the second apply = %v, want it unchanged at 25", got)
	}
}

// TestIntegration_W5A_OneOpenChangePerOrder, the partial unique index. Two live deltas against the
// same lines cannot be reasoned about by the people signing them.
func TestIntegration_W5A_OneOpenChangePerOrder(t *testing.T) {
	pool := intPool(t)
	_, ord := w5aSeedOrderWithPricedLine(t, pool, "PICKING", 10, 100)
	svc := w5aChangeService(t, 100, true)
	ctx := identity.ContextWithStaff(context.Background(), w5aStaff(true))

	first, err := w5aRaise(t, svc, ctx, ord, "12")
	if err != nil {
		t.Fatalf("first raise: %v", err)
	}
	if _, err := w5aRaise(t, svc, ctx, ord, "14"); !errors.Is(err, ErrChangeOpenExists) {
		t.Fatalf("second raise = %v, want ErrChangeOpenExists", err)
	}
	// Close the first, and a second becomes legal again, the index excludes terminal rows so an
	// order can accumulate a full history.
	if _, err := svc.CancelChange(ctx, first.ID, "withdrawn"); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if _, err := w5aRaise(t, svc, ctx, ord, "14"); err != nil {
		t.Fatalf("raise after the first was withdrawn: %v", err)
	}
}

// TestIntegration_W5A_ApplyFailsLoudlyWhenTheLineVanished. Ruling 1 (the order stays workable) means
// a line genuinely can disappear under a pending change. Silently dropping that half of a signed
// delta would apply something nobody signed, so the whole apply rolls back.
func TestIntegration_W5A_ApplyFailsLoudlyWhenTheLineVanished(t *testing.T) {
	pool := intPool(t)
	_, ord := w5aSeedOrderWithPricedLine(t, pool, "PICKING", 10, 100)
	svc := w5aChangeService(t, 100, true)
	ctx := identity.ContextWithStaff(context.Background(), w5aStaff(true))

	co, err := w5aRaise(t, svc, ctx, ord, "12")
	if err != nil {
		t.Fatalf("raise: %v", err)
	}
	if _, err := svc.YardAck(ctx, co.ID, ""); err != nil {
		t.Fatalf("yard ack: %v", err)
	}
	// The order moved on underneath the change.
	if _, err := pool.Exec(context.Background(),
		`DELETE FROM order_lines WHERE order_id = $1 AND line_no = 1`, ord); err != nil {
		t.Fatalf("remove the line: %v", err)
	}
	if _, err := svc.ApplyChange(ctx, co.ID); !errors.Is(err, ErrLineNotFound) {
		t.Fatalf("apply against a vanished line = %v, want ErrLineNotFound (loud, not silent)", err)
	}
	// The change must NOT have been marked applied by a partially-executed transaction.
	var status string
	if err := pool.QueryRow(context.Background(),
		`SELECT status FROM change_orders WHERE id = $1`, co.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != ChangeStatusApproved {
		t.Fatalf("change status after a failed apply = %q, want it still APPROVED", status)
	}
}

// w5aLineQty reads one order line's qty_base straight from Postgres.
func w5aLineQty(t *testing.T, pool *pgxpool.Pool, orderID string, lineNo int) float64 {
	t.Helper()
	var q float64
	if err := pool.QueryRow(context.Background(),
		`SELECT qty_base FROM order_lines WHERE order_id = $1 AND line_no = $2`, orderID, lineNo).
		Scan(&q); err != nil {
		t.Fatalf("read line qty: %v", err)
	}
	return q
}

// TestIntegration_W5A_ConcurrentApplyLandsExactlyOnce is the RE-TARGET of mutation M7.
//
// The first attempt at that mutation, deleting the APPROVED guard inside ApplyChangeTx, SURVIVED
// the sequential double-apply test, and the reason is instructive: the SERVICE also grades the
// status before it calls, so a second click on a quiet system is caught one layer up. That layer is
// a check-then-write, and it is exactly the layer a race walks through.
//
// This test drives the race directly: two applies fire simultaneously against the SAME APPROVED
// change. Both read APPROVED at the service layer; only one may win, and the winner is decided by
// the guard evaluated under the change row's lock inside the apply transaction. If that guard is
// removed, both transactions apply the same +15 delta and the line lands at 40 instead of 25.
func TestIntegration_W5A_ConcurrentApplyLandsExactlyOnce(t *testing.T) {
	// A WIDE pool, and the reason is worth stating because it bit once already.
	//
	// An apply holds its own transaction connection AND needs a SECOND one while it runs: inventory's
	// ReserveTx resolves the branch allocation policy off its repository POOL, not off the caller's
	// tx. Four racers therefore need eight connections. pgxpool's default MaxConns is
	// max(4, NumCPU), 4 on this two-core box, so with the default pool all four racers hold a
	// transaction and all four wait for a policy read that can never be served. That is a fixture
	// starving itself, not a defect in the code under test, and the fix belongs here.
	//
	// (It IS worth knowing about production: a caller inside a transaction reaching for a second
	// connection is a pool-sizing constraint on every reserve call site. Named in REPORT-W5-A.md.)
	pool := w5aWidePool(t)
	_, ord := w5aSeedOrderWithPricedLine(t, pool, "PICKING", 10, 100)
	svc := w5aChangeServiceOn(t, pool, 100, true)
	ctx := identity.ContextWithStaff(context.Background(), w5aStaff(true))

	co, err := w5aRaise(t, svc, ctx, ord, "25")
	if err != nil {
		t.Fatalf("raise: %v", err)
	}
	if _, err := svc.YardAck(ctx, co.ID, ""); err != nil {
		t.Fatalf("yard ack: %v", err)
	}

	const racers = 4
	var wg sync.WaitGroup
	results := make([]error, racers)
	start := make(chan struct{})
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start // release them together, so they contend for the change row rather than queue
			_, results[i] = svc.ApplyChange(ctx, co.ID)
		}(i)
	}
	close(start)
	wg.Wait()

	winners := 0
	for _, err := range results {
		if err == nil {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("%d of %d concurrent applies succeeded, want exactly 1, the under-lock APPROVED "+
			"guard is what decides this; the service-layer check alone is a check-then-write", winners, racers)
	}
	if got := w5aLineQty(t, pool, ord, 1); got != 25 {
		t.Fatalf("line qty after %d concurrent applies = %v, want 25, the delta landed more than once",
			racers, got)
	}
	// The resync is release-then-reserve, so it is IDEMPOTENT by construction: after the race the
	// order holds its lines exactly once, whichever apply won. (This fixture set PICKING directly
	// rather than walking through confirm, so the holds this asserts were created BY the apply, the
	// repair behaviour the resync is named for, applied to a post-pick order whose goods are
	// genuinely spoken for.)
	n, held, _ := w5aHolds(t, pool, ord)
	if n != 1 || held != 25 {
		t.Fatalf("holds after the race = %d row(s) for %v, want exactly 1 for 25", n, held)
	}
}
