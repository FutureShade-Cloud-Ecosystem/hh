//go:build integration

package main

// ux88_cross_dealer_integration_test.go, UX-88: the two open stock reads held to the caller's dealer
// org, through the registered surface with every guard real. The setup is the review probe's: one
// product stocked at North A, North B and South A (the other test org), with an average cost at each.
// The stock glance and the availability peek answer the North caller's org only, the peek names no
// branch of another org or of no org, and it serves no cost at all.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

func ux88Plant(t *testing.T, q *qd1bFixture) string {
	t.Helper()
	ctx := context.Background()
	var pid string
	tag := fmt.Sprintf("ux88-%d", time.Now().UnixNano())
	if err := q.pool.QueryRow(ctx,
		`INSERT INTO products (sku, name, base_uom, price_uom) VALUES ($1::text, $1::text, 'EA', 'EA') RETURNING id::text`, tag).Scan(&pid); err != nil {
		t.Fatalf("plant product: %v", err)
	}
	t.Cleanup(func() {
		_, _ = q.pool.Exec(context.Background(), `DELETE FROM product_branch_costs WHERE product_id = $1`, pid)
		_, _ = q.pool.Exec(context.Background(), `DELETE FROM stock_balances WHERE product_id = $1`, pid)
		_, _ = q.pool.Exec(context.Background(), `DELETE FROM products WHERE id = $1`, pid)
	})
	for _, p := range []struct {
		branch, org string
		qty, cost   int
	}{
		{testkit.BranchNorthAID, testkit.OrgNorthID, 11, 1111},
		{testkit.BranchNorthBID, testkit.OrgNorthID, 22, 2222},
		{testkit.BranchSouthAID, testkit.OrgSouthID, 33, 3333},
	} {
		if _, err := q.pool.Exec(ctx,
			`INSERT INTO stock_balances (product_id, location_id, branch_id, on_hand_base, org_id) VALUES ($1, $2, $2, $3, $4)`,
			pid, p.branch, p.qty, p.org); err != nil {
			t.Fatalf("plant stock at %s: %v", p.branch, err)
		}
		if _, err := q.pool.Exec(ctx,
			`INSERT INTO product_branch_costs (product_id, branch_id, avg_unit_cost_cents, org_id) VALUES ($1, $2, $3, $4)`,
			pid, p.branch, p.cost, p.org); err != nil {
			t.Fatalf("plant cost at %s: %v", p.branch, err)
		}
	}
	return pid
}

func TestUX88_StockGlance_HeldToTheCallersOrg(t *testing.T) {
	q := newQD1BFixture(t)
	pid := ux88Plant(t, q)
	for _, sub := range []string{testkit.SubInsideSales, testkit.SubSalesManager, testkit.SubGeneralMgr} {
		code, body := qd1bCall(t, q, http.MethodGet, "/api/v1/pos/stock-glance?product_id="+pid, sub, nil)
		if code != http.StatusOK {
			t.Fatalf("%s: stock glance = %d %s, want 200", sub, code, body)
		}
		var g struct {
			OnHand map[string]float64 `json:"on_hand"`
		}
		if err := json.Unmarshal([]byte(body), &g); err != nil {
			t.Fatalf("%s: decode %s: %v", sub, body, err)
		}
		if _, leak := g.OnHand[testkit.BranchSouthAID]; leak {
			t.Fatalf("%s: the glance names the other org's branch: %s", sub, body)
		}
		if g.OnHand[testkit.BranchNorthAID] != 11 || g.OnHand[testkit.BranchNorthBID] != 22 || len(g.OnHand) != 2 {
			t.Fatalf("%s: glance on_hand = %v, want exactly North A 11 and North B 22", sub, g.OnHand)
		}
	}
}

func TestUX88_AvailabilityPeek_HeldToTheCallersOrgAndServesNoCost(t *testing.T) {
	q := newQD1BFixture(t)
	pid := ux88Plant(t, q)
	ux88PlantNoOrgBranch(t, q)
	for _, sub := range []string{testkit.SubInsideSales, testkit.SubSalesManager, testkit.SubGeneralMgr} {
		code, body := qd1bCall(t, q, http.MethodGet, "/api/v1/quote-availability/"+pid+"/branches", sub, nil)
		if code != http.StatusOK {
			t.Fatalf("%s: peek = %d %s, want 200", sub, code, body)
		}
		var p struct {
			Branches []map[string]any `json:"branches"`
		}
		if err := json.Unmarshal([]byte(body), &p); err != nil {
			t.Fatalf("%s: decode %s: %v", sub, body, err)
		}
		got := map[string]float64{}
		for _, b := range p.Branches {
			for _, k := range []string{"avg_cost_cents", "avg_unit_cost_cents"} {
				if _, has := b[k]; has {
					t.Fatalf("%s: the peek serves %s: %s", sub, k, body)
				}
			}
			got[b["branch_id"].(string)] = b["on_hand_base"].(float64)
		}
		if len(got) != 2 || got[testkit.BranchNorthAID] != 11 || got[testkit.BranchNorthBID] != 22 {
			t.Fatalf("%s: peek branches = %v, want exactly North A 11 and North B 22 (no other org, no null org branch): %s", sub, got, body)
		}
	}
}

func TestUX88_StockBrowse_HeldToTheCallersOrg(t *testing.T) {
	q := newQD1BFixture(t)
	pid := ux88Plant(t, q)
	var sku string
	if err := q.pool.QueryRow(context.Background(), `SELECT sku FROM products WHERE id = $1`, pid).Scan(&sku); err != nil {
		t.Fatalf("read sku: %v", err)
	}
	for _, sub := range []string{testkit.SubInsideSales, testkit.SubSalesManager, testkit.SubGeneralMgr} {
		code, body := qd1bCall(t, q, http.MethodGet, "/api/v1/inventory/browse?text="+sku, sub, nil)
		if code != http.StatusOK {
			t.Fatalf("%s: browse = %d %s, want 200", sub, code, body)
		}
		var res struct {
			Rows []struct {
				ProductID  string  `json:"product_id"`
				OnHandBase float64 `json:"on_hand_base"`
			} `json:"rows"`
		}
		if err := json.Unmarshal([]byte(body), &res); err != nil {
			t.Fatalf("%s: decode %s: %v", sub, body, err)
		}
		if len(res.Rows) != 1 || res.Rows[0].ProductID != pid {
			t.Fatalf("%s: browse rows = %s, want the planted product", sub, body)
		}
		// 11 at North A plus 22 at North B; South A's 33 is another org's stock.
		if res.Rows[0].OnHandBase != 33 {
			t.Fatalf("%s: browse on hand = %v, want 33 (North A 11 plus North B 22, never South A's 33): %s", sub, res.Rows[0].OnHandBase, body)
		}
	}
}

func TestUX88_BranchList_HeldToTheCallersOrg(t *testing.T) {
	q := newQD1BFixture(t)
	ux88PlantNoOrgBranch(t, q)
	for _, sub := range []string{testkit.SubInsideSales, testkit.SubSalesManager, testkit.SubGeneralMgr} {
		code, body := qd1bCall(t, q, http.MethodGet, "/api/v1/branches", sub, nil)
		if code != http.StatusOK {
			t.Fatalf("%s: branch list = %d %s, want 200", sub, code, body)
		}
		var rows []struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal([]byte(body), &rows); err != nil {
			t.Fatalf("%s: decode %s: %v", sub, body, err)
		}
		for _, r := range rows {
			if r.ID != testkit.BranchNorthAID && r.ID != testkit.BranchNorthBID {
				t.Fatalf("%s: the branch list names a branch outside the caller's org: %s", sub, body)
			}
		}
		if sub == testkit.SubGeneralMgr && len(rows) != 2 {
			t.Fatalf("%s: a global caller sees %d branches, want North A and North B: %s", sub, len(rows), body)
		}
	}
}

// ux88NoOrgBranchID is the id of the dedicated branch these legs plant with no org. The migration planted
// branches are not used for it: a demo or populated box stamps them with its org, and a refusal leg built
// on them would lose its teeth the day they carry one.
const ux88NoOrgBranchID = "88a00000-0000-4000-8000-0000000000b1"

// ux88PlantNoOrgBranch plants a branch whose locations row carries no org and returns its id; the row
// leaves with the leg.
func ux88PlantNoOrgBranch(t *testing.T, q *qd1bFixture) string {
	t.Helper()
	if _, err := q.pool.Exec(context.Background(), `
		INSERT INTO locations (id, branch_id, parent_id, loc_type, code, name, address)
		VALUES ($1, $1, NULL, 'BRANCH', 'UX88NO', 'UX88 branch with no org', '{}')
		ON CONFLICT (id) DO NOTHING`, ux88NoOrgBranchID); err != nil {
		t.Fatalf("plant the no org branch: %v", err)
	}
	t.Cleanup(func() {
		_, _ = q.pool.Exec(context.Background(), `DELETE FROM locations WHERE id = $1`, ux88NoOrgBranchID)
	})
	return ux88NoOrgBranchID
}

// Round 2: the named branch form of the stock truth read. A scoped caller must hold the branch (403,
// the yard and branch doors' own shape for a branch out of reach); a GLOBAL caller holds every branch
// of its own org only, and another org's or a no org branch is not found (404), so the answer cannot
// tell a missing branch from one that is not theirs. Neither form serves another org's stock or cost.
func TestUX88_BranchStockNamedBranch_HeldToTheCallersReachAndOrg(t *testing.T) {
	q := newQD1BFixture(t)
	pid := ux88Plant(t, q)
	noOrg := ux88PlantNoOrgBranch(t, q)
	get := func(sub, branch string) (int, string) {
		return qd1bCall(t, q, http.MethodGet, "/api/v1/inventory/branch-stock?product_id="+pid+"&branch_id="+branch, sub, nil)
	}
	for _, c := range []struct {
		sub      string
		branch   string
		want     int
		leakWord string
	}{
		{testkit.SubInsideSales, testkit.BranchSouthAID, http.StatusForbidden, "3333"},
		{testkit.SubInsideSales, noOrg, http.StatusForbidden, ""},
		{testkit.SubInsideSales, testkit.BranchNorthBID, http.StatusForbidden, "2222"},
		{testkit.SubSalesManager, testkit.BranchSouthAID, http.StatusForbidden, "3333"},
		{testkit.SubGeneralMgr, testkit.BranchSouthAID, http.StatusNotFound, "3333"},
		{testkit.SubGeneralMgr, noOrg, http.StatusNotFound, ""},
	} {
		code, body := get(c.sub, c.branch)
		if code != c.want {
			t.Errorf("%s naming %s: %d %s, want %d", c.sub, c.branch, code, body, c.want)
		}
		if c.leakWord != "" && strings.Contains(body, c.leakWord) {
			t.Errorf("%s naming %s leaks another org's figure %s: %s", c.sub, c.branch, c.leakWord, body)
		}
	}
	// The branches the caller does reach still answer.
	if code, body := get(testkit.SubInsideSales, testkit.BranchNorthAID); code != http.StatusOK || !strings.Contains(body, `"on_hand_base":11`) {
		t.Errorf("a granted branch = %d %s, want 200 with North A's 11", code, body)
	}
	if code, body := get(testkit.SubGeneralMgr, testkit.BranchNorthBID); code != http.StatusOK || !strings.Contains(body, `"on_hand_base":22`) {
		t.Errorf("a global caller's own org branch = %d %s, want 200 with North B's 22", code, body)
	}
}

// Round 2: the floor preview naming a branch. The preview grades margins against that branch's cost,
// so the same fence and org hold apply. inside_sales is refused at the manager gate before the body
// is read (403 either way); the scoped manager is refused at the branch fence, a GLOBAL manager gets a
// not found for another org's or a no org branch.
func TestUX88_FloorPreviewNamedBranch_HeldToTheCallersReachAndOrg(t *testing.T) {
	q := newQD1BFixture(t)
	noOrg := ux88PlantNoOrgBranch(t, q)
	post := func(sub, branch string) (int, string) {
		return qd1bCall(t, q, http.MethodPost, "/api/v1/pricing/floor-preview", sub, map[string]any{
			"kind": "category_policy", "category_id": "00000000-0000-4000-8000-0000000000c1", "branch_id": branch,
			"policy": map[string]any{"margin_target_bps": 3000, "margin_floor_bps": 2000},
		})
	}
	for _, c := range []struct {
		sub    string
		branch string
		want   int
	}{
		{testkit.SubInsideSales, testkit.BranchSouthAID, http.StatusForbidden},
		{testkit.SubSalesManager, testkit.BranchSouthAID, http.StatusForbidden},
		{testkit.SubSalesManager, noOrg, http.StatusForbidden},
		{testkit.SubGeneralMgr, testkit.BranchSouthAID, http.StatusNotFound},
		{testkit.SubGeneralMgr, noOrg, http.StatusNotFound},
	} {
		if code, body := post(c.sub, c.branch); code != c.want {
			t.Errorf("%s previewing at %s: %d %s, want %d", c.sub, c.branch, code, body, c.want)
		}
	}
	// A branch of the caller's own org still previews.
	if code, body := post(testkit.SubGeneralMgr, testkit.BranchNorthBID); code != http.StatusOK {
		t.Errorf("a global manager's own org branch = %d %s, want 200", code, body)
	}
	if code, body := post(testkit.SubSalesManager, testkit.BranchNorthAID); code != http.StatusOK {
		t.Errorf("a scoped manager's granted branch = %d %s, want 200", code, body)
	}
}

// Round 2 (P3): the glance answer is keyed by branch. A data defect row whose branch is North A but
// whose location is South A's must not put South A's location id in a North caller's answer.
func TestUX88_StockGlance_KeyedByBranchNotLocation(t *testing.T) {
	q := newQD1BFixture(t)
	ctx := context.Background()
	var pid string
	if err := q.pool.QueryRow(ctx,
		`INSERT INTO products (sku, name, base_uom, price_uom) VALUES ($1::text, $1::text, 'EA', 'EA') RETURNING id::text`,
		fmt.Sprintf("ux88-defect-%d", time.Now().UnixNano())).Scan(&pid); err != nil {
		t.Fatalf("plant product: %v", err)
	}
	t.Cleanup(func() {
		_, _ = q.pool.Exec(context.Background(), `DELETE FROM stock_balances WHERE product_id = $1`, pid)
		_, _ = q.pool.Exec(context.Background(), `DELETE FROM products WHERE id = $1`, pid)
	})
	if _, err := q.pool.Exec(ctx,
		`INSERT INTO stock_balances (product_id, location_id, branch_id, on_hand_base, org_id) VALUES ($1, $2, $3, 55, $4)`,
		pid, testkit.BranchSouthAID, testkit.BranchNorthAID, testkit.OrgNorthID); err != nil {
		t.Fatalf("plant the defect row: %v", err)
	}
	code, body := qd1bCall(t, q, http.MethodGet, "/api/v1/pos/stock-glance?product_id="+pid, testkit.SubInsideSales, nil)
	if code != http.StatusOK {
		t.Fatalf("glance = %d %s, want 200", code, body)
	}
	var g struct {
		OnHand map[string]float64 `json:"on_hand"`
	}
	if err := json.Unmarshal([]byte(body), &g); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	if _, leak := g.OnHand[testkit.BranchSouthAID]; leak {
		t.Fatalf("the glance keys an answer by another org's location id: %s", body)
	}
	if g.OnHand[testkit.BranchNorthAID] != 55 || len(g.OnHand) != 1 {
		t.Fatalf("glance on_hand = %v, want North A alone at 55 (keyed by branch)", g.OnHand)
	}
}
