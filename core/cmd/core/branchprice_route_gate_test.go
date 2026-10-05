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
	// The price set refuses a unit the product cannot convert, so the leg's
	// product carries the base unit in its set, as every real product does.
	if _, err := graph.pool.Exec(ctx, `
INSERT INTO product_uoms (product_id, uom_code, qty_in_base, is_base)
VALUES ($1::uuid, 'EA', 1, TRUE) ON CONFLICT (product_id, uom_code) DO NOTHING`, bpGateProduct); err != nil {
		t.Fatalf("plant the product's base unit: %v", err)
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
	// EACH set files exactly one new branch-named trail row attributed to
	// its caller. The two set DIFFERENT prices: an identical re-set is the
	// value-equal arm and files nothing, so the second caller's price must
	// differ or the leg proves nothing about the second caller's logging.
	manager := bpHeaders(t, env, testkit.SubSalesManager)
	code, bodyOut = bpProbe(t, reg, http.MethodPut, path, manager, body)
	rgAdmitted(t, "row 12: the sales manager sets a branch price", code, bodyOut)
	if code != http.StatusOK {
		t.Fatalf("row 12: the sales manager's set answered %d (%s), want the stored 200", code, bodyOut)
	}
	bpGateAssertTrail(t, graph.pool, "the sales manager", 1, testkit.UserSalesMgrID, 48000)

	gm := bpHeaders(t, env, testkit.SubGeneralMgr)
	gmBody := []byte(`{"list_price_cents":53000,"price_uom":"EA"}`)
	code, bodyOut = bpProbe(t, reg, http.MethodPut, path, gm, gmBody)
	rgAdmitted(t, "row 12: the general manager sets a branch price", code, bodyOut)
	if code != http.StatusOK {
		t.Fatalf("row 12: the general manager's set answered %d (%s), want the stored 200", code, bodyOut)
	}
	bpGateAssertTrail(t, graph.pool, "the general manager", 2, testkit.UserGeneralMgrID, 53000)

	code, bodyOut = bpProbe(t, reg, http.MethodDelete, path, manager, nil)
	rgAdmitted(t, "row 12: the sales manager removes the branch price", code, bodyOut)
	if code != http.StatusOK {
		t.Fatalf("row 12: the delete answered %d (%s), want 200", code, bodyOut)
	}
}

// bpGateAssertTrail reads the change log rows the row 12 legs owe: exactly
// wantRows branch-named trail rows for the product, the newest attributed to
// wantChangedBy and carrying wantAfter as its after.
func bpGateAssertTrail(t *testing.T, pool *pgxpool.Pool, who string, wantRows int, wantChangedBy string, wantAfter int64) {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM price_change_log WHERE product_id = $1::uuid AND branch_id IS NOT NULL AND field = 'retail_cents'`,
		bpGateProduct).Scan(&n); err != nil {
		t.Fatalf("read the change log: %v", err)
	}
	if n != wantRows {
		t.Fatalf("%s's accepted set left %d branch-named change log rows, want exactly %d: each caller's set must land its own trail row", who, n, wantRows)
	}
	var changedBy *string
	var after *int64
	if err := pool.QueryRow(context.Background(),
		`SELECT changed_by::text, after_cents FROM price_change_log
		  WHERE product_id = $1::uuid AND branch_id IS NOT NULL AND field = 'retail_cents'
		  ORDER BY created_at DESC, id DESC LIMIT 1`, bpGateProduct).Scan(&changedBy, &after); err != nil {
		t.Fatalf("read the newest trail row: %v", err)
	}
	if changedBy == nil || *changedBy != wantChangedBy {
		got := "NULL"
		if changedBy != nil {
			got = *changedBy
		}
		t.Fatalf("%s's set filed its newest trail row as changed_by %s, want that caller's user id %s", who, got, wantChangedBy)
	}
	if after == nil || *after != wantAfter {
		t.Fatalf("%s's set filed after %v, want %d", who, after, wantAfter)
	}
}
