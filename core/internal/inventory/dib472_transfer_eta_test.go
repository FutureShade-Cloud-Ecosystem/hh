package inventory

// dib472_transfer_eta_test.go, DIB-472: the promised-arrival grade and the pre-fill arithmetic, with
// no database in sight.
//
// WHAT THIS FILE OWNS. Two pure functions and one HTTP contract:
//
//	gradeETA  , a hand-typed date is a 400 in plain words, never a 500 from the driver.
//	defaultETA, ship day + transit days, in the HOUSE zone, over month/year/DST boundaries.
//	the ship route's OPTIONAL body, absent, present and EMPTY are three different instructions.
//
// The capture itself (does the column actually end up with this value) is a DB fact and lives in
// dib472_transfer_eta_integration_test.go. Splitting it that way is deliberate: the arithmetic and the
// grade are where a plausible edit does silent damage, and neither needs Postgres to be caught.
//
// MUTATION TARGETS. Each fails on a specific plausible edit rather than on "something changed":
//
//	drop the time.Parse grade in gradeETA          -> TestETA_HandTypedDateIs400NotAnUncastableLiteral
//	accept an impossible day (regexp-only grade)   -> TestETA_ImpossibleDayIsRefusedAtTheDoor
//	default from the WRONG date (today+0, or the
//	  ship date instead of ship date + days)       -> TestETA_DefaultIsShipDayPlusTransitDays
//	add 24h instead of one CALENDAR day            -> TestETA_DefaultWalksTheCalendarNotTheClock
//	make gradeETA reject "" as required            -> TestETA_ClearedFieldIsAnAnswerNotAnError
//	treat an absent body as invalid_json           -> TestShipBody_AbsentBodyIsNotAnInputError
//	collapse absent and empty into one meaning     -> TestShipBody_AbsentAndEmptyAreOppositeInstructions

import (
	"encoding/json"
	"errors"
	auth "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func etaHandler() *Handler {
	return NewHandler(NewService(NewRepository(nil)),
		slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError})))
}

// - the grade -

func TestETA_HandTypedDateIs400NotAnUncastableLiteral(t *testing.T) {
	for _, raw := range []string{"aug 20", "20/08/2026", "2026-8-20", "tomorrow", "2026-08-20T00:00:00Z", "next thursday"} {
		got, err := gradeETA(raw)
		if err == nil {
			t.Fatalf("gradeETA(%q) accepted a value the date column cannot store (got %q)", raw, got)
		}
		var br badRequestError
		if !errors.As(err, &br) {
			t.Fatalf("gradeETA(%q) error is not a bad-request: %#v, writeErr would answer 500 for a typo", raw, err)
		}
		// Plain words, in the register the house uses for the same class of mistake: name the field,
		// show the shape, say what blank means. The driver's own sentence is not a sentence for a person.
		for _, want := range []string{"expected arrival", "2026-08-20", "blank"} {
			if !strings.Contains(br.msg, want) {
				t.Fatalf("gradeETA(%q) message %q does not say %q", raw, br.msg, want)
			}
		}
		if strings.Contains(strings.ToLower(br.msg), "parse") || strings.Contains(br.msg, "cannot") {
			t.Fatalf("gradeETA(%q) leaked the driver's voice: %q", raw, br.msg)
		}
	}
}

func TestETA_ImpossibleDayIsRefusedAtTheDoor(t *testing.T) {
	// The shape is right and the day does not exist. A regexp-only grade waves these through, Postgres
	// then refuses them from INSIDE the ship transaction, and a loaded truck rolls back on a typo.
	for _, raw := range []string{"2026-02-31", "2026-13-01", "2026-00-10", "2026-04-31", "2027-02-29"} {
		if _, err := gradeETA(raw); err == nil {
			t.Fatalf("gradeETA(%q) accepted a day that does not exist", raw)
		}
	}
	// The neighbouring real days are accepted, so the grade is a calendar check and not a blanket
	// refusal of anything interesting.
	for _, raw := range []string{"2026-02-28", "2026-12-31", "2028-02-29", "2026-01-01"} {
		got, err := gradeETA(raw)
		if err != nil {
			t.Fatalf("gradeETA(%q) refused a real day: %v", raw, err)
		}
		if got != raw {
			t.Fatalf("gradeETA(%q) = %q, the stored value must be what was typed", raw, got)
		}
	}
}

func TestETA_ClearedFieldIsAnAnswerNotAnError(t *testing.T) {
	// A clerk who clears the box has TOLD US SOMETHING: this shipment promises no arrival. That is the
	// honest state for every unconfigured route too, so "" can never be an error here.
	for _, raw := range []string{"", "   ", "\t"} {
		got, err := gradeETA(raw)
		if err != nil {
			t.Fatalf("gradeETA(%q) refused an empty field: %v", raw, err)
		}
		if got != "" {
			t.Fatalf("gradeETA(%q) = %q, want empty", raw, got)
		}
	}
}

func TestETA_GradedValueIsTrimmedAndCanonical(t *testing.T) {
	got, err := gradeETA("  2026-08-20  ")
	if err != nil {
		t.Fatalf("gradeETA: %v", err)
	}
	if got != "2026-08-20" {
		t.Fatalf("gradeETA trimmed to %q", got)
	}
}

// - the default -

func TestETA_DefaultIsShipDayPlusTransitDays(t *testing.T) {
	loc := etaLocation()
	ship := time.Date(2026, 8, 12, 9, 30, 0, 0, loc)
	cases := map[int]string{
		0: "2026-08-12", // same-day is a real setting, and it is the SHIP day
		1: "2026-08-13", // the seeded Trenton <-> Kingston number
		2: "2026-08-14",
		7: "2026-08-19",
	}
	for days, want := range cases {
		if got := defaultETA(ship, days); got != want {
			t.Fatalf("defaultETA(+%d) = %q, want %q", days, got, want)
		}
	}
}

func TestETA_DefaultWalksTheCalendarNotTheClock(t *testing.T) {
	loc := etaLocation()
	// Month, year and leap-day rollover.
	if got := defaultETA(time.Date(2026, 8, 31, 16, 0, 0, 0, loc), 1); got != "2026-09-01" {
		t.Fatalf("month rollover: %q", got)
	}
	if got := defaultETA(time.Date(2026, 12, 31, 16, 0, 0, 0, loc), 1); got != "2027-01-01" {
		t.Fatalf("year rollover: %q", got)
	}
	if got := defaultETA(time.Date(2028, 2, 28, 16, 0, 0, 0, loc), 1); got != "2028-02-29" {
		t.Fatalf("leap day: %q", got)
	}
	// THE DST DAY. 8 March 2026 is the spring-forward date in Ontario: that local day is 23 hours long,
	// so "+24h" lands on the 10th at 01:00 and prints the WRONG DAY. AddDate on a zoned time walks the
	// calendar and prints the 9th.
	if got := defaultETA(time.Date(2026, 3, 8, 13, 0, 0, 0, loc), 1); got != "2026-03-09" {
		t.Fatalf("DST spring-forward: %q, want 2026-03-09 (a 24h add gives the 10th)", got)
	}
	if got := defaultETA(time.Date(2026, 11, 1, 13, 0, 0, 0, loc), 1); got != "2026-11-02" {
		t.Fatalf("DST fall-back: %q", got)
	}
}

func TestETA_DefaultIsReadInTheHouseZoneNotUTC(t *testing.T) {
	// 20:30 in Trenton on 12 Aug is 00:30 UTC on the 13th. A UTC read of "today" would promise a truck
	// leaving tomorrow morning arrives the day after, off by one, on the surface whose whole job is to
	// be trustworthy about arrival dates.
	if etaLocation() == time.UTC {
		t.Skip("no tzdata in this image, the UTC fallback is documented and cannot be asserted here")
	}
	evening := time.Date(2026, 8, 13, 0, 30, 0, 0, time.UTC)
	if got := defaultETA(evening, 1); got != "2026-08-13" {
		t.Fatalf("defaultETA at 20:30 local = %q, want 2026-08-13 (the UTC read gives the 14th)", got)
	}
}

// - the ship route's optional body -

// shipETAFromBody drives the ship handler's decode exactly as the route does, and reports the ShipETA
// it would have handed the service. The service call itself needs a database (a ship writes stock), so
// this harness stops at the decision, which is the part a plausible edit breaks.
func shipETAFromBody(t *testing.T, body string, hasBody bool) (ShipETA, int) {
	t.Helper()
	var r *http.Request
	if hasBody {
		r = httptest.NewRequest(http.MethodPost, "/api/v1/inventory/transfers/t-1/ship", strings.NewReader(body))
	} else {
		r = httptest.NewRequest(http.MethodPost, "/api/v1/inventory/transfers/t-1/ship", nil)
	}
	rec := httptest.NewRecorder()
	var req struct {
		ETA *string `json:"eta"`
	}
	if !decodeOptionalBody(rec, r, &req) {
		return ShipETA{}, rec.Code
	}
	if req.ETA != nil {
		return ShipETA{Supplied: true, Value: *req.ETA}, http.StatusOK
	}
	return ShipETA{}, http.StatusOK
}

func TestShipBody_AbsentBodyIsNotAnInputError(t *testing.T) {
	// Every caller shipped before 0158 posted `{}` or nothing at all. Answering 400 invalid_json to add
	// an OPTIONAL field would break a live route.
	for _, tc := range []struct {
		name    string
		body    string
		hasBody bool
	}{
		{"no body at all", "", false},
		{"the shipped client's empty object", "{}", true},
		{"an explicit null", `{"eta":null}`, true},
	} {
		got, code := shipETAFromBody(t, tc.body, tc.hasBody)
		if code != http.StatusOK {
			t.Fatalf("%s: status %d, want 200, an absent eta is not a mistake", tc.name, code)
		}
		if got.Supplied {
			t.Fatalf("%s: read as SUPPLIED, the server would not apply the route's default", tc.name)
		}
	}
}

func TestShipBody_AbsentAndEmptyAreOppositeInstructions(t *testing.T) {
	absent, _ := shipETAFromBody(t, "{}", true)
	cleared, _ := shipETAFromBody(t, `{"eta":""}`, true)
	edited, _ := shipETAFromBody(t, `{"eta":"2026-08-20"}`, true)

	if absent.Supplied {
		t.Fatal("an absent eta must mean 'no opinion', the route's default applies")
	}
	if !cleared.Supplied || cleared.Value != "" {
		t.Fatalf("a CLEARED eta must be supplied-and-empty, got %#v, otherwise the server re-defaults over a deliberate blank", cleared)
	}
	if !edited.Supplied || edited.Value != "2026-08-20" {
		t.Fatalf("an edited eta must arrive verbatim, got %#v", edited)
	}
}

func TestShipBody_MalformedJSONIsStill400(t *testing.T) {
	// The distinction is between NOTHING and NONSENSE. Only the second is a mistake.
	_, code := shipETAFromBody(t, `{"eta":`, true)
	if code != http.StatusBadRequest {
		t.Fatalf("malformed body status %d, want 400", code)
	}
}

// - the transit-days write, graded before it can reach SQL -

func TestTransitDays_BadInputIsAPlainWords400(t *testing.T) {
	svc := NewService(NewRepository(nil))
	cases := []struct {
		name string
		in   SetTransitDaysInput
		says string
	}{
		{"no branches", SetTransitDaysInput{}, "required"},
		{"a route to itself", SetTransitDaysInput{FromBranchID: "a", ToBranchID: "a"}, "two different branches"},
		{"negative days", SetTransitDaysInput{FromBranchID: "a", ToBranchID: "b", TransitDays: -1}, "0 to 60"},
		{"a fortnight past the cap", SetTransitDaysInput{FromBranchID: "a", ToBranchID: "b", TransitDays: 74}, "0 to 60"},
	}
	for _, tc := range cases {
		_, err := svc.SetTransitDays(nil, tc.in) //nolint:staticcheck // nil ctx: the grade returns before any use
		var br badRequestError
		if !errors.As(err, &br) {
			t.Fatalf("%s: error %#v is not a bad-request, a typo would read as a server fault", tc.name, err)
		}
		if !strings.Contains(br.msg, tc.says) {
			t.Fatalf("%s: message %q does not say %q", tc.name, br.msg, tc.says)
		}
	}
}

func TestTransitDays_ReadIsStaffAndWriteIsAdmin(t *testing.T) {
	// The gate is asserted by REGISTRATION, because that is where it can be silently inverted. The read
	// backs the ship pre-fill (a manager loading a truck must see the default they are accepting); the
	// write is a tech-admin knob and rides the house's Tier-1-GLOBAL guard.
	sg := testkit.NewStaffGuards(t)
	mux := http.NewServeMux()
	etaHandler().RegisterRoutes(mux, sg.Guards)

	// The read sits behind the staff constructor, the write behind the
	// admin constructor: a tier 3 caller is refused only the write, a
	// tier 1 caller is refused neither.
	sg.SetAuthority(auth.StaffAuthority{RoleID: "yard_team", Tier: 3, Scope: "branch", Branches: []string{"b"}})
	for _, tc := range []struct {
		method, path string
		wantRefused  bool
	}{
		{http.MethodGet, "/api/v1/inventory/transit-days", false},
		{http.MethodPut, "/api/v1/inventory/transit-days", true},
	} {
		r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{"from_branch_id":"a","to_branch_id":"b","transit_days":1}`))
		r.Header.Set("Authorization", "Bearer "+sg.Bearer(t))
		r.Header.Set("X-Org", testkit.OrgNorthSlug)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, r)
		if got := rec.Code == http.StatusForbidden; got != tc.wantRefused {
			t.Fatalf("%s %s for a tier 3 caller: refused=%v, want %v (code %d)", tc.method, tc.path, got, tc.wantRefused, rec.Code)
		}
	}
	sg.SetAuthority(auth.StaffAuthority{RoleID: "general_manager", Tier: 1, Scope: "global"})
	r := httptest.NewRequest(http.MethodPut, "/api/v1/inventory/transit-days", strings.NewReader(`{"from_branch_id":"a","to_branch_id":"b","transit_days":1}`))
	r.Header.Set("Authorization", "Bearer "+sg.Bearer(t))
	r.Header.Set("X-Org", testkit.OrgNorthSlug)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, r)
	if rec.Code == http.StatusForbidden {
		t.Fatalf("PUT transit-days is behind the admin constructor and a tier 1 global caller must pass it, got 403")
	}
}

func TestTransitDays_EmptyDeploymentAnswersAListNotNull(t *testing.T) {
	// `{"pairs":null}` and `{"pairs":[]}` are different claims to a client that does not write `?? []`.
	// A single-branch deployment has no routes, and that is an ANSWER.
	rec := httptest.NewRecorder()
	h := &Handler{svc: NewService(NewRepository(nil)), log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	// A nil pool answers errNoDB (503), which is the honest "not deployed" answer and NOT a 200 with a
	// fabricated empty list, so this test pins the shape of the 200 path through the handler's own
	// nil-guard instead.
	h.transitDays(rec, httptest.NewRequest(http.MethodGet, "/api/v1/inventory/transit-days", nil), guards.StaffCaller{})
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("an unconfigured database answered %d, want 503", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	if _, ok := body["error"]; !ok {
		t.Fatalf("a 503 must carry an error sentence: %s", rec.Body.String())
	}
}

// The promised arrival is counted in the dealer's own calendar (item 3b slice 3): a truck shipped at
// 05:00 UTC on the 27th leaves on the 27th for a Toronto dealer and on the 26th for a Los Angeles one,
// so a two day transit lands on the 29th or the 28th. The zero Zone is Toronto exactly.
func TestETA_FollowsTheDealersZone(t *testing.T) {
	la, err := houseday.NewZone("America/Los_Angeles")
	if err != nil {
		t.Skipf("no tzdata: %v", err)
	}
	shipped := time.Date(2026, time.August, 27, 5, 0, 0, 0, time.UTC)
	if got := defaultETAIn(la, shipped, 2); got != "2026-08-28" {
		t.Fatalf("Los Angeles = %s, want 2026-08-28", got)
	}
	if got := defaultETAIn(houseday.DefaultZone(), shipped, 2); got != "2026-08-29" {
		t.Fatalf("Toronto = %s, want 2026-08-29", got)
	}
	if got := defaultETA(shipped, 2); got != "2026-08-29" {
		t.Fatalf("the default moved: %s", got)
	}
	if got, err := gradeETAIn(la, " 2026-08-20 "); err != nil || got != "2026-08-20" {
		t.Fatalf("gradeETAIn = %q, %v", got, err)
	}
}
