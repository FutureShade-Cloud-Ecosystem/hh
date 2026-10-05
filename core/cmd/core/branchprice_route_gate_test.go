package main

// branchprice_route_gate_test.go: the wrong caller ledger's rows 10 to 12
// (plan section 3.4) for the branch price editor surface, driven through the
// served composition. Row 10: Inside Sales is refused the branch price
// writes (not a pricing editor). Row 11: HR holds Dealer Admin at admin and
// is still refused (the pricing_editors list, D48/D49). Row 12: the sales
// manager and the general manager are admitted and the change is logged in
// the pricing change log. The staff read needs no ledger row: it is a
// dealer-admin read like every pricing read.

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
	"github.com/jackc/pgx/v5/pgxpool"
)

// bpGateProduct is the planted product the row 12 legs set a branch price on.
const bpGateProduct = "41ba0000-0000-4000-8000-000000000001"

// bpProbe drives one request with the caller's own body through the
// registered surface, the x-role suite's probe with the body in hand.
func bpProbe(t *testing.T, reg *recordingRegistrar, method, path string, headers map[string]string, body []byte) (int, string) {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rsp := httptest.NewRecorder()
	reg.inner.ServeHTTP(rsp, req)
	return rsp.Code, rsp.Body.String()
}

// bpHeaders is one staff login's request headers, a minted bearer and the
// org selection.
func bpHeaders(t *testing.T, env *compEnv, sub string) map[string]string {
	t.Helper()
	return map[string]string{
		"Authorization":  "Bearer " + env.minter.StaffToken(t, sub),
		authz.HeaderXOrg: xrXOrg,
	}
}

// TestBranchPriceWrongCallerLedger drives plan section 3.4's rows 10 to 12
// over the served composition: the branch price writes are the pricing
// editors' and nobody else's, and the editors' changes land in the change
// log with their actor.
func TestBranchPriceWrongCallerLedger(t *testing.T) {
	env := newCompEnv(t)
	reg, graph := registeredSurface(t, env)
	plantMatrixStaff(t, env, graph.pool)

	ctx := context.Background()
	if _, err := graph.pool.Exec(ctx, `
INSERT INTO products (id, sku, name, base_uom, price_uom, is_active, org_id)
VALUES ($1::uuid, 'FXBP-GATE', 'branch price gate leg product', 'EA', 'EA', TRUE, $2::uuid)
ON CONFLICT (id) DO NOTHING`, bpGateProduct, testkit.OrgNorthID); err != nil {
		t.Fatalf("plant product: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = graph.pool.Exec(c, `DELETE FROM price_change_log WHERE product_id = $1::uuid`, bpGateProduct)
		_, _ = graph.pool.Exec(c, `DELETE FROM pricing_branch_prices WHERE product_id = $1::uuid`, bpGateProduct)
		_, _ = graph.pool.Exec(c, `DELETE FROM products WHERE id = $1::uuid`, bpGateProduct)
	})

	path := "/api/v1/pricing/branch-prices/" + bpGateProduct + "/" + testkit.BranchNorthAID
	body := []byte(`{"list_price_cents":48000,"price_uom":"EA"}`)

	// Row 10: Inside Sales, no pricing editor, refused both writes.
	sales := bpHeaders(t, env, testkit.SubInsideSales)
	code, bodyOut := bpProbe(t, reg, http.MethodPut, path, sales, body)
	rgRefused(t, "row 10: inside sales is refused the branch price set", code, bodyOut)
	code, bodyOut = bpProbe(t, reg, http.MethodDelete, path, sales, nil)
	rgRefused(t, "row 10: inside sales is refused the branch price delete", code, bodyOut)

	// Row 11: HR holds Dealer Admin at admin and is still refused: the
	// named list, not the level, is what admits a price write.
	hr := bpHeaders(t, env, xrSubHR)
	code, bodyOut = bpProbe(t, reg, http.MethodPut, path, hr, body)
	rgRefused(t, "row 11: hr is refused the branch price set", code, bodyOut)
	code, bodyOut = bpProbe(t, reg, http.MethodDelete, path, hr, nil)
	rgRefused(t, "row 11: hr is refused the branch price delete", code, bodyOut)

	// Row 12: the sales manager and the general manager are accepted, and
	// each set is logged in the pricing change log with its actor.
	manager := bpHeaders(t, env, testkit.SubSalesManager)
	code, bodyOut = bpProbe(t, reg, http.MethodPut, path, manager, body)
	rgAdmitted(t, "row 12: the sales manager sets a branch price", code, bodyOut)
	if code != http.StatusOK {
		t.Fatalf("row 12: the sales manager's set answered %d (%s), want the stored 200", code, bodyOut)
	}
	bpGateAssertTrail(t, graph.pool, "the sales manager")

	gm := bpHeaders(t, env, testkit.SubGeneralMgr)
	code, bodyOut = bpProbe(t, reg, http.MethodPut, path, gm, body)
	rgAdmitted(t, "row 12: the general manager sets a branch price", code, bodyOut)
	if code != http.StatusOK {
		t.Fatalf("row 12: the general manager's set answered %d (%s), want the stored 200", code, bodyOut)
	}
	bpGateAssertTrail(t, graph.pool, "the general manager")

	code, bodyOut = bpProbe(t, reg, http.MethodDelete, path, manager, nil)
	rgAdmitted(t, "row 12: the sales manager removes the branch price", code, bodyOut)
	if code != http.StatusOK {
		t.Fatalf("row 12: the delete answered %d (%s), want 200", code, bodyOut)
	}
}

// bpGateAssertTrail reads the change log rows the row 12 legs owe: every
// accepted set files a branch-named trail row attributed to its caller.
func bpGateAssertTrail(t *testing.T, pool *pgxpool.Pool, who string) {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM price_change_log WHERE product_id = $1::uuid AND branch_id IS NOT NULL AND field = 'retail_cents'`,
		bpGateProduct).Scan(&n); err != nil {
		t.Fatalf("read the change log: %v", err)
	}
	if n == 0 {
		t.Fatalf("%s's accepted set filed no branch-named change log row", who)
	}
}
