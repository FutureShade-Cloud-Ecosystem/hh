//go:build integration

// driver_completes_run_integration_test.go, a driver closing their own day, against real rows.
//
//	DATABASE_URL=... go test -tags integration -count=1 -run DriverCompletesRun ./internal/dispatch/
//
// WHY THIS EXISTS ALONGSIDE THE UNIT TABLE. completionAuthority is pure and its table says which
// caller may close which run. What a table cannot say is that the DRIVER RECORD the rule compares
// against is the one DriverForUser actually resolves from a staff login, a join through
// dispatch_drivers.user_id that no fake exercises, or that the branch fence, the run matrix and the
// ownership check compose in an order where each one still fires. Three guards that are each correct
// and that run in the wrong order is a real failure mode, and it only shows up against a database.
//
// THE AUDIT ROW IS WRITTEN THROUGH THE REAL audit.Service, not a recorder. The interesting risk is not
// that dispatch calls Record, that is one line, it is that the row SURVIVES: `actor_role` is on a
// deny-by-default allowlist, and a key that is not on it is dropped silently before persistence,
// leaving a trail row that says a run was completed by nobody in particular. A fake recorder would
// prove nothing about that, which is the same argument this suite's header already makes about the
// print queue.
//
// FIXTURES RESTORE WHAT THEY TOUCH, and the ORDER MATTERS here in a way it does not elsewhere:
// delivery_runs.driver_id is ON DELETE RESTRICT, so the driver rows are registered for cleanup BEFORE
// the runs that crew them. t.Cleanup is LIFO, so the run goes first and the driver deletes cleanly.

package dispatch

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5/pgxpool"
)

// unlinkedUser finds a staff login that is NOT already a driver, so seedLinkedDriver's join resolves
// to the row this test made and not to one the demo seed happened to link first.
func unlinkedUser(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var id string
	err := pool.QueryRow(context.Background(), `
SELECT u.id::text FROM users u
 WHERE NOT EXISTS (SELECT 1 FROM dispatch_drivers d WHERE d.user_id = u.id)
 ORDER BY u.created_at LIMIT 1`).Scan(&id)
	if err != nil {
		t.Skipf("no unlinked seeded user to make a driver of: %v", err)
	}
	return id
}

// seedLinkedDriver creates a driver row wired to a staff login, the join MyRuns and the completion
// ownership check both walk. It returns the driver id.
func seedLinkedDriver(t *testing.T, pool *pgxpool.Pool, branchID, userID string) string {
	t.Helper()
	ctx := context.Background()
	var id string
	err := pool.QueryRow(ctx, `
INSERT INTO dispatch_drivers (branch_id, user_id, name, phone, licence_class, is_active)
VALUES ($1, $2, $3, '', 'AZ', TRUE) RETURNING id::text`,
		branchID, userID, "Completion Test "+uniqueSuffix()).Scan(&id)
	if err != nil {
		t.Fatalf("seed linked driver: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM dispatch_drivers WHERE id = $1`, id)
	})
	return id
}

// driverCtx stamps the caller identity.RequireStaff would stamp for a driver: the DRIVERS role,
// frontline tier, BRANCH scope, and a grant on the yard they work out of. Note that this caller
// PASSES guardRun's branch fence for every run at that branch, which is exactly why the ownership
// check has to exist somewhere else.
func driverCtx(userID, branchID string) context.Context {
	return identity.ContextWithStaff(context.Background(), identity.StaffContext{
		UserID: userID, RoleCode: identity.RoleDrivers, Tier: identity.TierFrontline,
		Scope: identity.ScopeBranch, Status: identity.StatusActive,
		Branches: map[string]bool{branchID: true},
	})
}

// seedRunForDriver crews a run on ONE named driver rather than on whichever driver sorts first.
func seedRunForDriver(t *testing.T, h *harness, ctx context.Context, branchID, driverID string) Run {
	t.Helper()
	run, err := h.svc.CreateRun(ctx, CreateRunInput{
		BranchID: branchID, ScheduledDate: today(),
		TruckID: firstTruck(t, h.pool, branchID), DriverID: driverID,
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = h.pool.Exec(c, `UPDATE delivery_stops SET run_id = NULL WHERE run_id = $1`, run.ID)
		_, _ = h.pool.Exec(c, `DELETE FROM print_queue WHERE ref_id = $1`, run.ID)
		_, _ = h.pool.Exec(c, `DELETE FROM delivery_runs WHERE id = $1`, run.ID)
	})
	return run
}

// TestDriverCompletesRun_OwnRunOnly walks the whole ruling in one day: the refusal that NAMES the
// unresolved stop, the colleague who is turned away, the owner who closes it, and the trail row that
// says which kind of person did.
func TestDriverCompletesRun_OwnRunOnly(t *testing.T) {
	h := newHarness(t)
	dispatcher := globalCtx(t, h.pool)

	// The real audit service, so the deny-by-default allowlist is exercised rather than mocked away.
	auditSvc := audit.NewService(audit.NewRepository(h.pool), nil)
	h.svc.WithAudit(auditSvc)

	priyaUser := unlinkedUser(t, h.pool)
	priya := seedLinkedDriver(t, h.pool, branchTRT, priyaUser)

	orderID := seedOrder(t, h.pool, branchTRT, "DELIVERY", "CONFIRMED")
	stop, err := h.svc.Schedule(dispatcher, ScheduleInput{
		OrderID: orderID, ScheduledDate: today(), AddressLine: "41 Gravel Pit Lane",
		City: "Trenton", PostalCode: "K8V 5R5", ContactName: "Site foreman",
	})
	if err != nil {
		t.Fatalf("schedule: %v", err)
	}
	run := seedRunForDriver(t, h, dispatcher, branchTRT, priya)
	if _, err := h.svc.AssignStop(dispatcher, stop.ID, AssignInput{RunID: run.ID}); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if _, err := h.svc.DispatchRun(dispatcher, run.ID); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	priyaCtx := driverCtx(priyaUser, branchTRT)

	// - 1 · REFUSED WHILE A STOP IS UNRESOLVED, AND THE REFUSAL NAMES IT -
	//
	// The truck has left, so the stop is LOADED. A driver who taps Complete here must be told WHICH
	// stop is outstanding, "2 stop(s) are not finished yet" is a thing they have to go and reconcile
	// against a list, on a phone, standing beside a truck.
	_, err = h.svc.CompleteRun(priyaCtx, run.ID)
	if err == nil {
		t.Fatal("a run whose stop is still on the truck must not close")
	}
	msg := err.Error()
	if !strings.Contains(msg, "stop 1") || !strings.Contains(msg, "on the truck") {
		t.Fatalf("the refusal must name the unresolved stop and say why: %q", msg)
	}
	if status, _, ok := ErrorHTTPStatus(err); !ok || status != 409 {
		t.Fatalf("an unresolved stop is a 409 conflict, got %d (mapped=%v)", status, ok)
	}

	// - 2 · THE STOP IS RESOLVED THE WAY A DRIVER RESOLVES ONE -
	if _, err := h.svc.AdvanceStop(priyaCtx, stop.ID, StopEnRoute, ""); err != nil {
		t.Fatalf("en route: %v", err)
	}
	if _, err := h.svc.CapturePOD(priyaCtx, stop.ID, PODInput{ReceivedBy: "Jo Ramirez", Affirmed: true}); err != nil {
		t.Fatalf("pod: %v", err)
	}

	// - 3 · A COLLEAGUE IS REFUSED, AND THE BRANCH FENCE IS NOT WHAT REFUSES THEM -
	//
	// Sam is a real driver, at the SAME branch, with a real driver record and a legitimate grant.
	// guardRun says yes to this request. Every gate that existed before this ruling says yes to this
	// request. Only the ownership check says no.
	samUser := unlinkedUser(t, h.pool)
	if samUser == priyaUser {
		t.Skip("only one unlinked user in this database, cannot stage a second driver")
	}
	_ = seedLinkedDriver(t, h.pool, branchTRT, samUser)
	if _, err := h.svc.CompleteRun(driverCtx(samUser, branchTRT), run.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("a driver must not close a colleague's run; got %v", err)
	}
	// And the run is untouched by the attempt, a refused write writes nothing.
	if after, err := h.svc.RunDetail(dispatcher, run.ID); err != nil || after.Status == RunCompleted {
		t.Fatalf("a refused completion must not have closed the run: %+v %v", after.Status, err)
	}

	// - 4 · THE OWNER CLOSES IT -
	closed, err := h.svc.CompleteRun(priyaCtx, run.ID)
	if err != nil {
		t.Fatalf("a driver must be able to close their own finished run: %v", err)
	}
	if closed.Status != RunCompleted {
		t.Fatalf("run should be COMPLETED, got %s", closed.Status)
	}

	// - 5 · THE TRAIL SAYS WHAT KIND OF PERSON DID IT -
	//
	// "A driver closed their own run" and "a dispatcher closed it for them" are different mornings,
	// and after today both are possible. actor_role is the fact that tells them apart, and it has to
	// survive the allowlist to be worth writing.
	entries, err := auditSvc.ReopenTransaction(context.Background(), "delivery_run", run.ID)
	if err != nil {
		t.Fatalf("read the trail: %v", err)
	}
	var found *audit.Entry
	for i := range entries {
		if entries[i].Action == "DELIVERY_RUN_COMPLETED" {
			found = &entries[i]
		}
	}
	if found == nil {
		t.Fatalf("closing a run must be audited; trail = %+v", entries)
	}
	if found.ActorRole != identity.RoleDrivers {
		t.Fatalf("the trail must record the ROLE that closed the run, got %q", found.ActorRole)
	}
	if found.ActorID != priyaUser {
		t.Fatalf("the trail must attribute the close to the caller, got %q", found.ActorID)
	}
	if got, _ := found.Changes["status"].(string); got != RunCompleted {
		t.Fatalf("the trail must say what the run became, got %v", found.Changes)
	}
	t.Cleanup(func() {
		_, _ = h.pool.Exec(context.Background(),
			`DELETE FROM audit_log WHERE entity_type = 'delivery_run' AND entity_id = $1`, run.ID)
	})
}

// TestDriverCompletesRun_ADriverWithNoDriverRecordIsNotADriver. A staff login can carry the DRIVERS
// role without ever having been set up as one, the role is granted in the admin screen, the record is
// created in the fleet registry, and nothing forces them to happen together. MyRuns already answers
// that person "you are not set up as a driver"; the completion path must not answer them "sure".
func TestDriverCompletesRun_ADriverWithNoDriverRecordIsNotADriver(t *testing.T) {
	h := newHarness(t)
	dispatcher := globalCtx(t, h.pool)

	// ORDER MATTERS: the driver is linked FIRST, so the next unlinked user really is somebody else.
	priya := seedLinkedDriver(t, h.pool, branchTRT, unlinkedUser(t, h.pool))
	orphan := unlinkedUser(t, h.pool)
	run := seedRunForDriver(t, h, dispatcher, branchTRT, priya)

	// The run here is still PLANNED, which makes this assertion do double duty: authority is checked
	// BEFORE the lifecycle matrix, so an unauthorised caller learns nothing about the run's state. If
	// the two were swapped this would come back ErrIllegalTransition, a 409 telling a stranger the
	// run exists and where it is in its life.
	if _, err := h.svc.CompleteRun(driverCtx(orphan, branchTRT), run.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("a DRIVERS login with no driver record owns no run; got %v", err)
	}
}

// TestDriverCompletesRun_EveryOtherRoleIsUnchanged. Half of this ruling is what it does NOT do. A
// dispatcher who could close a run yesterday closes it today, including a run crewed on a driver who
// is not them, which is the ordinary case and the one an ownership check written slightly too
// enthusiastically would break.
func TestDriverCompletesRun_EveryOtherRoleIsUnchanged(t *testing.T) {
	h := newHarness(t)
	dispatcher := globalCtx(t, h.pool)

	priya := seedLinkedDriver(t, h.pool, branchTRT, unlinkedUser(t, h.pool))
	orderID := seedOrder(t, h.pool, branchTRT, "DELIVERY", "CONFIRMED")
	stop, err := h.svc.Schedule(dispatcher, ScheduleInput{OrderID: orderID, ScheduledDate: today()})
	if err != nil {
		t.Fatalf("schedule: %v", err)
	}
	run := seedRunForDriver(t, h, dispatcher, branchTRT, priya)
	if _, err := h.svc.AssignStop(dispatcher, stop.ID, AssignInput{RunID: run.ID}); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if _, err := h.svc.DispatchRun(dispatcher, run.ID); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	// The precondition binds a dispatcher exactly as it binds a driver, it is a fact about the run,
	// not a restriction on frontline staff.
	if _, err := h.svc.CompleteRun(dispatcher, run.ID); err == nil {
		t.Fatal("a dispatcher cannot close a run with a stop still on the truck either")
	}
	if _, err := h.svc.AdvanceStop(dispatcher, stop.ID, StopEnRoute, ""); err != nil {
		t.Fatalf("en route: %v", err)
	}
	if _, err := h.svc.AdvanceStop(dispatcher, stop.ID, StopFailed, "gate locked"); err != nil {
		t.Fatalf("failed: %v", err)
	}
	closed, err := h.svc.CompleteRun(dispatcher, run.ID)
	if err != nil {
		t.Fatalf("a dispatcher must still close somebody else's run: %v", err)
	}
	if closed.Status != RunCompleted {
		t.Fatalf("run should be COMPLETED, got %s", closed.Status)
	}
	t.Cleanup(func() {
		_, _ = h.pool.Exec(context.Background(),
			`DELETE FROM audit_log WHERE entity_type = 'delivery_run' AND entity_id = $1`, run.ID)
	})
}

// TestDriverCompletesRun_ARunOfNothingButCancelledStopsIsRefused, the loophole wave 9's own verifier
// measured, walked end to end against real rows.
//
// WHAT WAS MEASURED, BEFORE: the driver cancels stop 1 (200), cancels stop 2 (200), taps Complete, and
// gets 200 with the run COMPLETED. A whole day filed with nothing delivered, nothing failed and NO
// REASON RECORDED ANYWHERE. The three-status precondition let it through because it counted CANCELLED
// as an answer, on the belief that only dispatch can cancel, and the driver's own run sheet draws a
// confirming "Cancel the stop" control.
//
// WHY THIS CANNOT BE A UNIT TEST ALONE. The predicate table proves the SET is right. What only a
// database proves is that the narrow gate is actually WIRED, that CompleteRun asks it after the
// count rather than instead of it, that a cancel really does leave OpenStopCount at zero (so the
// cheap gate genuinely cannot see this day), and that the run is still standing afterwards. A gate
// that is correct and unreached is the failure mode this file exists for.
func TestDriverCompletesRun_ARunOfNothingButCancelledStopsIsRefused(t *testing.T) {
	h := newHarness(t)
	dispatcher := globalCtx(t, h.pool)

	priyaUser := unlinkedUser(t, h.pool)
	priya := seedLinkedDriver(t, h.pool, branchTRT, priyaUser)
	run := seedRunForDriver(t, h, dispatcher, branchTRT, priya)

	var stopIDs []string
	for _, addr := range []string{"41 Gravel Pit Lane", "9 Quarry Road"} {
		orderID := seedOrder(t, h.pool, branchTRT, "DELIVERY", "CONFIRMED")
		stop, err := h.svc.Schedule(dispatcher, ScheduleInput{
			OrderID: orderID, ScheduledDate: today(), AddressLine: addr,
			City: "Trenton", PostalCode: "K8V 5R5", ContactName: "Site foreman",
		})
		if err != nil {
			t.Fatalf("schedule %s: %v", addr, err)
		}
		if _, err := h.svc.AssignStop(dispatcher, stop.ID, AssignInput{RunID: run.ID}); err != nil {
			t.Fatalf("assign %s: %v", addr, err)
		}
		stopIDs = append(stopIDs, stop.ID)
	}
	if _, err := h.svc.DispatchRun(dispatcher, run.ID); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	priyaCtx := driverCtx(priyaUser, branchTRT)

	// - 1 · THE DRIVER CANCELS EVERY STOP, AND EVERY CANCEL IS ACCEPTED -
	//
	// The cancel is NOT being closed here. It is a considered capability with a confirming sheet built
	// for it after a gloved mis-tap in a cab cancelled a customer's delivery, and deleting it to close
	// this hole would trade one defect for a worse one. What changes is what a cancel ENTITLES.
	for i, id := range stopIDs {
		if _, err := h.svc.AdvanceStop(priyaCtx, id, StopCancelled, ""); err != nil {
			t.Fatalf("a driver may still cancel their own stop %d: %v", i+1, err)
		}
	}

	// - 2 · THE CHEAP GATE SEES NOTHING, WHICH IS THE POINT -
	//
	// OpenStopCount's SQL is `status NOT IN ('DELIVERED','FAILED','CANCELLED')`, so this day reads as
	// zero open stops. Any fix that lived only in the count would have had to change what an open stop
	// means for the load board and the day-ahead tiles too.
	// The predicate is written out here rather than called through the repository, so this assertion is
	// about the SQL the shared count actually runs and not about a Go wrapper agreeing with itself.
	var open int
	if err := h.pool.QueryRow(context.Background(),
		`SELECT count(*)::int FROM delivery_stops
          WHERE run_id = $1 AND status NOT IN ('DELIVERED','FAILED','CANCELLED')`, run.ID).Scan(&open); err != nil {
		t.Fatalf("open stop count: %v", err)
	}
	if open != 0 {
		t.Fatalf("a cancelled day is zero open stops to the shared count; got %d", open)
	}

	// - 3 · AND THE DRIVER IS REFUSED, IN WORDS THAT NAME THE STOP AND SAY WHO TO ASK -
	_, err := h.svc.CompleteRun(priyaCtx, run.ID)
	if err == nil {
		t.Fatal("a driver must not file a day with nothing delivered, nothing failed and no reason recorded")
	}
	msg := err.Error()
	for _, want := range []string{"stop 1", "cancelled", "not an outcome a driver records", "ask dispatch to close this run"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("the refusal must contain %q: %q", want, msg)
		}
	}
	// The sentence must NOT tell a driver to do the impossible: CANCELLED is terminal in the matrix, so
	// "every stop must be delivered or marked not delivered" would be an instruction with no tap
	// behind it.
	if strings.Contains(msg, "delivered or marked not delivered") {
		t.Fatalf("a cancelled stop cannot be delivered or failed, this clause asks for the impossible: %q", msg)
	}
	if status, _, ok := ErrorHTTPStatus(err); !ok || status != 409 {
		t.Fatalf("a run that cannot be closed yet is a 409 conflict, got %d (mapped=%v)", status, ok)
	}
	after, err := h.svc.RunDetail(dispatcher, run.ID)
	if err != nil || after.Status == RunCompleted {
		t.Fatalf("a refused completion must not have closed the run: %+v %v", after.Status, err)
	}

	// - 4 · THE CONSEQUENCE, STATED RATHER THAN HIDDEN: DISPATCH CLOSES IT -
	//
	// This is the half of the narrowing that makes it a narrowing and not a deadlock. The day is not
	// stuck; it needs a dispatcher's eye before it is filed, which is exactly what a cancel, the one
	// outcome nobody wrote a reason for, deserves. Every other role's reach is byte-for-byte what it
	// was before this fix.
	closed, err := h.svc.CompleteRun(dispatcher, run.ID)
	if err != nil {
		t.Fatalf("dispatch must still be able to close a run carrying cancelled stops: %v", err)
	}
	if closed.Status != RunCompleted {
		t.Fatalf("run should be COMPLETED, got %s", closed.Status)
	}
	t.Cleanup(func() {
		_, _ = h.pool.Exec(context.Background(),
			`DELETE FROM audit_log WHERE entity_type = 'delivery_run' AND entity_id = $1`, run.ID)
	})
}
