package order

// summary_test.go, the caller-scoped order summary, without a database.
//
// THE TEST THIS FILE EXISTS FOR is TestSummaryByNumber_ACrossBranchUUIDDoesNotSlipThrough.
// ResolveOrderID passes a UUID through WITHOUT a round trip, that is its documented contract, and
// it is correct for the deep-link flow it was written for, where the branch check happens on the
// loaded order downstream. A read that resolves and then trusts the result would therefore be
// branch-scoped for order NUMBERS and wide open for order IDS, which is the worst kind of fence:
// one that passes every test written about the path people are expected to take.

import (
	"context"
	"testing"
	"time"
)

// summaryFake is the base fakeStore plus the summary seam.
type summaryFake struct {
	*fakeStore
	summary  Summary
	sumErr   error
	resolved string
	resErr   error

	gotIDOrNo  string
	gotGlobal  bool
	gotBranchs []string
}

func (f *summaryFake) ResolveOrderID(_ context.Context, idOrNo string, global bool, branchIDs []string) (string, error) {
	f.gotIDOrNo, f.gotGlobal, f.gotBranchs = idOrNo, global, branchIDs
	if f.resErr != nil {
		return "", f.resErr
	}
	if f.resolved != "" {
		return f.resolved, nil
	}
	return idOrNo, nil
}

func (f *summaryFake) OrderSummary(_ context.Context, _ string) (Summary, error) {
	return f.summary, f.sumErr
}

func newSummarySvc(f *summaryFake) *Service {
	f.fakeStore = &fakeStore{}
	return NewService(f)
}

func aSummary() Summary {
	return Summary{
		OrderID: "o-1", OrderNo: "#SO-4", Status: "PICKING", CustomerName: "Northgate Landscaping",
		BranchID: "b-trenton", BranchName: "Trenton", TotalCents: 512300,
		PlacedAt: time.Date(2026, 8, 11, 9, 30, 0, 0, time.UTC), LineCount: 3,
	}
}

func TestScopeFor_ProjectsACallerTheSameWayCallerBranchScopeDoes(t *testing.T) {
	if s := ScopeFor(true, []string{"ignored"}); !s.Global || len(s.BranchIDs) != 0 {
		t.Fatalf("a global caller = %+v, want Global with no ids", s)
	}
	s := ScopeFor(false, []string{"b-1", "b-2"})
	if s.Global || len(s.BranchIDs) != 2 {
		t.Fatalf("a scoped caller = %+v", s)
	}
	// No grants must NOT become "global". This is the inversion that turns the fence inside out.
	if n := ScopeFor(false, nil); n.Global {
		t.Fatal("a caller with no branch grants was projected as GLOBAL")
	}
}

func TestReadScope_Reaches(t *testing.T) {
	if !(ReadScope{Global: true}).reaches("anything") {
		t.Error("a global scope must reach every branch")
	}
	s := ReadScope{BranchIDs: []string{"b-1"}}
	if !s.reaches("b-1") {
		t.Error("a granted branch must be reachable")
	}
	if s.reaches("b-2") {
		t.Error("an ungranted branch was reachable")
	}
	if (ReadScope{}).reaches("b-1") || (ReadScope{}).reaches("") {
		t.Error("a scope with no grants reached a branch, including the empty branch id a zero-value record carries")
	}
}

func TestSummaryByNumber_ResolvesWithinTheCallersBranches(t *testing.T) {
	f := &summaryFake{summary: aSummary(), resolved: "o-1"}
	svc := newSummarySvc(f)

	sum, found, err := svc.SummaryByNumber(context.Background(), ReadScope{BranchIDs: []string{"b-trenton"}}, "SO-4")
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if f.gotIDOrNo != "SO-4" {
		t.Fatalf("resolve received %q", f.gotIDOrNo)
	}
	if f.gotGlobal {
		t.Fatal("the resolve was asked GLOBALLY for a branch-scoped caller, the #SO number space becomes an existence oracle")
	}
	if len(f.gotBranchs) != 1 || f.gotBranchs[0] != "b-trenton" {
		t.Fatalf("resolve received branches %v", f.gotBranchs)
	}
	if sum.OrderNo != "#SO-4" || sum.CustomerName != "Northgate Landscaping" || sum.TotalCents != 512300 {
		t.Fatalf("summary = %+v", sum)
	}
}

// TestSummaryByNumber_ACrossBranchUUIDDoesNotSlipThrough, see the file header. The fake resolves
// the id verbatim (as ResolveOrderID does for a UUID) and the loaded order belongs to a branch the
// caller cannot reach. Without the post-load branch check this returns another branch's order.
func TestSummaryByNumber_ACrossBranchUUIDDoesNotSlipThrough(t *testing.T) {
	kingstonOrder := aSummary()
	kingstonOrder.BranchID, kingstonOrder.BranchName = "b-kingston", "Kingston"
	f := &summaryFake{summary: kingstonOrder}
	svc := newSummarySvc(f)

	sum, found, err := svc.SummaryByNumber(context.Background(),
		ReadScope{BranchIDs: []string{"b-trenton"}}, "11111111-2222-4333-8444-555555555555")
	if err != nil {
		t.Fatalf("a cross-branch order is an ANSWER (not found), not an error: %v", err)
	}
	if found {
		t.Fatalf("a Trenton-scoped caller read a Kingston order by pasting its id: %+v", sum)
	}
}

func TestSummaryByNumber_AGlobalCallerReachesEveryBranch(t *testing.T) {
	kingstonOrder := aSummary()
	kingstonOrder.BranchID, kingstonOrder.BranchName = "b-kingston", "Kingston"
	f := &summaryFake{summary: kingstonOrder}
	svc := newSummarySvc(f)

	_, found, err := svc.SummaryByNumber(context.Background(), ReadScope{Global: true}, "SO-4")
	if err != nil || !found {
		t.Fatalf("a global caller must reach every branch: found=%v err=%v", found, err)
	}
}

func TestSummaryByNumber_UnknownNumberIsAnAnswerNotAnError(t *testing.T) {
	f := &summaryFake{resErr: ErrNotFound}
	svc := newSummarySvc(f)

	_, found, err := svc.SummaryByNumber(context.Background(), ReadScope{Global: true}, "SO-9999")
	if err != nil {
		t.Fatalf("an unknown order number is an ANSWER: %v", err)
	}
	if found {
		t.Fatal("found=true for a number that did not resolve")
	}
}

func TestSummaryByNumber_AnEmptyNumberAsksNothing(t *testing.T) {
	f := &summaryFake{summary: aSummary()}
	svc := newSummarySvc(f)

	_, found, err := svc.SummaryByNumber(context.Background(), ReadScope{Global: true}, "  ")
	if err != nil || found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if f.gotIDOrNo != "" {
		t.Error("an empty number reached the store")
	}
}
