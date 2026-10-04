//go:build integration

// qd1_floor_swap_integration_test.go pins the row identity under the lock
// (P2-A, round 2's finding): a row swapped into the line number inside the
// evaluation window (a delete and a re-add at the same gross) must never
// ride a decision that belongs to a row that is gone. The swap is forced
// with an arbiter double that runs it the moment the decision is being
// made, between the line state read and the guarded write, which is where a
// concurrent writer would land; the guarded write must refuse with the
// changed-line refusal and leave the approval unused and the swapped row
// untouched.
package quote

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pricing"
	"github.com/jackc/pgx/v5/pgxpool"
)

// qd1SwapArbiter runs the concurrent writer's swap inside the evaluation
// window, then answers the deterministic figures.
type qd1SwapArbiter struct {
	qd1Arbiter
	swap func()
}

func (a *qd1SwapArbiter) EvaluateLineFloor(ctx context.Context, in pricing.FloorQuestion) (pricing.FloorAnswer, error) {
	if a.swap != nil {
		a.swap()
	}
	return a.qd1Arbiter.EvaluateLineFloor(ctx, in)
}

// qd1SvcArbiter composes the service with an arbitrary floor arbiter (the
// swap double), the qd1 harness's own composition.
func qd1SvcArbiter(t *testing.T, pool *pgxpool.Pool, now time.Time, arb FloorArbiter) *StaffService {
	t.Helper()
	repo := NewRepository(pool).withNow(func() time.Time { return now })
	aud := audit.NewService(audit.NewRepository(pool), nil)
	return NewStaffService(repo).
		WithAudit(aud).
		WithAuditTrail(aud).
		WithPricing(qPricer{}).
		WithCostBasis(qInv{}).
		WithCustomerType(qCustType{}).
		WithActorNamer(repo).
		WithRoleConfig(qd1Cfg).
		WithFloorArbiter(arb).
		WithFloorRail(identity.NewApprovals(identity.NewApprovalRepository(pool))).
		withCaller(qCallerFrom).
		withNow(func() time.Time { return now })
}

// qd1OtherProduct picks a second active product, the one the swapped row
// carries: the approver decided a discount on one product and the write must
// not land it on another.
func qd1OtherProduct(t *testing.T, pool *pgxpool.Pool, notThis string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(),
		`SELECT id::text FROM products WHERE is_active AND id <> $1 LIMIT 1`, notThis).Scan(&id); err != nil {
		t.Fatalf("pick the second product: %v", err)
	}
	return id
}

// qd1SwapRow runs the review's probe: line 1's row goes away and a new row
// takes its number with ANOTHER product at the same gross, inside the
// evaluation window. discPct pre-seeds the swapped row's stored discount
// (the reprice leg re-checks that figure too, so the row identity is what
// must refuse, not the discount check).
func qd1SwapRow(t *testing.T, pool *pgxpool.Pool, quoteID, otherProd, uom string, discPct int) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`DELETE FROM quote_lines WHERE quote_id = $1 AND line_no = 1`, quoteID); err != nil {
		t.Errorf("the swap's delete: %v", err)
	}
	total := int64(2000)
	if discPct > 0 {
		total = (2000*int64(100-discPct) + 50) / 100
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO quote_lines (quote_id, line_no, product_id, description, qty_entered, uom_entered,
		                         qty_base, unit_price_cents, price_uom, line_total_cents,
		                         disc_pct, pre_discount_total_cents)
		VALUES ($1::uuid, 1, $2::uuid, 'qd1 swapped row', 2, $3, 2, 1000, $3, $4, $5, 2000)`,
		quoteID, otherProd, uom, total, discPct); err != nil {
		t.Errorf("the swap's insert: %v", err)
	}
}

// qd1ApprovalUnused counts the used stamps on one filed request.
func qd1ApprovalUnused(t *testing.T, pool *pgxpool.Pool, requestID string) int {
	t.Helper()
	var used int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM permission_approval_requests WHERE id = $1 AND (data->>'used')::boolean`,
		requestID).Scan(&used); err != nil {
		t.Fatalf("read the request's used stamps: %v", err)
	}
	return used
}

// The review's probe on the approval arm: an approval read a detail naming
// product X, and inside the window line 1 becomes another product at the
// same gross. The write must refuse, the approval must stay unused and the
// swapped row untouched.
func TestQD1_RowSwapInsideTheWindowCannotRideTheApproval(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	seller := qd1User(t, pool, "seller")
	_, quoteID, prod, uom := qd1SeedOwnedQuote(t, pool, "DRAFT", seller, true)
	otherProd := qd1OtherProduct(t, pool, prod)
	lineID, _ := qd1LineBinding(t, pool, quoteID, 1)
	filed := qd1FileAndApprove(t, pool, seller, intTrenton, quoteID, lineID, prod, 1, 40)

	arb := &qd1SwapArbiter{qd1Arbiter: qd1Arbiter{marginBps: 500, floorBps: 1200}}
	arb.records = pricing.NewService(pricing.NewRepository(pool))
	arb.swap = func() { qd1SwapRow(t, pool, quoteID, otherProd, uom, 0) }
	svc := qd1SvcArbiter(t, pool, now, arb)
	ctx := qCtxAs(intStaff(intTrenton))
	ctx = qd1WithRole(ctx, "INSIDE_SALES", intTrenton, seller)

	_, err := svc.SetLineDiscount(ctx, quoteID, 1, 40)
	if !errors.Is(err, errFloorStateChanged) {
		t.Fatalf("a row swapped inside the window must refuse with the changed-line refusal, got %T: %v", err, err)
	}
	// The swapped row is untouched: no discount landed on the other product.
	got := liveLine(t, pool, quoteID, 1)
	if got.ProductID != otherProd || got.LineTotalCents != 2000 {
		t.Fatalf("the swapped row must be untouched at its rule price, got product %s total %d", got.ProductID, got.LineTotalCents)
	}
	// The approval was never spent on the refused write.
	if n := qd1ApprovalUnused(t, pool, filed.ID); n != 0 {
		t.Fatalf("a refused write must leave the approval unused, got %d used stamps", n)
	}
	if rows := qd1BypassRows(t, pool, quoteID); len(rows) != 0 {
		t.Fatalf("a refused write logs nothing, got %v", rows)
	}
}

// The same probe on the standing clearing arm (ladder (a)): the clearing
// stamped on the gone row must not carry a write on the row that reused its
// number, even at the same gross and the same discount.
func TestQD1_RowSwapInsideTheWindowCannotRideTheStandingClearing(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	bm := qd1User(t, pool, "bm")
	_, quoteID, prod, uom := qd1SeedOwnedQuote(t, pool, "DRAFT", bm, true)
	otherProd := qd1OtherProduct(t, pool, prod)

	arb := &qd1SwapArbiter{qd1Arbiter: qd1Arbiter{marginBps: 500, floorBps: 1200}}
	arb.records = pricing.NewService(pricing.NewRepository(pool))
	svc := qd1SvcArbiter(t, pool, now, arb)
	bmCtx := qCtxAs(intStaff(intTrenton))
	bmCtx = qd1WithRole(bmCtx, "BRANCH_MANAGER", intTrenton, bm)

	// The branch manager clears line 1 at 40 percent, the clearing stamped
	// on the row that is about to go away.
	if _, err := svc.SetLineDiscount(bmCtx, quoteID, 1, 40); err != nil {
		t.Fatalf("the branch manager's clearing: %v", err)
	}
	// The swap is armed only now, for the SECOND write, inside its window.
	arb.swap = func() { qd1SwapRow(t, pool, quoteID, otherProd, uom, 40) }

	_, err := svc.SetLineDiscount(bmCtx, quoteID, 1, 40)
	if !errors.Is(err, errFloorStateChanged) {
		t.Fatalf("the standing clearing of a gone row must not carry the write, got %T: %v", err, err)
	}
	got := liveLine(t, pool, quoteID, 1)
	if got.ProductID != otherProd || got.LineTotalCents != 1200 {
		t.Fatalf("the swapped row must be untouched at its seeded discount, got product %s total %d", got.ProductID, got.LineTotalCents)
	}
	// Exactly the first write's BYPASS row: the refused second write added none.
	if rows := qd1BypassRows(t, pool, quoteID); len(rows) != 1 {
		t.Fatalf("the refused write must add no log row, got %v", rows)
	}
}

// The same pin on the quantity reprice arm: the swapped row carries the same
// stored discount (so the discount re-check passes) and the standing
// clearing of the gone row would cover the ask (so nothing else refuses);
// only the row identity refuses, before any approval is spent.
func TestQD1_RowSwapInsideTheWindowCannotRideTheReprice(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	bm := qd1User(t, pool, "bm")
	_, quoteID, prod, uom := qd1SeedOwnedQuote(t, pool, "DRAFT", bm, true)
	otherProd := qd1OtherProduct(t, pool, prod)

	arb := &qd1SwapArbiter{qd1Arbiter: qd1Arbiter{marginBps: 500, floorBps: 1200}}
	arb.records = pricing.NewService(pricing.NewRepository(pool))
	svc := qd1SvcArbiter(t, pool, now, arb)
	bmCtx := qCtxAs(intStaff(intTrenton))
	bmCtx = qd1WithRole(bmCtx, "BRANCH_MANAGER", intTrenton, bm)

	// The branch manager clears line 1 at 40 percent on its own quote.
	if _, err := svc.SetLineDiscount(bmCtx, quoteID, 1, 40); err != nil {
		t.Fatalf("the branch manager's clearing: %v", err)
	}
	// The swap is armed for the reprice: the new row carries the SAME
	// stored discount, so the discount re-check alone would let it through.
	arb.swap = func() { qd1SwapRow(t, pool, quoteID, otherProd, uom, 40) }

	_, err := svc.EditLineQty(bmCtx, quoteID, 1, "5")
	if !errors.Is(err, errFloorStateChanged) {
		t.Fatalf("a reprice must refuse a row swapped inside its window, got %T: %v", err, err)
	}
	got := liveLine(t, pool, quoteID, 1)
	if got.ProductID != otherProd || got.LineTotalCents != 1200 {
		t.Fatalf("the swapped row must be untouched, got product %s total %d", got.ProductID, got.LineTotalCents)
	}
}
