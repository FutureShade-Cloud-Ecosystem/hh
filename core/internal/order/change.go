package order

// change.go, SCOPE:F10: post-pick change orders.
//
// - THE PROBLEM DIB-175 LEFT OPEN -
// The staff line-edit path (edit.go) is a straight-through write whose matrix DIES AT READY. That
// is correct for "the customer rang while it was still on the shelf". It has nothing to say about
// "the truck is half loaded and they want two more pallets", which is the change that actually
// costs money: the yard is physically holding the goods, and if the delta is material somebody in
// finance has an opinion about it. A straight-through edit gives neither of them a say.
//
// So a post-pick change is not an edit. It is a REQUEST:
//
//	raise -► PENDING_YARD -yard ACK-► PENDING_FINANCE -controller-► APPROVED -apply-► APPLIED
//	                              -(|delta| < threshold)-
//	   any state -reject-► REJECTED        requester -withdraw-► CANCELLED
//
// - FIVE RULINGS, EACH ONE LOAD-BEARING -
//
// 1 · THE ORDER STAYS WORKABLE. Raising a change does not freeze the order, block its transitions
//     or stop the pick. A yard does not stand still for three hours while a controller finds their
//     phone, and a feature that made them would be switched off within a week. This is why the
//     stored artefact is a DELTA and not a target line set (0125 states the full reasoning).
//
// 2 · THE DELTA IS PRICED ONCE, AT REQUEST TIME, AND APPLY WRITES THE SIGNED NUMBERS. Re-pricing at
//     apply would mean a controller signs $480 and $610 lands, which makes the signature worthless.
//     The prices are resolved through the SAME pricing seam edit.go uses, never caller-supplied -
//     so the snapshot is a real resolved price, just a slightly older one. If the change sits long
//     enough for that to matter, the answer is to reject it and raise a fresh one, which is exactly
//     what a purchase order does.
//
// 3 · YARD ACK IS ALWAYS REQUIRED. Every post-pick change needs it, including a $12 one, because
//     the ACK is not about money, it is about whether the people holding the goods can still do
//     it. The finance signature is the money control; they are different questions and collapsing
//     them under one threshold would mean a cheap change silently rewriting a staged load.
//
// 4 · THE FINANCE THRESHOLD GRADES |delta|. A $600 REMOVAL is exactly as material as a $600
//     addition, it is revenue leaving a signed order, and a rule that only caught additions would
//     be trivially gamed by removing and re-adding.
//
// 5 · APPLY IS ATOMIC AND RE-ENTRANT. One transaction writes the line mutations, recomputes the
//     totals, RE-RUNS THE RESERVATIONS and flips the request to APPLIED, all under the order row
//     lock, with the request row's APPROVED→APPLIED move inside it. Two clicks on Apply cannot add
//     two pallets twice: the second finds the row no longer APPROVED and refuses.
//
// - WHAT THIS MODULE DOES NOT DO -
// It does not import tasks and it does not import gl. Whether a pick has started arrives through
// PickStateReader; whether this caller may sign a material delta arrives through FinanceApprover,
// which is the DIB-360 approvals substrate reached through a consumer-declared seam. Both adapters
// live in main.go.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// - injected seams -

// PickStateReader answers the one question that makes a change POST-pick: has the yard started on
// this order? Satisfied in main.go by an adapter over the W2/W3 tasks substrate. Returning the task
// id as well as the boolean lets the change row record WHICH pick it interrupted, which is the first
// thing anyone asks when reading one of these back.
type PickStateReader interface {
	PickStarted(ctx context.Context, orderID string) (started bool, taskID string, err error)
}

// FinanceApprover narrows who may sign a material delta. Satisfied in main.go by an adapter over
// *gl.Service.CheckApprover, the DIB-360/361 approvals substrate, so the same named-approver
// configuration that governs journal entries governs change orders, and this module contains no
// approval configuration of its own.
//
// NARROWING, NEVER WIDENING (gl's own words). identity still decides who is a manager; this seam can
// only remove people from that set. A nil seam means "not configured", which is the shipped rule:
// any manager may sign. That matches gl's deliberate fail-open for an unconfigured branch.
type FinanceApprover interface {
	CheckApprover(ctx context.Context, policyType, branchID, userID string) error
}

// PolicyOrderChangeOverThreshold is the approval policy this module files with the approvals
// substrate. A DISTINCT policy type from gl's JE_OVER_THRESHOLD on purpose: the people who may sign
// a $2,000 journal reclass and the people who may sign a $2,000 change to a customer's order are not
// necessarily the same people, and sharing the policy key would make them the same by accident.
const PolicyOrderChangeOverThreshold = "ORDER_CHANGE_OVER_THRESHOLD"

// WithPickState injects the tasks-substrate pick-state seam.
func (s *Service) WithPickState(p PickStateReader) *Service { s.picks = p; return s }

// WithFinanceApprover injects the gl approvals seam.
func (s *Service) WithFinanceApprover(f FinanceApprover) *Service { s.finance = f; return s }

// - errors -

var (
	// ErrChangeNotFound, no such change order (or it belongs to an order this caller cannot reach).
	ErrChangeNotFound = errors.New("order: change order not found")
	// ErrChangeNotPostPick, the order's pick has not started, so this is an ordinary line edit. The
	// message names the path deliberately: a 409 that does not tell you what to do instead is a wall.
	ErrChangeNotPostPick = errors.New(
		"order: this order's pick has not started, use the line-edit endpoints, not a change order")
	// ErrChangeOpenExists, one live request per order (0125 uq_change_orders_one_open).
	ErrChangeOpenExists = errors.New("order: this order already has an open change request")
	// ErrChangeYardAckRequired, apply attempted before the yard acknowledged.
	ErrChangeYardAckRequired = errors.New("order: the yard has not acknowledged this change")
	// ErrChangeFinanceRequired, apply attempted on a material delta with no finance signature.
	ErrChangeFinanceRequired = errors.New("order: this change exceeds the finance threshold and is not approved")
	// ErrChangeNotApproved, apply attempted from a state that is not APPROVED.
	ErrChangeNotApproved = errors.New("order: this change is not approved")
	// ErrChangeTerminal, a decision was attempted on a change that is already applied/rejected/withdrawn.
	ErrChangeTerminal = errors.New("order: this change order is already closed")
	// ErrChangeNoLines, a request with no delta rows. An empty change is not a change.
	ErrChangeNoLines = errors.New("order: a change order must carry at least one line change")
	// ErrChangeNotConfigured, the change-order store is not wired (a scaffold/fake store).
	ErrChangeNotConfigured = errors.New("order: change-order store not configured")
)

// defaultFinanceThresholdCents is the SCOPE:F10 threshold: $500. It is the DEFAULT stamped onto a
// new request, not a constant the apply path re-reads, see 0125.
const defaultFinanceThresholdCents int64 = 50000

// maxChangeLines bounds one request. A change with 200 line deltas is not a change, it is a rewrite,
// and nobody can meaningfully sign it.
const maxChangeLines = 100

// - domain shapes -

// Change-order lifecycle states (0125 CHECK set).
const (
	ChangeStatusPendingYard    = "PENDING_YARD"
	ChangeStatusPendingFinance = "PENDING_FINANCE"
	ChangeStatusApproved       = "APPROVED"
	ChangeStatusApplied        = "APPLIED"
	ChangeStatusRejected       = "REJECTED"
	ChangeStatusCancelled      = "CANCELLED"
)

// Delta operations (0125 CHECK set).
const (
	ChangeOpAdd    = "ADD"
	ChangeOpQty    = "QTY"
	ChangeOpRemove = "REMOVE"
)

// ChangeLineInput is one requested delta as the caller states it.
type ChangeLineInput struct {
	Op          string `json:"op"`
	LineNo      int    `json:"line_no,omitempty"`
	ProductID   string `json:"product_id,omitempty"`
	QtyEntered  string `json:"qty_entered,omitempty"` // the TARGET quantity for ADD/QTY
	UOM         string `json:"uom_entered,omitempty"`
	Description string `json:"description,omitempty"`
}

// ChangeRequestInput is the raise-a-change body.
type ChangeRequestInput struct {
	Reason string            `json:"reason"`
	Lines  []ChangeLineInput `json:"lines"`
}

// ChangeLine is one persisted delta row.
type ChangeLine struct {
	Seq            int     `json:"seq"`
	Op             string  `json:"op"`
	LineNo         int     `json:"line_no,omitempty"`
	ProductID      string  `json:"product_id,omitempty"`
	ProductName    string  `json:"product_name,omitempty"`
	Description    string  `json:"description"`
	QtyEnteredNew  string  `json:"qty_entered_new,omitempty"`
	UOMEntered     string  `json:"uom_entered,omitempty"`
	QtyBaseBefore  float64 `json:"qty_base_before"`
	QtyBaseAfter   float64 `json:"qty_base_after"`
	QtyDeltaBase   float64 `json:"qty_delta_base"`
	UnitPriceCents int64   `json:"unit_price_cents"`
	PriceUOM       string  `json:"price_uom,omitempty"`
	LineDeltaCents int64   `json:"line_delta_cents"`
}

// ChangeOrder is one change request with its delta.
type ChangeOrder struct {
	ID                    string       `json:"id"`
	OrderID               string       `json:"order_id"`
	BranchID              string       `json:"branch_id"`
	ChangeNo              string       `json:"change_no"`
	Status                string       `json:"status"`
	Reason                string       `json:"reason"`
	DeltaCents            int64        `json:"delta_cents"`
	FinanceThresholdCents int64        `json:"finance_threshold_cents"`
	FinanceRequired       bool         `json:"finance_required"`
	OrderStatusAtRequest  string       `json:"order_status_at_request"`
	PickStartedAtRequest  bool         `json:"pick_started_at_request"`
	RequestedBy           string       `json:"requested_by,omitempty"`
	RequestedAt           string       `json:"requested_at"`
	YardAckedBy           string       `json:"yard_acked_by,omitempty"`
	YardAckedAt           string       `json:"yard_acked_at,omitempty"`
	YardAckNote           string       `json:"yard_ack_note,omitempty"`
	YardTaskID            string       `json:"yard_task_id,omitempty"`
	FinanceApprovedBy     string       `json:"finance_approved_by,omitempty"`
	FinanceApprovedAt     string       `json:"finance_approved_at,omitempty"`
	FinanceNote           string       `json:"finance_note,omitempty"`
	DecisionNote          string       `json:"decision_note,omitempty"`
	AppliedAt             string       `json:"applied_at,omitempty"`
	Lines                 []ChangeLine `json:"lines"`
	// NextActions is what THIS caller may do to this change right now, computed server-side from the
	// same rules the write path enforces, the legal_actions discipline from OrderRead. A client
	// renders buttons from it rather than duplicating the ladder.
	NextActions []string `json:"next_actions"`
}

// financeRequired grades a delta against a threshold. It is a pure function over two integers so
// the $500 rule can be tested without a database, and it grades |delta|, ruling 4.
func financeRequired(deltaCents, thresholdCents int64) bool {
	if thresholdCents <= 0 {
		return false
	}
	if deltaCents < 0 {
		deltaCents = -deltaCents
	}
	return deltaCents >= thresholdCents
}

// nextStatusAfterYardAck is the ladder step the ACK lands on: straight to APPROVED for an immaterial
// delta, PENDING_FINANCE for a material one. Pure, for the same reason financeRequired is.
func nextStatusAfterYardAck(financeReq bool) string {
	if financeReq {
		return ChangeStatusPendingFinance
	}
	return ChangeStatusApproved
}

// changeActions computes what may be done to a change in a given state by THIS caller, from the same
// rules the write paths enforce (the legal_actions discipline): GetChange, the list and the cross order
// queue all read it, so they cannot disagree.
//
//   - the yard ack is open to any active in-branch staff: the person who can answer "can the yard still
//     do this" is the yard;
//   - sign_apply and finance_approve need a manager, and finance_approve and a sign on PENDING_FINANCE
//     need a named approver when the approvals seam is wired (narrowing, never widening);
//   - reject is open to staff while the yard decides, and a manager's alone once the change is waiting on
//     finance or approved: a frontline user cannot kill what a controller just signed;
//   - cancel is the requester withdrawing, so the requester and a manager at every live state.
//
// approver memoises the approver answer per branch for a list; nil asks every time.
func (s *Service) changeActions(ctx context.Context, caller identity.StaffContext, status, branchID, requestedBy string, approver map[string]bool) []string {
	isMgr := caller.IsManager()
	canCancel := isMgr || (requestedBy != "" && requestedBy == caller.UserID)
	out := []string{}
	switch status {
	case ChangeStatusPendingYard:
		out = append(out, "yard_ack", "reject")
	case ChangeStatusPendingFinance:
		if isMgr && s.mayFinanceSign(ctx, branchID, caller.UserID, approver) {
			out = append(out, "sign_apply", "finance_approve")
		}
		if isMgr {
			out = append(out, "reject")
		}
	case ChangeStatusApproved:
		if isMgr {
			out = append(out, "sign_apply", "apply", "reject")
		}
	default:
		return out
	}
	if canCancel {
		out = append(out, "cancel")
	}
	return out
}

// mayFinanceSign asks the approvals seam whether this manager is a named approver for the branch. An
// unwired seam is the shipped rule: any manager may sign.
func (s *Service) mayFinanceSign(ctx context.Context, branchID, userID string, memo map[string]bool) bool {
	if s.finance == nil {
		return true
	}
	if memo != nil {
		if ok, seen := memo[branchID]; seen {
			return ok
		}
	}
	ok := s.finance.CheckApprover(ctx, PolicyOrderChangeOverThreshold, branchID, userID) == nil
	if memo != nil {
		memo[branchID] = ok
	}
	return ok
}

// - raise -

// RaiseChange creates a change request against an order whose pick has started.
//
// AUTHORITY: active in-branch staff. Raising is not itself money-moving, nothing changes until the
// signatures are collected, and the person who knows the customer asked for two more pallets is the
// person on the phone, not a manager. The MONEY gates are on apply.
func (s *Service) RaiseChange(ctx context.Context, orderIDOrNo string, in ChangeRequestInput) (ChangeOrder, error) {
	caller, orderID, branchID, status, err := s.changeOrderContext(ctx, orderIDOrNo)
	if err != nil {
		return ChangeOrder{}, err
	}
	cs, err := s.changeStore()
	if err != nil {
		return ChangeOrder{}, err
	}
	if len(in.Lines) == 0 {
		return ChangeOrder{}, ErrChangeNoLines
	}
	if len(in.Lines) > maxChangeLines {
		return ChangeOrder{}, httpx.BadRequestf("a change order carries at most %d line changes", maxChangeLines)
	}
	if len(in.Reason) > maxReasonLen {
		return ChangeOrder{}, httpx.BadRequestf("reason exceeds %d characters", maxReasonLen)
	}

	// The post-pick gate. The tasks substrate is the authority; the status ladder is the fallback for
	// an order the yard is working WITHOUT a task row (a will-call pulled straight off the rack). Both
	// are recorded on the request so "was this really post-pick?" is answerable later.
	pickStarted, taskID, err := s.pickStarted(ctx, orderID)
	if err != nil {
		return ChangeOrder{}, err
	}
	if !pickStarted && !statusIsPostPick(status) {
		return ChangeOrder{}, ErrChangeNotPostPick
	}

	// Price the delta ONCE, here (ruling 2), through the same seam a line edit uses.
	lines, deltaCents, err := s.priceChangeDelta(ctx, cs, orderID, in.Lines)
	if err != nil {
		return ChangeOrder{}, err
	}

	co := ChangeOrder{
		OrderID:               orderID,
		BranchID:              branchID,
		Status:                ChangeStatusPendingYard,
		Reason:                strings.TrimSpace(in.Reason),
		DeltaCents:            deltaCents,
		FinanceThresholdCents: defaultFinanceThresholdCents,
		FinanceRequired:       financeRequired(deltaCents, defaultFinanceThresholdCents),
		OrderStatusAtRequest:  status,
		PickStartedAtRequest:  pickStarted,
		RequestedBy:           caller.UserID,
		YardTaskID:            taskID,
		Lines:                 lines,
	}
	out, err := cs.InsertChangeOrder(ctx, co)
	if err != nil {
		return ChangeOrder{}, err
	}
	s.recordChangeAudit(ctx, caller.UserID, out, "CHANGE_RAISED", out.Reason)
	out.NextActions = s.changeActions(ctx, caller, out.Status, out.BranchID, out.RequestedBy, nil)
	return out, nil
}

// statusIsPostPick reports whether the order's own status ladder says the yard has it. READY and
// PARTIALLY_FULFILLED count: the load is staged or half gone, which is emphatically post-pick.
func statusIsPostPick(status string) bool {
	switch status {
	case "PICKING", "READY", "PARTIALLY_FULFILLED":
		return true
	default:
		return false
	}
}

// pickStarted consults the tasks seam. An UNWIRED seam reports "not started" rather than erroring -
// the status-ladder fallback then decides, so a deploy without the tasks adapter degrades to the
// pre-tasks rule instead of refusing every change request.
func (s *Service) pickStarted(ctx context.Context, orderID string) (bool, string, error) {
	if s.picks == nil {
		return false, "", nil
	}
	started, taskID, err := s.picks.PickStarted(ctx, orderID)
	if err != nil {
		return false, "", err
	}
	return started, taskID, nil
}

// priceChangeDelta resolves each requested delta into a persisted delta row and sums the money.
//
// Every ADD and QTY row is priced through s.priceLine, the SAME resolver a line edit uses, never a
// caller-supplied price, so a change cannot smuggle a hand-set rate past the pricing waterfall and
// its margin gate. A REMOVE is priced at the line's CURRENT total (what leaves the order).
func (s *Service) priceChangeDelta(ctx context.Context, cs changeStore, orderID string, in []ChangeLineInput) ([]ChangeLine, int64, error) {
	es, err := s.editStore()
	if err != nil {
		return nil, 0, err
	}
	customerID, branchID, err := s.orderPricingContext(ctx, orderID)
	if err != nil {
		return nil, 0, err
	}
	seen := map[int]bool{}
	out := make([]ChangeLine, 0, len(in))
	var total int64

	for i, l := range in {
		op := strings.ToUpper(strings.TrimSpace(l.Op))
		row := ChangeLine{Seq: i + 1, Op: op, Description: l.Description}

		switch op {
		case ChangeOpAdd:
			if !looksLikeUUID(l.ProductID) {
				return nil, 0, httpx.BadRequestf("line %d: product_id must be a uuid for an ADD", i+1)
			}
			if strings.TrimSpace(l.UOM) == "" {
				return nil, 0, httpx.BadRequestf("line %d: uom_entered is required for an ADD", i+1)
			}
			qty, err := parseEditQty(l.QtyEntered)
			if err != nil {
				return nil, 0, err
			}
			lp, err := s.priceLine(ctx, customerID, branchID, l.ProductID, l.UOM, qty)
			if err != nil {
				return nil, 0, err
			}
			row.ProductID = l.ProductID
			row.QtyEnteredNew = l.QtyEntered
			row.UOMEntered = l.UOM
			row.QtyBaseAfter = lp.QtyBase
			row.QtyDeltaBase = lp.QtyBase
			row.UnitPriceCents = lp.UnitPriceCents
			row.PriceUOM = lp.PriceUOM
			row.LineDeltaCents = lp.LineTotalCents

		case ChangeOpQty, ChangeOpRemove:
			if l.LineNo <= 0 {
				return nil, 0, httpx.BadRequestf("line %d: line_no is required for a %s", i+1, op)
			}
			if seen[l.LineNo] {
				return nil, 0, httpx.BadRequestf("line %d: order line %d appears twice in this change", i+1, l.LineNo)
			}
			seen[l.LineNo] = true
			cur, uom, allocated, fulfilled, err := es.LineForEdit(ctx, orderID, l.LineNo)
			if err != nil {
				return nil, 0, err
			}
			before := parseQtyOrZero(cur.QtyBase)
			row.LineNo = l.LineNo
			row.ProductID = cur.ProductID
			row.QtyBaseBefore = before
			row.UOMEntered = uom

			if op == ChangeOpRemove {
				// A line the yard has already committed against cannot be removed, the same refusal
				// RemoveLine makes, made here so it lands at REQUEST time rather than after two people
				// have signed something that was never appliable.
				if allocated > 0 || fulfilled > 0 {
					return nil, 0, fmt.Errorf("%w: line %d has %.4f allocated, %.4f fulfilled",
						ErrLineInUse, l.LineNo, allocated, fulfilled)
				}
				row.QtyBaseAfter = 0
				row.QtyDeltaBase = -before
				row.UnitPriceCents = cur.UnitPriceCents
				row.LineDeltaCents = -cur.LineTotalCents
				break
			}
			if cur.ProductID == "" {
				return nil, 0, httpx.BadRequestf("line %d has no catalogue product and cannot be repriced", l.LineNo)
			}
			qty, err := parseEditQty(l.QtyEntered)
			if err != nil {
				return nil, 0, err
			}
			lp, err := s.priceLine(ctx, customerID, branchID, cur.ProductID, uom, qty)
			if err != nil {
				return nil, 0, err
			}
			if lp.QtyBase < fulfilled {
				return nil, 0, fmt.Errorf("%w: %.4f already fulfilled on line %d", ErrLineInUse, fulfilled, l.LineNo)
			}
			row.QtyEnteredNew = l.QtyEntered
			row.QtyBaseAfter = lp.QtyBase
			row.QtyDeltaBase = lp.QtyBase - before
			row.UnitPriceCents = lp.UnitPriceCents
			row.PriceUOM = lp.PriceUOM
			row.LineDeltaCents = lp.LineTotalCents - cur.LineTotalCents

		default:
			return nil, 0, httpx.BadRequestf("line %d: unknown change op %q (ADD|QTY|REMOVE)", i+1, l.Op)
		}
		total += row.LineDeltaCents
		out = append(out, row)
	}
	return out, total, nil
}

// - the decisions -

// YardAck records the yard's acknowledgement, the round trip that makes this different from an
// edit. Open to active in-branch staff (the yard is not a manager). Moves the request to APPROVED
// for an immaterial delta and to PENDING_FINANCE for a material one, so the ladder cannot be skipped
// by acking twice.
func (s *Service) YardAck(ctx context.Context, changeID, note string) (ChangeOrder, error) {
	caller, cs, co, err := s.loadChangeForDecision(ctx, changeID)
	if err != nil {
		return ChangeOrder{}, err
	}
	if co.Status != ChangeStatusPendingYard {
		if isTerminalChange(co.Status) {
			return ChangeOrder{}, ErrChangeTerminal
		}
		return ChangeOrder{}, fmt.Errorf("%w: the yard has already acknowledged this change", ErrChangeTerminal)
	}
	out, err := cs.RecordYardAck(ctx, co.ID, caller.UserID, clampNote(note),
		nextStatusAfterYardAck(co.FinanceRequired))
	if err != nil {
		return ChangeOrder{}, err
	}
	s.recordChangeAudit(ctx, caller.UserID, out, "CHANGE_YARD_ACKED", clampNote(note))
	out.NextActions = s.changeActions(ctx, caller, out.Status, out.BranchID, out.RequestedBy, nil)
	return out, nil
}

// FinanceApprove records the controller's signature on a material delta.
//
// TWO GATES, and they are different questions: identity says you are a MANAGER, and the DIB-360
// approvals substrate says you are one of the people NAMED for this branch. The second can only
// narrow the first (gl's stated invariant), so an unconfigured branch behaves exactly as it did
// before this feature: any manager may sign.
func (s *Service) FinanceApprove(ctx context.Context, changeID, note string) (ChangeOrder, error) {
	caller, cs, co, err := s.loadChangeForDecision(ctx, changeID)
	if err != nil {
		return ChangeOrder{}, err
	}
	if isTerminalChange(co.Status) {
		return ChangeOrder{}, ErrChangeTerminal
	}
	if co.Status == ChangeStatusPendingYard {
		return ChangeOrder{}, ErrChangeYardAckRequired
	}
	if co.Status != ChangeStatusPendingFinance {
		return ChangeOrder{}, fmt.Errorf("%w: this change does not need a finance signature", ErrChangeTerminal)
	}
	if !caller.IsManager() {
		return ChangeOrder{}, ErrForbidden
	}
	if s.finance != nil {
		if err := s.finance.CheckApprover(ctx, PolicyOrderChangeOverThreshold, co.BranchID, caller.UserID); err != nil {
			return ChangeOrder{}, ErrForbidden
		}
	}
	out, err := cs.RecordFinanceApproval(ctx, co.ID, caller.UserID, clampNote(note), ChangeStatusApproved)
	if err != nil {
		return ChangeOrder{}, err
	}
	s.recordChangeAudit(ctx, caller.UserID, out, "CHANGE_FINANCE_APPROVED", clampNote(note))
	out.NextActions = s.changeActions(ctx, caller, out.Status, out.BranchID, out.RequestedBy, nil)
	return out, nil
}

// RejectChange refuses a change. Any in-branch staff may reject while the yard is deciding (the yard
// saying "we cannot do that, the truck has gone" is as valid a refusal as a controller's); once the change
// waits on finance or is approved only a manager may, and the guarded write enforces that against a racing
// yard ack, not only the read before it.
func (s *Service) RejectChange(ctx context.Context, changeID, note string) (ChangeOrder, error) {
	return s.closeChange(ctx, changeID, ChangeStatusRejected, note, "CHANGE_REJECTED")
}

// CancelChange withdraws a change (the requester changed their mind, or the customer did).
func (s *Service) CancelChange(ctx context.Context, changeID, note string) (ChangeOrder, error) {
	return s.closeChange(ctx, changeID, ChangeStatusCancelled, note, "CHANGE_CANCELLED")
}

func (s *Service) closeChange(ctx context.Context, changeID, target, note, action string) (ChangeOrder, error) {
	caller, cs, co, err := s.loadChangeForDecision(ctx, changeID)
	if err != nil {
		return ChangeOrder{}, err
	}
	if isTerminalChange(co.Status) {
		return ChangeOrder{}, ErrChangeTerminal
	}
	// The service, not only the route guard, decides who may close a change. A cancel is the requester
	// withdrawing, so only the requester and a manager; a reject is any staff while the yard decides, and
	// a manager's alone once the change waits on finance or is approved.
	allowedFrom := liveChangeStatuses
	if !caller.IsManager() {
		if target == ChangeStatusCancelled && (co.RequestedBy == "" || co.RequestedBy != caller.UserID) {
			return ChangeOrder{}, ErrForbidden
		}
		if target == ChangeStatusRejected {
			if co.Status != ChangeStatusPendingYard {
				return ChangeOrder{}, ErrForbidden
			}
			// The status above came from an unlocked read. The write carries the same restriction, so a yard
			// ack that lands between the two cannot let this reject through.
			allowedFrom = []string{ChangeStatusPendingYard}
		}
	}
	out, err := cs.CloseChangeOrder(ctx, co.ID, target, caller.UserID, clampNote(note), allowedFrom)
	if errors.Is(err, ErrChangeTerminal) && len(allowedFrom) < len(liveChangeStatuses) {
		// Either the change closed meanwhile (terminal, as ever) or it moved past the status this caller may
		// reject from: that second case is an authority refusal, not a closed change.
		if now, gerr := cs.GetChangeOrder(ctx, co.ID); gerr == nil && !isTerminalChange(now.Status) {
			return ChangeOrder{}, ErrForbidden
		}
	}
	if err != nil {
		return ChangeOrder{}, err
	}
	s.recordChangeAudit(ctx, caller.UserID, out, action, clampNote(note))
	out.NextActions = s.changeActions(ctx, caller, out.Status, out.BranchID, out.RequestedBy, nil)
	return out, nil
}

// liveChangeStatuses are the states a change can still be closed from.
var liveChangeStatuses = []string{ChangeStatusPendingYard, ChangeStatusPendingFinance, ChangeStatusApproved, "DRAFT"}

// - apply -

// ApplyChange writes an APPROVED delta onto the order and re-runs its reservations, atomically.
//
// ONE TRANSACTION, under the ORDER row lock:
//
//	lock order            → serializes against a concurrent transition or a second apply
//	lock change (APPROVED)→ APPROVED→APPLIED here, so a double click cannot double-apply
//	money predicate       → no finalized invoice, no committed sale; a tax the module cannot
//	                        reproduce refuses (ErrEditTaxAssessed), a tax it can is re-assessed below
//	apply each delta row  → INSERT / UPDATE / DELETE on order_lines
//	recompute totals      → Σ line_total_cents, never a delta increment; tax re-assessed at the
//	                        rate in force the day the order was created, total = subtotal + quoted
//	                        freight + tax, line tax re-split by the largest remainder rule
//	resync reservations   → release ALL this order's holds, re-reserve from the NEW lines
//	record                → the order.change.applied outbox event (and, for the one tap, the audit
//	                        row carrying the Idempotency-Key), on the same transaction
//
// The reservation resync is release-then-reserve rather than a per-line adjustment for the reason
// stated at ResyncReservations: after it commits, the order's holds ARE its lines, with no path by
// which a stale hold survives. It shares this transaction, so a failure anywhere above leaves both
// the lines and the holds exactly as they were.
//
// AUTHORITY: manager. Apply is the money-moving act, everything before it is paperwork.
func (s *Service) ApplyChange(ctx context.Context, changeID string) (ChangeOrder, error) {
	caller, cs, co, err := s.loadChangeForDecision(ctx, changeID)
	if err != nil {
		return ChangeOrder{}, err
	}
	if !caller.IsManager() {
		return ChangeOrder{}, ErrForbidden
	}
	if isTerminalChange(co.Status) {
		return ChangeOrder{}, ErrChangeTerminal
	}
	// Grade the ladder BEFORE the write so the refusal names the missing signature rather than a
	// generic "not approved", the guard is re-evaluated under the lock inside ApplyChangeTx.
	switch co.Status {
	case ChangeStatusPendingYard:
		return ChangeOrder{}, ErrChangeYardAckRequired
	case ChangeStatusPendingFinance:
		return ChangeOrder{}, ErrChangeFinanceRequired
	case ChangeStatusApproved:
	default:
		return ChangeOrder{}, ErrChangeNotApproved
	}

	out, err := cs.ApplyChangeTx(ctx, co.ID, s.changeApplyHooks(caller.UserID, co.OrderID, "", ""))
	if err != nil {
		return ChangeOrder{}, err
	}
	s.recordChangeAudit(ctx, caller.UserID, out, "CHANGE_APPLIED", out.Reason)
	out.NextActions = s.changeActions(ctx, caller, out.Status, out.BranchID, out.RequestedBy, nil)
	return out, nil
}

// - reads -

// ListChanges returns an order's change history, newest first.
func (s *Service) ListChanges(ctx context.Context, orderIDOrNo string) ([]ChangeOrder, error) {
	caller, orderID, _, _, err := s.changeOrderContext(ctx, orderIDOrNo)
	if err != nil {
		return nil, err
	}
	cs, err := s.changeStore()
	if err != nil {
		return nil, err
	}
	out, err := cs.ListChangeOrders(ctx, orderID)
	if err != nil {
		return nil, err
	}
	approver := map[string]bool{}
	for i := range out {
		out[i].NextActions = s.changeActions(ctx, caller, out[i].Status, out[i].BranchID, out[i].RequestedBy, approver)
	}
	return out, nil
}

// GetChange returns one change request with its delta.
func (s *Service) GetChange(ctx context.Context, changeID string) (ChangeOrder, error) {
	caller, _, co, err := s.loadChangeForDecision(ctx, changeID)
	if err != nil {
		return ChangeOrder{}, err
	}
	co.NextActions = s.changeActions(ctx, caller, co.Status, co.BranchID, co.RequestedBy, nil)
	return co, nil
}

// - plumbing -

// changeOrderContext resolves the caller and the order, applying the same authority ladder every
// other staff order surface uses: authenticated → active → resolvable (order_no accepted, scoped) →
// in-branch. A missing order is 404 BEFORE any authority-specific refusal, so the change surface is
// not an existence oracle for another branch's orders.
func (s *Service) changeOrderContext(ctx context.Context, orderIDOrNo string) (identity.StaffContext, string, string, string, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return identity.StaffContext{}, "", "", "", err
	}
	if caller.Status != identity.StatusActive {
		return identity.StaffContext{}, "", "", "", ErrForbidden
	}
	if strings.TrimSpace(orderIDOrNo) == "" {
		return identity.StaffContext{}, "", "", "", httpx.BadRequestf("order id is required")
	}
	global, branches := callerBranchScope(caller)
	orderID, err := s.store.ResolveOrderID(ctx, orderIDOrNo, global, branches)
	if err != nil {
		return identity.StaffContext{}, "", "", "", err
	}
	status, branchID, err := s.store.OrderHeader(ctx, orderID)
	if err != nil {
		return identity.StaffContext{}, "", "", "", err
	}
	if !caller.CanAccessBranch(branchID) {
		return identity.StaffContext{}, "", "", "", ErrForbidden
	}
	return caller, orderID, branchID, status, nil
}

// loadChangeForDecision resolves the caller, loads the change and re-applies the branch check
// against the change's OWN branch. A change in a branch the caller cannot reach reads as NOT FOUND,
// never forbidden, same no-oracle rule as the order surface.
func (s *Service) loadChangeForDecision(ctx context.Context, changeID string) (identity.StaffContext, changeStore, ChangeOrder, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return identity.StaffContext{}, nil, ChangeOrder{}, err
	}
	if caller.Status != identity.StatusActive {
		return identity.StaffContext{}, nil, ChangeOrder{}, ErrForbidden
	}
	cs, err := s.changeStore()
	if err != nil {
		return identity.StaffContext{}, nil, ChangeOrder{}, err
	}
	if !looksLikeUUID(changeID) {
		return identity.StaffContext{}, nil, ChangeOrder{}, ErrChangeNotFound
	}
	co, err := cs.GetChangeOrder(ctx, changeID)
	if err != nil {
		return identity.StaffContext{}, nil, ChangeOrder{}, err
	}
	if !caller.CanAccessBranch(co.BranchID) {
		return identity.StaffContext{}, nil, ChangeOrder{}, ErrChangeNotFound
	}
	return caller, cs, co, nil
}

func (s *Service) changeStore() (changeStore, error) {
	cs, ok := s.store.(changeStore)
	if !ok {
		return nil, ErrChangeNotConfigured
	}
	return cs, nil
}

func isTerminalChange(status string) bool {
	switch status {
	case ChangeStatusApplied, ChangeStatusRejected, ChangeStatusCancelled:
		return true
	default:
		return false
	}
}

func clampNote(note string) string {
	note = strings.TrimSpace(note)
	if len(note) > maxReasonLen {
		return note[:maxReasonLen]
	}
	return note
}

// recordChangeAudit lands one attributed row per change-order decision on the Q-044 spine.
//
// IT REUSES ActOrderChanged AND ONLY ALLOWLISTED Extra KEYS. internal/audit is deny-by-default: a
// key that is not in allowedExtraKeys is DROPPED with a warning, so a record shaped around
// change_no/delta_cents/finance_required would look correct in this file and land as an empty trail.
// The allowlist lives in another module's fence, so the record is shaped to what is already
// permitted instead:
//
//	field   → "change_order"   (the vocabulary key the DIB-175 edit trail already uses)
//	outcome → the decision      (CHANGE_RAISED / _YARD_ACKED / _FINANCE_APPROVED / _APPLIED / …)
//	status  → the change's resulting state
//	policy  → the approval policy, present ONLY when a finance signature was actually required
//
// The change NUMBER rides in the top-level Reason (persisted as changes["reason"]), which is
// allowlisted, so the trail still names which change it is talking about. Adding change_no and
// delta_cents to the allowlist is a two-line change in internal/audit and is named as a park in
// REPORT-W5-A.md rather than made here.
//
// Best-effort, like every other order-surface record, the change_orders table stays the truth.
func (s *Service) recordChangeAudit(ctx context.Context, actor string, co ChangeOrder, action, reason string) {
	if s.audit == nil {
		return
	}
	extra := map[string]any{
		"field":       "change_order",
		"outcome":     action,
		"status":      co.Status,
		"change_no":   co.ChangeNo, // UNION-W5: named keys land with the allowlist widening
		"delta_cents": co.DeltaCents,
	}
	if co.FinanceRequired {
		extra["policy"] = PolicyOrderChangeOverThreshold
	}
	note := co.ChangeNo
	if reason != "" {
		note = co.ChangeNo + ": " + reason
	}
	_ = s.audit.Record(ctx, audit.SensitiveAccess{
		Action: audit.ActOrderChanged, EntityType: "order", EntityID: co.OrderID,
		ActorID: actor, Purpose: "ORDER_CHANGE_ORDER", Reason: note,
		Extra: extra,
	})
}
