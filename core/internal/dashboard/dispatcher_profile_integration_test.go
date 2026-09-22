//go:build integration

package dashboard

import (
	"context"
	"testing"
)

// dispatcher_profile_integration_test.go, 0203, ruled 1 Sep 2026: the dispatcher gets the dock Home.
//
// WHY THIS IS AN INTEGRATION TEST AND NOT A UNIT ONE. The thing being pinned is a ROW, not a
// function. `dashboard_role_profiles` is the one role→shape truth program-wide, and the failure this
// guards is somebody re-filing that row, in a later migration, or by hand on a deployment, and
// silently handing a dispatcher the controller's Home, the controller's tiles and the controller's
// pinned tabs. No fake can have that failure.

func TestIntegration_0203_TheDispatcherFilesUnderTheDock(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	var profile string
	if err := pool.QueryRow(ctx,
		`SELECT profile FROM dashboard_role_profiles WHERE role_code = 'LOGISTICS_MANAGER'`).
		Scan(&profile); err != nil {
		t.Fatalf("LOGISTICS_MANAGER has no profile row at all: %v", err)
	}
	if profile != "yard" {
		t.Fatalf("LOGISTICS_MANAGER files under %q; 0203 ruled it `yard`, the dock. Filed under "+
			"`manager` a dispatcher opens on the controller's Home (an AR-aging hero), carries the "+
			"controller's pinned tabs (the till and the statements) and loses the pick board.", profile)
	}

	// The control, and it is the whole point of the ruling: the controller must NOT have moved with
	// them. Before 0203 these two shared a shape, which is how the dispatcher ended up on the wrong
	// Home in the first place.
	if err := pool.QueryRow(ctx,
		`SELECT profile FROM dashboard_role_profiles WHERE role_code = 'FINANCIAL_CONTROLLER'`).
		Scan(&profile); err != nil {
		t.Fatalf("FINANCIAL_CONTROLLER has no profile row: %v", err)
	}
	if profile != "manager" {
		t.Fatalf("FINANCIAL_CONTROLLER files under %q, want `manager`, 0203 moved the dispatcher "+
			"OUT of the shape they shared, and must not have taken the controller with them", profile)
	}
}

// And the shape the dispatcher now resolves to must actually BE the dock, a role pointed at a
// profile with no widgets is a Home that renders nothing, which would pass the test above.
func TestIntegration_0203_TheDockHomeIsRealAndLeadsWithTheDeliveries(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	rows, err := pool.Query(ctx,
		`SELECT measure, position, size FROM dashboard_profile_widgets
		  WHERE profile = 'yard' ORDER BY position`)
	if err != nil {
		t.Fatalf("read the yard default: %v", err)
	}
	defer rows.Close()

	var measures []string
	for rows.Next() {
		var measure, size string
		var position int
		if err := rows.Scan(&measure, &position, &size); err != nil {
			t.Fatalf("scan: %v", err)
		}
		measures = append(measures, measure)
	}
	if len(measures) < 6 {
		t.Fatalf("the dock Home has %d widgets; the doctrine asks for 6-9 on a role landing", len(measures))
	}
	// One hero and one Needs-you, top-left, per the doctrine's second rule.
	if measures[0] != "deliveries.today" {
		t.Fatalf("the dock Home opens on %q; a dispatcher's first fact is the day's deliveries", measures[0])
	}
	if measures[1] != string(MeasureNeedsYou) {
		t.Fatalf("position 1 is %q, want the Needs-you queue, Home leads with what needs the person", measures[1])
	}
}

// 0204, ruled 1 Sep 2026: a takeover is right where there is no Home worth landing on, and wrong
// where there is. The `yard` shape's Home now opens on the pick queue and the Needs-you list, so
// skipping it cost a yard hand the one screen item 6 built for them. The `driver` shape has NO
// widget Home at all, so 0141's sentence about the run sheet is still true and its takeover stays.
//
// This test is the pair, and it has to be a pair: either half alone reads as an accident.
func TestIntegration_0204_TakeoverSurvivesOnlyWhereThereIsNoHome(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	type shape struct {
		takeover bool
		widgets  int
	}
	got := map[string]shape{}
	rows, err := pool.Query(ctx, `
		SELECT h.profile, h.takeover, COUNT(w.profile)
		  FROM dashboard_profile_home h
		  LEFT JOIN dashboard_profile_widgets w ON w.profile = h.profile
		 GROUP BY h.profile, h.takeover`)
	if err != nil {
		t.Fatalf("read the landings: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var p string
		var s shape
		if err := rows.Scan(&p, &s.takeover, &s.widgets); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got[p] = s
	}
	if len(got) < 5 {
		t.Fatalf("only %d job shapes have a landing row, the scan has drifted", len(got))
	}

	// THE RULE, applied to every shape rather than to the two this ruling named: a shape that HAS a
	// Home must not skip it, and a shape that skips must have nothing to skip to.
	for profile, s := range got {
		if s.takeover && s.widgets > 0 {
			t.Errorf("%s takes over its Home on a small screen AND has %d widgets waiting there, "+
				"including, for every seeded shape, a Needs-you queue nobody would ever see",
				profile, s.widgets)
		}
	}
	if y := got["yard"]; y.takeover {
		t.Errorf("the yard shape still takes over; 0204 retired it because its Home opens on the "+
			"pick queue (widgets: %d)", y.widgets)
	}
	if d := got["driver"]; !d.takeover {
		t.Error("the driver shape lost its takeover. A driver's surface IS the run sheet and they " +
			"have no widget Home to land on, 0141's argument is untouched by item 6")
	}
	if d := got["driver"]; d.widgets != 0 {
		t.Errorf("the driver shape has %d widgets, if a driver Home is ever seeded, this ruling "+
			"needs revisiting, because the takeover would then be skipping something real", d.widgets)
	}
}
