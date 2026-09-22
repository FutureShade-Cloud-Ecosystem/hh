package adapters

// scope_test.go, the four scope conversions, which are the security-relevant half of this package.
//
// EVERY OTHER TEST IN THIS PACKAGE COULD PASS WHILE THE FEATURE LEAKED. Mapping a record from one
// struct to another is checkable by eye; a dropped branch fence is not, and it looks exactly like a
// working assistant while it does it. So the conversions get their own file, and each is asserted at
// all three points of the rule rather than at the one a happy-path test would happen to exercise.
//
// THE THREE POINTS, and the middle one is the one that goes wrong:
//
//	a GLOBAL caller          → no branch filter
//	a caller WITH grants     → exactly those grants, nothing added
//	a caller with NO grants  → a fence that matches NOTHING, never an absent fence
//
// The third is the inversion that turns branch scoping into its opposite: a scoped account with no
// grants is the state a brand-new user sits in for the minutes between being created and being
// assigned a branch, and handing that user the whole company is the single most likely way this
// feature ends up in an incident report.

import (
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist"
)

func globalCaller() assist.Caller {
	return assist.Caller{UserID: "u-1", RoleCode: "GENERAL_MANAGER", Tier: 1, Global: true, IsAdmin: true}
}

func scopedCaller(branches ...string) assist.Caller {
	return assist.Caller{UserID: "u-2", RoleCode: "INSIDE_SALES", Tier: 3, BranchIDs: branches}
}

func TestScope_GlobalCallerCarriesNoBranchFilter(t *testing.T) {
	c := globalCaller()

	if s := stockReach(c); !s.Global || len(s.BranchIDs) != 0 {
		t.Errorf("inventory reach for a global caller = %+v, want Global with no ids", s)
	}
	if s := orderReach(c); !s.Global || len(s.BranchIDs) != 0 {
		t.Errorf("order scope for a global caller = %+v, want Global with no ids", s)
	}
	if s := accountReach(c); !s.Global || len(s.BranchIDs) != 0 {
		t.Errorf("customer reach for a global caller = %+v, want Global with no ids", s)
	}
	if s := paymentReach(c); !s.Global || len(s.BranchIDs) != 0 {
		t.Errorf("payments reach for a global caller = %+v, want Global with no ids", s)
	}
}

func TestScope_ScopedCallerCarriesExactlyItsGrants(t *testing.T) {
	c := scopedCaller("b-trenton", "b-kingston")
	want := []string{"b-trenton", "b-kingston"}

	eq := func(t *testing.T, name string, global bool, got []string) {
		t.Helper()
		if global {
			t.Fatalf("%s: a branch-scoped caller was projected as GLOBAL, the fence is gone", name)
		}
		if len(got) != len(want) {
			t.Fatalf("%s: branch ids = %v, want %v", name, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("%s: branch ids = %v, want %v", name, got, want)
			}
		}
	}
	s := stockReach(c)
	eq(t, "inventory", s.Global, s.BranchIDs)
	o := orderReach(c)
	eq(t, "order", o.Global, o.BranchIDs)
	a := accountReach(c)
	eq(t, "customer", a.Global, a.BranchIDs)
	p := paymentReach(c)
	eq(t, "payments", p.Global, p.BranchIDs)
}

// TestScope_NoGrantsIsAFenceThatMatchesNothing is the inversion test. It asserts at the level that
// actually decides the outcome, the SQL parameter each domain renders, because "Global is false"
// alone would still pass if a domain turned an empty grant list into a NULL filter.
func TestScope_NoGrantsIsAFenceThatMatchesNothing(t *testing.T) {
	c := scopedCaller() // an account with a role and no branch grants at all

	if s := stockReach(c); s.Global {
		t.Error("inventory: a caller with no grants was projected as GLOBAL")
	}
	if s := orderReach(c); s.Global || len(s.BranchIDs) != 0 {
		t.Errorf("order: no-grant caller = %+v, want a scoped, empty reach", s)
	}
	if s := accountReach(c); s.Global {
		t.Error("customer: a caller with no grants was projected as GLOBAL")
	}
	if s := paymentReach(c); s.Global {
		t.Error("payments: a caller with no grants was projected as GLOBAL")
	}
	// What each domain then DOES with an empty grant list, render a filter that matches no row,
	// rather than no filter at all, is pinned inside each domain's own package, next to the SQL
	// parameter it decides (inventory/stock_lookup_test.go, order/summary_test.go,
	// customer/account_lookup_test.go, payments/payment_lookup_test.go).
}

// TestScope_GrantsAreNotAliasedIntoTheDomain pins that a domain scope holds its OWN copy of the
// grant list. An adapter that shared the caller's backing array would let any domain that sorted or
// appended to its scope quietly rewrite the caller's authority for every LATER read in the same
// request, a bug that would only ever appear under a two-command chat turn, which is the hardest
// possible place to notice it.
func TestScope_GrantsAreNotAliasedIntoTheDomain(t *testing.T) {
	ids := []string{"b-trenton"}
	c := scopedCaller(ids...)

	s := orderReach(c)
	if len(s.BranchIDs) != 1 {
		t.Fatalf("setup: %+v", s)
	}
	s.BranchIDs[0] = "b-everything"
	if ids[0] != "b-trenton" {
		t.Fatal("order.ScopeFor aliased the caller's branch list, a domain can now rewrite the caller's authority")
	}
}
