package purchasing

// cost_gate.go, the first cost gate this module has ever had, and the one place it is decided.
//
// - WHAT WAS OPEN, AND TO WHOM -
//
// purchasing_po_lines.unit_cost_cents reached a client as a bare int64 through
// GET /api/v1/purchasing/pos/{id}, GET /purchasing/pos, GET /purchasing/receiving,
// GET /purchasing/receipts/{id} and GET /purchasing/incoming. Every one of those is RequireStaff,
// which is correct for the READ, a yard hand receiving a truck at 7am must be able to open the
// order, and wrong for the MONEY on it. What a dealer pays for a pallet of Unilock is the most
// commercially sensitive number a supply yard holds; it is the number a competitor would pay for
// and the number a departing salesperson can carry out in their head. Margin (0170) and quote cost
// (DIB-506) have been gated at the marshal layer for three trains. Purchase cost never was.
//
// - WHY THE GATE IS AT THE MARSHAL LAYER AND NOT AT THE ROUTE -
//
// Because the alternative, a second, manager-only copy of every read, is how a program grows two
// answers to one question. A manager and a clerk opening the same purchase order must see the same
// order: the same lines, the same quantities, the same vendor, the same expected date. Exactly one
// thing differs, and it differs by being ABSENT.
//
// ABSENT, not zero and not null. Three trains in this program have found the same defect from the
// other side: a zeroed cost reads as free, and a `null` cost is a field that failed to populate.
// Both of those are a screen telling a story about money that is not true. So the gated payload is
// a struct of POINTERS with omitempty, and a refused caller receives a struct with no money key in
// it at all, the same shape pos.MarginGlance and quote.QuoteMargin already marshal as `{}`.
//
// - WHY ONE STRUCT, EMBEDDED, RATHER THAN FIVE -
//
// POCosts is embedded into every purchasing read shape that carries money (PO, POLine, Receipt,
// ReceiptLine, WorklistItem, IncomingPOLine). Each of them uses a different two or three of its
// fields, which looks loose and is deliberate: FIVE purpose-built cost structs would be five places
// to forget the gate, and the one that got forgotten would be the one nobody was looking at. One
// struct means one producer (costLens.costs below), and a new money field added to it is gated the
// moment it exists rather than the moment somebody remembers.
//
// Embedding also keeps the WIRE unchanged for a caller who is allowed. `unit_cost_cents` is still
// `unit_cost_cents` at the top level of a line, because embedded fields are promoted by
// encoding/json, so this gate is invisible to every screen that may see cost, and total to every
// screen that may not. The domain fields keep their int64 type behind `json:"-"`: money arithmetic
// on a pointer is a nil dereference waiting for the one line whose cost nobody set.
//
// - WHERE THIS GATE DOES NOT REACH, AND WHY THAT IS A DECISION -
//
// The AP BILL surface (GET /purchasing/bills, /bills/{id}) is NOT gated by this switch, and that is
// argued rather than overlooked. A purchase order's unit cost is what we BUY AT, a negotiated
// price, per vendor, that a competitor would like to have. A vendor bill is what we OWE, and the
// people who read it are the people whose job is to pay it. Hiding a payable behind a
// purchase-cost switch would take the invoice away from AP, which is the opposite of the point.
// When the bill surface gets a gate it will be AP's own switch with AP's own audience, and it will
// not be this column.

import (
	"context"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/settings"
)

// - the policy -

// Policy is the resolved purchasing policy for a branch (the branch row, else the dealer default).
type Policy struct {
	BranchID         string `json:"branch_id,omitempty"`
	CostVisibility   string `json:"cost_visibility"`
	UntaggedFallback string `json:"untagged_fallback"`
	IsBranchOverride bool   `json:"is_branch_override"`
}

// The untagged-send postures (purchasing_policy.untagged_fallback, migration 0197). 0183's values,
// and 0183's omission: there is no BLAST.
const (
	FallbackConfirm = "CONFIRM"
	FallbackRefuse  = "REFUSE"
)

// DefaultPolicy is what an unreadable policy resolves to, and every field of it is the restrictive
// answer that the migration also seeds, so a database hiccup can never widen who sees cost.
//
// UntaggedFallback degrades to CONFIRM rather than REFUSE, which looks like the loose choice and is
// not: CONFIRM still requires a person to choose the fallback, still records it as FALLBACK forever,
// and still refuses outright when more than one address could match. REFUSE on an unreadable policy
// would turn a transient scan error into "nobody can order anything today", which is a failure that
// degrades UPWARD into the yard.
func DefaultPolicy() Policy {
	return Policy{CostVisibility: settings.VisibilityManagerUp, UntaggedFallback: FallbackConfirm}
}

// policyStore is the optional persistence seam for the policy read, discovered by type assertion off
// *Repository, the shape incomingStore and onOrderStore already use here, and for the same reason:
// a unit test that had to reach a database to prove WHO SEES COST would be proving it in the one
// place the decision is hardest to see.
type policyStore interface {
	Policy(ctx context.Context, branchID string) (Policy, error)
}

func (s *Service) policyReader() (policyStore, bool) {
	if s.policySeam != nil {
		return s.policySeam, true
	}
	if s.repo == nil || s.repo.pool == nil {
		return nil, false
	}
	return s.repo, true
}

// PolicyFor resolves the effective policy for a branch, FAILING CLOSED and never erroring.
//
// This is a READ PATH, so it degrades downward and returns a value in every case: no repository, no
// row, a scan error and an unknown vocabulary all resolve to DefaultPolicy(). A purchasing screen
// that 500s because a policy row could not be read is a screen nobody can receive a truck on.
func (s *Service) PolicyFor(ctx context.Context, branchID string) Policy {
	store, ok := s.policyReader()
	if !ok {
		return DefaultPolicy()
	}
	p, err := store.Policy(ctx, branchID)
	if err != nil {
		s.log.Warn("purchasing: policy unreadable, falling back to the restrictive default",
			"branch_id", branchID, "err", err)
		return DefaultPolicy()
	}
	switch p.CostVisibility {
	case settings.VisibilityManagerUp, settings.VisibilityAllStaff, settings.VisibilityHidden:
	default:
		p.CostVisibility = settings.VisibilityManagerUp
	}
	if p.UntaggedFallback != FallbackConfirm && p.UntaggedFallback != FallbackRefuse {
		p.UntaggedFallback = FallbackConfirm
	}
	return p
}

// - the gated payload -

// POCosts is the ONLY struct in this module that carries purchase cost to a client.
//
// Every field is a pointer and every field is omitempty. That is the gate: the zero value marshals
// as `{}`, and embedded in a read shape it contributes NO KEY AT ALL. A caller the dealer's
// cost_visibility does not allow therefore receives a purchase order with quantities, units, dates,
// vendor and status intact and not one byte of money, not a zero, not a null, nothing.
//
// The field NAMES differ from the domain fields they project (Subtotal, not SubtotalCents) so that
// embedding cannot produce an ambiguous selector; the JSON TAGS are identical to the keys those
// domain fields used to carry, so every screen that may see cost reads exactly what it always read.
type POCosts struct {
	// header money (PO)
	Subtotal              *int64 `json:"subtotal_cents,omitempty"`
	SubtotalCad           *int64 `json:"subtotal_cad_cents,omitempty"`
	WarrantyCreditApplied *int64 `json:"warranty_credit_applied_cents,omitempty"`
	NetTotal              *int64 `json:"net_total_cents,omitempty"`
	// line money (POLine, ReceiptLine, IncomingPOLine)
	UnitCost  *int64 `json:"unit_cost_cents,omitempty"`
	LineTotal *int64 `json:"line_total_cents,omitempty"`
	Billed    *int64 `json:"billed_cents,omitempty"`
	Value     *int64 `json:"value_cents,omitempty"`
	// document money (Receipt, WorklistItem)
	ValueCad  *int64 `json:"value_cad_cents,omitempty"`
	ValueOpen *int64 `json:"value_open_cents,omitempty"`
}

// costLens is the resolved answer to "may THIS caller see what we pay", carried down one read.
//
// The policy is asked ONCE per request rather than once per line, a hundred-line purchase order
// must not be a hundred policy reads, and more importantly a per-line ask could answer differently
// half way down a document.
type costLens struct{ allow bool }

// costs is the ONE method in this module that turns purchase money into a payload.
//
// It returns the ZERO VALUE for a refused caller, which marshals as `{}` and contributes nothing
// when embedded. Nothing else in this package constructs a POCosts with a non-nil field, which is
// what makes the gate provable rather than merely present: to leak a cost byte you would have to
// write a second producer.
func (l costLens) costs(fill func(c *POCosts)) POCosts {
	if !l.allow {
		return POCosts{}
	}
	var c POCosts
	fill(&c)
	return c
}

// costLensFor resolves the lens for one caller against one branch's policy.
//
// FAIL CLOSED at every unknown: an anonymous caller, an unreadable policy and an unrecognised
// visibility word all land on "manager and up", which for a frontline caller means no money. The
// gate's failure mode is silence, which is the only failure mode a money gate may have.
func (s *Service) costLensFor(ctx context.Context, branchID string) costLens {
	caller, err := s.caller(ctx)
	if err != nil {
		return costLens{}
	}
	switch s.PolicyFor(ctx, branchID).CostVisibility {
	case settings.VisibilityAllStaff:
		return costLens{allow: true}
	case settings.VisibilityHidden:
		return costLens{}
	default: // MANAGER_UP, the seeded default, and where every unknown resolves
		return costLens{allow: caller.IsManager()}
	}
}

// POCostsFor is the module's own answer to "may I see cost on this order", exposed so a surface can
// ask before it draws a column rather than discovering the answer by finding an absent key.
//
// It returns the PO's own money as the caller may have it: the header pair for an allowed caller,
// `{}` for a refused one. It is deliberately the same POCosts the read shapes embed, a second
// struct here would be a second producer, and the gate is only provable while there is one.
func (s *Service) POCostsFor(ctx context.Context, poID string) (POCosts, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return POCosts{}, err
	}
	orders, err := s.orders()
	if err != nil {
		return POCosts{}, err
	}
	po, err := orders.POHeader(ctx, poID)
	if err != nil {
		return POCosts{}, err
	}
	if !caller.CanAccessBranch(po.BranchID) {
		return POCosts{}, errNotFound
	}
	lines, err := orders.POLinesFor(ctx, poID)
	if err != nil {
		return POCosts{}, err
	}
	var subtotal int64
	for _, l := range lines {
		subtotal += l.LineTotalCents
	}
	cad := toCAD(subtotal, po.FXRate)
	cred := po.WarrantyCreditAppliedCents
	net := po.NetTotalCents
	if net == 0 && cred == 0 && subtotal > 0 {
		net = subtotal
	} else if cred > 0 && (net == 0 || net > subtotal) {
		net = subtotal - cred
	}
	if net < 0 {
		net = 0
	}
	return s.costLensFor(ctx, po.BranchID).costs(func(c *POCosts) {
		c.Subtotal, c.SubtotalCad = &subtotal, &cad
		c.WarrantyCreditApplied, c.NetTotal = &cred, &net
	}), nil
}

// - the stamps -
//
// One stamp per read shape, each a single line of intent over costLens.costs. They are here rather
// than beside each shape so that every place money crosses the boundary is on one screen.

func (l costLens) stampPO(p *PO) {
	sub, cad := p.SubtotalCents, p.SubtotalCadCents
	cred := p.WarrantyCreditAppliedCents
	net := p.NetTotalCents
	if net == 0 && cred == 0 && sub > 0 {
		net = sub
	} else if cred > 0 && (net == 0 || net > sub) {
		net = sub - cred
	}
	if net < 0 {
		net = 0
	}
	p.NetTotalCents = net
	p.POCosts = l.costs(func(c *POCosts) {
		c.Subtotal, c.SubtotalCad = &sub, &cad
		c.WarrantyCreditApplied = &cred
		c.NetTotal = &net
	})
}

func (l costLens) stampPOLines(lines []POLine) {
	for i := range lines {
		unit, total, billed := lines[i].UnitCostCents, lines[i].LineTotalCents, lines[i].BilledCents
		lines[i].POCosts = l.costs(func(c *POCosts) {
			c.UnitCost, c.LineTotal, c.Billed = &unit, &total, &billed
		})
	}
}

func (l costLens) stampReceipts(rs []Receipt) {
	for i := range rs {
		l.stampReceipt(&rs[i])
	}
}

func (l costLens) stampReceipt(r *Receipt) {
	cad := r.ValueCadCents
	r.POCosts = l.costs(func(c *POCosts) { c.ValueCad = &cad })
	for j := range r.Lines {
		unit, val := r.Lines[j].UnitCostCents, r.Lines[j].ValueCents
		r.Lines[j].POCosts = l.costs(func(c *POCosts) { c.UnitCost, c.Value = &unit, &val })
	}
}

func (l costLens) stampWorklist(items []WorklistItem) {
	for i := range items {
		open := items[i].ValueOpenCents
		items[i].POCosts = l.costs(func(c *POCosts) { c.ValueOpen = &open })
	}
}

func (l costLens) stampIncoming(lines []IncomingPOLine) {
	for i := range lines {
		unit := lines[i].UnitCostCents
		lines[i].POCosts = l.costs(func(c *POCosts) { c.UnitCost = &unit })
	}
}
