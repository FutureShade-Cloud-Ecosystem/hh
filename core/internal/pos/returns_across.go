package pos

// returns_across.go, sub item 10.2: the credit note across orders. One note, N orders of one
// customer, one branch, ONE transaction over the SINGLE return's own halves: every child's lines
// are guarded by guardReturnLines (the single return's own locks and words) and committed by
// commitReturnTx (the single return's own header, rail, restock and journals, one
// pos.return.committed per child, the GL builder and its key untouched). The note holds the
// children together: its own number, its own idempotency key (the children's keys derive from it),
// the note total the approval threshold judges (the Q3 ruling), and the per-sale locks taken for
// every order in ASCENDING order id before any line lock, so two overlapping notes serialize
// instead of deadlocking.

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5"
)

// reservedNoteKeyPrefix is the idempotency-key namespace ReturnAcross derives its children's keys
// from ("note:" + note key + ":" + order id). Like the return-request rail's "rreq:" it is
// RESERVED: a public single-order return may never use it (a colliding key would squat a note
// child's replay slot), and a note may never use "rreq:" either.
const reservedNoteKeyPrefix = "note:"

// noteChildKey derives one child's pos_returns idempotency key from the note's own key: one key
// per note, the children's keys derived, each child still unique under ux_pos_returns_idem.
func noteChildKey(noteKey, orderID string) string {
	return reservedNoteKeyPrefix + noteKey + ":" + orderID
}

// checkNoteShape is the note's own shape rules, shared by the commit and the credit preview so they
// refuse in the same words: a customer named, the house WALK-IN row refused (the customer lines
// read's own refusal: the note is one account customer's credit note, never the shared row's),
// every order named once, carrying lines, each with a line and a positive quantity. It answers the
// trimmed customer id.
func (s *Service) checkNoteShape(ctx context.Context, customerID, number string, orders []ReturnAcrossOrderInput, allowWalkIn bool) (string, error) {
	customerID, err := s.customerOrWalkIn(ctx, customerID, number)
	if err != nil {
		return "", err
	}
	if walkIn, werr := s.store.ResolveWalkIn(ctx); !allowWalkIn && werr == nil && walkIn != "" && walkIn == customerID {
		return "", errWalkInNotReturnable
	}
	seenOrder := make(map[string]bool, len(orders))
	for _, o := range orders {
		if o.OrderID == "" {
			return "", clientErr(http.StatusBadRequest, "pos: every order on a return note needs an order_id")
		}
		if seenOrder[o.OrderID] {
			return "", clientErr(http.StatusUnprocessableEntity, "pos: order %s appears twice on one note", o.OrderID)
		}
		seenOrder[o.OrderID] = true
		if len(o.Lines) == 0 {
			return "", clientErr(http.StatusUnprocessableEntity, "pos: order %s carries no return lines", o.OrderID)
		}
		for _, ln := range o.Lines {
			if ln.OrderLineID == "" || ln.QtyBase <= 0 {
				return "", clientErr(http.StatusUnprocessableEntity, "pos: every return line needs an order_line_id and a positive qty_base")
			}
		}
	}
	return customerID, nil
}

// noteWalls is the note's two walls around an order: it must be the note customer's, and sit in the
// same branch as the note's first order (nil for the first). Shared by the commit and the preview.
func noteWalls(first *returnOrderCtx, oc returnOrderCtx, customerID string) error {
	if oc.CustomerID != customerID {
		return clientErr(http.StatusUnprocessableEntity,
			"pos: order %s belongs to another customer, not the note's customer", oc.OrderNo)
	}
	if first != nil && first.BranchID != oc.BranchID {
		return clientErr(http.StatusUnprocessableEntity,
			"pos: a return note covers one branch, order %s sits in another", oc.OrderNo)
	}
	return nil
}

// PreviewReturnAcross answers the credit so far of a draft note: the same shape rules, branch wall
// and customer and branch walls as the commit, every line guarded and priced by the commit's own
// guardLines, read without locks, and nothing written. The note total is the sum of the same
// per-line figures the commit would book.
func (s *Service) PreviewReturnAcross(ctx context.Context, in ReturnPreviewInput) (ReturnPreview, error) {
	out, _, err := s.previewNote(ctx, in)
	return out, err
}

// previewNote is PreviewReturnAcross that also answers each order's resolved return context, which
// the swap judges its source orders and its leftover rail by.
func (s *Service) previewNote(ctx context.Context, in ReturnPreviewInput) (ReturnPreview, []returnOrderCtx, error) {
	out, ocs, _, err := s.previewNoteProducts(ctx, in, true)
	return out, ocs, err
}

// previewNoteProducts is previewNote that also answers the products the note's lines return.
func (s *Service) previewNoteProducts(ctx context.Context, in ReturnPreviewInput, allowWalkIn bool) (ReturnPreview, []returnOrderCtx, []string, error) {
	var ocs []returnOrderCtx
	var products []string
	if len(in.Orders) == 0 {
		return ReturnPreview{}, nil, nil, clientErr(http.StatusBadRequest, "pos: a return note needs at least one order")
	}
	customerID, err := s.checkNoteShape(ctx, in.CustomerID, in.Number, in.Orders, allowWalkIn)
	if err != nil {
		return ReturnPreview{}, nil, nil, err
	}
	method := in.RefundMethod
	switch method {
	case "", RefundCash, RefundCard:
	case RefundAccountCredit:
		if werr := s.accountCreditAllowed(ctx, customerID, false); werr != nil {
			return ReturnPreview{}, nil, nil, werr
		}
	default:
		return ReturnPreview{}, nil, nil, errBadRefundMethod
	}
	withSplit := method == RefundCash || method == RefundCard
	var sumRail, sumOffset int64
	caller, err := s.caller(ctx)
	if err != nil {
		return ReturnPreview{}, nil, nil, err
	}
	out := ReturnPreview{CustomerID: customerID, Orders: make([]ReturnPreviewOrder, 0, len(in.Orders))}
	var first *returnOrderCtx
	for _, o := range in.Orders {
		oc, found, err := s.store.ReturnOrderContextFor(ctx, o.OrderID, "", reachableBranches(caller))
		if err != nil {
			return ReturnPreview{}, nil, nil, err
		}
		if !found {
			return ReturnPreview{}, nil, nil, errReturnNotFound
		}
		if err := requireBranchReach(caller, oc.BranchID); err != nil {
			return ReturnPreview{}, nil, nil, err
		}
		if err := noteWalls(first, oc, customerID); err != nil {
			return ReturnPreview{}, nil, nil, err
		}
		if oc, err = s.resolveConverted(ctx, oc); err != nil {
			return ReturnPreview{}, nil, nil, err
		}
		if first == nil {
			ocCopy := oc
			first = &ocCopy
			out.BranchID = oc.BranchID
		}
		g, err := s.previewReturnLines(ctx, oc, o.Lines)
		if err != nil {
			return ReturnPreview{}, nil, nil, err
		}
		ocs = append(ocs, oc)
		for _, ln := range g.lines {
			products = append(products, ln.ProductID)
		}
		po := ReturnPreviewOrder{
			OrderID: oc.OrderID, OrderNo: oc.OrderNo, SubtotalCents: g.subtotal, TaxCents: g.taxTotal,
			TotalCents: g.subtotal + g.taxTotal, Lines: make([]ReturnPreviewLine, 0, len(g.lines)),
		}
		for _, ln := range g.lines {
			po.Lines = append(po.Lines, ReturnPreviewLine{
				OrderLineID: ln.OrderLineID, QtyBase: ln.QtyBase, SubtotalCents: ln.LineTotalCents,
				TaxCents: ln.TaxCents, TotalCents: ln.LineTotalCents + ln.TaxCents,
			})
		}
		if withSplit {
			sp, serr := s.railSplitFor(ctx, nil, oc, method, po.TotalCents)
			if serr != nil {
				return ReturnPreview{}, nil, nil, serr
			}
			rail, off := sp.rail, sp.offset
			po.RailCents, po.OffsetCents, po.RailNote = &rail, &off, sp.note
			sumRail += rail
			sumOffset += off
		}
		out.SubtotalCents += po.SubtotalCents
		out.TaxCents += po.TaxCents
		out.Orders = append(out.Orders, po)
	}
	out.TotalCents = out.SubtotalCents + out.TaxCents
	if withSplit {
		out.RailCents, out.OffsetCents = &sumRail, &sumOffset
	}
	return out, ocs, products, nil
}

// returnAcrossPlan is everything the note resolves BEFORE its transaction opens, one child plan
// per order (each built by the single return's own prepareReturn, so the branch wall, the rail
// checks and the replay discipline are the single path's) plus the note-level facts.
type returnAcrossPlan struct {
	in       ReturnAcrossInput
	caller   identity.StaffContext
	actor    string
	children []*returnPlan
	// preReplay carries the committed note the pre-transaction replay check answered: set means
	// no transaction and no child body at all.
	preReplay   ReturnNote
	preReplayed bool
}

// prepareReturnAcross is the note's pre-transaction section, run while no connection is held: the
// note-level validation, one child plan per order through the SINGLE return's own prepareReturn
// (the branch wall before any rail, per child), the one-branch rule, the note replay pre-check and
// the approver resolution against the note's prospect.
func (s *Service) prepareReturnAcross(ctx context.Context, in ReturnAcrossInput) (*returnAcrossPlan, error) {
	plan := &returnAcrossPlan{in: in}
	if in.IdempotencyKey == "" {
		return nil, errMissingKey
	}
	if strings.HasPrefix(in.IdempotencyKey, reservedRequestKeyPrefix) {
		return nil, errReservedKey
	}
	// "swap:" is the swap's namespace for its credit note's key: a public note keyed into it would
	// squat a swap's note slot, or answer its replay to a caller who reaches its branch.
	if !in.swap && strings.HasPrefix(in.IdempotencyKey, reservedSwapKeyPrefix) {
		return nil, errReservedKey
	}
	if len(in.Orders) == 0 {
		return nil, clientErr(http.StatusBadRequest, "pos: a return note needs at least one order")
	}
	switch in.RefundMethod {
	case RefundCash, RefundCard, RefundAccountCredit:
	default:
		return nil, errBadRefundMethod
	}
	customerID, shapeErr := s.checkNoteShape(ctx, in.CustomerID, in.Number, in.Orders, !in.swap)
	if shapeErr != nil {
		return nil, shapeErr
	}
	in.CustomerID = customerID
	plan.in = in

	// The note's own replay pre-check, judged against the replayed note's own branch by the wall.
	caller, err := s.caller(ctx)
	if err != nil {
		return nil, err
	}
	plan.caller = caller
	if cached, found, nerr := s.replayNoteIfAllowed(ctx, in.IdempotencyKey, caller); nerr != nil {
		return nil, nerr
	} else if found {
		plan.preReplay = cached
		plan.preReplayed = true
		return plan, nil
	}

	// One child plan per order through the single return's own prepareReturn: validation, the
	// order context, the branch wall BEFORE any rail, the CASH drawer and CARD capture checks, the
	// fail-closed role gate. The child key is derived from the note's own key.
	for _, o := range in.Orders {
		childIn := ReturnInput{
			IdempotencyKey: noteChildKey(in.IdempotencyKey, o.OrderID),
			OrderID:        o.OrderID,
			Lines:          o.Lines,
			RefundMethod:   in.RefundMethod,
			Reason:         in.Reason,
			Restock:        in.Restock,
			TillSessionID:  in.TillSessionID,
			noteChild:      true,
			railOptional:   in.railOptional,
		}
		child, perr := s.prepareReturn(ctx, childIn)
		if perr != nil {
			return nil, perr
		}
		// The note's walls around its children: one customer (an order that is not the note
		// customer's cannot ride the note) and one branch (the money rail is per branch).
		var firstOC *returnOrderCtx
		if len(plan.children) > 0 {
			firstOC = &plan.children[0].oc
		}
		if werr := noteWalls(firstOC, child.oc, in.CustomerID); werr != nil {
			return nil, werr
		}
		plan.children = append(plan.children, child)
	}

	// The approver resolution, against the note's own prospect: the note total the gate judges is
	// unknown until the children are guarded, so the credential resolves when the prospect of the
	// requested lines' own totals could cross the configured amount (the refundMayNeedApproval
	// discipline, summed over the note's orders).
	if s.roles == nil {
		return nil, errRoleConfigUnwired()
	}
	if in.ApproverCredential != "" && s.noteMayNeedApproval(ctx, plan) {
		first := plan.children[0]
		resolved, pending, rerr := s.resolveRefundApprover(ctx, in.ApproverCredential, caller, first.oc)
		for _, child := range plan.children {
			child.decision = resolved
			child.decision.pending = pending
			child.judgeErr = rerr
			child.credentialJudged = true
			// The decision's names, read before the transaction opens (the P3-D discipline). The
			// note's refusal row is its own (the note refusal audit row names the customer), so a
			// child cites no refusal id.
			child.refusalRowID = ""
		}
		if pending {
			if staff := s.approverStaffID(ctx, resolved.approver.PlatformUserID); staff != "" {
				for _, child := range plan.children {
					child.approverStaff = staff
				}
			}
		}
	}
	plan.actor = identity.StaffUserIDFromContext(ctx)
	return plan, nil
}

// ReturnAcross commits one credit note across several orders of one customer in ONE transaction:
// every order's per-sale lock taken in ascending order id before any line lock (two overlapping
// notes serialize on the shared lowest order), every child guarded and committed by the single
// return's own halves, the approval gate judging the NOTE total (Q3), the note header written ahead
// of its children so each child carries its note. Idempotent on the note's own key; a replay
// answers the committed note and writes nothing.
func (s *Service) ReturnAcross(ctx context.Context, in ReturnAcrossInput) (ReturnNote, error) {
	plan, err := s.prepareReturnAcross(ctx, in)
	if err != nil {
		return ReturnNote{}, err
	}
	if plan.preReplayed {
		return plan.preReplay, nil
	}
	var out ReturnNote
	var replayedHere, approvedHere bool
	// The two-transaction card refund (HH-690), the note's own shape: short tx 1 takes the
	// ascending per-sale locks, guards every child, judges the note gate and stages every card
	// child's attempt IN_FLIGHT, then COMMITS and releases every DB lock; the processor calls run
	// outside any DB transaction; short tx 2 (returnAcrossInTx below) books the note against the
	// staged reversal refs.
	if in.RefundMethod == RefundCard && !in.railOptional {
		err = s.store.RunInTx(ctx, func(tx pgx.Tx) error {
			var txErr error
			out, replayedHere, _, txErr = s.stageCardReturnAcrossInTx(ctx, tx, plan)
			return txErr
		})
		if err != nil {
			return s.returnAcrossFailed(ctx, plan, err)
		}
		if replayedHere {
			return out, nil
		}
		if derr := s.dispatchStagedCardRefunds(ctx, plan.children); derr != nil {
			return s.returnAcrossFailed(ctx, plan, derr)
		}
		out = ReturnNote{}
		replayedHere = false
	}
	err = s.store.RunInTx(ctx, func(tx pgx.Tx) error {
		var txErr error
		out, replayedHere, approvedHere, txErr = s.returnAcrossInTx(ctx, tx, plan)
		return txErr
	})
	if err == nil {
		s.settleCardRefunds(ctx, plan.children, nil)
	}
	if err != nil {
		return s.returnAcrossFailed(ctx, plan, err)
	}
	if replayedHere {
		return out, nil
	}
	s.recordNoteCompleted(ctx, plan, out, approvedHere)
	return out, nil
}

// returnAcrossFailed is the note's one failure path, shared by both transactions of the
// two-transaction card refund and the plain single-transaction body (the single return's
// returnFailed discipline, the note's own audit row).
func (s *Service) returnAcrossFailed(ctx context.Context, plan *returnAcrossPlan, err error) (ReturnNote, error) {
	// The approval refusal writes its own audit row naming the clerk, the customer and the
	// note's money asked for (the single path's own discipline, the note's own row).
	var refusal ErrRefundApprovalRequired
	if errors.As(err, &refusal) {
		// The note's refusal row names the clerk, the CUSTOMER (the note has no id before it
		// commits) and the money asked for, the single path's own audit discipline.
		s.record(ctx, audit.SensitiveAccess{
			Action: auditActionRefundApprovalRefused, EntityType: "pos.return_note", EntityID: plan.in.CustomerID,
			ActorID: plan.actor, BranchID: plan.children[0].oc.BranchID, Purpose: "POS_RETURN",
			Reason: refundApprovalRequiredError,
			Extra:  map[string]any{"amount_cents": refusal.RefundCents},
		})
		return ReturnNote{}, refusal
	}
	// A concurrent duplicate hit ux_return_notes_idem and rolled back: surface the winner.
	if isUniqueViolationErr(err) {
		if cached, found, ferr := s.replayNoteIfAllowed(ctx, plan.in.IdempotencyKey, plan.caller); ferr == nil && found {
			s.settleCardRefunds(ctx, plan.children, nil)
			return cached, nil
		}
	}
	s.settleCardRefunds(ctx, plan.children, err)
	return ReturnNote{}, err
}

// stageCardReturnAcrossInTx is short tx 1 of the note's two-transaction card refund (HH-690): the
// ascending per-sale locks, the in-tx replay re-check, every child's guard, the swap's applied
// distribution, the note-total gate and every card child's capture ceiling run HERE, each card
// child's attempt is staged IN_FLIGHT, and the transaction commits before any processor call.
func (s *Service) stageCardReturnAcrossInTx(ctx context.Context, tx pgx.Tx, plan *returnAcrossPlan) (ReturnNote, bool, bool, error) {
	in, caller := plan.in, plan.caller
	ordered := make([]*returnPlan, len(plan.children))
	copy(ordered, plan.children)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].oc.OrderID < ordered[j].oc.OrderID })
	for _, child := range ordered {
		if err := s.store.LockSaleForReturn(ctx, tx, child.oc.OrderID); err != nil {
			return ReturnNote{}, false, false, err
		}
	}
	cached, found, rerr := s.replayNoteIfAllowedTx(ctx, tx, in.IdempotencyKey, caller)
	if rerr != nil {
		return ReturnNote{}, false, false, rerr
	}
	if found {
		return cached, true, false, nil
	}
	guards := make([]guardedReturn, len(plan.children))
	var subtotal, taxTotal int64
	for i, child := range plan.children {
		g, gerr := s.guardReturnLines(ctx, tx, child.oc, child.in.Lines, child.in.Restock)
		if gerr != nil {
			return ReturnNote{}, false, false, gerr
		}
		guards[i] = g
		subtotal += g.subtotal
		taxTotal += g.taxTotal
	}
	noteTotal := subtotal + taxTotal
	// A swap (10.5): the same applied distribution the booking transaction runs, so each child's
	// staged rail amount is the amount tx 2 will book.
	if in.swap {
		if noteTotal != in.swapCredit {
			return ReturnNote{}, false, false, clientErr(http.StatusConflict,
				"pos: the credit changed while the swap was being rung (%s priced, %s now); review the swap and ring it again",
				centsWords(in.swapCredit), centsWords(noteTotal))
		}
		remaining := in.swapApplied
		for i, child := range plan.children {
			childTotal := guards[i].subtotal + guards[i].taxTotal
			child.applied = childTotal
			if child.applied > remaining {
				child.applied = remaining
			}
			remaining -= child.applied
		}
	}
	first := plan.children[0]
	priors := make([]int64, len(plan.children))
	shares := make([]int64, len(plan.children))
	for i, child := range plan.children {
		prior, perr := s.store.SumCommittedReturnTotalsTx(ctx, tx, child.oc.OrderID)
		if perr != nil {
			return ReturnNote{}, false, false, perr
		}
		priors[i], shares[i] = prior, guards[i].subtotal+guards[i].taxTotal
	}
	if _, gerr := s.judgeNoteGate(noteTotal, priors, shares, caller, first); gerr != nil {
		return ReturnNote{}, false, false, gerr
	}
	// D5 across the note, staged: every card child is judged against its own capture and staged
	// HERE, before any processor call, so a note that would cross one capture refuses whole with
	// the staging transaction rolled back (no attempt rows stand).
	staged := false
	for i, child := range plan.children {
		cardCents := guards[i].subtotal + guards[i].taxTotal - child.applied
		if child.applied == 0 {
			sp, serr := s.railSplitFor(ctx, tx, child.oc, RefundCard, cardCents)
			if serr != nil {
				return ReturnNote{}, false, false, serr
			}
			if sp.rail == 0 && sp.offset > 0 {
				return ReturnNote{}, false, false, clientErr(http.StatusUnprocessableEntity, "%s", sp.note)
			}
			cardCents = sp.rail
		}
		child.stagedRailTotal = cardCents
		if cardCents <= 0 {
			continue
		}
		if cerr := s.checkCardCapTx(ctx, tx, child.oc, "", "refund:"+child.in.IdempotencyKey, cardCents); cerr != nil {
			return ReturnNote{}, false, false, cerr
		}
		existing, err := s.store.StageCardRefundAttemptTx(ctx, tx, cardRefundFact{
			RefundKey: "refund:" + child.in.IdempotencyKey, OrderID: child.oc.OrderID, BranchID: child.oc.BranchID,
			CustomerID: child.oc.CustomerID, GatewayRef: child.oc.GatewayRef, AmountCents: cardCents,
		})
		if err != nil {
			return ReturnNote{}, false, false, err
		}
		child.stagedRefundRef = existing
		staged = true
	}
	return ReturnNote{}, false, staged, nil
}

// recordNoteCompleted writes the completion row of a committed note, one row for the one decision
// (the children's own trails are the rail's rows and journals themselves).
func (s *Service) recordNoteCompleted(ctx context.Context, plan *returnAcrossPlan, out ReturnNote, approvedHere bool) {
	in := plan.in
	completed := audit.SensitiveAccess{
		Action: audit.ActReturnCompleted, EntityType: "pos.return_note", EntityID: out.NoteID,
		ActorID: plan.actor, BranchID: out.BranchID, Purpose: "POS_RETURN", Reason: in.RefundMethod,
		Extra: map[string]any{"total_cents": out.TotalCents},
	}
	if approvedHere {
		if plan.children[0].approverStaff != "" {
			completed.Extra["approver_id"] = plan.children[0].approverStaff
		}
		completed.Extra["subject_ref"] = plan.children[0].decision.approver.PlatformUserID
		completed.Extra["amount_cents"] = out.TotalCents
		completed.Reason = refundApprovalRequiredError
	}
	s.record(ctx, completed)
}

// returnAcrossInTx is the note's transactional body: the ascending per-sale locks, the in-tx
// replay re-check on the note's key, every child's guard, the note-total gate, the note header,
// then every child's commit half.
func (s *Service) returnAcrossInTx(ctx context.Context, tx pgx.Tx, plan *returnAcrossPlan) (ReturnNote, bool, bool, error) {
	in := plan.in
	caller := plan.caller

	// Ascending per-sale locks BEFORE any line lock: two overlapping notes (orders A,B and B,A)
	// both start at the alphabetically first order id, so they serialize and both finish instead
	// of deadlocking. pg_advisory_xact_lock is reentrant inside one transaction, the children's
	// own lock calls below are no-ops.
	ordered := make([]*returnPlan, len(plan.children))
	copy(ordered, plan.children)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].oc.OrderID < ordered[j].oc.OrderID })
	for _, child := range ordered {
		if err := s.store.LockSaleForReturn(ctx, tx, child.oc.OrderID); err != nil {
			return ReturnNote{}, false, false, err
		}
	}

	// The note's children restock through the same seam the single return does: every child's
	// restock rows, one sorted pass over the union (the note is one branch, its own wall), taken
	// BEFORE the note's first counter draw, the single return's own stock-first rule. The swap is
	// the one caller that pre-locks itself: it takes the sorted union of BOTH its halves' products
	// ahead of this body (swap.go), one pass, so the body adds none there and the swap's pre-lock
	// stays the one call the review leg pins.
	if !in.swap {
		if err := s.lockRestockRowsTx(ctx, tx, ordered[0].oc.BranchID, noteRestockProducts(plan)); err != nil {
			return ReturnNote{}, false, false, err
		}
	}

	// Every invoice the note may touch, sorted, before any credit offset takes one and then the shared
	// invoice sequence row (P2-1).
	ocs := make([]returnOrderCtx, len(ordered))
	for i, child := range ordered {
		ocs[i] = child.oc
	}
	if !in.railOptional {
		// The single return's own counter rule (returns.go) holds for the note too, through the
		// same helper: lockReturnInvoicesTx draws the payments counter itself, before the rows, and
		// only when it will take any. A note that held an owed invoice and then waited on the
		// counter closed the cycle with a received payment apply (the delta reviews' P1), and a
		// note of fully paid sales with no deposit documents locks no invoice and must not hold the
		// counter at all (the delta reviews' P2).
		if err := s.lockReturnInvoicesTx(ctx, tx, ocs, in.RefundMethod != RefundAccountCredit); err != nil {
			return ReturnNote{}, false, false, err
		}
	}

	// The in-transaction same-key re-check on the NOTE's key, after the locks (a twin that waited
	// answers its sibling's committed note as the replay, no gate, no rail, no audit row).
	cached, found, rerr := s.replayNoteIfAllowedTx(ctx, tx, in.IdempotencyKey, caller)
	if rerr != nil {
		return ReturnNote{}, false, false, rerr
	}
	if found {
		return cached, true, false, nil
	}

	// Guard every child (its own lines under its own FOR UPDATE), summing the note.
	guards := make([]guardedReturn, len(plan.children))
	var subtotal, taxTotal int64
	for i, child := range plan.children {
		g, gerr := s.guardReturnLines(ctx, tx, child.oc, child.in.Lines, child.in.Restock)
		if gerr != nil {
			return ReturnNote{}, false, false, gerr
		}
		guards[i] = g
		subtotal += g.subtotal
		taxTotal += g.taxTotal
	}
	noteTotal := subtotal + taxTotal

	// A swap (10.5): the credit the swap was priced against must be exactly what the guard now
	// prices (a concurrent return of the same lines would change it), and the part spent on the new
	// order is taken from the children in order; the rest of each child goes out on the rail.
	if in.swap {
		if noteTotal != in.swapCredit {
			return ReturnNote{}, false, false, clientErr(http.StatusConflict,
				"pos: the credit changed while the swap was being rung (%s priced, %s now); review the swap and ring it again",
				centsWords(in.swapCredit), centsWords(noteTotal))
		}
		remaining := in.swapApplied
		for i, child := range plan.children {
			childTotal := guards[i].subtotal + guards[i].taxTotal
			child.applied = childTotal
			if child.applied > remaining {
				child.applied = remaining
			}
			remaining -= child.applied
		}
	}

	// The approval gate judges the NOTE total (the Q3 ruling), ahead of every write and every
	// rail, the single path's own refusal body and credential discipline.
	first := plan.children[0]
	// The gate judges the note total AND, per order, the sale's earlier committed returns plus this
	// note's share of it (read after the ascending locks, so exact): the single return's own
	// cumulative rule, so notes cannot walk one sale past the amount a slice at a time. Since the
	// rule is a threshold, the largest of these amounts decides.
	priors := make([]int64, len(plan.children))
	shares := make([]int64, len(plan.children))
	for i, child := range plan.children {
		prior, perr := s.store.SumCommittedReturnTotalsTx(ctx, tx, child.oc.OrderID)
		if perr != nil {
			return ReturnNote{}, false, false, perr
		}
		priors[i], shares[i] = prior, guards[i].subtotal+guards[i].taxTotal
	}
	approvedHere, gerr := s.judgeNoteGate(noteTotal, priors, shares, caller, first)
	if gerr != nil {
		return ReturnNote{}, false, false, gerr
	}

	// D5 across the note: every card child is judged against its own capture BEFORE any child's
	// rail runs, so a note that would cross one capture refuses whole without the processor having
	// heard of the others.
	if in.RefundMethod == RefundCard {
		for i, child := range plan.children {
			cardCents := guards[i].subtotal + guards[i].taxTotal - child.applied
			if child.applied == 0 {
				sp, serr := s.railSplitFor(ctx, tx, child.oc, RefundCard, cardCents)
				if serr != nil {
					return ReturnNote{}, false, false, serr
				}
				if sp.rail == 0 && sp.offset > 0 {
					return ReturnNote{}, false, false, clientErr(http.StatusUnprocessableEntity, "%s", sp.note)
				}
				cardCents = sp.rail
			}
			if cerr := s.checkCardCapTx(ctx, tx, child.oc, "", "refund:"+child.in.IdempotencyKey, cardCents); cerr != nil {
				return ReturnNote{}, false, false, cerr
			}
		}
	}

	// The note header ahead of its children: every child's row carries the note's id.
	noteID, noteNo, ierr := s.store.InsertReturnNote(ctx, tx, returnNoteRow{
		CustomerID: in.CustomerID, BranchID: first.oc.BranchID, RefundMethod: in.RefundMethod,
		SubtotalCents: subtotal, TaxCents: taxTotal, TotalCents: noteTotal,
		Reason: in.Reason, IdempotencyKey: in.IdempotencyKey, CreatedBy: plan.actor,
	})
	if ierr != nil {
		return ReturnNote{}, false, false, ierr
	}

	out := ReturnNote{
		NoteID: noteID, NoteNo: noteNo, CustomerID: in.CustomerID, BranchID: first.oc.BranchID,
		RefundMethod: in.RefundMethod, SubtotalCents: subtotal, TaxCents: taxTotal, TotalCents: noteTotal,
		Returns: []Return{},
	}
	for i, child := range plan.children {
		ret, cerr := s.commitReturnTx(ctx, tx, child, guards[i], noteID, approvedHere)
		if cerr != nil {
			return ReturnNote{}, false, false, cerr
		}
		out.Returns = append(out.Returns, ret)
	}
	return out, false, approvedHere, nil
}

// noteRestockProducts is the union of the note's children's restock products (one branch, the note's
// own wall), the set the note's body pre-locks, sorted, before its first counter draw.
func noteRestockProducts(plan *returnAcrossPlan) []string {
	seen := map[string]bool{}
	var out []string
	for _, child := range plan.children {
		for _, id := range child.restockProducts {
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// judgeNoteGate is the note's refund approval gate, ONE judgment for the transaction (exact, after the
// locks) and for the swap's pre-transaction look (before any card is charged): it judges the largest
// of the note total and, per order, the sale's earlier committed returns plus that order's share (a
// threshold rule, so the largest decides), with the single path's credential discipline. It answers
// whether a manager's decision stands behind the note, or the refusal.
func (s *Service) judgeNoteGate(noteTotal int64, priors, shares []int64, caller identity.StaffContext, first *returnPlan) (bool, error) {
	gateAmount := noteTotal
	for i := range priors {
		if c := priors[i] + shares[i]; c > gateAmount {
			gateAmount = c
		}
	}
	refusal, refused := s.refundApprovalRefusal(gateAmount, noteTotal, caller)
	if first.credentialJudged && s.roles.RefundRequiresApproval(gateAmount) {
		if first.judgeErr != nil {
			return false, first.judgeErr
		}
		if !first.decision.pending {
			refusal = s.standingRefusalFor(noteTotal)
			refused = true
		}
	}
	if refused {
		if !first.decision.pending {
			return false, refusal
		}
		return true, nil
	}
	return false, nil
}

// replayNoteIfAllowed answers a note replay only to a caller the wall admits to the replayed
// note's own branch (the replayIfAllowed discipline: the key is not a bearer of the answer).
func (s *Service) replayNoteIfAllowed(ctx context.Context, key string, caller identity.StaffContext) (ReturnNote, bool, error) {
	cached, found, err := s.store.FindReturnNoteByKey(ctx, key)
	if err != nil || !found {
		return ReturnNote{}, false, err
	}
	if err := requireBranchReach(caller, cached.BranchID); err != nil {
		return ReturnNote{}, false, err
	}
	cached.Replayed = true
	return cached, true, nil
}

// replayNoteIfAllowedTx is replayNoteIfAllowed on the open ReturnAcross transaction (the
// FindReturnByKeyTx discipline: no pool read while the transaction holds a connection).
func (s *Service) replayNoteIfAllowedTx(ctx context.Context, tx pgx.Tx, key string, caller identity.StaffContext) (ReturnNote, bool, error) {
	cached, found, err := s.store.FindReturnNoteByKeyTx(ctx, tx, key)
	if err != nil || !found {
		return ReturnNote{}, false, err
	}
	if err := requireBranchReach(caller, cached.BranchID); err != nil {
		return ReturnNote{}, false, err
	}
	cached.Replayed = true
	return cached, true, nil
}

// noteMayNeedApproval answers, from the note's requested lines without locks, whether the note
// COULD land where the dealer's configuration asks for an approver: the sum over the note's orders
// of the requested lines' original totals and tax, plus each order's earlier committed returns,
// against RefundRequiresApproval (the refundMayNeedApproval discipline, summed over the note).
func (s *Service) noteMayNeedApproval(ctx context.Context, plan *returnAcrossPlan) bool {
	var prospective int64
	for _, child := range plan.children {
		lines, err := s.store.ListReturnableLines(ctx, child.oc.OrderID)
		if err != nil {
			return true
		}
		prior, err := s.store.SumCommittedReturnTotals(ctx, child.oc.OrderID)
		if err != nil {
			return true
		}
		requested := make(map[string]bool, len(child.in.Lines))
		for _, ln := range child.in.Lines {
			requested[ln.OrderLineID] = true
		}
		for _, v := range lines {
			if requested[v.OrderLineID] {
				prospective += v.LineTotalCents + v.TaxCents
			}
		}
		prospective += prior
	}
	return s.roles.RefundRequiresApproval(prospective)
}
