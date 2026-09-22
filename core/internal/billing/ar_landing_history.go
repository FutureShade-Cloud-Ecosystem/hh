package billing

// ar_landing_history.go, the RECEIVABLES SETTINGS change history: who moved which rule, and what it
// was before.
//
// -- WHAT WAS MISSING, AND IT WAS MORE THAN A READ -----------------------------------------------
//
// Dispatch already recorded its policy changes and only lacked a way to read them back. AR recorded
// NOTHING: ARLandingService.SetPolicy wrote the row and moved on, and the service carried neither an
// audit.Recorder nor a trail seam. Both halves are added here, in the shape billing's own
// CreditService already uses for overrides and releases (credit_gate.go), a nil recorder is
// tolerated and simply records nothing, and an audit outage never blocks the write.
//
// These knobs are not cosmetic. aging_escalate_days decides whose account is called about, ar_tier
// decides what a dealer is paying for, and untagged_fallback decides whether a message goes to an
// address nobody tagged. "Somebody moved the escalation edge from 90 to 60 last Tuesday" is the
// question this file exists to answer.
//
// -- THREE TRAPS, ALL OF THEM ARGUED WHERE THEY BITE ---------------------------------------------
//
//  1. THE PRE-IMAGE comes from the STORE, never from Policy(). See SetPolicy.
//  2. THE ENTITY ID has a sentinel for the dealer-default scope. See arDealerScopeID.
//  3. AN EMPTY VALUE never reaches a screen as "<nil>". See arChangeStr.
//
// -- THE WIRE SHAPE IS THE SAME ON ALL THREE SETTINGS SCREENS ------------------------------------
//
// dispatch, pos and billing each serve {field, before, after, who, when}, where `who` is ALREADY A
// PERSON, "Alex Morgan (Financial Controller)", composed on the server. NO USER ID EVER GOES ON
// THE WIRE. One client component renders all three, which is why the shapes are held identical.

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/settings"
)

// ActARPolicyChanged is the action verb one moved receivables rule is filed under. A billing-owned
// literal rather than a constant added to internal/audit, for the same reason
// DISPATCH_POLICY_CHANGED is: the audit package's exported verbs are the SHARED ones, and a settings
// history only this module writes and only this module reads has no business widening a package
// every module imports.
const ActARPolicyChanged = "AR_POLICY_CHANGED"

// arPolicyEntity is the audit entity_type these rows are filed under.
const arPolicyEntity = "ar_landing_policy"

// arDealerScopeID is the entity id a DEALER-DEFAULT (branch_id "") policy change is filed under.
//
// WITHOUT IT THIS HISTORY WOULD BE EMPTY BY CONSTRUCTION, and AR is where that actually bites.
// audit's repository writes entity_id as NULLIF($3,”)::uuid and reads it back with
// `entity_id = $2::uuid`: a row written with "" lands as SQL NULL, and no equality predicate will
// ever match a NULL. Dispatch never meets this because its settings screen always carries a branch
// id. THE AR LANDING IS THE OPPOSITE CASE, it mounts with no branch id at all, keeps branchId = ”
// through the settings panel, and every AR policy save this product has ever made has therefore
// targeted the dealer row. A straight copy of the dispatch recipe would have written every row into
// the unreadable NULL scope and shipped a disclosure that could never contain anything.
//
// The nil uuid is the sentinel: it is the one uuid a v4 generator cannot produce (it carries neither
// the version nor the variant bits), audit_log.entity_id has no foreign key to satisfy, and the
// scope is further namespaced by entity_type. It needed no migration, which is why the 0196
// reservation this item held is left unspent.
//
// Rejected alternative: writing the literal "dealer" and widening the audit read to a text
// comparison. That turns a uuid column into nullable text across the whole table for one screen.
const arDealerScopeID = "00000000-0000-0000-0000-000000000000"

// arPolicyScopeID maps a policy's branch onto the entity id its history is filed under.
func arPolicyScopeID(branchID string) string {
	if strings.TrimSpace(branchID) == "" {
		return arDealerScopeID
	}
	return branchID
}

// ARPolicyTrailReader is the READ half of the audit seam (satisfied by *audit.Service). Its own
// interface rather than a widening of audit.Recorder, which every module implements and fakes:
// adding a read method there would make a dozen fakes grow a method none of them wants, to serve one
// disclosure. NIL IS AN EMPTY HISTORY, never an error.
type ARPolicyTrailReader interface {
	ReopenTransaction(ctx context.Context, entityType, entityID string) ([]audit.Entry, error)
}

// ARActorNameResolver maps audit actor ids to staff display names. Satisfied in main.go by a thin
// adapter over the LEFT JOIN on users that already exists, so this module gains a name without
// gaining a third copy of `SELECT display_name FROM users` and without reaching into another
// module's tables. An id it cannot name is simply absent from the map; the caller degrades to the
// role word and never renders a blank or a uuid.
type ARActorNameResolver interface {
	ActorNames(ctx context.Context, ids []string) (map[string]string, error)
}

// WithAudit attaches the audit recorder. Every policy change is recorded through it; a nil recorder
// is tolerated (scaffold/tests) and records nothing, CreditService.WithAudit's rule, verbatim.
func (s *ARLandingService) WithAudit(r audit.Recorder) *ARLandingService { s.audit = r; return s }

// WithAuditTrail injects the read seam. Nil is an empty history, not an error.
func (s *ARLandingService) WithAuditTrail(r ARPolicyTrailReader) *ARLandingService {
	s.trail = r
	return s
}

// WithActorNames injects the name resolver. Nil degrades every row to its role word.
func (s *ARLandingService) WithActorNames(r ARActorNameResolver) *ARLandingService {
	s.names = r
	return s
}

// ARPolicyChange is one knob that moved.
type ARPolicyChange struct {
	Field  string `json:"field"`
	Before string `json:"before"`
	After  string `json:"after"`
	Who    string `json:"who,omitempty"`
	When   string `json:"when,omitempty"`
}

// arPolicyChanges diffs two receivables policies into the rows a history shows.
//
// ONLY REAL CHANGES ARE EMITTED. A save that moved one knob writes one row; a save that moved
// nothing writes none. A history that logged every save whether or not anything changed would be a
// list of times somebody pressed a button, which is not what "who changed this, and from what" asks.
//
// Every value is an int, a bool or a CHECK-constrained enum, so none can carry a customer identifier
// and the audit layer's cleartext redaction has nothing to catch. That is a property of what
// ar_landing_policy holds (0183), there is no free-text column on it, and not luck.
func arPolicyChanges(before, after ARLandingPolicy) []ARPolicyChange {
	out := []ARPolicyChange{}
	add := func(field, b, a string) {
		if b != a {
			out = append(out, ARPolicyChange{Field: field, Before: b, After: a})
		}
	}
	add("aging_warn_days", strconv.Itoa(before.AgingWarnDays), strconv.Itoa(after.AgingWarnDays))
	add("aging_escalate_days", strconv.Itoa(before.AgingEscalateDays), strconv.Itoa(after.AgingEscalateDays))
	add("reminder_cadence_days", strconv.Itoa(before.ReminderCadenceDays), strconv.Itoa(after.ReminderCadenceDays))
	add("reminder_max_per_cycle", strconv.Itoa(before.ReminderMaxPerCycle), strconv.Itoa(after.ReminderMaxPerCycle))
	add("black_hole_window_days", strconv.Itoa(before.BlackHoleWindowDays), strconv.Itoa(after.BlackHoleWindowDays))
	add("black_hole_min_sends", strconv.Itoa(before.BlackHoleMinSends), strconv.Itoa(after.BlackHoleMinSends))
	add("reminder_email_enabled", strconv.FormatBool(before.ReminderEmailEnabled), strconv.FormatBool(after.ReminderEmailEnabled))
	add("reminder_sms_enabled", strconv.FormatBool(before.ReminderSMSEnabled), strconv.FormatBool(after.ReminderSMSEnabled))
	add("untagged_fallback", before.UntaggedFallback, after.UntaggedFallback)
	add("ar_tier", before.Tier, after.Tier)
	return out
}

// recordPolicyChanges writes one audit row per moved knob. Best-effort: an audit outage must never
// cost a dealer the setting they just saved.
func (s *ARLandingService) recordPolicyChanges(ctx context.Context, branchID string, rows []ARPolicyChange) {
	if s.audit == nil || len(rows) == 0 {
		return
	}
	// The caller is read here rather than passed in because SetPolicy is reachable from a future job
	// as well as from the admin route, and a job has no staff context. An unresolvable caller yields
	// an empty id and an empty role, which the history renders as "somebody", an honest answer, and
	// a better one than refusing to record the change at all.
	caller, _ := identity.StaffContextFromContext(ctx)
	for _, ch := range rows {
		_ = s.audit.Record(ctx, audit.SensitiveAccess{
			Action: ActARPolicyChanged, EntityType: arPolicyEntity,
			EntityID: arPolicyScopeID(branchID), ActorID: caller.UserID, BranchID: branchID,
			// actor_role rides ALONGSIDE the actor id, not instead of it. The id is what resolves a
			// NAME on the read; the role is what the read falls back to when the person has since been
			// deleted or renamed out of the directory. Neither is ever served on its own: the wire
			// carries the composed sentence and nothing else.
			Extra: map[string]any{
				"field": ch.Field, "before": ch.Before, "after": ch.After,
				"actor_role": caller.RoleCode,
			},
		})
	}
}

// PolicyHistory is the quiet trail the receivables settings screen shows behind a disclosure.
//
// ADMIN-GATED TO MATCH THE WRITE. PUT /api/v1/ar/landing/policy is requireAdmin, the bucket edges
// and the tier are dealer configuration and a money threshold, so somebody who may change the rules
// may see who changed them, and the manager who may only READ the landing may not. The check is
// repeated here rather than left to the route because an authority that lives only in a route
// registration is an authority one refactor away from gone. No branch fence is layered on top:
// IsAdmin is Tier-1 GLOBAL by definition (identity/rbac.go), so an admin already reaches every
// branch and a fence would refuse nobody.
func (s *ARLandingService) PolicyHistory(ctx context.Context, branchID string, limit int) ([]ARPolicyChange, error) {
	caller, ok := identity.StaffContextFromContext(ctx)
	if !ok {
		return nil, settings.Errf(http.StatusUnauthorized, "ar: not signed in")
	}
	if !caller.IsAdmin() {
		return nil, settings.Errf(http.StatusForbidden,
			"ar: only an administrator may read the receivables rule history")
	}
	if s.trail == nil {
		// UNWIRED IS AN EMPTY HISTORY, NOT AN ERROR. A deployment that has not attached the audit read
		// still gets a working settings screen; the disclosure says nothing has been recorded, which is
		// what it can honestly say.
		return []ARPolicyChange{}, nil
	}
	entries, err := s.trail.ReopenTransaction(ctx, arPolicyEntity, arPolicyScopeID(branchID))
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	out := []ARPolicyChange{}
	// attrib is PARALLEL TO out: the actor id the name lookup needs and the role code the fallback
	// needs. Kept beside the rows rather than on them because neither value goes on the wire, the
	// whole point is that a screen receives a person, not an id.
	type attribution struct{ actorID, roleCode string }
	attrib := []attribution{}
	// NEWEST FIRST, because a history is read from the top and the question is almost always "what
	// changed recently". The trail arrives oldest-first (its causal order), so it is walked backwards.
	for i := len(entries) - 1; i >= 0 && len(out) < limit; i-- {
		e := entries[i]
		field := arChangeStr(e.Changes["field"])
		if field == "" {
			continue // an action that names no field, not part of this history
		}
		out = append(out, ARPolicyChange{
			Field:  field,
			Before: arChangeStr(e.Changes["before"]),
			After:  arChangeStr(e.Changes["after"]),
			// The role word is written FIRST, so a name resolution that never runs, fails, or comes
			// back short leaves a row that still says who was allowed to do this.
			Who:  arActorWord(e.ActorRole),
			When: e.CreatedAt,
		})
		attrib = append(attrib, attribution{actorID: e.ActorID, roleCode: e.ActorRole})
	}
	if s.names == nil {
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
	names, nerr := s.names.ActorNames(ctx, ids)
	if nerr != nil {
		return out, nil
	}
	for i := range out {
		out[i].Who = arWhoWord(names[attrib[i].actorID], attrib[i].roleCode)
	}
	return out, nil
}

// arChangeStr renders one audit `changes` value as the string the history wire format carries.
//
// IT MUST NOT BE fmt.Sprint, AND AR IS WHERE THAT MATTERS. audit's record boundary DROPS EMPTY
// VALUES (service.go's put()), so a knob whose pre-image was empty writes a row with no `before` key
// at all, and fmt.Sprint of a missing map entry is the four characters "<nil>". Dispatch is safe
// from this by accident because every knob it records is an int or a non-empty CHECK enum. AR is
// not: untagged_fallback and ar_tier are TEXT columns that ValidateARLandingPolicy only defaults on
// the way IN, so a row saved over a half-migrated pre-image carries an empty before. A history that
// says a setting used to be "<nil>" is a history reporting a bug in itself. Empty in, empty out; the
// client's word map turns that into words.
//
// The float64 arm is the jsonb round-trip: this module stringifies every value before recording, so
// it should never fire, but a row written by an older shape (or by a fake in a test) would come back
// as a float64 and must not render as "1.25e+03".
func arChangeStr(v any) string {
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

// arRoleTitles are the role NAMES as the roles table itself spells them (migration 0015, plus 0043's
// platform role). They are what goes in the parentheses after a person: "Dana Reid (Payables/
// Receivables)".
//
// COPIED FROM THE SEED, NOT INVENTED HERE. roles.name is already the human spelling of roles.code,
// and the audit row records the code; a map that said "Credit Controller" where the roster says
// "Financial Controller" would put two names for one job in front of the same person.
var arRoleTitles = map[string]string{
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

// arRoleTitle spells a role code the way the roster spells it. The default arm degrades honestly -
// de-underscored and sentence-cased, so a role added to the table later reads as slightly strange
// rather than as a confident wrong title, and never reaches a screen SHOUTING. An absent role is ""
// so the caller can tell "no role recorded" from "a role I have no title for".
func arRoleTitle(roleCode string) string {
	if roleCode == "" {
		return ""
	}
	if t, ok := arRoleTitles[roleCode]; ok {
		return t
	}
	lower := strings.ToLower(strings.ReplaceAll(roleCode, "_", " "))
	return strings.ToUpper(lower[:1]) + lower[1:]
}

// arActorWord names who made a change in words rather than as a role code, for the rows where a name
// could not be resolved. An ABSENT role is "somebody", never blank, a history row with no who is a
// row that looks broken.
func arActorWord(roleCode string) string {
	switch roleCode {
	case "":
		return "somebody"
	case "SUPER_ADMIN":
		return "an administrator"
	case "GENERAL_MANAGER":
		return "the general manager"
	case "FINANCIAL_CONTROLLER":
		return "the controller"
	case "PAYABLES_RECEIVABLES":
		return "somebody in receivables"
	case "BRANCH_MANAGER":
		return "a branch manager"
	default:
		return "a " + strings.ToLower(strings.ReplaceAll(roleCode, "_", " "))
	}
}

// arWhoWord composes the one string this history puts on the wire under `who`.
//
// THE NAME GOES ON THE WIRE, NEVER THE ID. A settings history is read to find out who to go and talk
// to, and a uuid answers that for nobody; the role alone answers it for a dealer with one controller
// and for nobody with two branch managers. The role stays in the parentheses because it is the part
// that says whether the change was allowed.
//
// The ladder is total and never produces a blank cell:
//
//	name + role → "Dana Reid (Financial Controller)"
//	name only   → "Dana Reid"          (a role the roster no longer carries)
//	neither     → "a branch manager"   (the actor is gone, or was a system actor)
//	nothing     → "somebody"
func arWhoWord(name, roleCode string) string {
	name = strings.TrimSpace(name)
	title := arRoleTitle(roleCode)
	switch {
	case name != "" && title != "":
		return name + " (" + title + ")"
	case name != "":
		return name
	default:
		return arActorWord(roleCode)
	}
}
