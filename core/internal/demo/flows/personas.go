//go:build demo

package flows

import "strings"

// The persona list (state note section 5, rows 1524 and 1530). Personas are
// not FB Console users: the persona route mints a core session for one of
// these entries itself. The list is fixed in the build, so the entry page
// may only ever offer what this file lists, and the subject of every token
// the route mints is persona: plus an id from this list.
//
// The flow table above stays the demo mode's mint table; the personas reuse
// its roles and labels by construction (the eight flows are looked up, not
// copied) and extend it to the thirteen staff roles, the dealer
// administrator and the four contractor roles of the tenant sheet.

// SubjectPrefix opens every persona subject. An FB Console id never holds a
// colon, so no real account can equal a persona subject.
const SubjectPrefix = "persona:"

// Persona is one entry of the fixed list.
type Persona struct {
	// ID is the closed id the route's body names ("sales", "hr").
	ID string
	// Label is the role label the answer carries.
	Label string
	// Role is the staff role id (staff_role.id) or, for a contractor
	// persona, the canonical contractor role.
	Role string
	// AlsoRoles are further staff roles the same person holds (one person,
	// two roles): the yard manager is also a driver.
	AlsoRoles []string
	// Contractor marks the four contractor personas: listed, and answered
	// with the shared 403 until the Pro cycle.
	Contractor bool
	// LegacyCode is the roles catalog code (roles.code) the populate gives
	// the persona's users row, so the identity program derives the staff
	// grant. Empty for the roles the catalog has no code for.
	LegacyCode string
	// UserID is the id of the persona's users row.
	UserID string
	// Email is the persona's address at the reserved demo domain.
	Email string
}

// Subject is the token subject: persona: plus the id.
func (p Persona) Subject() string { return SubjectPrefix + p.ID }

// IsPersonaSubject reports whether a subject has the persona shape. It says
// nothing about whether the subject is on the list.
func IsPersonaSubject(subject string) bool { return strings.HasPrefix(subject, SubjectPrefix) }

// personaUserID is the id block of the persona users rows, the lane's own.
func personaUserID(n int) string {
	const hex = "0123456789abcdef"
	return "de5e0000-0000-4000-9000-0000000000" + string(hex[n>>4&0xf]) + string(hex[n&0xf])
}

func build(n int, id, label, role, legacy string, also ...string) Persona {
	return Persona{
		ID: id, Label: label, Role: role, AlsoRoles: also, LegacyCode: legacy,
		UserID: personaUserID(n), Email: "persona." + id + "@" + ReservedDomain,
	}
}

func contractor(n int, id, label, role string) Persona {
	p := build(n, id, label, role, "")
	p.Contractor = true
	return p
}

// fromFlow builds a persona from the flow table's own role and label.
func fromFlow(n int, flow, legacy string, also ...string) Persona {
	a := accounts[flow]
	return build(n, flow, a.Label, a.Role, legacy, also...)
}

var personas = []Persona{
	fromFlow(1, "sales", "INSIDE_SALES"),
	build(2, "outside-sales", "Outside Sales", "outside_sales", "OUTSIDE_SALES"),
	fromFlow(3, "sales-manager", "SALES_MANAGER"),
	fromFlow(4, "yard-team", "YARD_TEAM"),
	fromFlow(5, "yard-manager", "YARD_MANAGER", "driver"),
	fromFlow(6, "driver", "DRIVERS"),
	build(7, "logistics-manager", "Logistics Manager", "logistics_manager", "LOGISTICS_MANAGER"),
	fromFlow(8, "procurement-manager", "PROCUREMENT_MANAGER"),
	fromFlow(9, "payables-receivables", "PAYABLES_RECEIVABLES"),
	fromFlow(10, "financial-controller", "FINANCIAL_CONTROLLER"),
	build(11, "branch-manager", "Branch Manager", "branch_manager", "BRANCH_MANAGER"),
	build(12, "general-manager", "General Manager", "general_manager", "GENERAL_MANAGER"),
	build(13, "hr", "HR", "hr", "HR"),
	build(14, "dealer-admin", "Dealer Admin", "dealer_admin", ""),
	contractor(15, "contractor-owner", "Contractor Owner", "account_admin"),
	contractor(16, "contractor-buyer", "Contractor Buyer", "buyer"),
	contractor(17, "contractor-ap", "Contractor AP", "ap_finance"),
	contractor(18, "contractor-field", "Contractor Field", "field_crew"),
}

// Personas returns the whole list in its stable order, as a copy.
func Personas() []Persona {
	out := make([]Persona, len(personas))
	for i, p := range personas {
		p.AlsoRoles = append([]string(nil), p.AlsoRoles...)
		out[i] = p
	}
	return out
}

// PersonaByID returns the persona whose id is given. The prefix is not part
// of the id.
func PersonaByID(id string) (Persona, bool) {
	for _, p := range Personas() {
		if p.ID == id {
			return p, true
		}
	}
	return Persona{}, false
}

// PersonaBySubject returns the persona whose subject is given, exactly.
func PersonaBySubject(subject string) (Persona, bool) {
	if !IsPersonaSubject(subject) {
		return Persona{}, false
	}
	return PersonaByID(subject[len(SubjectPrefix):])
}
