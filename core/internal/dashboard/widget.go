package dashboard

// widget.go, THE WIDGET SUBSTRATE: what a widget IS, what may be stored, and the gate that decides
// which widgets a caller is allowed to be shown.
//
// A WIDGET IS A ROW OF DATA. {shape, measure, split, branch, range, size, title, position}, no code
// per widget, ever. The doctrine's own words, and the reason this file has no per-widget anything in
// it: a Home is assembled from rows and a component per SHAPE, so adding a widget is an INSERT and
// adding a shape is a release. Six shapes, five sizes, one free span, and everything else comes from
// the catalog.
//
// WHERE THE ROWS LIVE, AND WHY NOT IN 0172. Typed tables (0201), keyed the way 0140 keys its own -
// (user_id, widget_key) and (profile, widget_key). The long argument is in 0201's header; the short
// one is that six of a widget's eight fields cannot fit 0172's (surface, user, kind, item, position,
// visible) row, and encoding them into its opaque 64-character item_id would be a second schema
// hiding inside a string, the drift 0192 refused in writing this session.
//
// THE GATE, WHICH IS THE POINT OF THE FILE. A widget can only draw a CATALOG MEASURE, and the
// catalog a caller gets is MetricsFor(curation, costAllowed), the same function, the same two
// layers, the same ruling as the chart composer's (viz.go): curation decides the menu, the cost gate
// decides what may EXIST in the answer, and curation can never grant. On top of that:
//
//   READ  a widget whose measure is not in this caller's catalog is NOT RETURNED. Not returned
//         empty, not returned with a null measure, absent. `null` is not absence; three trains in
//         this codebase found that out, and the mutation test asserts the literal four bytes are
//         gone too.
//   WRITE a submitted widget naming a measure outside this caller's catalog is REFUSED, the whole
//         save, with a 422, and ZERO rows land. Byte-absence gates the DISPLAY; it was never going
//         to gate the ACT, and a forged PUT does not go anywhere near the read path.
//
// The write refusal deliberately does not distinguish "this build has no such measure" from "you may
// not see that measure": one message for both, so the refusal itself cannot be used to enumerate
// what exists. The twin test (widget_test.go) proves the store is never called.

import (
	"sort"
	"strings"
)

// -- the closed sets -----------------------------------------------------------------------------

// Widget body forms. Six, and the CHECK constraint in 0201 encodes the same six.
//
// These are named Widget* rather than Shape* because viz.go already owns Shape{Bar,Split,List}, the
// CHART composer's three, which decide how a series is drawn. Both sets contain the string "list"
// and they do not mean the same thing (a chart `list` is a labelled series; a widget `list` is a
// queue of things with a state), which is exactly why they are two sets and not one.
const (
	WidgetStat  = "stat"  // one number, a delta, optionally a sparkline
	WidgetTrend = "trend" // a line over 12+ points
	WidgetBars  = "bars"  // horizontal bars: label · track · value
	WidgetList  = "list"  // 3-6 rows of fact + state + tap-through
	WidgetBoard = "board" // status chips across a population
	WidgetGauge = "gauge" // a meter with a severity fill
)

// WidgetShapeSet is the closed set, in builder order.
var WidgetShapeSet = []string{WidgetStat, WidgetTrend, WidgetBars, WidgetList, WidgetBoard, WidgetGauge}

// Bento sizes. The span each one takes on the 12-column grid is the whole meaning of the value, so
// it lives beside it rather than in the client: a size that meant a different span on the server and
// in the browser would put two widgets in one cell.
//
// -- THE v3.5 GRID, AND WHY RE-RULING IT COST NO DATA MIGRATION ----------------------------------
//
// Home v3.5 rules twelve columns at >=1280px (six at tablet and phone) on a 132px row unit. The old
// grid was four columns, and the new preset table is the old one SCALED EXACTLY BY THREE: 1->3,
// 2->6, 4->12, with every row count untouched.
//
// Because a stored row holds a preset LETTER and never a span, 0201's choice, made so the arithmetic
// would live here and nowhere else, remapping this one function moved all thirty seeded role
// defaults and every person's saved layout onto the twelve-column grid at IDENTICAL PROPORTIONS. A
// card that was a quarter of the width is still a quarter of the width. Nothing was lost, nothing was
// recomputed, and 0207 had to UPDATE exactly one row of thirty (and only to take up the new preset).
//
// `T` for Tall is the sixth preset and the only genuinely new shape on the grid: 3x2 is a column, and
// there was no way to say "a narrow card, two rows deep" on a four-column bento at all.
const (
	SizeS  = "S"  // 3x1 , a stat, a clock/status
	SizeT  = "T"  // 3x2 , Tall: a stat with a ring gauge or a short list under it
	SizeM  = "M"  // 6x1 , a stat with a sparkline, compact bars, a board
	SizeL  = "L"  // 6x2 , a chart, a longer list, the Needs-you queue
	SizeXL = "XL" // 12x1, Wide: a full-width board or trend
)

// WidgetSizeSet is the closed set, SMALLEST FIRST BY AREA, and the order is load-bearing rather than
// cosmetic: it is what Alt-up/Alt-down step through when somebody resizes a focused widget from the
// keyboard, and it is the order the builder lists. 3 · 6 · 6 · 12 · 12 cells, T and M tie, and T
// comes first because it is the narrower of the two, so one press of Alt-up never jumps a card across
// half the screen.
var WidgetSizeSet = []string{SizeS, SizeT, SizeM, SizeL, SizeXL}

// -- free spans ----------------------------------------------------------------------------------

// WidgetColSet is the SNAP SET the grip lands on: the divisors and half-divisors of twelve that also
// survive the six-column breakpoint.
//
// 5, 7, 10 and 11 are excluded deliberately and it is not fussiness. A bento only works if every
// widget lands on a line the next widget can start from; a five-column card leaves a seven-column
// remainder that no other card in the vocabulary fits, so the row beside it is permanently ragged and
// the person who dragged it cannot tell why. The doctrine's own words: the grip "resizes FREELY,
// snapping to real grid lines".
var WidgetColSet = []int{3, 4, 6, 8, 9, 12}

// WidgetMaxRows is three. A fourth row is 560px of one card, which is a page rather than a widget.
const WidgetMaxRows = 3

// IsWidgetSpan reports whether a free span is one the grid can hold.
//
// ZERO/ZERO IS LEGAL AND IT IS THE COMMON CASE, "the preset decides", which is what every row in
// both tables carries today and what most rows will carry forever, because a person who never touched
// the grip has no opinion about columns: they picked a size. It is the same ruling IsBranchID makes
// about the empty string one screen down, for the same reason.
//
// HALF A PAIR IS NOT A SPAN. One of the two at zero is not a narrower answer, it is an incomplete
// one, and there is nothing honest to fill the other half with, the preset already answers both.
func IsWidgetSpan(cols, rows int) bool {
	if cols == 0 && rows == 0 {
		return true
	}
	if cols == 0 || rows == 0 {
		return false
	}
	return containsInt(WidgetColSet, cols) && rows >= 1 && rows <= WidgetMaxRows
}

func containsInt(set []int, v int) bool {
	for _, x := range set {
		if x == v {
			return true
		}
	}
	return false
}

// SplitNone is the builder's first Split option and the honest default: a widget with no split is a
// stat, which is most of a Home. The other split values are viz.go's dimensions, a split IS a
// dimension, and giving the same idea a second vocabulary would let the two drift.
const SplitNone = "none"

// SplitSet is every split value the substrate knows: none, then the dimensions.
var SplitSet = []string{SplitNone, DimBranch, DimStatus, DimBucket}

// Named windows. A widget names a WINDOW, never a pair of dates: two people reading the same widget
// must be reading the same period, and a stored date pair silently becomes wrong the next morning.
const (
	RangeToday  = "today"
	RangeWTD    = "wtd"
	RangeMTD    = "mtd"
	RangeQTD    = "qtd"
	RangeYTD    = "ytd"
	RangeLast7  = "last_7"
	RangeLast14 = "last_14"
	RangeLast30 = "last_30"
	RangeLast90 = "last_90"
	RangeOpen   = "open" // no window: the measure's own natural population (open orders, the book)
	// RangeAllTime and RangeNow are the two windows `open` could not honestly cover, and they exist
	// because a caption is only fixed when it is TRUE, not when it stops naming a period.
	//
	// `open` reads "Everything open", which is exactly right for a population that is genuinely open
	//, unpaid invoices, unclosed orders, unresolved claims, a pick queue. It is wrong for the other
	// two shapes a measure with no date predicate can have:
	//
	//   all_time  every record ever, open or closed. Gross margin is computed over every order line
	//             on every order that is not DRAFT or CANCELLED, INVOICED and CLOSED ones included,
	//             so "Everything open" was a second false caption sitting under a corrected one.
	//   now       a current-state snapshot with no history behind it. On-hand stock is read from
	//             stock_balances, a live table; there is no window to open at all, only this moment.
	RangeAllTime = "all_time"
	RangeNow     = "now"
	maxTitleRune = 48 // 0201's CHECK, restated so the refusal is a 422 in words and not a 500
)

// RangeSet is the closed set, in builder order.
var RangeSet = []string{
	RangeToday, RangeWTD, RangeMTD, RangeQTD, RangeYTD,
	RangeLast7, RangeLast14, RangeLast30, RangeLast90,
	RangeOpen, RangeAllTime, RangeNow,
}

// MeasureNeedsYou is the ONE reserved measure: the role's Needs-you queue.
//
// IT IS DELIBERATELY NOT IN MetricCatalog. The metric catalog is what the chart composer offers, and
// "Needs you" is not a series anybody can chart, it is a queue assembled from per-role source rows
// (needs.go). Putting it in the catalog would have offered it as a bar chart, and filtering it back
// out of MetricsFor would have changed the meaning of a function the chart composer depends on. So
// the widget layer knows one reserved id and the composer never sees it.
const MeasureNeedsYou = "needs.you"

// needsMeasureMeta is the catalog entry the reserved measure would have, so the read path has one
// shape to work with and no special case downstream of resolution.
var needsMeasureMeta = MetricMeta{
	ID: MeasureNeedsYou, Label: "Needs you", Module: "dashboard", Kind: KindCount,
	Class:        ClassOperational,
	Splits:       []string{SplitNone},
	WidgetShapes: []string{WidgetList, WidgetStat},
	// A QUEUE HAS NO WINDOW. Needs-you is what is waiting on this person NOW; asking for it "last 30
	// days" is not a narrower question, it is a different one nobody asked. This line is also load
	// bearing: needs.you sits at position 1 of all four seeded layouts and is the one widget the
	// doctrine says a person may never lose, so an absent Ranges here would fail every one of them
	// on the first save.
	Ranges: []string{RangeOpen},
	Lead:   "Everything waiting on this person right now.",
}

// -- the row -------------------------------------------------------------------------------------

// Widget is one stored (or submitted) widget, the shape of both dashboard_profile_widgets and
// dashboard_user_widgets.
//
// BranchID is a plain string with omitempty and NOT a *string. An absent branch means "the branches
// this caller may see", and the way to say absent on this wire is for the key not to be there. A
// pointer would marshal `null`, which is a fourth thing a client has to have an opinion about and
// which this codebase has three times now proved people read as a value.
//
// Cols/Rows are the FREE SPAN (0207) and carry omitempty for the identical reason. Zero is not a
// span, it is "the preset decides", and the way to say that on this wire is for the key not to be
// there, so a client that never touched the grip sends the same eleven keys it always sent and a
// client that reads the payload gets no third state to have an opinion about.
type Widget struct {
	Key      string `json:"widget_key"`
	Shape    string `json:"shape"`
	Measure  string `json:"measure"`
	Split    string `json:"split"`
	BranchID string `json:"branch_id,omitempty"`
	Range    string `json:"range"`
	Size     string `json:"size"`
	Cols     int    `json:"cols,omitempty"`
	Rows     int    `json:"rows,omitempty"`
	Title    string `json:"title"`
	Position int    `json:"position"`
	Visible  bool   `json:"visible"`
}

// WidgetView is one RESOLVED widget on the wire: the row plus what the client needs to draw and
// deep-link it, resolved from the measure's catalog entry. A widget whose measure is not in the
// caller's catalog never becomes one of these.
type WidgetView struct {
	Widget
	Label  string `json:"label"`
	Kind   Kind   `json:"kind"`
	Module string `json:"module"`
	Unit   string `json:"unit,omitempty"`
	Screen string `json:"screen,omitempty"`
	// Span is the grid footprint (columns, rows) for Size. Sent because the arithmetic that decides
	// whether a bento fits is the server's, like every other arithmetic on this wire.
	SpanCols int `json:"span_cols"`
	SpanRows int `json:"span_rows"`
}

// WidgetLayout is the GET /api/v1/dashboard/widgets payload.
type WidgetLayout struct {
	Profile string `json:"profile"`
	// Source is "user" when a personal layout is in force, "role" when the profile default is, and
	// "catalog" when neither exists, the same three values Layout.Source carries, so one
	// "Reset to role default" affordance keys off one vocabulary across the whole cockpit. There is
	// no code-side default widget layout, so "catalog" here means "nothing is configured".
	Source  string       `json:"source"`
	Widgets []WidgetView `json:"widgets"`
}

// SpanOf returns the grid footprint of a PRESET: columns, rows, on the twelve-column v3.5 grid.
//
// A size outside the set has no span (0, 0) rather than a default, and that is deliberate: every
// caller of this function has already been through NormalizeWidgets, so a zero here means the closed
// set and this switch have drifted apart, and a silent fallback to M would hide it behind a card that
// merely looks wrong.
func SpanOf(size string) (cols, rows int) {
	switch size {
	case SizeS:
		return 3, 1
	case SizeT:
		return 3, 2
	case SizeM:
		return 6, 1
	case SizeL:
		return 6, 2
	case SizeXL:
		return 12, 1
	}
	return 0, 0
}

// SpanFor is the footprint a widget ACTUALLY occupies: its free span when it has one, its preset's
// otherwise. One function, so the read path and any future caller cannot disagree about which of the
// two wins.
//
// THE OVERRIDE WINS, AND size IS STILL STORED BESIDE IT. That is not redundancy: `size` is the
// vocabulary the menu, the keyboard and every role default speak, and a row that threw it away the
// moment somebody dragged a corner would be frozen at whatever the grid meant that afternoon, the
// day the doctrine re-rules L, a card stored as 6x2 would not follow it and a card stored as 'L'
// would. 0207's header makes the same argument at length about the column being nullable.
func SpanFor(w Widget) (cols, rows int) {
	if w.Cols > 0 && w.Rows > 0 {
		return w.Cols, w.Rows
	}
	return SpanOf(w.Size)
}

// IsWidgetShape / IsWidgetSize / IsSplit / IsRange report membership of the closed sets. Small, but
// they are the only place each set is tested, so a seventh value can never be admitted by a
// forgotten switch statement somewhere else.
func IsWidgetShape(v string) bool { return containsString(WidgetShapeSet, v) }
func IsWidgetSize(v string) bool  { return containsString(WidgetSizeSet, v) }
func IsSplit(v string) bool       { return containsString(SplitSet, v) }
func IsRange(v string) bool       { return containsString(RangeSet, v) }

// IsBranchID reports whether a widget's branch narrowing is well formed.
//
// THE EMPTY STRING IS LEGAL AND IT IS THE COMMON CASE. 0201's column comment is the contract: "A
// NARROWING filter, never a grant: NULL means 'the branches this caller may see'". Every widget the
// product ships carries exactly that, so a check that treated "" as malformed would drop all thirty
// seeded role defaults on the first save.
//
// Shape only, whether the caller may SEE this branch is a per-caller fact and lives in
// ValidateWidgets, beside the other three. The column is `uuid REFERENCES locations(id)`, so without
// this a typo'd id reaches Postgres and comes back as a 500 three layers down instead of a refusal
// in words.
func IsBranchID(v string) bool {
	if v == "" {
		return true
	}
	if len(v) != 36 {
		return false
	}
	for i, r := range v {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if r != '-' {
				return false
			}
			continue
		}
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}

// BranchGate answers one question: may this caller narrow a widget to this branch?
//
// It is a small type rather than two loose arguments because the ANSWER FOR THE EMPTY STRING is the
// part everybody gets wrong. "" is not "no branch", it is "the branches this caller may see", which
// every caller may always ask for. A gate that refused it would refuse every widget shipped.
type BranchGate struct {
	// IDs are the caller's OWN grants, never a request parameter.
	IDs []string
	// All is a Tier-1 global caller, and, separately, the posture a ROLE DEFAULT is written under.
	All bool
}

// BranchGateFor is the gate for a caller, read off the scope the auth guard resolved.
func BranchGateFor(scope Scope) BranchGate {
	return BranchGate{IDs: scope.BranchIDs, All: scope.AllBranches}
}

// AnyBranch allows every branch. It is the gate for a caller who genuinely holds them all, and the
// posture the package's own tests are written under.
var AnyBranch = BranchGate{All: true}

// NoBranch refuses every named branch and allows only "", and it is the gate a ROLE DEFAULT is
// written under.
//
// -- WHY "NAMING IS NOT SEEING" DOES NOT CARRY HERE ----------------------------------------------
//
// SaveProfileWidgets gates MEASURES against the full catalog on the argument that curation hides
// things and never grants anything. That argument holds for a measure because of what happens to a
// reader who may not see it: WidgetViewsFor drops the widget, cleanly, and their Home is one card
// shorter. Nothing lies to them.
//
// A BRANCH HAS NO SUCH ANSWER. A role default is one row read by many people with different grants,
// and every option for the reader who lacks it is bad: measure their own branches instead and the
// card silently answers a different question than the one the admin wrote; measure nothing and they
// get a permanently blank card; drop the row and the whole-document re-PUT deletes it from their
// layout the first time they drag anything. A per-viewer answer cannot be stored in a per-role row.
//
// So a role default may not narrow to a branch at all. This costs nothing today, 0 of 0201's 30
// seeded rows carry one, and no client can reach that route, which makes now the cheapest moment
// this rule will ever be taken.
var NoBranch = BranchGate{}

// Allows reports whether this caller may narrow to `id`. The empty string, "every branch I can
// see", is always allowed, for every caller, including one with no grants at all.
//
// IT ANSWERS "MAY YOU", NOT "IS IT REAL", AND `g.All` IS WHERE THAT DISTINCTION USED TO COST A 500.
// A global caller is granted every branch, so every well-formed uuid passes here, including one
// that names no row in `locations`. That id then reached the INSERT and violated
// `branch_id uuid REFERENCES locations (id)` (0201), and the raw driver error became an "internal
// error" that told a real branch apart from an invented one. Existence is a question about a TABLE
// this package may not read, so it is asked one layer up, once per save, through the seam
// widget_service.go declares as BranchResolver, and its refusal is deliberately THIS one, so the
// two cases stay indistinguishable.
func (g BranchGate) Allows(id string) bool {
	if id == "" {
		return true
	}
	if g.All {
		return true
	}
	return containsString(g.IDs, id)
}

// IsWidgetKey enforces 0201's alphabet in Go, so a bad key is a 422 in words rather than a CHECK
// violation surfacing as a 500 three layers down.
func IsWidgetKey(v string) bool {
	if v == "" || len(v) > 64 {
		return false
	}
	for i, r := range v {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if i > 0 {
			ok = ok || r == '.' || r == '-' || r == '_'
		}
		if !ok {
			return false
		}
	}
	return true
}

// AutoTitle is the title a widget shows when nobody typed one: the measure's label, and the split
// when there is one. Two to four words, which is the budget.
//
// It is computed on the SERVER so the stored empty string keeps meaning "derive it", a client that
// materialised its own guess on save would turn "I never titled this" into "I titled it this", and
// the next release's better wording would never reach the person.
func AutoTitle(m MetricMeta, split string) string {
	if split == "" || split == SplitNone {
		return m.Label
	}
	switch split {
	case DimBranch:
		return m.Label + " by branch"
	case DimStatus:
		return m.Label + " by status"
	case DimBucket:
		return m.Label + " by age"
	}
	return m.Label
}

// -- resolution ----------------------------------------------------------------------------------

// ResolveWidgets picks the layer that decides and orders it. WHOLE OR ABSENT, exactly as
// ResolveLayout is for tiles: any personal row at all and the profile rows are not consulted.
//
// That is what makes "Reset to role default" one DELETE with nothing to unwind, no per-widget
// merge, no half-inherited ordering, no way for a reset to leave a fragment behind. It is also why
// there is deliberately NO route that writes the role default into a person's rows: 0191 spent a
// migration undoing exactly that, and 0192/0193 restate it.
//
// Pure. No context, no store, no clock, the part that has to be provably right is testable without
// a database.
func ResolveWidgets(profileRows, userRows []Widget) ([]Widget, string) {
	layer, source := userRows, SourceUser
	if len(layer) == 0 {
		layer, source = profileRows, SourceRole
	}
	if len(layer) == 0 {
		return []Widget{}, SourceCatalog
	}
	out := make([]Widget, len(layer))
	copy(out, layer)
	// Stable on position then key, so two rows sharing a position (possible from a hand-written PUT
	// or from 0202's two blocks) resolve the same way every time instead of by scan order.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Position != out[j].Position {
			return out[i].Position < out[j].Position
		}
		return out[i].Key < out[j].Key
	})
	for i := range out {
		out[i].Position = i
	}
	return out, source
}

// WidgetViewsFor turns resolved rows into what goes on the wire, DROPPING every widget whose measure
// is not in `allowed`.
//
// THIS IS THE BYTE-ABSENCE. A widget naming a measure the caller may not see is not marked hidden,
// not returned with its label stripped, and not returned with a null anything: it is not in the
// slice, so the response bytes never carry its measure id, its label or its number. There is nothing
// for a client to accidentally render and nothing for a proxy to log.
//
// A widget naming a measure this BUILD does not have is dropped by the same line, which is the 0172
// posture (an id you do not recognise renders nothing, silently) and means a measure retired from
// the catalog does not leave holes on anybody's Home.
// NOTE ON THE BRANCH, WHICH IS NOT REPAIRED HERE AND MUST NOT BE. A stored branch this caller
// cannot see is left exactly as it is. Rewriting it would not be a display repair at all, it would
// be a WRITE: the client re-PUTs the whole resolved layout on every drag and emits branch_id
// verbatim (home-bento-logic.ts layoutBody), so a "repair" on the way out becomes a permanent change
// to the person's stored row on the way back, silently, the first time they moved a card. Clearing
// it would also change the QUESTION, absent means "every branch I may see", turning "Sales at
// Trenton" into something broader than what was stored.
//
// The narrowing is applied where it belongs instead: at MEASUREMENT (narrowToBranch, widget_data.go),
// where an intersection that comes out empty produces no datum rather than a false zero.
func WidgetViewsFor(rows []Widget, allowed map[string]MetricMeta) []WidgetView {
	out := make([]WidgetView, 0, len(rows))
	for _, w := range rows {
		m, ok := widgetMeasureMeta(w.Measure, allowed)
		if !ok {
			continue // BYTE-ABSENT. Not hidden, not empty, absent.
		}
		w.Position = len(out)
		if w.Title == "" {
			w.Title = AutoTitle(m, w.Split)
		}
		// REPAIR THE WINDOW, DO NOT DROP THE WIDGET. A stored row naming a window its measure cannot
		// honour is the thirty-row-old state of every shipped install: nine of 0201's role defaults
		// were seeded that way, so a fresh dealer saw "last 14 days" under a number that had counted
		// every open order ever written.
		//
		// The `continue` above is reserved for a MEASURE the caller may not see, where absence is the
		// whole point. A range is not a permission, and dropping the widget would punch a hole in a
		// role default no admin chose. So it is coerced to the measure's natural window and the card
		// then says something true, the same posture as the AutoTitle fill directly above, which
		// repairs a missing field rather than refusing the row.
		if len(m.Ranges) > 0 && !containsString(m.Ranges, w.Range) {
			w.Range = m.Ranges[0]
		}
		cols, rowsN := SpanFor(w)
		out = append(out, WidgetView{
			Widget: w, Label: m.Label, Kind: m.Kind, Module: m.Module,
			Unit: m.Unit, Screen: m.Screen, SpanCols: cols, SpanRows: rowsN,
		})
	}
	return out
}

// naturalWindow is the window a measure is actually measured over, the first entry of its catalog
// list, which is the only one any provider can honour today.
//
// IT READS THE SHIPPED CATALOG, AND THAT IS NOT A PERMISSION DECISION. MetricByID is the build's own
// list, not the caller's curated view, and this is only ever consulted to fill a field somebody left
// blank. Nothing here decides whether a caller may SEE a measure; that is widgetMeasureMeta's job
// against the `allowed` map, and it still runs afterwards.
//
// The blanket `today` this replaced was correct while range was ungated and became a trap the moment
// it was not: a PUT that omitted `range`, an older client, a script, a hand-built body, had the
// whole layout refused with a 422, because Normalize filled `today` for measures whose one honest
// window is `open`. That is seven of the nine.
//
// An unknown measure falls back to `today` rather than guessing. The row is refused a moment later
// for naming a measure that does not exist, which is the honest reason, and inventing a window for
// it here would only change which error it got.
func naturalWindow(measure string) string {
	if measure == MeasureNeedsYou {
		return needsMeasureMeta.Ranges[0]
	}
	if m, ok := MetricByID[measure]; ok && len(m.Ranges) > 0 {
		return m.Ranges[0]
	}
	return RangeToday
}

// widgetMeasureMeta resolves a measure id to its catalog entry, honouring the one reserved id.
//
// Needs-you is allowed for EVERY caller and that is not a hole in the gate: the queue carries no
// catalog measure at all, and each of its rows is produced by a source adapter that applies its own
// module's authority, an approvals row a person may not approve is not counted for them, because
// the approvals read they are counted from is theirs. The gate this file owns is about MEASURES.
func widgetMeasureMeta(measure string, allowed map[string]MetricMeta) (MetricMeta, bool) {
	if measure == MeasureNeedsYou {
		return needsMeasureMeta, true
	}
	m, ok := allowed[measure]
	return m, ok
}

// -- validation ----------------------------------------------------------------------------------

// NormalizeWidgets cleans a submitted layout for storage WITHOUT deciding anything about
// permissions: malformed rows dropped, duplicate keys collapsed to their first occurrence, titles
// trimmed and bounded, positions renumbered 0..n-1 in submitted order.
//
// "Malformed" here means the SUBSTRATE's own vocabulary, a shape, size, split or range outside the
// closed sets, or a key outside 0201's alphabet. It says nothing about whether the measure exists or
// whether the caller may see it: that is a REFUSAL, not a silent drop, and it lives in
// ValidateWidgets so the two cannot be confused. Dropping a forbidden measure quietly would swallow
// exactly the request somebody most wants to hear about.
func NormalizeWidgets(rows []Widget) []Widget {
	sorted := make([]Widget, len(rows))
	copy(sorted, rows)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Position != sorted[j].Position {
			return sorted[i].Position < sorted[j].Position
		}
		return sorted[i].Key < sorted[j].Key
	})
	out := make([]Widget, 0, len(sorted))
	seen := make(map[string]bool, len(sorted))
	for _, w := range sorted {
		w.Split = strings.TrimSpace(w.Split)
		if w.Split == "" {
			w.Split = SplitNone
		}
		w.Range = strings.TrimSpace(w.Range)
		if w.Range == "" {
			w.Range = naturalWindow(w.Measure)
		}
		w.Size = strings.TrimSpace(w.Size)
		if w.Size == "" {
			w.Size = SizeM
		}
		// THE FREE SPAN IS DROPPED, NOT THE WIDGET, AND ONLY WHEN IT IS HALF A PAIR.
		//
		// A negative number or one of the two at zero is what an OLDER CLIENT sends, one that has
		// never heard of 0207 and emits `cols` without `rows`, or a hand-built body, or a script. That
		// is an OMISSION, and an omission must never cost somebody their Home: the identical mistake
		// with `range` (naturalWindow's header, immediately below) refused a whole layout with a 422
		// because Normalize filled a value the write gate then rejected. So the pair goes back to
		// "the preset decides", which is a true answer, and the save proceeds.
		//
		// A pair that is COMPLETE but names a grid line that does not exist, 5 columns, 4 rows, is a
		// different fact and is NOT repaired here. See ValidateWidgets.
		if w.Cols < 0 || w.Rows < 0 || (w.Cols == 0) != (w.Rows == 0) {
			w.Cols, w.Rows = 0, 0
		}
		w.Title = strings.TrimSpace(w.Title)
		if len([]rune(w.Title)) > maxTitleRune {
			w.Title = string([]rune(w.Title)[:maxTitleRune])
		}
		w.Measure = strings.TrimSpace(w.Measure)
		w.BranchID = strings.TrimSpace(w.BranchID)
		if !IsWidgetKey(w.Key) || !IsWidgetShape(w.Shape) || !IsWidgetSize(w.Size) ||
			!IsSplit(w.Split) || !IsRange(w.Range) || !IsBranchID(w.BranchID) ||
			w.Measure == "" || seen[w.Key] {
			continue
		}
		seen[w.Key] = true
		w.Position = len(out)
		out = append(out, w)
	}
	return out
}

// ValidateWidgets is the WRITE GATE: every widget must name a measure in THIS caller's catalog, in a
// shape and split that measure declares.
//
// IT REFUSES THE WHOLE SAVE. A layout is one document, the client always submits the complete list -
// so accepting the legal nine widgets of a submission whose tenth was forged would store a layout
// nobody asked for and report success. Refuse, store nothing, and let the client resubmit.
//
// ONE MESSAGE FOR TWO CASES, on purpose: "this build has no such measure" and "you may not see that
// measure" get the identical refusal, so the error cannot be used to ask the server which
// cost-bearing measures exist. The catalog already answers that question honestly for callers
// entitled to an answer.
func ValidateWidgets(rows []Widget, allowed map[string]MetricMeta, branches BranchGate) error {
	for _, w := range rows {
		m, ok := widgetMeasureMeta(w.Measure, allowed)
		if !ok {
			return ErrMeasureNotOffered
		}
		if !containsString(m.WidgetShapes, w.Shape) {
			return ErrShapeNotOffered
		}
		if !containsString(m.Splits, w.Split) {
			return ErrSplitNotOffered
		}
		// THE RANGE GATE BELONGS HERE AND NOWHERE ELSE, and the reason is not tidiness.
		//
		// NormalizeWidgets already tests IsRange, but that asks "is this one of the ten named windows
		// at all", which is the substrate's own vocabulary, and it answers by SILENTLY DROPPING the
		// row. Whether a window means anything for THIS MEASURE is a per-measure fact, the same class
		// as the two checks above it, and it has to be a refusal: the client re-submits the whole
		// resolved layout on every drag, so a silent drop here would permanently delete a person's
		// widget the first time they rearranged their Home, and delete a ROLE DEFAULT the first time
		// an admin opened the profile editor and saved.
		// The `len > 0` guard mirrors the read path deliberately. A measure added to the catalog with
		// no Ranges is a mistake, but it must fail the SAME way on both paths: without this, the read
		// renders the widget happily while the write refuses every layout containing it, and the
		// symptom is "my Home will not save" pointing at a widget that looks perfectly fine.
		// TestEveryMeasureDeclaresTheWindowsItsProviderCanActuallyHonour is what actually catches it.
		if len(m.Ranges) > 0 && !containsString(m.Ranges, w.Range) {
			return ErrRangeNotOffered
		}
		// A BRANCH IS A NARROWING AND THE CALLER MUST HOLD IT. The empty string, "every branch I
		// can see", is always allowed and is what every shipped widget carries; a named branch has
		// to be one this caller was granted, or the widget is a question they cannot ask.
		//
		// A ROLE DEFAULT is written under AnyBranch instead, on the same argument SaveProfileWidgets
		// already makes about measures: naming is not seeing. The reader's own gate still narrows it.
		if !branches.Allows(w.BranchID) {
			return ErrBranchNotOffered
		}
		// A FREE SPAN MUST NAME A GRID LINE THAT EXISTS, AND THIS IS A REFUSAL RATHER THAN A REPAIR.
		//
		// The split with NormalizeWidgets is the same one the range gate makes four checks up, and it
		// is worth stating in the same terms. Normalize owns the OMISSION, a half-pair from a client
		// that has not learned the two new fields, and clears it back to the preset, because a save
		// refused for a field somebody never sent is the trap widget.go's naturalWindow header already
		// documents this package walking into once.
		//
		// This owns the INVENTION. A complete pair naming 5 columns or 4 rows did not come from the
		// grip, the grip snaps, so it came from a script, an older grid, or a forged body, and there
		// is nothing honest to repair it TO: rounding 5 down to 4 and rounding it up to 6 are both a
		// different card than the one submitted, applied silently, on a request that reported success.
		// A person would find their widget a size they never chose and no message anywhere saying so.
		// Refuse in words, store nothing, and let the client resubmit on real grid lines, which it
		// can, because it owns the grip and already knows the snap set.
		if !IsWidgetSpan(w.Cols, w.Rows) {
			return ErrSpanNotOffered
		}
	}
	return nil
}
