//go:build integration

package inventory

// reorder_write_fence_integration_test.go: the reorder-points WRITE doors answer the caller's branch
// reach and no other (PR #497 round 1). HH-417 fenced the read; the put and bootstrap routes stayed
// RequireManager only, and procurement_manager passes that gate, so a branch scoped buyer could not
// SEE another yard's managed lines but could REWRITE them, and the shortfall worklist and reorder PO
// drafts are computed from exactly those lines.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"github.com/jackc/pgx/v5/pgxpool"
)

// putReorderPointAs issues the PUT through the handler as a buyer granted the given branches.
func putReorderPointAs(t *testing.T, h *Handler, body []byte, at ...string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := asBuyerAt(httptest.NewRequest(http.MethodPut, "/api/v1/inventory/reorder-points", bytes.NewReader(body)), at...)
	h.putReorderPoint(w, req, guards.StaffCaller{})
	return w
}

// managedLinesAt answers how many managed lines one product carries at one branch.
func managedLinesAt(t *testing.T, pool *pgxpool.Pool, product, branch string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM product_reorder_points WHERE product_id = $1 AND branch_id = $2`,
		product, branch).Scan(&n); err != nil {
		t.Fatalf("counting managed lines: %v", err)
	}
	return n
}

func TestIntegration_PR497_ReorderPointWriteRefusesABranchOutOfReach(t *testing.T) {
	pool := intPool(t)
	dib396LeaseShared(t, pool, dib396StockDriftLease)
	p := mkProduct(t, pool, "H497-W-OUT")
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM product_reorder_points WHERE product_id = $1`, p)
	})
	h := NewHandler(newIntSvc(pool), nil)

	body, _ := json.Marshal(ReorderPoint{
		ProductID: p, BranchID: branch2, ReorderPointBase: 10, ReorderQtyBase: 20, IsActive: true,
	})
	w := putReorderPointAs(t, h, body, branch1)
	if w.Code != http.StatusForbidden {
		t.Fatalf("a put at a branch out of reach answers %d (%s), want 403", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("out of reach: branch")) {
		t.Errorf("the refusal body does not name the fence: %s", w.Body.String())
	}
	if n := managedLinesAt(t, pool, p, branch2); n != 0 {
		t.Errorf("a refused put wrote %d managed lines at the branch out of reach, want 0", n)
	}
}

func TestIntegration_PR497_BootstrapRefusesABranchOutOfReach(t *testing.T) {
	pool := intPool(t)
	dib396LeaseShared(t, pool, dib396StockDriftLease)
	p := mkProduct(t, pool, "H497-B-OUT")
	seedBalance(t, pool, p, branch2, 4, 900)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM product_reorder_points WHERE product_id = $1`, p)
	})
	h := NewHandler(newIntSvc(pool), nil)

	w := httptest.NewRecorder()
	req := asBuyerAt(httptest.NewRequest(http.MethodPost, "/api/v1/inventory/reorder-points/bootstrap",
		bytes.NewReader([]byte(`{"branch_id":"`+branch2+`"}`))), branch1)
	h.bootstrapReorderPoints(w, req, guards.StaffCaller{})
	if w.Code != http.StatusForbidden {
		t.Fatalf("a bootstrap at a branch out of reach answers %d (%s), want 403", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("out of reach: branch")) {
		t.Errorf("the refusal body does not name the fence: %s", w.Body.String())
	}
	if n := managedLinesAt(t, pool, p, branch2); n != 0 {
		t.Errorf("a refused bootstrap wrote %d managed lines at the branch out of reach, want 0", n)
	}
}

func TestIntegration_PR497_ReorderPointWriteAcceptsTheCallersOwnBranch(t *testing.T) {
	pool := intPool(t)
	dib396LeaseShared(t, pool, dib396StockDriftLease)
	p := mkProduct(t, pool, "H497-W-OWN")
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM product_reorder_points WHERE product_id = $1`, p)
	})
	h := NewHandler(newIntSvc(pool), nil)

	body, _ := json.Marshal(ReorderPoint{
		ProductID: p, BranchID: branch1, ReorderPointBase: 30, ReorderQtyBase: 40, IsActive: true,
	})
	w := putReorderPointAs(t, h, body, branch1)
	if w.Code != http.StatusOK {
		t.Fatalf("a put at the caller's own branch answers %d (%s), want 200", w.Code, w.Body.String())
	}
	if n := managedLinesAt(t, pool, p, branch1); n != 1 {
		t.Errorf("an accepted put left %d managed lines at the caller's own branch, want 1", n)
	}
}
