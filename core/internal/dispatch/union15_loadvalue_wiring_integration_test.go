//go:build integration

// union15_loadvalue_wiring_integration_test.go, UNION TRAIN 15, closing a PRE-EXISTING hole of
// exactly the class train 14 named.
//
//	DATABASE_URL=... go test -tags integration -count=1 -run Union15LoadValue ./internal/dispatch/
//
// WHAT WAS ALREADY PINNED, AND WHAT WAS NOT.
//
//   - the PREDICATE, TestLoadValueAllowed_ManagerUpIsTheDefaultAndFailsClosed calls
//     loadValueAllowed directly and proves it answers no to a driver;
//   - the STRUCT TAGS, train 14's TestLoadStopPayload_MoneyIsBYTE_ABSENT_NotNull and
//     TestLoadBoardMoneyFields_AllCarryOmitempty prove a nil pointer marshals to nothing;
//   - the WIRING BETWEEN THEM, nothing at all.
//
// Measured by the union-15 gate: replacing `showValue := s.loadValueAllowed(ctx, branchID)` in
// Service.LoadBoard with `showValue := true` left the ENTIRE suite green, unit AND integration, on
// this tree and on origin/qa, while putting value_cents and freight_cents on a driver's wire. The
// policy object is still right in that world and the tags are still right; only the one line that
// joins them is wrong, and it was the one line no test read. That is train 14 §2.4's finding one
// layer up, and it is closed here the same way: additively, with no source change.
//
// PRE-EXISTING, NOT A MERGE DEFECT. The same mutation is green at 56d07fc.
//
// FIXTURES RESTORE WHAT THEY TOUCH: seedOrder's cleanup removes the order and its stops.

package dispatch

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// union15DriverCtx stamps a FRONTLINE caller the way identity.RequireStaff does in production, the
// audience the gate exists for. Deliberately not a helper the rest of the suite shares: it is the
// one caller this file is about.
func union15DriverCtx(t *testing.T, h *harness) context.Context {
	t.Helper()
	return identity.ContextWithStaff(context.Background(), identity.StaffContext{
		UserID: seededUser(t, h.pool), RoleCode: "DRIVERS", Tier: identity.TierFrontline,
		Scope: identity.ScopeGlobal, Status: identity.StatusActive,
	})
}

func TestUnion15LoadValue_TheGateIsWIREDIntoTheBoardAndNotMerelyDefined(t *testing.T) {
	h := newHarness(t)
	mgrCtx := globalCtx(t, h.pool)
	date := today()

	// A stop carrying BOTH gated figures, so the assertions below cannot pass because the board is
	// empty. The digits are distinctive enough to grep for on their own.
	orderID := seedOrder(t, h.pool, branchTRT, "DELIVERY", "CONFIRMED")
	if _, err := h.pool.Exec(context.Background(), `
INSERT INTO delivery_stops (branch_id, order_id, scheduled_date, status, stop_no, value_cents, freight_cents)
VALUES ($1, $2, $3::date, 'READY', 1, 353125, 9500)`, branchTRT, orderID, date); err != nil {
		t.Fatalf("seed stop: %v", err)
	}

	// THE MANAGER'S BOARD, first, so a vacuous pass is impossible. If the figures are not here, the
	// driver's board carrying none of them proves nothing.
	mgrView, err := h.svc.LoadBoard(mgrCtx, branchTRT, date)
	if err != nil {
		t.Fatalf("manager load board: %v", err)
	}
	mgrJSON, err := json.Marshal(mgrView)
	if err != nil {
		t.Fatalf("marshal manager board: %v", err)
	}
	if !mgrView.ValueVisible {
		t.Fatal("the fixture is wrong: a logistics manager must be allowed the load value")
	}
	for _, want := range []string{"353125", "9500", "value_cents", "freight_cents"} {
		if !strings.Contains(string(mgrJSON), want) {
			t.Fatalf("the manager's board must carry %q for this test to mean anything; got %s", want, mgrJSON)
		}
	}

	// THE DRIVER'S BOARD. Byte-absence, on the WHOLE payload, not "the pointer is nil", not
	// "the UI hides it", and not "value_visible says false".
	drvView, err := h.svc.LoadBoard(union15DriverCtx(t, h), branchTRT, date)
	if err != nil {
		t.Fatalf("driver load board: %v", err)
	}
	drvJSON, err := json.Marshal(drvView)
	if err != nil {
		t.Fatalf("marshal driver board: %v", err)
	}
	if drvView.ValueVisible {
		t.Fatal("value_visible must be false for a driver, the posture field is the caller's, not the data's")
	}
	assertUnion15DriverBoardJSON(t, drvJSON)

	// And the gate removes the MONEY, not the WORK: the driver sees the same stops.
	if countStopsIn(drvJSON) != countStopsIn(mgrJSON) {
		t.Fatalf("the driver's board must carry the same stops as the manager's, the gate hides "+
			"figures, not loads. driver=%d manager=%d", countStopsIn(drvJSON), countStopsIn(mgrJSON))
	}
}

// countStopsIn counts stop ids in a marshalled board, so "same work" is a measured claim.
func countStopsIn(payload []byte) int {
	return strings.Count(string(payload), `"stop_id"`) + strings.Count(string(payload), `"id":"`)
}
