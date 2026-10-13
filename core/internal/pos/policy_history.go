package pos

// policy_history.go, the COUNTER SETTINGS change history: who moved which rule, and what it was
// before.
//
// -- WHY THIS FILE EXISTS AT ALL -----------------------------------------------------------------
//
// It did not. Before this, `DISPATCH_POLICY_CHANGED` was the ONLY `*_POLICY_CHANGED` action in the
// program: the dispatch rules recorded who changed them and the counter rules did not, even though
// pos_policy carries the two knobs with the sharpest consequences in the module, margin_visibility
// and balance_visibility, which decide who may RECEIVE a dealer's own position and a customer's
// past-due balance. A visibility gate that can be moved with no record of who moved it is a gate
// with an untraceable off switch.
//
// -- IT READS THE AUDIT TRAIL RATHER THAN A TABLE OF ITS OWN -------------------------------------
//
// Same argument dispatch's history makes: the change is already recorded, by the module that made
// it, in the one place this program keeps "who did what". A second history table would be a second
// thing to keep in step and a second thing to forget to write to. It also means this file needed no
// migration, the 0196 reservation this item held is deliberately left unspent.
//
// -- THE SHAPE IS THE SAME ON ALL THREE SETTINGS SCREENS -----------------------------------------
//
// dispatch, pos and billing each emit ONE audit row per CHANGED KNOB in the {field, before, after,
// actor_role} shape the audit allowlist already admits (audit/service.go), and each serves it back
// as a PolicyChange whose `who` is ALREADY A PERSON, "Alex Morgan (General Manager)", resolved on
// the server through a consumer-declared ActorNames seam. NO USER ID IS EVER PUT ON THE WIRE. That
// is the original dispatch ruling's intent kept intact (service_loadboard.go chose a role word
// "without putting an identifier on a screen") while answering the question the screen is actually
// asked, which is "who", not "what rank".
//
// The three copies of the seam are deliberate and are the house rule, not an oversight: a consumer
// declares the interface it needs and main.go satisfies it with a thin adapter. order, quote and
// order/activity already declare this identical ActorNames interface four times over for exactly
// this reason.

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
)

// ActPosPolicyChanged is the action verb one moved counter rule is filed under. It is a pos-owned
// string rather than a constant added to internal/audit: the audit package's exported verbs are the
// SHARED ones several modules emit, and a settings history that only this module writes and only
// this module reads has no business widening a package every module imports. DISPATCH_POLICY_CHANGED
// is a local literal for the same reason.
const ActPosPolicyChanged = "POS_POLICY_CHANGED"

// posPolicyEntity is the audit entity_type these rows are filed under.
const posPolicyEntity = "pos_policy"

// dealerScopeID is the entity id a DEALER-DEFAULT (branch_id "") policy change is filed under.
//
// THIS EXISTS BECAUSE AN EMPTY ENTITY ID CANNOT BE READ BACK. audit's repository writes entity_id as
// NULLIF($3,”)::uuid and reads it with `entity_id = $2::uuid`, so a row written with "" lands as
// SQL NULL and no equality predicate will ever match it again. Dispatch never meets this because its
// settings screen always supplies a branch id. The counter's org-wide default row, the one 0043
// seeds and the one an administrator configuring a single-yard dealer actually edits, is branch_id
// NULL, i.e. exactly the "" case. Written naively, the dealer-default history would be empty BY
// CONSTRUCTION: rows written, none readable.
//
// The nil uuid is the sentinel because it is the one uuid that can never collide with a real
// branches.id (they are generated v4, which pins version and variant bits the nil uuid does not
// carry), it needs no migration, and audit_log.entity_id has no foreign key to satisfy. The scope is
// further namespaced by entity_type, so "the dealer default of the counter policy" and "the dealer
// default of anything else" cannot be confused for one another.
//
// The alternative considered and rejected: writing the literal string "dealer" and widening the
// audit read to a text comparison. That would have made every entity id in the table nullable-text
// for one screen's benefit, and the column is a uuid on purpose.
const dealerScopeID = "00000000-0000-0000-0000-000000000000"

// policyScopeID maps a policy's branch onto the entity id its history is filed under. See
// dealerScopeID for why "" cannot be used as itself.
func policyScopeID(branchID string) string {
	if strings.TrimSpace(branchID) == "" {
		return dealerScopeID
	}
	return branchID
}

// PolicyTrailReader is the READ half of the audit seam, "what has been recorded about this thing".
//
// IT IS ITS OWN INTERFACE rather than a widening of audit.Recorder, which every module in this
// program implements and fakes. Adding a read method there would make a dozen fakes grow a method
// none of them wants, to serve one screen's disclosure. NIL IS AN EMPTY HISTORY, never an error.
type PolicyTrailReader interface {
	ReopenTransaction(ctx context.Context, entityType, entityID string) ([]audit.Entry, error)
}

// ActorNameResolver maps audit actor ids to staff display names. Satisfied in main.go by a thin
// adapter over the one LEFT JOIN on users that already exists (order.Repository.ActorNames), so this
// module gains a name without gaining a third copy of `SELECT display_name FROM users` and without
// reaching into another module's tables itself.
//
// AN ID THE RESOLVER CANNOT NAME IS SIMPLY ABSENT from the returned map. The caller degrades to the
// role word; it never renders a blank and it never renders the uuid.
type ActorNameResolver interface {
	ActorNames(ctx context.Context, ids []string) (map[string]string, error)
}

// WithAuditTrail injects the read seam (satisfied by *audit.Service at the composition root).
func (s *Service) WithAuditTrail(r PolicyTrailReader) *Service { s.trail = r; return s }

// WithActorNames injects the name resolver. Nil is honest: every row degrades to its role word,
// which is what this history said before names existed at all.
func (s *Service) WithActorNames(r ActorNameResolver) *Service { s.actorNames = r; return s }

// PolicyChange is one knob that moved. The wire shape is IDENTICAL on all three settings screens so
// one client component can render all three.
type PolicyChange struct {
	Field  string `json:"field"`
	Before string `json:"before"`
	After  string `json:"after"`
	Who    string `json:"who,omitempty"`
	When   string `json:"when,omitempty"`
}

// posPolicyChanges diffs two counter policies into the rows a history shows.
//
// ONLY REAL CHANGES ARE EMITTED. A save that moved one knob writes one row; a save that moved
// nothing writes none. A history that logged every save whether or not anything changed would be a
// list of times somebody pressed a button, which is not what "who changed this, and from what" asks.
//
// EVERY VALUE HERE IS AN INT, A BOOL OR A CHECK-CONSTRAINED ENUM, and that is a property of the set
// this function chooses rather than of PosPolicy. `notes` is deliberately NOT diffed: it is the one
// free-text column on this policy, so it is the one value that could carry a phone number or an
// account number into a trail whose cleartext detector would silently rewrite it, and a history row
// showing neither the old value nor the new one is worse than no row. Recording that a note changed
// without being able to show it is a different feature, and it is not this one.
func posPolicyChanges(before, after PosPolicy) []PolicyChange {
	out := []PolicyChange{}
	add := func(field, b, a string) {
		if b != a {
			out = append(out, PolicyChange{Field: field, Before: b, After: a})
		}
	}
	yesNo := func(v bool) string { return strconv.FormatBool(v) }
	add("cod_enabled", yesNo(before.CODEnabled), yesNo(after.CODEnabled))
	add("restocking_fee_bps", strconv.Itoa(before.RestockingFeeBps), strconv.Itoa(after.RestockingFeeBps))
	add("line_presentation", before.LinePresentation, after.LinePresentation)
	add("quick_chips_enabled", yesNo(before.QuickChipsEnabled), yesNo(after.QuickChipsEnabled))
	add("workspace_split_pct", strconv.Itoa(before.WorkspaceSplitPct), strconv.Itoa(after.WorkspaceSplitPct))
	add("workspace_arrangement", before.WorkspaceArrangement, after.WorkspaceArrangement)
	add("lookup_presentation", before.LookupPresentation, after.LookupPresentation)
	add("margin_visibility", before.MarginVisibility, after.MarginVisibility)
	add("balance_visibility", before.BalanceVisibility, after.BalanceVisibility)
	add("supplier_deposit_cancel", before.SupplierDepositCancel, after.SupplierDepositCancel)
	add("quick_chip_set", strings.Join(before.QuickChipSet, ", "), strings.Join(after.QuickChipSet, ", "))
	return out
}

// recordPolicyChanges writes one audit row per moved knob. Best-effort, exactly like every other
// Recorder call on this service: an audit outage must never cost a dealer the setting they just
// saved.
func (s *Service) recordPolicyChanges(ctx context.Context, branchID, actorID, roleCode string, rows []PolicyChange) {
	for _, ch := range rows {
		s.record(ctx, audit.SensitiveAccess{
			Action: ActPosPolicyChanged, EntityType: posPolicyEntity,
			EntityID: policyScopeID(branchID), ActorID: actorID,
			// actor_role rides ALONGSIDE the actor id, not instead of it. The id is what resolves a
			// NAME on the read; the role is what the read falls back to when the person has since
			// been deleted, renamed out of the directory, or was a system actor with no staff row.
			// Neither is ever served: the wire carries the composed sentence and nothing else.
			Extra: map[string]any{
				"field": ch.Field, "before": ch.Before, "after": ch.After,
				"actor_role": roleCode,
			},
		})
	}
}

// PolicyHistory is the quiet trail the counter settings screen shows behind a disclosure.
//
// ADMIN-GATED TO MATCH THE WRITE. PUT /api/v1/pos/policy is requireAdmin, so somebody who may change
// the counter rules may see who changed them, and nobody else may. The check is repeated HERE and
// not left to the route because an authority that lives only in a route registration is an authority
// one refactor away from gone. It needs no branch fence on top: IsAdmin is Tier-1 GLOBAL by
// definition (identity/rbac.go), so an admin already reaches every branch and a fence would refuse
// nobody.
func (s *Service) PolicyHistory(ctx context.Context, branchID string, limit int) ([]PolicyChange, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return nil, err
	}
	if !caller.IsAdmin() {
		return nil, clientErr(http.StatusForbidden, "pos: only an administrator may read the counter rule history")
	}
	if s.trail == nil {
		// UNWIRED IS AN EMPTY HISTORY, NOT AN ERROR. A deployment that has not attached the audit read
		// still gets a working settings screen; the disclosure simply says nothing has been recorded,
		// which is what it can honestly say.
		return []PolicyChange{}, nil
	}
	entries, err := s.trail.ReopenTransaction(ctx, posPolicyEntity, policyScopeID(branchID))
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	out := []PolicyChange{}
	// attrib is PARALLEL TO out, one entry per emitted row: the actor id the name lookup needs and
	// the role code the fallback needs. Kept beside the rows rather than on them because neither
	// value goes on the wire, the whole point is that a screen receives a person, not an id.
	type attribution struct{ actorID, roleCode string }
	attrib := []attribution{}
	// NEWEST FIRST, because a history is read from the top and the question is almost always "what
	// changed recently". The trail arrives oldest-first (its causal order), so it is walked backwards.
	for i := len(entries) - 1; i >= 0 && len(out) < limit; i-- {
		e := entries[i]
		field := changeStr(e.Changes["field"])
		if field == "" {
			continue // an action that names no field, not part of this history
		}
		out = append(out, PolicyChange{
			Field:  field,
			Before: changeStr(e.Changes["before"]),
			After:  changeStr(e.Changes["after"]),
			// The role word is written FIRST, so a name resolution that never runs, fails, or comes
			// back short leaves a row that still says who was allowed to do this. "Degrade" below is
			// then literally "do nothing".
			Who:  actorWord(e.ActorRole),
			When: e.CreatedAt,
		})
		attrib = append(attrib, attribution{actorID: e.ActorID, roleCode: e.ActorRole})
	}
	if s.actorNames == nil {
		return out, nil
	}
	ids := make([]string, 0, len(attrib))
	for _, a := range attrib {
		if a.actorID != "" {
			ids = append(ids, a.actorID)
		}
	}
	if len(ids) == 0 {
		return out, nil
	}
	// A RESOLUTION FAILURE DEGRADES TO THE ROLE WORD AND NEVER FAILS THE READ, the history reads'
	// contract, stated identically at order/activity_trail.go, order/activity.go,
	// quote/edit_history.go and order/edit_history.go.
	names, nerr := s.actorNames.ActorNames(ctx, ids)
	if nerr != nil {
		return out, nil
	}
	for i := range out {
		out[i].Who = whoWord(names[attrib[i].actorID], attrib[i].roleCode)
	}
	return out, nil
}

// changeStr renders one audit `changes` value as the string the history wire format carries.
//
// IT MUST NOT BE fmt.Sprint. audit's record boundary DROPS EMPTY VALUES (service.go's put()), so a
// knob whose pre-image was genuinely empty, an uncurated quick_chip_set is the everyday case -
// writes a row with no `before` key at all, and fmt.Sprint of a missing map entry is the four
// characters "<nil>". A history that says a setting used to be "<nil>" is a history reporting a bug
// in itself. Empty in, empty out; the client's word map turns that into words.
//
// The float64 arm is the jsonb round-trip: this module stringifies every value before recording, so
// it should never fire, but a row written by an older shape (or by a fake in a test) would come back
// as a float64 and must not render as "1.25e+03".
func changeStr(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	default:
		return ""
	}
}

// roleTitles are the role NAMES as the roles table itself spells them (migration 0015, plus 0043's
// platform role). They are what goes in the parentheses after a person: "Alex Morgan (Yard Manager)".
//
// THE TITLES ARE COPIED FROM THE SEED, NOT INVENTED HERE. roles.name is already the human spelling
// of roles.code, and the audit row records the code; a map that said "Logistics Lead" where the
// roster says "Logistics Manager" would put two names for one job in front of the same person.
var roleTitles = map[string]string{
	"GENERAL_MANAGER":      "General Manager",
	"FINANCIAL_CONTROLLER": "Financial Controller",
	"HR":                   "HR",
	"BRANCH_MANAGER":       "Branch Manager",
	"PROCUREMENT_MANAGER":  "Procurement Manager",
	"SALES_MANAGER":        "Sales Manager",
	"YARD_MANAGER":         "Yard Manager",
	"LOGISTICS_MANAGER":    "Logistics Manager",
	"PAYABLES_RECEIVABLES": "Payables/Receivables",
	"INSIDE_SALES":         "Inside Sales",
	"OUTSIDE_SALES":        "Outside Sales",
	"YARD_TEAM":            "Yard Team",
	"DRIVERS":              "Drivers",
	"SUPER_ADMIN":          "Platform Administrator",
}

// roleTitle spells a role code the way the roster spells it.
//
// The default arm degrades honestly, de-underscored and sentence-cased, so a role added to the
// table later reads as slightly strange rather than as a confident wrong title, and NEVER reaches a
// screen as SHOUTING_SNAKE_CASE. An absent role is "" so the caller can tell the difference between
// "no role recorded" and "a role I have no title for".
func roleTitle(roleCode string) string {
	if roleCode == "" {
		return ""
	}
	if t, ok := roleTitles[roleCode]; ok {
		return t
	}
	lower := strings.ToLower(strings.ReplaceAll(roleCode, "_", " "))
	return strings.ToUpper(lower[:1]) + lower[1:]
}

// actorWord names who made a change in words rather than as a role code, for the rows where a name
// could not be resolved. LOGISTICS_MANAGER is a row in our roles table; "a dispatch manager" is what
// somebody reading a change history would say.
//
// An ABSENT role is "somebody", never blank, a history row with no who is a row that looks broken.
func actorWord(roleCode string) string {
	switch roleCode {
	case "":
		return "somebody"
	case "SUPER_ADMIN":
		return "an administrator"
	case "GENERAL_MANAGER":
		return "the general manager"
	case "LOGISTICS_MANAGER":
		return "a dispatch manager"
	case "BRANCH_MANAGER":
		return "a branch manager"
	default:
		return "a " + strings.ToLower(strings.ReplaceAll(roleCode, "_", " "))
	}
}

// whoWord composes the one string this history puts on the wire under `who`.
//
// THE NAME GOES ON THE WIRE, NEVER THE ID. A settings history is read to find out who to go and talk
// to, and a uuid answers that question for nobody; the role alone answers it for a dealer with one
// general manager and for nobody with two branch managers. The role stays in the parentheses because
// it is the part that says whether the change was allowed.
//
// The degradation ladder is total and never produces a blank cell:
//
//	name + role → "Alex Morgan (Yard Manager)"
//	name only   → "Alex Morgan"          (a role the roster no longer carries)
//	neither     → "a branch manager"     (the actor is gone, or was a system actor)
//	nothing     → "somebody"
func whoWord(name, roleCode string) string {
	name = strings.TrimSpace(name)
	title := roleTitle(roleCode)
	switch {
	case name != "" && title != "":
		return name + " (" + title + ")"
	case name != "":
		return name
	default:
		return actorWord(roleCode)
	}
}
