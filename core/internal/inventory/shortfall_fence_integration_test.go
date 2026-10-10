//go:build integration

package inventory

// shortfall_fence_integration_test.go: the replenishment worklist read answers the caller's branch
// reach and no other (the Opus review's P1-1). The handler used to hand the query's branch_id
// straight to the worklist, so a branch scoped buyer read another branch's rows (and, with the
// draft pointer, another branch's draft PO), and a named branch out of reach was a 200.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// shortfallAt plants one short managed line at a branch on its own product and answers the product.
func shortfallAt(t *testing.T, branch, tag string) string {
	t.Helper()
	pool := intPool(t)
	svc := newIntSvc(pool)
	p := mkProduct(t, pool, tag)
	seedBalance(t, pool, p, branch, 2, 900)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM product_reorder_points WHERE product_id = $1`, p)
	})
	if _, err := svc.SetReorderPoint(context.Background(), ReorderPoint{
		ProductID: p, BranchID: branch, ReorderPointBase: 50, ReorderQtyBase: 60, IsActive: true,
	}, ""); err != nil {
		t.Fatalf("SetReorderPoint: %v", err)
	}
	return p
}

func asBuyerAt(r *http.Request, branches ...string) *http.Request {
	granted := map[string]bool{}
	for _, b := range branches {
		granted[b] = true
	}
	return r.WithContext(identity.ContextWithStaff(r.Context(), identity.StaffContext{
		UserID: "d7000000-0000-4000-9000-000000000010", RoleCode: "PROCUREMENT_MANAGER", Tier: identity.TierManager,
		Scope: identity.ScopeBranch, Status: identity.StatusActive, Branches: granted,
	}))
}

func TestIntegration_P11_TheShortfallReadAnswersTheCallersReachOnly(t *testing.T) {
	pool := intPool(t)
	dib396LeaseShared(t, pool, dib396StockDriftLease)
	own := shortfallAt(t, branch1, "P11-OWN")
	other := shortfallAt(t, branch2, "P11-OTHER")
	h := NewHandler(newIntSvc(pool), nil)

	read := func(query string) (int, []map[string]any) {
		t.Helper()
		w := httptest.NewRecorder()
		req := asBuyerAt(httptest.NewRequest(http.MethodGet, "/api/v1/inventory/shortfall"+query, nil), branch1)
		h.shortfall(w, req, guards.StaffCaller{})
		var body struct {
			Items []map[string]any `json:"items"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		return w.Code, body.Items
	}
	has := func(rows []map[string]any, product string) bool {
		for _, r := range rows {
			if r["product_id"] == product {
				return true
			}
		}
		return false
	}

	code, rows := read("")
	if code != http.StatusOK || !has(rows, own) {
		t.Fatalf("an unnamed read answers %d with the caller's own row present = %v, want 200 and yes", code, has(rows, own))
	}
	if has(rows, other) {
		t.Errorf("an unnamed read carries the other branch's row: a branch scoped buyer read across the fence")
	}
	for _, r := range rows {
		if r["branch_id"] == branch2 {
			t.Errorf("an unnamed read carries a row at the other branch: %v", r["sku"])
		}
	}
	if code, rows := read("?branch_id=" + branch2); code != http.StatusForbidden || len(rows) != 0 {
		t.Errorf("a named branch out of reach answers %d with %d rows, want 403 and none", code, len(rows))
	}
	if code, rows := read("?branch_id=" + branch1); code != http.StatusOK || !has(rows, own) {
		t.Errorf("the caller's own named branch answers %d, own row present = %v", code, has(rows, own))
	}
}
