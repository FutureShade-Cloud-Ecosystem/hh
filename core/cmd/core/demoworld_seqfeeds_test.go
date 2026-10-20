//go:build demo

package main

// demoworld_seqfeeds_test.go: proof, through the sync pull the devices use (GET /api/v1/sync/<scope>),
// that the seeded rows reach the feeds of the demo roles the shells run as. The guard in
// demoworld_seqguard_test.go counts unsequenced rows; this file proves the other half, that the
// served pull then answers them, so a table that is sequenced but fenced out by a missing org stamp
// (the branches the joined pulls read) is caught too.
//
// Yard Pick runs on pick_tasks, shortages and weights. HH Pro (field, pay, procurement) is the
// contractor portal login and reads invoices, payments, payment_methods, team, projects, catalog and
// price_matrix as the account's own tenant. Work and Field read the staff feeds below as the roles
// that hold them.

import (
	"net/http"
	"testing"
)

// feedCount pulls one scope from cursor zero and returns how many entities it served.
func feedCount(t *testing.T, c *tillClient, scope string) int {
	t.Helper()
	rsp := c.call("GET", "/api/v1/sync/"+scope+"?since=0&limit=500", nil)
	rsp.ok(t, http.StatusOK)
	var page struct {
		Items []map[string]any `json:"items"`
	}
	rsp.decode(t, &page)
	return len(page.Items)
}

type feedWant struct {
	scope string
	min   int
}

func TestDemoWorld_SeededRowsReachTheDeviceFeeds(t *testing.T) {
	w := bootDriveWorld(t)

	staff := []struct {
		who  string
		sub  string
		want []feedWant
	}{
		// Yard Pick: the picks, the shortage notices and the weights queue, plus the catalogue it looks up.
		{"Yard Team", "d0200000-0000-4000-9000-000000000002", []feedWant{
			{"pick_tasks", 15}, {"shortages", 3}, {"weights", 2}, {"products", 22}, {"inventory_lookup", 22},
			{"customers", 16}, {"drivers", 7}, {"trucks", 7}, {"runs", 9}, {"stops", 22},
		}},
		{"Sales", qwSalesUserID, []feedWant{
			{"customers", 16}, {"quotes", 3}, {"price_matrix", 20}, {"parked_tickets", 2}, {"approvals", 5},
			{"will_call", 4}, {"warranty_claims", 8}, {"purchase_orders", 4}, {"vendors", 11}, {"invoices", 38},
		}},
		{"Sales manager", qwManagerUserID, []feedWant{{"quotes", 8}, {"approvals", 4}}},
		{"Financial Controller", cwControllerUserID, []feedWant{{"approvals", 6}, {"invoices", 38}}},
		{"Driver", driverPersonaUserID, []feedWant{{"runs", 4}, {"stops", 8}}},
	}
	for _, p := range staff {
		c := w.as(p.sub, p.who)
		for _, f := range p.want {
			if got := feedCount(t, c, f.scope); got < f.min {
				t.Errorf("%s: the %s feed serves %d entities, want at least %d", p.who, f.scope, got, f.min)
			}
		}
	}

	// HH Pro: the contractor account's own login, the account admin, who holds every Pro surface.
	pro := w.tenant("de2c0000-0000-4000-9000-0000000000a1", contractorCustomerID, "Contractor Owner")
	for _, f := range []feedWant{
		{"invoices", 4}, {"payments", 2}, {"payment_methods", 2}, {"team", 5}, {"projects", 3},
		{"catalog", 22}, {"price_matrix", 20},
	} {
		if got := feedCount(t, pro, f.scope); got < f.min {
			t.Errorf("Contractor Owner: the %s feed serves %d entities, want at least %d", f.scope, got, f.min)
		}
	}
}
