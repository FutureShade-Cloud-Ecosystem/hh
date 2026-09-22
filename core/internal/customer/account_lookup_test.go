package customer

// account_lookup_test.go, the fence on "which accounts may I ask about", without a database.

import (
	"context"
	"errors"
	"testing"
)

// TestAccountReach_TheThreeCases, the SQL parameter the fence renders.
//
// The third case is the one that inverts it. Note that for THIS fence an empty array is not "no
// accounts": the predicate is `home_branch_id = ANY(...) OR home_branch_id IS NULL`, so a caller
// with no grants still sees the company-wide (unhomed) accounts and nothing else. That is the
// correct floor, those accounts belong to every branch by construction, and it is deliberately
// different from the stock fence, where an unhomed yard does not exist.
func TestAccountReach_TheThreeCases(t *testing.T) {
	if got := (AccountReach{Global: true}).branchFilter(); got != nil {
		t.Fatalf("global filter = %#v, want nil (no fence)", got)
	}
	got, ok := (AccountReach{BranchIDs: []string{"b-1"}}).branchFilter().([]string)
	if !ok || len(got) != 1 || got[0] != "b-1" {
		t.Fatalf("granted filter = %#v", got)
	}
	empty, ok := (AccountReach{}).branchFilter().([]string)
	if !ok || len(empty) != 0 {
		t.Fatalf("no-grant filter = %#v, want an EMPTY []string, nil would drop the fence entirely", empty)
	}
}

// -- the service, over a fake seam ---------------------------------------------------------------

type fakeLookupStore struct {
	*fakeStore
	result   SearchResult
	found    bool
	err      error
	gotRef   string
	gotReach AccountReach
	calls    int
}

func (f *fakeLookupStore) LookupAccountScoped(_ context.Context, ref string, reach AccountReach) (SearchResult, bool, error) {
	f.gotRef, f.gotReach, f.calls = ref, reach, f.calls+1
	return f.result, f.found, f.err
}

func newLookupSvc(f *fakeLookupStore) *Service {
	f.fakeStore = newFakeStore()
	return NewService(f)
}

func TestLookupAccount_CarriesTheReachIntoTheStore(t *testing.T) {
	f := &fakeLookupStore{result: SearchResult{ID: "c-1", Name: "Northgate Landscaping", AccountNo: "CUST-1001"}, found: true}
	svc := newLookupSvc(f)

	got, found, err := svc.LookupAccount(context.Background(), AccountReach{BranchIDs: []string{"b-trenton"}}, " Northgate ")
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if f.gotReach.Global {
		t.Fatal("the lookup was asked GLOBALLY for a branch-scoped caller")
	}
	if len(f.gotReach.BranchIDs) != 1 || f.gotReach.BranchIDs[0] != "b-trenton" {
		t.Fatalf("store received branches %v", f.gotReach.BranchIDs)
	}
	if f.gotRef != "Northgate" {
		t.Fatalf("store received %q, the reference should arrive trimmed", f.gotRef)
	}
	if got.ID != "c-1" {
		t.Fatalf("result = %+v", got)
	}
}

func TestLookupAccount_UnknownAccountIsAnAnswerNotAnError(t *testing.T) {
	f := &fakeLookupStore{found: false}
	svc := newLookupSvc(f)

	_, found, err := svc.LookupAccount(context.Background(), AccountReach{Global: true}, "nobody")
	if err != nil {
		t.Fatalf("an unknown account is an ANSWER: %v", err)
	}
	if found {
		t.Fatal("found=true for an account that did not resolve")
	}
}

func TestLookupAccount_AnEmptyReferenceAsksNothing(t *testing.T) {
	f := &fakeLookupStore{}
	svc := newLookupSvc(f)

	_, found, err := svc.LookupAccount(context.Background(), AccountReach{Global: true}, "   ")
	if err != nil || found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if f.calls != 0 {
		t.Error("an empty reference reached the store")
	}
}

// TestLookupAccount_AStoreThatCannotFenceRefusesRatherThanLeaks mirrors SearchAccounts' own
// posture: a store with no fenced query must not be asked to serve a fenced read, because
// SearchResult carries no home branch and "best effort" would mean returning every branch's
// accounts.
func TestLookupAccount_AStoreThatCannotFenceRefusesRatherThanLeaks(t *testing.T) {
	svc := NewService(newFakeStore()) // the base fake implements accountStore only

	_, _, err := svc.LookupAccount(context.Background(), AccountReach{BranchIDs: []string{"b-trenton"}}, "Northgate")
	if !errors.Is(err, ErrLookupNotConfigured) {
		t.Fatalf("err = %v, want ErrLookupNotConfigured", err)
	}
}
