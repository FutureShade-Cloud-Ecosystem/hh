//go:build integration

// syncscope_board_integration_test.go: the dispatch board's sync trio
// against a real database (HH-3.5 work item #168's acceptance): the
// per-scope contract battery, GATE-7 and the replay property for
// loads, delivery_stops and will_call over the dispatch module's own
// tables, the shared-seq-stream proof (a loads write surfaces in a
// stops pull and the other way round, the one stream the frozen
// schema's single seq column gives three scopes), the will-call mint's
// pick gate, org isolation with the tenant filter failing closed, and
// the module's own invariants holding through sync.
//
// Run with:
//
//	DATABASE_URL='postgres://hardscape:hardscape@127.0.0.1:55439/hh35sync168?sslmode=disable' \
//	  go test -tags=integration -count=1 -run SyncBoard ./internal/dispatch/
package dispatch

import (
	"context"
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	syncharness "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync/harness"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// boardFixture is the board world: the real service (the desk legs run
// its own paths), the stops adapter (whose reads and creates the two
// board scopes embed), the three board stores, and one engine carrying
// all four scopes exactly as the composition root wires them (the
// shared-stream proof needs the stops scope beside the board's).
type boardFixture struct {
	pool        *pgxpool.Pool
	svc         *Service
	stops       *StopsSyncStore
	loads       *LoadsSyncStore
	delivery    *DeliveryStopsSyncStore
	willCall    *WillCallSyncStore
	engine      *sync.Engine
	north       sync.ScopeFilter
	south       sync.ScopeFilter
	stagedPicks map[string]bool
	openPicks   map[string]bool
	willCallNo  int
	t           *testing.T
}

// newBoardFixture builds the world. The pick-state seam is the test's
// own table: orders listed in stagedPicks read as staged pulls (the
// yard's fact), everything else reads as unstarted, and the seam's
// refusing arm is exercised by the mint-gate leg.
func newBoardFixture(t *testing.T) *boardFixture {
	t.Helper()
	h := newHarness(t)
	_ = h
	pool := h.pool
	ctx := context.Background()
	if err := testkit.Build(ctx, testkit.PgxPool(pool)); err != nil {
		t.Fatalf("fixture build: %v", err)
	}
	f := &boardFixture{
		pool:        pool,
		svc:         h.svc,
		north:       sync.ScopeFilter{OrgID: testkit.OrgNorthID, Actor: testkit.SubInsideSales},
		south:       sync.ScopeFilter{OrgID: testkit.OrgSouthID, Actor: testkit.SubInsideSales},
		stagedPicks: map[string]bool{},
		openPicks:   map[string]bool{},
	}
	picks := boardPicks{f: f}
	orders := testOrders{pool: pool}
	f.stops = NewStopsSyncStore(pool, orders, linkedSyncUser(pool))
	f.loads = NewLoadsSyncStore(f.stops)
	f.delivery = NewDeliveryStopsSyncStore(f.stops)
	f.willCall = NewWillCallSyncStore(pool, orders, picks, nil, linkedSyncUser(pool))
	f.engine = sync.NewEngine().WithAudit(syncIntAudit{svc: audit.NewService(audit.NewRepository(pool), nil)}, nil)
	for name, store := range map[string]sync.Store{
		"stops": f.stops, "loads": f.loads, "delivery_stops": f.delivery, "will_call": f.willCall,
	} {
		if err := f.engine.RegisterScope(name, store); err != nil {
			t.Fatalf("register %s: %v", name, err)
		}
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM will_call_pickup_events WHERE pickup_id IN (SELECT id FROM will_call_pickups WHERE branch_id IN ($1::uuid, $2::uuid))`,
			testkit.BranchNorthAID, testkit.BranchSouthAID)
		_, _ = pool.Exec(c, `DELETE FROM will_call_pickups WHERE branch_id IN ($1::uuid, $2::uuid)`,
			testkit.BranchNorthAID, testkit.BranchSouthAID)
		_, _ = pool.Exec(c, `DELETE FROM delivery_stops WHERE org_id IN ($1::uuid, $2::uuid) AND order_id IN (SELECT id FROM orders WHERE order_no LIKE '#SO-FXSYNC-%')`,
			testkit.OrgNorthID, testkit.OrgSouthID)
		_, _ = pool.Exec(c, `DELETE FROM orders WHERE order_no LIKE '#SO-FXSYNC-%'`)
		_, _ = pool.Exec(c, `DELETE FROM scope_sequences WHERE scope IN ('stops','will_call')`)
		_, _ = pool.Exec(c, `DELETE FROM audit_log WHERE entity_type IN ('sync.loads','sync.delivery_stops','sync.will_call')`)
		_ = testkit.Cleanup(c, testkit.PgxPool(pool))
	})
	return f
}

// boardPicks satisfies PickCompletionReader over the fixture's own
// table, the same shape the composition root's adapter carries (the
// yard's own read, STAGED and DONE both meaning pulled).
type boardPicks struct{ f *boardFixture }

func (p boardPicks) OrderPickState(ctx context.Context, orderID string) (PickCompletion, error) {
	if p.f.stagedPicks[orderID] {
		return PickCompletion{Exists: true, Staged: true, Status: "STAGED"}, nil
	}
	if p.f.openPicks[orderID] {
		return PickCompletion{Exists: true, Staged: false, Status: "ACCEPTED"}, nil
	}
	return PickCompletion{}, nil
}

func (p boardPicks) OrderPickStates(ctx context.Context, orderIDs []string) (map[string]PickCompletion, error) {
	out := map[string]PickCompletion{}
	for _, id := range orderIDs {
		c, err := p.OrderPickState(ctx, id)
		if err != nil {
			return nil, err
		}
		if c.Exists {
			out[id] = c
		}
	}
	return out, nil
}

// boardOrder seeds one confirmed DELIVERY order at the north branch.
func (f *boardFixture) boardOrder(t *testing.T) string {
	t.Helper()
	var id string
	err := f.pool.QueryRow(context.Background(), `
INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, requested_date, org_id, tenant_id)
VALUES ($1, $2, $3, 'CONFIRMED', 'DELIVERY', CURRENT_DATE, $4::uuid, $5::uuid) RETURNING id::text`,
		"#SO-FXSYNC-B"+uniqueSuffix(), testkit.TenantAlphaID, testkit.BranchNorthAID,
		testkit.OrgNorthID, testkit.TenantAlphaID).Scan(&id)
	if err != nil {
		t.Fatalf("seed board order: %v", err)
	}
	return id
}

// boardWillCallOrder seeds one confirmed WILL_CALL order at the north
// branch with its pick staged (the yard's fact the tag follows).
func (f *boardFixture) boardWillCallOrder(t *testing.T, staged bool) string {
	t.Helper()
	var id string
	err := f.pool.QueryRow(context.Background(), `
INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, requested_date, org_id, tenant_id)
VALUES ($1, $2, $3, 'CONFIRMED', 'WILL_CALL', CURRENT_DATE, $4::uuid, $5::uuid) RETURNING id::text`,
		"#SO-FXSYNC-W"+uniqueSuffix(), testkit.TenantAlphaID, testkit.BranchNorthAID,
		testkit.OrgNorthID, testkit.TenantAlphaID).Scan(&id)
	if err != nil {
		t.Fatalf("seed will-call order: %v", err)
	}
	if staged {
		f.stagedPicks[id] = true
	} else {
		f.openPicks[id] = true
	}
	return id
}

// tomorrow is the shared future date the board kits schedule for.
func boardTomorrow(i int) string {
	return time.Now().UTC().AddDate(0, 0, i).Format("2006-01-02")
}

// loadsKit: creates schedule a stop (the load's birth, the same create
// the stops scope runs), updates write a CONSTANT measurement (the
// replay posture), the chain evolves the weight, and the desk records
// the yard's facts through the module's own SetLoadFacts path.
func loadsKit(f *boardFixture) syncharness.Kit {
	info, ok := sync.Lookup("loads")
	if !ok {
		panic("loads missing from the registry")
	}
	return syncharness.Kit{
		Scope: info, Filter: f.north, Store: f.loads,
		Create: func(id string) map[string]any {
			fields := map[string]any{"order_id": f.boardOrder(f.t), "scheduled_date": boardTomorrow(1)}
			if id != "" {
				fields["id"] = id
			}
			return fields
		},
		Update: func(row map[string]any) map[string]any {
			return map[string]any{"id": row["id"], "weight_kg": 2500.5}
		},
		Chain: func(row map[string]any, n int) []map[string]any {
			out := make([]map[string]any, 0, n)
			for i := 1; i <= n; i++ {
				out = append(out, map[string]any{"id": row["id"], "weight_kg": float64(3000 + i*250)})
			}
			return out
		},
		Desk: func(ctx context.Context, row map[string]any) error {
			w := 4100.0
			_, err := f.svc.SetLoadFacts(globalCtx(f.t, f.pool), fmt.Sprint(row["id"]), LoadFactsInput{WeightKg: &w})
			return err
		},
	}
}

// deliveryStopsKit: creates schedule a stop, updates re-time with a
// CONSTANT date and reason, the chain evolves the date, and the desk
// reschedules through the module's own RescheduleStop path.
func deliveryStopsKit(f *boardFixture) syncharness.Kit {
	info, _ := sync.Lookup("delivery_stops")
	return syncharness.Kit{
		Scope: info, Filter: f.north, Store: f.delivery,
		Create: func(id string) map[string]any {
			fields := map[string]any{"order_id": f.boardOrder(f.t), "scheduled_date": boardTomorrow(1)}
			if id != "" {
				fields["id"] = id
			}
			return fields
		},
		Update: func(row map[string]any) map[string]any {
			return map[string]any{"id": row["id"], "scheduled_date": boardTomorrow(9), "reason": "FX sync re-time"}
		},
		Chain: func(row map[string]any, n int) []map[string]any {
			out := make([]map[string]any, 0, n)
			for i := 1; i <= n; i++ {
				out = append(out, map[string]any{
					"id": row["id"], "scheduled_date": boardTomorrow(10 + i), "reason": fmt.Sprintf("FX chain leg %d", i),
				})
			}
			return out
		},
		Desk: func(ctx context.Context, row map[string]any) error {
			_, err := f.svc.RescheduleStop(globalCtx(f.t, f.pool), fmt.Sprint(row["id"]), RescheduleInput{
				ToDate: boardTomorrow(20), Reason: "FX desk re-time",
			})
			return err
		},
	}
}

// willCallKit: creates mint a tag (the device-generated square, the
// hash landing at rest), updates extend the tag's life (the CONSTANT
// posture: one fixed far-out hour a replay cannot re-extend past, so a
// superseded replay refuses and moves nothing), the chain extends it
// hour by hour, and the desk revokes through the module's own path
// (the conflict leg then meets the server's revoked row).
func willCallKit(f *boardFixture) syncharness.Kit {
	info, _ := sync.Lookup("will_call")
	f.willCallNo++
	return syncharness.Kit{
		Scope: info, Filter: f.north, Store: f.willCall,
		Create: func(id string) map[string]any {
			// The square's own length: 32 bytes base64url is 43
			// characters, the shape the module's own mint prints.
			fields := map[string]any{
				"order_id":  f.boardWillCallOrder(f.t, true),
				"token":     "FXSQ" + uuid.NewString() + uuid.NewString(),
				"ttl_hours": 24.0,
			}
			if id != "" {
				fields["id"] = id
			}
			return fields
		},
		Update: func(row map[string]any) map[string]any {
			return map[string]any{
				"id":         row["id"],
				"expires_at": time.Now().UTC().Add(48 * time.Hour).Format(time.RFC3339),
			}
		},
		Chain: func(row map[string]any, n int) []map[string]any {
			out := make([]map[string]any, 0, n)
			base := time.Now().UTC()
			if row["expires_at"] != nil {
				if t, err := time.Parse(time.RFC3339, fmt.Sprint(row["expires_at"])); err == nil {
					base = t
				}
			}
			for i := 1; i <= n; i++ {
				out = append(out, map[string]any{
					"id":         row["id"],
					"expires_at": base.Add(time.Duration(i) * time.Hour).Format(time.RFC3339),
				})
			}
			return out
		},
		Desk: func(ctx context.Context, row map[string]any) error {
			return f.svc.RevokeWillCall(globalCtx(f.t, f.pool), fmt.Sprint(row["id"]))
		},
	}
}

// TestBoardContractBatteries: the battery, GATE-7 and the replay
// property on each of the three board scopes.
func TestBoardContractBatteries(t *testing.T) {
	// Each scope gets its OWN fixture: loads and delivery_stops are two
	// scopes over ONE table, and a battery's pull-shape leg must see
	// only the rows it created, so the fixtures (and their cleanups)
	// cannot share a table population.
	for name := range map[string]func(*boardFixture) syncharness.Kit{
		"loads": loadsKit, "delivery_stops": deliveryStopsKit, "will_call": willCallKit,
	} {
		t.Run(name, func(t *testing.T) {
			f := newBoardFixture(t)
			f.t = t
			kit := map[string]func(*boardFixture) syncharness.Kit{
				"loads": loadsKit, "delivery_stops": deliveryStopsKit, "will_call": willCallKit,
			}[name](f)
			syncharness.Contract(t, f.engine, kit)
			syncharness.Gate7(t, f.engine, kit)
			rng := rand.New(rand.NewSource(1680 + int64(len(name))))
			syncharness.ReplayProperty(t, f.engine, kit, 8, rng.Intn)
		})
	}
}

// TestBoardSharedSeqStream is the shared-stream proof: loads and stops
// are two scopes over ONE table with ONE seq column, so every write
// from either scope (and the module's own paths) draws from the one
// stops stream, and a device's cursor on either scope sees the move.
func TestBoardSharedSeqStream(t *testing.T) {
	f := newBoardFixture(t)
	f.t = t
	ctx := context.Background()
	loadsInfo, _ := sync.Lookup("loads")
	stopsInfo, _ := sync.Lookup("stops")

	// A load is born through the loads scope.
	res := boardPush(t, f, loadsInfo, []boardOp{
		{client: boardKey(1), kind: "create", payload: loadsKit(f).Create("")},
	})
	if res[0].Status != "applied" {
		t.Fatalf("loads create: %+v", res[0])
	}
	feed, err := f.engine.Pull(ctx, loadsInfo, f.north, 0, 10)
	if err != nil || len(feed.Items) != 1 {
		t.Fatalf("loads pull: %v %+v", err, feed.Items)
	}
	id, bornSeq := feed.Items[0].Id, feed.Items[0].Seq

	// The STOPS scope sees the same row at the same seq: one stream,
	// one cursor basis.
	stopsFeed, err := f.engine.Pull(ctx, stopsInfo, f.north, 0, 10)
	if err != nil {
		t.Fatalf("stops pull: %v", err)
	}
	seen := false
	for _, it := range stopsFeed.Items {
		if it.Id == id {
			seen = it.Seq == bornSeq
		}
	}
	if !seen {
		t.Fatalf("the stops scope did not see the loads-born row at its born seq")
	}

	// A LOADS write moves the row and the STOPS pull past its old
	// cursor carries it: the loads stream is the stops stream.
	if _, err := f.loads.Write(ctx, f.north, id, "00000000-0000-4000-8000-0000000000cf",
		map[string]any{"id": id, "weight_kg": 2200.0}); err != nil {
		t.Fatalf("loads write: %v", err)
	}
	stopsFeed2, err := f.engine.Pull(ctx, stopsInfo, f.north, bornSeq, 10)
	if err != nil {
		t.Fatalf("stops pull 2: %v", err)
	}
	loadsMoved := false
	for _, it := range stopsFeed2.Items {
		if it.Id == id && it.Seq > bornSeq {
			loadsMoved = true
		}
	}
	if !loadsMoved {
		t.Fatalf("a loads write never surfaced in the stops feed past the cursor")
	}

	// A stops-scope write (the stops adapter's own withdrawal, the
	// module's pre-departure move) moves the row, and the LOADS pull
	// past its old cursor carries it: the cursor never strands a row
	// between two counters.
	stopsRow, ok, err := f.stops.Load(ctx, f.north, id)
	if err != nil || !ok {
		t.Fatalf("stops load: ok=%v err=%v", ok, err)
	}
	if _, err := f.stops.Write(ctx, f.north, id, "00000000-0000-4000-8000-0000000000ce",
		map[string]any{"id": id, "status": StopCancelled}); err != nil {
		t.Fatalf("stops withdrawal: %v", err)
	}
	loadsFeed, err := f.engine.Pull(ctx, loadsInfo, f.north, stopsRow.Seq, 10)
	if err != nil {
		t.Fatalf("loads pull past cursor: %v", err)
	}
	moved := false
	for _, it := range loadsFeed.Items {
		if it.Id == id && it.Seq > stopsRow.Seq {
			moved = true
		}
	}
	if !moved {
		t.Fatalf("a stops-scope write never surfaced in the loads feed past the cursor")
	}
	t.Logf("shared stream: the loads-born row (seq %d) moved by a stops write and pulled through loads at a greater seq", bornSeq)
}

// TestWillCallMintGates: the mint's own gates through sync: nothing
// staged refuses, a non-will-call order refuses, and the raw token
// never lands at rest (only its hash) and never rides the projection.
func TestWillCallMintGates(t *testing.T) {
	f := newBoardFixture(t)
	f.t = t
	ctx := context.Background()
	info, _ := sync.Lookup("will_call")

	// Nothing staged: the tag follows the pick.
	unstaged := f.boardWillCallOrder(t, false)
	res := boardPush(t, f, info, []boardOp{
		{client: boardKey(1), kind: "create", payload: map[string]any{
			"order_id": unstaged, "token": "FXTOKEN-UNSTAGED-" + uniqueSuffix(), "ttl_hours": 24.0,
		}},
	})
	if len(res) != 1 || res[0].Status != "rejected" {
		t.Fatalf("unstaged mint answered %+v, want rejected", res[0])
	}
	t.Logf("mint gate: nothing staged refused (%s)", *res[0].Error)

	// A delivery order is not a rack tag.
	delivery := f.boardOrder(t)
	res = boardPush(t, f, info, []boardOp{
		{client: boardKey(2), kind: "create", payload: map[string]any{
			"order_id": delivery, "token": "FXSQ-DELIVERY-" + uuid.NewString() + uuid.NewString(), "ttl_hours": 24.0,
		}},
	})
	if len(res) != 1 || res[0].Status != "rejected" {
		t.Fatalf("delivery mint answered %+v, want rejected", res[0])
	}

	// The good mint: applied, the hash at rest, the raw token absent.
	raw := "FXSQ-GOOD-" + uuid.NewString() + uuid.NewString()
	res = boardPush(t, f, info, []boardOp{
		{client: boardKey(3), kind: "create", payload: map[string]any{
			"order_id": f.boardWillCallOrder(t, true), "token": raw, "ttl_hours": 24.0,
		}},
	})
	if len(res) != 1 || res[0].Status != "applied" {
		t.Fatalf("good mint answered %+v", res[0])
	}
	var stored string
	var n int
	if err := f.pool.QueryRow(ctx,
		`SELECT token_hash FROM will_call_pickups WHERE idempotency_key = $1::uuid`, boardKey(3)).Scan(&stored); err != nil {
		t.Fatalf("stored hash read: %v", err)
	}
	if stored == raw {
		t.Fatalf("the raw token landed at rest")
	}
	if err := f.pool.QueryRow(ctx,
		`SELECT count(*) FROM will_call_pickups WHERE token_hash = $1 AND idempotency_key = $2::uuid`,
		hashPickupToken(raw), boardKey(3)).Scan(&n); err != nil || n != 1 {
		t.Fatalf("the hashed token is the stored one: n=%d err=%v", n, err)
	}
	feed, err := f.engine.Pull(ctx, info, f.north, 0, 10)
	if err != nil {
		t.Fatalf("will_call pull: %v", err)
	}
	for _, it := range feed.Items {
		if _, has := it.Data["token_hash"]; has {
			t.Fatalf("the projection carries the token hash")
		}
		if _, has := it.Data["token"]; has {
			t.Fatalf("the projection carries the raw token")
		}
	}
	t.Logf("mint: the raw token exists once on the printer, its hash at rest, neither on the feed")
}

// TestBoardOrgIsolation: rows of another org never pull through the
// north filter, and the tenant filter answers nothing on all three.
func TestBoardOrgIsolation(t *testing.T) {
	f := newBoardFixture(t)
	f.t = t
	ctx := context.Background()
	northInfo, _ := sync.Lookup("loads")
	syncPush := boardPush(t, f, northInfo, []boardOp{
		{client: boardKey(1), kind: "create", payload: loadsKit(f).Create("")},
	})
	if syncPush[0].Status != "applied" {
		t.Fatalf("north create: %+v", syncPush[0])
	}
	// A south stop seeded through the module's own insert (org stamped
	// from the branch).
	southOrder := seedOrder(t, f.pool, testkit.BranchSouthAID, "DELIVERY", "CONFIRMED")
	if _, err := f.pool.Exec(ctx, `
INSERT INTO delivery_stops (branch_id, order_id, scheduled_date, org_id, seq)
VALUES ($1::uuid, $2::uuid, CURRENT_DATE + 1, $3::uuid, 1)`,
		testkit.BranchSouthAID, southOrder, testkit.OrgSouthID); err != nil {
		t.Fatalf("south stop seed: %v", err)
	}
	for _, scope := range []string{"loads", "delivery_stops", "will_call"} {
		info, _ := sync.Lookup(scope)
		pull, err := f.engine.Pull(ctx, info, f.north, 0, 200)
		if err != nil {
			t.Fatalf("%s north pull: %v", scope, err)
		}
		for _, it := range pull.Items {
			if it.Id == southOrder {
				t.Fatalf("the north org's %s pull saw the south org's row", scope)
			}
		}
		tenant := f.north
		tenant.TenantID, tenant.Tenant, tenant.Actor = testkit.TenantAlphaID, true, testkit.SubPortalAdminAlpha
		tenantPull, err := f.engine.Pull(ctx, info, tenant, 0, 200)
		if err != nil {
			t.Fatalf("%s tenant pull: %v", scope, err)
		}
		if len(tenantPull.Items) != 0 || len(tenantPull.Tombstones) != 0 {
			t.Fatalf("a tenant filter read org rows through %s: %+v", scope, tenantPull)
		}
	}
}

// TestBoardModuleInvariants: the module's own rules hold through the
// board scopes: a negative measurement refuses, a committed re-time
// through a past date refuses, and the run-assignment capability door
// stays shut (run_id is not a sync field at all).
func TestBoardModuleInvariants(t *testing.T) {
	f := newBoardFixture(t)
	f.t = t
	loadsInfo, _ := sync.Lookup("loads")
	dsInfo, _ := sync.Lookup("delivery_stops")

	created := boardPush(t, f, loadsInfo, []boardOp{
		{client: boardKey(1), kind: "create", payload: loadsKit(f).Create("")},
	})
	if created[0].Status != "applied" {
		t.Fatalf("loads create: %+v", created[0])
	}
	feed, err := f.engine.Pull(context.Background(), loadsInfo, f.north, 0, 10)
	if err != nil || len(feed.Items) != 1 {
		t.Fatalf("loads pull: %v", err)
	}
	id := feed.Items[0].Id

	// A negative measurement refuses.
	res := boardPush(t, f, loadsInfo, []boardOp{
		{client: boardKey(2), kind: "update", payload: map[string]any{"id": id, "weight_kg": -1}},
	})
	if len(res) != 1 || res[0].Status != "rejected" {
		t.Fatalf("negative measurement answered %+v, want rejected", res[0])
	}
	// Fractional pallet positions refuse.
	res = boardPush(t, f, loadsInfo, []boardOp{
		{client: boardKey(3), kind: "update", payload: map[string]any{"id": id, "pallet_positions": 2.5}},
	})
	if len(res) != 1 || res[0].Status != "rejected" {
		t.Fatalf("fractional pallets answered %+v, want rejected", res[0])
	}
	// A past re-time refuses (the module's own sentence).
	res = boardPush(t, f, dsInfo, []boardOp{
		{client: boardKey(4), kind: "update",
			payload: map[string]any{"id": id, "scheduled_date": boardTomorrow(-3), "reason": "FX past"}},
	})
	if len(res) != 1 || res[0].Status != "rejected" {
		t.Fatalf("past re-time answered %+v, want rejected", res[0])
	}
	// The run assignment is not a sync field: the capability question
	// it carries cannot be answered here, so the field refuses whole.
	res = boardPush(t, f, dsInfo, []boardOp{
		{client: boardKey(5), kind: "update",
			payload: map[string]any{"id": id, "run_id": "00000000-0000-4000-8000-0000000000aa"}},
	})
	if len(res) != 1 || res[0].Status != "rejected" {
		t.Fatalf("run assignment through sync answered %+v, want rejected", res[0])
	}
	t.Logf("invariants: negative and fractional facts refuse, past re-times refuse, the crew door stays the desk's (%s)", *res[0].Error)
}

// The small helpers.

type boardOp struct {
	client  string
	kind    string
	payload map[string]any
	base    *int64
}

func boardKey(n int) string { return fmt.Sprintf("33000000-0000-4000-8000-%012d", n) }

func boardPush(t *testing.T, f *boardFixture, info sync.ScopeInfo, ops []boardOp) []apigen.SyncPushResult {
	t.Helper()
	out := make([]apigen.SyncPushOperation, 0, len(ops))
	for _, o := range ops {
		out = append(out, apigen.SyncPushOperation{ClientId: o.client, Op: o.kind, Payload: o.payload, BaseSeq: o.base})
	}
	resp, err := f.engine.Push(context.Background(), info, f.north, out)
	if err != nil {
		t.Fatalf("push %s: %v", info.Name, err)
	}
	return resp.Results
}
