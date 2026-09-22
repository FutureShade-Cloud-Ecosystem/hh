package payments

// payment_lookup_test.go, the three shapes, the fence, and the refusal, without a database.

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestBranchReach_TheThreeCases, the SQL parameter the fence renders. The third case is the one
// that inverts it: nil would render SQL NULL, the predicate would short-circuit true, and a caller
// with no branch grants would be handed every till in the company.
func TestBranchReach_TheThreeCases(t *testing.T) {
	if got := (BranchReach{Global: true}).branchFilter(); got != nil {
		t.Fatalf("global filter = %#v, want nil", got)
	}
	got, ok := (BranchReach{BranchIDs: []string{"b-1", "b-2"}}).branchFilter().([]string)
	if !ok || len(got) != 2 {
		t.Fatalf("granted filter = %#v", got)
	}
	empty, ok := (BranchReach{}).branchFilter().([]string)
	if !ok || len(empty) != 0 {
		t.Fatalf("no-grant filter = %#v, want an EMPTY []string", empty)
	}
}

// - the service, over a fake seam -

type lookupFake struct {
	*fakeStore
	rows     []PaymentMatch
	err      error
	gotQuery LookupQuery
	gotReach BranchReach
	calls    int
}

func (f *lookupFake) FindPayments(_ context.Context, q LookupQuery, reach BranchReach) ([]PaymentMatch, error) {
	f.gotQuery, f.gotReach, f.calls = q, reach, f.calls+1
	return f.rows, f.err
}

var payAt = time.Date(2026, 8, 11, 9, 30, 0, 0, time.UTC)

func newLookupSvc(rows []PaymentMatch) (*Service, *lookupFake) {
	f := &lookupFake{fakeStore: newFakeStore(), rows: rows}
	return NewService(f, NewStubGateway()), f
}

func aMatch() PaymentMatch {
	return PaymentMatch{
		ID: "pay-1", PaymentNo: "PMT-1042", GatewayRef: "gw_88x", CustomerName: "Northgate Landscaping",
		BranchName: "Trenton", Method: "CHEQUE", Status: "RECEIVED", AmountCents: 124000, ReceivedAt: payAt,
	}
}

// TestFindPayments_AllThreeShapesAndTheirCombination, each fact reaches the store as itself, and
// two facts together narrow rather than replace each other.
func TestFindPayments_AllThreeShapesAndTheirCombination(t *testing.T) {
	cases := []struct {
		name string
		q    LookupQuery
	}{
		{"by reference", LookupQuery{Reference: "PMT-1042"}},
		{"by exact amount", LookupQuery{AmountCents: 124000}},
		{"by customer", LookupQuery{CustomerRef: "Northgate"}},
		{"amount and customer together", LookupQuery{AmountCents: 124000, CustomerRef: "Northgate"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, f := newLookupSvc([]PaymentMatch{aMatch()})
			rows, err := svc.FindPayments(context.Background(), BranchReach{Global: true}, tc.q)
			if err != nil {
				t.Fatalf("FindPayments: %v", err)
			}
			if len(rows) != 1 {
				t.Fatalf("rows = %d", len(rows))
			}
			if f.gotQuery.Reference != tc.q.Reference ||
				f.gotQuery.AmountCents != tc.q.AmountCents ||
				f.gotQuery.CustomerRef != tc.q.CustomerRef {
				t.Fatalf("the store received %+v, want %+v", f.gotQuery, tc.q)
			}
		})
	}
}

// TestFindPayments_TheAmountIsExact is the mutation target. The service must hand the store the
// EXACT cents it was given, no rounding, no widening, no tolerance. A person looking for $1,240.00
// who is shown $1,240.50 has been told the money arrived when it did not, and that ends an
// investigation which should have continued.
func TestFindPayments_TheAmountIsExact(t *testing.T) {
	svc, f := newLookupSvc([]PaymentMatch{aMatch()})
	if _, err := svc.FindPayments(context.Background(), BranchReach{Global: true}, LookupQuery{AmountCents: 124000}); err != nil {
		t.Fatalf("FindPayments: %v", err)
	}
	if f.gotQuery.AmountCents != 124000 {
		t.Fatalf("the store was asked for %d cents, want exactly 124000", f.gotQuery.AmountCents)
	}
}

func TestFindPayments_AnUnfilteredLookupIsRefused(t *testing.T) {
	svc, f := newLookupSvc(nil)
	_, err := svc.FindPayments(context.Background(), BranchReach{Global: true}, LookupQuery{Limit: 10})
	if !errors.Is(err, ErrUnfilteredLookup) {
		t.Fatalf("err = %v, want ErrUnfilteredLookup, an unfiltered payment list is a report, not a lookup", err)
	}
	if f.calls != 0 {
		t.Error("an unfiltered query reached the store")
	}
	// Whitespace is not a filter either.
	if _, err := svc.FindPayments(context.Background(), BranchReach{Global: true}, LookupQuery{Reference: "   ", CustomerRef: "  "}); !errors.Is(err, ErrUnfilteredLookup) {
		t.Fatalf("whitespace-only filters = %v, want ErrUnfilteredLookup", err)
	}
}

func TestFindPayments_CarriesTheReachAndTrimsTheFilters(t *testing.T) {
	svc, f := newLookupSvc([]PaymentMatch{aMatch()})
	if _, err := svc.FindPayments(context.Background(), BranchReach{BranchIDs: []string{"b-trenton"}},
		LookupQuery{Reference: "  PMT-1042 ", CustomerRef: " Northgate "}); err != nil {
		t.Fatalf("FindPayments: %v", err)
	}
	if f.gotReach.Global {
		t.Fatal("the read was asked GLOBALLY for a branch-scoped caller")
	}
	if len(f.gotReach.BranchIDs) != 1 || f.gotReach.BranchIDs[0] != "b-trenton" {
		t.Fatalf("store received branches %v", f.gotReach.BranchIDs)
	}
	if f.gotQuery.Reference != "PMT-1042" || f.gotQuery.CustomerRef != "Northgate" {
		t.Fatalf("filters reached the store untrimmed: %+v", f.gotQuery)
	}
}

// TestFindPayments_NoGrantsSeeNoTills, short-circuited before the store, because a
// `= ANY('{}')` that can never match is still a round trip.
func TestFindPayments_NoGrantsSeeNoTills(t *testing.T) {
	svc, f := newLookupSvc([]PaymentMatch{aMatch()})
	rows, err := svc.FindPayments(context.Background(), BranchReach{}, LookupQuery{Reference: "PMT-1042"})
	if err != nil {
		t.Fatalf("a caller with no grants gets an empty ANSWER, not an error: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("a caller with no branch grants saw %d payments", len(rows))
	}
	if f.calls != 0 {
		t.Error("a query was issued for a caller who can reach no till")
	}
}

func TestFindPayments_TheLimitIsBounded(t *testing.T) {
	svc, f := newLookupSvc(nil)
	for _, in := range []int{0, -3, 5000} {
		if _, err := svc.FindPayments(context.Background(), BranchReach{Global: true},
			LookupQuery{Reference: "PMT-1", Limit: in}); err != nil {
			t.Fatal(err)
		}
		if f.gotQuery.Limit != 10 {
			t.Fatalf("limit %d became %d, want the default 10", in, f.gotQuery.Limit)
		}
	}
	if _, err := svc.FindPayments(context.Background(), BranchReach{Global: true},
		LookupQuery{Reference: "PMT-1", Limit: 3}); err != nil {
		t.Fatal(err)
	}
	if f.gotQuery.Limit != 3 {
		t.Fatalf("an in-range limit was rewritten to %d", f.gotQuery.Limit)
	}
}

func TestFindPayments_NoMatchesIsAnAnswerNotAnError(t *testing.T) {
	svc, _ := newLookupSvc(nil)
	rows, err := svc.FindPayments(context.Background(), BranchReach{Global: true}, LookupQuery{Reference: "PMT-9"})
	if err != nil {
		t.Fatalf("no matches is an ANSWER: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("rows = %d", len(rows))
	}
}

func TestFindPayments_AStoreWithoutTheSeamSaysSo(t *testing.T) {
	svc := NewService(newFakeStore(), NewStubGateway())
	_, err := svc.FindPayments(context.Background(), BranchReach{Global: true}, LookupQuery{Reference: "PMT-1"})
	if !errors.Is(err, ErrPaymentLookupNotConfigured) {
		t.Fatalf("err = %v, want ErrPaymentLookupNotConfigured", err)
	}
}
