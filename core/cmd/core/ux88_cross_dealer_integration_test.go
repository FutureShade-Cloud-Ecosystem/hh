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
