//go:build integration

// hh182_shipgroup_branch_integration_test.go, HH-182: the branch fence on a shipment-group
// transition must hold BEFORE the write, not after it.
//
// TransitionGroup ran the repository's guarded UPDATE first and only then asked whether the caller
// could reach the group's order. A branch A staffer posting a legal target at a branch B group was
// answered not found while the row moved anyway: DISPATCHED landed with dispatched_at stamped, a
// COMPLETE group became invoiceable, and the audit trail recorded a 404 for a write that committed.
// The group id is a random UUID, so the caller needs an id obtained another way, which is exactly
// what a leaked or guessed id is.
//
// Every leg here runs through the SERVICE against the live database, the same shape the W5-A
// shipgroup suite uses, because the property under test is the ORDER of the fence relative to the
// write, and only the real repository commits anything.
package order

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5/pgxpool"
)

// hh182SeedOrderAtBranch seeds one customer and one CONFIRMED order with a single stocked line at
// `branch`, the W5-A fixture shape with the branch stated. The order carries no reservations and
// moves no stock projection, so it needs no DIB-396 lease; cleanup mirrors the W5-A fixture.
func hh182SeedOrderAtBranch(t *testing.T, pool *pgxpool.Pool, branch string) (cust, ord string) {
	t.Helper()
	ctx := context.Background()
	stamp := time.Now().UnixNano()
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name) VALUES ($1, 'HH-182 Tenant') RETURNING id::text`,
		fmt.Sprintf("HH182-%d", stamp)).Scan(&cust); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, subtotal_cents, total_cents)
		 VALUES ($1, $2, $3, 'CONFIRMED', 'PICKUP', 1000, 1000) RETURNING id::text`,
		fmt.Sprintf("#HH182-%d", stamp), cust, branch).Scan(&ord); err != nil {
		t.Fatalf("seed order: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO order_lines (order_id, line_no, product_id, description, qty_entered, uom_entered,
		                         qty_base, unit_price_cents, price_uom, line_total_cents)
		SELECT $1::uuid, 1, $2::uuid, 'HH-182 stocked line', 10, p.base_uom, 10, 100, p.base_uom, 1000
		  FROM products p WHERE p.id = $2::uuid`, ord, w5aProduct); err != nil {
		t.Fatalf("seed order line: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM shipment_groups WHERE order_id = $1`, ord)
		_, _ = pool.Exec(c, `DELETE FROM orders WHERE id = $1`, ord)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1`, cust)
	})
	return cust, ord
}

// hh182StaffAt builds an active front-line staff context scoped to one branch, the authority the
// route's RequireStaff guard resolves: the service, not the route, is where the branch fence lives.
func hh182StaffAt(branch string) identity.StaffContext {
	return identity.StaffContext{
		UserID: w5aUser, Status: identity.StatusActive, Tier: identity.Tier(4),
		Scope: identity.ScopeBranch, Branches: map[string]bool{branch: true},
	}
}

// hh182GlobalStaff builds an active GLOBAL-scoped caller, who reaches both yards by grant.
func hh182GlobalStaff() identity.StaffContext {
	return identity.StaffContext{
		UserID: w5aUser, Status: identity.StatusActive, Tier: identity.Tier(4),
		Scope: identity.ScopeGlobal,
	}
}

// hh182GroupState reads the group's status and whether the dispatch and completion stamps have been
// set, straight from Postgres: what the DATABASE holds, not what the refused call reported.
func hh182GroupState(t *testing.T, pool *pgxpool.Pool, groupID string) (status string, dispatched, completed bool) {
	t.Helper()
	err := pool.QueryRow(context.Background(),
		`SELECT status, dispatched_at IS NOT NULL, completed_at IS NOT NULL
		   FROM shipment_groups WHERE id = $1`, groupID).
		Scan(&status, &dispatched, &completed)
	if err != nil {
		t.Fatalf("read group state: %v", err)
	}
	return status, dispatched, completed
}

// TestIntegration_HH182_CrossBranchTransitionIsRefusedAndCommitsNothing, THE defect: a branch A
// staffer posts a LEGAL target at a branch B group. The call must answer not found AND the row must
// not move. On the unfixed tree the refusal and the commit both happen: the caller reads 404 while
// the truck record says dispatched, and the stamp says it was dispatched by nobody reachable.
func TestIntegration_HH182_CrossBranchTransitionIsRefusedAndCommitsNothing(t *testing.T) {
	pool := intPool(t)
	_, ord := hh182SeedOrderAtBranch(t, pool, kingstonBranch)
	svc := w5aService(t, pool)
	gid := w5aPlanWholeOrder(t, pool, ord)

	// Walk the group to READY from INSIDE the branch, so the state under attack is one the normal
	// ladder reaches rather than a hand-set status.
	inside := identity.ContextWithStaff(context.Background(), hh182StaffAt(kingstonBranch))
	for _, step := range []string{GroupPicking, GroupReady} {
		if _, err := svc.TransitionGroup(inside, gid, step); err != nil {
			t.Fatalf("in-branch walk to %s: %v", step, err)
		}
	}

	outside := identity.ContextWithStaff(context.Background(), hh182StaffAt(trentonBranch))
	if _, err := svc.TransitionGroup(outside, gid, GroupDispatched); !errors.Is(err, ErrGroupNotFound) {
		t.Fatalf("cross-branch transition = %v, want ErrGroupNotFound, a group the caller cannot "+
			"reach reads as not found exactly like every other order surface", err)
	}
	status, dispatched, completed := hh182GroupState(t, pool, gid)
	if status != GroupReady || dispatched || completed {
		t.Fatalf("after the refused call the group is %s (dispatched=%v completed=%v), want READY "+
			"with no stamps: a 404 that commits the write anyway denies the call while doing the "+
			"thing it denied", status, dispatched, completed)
	}
}

// TestIntegration_HH182_CrossBranchIllegalTargetIsNotFoundNotIllegal, the ordering rule the fence
// implies: an unreachable group answers not found even when the requested target is also illegal
// from its status. Answering the ladder's refusal first would tell a cross-branch caller that the
// group exists and what state it is not in, an existence oracle for another branch's stock.
func TestIntegration_HH182_CrossBranchIllegalTargetIsNotFoundNotIllegal(t *testing.T) {
	pool := intPool(t)
	_, ord := hh182SeedOrderAtBranch(t, pool, kingstonBranch)
	svc := w5aService(t, pool)
	gid := w5aPlanWholeOrder(t, pool, ord) // PLANNED, and COMPLETE is illegal from there

	outside := identity.ContextWithStaff(context.Background(), hh182StaffAt(trentonBranch))
	if _, err := svc.TransitionGroup(outside, gid, GroupComplete); !errors.Is(err, ErrGroupNotFound) {
		t.Fatalf("cross-branch illegal target = %v, want ErrGroupNotFound, the ladder's own refusal "+
			"must not answer a caller the fence has already turned away", err)
	}
	if status, _, _ := hh182GroupState(t, pool, gid); status != GroupPlanned {
		t.Fatalf("after the refused call the group is %s, want PLANNED, an illegal target from an "+
			"unreachable branch must move nothing", status)
	}
}

// TestIntegration_HH182_InBranchLegsUnchanged, the fence must cost an in-branch caller nothing: a
// legal transition still lands with its stamp, and an illegal target still answers the ladder's own
// refusal with the row untouched.
func TestIntegration_HH182_InBranchLegsUnchanged(t *testing.T) {
	pool := intPool(t)
	_, ord := hh182SeedOrderAtBranch(t, pool, kingstonBranch)
	svc := w5aService(t, pool)
	gid := w5aPlanWholeOrder(t, pool, ord)
	ctx := identity.ContextWithStaff(context.Background(), hh182StaffAt(kingstonBranch))

	for _, step := range []string{GroupPicking, GroupReady, GroupDispatched} {
		if _, err := svc.TransitionGroup(ctx, gid, step); err != nil {
			t.Fatalf("in-branch %s: %v", step, err)
		}
	}
	status, dispatched, _ := hh182GroupState(t, pool, gid)
	if status != GroupDispatched || !dispatched {
		t.Fatalf("after the legal ladder the group is %s (dispatched=%v), want DISPATCHED with the "+
			"stamp set", status, dispatched)
	}

	if _, err := svc.TransitionGroup(ctx, gid, GroupReady); !errors.Is(err, ErrGroupIllegalTransition) {
		t.Fatalf("un-dispatching a dispatched group = %v, want ErrGroupIllegalTransition", err)
	}
	if status, _, completed := hh182GroupState(t, pool, gid); status != GroupDispatched || completed {
		t.Fatalf("after the refused backward move the group is %s (completed=%v), want DISPATCHED "+
			"unchanged", status, completed)
	}
}

// TestIntegration_HH182_GlobalCallerTransitionsAnyBranch, the other leg the fence must not break:
// a GLOBAL-scoped caller reaches both yards, and the same call lands.
func TestIntegration_HH182_GlobalCallerTransitionsAnyBranch(t *testing.T) {
	pool := intPool(t)
	_, ord := hh182SeedOrderAtBranch(t, pool, kingstonBranch)
	svc := w5aService(t, pool)
	gid := w5aPlanWholeOrder(t, pool, ord)
	ctx := identity.ContextWithStaff(context.Background(), hh182GlobalStaff())

	if _, err := svc.TransitionGroup(ctx, gid, GroupPicking); err != nil {
		t.Fatalf("global caller transition: %v", err)
	}
	if status, _, _ := hh182GroupState(t, pool, gid); status != GroupPicking {
		t.Fatalf("group is %s after the global caller's legal move, want PICKING", status)
	}
}
