//go:build integration

// cost_gate_integration_test.go: the cost legs of the stock reads held
// out by role (X3, the yard fence). The yard team's Buying and Stock is
// on hand at the branch with NO cost; the cost and margin list is the
// one predicate that decides, the same rule the quote and till reads
// already answer through, and a caller off the list receives the cost
// legs byte-absent (the QuoteBranchStock idiom: omitted, never a zero
// that reads as free).
package inventory

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
)

// costGateWorld is one product with a real branch cost, a stocked
// balance and a managed reorder line above it, served by the real
// handlers under a caller the test stamps (the guard's own stamp,
// reached directly so the marshal layer is what runs).
type costGateWorld struct {
	h         *Handler
	productID string
	yard      *http.Request
	clerk     *http.Request
}

func newCostGateWorld(t *testing.T) *costGateWorld {
	t.Helper()
	pool := intPool(t)
	svc := newIntSvc(pool)
	ctx := context.Background()

	nonce := fmt.Sprintf("X3-GATE-%d", time.Now().UnixNano())
	var productID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO products (sku, name, base_uom, price_uom, manufacturer, is_active)
		VALUES ($1, 'Cost Gate Row', 'EA', 'EA', $2, true) RETURNING id::text`,
		nonce, nonce).Scan(&productID); err != nil {
		t.Fatalf("seed product: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM reorder_points WHERE product_id = $1`, productID)
		_, _ = pool.Exec(c, `DELETE FROM stock_balances WHERE product_id = $1`, productID)
		_, _ = pool.Exec(c, `DELETE FROM product_branch_costs WHERE product_id = $1`, productID)
		_, _ = pool.Exec(c, `DELETE FROM products WHERE id = $1`, productID)
	})
	// A real cost on file, a real balance and a managed line above the
	// balance, so the allowed caller receives numbers, the worklist has
	// a row to grade, and the refused caller's absence is the fence,
	// not an empty fact.
	if _, err := pool.Exec(ctx, `
		INSERT INTO product_branch_costs (product_id, branch_id, avg_unit_cost_cents)
		VALUES ($1, $2, 12345)`, productID, branch1); err != nil {
		t.Fatalf("seed branch cost: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO stock_balances (product_id, location_id, branch_id, on_hand_base)
		VALUES ($1, $2, $2, 10)`, productID, branch1); err != nil {
		t.Fatalf("seed balance: %v", err)
	}
	// The managed line carries a preferred vendor: the worklist's own
	// SQL joins the vendor for the name, and a vendor-less line is not a
	// shape it can grade.
	if _, err := svc.SetReorderPoint(ctx, ReorderPoint{
		ProductID: productID, BranchID: branch1, ReorderPointBase: 20, ReorderQtyBase: 20,
		PreferredVendorID: "f5700000-0000-4000-9000-000000000001", IsActive: true,
	}, "d7000000-0000-4000-9000-000000000010"); err != nil {
		t.Fatalf("seed reorder point: %v", err)
	}

	stamped := func(role string) *http.Request {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/api/v1/inventory/browse", nil)
		return r.WithContext(identity.ContextWithStaff(r.Context(), identity.StaffContext{
			RoleCode: role, Tier: identity.TierFrontline, Scope: identity.ScopeBranch,
			Status: identity.StatusActive, Branches: map[string]bool{branch1: true},
		}))
	}
	roles, err := roleconfig.Default()
	if err != nil {
		t.Fatalf("roleconfig.Default: %v", err)
	}
	return &costGateWorld{
		h:         NewHandler(svc, nil).WithCostRoles(roles),
		productID: productID,
		yard:      stamped("YARD_TEAM"),
		clerk:     stamped("INSIDE_SALES"),
	}
}

// withQuery returns req with extra query parameters set.
func withQuery(req *http.Request, kv ...string) *http.Request {
	q := req.URL.Query()
	for i := 0; i+1 < len(kv); i += 2 {
		q.Set(kv[i], kv[i+1])
	}
	out := req.Clone(req.Context())
	out.URL.RawQuery = q.Encode()
	return out
}

// costGateGet drives one guarded handler shape and decodes the JSON
// body, so the assertions ask whether a key is present, the
// byte-absence the fence promises.
func costGateGet(t *testing.T, h func(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller), req *http.Request) map[string]any {
	t.Helper()
	w := httptest.NewRecorder()
	h(w, req, guards.StaffCaller{})
	if w.Code != http.StatusOK {
		t.Fatalf("handler answered %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return body
}

// rowsOf picks the page's row array out of one read's envelope.
func rowsOf(t *testing.T, body map[string]any, key string) []map[string]any {
	t.Helper()
	raw, ok := body[key].([]any)
	if !ok || len(raw) == 0 {
		t.Fatalf("no %s rows in %v", key, body)
	}
	out := make([]map[string]any, 0, len(raw))
	for _, r := range raw {
		m, ok := r.(map[string]any)
		if !ok {
			t.Fatalf("%s row is not an object: %v", key, r)
		}
		out = append(out, m)
	}
	return out
}

// costKeyPresent answers whether the fixture's row carries a leg.
func costKeyPresent(rows []map[string]any, key string) bool {
	for _, r := range rows {
		if _, has := r[key]; has {
			return true
		}
	}
	return false
}

// TestIntegration_CostGate_TheStockReadsHoldCostOutByRole walks the
// reads the yard team reaches through Buying and Stock: browse, branch
// stock, on-hand and the replenishment worklist. The cost legs are
// byte-absent for a yard caller and present for a counter caller on the
// cost and margin list.
func TestIntegration_CostGate_TheStockReadsHoldCostOutByRole(t *testing.T) {
	w := newCostGateWorld(t)

	// stock browse
	yardRows := rowsOf(t, costGateGet(t, w.h.browse, withQuery(w.yard, "text", "Cost Gate Row")), "rows")
	if costKeyPresent(yardRows, "avg_cost_cents") {
		t.Errorf("the yard caller's browse row carries avg_cost_cents: %v", yardRows[0])
	}
	clerkRows := rowsOf(t, costGateGet(t, w.h.browse, withQuery(w.clerk, "text", "Cost Gate Row")), "rows")
	if !costKeyPresent(clerkRows, "avg_cost_cents") {
		t.Errorf("the counter caller's browse row lost avg_cost_cents: %v", clerkRows[0])
	}

	// branch stock
	yardBranches := rowsOf(t, costGateGet(t, w.h.branchStock, withQuery(w.yard, "product_id", w.productID)), "branches")
	if costKeyPresent(yardBranches, "avg_cost_cents") {
		t.Errorf("the yard caller's branch stock carries avg_cost_cents: %v", yardBranches[0])
	}
	clerkBranches := rowsOf(t, costGateGet(t, w.h.branchStock, withQuery(w.clerk, "product_id", w.productID)), "branches")
	if !costKeyPresent(clerkBranches, "avg_cost_cents") {
		t.Errorf("the counter caller's branch stock lost avg_cost_cents: %v", clerkBranches[0])
	}

	// on hand
	yardOnHand := rowsOf(t, costGateGet(t, w.h.onHand, withQuery(w.yard, "product_id", w.productID)), "on_hand")
	if costKeyPresent(yardOnHand, "avg_cost_cents") {
		t.Errorf("the yard caller's on-hand row carries avg_cost_cents: %v", yardOnHand[0])
	}
	clerkOnHand := rowsOf(t, costGateGet(t, w.h.onHand, withQuery(w.clerk, "product_id", w.productID)), "on_hand")
	if !costKeyPresent(clerkOnHand, "avg_cost_cents") {
		t.Errorf("the counter caller's on-hand row lost avg_cost_cents: %v", clerkOnHand[0])
	}

	// the replenishment worklist: avg_cost_cents and the suggested
	// value it derives from are both cost information (value over
	// suggested qty names the branch average), and both hold out.
	yardShort := rowsOf(t, costGateGet(t, w.h.shortfall, withQuery(w.yard, "branch_id", branch1)), "shortfall")
	if costKeyPresent(yardShort, "avg_cost_cents") || costKeyPresent(yardShort, "suggested_value_cents") {
		t.Errorf("the yard caller's shortfall row carries a cost leg: %v", yardShort[0])
	}
	clerkShort := rowsOf(t, costGateGet(t, w.h.shortfall, withQuery(w.clerk, "branch_id", branch1)), "shortfall")
	if !costKeyPresent(clerkShort, "avg_cost_cents") || !costKeyPresent(clerkShort, "suggested_value_cents") {
		t.Errorf("the counter caller's shortfall row lost a cost leg: %v", clerkShort[0])
	}
}

// TestIntegration_CostGate_TheMovesAndTransfersReadsHoldCostOutByRole
// walks the three reads the first cut of the gate missed: the stock
// move ledger and the transfer list and detail. A ship move prices its
// lines at the branch average, so each carries a unit cost a yard
// caller must not receive (byte-absent), while a caller on the cost and
// margin list keeps it.
func TestIntegration_CostGate_TheMovesAndTransfersReadsHoldCostOutByRole(t *testing.T) {
	w := newCostGateWorld(t)
	pool := w.h.svc.repo.pool
	// A shipped transfer leaves the source down and the destination not
	// yet up, the transient drift window the shared lease covers.
	dib396LeaseShared(t, pool, 3960002)
	product := mkProduct(t, pool, "X3-MOVES")
	seedBalance(t, pool, product, branch2, 50, 1900)
	tr := shippedTransfer(t, w.h.svc, branch2, branch1, product, 5)

	// the move ledger
	yardMoves := rowsOf(t, costGateGet(t, w.h.moves, withQuery(w.yard, "product_id", product)), "moves")
	if costKeyPresent(yardMoves, "unit_cost_cents") {
		t.Errorf("the yard caller's move row carries unit_cost_cents: %v", yardMoves[0])
	}
	clerkMoves := rowsOf(t, costGateGet(t, w.h.moves, withQuery(w.clerk, "product_id", product)), "moves")
	if !costKeyPresent(clerkMoves, "unit_cost_cents") {
		t.Errorf("the counter caller's move row lost unit_cost_cents: %v", clerkMoves[0])
	}

	// the transfer list: lines ride each transfer
	linesOf := func(body map[string]any) []map[string]any {
		var out []map[string]any
		for _, tm := range rowsOf(t, body, "transfers") {
			if tm["id"] != tr.ID {
				continue
			}
			raw, _ := tm["lines"].([]any)
			for _, l := range raw {
				if m, ok := l.(map[string]any); ok {
					out = append(out, m)
				}
			}
		}
		if len(out) == 0 {
			t.Fatalf("transfer %s has no lines in %v", tr.ID, body)
		}
		return out
	}
	yardList := linesOf(costGateGet(t, w.h.listTransfers, withQuery(w.yard, "branch_id", branch2, "limit", "200")))
	if costKeyPresent(yardList, "unit_cost_cents") {
		t.Errorf("the yard caller's transfer list carries unit_cost_cents: %v", yardList[0])
	}
	clerkList := linesOf(costGateGet(t, w.h.listTransfers, withQuery(w.clerk, "branch_id", branch2, "limit", "200")))
	if !costKeyPresent(clerkList, "unit_cost_cents") {
		t.Errorf("the counter caller's transfer list lost unit_cost_cents: %v", clerkList[0])
	}

	// the transfer detail
	detail := func(req *http.Request) []map[string]any {
		req.SetPathValue("id", tr.ID)
		w2 := httptest.NewRecorder()
		w.h.getTransfer(w2, req, guards.StaffCaller{})
		if w2.Code != http.StatusOK {
			t.Fatalf("transfer detail answered %d: %s", w2.Code, w2.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(w2.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		return rowsOf(t, body, "lines")
	}
	if costKeyPresent(detail(w.yard.Clone(w.yard.Context())), "unit_cost_cents") {
		t.Errorf("the yard caller's transfer detail carries unit_cost_cents")
	}
	if !costKeyPresent(detail(w.clerk.Clone(w.clerk.Context())), "unit_cost_cents") {
		t.Errorf("the counter caller's transfer detail lost unit_cost_cents")
	}
}
