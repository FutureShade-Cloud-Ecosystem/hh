package dashboard

// needs.go, THE NEEDS-YOU QUEUE: the widget that earns Home.
//
// "Home leads with what needs the person, not what the yard did." A role-specific queue of things
// waiting on THIS person, each row: what · how many · how old · how bad · one tap to the filtered
// surface. Computed SERVER-SIDE, because a client that assembled it would have to make five reads,
// know five modules' authority rules, and decide severity, and would get a different answer on a
// slow connection.
//
// -- SOURCES ARE CONFIGURATION, MEASUREMENT IS AN ADAPTER ---------------------------------------
//
// WHICH sources a role shows, in what ORDER, at what SEVERITY EDGES, is typed policy rows
// (dashboard_needs_sources, 0201), not a switch on profile in Go. A dealer can drop a source from a
// role, reorder it, or move the edge at which "three parked tickets" becomes a warning, without a
// release. That is 0183's argument about the AR escalation edge, applied to the same kind of fact.
//
// HOW to count a source cannot be a row, and this is the same split 0140 and 0182 already make. Each
// source is a NAMED READ THAT ALREADY EXISTS in another module, the load board's quick cards, the
// AR escalation card, the approvals queue, the parked-ticket count. This package imports none of
// them (that is the one rule the package exists to enforce), so NeedsSource is CONSUMER-DECLARED
// here and satisfied by an adapter in the composition root, exactly like MetricProvider and
// SeriesProvider. WIRE, DO NOT REINVENT: an adapter that recounted parked tickets with its own SQL
// would be a second number that can disagree with the screen it links to, which is the one thing a
// queue like this cannot afford.
//
// -- EVERY FAILURE IS AN ABSENT ROW, NEVER AN ERROR ---------------------------------------------
//
// A source with no adapter registered, an adapter that fails, an adapter that panics, a source key
// this build does not have, a policy table that will not read: in every case the ROW is missing and
// the rest of the queue answers. Never error a read path. The alternative, one module having a bad
// afternoon blanking the most important widget on the screen, is precisely the failure the whole
// cockpit was built to avoid, and it would be worse here than on a tile, because the person would
// not know they had stopped being told about their approvals.
//
// A source whose count is ZERO is also absent, and that is a design rule rather than a degradation:
// the empty state for this widget is one sentence ("Nothing is waiting on you right now."), never a
// zero in a big font. A row that says "0 approvals waiting" is noise wearing the clothes of work.

import (
	"context"
	"sort"
	"strconv"
	"time"
)

// -- the source catalog (code) -------------------------------------------------------------------

// Needs source keys. STABLE STRINGS: they are half the primary key of every policy row a dealer has
// saved (0201), so renaming one silently drops that source from every role that had it. Add, never
// rename, the 0140 rule, again.
const (
	NeedsApprovals     = "approvals.waiting"
	NeedsAREscalation  = "ar.escalation"
	NeedsUnassigned    = "dispatch.unassigned"
	NeedsDueToCommit   = "dispatch.due_to_commit"
	NeedsParkedTickets = "pos.parked"
	NeedsStalePicks    = "tasks.stale_picks"
	// The yard manager's two own sources (0381): shortages the yard team raised and left open, and
	// purchase orders that landed short and are still partial.
	NeedsOpenShortages        = "tasks.open_shortages"
	NeedsReceivingDifferences = "purchasing.receiving_differences"
	// The logistics manager's own source (0395): approvals they filed that are still pending.
	NeedsFiledPending = "approvals.filed_pending"
	// The change orders waiting on THIS manager (0411): signed or applied from Manager Home's Approvals.
	NeedsChangesWaiting = "orders.changes_waiting"
)

// NeedsSourceMeta is one catalog entry: what the row SAYS and where it GOES. It carries nothing
// about how to count, which is the adapter's job, and nothing about severity, which is the dealer's.
//
// Unit/Screen are the shell's own keys, so a row taps through on the frozen #unit/module contract
// like every other deep link in the cockpit rather than inventing a route. View is the filter the
// destination should apply, the words the surface will show as its scope, so the person lands on
// the four invoices the row counted, not on the register.
type NeedsSourceMeta struct {
	Key    string `json:"source_key"`
	What   string `json:"what"`
	Module string `json:"module"`
	Unit   string `json:"unit,omitempty"`
	Screen string `json:"screen,omitempty"`
	View   string `json:"view,omitempty"`
	Action string `json:"action,omitempty"` // the one word on the row's tap target
	// ManagerOnly marks a source whose destination is a MANAGER-GATED surface. The row, its words,
	// its count and its link, is byte-absent for anybody below that tier, EVEN IF a dealer has
	// curated the source onto their job shape.
	//
	// This is the catalog's half of "curation hides things; it never grants anything" (0182), and it
	// exists because the policy rows are dealer-editable: without it, an admin adding
	// `ar.escalation` to the counter shape would put the book's 90-day tail on a counter clerk's
	// Home, which the AR landing's own requireManager guard would never have allowed.
	//
	// The tier fact is Scope.CostAllowed, the handler's caller.IsManager(), the ONE tier answer this
	// package carries. Introducing a second boolean with identical provenance would be two places
	// for one answer to be wrong.
	//
	// It is NOT a substitute for the adapter's own obligation. A source still has to count what is
	// waiting on THIS person; this only refuses a whole row to a whole tier.
	ManagerOnly bool `json:"-"`
}

// NeedsSourceCatalog is the vetted set. Every entry names a read another module already serves.
var NeedsSourceCatalog = []NeedsSourceMeta{
	{
		Key: NeedsApprovals, What: "Approvals waiting", Module: "identity",
		Unit: "admin", Screen: "approvals", View: "Waiting on you", Action: "Review",
	},
	{
		Key: NeedsAREscalation, What: "Accounts past the escalation edge", Module: "billing",
		Unit: "finance", Screen: "ar", View: "Escalation queue", Action: "Escalate",
		// The AR landing is manager-gated at its own door (requireManager). A queue row that
		// counted the book for somebody who may not open it would be a smaller version of the same
		// leak, so the row is refused to the same tier the surface is.
		ManagerOnly: true,
	},
	{
		Key: NeedsUnassigned, What: "Loads with no truck", Module: "dispatch",
		Unit: "dispatch", Screen: "dispatch", View: "Staging · unassigned", Action: "Assign",
	},
	{
		Key: NeedsDueToCommit, What: "Loads due to commit", Module: "dispatch",
		Unit: "dispatch", Screen: "dispatch", View: "Load board · due to commit", Action: "Commit",
	},
	{
		Key: NeedsParkedTickets, What: "Parked tickets", Module: "pos",
		Unit: "sales", Screen: "pos", View: "Parked tickets", Action: "Resume",
	},
	{
		Key: NeedsStalePicks, What: "Picks past their deadline", Module: "tasks",
		Unit: "yard", Screen: "yard", View: "Picks · past deadline", Action: "Chase",
	},
	{
		Key: NeedsOpenShortages, What: "Open shortages", Module: "tasks",
		Unit: "yard", Screen: "yard", View: "Shortages · open", Action: "Resolve",
	},
	{
		Key: NeedsReceivingDifferences, What: "Receipts that landed short", Module: "purchasing",
		Unit: "yard", Screen: "yard", View: "Receiving · short", Action: "Settle",
	},
	{
		Key: NeedsFiledPending, What: "Approvals you filed", Module: "identity",
		Unit: "admin", Screen: "approvals", View: "Filed by you", Action: "Track",
	},
}

// NeedsSourceByKey indexes the catalog. Built once at init; never mutated.
var NeedsSourceByKey = func() map[string]NeedsSourceMeta {
	m := make(map[string]NeedsSourceMeta, len(NeedsSourceCatalog))
	for _, s := range NeedsSourceCatalog {
		m[s.Key] = s
	}
	return m
}()

// -- the policy (rows) ---------------------------------------------------------------------------

// NeedsPolicy is one dealer policy row (0201 dashboard_needs_sources).
type NeedsPolicy struct {
	SourceKey string
	Position  int
	Visible   bool
	WarnAt    int
	StopAt    int
	StaleMins int
}

// DefaultNeedsSources is what a build with no readable policy serves, per job shape, the same rows
// 0201 seeds, in Go.
//
// WHY A CODE COPY EXISTS AT ALL. The hotkey map made this call first (DefaultHotkeys / 0164) and the
// reasoning carries: an unreadable config table must not TAKE AWAY the queue somebody used
// yesterday. A build ahead of 0201, a store blip, a permission problem, none of those are a
// decision to stop telling a controller about the 90-day tail. An EMPTY answer is the opposite case:
// a dealer decided this shape has no sources, and empty is respected as the answer.
//
// The cost is a copy that can drift, and it is paid the way this codebase always pays it: a drift
// test reads 0201 and asserts the two agree (widget_test.go).
var DefaultNeedsSources = map[string][]NeedsPolicy{
	ProfileAdmin: {
		{SourceKey: NeedsApprovals, Position: 0, Visible: true, WarnAt: 1, StopAt: 5, StaleMins: 240},
		{SourceKey: NeedsAREscalation, Position: 1, Visible: true, WarnAt: 1, StopAt: 3},
		{SourceKey: NeedsUnassigned, Position: 2, Visible: true, WarnAt: 1, StopAt: 5},
		{SourceKey: NeedsParkedTickets, Position: 3, Visible: true, WarnAt: 3, StopAt: 10, StaleMins: 120},
	},
	ProfileManager: {
		{SourceKey: NeedsApprovals, Position: 0, Visible: true, WarnAt: 1, StopAt: 5, StaleMins: 240},
		{SourceKey: NeedsAREscalation, Position: 1, Visible: true, WarnAt: 1, StopAt: 3},
		{SourceKey: NeedsParkedTickets, Position: 2, Visible: true, WarnAt: 3, StopAt: 10, StaleMins: 120},
	},
	ProfileYard: {
		{SourceKey: NeedsUnassigned, Position: 0, Visible: true, WarnAt: 1, StopAt: 5},
		{SourceKey: NeedsDueToCommit, Position: 1, Visible: true, WarnAt: 1, StopAt: 3},
		{SourceKey: NeedsStalePicks, Position: 2, Visible: true, WarnAt: 1, StopAt: 5, StaleMins: 60},
	},
	ProfileCounter: {
		{SourceKey: NeedsParkedTickets, Position: 0, Visible: true, WarnAt: 3, StopAt: 10, StaleMins: 120},
		{SourceKey: NeedsStalePicks, Position: 1, Visible: true, WarnAt: 1, StopAt: 5, StaleMins: 60},
		{SourceKey: NeedsApprovals, Position: 2, Visible: true, WarnAt: 1, StopAt: 5, StaleMins: 240},
	},
}

// yardManagerNeeds is the yard manager's own policy, the Go copy of 0381 §5 as amended by 0395.
// Committing a delivery date on a load being built comes first; stale picks keep the yard shape's
// edges; the other sources carry count edges only.
var yardManagerNeeds = []NeedsPolicy{
	{SourceKey: NeedsDueToCommit, Position: 0, Visible: true, WarnAt: 1, StopAt: 3},
	{SourceKey: NeedsOpenShortages, Position: 1, Visible: true, WarnAt: 1, StopAt: 5},
	{SourceKey: NeedsStalePicks, Position: 2, Visible: true, WarnAt: 1, StopAt: 5, StaleMins: 60},
	{SourceKey: NeedsReceivingDifferences, Position: 3, Visible: true, WarnAt: 1, StopAt: 3},
}

// logisticsManagerNeeds is the logistics manager's own policy, the Go copy of 0395 §2. The dock
// shape's dispatch sources stay; the new source counts approvals they filed that are still pending.
var logisticsManagerNeeds = []NeedsPolicy{
	{SourceKey: NeedsUnassigned, Position: 0, Visible: true, WarnAt: 1, StopAt: 5},
	{SourceKey: NeedsDueToCommit, Position: 1, Visible: true, WarnAt: 1, StopAt: 3},
	{SourceKey: NeedsStalePicks, Position: 2, Visible: true, WarnAt: 1, StopAt: 5, StaleMins: 60},
	{SourceKey: NeedsFiledPending, Position: 3, Visible: true, WarnAt: 1, StopAt: 5, StaleMins: 240},
}

// procurementManagerNeeds is the procurement manager's own policy, the Go copy of 0392 §1: the
// gaps the yard reports and where each purchase order got to come first, and approvals stay (the
// AR escalation and the parked tickets are the controller's and the counter's work, not the buyer's).
var procurementManagerNeeds = []NeedsPolicy{
	{SourceKey: NeedsOpenShortages, Position: 0, Visible: true, WarnAt: 1, StopAt: 5},
	{SourceKey: NeedsReceivingDifferences, Position: 1, Visible: true, WarnAt: 1, StopAt: 3},
	{SourceKey: NeedsApprovals, Position: 2, Visible: true, WarnAt: 1, StopAt: 5, StaleMins: 240},
}

// branchManagerNeeds is the branch manager's own policy, the Go copy of 0392 §2: the branch manager
// commits a delivery date and stands behind it when dispatch escalates, so the two dispatch sources
// lead (with the dock shape's edges), and the manager shape's three rows follow.
var branchManagerNeeds = []NeedsPolicy{
	{SourceKey: NeedsDueToCommit, Position: 0, Visible: true, WarnAt: 1, StopAt: 3},
	{SourceKey: NeedsUnassigned, Position: 1, Visible: true, WarnAt: 1, StopAt: 5},
	{SourceKey: NeedsApprovals, Position: 2, Visible: true, WarnAt: 1, StopAt: 5, StaleMins: 240},
	{SourceKey: NeedsAREscalation, Position: 3, Visible: true, WarnAt: 1, StopAt: 3},
	{SourceKey: NeedsParkedTickets, Position: 4, Visible: true, WarnAt: 3, StopAt: 10, StaleMins: 120},
}

// defaultNeedsFor is the fallback policy for one profile: a job shape serves its own row set from
// DefaultNeedsSources, the yard, procurement and branch managers their own, and every other manager
// refinement the rows of the shape it was born from (0381 copies them, so the fallback and the
// table agree).
func defaultNeedsFor(profile string) []NeedsPolicy {
	switch profile {
	case ProfileManagerYard:
		return yardManagerNeeds
	case ProfileManagerLogistics:
		return logisticsManagerNeeds
	case ProfileManagerProcurement:
		return procurementManagerNeeds
	case ProfileManagerBranch:
		return branchManagerNeeds
	}
	if base, ok := ManagerBaseShape[profile]; ok {
		return DefaultNeedsSources[base]
	}
	return DefaultNeedsSources[profile]
}

// -- the measurement seam ------------------------------------------------------------------------

// NeedsMeasure is what one source answers: how many, and how old the oldest is.
//
// Oldest is a TIME rather than a duration because the source knows when the thing happened and the
// dashboard knows what "now" is (Scope.Now, injectable). A duration computed in an adapter would be
// computed against the machine clock, which is the one clock a test cannot pin.
type NeedsMeasure struct {
	Count  int
	Oldest time.Time
}

// NeedsSource measures ONE row of the queue.
//
// CONSUMER-DECLARED, like MetricProvider and SeriesProvider, and carrying the same non-negotiable
// obligation: Scope.BranchIDs / Scope.AllBranches are the caller's GRANTS and the read applies them.
// A queue row is a count of somebody else's data and the dashboard cannot enforce scoping on a
// number it did not compute.
//
// AND THE SECOND OBLIGATION, WHICH IS THIS SEAM'S OWN: the row must be what is waiting on THIS
// PERSON, not what exists. "Approvals waiting" means approvals this caller may decide (identity's
// own authority read), not every pending request in the building. An adapter that widened it would
// put five items in a counter lead's queue that they cannot act on, which is the fastest way to
// teach somebody to stop reading their Home.
type NeedsSource interface {
	Needs(ctx context.Context, scope Scope) (NeedsMeasure, error)
}

// NeedsFunc adapts a plain function to NeedsSource.
type NeedsFunc func(ctx context.Context, scope Scope) (NeedsMeasure, error)

func (f NeedsFunc) Needs(ctx context.Context, s Scope) (NeedsMeasure, error) { return f(ctx, s) }

// -- the wire ------------------------------------------------------------------------------------

// Severity values. Status colour is reserved for STATE and always ships with a word, never colour
// alone, which is why this is a word.
const (
	SeverityGood  = "good"
	SeverityWatch = "watch"
	SeverityStop  = "stop"
)

// NeedsRow is one line of the queue.
type NeedsRow struct {
	SourceKey string `json:"source_key"`
	What      string `json:"what"`
	Count     int    `json:"count"`
	// Age is the humanised age of the OLDEST item ("45m", "2h", "3 days"), and AgeMinutes is the
	// same fact as a number. Both, because the server is the arithmetic authority, a client that
	// formatted this from a timestamp would be doing date maths against its own clock, which is how
	// a tab left open overnight starts lying.
	Age        string `json:"age,omitempty"`
	AgeMinutes int    `json:"age_minutes,omitempty"`
	Severity   string `json:"severity"`
	Module     string `json:"module"`
	Unit       string `json:"unit,omitempty"`
	Screen     string `json:"screen,omitempty"`
	View       string `json:"view,omitempty"`
	Action     string `json:"action,omitempty"`
}

// NeedsQueue is the GET /api/v1/dashboard/needs payload.
//
// Rows is ALWAYS an array, never null: the widget renders its empty state from a list it can
// iterate, and `null` is the shape that makes a client invent an error surface for a legitimate
// empty. This package has learned that one more than once.
type NeedsQueue struct {
	Profile string     `json:"profile"`
	Rows    []NeedsRow `json:"rows"`
	// Total is the queue's headline, what the S-size form of the widget shows. Summed here because
	// the client must never add up a list it may be showing only the first few rows of.
	//
	// THE SERVER DOES NOT TRUNCATE. An early draft capped the queue at eight rows, which would have
	// made this total undercount the moment the cap bit, a headline that disagrees with its own
	// list. How many rows to show is a RENDER decision (the doctrine's 3-6, with "2 more"); how many
	// there ARE is this number, and it is always all of them. The length is bounded anyway: a policy
	// row naming a key outside the source catalog is skipped, so the catalog is the ceiling.
	Total int `json:"total"`
	// Worst is the highest severity present, so the widget can carry one state without re-deriving
	// it from the rows.
	Worst string `json:"worst"`
}

// SeverityFor scores one row.
//
// COUNT DECIDES THE SEVERITY; AGE ESCALATES IT BY EXACTLY ONE STEP. Age is a different complaint
// from volume, and a source that shouted `stop` because one item was old would drown the source that
// is genuinely deep. So a stale `good` becomes `watch` and a stale `watch` becomes `stop`, and a
// stale `stop` is already as loud as this vocabulary goes.
//
// Zero thresholds are OFF, not "always": StopAt 0 means this source never reaches stop, StaleMins 0
// means age never escalates it. WarnAt 0 would mean "warn at zero items", which is meaningless for a
// row that is only rendered when there is at least one, so 0 there is read as 1.
func SeverityFor(count, ageMinutes int, p NeedsPolicy) string {
	sev := SeverityGood
	warn := p.WarnAt
	if warn <= 0 {
		warn = 1
	}
	if count >= warn {
		sev = SeverityWatch
	}
	if p.StopAt > 0 && count >= p.StopAt {
		sev = SeverityStop
	}
	if p.StaleMins > 0 && ageMinutes >= p.StaleMins {
		switch sev {
		case SeverityGood:
			sev = SeverityWatch
		case SeverityWatch:
			sev = SeverityStop
		}
	}
	return sev
}

// HumanAge is the age words on a row. Short, because a queue row has one line.
func HumanAge(minutes int) string {
	switch {
	case minutes <= 0:
		return ""
	case minutes < 60:
		return strconv.Itoa(minutes) + "m"
	case minutes < 60*36:
		return strconv.Itoa(minutes/60) + "h"
	default:
		d := minutes / (60 * 24)
		if d == 1 {
			return "1 day"
		}
		return strconv.Itoa(d) + " days"
	}
}

// worstOf reduces the rows to one state.
func worstOf(rows []NeedsRow) string {
	worst := SeverityGood
	for _, r := range rows {
		if r.Severity == SeverityStop {
			return SeverityStop
		}
		if r.Severity == SeverityWatch {
			worst = SeverityWatch
		}
	}
	return worst
}

// sortNeedsPolicy orders policy rows the way the dealer curated them, deterministically for a tie.
func sortNeedsPolicy(rows []NeedsPolicy) {
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Position != rows[j].Position {
			return rows[i].Position < rows[j].Position
		}
		return rows[i].SourceKey < rows[j].SourceKey
	})
}
