//go:build demo

package flows

import "testing"

// The table is closed: the Counter Till pair, the Yard Team and Driver
// personas (migration 0200's planted logins) and the Financial Controller
// the roles roadmap adds, no more, and every entry carries the account
// facts the other lanes share.
func TestTableIsTheClosedSet(t *testing.T) {
	all := All()
	if len(all) != 5 {
		t.Fatalf("table holds %d flows, want the closed set of five", len(all))
	}
	if all[0].Flow != "sales" || all[1].Flow != "sales-manager" ||
		all[2].Flow != "yard-team" || all[3].Flow != "driver" ||
		all[4].Flow != "financial-controller" {
		t.Fatalf("flow order drifted: %q, %q, %q, %q, %q", all[0].Flow, all[1].Flow, all[2].Flow, all[3].Flow, all[4].Flow)
	}
	for _, a := range all {
		if a.PlatformUserID == "" || a.Email == "" || a.DisplayName == "" || a.Label == "" || a.Role == "" {
			t.Fatalf("flow %q carries an empty fact: %+v", a.Flow, a)
		}
		switch a.Role {
		case "inside_sales", "sales_manager", "yard_team", "driver", "financial_controller":
		default:
			t.Fatalf("flow %q holds role %q, want one of the table's five staff roles", a.Flow, a.Role)
		}
	}
}

// Every address sits at a domain the boot guard admits, so the database
// guard's domain check and the populate agree on what a demo account looks
// like: the reserved demo domain for the accounts the populate itself
// creates, and the migration's planted .example domain for the personas
// 0200 plants (the guard admits both, and the populate moves neither).
func TestEveryEmailSitsAtADomainTheBootGuardAdmits(t *testing.T) {
	for _, a := range All() {
		at := len(ReservedDomain) + 1
		reserved := len(a.Email) > at && a.Email[len(a.Email)-at:] == "@"+ReservedDomain
		planted := len(a.Email) > 8 && a.Email[len(a.Email)-8:] == ".example"
		if !reserved && !planted {
			t.Fatalf("flow %q email %q sits at no domain the boot guard admits", a.Flow, a.Email)
		}
	}
}

// The lookups answer the table and nothing else.
func TestLookups(t *testing.T) {
	a, ok := Get("sales")
	if !ok || a.Role != "inside_sales" {
		t.Fatalf("Get(sales) = %+v %v", a, ok)
	}
if _, ok := Get("receivables"); ok {
		t.Fatal("a flow outside the table answered")
	}
	a, ok = Get("financial-controller")
	if !ok || a.Role != "financial_controller" || a.Label != "Financial Controller" {
		t.Fatalf("Get(financial-controller) = %+v %v", a, ok)
	}
	if _, ok := Get("yard-pick"); ok {
		t.Fatal("a flow outside the table answered")
	}
	a, ok = ByPlatformUserID("de110000-0000-4000-9000-000000000002")
	if !ok || a.Flow != "sales-manager" {
		t.Fatalf("ByPlatformUserID(manager id) = %+v %v", a, ok)
	}
	a, ok = ByPlatformUserID("d0200000-0000-4000-9000-000000000002")
	if !ok || a.Flow != "yard-team" {
		t.Fatalf("ByPlatformUserID(yard persona id) = %+v %v", a, ok)
	}
	a, ok = ByPlatformUserID("d0200000-0000-4000-9000-000000000003")
	if !ok || a.Flow != "driver" {
		t.Fatalf("ByPlatformUserID(driver persona id) = %+v %v", a, ok)
	}
	if _, ok := ByPlatformUserID("someone-else"); ok {
		t.Fatal("a subject outside the table answered")
	}
}
