package pos

// branch_fence_sweep_test.go: the branch wall on the till reads and writes the UX-79 sweep found open. A scoped
// caller reaches only the branches it is granted: pricing orders (create, list, read by id), return pickups
// (list, advance) and the quick-pick prices answer nothing for another branch. A by-id read behind the wall
// answers what a missing row answers; a read that names the branch itself is the 403 create sale gives.

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

func scopedAtTrenton(h *harness) {
	h.caller = identity.StaffContext{UserID: "u-scoped", RoleCode: "SALES_MANAGER", Tier: identity.TierManager,
		Scope: identity.ScopeBranch, Branches: map[string]bool{"branch-trt": true}}
}

func statusOf(t *testing.T, err error) int {
	t.Helper()
	var ce clientError
	if !errors.As(err, &ce) {
		t.Fatalf("want a client error, got %v", err)
	}
	return ce.status
}

func TestBranchFence_PricingOrders(t *testing.T) {
	h := newHarness()
	h.store.pricingDocs["doc-trt"] = PricingOrder{ID: "doc-trt", BranchID: "branch-trt"}
	h.store.pricingDocs["doc-kgn"] = PricingOrder{ID: "doc-kgn", BranchID: "branch-kgn"}
	scopedAtTrenton(h)
	ctx := context.Background()

	_, err := h.svc.CreatePricingOrder(ctx, PricingOrderInput{BranchID: "branch-kgn", CustomerID: "cust-abc",
		Lines: []PricingOrderLineInput{{ProductID: "prod-paver", QtyEntered: 1}}})
	if err == nil || statusOf(t, err) != http.StatusForbidden || !strings.Contains(err.Error(), "pos: caller cannot access branch branch-kgn") {
		t.Fatalf("create at another branch: want the 403 words create sale gives, got %v", err)
	}
	if len(h.store.pricingDocs) != 2 {
		t.Fatalf("a refused create wrote a document")
	}
	if _, err := h.svc.ListPricingOrders(ctx, "branch-kgn"); err == nil || statusOf(t, err) != http.StatusForbidden {
		t.Fatalf("list at another branch: want 403, got %v", err)
	}
	if _, err := h.svc.ListPricingOrders(ctx, "branch-trt"); err != nil {
		t.Fatalf("list at the granted branch: %v", err)
	}
	if _, err := h.svc.GetPricingOrder(ctx, "doc-kgn"); err == nil || statusOf(t, err) != http.StatusNotFound {
		t.Fatalf("read by id behind the wall: want what a missing document answers, got %v", err)
	}
	if _, err := h.svc.GetPricingOrder(ctx, "doc-trt"); err != nil {
		t.Fatalf("read by id at the granted branch: %v", err)
	}
}

func TestBranchFence_ReturnPickups(t *testing.T) {
	h := newHarness()
	h.store.pickups["ret-kgn"] = ReturnPickup{ReturnID: "ret-kgn", BranchID: "branch-kgn", PickupStatus: PickupRequested}
	h.store.pickups["ret-trt"] = ReturnPickup{ReturnID: "ret-trt", BranchID: "branch-trt", PickupStatus: PickupRequested}
	scopedAtTrenton(h)
	ctx := context.Background()

	if _, err := h.svc.ListReturnPickups(ctx, "branch-kgn"); err == nil || statusOf(t, err) != http.StatusForbidden {
		t.Fatalf("list at another branch: want 403, got %v", err)
	}
	if _, err := h.svc.ListReturnPickups(ctx, "branch-trt"); err != nil {
		t.Fatalf("list at the granted branch: %v", err)
	}
	if _, err := h.svc.AdvancePickup(ctx, "ret-kgn", PickupScheduled); err == nil || statusOf(t, err) != http.StatusNotFound {
		t.Fatalf("advance behind the wall: want what a missing return answers, got %v", err)
	}
	if h.store.pickups["ret-kgn"].PickupStatus != PickupRequested {
		t.Fatalf("a refused advance moved the pickup")
	}
	if _, err := h.svc.AdvancePickup(ctx, "ret-trt", PickupScheduled); err != nil {
		t.Fatalf("advance at the granted branch: %v", err)
	}
}

func TestBranchFence_QuickPickPricesStayHome(t *testing.T) {
	h, q := counterHarness(t)
	scopedAtTrenton(h)
	if _, err := h.svc.QuickPicks(context.Background(), QuickPickInput{BranchID: "branch-kgn", CustomerID: "cust-abc"}); err != nil {
		t.Fatalf("quick picks stay an ordinary answer: %v", err)
	}
	if len(q.got) != 0 {
		t.Fatalf("the grid priced tiles at another branch for a named account: %+v", q.got)
	}
}
