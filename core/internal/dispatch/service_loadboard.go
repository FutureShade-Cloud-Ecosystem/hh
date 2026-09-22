package dispatch

// service_loadboard.go, the load-building service (Aug-26 call, items A-I).
//
// FOUR THINGS THIS FILE IS RESPONSIBLE FOR, AND ONLY THESE:
//
//   1. THE MONEY GATE. A load's dollar value and its freight share are marshalled for a caller the
//      dealer's dispatch_policy.load_value_visibility allows and for nobody else. They are absent by
//      TYPE from the general Stop payload (model.go says why) and appear only on LoadStop, which is
//      only ever built by loadStops() below, which has already asked the policy. Fail closed: an
//      unreadable policy, an unresolvable caller, or an unwired repository all deny.
//
//   2. THE COMMITMENT (0185). One state machine on the LOAD: REQUESTED until a dispatcher commits it
//      onto a truck, COMMITTED after. Changing a committed load is gated; BUILDING one is not, and
//      reopening one is an ordinary act with a reason on it. Enforced on the WRITE PATH, in the
//      service, before the repository is touched, so it refuses the mutation no matter what the
//      client sends, which is the only kind of gate there is.
//
//   3. THE ARITHMETIC. The freight split is computed here from integer cents and written in one
//      transaction. A client may ask for "even, over three loads" or propose an override; it may
//      never hand over the numbers and have them stored unchecked.
//
//   4. THE COUNTS. The quick cards are computed from the SAME rows the board renders, in the same
//      call, so a count cannot disagree with the board underneath it. A count that can be stale is
//      worse than no count.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/settings"
)

// OrderValueReader is the consumer-declared seam for "what is this order worth".
//
// IT IS DELIBERATELY NOT A WIDENING OF OrderReader. module.go states that the four-field authority
// brief is FROZEN and that a consumer needing more asks through its own interface (that is exactly
// what picksource.go's OrderLineReader did, and its header says why). Value is a different question
// with a different failure mode: a brief that cannot be read means the order does not exist, a value
// that cannot be read means the gauge shows a dash.
//
// CONTRACT: (cents, known, err). known=false with a nil error means the order carries no total worth
// stamping, the load's value stays NULL, which the card prints as '-' and never as zero.
type OrderValueReader interface {
	// OrderValueCents is what the MATERIAL on the order is worth, the client's porcelain number.
	OrderValueCents(ctx context.Context, orderID string) (cents int64, known bool, err error)
	// OrderFreightCents is the freight the order carries, which is the figure a split divides. It is
	// read here, on the server, and never accepted from a request: a client that supplies both the
	// total and the split has supplied the answer to its own sum.
	OrderFreightCents(ctx context.Context, orderID string) (cents int64, known bool, err error)
	// OrderQuotedFreightCents is the frozen baseline (0194) the second-truck gate measures a raise
	// against, what the customer was actually shown, at conversion, and never moved since.
	//
	// ITS THIRD RETURN IS NOT THE SAME BOOLEAN AS THE TWO ABOVE, and conflating them would break the
	// gate in the direction that costs a customer money. `known` on a VALUE means "there is a figure
	// worth stamping", and the shipped adapter derives it from `cents > 0` because a load worth
	// nothing is a load nobody priced. `quoted` here means the column is NOT NULL, and 0 is a
	// perfectly good baseline, the very one a "free delivery" quote produces. An adapter that
	// answered quoted=false for a quoted zero would turn every promise of free delivery into an
	// order that can be charged freight without anybody being asked. 0194's own header forbids
	// reading that column through a "<= 0 is unknown" rule; this is the signature that makes obeying
	// it possible.
	OrderQuotedFreightCents(ctx context.Context, orderID string) (cents int64, quoted bool, err error)
}

// WithOrderValues injects the value seam. Nil is honest: loads schedule with no value stamped, and
// the card shows a dash rather than a number nobody produced.
func (s *Service) WithOrderValues(r OrderValueReader) *Service { s.values = r; return s }

// WithStockAvailability injects the procurement-gap seam. Nil means the board reports NO GAPS rather
// than refusing to draw, the gate posture (service_w3.go: "a gate that cannot answer must allow -
// the alternative is a yard stopped by an outage"), with the honesty flag below so the surface can
// say the check did not run instead of implying the yard is clear.
func (s *Service) WithStockAvailability(r StockAvailabilityReader) *Service { s.stock = r; return s }

// WithOrderLines injects the line reader the gap scan walks. The pick feed already declares this
// seam (picksource.go); the service consumes the same interface rather than a second copy.
func (s *Service) WithOrderLines(r OrderLineReader) *Service { s.lines = r; return s }

// - the policy -

// PolicyFor resolves the effective policy for a branch, FAILING CLOSED. Every failure path, no
// repository, no row, a scan error, returns DefaultPolicy(), which keeps a commitment reopenable
// only by a dispatch manager and keeps value manager-only. A service that ran open because a SELECT
// failed would be a gate that opens itself under load.
func (s *Service) PolicyFor(ctx context.Context, branchID string) Policy {
	if s.repo == nil {
		return DefaultPolicy()
	}
	p, err := s.repo.ReadPolicy(ctx, branchID)
	if err != nil {
		s.log.Warn("dispatch: policy read failed, failing closed", "branch_id", branchID, "err", err)
		return DefaultPolicy()
	}
	out, err := ValidatePolicy(p)
	if err != nil {
		s.log.Warn("dispatch: stored policy is not valid, failing closed", "branch_id", branchID, "err", err)
		return DefaultPolicy()
	}
	return out
}

// DriverSessionHours answers the ONE question identity's SessionTTLResolver seam asks: how long, in
// hours, is a driver's session honoured (0209). It is the whole of dispatch's side of that seam.
//
// THREE PROPERTIES, EACH OF WHICH IS THE POINT OF A LINE OF CODE HERE.
//
//  1. IT TAKES NO BRANCH AND HAS NOWHERE TO PUT ONE. The dealer-default row (branch_id IS NULL) is
//     the only row it reads, because a token is minted at LOGIN, before anybody has named a branch,
//     for a person whose grants may be several and whose scope may be GLOBAL. Choosing a branch there
//     would mean choosing one of "first grant", "largest value" or "home branch", and every one of
//     those makes a person's session length depend on data they cannot see and did not pick. 0209's
//     header argues this at length; the empty string passed to PolicyFor is that argument in code,
//     and PolicyFor's own SQL (`$1 <> ” AND branch_id::text = $1 OR branch_id IS NULL`) resolves it
//     to the default row and nothing else.
//
//  2. IT IS UNFENCED, DELIBERATELY, AND IT IS NOT A LEAK. Every other policy read on this service
//     goes through s.caller and requireBranch. This one cannot: it runs during Login, when there is
//     no caller yet, that is what it is FOR. What makes it safe is that it returns one integer that
//     is already the dealer's published setting, carries no branch, no money, no identifier and no
//     row the caller did not already cause to be written by signing in successfully.
//
//  3. IT FAILS CLOSED TO THE SHORT NUMBER. PolicyFor already returns DefaultPolicy() on every failure
//     path, no repository, no row, an unreadable scan, a stored policy that no longer validates -
//     and DefaultPolicy's DriverSessionHours is today's twelve. So an outage shortens a driver's
//     session back to what it has always been; it can never lengthen one.
func (s *Service) DriverSessionHours(ctx context.Context) int {
	return s.PolicyFor(ctx, "").DriverSessionHours
}

// ReadPolicy is the fenced read the settings surface calls.
func (s *Service) ReadPolicy(ctx context.Context, branchID string) (Policy, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return Policy{}, err
	}
	if err := s.requireBranch(caller, branchID); err != nil {
		return Policy{}, err
	}
	return s.PolicyFor(ctx, branchID), nil
}

// SavePolicy writes the dealer default or a branch override. MANAGER-GATED IN THE SERVICE, not only
// at the mux, this changes who may reopen a committed load and who may see a load's value, and an
// authority that lives only in a route registration is an authority one refactor away from gone.
func (s *Service) SavePolicy(ctx context.Context, p Policy) (Policy, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return Policy{}, err
	}
	if !caller.IsManager() {
		return Policy{}, ErrForbidden
	}
	if err := s.requireBranch(caller, p.BranchID); err != nil {
		return Policy{}, err
	}
	clean, err := ValidatePolicy(p)
	if err != nil {
		return Policy{}, err
	}
	clean.BranchID = p.BranchID
	if s.repo == nil {
		return Policy{}, fmt.Errorf("dispatch: repository is not wired")
	}
	// THE PRE-IMAGE IS READ BEFORE THE WRITE, which is the whole reason before→after can be truthful.
	// This save used to write the NEW values into the audit row and nothing else, so the trail said
	// what the policy became and never what it had been, and "commit_prompt_hours is 24" is not a
	// change, it is a reading.
	//
	// A FAILED PRE-READ DOES NOT BLOCK THE SAVE. The dealer's intent is the important thing; losing
	// the before-image costs a history row, and refusing a legitimate policy change because a second
	// read failed would cost them the change itself.
	before, beforeErr := s.repo.ReadPolicy(ctx, clean.BranchID)
	if err := s.repo.UpsertPolicy(ctx, clean); err != nil {
		return Policy{}, err
	}
	if beforeErr == nil {
		// ONE ROW PER CHANGED KNOB, in the field/before/after shape DIB-175 already proved and the
		// audit allowlist already admits. The alternative, a new allowlisted key per knob per module
		//, scales to a key per setting in the system, and the allowlist is deny-by-default precisely
		// so that it stays small enough to read.
		for _, ch := range policyChanges(before, clean) {
			s.record(ctx, audit.SensitiveAccess{
				Action: "DISPATCH_POLICY_CHANGED", EntityType: "dispatch_policy", EntityID: clean.BranchID,
				// actor_role, not a name and not a user id. It is already allowlisted and already
				// lifted into a first-class column by the audit repository, and it answers the
				// question a settings history is actually asked, "who is allowed to have done this"
				//, without putting an identifier on a screen. A person's NAME would need the staff
				// directory read the price-change log uses, and that is a cross-module read this
				// module does not have; it is recorded as a deferral rather than faked.
				Extra: map[string]any{
					"field": ch.Field, "before": ch.Before, "after": ch.After,
					"actor_role": caller.RoleCode,
				},
			})
		}
	} else {
		s.log.Warn("dispatch: policy pre-image unreadable, change history will be short one entry",
			"branch", clean.BranchID, "err", beforeErr)
	}
	return clean, nil
}

// PolicyTrailReader is the READ half of the audit seam, "what has been recorded about this thing".
//
// IT IS ITS OWN INTERFACE rather than a widening of audit.Recorder, which every module in this program
// implements and fakes. Adding a read method there would make a dozen fakes grow a method none of them
// wants, to serve one screen's disclosure. NIL IS AN EMPTY HISTORY, never an error.
type PolicyTrailReader interface {
	ReopenTransaction(ctx context.Context, entityType, entityID string) ([]audit.Entry, error)
}

// WithAuditTrail injects the read seam (satisfied by *audit.Service at the composition root).
func (s *Service) WithAuditTrail(r PolicyTrailReader) *Service { s.trail = r; return s }

// ActorNameResolver turns actor ids into the names people call each other by.
//
// A CONSUMER-DECLARED INTERFACE, satisfied by a thin adapter at the composition root, the same
// shape order.ActorNameResolver and quote.ActorNamer already take, and satisfied in main.go by the
// SAME staffNameAdapter the pos and billing histories use. Dispatch does not read the users table,
// does not learn what a user row looks like, and does not gain a third copy of
// `SELECT display_name FROM users`.
//
// It is deliberately a BATCH call: a history page is one read, not one read per row.
type ActorNameResolver interface {
	ActorNames(ctx context.Context, ids []string) (map[string]string, error)
}

// WithActorNames injects the name seam. Absent, every history row still answers, with the role
// word it answered with before item 4a, which is the degradation this whole ladder is built on.
func (s *Service) WithActorNames(r ActorNameResolver) *Service { s.names = r; return s }

// roleTitles spells a role the way the staff roster spells it. Kept per-module rather than shared
// because each module's history is its own screen and Go cannot share a var across packages without
// one importing the other, which is the coupling the seam above exists to avoid.
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

// roleTitle spells a role code. The default arm degrades honestly, de-underscored, sentence-cased -
// so a role added to the table later reads as slightly strange rather than as a confident wrong
// title, and never reaches a screen as SHOUTING_SNAKE_CASE.
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

// changeStr renders a recorded value without ever printing "<nil>".
//
// THE TRAP IT CLOSES, and it is a live one on this screen: audit's put() DROPS empty values, so a
// knob whose previous value was the empty string writes a row with no `before` key at all, and
// fmt.Sprint of a missing map entry is the four characters "<nil>". A history that says a setting
// used to be "<nil>" is a history reporting a bug in itself. Dispatch was safe by ACCIDENT (every
// dispatch knob is an int or a non-empty CHECK enum, so no key was ever dropped); it is safe on
// purpose now, because the next knob added here will not be.
//
// The float64 arm is the jsonb round-trip: this module stringifies before recording, so it should
// never fire, but a row written by an older shape would come back a float64 and must not render as
// "1.25e+03".
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
		return fmt.Sprint(t)
	}
}

// whoWord is the LADDER, and every rung of it is a real answer:
//
//	name + role   "Robin D (General Manager)"       what the brief asked for
//	name          "Robin D"                         the role was not recorded
//	role word     "a dispatch manager"              the name could not be resolved, what this
//	                                                screen said before item 4a, so a name seam that
//	                                                is unwired or failing costs nothing
//	"somebody"    neither                           a history row with no who looks broken
//
// THE NAME GOES ON THE WIRE AND THE ID NEVER DOES. service_loadboard.go's original comment chose a
// role "without putting an identifier on a screen", and that intent is kept exactly: resolving the
// name SERVER-side means the id stays here, where e.ActorID already was.
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

// PolicyChange is one knob that moved.
type PolicyChange struct {
	Field  string `json:"field"`
	Before string `json:"before"`
	After  string `json:"after"`
	Who    string `json:"who,omitempty"`
	When   string `json:"when,omitempty"`
}

// policyChanges diffs two policies into the rows a history shows.
//
// ONLY REAL CHANGES ARE EMITTED. A save that moved one knob writes one row; a save that moved nothing
// writes none. A history that logged every save whether or not anything changed would be a list of
// times somebody pressed a button, which is not what "who changed this, and from what" asks.
//
// Every value here is an int or a CHECK-constrained enum, so none can carry a customer identifier and
// the audit layer's cleartext redaction has nothing to catch. That is a property of the policy shape,
// not luck, and it is why these are safe to persist verbatim.
func policyChanges(before, after Policy) []PolicyChange {
	out := []PolicyChange{}
	add := func(field, b, a string) {
		if b != a {
			out = append(out, PolicyChange{Field: field, Before: b, After: a})
		}
	}
	add("commit_prompt_hours", strconv.Itoa(before.CommitPromptHours), strconv.Itoa(after.CommitPromptHours))
	add("reopen_policy", before.ReopenPolicy, after.ReopenPolicy)
	add("load_value_visibility", before.LoadValueVisibility, after.LoadValueVisibility)
	add("commit_horizon_weeks", strconv.Itoa(before.CommitHorizonWeeks), strconv.Itoa(after.CommitHorizonWeeks))
	add("working_view_days", strconv.Itoa(before.WorkingViewDays), strconv.Itoa(after.WorkingViewDays))
	add("freight_split_mode", before.FreightSplitMode, after.FreightSplitMode)
	// 0209. A change to how long a driver stays signed in is a change to how long a credential is
	// honoured, so it is the LAST knob on this list anybody should be able to move without a trace.
	// It rides the same field/before/after row as the other five, no new allowlisted audit key, no
	// second history shape, and the settings screen's disclosure picks it up for free.
	add("driver_session_hours", strconv.Itoa(before.DriverSessionHours), strconv.Itoa(after.DriverSessionHours))
	return out
}

// actorWord names who made a change, in words rather than as a role code. LOGISTICS_MANAGER is a row
// in our roles table; "a dispatch manager" is what somebody reading a change history would say.
//
// Same degrade-honestly default arm as every other word map in this program: an unmapped role comes
// back as itself, lowercased and de-underscored, so a role added later reads as strange rather than
// as a confident wrong title. An ABSENT role is "somebody", never blank, a history row with no who
// is a row that looks broken.
func actorWord(roleCode string) string {
	switch roleCode {
	case "":
		return "somebody"
	case "SUPER_ADMIN", "TECH_ADMIN":
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

// PolicyHistory is the quiet trail the settings screen shows behind a disclosure: who changed which
// rule, and what it was before.
//
// IT READS THE AUDIT TRAIL RATHER THAN A TABLE OF ITS OWN. The change is already recorded, by the
// module that made it, in the one place this program keeps "who did what"; a second history table
// would be a second thing to keep in step and a second thing to forget to write to.
//
// Manager-gated to match SavePolicy: somebody who may change the rules may see who changed them.
func (s *Service) PolicyHistory(ctx context.Context, branchID string, limit int) ([]PolicyChange, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return nil, err
	}
	if !caller.IsManager() {
		return nil, ErrForbidden
	}
	if err := s.requireBranch(caller, branchID); err != nil {
		return nil, err
	}
	if branchID == "" {
		return nil, httpx.BadRequestf("dispatch: branch_id is required")
	}
	if s.trail == nil {
		// UNWIRED IS AN EMPTY HISTORY, NOT AN ERROR. A deployment that has not attached the audit read
		// still gets a working settings screen; the disclosure simply says nothing has been recorded,
		// which is what it can honestly say.
		return []PolicyChange{}, nil
	}
	entries, err := s.trail.ReopenTransaction(ctx, "dispatch_policy", branchID)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	out := []PolicyChange{}
	// NEWEST FIRST, because a history is read from the top and the question is almost always "what
	// changed recently". The trail arrives oldest-first (its causal order), so it is walked backwards.
	//
	// TWO PASSES, AND THE SECOND IS ONE READ. The rows are collected first so every actor id on the
	// page can be resolved in a single batch call, a per-row lookup would turn a history page into
	// N queries, and the seam is a batch for exactly that reason.
	type pending struct {
		change   PolicyChange
		actorID  string
		roleCode string
	}
	rows := make([]pending, 0, limit)
	for i := len(entries) - 1; i >= 0 && len(rows) < limit; i-- {
		e := entries[i]
		field, _ := e.Changes["field"].(string)
		if field == "" {
			continue // a row from before the per-knob shape, or an action that names no field
		}
		rows = append(rows, pending{
			change: PolicyChange{
				Field:  field,
				Before: changeStr(e.Changes["before"]),
				After:  changeStr(e.Changes["after"]),
				When:   e.CreatedAt,
			},
			actorID:  e.ActorID,
			roleCode: e.ActorRole,
		})
	}

	// THE NAMES. A failure here is NOT a failure of the history: every row already carries the role
	// word this screen showed before item 4a, so an unwired seam, a dropped connection or an actor
	// nobody can name all degrade to exactly the previous behaviour rather than to a blank or an
	// error. That is the whole reason the ladder is built the way it is.
	names := map[string]string{}
	if s.names != nil {
		ids := make([]string, 0, len(rows))
		seen := map[string]bool{}
		for _, r := range rows {
			if r.actorID != "" && !seen[r.actorID] {
				seen[r.actorID] = true
				ids = append(ids, r.actorID)
			}
		}
		if len(ids) > 0 {
			if m, err := s.names.ActorNames(ctx, ids); err == nil {
				names = m
			}
		}
	}

	for _, r := range rows {
		r.change.Who = whoWord(names[r.actorID], r.roleCode)
		out = append(out, r.change)
	}
	return out, nil
}

// loadValueAllowed is THE GATE. It answers one question, may THIS caller receive a load's money
// figures at THIS branch, and it answers "no" to every question it cannot answer.
func (s *Service) loadValueAllowed(ctx context.Context, branchID string) bool {
	caller, err := s.caller(ctx)
	if err != nil {
		return false
	}
	switch s.PolicyFor(ctx, branchID).LoadValueVisibility {
	case settings.VisibilityAllStaff:
		return true
	case settings.VisibilityHidden:
		return false
	default: // MANAGER_UP, the column default, and where a partial read lands
		return caller.IsManager()
	}
}

// - the load board payload -

// LoadStop is a stop AS THE LOAD BOARD SEES IT: the ordinary stop plus the two money figures, and
// the money figures exist on this type and on no other. A caller the policy refuses gets a LoadStop
// with both pointers nil, which marshals with neither key present.
type LoadStop struct {
	Stop
	ValueCents   *int64 `json:"value_cents,omitempty"`
	FreightCents *int64 `json:"freight_cents,omitempty"`
	// GapCount is how many lines on this load the yard is short of. Zero renders nothing; the surface
	// shows a gap badge only when there is a gap (0 gaps is not news, 1 gap is).
	GapCount int `json:"gap_count,omitempty"`
}

// LoadRow is one truck's lane on the board: the run, what is on it, what that weighs against what
// the truck can take, and whether what is on it has been COMMITTED.
type LoadRow struct {
	Run    Run        `json:"run"`
	Gauge  LoadGauge  `json:"gauge"`
	Commit LoadCommit `json:"commit"`
	Stops  []LoadStop `json:"stops"`
}

// QuickCounts are the cards across the top. Computed from the SAME rows the board below renders, in
// the same call, the client never adds anything up, so a card and the board cannot disagree.
type QuickCounts struct {
	Deliveries int `json:"deliveries"`
	// Pickups is OPEN CUSTOMER PICKUPS, orders promised for collection that nobody has collected -
	// and it is the sum of the two below.
	//
	// D-110, AND WHAT IT REPLACED. This number used to be a count of LIVE RACK-TAG CODES
	// (will_call_pickups WHERE status='ACTIVE'). That is a different population in both directions: a
	// pickup nobody has pulled yet has no code at all, because the mint gate refuses one until the
	// pick is staged, so the pickups most in need of attention were the ones the card could not see;
	// and a code that was printed and not yet expired kept counting after the customer had driven
	// away with the material. The card is now counted from the PICK-QUEUE population, which is what a
	// yard lead means by "customer pickups".
	Pickups int `json:"pickups"`
	// PickupsAwaitingPick and PickupsReadyToCollect are the split the ruling asks for, "where they
	// are", in a principal's words, rather than how many there are. They are counted from the same
	// population in the same call as Pickups, so the two numbers and their sum cannot disagree.
	PickupsAwaitingPick   int `json:"pickups_awaiting_pick"`
	PickupsReadyToCollect int `json:"pickups_ready_to_collect"`
	// PickupsCapped says the split was computed over a bounded slice of a very large population, so
	// the numbers are a floor rather than the truth. It is on the wire because a card that quietly
	// undercounts is worse than one that says it is unsure.
	PickupsCapped bool `json:"pickups_capped,omitempty"`
	// Unassigned counts the WORKING WINDOW, not the day, because that is exactly what the staging lane
	// shows. A card whose number disagrees with the lane under it is worse than no card.
	Unassigned int `json:"unassigned"`
	Ready      int `json:"ready"`
	Requested  int `json:"requested"`
	Committed  int `json:"committed"`
	// Due is the prompt: loads still REQUESTED whose delivery day is inside commit_prompt_hours.
	Due int `json:"due"`
}

// LoadBoardView is the whole surface in one read.
type LoadBoardView struct {
	BranchID string `json:"branch_id"`
	Date     string `json:"date"`
	Policy   Policy `json:"policy"`
	// ValueVisible tells the surface whether the ABSENCE of a value means "you may not see it" or
	// "nobody priced it". It is a capability echo, not data: it is a boolean about the caller, and it
	// carries no figure. Without it a manager with an unpriced load and a driver with a priced one
	// see the identical blank, which is the one confusion this surface cannot afford.
	ValueVisible bool `json:"value_visible"`
	// GapsChecked is the same honesty for procurement: false means the availability seam did not
	// answer, so "no gaps" must not be read as "the yard is clear".
	GapsChecked bool `json:"gaps_checked"`
	// CanCommit is the salesperson gate as a capability echo: false means this caller may request a
	// date and may never commit one, and the surface draws no commit control at all. It is a boolean
	// about the CALLER and carries no data (the ValueVisible posture, re-applied).
	CanCommit bool `json:"can_commit"`
	// CanReopen is the manager half: false means the visible one-action Reopen is not this person's.
	CanReopen bool `json:"can_reopen"`
	// The WORKING VIEW (0185). The truck lanes are the day; the staging lane is everything still
	// waiting to go on a truck between these two dates. The dealer: "what I don't want to do is have 100
	// items in their daily workflow view that aren't relevant until next week."
	WorkingFrom string      `json:"working_from"`
	WorkingTo   string      `json:"working_to"`
	WorkingDays int         `json:"working_days"`
	Counts      QuickCounts `json:"counts"`
	Trucks      []Truck     `json:"trucks"`
	Loads       []LoadRow   `json:"loads"`
	Staging     []LoadStop  `json:"staging"`
}

// LoadBoard is the dispatcher's 6:45am read: every run on the date with its gauge and its lock, the
// staging lane of stops that are on no truck, the quick-card counts, and the branch's trucks.
func (s *Service) LoadBoard(ctx context.Context, branchID, date string) (LoadBoardView, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return LoadBoardView{}, err
	}
	if err := s.requireBranch(caller, branchID); err != nil {
		return LoadBoardView{}, err
	}
	if branchID == "" {
		return LoadBoardView{}, httpx.BadRequestf("dispatch: branch_id is required")
	}
	if date == "" {
		date = s.now().UTC().Format("2006-01-02")
	}
	if !validDate(date) {
		return LoadBoardView{}, httpx.BadRequestf("dispatch: date must be YYYY-MM-DD")
	}

	runs, err := s.repo.ListRuns(ctx, branchID, date)
	if err != nil {
		return LoadBoardView{}, err
	}
	stops, err := s.repo.ListStops(ctx, branchID, date)
	if err != nil {
		return LoadBoardView{}, err
	}
	trucks, err := s.repo.ListTrucks(ctx, branchID, false)
	if err != nil {
		return LoadBoardView{}, err
	}

	policy := s.PolicyFor(ctx, branchID)
	showValue := s.loadValueAllowed(ctx, branchID)
	now := s.now()

	// THE WORKING VIEW. The truck lanes are the day being built; the staging lane is the work still
	// waiting, and it reaches working_view_days ahead, because a load requested for next Tuesday that
	// is invisible until somebody navigates to next Tuesday is a load nobody plans.
	from, to := WorkingWindow(date, policy.WorkingViewDays)
	waiting, err := s.repo.UnassignedInWindow(ctx, branchID, from, to)
	if err != nil {
		return LoadBoardView{}, err
	}

	gaps, gapsChecked := s.gapCounts(ctx, branchID, date)

	capacityOf := map[string]LoadCapacity{}
	for _, t := range trucks {
		capacityOf[t.ID] = LoadCapacity{CapacityKg: t.CapacityKg, CapacityM3: t.CapacityM3, PalletPositions: t.PalletPositions}
	}

	byRun := map[string][]Stop{}
	view := LoadBoardView{
		BranchID: branchID, Date: date, Policy: policy, ValueVisible: showValue,
		GapsChecked: gapsChecked, Trucks: trucks, Loads: []LoadRow{}, Staging: []LoadStop{},
		CanCommit:   canCommitLoad(caller) == nil,
		CanReopen:   canReopenLoad(caller, policy) == nil,
		WorkingFrom: from, WorkingTo: to, WorkingDays: policy.WorkingViewDays,
	}
	for _, st := range stops {
		if st.RunID != "" {
			byRun[st.RunID] = append(byRun[st.RunID], st)
		}
	}
	// The staging lane is the WINDOW's unassigned work, not the day's. The repository already excludes
	// cancelled and failed rows, so what is drawn and what is counted come from one slice.
	for _, st := range waiting {
		view.Staging = append(view.Staging, s.loadStop(st, showValue, gaps))
	}

	for _, run := range runs {
		row := LoadRow{Run: run, Stops: []LoadStop{}}
		mine := byRun[run.ID]
		row.Gauge = BuildGauge(mine, capacityOf[run.TruckID], showValue)
		committed, requested := 0, 0
		for _, st := range mine {
			if st.Status == StopCancelled || st.Status == StopFailed {
				continue
			}
			if st.CommitState == CommitCommitted {
				committed++
			} else {
				requested++
			}
		}
		row.Commit = EvaluateCommit(run.ScheduledDate, run.CommitState, committed, requested, policy, now)
		// The caller halves. Computed per board read from the SAME capability checks the write path
		// enforces, so a control and the server can never disagree.
		row.Commit.CanCommit = view.CanCommit
		row.Commit.CanReopen = row.Commit.Committed && row.Commit.Reopenable && canReopenLoad(caller, policy) == nil
		row.Commit.ReopenedAt = run.ReopenedAt
		row.Commit.ReopenReason = run.ReopenReason
		for _, st := range mine {
			row.Stops = append(row.Stops, s.loadStop(st, showValue, gaps))
		}
		row.Run.Commit = &row.Commit
		row.Run.Gauge = &row.Gauge
		view.Loads = append(view.Loads, row)
	}

	view.Counts = countQuickCards(stops, waiting, policy, now)
	return view, nil
}

// loadStop applies the gate to ONE stop. This is the only place a money figure is copied onto a
// payload, which is what makes the gate reviewable.
func (s *Service) loadStop(st Stop, showValue bool, gaps map[string]int) LoadStop {
	out := LoadStop{Stop: st, GapCount: gaps[st.ID]}
	if showValue {
		out.ValueCents = st.ValueCents
		out.FreightCents = st.FreightCents
	}
	return out
}

// countQuickCards computes the cards. DELIBERATELY DERIVED FROM THE SAME SLICES the board just
// rendered, not from a set of COUNT(*) queries: a card that ran its own query is a card that can be
// one transaction out of date, and the whole promise of these cards is that they are true at a glance.
//
// `day` is the board's date; `waiting` is the WORKING WINDOW's unassigned work, which is exactly what
// the staging lane draws, so Unassigned and that lane cannot disagree.
//
// Cancelled and failed stops are excluded from every count. A dispatcher counting "18 deliveries"
// means eighteen deliveries that are happening.
func countQuickCards(day []Stop, waiting []Stop, policy Policy, now time.Time) QuickCounts {
	c := QuickCounts{}
	for _, st := range day {
		if st.Status == StopCancelled || st.Status == StopFailed {
			continue
		}
		c.Deliveries++
		if st.Status == StopReady {
			c.Ready++
		}
		if st.CommitState == CommitCommitted {
			c.Committed++
		} else {
			c.Requested++
		}
	}
	for _, st := range waiting {
		c.Unassigned++
		// The prompt, and the only place the 48 hours still appears: a load nobody has committed whose
		// delivery day is close. It refuses nothing; it puts the load in front of a dispatcher.
		if st.CommitState != CommitCommitted && DueToCommit(st.ScheduledDate, now, policy.CommitPromptHours) {
			c.Due++
		}
	}
	return c
}

// SetPickupCounts folds the customer-pickup numbers into the counts. It is a SEPARATE read because a
// pickup is not a delivery stop, it never appears on a truck, and inventing it from delivery rows
// would be a number with no source.
//
// THE SUM IS COMPUTED HERE AND NOT PASSED IN, so the card and its split are arithmetically incapable
// of disagreeing. That was the failure mode of the shape this replaces: one setter for one number,
// with the breakdown left to whoever rendered it.
func (c *QuickCounts) SetPickupCounts(awaitingPick, readyToCollect int, capped bool) {
	c.PickupsAwaitingPick = awaitingPick
	c.PickupsReadyToCollect = readyToCollect
	c.Pickups = awaitingPick + readyToCollect
	c.PickupsCapped = capped
}

// - the commitment, enforced -

// canCommitLoad is item D of the ruling, as a capability. A dealer principal: "I don't think that the
// salesperson has any business even trying to attempt." The authority set lives in identity beside
// CanSellOnCredit, because "who may commit a delivery" is a role question and this package is not
// where the role catalog lives.
func canCommitLoad(caller identity.StaffContext) error {
	if !identity.CanCommitDelivery(caller) {
		return ErrCommitNotPermitted
	}
	return nil
}

// canReopenLoad is the SINGLE predicate behind both "may you reopen this" and "may you change a
// committed load", and they are one question on purpose: changing a committed load IS reopening it
// and re-committing it in one gesture. The client's sentence, "the dispatch manager should be able
// to unlock and run it again", says WHO may pass, not that they must press a button first.
//
//	NOBODY   nobody passes, including leadership. That is what the dealer chose.
//	MANAGER  a dispatch manager: someone who may commit AND holds a manager tier. (default)
//	ANYONE   anyone who may commit at all.
func canReopenLoad(caller identity.StaffContext, policy Policy) error {
	if policy.ReopenPolicy == ReopenNobody {
		return ErrReopenDisabled
	}
	if err := canCommitLoad(caller); err != nil {
		return err
	}
	if policy.ReopenPolicy == ReopenManager && !caller.IsManager() {
		return ErrReopenNotPermitted
	}
	return nil
}

// assertLoadChangeable is the guard every load-CHANGING write calls FIRST.
//
// IT REFUSES THE MUTATION, NOT THE BUTTON. The board disables a control on a committed load as a
// courtesy; this is the gate. It runs before the repository is reached, it reads the load's own state
// and the dealer's policy rather than anything on the request, and there is no field a client can
// send that skips it.
//
// WHAT IT COVERS, AND THE BUG THE PREVIOUS LANE FOUND (reversible in one line; NOTES carries it).
//
// 0184 read the client's "inside 48 hours the load is locked" as a clock window, and at its widest
// that froze the act of BUILDING a load: a 48-hour window measured back from a delivery day covers
// today AND tomorrow, so a dispatcher opening the board at 06:45 on Thursday could not put a pallet
// on Friday's truck without finding a manager. The shipped integration suite said so out loud -
// every dispatch flow in it builds a load for tomorrow.
//
// 0185 removes the clock from the gate entirely, which fixes that at the root rather than by carve-
// out. The gate is the COMMITMENT:
//
//	FREE, always, putting a REQUESTED load onto a truck (that act IS the commit), and recording what
//	  a load actually weighs. Neither takes anything away from anyone; both are the day being built.
//	GATED, changing a load somebody has already COMMITTED: moving it off its truck, moving it to
//	  another truck, or moving its date. Each of those changes something the yard is already picking
//	  against or the customer has already been told.
//
// AND WHO PASSES. Whoever may reopen the load may change it directly, and the OVERRIDE IS RECORDED -
// the audit row is the part that matters, and forcing a two-step would buy ceremony rather than
// authority. Everyone below that bar is refused absolutely, whatever they send.
//
// The visible Reopen control still exists and still earns its place: it returns the load to REQUESTED
// for the WHOLE TEAM with a stated reason, which is a different act from one manager making one
// change, and it is the one a principal asked for when a better-fitting order turns up.
func (s *Service) assertLoadChangeable(ctx context.Context, stop Stop) error {
	caller, err := s.caller(ctx)
	if err != nil {
		return err
	}
	if stop.CommitState != CommitCommitted {
		// A requested load is a plan. Anyone who can edit the board may change a plan.
		return nil
	}
	policy := s.PolicyFor(ctx, stop.BranchID)
	if err := canReopenLoad(caller, policy); err != nil {
		return conflictf("dispatch: %s is committed, reopen it first, or ask a dispatch manager to",
			loadName(stop))
	}
	s.record(ctx, audit.SensitiveAccess{
		Action: "DELIVERY_COMMITTED_LOAD_CHANGED", EntityType: "delivery_stop", EntityID: stop.ID,
		Extra: map[string]any{"order_id": stop.OrderID, "scheduled_date": stop.ScheduledDate},
	})
	return nil
}

// loadName is what a refusal calls the load. The order number is what is printed on the paperwork;
// the id is not something anybody can act on.
func loadName(stop Stop) string {
	if stop.OrderNo != "" {
		return stop.OrderNo
	}
	return "this load"
}

// assertRunEditable is the RUN-level door: changing the truck under a run, or its date, touches every
// load on it. It grades the run through the same one predicate, load by load, so there is no second
// rule to drift.
func (s *Service) assertRunEditable(ctx context.Context, run Run) error {
	if s.repo == nil {
		return nil
	}
	stops, err := s.repo.ListStopsForRun(ctx, run.ID)
	if err != nil {
		return err
	}
	for _, st := range stops {
		if st.Status == StopCancelled || st.Status == StopFailed {
			continue
		}
		if err := s.assertLoadChangeable(ctx, st); err != nil {
			return err
		}
	}
	return nil
}

// - committing, and reopening -

// CommitStop turns a salesperson's ASK into a dispatcher's PROMISE, optionally on a different date.
//
// The requested date survives (the repository back-fills it), because "we said the 14th and it went
// out the 16th" is the question this pair of states exists to answer.
func (s *Service) CommitStop(ctx context.Context, stopID, toDate string) (Stop, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return Stop{}, err
	}
	// ITEM D, AT THE DOOR. A salesperson may request and may never commit, enforced here and not only
	// by not drawing the button, because a button that is not drawn is not a gate.
	if err := canCommitLoad(caller); err != nil {
		return Stop{}, err
	}
	stop, err := s.guardStop(ctx, stopID)
	if err != nil {
		return Stop{}, err
	}
	date := strings.TrimSpace(toDate)
	if date != "" && !validDate(date) {
		return Stop{}, httpx.BadRequestf("dispatch: scheduled_date must be YYYY-MM-DD")
	}
	policy := s.PolicyFor(ctx, stop.BranchID)
	target := stop.ScheduledDate
	if date != "" {
		target = date
	}
	if !WithinHorizon(target, s.now(), policy.CommitHorizonWeeks) {
		return Stop{}, settings.Errf(http.StatusUnprocessableEntity,
			"dispatch: %s is outside the %d-week planning horizon", target, policy.CommitHorizonWeeks)
	}
	// Re-committing an already-committed load onto a DIFFERENT date is a change to a promise, and is
	// gated as one. Committing an ask is not.
	if date != "" && date != stop.ScheduledDate {
		if err := s.assertLoadChangeable(ctx, stop); err != nil {
			return Stop{}, err
		}
	}
	if err := s.repo.CommitStopDate(ctx, stop.ID, date, caller.UserID, s.now()); err != nil {
		return Stop{}, err
	}
	s.record(ctx, audit.SensitiveAccess{
		Action: "DELIVERY_DATE_COMMITTED", EntityType: "delivery_stop", EntityID: stop.ID,
		Extra: map[string]any{"from": stop.ScheduledDate, "to": target},
	})
	return s.repo.GetStop(ctx, stop.ID)
}

// ReopenStop is the reopen objection, built. "What if an order comes in that's a better fit for
// that load?", so the answer is ONE ACTION, visible on the load, for a dispatch manager: the load
// goes back to REQUESTED, the re-optimisation may shuffle it again, and the reason is recorded.
//
// It is not an error state and it does not apologise. The reason is required for the same purpose an
// unlock reason was: somebody reads it later when a customer asks why the truck changed.
func (s *Service) ReopenStop(ctx context.Context, stopID, reason string) (Stop, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return Stop{}, err
	}
	stop, err := s.guardStop(ctx, stopID)
	if err != nil {
		return Stop{}, err
	}
	policy := s.PolicyFor(ctx, stop.BranchID)
	if err := canReopenLoad(caller, policy); err != nil {
		return Stop{}, err
	}
	if stop.CommitState != CommitCommitted {
		return Stop{}, conflictf("dispatch: %s is already open, nobody has committed it", loadName(stop))
	}
	why, err := reopenReason(reason)
	if err != nil {
		return Stop{}, err
	}
	if err := s.repo.ReopenStop(ctx, stop.ID, why, caller.UserID, s.now()); err != nil {
		return Stop{}, err
	}
	s.record(ctx, audit.SensitiveAccess{
		Action: "DELIVERY_LOAD_REOPENED", EntityType: "delivery_stop", EntityID: stop.ID,
		Extra: map[string]any{"order_id": stop.OrderID, "reason": why},
	})
	return s.repo.GetStop(ctx, stop.ID)
}

// ReopenRun is the bulk form of the same act: reopen a WHOLE TRUCK so the day can be run again.
// The dealer: "the dispatch manager should be able to unlock and run it again." Every live load on the
// run returns to REQUESTED in one transaction, and the run row carries who/when/why.
func (s *Service) ReopenRun(ctx context.Context, runID, reason string) (Run, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return Run{}, err
	}
	run, err := s.guardRun(ctx, runID)
	if err != nil {
		return Run{}, err
	}
	policy := s.PolicyFor(ctx, run.BranchID)
	if err := canReopenLoad(caller, policy); err != nil {
		return Run{}, err
	}
	why, err := reopenReason(reason)
	if err != nil {
		return Run{}, err
	}
	if err := s.repo.ReopenRun(ctx, run.ID, why, caller.UserID, s.now()); err != nil {
		return Run{}, err
	}
	s.record(ctx, audit.SensitiveAccess{
		Action: "DELIVERY_RUN_REOPENED", EntityType: "delivery_run", EntityID: run.ID,
		Extra: map[string]any{"run_no": run.RunNo, "reason": why},
	})
	return s.repo.GetRun(ctx, run.ID)
}

// reopenReason is the one validator both reopens share. A reopen with no reason is an audit row that
// explains nothing, and this is the row somebody reads after a truck went out differently.
func reopenReason(reason string) (string, error) {
	why := strings.TrimSpace(reason)
	if why == "" {
		return "", settings.Errf(http.StatusUnprocessableEntity,
			"dispatch: say why this load is being reopened, somebody will read it after the truck has gone")
	}
	if len(why) > 400 {
		return "", settings.Errf(http.StatusUnprocessableEntity, "dispatch: keep the reason under 400 characters")
	}
	return why, nil
}

// ClearRunReopen takes the REOPENED marker off a run once its loads have been committed again.
// Anyone who can edit the board may do it: tidying a marker is not the act that needs authority, and
// the loads' own states are the authority regardless.
func (s *Service) ClearRunReopen(ctx context.Context, runID string) (Run, error) {
	run, err := s.guardRun(ctx, runID)
	if err != nil {
		return Run{}, err
	}
	if err := s.repo.ClearRunReopen(ctx, run.ID); err != nil {
		return Run{}, err
	}
	return s.repo.GetRun(ctx, run.ID)
}

// - the load's own measurements -

// LoadFactsInput is what somebody weighing a load types. Every field is a POINTER so "leave it
// alone" and "set it to zero" are different requests, an empty weight box must not silently zero a
// measurement the yard took this morning.
type LoadFactsInput struct {
	WeightKg        *float64 `json:"weight_kg"`
	VolumeM3        *float64 `json:"volume_m3"`
	PalletPositions *int     `json:"pallet_positions"`
	Clear           []string `json:"clear"`
}

// SetLoadFacts records what a load weighs and takes up. NOT money: value is stamped from the order
// and is never typed on this screen.
func (s *Service) SetLoadFacts(ctx context.Context, stopID string, in LoadFactsInput) (Stop, error) {
	stop, err := s.guardStop(ctx, stopID)
	if err != nil {
		return Stop{}, err
	}
	if in.WeightKg != nil && *in.WeightKg < 0 {
		return Stop{}, httpx.BadRequestf("dispatch: a weight cannot be negative")
	}
	if in.VolumeM3 != nil && *in.VolumeM3 < 0 {
		return Stop{}, httpx.BadRequestf("dispatch: a volume cannot be negative")
	}
	if in.PalletPositions != nil && *in.PalletPositions < 0 {
		return Stop{}, httpx.BadRequestf("dispatch: pallet positions cannot be negative")
	}
	// NOT LOCK-GUARDED, deliberately. Recording what a load actually weighs is not a change to the
	// plan, it is the yard telling the truth about the plan, and it happens at the forklift, inside
	// the window, on the loads that matter most. Refusing it would leave the gauge showing the guess.
	if err := s.repo.UpdateLoadFacts(ctx, stop.ID, in); err != nil {
		return Stop{}, err
	}
	return s.repo.GetStop(ctx, stop.ID)
}

// - the freight split -

// SplitInput asks for a split. `loads` is how many trucks the order is going out on; `amounts` is an
// OPTIONAL override in the same order as the loads.
//
// NOTE WHAT IS STILL NOT HERE: the answer. The server divides, the server validates, and a client
// can never hand over a set of numbers and have them stored unchecked.
//
// FreightCents is the second-truck field (0195), and it is the ONE thing a client may now propose
// that it could not before. It is not "the total" in the sense the original refusal meant, the
// original refusal was about a client supplying both the total AND the split, so that the sum
// checked itself. This is a PROPOSED CHARGE: what the customer should now pay for delivery because
// an unforeseen truck was added. Three properties keep it honest, and they are the reason it can
// exist at all:
//
//   - it is OPT-IN. Absent (nil), everything behaves exactly as it did: the order's own freight is
//     read from the order and divided. Every existing caller is unchanged.
//   - it can only go UP. A proposal below the order's current freight is refused in words; lowering
//     what a customer is charged is a credit note, and billing writes those.
//   - going up above the QUOTED baseline does not apply. It parks and waits for an approver. This
//     field is how a raise is ASKED FOR; it is not how one is made.
type SplitInput struct {
	OrderID      string  `json:"order_id"`
	Loads        int     `json:"loads"`
	Amounts      []int64 `json:"amounts,omitempty"`
	FreightCents *int64  `json:"freight_cents,omitempty"`
}

// SplitResult is what the surface renders back: the loads, and what each one now carries.
//
// Parked is the whole of the gate's answer, and its ABSENCE is the good news. When it is nil the
// split happened and Loads carry their new shares. When it is present nothing was written: Loads are
// the loads exactly as they already stood, FreightCents is what the order still charges, and the
// proposal named here is sitting on the approvals queue waiting for somebody who may decide it.
//
// It is one nullable object rather than a boolean plus an id plus a sentence, because those three
// can disagree and this cannot.
type SplitResult struct {
	OrderID      string            `json:"order_id"`
	FreightCents int64             `json:"freight_cents"`
	Override     bool              `json:"override"`
	Loads        []LoadStop        `json:"loads"`
	Parked       *FreightRaiseView `json:"parked,omitempty"`
}

// MaxLoadsPerOrder bounds a split. Three is the client's case; ten is a generous ceiling and a guard
// against a fat-fingered 300 minting three hundred stops.
const MaxLoadsPerOrder = 10

// SplitFreight divides one order's freight across its loads, minting loads as needed.
//
// THE ARITHMETIC HAPPENS HERE AND NOWHERE ELSE. The default is an even split with the remainder on
// the earliest loads (EvenSplit, no cent lost, none invented). An override is accepted only if it
// sums to the order's own freight EXACTLY; a set that is a dollar light is refused in words with
// both figures named, because the alternative is an invoice that is a dollar light.
func (s *Service) SplitFreight(ctx context.Context, in SplitInput) (SplitResult, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return SplitResult{}, err
	}
	if s.orders == nil {
		return SplitResult{}, fmt.Errorf("dispatch: order seam is not wired")
	}
	orderRef, err := s.resolveOrderRef(ctx, caller, in.OrderID)
	if err != nil {
		return SplitResult{}, err
	}
	brief, err := s.orders.Brief(ctx, orderRef)
	if err != nil {
		return SplitResult{}, ErrNotFound
	}
	if !caller.CanAccessBranch(brief.BranchID) {
		return SplitResult{}, ErrForbidden
	}
	if !s.loadValueAllowed(ctx, brief.BranchID) {
		// Splitting freight IS moving money between invoice lines. A caller who may not see the
		// figure may not divide it either.
		return SplitResult{}, ErrForbidden
	}
	if in.Loads < 1 || in.Loads > MaxLoadsPerOrder {
		return SplitResult{}, settings.Errf(http.StatusUnprocessableEntity,
			"dispatch: an order goes out on between 1 and %d loads", MaxLoadsPerOrder)
	}

	existing, err := s.repo.LoadsForOrder(ctx, brief.ID)
	if err != nil {
		return SplitResult{}, err
	}
	if len(existing) == 0 {
		return SplitResult{}, conflictf("dispatch: schedule this order for delivery before splitting its freight")
	}
	if len(existing) > in.Loads {
		return SplitResult{}, conflictf("dispatch: this order is already on %d loads, cancel one before splitting into %d",
			len(existing), in.Loads)
	}
	// Splitting an order across more trucks CHANGES every load it already has, so each one is graded
	// through the one predicate. A committed load refuses the split unless the caller could reopen it.
	for _, st := range existing {
		if err := s.assertLoadChangeable(ctx, st); err != nil {
			return SplitResult{}, err
		}
	}

	// NOTHING IS MINTED YET, AND THE ORDER OF THIS FUNCTION IS NOW LOAD-BEARING.
	//
	// Before 0195 the missing loads were inserted HERE, before the arithmetic, because the arithmetic
	// could not refuse. It can now: a raise above the quoted freight parks and waits for an approver,
	// and "the split is not applied" has to mean the FOURTH TRUCK TOO. A stop minted at ask time is a
	// load on tomorrow's board that nobody agreed to, sitting in front of a dispatcher who reads it as
	// a decision. So every insert moved below the gate, and the apply path mints them on the decide
	// transaction instead.
	//
	// The arithmetic that used to divide across len(existing) now divides across in.Loads. It is the
	// same number either way, the mint below grows `existing` to exactly in.Loads, and a shrink was
	// refused above, but in.Loads is the honest one to name while the loads do not exist yet.
	current, freightKnown, err := s.orderFreightCents(ctx, brief.ID)
	if err != nil {
		return SplitResult{}, err
	}
	if !freightKnown {
		// A plain split of nothing is still a refusal: silently dividing zero across three loads would
		// look like it worked. But a caller who is PROPOSING a figure is not asking to divide an
		// existing one, they are asking to charge for the extra truck on an order the customer was
		// quoted free delivery on, which is the client's case exactly. Treat the standing charge as
		// zero and let the gate grade the proposal against the real baseline.
		if in.FreightCents == nil {
			return SplitResult{}, conflictf("dispatch: this order carries no freight to split")
		}
		current = 0
	}

	// What the customer would be charged if this went through. Absent a proposal that is exactly what
	// they are charged now, which is why an ordinary split cannot trip the gate however it divides.
	proposed := current
	if in.FreightCents != nil {
		proposed = *in.FreightCents
		if proposed < 0 {
			return SplitResult{}, settings.Errf(http.StatusUnprocessableEntity, "dispatch: freight cannot be negative")
		}
		if proposed < current {
			// Deliberately a refusal and not a silent clamp. Dispatch adds trucks; it does not give
			// money back, and a screen that quietly ignored the figure typed into it would be worse
			// than one that says where the act belongs.
			return SplitResult{}, settings.Errf(http.StatusUnprocessableEntity,
				"dispatch: this order's freight is %s, lowering it is a credit note, not a split", money(current))
		}
	}

	amounts := EvenSplit(proposed, in.Loads)
	override := false
	if len(in.Amounts) > 0 {
		if len(in.Amounts) != in.Loads {
			return SplitResult{}, settings.Errf(http.StatusUnprocessableEntity,
				"dispatch: the split has %d amounts and the order is on %d loads", len(in.Amounts), in.Loads)
		}
		if err := ValidateOverride(in.Amounts, proposed); err != nil {
			return SplitResult{}, err
		}
		amounts = in.Amounts
		override = true
	}

	// - THE GATE -
	//
	// It lives HERE, after the arithmetic, before the first mint and before the only write, because
	// this is the single place in the system where a proposal becomes freight on delivery_stops.
	//
	// THERE IS NO SECOND DOOR, and it is asserted rather than asserted-to-be: one statement writes a
	// load's freight (repository_freightraise.go), it is reached through exactly two functions, and
	// the second of those can only be entered from an approval decision. `freightraise_wiring_test.go`
	// counts all three and fails if a fourth path appears.
	//
	// The baseline is read through the value seam and NEVER by touching orders here. dispatch owns no
	// row in that table and asks for the two figures it needs through the interface it declared.
	quoted, quotedKnown, err := s.quotedFreightCents(ctx, brief.ID)
	if err != nil {
		// FAIL CLOSED, and only here. Everywhere else in this module a read that cannot answer
		// degrades toward showing less; a gate that cannot answer must refuse, because the failure it
		// would otherwise wave through is a charge on somebody's invoice. loadValueAllowed takes the
		// same posture two hundred lines up, for the same reason.
		s.log.Warn("dispatch: quoted freight baseline unreadable, refusing the split",
			"order_id", brief.ID, "err", err)
		return SplitResult{}, conflictf("dispatch: this order's quoted delivery could not be read, so the freight cannot be changed here")
	}
	if GatesFreightRaise(proposed, current, quoted, quotedKnown) {
		return s.parkFreightRaise(ctx, caller, brief, existing, freightAsk{
			quoted: quoted, current: current, proposed: proposed, loads: in.Loads, amounts: in.Amounts,
		})
	}

	// Past the gate: mint the missing loads. They copy the parent's delivery facts, the material
	// arrived on two trucks, the driveway did not move.
	parent := existing[0]
	for seq := len(existing) + 1; seq <= in.Loads; seq++ {
		st, err := s.repo.InsertSplitLoad(ctx, parent, seq, in.Loads, caller.UserID)
		if err != nil {
			return SplitResult{}, asConflict(err, "dispatch: this order already has a load in that position")
		}
		existing = append(existing, st)
	}
	sort.SliceStable(existing, func(i, j int) bool { return existing[i].LoadSeq < existing[j].LoadSeq })

	ids := make([]string, len(existing))
	for i, st := range existing {
		ids[i] = st.ID
	}
	if err := s.applySplit(ctx, brief.ID, ids, amounts, override, current, proposed); err != nil {
		return SplitResult{}, err
	}
	s.record(ctx, audit.SensitiveAccess{
		Action: "DELIVERY_FREIGHT_SPLIT", EntityType: "order", EntityID: brief.ID, BranchID: brief.BranchID,
		Extra: map[string]any{"loads": len(existing), "override": override, "freight_cents": proposed},
	})

	fresh, err := s.repo.LoadsForOrder(ctx, brief.ID)
	if err != nil {
		return SplitResult{}, err
	}
	out := SplitResult{OrderID: brief.ID, FreightCents: proposed, Override: override, Loads: []LoadStop{}}
	for _, st := range fresh {
		out.Loads = append(out.Loads, s.loadStop(st, true, nil))
	}
	return out, nil
}

// applySplit writes one split, the loads' shares, and the order's own freight when it moved, in ONE
// transaction on dispatch's own pool.
//
// The header write is conditional on the figure actually having moved, and that is not an
// optimisation: `SetOrderFreightTx` reaches across a module boundary, and a seam called on every
// ordinary split (where nothing about the order changed) is a seam that has to be wired for the
// board to work at all. Wiring it stays required only for the thing it is for, a raise.
func (s *Service) applySplit(ctx context.Context, orderID string, stopIDs []string, amounts []int64, override bool, current, proposed int64) error {
	if proposed == current {
		return s.repo.WriteFreightSplit(ctx, stopIDs, amounts, override, len(stopIDs))
	}
	if s.freightWriter == nil {
		return httpx.BadRequestf("dispatch: the order freight seam is not wired, this order's freight cannot be changed here")
	}
	tx, err := s.repo.BeginTx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := s.repo.WriteFreightSplitTx(ctx, tx, stopIDs, amounts, override, len(stopIDs)); err != nil {
		return err
	}
	if err := s.freightWriter.SetOrderFreightTx(ctx, tx, orderID, proposed); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// orderFreightCents reads the order's own freight figure through the value seam. An unwired seam is
// a 501, not a zero: silently splitting nothing across three loads would look like it worked.
//
// IT NOW RETURNS `known` RATHER THAN REFUSING ON ITS OWN, and the refusal moved to the one caller.
// The shipped adapter maps `cents <= 0` to "no figure", which is right for a load's value and reads
// as "there is nothing here to divide", a fair refusal for a plain split. It is the WRONG refusal
// for a caller proposing a charge for an unforeseen truck on an order that was quoted free delivery:
// there is nothing to divide, and that is precisely why they are naming a figure. Only the caller
// knows which of the two it is looking at, so only the caller can pick the sentence.
func (s *Service) orderFreightCents(ctx context.Context, orderID string) (int64, bool, error) {
	if s.values == nil {
		return 0, false, httpx.BadRequestf("dispatch: the order value seam is not wired, freight cannot be split here")
	}
	return s.values.OrderFreightCents(ctx, orderID)
}

// - procurement gaps -

// gapCounts scans a day's live loads for material the yard does not have. It answers (counts, ran):
// `ran=false` means the seam did not answer and the surface must say the check did not run rather
// than draw a clear board.
func (s *Service) gapCounts(ctx context.Context, branchID, date string) (map[string]int, bool) {
	if s.stock == nil || s.lines == nil {
		return nil, false
	}
	triples, err := s.repo.StopsForGapScan(ctx, branchID, date)
	if err != nil {
		s.log.Warn("dispatch: gap scan read failed", "err", err)
		return nil, false
	}
	out := map[string]int{}
	for _, t := range triples {
		gaps, err := s.gapsForStop(ctx, branchID, t[0], t[1], t[2])
		if err != nil {
			return nil, false
		}
		if len(gaps) > 0 {
			out[t[0]] = len(gaps)
		}
	}
	return out, true
}

func (s *Service) gapsForStop(ctx context.Context, branchID, stopID, orderID, orderNo string) ([]ProcurementGap, error) {
	lines, err := s.lines.OrderPickLines(ctx, orderID)
	if err != nil {
		return nil, err
	}
	onHand := map[string]float64{}
	for _, l := range lines {
		if l.ProductID == "" {
			continue
		}
		if _, seen := onHand[l.ProductID]; seen {
			continue
		}
		qty, err := s.stock.OnHandBase(ctx, l.ProductID, branchID)
		if err != nil {
			return nil, err
		}
		onHand[l.ProductID] = qty
	}
	return GapsFor(stopID, orderNo, lines, onHand), nil
}

// GapsForOrder is the gap scan for ONE order, what the yard is short of before it starts pulling.
//
// WHY THE PICKER NEEDS THIS AND THE BOARD ALREADY HAD IT. A dealer principal: "we go to pick it 48 hours
// before, 24 hours before and we're missing something… make sure that we surface from a procurement
// perspective… really in their face." The scan existed and was wired to the DISPATCHER's board. The
// person who discovers the gap is the one at the forklift, and they were the one not being told.
//
// IT IS A SEPARATE READ RATHER THAN A FIELD ON THE TICKET, deliberately. Folding gaps onto every
// PickFeedTicket would put an inventory read per product per ticket inside a board refresh that is
// already N+1 on the ticket itself; the yard console fetches this when a pick is OPENED, which is the
// moment the answer is wanted and the only moment it is worth paying for. The load board's own gap
// badge (dispatch-loadboard.ts) already works exactly this way.
//
// THE SECOND RETURN IS HONESTY, NOT DECORATION. `false` means the scan could not run at all, the
// stock or line seam is unwired, and an empty list under it must never be read as "the yard is
// clear". It is the same flag BoardGaps returns, for the same reason, and the screen renders the same
// sentence off it.
func (s *Service) GapsForOrder(ctx context.Context, branchID, orderID string) ([]ProcurementGap, bool, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return nil, false, err
	}
	if err := s.requireBranch(caller, branchID); err != nil {
		return nil, false, err
	}
	if branchID == "" {
		return nil, false, httpx.BadRequestf("dispatch: branch_id is required")
	}
	if orderID == "" || !looksLikeUUID(orderID) {
		return nil, false, httpx.BadRequestf("dispatch: order id must be a uuid")
	}
	if s.stock == nil || s.lines == nil {
		return []ProcurementGap{}, false, nil
	}
	// The stop id is a LABEL on the gap rows, and a pickup has no stop. Passing the order id keeps
	// every gap traceable to something real rather than to an empty string.
	gaps, err := s.gapsForStop(ctx, branchID, orderID, orderID, "")
	if err != nil {
		return nil, false, err
	}
	return gaps, true, nil
}

// BoardGaps is the detailed read behind a gap badge: every short line on a day's loads.
func (s *Service) BoardGaps(ctx context.Context, branchID, date string) ([]ProcurementGap, bool, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return nil, false, err
	}
	if err := s.requireBranch(caller, branchID); err != nil {
		return nil, false, err
	}
	// NAME THE MISSING FIELD, LIKE EVERY SIBLING READ. Without this guard the empty string reached
	// StopsForGapScan and failed inside the driver, so a caller who simply forgot ?branch_id= was
	// told "internal error", a 500 that blames the server for the client's omission and tells
	// nobody what to fix. LoadBoard and Calendar have carried the same guard since 0184; this read
	// was the one that did not, and a badge that 500s is a badge a dispatcher learns to ignore.
	if branchID == "" {
		return nil, false, httpx.BadRequestf("dispatch: branch_id is required")
	}
	if date == "" {
		date = s.now().UTC().Format("2006-01-02")
	}
	if !validDate(date) {
		return nil, false, httpx.BadRequestf("dispatch: date must be YYYY-MM-DD")
	}
	if s.stock == nil || s.lines == nil {
		return []ProcurementGap{}, false, nil
	}
	triples, err := s.repo.StopsForGapScan(ctx, branchID, date)
	if err != nil {
		return nil, false, err
	}
	out := []ProcurementGap{}
	for _, t := range triples {
		gaps, err := s.gapsForStop(ctx, branchID, t[0], t[1], t[2])
		if err != nil {
			return nil, false, err
		}
		out = append(out, gaps...)
	}
	return out, true, nil
}

// - the calendar -

// Calendar answers the one question the client said the calendar is for: HOW MANY TRUCKS ARE FULL.
// Everything else on a square, how many runs, how many stops, how many still unassigned, is there
// to make that number readable, and nothing else is on it.
func (s *Service) Calendar(ctx context.Context, branchID, from, to string) ([]CalendarDay, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.requireBranch(caller, branchID); err != nil {
		return nil, err
	}
	if branchID == "" {
		return nil, httpx.BadRequestf("dispatch: branch_id is required")
	}
	if !validDate(from) || !validDate(to) {
		return nil, httpx.BadRequestf("dispatch: from and to must be YYYY-MM-DD")
	}
	f, _ := time.Parse("2006-01-02", from)
	t, _ := time.Parse("2006-01-02", to)
	if t.Before(f) {
		return nil, httpx.BadRequestf("dispatch: the range ends before it starts")
	}
	if t.Sub(f) > 400*24*time.Hour {
		return nil, httpx.BadRequestf("dispatch: a calendar range is at most a year")
	}
	days, err := s.repo.CalendarRows(ctx, branchID, from, to)
	if err != nil {
		return nil, err
	}
	// "Full" is a GAUGE question, so it is answered where the gauge lives rather than in SQL: a run
	// is full when its worst band is amber or red. Computing it in the query would mean teaching
	// Postgres the 85% line, and then there would be two of them.
	runs, err := s.repo.ListRunsRange(ctx, branchID, from, to)
	if err != nil {
		return nil, err
	}
	stops, err := s.repo.ListStopsRange(ctx, branchID, from, to)
	if err != nil {
		return nil, err
	}
	trucks, err := s.repo.ListTrucks(ctx, branchID, true)
	if err != nil {
		return nil, err
	}
	capacityOf := map[string]LoadCapacity{}
	for _, tr := range trucks {
		capacityOf[tr.ID] = LoadCapacity{CapacityKg: tr.CapacityKg, CapacityM3: tr.CapacityM3, PalletPositions: tr.PalletPositions}
	}
	byRun := map[string][]Stop{}
	for _, st := range stops {
		if st.RunID != "" {
			byRun[st.RunID] = append(byRun[st.RunID], st)
		}
	}
	fullByDate := map[string]int{}
	for _, r := range runs {
		if r.Status == RunCancelled {
			continue
		}
		g := BuildGauge(byRun[r.ID], capacityOf[r.TruckID], false)
		if g.Band == BandAmber || g.Band == BandRed {
			fullByDate[r.ScheduledDate]++
		}
	}
	for i := range days {
		days[i].Full = fullByDate[days[i].Date]
	}
	return days, nil
}

// PendingPickupCounts is the "customer pickups" card, as D-110 ruled it: OPEN PICKUPS SPLIT BY STAGE
// , awaiting pick, ready to collect, counted from the pick-queue population.
//
// WHAT IT REPLACED, AND WHY THAT NUMBER WAS WRONG IN BOTH DIRECTIONS. The card used to count live
// rack-tag CODES (CountActivePickups, repository_loadboard.go). A code is minted only from a STAGED
// pick, that gate is the point of the pull-instruction lane, so every pickup that nobody had pulled
// yet was invisible to a card whose whole job is to keep pickups "in your face". And a code that was
// printed and had not yet expired kept being counted after the customer had driven away with the
// material. The card was counting credentials and calling them customers.
//
// THE POPULATION IS THE PICK QUEUE'S OWN. PendingPickupOrderIDs is literally the same predicate the
// pickup half of the feed lists from (repository_w3.go's pickupFeedWhere + pickupAwaitingCollection),
// so the card's two numbers and the queue's population are the same set by construction rather than
// by two predicates that agree today.
//
// THE SPLIT COMES THROUGH THE SEAM, NOT THROUGH A JOIN. "Ready to collect" means the yard staged it,
// and that fact lives in the tasks module's own table. dispatch does not read it, PickCompletionReader
// (module.go) exists precisely to reverse that dependency, so the population is graded by asking the
// seam about those ids in ONE call. Unwired, the seam is nil and every open pickup is reported as
// awaiting pick: a yard that has not been wired has not staged anything, which is the honest reading
// rather than a zero that would hide the population entirely.
//
// Fenced like every other read; a caller who cannot reach the branch gets the fence's answer, not a
// zero.
func (s *Service) PendingPickupCounts(ctx context.Context, branchID string) (awaitingPick, readyToCollect int, capped bool, err error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return 0, 0, false, err
	}
	if err := s.requireBranch(caller, branchID); err != nil {
		return 0, 0, false, err
	}
	if branchID == "" || s.repo == nil {
		return 0, 0, false, nil
	}
	ids, capped, err := s.repo.PendingPickupOrderIDs(ctx, branchID, pickupCardCap)
	if err != nil {
		return 0, 0, false, err
	}
	if len(ids) == 0 {
		return 0, 0, capped, nil
	}
	if s.picks == nil {
		return len(ids), 0, capped, nil
	}
	states, err := s.picks.OrderPickStates(ctx, ids)
	if err != nil {
		// A GRADING FAILURE DOES NOT BLANK THE CARD. The population is known and it is the number a
		// yard lead actually needs; what is lost is only the split. Reporting the whole population as
		// awaiting pick is the pessimistic reading and the one that sends somebody to look.
		s.log.Warn("dispatch: pickup stage split unavailable", "branch", branchID, "err", err)
		return len(ids), 0, capped, nil
	}
	for _, id := range ids {
		if st, ok := states[id]; ok && st.Staged {
			readyToCollect++
			continue
		}
		awaitingPick++
	}
	return awaitingPick, readyToCollect, capped, nil
}

// pickupCardCap bounds the population the card grades in one pass. A branch's OPEN pickups is a small
// set by construction, an order leaves it the moment it is collected, so this is a backstop against
// a pathological board, not a page size. Hitting it is REPORTED (capped), never silently applied.
const pickupCardCap = 500

// - item 1c (session A) · loading several trucks in one flow -

// LoadOutcome is what happened to ONE stop in a bulk load. It is not an error type: a bulk gesture
// over nine loads where one refuses has one refusal and eight successes, and both halves are news.
type LoadOutcome struct {
	StopID  string `json:"stop_id"`
	OrderNo string `json:"order_no,omitempty"`
	Loaded  bool   `json:"loaded"`
	// Refusal is the sentence for a stop that did not move, in the words the yard uses. Empty when
	// the stop loaded.
	Refusal string `json:"refusal,omitempty"`
}

// MarkLoaded is a principal's sentence made possible: "often we'll load many trucks at the same time.
// So we need to figure out a bulk workflow for this as well."
//
// WHY THIS IS A ROUTE AND NOT N CALLS FROM THE CLIENT. The client could already loop
// POST /stops/{id}/status, and that is exactly what makes it the wrong shape: nine round trips whose
// partial failure story is "whatever the browser did before the tab was closed", and a yard lead with
// no way to see which of the nine did not take. One call, one list of outcomes, and the screen can
// say which load refused and why.
//
// WHY IT IS NOT A TRANSACTION. Loading eight pallets onto trucks is eight things that HAPPENED in a
// yard; rolling back seven real loads because the eighth stop had been cancelled under us would make
// the screen disagree with the world, which is the one thing a dispatch board must never do. Each
// stop goes through the SAME guarded per-stop write DispatchRun uses, so the forward-only matrix,
// the branch fence and the lost-race check are unchanged, this adds a gesture, not a second set of
// rules.
//
// EVERY REFUSAL IS NAMED. A stop that is not on a truck cannot be loaded onto one (ErrStopUnassigned
// is the server's own rule); a stop already loaded is not an error at all and says so; a stop that
// moved under us reports the race rather than pretending. "Some loads did not move" is not a
// sentence anybody can act on.
func (s *Service) MarkLoaded(ctx context.Context, stopIDs []string) ([]LoadOutcome, error) {
	if len(stopIDs) == 0 {
		return nil, httpx.BadRequestf("dispatch: choose the loads to mark loaded")
	}
	if len(stopIDs) > maxBulkLoads {
		return nil, httpx.BadRequestf(
			"dispatch: %d loads is more than one gesture should move, mark them a truck at a time",
			len(stopIDs))
	}
	at := s.now().UTC()
	seen := make(map[string]bool, len(stopIDs))
	out := make([]LoadOutcome, 0, len(stopIDs))
	for _, id := range stopIDs {
		if seen[id] {
			continue // the same load twice in one selection is one load
		}
		seen[id] = true

		// guardStop is the branch fence and the existence check, per stop. A caller who cannot reach
		// one branch does not get to move a load there by burying it in a list.
		stop, err := s.guardStop(ctx, id)
		if err != nil {
			out = append(out, LoadOutcome{StopID: id, Refusal: loadRefusalWord(err)})
			continue
		}
		switch {
		case stop.Status == StopLoaded:
			// ALREADY DONE IS NOT A FAILURE. Two people pressing the same button in a yard is a
			// Tuesday, and reporting it as a refusal would send somebody to check a truck that is
			// already loaded.
			out = append(out, LoadOutcome{StopID: id, OrderNo: stop.OrderNo, Loaded: true})
			continue
		case stop.RunID == "":
			out = append(out, LoadOutcome{StopID: id, OrderNo: stop.OrderNo,
				Refusal: "this load is not on a truck yet, put it on one first"})
			continue
		case !stopCanTransition(stop.Status, StopLoaded):
			out = append(out, LoadOutcome{StopID: id, OrderNo: stop.OrderNo,
				Refusal: "this load is " + stopStateWord(stop.Status) + ", so it cannot be loaded now"})
			continue
		}
		ok, err := s.repo.UpdateStopStatus(ctx, stop.ID, stop.Status, StopLoaded, "", at)
		if err != nil {
			return nil, err
		}
		if !ok {
			out = append(out, LoadOutcome{StopID: id, OrderNo: stop.OrderNo,
				Refusal: "somebody moved this load while you were selecting, refresh and look again"})
			continue
		}
		s.record(ctx, audit.SensitiveAccess{
			Action: "DELIVERY_STOP_" + StopLoaded, EntityType: "delivery_stop", EntityID: stop.ID,
		})
		out = append(out, LoadOutcome{StopID: id, OrderNo: stop.OrderNo, Loaded: true})
	}
	return out, nil
}

// maxBulkLoads bounds one gesture. Fifty is far more than a yard loads at once and far short of a
// request that would hold a connection open doing per-stop guarded writes.
const maxBulkLoads = 50

// loadRefusalWord turns a guard's error into something a yard lead can act on, without leaking which
// of our packages was unhappy.
func loadRefusalWord(err error) string {
	switch {
	case errors.Is(err, ErrNotFound):
		return "this load is no longer on the board"
	case errors.Is(err, ErrForbidden):
		return "this load belongs to another branch"
	default:
		return "this load could not be read"
	}
}

// stopStateWord is the yard's word for a stop's status, so a refusal never prints a column value.
// Same rule and same degrade-honestly default arm as willcall.go's pickStateWord.
func stopStateWord(status string) string {
	switch status {
	case StopReady:
		return "ready to load"
	case StopLoaded:
		return "already on the truck"
	case StopEnRoute:
		return "already on the road"
	case StopDelivered:
		return "already delivered"
	case StopFailed:
		return "recorded as not delivered"
	case StopCancelled:
		return "cancelled"
	default:
		return strings.ToLower(strings.ReplaceAll(status, "_", " "))
	}
}
