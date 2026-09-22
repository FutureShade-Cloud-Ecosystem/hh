package warranty

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// service_test.go, service orchestration over a DB-free fakeStore: claim-#WTY-n assignment, the checklist-gated
// phase machine, back-dated capture (FR-9), and settlement recording + the GL seam.

// - test caller plumbing -

type ctxKeyT struct{}

func ctxWith(sc identity.StaffContext) context.Context {
	return context.WithValue(context.Background(), ctxKeyT{}, sc)
}
func testCallerFrom(ctx context.Context) (identity.StaffContext, bool) {
	sc, ok := ctx.Value(ctxKeyT{}).(identity.StaffContext)
	return sc, ok
}

// staff builds a branch-scoped caller GRANTED testBranch (the default caller for the existing tests, which all
// operate on testBranch). Branch scoping is now enforced on every read/mutation (the IDOR fix), so a caller
// must actually hold the claim's branch.
func staff(uid string) identity.StaffContext {
	return identity.StaffContext{UserID: uid, RoleCode: "INSIDE_SALES", Tier: identity.TierFrontline, Scope: identity.ScopeBranch, Status: "ACTIVE", Branches: map[string]bool{testBranch: true}}
}

// staffAt builds a branch-scoped caller granted a SPECIFIC branch (used by the cross-branch IDOR tests).
func staffAt(uid string, branches ...string) identity.StaffContext {
	m := map[string]bool{}
	for _, b := range branches {
		m[b] = true
	}
	return identity.StaffContext{UserID: uid, RoleCode: "INSIDE_SALES", Tier: identity.TierFrontline, Scope: identity.ScopeBranch, Status: "ACTIVE", Branches: m}
}

// globalStaff builds a Tier-1 GLOBAL caller that reaches every branch (the GM/admin scope).
func globalStaff(uid string) identity.StaffContext {
	return identity.StaffContext{UserID: uid, RoleCode: "GENERAL_MANAGER", Tier: identity.TierLeadership, Scope: identity.ScopeGlobal, Status: "ACTIVE"}
}

const (
	testBranch  = "5d1b3f6e-0000-4000-9000-000000000001"
	otherBranch = "5d1b3f6e-0000-4000-9000-000000000002"
)

// - fakeStore -

type fakeStore struct {
	mu        sync.Mutex               // guards the atomic settlement winner-election (mirrors the real repo's row lock)
	profiles  map[string]VendorProfile // keyed by vendor_id
	claims    map[string]Claim
	lines     map[string][]ClaimLine
	events    []ClaimEvent
	checklist []ChecklistItem
	seq       int

	// - W5-B state (see fakestore_w5b_test.go for the methods) -
	paths      map[string]MoneyPathConfig   // outcome → configured money path (defaults mirror 0129)
	postings   map[string]SettlementPosting // keyed "<claimID>|<moneyPath>" (mirrors the 0129 UNIQUE)
	recovery   map[string][]RecoveryAction  // claimID → chase log
	slaTargets []SLATarget
	slaAlerts  map[string]SLAAlert // keyed "<claimID>|<phase>" (mirrors the 0132 UNIQUE)
	intakeCfg  IntakeConfig
	rateHits   map[string]int // "<bucket>|<windowRFC3339>" → hits
	photos     map[string][]IntakePhoto
	failOn     map[string]error // method name → forced error (unwired/broken-store paths)
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		profiles: map[string]VendorProfile{}, claims: map[string]Claim{}, lines: map[string][]ClaimLine{},
		paths:      defaultOutcomePaths(),
		postings:   map[string]SettlementPosting{},
		recovery:   map[string][]RecoveryAction{},
		slaTargets: defaultSLATargets(),
		slaAlerts:  map[string]SLAAlert{},
		intakeCfg:  defaultIntakeConfig(),
		rateHits:   map[string]int{},
		photos:     map[string][]IntakePhoto{},
		failOn:     map[string]error{},
	}
}

func (f *fakeStore) id(p string) string { f.seq++; return p + "-" + strconv.Itoa(f.seq) }

// default checklist template mirroring migration 0054 (INTAKE + SUBMITTED gates used by the tests).
func defaultChecklist() []ChecklistItem {
	return []ChecklistItem{
		{ID: "i1", Phase: PhaseIntake, ItemKey: "contact_captured", Label: "Contact captured", Required: true, SortOrder: 10},
		{ID: "i2", Phase: PhaseIntake, ItemKey: "product_identified", Label: "Product identified", Required: true, SortOrder: 20},
		{ID: "i3", Phase: PhaseIntake, ItemKey: "description_captured", Label: "Description recorded", Required: true, SortOrder: 30},
		{ID: "i4", Phase: PhaseIntake, ItemKey: "photos_attached", Label: "Photos attached", Required: false, SortOrder: 40},
		{ID: "t1", Phase: PhaseTriage, ItemKey: "owner_assigned", Label: "Owner assigned", Required: true, SortOrder: 10},
		{ID: "t2", Phase: PhaseTriage, ItemKey: "vendor_identified", Label: "Vendor identified", Required: true, SortOrder: 20},
		{ID: "t3", Phase: PhaseTriage, ItemKey: "coverage_reviewed", Label: "Coverage reviewed", Required: true, SortOrder: 30},
		{ID: "t4", Phase: PhaseTriage, ItemKey: "proof_of_purchase", Label: "Proof located", Required: true, SortOrder: 40},
		// SUBMITTED / WAITING_ON_DECISION / DECISION gates (mirror 0054) so a claim can be walked to the
		// settle-eligible DECISION/SETTLEMENT phases in tests.
		{ID: "s1", Phase: PhaseSubmitted, ItemKey: "photos_attached", Label: "Photos attached", Required: true, SortOrder: 10},
		{ID: "s2", Phase: PhaseSubmitted, ItemKey: "proof_attached", Label: "Proof attached", Required: true, SortOrder: 20},
		{ID: "s3", Phase: PhaseSubmitted, ItemKey: "vendor_form_sent", Label: "Vendor form sent", Required: true, SortOrder: 30},
		{ID: "w1", Phase: PhaseWaitingOnDecision, ItemKey: "submission_ack", Label: "Submission acknowledged", Required: true, SortOrder: 10},
		{ID: "d1", Phase: PhaseDecision, ItemKey: "decision_recorded", Label: "Decision recorded", Required: true, SortOrder: 10},
		{ID: "d2", Phase: PhaseDecision, ItemKey: "terms_captured", Label: "Terms captured", Required: true, SortOrder: 20},
	}
}

func (f *fakeStore) UpsertVendorProfile(_ context.Context, in VendorProfileInput, _ string) (VendorProfile, error) {
	p := VendorProfile{ID: f.id("prof"), VendorID: in.VendorID, Status: in.Status, Coverage: in.Coverage,
		TermMonths: in.TermMonths, Channel: in.Channel, ChannelRef: in.ChannelRef, Notes: in.Notes, Docs: in.Docs}
	f.profiles[in.VendorID] = p
	return p, nil
}
func (f *fakeStore) GetVendorProfileByVendor(_ context.Context, vendorID string) (VendorProfile, error) {
	p, ok := f.profiles[vendorID]
	if !ok {
		return VendorProfile{}, errProfileNotFound
	}
	return p, nil
}
func (f *fakeStore) ListVendorProfiles(_ context.Context) ([]VendorProfile, error) {
	var out []VendorProfile
	for _, p := range f.profiles {
		out = append(out, p)
	}
	return out, nil
}
func (f *fakeStore) AddProductOverride(_ context.Context, in ProductOverrideInput) (ProductOverride, error) {
	return ProductOverride{ID: f.id("ov"), ProfileID: in.ProfileID, ProductID: in.ProductID, Coverage: in.Coverage, TermMonths: in.TermMonths, Notes: in.Notes}, nil
}
func (f *fakeStore) OverridesForProfile(_ context.Context, _ string) ([]ProductOverride, error) {
	return nil, nil
}

func (f *fakeStore) InsertClaim(_ context.Context, in ClaimInput, claimNo string, openedAt time.Time, createdBy string) (Claim, error) {
	if claimNo == "" { // mirror the real repo: assign the next #WTY-n atomically
		max := 0
		for _, c := range f.claims {
			if n, err := strconv.Atoi(strings.TrimPrefix(c.ClaimNo, "WTY-")); err == nil && n > max {
				max = n
			}
		}
		claimNo = "WTY-" + strconv.Itoa(max+1)
	}
	id := f.id("claim")
	c := Claim{
		ID: id, ClaimNo: claimNo, CustomerID: in.CustomerID, JobAddress: in.JobAddress, VendorID: in.VendorID,
		OwnerID: in.OwnerID, BranchID: in.BranchID, Phase: PhaseIntake, Source: in.Source, Description: in.Description,
		InvoiceID: in.InvoiceID, OrderID: in.OrderID, POID: in.POID, ExpectedCents: in.ExpectedCents,
		OpenedAt: openedAt.Format(time.RFC3339),
	}
	f.claims[id] = c
	for _, l := range in.Lines {
		f.lines[id] = append(f.lines[id], ClaimLine{ID: f.id("line"), ClaimID: id, ProductID: l.ProductID, ProductText: l.ProductText, Qty: l.Qty, UOMCode: l.UOMCode})
	}
	return c, nil
}
func (f *fakeStore) GetClaim(_ context.Context, id string) (Claim, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.claims[id]
	if !ok {
		return Claim{}, errClaimNotFound
	}
	return c, nil
}
func (f *fakeStore) ListClaims(_ context.Context, flt ClaimFilter) ([]Claim, error) {
	var out []Claim
	for _, c := range f.claims {
		if flt.BranchID != "" && c.BranchID != flt.BranchID {
			continue
		}
		if flt.Phase != "" && c.Phase != flt.Phase {
			continue
		}
		if flt.VendorID != "" && c.VendorID != flt.VendorID {
			continue
		}
		out = append(out, c)
	}
	return out, nil
}
func (f *fakeStore) ClaimLines(_ context.Context, claimID string) ([]ClaimLine, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lines[claimID], nil
}
func (f *fakeStore) UpdateClaimVendorOwner(_ context.Context, claimID, vendorID, ownerID string) error {
	c := f.claims[claimID]
	c.VendorID = vendorID
	c.OwnerID = ownerID
	f.claims[claimID] = c
	return nil
}

// AdvanceClaimPhaseGuarded mirrors the real repo's locked, guarded advance: under f.mu (standing in for the
// SELECT … FOR UPDATE row lock) it re-checks the mutual-exclusion guard against the CURRENT stored state, then
// writes the phase + PHASE_CHANGE event in the same critical section. guardExit refuses advancing a SETTLED
// claim to a terminal-LOST exit (→ errAlreadySettledExit / 422); a terminal claim is refused (→ errAlreadyTerminal
// / 409). The f.mu critical section is what makes this atomic vs a concurrent ClaimSettlementWinner in the race
// regression test.
func (f *fakeStore) AdvanceClaimPhaseGuarded(_ context.Context, claimID, toPhase string, guardExit bool, submittedAt, slaDue, closedAt *time.Time, event ClaimEvent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.claims[claimID]
	if !ok {
		return errClaimNotFound
	}
	if IsTerminal(c.Phase) {
		return errAlreadyTerminal
	}
	if guardExit && c.Outcome != "" {
		return errAlreadySettledExit
	}
	c.Phase = toPhase
	if submittedAt != nil {
		c.SubmittedAt = submittedAt.Format(time.RFC3339)
	}
	if slaDue != nil {
		c.SLADue = slaDue.Format(time.RFC3339)
	}
	if closedAt != nil {
		c.ClosedAt = closedAt.Format(time.RFC3339)
	}
	f.claims[claimID] = c
	event.ID = f.id("evt")
	f.events = append(f.events, event)
	return nil
}
func (f *fakeStore) InsertEvent(_ context.Context, e ClaimEvent) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e.ID = f.id("evt")
	f.events = append(f.events, e)
	return e.ID, nil
}
func (f *fakeStore) EventsForClaim(_ context.Context, claimID string) ([]ClaimEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []ClaimEvent
	for _, e := range f.events {
		if e.ClaimID == claimID {
			out = append(out, e)
		}
	}
	return out, nil
}
func (f *fakeStore) TickedKeys(_ context.Context, claimID string) (map[string]bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]bool{}
	for _, e := range f.events { // last tick wins (append order)
		if e.ClaimID == claimID && e.EventType == "CHECKLIST_TICK" && e.ChecklistKey != "" && e.Checked != nil {
			out[e.ChecklistKey] = *e.Checked
		}
	}
	return out, nil
}
func (f *fakeStore) ChecklistForPhase(_ context.Context, phase, _ string) ([]ChecklistItem, error) {
	var out []ChecklistItem
	for _, it := range f.checklist {
		if it.Phase == phase {
			out = append(out, it)
		}
	}
	return out, nil
}
func (f *fakeStore) ChecklistItemByKey(_ context.Context, phase, _, itemKey string) (ChecklistItem, error) {
	for _, it := range f.checklist {
		if it.Phase == phase && it.ItemKey == itemKey {
			return it, nil
		}
	}
	return ChecklistItem{}, unprocessable("no item")
}

// ClaimSettlementWinner mirrors the real repo's atomic winner-election: under f.mu it conditionally writes the
// outcome only when the claim is still unsettled (outcome == ""), returning won=false for a repeat settle. The
// SETTLEMENT event rides the same critical section (as it rides the same tx in the real repo).
func (f *fakeStore) ClaimSettlementWinner(_ context.Context, claimID, outcome string, expectedCents, realizedCents int64, event ClaimEvent) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.claims[claimID]
	// Re-check UNDER the lock (mirrors the real repo's FOR UPDATE): a claim a concurrent advance drove to a
	// terminal exit cannot be settled, this is the settle half of the mutual exclusion in the race regression.
	if IsTerminal(c.Phase) {
		return false, errAlreadyTerminal
	}
	if c.Outcome != "" {
		return false, nil // already settled, a loser
	}
	c.Outcome = outcome
	c.ExpectedCents = expectedCents
	c.RealizedCents = realizedCents
	f.claims[claimID] = c
	event.ID = f.id("evt")
	f.events = append(f.events, event)
	return true, nil
}

func (f *fakeStore) StampSettlementJournal(_ context.Context, claimID, glJournalID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.claims[claimID]
	c.GLJournalID = glJournalID
	f.claims[claimID] = c
	return nil
}
func (f *fakeStore) AllClaimsForReport(_ context.Context, branchID string) ([]ReportClaim, error) {
	var out []ReportClaim
	for _, c := range f.claims {
		if branchID != "" && c.BranchID != branchID {
			continue
		}
		opened, _ := time.Parse(time.RFC3339, c.OpenedAt)
		var closed *time.Time
		if c.ClosedAt != "" {
			t, _ := time.Parse(time.RFC3339, c.ClosedAt)
			closed = &t
		}
		out = append(out, ReportClaim{ID: c.ID, ClaimNo: c.ClaimNo, BranchID: c.BranchID, VendorID: c.VendorID, Phase: c.Phase, Outcome: c.Outcome, ExpectedCents: c.ExpectedCents, RealizedCents: c.RealizedCents, OpenedAt: opened, ClosedAt: closed})
	}
	return out, nil
}

// - fake GL poster -

type fakeGL struct {
	posted []GLJournal
	fail   bool
	// blankID makes the provider ACCEPT a journal and return no id, the "posted but showed no work" case the
	// 0129 CHECK forbids, which the money-path executor must therefore treat as a failure rather than POSTED.
	blankID bool
}

func (g *fakeGL) PostJournal(_ context.Context, in GLJournal) (string, error) {
	if g.fail {
		return "", unprocessable("gl down")
	}
	g.posted = append(g.posted, in)
	if g.blankID {
		return "", nil
	}
	return "jrnl-" + strconv.Itoa(len(g.posted)), nil
}

// testClock is THE clock for this file: the single instant every service in it runs at, and the
// single instant every fixture date must be derived from.
//
// DIB-427. A fixture time taken from time.Now() while the service under test runs at a fixed instant
// is a dated time bomb: the gap between the two widens by one day per real day until some comparison
// flips. Here the pair was svc.now = 2026-07-14 against a closed_at taken from the wall clock, a claim that
// closed 28 days after it opened today, 90 days after next quarter, and BEFORE it opened once the
// wall clock passes the pinned instant. Both sides now come from testClock, so the relationship the
// tests assert is fixed at whatever date the suite runs. (House pattern: see the comment block in
// internal/messagingprovider/collections_integration_test.go, which defused the first of these.)
func testClock() time.Time { return time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC) }

// newSvc wires a service over a fresh fakeStore seeded with the default checklist.
func newSvc() (*Service, *fakeStore, *fakeGL) {
	fs := newFakeStore()
	fs.checklist = defaultChecklist()
	gl := &fakeGL{}
	svc := NewService(nil).withStore(fs).withCaller(testCallerFrom).WithGLPoster(gl)
	svc.now = testClock
	return svc, fs, gl
}

// - claim capture + #WTY-n assignment -

func TestCreateClaim_AssignsSequentialClaimNo(t *testing.T) {
	svc, _, _ := newSvc()
	ctx := ctxWith(staff("u1"))
	c1, err := svc.CreateClaim(ctx, ClaimInput{BranchID: testBranch, Description: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if c1.ClaimNo != "WTY-1" {
		t.Errorf("first claim should be WTY-1, got %s", c1.ClaimNo)
	}
	c2, _ := svc.CreateClaim(ctx, ClaimInput{BranchID: testBranch, Description: "b"})
	if c2.ClaimNo != "WTY-2" {
		t.Errorf("second claim should be WTY-2, got %s", c2.ClaimNo)
	}
}

func TestCreateClaim_ContinuesPastBackDatedSet(t *testing.T) {
	// Simulate the migrated live set (WTY-1..WTY-5 already present) → next capture is WTY-6 (gap-free).
	svc, fs, _ := newSvc()
	for i := 1; i <= 5; i++ {
		id := "seed-" + strconv.Itoa(i)
		fs.claims[id] = Claim{ID: id, ClaimNo: "WTY-" + strconv.Itoa(i), BranchID: testBranch, Phase: PhaseIntake, OpenedAt: "2026-01-01T00:00:00Z"}
	}
	c, err := svc.CreateClaim(ctxWith(staff("u1")), ClaimInput{BranchID: testBranch})
	if err != nil {
		t.Fatal(err)
	}
	if c.ClaimNo != "WTY-6" {
		t.Errorf("next after the migrated set should be WTY-6, got %s", c.ClaimNo)
	}
}

func TestCreateClaim_DefaultsOwnerToCaller(t *testing.T) {
	svc, _, _ := newSvc()
	c, _ := svc.CreateClaim(ctxWith(staff("u-caller")), ClaimInput{BranchID: testBranch})
	if c.OwnerID != "u-caller" {
		t.Errorf("owner should default to the capturing staff member; got %q", c.OwnerID)
	}
}

func TestCreateClaim_RequiresBranch(t *testing.T) {
	svc, _, _ := newSvc()
	_, err := svc.CreateClaim(ctxWith(staff("u1")), ClaimInput{Description: "no branch"})
	if err == nil {
		t.Fatal("expected branch_id required error")
	}
}

func TestCreateClaim_Unauthenticated(t *testing.T) {
	svc, _, _ := newSvc()
	_, err := svc.CreateClaim(context.Background(), ClaimInput{BranchID: testBranch})
	if err == nil {
		t.Fatal("expected unauthenticated error with no caller in context")
	}
}

func TestCreateClaim_VendorUnknownAllowed(t *testing.T) {
	// FR-2: a claim may start with NO vendor (UNKNOWN pending triage).
	svc, _, _ := newSvc()
	c, err := svc.CreateClaim(ctxWith(staff("u1")), ClaimInput{BranchID: testBranch, VendorID: ""})
	if err != nil {
		t.Fatal(err)
	}
	if c.VendorID != "" {
		t.Errorf("vendor should remain UNKNOWN, got %q", c.VendorID)
	}
}

// - FR-9 back-dated capture -

func TestCreateClaim_BackDated(t *testing.T) {
	svc, _, _ := newSvc()
	c, err := svc.CreateClaim(ctxWith(staff("u1")), ClaimInput{BranchID: testBranch, OpenedAt: "2026-01-15"})
	if err != nil {
		t.Fatal(err)
	}
	opened, _ := time.Parse(time.RFC3339, c.OpenedAt)
	if opened.Year() != 2026 || opened.Month() != time.January || opened.Day() != 15 {
		t.Errorf("back-dated opened_at not honoured: %s", c.OpenedAt)
	}
	// The CREATED event should carry the same true past time (history integrity).
	evs, _ := svc.ClaimEvents(ctxWith(staff("u1")), c.ID)
	if len(evs) == 0 || !strings.HasPrefix(evs[0].OccurredAt, "2026-01-15") {
		t.Errorf("CREATED event should be back-dated to opened_at; got %v", evs)
	}
}

func TestCreateClaim_RejectsFutureOpenedAt(t *testing.T) {
	svc, _, _ := newSvc() // clock is 2026-07-14
	_, err := svc.CreateClaim(ctxWith(staff("u1")), ClaimInput{BranchID: testBranch, OpenedAt: "2027-01-01"})
	if err == nil {
		t.Fatal("expected a future opened_at to be rejected")
	}
}

// - FR-3 checklist-gated phase machine -

func TestAdvancePhase_BlockedByChecklistGate(t *testing.T) {
	svc, _, _ := newSvc()
	ctx := ctxWith(staff("u1"))
	c, _ := svc.CreateClaim(ctx, ClaimInput{BranchID: testBranch})
	_, err := svc.AdvancePhase(ctx, c.ID, PhaseTransitionInput{ToPhase: PhaseTriage})
	if err == nil {
		t.Fatal("advancing INTAKE→TRIAGE with unticked required items must be blocked")
	}
	if !strings.Contains(err.Error(), "checklist") {
		t.Errorf("error should mention the checklist gate; got %v", err)
	}
	// The claim must NOT have moved.
	got, _ := svc.GetClaim(ctx, c.ID)
	if got.Phase != PhaseIntake {
		t.Errorf("claim should stay in INTAKE after a blocked advance; got %s", got.Phase)
	}
}

func TestAdvancePhase_PassesAfterTicking(t *testing.T) {
	svc, _, _ := newSvc()
	ctx := ctxWith(staff("u1"))
	c, _ := svc.CreateClaim(ctx, ClaimInput{BranchID: testBranch})
	for _, k := range []string{"contact_captured", "product_identified", "description_captured"} {
		if _, err := svc.TickChecklist(ctx, c.ID, ChecklistTickInput{ItemKey: k, Checked: true}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := svc.AdvancePhase(ctx, c.ID, PhaseTransitionInput{ToPhase: PhaseTriage})
	if err != nil {
		t.Fatalf("advance should succeed once required items ticked: %v", err)
	}
	if got.Phase != PhaseTriage {
		t.Errorf("expected TRIAGE, got %s", got.Phase)
	}
}

func TestTickChecklist_UntickReblocks(t *testing.T) {
	svc, _, _ := newSvc()
	ctx := ctxWith(staff("u1"))
	c, _ := svc.CreateClaim(ctx, ClaimInput{BranchID: testBranch})
	for _, k := range []string{"contact_captured", "product_identified", "description_captured"} {
		_, _ = svc.TickChecklist(ctx, c.ID, ChecklistTickInput{ItemKey: k, Checked: true})
	}
	// Untick one → the gate blocks again (last tick wins).
	_, _ = svc.TickChecklist(ctx, c.ID, ChecklistTickInput{ItemKey: "contact_captured", Checked: false})
	_, err := svc.AdvancePhase(ctx, c.ID, PhaseTransitionInput{ToPhase: PhaseTriage})
	if err == nil {
		t.Fatal("un-ticking a required item should re-block advancement")
	}
}

func TestAdvancePhase_IllegalTransitionRejected(t *testing.T) {
	svc, _, _ := newSvc()
	ctx := ctxWith(staff("u1"))
	c, _ := svc.CreateClaim(ctx, ClaimInput{BranchID: testBranch})
	// INTAKE → SUBMITTED skips TRIAGE, illegal even with everything ticked.
	_, err := svc.AdvancePhase(ctx, c.ID, PhaseTransitionInput{ToPhase: PhaseSubmitted})
	if err == nil {
		t.Fatal("skipping a phase must be rejected")
	}
}

func TestAdvancePhase_AbandonNeverGated(t *testing.T) {
	svc, _, _ := newSvc()
	ctx := ctxWith(staff("u1"))
	c, _ := svc.CreateClaim(ctx, ClaimInput{BranchID: testBranch})
	// No items ticked, but abandoning is always allowed.
	got, err := svc.AdvancePhase(ctx, c.ID, PhaseTransitionInput{ToPhase: PhaseAbandoned})
	if err != nil {
		t.Fatalf("abandon should never be gated: %v", err)
	}
	if got.Phase != PhaseAbandoned || got.ClosedAt == "" {
		t.Errorf("abandon should close the claim (phase=%s closed_at=%q)", got.Phase, got.ClosedAt)
	}
}

func TestAdvancePhase_SubmittedStampsSLA(t *testing.T) {
	svc, _, _ := newSvc()
	ctx := ctxWith(staff("u1"))
	c, _ := svc.CreateClaim(ctx, ClaimInput{BranchID: testBranch})
	// intake → triage (tick intake requireds)
	for _, k := range []string{"contact_captured", "product_identified", "description_captured"} {
		_, _ = svc.TickChecklist(ctx, c.ID, ChecklistTickInput{ItemKey: k, Checked: true})
	}
	_, _ = svc.AdvancePhase(ctx, c.ID, PhaseTransitionInput{ToPhase: PhaseTriage})
	// triage → submitted (tick triage requireds)
	for _, k := range []string{"owner_assigned", "vendor_identified", "coverage_reviewed", "proof_of_purchase"} {
		_, _ = svc.TickChecklist(ctx, c.ID, ChecklistTickInput{ItemKey: k, Checked: true})
	}
	got, err := svc.AdvancePhase(ctx, c.ID, PhaseTransitionInput{ToPhase: PhaseSubmitted})
	if err != nil {
		t.Fatal(err)
	}
	if got.SubmittedAt == "" || got.SLADue == "" {
		t.Errorf("entering Submitted must stamp submitted_at + sla_due (got %q / %q)", got.SubmittedAt, got.SLADue)
	}
}

// A back-dated terminal advance must stamp the TRUE closed_at (FR-9 migration), not today, otherwise
// time-to-settlement and age are inflated for a claim that resolved before go-live. Guards MAJOR-2.
func TestAdvancePhase_BackDatedClose(t *testing.T) {
	svc, fs, _ := newSvc()
	ctx := ctxWith(staff("u1"))
	// Open 90 days ago; abandon (an exit, ungated) as of 40 days ago, both before "now" (2026-07-14).
	c, _ := svc.CreateClaim(ctx, ClaimInput{BranchID: testBranch, OpenedAt: "2026-04-15"})
	got, err := svc.AdvancePhase(ctx, c.ID, PhaseTransitionInput{ToPhase: PhaseAbandoned, OccurredAt: "2026-06-04"})
	if err != nil {
		t.Fatal(err)
	}
	if got.ClosedAt == "" {
		t.Fatal("a terminal advance must stamp closed_at")
	}
	closed, _ := time.Parse(time.RFC3339, got.ClosedAt)
	if closed.Year() != 2026 || closed.Month() != 6 || closed.Day() != 4 {
		t.Errorf("closed_at must be the back-dated 2026-06-04, not now(); got %s", got.ClosedAt)
	}
	// The PHASE_CHANGE event carries the same true time (audit spine stays honest).
	evs, _ := fs.EventsForClaim(ctx, c.ID)
	var found bool
	for _, e := range evs {
		if e.EventType == "PHASE_CHANGE" && e.ToPhase == PhaseAbandoned {
			if !strings.HasPrefix(e.OccurredAt, "2026-06-04") {
				t.Errorf("phase-change event should carry the back-dated time; got %s", e.OccurredAt)
			}
			found = true
		}
	}
	if !found {
		t.Error("expected a back-dated PHASE_CHANGE event")
	}
}

// A close back-dated BEFORE the open date is nonsensical and must be rejected.
func TestAdvancePhase_OccurredBeforeOpenRejected(t *testing.T) {
	svc, _, _ := newSvc()
	ctx := ctxWith(staff("u1"))
	c, _ := svc.CreateClaim(ctx, ClaimInput{BranchID: testBranch, OpenedAt: "2026-06-01"})
	_, err := svc.AdvancePhase(ctx, c.ID, PhaseTransitionInput{ToPhase: PhaseAbandoned, OccurredAt: "2026-05-01"})
	if err == nil {
		t.Fatal("a transition dated before the claim was opened must be rejected")
	}
}

// A future-dated transition must be rejected (symmetric with capture's opened_at guard).
func TestAdvancePhase_FutureOccurredRejected(t *testing.T) {
	svc, _, _ := newSvc()
	ctx := ctxWith(staff("u1"))
	c, _ := svc.CreateClaim(ctx, ClaimInput{BranchID: testBranch})
	_, err := svc.AdvancePhase(ctx, c.ID, PhaseTransitionInput{ToPhase: PhaseAbandoned, OccurredAt: "2099-01-01"})
	if err == nil {
		t.Fatal("a future-dated transition must be rejected")
	}
}

func TestAdvancePhase_TerminalCannotAdvance(t *testing.T) {
	svc, _, _ := newSvc()
	ctx := ctxWith(staff("u1"))
	c, _ := svc.CreateClaim(ctx, ClaimInput{BranchID: testBranch})
	_, _ = svc.AdvancePhase(ctx, c.ID, PhaseTransitionInput{ToPhase: PhaseAbandoned})
	_, err := svc.AdvancePhase(ctx, c.ID, PhaseTransitionInput{ToPhase: PhaseTriage})
	if err == nil {
		t.Fatal("a closed (terminal) claim must not advance")
	}
}

// - FR-5 settlement + GL seam -

func TestRecordSettlement_PostsGLForVendorCredit(t *testing.T) {
	svc, fs, gl := newSvc()
	ctx := ctxWith(staff("u1"))
	id := settleableClaim(t, svc, ctx, ClaimInput{BranchID: testBranch, ExpectedCents: 100000})
	res, err := svc.RecordSettlement(ctx, id, SettlementInput{Outcome: OutcomeVendorCredit, ExpectedCents: ptrI64(100000), RealizedCents: 95000})
	if err != nil {
		t.Fatal(err)
	}
	if !res.GLPosted || res.GLJournalID == "" {
		t.Errorf("a realized vendor credit should post to GL; got %+v", res)
	}
	if len(gl.posted) != 1 {
		t.Fatalf("expected 1 GL journal, got %d", len(gl.posted))
	}
	// Balanced: DR total == CR total.
	var dr, cr int64
	for _, l := range gl.posted[0].Lines {
		dr += l.DebitCents
		cr += l.CreditCents
	}
	if dr != cr || dr != 95000 {
		t.Errorf("journal should balance at 95000; dr=%d cr=%d", dr, cr)
	}
	// The claim now carries realized + the journal id.
	got := fs.claims[id]
	if got.RealizedCents != 95000 || got.GLJournalID == "" {
		t.Errorf("claim should carry realized + journal id; got realized=%d jrnl=%q", got.RealizedCents, got.GLJournalID)
	}
}

func TestRecordSettlement_RecordedEvenWhenGLFails(t *testing.T) {
	svc, fs, gl := newSvc()
	gl.fail = true // GL down
	ctx := ctxWith(staff("u1"))
	id := settleableClaim(t, svc, ctx, ClaimInput{BranchID: testBranch})
	res, err := svc.RecordSettlement(ctx, id, SettlementInput{Outcome: OutcomeVendorCredit, RealizedCents: 50000})
	if err != nil {
		t.Fatalf("settlement must be RECORDED even if GL post fails (P1): %v", err)
	}
	if res.GLPosted {
		t.Error("GLPosted should be false when the post failed")
	}
	if fs.claims[id].RealizedCents != 50000 {
		t.Error("amounts must be recorded regardless of the GL outcome")
	}
}

func TestRecordSettlement_WriteOffZeroNoJournal(t *testing.T) {
	svc, _, gl := newSvc()
	ctx := ctxWith(staff("u1"))
	id := settleableClaim(t, svc, ctx, ClaimInput{BranchID: testBranch})
	res, err := svc.RecordSettlement(ctx, id, SettlementInput{Outcome: OutcomeWriteOff, RealizedCents: 0})
	if err != nil {
		t.Fatal(err)
	}
	if res.GLPosted || len(gl.posted) != 0 {
		t.Error("a $0 write-off should record without a GL journal")
	}
	// MINOR-2, re-targeted for ERP:warranty-p2: gl_posted must still be HONEST about WHY nothing posted, a
	// zero-effect settlement, not an unwired seam. The P2 vocabulary for that is the NO_EFFECT path status; the
	// note now says "no money moved" rather than "no cash effect", and the STATUS is the assertion that matters
	// because it is the thing the recovery board and the claim card both read.
	if res.PathStatus != PathNoEffect {
		t.Errorf("a $0 settlement must record path status NO_EFFECT (not an unwired-seam status); got %q (%q)",
			res.PathStatus, res.GLNote)
	}
	if res.MoneyPath != PathExpenseWriteOff {
		t.Errorf("a WRITE_OFF must still name its own money path even at $0; got %q", res.MoneyPath)
	}
}

func TestRecordSettlement_ReplacementStockNoCashJournal(t *testing.T) {
	svc, _, gl := newSvc()
	ctx := ctxWith(staff("u1"))
	id := settleableClaim(t, svc, ctx, ClaimInput{BranchID: testBranch})
	res, err := svc.RecordSettlement(ctx, id, SettlementInput{Outcome: OutcomeReplacementStock, RealizedCents: 40000})
	if err != nil {
		t.Fatal(err)
	}
	// Replacement stock books through the INVENTORY seam, never a cash journal from warranty. That rule is
	// unchanged from P1; what changed in P2 is that the seam now has a NAME and a status rather than a comment.
	if res.GLPosted || len(gl.posted) != 0 {
		t.Error("replacement stock must never post a cash journal from warranty (inventory owns the cost movement)")
	}
	if res.MoneyPath != PathInventoryReplacement || res.PathSeam != SeamInventory {
		t.Errorf("replacement stock must route to the inventory replacement path; got path=%q seam=%q",
			res.MoneyPath, res.PathSeam)
	}
	// Unbound here (newSvc wires only GL), so the honest answer is SEAM_UNWIRED with the request preserved -
	// deliberately NOT a fallback journal, because fabricating a cost movement for stock that may never have
	// left the yard would be a lie in the inventory valuation.
	if res.PathStatus != PathSeamUnwired {
		t.Errorf("an unbound inventory seam must record SEAM_UNWIRED; got %q (%q)", res.PathStatus, res.GLNote)
	}
}

func TestRecordSettlement_RejectsBadOutcome(t *testing.T) {
	svc, _, _ := newSvc()
	ctx := ctxWith(staff("u1"))
	id := settleableClaim(t, svc, ctx, ClaimInput{BranchID: testBranch})
	_, err := svc.RecordSettlement(ctx, id, SettlementInput{Outcome: "BOGUS", RealizedCents: 1})
	if err == nil {
		t.Fatal("an unknown outcome must be rejected")
	}
}

// A second settle on an already-settled claim must be REFUSED (409), not silently re-book: the GL journal is
// idempotent per claim, so a "correction" would overwrite the claim/report money while REPLAYING the original
// journal, the claim record and the ledger would diverge. Guards MAJOR (authz + correctness reviews).
func TestRecordSettlement_ReSettleRefused(t *testing.T) {
	svc, fs, gl := newSvc()
	ctx := ctxWith(staff("u1"))
	id := settleableClaim(t, svc, ctx, ClaimInput{BranchID: testBranch})
	if _, err := svc.RecordSettlement(ctx, id, SettlementInput{Outcome: OutcomeVendorCredit, RealizedCents: 50000}); err != nil {
		t.Fatalf("first settle: %v", err)
	}
	_, err := svc.RecordSettlement(ctx, id, SettlementInput{Outcome: OutcomeWriteOff, RealizedCents: 80000})
	if err == nil {
		t.Fatal("a second settle on an already-settled claim must be refused")
	}
	var ce clientError
	if !errors.As(err, &ce) || ce.status != 409 {
		t.Fatalf("expected 409 conflict; got %v", err)
	}
	// The claim + ledger stay at the FIRST settlement (no divergence).
	if got := fs.claims[id]; got.Outcome != OutcomeVendorCredit || got.RealizedCents != 50000 {
		t.Errorf("claim must retain the first settlement; got outcome=%s realized=%d", got.Outcome, got.RealizedCents)
	}
	if len(gl.posted) != 1 {
		t.Errorf("exactly one GL journal should exist; got %d", len(gl.posted))
	}
}

// CONCURRENT double-submit: N goroutines settle ONE claim with DIFFERENT outcomes/amounts at once. Exactly ONE
// must win; the rest must be 409. The claim row + the single GL journal must both reflect the SAME winner (no
// last-writer-wins divergence). Run with -race, this also proves the winner-election has no data race. This is
// the DB-free mirror of the live TestIntegration_ConcurrentSettleSingleWinner regression guard.
func TestRecordSettlement_ConcurrentSingleWinner(t *testing.T) {
	svc, fs, gl := newSvc()
	ctx := ctxWith(staff("u1"))
	id := settleableClaim(t, svc, ctx, ClaimInput{BranchID: testBranch, ExpectedCents: 50000})

	type attempt struct {
		outcome  string
		realized int64
	}
	attempts := []attempt{
		{OutcomeVendorCredit, 50000}, {OutcomeWriteOff, 80000},
		{OutcomeVendorCredit, 51000}, {OutcomeWriteOff, 82000},
		{OutcomeVendorCredit, 52000}, {OutcomeWriteOff, 83000},
		{OutcomeVendorCredit, 53000}, {OutcomeWriteOff, 84000},
	}
	const n = 8

	type result struct {
		res SettlementResult
		err error
	}
	results := make(chan result, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		a := attempts[i]
		go func() {
			<-start
			res, err := svc.RecordSettlement(ctx, id, SettlementInput{Outcome: a.outcome, ExpectedCents: ptrI64(a.realized), RealizedCents: a.realized})
			results <- result{res: res, err: err}
		}()
	}
	close(start)

	var winners int
	var winner SettlementResult
	for i := 0; i < n; i++ {
		r := <-results
		if r.err == nil {
			winners++
			winner = r.res
			continue
		}
		var ce clientError
		if !errors.As(r.err, &ce) || ce.status != 409 {
			t.Fatalf("a losing concurrent settle must be a 409; got %v", r.err)
		}
	}
	if winners != 1 {
		t.Fatalf("EXACTLY ONE concurrent settle must win; got %d", winners)
	}
	// The claim row reflects the winner (this row is the FR-7 reporting money).
	if got := fs.claims[id]; got.Outcome != winner.Outcome || got.RealizedCents != winner.RealizedCents {
		t.Fatalf("claim must reflect the winner (outcome=%s realized=%d); got outcome=%s realized=%d",
			winner.Outcome, winner.RealizedCents, got.Outcome, got.RealizedCents)
	}
	// Exactly ONE journal posted, and it carries the WINNER's amount (the journal cannot reflect a loser).
	if len(gl.posted) != 1 {
		t.Fatalf("exactly ONE GL journal must be posted under concurrency; got %d", len(gl.posted))
	}
	var dr, cr int64
	for _, l := range gl.posted[0].Lines {
		dr += l.DebitCents
		cr += l.CreditCents
	}
	if dr != cr || dr != winner.RealizedCents {
		t.Fatalf("the single journal must balance at the winner's amount %d; got dr=%d cr=%d", winner.RealizedCents, dr, cr)
	}
	if fs.claims[id].GLJournalID == "" {
		t.Fatal("the winning claim must carry the posted journal id")
	}
}

// CONCURRENT advance-to-exit vs settle (MONEY-CRITICAL TOCTOU regression, DB-free mirror of
// TestIntegration_ConcurrentAdvanceExitVsSettle): N times, race advance(ABANDONED) against settle(VENDOR_CREDIT)
// on a DECISION claim. The claim must NEVER end terminal-LOST (ABANDONED/REJECTED) WHILE carrying a recovery
// (outcome/realized/journal), and an abandoned claim must carry NO posted journal. The fake's f.mu critical
// section models the real repo's SELECT … FOR UPDATE row lock: both AdvanceClaimPhaseGuarded and
// ClaimSettlementWinner re-check the mutual-exclusion guard under it, so exactly one terminal decision commits.
// Run with -race this also proves no data race. Fails against the pre-fix unconditional-write path.
func TestAdvanceExitVsSettle_ConcurrentMutualExclusion(t *testing.T) {
	// DETERMINISTIC guard (fails against the pre-fix under-lock re-check REGARDLESS of goroutine scheduling): the
	// two committed orderings the TOCTOU produced. These exercise the store's under-lock re-check directly, so
	// they are a reliable red-before / green-after regression (the concurrent loop below adds coverage under real
	// contention but is scheduler-dependent).
	t.Run("settled_then_advance_to_exit_refused", func(t *testing.T) {
		svc, fs, _ := newSvc()
		ctx := ctxWith(staff("u1"))
		id := settleableClaim(t, svc, ctx, ClaimInput{BranchID: testBranch, ExpectedCents: 100000})
		if _, err := svc.RecordSettlement(ctx, id, SettlementInput{Outcome: OutcomeVendorCredit, RealizedCents: 99000}); err != nil {
			t.Fatalf("settle: %v", err)
		}
		// The guarded store call MUST refuse advancing a settled claim to a LOST exit (the under-lock re-check; a
		// pre-fix unconditional write would strand the claim terminal-LOST-with-a-recovery).
		err := fs.AdvanceClaimPhaseGuarded(ctx, id, PhaseAbandoned, true, nil, nil, ptrTime(testClock()), ClaimEvent{ClaimID: id, EventType: "PHASE_CHANGE", ToPhase: PhaseAbandoned})
		if status(err) != 422 {
			t.Fatalf("advance-to-exit on a settled claim must be refused 422 under the lock; got %v", err)
		}
		if c := fs.claims[id]; c.Phase == PhaseAbandoned || c.Outcome != OutcomeVendorCredit || c.RealizedCents != 99000 {
			t.Fatalf("settled claim must retain its recovery, never go terminal-LOST; got phase=%s outcome=%s realized=%d", c.Phase, c.Outcome, c.RealizedCents)
		}
	})
	t.Run("terminal_exit_then_settle_refused", func(t *testing.T) {
		svc, fs, gl := newSvc()
		ctx := ctxWith(staff("u1"))
		id := settleableClaim(t, svc, ctx, ClaimInput{BranchID: testBranch, ExpectedCents: 100000})
		if err := fs.AdvanceClaimPhaseGuarded(ctx, id, PhaseAbandoned, true, nil, nil, ptrTime(testClock()), ClaimEvent{ClaimID: id, EventType: "PHASE_CHANGE", ToPhase: PhaseAbandoned}); err != nil {
			t.Fatalf("advance to abandoned: %v", err)
		}
		// A settle on the now-terminal claim MUST be refused under the lock (409) and post NOTHING, the pre-fix
		// path (outcome still NULL) would have written a recovery + posted a journal onto the abandoned claim.
		won, err := fs.ClaimSettlementWinner(ctx, id, OutcomeVendorCredit, 100000, 99000, ClaimEvent{ClaimID: id, EventType: "SETTLEMENT"})
		if won || status(err) != 409 {
			t.Fatalf("settling a terminal claim must be refused 409 under the lock; got won=%v err=%v", won, err)
		}
		if c := fs.claims[id]; c.Outcome != "" || c.RealizedCents != 0 {
			t.Fatalf("an abandoned claim must carry NO recovery; got outcome=%s realized=%d", c.Outcome, c.RealizedCents)
		}
		if len(gl.posted) != 0 {
			t.Fatalf("an abandoned claim must have NO posted journal; got %d", len(gl.posted))
		}
	})

	const n = 16 // ≥12 as required
	for i := 0; i < n; i++ {
		svc, fs, gl := newSvc()
		ctx := ctxWith(staff("u1"))
		id := settleableClaim(t, svc, ctx, ClaimInput{BranchID: testBranch, ExpectedCents: 100000})

		type res struct {
			what string
			err  error
		}
		out := make(chan res, 2)
		start := make(chan struct{})
		go func() {
			<-start
			_, err := svc.AdvancePhase(ctx, id, PhaseTransitionInput{ToPhase: PhaseAbandoned})
			out <- res{"advance", err}
		}()
		go func() {
			<-start
			_, err := svc.RecordSettlement(ctx, id, SettlementInput{Outcome: OutcomeVendorCredit, RealizedCents: 99000})
			out <- res{"settle", err}
		}()
		close(start)

		var advErr, setErr error
		for k := 0; k < 2; k++ {
			r := <-out
			if r.what == "advance" {
				advErr = r.err
			} else {
				setErr = r.err
			}
		}

		// At most one may succeed; a loser is refused cleanly with a 4xx, never a 500 and never a silent double
		// success. A losing settle is 409 (lost the row-lock race) OR 422 (its stale-read pre-check already saw
		// the terminal phase), both are safe refusals that post nothing. A losing advance-to-exit is 422 (the
		// under-lock guard saw the committed outcome).
		if advErr == nil && setErr == nil {
			t.Fatalf("iter %d: BOTH advance(ABANDONED) and settle(VENDOR_CREDIT) succeeded, TOCTOU stranded a settled recovery on an abandoned claim", i)
		}
		if setErr != nil && status(setErr) != 409 && status(setErr) != 422 {
			t.Fatalf("iter %d: a losing settle must be a 4xx (409/422); got %v", i, setErr)
		}
		if advErr != nil && status(advErr) != 422 {
			t.Fatalf("iter %d: a losing advance-to-exit must be 422; got %v", i, advErr)
		}

		// THE INVARIANT: the committed claim is never terminal-LOST with a recovery.
		c := fs.claims[id]
		terminalLost := c.Phase == PhaseAbandoned || c.Phase == PhaseRejected
		if terminalLost && (c.Outcome != "" || c.RealizedCents != 0 || c.GLJournalID != "") {
			t.Fatalf("iter %d: STRANDED LEDGER, terminal-LOST (%s) with recovery outcome=%s realized=%d journal=%s",
				i, c.Phase, c.Outcome, c.RealizedCents, c.GLJournalID)
		}
		// An abandoned claim must have posted NO GL journal.
		if terminalLost && len(gl.posted) != 0 {
			t.Fatalf("iter %d: an abandoned claim must have NO posted journal; got %d", i, len(gl.posted))
		}
		// If settle won, the row + its single journal agree and the claim is NOT terminal-LOST.
		if setErr == nil {
			if c.Outcome != OutcomeVendorCredit || c.RealizedCents != 99000 || terminalLost {
				t.Fatalf("iter %d: settle won but row inconsistent: phase=%s outcome=%s realized=%d", i, c.Phase, c.Outcome, c.RealizedCents)
			}
			if len(gl.posted) != 1 {
				t.Fatalf("iter %d: a winning VENDOR_CREDIT settle must post exactly one journal; got %d", i, len(gl.posted))
			}
		}
	}
}

// An explicit expected_cents of 0 (a decision that zeroed a previously-estimated recovery) must be honoured, not
// treated as "omitted" and overwritten by the stale captured exposure. Guards MINOR-1 (money-field sentinel).
func TestRecordSettlement_ExplicitZeroExpectedHonoured(t *testing.T) {
	svc, fs, _ := newSvc()
	ctx := ctxWith(staff("u1"))
	id := settleableClaim(t, svc, ctx, ClaimInput{BranchID: testBranch, ExpectedCents: 120000})
	res, err := svc.RecordSettlement(ctx, id, SettlementInput{Outcome: OutcomeWriteOff, ExpectedCents: ptrI64(0), RealizedCents: 0})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExpectedCents != 0 || fs.claims[id].ExpectedCents != 0 {
		t.Errorf("an explicit expected 0 must overwrite the captured exposure; got res=%d claim=%d", res.ExpectedCents, fs.claims[id].ExpectedCents)
	}
}

// An omitted expected_cents (nil) keeps the captured exposure.
func TestRecordSettlement_OmittedExpectedKeepsCapture(t *testing.T) {
	svc, fs, _ := newSvc()
	ctx := ctxWith(staff("u1"))
	id := settleableClaim(t, svc, ctx, ClaimInput{BranchID: testBranch, ExpectedCents: 120000})
	if _, err := svc.RecordSettlement(ctx, id, SettlementInput{Outcome: OutcomeVendorCredit, RealizedCents: 100000}); err != nil {
		t.Fatal(err)
	}
	if fs.claims[id].ExpectedCents != 120000 {
		t.Errorf("an omitted expected must keep the captured exposure; got %d", fs.claims[id].ExpectedCents)
	}
}

func ptrI64(v int64) *int64 { return &v }

func ptrTime(t time.Time) *time.Time { return &t }

// settleableClaim creates a claim and walks it through the checklist gates to the SETTLEMENT phase, so a
// RecordSettlement call is in-sequence (settlement is now restricted to DECISION/SETTLEMENT, MINOR-1). Returns
// the claim id. Used by the settlement tests, which previously settled straight from INTAKE.
func settleableClaim(t *testing.T, svc *Service, ctx context.Context, in ClaimInput) string {
	t.Helper()
	c, err := svc.CreateClaim(ctx, in)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	tick := func(keys ...string) {
		for _, k := range keys {
			if _, err := svc.TickChecklist(ctx, c.ID, ChecklistTickInput{ItemKey: k, Checked: true}); err != nil {
				t.Fatalf("tick %s: %v", k, err)
			}
		}
	}
	advance := func(to string) {
		if _, err := svc.AdvancePhase(ctx, c.ID, PhaseTransitionInput{ToPhase: to}); err != nil {
			t.Fatalf("advance to %s: %v", to, err)
		}
	}
	tick("contact_captured", "product_identified", "description_captured")
	advance(PhaseTriage)
	tick("owner_assigned", "vendor_identified", "coverage_reviewed", "proof_of_purchase")
	advance(PhaseSubmitted)
	tick("photos_attached", "proof_attached", "vendor_form_sent")
	advance(PhaseWaitingOnDecision)
	tick("submission_ack")
	advance(PhaseDecision)
	return c.ID
}

// - FR-1 registry -

func TestUpsertVendorProfile_UndocumentedAllowed(t *testing.T) {
	svc, _, _ := newSvc()
	ctx := ctxWith(staff("u1"))
	p, err := svc.UpsertVendorProfile(ctx, VendorProfileInput{VendorID: "v-oaks", Status: "UNDOCUMENTED"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Status != "UNDOCUMENTED" {
		t.Errorf("the Oaks case: UNDOCUMENTED must be allowed; got %s", p.Status)
	}
}

func TestUpsertVendorProfile_DefaultsUndocumented(t *testing.T) {
	svc, _, _ := newSvc()
	p, err := svc.UpsertVendorProfile(ctxWith(staff("u1")), VendorProfileInput{VendorID: "v-x"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Status != "UNDOCUMENTED" {
		t.Errorf("a new profile should default to UNDOCUMENTED (the registry learns); got %s", p.Status)
	}
}

func TestUpsertVendorProfile_RejectsBadStatus(t *testing.T) {
	svc, _, _ := newSvc()
	_, err := svc.UpsertVendorProfile(ctxWith(staff("u1")), VendorProfileInput{VendorID: "v-x", Status: "MAYBE"})
	if err == nil {
		t.Fatal("an invalid status must be rejected")
	}
}

// - reporting end-to-end through the service -

func TestReports_ThroughService(t *testing.T) {
	svc, fs, _ := newSvc()
	// Seed two open + one won claim directly.
	fs.claims["a"] = Claim{ID: "a", ClaimNo: "WTY-1", BranchID: testBranch, VendorID: "v1", Phase: PhaseTriage, ExpectedCents: 100000, OpenedAt: "2026-06-01T00:00:00Z"}
	fs.claims["b"] = Claim{ID: "b", ClaimNo: "WTY-2", BranchID: testBranch, VendorID: "v1", Phase: PhaseClosed, Outcome: OutcomeVendorCredit, ExpectedCents: 50000, RealizedCents: 48000, OpenedAt: "2026-05-01T00:00:00Z", ClosedAt: "2026-05-21T00:00:00Z"}
	b, err := svc.Reports(ctxWith(staff("u1")), testBranch)
	if err != nil {
		t.Fatal(err)
	}
	if b.TotalOpen != 1 || b.TotalExposureCents != 100000 {
		t.Errorf("expected 1 open / 100000 exposure; got open=%d exp=%d", b.TotalOpen, b.TotalExposureCents)
	}
	if b.TimeToSettlement.SettledCount != 1 || b.TimeToSettlement.AvgDays != 20 {
		t.Errorf("expected 1 settled at 20 days; got %+v", b.TimeToSettlement)
	}
}
