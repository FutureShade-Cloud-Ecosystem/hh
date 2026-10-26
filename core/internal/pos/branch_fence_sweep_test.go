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

func TestBranchFence_ReturnRequestsQueue(t *testing.T) {
	h := newHarness()
	h.store.returnRequests["rr-trt"] = ReturnRequest{ID: "rr-trt", RequestNo: "RR-1", BranchID: "branch-trt", Status: RequestPending}
	h.store.returnRequests["rr-kgn"] = ReturnRequest{ID: "rr-kgn", RequestNo: "RR-2", BranchID: "branch-kgn", Status: RequestPending}
	scopedAtTrenton(h)
	ctx := context.Background()

	list, err := h.svc.ListReturnRequests(ctx, "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].ID != "rr-trt" {
		t.Fatalf("the queue shows only the branches the caller reaches: %+v", list)
	}
	// The reach goes into the store's query, so its row limit counts the caller's own rows (UX-89).
	if !h.store.requestReachSet || len(h.store.requestReach) != 1 || h.store.requestReach[0] != "branch-trt" {
		t.Fatalf("the queue read must hand the store the caller's branches, got %v", h.store.requestReach)
	}
	if _, err := h.svc.RejectReturnRequest(ctx, "rr-kgn", "no"); err == nil || statusOf(t, err) != http.StatusNotFound {
		t.Fatalf("reject behind the wall: want what a missing request answers, got %v", err)
	}
	if _, err := h.svc.ApproveReturnRequest(ctx, "rr-kgn"); err == nil || statusOf(t, err) != http.StatusNotFound {
		t.Fatalf("approve behind the wall: want what a missing request answers, got %v", err)
	}
	if h.store.returnRequests["rr-kgn"].Status != RequestPending {
		t.Fatalf("a refused decision moved the request")
	}
	h.caller = identity.StaffContext{UserID: "u-global", RoleCode: "GENERAL_MANAGER", Tier: identity.TierLeadership, Scope: identity.ScopeGlobal}
	all, err := h.svc.ListReturnRequests(ctx, "")
	if err != nil || len(all) != 2 {
		t.Fatalf("a global caller reaches every branch's queue: %v %+v", err, all)
	}
	if h.store.requestReach != nil {
		t.Fatalf("a global caller's read carries no branch list, got %v", h.store.requestReach)
	}
}

// UX-89: a customer the caller cannot reach prices as not found on the counter prices read (with a branch
// named, or none) and on pricing order create, the account read's own answer. An account homed to the
// caller's branch, or to none, is answered as before.
func TestBranchFence_CounterPricesCustomerWall(t *testing.T) {
	h, q := counterHarness(t)
	h.store.customers["cust-kgn"] = customerRec{ID: "cust-kgn", CustomerType: "CONTRACTOR", HomeBranchID: "branch-kgn"}
	h.store.customers["cust-trt"] = customerRec{ID: "cust-trt", CustomerType: "CONTRACTOR", HomeBranchID: "branch-trt"}
	h.store.customers["cust-any"] = customerRec{ID: "cust-any", CustomerType: "CONTRACTOR"}
	scopedAtTrenton(h)
	ctx := context.Background()
	items := []CounterPriceItem{{ProductID: "prod-paver", QtyEntered: 1}}

	for _, branch := range []string{"", "branch-trt"} {
		_, err := h.svc.CounterPrices(ctx, CounterPriceInput{BranchID: branch, CustomerID: "cust-kgn", Items: items})
		if err == nil || statusOf(t, err) != http.StatusNotFound {
			t.Fatalf("an unreachable account (branch %q): want 404, got %v", branch, err)
		}
	}
	if len(q.got) != 0 {
		t.Fatalf("the engine was asked for an unreachable account: %+v", q.got)
	}
	for _, id := range []string{"cust-trt", "cust-any"} {
		if _, err := h.svc.CounterPrices(ctx, CounterPriceInput{BranchID: "branch-trt", CustomerID: id, Items: items}); err != nil {
			t.Fatalf("a reachable account %s is answered: %v", id, err)
		}
	}
	// One answer for every customer taking door: the same status and words as an id that is no account.
	_, err0 := h.svc.CounterPrices(ctx, CounterPriceInput{BranchID: "branch-trt", CustomerID: "cust-kgn", Items: items})
	if err0 == nil || err0.Error() != "customer not found" {
		t.Fatalf("the shared words: got %v", err0)
	}
	_, err := h.svc.CreatePricingOrder(ctx, PricingOrderInput{BranchID: "branch-trt", CustomerID: "cust-kgn",
		Lines: []PricingOrderLineInput{{ProductID: "prod-paver", QtyEntered: 1}}})
	if err == nil || statusOf(t, err) != http.StatusNotFound {
		t.Fatalf("pricing order for an unreachable account: want 404, got %v", err)
	}
}
