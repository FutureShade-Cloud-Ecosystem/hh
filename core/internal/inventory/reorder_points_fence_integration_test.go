//go:build integration

package inventory

// reorder_points_fence_integration_test.go: the managed-lines read answers the caller's branch reach
// and no other (HH-417), the same fence the shortfall worklist carries. The handler used to hand
// branch_id straight to the repository, so a branch scoped buyer read every branch's managed lines
// and a named branch out of reach was a 200.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
)

// reorderPointAt opens one managed line on its own product at a branch and answers the product.
func reorderPointAt(t *testing.T, branch, tag string) string {
	t.Helper()
	pool := intPool(t)
	svc := newIntSvc(pool)
	p := mkProduct(t, pool, tag)
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

func TestIntegration_HH417_ReorderPointsAnswerTheCallersReachOnly(t *testing.T) {
	pool := intPool(t)
	dib396LeaseShared(t, pool, dib396StockDriftLease)
	const branch2 = "5d1b3f6e-0000-4000-9000-000000000002"
	own := reorderPointAt(t, branch1, "H417-OWN")
	other := reorderPointAt(t, branch2, "H417-OTHER")
	h := NewHandler(newIntSvc(pool), nil)

	read := func(query string) (int, []ReorderPoint) {
		t.Helper()
		w := httptest.NewRecorder()
		req := asBuyerAt(httptest.NewRequest(http.MethodGet, "/api/v1/inventory/reorder-points"+query, nil), branch1)
		h.listReorderPoints(w, req, guards.StaffCaller{})
		if w.Code != http.StatusOK {
			return w.Code, nil
		}
		var body struct {
			ReorderPoints []ReorderPoint `json:"reorder_points"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("the body is not the managed-lines page: %v (%s)", err, w.Body.String())
		}
		return w.Code, body.ReorderPoints
	}
	has := func(rows []ReorderPoint, product string) bool {
		for _, r := range rows {
			if r.ProductID == product {
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
		t.Errorf("an unnamed read carries the other branch's managed line: a branch scoped buyer read across the fence")
	}
	if code, rows := read("?branch_id=" + branch2); code != http.StatusForbidden || len(rows) != 0 {
		t.Errorf("a named branch out of reach answers %d, want 403", code)
	}
	if code, rows := read("?branch_id=" + branch1); code != http.StatusOK || !has(rows, own) {
		t.Errorf("the caller's own named branch answers %d, own row present = %v", code, has(rows, own))
	}
}
