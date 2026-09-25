package sync

import "testing"

// The registry is the Go-side spine of the confirmed policy table (the
// Phase D pack's START-checkpoint confirmation) and the catalog's
// audience declarations. This test pins it entry by entry so a policy
// edit without the pack, a scope added or dropped, or a tenancy flip
// fails here rather than at a desk. The SyncScope enum gate
// (scripts/sync-scope-catalog.test.ts) separately holds the catalog and
// the contract's SyncScope enum to the same 43 names.
func TestRegistryMatchesConfirmedTable(t *testing.T) {
	confirmed := map[string]Policy{
		// server_wins, the confirmed table's first block.
		"pick_tasks": PolicyServerWins, "weights": PolicyServerWins, "stops": PolicyServerWins,
		"runs": PolicyServerWins, "loads": PolicyServerWins, "delivery_stops": PolicyServerWins,
		"will_call": PolicyServerWins, "purchase_orders": PolicyServerWins, "approvals": PolicyServerWins,
		"messages": PolicyServerWins, "acks": PolicyServerWins, "orders": PolicyServerWins,
		"tracking_events": PolicyServerWins, "invoices": PolicyServerWins, "payments": PolicyServerWins,
		"price_matrix": PolicyServerWins,
		// last_write_wins_by_field.
		"attachments": PolicyLWWByField, "quotes": PolicyLWWByField, "notify_prefs": PolicyLWWByField,
		"scope_items": PolicyLWWByField, "delivery_instructions": PolicyLWWByField,
		// manual.
		"shortages": PolicyManual, "receiving": PolicyManual, "pod": PolicyManual,
		"cycle_counts": PolicyManual, "customer_quotes": PolicyManual, "team": PolicyManual,
		"pickups": PolicyManual,
		// pull-only, the ruling on undeclared scopes.
		"products": PolicyPullOnly, "customers": PolicyPullOnly, "inventory_lookup": PolicyPullOnly,
		"trucks": PolicyPullOnly, "drivers": PolicyPullOnly, "vendors": PolicyPullOnly,
		"channels": PolicyPullOnly, "projects": PolicyPullOnly, "catalog": PolicyPullOnly,
		"parked_tickets": PolicyPullOnly, "warranty_claims": PolicyPullOnly,
		"widget_measures": PolicyPullOnly, "warranty_board": PolicyPullOnly,
		"payment_methods": PolicyPullOnly, "aging": PolicyPullOnly,
	}
	if len(confirmed) != 43 {
		t.Fatalf("confirmed table holds %d scopes, want 43", len(confirmed))
	}
	if len(scopes) != len(confirmed) {
		t.Fatalf("registry holds %d scopes, want %d", len(scopes), len(confirmed))
	}
	for name, policy := range confirmed {
		info, ok := Lookup(name)
		if !ok {
			t.Fatalf("scope %s of the confirmed table is missing from the registry", name)
		}
		if info.Policy != policy {
			t.Fatalf("scope %s policy %q, want %q", name, info.Policy, policy)
		}
		switch info.Policy {
		case PolicyServerWins, PolicyLWWByField, PolicyManual, PolicyPullOnly:
		default:
			t.Fatalf("scope %s carries a policy outside the contract vocabulary: %q", name, info.Policy)
		}
	}
	// No extra scopes beyond the confirmed set.
	for name := range scopes {
		if _, ok := confirmed[name]; !ok {
			t.Fatalf("registry carries scope %s the confirmed table does not declare", name)
		}
	}
}

// Tenancy classes, derived from the catalog's audience declarations: the
// two scopes both sides declare serve both caller classes, the
// contractor-declared scopes are tenant scopes, and the staff-declared
// scopes are org scopes. Spot checks stand for the whole derivation, the
// full map being three frontend families.
func TestRegistryTenancyClasses(t *testing.T) {
	cases := []struct {
		scope          string
		tenancy        Tenancy
		staffAdmitted  bool
		tenantAdmitted bool
	}{
		{"pick_tasks", TenancyOrg, true, false},
		{"shortages", TenancyOrg, true, false},
		{"messages", TenancyOrg, true, false},
		{"orders", TenancyTenant, false, true},
		{"customer_quotes", TenancyTenant, false, true},
		{"aging", TenancyTenant, false, true},
		{"invoices", TenancyBoth, true, true},
		{"price_matrix", TenancyBoth, true, true},
	}
	for _, c := range cases {
		info, ok := Lookup(c.scope)
		if !ok {
			t.Fatalf("scope %s missing", c.scope)
		}
		if info.Tenancy != c.tenancy {
			t.Fatalf("scope %s tenancy %q, want %q", c.scope, info.Tenancy, c.tenancy)
		}
		if info.Admits(false) != c.staffAdmitted {
			t.Fatalf("scope %s staff admission = %v, want %v", c.scope, info.Admits(false), c.staffAdmitted)
		}
		if info.Admits(true) != c.tenantAdmitted {
			t.Fatalf("scope %s tenant admission = %v, want %v", c.scope, info.Admits(true), c.tenantAdmitted)
		}
	}
	// Every scope carries a tenancy class; an unknown class admits
	// nobody (fail closed).
	for _, info := range Scopes() {
		switch info.Tenancy {
		case TenancyOrg, TenancyTenant, TenancyBoth:
		default:
			t.Fatalf("scope %s carries an unknown tenancy class %q", info.Name, info.Tenancy)
		}
	}
	if (ScopeInfo{Tenancy: "nonsense"}).Admits(false) || (ScopeInfo{Tenancy: "nonsense"}).Admits(true) {
		t.Fatalf("an unknown tenancy class admitted a caller")
	}
}

// Scopes is sorted and complete: the stable order later items' listings
// rely on.
func TestScopesSortedAndComplete(t *testing.T) {
	all := Scopes()
	if len(all) != 43 {
		t.Fatalf("%d scopes, want 43", len(all))
	}
	for i := 1; i < len(all); i++ {
		if all[i-1].Name >= all[i].Name {
			t.Fatalf("Scopes not sorted at %d: %q then %q", i, all[i-1].Name, all[i].Name)
		}
	}
	if _, ok := Lookup("not_a_scope"); ok {
		t.Fatalf("an unknown scope name resolved")
	}
}
