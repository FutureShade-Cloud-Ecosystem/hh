package order

// shipgroup.go, DIB-78: multi-shipment / single-invoice fulfilment groups.
//
// A landscape order goes out on three trucks over two weeks. dispatch already keys stops to ORDERS,
// so today that is three stops pointing at one order with NOTHING saying which lines rode on which
// truck. The second truck's picker gets the whole order again; the customer gets one invoice at the
// end whose lines tie to no delivery they witnessed.
//
// A SHIPMENT GROUP is the missing noun: a named subset of an order's line QUANTITIES that is picked,
// staged and dispatched as a unit. Groups are ADDITIVE, a stop still points at the order, and an
// order with no groups behaves exactly as it does today.
//
// - THE INVARIANT -
//
//	for an order that HAS live groups: for every line, Σ group quantities = the line's qty_base
//
// No quantity in two groups (double-shipped), none in zero groups (the orphan line nobody loads).
// It is enforced by a DEFERRABLE INITIALLY DEFERRED constraint trigger in 0126, not by this file:
// a Go-side check binds only Go callers, and the whole point of an invariant is that it binds the
// 2am psql session too. This module's job is to make the guard easy to SATISFY, hence PlanShipment,
// which writes a whole plan in one transaction rather than offering an "add one group" endpoint that
// leaves the order transiently un-partitioned and forces the caller to remember to finish.
//
// - READINESS IS DERIVED, NEVER STORED -
// "Is this group ready to go?" is a function of its lines' availability and the group's own status.
// Storing it would create a second source of truth that goes stale the moment a line moves, which is
// exactly the class of defect the four-bucket work spent a wave removing.
//
// - THE INVOICING RULING (reversible) -
// DEFAULT ON_ALL_COMPLETE: ONE invoice, when every live group is COMPLETE. A job is one bill, and
// that is the ask.
//
// OPT-IN ON_DEMAND_SUBSET: an invoice may be raised for the DELIVERED subset on request. It exists
// because the same contractor on 30-day terms with a six-week phased job will not carry six weeks of
// unbilled work, and progress billing is how that trade is actually done.
//
// The choice is REVERSIBLE while no invoice has been raised against the order, and frozen after -
// flipping it later would change what an already-issued invoice means. It is per-ORDER because it is
// a commercial term of one job, not a company posture.
//
// - WHAT IS PARKED, AND EXACTLY WHERE -
// This module does not MINT invoices, because nothing outside the POS ring does: there is no
// order→invoice writer in the tree (the only INSERT INTO invoices in production code is pos's).
// InvoiceReadiness computes and publishes the decision, which groups are complete, whether the
// policy is satisfied, and what the invoiceable subset is, and MultiShipInvoicer is the port a
// future billing writer binds. Unbound, the readiness read still works and the surface shows the
// decision; only the button is absent. Naming a stub that fabricates an invoice number would be
// worse than no button at all.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// Shipment-group lifecycle (0126 CHECK set).
const (
	GroupPlanned    = "PLANNED"
	GroupPicking    = "PICKING"
	GroupReady      = "READY"
	GroupDispatched = "DISPATCHED"
	GroupComplete   = "COMPLETE"
	GroupCancelled  = "CANCELLED"
)

// Invoice policies (0126 orders.multiship_invoice_policy CHECK set).
const (
	InvoiceOnAllComplete  = "ON_ALL_COMPLETE"
	InvoiceOnDemandSubset = "ON_DEMAND_SUBSET"
)

var (
	// ErrGroupNotFound, no such shipment group on a reachable order.
	ErrGroupNotFound = errors.New("order: shipment group not found")
	// ErrShipPlanIncomplete, the submitted plan does not partition the order. Raised BEFORE the
	// write as a readable 400 listing the offending lines; the 0126 trigger is the backstop that
	// catches anything this check does not.
	ErrShipPlanIncomplete = errors.New("order: the shipment plan does not cover every order line exactly once")
	// ErrGroupIllegalTransition, the requested group move is not legal from its current status.
	ErrGroupIllegalTransition = errors.New("order: illegal shipment group transition")
	// ErrInvoicePolicyFrozen, the policy cannot be changed once money has been billed on the order.
	ErrInvoicePolicyFrozen = errors.New("order: an invoice has already been raised, the invoicing policy is now fixed")
	// ErrShipNotConfigured, the shipment store is not wired (a scaffold/fake store).
	ErrShipNotConfigured = errors.New("order: shipment-group store not configured")
	// ErrInvoicerUnwired, an invoice was requested but no invoice writer is bound. 503, with the
	// readiness payload, so the surface shows WHAT would be invoiced rather than an empty error.
	ErrInvoicerUnwired = errors.New("order: no invoice writer is bound, multi-ship invoicing is not enabled")
)

// maxShipmentGroups bounds a plan. Twenty trucks on one order is a project, not a delivery, and a
// plan nobody can read on one screen is a plan nobody checks.
const maxShipmentGroups = 20

// ShipmentGroupLineInput is one line's share of a group in a submitted plan.
type ShipmentGroupLineInput struct {
	LineNo  int     `json:"line_no"`
	QtyBase float64 `json:"qty_base"`
}

// ShipmentGroupInput is one group in a submitted plan.
type ShipmentGroupInput struct {
	Label         string                   `json:"label"`
	RequestedDate string                   `json:"requested_date,omitempty"`
	Lines         []ShipmentGroupLineInput `json:"lines"`
}

// ShipmentGroupLine is one persisted group line, decorated for the surface.
type ShipmentGroupLine struct {
	LineNo         int     `json:"line_no"`
	OrderLineID    string  `json:"order_line_id"`
	ProductID      string  `json:"product_id"`
	Description    string  `json:"description"`
	QtyBase        float64 `json:"qty_base"`
	OrderQtyBase   float64 `json:"order_qty_base"`
	FulfilledBase  float64 `json:"fulfilled_base"`
	UnitPriceCents int64   `json:"unit_price_cents"`
	// ShareCents is this group's share of the line's money, the line total pro-rated by quantity.
	// PRO-RATED, not recomputed from unit price × qty, because a line total may carry rounding the
	// unit price cannot reproduce, and three groups whose shares do not sum to the line would put a
	// cent of drift into a progress invoice.
	ShareCents int64 `json:"share_cents"`
}

// ShipmentGroup is one group with its lines.
type ShipmentGroup struct {
	ID             string              `json:"id"`
	OrderID        string              `json:"order_id"`
	GroupNo        int                 `json:"group_no"`
	Label          string              `json:"label"`
	Status         string              `json:"status"`
	RequestedDate  string              `json:"requested_date,omitempty"`
	DeliveryStopID string              `json:"delivery_stop_id,omitempty"`
	DispatchedAt   string              `json:"dispatched_at,omitempty"`
	CompletedAt    string              `json:"completed_at,omitempty"`
	Lines          []ShipmentGroupLine `json:"lines"`
	// SubtotalCents is Σ ShareCents, what this truckload is worth.
	SubtotalCents int64 `json:"subtotal_cents"`
	// LegalActions is what may be done to this group now, from the same ladder the write enforces.
	LegalActions []string `json:"legal_actions"`

	// - W6-C / 0147: WHICH DOCUMENT BILLED THIS GROUP -
	// Deliberately NOT a seventh ladder rung. The ladder says where the STOCK is, and billing is not
	// a place stock can be; a billed group is still COMPLETE and now additionally has a document.
	InvoiceID string `json:"invoice_id,omitempty"`
	InvoiceNo string `json:"invoice_no,omitempty"`
	// Invoiced is the fact the readiness rules actually turn on, see computeReadiness.
	Invoiced bool `json:"invoiced"`
}

// ShipmentPlan is the whole multi-ship view of one order.
type ShipmentPlan struct {
	OrderID       string          `json:"order_id"`
	OrderNo       string          `json:"order_no"`
	InvoicePolicy string          `json:"invoice_policy"`
	PolicyFrozen  bool            `json:"policy_frozen"`
	Groups        []ShipmentGroup `json:"groups"`
	// Coverage reports the invariant AS OBSERVED, per line. It is computed by the same arithmetic
	// the 0126 trigger enforces, so a surface can show a plan's state without waiting for a commit
	// to fail. A well-formed plan has every entry Covered.
	Coverage []ShipmentCoverage `json:"coverage"`
	// Readiness is the invoicing decision (see InvoiceReadiness).
	Readiness InvoiceReadiness `json:"readiness"`
}

// ShipmentCoverage is one order line's coverage fact.
type ShipmentCoverage struct {
	LineNo       int     `json:"line_no"`
	OrderQtyBase float64 `json:"order_qty_base"`
	PlannedBase  float64 `json:"planned_base"`
	Covered      bool    `json:"covered"`
}

// InvoiceReadiness is the multi-ship invoicing decision, computed and published rather than acted on
// (see the header: no order→invoice writer exists in the tree yet).
type InvoiceReadiness struct {
	Policy string `json:"policy"`
	// GroupsTotal / GroupsComplete count LIVE groups (cancelled ones are not part of the plan).
	GroupsTotal    int `json:"groups_total"`
	GroupsComplete int `json:"groups_complete"`
	// GroupsInvoiced counts LIVE groups that already carry a document (0147). A group is billed at
	// most once, ever.
	GroupsInvoiced int `json:"groups_invoiced"`
	// InvoiceableCents is what may be billed RIGHT NOW under the policy: the completed groups that
	// have NOT already been billed. Zero when everything complete is already on a document.
	InvoiceableCents int64 `json:"invoiceable_cents"`
	// Ready reports whether an invoice may be raised now.
	Ready bool `json:"ready"`
	// Reason is a human sentence for the surface when Ready is false.
	Reason string `json:"reason,omitempty"`
	// WriterBound reports whether an invoice writer is actually available (the PARK, see header).
	WriterBound bool `json:"writer_bound"`
}

// MultiShipInvoicer is the port a future order→invoice writer binds. Deliberately narrow: it is
// handed the order, the policy and the exact group ids whose lines are being billed, and returns the
// invoice it raised. Nothing in this module writes an invoices row.
type MultiShipInvoicer interface {
	InvoiceGroups(ctx context.Context, orderID string, groupIDs []string, policy string) (invoiceID, invoiceNo string, err error)
}

// WithMultiShipInvoicer binds the invoice writer. Unbound is the shipped state (a PARK, named in the
// report) and the readiness read works regardless.
func (s *Service) WithMultiShipInvoicer(i MultiShipInvoicer) *Service { s.invoicer = i; return s }

// groupTransitions is the shipment-group ladder, forward only, with CANCELLED reachable from any
// live state. Same shape and same reasoning as the order matrix: one table both the write path and
// the surface read, so a control can never be offered for a move the server would refuse.
var groupTransitions = map[string]map[string]bool{
	GroupPlanned:    {GroupPicking: true, GroupCancelled: true},
	GroupPicking:    {GroupReady: true, GroupCancelled: true},
	GroupReady:      {GroupDispatched: true, GroupCancelled: true},
	GroupDispatched: {GroupComplete: true, GroupCancelled: true},
	GroupComplete:   {},
	GroupCancelled:  {},
}

// groupLegalActions renders the ladder for the surface, deterministically ordered.
func groupLegalActions(status string) []string {
	order := []string{GroupPicking, GroupReady, GroupDispatched, GroupComplete, GroupCancelled}
	allowed := groupTransitions[status]
	out := []string{}
	for _, t := range order {
		if allowed[t] {
			out = append(out, t)
		}
	}
	return out
}

// prorateShares splits a line's total money across N group quantities so the shares SUM EXACTLY to
// the line total. The remainder cents go to the FIRST groups, one each, the largest-remainder rule
// every money split in this codebase uses (landed cost, split tender), applied here for the same
// reason: three shares of a $100.00 line must be $33.34 / $33.33 / $33.33 and never $33.33 × 3.
func prorateShares(lineTotalCents int64, qtys []float64) []int64 {
	out := make([]int64, len(qtys))
	if len(qtys) == 0 {
		return out
	}
	var totalQty float64
	for _, q := range qtys {
		totalQty += q
	}
	if totalQty <= 0 {
		return out
	}
	var assigned int64
	for i, q := range qtys {
		share := int64(float64(lineTotalCents) * q / totalQty)
		out[i] = share
		assigned += share
	}
	// Distribute the rounding remainder one cent at a time, first group first. Signed-safe: a
	// negative line total (which the 0008 CHECK forbids today) would still sum correctly.
	rem := lineTotalCents - assigned
	step := int64(1)
	if rem < 0 {
		step = -1
		rem = -rem
	}
	for i := int64(0); i < rem; i++ {
		out[int(i)%len(out)] += step
	}
	return out
}

// computeReadiness is the invoicing decision as a PURE function over the plan facts, so the ruling is
// testable without a database and the surface, the API and the (future) writer all grade identically.
func computeReadiness(policy string, groups []ShipmentGroup, writerBound bool) InvoiceReadiness {
	r := InvoiceReadiness{Policy: policy, WriterBound: writerBound}
	if policy == "" {
		r.Policy = InvoiceOnAllComplete
	}
	// billableCents is the COMPLETE AND UNBILLED money. Splitting it out from completeCents is the
	// whole of the W6-C double-bill guard, and it is not hypothetical: under ON_DEMAND_SUBSET a phased
	// job bills groups 1-2 in June and group 3 in July. If "invoiceable" still meant "every complete
	// group", July's invoice would re-bill June's two truckloads, the customer pays twice for one
	// delivery, and the only person who finds out is the one reading their own statement.
	var completeCents, billableCents int64
	for _, g := range groups {
		if g.Status == GroupCancelled {
			continue
		}
		r.GroupsTotal++
		if g.Invoiced {
			r.GroupsInvoiced++
		}
		if g.Status == GroupComplete {
			r.GroupsComplete++
			completeCents += g.SubtotalCents
			if !g.Invoiced {
				billableCents += g.SubtotalCents
			}
		}
	}
	unbilledComplete := r.GroupsComplete - r.GroupsInvoiced
	switch {
	case r.GroupsTotal == 0:
		// DIB-513: this used to open "This order ships as one delivery, …", which is word for word
		// how the panel's own empty state opens a few rows above it ("This order ships as one
		// delivery. Splitting it across trucks starts here."). One fact, stated twice, on screen at
		// once. The empty state is the one that also says what to DO about it, so this line keeps
		// only the part that is its own job: why the Invoice button beside it is refusing.
		r.Reason = "No shipment groups, nothing to invoice separately."
	case r.GroupsComplete == r.GroupsTotal && unbilledComplete > 0:
		r.InvoiceableCents = billableCents
		r.Ready = true
	case r.GroupsComplete == r.GroupsTotal:
		// Everything delivered, everything billed. Not an error and not a failure, the happy end of
		// the ladder, so the sentence says so plainly rather than sounding like a refusal.
		r.Reason = "Every shipment group on this order has been invoiced."
	case r.Policy == InvoiceOnDemandSubset && unbilledComplete > 0:
		r.InvoiceableCents = billableCents
		r.Ready = true
	case r.Policy == InvoiceOnDemandSubset && r.GroupsComplete > 0:
		r.Reason = fmt.Sprintf("all %d completed shipment group(s) have already been invoiced, "+
			"the next invoice waits on the next delivery", r.GroupsComplete)
	case r.Policy == InvoiceOnDemandSubset:
		r.Reason = "no shipment group is complete yet"
	default:
		r.Reason = fmt.Sprintf("%d of %d shipment groups are complete, this order bills as one invoice "+
			"when they all are", r.GroupsComplete, r.GroupsTotal)
	}
	return r
}

// - the writes -

// PlanShipment REPLACES an order's shipment plan in one transaction.
//
// REPLACE, not append. A plan is a partition, and a partition is only meaningful whole: an endpoint
// that added one group at a time would leave the order un-partitioned between calls (which the 0126
// trigger refuses at commit anyway) and would push the burden of "did I finish?" onto the caller.
// Groups that have already DISPATCHED are preserved verbatim and may not be re-planned, the truck
// has gone, and rewriting what was on it is a lie about a delivery that happened.
//
// AUTHORITY: active in-branch staff. Splitting an order across trucks is dispatch planning, not a
// money move; it changes no quantity, no price and no total.
func (s *Service) PlanShipment(ctx context.Context, orderIDOrNo string, groups []ShipmentGroupInput) (ShipmentPlan, error) {
	caller, orderID, _, _, err := s.changeOrderContext(ctx, orderIDOrNo)
	if err != nil {
		return ShipmentPlan{}, err
	}
	ss, err := s.shipStore()
	if err != nil {
		return ShipmentPlan{}, err
	}
	if len(groups) == 0 {
		return ShipmentPlan{}, httpx.BadRequestf("a shipment plan needs at least one group")
	}
	if len(groups) > maxShipmentGroups {
		return ShipmentPlan{}, httpx.BadRequestf("a shipment plan carries at most %d groups", maxShipmentGroups)
	}
	for i := range groups {
		groups[i].Label = strings.TrimSpace(groups[i].Label)
		if len(groups[i].Label) > 120 {
			return ShipmentPlan{}, httpx.BadRequestf("group %d: label exceeds 120 characters", i+1)
		}
		if len(groups[i].Lines) == 0 {
			return ShipmentPlan{}, httpx.BadRequestf("group %d: a shipment group needs at least one line", i+1)
		}
		for _, l := range groups[i].Lines {
			if l.QtyBase <= 0 {
				return ShipmentPlan{}, httpx.BadRequestf(
					"group %d line %d: a group quantity must be positive, omit the line instead", i+1, l.LineNo)
			}
		}
	}
	if err := ss.ReplaceShipmentPlan(ctx, orderID, caller.UserID, groups); err != nil {
		return ShipmentPlan{}, err
	}
	return s.shipmentPlanFor(ctx, caller, orderID)
}

// TransitionGroup moves one group along its ladder. A group's status is INDEPENDENT of the order's:
// truck one may be COMPLETE while truck three is still PLANNED, which is the entire point.
func (s *Service) TransitionGroup(ctx context.Context, groupID, target string) (ShipmentGroup, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return ShipmentGroup{}, err
	}
	if caller.Status != identity.StatusActive {
		return ShipmentGroup{}, ErrForbidden
	}
	ss, err := s.shipStore()
	if err != nil {
		return ShipmentGroup{}, err
	}
	if !looksLikeUUID(groupID) {
		return ShipmentGroup{}, ErrGroupNotFound
	}
	target = strings.ToUpper(strings.TrimSpace(target))
	if _, ok := groupTransitions[target]; !ok {
		return ShipmentGroup{}, httpx.BadRequestf("unknown shipment group status %q", target)
	}
	allowed := make([]string, 0, 4)
	for from, tos := range groupTransitions {
		if tos[target] {
			allowed = append(allowed, from)
		}
	}
	if len(allowed) == 0 {
		return ShipmentGroup{}, ErrGroupIllegalTransition
	}
	g, branchID, err := ss.TransitionShipmentGroup(ctx, groupID, target, allowed)
	if err != nil {
		return ShipmentGroup{}, err
	}
	if !caller.CanAccessBranch(branchID) {
		return ShipmentGroup{}, ErrGroupNotFound
	}
	g.LegalActions = groupLegalActions(g.Status)
	return g, nil
}

// SetInvoicePolicy flips the per-order invoicing ruling. Manager-gated (it is a commercial term) and
// REFUSED once an invoice exists against the order, see the header.
func (s *Service) SetInvoicePolicy(ctx context.Context, orderIDOrNo, policy string) (ShipmentPlan, error) {
	caller, orderID, _, _, err := s.changeOrderContext(ctx, orderIDOrNo)
	if err != nil {
		return ShipmentPlan{}, err
	}
	if !caller.IsManager() {
		return ShipmentPlan{}, ErrForbidden
	}
	ss, err := s.shipStore()
	if err != nil {
		return ShipmentPlan{}, err
	}
	policy = strings.ToUpper(strings.TrimSpace(policy))
	if policy != InvoiceOnAllComplete && policy != InvoiceOnDemandSubset {
		return ShipmentPlan{}, httpx.BadRequestf(
			"invoice policy must be %s or %s", InvoiceOnAllComplete, InvoiceOnDemandSubset)
	}
	frozen, err := ss.OrderHasInvoice(ctx, orderID)
	if err != nil {
		return ShipmentPlan{}, err
	}
	if frozen {
		return ShipmentPlan{}, ErrInvoicePolicyFrozen
	}
	if err := ss.SetInvoicePolicy(ctx, orderID, policy); err != nil {
		return ShipmentPlan{}, err
	}
	return s.shipmentPlanFor(ctx, caller, orderID)
}

// InvoiceShipments raises the invoice the readiness decision says may be raised. Unbound writer →
// ErrInvoicerUnwired (503), never a fabricated invoice number.
func (s *Service) InvoiceShipments(ctx context.Context, orderIDOrNo string) (InvoiceReadiness, string, string, error) {
	caller, orderID, _, _, err := s.changeOrderContext(ctx, orderIDOrNo)
	if err != nil {
		return InvoiceReadiness{}, "", "", err
	}
	if !caller.IsManager() {
		return InvoiceReadiness{}, "", "", ErrForbidden
	}
	plan, err := s.shipmentPlanFor(ctx, caller, orderID)
	if err != nil {
		return InvoiceReadiness{}, "", "", err
	}
	if !plan.Readiness.Ready {
		return plan.Readiness, "", "", fmt.Errorf("%w: %s", ErrGroupIllegalTransition, plan.Readiness.Reason)
	}
	if s.invoicer == nil {
		return plan.Readiness, "", "", ErrInvoicerUnwired
	}
	// COMPLETE **AND UNBILLED**. The `!g.Invoiced` half is the double-bill guard; see computeReadiness
	// for why a phased job makes it a normal monthly occurrence rather than an edge case. Readiness
	// has already refused the call when this set would be empty, so the writer never sees a zero-line
	// request from here.
	ids := make([]string, 0, len(plan.Groups))
	for _, g := range plan.Groups {
		if g.Status == GroupComplete && !g.Invoiced {
			ids = append(ids, g.ID)
		}
	}
	invID, invNo, err := s.invoicer.InvoiceGroups(ctx, orderID, ids, plan.Readiness.Policy)
	if err != nil {
		return plan.Readiness, "", "", err
	}
	return plan.Readiness, invID, invNo, nil
}

// - the read -

// GetShipmentPlan returns an order's multi-ship view.
func (s *Service) GetShipmentPlan(ctx context.Context, orderIDOrNo string) (ShipmentPlan, error) {
	caller, orderID, _, _, err := s.changeOrderContext(ctx, orderIDOrNo)
	if err != nil {
		return ShipmentPlan{}, err
	}
	return s.shipmentPlanFor(ctx, caller, orderID)
}

func (s *Service) shipmentPlanFor(ctx context.Context, _ identity.StaffContext, orderID string) (ShipmentPlan, error) {
	ss, err := s.shipStore()
	if err != nil {
		return ShipmentPlan{}, err
	}
	plan, err := ss.ShipmentPlan(ctx, orderID)
	if err != nil {
		return ShipmentPlan{}, err
	}
	for i := range plan.Groups {
		plan.Groups[i].LegalActions = groupLegalActions(plan.Groups[i].Status)
	}
	plan.Readiness = computeReadiness(plan.InvoicePolicy, plan.Groups, s.invoicer != nil)
	return plan, nil
}

func (s *Service) shipStore() (shipStore, error) {
	ss, ok := s.store.(shipStore)
	if !ok {
		return nil, ErrShipNotConfigured
	}
	return ss, nil
}
