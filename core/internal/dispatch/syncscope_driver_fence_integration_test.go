//go:build integration

// syncscope_driver_fence_integration_test.go: the driver's sync read held
// to its own run (the HH Drive fence, roadmap 1.4 and ROLE-DEFAULTS-r1
// Driver question 1). The sync door is one door for every scope, so the
// fence lives in the stores the door opens onto, not at the route: a
// driver caller's runs, stops, pod, loads and delivery_stops pulls answer
// only the rows of the runs assigned to the driver record linked to that
// login, and every desk caller keeps the org scope they have today.
//
// The identity chain the fence rides, verified at source: the sync
// handler stamps ScopeFilter.Actor with the caller's platform subject
// (the token's sub, sync/handler.go scopeFilter), the adapters' linker
// seam resolves it to the ERP users.id (users.platform_user_id, the B4
// ruling), and dispatch's own dispatch_drivers.user_id carries that
// users.id while delivery_runs.driver_id carries the
// dispatch_drivers.id: so Actor reaches the driver's runs through the
// module's own table with no cross-module read.
package dispatch

import (
	"context"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	erpidentity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// fenceWorld is the fence's proof world: two linked drivers with one run,
// one stop and one POD photo each at the north branch, the three
// drive-run adapters plus the two board adapters wired into one engine
// exactly as the composition root wires them, and the filters the
// middleware would stamp for a driver, a colleague driver, a desk caller
// and a driver login that links to no driver record.
type fenceWorld struct {
	pool    *pgxpool.Pool
	role    SyncRoleResolver
	engine  *sync.Engine
	runs    *RunsSyncStore
	stops   *StopsSyncStore
	pod     *PODSyncStore
	runA    Run
	runB    Run
	stopA   string
	stopB   string
	driverA sync.ScopeFilter
	driverB sync.ScopeFilter
	desk    sync.ScopeFilter
	linked  sync.ScopeFilter
	bare    sync.ScopeFilter
}

// newFenceWorld builds the world. Driver A is the kit's seeded driver
// login (testkit.SubDriver, the users row the linker resolves); driver B
// is a second invented login with its own grant, user row and linked
// driver record, so the two drivers share the org, the branch and the
// truck and differ only in who is calling.
func newFenceWorld(t *testing.T) *fenceWorld {
	t.Helper()
	h := newHarness(t)
	pool := h.pool
	ctx := globalCtx(t, pool)
	if err := testkit.Build(ctx, testkit.PgxPool(pool)); err != nil {
		t.Fatalf("fixture build: %v", err)
	}

	driverA := seedLinkedDriver(t, pool, testkit.BranchNorthAID, testkit.UserDriverID)
	subB, userB := seedFenceLogin(t, pool)
	driverB := seedLinkedDriver(t, pool, testkit.BranchNorthAID, userB)

	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM delivery_pod_photos p USING delivery_stops s
			WHERE p.stop_id = s.id AND s.org_id = $1::uuid`, testkit.OrgNorthID)
		_, _ = pool.Exec(c, `DELETE FROM delivery_stops WHERE org_id = $1::uuid`, testkit.OrgNorthID)
		_, _ = pool.Exec(c, `DELETE FROM delivery_runs WHERE org_id = $1::uuid`, testkit.OrgNorthID)
		_, _ = pool.Exec(c, `DELETE FROM dispatch_drivers WHERE user_id = $1::uuid`, testkit.UserDriverID)
		_, _ = pool.Exec(c, `DELETE FROM orders WHERE order_no LIKE '#SO-FXSYNC-%%' AND org_id = $1::uuid`, testkit.OrgNorthID)
		_, _ = pool.Exec(c, `DELETE FROM scope_sequences WHERE scope IN ('runs','stops','pod')`)
		_ = testkit.Cleanup(c, testkit.PgxPool(pool))
	})

	// The role seam the composition root wires: identity's own
	// resolver, bridged to the ERP role code, so the fence binds the
	// DRIVERS role (an unlinked driver login reads nothing rather than
	// everything, and a linked desk login keeps the org scope).
	roles, err := erpidentity.NewStaffAuthorityResolver(pool)
	if err != nil {
		t.Fatalf("role resolver: %v", err)
	}
	driverRole := func(ctx context.Context, platformUserID, orgID string) (string, bool, error) {
		a, has, err := roles.ResolveStaff(ctx, platformUserID, orgID)
		if err != nil || !has {
			return "", has, err
		}
		return identity.FromAuthority(a).RoleCode, true, nil
	}
	linker := linkedSyncUser(pool)
	runsStore := NewRunsSyncStore(pool, linker).WithDriverRole(driverRole)
	stopsStore := NewStopsSyncStore(pool, testOrders{pool: pool}, linker).WithDriverRole(driverRole)
	podStore := NewPODSyncStore(pool, linker).WithDriverRole(driverRole)
	engine := sync.NewEngine()
	for name, store := range map[string]sync.Store{
		"runs": runsStore, "stops": stopsStore, "pod": podStore,
		"loads": NewLoadsSyncStore(stopsStore), "delivery_stops": NewDeliveryStopsSyncStore(stopsStore),
	} {
		if err := engine.RegisterScope(name, store); err != nil {
			t.Fatalf("register %s: %v", name, err)
		}
	}

	// An unlinked driver login (the DRIVERS role, no driver record: the
	// "you are not set up as a driver" state) and a linked desk login
	// (a sales manager who also holds a driver record: the two-role
	// edge the REST surface resolves by role, not record).
	subUnlinked, _ := seedFenceLogin(t, pool)
	seedLinkedDriver(t, pool, testkit.BranchNorthAID, testkit.UserSalesMgrID)

	runA := seedRunForDriver(t, h, ctx, testkit.BranchNorthAID, driverA)
	runB := seedRunForDriver(t, h, ctx, testkit.BranchNorthAID, driverB)
	stopA := fenceStopOnRun(t, h, ctx, runA)
	stopB := fenceStopOnRun(t, h, ctx, runB)
	fenceSeedPhoto(t, h, ctx, podStore, runB, stopB)

	return &fenceWorld{
		pool: pool, role: driverRole, engine: engine, runs: runsStore, stops: stopsStore, pod: podStore,
		runA: runA, runB: runB, stopA: stopA, stopB: stopB,
		driverA: sync.ScopeFilter{OrgID: testkit.OrgNorthID, Actor: testkit.SubDriver},
		driverB: sync.ScopeFilter{OrgID: testkit.OrgNorthID, Actor: subB},
		desk:    sync.ScopeFilter{OrgID: testkit.OrgNorthID, Actor: testkit.SubInsideSales},
		linked:  sync.ScopeFilter{OrgID: testkit.OrgNorthID, Actor: testkit.SubSalesManager},
		bare:    sync.ScopeFilter{OrgID: testkit.OrgNorthID, Actor: subUnlinked},
	}
}

// seedFenceLogin plants a second driver login: a users row, a DRIVERS
// grant at the north branch and no driver record, and returns the
// platform subject and the users.id the linker resolves.
func seedFenceLogin(t *testing.T, pool *pgxpool.Pool) (subject, userID string) {
	t.Helper()
	subject = fmt.Sprintf("fxsync-driver-b-%s", uniqueSuffix())
	var org string
	if err := pool.QueryRow(context.Background(),
		`SELECT org_id::text FROM users WHERE id = $1::uuid`, testkit.UserDriverID).Scan(&org); err != nil {
		t.Fatalf("read the kit org: %v", err)
	}
	if err := pool.QueryRow(context.Background(), `
INSERT INTO users (email, display_name, status, platform_user_id, org_id)
VALUES ($1, 'FX Sync Driver B', 'ACTIVE', $2, $3::uuid) RETURNING id::text`,
		subject+"@fixture.test", subject, org).Scan(&userID); err != nil {
		t.Fatalf("seed driver B login: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
INSERT INTO staff_role_grant (platform_user_id, role_id, org_id, branch_id)
VALUES ($1, 'driver', $2::uuid, $3::uuid)`,
		subject, org, testkit.BranchNorthAID); err != nil {
		t.Fatalf("seed driver B grant: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM dispatch_drivers WHERE user_id = $1::uuid`, userID)
		_, _ = pool.Exec(c, `DELETE FROM staff_role_grant WHERE platform_user_id = $1`, subject)
		_, _ = pool.Exec(c, `DELETE FROM users WHERE id = $1::uuid`, userID)
	})
	return subject, userID
}

// fenceStopOnRun schedules one invented order's stop onto the run through
// the real service path, the desk's own route to a stop row.
func fenceStopOnRun(t *testing.T, h *harness, ctx context.Context, run Run) string {
	t.Helper()
	orderID := syncSeedOrder(t, &syncFixture{pool: h.pool, svc: h.svc}, testkit.BranchNorthAID)
	stop, err := h.svc.Schedule(ctx, ScheduleInput{
		OrderID: orderID, ScheduledDate: syncToday(),
		AddressLine: "Fence address", City: "Fence City",
	})
	if err != nil {
		t.Fatalf("schedule stop on %s: %v", run.ID, err)
	}
	if _, err := h.svc.AssignStop(ctx, stop.ID, AssignInput{RunID: run.ID}); err != nil {
		t.Fatalf("assign stop to %s: %v", run.ID, err)
	}
	return stop.ID
}

// fenceSeedPhoto walks the stop to EN_ROUTE through the real service
// path (a proof photo needs a stop with something to photograph) and
// captures one POD photo record against it through the store the device
// drives, under the desk filter (a caller the fence must leave alone),
// so driver B's stop carries proof a colleague must not read.
func fenceSeedPhoto(t *testing.T, h *harness, ctx context.Context, pod *PODSyncStore, run Run, stopID string) {
	t.Helper()
	if _, err := h.svc.DispatchRun(ctx, run.ID); err != nil {
		t.Fatalf("dispatch run %s: %v", run.ID, err)
	}
	if _, err := h.svc.AdvanceStop(ctx, stopID, StopEnRoute, ""); err != nil {
		t.Fatalf("advance stop %s to EN_ROUTE: %v", stopID, err)
	}
	desk := sync.ScopeFilter{OrgID: testkit.OrgNorthID, Actor: testkit.SubInsideSales}
	if _, err := pod.Create(context.Background(), desk, syncFreshKey(), map[string]any{
		"stop_id": stopID, "content_type": "image/png",
		"image_bytes_b64": "RkVOQ0UgUEhPVE8=", "caption": "colleague proof",
	}); err != nil {
		t.Fatalf("seed photo on %s: %v", stopID, err)
	}
}

// fencePull drives one scope's pull under a filter and returns the entity
// ids the caller saw.
func fencePull(t *testing.T, w *fenceWorld, scope string, f sync.ScopeFilter) []string {
	t.Helper()
	info, ok := sync.Lookup(scope)
	if !ok {
		t.Fatalf("scope %s missing from the registry", scope)
	}
	resp, err := w.engine.Pull(context.Background(), info, f, 0, 200)
	if err != nil {
		t.Fatalf("pull %s: %v", scope, err)
	}
	ids := make([]string, 0, len(resp.Items))
	for _, e := range resp.Items {
		ids = append(ids, e.Id)
	}
	return ids
}

// fenceHas answers whether ids hold id.
func fenceHas(ids []string, id string) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

// TestIntegration_DriverSyncFence_RunsHeldToTheCallingDriver is the
// fence's own leg: a driver's runs pull answers the runs assigned to the
// driver record linked to that login and nothing else, the colleague's
// pull answers theirs, and the desk caller keeps the org list.
func TestIntegration_DriverSyncFence_RunsHeldToTheCallingDriver(t *testing.T) {
	w := newFenceWorld(t)

	idsA := fencePull(t, w, "runs", w.driverA)
	if !fenceHas(idsA, w.runA.ID) {
		t.Errorf("driver A's runs pull is missing their own run %s: %v", w.runA.ID, idsA)
	}
	if fenceHas(idsA, w.runB.ID) {
		t.Errorf("driver A's runs pull sees driver B's run %s: %v", w.runB.ID, idsA)
	}

	idsB := fencePull(t, w, "runs", w.driverB)
	if !fenceHas(idsB, w.runB.ID) {
		t.Errorf("driver B's runs pull is missing their own run %s: %v", w.runB.ID, idsB)
	}
	if fenceHas(idsB, w.runA.ID) {
		t.Errorf("driver B's runs pull sees driver A's run %s: %v", w.runA.ID, idsB)
	}

	desk := fencePull(t, w, "runs", w.desk)
	if !fenceHas(desk, w.runA.ID) || !fenceHas(desk, w.runB.ID) {
		t.Errorf("the desk caller's runs pull must keep both runs (managers unchanged): %v", desk)
	}
}

// TestIntegration_DriverSyncFence_StopsHeldToTheCallingDriver walks the
// stops scope and both board scopes over delivery_stops (loads and
// delivery_stops ride the stops adapter's own reads): a driver sees only
// the stops of their own runs, on every scope that serves the table.
func TestIntegration_DriverSyncFence_StopsHeldToTheCallingDriver(t *testing.T) {
	w := newFenceWorld(t)

	for _, scope := range []string{"stops", "loads", "delivery_stops"} {
		idsA := fencePull(t, w, scope, w.driverA)
		if !fenceHas(idsA, w.stopA) {
			t.Errorf("driver A's %s pull is missing their own stop %s: %v", scope, w.stopA, idsA)
		}
		if fenceHas(idsA, w.stopB) {
			t.Errorf("driver A's %s pull sees driver B's stop %s: %v", scope, w.stopB, idsA)
		}
		idsB := fencePull(t, w, scope, w.driverB)
		if fenceHas(idsB, w.stopA) {
			t.Errorf("driver B's %s pull sees driver A's stop %s: %v", scope, w.stopA, idsB)
		}
		desk := fencePull(t, w, scope, w.desk)
		if !fenceHas(desk, w.stopA) || !fenceHas(desk, w.stopB) {
			t.Errorf("the desk caller's %s pull must keep both stops: %v", scope, desk)
		}
	}
}

// TestIntegration_DriverSyncFence_PODHeldToTheCallingDriver: the pod
// scope answers a driver only the proof records of their own runs'
// stops. Driver B's stop carries a photo; driver A's pull must not see
// it, and the desk caller's must.
func TestIntegration_DriverSyncFence_PODHeldToTheCallingDriver(t *testing.T) {
	w := newFenceWorld(t)

	stopIDs := func(f sync.ScopeFilter) []string {
		t.Helper()
		info, _ := sync.Lookup("pod")
		resp, err := w.engine.Pull(context.Background(), info, f, 0, 200)
		if err != nil {
			t.Fatalf("pull pod: %v", err)
		}
		out := make([]string, 0, len(resp.Items))
		for _, e := range resp.Items {
			out = append(out, fmt.Sprint(e.Data["stop_id"]))
		}
		return out
	}

	if seen := stopIDs(w.driverA); fenceHas(seen, w.stopB) {
		t.Errorf("driver A's pod pull answered a photo of driver B's stop %s: %v", w.stopB, seen)
	}
	if seen := stopIDs(w.desk); !fenceHas(seen, w.stopB) {
		t.Errorf("the desk caller's pod pull lost driver B's photo: %v", seen)
	}
}

// TestIntegration_DriverSyncFence_RowReadsAndWritesHoldToo pins the
// row-addressed half of the fence on all three scopes: a driver's Load,
// FindApplied and Write treat a colleague's row exactly as the engine
// treats a row outside the scope, the same as absent, so the id itself
// is not an existence oracle and the push cannot move another driver's
// day.
func TestIntegration_DriverSyncFence_RowReadsAndWritesHoldToo(t *testing.T) {
	w := newFenceWorld(t)
	ctx := context.Background()

	if _, found, err := w.runs.Load(ctx, w.driverA, w.runB.ID); err != nil {
		t.Fatalf("driver A loads driver B's run: %v", err)
	} else if found {
		t.Errorf("driver A's runs Load found driver B's run %s", w.runB.ID)
	}
	if _, found, err := w.stops.Load(ctx, w.driverA, w.stopB); err != nil {
		t.Fatalf("driver A loads driver B's stop: %v", err)
	} else if found {
		t.Errorf("driver A's stops Load found driver B's stop %s", w.stopB)
	}
	if _, err := w.runs.Write(ctx, w.driverA, w.runB.ID, syncFreshKey(), map[string]any{
		"id": w.runB.ID, "notes": "moved by a colleague",
	}); err == nil {
		t.Errorf("driver A's runs Write on driver B's run %s applied; it must be the same as absent", w.runB.ID)
	}
	if _, err := w.stops.Write(ctx, w.driverA, w.stopB, syncFreshKey(), map[string]any{
		"id": w.stopB, "status": StopFailed, "failure_reason": "moved by a colleague",
	}); err == nil {
		t.Errorf("driver A's stops Write on driver B's stop %s applied; it must be the same as absent", w.stopB)
	}

	if _, found, err := w.runs.Load(ctx, w.desk, w.runB.ID); err != nil || !found {
		t.Errorf("the desk caller's runs Load must keep finding the run: found=%v err=%v", found, err)
	}
}

// TestIntegration_DriverSyncFence_TheRoleDecidesWhoIsHeld pins the role
// seam's two edges. An unlinked driver login (the role without the
// record) reads NOTHING through every scope, never the org feed: the
// fence's job is to make the misconfigured state safe, and MyRuns
// already tells that person "you are not set up as a driver". A linked
// login whose role is not the driver role (the desk caller who also
// holds a driver record) keeps the org scope: managers unchanged, the
// same role-first rule the REST surface's ownership check applies.
func TestIntegration_DriverSyncFence_TheRoleDecidesWhoIsHeld(t *testing.T) {
	w := newFenceWorld(t)

	for _, scope := range []string{"runs", "stops", "pod", "loads", "delivery_stops"} {
		if seen := fencePull(t, w, scope, w.bare); len(seen) != 0 {
			t.Errorf("an unlinked driver login's %s pull answered %d rows; nothing is theirs until a driver record links to the login: %v", scope, len(seen), seen)
		}
	}

	seen := fencePull(t, w, "runs", w.linked)
	if !fenceHas(seen, w.runA.ID) || !fenceHas(seen, w.runB.ID) {
		t.Errorf("a linked desk caller's runs pull must keep the org list (managers unchanged): %v", seen)
	}
	seenStops := fencePull(t, w, "stops", w.linked)
	if !fenceHas(seenStops, w.stopA) || !fenceHas(seenStops, w.stopB) {
		t.Errorf("a linked desk caller's stops pull must keep the org list: %v", seenStops)
	}
}

// TestIntegration_DriverSyncFence_TheStopSeamAnswersForAttachments pins
// the seam the attachments scope fences through, on dispatch's own
// tables: the calling driver's candidate stops come back marked, a
// colleague's do not, and a desk caller is not held to the fence at
// all. The attachments module's own test drives its half with a double;
// this leg proves the dispatch-backed adapter the composition root
// wires behind it.
func TestIntegration_DriverSyncFence_TheStopSeamAnswersForAttachments(t *testing.T) {
	w := newFenceWorld(t)
	ctx := context.Background()
	seam := NewDriverStopFence(w.pool, linkedSyncUser(w.pool), w.role)

	got, isDriver, err := seam.StopsOnDriverRuns(ctx, testkit.OrgNorthID, testkit.SubDriver, []string{w.stopA, w.stopB})
	if err != nil {
		t.Fatalf("stop seam: %v", err)
	}
	if !isDriver {
		t.Fatalf("the seeded driver login must be held to the fence")
	}
	if !got[w.stopA] {
		t.Errorf("the driver's own stop %s is missing from the seam's answer: %v", w.stopA, got)
	}
	if got[w.stopB] {
		t.Errorf("the colleague's stop %s is in the seam's answer: %v", w.stopB, got)
	}

	if _, isDriver, err := seam.StopsOnDriverRuns(ctx, testkit.OrgNorthID, testkit.SubInsideSales, []string{w.stopA}); err != nil || isDriver {
		t.Errorf("a desk caller must not be held to the fence: isDriver=%v err=%v", isDriver, err)
	}
}
