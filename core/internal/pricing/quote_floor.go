package pricing

// quote_floor.go, the quote module's floor seam (plan 3.3 and 3.5): one
// evaluation owned by pricing, the SAME gate and the SAME floor source the
// resolver uses, offered to the quote module for the two writes that put a
// line below its rule price (the discount arm and the quantity reprice of a
// discounted line). The till and the generic resolve route never reach this
// seam: it answers a question only the quote write paths ask, and nothing
// here runs inside resolve, so their behaviour cannot change.
//
// WHAT THE SEAM OWNS AND WHAT IT DOES NOT. The seam owns the arithmetic and
// the floor source: the margin at the candidate price against the effective
// per category floor (the category policy row the resolver reads, the
// product policy's enforcement switch, the same MarginGate). It does NOT
// own the authority: who may price below the floor (the clearing lists and
// the approval rail) is the quote module's ruled ladder over the role
// configuration and the approvals substrate, decided by the caller the
// guard resolved, never by anything here.

import (
	"context"
	"fmt"
)

// FloorQuestion is the quote module's question about one line write: may
// this line sit at CandidateUnitCents, per PriceUOM? The rule price is the
// line's stored one (what the resolver struck at the last add or reprice),
// NOT re-resolved: the write discounts the STORED gross, so the decision
// must be made against the same figure the write will apply the discount to.
type FloorQuestion struct {
	CustomerID   string
	CustomerType string
	ProductID    string
	BranchID     string
	QtyEntered   float64
	UOMEntered   string
	CostCents    int64 // the line's cost basis, per the base unit, as the resolver takes it

	RuleUnitCents      int64  // the stored rule unit price, per PriceUOM
	CandidateUnitCents int64  // the unit price the write would set, per PriceUOM
	PriceUOM           string // the line's price uom, the unit both figures are per
}

// FloorAnswer is the seam's verdict on one FloorQuestion.
type FloorAnswer struct {
	// MarginBps is the margin at the CANDIDATE price (the price the write
	// would set), the figure the clearing stamp and the refusal carry.
	MarginBps int64
	// FloorBps is the effective per category floor, the same source the
	// resolver's gate reads.
	FloorBps int64
	// CostCents is the cost in the line's price uom, the figure the margin
	// was struck over, for the clearing stamp.
	CostCents int64
	// CategoryID is the category the pricing log row's entity_id names; the
	// product id when no catalog seam is wired.
	CategoryID string
	// BelowRule: the candidate sits below the rule price (a discount).
	BelowRule bool
	// BelowFloor: the margin at the candidate is under the floor. False
	// when enforcement is disabled for the category or product (the gate
	// does not run, exactly as in a live resolve).
	BelowFloor bool
	// EnforcementDisabled mirrors the resolver's own bypass state, so the
	// caller can tell "no floor question" from "answered, above the floor".
	EnforcementDisabled bool
}

// EvaluateLineFloor answers one FloorQuestion. It reads the category and
// product policies and the cost the resolver's own gate would read, and it
// runs the same MarginGate, but it resolves no price, writes no audit row
// and consults no override authority: those stay where they already live.
func (s *Service) EvaluateLineFloor(ctx context.Context, in FloorQuestion) (FloorAnswer, error) {
	if in.PriceUOM == "" {
		return FloorAnswer{}, badReq("price_uom is required")
	}
	// The same conversion the resolver applies (the shared uomFactorFor
	// helper, one code path), so the cost the margin is struck over is
	// denominated exactly as the priced line's was.
	fRes, err := s.uomFactorFor(ctx, in.ProductID, in.PriceUOM)
	if err != nil {
		return FloorAnswer{}, err
	}
	if fRes == 0 {
		return FloorAnswer{}, errNoConversion
	}
	costInResUOM := roundCents(float64(in.CostCents) * fRes)

	// The floor source: the category policy row the resolver reads, with
	// the same unseeded-environment fallback.
	policy, err := s.store.CategoryPolicy(ctx, "")
	if err != nil {
		return FloorAnswer{}, fmt.Errorf("category policy: %w", err)
	}
	prodPolicy, err := s.store.ProductPolicy(ctx, in.ProductID)
	if err != nil {
		return FloorAnswer{}, fmt.Errorf("product policy: %w", err)
	}
	floorBps := defaultFloorBps
	if policy != nil {
		floorBps = effectiveFloor(*policy)
	}

	// The category the row's entity_id names (the honoured override's
	// shape): resolved through the catalog seam when one is wired, else the
	// product, so the NOT NULL column always holds the honest target.
	categoryID := ""
	if s.cat != nil {
		if cid, err := s.cat.GetCategory(ctx, in.ProductID); err == nil {
			categoryID = cid
		}
	}
	ans := FloorAnswer{
		FloorBps:   floorBps,
		CostCents:  costInResUOM,
		CategoryID: categoryID,
		BelowRule:  in.CandidateUnitCents < in.RuleUnitCents,
		MarginBps:  marginBps(in.CandidateUnitCents, costInResUOM),
	}
	if (policy != nil && policy.EnforcementDisabled) || (prodPolicy != nil && prodPolicy.EnforcementDisabled) {
		ans.EnforcementDisabled = true
		return ans, nil
	}
	if s.gate != nil {
		if blk := s.gate.Check(in.CandidateUnitCents, costInResUOM, in.BranchID, false, floorBps); blk != nil {
			ans.BelowFloor = true
		}
	}
	return ans, nil
}

// FloorRecord is one pricing log row for a quote line floor decision: a
// BYPASS row when a clearing or an approval let a below floor price through
// (the honoured override's shape), an UPDATE row for a below rule above
// floor price (the row QD-2 completes). One call site, RecordFloorDecision,
// writes both.
type FloorRecord struct {
	UserID     string
	QuoteID    string
	LineNo     int
	ProductID  string
	CategoryID string

	RuleUnitCents int64 // the rule price the discount was struck from
	NetUnitCents  int64 // the price the write set
	DiscPct       int
	MarginBps     int64
	FloorBps      int64

	Action       string // "BYPASS" or "UPDATE"
	BypassReason string // for BYPASS rows: APPROVED_OVERRIDE or MANAGER_SCOPE_OVERRIDE
}

// RecordFloorDecision writes one FloorRecord to pricing_audit_log, the same
// best effort append the resolver's own bypass rows ride (an audit blip
// never fails the quote write; the quote's own change trail is the
// transactional record).
// entityIDOf names the row's entity: the category when the answer carried
// one, else the product. entity_id is NOT NULL, so the fallback keeps the
// row rather than losing it to a discarded INSERT error.
func entityIDOf(rec FloorRecord) string {
	if rec.CategoryID != "" {
		return rec.CategoryID
	}
	return rec.ProductID
}

func (s *Service) RecordFloorDecision(ctx context.Context, rec FloorRecord) {
	if rec.Action != "BYPASS" && rec.Action != "UPDATE" {
		return
	}
	if rec.Action == "BYPASS" && !validBypassReasons[rec.BypassReason] {
		// An unknown reason would be rejected by the 0114 CHECK and the
		// INSERT silently lost; refusing to write is louder than a phantom
		// row, so the bad reason surfaces in the caller's tests.
		return
	}
	_ = s.store.AppendAudit(ctx, AuditEntry{
		EntityType: "margin_enforcement",
		// entity_id is NOT NULL: the record's own fallback keeps the row.
		EntityID:      entityIDOf(rec),
		Action:        rec.Action,
		FieldName:     fmt.Sprintf("line_%d", rec.LineNo),
		OldValue:      fmt.Sprintf("rule %d at %d%%", rec.RuleUnitCents, rec.DiscPct),
		NewValue:      fmt.Sprintf("net %d margin %d floor %d", rec.NetUnitCents, rec.MarginBps, rec.FloorBps),
		UserID:        rec.UserID,
		RefEntityType: RefQuote,
		RefEntityID:   rec.QuoteID,
		BypassReason:  rec.BypassReason,
	})
}
