//go:build demo

package main

// demoworld_proorders_test.go: proof, through the sync pull HH Pro uses (GET /api/v1/sync/<scope>) as the
// contractor account's own login, that the account's orders reach the device. The contractor orders scope
// reads the plan table and scope_items reads plan_line, and pickups read will_call_pickups of the tenant's
// orders; seeding only orders and order_lines left all three feeds empty (UX-63 finding 1).

import (
	"net/http"
	"testing"
)

type proFeedPage struct {
	Items []struct {
		ID   string         `json:"id"`
		Data map[string]any `json:"data"`
	} `json:"items"`
}

func proFeed(t *testing.T, c *tillClient, scope string) proFeedPage {
	t.Helper()
	rsp := c.call("GET", "/api/v1/sync/"+scope+"?since=0&limit=500", nil)
	rsp.ok(t, http.StatusOK)
	var page proFeedPage
	rsp.decode(t, &page)
	return page
}

func TestDemoWorld_ProOrdersReachTheContractorFeeds(t *testing.T) {
	w := bootDriveWorld(t)
	pro := w.tenant("de2c0000-0000-4000-9000-0000000000a1", contractorCustomerID, "Contractor Owner")

	cards := proFeed(t, pro, "orders")
	stages := map[string]int{}
	byID := map[string]bool{}
	for _, it := range cards.Items {
		byID[it.ID] = true
		s, _ := it.Data["stage"].(string)
		stages[s]++
	}
	if len(cards.Items) < 8 {
		t.Errorf("Contractor Owner: the orders feed serves %d cards, want at least 8", len(cards.Items))
	}
	for _, s := range []string{"plan", "quote", "order", "invoice"} {
		if stages[s] == 0 {
			t.Errorf("Contractor Owner: the orders feed serves no card in stage %q (stages %v)", s, stages)
		}
	}

	lines := proFeed(t, pro, "scope_items")
	if len(lines.Items) < 15 {
		t.Errorf("Contractor Owner: the scope_items feed serves %d lines, want at least 15", len(lines.Items))
	}
	perCard := map[string]int{}
	for _, it := range lines.Items {
		pid, _ := it.Data["plan_id"].(string)
		if !byID[pid] {
			t.Errorf("a scope item points at plan %q the orders feed does not serve", pid)
		}
		perCard[pid]++
	}
	for _, it := range cards.Items {
		if s, _ := it.Data["stage"].(string); s == "order" || s == "invoice" {
			if perCard[it.ID] == 0 {
				t.Errorf("the %s card %q has no lines in the scope_items feed", s, it.ID)
			}
		}
	}

	pickups := proFeed(t, pro, "pickups")
	if len(pickups.Items) < 1 {
		t.Errorf("Contractor Owner: the pickups feed serves %d pickups, want at least 1", len(pickups.Items))
	}
}
