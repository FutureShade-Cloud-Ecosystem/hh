package sync

import (
	"os"
	"regexp"
	"slices"
	"sort"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
)

// staffRoleRows reads the staff_role seed rows from migration 0215, the
// table the identity layer grants from, so the fence tests enumerate the
// real rows and never a hand list.
func staffRoleRows(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile("../../db/migrations/0215_identity_audit.sql")
	if err != nil {
		t.Fatalf("read migration 0215: %v", err)
	}
	// The seed is the INSERT INTO staff_role statement: each row opens
	// with its quoted id.
	insert := regexp.MustCompile(`(?s)INSERT INTO staff_role \(.*?\) VALUES(.*?);`).FindSubmatch(raw)
	if insert == nil {
		t.Fatalf("migration 0215: no staff_role seed found")
	}
	var roles []string
	for _, m := range regexp.MustCompile(`(?m)^\s*\('([a-z_]+)',`).FindAllSubmatch(insert[1], -1) {
		roles = append(roles, string(m[1]))
	}
	if len(roles) == 0 {
		t.Fatalf("migration 0215: staff_role seed parsed to no rows")
	}
	return roles
}

// contractorRoles is the canonical contractor role set (identity).
func contractorRoles() []string {
	return []string{
		identity.PortalRoleAccountAdmin, identity.PortalRoleBuyer, identity.PortalRoleBuyerNoPay,
		identity.PortalRoleAPFinance, identity.PortalRoleFieldCrew,
	}
}

// TestEveryScopeCarriesAFenceDecision: deny by default holds at test time.
// Every scope is pull-only, money, or carries writers and ops; every role
// named is a real role of the right class for the scope's tenancy; every op
// is in the engine's vocabulary.
func TestEveryScopeCarriesAFenceDecision(t *testing.T) {
	staff, portal := staffRoleRows(t), contractorRoles()
	for _, info := range Scopes() {
		switch {
		case info.Money:
			if len(info.Writers) != 0 || len(info.Ops) != 0 {
				t.Errorf("%s: a money scope carries writers or ops: %v %v", info.Name, info.Writers, info.Ops)
			}
		case info.Policy == PolicyPullOnly:
			if len(info.Writers) != 0 || len(info.Ops) != 0 {
				t.Errorf("%s: a pull-only scope carries writers or ops: %v %v", info.Name, info.Writers, info.Ops)
			}
		default:
			if len(info.Writers) == 0 || len(info.Ops) == 0 {
				t.Errorf("%s: no writer entry (deny by default needs an explicit decision): writers=%v ops=%v", info.Name, info.Writers, info.Ops)
			}
			if info.Tenancy == TenancyBoth {
				t.Errorf("%s: a scope serving both caller classes must be money until it names writers per class", info.Name)
			}
		}
		seen := map[string]bool{}
		for _, r := range info.Writers {
			if seen[r] {
				t.Errorf("%s: writer %q listed twice", info.Name, r)
			}
			seen[r] = true
			real := false
			switch info.Tenancy {
			case TenancyOrg:
				real = slices.Contains(staff, r)
			case TenancyTenant:
				real = slices.Contains(portal, r)
			}
			if !real {
				t.Errorf("%s (%s): writer %q is not a real role of that class", info.Name, info.Tenancy, r)
			}
		}
		for _, o := range info.Ops {
			if o != opCreate && o != opUpdate && o != opDelete {
				t.Errorf("%s: op %q is outside the engine vocabulary", info.Name, o)
			}
		}
	}
}

// TestMoneyScopesPinned: the money list is a decision, not an accident.
func TestMoneyScopesPinned(t *testing.T) {
	var got []string
	for _, info := range Scopes() {
		if info.Money {
			got = append(got, info.Name)
		}
	}
	sort.Strings(got)
	want := []string{"aging", "invoices", "payment_methods", "payments", "price_matrix"}
	if !slices.Equal(got, want) {
		t.Fatalf("money scopes = %v, want %v", got, want)
	}
}

// TestWriterTablePinned: the writer decisions, scope by scope, as literal
// expectations independent of the registry's own groups, so widening any
// scope's writers or ops is a visible edit here.
func TestWriterTablePinned(t *testing.T) {
	yard := []string{"yard_manager", "yard_team"}
	dispatch := []string{"branch_manager", "logistics_manager", "yard_manager"}
	buying := []string{"procurement_manager", "yard_manager"}
	messaging := []string{
		"branch_manager", "driver", "financial_controller", "general_manager", "hr",
		"inside_sales", "logistics_manager", "outside_sales", "payables_receivables",
		"procurement_manager", "sales_manager", "super_admin", "yard_manager", "yard_team",
	}
	want := map[string]struct {
		writers []string
		ops     []string
	}{
		"pick_tasks":      {yard, []string{"create", "update"}},
		"weights":         {yard, []string{"create", "update"}},
		"shortages":       {yard, []string{"create", "update"}},
		"receiving":       {yard, []string{"create", "update"}},
		"runs":            {[]string{"driver"}, []string{"create", "update"}},
		"stops":           {[]string{"driver"}, []string{"create", "update"}},
		"pod":             {[]string{"driver"}, []string{"create", "update"}},
		"attachments":     {[]string{"driver", "yard_manager", "yard_team"}, []string{"create", "delete", "update"}},
		"loads":           {dispatch, []string{"create", "update"}},
		"delivery_stops":  {dispatch, []string{"create", "update"}},
		"will_call":       {dispatch, []string{"create", "update"}},
		"purchase_orders": {buying, []string{"create", "update"}},
		"cycle_counts":    {buying, []string{"create", "update"}},
		"quotes": {[]string{"branch_manager", "inside_sales", "outside_sales", "sales_manager"},
			[]string{"create", "delete", "update"}},
		"approvals": {[]string{
			"branch_manager", "financial_controller", "general_manager", "inside_sales",
			"logistics_manager", "outside_sales", "payables_receivables", "procurement_manager",
			"sales_manager", "yard_manager",
		}, []string{"create", "delete", "update"}},
		"messages":              {messaging, []string{"create", "update"}},
		"acks":                  {messaging, []string{"create"}},
		"notify_prefs":          {messaging, []string{"create", "update"}},
		"orders":                {[]string{"account_admin", "buyer"}, []string{"create", "update"}},
		"scope_items":           {[]string{"account_admin", "buyer"}, []string{"create", "update"}},
		"customer_quotes":       {[]string{"account_admin", "buyer"}, []string{"create", "update"}},
		"team":                  {[]string{"account_admin"}, []string{"create", "update"}},
		"delivery_instructions": {[]string{"account_admin", "buyer", "field_crew"}, []string{"create", "update"}},
		"pickups":               {[]string{"account_admin", "buyer", "field_crew"}, []string{"create", "update"}},
		"tracking_events":       {[]string{"account_admin", "buyer", "field_crew"}, []string{"create"}},
	}
	for _, info := range Scopes() {
		w, ok := want[info.Name]
		if !ok {
			if len(info.Writers) != 0 {
				t.Errorf("%s: writers %v but no pinned expectation", info.Name, info.Writers)
			}
			continue
		}
		gw, go_ := append([]string(nil), info.Writers...), append([]string(nil), info.Ops...)
		sort.Strings(gw)
		sort.Strings(go_)
		if !slices.Equal(gw, w.writers) || !slices.Equal(go_, w.ops) {
			t.Errorf("%s: writers %v ops %v, want %v %v", info.Name, gw, go_, w.writers, w.ops)
		}
	}
}
