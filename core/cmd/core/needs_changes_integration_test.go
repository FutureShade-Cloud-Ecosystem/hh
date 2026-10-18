package main

// needs_changes_integration_test.go, HH-681 Needs You: through the REAL composition root (dashFold: wireMoney,
// wireOperations and wireTops behind the kit's staff guards) the Needs You queue carries the change orders
// waiting on the manager. It proves what only the root and the migration can get wrong together: the
// adapter is registered, migration 0411 put the source on the manager shapes, the count is the caller's own
// (a branch manager's stops at their branch), and a counter clerk never sees the row.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/dashboard"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// plantChange plants one post pick order with one open change at the given status, deleting both by id.
func plantChange(t *testing.T, pool *pgxpool.Pool, cust, branch, status string) string {
	t.Helper()
	ctx := context.Background()
	tag := time.Now().UnixNano()
	var ord, co string
	if err := pool.QueryRow(ctx, `
		INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment)
		VALUES ($1, $2::uuid, $3::uuid, 'PICKING', 'PICKUP') RETURNING id::text`,
		"#NY-"+status+"-"+time.Now().Format("150405.000000"), cust, branch).Scan(&ord); err != nil {
		t.Fatalf("plant order (%d): %v", tag, err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO change_orders (order_id, branch_id, change_no, status, reason, delta_cents,
		                           finance_required, order_status_at_request, pick_started_at_request)
		VALUES ($1::uuid, $2::uuid, '#CO-' || nextval('change_order_no_seq'), $3, 'needs you', 60000,
		        $3 <> 'APPROVED', 'PICKING', true) RETURNING id::text`, ord, branch, status).Scan(&co); err != nil {
		t.Fatalf("plant change: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM change_orders WHERE order_id = $1::uuid`, ord)
		_, _ = pool.Exec(c, `DELETE FROM orders WHERE id = $1::uuid`, ord)
	})
	return co
}

// changesWaitingRow reads the queue and returns the change orders row's count, or -1 when it is absent.
func changesWaitingRow(t *testing.T, mux *http.ServeMux, sg *testkit.StaffGuards) int {
	t.Helper()
	rec := driveJSON(t, mux, sg, http.MethodGet, "/api/v1/dashboard/needs", nil)
	return decodeNeedsRow(t, rec, dashboard.NeedsChangesWaiting)
}

func decodeNeedsRow(t *testing.T, rec *httptest.ResponseRecorder, key string) int {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("needs queue = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	var queue struct {
		Rows []struct {
			SourceKey string `json:"source_key"`
			Count     int    `json:"count"`
			What      string `json:"what"`
			Action    string `json:"action"`
			Screen    string `json:"screen"`
		} `json:"rows"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &queue); err != nil {
		t.Fatalf("decode needs queue: %v", err)
	}
	for _, row := range queue.Rows {
		if row.SourceKey == key {
			if row.What != "Change orders to sign" || row.Action != "Sign" || row.Screen != "approvals" {
				t.Fatalf("the row's words = %q / %q / %q, want the catalog's", row.What, row.Action, row.Screen)
			}
			return row.Count
		}
	}
	return -1
}

func TestNeedsYou_ChangeOrdersWaitingThroughTheRealRoot(t *testing.T) {
	pool := wiringPool(t)
	_, branchA := plantDashboardCaller(t, pool)
	branchB := qd1bOtherBranch(t, pool, branchA)
	ctx := context.Background()
	var cust string
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name, customer_type) VALUES ($1, 'Needs You change orders', 'CONTRACTOR') RETURNING id::text`,
		"NY-"+time.Now().Format("150405.000000")).Scan(&cust); err != nil {
		t.Fatalf("plant customer: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM customers WHERE id = $1::uuid`, cust) })

	plantChange(t, pool, cust, branchA, "PENDING_FINANCE") // a manager signs it
	plantChange(t, pool, cust, branchA, "APPROVED")        // a manager applies it
	plantChange(t, pool, cust, branchA, "PENDING_YARD")    // waits on the yard, not on a manager
	plantChange(t, pool, cust, branchB, "PENDING_FINANCE") // the other branch's

	// A general manager (global) is waited on for all three signable or appliable changes.
	gm := testkit.NewStaffGuards(t)
	gm.SetAuthority(identity.StaffAuthority{RoleID: "general_manager", Tier: identity.TierLeadership, Scope: identity.ScopeGlobal})
	if n := changesWaitingRow(t, dashFold(t, pool, gm), gm); n != 3 {
		t.Fatalf("the general manager's Needs You counts %d change orders, want 3 (pending finance and approved at both branches, never the pending yard one)", n)
	}

	// A branch manager's count stops at their branch.
	bm := testkit.NewStaffGuards(t)
	bm.SetAuthority(identity.StaffAuthority{RoleID: "branch_manager", Tier: identity.TierManager, Scope: identity.ScopeBranch, Branches: []string{branchA}})
	if n := changesWaitingRow(t, dashFold(t, pool, bm), bm); n != 2 {
		t.Fatalf("the branch manager's Needs You counts %d change orders, want 2 (their branch's pending finance and approved)", n)
	}

	// A counter clerk's queue never carries the row, whatever is waiting.
	clerk := testkit.NewStaffGuards(t)
	clerk.SetAuthority(identity.StaffAuthority{RoleID: "inside_sales", Tier: 3, Scope: identity.ScopeBranch, Branches: []string{branchA}})
	if n := changesWaitingRow(t, dashFold(t, pool, clerk), clerk); n != -1 {
		t.Fatalf("a counter clerk's Needs You carries the change orders row (count %d), want it absent", n)
	}
}
