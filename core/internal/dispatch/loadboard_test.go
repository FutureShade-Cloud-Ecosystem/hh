package dispatch

// loadboard_test.go, the no-database half of the load board's proofs (Aug-26 call, items A-I).
//
// TWO OF THESE TESTS ARE THE POINT OF THE FILE and the rest support them:
//
//   TestLoadValue_ByteAbsentForARoleWithoutIt  , the money gate, asserted on the BYTES. Not "the
//       field is empty", not "the UI hides it": the string `value_cents` does not occur in the
//       response a frontline caller receives.
//   TestLock_RefusesTheMutationWhateverTheClientSends, the COMMITMENT gate (0185), asserted at the
//       SERVICE, through every field a client could set trying to get around it.
//
// THE TEST NAMES STILL SAY "Lock" AND THAT IS DELIBERATE. R5 renamed the vocabulary; the merge proof
// this branch owes is that no pre-existing test NAME disappeared, so the identifiers are left alone
// and the bodies now prove the same substantive property under the unified model. A renamed test
// looks identical to a deleted one in `comm -23`, and the point of the arithmetic is to tell them
// apart.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/settings"
)

// - the gauges -

func TestCapacityBand_TheThreeColoursAndTheFourthAnswer(t *testing.T) {
	cases := []struct {
		used, cap float64
		want      string
		why       string
	}{
		{0, 10000, BandGreen, "an empty truck is green"},
		{8000, 10000, BandGreen, "80% is still green, there is a pallet's room left"},
		{8500, 10000, BandAmber, "the amber line is 85%, and it is inclusive"},
		{9999, 10000, BandAmber, "under capacity is never red"},
		{10000, 10000, BandAmber, "exactly full is amber, not red, it fits"},
		{10001, 10000, BandRed, "one kilo over is over"},
		{25000, 10000, BandRed, "the client's porcelain load on the wrong truck"},
		// The one that matters most, and the reason this is not a two-line function.
		{8000, 0, BandUnknown, "a truck nobody measured is UNKNOWN, painting it green says 'there is room'"},
		{8000, -1, BandUnknown, "a nonsense capacity is unknown, not green"},
	}
	for _, c := range cases {
		if got := CapacityBand(c.used, c.cap); got != c.want {
			t.Errorf("CapacityBand(%v, %v) = %s, want %s, %s", c.used, c.cap, got, c.want, c.why)
		}
	}
}

func TestWorstBand_IsPessimistic(t *testing.T) {
	if got := WorstBand(BandGreen, BandRed, BandGreen); got != BandRed {
		t.Fatalf("a load that is fine on weight and over on volume is an OVER load; got %s", got)
	}
	if got := WorstBand(BandGreen, BandUnknown); got != BandGreen {
		t.Fatalf("an unknown gauge must not drown a known one; got %s", got)
	}
	if got := WorstBand(); got != BandUnknown {
		t.Fatalf("nothing measured is UNKNOWN; got %s", got)
	}
}

func iptr(v int) *int    { return &v }
func i64(v int64) *int64 { return &v }

func TestBuildGauge_NilIsNotZero(t *testing.T) {
	stops := []Stop{
		{WeightKg: f64(4000), VolumeM3: f64(6), PalletPositions: iptr(4)},
		{}, // nobody weighed this one
		{WeightKg: f64(3000)},
	}
	g := BuildGauge(stops, LoadCapacity{CapacityKg: f64(10000), CapacityM3: f64(30), PalletPositions: iptr(12)}, false)
	if g.StopCount != 3 {
		t.Fatalf("stop count = %d, want 3", g.StopCount)
	}
	if g.WeighedStops != 2 {
		t.Fatalf("weighed = %d, want 2, an unweighed stop must not count as weighed", g.WeighedStops)
	}
	if g.WeightKg == nil || *g.WeightKg != 7000 {
		t.Fatalf("weight = %v, want 7000, the unmeasured stop contributes nothing", g.WeightKg)
	}
	if g.WeightBand != BandGreen {
		t.Fatalf("7000 of 10000 is green; got %s", g.WeightBand)
	}
	// Only one stop had a volume; the volume gauge is real but partial, and MeasuredStops says so.
	if g.MeasuredStops != 1 || g.VolumeM3 == nil || *g.VolumeM3 != 6 {
		t.Fatalf("volume must be 6 from 1 measured stop; got %v from %d", g.VolumeM3, g.MeasuredStops)
	}
}

func TestBuildGauge_UnmeasuredTruckDrawsNoGauge(t *testing.T) {
	g := BuildGauge([]Stop{{WeightKg: f64(9000)}}, LoadCapacity{}, false)
	if g.WeightBand != BandUnknown || g.Band != BandUnknown {
		t.Fatalf("a truck with no capacity on file must gauge UNKNOWN, got %s/%s", g.WeightBand, g.Band)
	}
	if g.WeightKg == nil || *g.WeightKg != 9000 {
		t.Fatal("the TOTAL is still real even when the capacity is not, the dispatcher still needs the number")
	}
}

// - the money gate, on the bytes -

func TestBuildGauge_ValueIsAbsentUntilTheGateSaysOtherwise(t *testing.T) {
	stops := []Stop{{ValueCents: i64(3000000), FreightCents: i64(45000)}}

	denied, err := json.Marshal(BuildGauge(stops, LoadCapacity{}, false))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(denied), "value_cents") {
		t.Fatalf("value_cents must be BYTE-ABSENT when the gate denies; got %s", denied)
	}
	if strings.Contains(string(denied), "freight_cents") {
		t.Fatalf("freight is money too, freight_cents must be byte-absent; got %s", denied)
	}
	if strings.Contains(string(denied), "3000000") {
		t.Fatalf("the figure itself must not appear anywhere in the payload; got %s", denied)
	}

	allowed, err := json.Marshal(BuildGauge(stops, LoadCapacity{}, true))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(allowed), `"value_cents":3000000`) {
		t.Fatalf("an allowed caller must receive the figure; got %s", allowed)
	}
}

// TestLoadStopPayload_MoneyIsBYTE_ABSENT_NotNull is the per-load half of the same gate, and it is
// the one the DRIVER actually meets: LoadBoard serialises a LoadStop for every card on the board,
// so this, not BuildGauge's run-level LoadGauge, is where a driver's money would leak.
//
// UNION TRAIN 14 ADDED THIS. The run-level gauge was already pinned by
// TestBuildGauge_ValueIsAbsentUntilTheGateSaysOtherwise, but LoadStop's two pointers were pinned by
// NOTHING at `origin/qa`. Dropping `omitempty` from either one, a one-word edit, the exact change
// train 13 caught on a different struct, left the whole suite GREEN while putting
//
//	"value_cents":null,"freight_cents":null
//
// on a driver's wire seven times over. Measured over HTTP against a real server before this test
// existed. The POLICY is still right in that world and no figure leaks, which is precisely why a
// test written as "the pointer is nil" would pass it happily: the doctrine's contract is
// BYTE-ABSENCE, the KEY is not on the wire, so this test asserts bytes.
func TestLoadStopPayload_MoneyIsBYTE_ABSENT_NotNull(t *testing.T) {
	// A refused caller: the service leaves both pointers nil.
	denied, err := json.Marshal(LoadStop{Stop: Stop{ID: "s-1"}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, forbidden := range []string{"value_cents", "freight_cents"} {
		if strings.Contains(string(denied), forbidden) {
			t.Fatalf("a refused caller's LoadStop must not carry the %q KEY at all, not even as null. "+
				"Got %s", forbidden, denied)
		}
	}
	if strings.Contains(string(denied), "null") {
		t.Fatalf("no field on a refused LoadStop may marshal as null; got %s", denied)
	}

	// An allowed caller: the figures are there, and they are the real ones.
	allowed, err := json.Marshal(LoadStop{Stop: Stop{ID: "s-1"}, ValueCents: i64(3000000), FreightCents: i64(45000)})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(allowed), `"value_cents":3000000`) {
		t.Fatalf("an allowed caller must receive the load value; got %s", allowed)
	}
	if !strings.Contains(string(allowed), `"freight_cents":45000`) {
		t.Fatalf("an allowed caller must receive the freight; got %s", allowed)
	}
}

// TestLoadBoardMoneyFields_AllCarryOmitempty is the same gate stated once for the whole package, so
// a THIRD money field added to a THIRD payload struct cannot arrive unguarded the way LoadStop did.
// It reads the source rather than a value, because the defect is a missing struct tag.
func TestLoadBoardMoneyFields_AllCarryOmitempty(t *testing.T) {
	for _, file := range []string{"loadboard.go", "service_loadboard.go"} {
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		for _, line := range strings.Split(string(body), "\n") {
			if !strings.Contains(line, "*int64 `json:\"") {
				continue
			}
			if !strings.Contains(line, "value_cents") && !strings.Contains(line, "freight_cents") {
				continue
			}
			if !strings.Contains(line, ",omitempty") {
				t.Errorf("%s: a gated money pointer without omitempty marshals as null, which is not "+
					"byte-absence: %s", file, strings.TrimSpace(line))
			}
		}
	}
}

// TestStopPayload_NeverCarriesMoney is the structural half of the gate: the ORDINARY stop payload -
// the one the day board, the driver view, the manifest feed and every other read return, has no
// money on it AT ALL, for anybody. That is why there is no read path left to forget.
func TestStopPayload_NeverCarriesMoney(t *testing.T) {
	blob, err := json.Marshal(Stop{
		ID: "s-1", ValueCents: i64(3000000), FreightCents: i64(45000), WeightKg: f64(25000),
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, forbidden := range []string{"value_cents", "freight_cents", "3000000", "45000"} {
		if strings.Contains(string(blob), forbidden) {
			t.Fatalf("the ordinary Stop payload must never carry %q; got %s", forbidden, blob)
		}
	}
	if !strings.Contains(string(blob), `"weight_kg":25000`) {
		t.Fatalf("weight is NOT money and must still be there; got %s", blob)
	}
}

// - the gate, through the service -

func lbCaller(tier identity.Tier, role string) identity.StaffContext {
	return identity.StaffContext{
		UserID: "u-1", RoleCode: role, Tier: tier, Scope: identity.ScopeGlobal,
		Status: identity.StatusActive,
	}
}

func lbService(caller identity.StaffContext, now string) *Service {
	at, _ := time.Parse(time.RFC3339, now)
	return NewService(nil).
		withCaller(func(context.Context) (identity.StaffContext, bool) { return caller, true }).
		WithClock(func() time.Time { return at })
}

func TestLoadValueAllowed_ManagerUpIsTheDefaultAndFailsClosed(t *testing.T) {
	// With no repository the policy read cannot happen at all, so the DEFAULT policy applies -
	// MANAGER_UP. A frontline caller must be denied by that path, not waved through it.
	front := lbService(lbCaller(identity.TierFrontline, "DRIVERS"), "2026-08-27T06:45:00Z")
	if front.loadValueAllowed(context.Background(), "b-1") {
		t.Fatal("a driver must not receive a load's dollar value under the MANAGER_UP default")
	}
	mgr := lbService(lbCaller(identity.TierManager, "LOGISTICS_MANAGER"), "2026-08-27T06:45:00Z")
	if !mgr.loadValueAllowed(context.Background(), "b-1") {
		t.Fatal("a logistics manager must receive it under MANAGER_UP")
	}
	// An UNRESOLVABLE caller is denied, the gate answers no to every question it cannot answer.
	anon := NewService(nil).withCaller(func(context.Context) (identity.StaffContext, bool) {
		return identity.StaffContext{}, false
	})
	if anon.loadValueAllowed(context.Background(), "b-1") {
		t.Fatal("an unresolved caller must be denied, fail closed")
	}
}

func TestDefaultPolicy_FailsClosedInBothDirections(t *testing.T) {
	p := DefaultPolicy()
	if p.CommitPromptHours != 48 || p.ReopenPolicy != ReopenManager {
		t.Fatalf("the fallback policy must keep a commitment manager-reopenable, not run open: %+v", p)
	}
	if p.LoadValueVisibility != settings.VisibilityManagerUp {
		t.Fatalf("the fallback must keep value manager-only, got %s", p.LoadValueVisibility)
	}
	// The working view is its own number and it is NOT the planning horizon. Falling back to the
	// horizon would put eight weeks of work in a fortnight's view.
	if p.WorkingViewDays != 14 {
		t.Fatalf("the working view defaults to about two weeks, got %d", p.WorkingViewDays)
	}
	if p.CommitHorizonWeeks != 8 {
		t.Fatalf("the PLANNING horizon is unchanged at 8 weeks, got %d", p.CommitHorizonWeeks)
	}
}

// - the 48 hours, in its only remaining role -

// TestEvaluateLock_TheWindowAndItsEdges keeps its name and changes its subject, because 0185 changed
// what the window IS. It no longer gates anything: it PROMPTS. The arithmetic and its edges are the
// same, and this pins them at the boundary where an off-by-one would either nag a day early or stay
// silent on the morning a truck loads.
func TestEvaluateLock_TheWindowAndItsEdges(t *testing.T) {
	policy := DefaultPolicy() // 48 hours, MANAGER
	at := func(s string) time.Time {
		v, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatalf("bad time %q", s)
		}
		return v
	}
	// The delivery day starts at midnight on 2026-09-01, so the prompt opens at 2026-08-30T00:00Z.
	cases := []struct {
		now string
		due bool
		why string
	}{
		{"2026-08-29T23:59:00Z", false, "one minute before the window opens nobody is prompted"},
		{"2026-08-30T00:00:00Z", true, "the window is inclusive at its opening instant"},
		{"2026-08-31T12:00:00Z", true, "well inside the window"},
		{"2026-09-01T08:00:00Z", true, "the day itself is inside the window"},
	}
	for _, c := range cases {
		if got := DueToCommit("2026-09-01", at(c.now), policy.CommitPromptHours); got != c.due {
			t.Errorf("at %s due=%v, want %v, %s", c.now, got, c.due, c.why)
		}
	}
	// A zero window switches the prompt off without changing anything else.
	if DueToCommit("2026-09-01", at("2026-09-01T08:00:00Z"), 0) {
		t.Fatal("a zero-hour prompt window must never prompt")
	}
	// A date that will not parse PROMPTS. This is the opposite of 0184's fail-closed on the same
	// arithmetic, and deliberately: a prompt is not a gate, and a broken row belongs in front of a
	// person rather than hidden by a silent predicate.
	if !DueToCommit("not-a-date", at("2026-08-31T12:00:00Z"), 48) {
		t.Fatal("a load whose date cannot be read must be surfaced, not silently skipped")
	}

	// AND THE THING THAT ACTUALLY GATES IS THE COMMITMENT, WITH NO CLOCK IN IT AT ALL. A truck two
	// months out carrying a committed load is committed; a truck loading this afternoon carrying only
	// asks is not.
	far := EvaluateCommit("2026-11-01", RunAuto, 1, 0, policy, at("2026-08-31T12:00:00Z"))
	if !far.Committed {
		t.Fatal("a truck carrying a committed load is committed however far out it is")
	}
	if far.DueToCommit {
		t.Fatal("a truck with no asks on it has nothing to prompt about")
	}
	near := EvaluateCommit("2026-09-01", RunAuto, 0, 2, policy, at("2026-08-31T12:00:00Z"))
	if near.Committed {
		t.Fatal("a truck carrying only asks is NOT committed, the clock does not commit anything")
	}
	if !near.DueToCommit {
		t.Fatal("and it is due: two asks, and the delivery day is tomorrow")
	}
	// NOBODY is the dealer for whom a commitment is final.
	hard := policy
	hard.ReopenPolicy = ReopenNobody
	if EvaluateCommit("2026-09-01", RunAuto, 1, 0, hard, at("2026-08-31T12:00:00Z")).Reopenable {
		t.Fatal("under NOBODY a committed load offers no reopen")
	}
	if !EvaluateCommit("2026-09-01", RunReopened, 0, 1, policy, at("2026-08-31T12:00:00Z")).Reopened {
		t.Fatal("a reopened run says so")
	}
}

// TestWorkingWindow_IsAFortnightAndNotThePlanningHorizon pins R5's other 48. Colton asked "t-minus
// 24, 28 or 48 hours?"; Ryan Bostock said "more than 48 hours"; Ryan Rood said "almost a couple
// weeks". The two numbers must not be the same number.
func TestWorkingWindow_IsAFortnightAndNotThePlanningHorizon(t *testing.T) {
	from, to := WorkingWindow("2026-09-01", DefaultWorkingViewDays)
	if from != "2026-09-01" || to != "2026-09-14" {
		t.Fatalf("the working view is fourteen days from the board's date; got %s..%s", from, to)
	}
	if _, short := WorkingWindow("2026-09-01", 3); short != "2026-09-03" {
		t.Fatalf("a dealer who wants three days gets three days; got %s", short)
	}
	// A zero or absent knob falls back to the fortnight, NEVER to the planning horizon.
	if _, def := WorkingWindow("2026-09-01", 0); def != "2026-09-14" {
		t.Fatalf("an unset working view is a fortnight, not eight weeks; got %s", def)
	}
	p := DefaultPolicy()
	if p.WorkingViewDays == p.CommitHorizonWeeks*7 {
		t.Fatal("the working view and the planning horizon must be two different numbers, that conflation IS the bug")
	}
}

// TestLock_RefusesTheMutationWhateverTheClientSends is the server-side proof the brief asks for.
// The gate is evaluated from the LOAD'S OWN STATE and the dealer's policy, values a request cannot
// influence, so no combination of body fields reaches the repository.
func TestLock_RefusesTheMutationWhateverTheClientSends(t *testing.T) {
	svc := lbService(lbCaller(identity.TierFrontline, "INSIDE_SALES"), "2026-08-31T12:00:00Z")
	committed := Stop{ID: "s-1", OrderNo: "#SO-TRT-9", BranchID: "b-1", ScheduledDate: "2026-09-01",
		Status: StopReady, CommitState: CommitCommitted}

	err := svc.assertLoadChangeable(context.Background(), committed)
	if err == nil {
		t.Fatal("a committed load must refuse the mutation from a caller who cannot reopen it")
	}
	if !strings.Contains(err.Error(), "#SO-TRT-9") {
		t.Fatalf("the refusal must name the load: %v", err)
	}
	if !strings.Contains(err.Error(), "reopen") {
		t.Fatalf("the refusal must name the way forward, reopening is the answer, not waiting: %v", err)
	}
	if !strings.Contains(err.Error(), "dispatch manager") {
		t.Fatalf("and who can do it: %v", err)
	}
	// THE CLOCK IS NOT IN THE SENTENCE ANY MORE, and that is the model correction. A refusal that
	// blamed a 48-hour window would be describing a mechanism that no longer exists.
	if strings.Contains(err.Error(), "48") || strings.Contains(strings.ToLower(err.Error()), "window") {
		t.Fatalf("0185 removed the clock from the gate, the refusal must not cite a window: %v", err)
	}

	// The client cannot lie its way out of it: commit_state is a STORED column read from the row, not
	// a body field, and every other stop field it could try changes nothing about the verdict.
	for _, tamper := range []Stop{
		{ID: "s-1", OrderNo: "#SO-TRT-9", BranchID: "b-1", ScheduledDate: "2026-09-01", Status: StopReady, CommitState: CommitCommitted, ReopenReason: "because I said so"},
		{ID: "s-1", OrderNo: "#SO-TRT-9", BranchID: "b-1", ScheduledDate: "2100-01-01", Status: StopReady, CommitState: CommitCommitted},
		{ID: "s-1", OrderNo: "#SO-TRT-9", BranchID: "b-1", ScheduledDate: "2026-09-01", Status: StopLoaded, CommitState: CommitCommitted},
	} {
		if svc.assertLoadChangeable(context.Background(), tamper) == nil {
			t.Fatalf("the gate must hold for %+v", tamper)
		}
	}

	// And a REQUESTED load is editable by anyone who can edit the board, a plan is not a promise.
	open := Stop{ID: "s-2", OrderNo: "#SO-TRT-10", BranchID: "b-1", ScheduledDate: "2026-09-01",
		Status: StopReady, CommitState: CommitRequested}
	if err := svc.assertLoadChangeable(context.Background(), open); err != nil {
		t.Fatalf("a requested load must stay editable: %v", err)
	}
	// THE 6:45AM CASE, PINNED. A load for TOMORROW that nobody has committed is the day being built,
	// and 0184's clock reading refused exactly this. It must not refuse it again.
	tomorrow := Stop{ID: "s-3", OrderNo: "#SO-TRT-11", BranchID: "b-1", ScheduledDate: "2026-09-01",
		Status: StopReady, CommitState: CommitRequested}
	yard := lbService(lbCaller(identity.TierFrontline, "YARD_TEAM"), "2026-08-31T06:45:00Z")
	if err := yard.assertLoadChangeable(context.Background(), tomorrow); err != nil {
		t.Fatalf("a yard hand building tomorrow's truck at 06:45 must not be stopped: %v", err)
	}
}

// TestLock_ADispatchManagerPassesAndItIsRECORDED is the other half of the client's sentence. "Only a
// dispatch manager can unlock it" says WHO may pass; it does not say they must press a button first.
// So a manager's change goes through, and leaves a row saying it did.
func TestLock_ADispatchManagerPassesAndItIsRecorded(t *testing.T) {
	rec := &lbRecorder{}
	svc := lbService(lbCaller(identity.TierManager, "LOGISTICS_MANAGER"), "2026-08-31T12:00:00Z").WithAudit(rec)
	committed := Stop{ID: "s-1", OrderNo: "#SO-TRT-9", BranchID: "b-1", ScheduledDate: "2026-09-01",
		Status: StopReady, CommitState: CommitCommitted}
	if err := svc.assertLoadChangeable(context.Background(), committed); err != nil {
		t.Fatalf("a dispatch manager must be able to change a committed load: %v", err)
	}
	if len(rec.actions) != 1 || rec.actions[0] != "DELIVERY_COMMITTED_LOAD_CHANGED" {
		t.Fatalf("the override must leave an audit row; got %v", rec.actions)
	}
	// A FRONTLINE caller is refused and records nothing, because nothing happened.
	rec.actions = nil
	front := lbService(lbCaller(identity.TierFrontline, "YARD_TEAM"), "2026-08-31T12:00:00Z").WithAudit(rec)
	if err := front.assertLoadChangeable(context.Background(), committed); err == nil {
		t.Fatal("a yard hand must still be refused a change to a committed load")
	}
	if len(rec.actions) != 0 {
		t.Fatalf("a refused change must record nothing; got %v", rec.actions)
	}
	// And the same yard hand may still BUILD: the requested load beside it is untouched by any of this.
	rec.actions = nil
	if err := front.assertLoadChangeable(context.Background(), Stop{ID: "s-9", BranchID: "b-1",
		ScheduledDate: "2026-09-01", Status: StopReady, CommitState: CommitRequested}); err != nil {
		t.Fatalf("building is free: %v", err)
	}
}

// lbRecorder is the narrowest audit.Recorder a test needs: it remembers what it was told.
type lbRecorder struct{ actions []string }

func (r *lbRecorder) Record(_ context.Context, a audit.SensitiveAccess) error {
	r.actions = append(r.actions, a.Action)
	return nil
}

func TestCanUnlockLoad_IsACapabilityCheckAndNotAButton(t *testing.T) {
	policy := DefaultPolicy() // MANAGER
	if err := canReopenLoad(lbCaller(identity.TierFrontline, "YARD_TEAM"), policy); err == nil {
		t.Fatal("a yard hand may build loads but must not reopen a committed one")
	}
	if err := canReopenLoad(lbCaller(identity.TierFrontline, "DRIVERS"), policy); err == nil {
		t.Fatal("a driver must not be able to reopen a load")
	}
	if err := canReopenLoad(lbCaller(identity.TierManager, "LOGISTICS_MANAGER"), policy); err != nil {
		t.Fatalf("a logistics manager IS a dispatch manager: %v", err)
	}
	if err := canReopenLoad(lbCaller(identity.TierLeadership, "GENERAL_MANAGER"), policy); err != nil {
		t.Fatalf("leadership must be able to reopen: %v", err)
	}
	// A SALES MANAGER is a manager and is still not dispatch. A tier test would have let them through,
	// which is exactly why the authority is a role allowlist.
	if err := canReopenLoad(lbCaller(identity.TierManager, "SALES_MANAGER"), policy); err == nil {
		t.Fatal("a sales manager is one of 'our people', dispatch owns the commitment, not sales")
	}
	hard := policy
	hard.ReopenPolicy = ReopenNobody
	err := canReopenLoad(lbCaller(identity.TierLeadership, "GENERAL_MANAGER"), hard)
	if err == nil {
		t.Fatal("under NOBODY nothing reopens, that is what the dealer chose")
	}
	// The refusals carry DIFFERENT sentences. Telling a general manager to ask a dispatch manager,
	// when the dealer has switched reopening off entirely, sends them to a person who cannot help.
	if !errors.Is(err, ErrReopenDisabled) {
		t.Fatalf("under NOBODY the refusal must be ErrReopenDisabled, got %v", err)
	}
	if errors.Is(canReopenLoad(lbCaller(identity.TierFrontline, "DRIVERS"), policy), ErrReopenDisabled) {
		t.Fatal("a frontline refusal is about the PERSON, not about the dealer's policy")
	}
	// Under ANYONE, anybody who could commit may reopen, and nobody else.
	open := policy
	open.ReopenPolicy = ReopenAnyone
	if err := canReopenLoad(lbCaller(identity.TierFrontline, "YARD_TEAM"), open); err != nil {
		t.Fatalf("under ANYONE a yard hand who can commit may reopen: %v", err)
	}
	if err := canReopenLoad(lbCaller(identity.TierFrontline, "INSIDE_SALES"), open); err == nil {
		t.Fatal("but a salesperson never can, under any policy")
	}
}

// TestCanCommitLoad_ASalespersonRequestsAndNeverCommits is item D of the ruling, at the capability.
// Ryan Rood: "our dispatchers definitely do not want our people trying to overpromise or overcommit
// to something that they know nothing about."
func TestCanCommitLoad_ASalespersonRequestsAndNeverCommits(t *testing.T) {
	for _, role := range []string{"INSIDE_SALES", "OUTSIDE_SALES"} {
		if err := canCommitLoad(lbCaller(identity.TierFrontline, role)); !errors.Is(err, ErrCommitNotPermitted) {
			t.Fatalf("%s must be refused the commit, got %v", role, err)
		}
	}
	if err := canCommitLoad(lbCaller(identity.TierManager, "SALES_MANAGER")); err == nil {
		t.Fatal("a sales manager is sales, dispatch owns the commitment")
	}
	// The people who actually build trucks pass, INCLUDING the frontline yard. A manager-tier gate
	// here would stop the 06:45 work this surface exists for.
	for _, c := range []identity.StaffContext{
		lbCaller(identity.TierFrontline, "YARD_TEAM"),
		lbCaller(identity.TierManager, "YARD_MANAGER"),
		lbCaller(identity.TierManager, "LOGISTICS_MANAGER"),
		lbCaller(identity.TierManager, "BRANCH_MANAGER"),
		lbCaller(identity.TierLeadership, "GENERAL_MANAGER"),
	} {
		if err := canCommitLoad(c); err != nil {
			t.Fatalf("%s builds loads and must be able to commit: %v", c.RoleCode, err)
		}
	}
	// A DRIVER executes a committed load; they do not decide what is on it.
	if err := canCommitLoad(lbCaller(identity.TierFrontline, "DRIVERS")); err == nil {
		t.Fatal("a driver must not commit a delivery date")
	}
	// An INACTIVE holder of a dispatch role is not a dispatcher.
	sacked := lbCaller(identity.TierManager, "LOGISTICS_MANAGER")
	sacked.Status = "SUSPENDED"
	if err := canCommitLoad(sacked); err == nil {
		t.Fatal("a suspended account holds no capability")
	}
	// The refusal names what the person CAN do, which is the walkthrough-error voice.
	msg := ErrCommitNotPermitted.Error()
	if !strings.Contains(msg, "request") || !strings.Contains(msg, "dispatch") {
		t.Fatalf("the refusal must say a salesperson may request and dispatch commits: %q", msg)
	}
}

// - requested vs committed -

func TestValidateDateState_OmittedIsAnASKAndNotAPROMISE(t *testing.T) {
	got, err := ValidateDateState("")
	if err != nil || got != DateRequested {
		t.Fatalf("an omitted state must normalise to REQUESTED (the safe direction); got %q %v", got, err)
	}
	if _, err := ValidateDateState("PROBABLY"); err == nil {
		t.Fatal("an unknown state must be refused in words, not stored")
	}
	for _, s := range []string{DateRequested, DateCommitted} {
		if got, err := ValidateDateState(s); err != nil || got != s {
			t.Fatalf("%s must survive validation; got %q %v", s, got, err)
		}
	}
}

func TestWithinHorizon(t *testing.T) {
	today, _ := time.Parse(time.RFC3339, "2026-08-27T06:45:00Z")
	if !WithinHorizon("2026-08-27", today, 8) {
		t.Fatal("today is inside the horizon")
	}
	if !WithinHorizon("2026-10-22", today, 8) {
		t.Fatal("eight weeks out is inside an eight-week horizon")
	}
	if WithinHorizon("2026-11-30", today, 8) {
		t.Fatal("three months out is beyond an eight-week horizon")
	}
	if WithinHorizon("2026-08-01", today, 8) {
		t.Fatal("a date in the past is not a plan")
	}
}

// - the freight split -

func TestEvenSplit_LosesNoCentAndInventsNone(t *testing.T) {
	cases := []struct {
		total int64
		n     int
		want  []int64
	}{
		{0, 3, []int64{0, 0, 0}},
		{300, 3, []int64{100, 100, 100}},
		{100, 3, []int64{34, 33, 33}}, // the remainder rides on the EARLIEST loads
		{2200000, 3, []int64{733334, 733333, 733333}},
		{7, 2, []int64{4, 3}},
		{5, 1, []int64{5}},
	}
	for _, c := range cases {
		got := EvenSplit(c.total, c.n)
		if len(got) != len(c.want) {
			t.Fatalf("EvenSplit(%d,%d) length %d, want %d", c.total, c.n, len(got), len(c.want))
		}
		var sum int64
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("EvenSplit(%d,%d)[%d] = %d, want %d", c.total, c.n, i, got[i], c.want[i])
			}
			sum += got[i]
		}
		if sum != c.total {
			t.Errorf("EvenSplit(%d,%d) sums to %d, a split that loses a cent is an invoice that loses a cent", c.total, c.n, sum)
		}
	}
	if EvenSplit(100, 0) != nil {
		t.Fatal("a split over zero loads is nothing, not a panic")
	}
}

func TestValidateOverride_TheSumIsTheWholePoint(t *testing.T) {
	// The client's case: $22,000 of armour stone, freight $450, over three trucks, hand-adjusted.
	if err := ValidateOverride([]int64{20000, 15000, 10000}, 45000); err != nil {
		t.Fatalf("a split that adds up must be accepted: %v", err)
	}
	err := ValidateOverride([]int64{20000, 15000, 9900}, 45000)
	if err == nil {
		t.Fatal("a split that is a dollar light must be refused")
	}
	if !strings.Contains(err.Error(), "$449.00") || !strings.Contains(err.Error(), "$450.00") {
		t.Fatalf("the refusal must name BOTH figures so the typo is visible: %v", err)
	}
	if ValidateOverride([]int64{-100, 45100}, 45000) == nil {
		t.Fatal("a negative share must be refused even though it sums correctly")
	}
	if ValidateOverride(nil, 45000) == nil {
		t.Fatal("an empty override is not an override")
	}
}

// - quick cards -

// TestQuickCards_CountTheSameRowsTheBoardDraws is the "true at a glance" proof. The counts come from
// the slice the board just rendered, so there is no window in which a card and the board disagree.
func TestQuickCards_CountTheSameRowsTheBoardDraws(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-08-31T06:45:00Z")
	day := []Stop{
		{ID: "1", Status: StopReady, CommitState: CommitCommitted},
		{ID: "2", Status: StopReady, RunID: "r-1", CommitState: CommitCommitted},
		{ID: "3", Status: StopLoaded, RunID: "r-1", CommitState: CommitCommitted},
		{ID: "4", Status: StopReady, CommitState: CommitRequested},
		{ID: "5", Status: StopCancelled},            // excluded
		{ID: "6", Status: StopFailed, RunID: "r-1"}, // excluded
		{ID: "7", Status: StopDelivered, RunID: "r-2", CommitState: CommitCommitted},
	}
	// The WORKING WINDOW's waiting work, which is exactly what the staging lane draws, so the
	// unassigned card and that lane cannot disagree. Two of these are for the day itself and one is
	// next week; the third is inside neither prompt window nor anybody's attention yet.
	waiting := []Stop{
		{ID: "1", Status: StopReady, CommitState: CommitCommitted, ScheduledDate: "2026-09-01"},
		{ID: "4", Status: StopReady, CommitState: CommitRequested, ScheduledDate: "2026-09-01"},
		{ID: "8", Status: StopReady, CommitState: CommitRequested, ScheduledDate: "2026-09-10"},
	}
	c := countQuickCards(day, waiting, DefaultPolicy(), now)
	if c.Deliveries != 5 {
		t.Errorf("deliveries = %d, want 5, cancelled and failed stops are not deliveries that are happening", c.Deliveries)
	}
	if c.Unassigned != 3 {
		t.Errorf("unassigned = %d, want 3, the card counts the WORKING WINDOW, which is what the lane shows", c.Unassigned)
	}
	if c.Ready != 3 {
		t.Errorf("ready = %d, want 3", c.Ready)
	}
	if c.Requested != 1 {
		t.Errorf("requested = %d, want 1", c.Requested)
	}
	if c.Committed != 4 {
		t.Errorf("committed = %d, want 4", c.Committed)
	}
	// The prompt: one ask for tomorrow is due; the ask ten days out is not, and neither is the load
	// somebody already committed.
	if c.Due != 1 {
		t.Errorf("due = %d, want 1, the 48 hours prompts, it does not gate", c.Due)
	}
	if c.Pickups != 0 {
		t.Errorf("pickups must be 0 until its own read supplies it, it is not derivable from delivery rows")
	}
}

// - procurement gaps -

func TestGapsFor_OnlyShortagesAndNeverAWallOfGreen(t *testing.T) {
	lines := []PickFeedLine{
		{ProductID: "p1", SKU: "FLAG-2X2", Description: "Flagstone", QtyOrdered: "40", UOM: "EA"},
		{ProductID: "p2", SKU: "ARMR-24", Description: "Armour stone", QtyOrdered: "12", UOM: "EA"},
		{ProductID: "p3", SKU: "SAND-HPB", Description: "HPB", QtyOrdered: "3.5", UOM: "TON"},
	}
	onHand := map[string]float64{"p1": 40, "p2": 4, "p3": 0}
	gaps := GapsFor("s-1", "#SO-TRT-1041", lines, onHand)
	if len(gaps) != 2 {
		t.Fatalf("only the two short lines are gaps; got %d (%+v)", len(gaps), gaps)
	}
	if gaps[0].SKU != "ARMR-24" || gaps[0].QtyShort != 8 {
		t.Fatalf("armour stone is 8 short; got %+v", gaps[0])
	}
	if gaps[1].SKU != "SAND-HPB" || gaps[1].QtyShort != 3.5 {
		t.Fatalf("a line with nothing on hand is short by the whole quantity; got %+v", gaps[1])
	}
	// A load with nothing short produces an EMPTY SLICE, never nil, the surface iterates it.
	clear := GapsFor("s-2", "#SO-TRT-1042", lines[:1], onHand)
	if clear == nil || len(clear) != 0 {
		t.Fatalf("a clear load must produce an empty slice, not nil; got %#v", clear)
	}
}

// - the policy validator -

func TestValidatePolicy_RefusesInWordsBeforeTheCheckCould500(t *testing.T) {
	if _, err := ValidatePolicy(Policy{CommitPromptHours: 500, ReopenPolicy: ReopenAnyone}); err == nil {
		t.Fatal("a 500-hour prompt window must be refused in words")
	}
	if _, err := ValidatePolicy(Policy{CommitPromptHours: 48, ReopenPolicy: "SOMETIMES"}); err == nil {
		t.Fatal("an unknown reopen policy must be refused in words")
	}
	if _, err := ValidatePolicy(Policy{CommitPromptHours: 48, ReopenPolicy: ReopenAnyone, LoadValueVisibility: "EVERYONE"}); err == nil {
		t.Fatal("an unknown visibility must be refused in words")
	}
	if _, err := ValidatePolicy(Policy{CommitPromptHours: 48, ReopenPolicy: ReopenAnyone, CommitHorizonWeeks: 99}); err == nil {
		t.Fatal("a 99-week horizon must be refused in words")
	}
	if _, err := ValidatePolicy(Policy{CommitPromptHours: 48, ReopenPolicy: ReopenAnyone, WorkingViewDays: 400}); err == nil {
		t.Fatal("a 400-day working view must be refused in words, it is a fortnight's view, not a year's")
	}
	// The empty policy normalises to the safe defaults rather than to zeros.
	p, err := ValidatePolicy(Policy{})
	if err != nil {
		t.Fatalf("an empty policy must normalise, not fail: %v", err)
	}
	if p.CommitPromptHours != 48 || p.ReopenPolicy != ReopenManager ||
		p.LoadValueVisibility != settings.VisibilityManagerUp || p.CommitHorizonWeeks != 8 ||
		p.WorkingViewDays != 14 || p.FreightSplitMode != FreightSplitEven {
		t.Fatalf("normalised policy is not the safe default: %+v", p)
	}
	// A DELIBERATE zero prompt window is a real setting, a dealer who wants no nagging, and must
	// survive. It switches the PROMPT off and changes nothing about who may commit or reopen.
	z, err := ValidatePolicy(Policy{CommitPromptHours: 0, ReopenPolicy: ReopenAnyone, LoadValueVisibility: settings.VisibilityAllStaff})
	if err != nil || z.CommitPromptHours != 0 {
		t.Fatalf("a deliberate zero prompt window must be storable; got %+v %v", z, err)
	}
	if z.ReopenPolicy != ReopenAnyone {
		t.Fatalf("switching the prompt off must not change the reopen authority: %+v", z)
	}
}

// - the state machine, end to end at the service -

// TestCommitStop_RefusesAForgedCommitFromASalesperson is the server-side proof the brief names: the
// salesperson's UI draws no Commit control, and that is not what stops them.
func TestCommitStop_RefusesAForgedCommitFromASalesperson(t *testing.T) {
	svc := lbService(lbCaller(identity.TierFrontline, "INSIDE_SALES"), "2026-08-31T12:00:00Z")
	_, err := svc.CommitStop(context.Background(), "s-1", "2026-09-02")
	if !errors.Is(err, ErrCommitNotPermitted) {
		t.Fatalf("a salesperson posting straight to /commit must be refused by CAPABILITY, got %v", err)
	}
	// The refusal happens BEFORE the stop is even resolved, there is no ordering in which a
	// salesperson's request touches a load. (The service has no repository here; a capability check
	// that ran after the read would have failed with a wiring error instead.)
	if strings.Contains(err.Error(), "repository") {
		t.Fatalf("the capability must be checked at the door, before anything is read: %v", err)
	}
}

// TestReopenStop_IsAManagerCapabilityAndNotARoute proves the reopen route's authority lives in the
// service, where a route-registration refactor cannot lose it.
func TestReopenStop_IsAManagerCapabilityAndNotARoute(t *testing.T) {
	for _, role := range []string{"INSIDE_SALES", "DRIVERS", "YARD_TEAM"} {
		svc := lbService(lbCaller(identity.TierFrontline, role), "2026-08-31T12:00:00Z")
		if _, err := svc.ReopenStop(context.Background(), "s-1", "a better fit came in"); err == nil {
			t.Fatalf("%s must not be able to reopen a committed load", role)
		}
	}
	if _, err := lbService(lbCaller(identity.TierManager, "SALES_MANAGER"), "2026-08-31T12:00:00Z").
		ReopenStop(context.Background(), "s-1", "a better fit came in"); err == nil {
		t.Fatal("a sales manager is not a dispatch manager")
	}
}

func TestReopenReason_IsRequiredAndBounded(t *testing.T) {
	if _, err := reopenReason("   "); err == nil {
		t.Fatal("a reopen with no reason is an audit row that explains nothing")
	}
	if _, err := reopenReason(strings.Repeat("x", 401)); err == nil {
		t.Fatal("a 401-character reason must be refused in words")
	}
	why, err := reopenReason("  a better fit came in  ")
	if err != nil || why != "a better fit came in" {
		t.Fatalf("a real reason survives, trimmed; got %q %v", why, err)
	}
	// THE SENTENCE DOES NOT APOLOGISE. Reopening is a normal Thursday, so the prompt asks what
	// happened rather than warning the person off.
	_, err = reopenReason("")
	for _, banned := range []string{"cannot", "not allowed", "warning", "sure"} {
		if strings.Contains(strings.ToLower(err.Error()), banned) {
			t.Fatalf("the reopen prompt must read as ordinary, not as a refusal: %q", err.Error())
		}
	}
}

// - item 5a (session A), the gaps read names the field it is missing -

// TestBoardGaps_RefusesAMissingBranchByNameLikeItsSiblings pins the one guard that was absent.
//
// THE DEFECT, AS IT REACHED A BROWSER. GET /api/v1/dispatch/loadboard/gaps with no branch_id
// answered 500 {"error":"internal error"} while its two siblings on the same board answered
// 400 {"error":"dispatch: branch_id is required"}. The empty string went all the way to
// StopsForGapScan and failed inside the driver, so the omission was reported as OUR fault and the
// caller was told nothing they could act on.
//
// The test asserts the STATUS and the NAMED FIELD together, because either alone is passable and
// wrong: a 400 that says "bad request" is no better than the 500 at telling a dispatcher which
// parameter they forgot.
func TestBoardGaps_RefusesAMissingBranchByNameLikeItsSiblings(t *testing.T) {
	svc := lbService(lbCaller(identity.TierManager, "LOGISTICS_MANAGER"), "2026-08-31T12:00:00Z")

	_, _, err := svc.BoardGaps(context.Background(), "", "2026-08-31")
	if err == nil {
		t.Fatal("a gaps read with no branch must be refused, not attempted")
	}
	var bad httpx.BadRequestError
	if !errors.As(err, &bad) {
		t.Fatalf("the refusal must be the shared 400 type every handler maps, not an opaque 500: %#v", err)
	}
	if !strings.Contains(err.Error(), "branch_id") {
		t.Fatalf("the refusal must name the missing field: %v", err)
	}

	// THE SIBLINGS ARE THE SPEC. Whatever words this read uses, they are the words the board's other
	// two reads already use for the same omission, one vocabulary per surface, so a caller who
	// learns the message once has learned it everywhere.
	_, lerr := svc.LoadBoard(context.Background(), "", "2026-08-31")
	_, cerr := svc.Calendar(context.Background(), "", "2026-08-31", "2026-09-01")
	if lerr == nil || cerr == nil {
		t.Fatal("the sibling reads must still refuse a missing branch, this test compares against them")
	}
	if err.Error() != lerr.Error() || err.Error() != cerr.Error() {
		t.Fatalf("the three reads must refuse a missing branch identically:\n gaps:     %v\n loadboard: %v\n calendar:  %v",
			err, lerr, cerr)
	}

	// And the guard is a guard, not a wall: a real branch still reaches the scan (which, with no
	// stock or line reader wired on this bare service, honestly reports "not checked").
	gaps, checked, gerr := svc.BoardGaps(context.Background(), "b-1", "2026-08-31")
	if gerr != nil {
		t.Fatalf("a named branch must still be read: %v", gerr)
	}
	if checked {
		t.Fatal("with no stock or line reader wired the scan must report itself unchecked")
	}
	if len(gaps) != 0 {
		t.Fatalf("an unchecked scan reports no gaps, not invented ones: %v", gaps)
	}
}

// - item 1c (session A), the bulk load refuses in words, per load -

// TestMarkLoaded_RefusesInWordsBeforeItTouchesAnything pins the guards a bulk gesture must keep.
//
// THE RISK A BULK ROUTE INTRODUCES is that a list becomes a way around a rule that holds for one. So
// the refusals are asserted here on a bare service, no repository, no caller, which is exactly the
// state in which a careless implementation would wave everything through.
func TestMarkLoaded_RefusesInWordsBeforeItTouchesAnything(t *testing.T) {
	svc := lbService(lbCaller(identity.TierManager, "LOGISTICS_MANAGER"), "2026-08-31T12:00:00Z")

	// AN EMPTY SELECTION IS A MISTAKE, NOT A NO-OP. Answering 200 with an empty list would tell a
	// yard lead their gesture worked when they had selected nothing.
	if _, err := svc.MarkLoaded(context.Background(), nil); err == nil {
		t.Fatal("marking nothing loaded must be refused")
	} else if !strings.Contains(err.Error(), "choose the loads") {
		t.Fatalf("the refusal must name the way forward: %v", err)
	}

	// AND A SELECTION NOBODY COULD HAVE MADE IS REFUSED BY SIZE, in words, naming the alternative.
	huge := make([]string, maxBulkLoads+1)
	for i := range huge {
		huge[i] = "s-" + strconv.Itoa(i)
	}
	_, err := svc.MarkLoaded(context.Background(), huge)
	if err == nil {
		t.Fatal("a selection past the cap must be refused rather than held open for a minute")
	}
	var bad httpx.BadRequestError
	if !errors.As(err, &bad) {
		t.Fatalf("the refusal must be a 400, not a 500: %#v", err)
	}
	if !strings.Contains(err.Error(), "a truck at a time") {
		t.Fatalf("the refusal must name what to do instead: %v", err)
	}
}

// TestStopStateWord_NeverPrintsAColumnValue is the counter-vocabulary rule at the one place this
// item added refusals. A bulk load's refusal is read by a yard lead on a tablet, and "this load is
// EN_ROUTE" is a row's status where a sentence belongs.
func TestStopStateWord_NeverPrintsAColumnValue(t *testing.T) {
	for enum, want := range map[string]string{
		StopReady:     "ready to load",
		StopLoaded:    "already on the truck",
		StopEnRoute:   "already on the road",
		StopDelivered: "already delivered",
		StopFailed:    "recorded as not delivered",
		StopCancelled: "cancelled",
	} {
		got := stopStateWord(enum)
		if got != want {
			t.Errorf("stopStateWord(%q) = %q, want %q", enum, got, want)
		}
		if strings.Contains(got, enum) && enum != "CANCELLED" {
			t.Errorf("the enum itself survived into the words: %q", got)
		}
	}
	// An unmapped status degrades to an honest odd phrase rather than a confident wrong one, the
	// same default arm as pickStateWord, and the reason a new status shows up as strange instead of
	// silently mistranslated.
	if got := stopStateWord("HELD_AT_GATE"); got != "held at gate" {
		t.Fatalf("an unmapped status must degrade honestly: %q", got)
	}
}

// - item 3d (session A), the policy audit says what CHANGED -

// TestPolicyChanges_OnlyRealChangesAndBothSidesOfEach pins the diff behind the settings screen's
// change history.
//
// WHAT THE AUDIT ROW USED TO SAY. SavePolicy wrote the NEW values into one row and nothing else, so
// the trail recorded what the policy BECAME and never what it had been. "commit_prompt_hours is 24"
// is not a change; it is a reading. Worse, the four keys it used were not on the audit allowlist at
// all, so they were dropped before persistence and the row landed with changes = {}, a history of
// timestamps with no content.
func TestPolicyChanges_OnlyRealChangesAndBothSidesOfEach(t *testing.T) {
	before := Policy{
		CommitPromptHours: 48, ReopenPolicy: ReopenManager, LoadValueVisibility: "MANAGER_UP",
		CommitHorizonWeeks: 8, WorkingViewDays: 14, FreightSplitMode: FreightSplitEven,
	}

	// A SAVE THAT MOVED NOTHING WRITES NOTHING. A history that logged every save whether or not
	// anything changed would be a list of times somebody pressed a button, which is not what "who
	// changed this, and from what" asks.
	if got := policyChanges(before, before); len(got) != 0 {
		t.Fatalf("an unchanged save must record no change: %+v", got)
	}

	after := before
	after.WorkingViewDays = 21
	after.ReopenPolicy = ReopenNobody
	got := policyChanges(before, after)
	if len(got) != 2 {
		t.Fatalf("two knobs moved, so two rows: %+v", got)
	}
	byField := map[string]PolicyChange{}
	for _, c := range got {
		byField[c.Field] = c
	}
	if c := byField["working_view_days"]; c.Before != "14" || c.After != "21" {
		t.Fatalf("both sides of the change must be recorded: %+v", c)
	}
	if c := byField["reopen_policy"]; c.Before != ReopenManager || c.After != ReopenNobody {
		t.Fatalf("an enum knob records its words: %+v", c)
	}
	if _, unchanged := byField["commit_prompt_hours"]; unchanged {
		t.Fatal("a knob that did not move must not appear in the history")
	}
}

// TestPolicyHistory_UnwiredIsAnEmptyHistoryAndNotAFault. A deployment that has not attached the audit
// read still gets a working settings screen; the disclosure says nothing has been recorded, which is
// what it can honestly say. An error here would take the whole screen down for a trail nobody needs
// in order to change a rule.
func TestPolicyHistory_UnwiredIsAnEmptyHistoryAndNotAFault(t *testing.T) {
	svc := lbService(lbCaller(identity.TierManager, "LOGISTICS_MANAGER"), "2026-08-31T12:00:00Z")
	got, err := svc.PolicyHistory(context.Background(), "5d1b3f6e-0000-4000-9000-000000000001", 0)
	if err != nil {
		t.Fatalf("an unwired trail must not be an error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("and it must be empty rather than invented: %+v", got)
	}

	// The branch guard still holds: a history read is a read of one branch's rules.
	if _, err := svc.PolicyHistory(context.Background(), "", 0); err == nil {
		t.Fatal("a history read with no branch must be refused, like every other read on this board")
	}
}

// TestPolicyHistory_IsManagerGatedLikeTheSaveItReports. Somebody who may change the rules may see who
// changed them; somebody who may not, may not, a change history is a list of who did what, and that
// is not frontline information.
func TestPolicyHistory_IsManagerGatedLikeTheSaveItReports(t *testing.T) {
	front := lbService(lbCaller(identity.TierFrontline, "INSIDE_SALES"), "2026-08-31T12:00:00Z")
	if _, err := front.PolicyHistory(context.Background(), "5d1b3f6e-0000-4000-9000-000000000001", 0); !errors.Is(err, ErrForbidden) {
		t.Fatalf("a frontline caller must be refused the change history: %v", err)
	}
}

// TestActorWord_NeverPrintsARoleCode, the counter-vocabulary rule, at the change history.
// LOGISTICS_MANAGER is a row in our roles table; "a dispatch manager" is what somebody reading a
// change history would actually say.
func TestActorWord_NeverPrintsARoleCode(t *testing.T) {
	for code, want := range map[string]string{
		"SUPER_ADMIN":       "an administrator",
		"TECH_ADMIN":        "an administrator",
		"GENERAL_MANAGER":   "the general manager",
		"LOGISTICS_MANAGER": "a dispatch manager",
		"BRANCH_MANAGER":    "a branch manager",
	} {
		if got := actorWord(code); got != want {
			t.Errorf("actorWord(%q) = %q, want %q", code, got, want)
		}
	}
	// An unmapped role degrades to an honest odd phrase rather than a confident wrong title.
	if got := actorWord("YARD_MANAGER"); got != "a yard manager" {
		t.Fatalf("an unmapped role must degrade honestly: %q", got)
	}
	// AN ABSENT ROLE IS "somebody", never blank: a history row with no who is a row that looks broken,
	// and a reader cannot tell a missing name from a missing feature.
	if got := actorWord(""); got != "somebody" {
		t.Fatalf("a missing role must still read as a person: %q", got)
	}
}
