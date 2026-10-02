//go:build integration

// syncscope_integration_test.go: the drive-run sync scopes against a
// real database (work item #167's acceptance): the per-scope contract
// battery, GATE-7 and the replay property for runs, stops and pod over
// the dispatch module's own tables, the operator walkthrough (a driver
// closes a run offline with one cancelled stop, the outbox replays
// when signal returns, dispatch reads exactly what the ERP's own rule
// writes, a duplicate replay changes nothing), org isolation with the
// tenant filter failing closed, race-clean seq allocation, the stops
// payload proof against the pulled feed, and the audit rows.
//
// Run with:
//
//	DATABASE_URL='postgres://hardscape:hardscape@127.0.0.1:55439/hh35sync167?sslmode=disable' \
//	  go test -tags=integration -count=1 -run SyncScope ./internal/dispatch/
//
// The database is a throwaway scratch (created empty, the frozen
// migration set applied by the core's migrate role); never a shared
// proof database. Nothing here fakes the module whose correctness the
// acceptance depends on: the Stores under test are the dispatch
// adapters, the desk legs run through the real dispatch service, and
// every fixture value is invented.
package dispatch

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	syncharness "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync/harness"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// syncFixture is the drive-run world: the real service over the pool
// (the desk legs run its own paths), the three adapters wired into an
// engine exactly as the composition root wires them, and the north org
// caller the middleware would stamp for a staff bearer with X-Org.
type syncFixture struct {
	pool   *pgxpool.Pool
	svc    *Service
	runs   *RunsSyncStore
	stops  *StopsSyncStore
	pod    *PODSyncStore
	engine *sync.Engine
	north  sync.ScopeFilter
	south  sync.ScopeFilter
	truck  string
	driver string
}

// syncToday is the shared date every fixture row schedules for.
func syncToday() string { return time.Now().UTC().Format("2006-01-02") }

// newSyncFixture builds the world: the kit fixtures (two orgs, three
// branches, a truck, orders), one driver row at the north branch, and
// the seeded dispatched run whose stops the battery walks.
func newSyncFixture(t *testing.T) *syncFixture {
	t.Helper()
	h := newHarness(t)
	pool := h.pool
	ctx := context.Background()
	if err := testkit.Build(ctx, testkit.PgxPool(pool)); err != nil {
		t.Fatalf("fixture build: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM delivery_pod_photos p USING delivery_stops s
			WHERE p.stop_id = s.id AND s.org_id IN ($1::uuid, $2::uuid)`, testkit.OrgNorthID, testkit.OrgSouthID)
		_, _ = pool.Exec(c, `DELETE FROM delivery_stops WHERE org_id IN ($1::uuid, $2::uuid)`, testkit.OrgNorthID, testkit.OrgSouthID)
		_, _ = pool.Exec(c, `DELETE FROM delivery_runs WHERE org_id IN ($1::uuid, $2::uuid)`, testkit.OrgNorthID, testkit.OrgSouthID)
		_, _ = pool.Exec(c, `DELETE FROM dispatch_drivers WHERE branch_id = $1::uuid AND name LIKE 'FX Sync%%'`, testkit.BranchNorthAID)
		_, _ = pool.Exec(c, `DELETE FROM orders WHERE order_no LIKE '#SO-FXSYNC-%%' AND branch_id IN ($1::uuid, $2::uuid)`,
			testkit.BranchNorthAID, testkit.BranchSouthAID)
		_, _ = pool.Exec(c, `DELETE FROM scope_sequences WHERE scope IN ('runs','stops','pod')`)
		_, _ = pool.Exec(c, `DELETE FROM audit_log WHERE entity_type IN ('sync.runs','sync.stops','sync.pod')`)
		_ = testkit.Cleanup(c, testkit.PgxPool(pool))
	})

	var driver string
	if err := pool.QueryRow(ctx, `
INSERT INTO dispatch_drivers (branch_id, name, phone, licence_class, is_active)
VALUES ($1, $2, '', 'FX', TRUE) RETURNING id::text`,
		testkit.BranchNorthAID, "FX Sync Driver "+uniqueSuffix()).Scan(&driver); err != nil {
		t.Fatalf("seed driver: %v", err)
	}

	linker := linkedSyncUser(pool)
	runsStore := NewRunsSyncStore(pool, linker)
	stopsStore := NewStopsSyncStore(pool, testOrders{pool: pool}, linker)
	podStore := NewPODSyncStore(pool, linker)
	engine := sync.NewEngine().WithAudit(syncIntAudit{svc: audit.NewService(audit.NewRepository(pool), nil)}, nil)
	for name, store := range map[string]sync.Store{"runs": runsStore, "stops": stopsStore, "pod": podStore} {
		if err := engine.RegisterScope(name, store); err != nil {
			t.Fatalf("register %s: %v", name, err)
		}
	}
	return &syncFixture{
		pool: pool, svc: h.svc, runs: runsStore, stops: stopsStore, pod: podStore, engine: engine,
		north: sync.ScopeFilter{OrgID: testkit.OrgNorthID, Actor: testkit.SubDriver},
		south: sync.ScopeFilter{OrgID: testkit.OrgSouthID, Actor: testkit.SubInsideSales},
		truck: testkit.TruckID, driver: driver,
	}
}

// linkedSyncUser resolves the ERP user row for a platform subject, the
// same shape the composition root's linker carries.
func linkedSyncUser(pool *pgxpool.Pool) syncLinker {
	return func(ctx context.Context, platformUserID string) (string, bool) {
		var id string
		err := pool.QueryRow(ctx,
			`SELECT id::text FROM users WHERE platform_user_id = $1 AND deleted_at IS NULL`,
			platformUserID).Scan(&id)
		if err != nil {
			return "", false
		}
		return id, true
	}
}

// syncIntAudit maps the engine's entries onto the audit module's rows
// for the scope and op legs only. It records no actor (actor_id empty,
// no actor_subject): the actor mapping is cmd/core's syncAuditSeam
// alone, proven by the composition test in cmd/core
// (sync_audit_wire_test.go).
type syncIntAudit struct{ svc *audit.Service }

func (a syncIntAudit) RecordSync(ctx context.Context, e sync.AuditEntry) error {
	return a.svc.Record(ctx, audit.SensitiveAccess{
		Action:     e.Action,
		EntityType: "sync." + e.Scope,
		EntityID:   e.EntityID,
		Extra:      map[string]any{"scope": e.Scope, "op": e.Op, "client_id": e.ClientID, "status": e.Outcome},
	})
}

// syncSeedOrder inserts one invented order at a branch for a customer
// of the fixture's north org, registered for pattern cleanup.
func syncSeedOrder(t *testing.T, f *syncFixture, branchID string) string {
	t.Helper()
	var id string
	err := f.pool.QueryRow(context.Background(), `
INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, requested_date, org_id, tenant_id)
VALUES ($1, $2, $3, 'CONFIRMED', 'DELIVERY', CURRENT_DATE, $4::uuid, $5::uuid) RETURNING id::text`,
		"#SO-FXSYNC-"+uniqueSuffix(), testkit.TenantAlphaID, branchID,
		testkit.OrgNorthID, testkit.TenantAlphaID).Scan(&id)
	if err != nil {
		t.Fatalf("seed sync order: %v", err)
	}
	return id
}

// syncOpenRun crews one run at the north branch through the real
// service path and leaves it PLANNED; the day the battery's stops ride
// on dispatches only once its first stop is aboard (the module's own
// rule: an empty run has nothing to dispatch), which syncDispatchRun
// does beside the stop it walks.
func syncOpenRun(t *testing.T, f *syncFixture) Run {
	t.Helper()
	ctx := globalCtx(t, f.pool)
	run, err := f.svc.CreateRun(ctx, CreateRunInput{
		BranchID: testkit.BranchNorthAID, ScheduledDate: syncToday(),
		TruckID: f.truck, DriverID: f.driver, Notes: "FX sync fixture run",
	})
	if err != nil {
		t.Fatalf("fixture create run: %v", err)
	}
	return run
}

// syncDispatchRun sends the fixture run out, the walk that turns its
// READY stops LOADED, through the real service path.
func syncDispatchRun(t *testing.T, f *syncFixture, run Run) {
	t.Helper()
	ctx := globalCtx(t, f.pool)
	if _, err := f.svc.DispatchRun(ctx, run.ID); err != nil {
		t.Fatalf("fixture dispatch run: %v", err)
	}
}

// syncEnRouteStop schedules one order's stop onto the fixture run and
// walks it to EN_ROUTE through the store the device drives (the state
// a stop with something to photograph is in).
func syncEnRouteStop(t *testing.T, f *syncFixture, run Run) Stop {
	t.Helper()
	orderID := syncSeedOrder(t, f, testkit.BranchNorthAID)
	createKey := syncFreshKey()
	syncApplied(t, f, "stops", f.north, "create", createKey, map[string]any{
		"order_id": orderID, "scheduled_date": syncToday(), "run_id": run.ID,
		"address_line": "Fixture address", "city": "Fixture City",
	}, nil)
	stop := syncRowOf(t, f, "stops", f.north, createKey)
	syncDispatchRun(t, f, run)
	syncApplied(t, f, "stops", f.north, "update", syncFreshKey(), map[string]any{"id": stop.ID, "status": StopEnRoute}, nil)
	row, _, err := f.stops.Load(context.Background(), f.north, stop.ID)
	if err != nil || row.Data["status"] != StopEnRoute {
		t.Fatalf("fixture stop never reached EN_ROUTE: %+v err %v", row.Data, err)
	}
	return Stop{ID: stop.ID}
}

// - the small push helpers, named for readability -

// syncKey builds one fixed client id from the fixture's numbered block
// (deterministic ids keep cleanup and debugging simple); syncFreshKey
// mints one collision-free id beyond it for the battery-scale legs.
func syncKey(n int) string { return fmt.Sprintf("40000000-0000-4000-8000-%012d", n) }

var syncKeyCounter int64

func syncFreshKey() string {
	syncKeyCounter++
	return fmt.Sprintf("41000000-0000-4000-8000-%012d", 1000000+syncKeyCounter)
}

// syncOp builds one push operation; base is the seq the client based
// its write on, nil for a blind write.
func syncOp(kind, key string, payload map[string]any, base *int64) apigen.SyncPushOperation {
	return apigen.SyncPushOperation{ClientId: key, Op: kind, Payload: payload, BaseSeq: base}
}

// syncPush replays one outbox through the engine under a filter.
func syncPush(t *testing.T, f *syncFixture, scope string, filter sync.ScopeFilter, ops ...apigen.SyncPushOperation) []apigen.SyncPushResult {
	t.Helper()
	info, ok := sync.Lookup(scope)
	if !ok {
		t.Fatalf("scope %s missing from the registry", scope)
	}
	resp, err := f.engine.Push(context.Background(), info, filter, ops)
	if err != nil {
		t.Fatalf("push %s: %v", scope, err)
	}
	return resp.Results
}

// syncApplied pushes one operation and refuses anything but applied.
func syncApplied(t *testing.T, f *syncFixture, scope string, filter sync.ScopeFilter, kind, key string, payload map[string]any, base *int64) apigen.SyncPushResult {
	t.Helper()
	res := syncPush(t, f, scope, filter, syncOp(kind, key, payload, base))
	if len(res) != 1 || res[0].Status != "applied" || res[0].Seq == nil {
		t.Fatalf("%s %s %s: %+v", scope, kind, key, res[0])
	}
	return res[0]
}

// syncRowOf resolves one applied create's row id through the dedup
// lookup (the row the key landed on).
func syncRowOf(t *testing.T, f *syncFixture, scope string, filter sync.ScopeFilter, key string) sync.RowState {
	t.Helper()
	info, _ := sync.Lookup(scope)
	store, ok := f.engine.Store(scope)
	if !ok {
		t.Fatalf("scope %s not wired", scope)
	}
	row, applied, err := store.FindApplied(context.Background(), filter, key)
	if err != nil || !applied {
		_ = info
		t.Fatalf("find applied %s %s: applied=%v err=%v", scope, key, applied, err)
	}
	return row
}

// - the per-scope kits -

// runsKit: creates open a crewed run at the north branch (the desk's
// own birth shape), updates walk the run matrix one legal move and
// stamp the end-of-run note on a terminal row (the module accepts the
// note repeatedly), and the desk hook drives the real service paths
// (DispatchRun, CompleteRun) until the matrix is exhausted, then the
// direct seq-stamped note the pilot's terminal desk leg uses.
func runsKit(t *testing.T, f *syncFixture) syncharness.Kit {
	info, ok := sync.Lookup("runs")
	if !ok {
		panic("runs missing from the registry")
	}
	caller := globalCtx(t, f.pool)
	return syncharness.Kit{
		Scope:  info,
		Filter: f.north,
		Store:  f.runs,
		Create: func(id string) map[string]any {
			fields := map[string]any{
				"branch_id": testkit.BranchNorthAID, "scheduled_date": syncToday(),
				"truck_id": f.truck, "driver_id": f.driver,
			}
			if id != "" {
				fields["id"] = id
			}
			return fields
		},
		Update: func(row map[string]any) map[string]any {
			if to, ok := syncRunNextStatus(fmt.Sprint(row["status"])); ok {
				return map[string]any{"id": row["id"], "status": to}
			}
			return map[string]any{"id": row["id"], "notes": "closed from the truck"}
		},
		Chain: func(row map[string]any, n int) []map[string]any {
			out := make([]map[string]any, 0, n)
			status := fmt.Sprint(row["status"])
			for i := 0; i < n; i++ {
				if to, ok := syncRunNextStatus(status); ok {
					status = to
					out = append(out, map[string]any{"id": row["id"], "status": to})
					continue
				}
				out = append(out, map[string]any{"id": row["id"], "notes": fmt.Sprintf("closed from the truck, leg %d", i)})
			}
			return out
		},
		Desk: func(ctx context.Context, row map[string]any) error {
			id := fmt.Sprint(row["id"])
			var from string
			if err := f.pool.QueryRow(ctx, `SELECT status FROM delivery_runs WHERE id = $1::uuid`, id).Scan(&from); err != nil {
				return err
			}
			switch from {
			case RunPlanned:
				_, err := f.svc.DispatchRun(caller, id)
				return err
			case RunDispatched:
				_, err := f.svc.CompleteRun(caller, id)
				return err
			}
			// Terminal: the desk stamps a note and draws a seq from the
			// scope's own allocator, the same upsert every run write
			// path runs.
			var seq int64
			if err := f.pool.QueryRow(ctx,
				`INSERT INTO scope_sequences (scope) VALUES ('runs')
				 ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + 1
				 RETURNING next_value`).Scan(&seq); err != nil {
				return err
			}
			_, err := f.pool.Exec(ctx,
				`UPDATE delivery_runs SET notes = 'FX desk note', updated_at = now(), seq = $2 WHERE id = $1::uuid`, id, seq)
			return err
		},
	}
}

// syncRunNextStatus is the run matrix's forward walk.
func syncRunNextStatus(from string) (string, bool) {
	next := map[string]string{RunPlanned: RunDispatched, RunDispatched: RunCompleted}
	to, ok := next[from]
	return to, ok
}

// stopsKit: creates schedule one fresh order's stop onto the fixture's
// dispatched run (one live stop per order, the 0090 rule, so each
// create carries its own order), updates walk the stop matrix one
// legal move (the driver's own queue shape: loaded, en route, failed)
// and correct a failed stop's reason on a terminal row, and the desk
// hook drives the real AdvanceStop path until the matrix is exhausted.
func stopsKit(t *testing.T, f *syncFixture, run Run) syncharness.Kit {
	info, ok := sync.Lookup("stops")
	if !ok {
		panic("stops missing from the registry")
	}
	caller := globalCtx(t, f.pool)
	return syncharness.Kit{
		Scope:  info,
		Filter: f.north,
		Store:  f.stops,
		Create: func(id string) map[string]any {
			orderID := syncSeedOrder(t, f, testkit.BranchNorthAID)
			fields := map[string]any{
				"order_id": orderID, "scheduled_date": syncToday(), "run_id": run.ID,
			}
			if id != "" {
				fields["id"] = id
			}
			return fields
		},
		Update: func(row map[string]any) map[string]any {
			status := fmt.Sprint(row["status"])
			switch status {
			case StopReady:
				return map[string]any{"id": row["id"], "status": StopLoaded}
			case StopLoaded:
				return map[string]any{"id": row["id"], "status": StopEnRoute}
			case StopEnRoute:
				return map[string]any{"id": row["id"], "status": StopFailed, "failure_reason": "FX gate refused"}
			default:
				// Terminal: a reason correction on a failed stop, the
				// one non-status write the scope admits.
				return map[string]any{"id": row["id"], "failure_reason": "FX checked at the gate"}
			}
		},
		Chain: func(row map[string]any, n int) []map[string]any {
			out := make([]map[string]any, 0, n)
			current := row
			for i := 0; i < n; i++ {
				payload := stopChainPayload(i, current)
				out = append(out, payload)
				current = map[string]any{"id": row["id"], "status": payload["status"], "failure_reason": payload["failure_reason"]}
			}
			return out
		},
		Desk: func(ctx context.Context, row map[string]any) error {
			id := fmt.Sprint(row["id"])
			var from string
			if err := f.pool.QueryRow(ctx, `SELECT status FROM delivery_stops WHERE id = $1::uuid`, id).Scan(&from); err != nil {
				return err
			}
			switch from {
			case StopReady:
				_, err := f.svc.AdvanceStop(caller, id, StopLoaded, "")
				return err
			case StopLoaded:
				_, err := f.svc.AdvanceStop(caller, id, StopEnRoute, "")
				return err
			}
			// Terminal: the desk stamps the reason and draws a seq from
			// the scope's allocator, the same upsert every stop write
			// path runs.
			var seq int64
			if err := f.pool.QueryRow(ctx,
				`INSERT INTO scope_sequences (scope) VALUES ('stops')
				 ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + 1
				 RETURNING next_value`).Scan(&seq); err != nil {
				return err
			}
			_, err := f.pool.Exec(ctx,
				`UPDATE delivery_stops SET failure_reason = 'FX desk reason', updated_at = now(), seq = $2 WHERE id = $1::uuid`, id, seq)
			return err
		},
	}
}

// stopChainPayload builds the i-th leg of the stop walk: loaded, en
// route, failed, then reason corrections on the failed row.
func stopChainPayload(i int, row map[string]any) map[string]any {
	status := fmt.Sprint(row["status"])
	switch {
	case i == 0 || status == StopReady:
		return map[string]any{"id": row["id"], "status": StopLoaded}
	case i == 1 || status == StopLoaded:
		return map[string]any{"id": row["id"], "status": StopEnRoute}
	default:
		if fmt.Sprint(row["failure_reason"]) == "" || status != StopFailed {
			return map[string]any{"id": row["id"], "status": StopFailed, "failure_reason": "FX gate refused"}
		}
		return map[string]any{"id": row["id"], "failure_reason": fmt.Sprintf("FX checked at the gate, leg %d", i)}
	}
}

// podKit: creates capture one tiny invented proof photo against the
// fixture's EN_ROUTE stop, updates re-caption the record, and the desk
// hook folds the photos into the sealed POD record through the
// repository's own attach path (the write that bumps the row's seq
// server-side).
func podKit(t *testing.T, f *syncFixture, stop Stop) syncharness.Kit {
	info, ok := sync.Lookup("pod")
	if !ok {
		panic("pod missing from the registry")
	}
	tiny := "ZklYIFNZTkMgUEhPVE8=" // "FX SYNC PHOTO", invented bytes
	return syncharness.Kit{
		Scope:  info,
		Filter: f.north,
		Store:  f.pod,
		Create: func(id string) map[string]any {
			fields := map[string]any{
				"stop_id": stop.ID, "content_type": "image/png",
				"image_bytes_b64": tiny, "caption": "signed at the gate",
			}
			if id != "" {
				fields["id"] = id
			}
			return fields
		},
		Update: func(row map[string]any) map[string]any {
			return map[string]any{"id": row["id"], "caption": "signed at the gate, checked"}
		},
		Chain: func(row map[string]any, n int) []map[string]any {
			out := make([]map[string]any, 0, n)
			for i := 0; i < n; i++ {
				out = append(out, map[string]any{"id": row["id"], "caption": fmt.Sprintf("signed at the gate, leg %d", i)})
			}
			return out
		},
		Desk: func(ctx context.Context, row map[string]any) error {
			stopID := fmt.Sprint(row["stop_id"])
			_, err := NewRepository(f.pool).AttachPODPhotos(ctx, stopID, "FXSIG-DESK-"+uniqueSuffix(), time.Now().UTC())
			return err
		},
	}
}

// syncSeedStopAboard schedules one stop onto the run through the real
// service path and returns its id, the stop a fixture dispatch needs
// aboard (the module's own rule: an empty run has nothing to
// dispatch).
func syncSeedStopAboard(t *testing.T, f *syncFixture, run Run) string {
	t.Helper()
	ctx := globalCtx(t, f.pool)
	orderID := syncSeedOrder(t, f, testkit.BranchNorthAID)
	stop, err := f.svc.Schedule(ctx, ScheduleInput{
		OrderID: orderID, ScheduledDate: syncToday(),
		AddressLine: "Fixture address", City: "Fixture City",
	})
	if err != nil {
		t.Fatalf("fixture schedule: %v", err)
	}
	if _, err := f.svc.AssignStop(ctx, stop.ID, AssignInput{RunID: run.ID}); err != nil {
		t.Fatalf("fixture assign stop: %v", err)
	}
	return stop.ID
}

// syncHardDeleteStop removes a fixture stop outright (a fixture
// cleanup move, not a sync tombstone): the batteries read the whole
// table under one org, so the seed stop that let the run dispatch must
// not be a row the scope's pull can see, tombstone or item.
func syncHardDeleteStop(t *testing.T, f *syncFixture, stopID string) {
	t.Helper()
	if _, err := f.pool.Exec(context.Background(),
		`DELETE FROM delivery_pod_photos WHERE stop_id = $1::uuid`, stopID); err != nil {
		t.Fatalf("fixture stop photo cleanup: %v", err)
	}
	if _, err := f.pool.Exec(context.Background(),
		`DELETE FROM delivery_stops WHERE id = $1::uuid`, stopID); err != nil {
		t.Fatalf("fixture stop cleanup: %v", err)
	}
}

// - the batteries -

// TestSyncScopeBatteries: the per-scope contract battery, GATE-7 and
// the replay property for each of the three scopes, over the real
// tables, through the same Stores the served routes drive. The
// fixtures sequence so each scope's own table starts empty for the
// north org (the battery reads the org's whole feed): the runs battery
// runs first, then the stops fixture's seed stop is removed outright
// once it has let the run dispatch, then the stops battery, then the
// pod fixture's EN_ROUTE stop is built (a stops row, invisible to the
// pod scope) and the pod battery runs over the empty photo table.
func TestSyncScopeBatteries(t *testing.T) {
	f := newSyncFixture(t)

	t.Run("runs", func(t *testing.T) {
		kit := runsKit(t, f)
		syncharness.Contract(t, f.engine, kit)
		syncharness.Gate7(t, f.engine, kit)
		rng := rand.New(rand.NewSource(1671))
		syncharness.ReplayProperty(t, f.engine, kit, 8, rng.Intn)
	})

	stopsRun := syncOpenRun(t, f)
	seedStop := syncSeedStopAboard(t, f, stopsRun)
	syncDispatchRun(t, f, stopsRun)
	syncHardDeleteStop(t, f, seedStop)
	t.Run("stops", func(t *testing.T) {
		kit := stopsKit(t, f, stopsRun)
		syncharness.Contract(t, f.engine, kit)
		syncharness.Gate7(t, f.engine, kit)
		rng := rand.New(rand.NewSource(1672))
		syncharness.ReplayProperty(t, f.engine, kit, 8, rng.Intn)
	})

	podRun := syncOpenRun(t, f)
	podStop := syncEnRouteStop(t, f, podRun)
	t.Run("pod", func(t *testing.T) {
		kit := podKit(t, f, podStop)
		syncharness.Contract(t, f.engine, kit)
		syncharness.Gate7(t, f.engine, kit)
		rng := rand.New(rand.NewSource(1673))
		syncharness.ReplayProperty(t, f.engine, kit, 8, rng.Intn)
	})
}

// - the operator walkthrough -

// syncSnap snapshots the three feeds under one filter, the state
// compare the replay legs hang on.
func syncSnap(t *testing.T, f *syncFixture, filter sync.ScopeFilter) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, scope := range []string{"runs", "stops", "pod"} {
		info, _ := sync.Lookup(scope)
		resp, err := f.engine.Pull(context.Background(), info, filter, 0, 200)
		if err != nil {
			t.Fatalf("snap pull %s: %v", scope, err)
		}
		for _, it := range resp.Items {
			status := fmt.Sprint(it.Data["status"])
			if scope == "pod" {
				status = fmt.Sprint(it.Data["caption"])
			}
			out[scope+"/"+it.Id] = fmt.Sprintf("%d|%s", it.Seq, status)
		}
		for _, tb := range resp.Tombstones {
			out[scope+"/"+tb.Id] = fmt.Sprintf("tombstone|%d", tb.Seq)
		}
	}
	return out
}

// TestSyncWalkthrough: the operator walkthrough, scripted against the
// seeded database exactly as the acceptance words it. A driver closes
// a run offline with one cancelled stop, the outbox replays when
// signal returns, dispatch reads the closed run and the cancelled stop
// exactly as the ERP's own rule writes them, and a duplicate replay
// changes nothing. The steps log their outcomes so the PR body carries
// the transcript.
func TestSyncWalkthrough(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()
	desk := globalCtx(t, f.pool)

	// Step 1: dispatch builds the day: a crewed run, two stops on it,
	// the truck out. The service's own paths do every write.
	run, err := f.svc.CreateRun(desk, CreateRunInput{
		BranchID: testkit.BranchNorthAID, ScheduledDate: syncToday(),
		TruckID: f.truck, DriverID: f.driver, Notes: "FX walkthrough run",
	})
	if err != nil {
		t.Fatalf("walkthrough 1: create run: %v", err)
	}
	mkStop := func() string {
		orderID := syncSeedOrder(t, f, testkit.BranchNorthAID)
		stop, err := f.svc.Schedule(desk, ScheduleInput{
			OrderID: orderID, ScheduledDate: syncToday(),
			AddressLine: "Fixture address", City: "Fixture City",
		})
		if err != nil {
			t.Fatalf("walkthrough 1: schedule: %v", err)
		}
		if _, err := f.svc.AssignStop(desk, stop.ID, AssignInput{RunID: run.ID}); err != nil {
			t.Fatalf("walkthrough 1: assign: %v", err)
		}
		return stop.ID
	}
	stopA, stopB := mkStop(), mkStop()
	if _, err := f.svc.DispatchRun(desk, run.ID); err != nil {
		t.Fatalf("walkthrough 1: dispatch: %v", err)
	}
	t.Logf("walkthrough 1: dispatch crewed run %s (%s) and sent it out, stops %s and %s LOADED",
		run.RunNo, run.ID, stopA, stopB)

	// Step 2: the driver delivers stop B while the phone still has
	// signal: en route, then the POD, the harness's recording signer
	// standing in for the sealing provider and asserting the
	// attestation contract (a name, an affirmative, both present).
	if _, err := f.svc.AdvanceStop(desk, stopB, StopEnRoute, ""); err != nil {
		t.Fatalf("walkthrough 2: en route: %v", err)
	}
	if _, err := f.svc.CapturePOD(desk, stopB, PODInput{ReceivedBy: "Fixture Receiver", Affirmed: true}); err != nil {
		t.Fatalf("walkthrough 2: pod: %v", err)
	}
	t.Logf("walkthrough 2: stop %s delivered, POD sealed (typed name plus affirmative) while signal remained", stopB)

	// Step 3: the device pulls both feeds and goes dark.
	runsInfo, _ := sync.Lookup("runs")
	stopsInfo, _ := sync.Lookup("stops")
	runsPull, err := f.engine.Pull(ctx, runsInfo, f.north, 0, 50)
	if err != nil {
		t.Fatalf("walkthrough 3: runs pull: %v", err)
	}
	stopsPull, err := f.engine.Pull(ctx, stopsInfo, f.north, 0, 50)
	if err != nil {
		t.Fatalf("walkthrough 3: stops pull: %v", err)
	}
	var baseRun, baseA int64
	for _, it := range runsPull.Items {
		if it.Id == run.ID {
			baseRun = it.Seq
		}
	}
	for _, it := range stopsPull.Items {
		if it.Id == stopA {
			baseA = it.Seq
		}
	}
	if baseRun == 0 || baseA == 0 {
		t.Fatalf("walkthrough 3: the device never saw the run or the stop (run %d, stop %d)", baseRun, baseA)
	}
	t.Logf("walkthrough 3: device pulled the day, run seq %d, stop A seq %d, then signal dropped", baseRun, baseA)

	// Step 4: offline, the end-of-run summary queues two writes: stop A
	// withdrawn before its drop (the pre-departure cancel) and the run
	// closed. One queue, replayed in order when signal returns. The
	// close that arrives before the cancel is that operation's
	// rejected result, never a failed request, and the retry after the
	// cancel lands (a refused operation never sinks the queue).
	closeKey, cancelKey := syncFreshKey(), syncFreshKey()
	early := syncPush(t, f, "runs", f.north, syncOp("update", closeKey,
		map[string]any{"id": run.ID, "status": RunCompleted}, &baseRun))
	if len(early) != 1 || early[0].Status != "rejected" {
		t.Fatalf("walkthrough 4: the close before the cancel answered %+v, want rejected (an open stop stands)", early[0])
	}
	t.Logf("walkthrough 4: offline queue holds the cancel then the close; the early close answered rejected (%s)", *early[0].Error)
	res := syncPush(t, f, "stops", f.north, syncOp("update", cancelKey,
		map[string]any{"id": stopA, "status": StopCancelled}, &baseA))
	if len(res) != 1 || res[0].Status != "applied" {
		t.Fatalf("walkthrough 4: the cancel answered %+v", res[0])
	}
	cancelSeq := *res[0].Seq
	res = syncPush(t, f, "runs", f.north, syncOp("update", closeKey,
		map[string]any{"id": run.ID, "status": RunCompleted}, &baseRun))
	if len(res) != 1 || res[0].Status != "applied" {
		t.Fatalf("walkthrough 4: the retried close answered %+v", res[0])
	}
	closeSeq := *res[0].Seq
	if closeSeq <= baseRun {
		t.Fatalf("walkthrough 4: the close drew seq %d over base %d", closeSeq, baseRun)
	}
	t.Logf("walkthrough 4: replay in order: cancel applied (stops seq %d), then the close applied (runs seq %d)", cancelSeq, closeSeq)

	// Step 5: dispatch reads the day back: the closed run, the
	// cancelled stop, the delivered stop, exactly as the ERP's own rule
	// writes them (a run carrying a cancelled stop closes over the
	// settled day, the stop stays cancelled, the delivered stop keeps
	// its proof).
	detail, err := f.svc.RunDetail(desk, run.ID)
	if err != nil {
		t.Fatalf("walkthrough 5: read run: %v", err)
	}
	if detail.Run.Status != RunCompleted {
		t.Fatalf("walkthrough 5: run status %s, want COMPLETED", detail.Run.Status)
	}
	states := map[string]string{}
	var podRef string
	for _, st := range detail.Stops {
		states[st.ID] = st.Status
		if st.ID == stopB {
			podRef = st.PODSignedRef
		}
	}
	if states[stopA] != StopCancelled || states[stopB] != StopDelivered {
		t.Fatalf("walkthrough 5: stops read %v, want A CANCELLED and B DELIVERED", states)
	}
	if podRef == "" {
		t.Fatalf("walkthrough 5: the delivered stop lost its proof")
	}
	t.Logf("walkthrough 5: dispatch reads the run %s COMPLETED, stop A CANCELLED, stop B DELIVERED with its sealed proof", detail.Run.RunNo)

	// Step 6: a duplicate replay changes nothing.
	before := syncSnap(t, f, f.north)
	res = syncPush(t, f, "stops", f.north, syncOp("update", cancelKey,
		map[string]any{"id": stopA, "status": StopCancelled}, &baseA))
	if len(res) != 1 || res[0].Status != "applied" || *res[0].Seq != cancelSeq {
		t.Fatalf("walkthrough 6: the duplicate cancel answered %+v, want applied at the retained seq %d", res[0], cancelSeq)
	}
	res = syncPush(t, f, "runs", f.north, syncOp("update", closeKey,
		map[string]any{"id": run.ID, "status": RunCompleted}, &baseRun))
	if len(res) != 1 || res[0].Status != "applied" || *res[0].Seq != closeSeq {
		t.Fatalf("walkthrough 6: the duplicate close answered %+v, want applied at the retained seq %d", res[0], closeSeq)
	}
	after := syncSnap(t, f, f.north)
	if fmt.Sprint(before) != fmt.Sprint(after) {
		t.Fatalf("walkthrough 6: the duplicate replay moved the server state: %v, want %v", after, before)
	}
	t.Logf("walkthrough 6: duplicate replay applied through dedup at the retained seqs, server state unchanged")
}

// TestSyncOrgIsolationAndTenantClosed: rows of another org never pull
// through the north filter on any of the three scopes, the south
// filter reads only its own, and a tenant filter answers nothing and
// refuses every write: the drive-run scopes are org scopes, and the
// adapters fail closed on a tenant predicate besides (the route's 403
// is the unit suite's leg).
func TestSyncOrgIsolationAndTenantClosed(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()

	// A south run through the runs store's own create (no crew: a
	// planned row is enough to be a row).
	southRunKey := syncFreshKey()
	res := syncPush(t, f, "runs", f.south, syncOp("create", southRunKey, map[string]any{
		"branch_id": testkit.BranchSouthAID, "scheduled_date": syncToday(),
	}, nil))
	if len(res) != 1 || res[0].Status != "applied" {
		t.Fatalf("south run create: %+v", res[0])
	}
	// A south order and an EN_ROUTE stop it hangs from, seeded directly
	// (the fixture row the south photo proves).
	var southOrder, southStop string
	if err := f.pool.QueryRow(ctx, `
INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, requested_date, org_id, tenant_id)
VALUES ($1, $2, $3, 'CONFIRMED', 'DELIVERY', CURRENT_DATE, $4::uuid, $5::uuid) RETURNING id::text`,
		"#SO-FXSYNC-S-"+uniqueSuffix(), testkit.TenantGammaID, testkit.BranchSouthAID,
		testkit.OrgSouthID, testkit.TenantGammaID).Scan(&southOrder); err != nil {
		t.Fatalf("south order: %v", err)
	}
	if err := f.pool.QueryRow(ctx, `
INSERT INTO delivery_stops (branch_id, order_id, scheduled_date, status, org_id)
VALUES ($1::uuid, $2::uuid, CURRENT_DATE, 'EN_ROUTE', $3::uuid) RETURNING id::text`,
		testkit.BranchSouthAID, southOrder, testkit.OrgSouthID).Scan(&southStop); err != nil {
		t.Fatalf("south stop: %v", err)
	}
	// A south photo through the pod store's own create.
	southPhotoKey := syncFreshKey()
	res = syncPush(t, f, "pod", f.south, syncOp("create", southPhotoKey, map[string]any{
		"stop_id": southStop, "content_type": "image/png",
		"image_bytes_b64": "ZklYIFNPVVE=", "caption": "south proof",
	}, nil))
	if len(res) != 1 || res[0].Status != "applied" {
		t.Fatalf("south photo create: %+v", res[0])
	}

	for _, scope := range []string{"runs", "stops", "pod"} {
		info, _ := sync.Lookup(scope)
		north, err := f.engine.Pull(ctx, info, f.north, 0, 200)
		if err != nil {
			t.Fatalf("north pull %s: %v", scope, err)
		}
		for _, it := range north.Items {
			if scope == "runs" && it.Id == syncRowOf(t, f, "runs", f.south, southRunKey).ID {
				t.Fatalf("the north org's runs pull saw the south org's run")
			}
			if scope == "stops" && it.Id == southStop {
				t.Fatalf("the north org's stops pull saw the south org's stop")
			}
			if scope == "pod" && it.Id == syncRowOf(t, f, "pod", f.south, southPhotoKey).ID {
				t.Fatalf("the north org's pod pull saw the south org's photo")
			}
		}
		south, err := f.engine.Pull(ctx, info, f.south, 0, 200)
		if err != nil {
			t.Fatalf("south pull %s: %v", scope, err)
		}
		if len(south.Items) == 0 && scope != "stops" {
			t.Fatalf("the south org's %s pull saw nothing of its own", scope)
		}
	}

	// The tenant filter: reads answer nothing, writes refuse.
	tenant := sync.ScopeFilter{OrgID: testkit.OrgNorthID, TenantID: testkit.TenantAlphaID, Tenant: true, Actor: testkit.SubPortalAdminAlpha}
	for _, scope := range []string{"runs", "stops", "pod"} {
		info, _ := sync.Lookup(scope)
		pull, err := f.engine.Pull(ctx, info, tenant, 0, 200)
		if err != nil {
			t.Fatalf("tenant pull %s: %v", scope, err)
		}
		if len(pull.Items) != 0 || len(pull.Tombstones) != 0 {
			t.Fatalf("a tenant filter read org rows through %s: %+v", scope, pull)
		}
		res := syncPush(t, f, scope, tenant, syncOp("create", syncFreshKey(), map[string]any{}, nil))
		if len(res) != 1 || res[0].Status != "rejected" {
			t.Fatalf("a tenant filter wrote through %s: %+v", scope, res[0])
		}
	}
}

// TestSyncRaceCleanSeqAllocation: concurrent pushes through the runs
// and pod scopes draw pairwise-distinct seqs from the per-scope
// allocator, race-clean under the upsert every adapter write runs.
func TestSyncRaceCleanSeqAllocation(t *testing.T) {
	f := newSyncFixture(t)
	run := syncOpenRun(t, f)
	stop := syncEnRouteStop(t, f, run)

	const workers, per = 8, 3
	race := func(scope string, payload func() map[string]any) {
		t.Helper()
		info, _ := sync.Lookup(scope)
		seqs := make(chan int64, workers*per)
		done := make(chan int, workers*per)
		for w := 0; w < workers; w++ {
			go func() {
				for i := 0; i < per; i++ {
					resp, err := f.engine.Push(context.Background(), info, f.north,
						[]apigen.SyncPushOperation{{ClientId: syncFreshKey(), Op: "create", Payload: payload()}})
					if err != nil || len(resp.Results) != 1 || resp.Results[0].Status != "applied" || resp.Results[0].Seq == nil {
						done <- 0
						return
					}
					seqs <- *resp.Results[0].Seq
					done <- 1
				}
			}()
		}
		ok := 0
		for i := 0; i < workers*per; i++ {
			ok += <-done
		}
		if ok != workers*per {
			t.Fatalf("%s: %d of %d concurrent creates applied", scope, ok, workers*per)
		}
		close(seqs)
		seen := map[int64]bool{}
		for seq := range seqs {
			if seen[seq] {
				t.Fatalf("%s: the allocator handed seq %d to two concurrent pushes", scope, seq)
			}
			seen[seq] = true
		}
		t.Logf("%s: %d concurrent pushes drew %d pairwise-distinct seqs", scope, workers*per, len(seen))
	}
	race("runs", func() map[string]any {
		return map[string]any{
			"branch_id": testkit.BranchNorthAID, "scheduled_date": syncToday(),
			"truck_id": f.truck, "driver_id": f.driver,
		}
	})
	race("pod", func() map[string]any {
		return map[string]any{
			"stop_id": stop.ID, "content_type": "image/png",
			"image_bytes_b64": "ZklYIFNZTkMgUEhPVE8=", "caption": "race proof",
		}
	})
}

// TestSyncStopsPayloadOverTheFeed: the stops feed's payload, pulled
// from the real projection over the real row, serialises only the
// contract's DispatchStop fields (the DB-free leg pins the marshal;
// this leg pins the query).
func TestSyncStopsPayloadOverTheFeed(t *testing.T) {
	f := newSyncFixture(t)
	run := syncOpenRun(t, f)
	key := syncFreshKey()
	syncApplied(t, f, "stops", f.north, "create", key, map[string]any{
		"order_id":       syncSeedOrder(t, f, testkit.BranchNorthAID),
		"scheduled_date": syncToday(), "run_id": run.ID,
		"address_line": "Fixture address", "city": "Fixture City",
		"contact_name": "Fixture contact", "instructions": "Fixture instructions",
	}, nil)
	stopsInfo, _ := sync.Lookup("stops")
	pull, err := f.engine.Pull(context.Background(), stopsInfo, f.north, 0, 10)
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	if len(pull.Items) != 1 {
		t.Fatalf("pull carried %d items, want 1", len(pull.Items))
	}
	for k := range pull.Items[0].Data {
		if !contractStopFields[k] {
			t.Fatalf("the stops feed carries %q, which the contract's DispatchStop schema does not declare", k)
		}
	}
	for _, k := range []string{"value_cents", "freight_cents"} {
		if _, ok := pull.Items[0].Data[k]; ok {
			t.Fatalf("the stops feed carries the gated money leg %q", k)
		}
	}
}

// TestSyncAuditRows: every push operation writes its audit row through
// the real audit service, the per-operation half of the audit
// invariant.
func TestSyncAuditRows(t *testing.T) {
	f := newSyncFixture(t)
	syncApplied(t, f, "runs", f.north, "create", syncFreshKey(), map[string]any{
		"branch_id": testkit.BranchNorthAID, "scheduled_date": syncToday(),
		"truck_id": f.truck, "driver_id": f.driver,
	}, nil)
	var n int
	if err := f.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE entity_type = 'sync.runs' AND action = 'SYNC_PUSH'`).Scan(&n); err != nil {
		t.Fatalf("audit count: %v", err)
	}
	if n < 1 {
		t.Fatalf("no SYNC_PUSH audit rows landed on the runs scope")
	}
	t.Logf("audit: %d SYNC_PUSH rows on the runs scope", n)
}

// TestSyncModuleInvariantsHold: the adapters enforce the module's own
// rules through sync: DELIVERED is the pod door's alone, an illegal
// jump is that operation's rejected result, a close over an open stop
// refuses, and a create against an unknown order refuses.
func TestSyncModuleInvariantsHold(t *testing.T) {
	f := newSyncFixture(t)
	run := syncOpenRun(t, f)
	stop := syncEnRouteStop(t, f, run)

	// DELIVERED through sync is refused with the reason naming the
	// door, exactly as AdvanceStop answers.
	res := syncPush(t, f, "stops", f.north, syncOp("update", syncFreshKey(),
		map[string]any{"id": stop.ID, "status": StopDelivered}, nil))
	if len(res) != 1 || res[0].Status != "rejected" || !strings.Contains(*res[0].Error, "proof-of-delivery") {
		t.Fatalf("DELIVERED through sync answered %+v", res[0])
	}
	t.Logf("module invariant: EN_ROUTE to DELIVERED through sync answered rejected (%s)", *res[0].Error)

	// An illegal stop jump (READY to FAILED without the truck leaving
	// the yard, tried on a fresh stop) is rejected.
	key := syncFreshKey()
	syncApplied(t, f, "stops", f.north, "create", key, map[string]any{
		"order_id":       syncSeedOrder(t, f, testkit.BranchNorthAID),
		"scheduled_date": syncToday(), "run_id": run.ID,
	}, nil)
	fresh := syncRowOf(t, f, "stops", f.north, key)
	res = syncPush(t, f, "stops", f.north, syncOp("update", syncFreshKey(),
		map[string]any{"id": fresh.ID, "status": StopFailed, "failure_reason": "FX jump"}, nil))
	if len(res) != 1 || res[0].Status != "rejected" {
		t.Fatalf("the illegal READY to FAILED jump answered %+v", res[0])
	}

	// A run close over an open stop refuses (the settled-set
	// precondition), and a create against an unknown order refuses.
	runKey := syncFreshKey()
	syncApplied(t, f, "runs", f.north, "create", runKey, map[string]any{
		"branch_id": testkit.BranchNorthAID, "scheduled_date": syncToday(),
		"truck_id": f.truck, "driver_id": f.driver,
	}, nil)
	openRun := syncRowOf(t, f, "runs", f.north, runKey)
	// A stop on the new run, left READY: the day has an open stop.
	stopKey := syncFreshKey()
	syncApplied(t, f, "stops", f.north, "create", stopKey, map[string]any{
		"order_id":       syncSeedOrder(t, f, testkit.BranchNorthAID),
		"scheduled_date": syncToday(), "run_id": openRun.ID,
	}, nil)
	res = syncPush(t, f, "runs", f.north, syncOp("update", syncFreshKey(),
		map[string]any{"id": openRun.ID, "status": RunDispatched}, nil))
	if len(res) != 1 || res[0].Status != "applied" {
		t.Fatalf("the crewed run refused to dispatch: %+v", res[0])
	}
	res = syncPush(t, f, "runs", f.north, syncOp("update", syncFreshKey(),
		map[string]any{"id": openRun.ID, "status": RunCompleted}, nil))
	if len(res) != 1 || res[0].Status != "rejected" {
		t.Fatalf("the close over an open stop answered %+v, want rejected", res[0])
	}
	t.Logf("module invariant: the close over an open stop answered rejected (%s)", *res[0].Error)

	// A create naming an unknown order and one naming another org's
	// order answer byte-identical not-founds, so existence never leaks
	// across the scope boundary (the round-2 finding, the pilot's
	// cross-scope posture).
	unknown := syncPush(t, f, "stops", f.north, syncOp("create", syncFreshKey(), map[string]any{
		"order_id": "3ef10000-0000-4000-8000-0f0000000002", "scheduled_date": syncToday(),
	}, nil))
	foreign := syncPush(t, f, "stops", f.north, syncOp("create", syncFreshKey(), map[string]any{
		"order_id": testkit.OrderGammaDraftID, "scheduled_date": syncToday(),
	}, nil))
	if len(unknown) != 1 || unknown[0].Status != "rejected" || unknown[0].Error == nil {
		t.Fatalf("the unknown order create answered %+v", unknown[0])
	}
	if len(foreign) != 1 || foreign[0].Status != "rejected" || foreign[0].Error == nil {
		t.Fatalf("the foreign org order create answered %+v", foreign[0])
	}
	if *unknown[0].Error != *foreign[0].Error {
		t.Fatalf("the unknown (%s) and foreign (%s) order refusals differ: existence leaks", *unknown[0].Error, *foreign[0].Error)
	}
	t.Logf("module invariant: the unknown and the foreign-org order creates answer one refusal (%s)", *unknown[0].Error)
}

// TestSyncReviewRound1Fixes: the two round-1 review findings, each
// pinned by its own leg: a cancelled run's READY stops fall back to
// unassigned WITH a stops-scope seq (the unassignment is a stop change
// a device's pull must see), and an empty reason correction on a
// failed stop is that operation's refusal, never a database CHECK
// tripping an outage.
func TestSyncReviewRound1Fixes(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()

	// F1: the cancel arm's unassignment is sync-visible on the stop.
	runKey := syncFreshKey()
	syncApplied(t, f, "runs", f.north, "create", runKey, map[string]any{
		"branch_id": testkit.BranchNorthAID, "scheduled_date": syncToday(),
		"truck_id": f.truck, "driver_id": f.driver,
	}, nil)
	run := syncRowOf(t, f, "runs", f.north, runKey)
	stopKey := syncFreshKey()
	syncApplied(t, f, "stops", f.north, "create", stopKey, map[string]any{
		"order_id":       syncSeedOrder(t, f, testkit.BranchNorthAID),
		"scheduled_date": syncToday(), "run_id": run.ID,
	}, nil)
	stop := syncRowOf(t, f, "stops", f.north, stopKey)
	syncApplied(t, f, "runs", f.north, "update", syncFreshKey(),
		map[string]any{"id": run.ID, "status": RunCancelled}, nil)
	after, ok, err := f.stops.Load(ctx, f.north, stop.ID)
	if err != nil || !ok {
		t.Fatalf("F1: the stop vanished on the run cancel: ok=%v err=%v", ok, err)
	}
	if after.Data["run_id"] != nil && fmt.Sprint(after.Data["run_id"]) != "" {
		t.Fatalf("F1: the stop still rides the cancelled run: %+v", after.Data)
	}
	if after.Seq <= stop.Seq {
		t.Fatalf("F1: the unassignment drew no stops-scope seq (%d then %d): a device pull would never see it", stop.Seq, after.Seq)
	}
	t.Logf("F1: the cancelled run's stop fell back to unassigned, stops seq %d to %d", stop.Seq, after.Seq)

	// F2: an empty reason correction on a failed stop is refused as
	// that operation, and a second live stop for a served order is
	// answered as a refusal, never an outage.
	f2Run := syncOpenRun(t, f)
	f2Stop := syncEnRouteStop(t, f, f2Run)
	syncApplied(t, f, "stops", f.north, "update", syncFreshKey(),
		map[string]any{"id": f2Stop.ID, "status": StopFailed, "failure_reason": "FX reason"}, nil)
	res := syncPush(t, f, "stops", f.north, syncOp("update", syncFreshKey(),
		map[string]any{"id": f2Stop.ID, "failure_reason": "  "}, nil))
	if len(res) != 1 || res[0].Status != "rejected" || !strings.Contains(*res[0].Error, "needs a reason") {
		t.Fatalf("F2: the empty reason correction answered %+v", res[0])
	}
	t.Logf("F2: the empty reason correction answered rejected (%s)", *res[0].Error)

	orderID := syncSeedOrder(t, f, testkit.BranchNorthAID)
	syncApplied(t, f, "stops", f.north, "create", syncFreshKey(), map[string]any{
		"order_id": orderID, "scheduled_date": syncToday(), "run_id": f2Run.ID,
	}, nil)
	res = syncPush(t, f, "stops", f.north, syncOp("create", syncFreshKey(), map[string]any{
		"order_id": orderID, "scheduled_date": syncToday(), "run_id": f2Run.ID,
	}, nil))
	if len(res) != 1 || res[0].Status != "rejected" {
		t.Fatalf("F2: the second live stop for a served order answered %+v, want a refusal", res[0])
	}
	if msg := *res[0].Error; !strings.Contains(msg, "live delivery") && !strings.Contains(msg, "already has a delivery") {
		t.Fatalf("F2: the second live stop's refusal %q does not name the standing delivery", msg)
	}
	t.Logf("F2: the second live stop for a served order answered rejected (%s)", *res[0].Error)
}

// TestValueEqualWriteKeepsStandingSeq pins the value-equal write arm deterministically
// (the independent review's P2 on this pull request: the arm was reached only by the
// property test's random rounds before). A write whose declared values the row already
// carries must answer applied with the standing row and draw NO fresh seq, under a
// fresh idempotency key; and a different-valued write under its own fresh key must
// still draw one, so this pin cannot pass by never allocating at all.
func TestValueEqualWriteKeepsStandingSeq(t *testing.T) {
	f := newSyncFixture(t)
	run := syncOpenRun(t, f)
	ctx := globalCtx(t, f.pool)

	runsInfo, ok := sync.Lookup("runs")
	if !ok {
		t.Fatalf("runs scope missing from the registry")
	}
	pull, err := f.engine.Pull(ctx, runsInfo, f.north, 0, 50)
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	var base int64
	for _, it := range pull.Items {
		if it.Id == run.ID {
			base = it.Seq
		}
	}
	if base == 0 {
		t.Fatalf("the fixture run never reached the feed")
	}

	// A different-valued note lands and draws a seq.
	first := syncApplied(t, f, "runs", f.north, "update", syncFreshKey(),
		map[string]any{"id": run.ID, "notes": "value equal pin first"}, &base)
	if *first.Seq <= base {
		t.Fatalf("the first write drew seq %d at base %d; a fresh fact must draw a fresh seq", *first.Seq, base)
	}

	// The SAME declared values under a FRESH key: applied at the standing seq, nothing drawn.
	same := syncApplied(t, f, "runs", f.north, "update", syncFreshKey(),
		map[string]any{"id": run.ID, "notes": "value equal pin first"}, first.Seq)
	if *same.Seq != *first.Seq {
		t.Fatalf("the value-equal write answered seq %d, want the standing %d (a superseded replay must not move the row's feed position)", *same.Seq, *first.Seq)
	}

	// A different value under its own fresh key draws again.
	next := syncApplied(t, f, "runs", f.north, "update", syncFreshKey(),
		map[string]any{"id": run.ID, "notes": "value equal pin second"}, same.Seq)
	if *next.Seq <= *same.Seq {
		t.Fatalf("the second fresh fact drew seq %d after %d; only equal values stand still", *next.Seq, *same.Seq)
	}
}
