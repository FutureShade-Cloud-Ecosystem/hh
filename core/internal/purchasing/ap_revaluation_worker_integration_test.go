//go:build integration

package purchasing

import (
	"context"
	"testing"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// fx2bSystemCtx is the worker's identity: a GLOBAL system caller, as the jobs runner stamps it.
func fx2bSystemCtx() context.Context {
	return identity.ContextWithStaff(context.Background(), identity.StaffContext{
		UserID: "system:jobs", Scope: identity.ScopeGlobal, Status: identity.StatusActive,
	})
}

// W3 FX-2b: the worker's reversal. After the month's last house day the worker reverses the
// previous run on the next house day if it is not reversed; it never presses a run, because the
// rate is a Controller's input. The house midnight is the boundary (the evening guard reruns this
// leg with the clock pinned inside the dealer's last evening).
func TestIntegration_W3FX2B_WorkerReversesOnTheNextHouseDay(t *testing.T) {
	rig := fx2Rig(t)
	fx2World(t, rig)
	rig.svc.withCaller(identity.StaffContextFromContext)
	loc := fx2Loc()
	t.Cleanup(func() { rig.svc.WithClock(time.Now) })

	// A Controller presses September at the house midnight after it.
	pressCtx := identity.ContextWithStaff(rig.ctx, identity.StaffContext{UserID: rig.user, RoleCode: "FINANCIAL_CONTROLLER", Scope: identity.ScopeGlobal, Status: identity.StatusActive})
	rig.svc.WithClock(func() time.Time { return time.Date(2026, 10, 1, 0, 0, 0, 0, loc) })
	if _, err := rig.svc.RevalueAP(pressCtx, RevalueAPRequest{BranchID: branchTRT, Month: fx2Month, FXRate: fx2RunRate}); err != nil {
		t.Fatalf("the Controller's press: %v", err)
	}
	// A second branch has open USD bills and no run: the worker must never press one.
	fx2PostUSDBillInBranch(t, rig, "w3fx2b-norun", branchKGN, "2026-09-15", "2026-10-15", 100_000, fx2BillARate, fx2Instant(15, 9, 0))

	// Inside the dealer's last evening (already October in UTC) the next house day has not begun:
	// nothing is reversed.
	rig.svc.WithClock(func() time.Time { return time.Date(2026, 9, 30, 23, 59, 59, 999_000_000, loc) })
	sweep, err := rig.svc.ReverseDueAPRevaluations(fx2bSystemCtx())
	if err != nil || sweep.Reversed != 0 || sweep.NotYetDue != 1 {
		t.Fatalf("W3-FX2b red: the sweep before the house midnight = %+v, %v, want nothing reversed and 1 not yet due", sweep, err)
	}
	if row := fx2bRun(t, rig, branchTRT, fx2Month); row.HasReversed || row.ReversalID != "" {
		t.Fatalf("W3-FX2b red: the early sweep reversed the run: %+v", row)
	}

	// At the house midnight the worker takes the entry back, as the system.
	rig.svc.WithClock(func() time.Time { return time.Date(2026, 10, 1, 0, 0, 0, 0, loc) })
	sweep, err = rig.svc.ReverseDueAPRevaluations(fx2bSystemCtx())
	if err != nil || sweep.Reversed != 1 || sweep.Failed != 0 {
		t.Fatalf("W3-FX2b red: the sweep at the house midnight = %+v, %v, want 1 reversed", sweep, err)
	}
	row := fx2bRun(t, rig, branchTRT, fx2Month)
	if !row.HasReversed || row.ReversalID == "" || row.ReversedBy != "system:jobs" {
		t.Fatalf("W3-FX2b red: the run record after the sweep = %+v, want it reversed by system:jobs", row)
	}
	legs := fx2JournalLegs(t, rig, "purchasing.ap.revaluation:"+branchTRT+":"+fx2Month+":reverse")
	if legs["2100"] != ([2]int64{fx2RunDiff, 0}) || legs["6315"] != ([2]int64{0, fx2RunDiff}) {
		t.Fatalf("W3-FX2b red: the worker's reversal legs = %v, want 2100 DR %d and 6315 CR %d", legs, fx2RunDiff, fx2RunDiff)
	}
	var postedAtUTC string
	if err := rig.pool.QueryRow(rig.ctx, `SELECT to_char(posted_at AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS') FROM gl_journals WHERE id = $1::uuid`, row.ReversalID).Scan(&postedAtUTC); err != nil {
		t.Fatalf("read the reversal journal: %v", err)
	}
	if postedAtUTC != "2026-10-01 04:00:00" {
		t.Fatalf("W3-FX2b red: the worker's reversal posted_at (UTC) = %q, want the next house day's first instant 2026-10-01 04:00:00", postedAtUTC)
	}

	// The sweep is idempotent, and it never pressed a run for the branch that has none.
	sweep, err = rig.svc.ReverseDueAPRevaluations(fx2bSystemCtx())
	if err != nil || sweep.Reversed != 0 || sweep.Examined != 0 {
		t.Fatalf("W3-FX2b red: a second sweep = %+v, %v, want nothing left to reverse", sweep, err)
	}
	if row := fx2bRun(t, rig, branchKGN, fx2Month); row.Found {
		t.Fatalf("W3-FX2b red: the worker pressed a run on its own: %+v", row)
	}
	if n := fx2JournalCount(t, rig, "purchasing.ap.revaluation:"+branchKGN+":"+fx2Month); n != 0 {
		t.Fatalf("W3-FX2b red: the worker posted %d journals for a branch no Controller ran", n)
	}
}

// The reversal job is scheduled by name: the migration seeds the row the worker registers.
func TestIntegration_W3FX2B_ReversalJobIsScheduled(t *testing.T) {
	rig := newAPAgingRig(t)
	var kind string
	var enabled bool
	if err := rig.pool.QueryRow(rig.ctx, `SELECT kind, enabled FROM job_schedules WHERE name = $1`, APRevaluationReversalJobName).Scan(&kind, &enabled); err != nil {
		t.Fatalf("W3-FX2b red: no schedule row named %q: %v", APRevaluationReversalJobName, err)
	}
	if kind != "NIGHTLY" || !enabled {
		t.Fatalf("W3-FX2b red: the schedule is %s enabled=%v, want a nightly job that is on", kind, enabled)
	}
}
