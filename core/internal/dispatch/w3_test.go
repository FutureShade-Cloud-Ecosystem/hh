package dispatch

// w3_test.go, the pure-logic guards for the Wave 3 rules. Everything tested here is a total function
// over plain data, which is where these rules were deliberately put: the MTO roll-up, the scan-trail
// fold and the reschedule shape checks are the decisions that could be WRONG, and a database is not
// needed to be sure of any of them.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

func f64(v float64) *float64 { return &v }

// - the MTO roll-up (DIB-63) -

func TestRollUpMTO_OneMadeToOrderStopMakesTheRunMixed(t *testing.T) {
	// The rule that matters operationally: a run of eleven standard drops and ONE cut-to-order pallet
	// is a run the driver must be told about. A "majority" or "most common class" rule would print
	// STOCK on that sheet, which is the exact failure DIB-63 exists to end.
	stops := []ManifestStop{
		{MTO: StopMTO{Class: MTOStandard}},
		{MTO: StopMTO{Class: MTOStandard}},
		{MTO: StopMTO{Class: MTOMade}},
	}
	got := rollUpMTO(stops, nil)
	if got.Class != MTOMixed {
		t.Fatalf("run class = %q, want MIXED, one MTO stop among standard ones is a mixed load", got.Class)
	}
	if got.MTOStops != 1 {
		t.Fatalf("mto stop count = %d, want 1", got.MTOStops)
	}
}

func TestRollUpMTO_ClassEdges(t *testing.T) {
	cases := map[string]struct {
		classes []string
		want    string
	}{
		"all standard":        {[]string{MTOStandard, MTOStandard}, MTOStandard},
		"all made to order":   {[]string{MTOMade, MTOMade}, MTOMade},
		"a single mixed stop": {[]string{MTOMixed}, MTOMixed},
		"empty run":           {nil, MTOStandard},
	}
	for name, c := range cases {
		stops := make([]ManifestStop, 0, len(c.classes))
		for _, cl := range c.classes {
			stops = append(stops, ManifestStop{MTO: StopMTO{Class: cl}})
		}
		if got := rollUpMTO(stops, nil); got.Class != c.want {
			t.Fatalf("%s: class = %q, want %q", name, got.Class, c.want)
		}
	}
}

func TestRollUpMTO_WeightTotalIsHonestAboutWhatItCounted(t *testing.T) {
	stops := []ManifestStop{
		{MTO: StopMTO{Class: MTOStandard, WeightKg: f64(900)}},
		{MTO: StopMTO{Class: MTOStandard}}, // never weighed
		{MTO: StopMTO{Class: MTOStandard, WeightKg: f64(600)}},
	}
	got := rollUpMTO(stops, f64(1200))
	if got.TotalWeightKg != 1500 {
		t.Fatalf("total = %v, want 1500 (only the weighed stops)", got.TotalWeightKg)
	}
	if got.WeighedStops != 2 || got.UnweighedStops != 1 {
		t.Fatalf("weighed/unweighed = %d/%d, want 2/1", got.WeighedStops, got.UnweighedStops)
	}
	// 1500 > 1200, but one stop was never weighed, so the total is NOT the load. Warning suppressed.
	if got.OverCapacity {
		t.Fatal("a partial weight total must never raise an over-capacity warning, the number is not the load")
	}
}

func TestRollUpMTO_OverCapacityFiresOnlyOnACompleteTotal(t *testing.T) {
	stops := []ManifestStop{
		{MTO: StopMTO{Class: MTOStandard, WeightKg: f64(900)}},
		{MTO: StopMTO{Class: MTOStandard, WeightKg: f64(600)}},
	}
	if got := rollUpMTO(stops, f64(1200)); !got.OverCapacity {
		t.Fatal("a complete total over a known capacity must warn")
	}
	if got := rollUpMTO(stops, nil); got.OverCapacity {
		t.Fatal("no capacity recorded means no comparison is possible, it must not warn")
	}
	if got := rollUpMTO(stops, f64(1500)); got.OverCapacity {
		t.Fatal("exactly at capacity is not over it")
	}
}

// - the manifest render (DIB-63) -

func TestRenderManifest_CarriesTheMTOBandAndPerStopClass(t *testing.T) {
	m := Manifest{
		Run:        Run{RunNo: "#RUN-TRT-9", ScheduledDate: "2026-08-06", Status: RunPlanned},
		BranchName: "Trenton",
		MTOStops: []ManifestStop{
			{Stop: Stop{StopNo: 1, OrderNo: "#SO-TRT-1", CustomerName: "Ridgeline"},
				MTO: StopMTO{Class: MTOMade, Note: "cut to 1150mm, do not tip", WeightKg: f64(820)}},
			{Stop: Stop{StopNo: 2, OrderNo: "#SO-TRT-2", CustomerName: "Bay Hardscape"},
				MTO: StopMTO{Class: MTOStandard}},
		},
	}
	m.MTO = rollUpMTO(m.MTOStops, f64(4000))
	html, err := RenderManifestHTML(m)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, want := range []string{
		"MIXED, MTO AND STOCK", // the run band
		"MADE TO ORDER",        // the per-stop class
		"cut to 1150mm, do not tip",
		"820 kg",
		"Made-to-order stops",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("manifest is missing %q, the compliance band is what DIB-63 adds", want)
		}
	}
}

func TestRenderManifest_AWave2ModelStillPrintsEveryStop(t *testing.T) {
	// The regression guard for the whole feature: a Manifest assembled the Wave 2 way (Stops set,
	// MTOStops empty) must still render its rows. A compliance column that silently blanks a printed
	// manifest would be a strictly worse outcome than not having one.
	m := Manifest{
		Run:        Run{RunNo: "#RUN-TRT-4", ScheduledDate: "2026-08-06"},
		BranchName: "Trenton",
		Stops:      []Stop{{StopNo: 1, OrderNo: "#SO-TRT-77", CustomerName: "Ridgeline"}},
	}
	html, err := RenderManifestHTML(m)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(html, "#SO-TRT-77") {
		t.Fatal("a Wave 2 manifest model must still print its stops")
	}
	if !strings.Contains(html, "STOCK") {
		t.Fatal("an unclassified load renders as STOCK, that is what an unclassified load is")
	}
}

// - the scan trail fold (DIB-75) -

func scanAt(loadRef, cp string, min int) ScanCheckpoint {
	return ScanCheckpoint{
		LoadRef: loadRef, Checkpoint: cp,
		ScannedAt: time.Date(2026, 8, 6, 8, min, 0, 0, time.UTC),
	}
}

func TestBuildScanTrail_GroupsByLoadAndOrdersByStageNotByClock(t *testing.T) {
	// The IN_TRANSIT scan is recorded with an EARLIER clock than the PICKED one, two scanners whose
	// clocks disagree. The strip must still draw the stages in order.
	rows := []ScanCheckpoint{
		scanAt("PAL-1", ScanReceived, 0),
		scanAt("PAL-1", ScanInTransit, 10),
		scanAt("PAL-1", ScanPicked, 20),
	}
	trail := BuildScanTrail("o-1", rows)
	if len(trail.Loads) != 1 {
		t.Fatalf("loads = %d, want 1", len(trail.Loads))
	}
	got := trail.Loads[0].Reached
	want := []string{ScanReceived, ScanPicked, ScanInTransit}
	if len(got) != len(want) {
		t.Fatalf("reached = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("reached = %v, want %v (stage order, not clock order)", got, want)
		}
	}
	if trail.Loads[0].Furthest != ScanInTransit {
		t.Fatalf("furthest = %q, want IN_TRANSIT", trail.Loads[0].Furthest)
	}
}

func TestBuildScanTrail_CompleteNeedsEVERYLoadDelivered(t *testing.T) {
	// Three pallets, two delivered. An order is not delivered because most of it is.
	rows := []ScanCheckpoint{
		scanAt("PAL-1", ScanDelivered, 0),
		scanAt("PAL-2", ScanDelivered, 1),
		scanAt("PAL-3", ScanInTransit, 2),
	}
	if BuildScanTrail("o-1", rows).Complete {
		t.Fatal("an order with a load still in transit is not complete")
	}
	rows = append(rows, scanAt("PAL-3", ScanDelivered, 3))
	if !BuildScanTrail("o-1", rows).Complete {
		t.Fatal("every load delivered means the order's trail is complete")
	}
}

func TestBuildScanTrail_EmptyIsEmptyNotComplete(t *testing.T) {
	trail := BuildScanTrail("o-1", nil)
	if trail.Complete {
		t.Fatal("an order nobody has scanned is not a delivered order")
	}
	if len(trail.Loads) != 0 {
		t.Fatalf("loads = %d, want 0", len(trail.Loads))
	}
}

func TestBuildScanTrail_PODRefTravelsToTheLoad(t *testing.T) {
	rows := []ScanCheckpoint{
		scanAt("PAL-1", ScanInTransit, 0),
		{LoadRef: "PAL-1", Checkpoint: ScanDelivered, PODSignatureRef: "sig_abc",
			ScannedAt: time.Date(2026, 8, 6, 9, 0, 0, 0, time.UTC)},
	}
	trail := BuildScanTrail("o-1", rows)
	if trail.Loads[0].PODSignatureRef != "sig_abc" {
		t.Fatal("the attestation that closed the trail must be visible on the load, that is the DIB-75 tie")
	}
}

// - the reschedule shape rules (DIB-382) -

func w3Service(now string) *Service {
	at, _ := time.Parse("2006-01-02", now)
	return NewService(NewRepository(nil)).WithClock(func() time.Time { return at })
}

func TestValidateReschedule_Refusals(t *testing.T) {
	s := w3Service("2026-08-06")
	cases := map[string]struct {
		in   RescheduleInput
		from string
	}{
		"a date that is not a date":  {RescheduleInput{ToDate: "next tuesday", Reason: "customer"}, "2026-08-06"},
		"the date it already has":    {RescheduleInput{ToDate: "2026-08-06", Reason: "customer"}, "2026-08-06"},
		"a date that has passed":     {RescheduleInput{ToDate: "2026-08-05", Reason: "customer"}, "2026-08-06"},
		"no reason at all":           {RescheduleInput{ToDate: "2026-08-08"}, "2026-08-06"},
		"a reason of only spaces":    {RescheduleInput{ToDate: "2026-08-08", Reason: "   "}, "2026-08-06"},
		"an impossible calendar day": {RescheduleInput{ToDate: "2026-02-30", Reason: "customer"}, "2026-08-06"},
	}
	for name, c := range cases {
		if _, _, err := s.validateReschedule(c.in, c.from); err == nil {
			t.Fatalf("%s: expected a refusal, got none", name)
		}
	}
}

func TestValidateReschedule_AcceptsAForwardMoveWithAReason(t *testing.T) {
	s := w3Service("2026-08-06")
	to, reason, err := s.validateReschedule(RescheduleInput{ToDate: "2026-08-11", Reason: "  customer away  "}, "2026-08-06")
	if err != nil {
		t.Fatalf("unexpected refusal: %v", err)
	}
	if to != "2026-08-11" {
		t.Fatalf("to = %q", to)
	}
	if reason != "customer away" {
		t.Fatalf("reason = %q, want it trimmed", reason)
	}
}

func TestValidateReschedule_TodayIsStillAllowed(t *testing.T) {
	// Pulling a delivery FORWARD onto today is a normal act ("they can take it this afternoon"). Only
	// dates that have already gone are refused.
	s := w3Service("2026-08-06")
	if _, _, err := s.validateReschedule(RescheduleInput{ToDate: "2026-08-06", Reason: "x"}, "2026-08-09"); err != nil {
		t.Fatalf("moving a stop onto today must be allowed: %v", err)
	}
}

// - the AR/COD gate (Swarm B's seam) -

type fakeGate struct {
	deny map[string]DispatchGateDecision
	err  error
	hits int
}

func (g *fakeGate) AllowDispatch(_ context.Context, orderID string) (DispatchGateDecision, error) {
	g.hits++
	if g.err != nil {
		return DispatchGateDecision{}, g.err
	}
	if d, ok := g.deny[orderID]; ok {
		return d, nil
	}
	return DispatchGateDecision{Allow: true}, nil
}

func gateCtx() context.Context {
	return identity.ContextWithStaff(context.Background(), identity.StaffContext{
		UserID: "u-1", Scope: identity.ScopeGlobal, Status: identity.StatusActive,
	})
}

func TestGate_NilAllowsAndIsNeverCalled(t *testing.T) {
	s := w3Service("2026-08-06")
	if d := s.gateFor(gateCtx(), "o-1"); !d.Allow {
		t.Fatal("a nil gate must ALLOW, an unwired predicate cannot be allowed to stop the yard")
	}
	if err := s.assertGateAllowsRun(gateCtx(), []Stop{{OrderID: "o-1"}}); err != nil {
		t.Fatalf("a nil gate must not refuse a run: %v", err)
	}
}

func TestGate_ErrorAllowsAndIsLoggedNotFatal(t *testing.T) {
	s := w3Service("2026-08-06").WithARGate(&fakeGate{err: errors.New("AR is down")})
	if d := s.gateFor(gateCtx(), "o-1"); !d.Allow {
		t.Fatal("a gate that cannot answer must allow, the alternative is a yard stopped by an outage")
	}
}

func TestGate_DeniedRunIsRefusedNamingTheOrderAndTheReason(t *testing.T) {
	gate := &fakeGate{deny: map[string]DispatchGateDecision{
		"o-2": {Allow: false, Reason: "COD balance of $1,240.00 is outstanding.", Code: "COD_UNPAID"},
	}}
	s := w3Service("2026-08-06").WithARGate(gate)
	err := s.assertGateAllowsRun(gateCtx(), []Stop{
		{OrderID: "o-1", OrderNo: "#SO-TRT-1"},
		{OrderID: "o-2", OrderNo: "#SO-TRT-2"},
	})
	if err == nil {
		t.Fatal("a run carrying a held order must be refused")
	}
	if !strings.Contains(err.Error(), "#SO-TRT-2") {
		t.Fatalf("the refusal must NAME the held order, got %q", err)
	}
	if !strings.Contains(err.Error(), "COD balance") {
		t.Fatalf("the refusal must carry the gate's own sentence, got %q", err)
	}
}

func TestGate_CancelledAndFailedStopsAreNotGated(t *testing.T) {
	// A cancelled stop is not going on the truck, so its payment state is irrelevant. Gating it would
	// let a stale cancelled row block a whole run.
	gate := &fakeGate{deny: map[string]DispatchGateDecision{
		"o-dead": {Allow: false, Reason: "unpaid"},
	}}
	s := w3Service("2026-08-06").WithARGate(gate)
	if err := s.assertGateAllowsRun(gateCtx(), []Stop{
		{OrderID: "o-dead", OrderNo: "#SO-X", Status: StopCancelled},
		{OrderID: "o-dead", OrderNo: "#SO-Y", Status: StopFailed},
	}); err != nil {
		t.Fatalf("cancelled/failed stops must not be gated: %v", err)
	}
}

func TestGate_ABlankRefusalStillGetsASentence(t *testing.T) {
	gate := &fakeGate{deny: map[string]DispatchGateDecision{"o-1": {Allow: false}}}
	s := w3Service("2026-08-06").WithARGate(gate)
	d := s.gateFor(gateCtx(), "o-1")
	if strings.TrimSpace(d.Reason) == "" {
		t.Fatal("a refusal with no sentence is unusable at a customer's gate, one must be substituted")
	}
}

// - the pick feed's urgency grading (item 1) -

func TestPickFeedPriority_GradesByDeliveryDate(t *testing.T) {
	at := time.Date(2026, 8, 6, 14, 0, 0, 0, time.UTC)
	f := NewPickFeed(NewRepository(nil)).WithClock(func() time.Time { return at })
	cases := map[string]string{
		"2026-08-05": "URGENT", // yesterday, still not picked, it is already late
		"2026-08-06": "HIGH",   // today; the truck goes this afternoon
		"2026-08-07": "NORMAL",
		"garbage":    "NORMAL", // a formatting bug must not flood the top of the queue
	}
	for date, want := range cases {
		if got := f.priorityFor(date); got != want {
			t.Fatalf("priorityFor(%q) = %q, want %q", date, got, want)
		}
	}
}

func TestPickFeedTicket_ReferenceIsTheStopID(t *testing.T) {
	// The fixture's reference was FIXTURE-XXXXXX and meant nothing to anybody. A real reference is the
	// dispatch handle a picker can read to a dispatcher and get an answer.
	f := NewPickFeed(NewRepository(nil))
	tk := f.ticket(pickFeedStop{
		StopID: "11111111-1111-4111-8111-111111111111", OrderID: "o-1",
		ScheduledDate: "2026-08-06", MTOClass: MTOMade,
	})
	if tk.Reference != "11111111-1111-4111-8111-111111111111" {
		t.Fatalf("reference = %q, want the stop id", tk.Reference)
	}
	if tk.Fulfillment != "DELIVERY" {
		t.Fatalf("fulfillment = %q, a delivery stop exists because the order is being delivered", tk.Fulfillment)
	}
	if tk.MTOClass != MTOMade {
		t.Fatal("the picker needs the MTO class before the driver does")
	}
	if tk.RequestedAt == nil {
		t.Fatal("a parseable scheduled date must produce a requested-at")
	}
}
