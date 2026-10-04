package quote

// floor.go, the floor decision of plan 3.3 at the two write arms that put a
// line below its rule price (defects D2, D5, D6) and the request door of
// plan 1.1. Three cases:
//
//	at the rule price           the decision is not asked; a rule price
//	                             below the floor is accepted, marked on the
//	                             read and logged (CP0-3 answer A,
//	                             rulePriceLogRows)
//	below rule, above floor     accepted, no manager; one UPDATE log row
//	                             (floorLogRows) and the change trail
//	below floor                 refused with a typed 409 unless (a) the
//	                             caller clears the floor itself
//	                             (floor_clearers_own_branch, in its own
//	                             branch, under the manager_own_branch_floor
//	                             _clearing setting), the clearing stamped on
//	                             the line, or (b) an approved and unused
//	                             request exists for exactly that quote, that
//	                             line, no deeper a discount than was asked,
//	                             consumed by that write on its own
//	                             transaction
//
// A later write that keeps the discount no deeper and the margin no lower
// needs no new clearing: the stamped clearing on the line stands.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pricing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/settings"
	"github.com/jackc/pgx/v5"
)

// FloorArbiter is the pricing owned floor decision seam (plan 3.5): the
// same gate and floor source the resolver uses, asked only by the two
// writes below. Satisfied by *pricing.Service. Nil fails closed.
type FloorArbiter interface {
	EvaluateLineFloor(ctx context.Context, in pricing.FloorQuestion) (pricing.FloorAnswer, error)
	// RecordFloorDecisionTx writes the pricing log row a finished write owes
	// ON that write's own transaction (plan 4.2): the row commits with the
	// line it describes or not at all, and a failed append refuses the write.
	RecordFloorDecisionTx(ctx context.Context, tx pgx.Tx, rec pricing.FloorRecord) error
}

// FloorApprovalRail is the identity approvals rail as the quote module
// needs it (plan 3.5: the quote module files the request; identity stores
// it, decides it, and consumes it on the quote's own transaction).
// Satisfied by *identity.Approvals. Nil fails closed.
type FloorApprovalRail interface {
	FileFloorApproval(ctx context.Context, in identity.FloorApprovalFiling) (identity.FloorRequestRecord, error)
	FloorRequestFor(ctx context.Context, requestedBy, branchID, subjectRef string, lineNo int, lineID, productID string) (identity.FloorRequestRecord, bool, error)
	ConsumeApprovedFloorTx(ctx context.Context, tx pgx.Tx, requestedBy, branchID, subjectRef string, lineNo, discPct int, lineID, productID string) (identity.FloorRequestRecord, identity.FloorConsumption, error)
}

// floorStore is the line state the floor decision reads (type-asserted off
// the store, the builderStore pattern, so a store that predates the floor
// surface degrades to the not-configured refusal rather than breaking).
type floorStore interface {
	// LineFloorState reads one line's floor facts: the line's OWN row id
	// (the approval's binding; a line number is reused by the next line
	// added after a delete, the row id never is), the stored discount, the
	// rule unit price (the discount is struck from the stored gross), the
	// pre-discount gross total, the price uom, and the line's stamped
	// clearing (nil when none).
	LineFloorState(ctx context.Context, quoteID string, lineNo int) (lineID string, discPct int, unitPriceCents, grossCents int64, priceUOM string, clearing *floorClearingStamp, err error)
}

func (s *StaffService) floorStore() (floorStore, bool) {
	f, ok := s.store.(floorStore)
	return f, ok
}

// floorClearingStamp is the clearing stamped on the line's metadata column:
// who cleared it, by which authority, at what discount and margin (plan
// 3.3). The freeze holds: metadata is a JSONB column that has always been
// there.
type floorClearingStamp struct {
	By         string `json:"cleared_by"`
	Authority  string `json:"authority"` // MANAGER_SCOPE | APPROVAL
	DiscPct    int    `json:"disc_pct"`
	MarginBps  int64  `json:"margin_bps"`
	ApprovalID string `json:"approval_id,omitempty"`
}

// The clearing authorities, stamped on the line and named in the BYPASS row
// (the pricing log's reason vocabulary: MANAGER_SCOPE_OVERRIDE and
// APPROVED_OVERRIDE, migration 0133).
const (
	floorAuthorityManagerScope = "MANAGER_SCOPE"
	floorAuthorityApproval     = "APPROVAL"
)

// floorWritePlan is what a guarded write does with the floor decision: the
// pre-state the decision was priced from (a mismatch under the lock refuses
// the write rather than discounting figures nobody decided on), the
// approval consumption that rides the write's own transaction, and the
// clearing stamp the write sets on the line. A zero plan is a write the
// floor question was never asked of.
type floorWritePlan struct {
	// expectedGross, when non-zero, is the pre-discount gross the decision
	// priced (the discount arm).
	expectedGross int64
	// expectedDisc, when non-negative-and-asked, is the stored discount the
	// decision repriced (the quantity arm). -1 = not asked.
	expectedDisc int
	// consume runs INSIDE the write's transaction, before the mutation: it
	// uses the approved request up, on the same commit as the line it
	// cleared. found=false refuses the write (the typed refusal).
	consume func(ctx context.Context, tx pgx.Tx) (identity.FloorConsumption, bool, error)
	// clearing is stamped into the line's metadata; nil stamps nothing.
	clearing *floorClearingStamp
	// refusal is the typed refusal the decision built. The consume arm
	// answers it when the request is gone under the lock (the race
	// loser), so a loser is told who may approve, not only that it lost.
	refusal *FloorRefusalError
	// asked marks a below-rule write the decision was consulted on.
	asked bool
}

// FloorRequestState is a filed floor request's state, carried on the typed
// refusal and the request door's answer.
type FloorRequestState struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	LineNo  int    `json:"line_no"`
	DiscPct int    `json:"disc_pct"`
}

// FloorRefusalError is the typed 409 the floor decision answers when a
// write would put a line below the margin floor and the caller holds no
// clearing and no approved request for it. The body carries the policy, who
// may approve (the General Manager, the Financial Controller, and the
// branch's Sales manager, CP0-1 answer B), whether a manager of the branch
// may set it, the caller's pending or approved request if any, and the
// margin and floor figures for callers on the cost list only.
type FloorRefusalError struct {
	Policy         string             `json:"policy"`
	Approvers      []string           `json:"approver_roles"`
	ManagerMaySet  bool               `json:"manager_may_set"`
	Request        *FloorRequestState `json:"existing_request,omitempty"`
	MarginBps      int64              `json:"margin_bps,omitempty"`
	FloorBps       int64              `json:"floor_bps,omitempty"`
	ShowFigures    bool               `json:"-"`
	clearedFigures bool
}

func (e *FloorRefusalError) Error() string {
	msg := "quote: this discount puts the line below the margin floor, ask the General Manager or the Financial Controller to approve it, or the branch's Sales manager"
	if e.ManagerMaySet {
		msg = "quote: this discount puts the line below the margin floor, ask the General Manager, the Financial Controller or the branch's Sales manager to approve it, or have the branch's Sales manager set it"
	}
	if e.Request != nil {
		return fmt.Sprintf("%s; your request for this line is %s", msg, e.Request.Status)
	}
	return msg
}

// NothingToApproveError is the request door's 409: the asked discount does
// not land below the floor, so there is nothing to approve.
type NothingToApproveError struct{ DiscPct int }

func (e *NothingToApproveError) Error() string {
	return fmt.Sprintf(
		"quote: a %d percent discount does not land below the margin floor, there is nothing to approve", e.DiscPct)
}

// - who clears, who approves -

// canonicalRole maps an ERP role code onto the configuration's vocabulary,
// "" when unmapped.
func canonicalRole(erpCode string) string {
	id, ok := identity.CanonicalForERPCode(erpCode)
	if !ok {
		return ""
	}
	return id
}

// callerClearsOwnBranchFloor answers whether the caller clears the floor on
// its own price in this branch (X5): the configuration's
// floor_clearers_own_branch list, the manager_own_branch_floor_clearing
// setting folded in, exactly the reader the rules call.
func (s *StaffService) callerClearsOwnBranchFloor(c identity.StaffContext, branchID string) bool {
	if s.roles == nil {
		return false
	}
	role := canonicalRole(c.RoleCode)
	if role == "" {
		return false
	}
	// "Its own branch": the branch reach the edit ladder already proved.
	if !c.CanAccessBranch(branchID) {
		return false
	}
	clears, err := s.roles.ClearsOwnBranchFloor(role)
	if err != nil {
		return false
	}
	return clears
}

// floorApprovers answers who may approve a below floor price, as ERP role
// codes: the configuration's floor list plus the branch scoped quote floor
// approvers (CP0-1 answer B), never a code constant.
func (s *StaffService) floorApprovers() []string {
	if s.roles == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	add := func(canonical string) {
		if canonical == "" || seen[canonical] {
			return
		}
		seen[canonical] = true
		if erp, ok := identity.ERPCodeFor(canonical); ok {
			out = append(out, erp)
		}
	}
	if wide, err := s.roles.ExceptionApprovers(roleconfig.ExceptionFloorQuote); err == nil {
		for _, r := range wide {
			add(r)
		}
	}
	for _, r := range s.roles.QuoteFloorBranchApprovers() {
		add(r)
	}
	return out
}

// seesCostAndMargin answers whether the caller is on the configuration's
// cost and margin list (X3), the same reader the glance uses.
func (s *StaffService) seesCostAndMargin(c identity.StaffContext) bool {
	if s.roles == nil {
		return false
	}
	role := canonicalRole(c.RoleCode)
	if role == "" {
		return false
	}
	ok, err := s.roles.SeesCostAndMargin(role)
	return err == nil && ok
}

// - the decision -

// discountUnitCents applies a discount percent to a unit price, half-up
// once, the same arithmetic the guarded write applies to the gross.
func discountUnitCents(unitCents int64, discPct int) int64 {
	return (unitCents*int64(100-discPct) + 50) / 100
}

// floorOutcome is the decision's verdict for one write.
type floorOutcome struct {
	asked      bool
	belowFloor bool
	answer     pricing.FloorAnswer
	plan       floorWritePlan
	refusal    *FloorRefusalError
	approved   bool // the write rides an approved request (for the BYPASS reason)
	// ruleUnitCents and netUnitCents are the figures the write struck, for
	// the pricing log row.
	ruleUnitCents int64
	netUnitCents  int64
}

// decideLineFloor runs the ruled ladder for one below-rule write. q carries
// the line's figures (rule unit price, candidate unit price, price uom,
// cost); clearing is the line's stamped clearing (nil when none); discPct
// is the discount the write would set. lineID is the line's OWN row id: it
// binds any approval to this row, not to a number the next line added
// after a delete can reuse. The caller has already passed the edit ladder
// (own or Manage) and the branch reach.
func (s *StaffService) decideLineFloor(ctx context.Context, caller identity.StaffContext,
	quoteID, branchID, productID string, lineNo, discPct int, lineID, uomEntered string,
	ruleUnitCents, grossCents int64, priceUOM string, clearing *floorClearingStamp,
) (*floorOutcome, error) {
	out := &floorOutcome{plan: floorWritePlan{expectedDisc: -1}}

	// At the rule price the decision is not asked (a zero discount IS the
	// rule price; a negative one cannot reach here).
	if discPct <= 0 {
		return out, nil
	}
	if s.floor == nil {
		// Fail closed: a below rule write with no arbiter wired is refused,
		// never waved through (the priceLine nil-seam posture).
		return nil, ErrEditNotConfigured
	}
	fs, ok := s.floorStore()
	if !ok {
		return nil, ErrEditNotConfigured
	}
	_ = fs

	qty, uom, err := s.lineQtyUom(ctx, quoteID, lineNo)
	if err != nil {
		return nil, err
	}
	customerID, _, err := s.store.QuotePricingContext(ctx, quoteID)
	if err != nil {
		return nil, err
	}
	custType, err := s.custType.CustomerType(ctx, customerID)
	if err != nil {
		return nil, fmt.Errorf("resolve customer pricing tier: %w", err)
	}
	cost, _, err := s.inv.CurrentCostCents(ctx, productID, branchID)
	if err != nil {
		return nil, fmt.Errorf("cost basis: %w", err)
	}

	answer, err := s.floor.EvaluateLineFloor(ctx, pricing.FloorQuestion{
		CustomerID: customerID, CustomerType: custType,
		ProductID: productID, BranchID: branchID,
		QtyEntered: qty, UOMEntered: uom, CostCents: cost,
		RuleUnitCents: ruleUnitCents, CandidateUnitCents: discountUnitCents(ruleUnitCents, discPct),
		PriceUOM: priceUOM,
	})
	if err != nil {
		return nil, err
	}
	out.answer = answer
	out.ruleUnitCents = ruleUnitCents
	out.netUnitCents = discountUnitCents(ruleUnitCents, discPct)
	if !answer.BelowRule {
		// The arbiter says the candidate is not below the rule price
		// (the caller's discount is no discount); nothing is asked.
		return out, nil
	}
	out.asked = true
	out.plan.asked = true
	out.plan.expectedGross = grossCents
	out.plan.expectedDisc = discPct

	if !answer.BelowFloor || answer.EnforcementDisabled {
		// Below the rule price, above the floor (or no floor question):
		// accepted, no manager. The UPDATE log row rides the one hook
		// floorLogRows builds for the guarded write.
		return out, nil
	}
	out.belowFloor = true

	// (a) a prior clearing on the line stands for a write no deeper and no
	// lower: no new clearing is asked (plan 3.3).
	if clearing != nil && clearing.DiscPct >= discPct && answer.MarginBps >= clearing.MarginBps {
		return out, nil
	}

	// (b) the caller clears the floor itself, in its own branch, under the
	// dealer setting: the clearing is stamped on the line.
	if s.callerClearsOwnBranchFloor(caller, branchID) {
		out.plan.clearing = &floorClearingStamp{
			By: caller.UserID, Authority: floorAuthorityManagerScope,
			DiscPct: discPct, MarginBps: answer.MarginBps,
		}
		return out, nil
	}

	// (c) an approved and unused request for exactly that quote, that
	// line's own row, no deeper a discount than was asked, consumed by this
	// write on its own transaction. Anything short of it is the typed
	// refusal, carrying the caller's pending or approved request if one
	// stands.
	refusal := &FloorRefusalError{
		Policy:        string(identity.PolicyMargin),
		Approvers:     s.floorApprovers(),
		ManagerMaySet: s.managerMaySet(),
	}
	if s.seesCostAndMargin(caller) {
		refusal.ShowFigures = true
		refusal.MarginBps = answer.MarginBps
		refusal.FloorBps = answer.FloorBps
	}
	// The refusal rides the plan from the moment it is built: a writer
	// whose consume loses the race under the lock answers THIS refusal,
	// approvers and all, not a bare policy word.
	out.plan.refusal = refusal
	if s.floorRail != nil {
		if rec, ok, rerr := s.floorRail.FloorRequestFor(ctx, caller.UserID, branchID, quoteID, lineNo, lineID, productID); rerr == nil && ok {
			st := &FloorRequestState{ID: rec.ID, Status: string(rec.Status), LineNo: rec.LineNo, DiscPct: rec.DiscPct}
			if rec.Status == identity.ApprovalApproved && rec.DiscPct >= discPct {
				// The request covers this write: consume it on the write's
				// own transaction, stamping the line with the APPROVAL
				// authority.
				out.approved = true
				rail, quoteID_, branchID_, lineNo_, lineID_, productID_, discPct_ :=
					s.floorRail, quoteID, branchID, lineNo, lineID, productID, discPct
				requester := caller.UserID
				out.plan.consume = func(ctx context.Context, tx pgx.Tx) (identity.FloorConsumption, bool, error) {
					_, c, err := rail.ConsumeApprovedFloorTx(ctx, tx, requester, branchID_, quoteID_, lineNo_, discPct_, lineID_, productID_)
					return c, c.ApprovalID != "", err
				}
				out.plan.clearing = &floorClearingStamp{
					By: caller.UserID, Authority: floorAuthorityApproval,
					DiscPct: discPct, MarginBps: answer.MarginBps,
				}
				return out, nil
			}
			refusal.Request = st
		}
	}
	out.refusal = refusal
	return out, nil
}

// managerMaySet answers the refusal's "may a manager of the branch set it":
// the dealer setting on and at least one floor clearer configured.
func (s *StaffService) managerMaySet() bool {
	if s.roles == nil {
		return false
	}
	return s.roles.ManagerOwnBranchFloorClearing() && len(s.roles.FloorClearersOwnBranch()) > 0
}

// lineQtyUom reads one line's entered quantity and uom for the arbiter's
// question.
func (s *StaffService) lineQtyUom(ctx context.Context, quoteID string, lineNo int) (float64, string, error) {
	snap, uom, err := s.store.LineForEdit(ctx, quoteID, lineNo)
	if err != nil {
		return 0, "", err
	}
	qty, err := parseQuoteQty(snap.QtyEntered)
	if err != nil {
		return 0, "", err
	}
	return qty, uom, nil
}

// floorLogRows returns the pricing log rows a finished floor write owes: the
// BYPASS row for a cleared below floor price (the honoured override's
// shape), and the UPDATE row for a below rule above floor write (plan 4.2).
// THE single builder for both; the rows ride the write's own transaction
// through pricingLogHook.
func floorLogRows(caller identity.StaffContext, quoteID, productID string, discPct int, out *floorOutcome) []pricing.FloorRecord {
	if out == nil || !out.asked {
		return nil
	}
	if out.belowFloor && out.plan.clearing != nil {
		reason := pricing.BypassManagerScope
		if out.approved {
			reason = pricing.BypassApprovedOverr
		}
		return []pricing.FloorRecord{{
			UserID: caller.UserID, QuoteID: quoteID,
			ProductID:     productID,
			CategoryID:    out.answer.CategoryID,
			RuleUnitCents: out.ruleUnitCents,
			NetUnitCents:  out.netUnitCents,
			DiscPct:       discPct,
			MarginBps:     out.answer.MarginBps,
			FloorBps:      out.answer.FloorBps,
			Action:        "BYPASS", BypassReason: reason,
		}}
	}
	// The below rule above floor UPDATE row (plan 4.2): accepted, no
	// manager, logged for the pricing editors through the same seam. A
	// write that rode a standing clearing (below floor, no new stamp) and
	// a write on a category with the floor question switched off write no
	// row here: the first already has its BYPASS row from the clearing
	// that stamped it, the second was never asked a floor question.
	if !out.belowFloor && !out.answer.EnforcementDisabled {
		return []pricing.FloorRecord{{
			UserID: caller.UserID, QuoteID: quoteID,
			ProductID:     productID,
			CategoryID:    out.answer.CategoryID,
			RuleUnitCents: out.ruleUnitCents,
			NetUnitCents:  out.netUnitCents,
			DiscPct:       discPct,
			MarginBps:     out.answer.MarginBps,
			FloorBps:      out.answer.FloorBps,
			Action:        "UPDATE",
		}}
	}
	return nil
}

// rulePriceLogRows returns the pricing log row a finished write owes when it
// accepts a line at a rule price that sits below the floor (CP0-3 answer
// A): accepted with no manager, logged for the pricing editors so a rule
// that sells under the floor is seen. bd is the resolver's own answer, so
// the row carries the same figures the resolve struck; the rule and the net
// are the same price (no discount was asked), and the row names the product
// when no category rode the breakdown, the FloorRecord fallback that keeps
// the NOT NULL entity id honest.
func rulePriceLogRows(caller identity.StaffContext, quoteID, productID string, bd pricing.Breakdown) []pricing.FloorRecord {
	if !bd.Blocked {
		return nil
	}
	return []pricing.FloorRecord{{
		UserID: caller.UserID, QuoteID: quoteID,
		ProductID:     productID,
		RuleUnitCents: bd.FinalCents,
		NetUnitCents:  bd.FinalCents,
		DiscPct:       0,
		MarginBps:     bd.MarginBps,
		FloorBps:      bd.FloorBps,
		Action:        "UPDATE",
	}}
}

// pricingLogHook chains the pricing log rows a guarded write owes into the
// hook the write runs on its own transaction (plan 4.2): each row commits
// with the line it describes or not at all, and a failed append refuses the
// write rather than being swallowed after the commit. lineNo is fed by the
// write itself: the add learns its line number only inside the lock, so the
// rows are built without one and named here.
func (s *StaffService) pricingLogHook(rows []pricing.FloorRecord) func(context.Context, pgx.Tx, int) error {
	if s.floor == nil || len(rows) == 0 {
		return nil
	}
	return func(ctx context.Context, tx pgx.Tx, lineNo int) error {
		for _, rec := range rows {
			rec.LineNo = lineNo
			if err := s.floor.RecordFloorDecisionTx(ctx, tx, rec); err != nil {
				return err
			}
		}
		return nil
	}
}

// - the note bound -

// maxFloorApprovalNote bounds the request's free text note.
const maxFloorApprovalNote = 500

// - the request door (plan 1.1) -

// FloorApprovalAnswer is the request door's answer: the filed (or already
// standing) request's state, or the word that the caller may clear the
// floor itself and no request is filed.
type FloorApprovalAnswer struct {
	Filed          bool               `json:"filed"`
	MayClearItself bool               `json:"may_clear_itself"`
	Request        *FloorRequestState `json:"request,omitempty"`
}

// RequestFloorApproval files one approval request bound to that quote, that
// line and that discount, for a line the caller may edit whose asked
// discount lands below the floor. The server builds the subject and the
// detail from its own figures (quote number, line, product, discount,
// resulting margin, floor); the caller supplies neither.
func (s *StaffService) RequestFloorApproval(ctx context.Context, quoteID string, lineNo, discPct int, note string) (FloorApprovalAnswer, error) {
	caller, rule, quoteID, err := s.authorizeQuoteEdit(ctx, quoteID, FieldQuoteLineQty)
	if err != nil {
		return FloorApprovalAnswer{}, err
	}
	if discPct < 0 || discPct > 100 {
		return FloorApprovalAnswer{}, settings.Errf(http.StatusUnprocessableEntity,
			"quote: disc_pct must be between 0 and 100")
	}
	if len(note) > maxFloorApprovalNote {
		return FloorApprovalAnswer{}, settings.Errf(http.StatusUnprocessableEntity,
			"quote: note exceeds the %d character maximum", maxFloorApprovalNote)
	}
	fs, ok := s.floorStore()
	if !ok || s.floor == nil {
		return FloorApprovalAnswer{}, ErrEditNotConfigured
	}
	lineID, disc, ruleUnit, _, priceUOM, clearing, err := fs.LineFloorState(ctx, quoteID, lineNo)
	if err != nil {
		return FloorApprovalAnswer{}, err
	}
	_ = clearing
	_, branchID, _, _, err := s.store.QuoteEditHeader(ctx, quoteID)
	if err != nil {
		return FloorApprovalAnswer{}, err
	}
	snap, _, err := s.store.LineForEdit(ctx, quoteID, lineNo)
	if err != nil {
		return FloorApprovalAnswer{}, err
	}

	out, err := s.decideLineFloor(ctx, caller, quoteID, branchID, snap.ProductID, lineNo, discPct, lineID, "",
		ruleUnit, 0, priceUOM, nil)
	if err != nil {
		return FloorApprovalAnswer{}, err
	}
	if !out.belowFloor {
		return FloorApprovalAnswer{}, &NothingToApproveError{DiscPct: discPct}
	}
	// A caller who may clear the floor itself is told so, no request filed.
	if s.callerClearsOwnBranchFloor(caller, branchID) {
		return FloorApprovalAnswer{MayClearItself: true}, nil
	}
	if s.floorRail == nil {
		return FloorApprovalAnswer{}, ErrEditNotConfigured
	}
	// An open request for this line already stands: answer its state rather
	// than filing a twin.
	if rec, ok, rerr := s.floorRail.FloorRequestFor(ctx, caller.UserID, branchID, quoteID, lineNo, lineID, snap.ProductID); rerr == nil && ok {
		return FloorApprovalAnswer{Request: &FloorRequestState{
			ID: rec.ID, Status: string(rec.Status), LineNo: rec.LineNo, DiscPct: rec.DiscPct,
		}}, nil
	}

	// The detail the approver reads, built from the server's own figures.
	view, err := s.store.StaffDetail(ctx, quoteID)
	if err != nil {
		return FloorApprovalAnswer{}, err
	}
	detail := fmt.Sprintf(
		"quote %s line %d (%s): %d percent discount, resulting margin %d bps against the %d bps floor",
		view.QuoteNo, lineNo, snap.ProductID, discPct, out.answer.MarginBps, out.answer.FloorBps)
	rec, err := s.floorRail.FileFloorApproval(ctx, identity.FloorApprovalFiling{
		RequestedBy: caller.UserID, BranchID: branchID, SubjectRef: quoteID,
		Detail: detail, LineNo: lineNo, DiscPct: discPct, LineID: lineID, ProductID: snap.ProductID,
		Note: note,
	})
	if err != nil {
		return FloorApprovalAnswer{}, err
	}
	_ = rule
	_ = disc
	return FloorApprovalAnswer{
		Filed: true,
		Request: &FloorRequestState{
			ID: rec.ID, Status: string(rec.Status), LineNo: rec.LineNo, DiscPct: rec.DiscPct,
		},
	}, nil
}

// stampClearingJSON renders a clearing stamp for the repository's metadata
// merge.
func stampClearingJSON(c *floorClearingStamp) []byte {
	if c == nil {
		return nil
	}
	b, err := json.Marshal(c)
	if err != nil {
		return nil
	}
	return b
}
