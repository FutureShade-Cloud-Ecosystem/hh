//go:build integration

// w5a_shipgroup_integration_test.go, DIB-78, the PARTITION INVARIANT, proved at the DATABASE.
//
// The acceptance criterion is "sum of group lines == order lines, no line orphaned, DB constraint
// or tx guard, mutation-checked". This file proves it is the FORMER: a DEFERRABLE INITIALLY DEFERRED
// constraint trigger (0126), which means it holds for ANY writer, this module, a future one, or a
// psql session at 2am. A Go-side guard would bind only Go callers, and every one of the tests below
// bypasses the service entirely and writes raw SQL for exactly that reason.
package order

import (
	"context"
	"errors"
	"strings"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5/pgxpool"
)

// w5aSeedTwoLineOrder adds a SECOND stocked line to the fixture order, so a plan has something real
// to split. Returns the two line ids.
func w5aSeedTwoLineOrder(t *testing.T, pool *pgxpool.Pool, status string) (ord string, lineIDs []string) {
	t.Helper()
	_, ord = w5aSeedOrderWithPricedLine(t, pool, status, 10, 100)
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO order_lines (order_id, line_no, product_id, description, qty_entered, uom_entered,
		                         qty_base, unit_price_cents, price_uom, line_total_cents)
		SELECT $1::uuid, 2, $2::uuid, 'W5A second line', 4, p.base_uom, 4, 100, p.base_uom, 400
		  FROM products p WHERE p.id = $2::uuid`, ord, w5aProduct); err != nil {
		t.Fatalf("seed second line: %v", err)
	}
	rows, err := pool.Query(context.Background(),
		`SELECT id::text FROM order_lines WHERE order_id = $1 ORDER BY line_no`, ord)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		lineIDs = append(lineIDs, id)
	}
	return ord, lineIDs
}

// isPartitionViolation reports the 0126 trigger's refusal. Matched on the message this migration
// itself raises rather than on a bare SQLSTATE, so a DIFFERENT check_violation (say, a negative
// quantity) cannot pass a test that claims to be about coverage.
func isPartitionViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "shipment groups do not partition order")
}

// TestIntegration_W5A_PartialCoverageIsRefusedAtCommit, the ORPHANED-LINE case, the failure this
// whole feature exists to prevent: a line nobody ever loads.
//
// Written in RAW SQL, deliberately. The service's PlanShipment is not the thing under test; the
// DATABASE is, because a partition that only holds when Go asks nicely is not an invariant.
func TestIntegration_W5A_PartialCoverageIsRefusedAtCommit(t *testing.T) {
	pool := intPool(t)
	ord, lineIDs := w5aSeedTwoLineOrder(t, pool, "CONFIRMED")
	ctx := context.Background()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var gid string
	if err := tx.QueryRow(ctx,
		`INSERT INTO shipment_groups (order_id, group_no, label) VALUES ($1,1,'Truck 1') RETURNING id::text`,
		ord).Scan(&gid); err != nil {
		t.Fatalf("insert group: %v", err)
	}
	// Cover line 1 ONLY. Line 2 is the orphan.
	if _, err := tx.Exec(ctx,
		`INSERT INTO shipment_group_lines (shipment_group_id, order_line_id, qty_base)
		 SELECT $1::uuid, id, qty_base FROM order_lines WHERE id = $2::uuid`, gid, lineIDs[0]); err != nil {
		t.Fatalf("insert group line: %v", err)
	}
	// The trigger is DEFERRED, the inserts themselves succeed and the refusal lands at COMMIT, which
	// is the whole reason a partition can be BUILT one statement at a time.
	err = tx.Commit(ctx)
	if !isPartitionViolation(err) {
		t.Fatalf("commit of a partial plan = %v, want the 0126 partition refusal, a line in no group "+
			"is a line nobody loads and nobody notices until the customer calls", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM shipment_groups WHERE order_id = $1`, ord).
		Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("%d group(s) survived the refused commit, want 0", n)
	}
}

// TestIntegration_W5A_DoubleCoverageIsRefusedAtCommit, the other half of "exactly once": the same
// quantity in two groups is stock shipped twice.
func TestIntegration_W5A_DoubleCoverageIsRefusedAtCommit(t *testing.T) {
	pool := intPool(t)
	ord, lineIDs := w5aSeedTwoLineOrder(t, pool, "CONFIRMED")
	ctx := context.Background()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var g1, g2 string
	_ = tx.QueryRow(ctx, `INSERT INTO shipment_groups (order_id, group_no, label) VALUES ($1,1,'A') RETURNING id::text`, ord).Scan(&g1)
	_ = tx.QueryRow(ctx, `INSERT INTO shipment_groups (order_id, group_no, label) VALUES ($1,2,'B') RETURNING id::text`, ord).Scan(&g2)
	// Line 1's FULL quantity in BOTH groups; line 2 covered once.
	for _, g := range []string{g1, g2} {
		if _, err := tx.Exec(ctx,
			`INSERT INTO shipment_group_lines (shipment_group_id, order_line_id, qty_base)
			 SELECT $1::uuid, id, qty_base FROM order_lines WHERE id = $2::uuid`, g, lineIDs[0]); err != nil {
			t.Fatalf("insert group line: %v", err)
		}
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO shipment_group_lines (shipment_group_id, order_line_id, qty_base)
		 SELECT $1::uuid, id, qty_base FROM order_lines WHERE id = $2::uuid`, g1, lineIDs[1]); err != nil {
		t.Fatalf("insert group line: %v", err)
	}
	if err := tx.Commit(ctx); !isPartitionViolation(err) {
		t.Fatalf("commit of a double-covered plan = %v, want the 0126 partition refusal", err)
	}
}

// TestIntegration_W5A_AddingALineToAPlannedOrderIsRefused, the trigger fires on order_lines too.
// This is the case a SCOPE:F10 change-order apply would hit, which is why the two shipped together.
func TestIntegration_W5A_AddingALineToAPlannedOrderIsRefused(t *testing.T) {
	pool := intPool(t)
	ord, _ := w5aSeedTwoLineOrder(t, pool, "CONFIRMED")
	ctx := context.Background()
	w5aPlanWholeOrder(t, pool, ord)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_lines (order_id, line_no, product_id, description, qty_entered, uom_entered,
		                         qty_base, unit_price_cents, price_uom, line_total_cents)
		SELECT $1::uuid, 99, $2::uuid, 'unplanned', 1, p.base_uom, 1, 100, p.base_uom, 100
		  FROM products p WHERE p.id = $2::uuid`, ord, w5aProduct); err != nil {
		t.Fatalf("insert line: %v", err)
	}
	if err := tx.Commit(ctx); !isPartitionViolation(err) {
		t.Fatalf("adding an unplanned line to a planned order = %v, want the 0126 partition refusal, "+
			"this is exactly how a change-order apply would orphan a line", err)
	}
}

// TestIntegration_W5A_OrdersWithNoGroupsAreUnaffected. Multi-ship is OPT-IN: every order written
// before 0126, and every POS ring / quote conversion / portal reorder after it, must be untouched.
// If this fails, the migration broke order writing across the entire product.
func TestIntegration_W5A_OrdersWithNoGroupsAreUnaffected(t *testing.T) {
	pool := intPool(t)
	_, ord := w5aSeedOrderWithPricedLine(t, pool, "CONFIRMED", 10, 100)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		INSERT INTO order_lines (order_id, line_no, product_id, description, qty_entered, uom_entered,
		                         qty_base, unit_price_cents, price_uom, line_total_cents)
		SELECT $1::uuid, 7, $2::uuid, 'ordinary line on an ordinary order', 3, p.base_uom, 3, 100,
		       p.base_uom, 300 FROM products p WHERE p.id = $2::uuid`, ord, w5aProduct); err != nil {
		t.Fatalf("an order with NO shipment groups must accept an ordinary line write: %v", err)
	}
}

// w5aPlanWholeOrder covers every line of the order with one group, through raw SQL.
func w5aPlanWholeOrder(t *testing.T, pool *pgxpool.Pool, ord string) string {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var gid string
	if err := tx.QueryRow(ctx,
		`INSERT INTO shipment_groups (order_id, group_no, label)
		 VALUES ($1, COALESCE((SELECT MAX(group_no) FROM shipment_groups WHERE order_id=$1),0)+1, 'Whole order')
		 RETURNING id::text`, ord).Scan(&gid); err != nil {
		t.Fatalf("plan group: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO shipment_group_lines (shipment_group_id, order_line_id, qty_base)
		 SELECT $1::uuid, id, qty_base FROM order_lines WHERE order_id = $2::uuid`, gid, ord); err != nil {
		t.Fatalf("plan group lines: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("a plan that covers every line must commit: %v", err)
	}
	return gid
}

// TestIntegration_W5A_PlanShipmentSplitsAndReads, the service path end to end: a two-truck split
// commits, the coverage read reports the invariant holding, and each group's money is its share.
func TestIntegration_W5A_PlanShipmentSplitsAndReads(t *testing.T) {
	pool := intPool(t)
	ord, _ := w5aSeedTwoLineOrder(t, pool, "CONFIRMED")
	svc := w5aChangeService(t, 100, true)
	ctx := identity.ContextWithStaff(context.Background(), w5aStaff(true))

	plan, err := svc.PlanShipment(ctx, ord, []ShipmentGroupInput{
		{Label: "Truck 1, Tuesday", Lines: []ShipmentGroupLineInput{{LineNo: 1, QtyBase: 6}}},
		{Label: "Truck 2, Thursday", Lines: []ShipmentGroupLineInput{
			{LineNo: 1, QtyBase: 4}, {LineNo: 2, QtyBase: 4}}},
	})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(plan.Groups) != 2 {
		t.Fatalf("groups = %d, want 2", len(plan.Groups))
	}
	for _, c := range plan.Coverage {
		if !c.Covered {
			t.Errorf("line %d: planned %v of %v, the coverage read disagrees with the constraint that "+
				"just let this commit", c.LineNo, c.PlannedBase, c.OrderQtyBase)
		}
	}
	// The money splits and the shares add back up to the order.
	var sum int64
	for _, g := range plan.Groups {
		sum += g.SubtotalCents
	}
	if sum != 1400 { // line 1: 10 × $1.00 = 1000, line 2: 4 × $1.00 = 400
		t.Fatalf("group subtotals sum to %d, want 1400 (Σ order line totals)", sum)
	}
	// Readiness: nothing is complete, and the DEFAULT policy says one invoice at the end.
	if plan.Readiness.Ready {
		t.Error("a freshly planned order must not be invoiceable")
	}
	if plan.Readiness.Policy != InvoiceOnAllComplete {
		t.Errorf("policy = %q, want the ON_ALL_COMPLETE default", plan.Readiness.Policy)
	}
	if plan.Readiness.WriterBound {
		t.Error("no invoice writer is bound in this build, readiness must say so rather than imply a button")
	}
}

// TestIntegration_W5A_PlanRejectsAnIncompleteSplit, the service refuses a plan that does not cover
// the order, and the refusal carries the constraint's own message naming the line and both
// quantities. The point: the SERVICE does not need its own duplicate arithmetic to be safe.
func TestIntegration_W5A_PlanRejectsAnIncompleteSplit(t *testing.T) {
	pool := intPool(t)
	ord, _ := w5aSeedTwoLineOrder(t, pool, "CONFIRMED")
	svc := w5aChangeService(t, 100, true)
	ctx := identity.ContextWithStaff(context.Background(), w5aStaff(true))

	_, err := svc.PlanShipment(ctx, ord, []ShipmentGroupInput{
		{Label: "Truck 1", Lines: []ShipmentGroupLineInput{{LineNo: 1, QtyBase: 6}}},
	})
	if !errors.Is(err, ErrShipPlanIncomplete) {
		t.Fatalf("plan that leaves 4 units of line 1 and all of line 2 unplanned = %v, want "+
			"ErrShipPlanIncomplete", err)
	}
	if !strings.Contains(err.Error(), "shipment groups do not partition order") {
		t.Errorf("the refusal must carry the constraint's own message so the caller can see WHICH line "+
			"is short: %v", err)
	}
	var n int
	_ = pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM shipment_groups WHERE order_id = $1`, ord).Scan(&n)
	if n != 0 {
		t.Fatalf("%d group(s) survived a refused plan, want 0", n)
	}
}

// TestIntegration_W5A_GroupsDispatchIndependently, the feature's whole point: truck one can be
// COMPLETE while truck two is still PLANNED, and the invoicing decision follows the ruling.
func TestIntegration_W5A_GroupsDispatchIndependently(t *testing.T) {
	pool := intPool(t)
	ord, _ := w5aSeedTwoLineOrder(t, pool, "CONFIRMED")
	svc := w5aChangeService(t, 100, true)
	ctx := identity.ContextWithStaff(context.Background(), w5aStaff(true))

	plan, err := svc.PlanShipment(ctx, ord, []ShipmentGroupInput{
		{Label: "Truck 1", Lines: []ShipmentGroupLineInput{{LineNo: 1, QtyBase: 10}}},
		{Label: "Truck 2", Lines: []ShipmentGroupLineInput{{LineNo: 2, QtyBase: 4}}},
	})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	g1 := plan.Groups[0].ID

	for _, step := range []string{GroupPicking, GroupReady, GroupDispatched, GroupComplete} {
		if _, err := svc.TransitionGroup(ctx, g1, step); err != nil {
			t.Fatalf("group 1 → %s: %v", step, err)
		}
	}
	// A backward move is refused by the ladder.
	if _, err := svc.TransitionGroup(ctx, g1, GroupReady); !errors.Is(err, ErrGroupIllegalTransition) {
		t.Fatalf("un-dispatching a completed truck = %v, want ErrGroupIllegalTransition", err)
	}

	after, err := svc.GetShipmentPlan(ctx, ord)
	if err != nil {
		t.Fatalf("read plan: %v", err)
	}
	if after.Readiness.GroupsComplete != 1 || after.Readiness.GroupsTotal != 2 {
		t.Fatalf("readiness counts = %d/%d, want 1/2",
			after.Readiness.GroupsComplete, after.Readiness.GroupsTotal)
	}
	if after.Readiness.Ready {
		t.Error("ON_ALL_COMPLETE must not bill a half-delivered order")
	}

	// Flip the ruling to progress billing and the SAME state becomes invoiceable, the reversible
	// half of the DIB-78 ruling.
	flipped, err := svc.SetInvoicePolicy(ctx, ord, InvoiceOnDemandSubset)
	if err != nil {
		t.Fatalf("set policy: %v", err)
	}
	if !flipped.Readiness.Ready || flipped.Readiness.InvoiceableCents != 1000 {
		t.Fatalf("after switching to ON_DEMAND_SUBSET: ready=%v invoiceable=%d, want true/1000 "+
			"(truck one's share only)", flipped.Readiness.Ready, flipped.Readiness.InvoiceableCents)
	}
	// And back again, reversible while no invoice has been raised.
	if _, err := svc.SetInvoicePolicy(ctx, ord, InvoiceOnAllComplete); err != nil {
		t.Fatalf("the ruling must be reversible before any money is billed: %v", err)
	}

	// The invoice endpoint refuses LOUDLY rather than fabricating an invoice number (the PARK).
	if _, _, _, err := svc.InvoiceShipments(ctx, ord); err == nil {
		t.Fatal("invoicing must not succeed with no writer bound")
	}
}

// TestIntegration_W5A_ReplanPreservesADispatchedTruck. The truck has gone; rewriting what was on it
// is a lie about a delivery that happened. A re-plan therefore plans THE REMAINDER.
func TestIntegration_W5A_ReplanPreservesADispatchedTruck(t *testing.T) {
	pool := intPool(t)
	ord, _ := w5aSeedTwoLineOrder(t, pool, "CONFIRMED")
	svc := w5aChangeService(t, 100, true)
	ctx := identity.ContextWithStaff(context.Background(), w5aStaff(true))

	plan, err := svc.PlanShipment(ctx, ord, []ShipmentGroupInput{
		{Label: "Truck 1", Lines: []ShipmentGroupLineInput{{LineNo: 1, QtyBase: 10}}},
		{Label: "Truck 2", Lines: []ShipmentGroupLineInput{{LineNo: 2, QtyBase: 4}}},
	})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	g1 := plan.Groups[0].ID
	for _, step := range []string{GroupPicking, GroupReady, GroupDispatched} {
		if _, err := svc.TransitionGroup(ctx, g1, step); err != nil {
			t.Fatalf("group 1 → %s: %v", step, err)
		}
	}

	// Re-plan the REMAINDER. Submitting line 1 again would double-cover it, the constraint says so.
	if _, err := svc.PlanShipment(ctx, ord, []ShipmentGroupInput{
		{Label: "Truck 2 (rescheduled)", Lines: []ShipmentGroupLineInput{{LineNo: 2, QtyBase: 4}}},
	}); err != nil {
		t.Fatalf("re-planning the remainder: %v", err)
	}
	after, err := svc.GetShipmentPlan(ctx, ord)
	if err != nil {
		t.Fatal(err)
	}
	var dispatched int
	for _, g := range after.Groups {
		if g.Status == GroupDispatched {
			dispatched++
			if g.ID != g1 {
				t.Error("the dispatched group was replaced rather than preserved")
			}
		}
	}
	if dispatched != 1 {
		t.Fatalf("dispatched groups after a re-plan = %d, want the original 1 preserved", dispatched)
	}

	// Re-planning line 1 (already on the road) must be refused.
	if _, err := svc.PlanShipment(ctx, ord, []ShipmentGroupInput{
		{Label: "Truck 3", Lines: []ShipmentGroupLineInput{
			{LineNo: 1, QtyBase: 10}, {LineNo: 2, QtyBase: 4}}},
	}); !errors.Is(err, ErrShipPlanIncomplete) {
		t.Fatalf("re-planning a quantity already on a dispatched truck = %v, want the partition "+
			"refusal (it would be shipped twice)", err)
	}
}
