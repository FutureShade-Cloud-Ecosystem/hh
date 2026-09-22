package pricing

// uomplan.go, SCOPE M2-uom. Inline UoM conversion for sales flows: SF → PLT → TON at line entry,
// with round-UP at pick.
//
// WHY A GRAPH WALK AND NOT A TABLE OF PAIRS
// -----------------------------------------
// The ask is "SF → PLT → TON". Three units is three ordered pairs each way, six rows per product,
// and every one of them has to be kept consistent by hand when a pallet count changes. Add LYR
// (layer) and it is twelve. Authoring pairs is how conversion tables end up with a SF→PLT that
// disagrees with the SF→T ÷ PLT→T that should equal it, and there is no way to tell from the data
// which of the two is wrong.
//
// So the table holds EDGES and this file walks them. Two authored rows (SF→PLT, PLT→T) give all six
// conversions, and they cannot disagree with each other because there is only one path. An edge is
// usable in reverse at 1/factor, which is a mathematical identity rather than a modelling choice.
//
// SCOPE PRECEDENCE runs per EDGE, not per lookup: product beats category beats global, and a
// product that overrides only its weight edge still inherits its category's pallet edge. Doing it
// per lookup would mean one product-scoped row silently disabling every inherited edge.
//
// ROUND-UP AT PICK, the thing this is actually for
// ------------------------------------------------
// A customer orders 77 SF. At 0.082 PLT/SF that is 6.314 pallets. Nobody picks 0.314 of a banded
// pallet, so the PICK quantity is 7, and the difference is not a rounding detail, it is 0.686 of a
// pallet of product that leaves the yard. Somebody has to decide whether the customer is charged
// for 77 SF or for 7 pallets, and they cannot decide it if the system never shows them the gap.
// UOMPlan therefore reports the exact quantity, the rounded quantity AND the overage, rather than
// quietly returning the rounded number as if it were the answer.
//
// Ceiling, not rounding. `math.Round(6.314)` is 6, which is a pallet short, the customer drives
// away without enough product. The ONLY direction a pick quantity can move is up.

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"
)

// Conversion factor provenance (mirrors the 0113 CHECK).
const (
	ConvSeededDefault = "SEEDED_DEFAULT"
	ConvClientConfirm = "CLIENT_CONFIRMED"
	ConvVendorSheet   = "VENDOR_SHEET"
	ConvMeasured      = "MEASURED"
)

var convSources = map[string]bool{
	ConvSeededDefault: true, ConvClientConfirm: true, ConvVendorSheet: true, ConvMeasured: true,
}

// UOMConversion is one pricing_uom_conversions row: a directed edge from_uom → to_uom.
type UOMConversion struct {
	ID            string    `json:"id"`
	ProductID     string    `json:"product_id,omitempty"`
	ProductSKU    string    `json:"product_sku,omitempty"` // projection-only, from the list read
	CategoryID    string    `json:"category_id,omitempty"`
	FromUOM       string    `json:"from_uom"`
	ToUOM         string    `json:"to_uom"`
	Factor        float64   `json:"factor"`
	RoundUpAtPick bool      `json:"round_up_at_pick"`
	Source        string    `json:"source"`
	SourceNote    string    `json:"source_note"`
	IsActive      bool      `json:"is_active"`
	Version       int64     `json:"version"`
	UpdatedAt     time.Time `json:"updated_at,omitempty"`
}

// scopeRank is the per-edge precedence: product (2) beats category (1) beats global (0).
func (c UOMConversion) scopeRank() int {
	switch {
	case c.ProductID != "":
		return 2
	case c.CategoryID != "":
		return 1
	default:
		return 0
	}
}

// UOMStep is one hop of a resolved chain, surfaced so the counter can see HOW a number was reached
// and, critically, the provenance of every factor it passed through.
type UOMStep struct {
	FromUOM string  `json:"from_uom"`
	ToUOM   string  `json:"to_uom"`
	Factor  float64 `json:"factor"`
	Source  string  `json:"source"`
}

// UOMPlanInput is a line-entry conversion request.
type UOMPlanInput struct {
	ProductID  string  `json:"product_id"`
	CategoryID string  `json:"category_id"`
	QtyEntered float64 `json:"qty_entered"`
	UOMEntered string  `json:"uom_entered"`
	// PickUOM is the handling unit the yard pulls in (PLT). Empty = "use whatever edge out of the
	// entered unit is flagged round_up_at_pick", which is the common case and keeps the caller from
	// having to know the yard's packaging.
	PickUOM string `json:"pick_uom,omitempty"`
	// WeightUOM is the weight unit to report (T). Empty = skip the weight leg; a missing weight
	// chain is then not an error, because plenty of lines do not need one.
	WeightUOM string `json:"weight_uom,omitempty"`
}

// UOMPlan is what a line-entry surface renders.
type UOMPlan struct {
	ProductID  string  `json:"product_id"`
	QtyEntered float64 `json:"qty_entered"`
	UOMEntered string  `json:"uom_entered"`

	PickUOM      string    `json:"pick_uom,omitempty"`
	PickQtyExact float64   `json:"pick_qty_exact"`
	PickQty      float64   `json:"pick_qty"` // rounded UP when the edge is flagged indivisible
	RoundedUp    bool      `json:"rounded_up"`
	OverageQty   float64   `json:"overage_qty"` // PickQty − PickQtyExact, in PickUOM
	PickSteps    []UOMStep `json:"pick_steps,omitempty"`

	WeightUOM   string    `json:"weight_uom,omitempty"`
	WeightQty   float64   `json:"weight_qty"`
	WeightSteps []UOMStep `json:"weight_steps,omitempty"`

	// Unconfirmed is TRUE when ANY factor on ANY resolved chain is still a SEEDED_DEFAULT. It rides
	// on the payload rather than being left for the UI to derive, because the open provenance
	// question (see 0113) has to travel with the number to every surface that shows it, not just to
	// the one screen someone remembered to annotate.
	Unconfirmed bool     `json:"unconfirmed"`
	Notes       []string `json:"notes,omitempty"`
}

// convStore is the M2-uom persistence seam. Optional and flag-off, like promoStore/rebateStore.
type convStore interface {
	// ConversionsFor returns every ACTIVE edge applicable to a product: its own rows, its
	// category's rows and the global rows, in one query. The walk resolves precedence in Go.
	ConversionsFor(ctx context.Context, productID, categoryID string) ([]UOMConversion, error)
	ListConversions(ctx context.Context, productID string, includeInactive bool) ([]UOMConversion, error)
	UpsertConversion(ctx context.Context, c UOMConversion) (UOMConversion, error)
	DeleteConversion(ctx context.Context, id string) error
}

// WithConversionStore injects the M2-uom store (additive, flag-off).
func (s *Service) WithConversionStore(c convStore) *Service { s.conv = c; return s }

func (s *Service) convOK() error {
	if s.conv == nil {
		return errNoDB
	}
	return nil
}

// convGraph is the resolved adjacency for one product: for each (from, to) the WINNING edge.
type convGraph map[string]map[string]UOMConversion

// buildGraph collapses the applicable rows into a directed graph, applying per-edge scope
// precedence and adding the reverse of every edge at 1/factor.
//
// The reverse edge inherits the forward edge's round_up_at_pick flag as FALSE, never as-is. The
// flag means "the TARGET of this edge is an indivisible handling unit"; reversing the edge changes
// what the target is, so carrying the flag over would claim SF is indivisible because PLT is.
func buildGraph(rows []UOMConversion) convGraph {
	// Deterministic: sort by scope rank so the highest-ranked row for an edge lands last and wins.
	sorted := append([]UOMConversion(nil), rows...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].scopeRank() < sorted[j].scopeRank() })

	g := convGraph{}
	put := func(from, to string, c UOMConversion) {
		from, to = upUOM(from), upUOM(to)
		if g[from] == nil {
			g[from] = map[string]UOMConversion{}
		}
		g[from][to] = c
	}
	for _, r := range sorted {
		if !r.IsActive || r.Factor <= 0 {
			continue
		}
		fwd := r
		fwd.FromUOM, fwd.ToUOM = upUOM(r.FromUOM), upUOM(r.ToUOM)
		put(fwd.FromUOM, fwd.ToUOM, fwd)

		rev := r
		rev.FromUOM, rev.ToUOM = upUOM(r.ToUOM), upUOM(r.FromUOM)
		rev.Factor = 1 / r.Factor
		rev.RoundUpAtPick = false
		put(rev.FromUOM, rev.ToUOM, rev)
	}
	return g
}

// chain finds the SHORTEST path from → to and returns its steps. Breadth-first, so a direct edge
// always beats a two-hop route through another unit, which matters because a direct
// CLIENT_CONFIRMED SF→T must not be bypassed in favour of composing two SEEDED_DEFAULT hops.
//
// Neighbours are visited in sorted order so the path is STABLE between runs when two equal-length
// routes exist. Map iteration order in Go is randomised; without the sort, a product with two
// two-hop routes would convert through a different one on each request and the displayed factor
// would flicker.
func (g convGraph) chain(from, to string) ([]UOMStep, bool) {
	from, to = upUOM(from), upUOM(to)
	if from == to {
		return nil, true
	}
	type node struct {
		uom   string
		steps []UOMStep
	}
	seen := map[string]bool{from: true}
	queue := []node{{uom: from}}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		nbrs := make([]string, 0, len(g[cur.uom]))
		for k := range g[cur.uom] {
			nbrs = append(nbrs, k)
		}
		sort.Strings(nbrs)
		for _, n := range nbrs {
			if seen[n] {
				continue
			}
			e := g[cur.uom][n]
			steps := append(append([]UOMStep(nil), cur.steps...), UOMStep{
				FromUOM: cur.uom, ToUOM: n, Factor: e.Factor, Source: e.Source,
			})
			if n == to {
				return steps, true
			}
			seen[n] = true
			queue = append(queue, node{uom: n, steps: steps})
		}
	}
	return nil, false
}

// convert applies a chain to a quantity.
func convert(qty float64, steps []UOMStep) float64 {
	for _, st := range steps {
		qty *= st.Factor
	}
	return qty
}

// pickEdgeFor answers "which unit does this line get PICKED in" when the caller did not say. It is
// the target of the round_up_at_pick edge reachable from the entered unit, the yard's packaging is
// a property of the product, not something a quote screen should have to know.
func (g convGraph) pickEdgeFor(fromUOM string) (string, bool) {
	fromUOM = upUOM(fromUOM)
	targets := make([]string, 0, len(g[fromUOM]))
	for k, e := range g[fromUOM] {
		if e.RoundUpAtPick {
			targets = append(targets, k)
		}
	}
	if len(targets) == 0 {
		return "", false
	}
	// Deterministic when a product is packaged two ways (PLT and LYR both flagged): alphabetical,
	// and the caller can always name PickUOM explicitly to override.
	sort.Strings(targets)
	return targets[0], true
}

// PlanUOM resolves a line-entry quantity into its pick and weight views.
//
// A MISSING CHAIN IS AN ERROR, NOT A ZERO. errNoConversion propagates rather than the plan quietly
// reporting 0 pallets, the same ruling the resolver already makes for a missing price factor
// (fix #3: "a misconfigured break must fail loud, never silently undercharge"). A pick ticket that
// says 0 is worse than a line-entry screen that says "no conversion configured".
func (s *Service) PlanUOM(ctx context.Context, in UOMPlanInput) (UOMPlan, error) {
	if err := s.convOK(); err != nil {
		return UOMPlan{}, err
	}
	if in.ProductID == "" {
		return UOMPlan{}, badReq("product_id is required")
	}
	if in.QtyEntered <= 0 {
		return UOMPlan{}, badReq("qty_entered must be > 0")
	}
	if in.UOMEntered == "" {
		return UOMPlan{}, badReq("uom_entered is required")
	}
	rows, err := s.conv.ConversionsFor(ctx, in.ProductID, in.CategoryID)
	if err != nil {
		return UOMPlan{}, fmt.Errorf("uom conversions: %w", err)
	}
	g := buildGraph(rows)

	plan := UOMPlan{
		ProductID:  in.ProductID,
		QtyEntered: in.QtyEntered,
		UOMEntered: upUOM(in.UOMEntered),
	}

	pickUOM := upUOM(in.PickUOM)
	if pickUOM == "" {
		if t, ok := g.pickEdgeFor(in.UOMEntered); ok {
			pickUOM = t
		}
	}
	if pickUOM != "" {
		steps, ok := g.chain(in.UOMEntered, pickUOM)
		if !ok {
			return UOMPlan{}, fmt.Errorf("pick uom %s: %w", pickUOM, errNoConversion)
		}
		exact := convert(in.QtyEntered, steps)
		plan.PickUOM, plan.PickSteps, plan.PickQtyExact = pickUOM, steps, exact
		plan.PickQty = exact
		// ROUND UP. Only when the WINNING edge into the pick unit says the unit is indivisible -
		// a category that sells by the partial pallet leaves the flag off and the exact quantity
		// stands.
		if e, ok := g.edge(in.UOMEntered, pickUOM); ok && e.RoundUpAtPick {
			plan.PickQty = math.Ceil(exact)
			plan.RoundedUp = plan.PickQty > exact
		} else if last, ok := lastStepEdge(g, steps); ok && last.RoundUpAtPick {
			plan.PickQty = math.Ceil(exact)
			plan.RoundedUp = plan.PickQty > exact
		}
		plan.OverageQty = plan.PickQty - exact
	}

	if in.WeightUOM != "" {
		steps, ok := g.chain(in.UOMEntered, in.WeightUOM)
		if !ok {
			return UOMPlan{}, fmt.Errorf("weight uom %s: %w", upUOM(in.WeightUOM), errNoConversion)
		}
		plan.WeightUOM, plan.WeightSteps = upUOM(in.WeightUOM), steps
		// WEIGHT IS OF WHAT LEAVES THE YARD. When the pick rounded up to 7 pallets, the truck
		// carries 7 pallets' worth, computing the weight off the ordered 77 SF would under-state
		// the load, and load weight is a legal limit, not a nicety. So the weight is derived from
		// the ROUNDED pick quantity whenever there is one and the two chains share a route.
		if plan.PickUOM != "" && plan.PickQty > 0 {
			if wSteps, ok := g.chain(plan.PickUOM, in.WeightUOM); ok {
				plan.WeightSteps = wSteps
				plan.WeightQty = convert(plan.PickQty, wSteps)
			} else {
				plan.WeightQty = convert(in.QtyEntered, steps)
			}
		} else {
			plan.WeightQty = convert(in.QtyEntered, steps)
		}
	}

	for _, st := range append(append([]UOMStep(nil), plan.PickSteps...), plan.WeightSteps...) {
		if st.Source == ConvSeededDefault {
			plan.Unconfirmed = true
			plan.Notes = append(plan.Notes, fmt.Sprintf(
				"%s→%s factor is a SEEDED DEFAULT, provenance unconfirmed", st.FromUOM, st.ToUOM))
		}
	}
	return plan, nil
}

// edge returns the direct edge if one exists.
func (g convGraph) edge(from, to string) (UOMConversion, bool) {
	e, ok := g[upUOM(from)][upUOM(to)]
	return e, ok
}

// lastStepEdge returns the edge behind the FINAL hop of a chain, the one whose target is the pick
// unit, and therefore the only one whose round_up flag can mean "the thing you pick is indivisible".
func lastStepEdge(g convGraph, steps []UOMStep) (UOMConversion, bool) {
	if len(steps) == 0 {
		return UOMConversion{}, false
	}
	last := steps[len(steps)-1]
	return g.edge(last.FromUOM, last.ToUOM)
}

// upUOM normalises a uom code for map keys. uoms.code is CITEXT in the database, so 'plt' and 'PLT'
// are the same unit and must hash to the same graph node, otherwise a hand-typed lowercase unit
// silently has no conversions.
func upUOM(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'a' && b[i] <= 'z' {
			b[i] -= 'a' - 'A'
		}
	}
	return string(b)
}

// - conversion-data admin -

func (s *Service) ListConversions(ctx context.Context, productID string, includeInactive bool) ([]UOMConversion, error) {
	if err := s.convOK(); err != nil {
		return nil, err
	}
	return s.conv.ListConversions(ctx, productID, includeInactive)
}

func (s *Service) UpsertConversion(ctx context.Context, c UOMConversion, userID string) (UOMConversion, error) {
	if err := s.convOK(); err != nil {
		return UOMConversion{}, err
	}
	c.FromUOM, c.ToUOM = upUOM(c.FromUOM), upUOM(c.ToUOM)
	if c.FromUOM == "" || c.ToUOM == "" {
		return UOMConversion{}, badReq("from_uom and to_uom are required")
	}
	if c.FromUOM == c.ToUOM {
		return UOMConversion{}, unprocessable("from_uom and to_uom must differ")
	}
	if c.Factor <= 0 {
		return UOMConversion{}, unprocessable("factor must be > 0")
	}
	if c.ProductID != "" && c.CategoryID != "" {
		return UOMConversion{}, unprocessable("a conversion is scoped to a product OR a category, not both")
	}
	if c.Source == "" {
		c.Source = ConvSeededDefault
	}
	if !convSources[c.Source] {
		return UOMConversion{}, unprocessable("source must be one of SEEDED_DEFAULT, CLIENT_CONFIRMED, VENDOR_SHEET, MEASURED")
	}
	out, err := s.conv.UpsertConversion(ctx, c)
	if err != nil {
		return UOMConversion{}, err
	}
	refType, refID := convRef(out)
	_ = s.store.AppendAudit(ctx, AuditEntry{
		EntityType: "uom_conversion", EntityID: out.ID, Action: "UPDATE",
		FieldName:     fmt.Sprintf("%s_to_%s", out.FromUOM, out.ToUOM),
		NewValue:      fmt.Sprintf("%.8f (%s)", out.Factor, out.Source),
		UserID:        userID,
		RefEntityType: refType, RefEntityID: refID,
	})
	return out, nil
}

func (s *Service) DeleteConversion(ctx context.Context, id, userID string) error {
	if err := s.convOK(); err != nil {
		return err
	}
	if err := s.conv.DeleteConversion(ctx, id); err != nil {
		return err
	}
	_ = s.store.AppendAudit(ctx, AuditEntry{
		EntityType: "uom_conversion", EntityID: id, Action: "DELETE",
		FieldName: "is_active", OldValue: "true", NewValue: "false", UserID: userID,
	})
	return nil
}

func convRef(c UOMConversion) (string, string) {
	switch {
	case c.ProductID != "":
		return RefProduct, c.ProductID
	case c.CategoryID != "":
		return RefCategory, c.CategoryID
	default:
		return "", ""
	}
}
