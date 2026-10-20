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

// TestWillCallPickups_OneCounter_FloorNPlusOneAndRowMax proves both arithmetic
// invariants of willCallSeqUpsert (UX-72 round 2):
//  1. pickups counter at N, will_call counter below N: because scope_sequences
//     stores the last value issued (the upsert increments and returns next_value),
//     the next draw is N + 1, never N.
//  2. a row in will_call_pickups at seq above both counters: the next draw is
//     strictly above that row, on both the conflict update branch and the fresh
//     insert branch.
func TestWillCallPickups_OneCounter_FloorNPlusOneAndRowMax(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("will_call one counter floor leg: DATABASE_URL not set")
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

	orderIDs := make([]string, 2)
	for i := range orderIDs {
		if err := pool.QueryRow(ctx, `
			INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, requested_date, org_id, tenant_id)
			VALUES ($1, $2, $3, 'CONFIRMED', 'WILL_CALL', CURRENT_DATE, $4::uuid, $5::uuid) RETURNING id::text`,
			fmt.Sprintf("#SO-FXONECOUNTER-FLOOR-%d", i+1), testkit.TenantAlphaID, testkit.BranchNorthAID,
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

	var base int64
	if err := pool.QueryRow(ctx, `
		SELECT GREATEST(
			COALESCE((SELECT next_value FROM scope_sequences WHERE scope = 'will_call'), 0),
			COALESCE((SELECT next_value FROM scope_sequences WHERE scope = 'pickups'), 0),
			COALESCE((SELECT MAX(seq) FROM will_call_pickups), 0)
		)`).Scan(&base); err != nil {
		t.Fatalf("read counter floor: %v", err)
	}

	// Leg 1: pickups counter at N, will_call counter below N -> next draw is N + 1, never N.
	willCallBelow := base + 100
	n := base + 500
	if _, err := pool.Exec(ctx, `
		INSERT INTO scope_sequences (scope, next_value) VALUES ('will_call', $1)
		ON CONFLICT (scope) DO UPDATE SET next_value = $1`, willCallBelow); err != nil {
		t.Fatalf("set will_call below N: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO scope_sequences (scope, next_value) VALUES ('pickups', $1)
		ON CONFLICT (scope) DO UPDATE SET next_value = $1`, n); err != nil {
		t.Fatalf("set pickups at N: %v", err)
	}

	repo := NewRepository(pool)
	w1, err := repo.InsertPickup(ctx, orderIDs[0], testkit.BranchNorthAID, "one-counter-floor-hash-1", "", time.Now().Add(24*time.Hour))
	if err != nil {
		t.Fatalf("staff insert 1: %v", err)
	}
	var gotSeq1 int64
	if err := pool.QueryRow(ctx, `SELECT seq FROM will_call_pickups WHERE id = $1::uuid`, w1.ID).Scan(&gotSeq1); err != nil {
		t.Fatalf("read w1 seq: %v", err)
	}
	if gotSeq1 != n+1 {
		t.Fatalf("pickups counter at N=%d, will_call at %d below N: next draw was %d, want N+1=%d (never N)", n, willCallBelow, gotSeq1, n+1)
	}

	// Also prove the fresh-insert branch when will_call row is absent and pickups is at N (rolled back so no counter is lowered).
	func() {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin tx for fresh insert N+1 check: %v", err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		nFresh := n + 200
		if _, err := tx.Exec(ctx, `DELETE FROM scope_sequences WHERE scope = 'will_call'`); err != nil {
			t.Fatalf("delete will_call in tx: %v", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE scope_sequences SET next_value = $1 WHERE scope = 'pickups'`, nFresh); err != nil {
			t.Fatalf("set pickups in tx: %v", err)
		}
		drew, err := allocateSyncSeqTx(ctx, tx, willCallSyncScope)
		if err != nil {
			t.Fatalf("allocateSyncSeqTx fresh insert N+1: %v", err)
		}
		if drew != nFresh+1 {
			t.Fatalf("fresh insert with pickups at N=%d: next draw was %d, want N+1=%d (never N)", nFresh, drew, nFresh+1)
		}
	}()

	// Leg 2: a row in will_call_pickups at seq above both counters -> next draw is above it.
	rowSeq := n + 500
	if _, err := pool.Exec(ctx, `UPDATE will_call_pickups SET seq = $2 WHERE id = $1::uuid`, w1.ID, rowSeq); err != nil {
		t.Fatalf("stamp w1 seq above both counters: %v", err)
	}
	w2, err := repo.InsertPickup(ctx, orderIDs[1], testkit.BranchNorthAID, "one-counter-floor-hash-2", "", time.Now().Add(24*time.Hour))
	if err != nil {
		t.Fatalf("staff insert 2: %v", err)
	}
	var gotSeq2 int64
	if err := pool.QueryRow(ctx, `SELECT seq FROM will_call_pickups WHERE id = $1::uuid`, w2.ID).Scan(&gotSeq2); err != nil {
		t.Fatalf("read w2 seq: %v", err)
	}
	if gotSeq2 <= rowSeq || gotSeq2 != rowSeq+1 {
		t.Fatalf("row at seq=%d above both counters (will_call=%d, pickups=%d): next draw was %d, want %d strictly above the row",
			rowSeq, gotSeq1, n, gotSeq2, rowSeq+1)
	}

	// Also prove the fresh-insert branch when will_call row is absent and a row sits above pickups (rolled back).
	func() {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin tx for fresh insert row-max check: %v", err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		rowSeqFresh := rowSeq + 300
		if _, err := tx.Exec(ctx, `DELETE FROM scope_sequences WHERE scope = 'will_call'`); err != nil {
			t.Fatalf("delete will_call in tx: %v", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE will_call_pickups SET seq = $2 WHERE id = $1::uuid`, w2.ID, rowSeqFresh); err != nil {
			t.Fatalf("stamp w2 seq in tx: %v", err)
		}
		drew, err := allocateSyncSeqTx(ctx, tx, willCallSyncScope)
		if err != nil {
			t.Fatalf("allocateSyncSeqTx fresh insert row-max: %v", err)
		}
		if drew <= rowSeqFresh || drew != rowSeqFresh+1 {
			t.Fatalf("fresh insert with row at seq=%d above pickups=%d: next draw was %d, want %d strictly above the row",
				rowSeqFresh, n, drew, rowSeqFresh+1)
		}
	}()
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
