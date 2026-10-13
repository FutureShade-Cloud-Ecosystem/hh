//go:build demo

package flows

import (
	"strings"
	"testing"
)

// The list is the thirteen staff roles, the dealer administrator and the
// four contractor roles of the tenant sheet, and nothing else.
func TestPersonasAreTheClosedSetOfEighteen(t *testing.T) {
	all := Personas()
	if len(all) != 18 {
		t.Fatalf("the persona list holds %d entries, want 18", len(all))
	}
	staff, contractors, admin := 0, 0, 0
	for _, p := range all {
		switch {
		case p.Contractor:
			contractors++
		case p.Role == "dealer_admin":
			admin++
		default:
			staff++
		}
	}
	if staff != 13 || admin != 1 || contractors != 4 {
		t.Fatalf("staff %d, dealer admin %d, contractor %d, want 13, 1, 4", staff, admin, contractors)
	}
}

// Every entry carries its facts, ids and subjects and user ids and emails
// are unique, the subject is persona: plus the id, and the email sits at
// the reserved demo domain.
func TestPersonaFactsAreCompleteAndUnique(t *testing.T) {
	ids, subjects, users, emails := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, p := range Personas() {
		if p.ID == "" || p.Label == "" || p.Role == "" || p.UserID == "" || p.Email == "" {
			t.Fatalf("persona %q carries an empty fact: %+v", p.ID, p)
		}
		if p.Subject() != "persona:"+p.ID {
			t.Fatalf("persona %q subject %q", p.ID, p.Subject())
		}
		if !strings.HasSuffix(p.Email, "@"+ReservedDomain) {
			t.Fatalf("persona %q email sits outside the reserved domain", p.ID)
		}
		for _, c := range []struct {
			key  string
			seen map[string]bool
		}{{p.ID, ids}, {p.Subject(), subjects}, {p.UserID, users}, {p.Email, emails}} {
			if c.seen[c.key] {
				t.Fatalf("persona %q repeats %q", p.ID, c.key)
			}
			c.seen[c.key] = true
		}
		if strings.ContainsAny(p.ID, ": ") {
			t.Fatalf("persona id %q holds a colon or a space", p.ID)
		}
	}
}

// The flow table's roles and labels are reused where they exist: the eight
// flows are personas of the same id, role and label.
func TestFlowRolesAndLabelsAreReused(t *testing.T) {
	for _, a := range All() {
		p, ok := PersonaByID(a.Flow)
		if !ok || p.Role != a.Role || p.Label != a.Label {
			t.Fatalf("flow %q is not the persona of the same role and label: %+v %v", a.Flow, p, ok)
		}
	}
}

// Contractor personas are listed but not enterable until the Pro cycle.
func TestContractorPersonasAreListedNotEnterable(t *testing.T) {
	want := map[string]string{
		"contractor-owner": "account_admin", "contractor-buyer": "buyer",
		"contractor-ap": "ap_finance", "contractor-field": "field_crew",
	}
	for id, role := range want {
		p, ok := PersonaByID(id)
		if !ok || !p.Contractor || p.Role != role {
			t.Fatalf("%s: %+v %v", id, p, ok)
		}
	}
}

// One person, two roles: the yard manager also holds a driver grant.
func TestYardManagerAlsoHoldsDriver(t *testing.T) {
	p, _ := PersonaByID("yard-manager")
	if len(p.AlsoRoles) != 1 || p.AlsoRoles[0] != "driver" {
		t.Fatalf("yard manager roles: %+v", p)
	}
	for _, q := range Personas() {
		if q.ID != "yard-manager" && len(q.AlsoRoles) != 0 {
			t.Fatalf("%s holds extra roles %v", q.ID, q.AlsoRoles)
		}
	}
}

// Subject lookups answer the list and nothing else.
func TestPersonaLookups(t *testing.T) {
	if _, ok := PersonaBySubject("persona:sales"); !ok {
		t.Fatal("persona:sales not found")
	}
	for _, s := range []string{"", "sales", "persona:", "persona:nobody", "PERSONA:sales", "persona:sales ", "persona:persona:sales"} {
		if _, ok := PersonaBySubject(s); ok {
			t.Fatalf("subject %q resolved", s)
		}
	}
	if _, ok := PersonaByID("persona:sales"); ok {
		t.Fatal("an id with the prefix resolved")
	}
	if !IsPersonaSubject("persona:x") || IsPersonaSubject("x") {
		t.Fatal("IsPersonaSubject")
	}
}

// The returned slice is a copy: a caller cannot edit the list.
func TestPersonasReturnsACopy(t *testing.T) {
	a := Personas()
	a[0].ID = "tampered"
	if Personas()[0].ID == "tampered" {
		t.Fatal("the list was edited through its accessor")
	}
}

func TestStaffRolesAndLegacyCodes(t *testing.T) {
	p, _ := PersonaByID("yard-manager")
	if r := p.StaffRoles(); len(r) != 2 || r[0] != "yard_manager" || r[1] != "driver" {
		t.Fatalf("yard manager roles %v", r)
	}
	if c, _ := PersonaByID("contractor-ap"); c.StaffRoles() != nil {
		t.Fatal("a contractor persona holds staff roles")
	}
	for role, want := range map[string]string{
		"inside_sales": "INSIDE_SALES", "driver": "DRIVERS", "payables_receivables": "PAYABLES_RECEIVABLES",
		"hr": "HR", "dealer_admin": "",
	} {
		if got := LegacyCode(role); got != want {
			t.Errorf("LegacyCode(%s) = %q, want %q", role, got, want)
		}
	}
}
