package dashboard

// viz.go, the CURATED METRIC CATALOG and the tiles a person composes from it.
//
// THE RULING THIS FILE IMPLEMENTS. The home screen's "build me a chart" affordance is NOT a query
// tool. A person composes a tile from a catalog of VETTED METRICS, each carrying the DIMENSIONS it
// may be split by and the SHAPES it may be drawn as. Three closed sets, and nothing a user types
// reaches a query.
//
// WHY THAT IS THE SECURITY MODEL AND NOT MERELY A SIMPLIFICATION. A free-form builder, pick a
// table, pick a column, group by, has to re-derive every role gate the ERP already owns, in a
// second place, over expressions nobody wrote by hand. Cost and margin would be one clever
// GROUP BY away from a counter clerk's home screen. With a catalog, a metric that bears cost or
// margin is simply ABSENT from what the caller is offered: MetricsFor drops it, the payload never
// carries its id or its label, and SeriesFor refuses a composed tile that names it. There is no
// CSS to hide and nothing to strip in the client, because nothing arrived.
//
//   ClassCost metrics are gated at the MARSHAL layer (Scope.CostAllowed, resolved by the handler
//   from the caller's tier) and byte-absence is what the mutation test asserts, the metric id,
//   its label and its series must not appear anywhere in the response bytes for a role that may
//   not see them.
//
// WHERE A COMPOSED TILE LIVES. Nowhere new. The composition is a STRING, `viz.<metric>-<dim>-<shape>`
//, stored as a presentation item in the 0172 substrate (surface_user_items, surface `home.dashboard`,
// kind `panel`), which is the 0140/0149 idiom the whole product curates with. No table, no migration,
// no second meaning of "reset": deleting the rows is reset, exactly as it is for tiles, tabs, columns
// and chips. WHICH METRICS A JOB SHAPE IS OFFERED is the other half of the same substrate
// (surface_role_items, surface `home.metrics`, kind `chip`), dealer curation, editable without a
// release, and never the security boundary. The gate above is the security boundary.

import (
	"context"
	"sort"
	"strings"
)

// -- the closed sets -----------------------------------------------------------------------------

// Dimensions a metric may be split by. Closed set: a dimension is a JOIN somebody wrote and tested,
// never a column name off the wire.
const (
	DimBranch = "branch" // one bar per branch the caller may see
	DimStatus = "status" // one bar per lifecycle state
	DimBucket = "bucket" // one segment per ordered band (the AR aging idea, generalized)
)

// Shapes a metric may be drawn as. Closed set, and deliberately three: the product already draws
// all three (the AR distribution bar is `split`), so a composed tile introduces no new visual
// language and no chart library.
const (
	ShapeBar   = "bar"   // ranked comparison across an unordered dimension
	ShapeSplit = "split" // part-to-whole on one line, the shipped aging bar
	ShapeList  = "list"  // the same numbers as labelled rows, for a dimension with long labels
)

// Metric classes. The class is what a metric BEARS, not what it looks like, and it is the only
// thing on MetricMeta that decides whether a caller is offered it at all.
const (
	ClassOperational = "operational" // counts and queues, everybody's business
	ClassRevenue     = "revenue"     // money coming in, visible to whoever sees the till
	ClassCost        = "cost"        // cost, margin, inventory value, MANAGER-UP, byte-absent below
)

// Metric ids. Stable strings: they are half of the primary key of every composed tile a person has
// saved (surface_user_items.item_id), so renaming one orphans saved tiles. Add, never rename.
const (
	MetricSalesByBranch   = "sales.today"
	MetricOrdersOpen      = "orders.open"
	MetricARBalance       = "ar.aging"
	MetricWarrantyOpen    = "warranty.open_claims"
	MetricDeliveriesToday = "deliveries.today"
	MetricPickQueue       = "picks.queue"
	// -- cost class ------------------------------------------------------------------------------
	// These two exist so the gate is REAL and testable rather than a comment about a hypothetical
	// future metric. Both bear figures the counter tier may never see.
	MetricInventoryValue = "inventory.value"  // on-hand at weighted average cost
	MetricGrossMargin    = "margin.gross_bps" // gross margin, basis points
)

// MetricMeta is one catalog entry: everything the composer needs to offer it, and nothing about
// how to measure it.
//
// Class carries NO json tag on purpose, it is a server-side classification, and echoing "this one
// is the cost one" to a client that is not allowed cost metrics would be a smaller leak of the same
// kind the catalog exists to prevent.
type MetricMeta struct {
	ID     string `json:"metric_id"`
	Label  string `json:"label"`
	Module string `json:"module"`
	Kind   Kind   `json:"kind"`
	Class  string `json:"-"`
	// Dimensions and Shapes are the vetted choices for THIS metric, in the order the composer
	// offers them. The first of each is the default the composer opens on.
	Dimensions []string `json:"dimensions"`
	Shapes     []string `json:"shapes"`
	// -- the widget substrate's half of the same entry (widget.go) -------------------------------
	//
	// Splits is the widget builder's SPLIT field: SplitNone first, "None" is the doctrine's first
	// option and the honest default for a stat, then this metric's dimensions, in the same order.
	// It is written out per entry rather than derived at init so the catalog stays a list somebody
	// can read; the drift test asserts it equals SplitNone + Dimensions, so it cannot rot.
	//
	// WidgetShapes is a DIFFERENT closed set from Shapes and both belong here. Shapes is the chart
	// composer's three (bar/split/list) and it decides how a SERIES is drawn; WidgetShapes is the
	// bento's six (stat/trend/bars/list/board/gauge) and it decides which BODY FORM a widget may
	// take for this measure. Collapsing them would force one vocabulary to mean two things, a
	// composed chart's `list` is a labelled series, a widget's `list` is a queue of things with a
	// state, and the migration that carries composed tiles onto widgets (0202) exists precisely
	// because those are not the same object.
	Splits       []string `json:"splits"`
	WidgetShapes []string `json:"widget_shapes"`
	// Ranges is the windows THIS measure can actually be asked for, first one being its natural one
	// and the builder's default.
	//
	// IT IS A LIST OF ONE FOR EVERY MEASURE TODAY, and that is not a placeholder, it is the honest
	// state of the measurement path. Not one series provider takes a window: SeriesProvider.Series is
	// (ctx, scope, dimension) and no query below it has a caller-supplied date predicate. So a widget
	// saved as "last 14 days" on a measure whose SQL has no date column at all was drawing every open
	// order ever written, under a context line that said otherwise, the card lied, in the one place a
	// person has no way to check.
	//
	// Written out per entry rather than derived, like Splits, so the catalog stays a list somebody can
	// read and disagree with. A measure grows this list when its PROVIDER learns the window, never
	// before: the entry is a promise about the SQL, and adding a value here without the predicate to
	// match puts the lie back.
	Ranges []string `json:"ranges"`
	// Unit/Screen make a composed tile a deep link like every other tile, the frozen #unit/module
	// contract, not a private route.
	Unit   string `json:"unit,omitempty"`
	Screen string `json:"screen,omitempty"`
	Lead   string `json:"lead"`
}

// MetricCatalog is the vetted set, in the order the composer lists it.
var MetricCatalog = []MetricMeta{
	{
		ID: MetricSalesByBranch, Label: "Sales today", Module: "pos", Kind: KindMoneyCents,
		Class: ClassRevenue, Dimensions: []string{DimBranch}, Shapes: []string{ShapeBar, ShapeSplit, ShapeList},
		Splits:       []string{SplitNone, DimBranch},
		WidgetShapes: []string{WidgetStat, WidgetTrend, WidgetBars, WidgetList},
		// the SQL is one half-open day on s.created_at, bound to w6aDay(scope), one day, and only ever the one
		Ranges: []string{RangeToday},
		Unit:   "sales", Screen: "pos",
		Lead: "Committed counter sales for today.",
	},
	{
		ID: MetricOrdersOpen, Label: "Open orders", Module: "order", Kind: KindCount,
		Class: ClassOperational, Dimensions: []string{DimStatus, DimBranch}, Shapes: []string{ShapeBar, ShapeList, ShapeSplit},
		Splits:       []string{SplitNone, DimStatus, DimBranch},
		WidgetShapes: []string{WidgetStat, WidgetTrend, WidgetBars, WidgetList},
		// no date predicate and no date column: the population is a status set, not a period
		Ranges: []string{RangeOpen},
		Unit:   "sales", Screen: "pos",
		Lead: "Orders confirmed and not yet closed.",
	},
	{
		ID: MetricARBalance, Label: "AR balance", Module: "billing", Kind: KindMoneyCents,
		Class: ClassRevenue, Dimensions: []string{DimBucket, DimBranch}, Shapes: []string{ShapeSplit, ShapeBar, ShapeList},
		Splits:       []string{SplitNone, DimBucket, DimBranch},
		WidgetShapes: []string{WidgetStat, WidgetBars, WidgetList, WidgetGauge},
		// the book. $2 in the bucket query is the AGING PIVOT, not a window, narrowing it would move where the bands fall, not which invoices count
		Ranges: []string{RangeOpen},
		Unit:   "finance", Screen: "ar",
		Lead: "Outstanding receivables, by age or by branch.",
	},
	{
		ID: MetricWarrantyOpen, Label: "Open claims", Module: "warranty", Kind: KindCount,
		Class: ClassOperational, Dimensions: []string{DimStatus, DimBranch}, Shapes: []string{ShapeBar, ShapeList, ShapeSplit},
		Splits:       []string{SplitNone, DimStatus, DimBranch},
		WidgetShapes: []string{WidgetStat, WidgetBars, WidgetList},
		// no date predicate: every claim not in a closed phase, however old
		Ranges: []string{RangeOpen},
		Unit:   "warranty", Screen: "warranty",
		Lead: "Warranty claims that have not reached a closed phase.",
	},
	{
		ID: MetricDeliveriesToday, Label: "Deliveries today", Module: "dispatch", Kind: KindCount,
		Class: ClassOperational, Dimensions: []string{DimStatus, DimBranch}, Shapes: []string{ShapeBar, ShapeList},
		Splits:       []string{SplitNone, DimStatus, DimBranch},
		WidgetShapes: []string{WidgetStat, WidgetBoard, WidgetBars, WidgetList},
		// one equality on scheduled_date, bound to w6aDay(scope)
		Ranges: []string{RangeToday},
		Unit:   "dispatch", Screen: "dispatch",
		Lead: "Delivery stops scheduled for today.",
	},
	{
		ID: MetricPickQueue, Label: "Pick queue", Module: "tasks", Kind: KindCount,
		Class: ClassOperational, Dimensions: []string{DimStatus, DimBranch}, Shapes: []string{ShapeBar, ShapeList},
		Splits:       []string{SplitNone, DimStatus, DimBranch},
		WidgetShapes: []string{WidgetStat, WidgetBoard, WidgetBars, WidgetList, WidgetGauge},
		// no date predicate: the queue is a status set. It was seeded as `today` on three of the four role defaults, which is the widest of the lies this field closes
		Ranges: []string{RangeOpen},
		Unit:   "yard", Screen: "yard",
		Lead: "Pick tasks waiting or in progress.",
	},
	{
		ID: MetricInventoryValue, Label: "Inventory value", Module: "inventory", Kind: KindMoneyCents,
		Class: ClassCost, Dimensions: []string{DimBranch}, Shapes: []string{ShapeBar, ShapeSplit, ShapeList},
		Splits:       []string{SplitNone, DimBranch},
		WidgetShapes: []string{WidgetStat, WidgetBars, WidgetList},
		// structurally un-windowable: stock_balances is a current-state table with no history, so a
		// window would need the stock ledger, a different source, not a different predicate. `now`
		// rather than `open`: there is nothing open about on-hand stock, only this moment's count.
		Ranges: []string{RangeNow},
		Unit:   "inventory", Screen: "inventory",
		Lead: "On-hand stock at weighted average cost.",
	},
	{
		ID: MetricGrossMargin, Label: "Gross margin", Module: "pos", Kind: KindBasisPoints,
		Class: ClassCost, Dimensions: []string{DimBranch}, Shapes: []string{ShapeBar, ShapeList},
		Splits:       []string{SplitNone, DimBranch},
		WidgetShapes: []string{WidgetStat, WidgetTrend, WidgetBars},
		// all-time, and worth saying plainly: every order line on every order that is not DRAFT or
		// CANCELLED, ever, INVOICED and CLOSED included. It was seeded as `mtd` on two role
		// defaults, and `open` would have been the second false caption rather than the fix.
		Ranges: []string{RangeAllTime},
		Unit:   "sales", Screen: "pos",
		Lead: "Gross margin on invoiced order lines.",
	},
}

// MetricByID indexes MetricCatalog. Built once at init; never mutated.
var MetricByID = func() map[string]MetricMeta {
	m := make(map[string]MetricMeta, len(MetricCatalog))
	for _, x := range MetricCatalog {
		m[x.ID] = x
	}
	return m
}()

// -- the composed tile id ------------------------------------------------------------------------

// VizPrefix marks an item id in the 0172 substrate as a composed chart. Anything else in that
// surface's item set is somebody else's business and is passed over, not rejected, the substrate's
// posture is that an id you do not recognise renders nothing, silently.
const VizPrefix = "viz."

// Composition is a parsed composed-tile id.
type Composition struct {
	ID        string `json:"tile_id"`
	MetricID  string `json:"metric_id"`
	Dimension string `json:"dimension"`
	Shape     string `json:"shape"`
}

// ComposeID builds the stored id for one composition.
//
// THE SEPARATOR IS A DASH AND THE METRIC KEEPS ITS DOTS. Metric ids are the shipped tile ids
// ('ar.aging'), which already carry a dot; splitting on dots would make 'ar.aging' and a metric
// called 'ar' with dimension 'aging' the same string. Dashes appear in no metric id, no dimension
// and no shape, so one Cut from the right of each is unambiguous, and the whole id stays inside
// the substrate's `^[a-z0-9][a-z0-9._-]{0,63}$` alphabet with room to spare.
func ComposeID(metricID, dimension, shape string) string {
	return VizPrefix + metricID + "-" + dimension + "-" + shape
}

// ParseComposition reads a stored item id back into its three choices, VALIDATING each against the
// closed sets. An id naming a metric this build no longer has, a dimension that metric does not
// offer, or a shape nobody vetted is refused, a stale saved tile must not become a query.
func ParseComposition(id string) (Composition, bool) {
	rest, ok := strings.CutPrefix(id, VizPrefix)
	if !ok {
		return Composition{}, false
	}
	head, shape, ok := cutLast(rest, "-")
	if !ok {
		return Composition{}, false
	}
	metricID, dim, ok := cutLast(head, "-")
	if !ok {
		return Composition{}, false
	}
	m, ok := MetricByID[metricID]
	if !ok {
		return Composition{}, false
	}
	if !containsString(m.Dimensions, dim) || !containsString(m.Shapes, shape) {
		return Composition{}, false
	}
	return Composition{ID: id, MetricID: metricID, Dimension: dim, Shape: shape}, true
}

func cutLast(s, sep string) (before, after string, found bool) {
	i := strings.LastIndex(s, sep)
	if i < 0 {
		return "", "", false
	}
	return s[:i], s[i+len(sep):], true
}

func containsString(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

// -- the gate ------------------------------------------------------------------------------------

// CuratedMetric is one dealer-curation row for the metric catalog: which metrics a job shape is
// OFFERED. The shape is 0172's surface_role_items, read for surface `home.metrics`, kind `chip`.
type CuratedMetric struct {
	MetricID string
	Position int
	Visible  bool
}

// MetricsFor answers the catalog THIS caller may compose from.
//
// TWO LAYERS, AND ONLY ONE OF THEM IS SECURITY:
//
//  1. CURATION decides the menu. A dealer's rows (surface_role_items) name the metrics a job shape
//     is offered, in the order it is offered them. NO rows for the profile is not "nothing", it is
//     "nobody has curated this yet", and the honest answer is the shipped catalog, so a fresh
//     deployment has a working composer without a setup step.
//  2. THE COST GATE decides what may EXIST in the answer. Every ClassCost metric is dropped unless
//     the caller is allowed cost. This runs AFTER curation and cannot be curated around: an admin
//     who marks 'margin.gross_bps' visible for the counter shape still sends a counter clerk a
//     payload with no margin metric in it. Curation hides things; it never grants anything.
func MetricsFor(curated []CuratedMetric, costAllowed bool) []MetricMeta {
	order := map[string]int{}
	hidden := map[string]bool{}
	for _, c := range curated {
		if _, known := MetricByID[c.MetricID]; !known {
			continue // an id this build does not have: renders nothing, silently (the 0172 posture)
		}
		if !c.Visible {
			hidden[c.MetricID] = true
			continue
		}
		order[c.MetricID] = c.Position
	}
	out := make([]MetricMeta, 0, len(MetricCatalog))
	for _, m := range MetricCatalog {
		if m.Class == ClassCost && !costAllowed {
			continue // BYTE-ABSENT. Not hidden, not empty, absent.
		}
		if hidden[m.ID] {
			continue
		}
		if len(order) > 0 {
			if _, offered := order[m.ID]; !offered {
				continue
			}
		}
		out = append(out, m)
	}
	if len(order) > 0 {
		sort.SliceStable(out, func(i, j int) bool { return order[out[i].ID] < order[out[j].ID] })
	}
	return out
}

// MetricAllowed answers whether one metric id is in this caller's catalog, the same decision
// MetricsFor makes, for one id, so a composed tile and the catalog can never disagree.
func MetricAllowed(curated []CuratedMetric, costAllowed bool, metricID string) bool {
	for _, m := range MetricsFor(curated, costAllowed) {
		if m.ID == metricID {
			return true
		}
	}
	return false
}

// -- the series ----------------------------------------------------------------------------------

// Point is one bar, segment or row of a composed tile.
// The severities a measured point may carry. They are the screen doctrine's own three, and the
// fourth state, no tone at all, is the empty string and the default.
//
// COLOUR IS RESERVED FOR STATE and never ships alone: every one of these reaches a screen beside the
// word it belongs to, so a person who cannot separate the hues still reads the answer.
const (
	ToneGood  = "good"  // finished, settled, delivered
	ToneWatch = "watch" // blocked on somebody else, or an adverse outcome worth reading
	ToneStop  = "stop"  // somebody has to pick this up
)

type Point struct {
	Label  string `json:"label"`
	Number int64  `json:"number"`
	// Tone is what this point's STATE means, when it means anything: "" (neutral), "good", "watch"
	// or "stop". Omitted when neutral, which is the overwhelming majority.
	//
	// THE SERVER DECIDES SEVERITY BECAUSE ONLY THE SERVER STILL KNOWS THE ENUM. Item 3 closed the
	// third door by wording status labels here, READY became "Ready", so the browser now receives a
	// sentence and could only map it back to a severity by re-introducing the raw value on the wire,
	// which is the thing that was removed.
	//
	// AND MOST STATES HAVE NO SEVERITY. A board painted a colour per row says nothing; the doctrine
	// reserves good/watch/stop for state and requires a word or an icon beside the colour. READY,
	// LOADED and EN_ROUTE are not good or bad, they are where the thing IS. Only a state somebody
	// would act on differently earns a tone.
	Tone string `json:"tone,omitempty"`
}

// SeriesProvider measures ONE metric split by ONE dimension.
//
// CONSUMER-DECLARED, exactly like MetricProvider and for the same reason: this package wants a
// series and must not learn who owns the data. Implementations are adapters in the composition
// root, and they carry the same non-negotiable obligation, Scope.BranchIDs / Scope.AllBranches
// are the caller's grants and the query applies them.
type SeriesProvider interface {
	Series(ctx context.Context, scope Scope, dimension string) ([]Point, error)
}

// SeriesFunc adapts a plain function to SeriesProvider.
type SeriesFunc func(ctx context.Context, scope Scope, dimension string) ([]Point, error)

func (f SeriesFunc) Series(ctx context.Context, scope Scope, dim string) ([]Point, error) {
	return f(ctx, scope, dim)
}

// VizTile is one composed tile as it goes over the wire.
//
// Points is omitempty and State says which kind of nothing an empty one is, the same three-way
// distinction the measured tiles make, because "no chart is wired for this yet" and "the yard is
// clear today" are different sentences and the screen must not print the wrong one.
type VizTile struct {
	Composition
	Label    string  `json:"label"`
	Kind     Kind    `json:"kind"`
	Module   string  `json:"module"`
	Unit     string  `json:"unit,omitempty"`
	Screen   string  `json:"screen,omitempty"`
	Position int     `json:"position"`
	State    string  `json:"state"`
	Total    *int64  `json:"total,omitempty"`
	Points   []Point `json:"points,omitempty"`
	Note     string  `json:"note,omitempty"`
}

// TotalOf sums a series. Exported because the client must never do this arithmetic: the server is
// the arithmetic authority, and a total the browser adds up is a total that disagrees with the
// screen that owns it the moment one point is filtered.
func TotalOf(points []Point) int64 {
	var n int64
	for _, p := range points {
		n += p.Number
	}
	return n
}
