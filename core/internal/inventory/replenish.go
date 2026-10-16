package inventory

// replenish.go, DIB-72: reorder points per (product, branch) and the shortfall worklist.
//
// WHAT THE WORKLIST GRADES. Not on-hand. The projected position:
//
//	projected = available + in_transit_in + on_order
//	short when projected < reorder_point
//
// Grading on on-hand alone produces the two classic bad worklists at once. It HIDES a shortfall
// behind stock that is already hard-held for a confirmed order (the yard looks fine and the pallets
// are spoken for), and it RE-ORDERS stock that is already on a truck or already on a purchase order
// (the buyer orders it twice and the yard drowns). Both mistakes are invisible until the invoice
// arrives, which is why the position, not the balance, is what the worklist reads.
//
// THE SUGGESTED QUANTITY closes the gap to the reorder point, floored at the standard replenishment
// quantity, then rounded UP to the order multiple when one is set:
//
//	suggested = max(reorder_qty, reorder_point - projected)  →  ceil to order_multiple
//
// Rounding up rather than to-nearest is not a style choice: rounding a 65-unit need down to a 60-unit
// skid leaves the branch short after the delivery arrives, which is the one outcome replenishment
// exists to prevent.
//
// THE CREATE-PO HANDOFF IS A CONSUMER-DECLARED SEAM. Purchasing is another swarm's fence this wave,
// so this module does not create purchase orders, it hands a fully-specified draft to a POCreator
// port whose default implementation records the intent and reports that no purchasing backend is
// wired. The exact signature is in REPORT-W4-B.md for the union to wire.

import (
	"context"
	"math"
	"strings"
)

// ShortfallRow is one line of the replenishment worklist.
type ShortfallRow struct {
	ProductID   string `json:"product_id"`
	SKU         string `json:"sku"`
	ProductName string `json:"product_name"`
	BranchID    string `json:"branch_id"`
	BranchCode  string `json:"branch_code"`

	// UOM is the product's BASE unit, the unit every quantity on this row is expressed in.
	// DIB-461 added it: this row's quantities are rendered on a product surface now, and the house
	// rule is that a quantity travels with its unit rather than being paired with one by a caller.
	UOM string `json:"uom"`

	OnHandBase    float64 `json:"on_hand_base"`
	ReservedBase  float64 `json:"reserved_base"`
	AvailableBase float64 `json:"available_base"`
	InTransitBase float64 `json:"in_transit_base"`
	OnOrderBase   float64 `json:"on_order_base"`
	// ProjectedBase = available + in_transit + on_order, the number graded against the point.
	ProjectedBase float64 `json:"projected_base"`

	ReorderPointBase  float64 `json:"reorder_point_base"`
	ReorderQtyBase    float64 `json:"reorder_qty_base"`
	OrderMultipleBase float64 `json:"order_multiple_base"`
	SuggestedQtyBase  float64 `json:"suggested_qty_base"`
	// ShortBase is how far under the point the projected position sits (always > 0 on a listed row).
	ShortBase float64 `json:"short_base"`

	PreferredVendorID   string `json:"preferred_vendor_id"`
	PreferredVendorName string `json:"preferred_vendor_name"`
	LeadTimeDays        int    `json:"lead_time_days"`
	AvgCostCents        int64  `json:"avg_cost_cents,omitempty"`
	// SuggestedValueCents is suggested_qty × the branch weighted-average, what saying yes costs.
	SuggestedValueCents int64 `json:"suggested_value_cents,omitempty"`

	// DraftPOID and DraftPONo point at the newest draft purchase order a buyer has started for this
	// product at this branch, unplaced. A draft never covers a shortfall, so the row is still here;
	// these let a screen say "drafted, not placed" and link the draft. Absent when there is none.
	DraftPOID string `json:"draft_po_id,omitempty"`
	DraftPONo string `json:"draft_po_no,omitempty"`

	// ShortOrderNos names, oldest first, the open sales orders waiting on this product at this
	// branch: the orders the shortfall holds up, at most three of them, the row keeping the order
	// module's oldest first. Absent when the orders seam is unbound or nothing waits; the count of
	// any rest rides in ShortOrderMore. See short_order_seam.go.
	ShortOrderNos []string `json:"short_order_nos,omitempty"`
	// ShortOrderMore is how many further open sales orders wait on this product beyond the three
	// ShortOrderNos names. Absent when there are none.
	ShortOrderMore int `json:"short_order_more,omitempty"`
}

// ReorderPoint is the per (product, branch) replenishment policy row.
type ReorderPoint struct {
	ProductID         string  `json:"product_id"`
	SKU               string  `json:"sku"`
	ProductName       string  `json:"product_name"`
	BranchID          string  `json:"branch_id"`
	ReorderPointBase  float64 `json:"reorder_point_base"`
	ReorderQtyBase    float64 `json:"reorder_qty_base"`
	OrderMultipleBase float64 `json:"order_multiple_base"`
	PreferredVendorID string  `json:"preferred_vendor_id"`
	LeadTimeDays      int     `json:"lead_time_days"`
	IsActive          bool    `json:"is_active"`
	Notes             string  `json:"notes"`
}

// Worklist is the shortfall rows plus the PROVENANCE of the on-order leg they were graded with.
//
// The flag rides with the rows rather than being a separate call, because the two facts are only
// useful together: "nothing is short" means one thing when on-order was measured and a weaker thing
// when it was not. DIB-470.
type Worklist struct {
	Rows []ShortfallRow `json:"items"`
	// OnOrderDegraded is true when no OnOrderReader is bound, i.e. every on_order_base on these rows
	// is a ZERO NOBODY MEASURED rather than a measured nothing. The surface says so; see the seam.
	OnOrderDegraded bool `json:"on_order_degraded"`
}

// - the arithmetic (pure, so it is testable and mutable on its own) -

// ProjectedPosition is what the worklist grades against the reorder point: what the branch can
// promise today plus what is already on the way.
func ProjectedPosition(availableBase, inTransitBase, onOrderBase float64) float64 {
	return availableBase + inTransitBase + onOrderBase
}

// Derive fills a row's computed legs from its components, available, projected, short, the
// suggested quantity and what saying yes costs.
//
// THE ONE GO DEFINITION, called by every read that produces a ShortfallRow (the worklist and
// DIB-461's single-line read). The SQL computes the same numbers so that its ORDER BY and LIMIT
// operate on the real ranking; this is what the caller actually gets, and
// TestIntegration_Shortfall_SQLAndGoAgree pins the two together.
//
// The suggested value is exact integer arithmetic on the DECIMAL(19,4) grain (HH-183): a suggested
// quantity beyond that grain (or a value beyond integer cents) returns an error rather than a
// rounded guess at what saying yes costs.
func (s *ShortfallRow) Derive() error {
	s.AvailableBase = AvailableBase(s.OnHandBase, s.ReservedBase)
	s.ProjectedBase = ProjectedPosition(s.AvailableBase, s.InTransitBase, s.OnOrderBase)
	s.ShortBase = s.ReorderPointBase - s.ProjectedBase
	s.SuggestedQtyBase = SuggestQty(s.ReorderPointBase, s.ProjectedBase, s.ReorderQtyBase, s.OrderMultipleBase)
	v, err := qtyTimesCents(s.SuggestedQtyBase, s.AvgCostCents)
	if err != nil {
		return err
	}
	s.SuggestedValueCents = v
	return nil
}

// SuggestQty computes the replenishment suggestion for a short line: enough to clear the reorder
// point, never less than the standard order quantity, rounded UP to the order multiple.
//
// multiple <= 0 means "no multiple", suggest the raw number.
func SuggestQty(reorderPointBase, projectedBase, reorderQtyBase, multipleBase float64) float64 {
	gap := reorderPointBase - projectedBase
	if gap < 0 {
		gap = 0
	}
	qty := gap
	if reorderQtyBase > qty {
		qty = reorderQtyBase
	}
	if multipleBase > 0 && qty > 0 {
		// Ceil in multiples. The epsilon guards the exact-multiple case: 120 units of a 60-multiple
		// must stay 120, and float division of a DECIMAL(19,4) round trip can land at 2.0000000001,
		// which would otherwise round up to three skids.
		qty = math.Ceil(qty/multipleBase-qtyEpsilon) * multipleBase
	}
	return qty
}

// - service -

// shortfallStore is the optional persistence seam for the worklist read, discovered by type
// assertion off the base store like every other optional read in this package, so a unit test can
// state what the worklist GRADES without reaching a database.
type shortfallStore interface {
	ShortfallWorklist(ctx context.Context, branchID string, reach BranchReach, limit int, onOrder []OnOrderQty) ([]ShortfallRow, error)
}

func (s *Service) shortfallStore() (shortfallStore, error) {
	st, ok := s.db().(shortfallStore)
	if !ok {
		return nil, ErrStockLookupNotConfigured
	}
	return st, nil
}

// ShortfallWorklist returns the replenishment lines whose projected position has fallen below their
// reorder point, worst first (deepest shortfall relative to the point).
//
// THE CALLER'S REACH IS THE FENCE (the Opus review's P1-1): branchID "" reads every branch the reach
// holds (all of them for a global caller, only the grants for a scoped one, nothing for a scoped
// caller with no grants), a named branch out of reach is a 403 in words, and the reach goes into the
// SQL as a branch set so the shortfall predicate, the ORDER BY and the LIMIT all run inside it. The
// draft pointers ride the same reach, so a row never carries another branch's draft PO.
//
// The ON-ORDER leg is read through the DIB-470 seam and handed to the query, rather than computed
// inside it: purchase orders are purchasing's rows. It goes IN to the SQL, instead of being merged
// onto the rows afterwards, because the shortfall predicate, the ORDER BY and the LIMIT all run in
// SQL, and a position that the ranking could not see would page and sort on the wrong number.
func (s *Service) ShortfallWorklist(ctx context.Context, reach BranchReach, branchID string, limit int) (Worklist, error) {
	branchID, err := fenceNamedBranch(reach, branchID)
	if err != nil {
		return Worklist{}, err
	}
	if reach.reachesNothing() {
		return Worklist{}, nil
	}
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	st, err := s.shortfallStore()
	if err != nil {
		return Worklist{}, err
	}
	// nil productIDs: the worklist grades every managed line, so it wants every product with
	// something outstanding.
	onOrder, degraded, err := s.onOrderFor(ctx, branchID, nil)
	if err != nil {
		return Worklist{}, err
	}
	rows, err := st.ShortfallWorklist(ctx, branchID, reach, limit, onOrder)
	if err != nil {
		return Worklist{}, err
	}
	if err := s.attachDrafts(ctx, reach, branchID, rows); err != nil {
		return Worklist{}, err
	}
	if err := s.attachShortOrders(ctx, reach, branchID, rows); err != nil {
		return Worklist{}, err
	}
	return Worklist{Rows: rows, OnOrderDegraded: degraded}, nil
}

// ListReorderPoints returns the managed lines at a branch.
func (s *Service) ListReorderPoints(ctx context.Context, branchID string, limit int) ([]ReorderPoint, error) {
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	return s.repo.ListReorderPoints(ctx, branchID, limit)
}

// SetReorderPoint upserts one managed line.
func (s *Service) SetReorderPoint(ctx context.Context, in ReorderPoint, actorID string) (ReorderPoint, error) {
	if in.ProductID == "" || in.BranchID == "" {
		return ReorderPoint{}, badReq("inventory: product_id and branch_id are required")
	}
	if in.ReorderPointBase < 0 || in.ReorderQtyBase < 0 {
		return ReorderPoint{}, badReq("inventory: reorder quantities must be >= 0")
	}
	if in.OrderMultipleBase < 0 {
		return ReorderPoint{}, badReq("inventory: order_multiple_base must be > 0 (or 0 for none)")
	}
	if in.LeadTimeDays < 0 {
		return ReorderPoint{}, badReq("inventory: lead_time_days must be >= 0")
	}
	return s.repo.UpsertReorderPoint(ctx, in, actorID)
}

// BootstrapReorderPoints opens a managed line for every stocked product at a branch that does not
// already have one, with the point at HALF of current on-hand and the multiple taken from the A-18
// full_skid_quantity where the catalogue knows it.
//
// This is where the demo seeding lives, and NOT in migration 0117, `cmd/migrate` runs before
// `cmd/seed`, so a migration doing this sees an empty stock ledger on every clean environment and
// silently creates almost nothing. Here it can see real data, it is re-runnable, and it never
// overwrites a line a human has tuned.
//
// Half is deliberate: nothing is short immediately after a bootstrap, so the worklist opens empty
// and the first row that appears is one somebody's action caused.
func (s *Service) BootstrapReorderPoints(ctx context.Context, branchID, actorID string) (int, error) {
	if branchID == "" {
		return 0, badReq("inventory: branch_id is required")
	}
	n, err := s.repo.BootstrapReorderPoints(ctx, branchID, actorID)
	if err != nil {
		return 0, err
	}
	s.log.Info("inventory: reorder points bootstrapped", "branch_id", branchID, "created", n)
	return n, nil
}

// - the CREATE-PO handoff (consumer-declared seam) -

// SuggestedPO is the draft this module hands to purchasing. Primitive-only by design so neither
// module imports the other's types.
type SuggestedPO struct {
	BranchID string            `json:"branch_id"`
	VendorID string            `json:"vendor_id"`
	Memo     string            `json:"memo"`
	Lines    []SuggestedPOLine `json:"lines"`
}

// SuggestedPOLine is one product+quantity on the draft.
type SuggestedPOLine struct {
	ProductID     string  `json:"product_id"`
	QtyBase       float64 `json:"qty_base"`
	UnitCostCents int64   `json:"unit_cost_cents"`
}

// POCreator is the CONSUMER-DECLARED SEAM for the create-PO handoff. Purchasing (Swarm C) implements
// it; a main.go adapter binds it. Returns the created PO id and number.
//
// FROZEN SIGNATURE, restated verbatim in REPORT-W4-B.md so the union can wire it without reading
// this file.
type POCreator interface {
	CreateDraftPO(ctx context.Context, in SuggestedPO) (poID string, poNo string, err error)
}

// WithPOCreator injects the purchasing seam.
func (s *Service) WithPOCreator(c POCreator) *Service { s.poCreator = c; return s }

// StubPOCreator is the default: it creates nothing and says so. Named "Stub" out loud, and returning
// an error rather than a fabricated id, because a silent success here would tell a buyer their
// purchase order exists when no purchasing backend is wired, and they would find out at the vendor.
type StubPOCreator struct{}

// CreateDraftPO reports that purchasing is not wired.
func (StubPOCreator) CreateDraftPO(context.Context, SuggestedPO) (string, string, error) {
	return "", "", clientError{
		status: 503,
		msg:    "inventory: purchasing is not wired, the suggested PO was not created (POCreator seam unbound)",
	}
}

// CreatePOFromShortfall builds a draft PO from the selected shortfall rows and hands it to the
// purchasing seam. Rows are grouped by (branch, vendor), one PO per vendor per branch, which is
// what a buyer actually sends, and a row with no preferred vendor is REFUSED rather than silently
// dropped or lumped onto someone else's order.
func (s *Service) CreatePOFromShortfall(ctx context.Context, reach BranchReach, branchID string, productIDs []string) (SuggestedPO, string, string, error) {
	if strings.TrimSpace(branchID) == "" {
		return SuggestedPO{}, "", "", badReq("inventory: branch_id is required")
	}
	// The reach is checked BEFORE the worklist is read, so the answer for a branch out of reach never
	// depends on that branch's stock (a 400 "none of the selected products is short" against a 403).
	branchID, err := fenceNamedBranch(reach, branchID)
	if err != nil {
		return SuggestedPO{}, "", "", err
	}
	if len(productIDs) == 0 {
		return SuggestedPO{}, "", "", badReq("inventory: select at least one shortfall line")
	}
	// The SAME read the screen showed, seam and all, so the quantity a buyer confirms is the
	// quantity they were looking at, and a product that is only short because on-order is missing
	// is short in both places or in neither.
	wl, err := s.ShortfallWorklist(ctx, reach, branchID, 500)
	if err != nil {
		return SuggestedPO{}, "", "", err
	}
	rows := wl.Rows
	wanted := make(map[string]bool, len(productIDs))
	for _, id := range productIDs {
		wanted[id] = true
	}
	var draft SuggestedPO
	draft.BranchID = branchID
	for _, r := range rows {
		if !wanted[r.ProductID] {
			continue
		}
		if r.PreferredVendorID == "" {
			return SuggestedPO{}, "", "", badReq(
				"inventory: %s has no preferred vendor, set one before creating a purchase order", r.SKU)
		}
		if draft.VendorID == "" {
			draft.VendorID = r.PreferredVendorID
		} else if draft.VendorID != r.PreferredVendorID {
			return SuggestedPO{}, "", "", badReq(
				"inventory: selection spans more than one vendor, create one purchase order per vendor")
		}
		draft.Lines = append(draft.Lines, SuggestedPOLine{
			ProductID: r.ProductID, QtyBase: r.SuggestedQtyBase, UnitCostCents: r.AvgCostCents,
		})
	}
	if len(draft.Lines) == 0 {
		return SuggestedPO{}, "", "", badReq("inventory: none of the selected products is currently short")
	}
	draft.Memo = "replenishment from shortfall worklist"

	creator := s.poCreator
	if creator == nil {
		creator = StubPOCreator{}
	}
	poID, poNo, err := creator.CreateDraftPO(ctx, draft)
	if err != nil {
		// The draft is returned alongside the error on purpose: the surface can show the buyer
		// exactly what WOULD have been ordered even when purchasing is not wired, which is the
		// difference between a useful stub and a dead button.
		return draft, "", "", err
	}
	return draft, poID, poNo, nil
}
