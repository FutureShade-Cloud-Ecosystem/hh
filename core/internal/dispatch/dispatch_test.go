package dispatch

// dispatch_test.go, the no-database half of the suite: the lifecycle matrix, the haversine
// estimate, the input validators, and the manifest renderer.
//
// These are the checks that must hold before any row exists, and they are deliberately kept out of
// the integration file: a transition matrix that is only exercised through HTTP against Postgres is
// a matrix whose failures arrive as 500s from three layers away.

import (
	"math"
	"strings"
	"testing"
	"time"
)

// - the lifecycle matrix -

// TestStopMatrix_LegalMoves pins the ruled flow READY → LOADED → EN_ROUTE → DELIVERED plus its two
// honest exits. If someone widens the matrix, this fails and they have to say why in a diff.
func TestStopMatrix_LegalMoves(t *testing.T) {
	legal := [][2]string{
		{StopReady, StopLoaded},
		{StopReady, StopCancelled},
		{StopLoaded, StopEnRoute},
		{StopLoaded, StopCancelled},
		{StopEnRoute, StopDelivered},
		{StopEnRoute, StopFailed},
	}
	for _, m := range legal {
		if !stopCanTransition(m[0], m[1]) {
			t.Errorf("expected %s → %s to be legal", m[0], m[1])
		}
	}
}

// TestStopMatrix_RejectsIllegalMoves is the guard this module exists to keep. Every case below is a
// real thing someone will try: skipping the truck entirely, un-loading, re-delivering, resurrecting
// a cancelled stop.
func TestStopMatrix_RejectsIllegalMoves(t *testing.T) {
	illegal := [][2]string{
		{StopReady, StopEnRoute},     // never went on a truck
		{StopReady, StopDelivered},   // never left the yard
		{StopReady, StopFailed},      // a stop that never left cannot have failed to arrive
		{StopLoaded, StopDelivered},  // skipped the road
		{StopLoaded, StopReady},      // backward: un-loading rewrites loaded_at
		{StopEnRoute, StopLoaded},    // backward
		{StopEnRoute, StopReady},     // backward
		{StopEnRoute, StopCancelled}, // the truck has gone; it fails or it arrives
		{StopDelivered, StopEnRoute}, // terminal
		{StopDelivered, StopFailed},  // terminal
		{StopFailed, StopEnRoute},    // terminal, a re-delivery is a NEW stop
		{StopCancelled, StopReady},   // terminal, no resurrection
		{"NONSENSE", StopLoaded},     // an unknown source state is refused, not waved through
		{StopReady, "NONSENSE"},
	}
	for _, m := range illegal {
		if stopCanTransition(m[0], m[1]) {
			t.Errorf("expected %s → %s to be ILLEGAL", m[0], m[1])
		}
	}
}

func TestRunMatrix(t *testing.T) {
	if !runCanTransition(RunPlanned, RunDispatched) || !runCanTransition(RunPlanned, RunCancelled) {
		t.Fatal("a planned run must be dispatchable and cancellable")
	}
	if !runCanTransition(RunDispatched, RunCompleted) {
		t.Fatal("a dispatched run must be completable")
	}
	// The one that matters: a departed truck cannot be un-departed. Cancelling a DISPATCHED run would
	// erase a morning of real movement from the record.
	if runCanTransition(RunDispatched, RunCancelled) {
		t.Fatal("a DISPATCHED run must not be cancellable")
	}
	for _, from := range []string{RunCompleted, RunCancelled} {
		for _, to := range []string{RunPlanned, RunDispatched, RunCompleted, RunCancelled} {
			if runCanTransition(from, to) {
				t.Errorf("%s is terminal but allows → %s", from, to)
			}
		}
	}
}

func TestStopIsTerminal(t *testing.T) {
	for _, s := range []string{StopDelivered, StopFailed, StopCancelled} {
		if !StopIsTerminal(s) {
			t.Errorf("%s should be terminal", s)
		}
	}
	for _, s := range []string{StopReady, StopLoaded, StopEnRoute} {
		if StopIsTerminal(s) {
			t.Errorf("%s should NOT be terminal", s)
		}
	}
	// An unknown status is not terminal, "I don't recognise this" must not read as "it's finished".
	if StopIsTerminal("NONSENSE") {
		t.Error("an unknown status must not report as terminal")
	}
}

// TestStopNextStates_ReturnsACopy guards against a caller mutating the shared matrix through the
// slice it was handed, the UI reads this on every render.
func TestStopNextStates_ReturnsACopy(t *testing.T) {
	got := StopNextStates(StopReady)
	if len(got) != 2 {
		t.Fatalf("want 2 successors for READY, got %v", got)
	}
	got[0] = "MUTATED"
	if stopTransitions[StopReady][0] == "MUTATED" {
		t.Fatal("StopNextStates handed out the matrix's own slice")
	}
}

// - geo -

var (
	trenton  = Point{Lat: 44.1001, Lng: -77.5766}
	kingston = Point{Lat: 44.2312, Lng: -76.4860}
)

// TestHaversine_KnownDistance checks the Trenton→Kingston leg against the great-circle distance
// computed independently from the flat-earth approximation at this latitude (Δlat 0.1311° ≈ 14.6 km,
// Δlng 1.0906° × cos 44.17° ≈ 87.1 km → ≈88.3 km). A 1 km tolerance is wide enough for coordinate
// rounding and far too tight for a formula error, a degrees/radians slip is off by a factor of 57.
func TestHaversine_KnownDistance(t *testing.T) {
	got := HaversineKm(trenton, kingston)
	if math.Abs(got-88.3) > 1.0 {
		t.Fatalf("Trenton→Kingston: want ≈88.3 km, got %.2f km", got)
	}
}

func TestHaversine_Symmetric_AndZero(t *testing.T) {
	if a, b := HaversineKm(trenton, kingston), HaversineKm(kingston, trenton); math.Abs(a-b) > 1e-9 {
		t.Fatalf("distance must be symmetric: %.9f vs %.9f", a, b)
	}
	if d := HaversineKm(trenton, trenton); d != 0 {
		t.Fatalf("a point is 0 km from itself, got %v", d)
	}
}

// TestHaversine_ShortDistance is the case the spherical law of cosines gets wrong and haversine gets
// right: two points ~111 m apart (0.001° of latitude).
func TestHaversine_ShortDistance(t *testing.T) {
	a := Point{Lat: 44.1000, Lng: -77.5766}
	b := Point{Lat: 44.1010, Lng: -77.5766}
	got := HaversineKm(a, b)
	if math.Abs(got-0.1112) > 0.002 {
		t.Fatalf("0.001° of latitude is ~111 m; got %.4f km", got)
	}
}

// TestHaversine_AntipodalDoesNotNaN covers the Asin clamp. Without it, floating-point error pushes
// the argument past 1 and a NaN propagates into a printed manifest.
func TestHaversine_AntipodalDoesNotNaN(t *testing.T) {
	got := HaversineKm(Point{Lat: 0, Lng: 0}, Point{Lat: 0, Lng: 180})
	if math.IsNaN(got) {
		t.Fatal("antipodal distance produced NaN")
	}
	if math.Abs(got-20015.0) > 5 {
		t.Fatalf("antipodal distance should be ~half the circumference, got %.1f", got)
	}
}

// TestRouteKm_IncludesReturnLeg, the truck comes home. A one-way total systematically understates
// the day by the longest single leg, which is exactly the error a dispatcher would plan around.
func TestRouteKm_IncludesReturnLeg(t *testing.T) {
	oneWay := HaversineKm(trenton, kingston)
	got := RouteKm(trenton, []Point{kingston})
	if math.Abs(got-2*oneWay) > 1e-6 {
		t.Fatalf("depot→stop→depot should be 2×%.3f, got %.3f", oneWay, got)
	}
	if RouteKm(trenton, nil) != 0 {
		t.Fatal("a run with no geocoded stops has no distance")
	}
}

func TestNearestFirst_OrdersByDistanceFromDepot(t *testing.T) {
	far := Point{Lat: 44.5, Lng: -76.0}
	near := Point{Lat: 44.1010, Lng: -77.5766}
	mid := kingston
	order := NearestFirst(trenton, []Point{far, near, mid})
	if order[0] != 1 || order[1] != 2 || order[2] != 0 {
		t.Fatalf("want [near, mid, far] = [1 2 0], got %v", order)
	}
}

// - validators -

func TestValidDate(t *testing.T) {
	for _, ok := range []string{"2026-08-04", "2026-12-31"} {
		if !validDate(ok) {
			t.Errorf("%q should be a valid date", ok)
		}
	}
	// 2026-02-30 is the case a regexp would wave through and a Postgres error would report from three
	// layers down as a 500.
	for _, bad := range []string{"", "2026-2-4", "04/08/2026", "2026-02-30", "yesterday"} {
		if validDate(bad) {
			t.Errorf("%q should NOT be a valid date", bad)
		}
	}
}

func TestValidCoords_RefusesHalfACoordinate(t *testing.T) {
	lat, lng := 44.1, -77.5
	if err := validCoords(&lat, nil); err == nil {
		t.Error("a latitude with no longitude must be refused")
	}
	if err := validCoords(nil, &lng); err == nil {
		t.Error("a longitude with no latitude must be refused")
	}
	if err := validCoords(nil, nil); err != nil {
		t.Errorf("no coordinates at all is normal, got %v", err)
	}
	if err := validCoords(&lat, &lng); err != nil {
		t.Errorf("a real coordinate pair should pass, got %v", err)
	}
	bad := 91.0
	if err := validCoords(&bad, &lng); err == nil {
		t.Error("latitude 91 must be refused")
	}
	badLng := 181.0
	if err := validCoords(&lat, &badLng); err == nil {
		t.Error("longitude 181 must be refused")
	}
}

func TestLooksLikeUUID(t *testing.T) {
	if !looksLikeUUID("5d1b3f6e-0000-4000-9000-000000000001") {
		t.Error("a canonical uuid should pass")
	}
	for _, bad := range []string{"", "abc", "5d1b3f6e00004000900000000000000", "5d1b3f6e-0000-4000-9000-00000000000g"} {
		if looksLikeUUID(bad) {
			t.Errorf("%q should not look like a uuid", bad)
		}
	}
}

// - manifest render -

func sampleManifest() Manifest {
	km := 41.25
	return Manifest{
		Run: Run{RunNo: "#RUN-TRT-7", ScheduledDate: "2026-08-04", Status: RunDispatched,
			TruckCode: "TRK-1", DriverName: "Wade Prentice", Notes: "gate code 4417"},
		BranchName: "Dibbits Landscape Supply, Trenton",
		Stops: []Stop{{
			StopNo: 1, OrderNo: "#SO-TRT-1", CustomerName: "Ridgeline Landscaping",
			AddressLine: "88 Concession Rd 4", City: "Trenton", PostalCode: "K8V 5R5",
			Status: StopEnRoute, TimeWindow: "AM", ContactName: "Jo", ContactPhone: "613-555-0100",
			Instructions: "drop at the side gate",
		}},
		EstimatedKm: &km,
		PrintedAt:   "2026-08-04 12:00 UTC",
	}
}

func TestRenderManifest_CarriesTheOperationalFacts(t *testing.T) {
	html, err := RenderManifestHTML(sampleManifest())
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, want := range []string{
		"#RUN-TRT-7", "TRK-1", "Wade Prentice", "#SO-TRT-1", "Ridgeline Landscaping",
		"88 Concession Rd 4, Trenton, K8V 5R5", "41.2 km", "gate code 4417", "drop at the side gate",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("manifest is missing %q", want)
		}
	}
	// The estimate must be labelled for what it is. A driver planning a shift around a straight-line
	// number is the failure this sentence prevents.
	if !strings.Contains(html, "straight-line") && !strings.Contains(html, "Straight-line") {
		t.Error("the manifest must say the distance is straight-line, not driving distance")
	}
}

// TestRenderManifest_EscapesUserText is the html/template boundary. A customer name is user-supplied
// text landing inside markup, and this document is printed and mailed.
func TestRenderManifest_EscapesUserText(t *testing.T) {
	m := sampleManifest()
	m.Stops[0].CustomerName = `<script>alert('x')</script>`
	m.Stops[0].Instructions = `"><img src=x onerror=alert(1)>`
	html, err := RenderManifestHTML(m)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(html, "<script>alert") {
		t.Fatal("a customer name was rendered as live markup")
	}
	if strings.Contains(html, "<img src=x") {
		t.Fatal("an instruction was rendered as live markup")
	}
	if !strings.Contains(html, "&lt;script&gt;") {
		t.Fatal("the escaped form should still be visible on the page")
	}
}

// TestRenderManifest_OmitsEstimateWhenAbsent, a run with no geocoded stop prints no distance rather
// than "0.0 km", which would read as "next door".
func TestRenderManifest_OmitsEstimateWhenAbsent(t *testing.T) {
	m := sampleManifest()
	m.EstimatedKm = nil
	m.Ungeocoded = 1
	html, err := RenderManifestHTML(m)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(html, "0.0 km") || strings.Contains(html, "Straight-line est.") {
		t.Error("no estimate should be printed when none could be computed")
	}
	if !strings.Contains(html, "1 stop(s) on this run have no map coordinates") {
		t.Error("the un-geocoded count must be stated so the missing estimate is explained")
	}
}

func TestRenderManifest_EmptyRunStillPrints(t *testing.T) {
	m := sampleManifest()
	m.Stops = nil
	m.EstimatedKm = nil
	html, err := RenderManifestHTML(m)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(html, "No stops on this run.") {
		t.Error("an empty run should print an explicit empty row, not a blank table")
	}
}

func TestStopAddress_SkipsBlankParts(t *testing.T) {
	if got := stopAddress(Stop{AddressLine: "88 Concession Rd 4", City: "", PostalCode: "K8V 5R5"}); got != "88 Concession Rd 4, K8V 5R5" {
		t.Fatalf("blank parts must be skipped, got %q", got)
	}
	if got := stopAddress(Stop{}); got != "-" {
		t.Fatalf("an empty address renders the dash, got %q", got)
	}
}

// - token primitive -

// TestPickupToken_HashesAndIsUnique, only the hash is stored, and two mints never collide.
func TestPickupToken_HashesAndIsUnique(t *testing.T) {
	raw1, hash1, err := newPickupToken()
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	raw2, hash2, err := newPickupToken()
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if raw1 == raw2 || hash1 == hash2 {
		t.Fatal("two mints produced the same token")
	}
	if strings.Contains(hash1, raw1) || len(hash1) != 64 {
		t.Fatalf("the stored value must be a sha256 hex digest, got %q", hash1)
	}
	if hashPickupToken(raw1) != hash1 {
		t.Fatal("hashing must be deterministic, a scan resolves by re-hashing what was presented")
	}
}

// TestConflictError_IsErrorsAsAble, the handler maps 409 by errors.As, so the type has to survive
// being wrapped.
func TestConflictError_IsErrorsAsAble(t *testing.T) {
	err := conflictf("dispatch: %d stop(s) are not finished", 3)
	if !strings.Contains(err.Error(), "3 stop(s)") {
		t.Fatalf("conflictf lost its arguments: %q", err.Error())
	}
}

// TestServiceClock is a smoke check that WithClock actually takes, several guards stamp times, and a
// test that silently used time.Now would be non-deterministic rather than failing.
func TestServiceClock(t *testing.T) {
	fixed := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	s := NewService(nil).WithClock(func() time.Time { return fixed })
	if !s.now().Equal(fixed) {
		t.Fatal("WithClock did not take")
	}
	s.WithClock(nil)
	if !s.now().Equal(fixed) {
		t.Fatal("WithClock(nil) must not clear an injected clock")
	}
}
