//go:build demo

package flows

import "testing"

// The contractor flows: Summit Ridge Builders' five personas, one per role
// the core stores, in the table's order after the staff rows.
func TestTenantFlowsAreSummitRidgeBuildersFivePersonas(t *testing.T) {
	want := []struct{ flow, role, label string }{
		{"contractor-account-admin", "account_admin", "Contractor owner"},
		{"contractor-buyer", "buyer", "Contractor buyer"},
		{"contractor-buyer-no-pay", "buyer_no_pay", "Contractor buyer (no pay)"},
		{"contractor-ap-finance", "ap_finance", "Contractor AP"},
		{"contractor-field-crew", "field_crew", "Contractor field"},
	}
	all := All()
	if len(all) != 14 {
		t.Fatalf("table holds %d flows, want 9 staff and 5 contractor", len(all))
	}
	for i, w := range want {
		a := all[9+i]
		if a.Flow != w.flow || a.Role != w.role || a.Label != w.label {
			t.Fatalf("row %d = %q %q %q, want %q %q %q", 9+i, a.Flow, a.Role, a.Label, w.flow, w.role, w.label)
		}
		if a.Class() != ClassTenant {
			t.Fatalf("%s class = %q, want tenant", a.Flow, a.Class())
		}
		if a.Tenant != TenantAccountID {
			t.Fatalf("%s tenant = %q, want the one Summit Ridge Builders account %q", a.Flow, a.Tenant, TenantAccountID)
		}
		if a.Email != w.role+"@"+ReservedDomain {
			t.Fatalf("%s email = %q, want <role>@%s", a.Flow, a.Email, ReservedDomain)
		}
	}
}

// A staff row never carries a tenant account, and a tenant row always does.
func TestClassFollowsTheTenantAccount(t *testing.T) {
	for _, a := range All() {
		if (a.Tenant != "") != (a.Class() == ClassTenant) {
			t.Fatalf("%s: tenant %q but class %q", a.Flow, a.Tenant, a.Class())
		}
		if a.Tenant == "" && a.Class() != ClassStaff {
			t.Fatalf("%s: a row with no tenant account must be staff, got %q", a.Flow, a.Class())
		}
	}
}

// Every platform user id and every address is unique across the whole
// table, so a lookup by subject can never answer a second flow.
func TestTableFactsAreUnique(t *testing.T) {
	ids := map[string]string{}
	emails := map[string]string{}
	for _, a := range All() {
		if prev, dup := ids[a.PlatformUserID]; dup {
			t.Fatalf("platform user id of %s repeats %s", a.Flow, prev)
		}
		ids[a.PlatformUserID] = a.Flow
		if prev, dup := emails[a.Email]; dup {
			t.Fatalf("email of %s repeats %s", a.Flow, prev)
		}
		emails[a.Email] = a.Flow
		if a.Tenant == a.PlatformUserID {
			t.Fatalf("%s: the account id and the user id must differ", a.Flow)
		}
	}
}

func TestTenantLookups(t *testing.T) {
	a, ok := Get("contractor-buyer-no-pay")
	if !ok || a.Role != "buyer_no_pay" || a.Class() != ClassTenant {
		t.Fatalf("Get(contractor-buyer-no-pay) = %+v %v", a, ok)
	}
	b, ok := ByPlatformUserID(a.PlatformUserID)
	if !ok || b.Flow != a.Flow {
		t.Fatalf("ByPlatformUserID = %+v %v", b, ok)
	}
}

// The jobs the tenant world plants, named in the tenant sheet.
func TestTenantJobsAreTheThreeSites(t *testing.T) {
	jobs := TenantJobs()
	if len(jobs) != 3 {
		t.Fatalf("%d jobs, want 3", len(jobs))
	}
	for i, area := range []string{"La Jolla", "Santa Fe", "Point Loma"} {
		if jobs[i].Area != area {
			t.Fatalf("job %d area = %q, want %q", i, jobs[i].Area, area)
		}
		if jobs[i].ID == "" || jobs[i].Name == "" || jobs[i].Address == "" {
			t.Fatalf("job %d carries an empty fact: %+v", i, jobs[i])
		}
	}
}
