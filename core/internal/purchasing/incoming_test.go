package purchasing

// incoming_test.go, DIB-460's read, at the level that decides what it is allowed to see.
//
// The SQL is proved in incoming_integration_test.go. What is proved HERE is the thing that decides
// what the SQL is handed: the BranchReach the service builds from the caller. That is one value with
// three meanings, and the third one, a scoped caller with NO grants, inverts the entire fence while
// leaving a working-looking screen behind. So the fake RECORDS the reach it was handed (the DIB-431b
// pattern) and the assertions are about that recording, not about rows.

import (
	"context"

	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// - the fake read seam: it records the reach it was handed -

type fakeIncoming struct {
	rows []IncomingPOLine
	err  error

	gotProductID string
	gotReach     BranchReach
	queried      bool

	gotExpectedPO   string
	gotExpectedDate string
	updated         bool
}

func (f *fakeIncoming) OpenPOLinesForProduct(_ context.Context, productID string, reach BranchReach) ([]IncomingPOLine, error) {
	f.gotProductID, f.gotReach, f.queried = productID, reach, true
	return f.rows, f.err
}

func (f *fakeIncoming) UpdateExpectedAt(_ context.Context, poID, expectedAt string) error {
	f.gotExpectedPO, f.gotExpectedDate, f.updated = poID, expectedAt, true
	return f.err
}

// svcAs builds a service whose caller is exactly this StaffContext, over the recording fake.
func svcAs(f *fakeIncoming, c identity.StaffContext) *Service {
	return NewService(nil).
		withCaller(func(context.Context) (identity.StaffContext, bool) { return c, true }).
		withIncomingStore(f)
}

func dib460GlobalCaller() identity.StaffContext {
	return identity.StaffContext{UserID: "u-1", Tier: identity.TierLeadership, Scope: identity.ScopeGlobal}
}

func dib460ScopedCaller(branches ...string) identity.StaffContext {
	m := map[string]bool{}
	for _, b := range branches {
		m[b] = true
	}
	return identity.StaffContext{UserID: "u-2", Tier: identity.TierFrontline, Scope: identity.ScopeBranch, Branches: m}
}

// - the reach, as a value -

func TestBranchReach_TheThreeCases(t *testing.T) {
	t.Run("global reaches everything", func(t *testing.T) {
		if got := (BranchReach{Global: true}).branchFilter(); got != nil {
			t.Fatalf("filter = %#v, want nil (SQL NULL → no branch predicate)", got)
		}
	})
	t.Run("grants reach exactly those branches", func(t *testing.T) {
		got, ok := (BranchReach{BranchIDs: []string{"b-1", "b-2"}}).branchFilter().([]string)
		if !ok || len(got) != 2 || got[0] != "b-1" || got[1] != "b-2" {
			t.Fatalf("filter = %#v, want [b-1 b-2]", got)
		}
	})
	// THE ONE THAT INVERTS THE FENCE. nil here renders SQL NULL, the predicate short-circuits true,
	// and a caller with no branch grants is handed every purchase order in the company, while the
	// screen looks entirely normal.
	t.Run("no grants reach nothing", func(t *testing.T) {
		got, ok := (BranchReach{}).branchFilter().([]string)
		if !ok {
			t.Fatalf("filter = %#v, want an EMPTY []string, nil would hand the whole company to a caller with no grants", got)
		}
		if len(got) != 0 {
			t.Fatalf("filter = %#v, want an empty list", got)
		}
	})
}

// - the reach the SERVICE hands the seam -

// TestIncoming_GlobalCallerReachesEveryBranch, the seam is handed Global, not a list of branches.
func TestIncoming_GlobalCallerReachesEveryBranch(t *testing.T) {
	f := &fakeIncoming{}
	svc := svcAs(f, dib460GlobalCaller())

	if _, err := svc.IncomingForProduct(context.Background(), "p-1", ""); err != nil {
		t.Fatalf("read: %v", err)
	}
	if !f.queried {
		t.Fatal("the read seam was never reached")
	}
	if !f.gotReach.Global {
		t.Fatalf("reach = %#v, want Global, a GLOBAL caller must not be narrowed to a branch list", f.gotReach)
	}
	if f.gotProductID != "p-1" {
		t.Fatalf("product id = %q", f.gotProductID)
	}
}

// TestIncoming_ScopedCallerReachesOnlyItsGrants, the ordinary case, and it must be the GRANTS and
// not "the first one" or "all of them".
func TestIncoming_ScopedCallerReachesOnlyItsGrants(t *testing.T) {
	f := &fakeIncoming{}
	svc := svcAs(f, dib460ScopedCaller("b-kgn", "b-trt"))

	if _, err := svc.IncomingForProduct(context.Background(), "p-1", ""); err != nil {
		t.Fatalf("read: %v", err)
	}
	if f.gotReach.Global {
		t.Fatal("a BRANCH-scoped caller was handed a GLOBAL reach, the fence is inverted")
	}
	if len(f.gotReach.BranchIDs) != 2 || f.gotReach.BranchIDs[0] != "b-kgn" || f.gotReach.BranchIDs[1] != "b-trt" {
		t.Fatalf("reach = %#v, want both grants, sorted", f.gotReach.BranchIDs)
	}
}

// TestIncoming_CallerWithNoGrantsReachesNothing is the inversion test, at the seam.
//
// The answer is an EMPTY LIST rather than a 403 on purpose (see reachFor's doc comment): "nothing is
// on order in the yards you can see" is true, actionable and quiet, where a refusal on a read is a
// broken card. What must NEVER happen is the third possibility, a reach that matches everything.
func TestIncoming_CallerWithNoGrantsReachesNothing(t *testing.T) {
	f := &fakeIncoming{rows: []IncomingPOLine{{PONo: "#PO-TRT-1"}}}
	svc := svcAs(f, dib460ScopedCaller())

	rows, err := svc.IncomingForProduct(context.Background(), "p-1", "")
	if err != nil {
		t.Fatalf("a caller with no grants gets an ANSWER, not an error: %v", err)
	}
	if !f.queried {
		t.Fatal("the read seam was never reached, the fence must be proved where the query is made")
	}
	if f.gotReach.Global {
		t.Fatal("a caller with NO branch grants was handed a GLOBAL reach, every PO in the company")
	}
	if !f.gotReach.reachesNothing() {
		t.Fatalf("reach = %#v, want a reach that matches nothing", f.gotReach)
	}
	// The fake ignores the reach (that is what makes it a fake); the real store's SQL renders it as
	// `= ANY('{}')`. What is asserted here is the PARAMETER, which is the only part the service owns.
	_ = rows
}

// TestIncoming_ExplicitBranchNarrowsWithinTheReach, asking about one of your branches narrows.
func TestIncoming_ExplicitBranchNarrowsWithinTheReach(t *testing.T) {
	f := &fakeIncoming{}
	svc := svcAs(f, dib460ScopedCaller("b-kgn", "b-trt"))

	if _, err := svc.IncomingForProduct(context.Background(), "p-1", "b-trt"); err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(f.gotReach.BranchIDs) != 1 || f.gotReach.BranchIDs[0] != "b-trt" {
		t.Fatalf("reach = %#v, want exactly the requested branch", f.gotReach.BranchIDs)
	}
}

// TestIncoming_ExplicitBranchOutsideTheReachIsForbidden, asking about a yard you cannot see is a
// forbidden QUESTION, not an empty answer: an empty answer would let someone map the company by
// guessing branch ids and reading which ones come back "empty" versus "denied".
func TestIncoming_ExplicitBranchOutsideTheReachIsForbidden(t *testing.T) {
	f := &fakeIncoming{}
	svc := svcAs(f, dib460ScopedCaller("b-trt"))

	_, err := svc.IncomingForProduct(context.Background(), "p-1", "b-kgn")
	if err == nil {
		t.Fatal("asking about an ungranted branch must be refused")
	}
	if f.queried {
		t.Fatal("the query ran for a branch the caller cannot see")
	}
}

// TestIncoming_UnauthenticatedIsRefusedBeforeTheQuery, no caller, no read.
func TestIncoming_UnauthenticatedIsRefusedBeforeTheQuery(t *testing.T) {
	f := &fakeIncoming{}
	svc := NewService(nil).
		withCaller(func(context.Context) (identity.StaffContext, bool) { return identity.StaffContext{}, false }).
		withIncomingStore(f)

	if _, err := svc.IncomingForProduct(context.Background(), "p-1", ""); err == nil {
		t.Fatal("an unauthenticated read must be refused")
	}
	if f.queried {
		t.Fatal("the query ran without a caller")
	}
}

func TestIncoming_ProductIDIsRequired(t *testing.T) {
	f := &fakeIncoming{}
	svc := svcAs(f, dib460GlobalCaller())
	if _, err := svc.IncomingForProduct(context.Background(), "  ", ""); err == nil {
		t.Fatal("a read with no product is a 400, not a scan of every open PO")
	}
	if f.queried {
		t.Fatal("the query ran with no product id")
	}
}

// - the definition of "on order" -

// TestOpenPOStatuses_AreTheUnfinishedOnes pins the status set the SQL is parameterised with.
//
// It is written out longhand rather than derived, because a test that derives its expectation from
// the code under test asserts nothing. Adding RECEIVED or CANCELLED here would report stock as
// coming that is already in the yard or was never bought, the exact double-order the DIB-72
// worklist exists to prevent, arriving through a different screen.
func TestOpenPOStatuses_AreTheUnfinishedOnes(t *testing.T) {
	want := map[string]bool{StatusDraft: true, StatusOrdered: true, StatusPartial: true}
	if len(openPOStatuses) != len(want) {
		t.Fatalf("openPOStatuses = %v, want exactly DRAFT, ORDERED, PARTIAL", openPOStatuses)
	}
	for _, s := range openPOStatuses {
		if !want[s] {
			t.Fatalf("%q counts as on-order; RECEIVED/CLOSED/CANCELLED have nothing coming", s)
		}
	}
}

// - the expected-date edit path -

func TestCanSetExpectedDate_OnlyWhileSomethingIsStillComing(t *testing.T) {
	for _, s := range []string{StatusDraft, StatusOrdered, StatusPartial} {
		if !CanSetExpectedDate(s) {
			t.Errorf("%s still has something coming, its date must be editable", s)
		}
	}
	for _, s := range []string{StatusReceived, StatusClosed, StatusCancelled} {
		if CanSetExpectedDate(s) {
			t.Errorf("%s is finished, editing its promised date rewrites history nobody is waiting on", s)
		}
	}
}

func TestSetExpectedDate_RejectsAShapeThatIsNotADate(t *testing.T) {
	for _, bad := range []string{"19 Aug", "2026/08/19", "Aug 19 2026", "2026-8-19", "20260819", "tomorrow"} {
		f := &fakeIncoming{}
		svc := svcAs(f, dib460GlobalCaller())
		_, err := svc.SetExpectedDate(context.Background(), "po-1", bad)
		if err == nil {
			t.Errorf("%q was accepted as a date", bad)
		}
		if f.updated {
			t.Errorf("%q reached the database", bad)
		}
	}
}

func TestIsISODate_AcceptsTheShapeAndNothingElse(t *testing.T) {
	if !isISODate("2026-08-19") {
		t.Error("a real date was rejected")
	}
	// The guard is a SHAPE check; Postgres remains the authority on whether the calendar has this
	// day. What matters is that a non-date becomes a 400 here rather than a 500 out of the driver.
	if !isISODate("2026-02-30") {
		t.Error("the shape guard must not try to be a calendar")
	}
	for _, bad := range []string{"", "2026-08-1", "2026-08-190", "x026-08-19", "2026:08:19"} {
		if isISODate(bad) {
			t.Errorf("%q passed the shape guard", bad)
		}
	}
}

// TestSetExpectedDate_RefusesWithoutAPOID, refused before anything is read or written.
func TestSetExpectedDate_RefusesWithoutAPOID(t *testing.T) {
	f := &fakeIncoming{}
	svc := svcAs(f, dib460GlobalCaller())
	if _, err := svc.SetExpectedDate(context.Background(), "  ", "2026-08-19"); err == nil {
		t.Fatal("a set with no purchase order is a 400")
	}
	if f.updated {
		t.Fatal("an UPDATE ran with no purchase order id")
	}
}

// The CLEAR case (an empty date, for a withdrawn vendor promise) has to read the PO to fence it, so
// it is proved end to end in incoming_integration_test.go against a real row rather than mimed here.

// - the omitted UOM (found by DIB-461's walkthrough) -

// TestValidateLine_AnOmittedUOMIsAllowedBecauseTheSeamSpeaksBaseQuantities.
//
// This is a FLIPPED requirement, and the reason is worth keeping. validateLine used to refuse a line
// with no uom, which reads as careful, every quantity carries a unit is the house rule. But
// inventory's POCreator seam (the DIB-72 create-PO handoff) carries BASE quantities and no unit,
// because the unit a base quantity is in is a fact about the PRODUCT. So the composition-root
// adapter sent none, and "create a purchase order from the shortfall worklist" 500'd for every user
// since the union, with "internal error" as its only symptom.
//
// The line is still never written unitless: InsertPOLine resolves the product's base_uom in SQL.
func TestValidateLine_AnOmittedUOMIsAllowedBecauseTheSeamSpeaksBaseQuantities(t *testing.T) {
	err := validateLine(AddLineParams{ProductID: "p-1", QtyOrderedBase: 120}, 1)
	if err != nil {
		t.Fatalf("a line with no uom must be accepted (the base unit is resolved from the product): %v", err)
	}
	// The other three guards are untouched, this widened ONE field, not the validation.
	if validateLine(AddLineParams{QtyOrderedBase: 1, UOM: "EA"}, 1) == nil {
		t.Error("a line with no product_id was accepted")
	}
	if validateLine(AddLineParams{ProductID: "p-1", QtyOrderedBase: 0, UOM: "EA"}, 1) == nil {
		t.Error("a line with no quantity was accepted")
	}
	if validateLine(AddLineParams{ProductID: "p-1", QtyOrderedBase: 1, UOM: "EA", UnitCostCents: -1}, 1) == nil {
		t.Error("a line with a negative unit cost was accepted")
	}
}
