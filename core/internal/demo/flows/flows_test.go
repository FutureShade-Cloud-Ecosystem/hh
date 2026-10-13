//go:build demo

package flows

import "testing"

// The table is closed: the two Counter Till flows, Manager Home's yard
// manager, the two Yard personas migration 0200 plants, and the three
// roles the roadmap added one reviewed row at a time (Payables and
// Receivables after PR 300, the Procurement Manager after PR 303, the
// Financial Controller), no more, and every entry carries the account
// facts the other lanes share.
func TestTableIsTheClosedSet(t *testing.T) {
	all := All()
	if len(all) != 13 {
		t.Fatalf("table holds %d flows, want the closed set of thirteen", len(all))
	}
	if all[0].Flow != "sales" || all[1].Flow != "sales-manager" ||
		all[2].Flow != "yard-manager" || all[3].Flow != "yard-team" ||
		all[4].Flow != "driver" ||
		all[5].Flow != "payables-receivables" ||
		all[6].Flow != "procurement-manager" || all[7].Flow != "financial-controller" ||
		all[8].Flow != "general-manager" ||
		all[9].Flow != "hr" ||
		all[10].Flow != "branch-manager" ||
		all[11].Flow != "logistics-manager" ||
		all[12].Flow != "outside-sales" {
		t.Fatalf("flow order drifted: %q, %q, %q, %q, %q, %q, %q, %q, %q, %q, %q, %q, %q", all[0].Flow, all[1].Flow, all[2].Flow, all[3].Flow, all[4].Flow, all[5].Flow, all[6].Flow, all[7].Flow, all[8].Flow, all[9].Flow, all[10].Flow, all[11].Flow, all[12].Flow)
	}
	for _, a := range all {
		if a.PlatformUserID == "" || a.Email == "" || a.DisplayName == "" || a.Label == "" || a.Role == "" {
			t.Fatalf("flow %q carries an empty fact: %+v", a.Flow, a)
		}
		switch a.Role {
		case "inside_sales", "sales_manager", "yard_manager", "yard_team", "driver", "payables_receivables", "procurement_manager", "financial_controller", "general_manager", "hr", "branch_manager", "logistics_manager", "outside_sales":
		default:
			t.Fatalf("flow %q holds role %q, want one of the table's thirteen staff roles", a.Flow, a.Role)
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
	a, ok = Get("yard-manager")
	if !ok || a.Role != "yard_manager" || a.Label != "Yard manager" {
		t.Fatalf("Get(yard-manager) = %+v %v", a, ok)
	}
	a, ok = Get("payables-receivables")
	if !ok || a.Role != "payables_receivables" || a.Label != "Payables and Receivables" {
		t.Fatalf("Get(payables-receivables) = %+v %v", a, ok)
	}
	a, ok = Get("procurement-manager")
	if !ok || a.Role != "procurement_manager" || a.Label != "Procurement manager" {
		t.Fatalf("Get(procurement-manager) = %+v %v", a, ok)
	}
	a, ok = Get("financial-controller")
	if !ok || a.Role != "financial_controller" || a.Label != "Financial Controller" {
		t.Fatalf("Get(financial-controller) = %+v %v", a, ok)
	}
	a, ok = Get("general-manager")
	if !ok || a.Role != "general_manager" || a.Label != "General Manager" || a.PlatformUserID != "de5b2000-0000-4000-9000-000000000001" {
		t.Fatalf("Get(general-manager) = %+v %v", a, ok)
	}
	a, ok = Get("hr")
	if !ok || a.Role != "hr" || a.Label != "HR" || a.PlatformUserID != "de5b2000-0000-4000-9000-000000000002" {
		t.Fatalf("Get(hr) = %+v %v", a, ok)
	}
	a, ok = Get("branch-manager")
	if !ok || a.Role != "branch_manager" || a.Label != "Branch Manager" || a.PlatformUserID != "de5b2000-0000-4000-9000-000000000003" {
		t.Fatalf("Get(branch-manager) = %+v %v", a, ok)
	}
	a, ok = Get("logistics-manager")
	if !ok || a.Role != "logistics_manager" || a.Label != "Logistics Manager" || a.PlatformUserID != "de5b2000-0000-4000-9000-000000000004" {
		t.Fatalf("Get(logistics-manager) = %+v %v", a, ok)
	}
	a, ok = Get("outside-sales")
	if !ok || a.Role != "outside_sales" || a.Label != "Outside Sales" || a.PlatformUserID != "de5b2000-0000-4000-9000-000000000005" {
		t.Fatalf("Get(outside-sales) = %+v %v", a, ok)
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
