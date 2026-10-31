package purchasing

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// revalUnitSvc is a service with no database and a pinned clock, so the revaluation run's
// input refusals are proved in the order the run judges them: refused in words before any
// database is ever asked (a nil pool answers errNoDB, which would be the wrong reason).
func revalUnitSvc(now time.Time) *Service {
	return NewService(NewRepository(nil)).
		WithClock(func() time.Time { return now }).
		withCaller(func(context.Context) (identity.StaffContext, bool) {
			return identity.StaffContext{UserID: "u1", RoleCode: "GENERAL_MANAGER",
				Tier: identity.TierLeadership, Scope: identity.ScopeGlobal, Status: "ACTIVE"}, true
		})
}

func TestW3FX2_ParseAPMonth(t *testing.T) {
	for _, bad := range []string{"", "2026", "2026-9", "2026-0", "2026-13", "2026-09-01", "26-09", "2026/09"} {
		if _, _, err := parseAPMonth(bad); err == nil {
			t.Errorf("parseAPMonth(%q) accepted a malformed month", bad)
		}
	}
	// Surrounding space is trimmed before the shape is judged, so one month has one key.
	y, m, err := parseAPMonth(" 2026-09 ")
	if err != nil || y != 2026 || m != 9 {
		t.Fatalf("parseAPMonth( 2026-09 ) = %d, %d, %v, want the trimmed month", y, m, err)
	}
}

// TestW3FX2_MonthBoundsAreTheHouseZone proves the month end instant is the last
// nanosecond of the month's last house day in the dealer's own zone, never a Greenwich
// midnight: the whole last second of a September house day in Toronto is already the next
// UTC day, so a month end read at UTC would file the run's journal into the month before
// it. This is the leg the evening guard reruns with the clock pinned inside that window.
func TestW3FX2_MonthBoundsAreTheHouseZone(t *testing.T) {
	loc, err := time.LoadLocation("America/Toronto")
	if err != nil {
		t.Fatalf("load Toronto: %v", err)
	}
	start, end := apMonthBounds(2026, 9, loc)
	if got := start.Format("2006-01-02 15:04:05.999999999 MST"); got != "2026-09-01 00:00:00 EDT" {
		t.Fatalf("month start = %q, want the first house midnight of September", got)
	}
	if got := end.Format("2006-01-02 15:04:05.999999999 MST"); got != "2026-09-30 23:59:59.999999999 EDT" {
		t.Fatalf("month end = %q, want the last nanosecond of the last house day", got)
	}
	if got := end.In(time.UTC).Format("2006-01-02 15:04:05.999999999 UTC"); got != "2026-10-01 03:59:59.999999999 UTC" {
		t.Fatalf("month end in UTC = %q, want the next UTC day, the class a UTC month end would miss", got)
	}
	// December rolls into the next year without a bridge.
	_, decEnd := apMonthBounds(2027, 12, loc)
	if got := decEnd.In(loc).Format("2006-01-02"); got != "2027-12-31" {
		t.Fatalf("December end = %q, want the last house day of December", got)
	}
}

const revalTestBranch = "5d1b3f6e-0000-4000-9000-000000000001"

func TestW3FX2_RevalKey(t *testing.T) {
	if got := apRevalKey(revalTestBranch, "2026-09"); got != "purchasing.ap.revaluation:"+revalTestBranch+":2026-09" {
		t.Fatalf("apRevalKey = %q", got)
	}
}

// TestW3FX2_RunRefusesBadInputsInWords proves the run's own input refusals: the branch,
// the month and the rate's shape are judged in words before anything is read, and a month
// that has not begun yet is refused on the dealer's own clock, all with no database.
func TestW3FX2_RunRefusesBadInputsInWords(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	svc := revalUnitSvc(now)

	cases := []struct {
		name   string
		branch string
		month  string
		rate   float64
		status int
		wantIn string
	}{
		{"no branch", "", "2026-09", 1.4, http.StatusBadRequest, "branch_id is required"},
		{"malformed month", revalTestBranch, "2026-9", 1.4, http.StatusBadRequest, "YYYY-MM"},
		{"zero rate", revalTestBranch, "2026-09", 0, http.StatusBadRequest, "must be positive"},
		{"negative rate", revalTestBranch, "2026-09", -1.4, http.StatusBadRequest, "must be positive"},
		{"seven decimals", revalTestBranch, "2026-09", 1.4000001, http.StatusBadRequest, "7 decimals"},
		{"month not begun", revalTestBranch, "2026-11", 1.4, http.StatusUnprocessableEntity, "next house day"},
	}
	for _, tc := range cases {
		_, err := svc.RevalueAP(context.Background(), RevalueAPRequest{BranchID: tc.branch, Month: tc.month, FXRate: tc.rate})
		if err == nil {
			t.Fatalf("%s: RevalueAP accepted %q", tc.name, tc.month)
		}
		var ce clientError
		if !errors.As(err, &ce) || ce.status != tc.status {
			t.Fatalf("%s: error = %v, want status %d", tc.name, err, tc.status)
		}
		if !strings.Contains(err.Error(), tc.wantIn) {
			t.Fatalf("%s: error %q does not say %q", tc.name, err.Error(), tc.wantIn)
		}
	}
}

// TestW3FX2_RunWaitsForTheMonthToEnd proves the run takes the reversal's guard: a month is
// revalued only once the house day after its last day has begun, judged on the dealer's
// zone and not the UTC day. Mid month and the last house evening (already the next UTC day)
// are refused in words; the house midnight after the month passes the guard and reaches the
// database, which the nil pool answers with a 503.
func TestW3FX2_RunWaitsForTheMonthToEnd(t *testing.T) {
	loc, err := time.LoadLocation("America/Toronto")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		now  time.Time
	}{
		{"the 15th", time.Date(2026, 9, 15, 12, 0, 0, 0, loc)},
		{"23:30 on the last house day", time.Date(2026, 9, 30, 23, 30, 0, 0, loc)},
	} {
		svc := revalUnitSvc(tc.now)
		_, err := svc.RevalueAP(context.Background(), RevalueAPRequest{BranchID: revalTestBranch, Month: "2026-09", FXRate: 1.4})
		var ce clientError
		if !errors.As(err, &ce) || ce.status != http.StatusUnprocessableEntity {
			t.Fatalf("%s: error = %v, want a 422 refusal", tc.name, err)
		}
		if !strings.Contains(err.Error(), "next house day") || !strings.Contains(err.Error(), "2026-10-01") {
			t.Fatalf("%s: refusal %q does not name the next house day 2026-10-01", tc.name, err.Error())
		}
	}
	svc := revalUnitSvc(time.Date(2026, 10, 1, 0, 0, 0, 0, loc))
	_, err = svc.RevalueAP(context.Background(), RevalueAPRequest{BranchID: revalTestBranch, Month: "2026-09", FXRate: 1.4})
	var ce clientError
	if !errors.As(err, &ce) || ce.status != http.StatusServiceUnavailable {
		t.Fatalf("at the house midnight: error = %v, want the database refusal (the guard passed), status 503", err)
	}
}

func TestW3FX2_RevaluationLegs(t *testing.T) {
	up := revaluationLegs(10_000, "2026-09")
	if len(up) != 2 || up[0].AccountCode != glAccountAPCode || up[0].CreditCents != 10_000 || up[0].DebitCents != 0 {
		t.Fatalf("liability up legs = %+v, want 2100 CR 10000 first", up)
	}
	if up[1].AccountCode != glAccountFXGainLoss || up[1].DebitCents != 10_000 || up[1].CreditCents != 0 {
		t.Fatalf("liability up FX leg = %+v, want the FX account DR 10000", up)
	}
	down := revaluationLegs(-4_000, "2026-09")
	if down[0].DebitCents != 4_000 || down[0].CreditCents != 0 {
		t.Fatalf("liability down legs = %+v, want 2100 DR 4000 first", down)
	}
	if down[1].CreditCents != 4_000 || down[1].DebitCents != 0 {
		t.Fatalf("liability down FX leg = %+v, want the FX account CR 4000", down)
	}
	if got := postedLegsDifference(up); got != 10_000 {
		t.Fatalf("postedLegsDifference(up) = %d, want 10000", got)
	}
	if got := postedLegsDifference(down); got != -4_000 {
		t.Fatalf("postedLegsDifference(down) = %d, want -4000", got)
	}
}

// TestW3FX2_ZeroRunMarker proves a zero difference run is recorded by a marker that moves
// nothing: one cent each way on 2100, read back as a zero difference, never mistaken for a
// real run's legs.
func TestW3FX2_ZeroRunMarker(t *testing.T) {
	legs := revaluationLegs(0, "2026-09")
	if !isRevaluationMarker(legs) || postedLegsDifference(legs) != 0 {
		t.Fatalf("zero run legs = %+v, want the one cent marker reading as difference 0", legs)
	}
	for _, d := range []int64{1, -1, 10_000} {
		if isRevaluationMarker(revaluationLegs(d, "2026-09")) {
			t.Fatalf("a run of %d cents was read as the zero marker", d)
		}
	}
}
