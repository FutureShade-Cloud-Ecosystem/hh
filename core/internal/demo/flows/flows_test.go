//go:build demo

package flows

import "testing"

// The table is closed: exactly the two Counter Till flows, no more, and
// every entry carries the account facts the other lanes share.
func TestTableIsTheClosedPair(t *testing.T) {
	all := All()
	if len(all) != 2 {
		t.Fatalf("table holds %d flows, want the closed pair", len(all))
	}
	if all[0].Flow != "sales" || all[1].Flow != "sales-manager" {
		t.Fatalf("flow order drifted: %q, %q", all[0].Flow, all[1].Flow)
	}
	for _, a := range all {
		if a.PlatformUserID == "" || a.Email == "" || a.DisplayName == "" || a.Label == "" || a.Role == "" {
			t.Fatalf("flow %q carries an empty fact: %+v", a.Flow, a)
		}
		if a.Role != "inside_sales" && a.Role != "sales_manager" {
			t.Fatalf("flow %q holds role %q, want a Counter Till staff role", a.Flow, a.Role)
		}
	}
}

// Every address sits at the reserved domain, so the database guard's
// domain check and the populate agree on what a demo account looks like.
func TestEveryEmailIsAtTheReservedDomain(t *testing.T) {
	for _, a := range All() {
		if len(a.Email) <= len(ReservedDomain)+1 || a.Email[len(a.Email)-len(ReservedDomain):] != ReservedDomain {
			t.Fatalf("flow %q email %q is not at the reserved domain %q", a.Flow, a.Email, ReservedDomain)
		}
	}
}

// The lookups answer the table and nothing else.
func TestLookups(t *testing.T) {
	a, ok := Get("sales")
	if !ok || a.Role != "inside_sales" {
		t.Fatalf("Get(sales) = %+v %v", a, ok)
	}
	if _, ok := Get("yard-pick"); ok {
		t.Fatal("a flow outside the table answered")
	}
	a, ok = ByPlatformUserID("de110000-0000-4000-9000-000000000002")
	if !ok || a.Flow != "sales-manager" {
		t.Fatalf("ByPlatformUserID(manager id) = %+v %v", a, ok)
	}
	if _, ok := ByPlatformUserID("someone-else"); ok {
		t.Fatal("a subject outside the table answered")
	}
}
