package warranty

import (
	"testing"
)

// scope_test.go, regression guards for the WTY-01 L8 remediation:
//   * MAJOR IDOR: branch/tenant scoping on warranty reads + mutations (module.go:124 / handler).
//   * MAJOR settled-exit: a SETTLED claim cannot be diverted to ABANDONED/REJECTED (phases.go:153 / FR-7).
//   * MINOR settlement-phase: RecordSettlement is restricted to DECISION/SETTLEMENT (service.go:418).
// Each test FAILS without its fix.

// - MAJOR: IDOR, cross-branch access is forbidden; GLOBAL reaches all -

// A branch-scoped staffer who does NOT hold a claim's branch must not READ it by id (IDOR). Without the
// callerScopedClaim gate this returns the claim; with it, a 403.
func TestGetClaim_CrossBranchForbidden(t *testing.T) {
	svc, fs, _ := newSvc()
	// A claim owned by testBranch, planted directly.
	fs.claims["x"] = Claim{ID: "x", ClaimNo: "WTY-1", BranchID: testBranch, Phase: PhaseIntake, OpenedAt: "2026-06-01T00:00:00Z"}

	// A caller scoped ONLY to otherBranch tries to read it → forbidden (403), never the claim.
	_, err := svc.GetClaim(ctxWith(staffAt("intruder", otherBranch)), "x")
	if status(err) != 403 {
		t.Fatalf("a cross-branch staffer must be forbidden (403) from reading the claim; got status %d (err %v)", status(err), err)
	}
}

// A missing claim must report NOT-FOUND (404), never forbidden, no existence leak through the authz path.
func TestGetClaim_MissingIsNotFound(t *testing.T) {
	svc, _, _ := newSvc()
	_, err := svc.GetClaim(ctxWith(staffAt("u", otherBranch)), "does-not-exist")
	if status(err) != 404 {
		t.Fatalf("a missing claim must be 404 (no existence leak); got status %d (err %v)", status(err), err)
	}
}

// A GLOBAL (Tier-1) caller reaches a claim in ANY branch.
func TestGetClaim_GlobalReachesAllBranches(t *testing.T) {
	svc, fs, _ := newSvc()
	fs.claims["x"] = Claim{ID: "x", ClaimNo: "WTY-1", BranchID: testBranch, Phase: PhaseIntake, OpenedAt: "2026-06-01T00:00:00Z"}
	if _, err := svc.GetClaim(ctxWith(globalStaff("gm")), "x"); err != nil {
		t.Fatalf("a GLOBAL caller must reach any branch's claim; got %v", err)
	}
}

// A cross-branch staffer must not MUTATE a claim (advance/settle/tick/assign/evidence), the mutation paths
// all funnel through the same scope gate. Spot-check advance + settle (the money-critical one).
func TestMutations_CrossBranchForbidden(t *testing.T) {
	svc, fs, _ := newSvc()
	fs.claims["x"] = Claim{ID: "x", ClaimNo: "WTY-1", BranchID: testBranch, Phase: PhaseDecision, OpenedAt: "2026-06-01T00:00:00Z"}
	intruder := ctxWith(staffAt("intruder", otherBranch))

	if _, err := svc.AdvancePhase(intruder, "x", PhaseTransitionInput{ToPhase: PhaseSettlement}); status(err) != 403 {
		t.Fatalf("cross-branch advance must be forbidden; got status %d", status(err))
	}
	if _, err := svc.RecordSettlement(intruder, "x", SettlementInput{Outcome: OutcomeVendorCredit, RealizedCents: 1000}); status(err) != 403 {
		t.Fatalf("cross-branch settle must be forbidden; got status %d", status(err))
	}
	if _, err := svc.TickChecklist(intruder, "x", ChecklistTickInput{ItemKey: "decision_recorded", Checked: true}); status(err) != 403 {
		t.Fatalf("cross-branch tick must be forbidden; got status %d", status(err))
	}
	if _, err := svc.AssignClaim(intruder, "x", "v1", "u2"); status(err) != 403 {
		t.Fatalf("cross-branch assign must be forbidden; got status %d", status(err))
	}
	if _, err := svc.AttachEvidence(intruder, "x", EvidenceInput{EvidenceRef: "ref://masked"}); status(err) != 403 {
		t.Fatalf("cross-branch evidence must be forbidden; got status %d", status(err))
	}
	if _, err := svc.ClaimEvents(intruder, "x"); status(err) != 403 {
		t.Fatalf("cross-branch events read must be forbidden; got status %d", status(err))
	}
	// The claim never moved (mutation was blocked before any write).
	if got := fs.claims["x"]; got.Phase != PhaseDecision || got.Outcome != "" {
		t.Fatalf("a forbidden mutation must not have touched the claim; got phase=%s outcome=%s", got.Phase, got.Outcome)
	}
}

// A creation in a branch the caller cannot access is forbidden (the write choke-point at capture).
func TestCreateClaim_CrossBranchForbidden(t *testing.T) {
	svc, _, _ := newSvc()
	_, err := svc.CreateClaim(ctxWith(staffAt("u", otherBranch)), ClaimInput{BranchID: testBranch, Description: "cross-branch capture"})
	if status(err) != 403 {
		t.Fatalf("capturing a claim in an unreachable branch must be forbidden; got status %d (err %v)", status(err), err)
	}
}

// ListClaims is branch-scoped: a scoped caller sees ONLY their branch's claims; an explicit unreachable branch
// filter is forbidden; a GLOBAL caller sees every branch.
func TestListClaims_BranchScoped(t *testing.T) {
	svc, fs, _ := newSvc()
	fs.claims["a"] = Claim{ID: "a", ClaimNo: "WTY-1", BranchID: testBranch, Phase: PhaseIntake, OpenedAt: "2026-06-01T00:00:00Z"}
	fs.claims["b"] = Claim{ID: "b", ClaimNo: "WTY-2", BranchID: otherBranch, Phase: PhaseIntake, OpenedAt: "2026-06-01T00:00:00Z"}

	// Scoped to testBranch, no filter → sees ONLY the testBranch claim (not the otherBranch one).
	got, err := svc.ListClaims(ctxWith(staffAt("u", testBranch)), ClaimFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("a scoped caller must see only their branch's claims; got %d claims", len(got))
	}
	// An explicit filter for a branch they cannot access is forbidden.
	if _, err := svc.ListClaims(ctxWith(staffAt("u", testBranch)), ClaimFilter{BranchID: otherBranch}); status(err) != 403 {
		t.Fatalf("a filter on an unreachable branch must be forbidden; got status %d", status(err))
	}
	// GLOBAL sees both.
	all, err := svc.ListClaims(ctxWith(globalStaff("gm")), ClaimFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("GLOBAL must see every branch's claims; got %d", len(all))
	}
}

// Reports are branch-scoped: a scoped caller's FR-7 totals include ONLY their branch's money.
func TestReports_BranchScoped(t *testing.T) {
	svc, fs, _ := newSvc()
	fs.claims["a"] = Claim{ID: "a", ClaimNo: "WTY-1", BranchID: testBranch, VendorID: "v1", Phase: PhaseTriage, ExpectedCents: 100000, OpenedAt: "2026-06-01T00:00:00Z"}
	fs.claims["b"] = Claim{ID: "b", ClaimNo: "WTY-2", BranchID: otherBranch, VendorID: "v1", Phase: PhaseTriage, ExpectedCents: 999999, OpenedAt: "2026-06-01T00:00:00Z"}

	// A caller scoped to testBranch, asking for ALL branches (""), must still only see testBranch exposure.
	b, err := svc.Reports(ctxWith(staffAt("u", testBranch)), "")
	if err != nil {
		t.Fatal(err)
	}
	if b.TotalExposureCents != 100000 {
		t.Fatalf("a scoped caller's report must exclude other branches' exposure; got %d (leaked the other branch)", b.TotalExposureCents)
	}
	// GLOBAL sees the full exposure.
	gb, err := svc.Reports(ctxWith(globalStaff("gm")), "")
	if err != nil {
		t.Fatal(err)
	}
	if gb.TotalExposureCents != 1099999 {
		t.Fatalf("GLOBAL report must include every branch; got %d", gb.TotalExposureCents)
	}
}

// - MAJOR: a settled claim cannot exit to ABANDONED/REJECTED (FR-7 corruption guard) -

// A claim settled with a real recovery (VENDOR_CREDIT) is in SETTLEMENT with an outcome recorded. Diverting it
// to ABANDONED must be REFUSED, otherwise classify() would report it LOST (won=0) while its realized recovery
// still feeds exposure/realized reporting, corrupting FR-7. The coherent rule: a settled claim only CLOSES.
func TestAdvancePhase_SettledClaimCannotExitToAbandoned(t *testing.T) {
	svc, fs, _ := newSvc()
	ctx := ctxWith(staff("u1"))
	id := settleableClaim(t, svc, ctx, ClaimInput{BranchID: testBranch, ExpectedCents: 210000})
	if _, err := svc.RecordSettlement(ctx, id, SettlementInput{Outcome: OutcomeVendorCredit, RealizedCents: 198000}); err != nil {
		t.Fatalf("settle: %v", err)
	}
	// The claim was settled in-place at DECISION (settle ≠ advance). DECISION → ABANDONED is a legal PHASE
	// transition, but the settled-claim guard must block it.
	_, err := svc.AdvancePhase(ctx, id, PhaseTransitionInput{ToPhase: PhaseAbandoned})
	if status(err) != 422 {
		t.Fatalf("a settled claim must NOT be advanceable to ABANDONED (FR-7 corruption); got status %d (err %v)", status(err), err)
	}
	// It also cannot be REJECTED.
	if _, err := svc.AdvancePhase(ctx, id, PhaseTransitionInput{ToPhase: PhaseRejected}); status(err) != 422 {
		t.Fatalf("a settled claim must NOT be advanceable to REJECTED; got status %d", status(err))
	}
	// The claim is intact, carrying its recovery, and closing forward (via SETTLEMENT) IS allowed.
	got := fs.claims[id]
	if got.Phase != PhaseDecision || got.Outcome != OutcomeVendorCredit || got.RealizedCents != 198000 {
		t.Fatalf("the settled claim must be intact at DECISION; got phase=%s outcome=%s realized=%d", got.Phase, got.Outcome, got.RealizedCents)
	}
	for _, k := range []string{"decision_recorded", "terms_captured"} {
		if _, err := svc.TickChecklist(ctx, id, ChecklistTickInput{ItemKey: k, Checked: true}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc.AdvancePhase(ctx, id, PhaseTransitionInput{ToPhase: PhaseSettlement}); err != nil {
		t.Fatalf("a settled claim must still advance forward to SETTLEMENT; got %v", err)
	}
	if _, err := svc.AdvancePhase(ctx, id, PhaseTransitionInput{ToPhase: PhaseClosed}); err != nil {
		t.Fatalf("a settled claim must still be closeable forward; got %v", err)
	}
	// After close, the report classifies it as WON (its recovery is a win, never a loss).
	b, err := svc.Reports(ctx, testBranch)
	if err != nil {
		t.Fatal(err)
	}
	var won int
	for _, m := range b.ByManufacturer {
		won += m.WonClaims
	}
	if won != 1 {
		t.Fatalf("the settled+closed recovery must count as exactly one WON claim; got %d", won)
	}
}

// An UNSETTLED claim (no outcome) may still be abandoned/rejected freely, the guard only bites on a settled one.
func TestAdvancePhase_UnsettledClaimStillExitable(t *testing.T) {
	svc, _, _ := newSvc()
	ctx := ctxWith(staff("u1"))
	c, _ := svc.CreateClaim(ctx, ClaimInput{BranchID: testBranch})
	if _, err := svc.AdvancePhase(ctx, c.ID, PhaseTransitionInput{ToPhase: PhaseAbandoned}); err != nil {
		t.Fatalf("an unsettled claim must still be abandonable; got %v", err)
	}
}

// - MINOR: RecordSettlement is restricted to DECISION/SETTLEMENT -

// Settling a claim still in an EARLY phase (INTAKE) is out of sequence and must be refused (422).
func TestRecordSettlement_RejectedFromEarlyPhase(t *testing.T) {
	svc, fs, _ := newSvc()
	ctx := ctxWith(staff("u1"))
	c, _ := svc.CreateClaim(ctx, ClaimInput{BranchID: testBranch, ExpectedCents: 50000})
	// Fresh claim is in INTAKE, not DECISION/SETTLEMENT.
	_, err := svc.RecordSettlement(ctx, c.ID, SettlementInput{Outcome: OutcomeVendorCredit, RealizedCents: 40000})
	if status(err) != 422 {
		t.Fatalf("settling from INTAKE must be refused (422); got status %d (err %v)", status(err), err)
	}
	// Nothing was booked.
	if got := fs.claims[c.ID]; got.Outcome != "" || got.RealizedCents != 0 {
		t.Fatalf("a rejected early settle must book nothing; got outcome=%s realized=%d", got.Outcome, got.RealizedCents)
	}
}

// Settlement is allowed from DECISION and from SETTLEMENT (the two in-sequence phases).
func TestRecordSettlement_AllowedFromDecisionAndSettlement(t *testing.T) {
	// From DECISION.
	svc, _, _ := newSvc()
	ctx := ctxWith(staff("u1"))
	id := settleableClaim(t, svc, ctx, ClaimInput{BranchID: testBranch, ExpectedCents: 50000}) // lands in DECISION
	if _, err := svc.RecordSettlement(ctx, id, SettlementInput{Outcome: OutcomeVendorCredit, RealizedCents: 40000}); err != nil {
		t.Fatalf("settlement from DECISION must be allowed; got %v", err)
	}

	// From SETTLEMENT (advance one more, then settle).
	svc2, _, _ := newSvc()
	ctx2 := ctxWith(staff("u1"))
	id2 := settleableClaim(t, svc2, ctx2, ClaimInput{BranchID: testBranch, ExpectedCents: 50000})
	for _, k := range []string{"decision_recorded", "terms_captured"} {
		if _, err := svc2.TickChecklist(ctx2, id2, ChecklistTickInput{ItemKey: k, Checked: true}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc2.AdvancePhase(ctx2, id2, PhaseTransitionInput{ToPhase: PhaseSettlement}); err != nil {
		t.Fatalf("advance to SETTLEMENT: %v", err)
	}
	if _, err := svc2.RecordSettlement(ctx2, id2, SettlementInput{Outcome: OutcomeVendorCredit, RealizedCents: 40000}); err != nil {
		t.Fatalf("settlement from SETTLEMENT must be allowed; got %v", err)
	}
}
