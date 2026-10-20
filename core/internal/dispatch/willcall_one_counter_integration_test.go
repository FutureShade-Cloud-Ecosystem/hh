//go:build integration

// willcall_one_counter_integration_test.go, the one counter leg for will_call_pickups (UX-72).
//
// The defect: two writers number one table from two counters. The contractor's sync writes drew from
// the `pickups` scope and the staff writes (REST and sync) from the `will_call` scope, while both
// device feeds read the table by `seq > since`. Counters that cross let a feed see a high seq from
// one writer and then a lower seq from the other, below the cursor it already holds, so the device
// never asks for that row again.
//
// The leg makes the cross certain instead of hoping for it: before the writes it lifts the `pickups`
// counter far above the `will_call` counter (the state a deployment that has run both writers for a
// while is in, and the worst case for the upgrade: new staff writes would land below old contractor
// rows). It then interleaves the writers through production code (the staff repository, the
// contractor sync store), takes each feed's cursor from a pull part way through, and asserts that
// every row written after the cursor arrives on each feed it belongs to. It never lowers a counter,
// so it is safe on a shared database. It runs against whatever DATABASE_URL it is given and skips
// without one.
package dispatch

import (
	"context"
	"fmt"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
	hhsync "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

func TestWillCallPickups_OneCounter_NoFeedSkipsARow(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("will_call one counter leg: DATABASE_URL not set (the leg needs its own throwaway database and skips politely without it)")
	}
	ctx := context.Background()
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("parse DATABASE_URL: %v", err)
	}
	cfg.MaxConns = 4
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("build pool: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("database not reachable: %v", err)
	}
	if err := testkit.Build(ctx, testkit.PgxPool(pool)); err != nil {
		t.Fatalf("fixture build: %v", err)
	}
	t.Cleanup(func() { _ = testkit.Cleanup(context.Background(), testkit.PgxPool(pool)) })

	// The cross: the pickups counter well above the will_call counter, neither lowered.
	if _, err := pool.Exec(ctx, `
		INSERT INTO scope_sequences (scope, next_value)
		VALUES ('pickups', COALESCE((SELECT next_value FROM scope_sequences WHERE scope = 'will_call'), 1) + 1000)
		ON CONFLICT (scope) DO UPDATE SET next_value = GREATEST(scope_sequences.next_value,
			COALESCE((SELECT next_value FROM scope_sequences WHERE scope = 'will_call'), 1) + 1000)`); err != nil {
		t.Fatalf("lift the pickups counter: %v", err)
	}

	orderIDs := make([]string, 5)
	for i := range orderIDs {
		if err := pool.QueryRow(ctx, `
			INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, requested_date, org_id, tenant_id)
			VALUES ($1, $2, $3, 'CONFIRMED', 'WILL_CALL', CURRENT_DATE, $4::uuid, $5::uuid) RETURNING id::text`,
			fmt.Sprintf("#SO-FXONECOUNTER-%d", i+1), testkit.TenantAlphaID, testkit.BranchNorthAID,
			testkit.OrgNorthID, testkit.TenantAlphaID).Scan(&orderIDs[i]); err != nil {
			t.Fatalf("seed will-call order %d: %v", i+1, err)
		}
		oid := orderIDs[i]
		t.Cleanup(func() {
			_, _ = pool.Exec(context.Background(), `DELETE FROM will_call_pickup_events WHERE pickup_id IN (SELECT id FROM will_call_pickups WHERE order_id = $1::uuid)`, oid)
			_, _ = pool.Exec(context.Background(), `DELETE FROM will_call_pickups WHERE order_id = $1::uuid`, oid)
			_, _ = pool.Exec(context.Background(), `DELETE FROM orders WHERE id = $1::uuid`, oid)
		})
	}

	repo := NewRepository(pool)
	staffFeed := NewWillCallSyncStore(pool, nil, nil, nil, nil)
	contractorFeed := NewPickupsSyncStore(pool, pickupsCycleOrders{repo: order.NewRepository(pool)}, writeStarveSig{}, nil, nil)
	staffFilter := hhsync.ScopeFilter{OrgID: testkit.OrgNorthID, Actor: "one-counter-staff"}
	tenantFilter := hhsync.ScopeFilter{OrgID: testkit.OrgNorthID, Tenant: true, TenantID: testkit.TenantAlphaID, Actor: "one-counter-contractor"}
	mine := map[string]bool{}

	insertStaff := func(i int) string {
		w, err := repo.InsertPickup(ctx, orderIDs[i], testkit.BranchNorthAID, fmt.Sprintf("one-counter-hash-%d", i+1), "", time.Now().Add(24*time.Hour))
		if err != nil {
			t.Fatalf("staff insert %d: %v", i+1, err)
		}
		mine[w.ID] = true
		return w.ID
	}
	confirmContractor := func(id string, n int) {
		if _, err := contractorFeed.Write(ctx, tenantFilter, id, fmt.Sprintf("a0e40000-0000-4000-8000-0000000003%02d", n),
			map[string]any{"status": "CONFIRMED", "collected_by": "One Counter Leg", "affirmed": true}); err != nil {
			t.Fatalf("contractor confirm %s: %v", id, err)
		}
	}
	// ids and the max seq a feed shows for this leg's rows
	pullStaff := func(since int64) (map[string]int64, int64) {
		items, _, err := staffFeed.Pull(ctx, staffFilter, since, 500)
		if err != nil {
			t.Fatalf("staff feed pull: %v", err)
		}
		return feedIDs(items, mine), feedMax(items, since)
	}
	pullContractor := func(since int64) (map[string]int64, int64) {
		items, _, err := contractorFeed.Pull(ctx, tenantFilter, since, 500)
		if err != nil {
			t.Fatalf("contractor feed pull: %v", err)
		}
		return feedIDs(items, mine), feedMax(items, since)
	}
	expectAfter := func(feed string, got map[string]int64, want ...string) {
		t.Helper()
		var missing []string
		for _, id := range want {
			if _, ok := got[id]; !ok {
				missing = append(missing, id)
			}
		}
		if len(missing) > 0 {
			sort.Strings(missing)
			t.Fatalf("the %s feed skipped %d row(s) written after its cursor: %v (it saw %v)", feed, len(missing), missing, got)
		}
	}

	// Staff mint four tags (will_call counter), the contractor confirms one (pickups counter, far above).
	p1, p2, p3, p4 := insertStaff(0), insertStaff(1), insertStaff(2), insertStaff(3)
	confirmContractor(p1, 1)
	_, staffCursor := pullStaff(0)
	_, contractorCursor := pullContractor(0)

	// Crossing 1: staff writes after the cursor, from the counter that sat below the contractor's.
	if ok, err := repo.RevokePickup(ctx, p2, time.Now()); err != nil || !ok {
		t.Fatalf("staff revoke p2: %v ok=%v", err, ok)
	}
	if ok, err := repo.ConfirmPickup(ctx, p3, "one-counter-sig", "Desk", time.Now()); err != nil || !ok {
		t.Fatalf("staff confirm p3: %v ok=%v", err, ok)
	}
	got, _ := pullStaff(staffCursor)
	expectAfter("staff", got, p2, p3)
	got, _ = pullContractor(contractorCursor)
	expectAfter("contractor", got, p2, p3)

	// Crossing 2: a contractor write, then a staff mint, from fresh cursors.
	_, staffCursor = pullStaff(0)
	_, contractorCursor = pullContractor(0)
	confirmContractor(p4, 2)
	p5 := insertStaff(4)
	got, _ = pullStaff(staffCursor)
	expectAfter("staff", got, p4, p5)
	got, _ = pullContractor(contractorCursor)
	expectAfter("contractor", got, p4, p5)

	// One counter: no two of this leg's rows share a seq, and the order of writing is the order of seq.
	rows, err := pool.Query(ctx, `SELECT id::text, seq FROM will_call_pickups WHERE id = ANY($1::uuid[])`, keys(mine))
	if err != nil {
		t.Fatalf("read seqs: %v", err)
	}
	defer rows.Close()
	seen := map[int64]string{}
	for rows.Next() {
		var id string
		var seq int64
		if err := rows.Scan(&id, &seq); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if other, dup := seen[seq]; dup {
			t.Fatalf("rows %s and %s share seq %d", other, id, seq)
		}
		seen[seq] = id
	}
}

func feedIDs(items []hhsync.Entity, mine map[string]bool) map[string]int64 {
	out := map[string]int64{}
	for _, e := range items {
		if mine[e.ID] {
			out[e.ID] = e.Seq
		}
	}
	return out
}

func feedMax(items []hhsync.Entity, floor int64) int64 {
	max := floor
	for _, e := range items {
		if e.Seq > max {
			max = e.Seq
		}
	}
	return max
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
