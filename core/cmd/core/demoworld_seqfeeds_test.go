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
	"context"
	"net/http"
	"testing"

	erpidentity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
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

// feedIDs pulls one scope from cursor zero and returns the ids it served.
func feedIDs(t *testing.T, c *tillClient, scope string) []string {
	t.Helper()
	rsp := c.call("GET", "/api/v1/sync/"+scope+"?since=0&limit=500", nil)
	rsp.ok(t, http.StatusOK)
	var page struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	rsp.decode(t, &page)
	ids := make([]string, 0, len(page.Items))
	for _, it := range page.Items {
		ids = append(ids, it.ID)
	}
	return ids
}

// taskIDsInReach reads the seeded tasks of one type (PICK or NEED_WEIGHT) the persona's branch reach covers, the rule the pick_tasks pull holds to
// (UX-81): the staff authority's branch grants, GLOBAL reaching every pick, a scoped persona only the picks of its
// own branches (a pick with no branch is a GLOBAL caller's alone). It also returns how many sequenced picks the
// seed holds in all, so the proof can show the fence has something to hold back.
func taskIDsInReach(t *testing.T, w *driveWorld, sub, taskType string) (reach map[string]bool, all int) {
	t.Helper()
	ctx := context.Background()
	var orgID string
	if err := w.pool.QueryRow(ctx, `SELECT org_id::text FROM tasks WHERE task_type = 'PICK' AND org_id IS NOT NULL LIMIT 1`).Scan(&orgID); err != nil {
		t.Fatalf("the seed holds no pick with an org: %v", err)
	}
	resolver, err := erpidentity.NewStaffAuthorityResolver(w.pool)
	if err != nil {
		t.Fatalf("authority resolver: %v", err)
	}
	a, has, err := resolver.ResolveStaff(ctx, sub, orgID)
	if err != nil || !has {
		t.Fatalf("the persona %s has no staff authority: %v %v", sub, has, err)
	}
	sc := identity.FromAuthority(a)
	rows, err := w.pool.Query(ctx, `SELECT id::text, COALESCE(branch_id::text, '') FROM tasks
		WHERE task_type = $2 AND org_id = $1::uuid AND seq IS NOT NULL AND deleted_at IS NULL`, orgID, taskType)
	if err != nil {
		t.Fatalf("read the seeded picks: %v", err)
	}
	defer rows.Close()
	reach = map[string]bool{}
	for rows.Next() {
		var id, branch string
		if err := rows.Scan(&id, &branch); err != nil {
			t.Fatalf("scan: %v", err)
		}
		all++
		if sc.Scope == identity.ScopeGlobal || (branch != "" && sc.CanAccessBranch(branch)) {
			reach[id] = true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("seeded picks: %v", err)
	}
	return reach, all
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
			{"shortages", 3}, {"products", 22}, {"inventory_lookup", 22},
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

	// Yard Team's pick_tasks feed is held to the persona's branch reach (UX-81, item 33: the yard persona holds one
	// branch), so the proof counts the seeded picks in that reach rather than a literal: the feed must serve every
	// one of them and no pick from another branch.
	yard := w.as("d0200000-0000-4000-9000-000000000002", "Yard Team")
	reach, all := taskIDsInReach(t, w, "d0200000-0000-4000-9000-000000000002", "PICK")
	if len(reach) == 0 {
		t.Fatal("the seed holds no pick in the yard persona's branch reach: the proof would be empty")
	}
	served := map[string]bool{}
	for _, id := range feedIDs(t, yard, "pick_tasks") {
		served[id] = true
		if !reach[id] {
			t.Errorf("Yard Team: the pick_tasks feed serves pick %s, which is outside the persona's branch reach", id)
		}
	}
	for id := range reach {
		if !served[id] {
			t.Errorf("Yard Team: the pick_tasks feed omits pick %s, which is in the persona's branch reach", id)
		}
	}
	// The weights feed is held to the same reach (UX-81c round 3): the persona's weights queue is the seeded weight
	// tasks of its own branch, every one served and none from another branch.
	weightReach, weightAll := taskIDsInReach(t, w, "d0200000-0000-4000-9000-000000000002", "NEED_WEIGHT")
	if len(weightReach) == 0 || len(weightReach) == weightAll {
		t.Errorf("the seed holds %d weight tasks, %d in the yard persona's reach: the proof needs some in reach and some held back", weightAll, len(weightReach))
	}
	weightServed := map[string]bool{}
	for _, id := range feedIDs(t, yard, "weights") {
		weightServed[id] = true
		if !weightReach[id] {
			t.Errorf("Yard Team: the weights feed serves task %s, which is outside the persona's branch reach", id)
		}
	}
	for id := range weightReach {
		if !weightServed[id] {
			t.Errorf("Yard Team: the weights feed omits task %s, which is in the persona's branch reach", id)
		}
	}
	t.Logf("Yard Team: %d of the seed's %d weight tasks are in the persona's branch reach, and the feed serves %d", len(weightReach), weightAll, len(weightServed))
	t.Logf("Yard Team: %d of the seed's %d picks are in the persona's branch reach, and the feed serves %d", len(reach), all, len(served))

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
