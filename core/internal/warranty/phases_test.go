package warranty

import (
	"testing"
	"time"
)

// phases_test.go, PURE FR-3 lifecycle + FR-7 reporting math (no DB). These lock the confirmed phase machine,
// the checklist gate, win/loss classification, and every reporting aggregation.

func TestKanbanColumns_ConfirmedSequence(t *testing.T) {
	// The CLIENT-CONFIRMED order (2026-07-07), verbatim, then the two exits.
	want := []string{
		"INTAKE", "TRIAGE", "SUBMITTED", "WAITING_ON_DECISION", "DECISION", "SETTLEMENT", "CLOSED",
		"REJECTED", "ABANDONED",
	}
	got := KanbanColumns()
	if len(got) != len(want) {
		t.Fatalf("column count: got %d want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("column %d: got %q want %q", i, got[i], want[i])
		}
	}
}

func TestCanTransition_ForwardPath(t *testing.T) {
	forward := [][2]string{
		{PhaseIntake, PhaseTriage},
		{PhaseTriage, PhaseSubmitted},
		{PhaseSubmitted, PhaseWaitingOnDecision},
		{PhaseWaitingOnDecision, PhaseDecision},
		{PhaseDecision, PhaseSettlement},
		{PhaseSettlement, PhaseClosed},
	}
	for _, tc := range forward {
		if !CanTransition(tc[0], tc[1]) {
			t.Errorf("expected legal forward transition %s → %s", tc[0], tc[1])
		}
	}
}

func TestCanTransition_IllegalSkips(t *testing.T) {
	illegal := [][2]string{
		{PhaseIntake, PhaseSubmitted},    // can't skip triage
		{PhaseIntake, PhaseClosed},       // can't skip straight to closed
		{PhaseTriage, PhaseDecision},     // can't skip submitted+waiting
		{PhaseClosed, PhaseTriage},       // terminal has no outbound
		{PhaseRejected, PhaseIntake},     // terminal has no outbound
		{PhaseSettlement, PhaseRejected}, // settlement only closes/abandons
	}
	for _, tc := range illegal {
		if CanTransition(tc[0], tc[1]) {
			t.Errorf("expected ILLEGAL transition %s → %s to be rejected", tc[0], tc[1])
		}
	}
}

func TestCanTransition_ExitsFromWorkingPhases(t *testing.T) {
	// Every working (non-terminal, non-intake-special) phase can be abandoned; most can be rejected.
	if !CanTransition(PhaseIntake, PhaseAbandoned) {
		t.Error("intake should be abandonable")
	}
	for _, p := range []string{PhaseTriage, PhaseSubmitted, PhaseWaitingOnDecision, PhaseDecision} {
		if !CanTransition(p, PhaseRejected) {
			t.Errorf("%s should be rejectable", p)
		}
		if !CanTransition(p, PhaseAbandoned) {
			t.Errorf("%s should be abandonable", p)
		}
	}
}

func TestIsTerminal(t *testing.T) {
	for _, p := range []string{PhaseClosed, PhaseRejected, PhaseAbandoned} {
		if !IsTerminal(p) {
			t.Errorf("%s should be terminal", p)
		}
	}
	for _, p := range []string{PhaseIntake, PhaseTriage, PhaseSettlement} {
		if IsTerminal(p) {
			t.Errorf("%s should NOT be terminal", p)
		}
	}
}

func TestStampsSLA_OnlySubmitted(t *testing.T) {
	if !StampsSLA(PhaseSubmitted) {
		t.Error("entering Submitted must stamp the SLA (FR-3 automation)")
	}
	for _, p := range []string{PhaseTriage, PhaseWaitingOnDecision, PhaseClosed} {
		if StampsSLA(p) {
			t.Errorf("%s should not stamp SLA", p)
		}
	}
}

// - checklist gate -

func submitItems() []ChecklistItem {
	return []ChecklistItem{
		{Phase: PhaseSubmitted, ItemKey: "photos_attached", Label: "Photos attached", Required: true},
		{Phase: PhaseSubmitted, ItemKey: "proof_attached", Label: "Proof of purchase attached", Required: true},
		{Phase: PhaseSubmitted, ItemKey: "vendor_form_sent", Label: "Vendor form sent", Required: true},
		{Phase: PhaseSubmitted, ItemKey: "optional_note", Label: "Optional note", Required: false},
	}
}

func TestGateAdvance_BlocksWithUncheckedRequired(t *testing.T) {
	ticked := map[string]bool{"photos_attached": true} // only 1 of 3 required
	g := GateAdvance(PhaseSubmitted, PhaseWaitingOnDecision, submitItems(), ticked)
	if g.OK {
		t.Fatal("gate should BLOCK when required items are unchecked")
	}
	if len(g.Missing) != 2 {
		t.Errorf("expected 2 missing required items, got %d (%v)", len(g.Missing), g.Missing)
	}
}

func TestGateAdvance_PassesWhenAllRequiredChecked(t *testing.T) {
	ticked := map[string]bool{"photos_attached": true, "proof_attached": true, "vendor_form_sent": true}
	g := GateAdvance(PhaseSubmitted, PhaseWaitingOnDecision, submitItems(), ticked)
	if !g.OK {
		t.Fatalf("gate should PASS when all required checked; missing=%v", g.Missing)
	}
}

func TestGateAdvance_OptionalDoesNotBlock(t *testing.T) {
	// all required ticked, optional NOT ticked → still passes.
	ticked := map[string]bool{"photos_attached": true, "proof_attached": true, "vendor_form_sent": true}
	g := GateAdvance(PhaseSubmitted, PhaseWaitingOnDecision, submitItems(), ticked)
	if !g.OK {
		t.Error("an unchecked OPTIONAL item must not block advancement")
	}
}

func TestGateAdvance_ExitsAreNeverGated(t *testing.T) {
	// no items ticked, but rejecting/abandoning is always allowed.
	for _, to := range []string{PhaseRejected, PhaseAbandoned} {
		g := GateAdvance(PhaseSubmitted, to, submitItems(), map[string]bool{})
		if !g.OK {
			t.Errorf("exit transition to %s must never be gated by the checklist", to)
		}
	}
}

func TestGateAdvance_OnlyGatesFromPhaseItems(t *testing.T) {
	// An item belonging to a DIFFERENT phase must be ignored by the gate for the current phase.
	items := []ChecklistItem{
		{Phase: PhaseTriage, ItemKey: "owner_assigned", Label: "Owner", Required: true}, // wrong phase
		{Phase: PhaseSubmitted, ItemKey: "photos_attached", Label: "Photos", Required: true},
	}
	ticked := map[string]bool{"photos_attached": true} // the triage item is unticked but irrelevant here
	g := GateAdvance(PhaseSubmitted, PhaseWaitingOnDecision, items, ticked)
	if !g.OK {
		t.Errorf("gate should ignore other-phase items; missing=%v", g.Missing)
	}
}

// - win/loss classification -

func TestClassify(t *testing.T) {
	cases := []struct {
		phase, outcome string
		want           ClaimDisposition
	}{
		{PhaseClosed, OutcomeVendorCredit, DispositionWon},
		{PhaseClosed, OutcomeReplacementStock, DispositionWon},
		{PhaseClosed, OutcomeCustomerAccommodation, DispositionWon},
		{PhaseClosed, OutcomeWriteOff, DispositionLost},
		{PhaseClosed, "", DispositionLost}, // closed with no recovery = lost
		{PhaseRejected, "", DispositionLost},
		{PhaseAbandoned, "", DispositionLost},
		{PhaseIntake, "", DispositionOpen},
		{PhaseWaitingOnDecision, "", DispositionOpen},
		{PhaseSettlement, OutcomeVendorCredit, DispositionOpen}, // not yet closed
	}
	for _, c := range cases {
		if got := classify(c.phase, c.outcome); got != c.want {
			t.Errorf("classify(%s,%s)=%v want %v", c.phase, c.outcome, got, c.want)
		}
	}
}

func TestAgeBucket(t *testing.T) {
	cases := []struct {
		days int
		want string
	}{{0, "0-30"}, {30, "0-30"}, {31, "31-60"}, {60, "31-60"}, {61, "61-90"}, {90, "61-90"}, {91, "90+"}, {365, "90+"}}
	for _, c := range cases {
		if got := ageBucket(c.days); got != c.want {
			t.Errorf("ageBucket(%d)=%s want %s", c.days, got, c.want)
		}
	}
}

// - reporting math -

func day(daysAgo int, now time.Time) time.Time { return now.AddDate(0, 0, -daysAgo) }

// reportFixture builds a representative claim set for the FR-7 math.
func reportFixture(now time.Time) []ReportClaim {
	closed30 := day(10, now)
	closed20 := day(5, now)
	return []ReportClaim{
		// Unilock: 1 open (in triage), exposure 1450.
		{ID: "1", ClaimNo: "WTY-1", VendorID: "v-ul", VendorName: "Unilock", Phase: PhaseTriage, ExpectedCents: 145000, OpenedAt: day(20, now)},
		// Techo: 1 open (waiting), exposure 890.
		{ID: "2", ClaimNo: "WTY-2", VendorID: "v-tb", VendorName: "Techo", Phase: PhaseWaitingOnDecision, ExpectedCents: 89000, OpenedAt: day(45, now)},
		// Permacon: WON, closed after 30d, realized 1980.
		{ID: "3", ClaimNo: "WTY-3", VendorID: "v-pm", VendorName: "Permacon", Phase: PhaseClosed, Outcome: OutcomeVendorCredit, ExpectedCents: 210000, RealizedCents: 198000, OpenedAt: day(40, now), ClosedAt: &closed30},
		// Permacon: LOST (rejected), closed after 20d, makes Permacon 2 total, 1 win 1 loss.
		{ID: "4", ClaimNo: "WTY-4", VendorID: "v-pm", VendorName: "Permacon", Phase: PhaseRejected, ExpectedCents: 62000, OpenedAt: day(25, now), ClosedAt: &closed20},
		// UNKNOWN vendor: 1 open (intake), exposure 0.
		{ID: "5", ClaimNo: "WTY-5", VendorID: "", VendorName: "", Phase: PhaseIntake, ExpectedCents: 0, OpenedAt: day(3, now)},
	}
}

func TestBuildFunnel_CountsPerPhaseAllColumns(t *testing.T) {
	now := time.Now()
	f := buildFunnel(reportFixture(now))
	if len(f) != len(KanbanColumns()) {
		t.Fatalf("funnel should have every column; got %d want %d", len(f), len(KanbanColumns()))
	}
	byPhase := map[string]int{}
	for _, fc := range f {
		byPhase[fc.Phase] = fc.Count
	}
	if byPhase[PhaseTriage] != 1 || byPhase[PhaseWaitingOnDecision] != 1 || byPhase[PhaseClosed] != 1 ||
		byPhase[PhaseRejected] != 1 || byPhase[PhaseIntake] != 1 {
		t.Errorf("funnel counts wrong: %v", byPhase)
	}
	if byPhase[PhaseSubmitted] != 0 {
		t.Errorf("empty phase should be 0, got %d", byPhase[PhaseSubmitted])
	}
}

func TestBuildByManufacturer_WinLoss(t *testing.T) {
	now := time.Now()
	rows := buildByManufacturer(reportFixture(now))
	byV := map[string]ManufacturerFrequency{}
	for _, r := range rows {
		byV[r.VendorID] = r
	}
	pm := byV["v-pm"]
	if pm.TotalClaims != 2 || pm.WonClaims != 1 || pm.LostClaims != 1 {
		t.Errorf("Permacon: total=%d won=%d lost=%d want 2/1/1", pm.TotalClaims, pm.WonClaims, pm.LostClaims)
	}
	if pm.WinRate != 0.5 {
		t.Errorf("Permacon win rate: got %v want 0.5", pm.WinRate)
	}
	// Unclosed vendors have 0 decided → win rate 0.
	if byV["v-ul"].WinRate != 0 {
		t.Errorf("Unilock (all open) win rate should be 0, got %v", byV["v-ul"].WinRate)
	}
	// UNKNOWN vendor is bucketed and counted.
	if byV["UNKNOWN"].TotalClaims != 1 {
		t.Errorf("UNKNOWN vendor claim should be counted, got %d", byV["UNKNOWN"].TotalClaims)
	}
}

// TestBuildByManufacturer_RealizedOnlyOnWins is the FR-7 MONEY-CRITICAL regression (L8 round 5): realized
// recovery in the by-manufacturer report must be CONSISTENT with classify(), a claim in a terminal-LOST phase
// (ABANDONED/REJECTED) that still carries a settlement outcome + realized amount is NOT a win, so its realized
// dollars must not be attributed to the vendor. Mirrors the live corruption (recovery on vendors with ~zero
// wins). Without the fix (unconditional a.realized += c.RealizedCents) this fails.
func TestBuildByManufacturer_RealizedOnlyOnWins(t *testing.T) {
	now := time.Now()
	closed := day(5, now)
	rows := buildByManufacturer([]ReportClaim{
		// Techo: a genuine WIN, realized recovery counts.
		{ID: "1", ClaimNo: "WTY-1", VendorID: "v-tb", VendorName: "Techo", Phase: PhaseClosed, Outcome: OutcomeVendorCredit, ExpectedCents: 100000, RealizedCents: 90000, OpenedAt: day(20, now), ClosedAt: &closed},
		// Techo: ABANDONED (terminal-LOST) but still carrying a settlement outcome + realized, the corrupt
		// state. classify() → LOST, so its realized MUST NOT inflate Techo's realized total.
		{ID: "2", ClaimNo: "WTY-2", VendorID: "v-tb", VendorName: "Techo", Phase: PhaseAbandoned, Outcome: OutcomeVendorCredit, ExpectedCents: 80000, RealizedCents: 87700, OpenedAt: day(30, now)},
		// Techo: REJECTED with realized, also LOST, also excluded.
		{ID: "3", ClaimNo: "WTY-3", VendorID: "v-tb", VendorName: "Techo", Phase: PhaseRejected, Outcome: OutcomeVendorCredit, ExpectedCents: 50000, RealizedCents: 50000, OpenedAt: day(15, now)},
	})
	var tb ManufacturerFrequency
	for _, r := range rows {
		if r.VendorID == "v-tb" {
			tb = r
		}
	}
	if tb.WonClaims != 1 || tb.LostClaims != 2 {
		t.Fatalf("Techo win/loss: won=%d lost=%d want 1/2", tb.WonClaims, tb.LostClaims)
	}
	// Only the WON claim's realized (90000) may count, NOT the abandoned/rejected 87700+50000.
	if tb.RealizedCents != 90000 {
		t.Errorf("Techo realized: got %d want 90000 (realized on LOST claims must be excluded)", tb.RealizedCents)
	}
}

func TestBuildTimeToSettlement_OnlyWins(t *testing.T) {
	now := time.Now()
	t2s := buildTimeToSettlement(reportFixture(now))
	// Only WTY-3 is a WIN (WTY-4 is a rejected loss, excluded). 40d open → closed 10d ago = 30 days.
	if t2s.SettledCount != 1 {
		t.Fatalf("settled count: got %d want 1 (only wins count)", t2s.SettledCount)
	}
	if t2s.MinDays != 30 || t2s.MaxDays != 30 || t2s.AvgDays != 30 || t2s.MedianDays != 30 {
		t.Errorf("time-to-settlement wrong: %+v (want 30 across the board)", t2s)
	}
}

func TestBuildExposure_OpenOnlyByVendor(t *testing.T) {
	now := time.Now()
	ex := buildExposure(reportFixture(now))
	byV := map[string]int64{}
	for _, e := range ex {
		byV[e.VendorID] = e.ExposureCents
	}
	if byV["v-ul"] != 145000 {
		t.Errorf("Unilock exposure: got %d want 145000", byV["v-ul"])
	}
	if byV["v-tb"] != 89000 {
		t.Errorf("Techo exposure: got %d want 89000", byV["v-tb"])
	}
	// Permacon has no OPEN claims → not in exposure.
	if _, ok := byV["v-pm"]; ok {
		t.Error("Permacon (no open claims) should not appear in exposure")
	}
	// Biggest exposure sorts first.
	if len(ex) > 0 && ex[0].VendorID != "v-ul" {
		t.Errorf("exposure should sort biggest-first; got %s", ex[0].VendorID)
	}
}

func TestBuildReport_Totals(t *testing.T) {
	now := time.Now()
	b := buildReport(reportFixture(now), now)
	// 3 open claims (triage, waiting, intake); exposure 145000+89000+0 = 234000.
	if b.TotalOpen != 3 {
		t.Errorf("total open: got %d want 3", b.TotalOpen)
	}
	if b.TotalExposureCents != 234000 {
		t.Errorf("total exposure: got %d want 234000", b.TotalExposureCents)
	}
	if len(b.OpenClaimsByAge) != 3 {
		t.Errorf("open-by-age rows: got %d want 3", len(b.OpenClaimsByAge))
	}
	// Oldest open (WTY-2, 45d) sorts first.
	if b.OpenClaimsByAge[0].ClaimNo != "WTY-2" {
		t.Errorf("oldest open should sort first; got %s", b.OpenClaimsByAge[0].ClaimNo)
	}
}

func TestMedian(t *testing.T) {
	if median([]int{5}) != 5 {
		t.Error("median of one")
	}
	if median([]int{2, 4}) != 3 {
		t.Error("median of two = avg")
	}
	if median([]int{1, 2, 3, 4, 5}) != 3 {
		t.Error("median odd")
	}
	if median([]int{1, 2, 3, 4}) != 2.5 {
		t.Error("median even")
	}
}
