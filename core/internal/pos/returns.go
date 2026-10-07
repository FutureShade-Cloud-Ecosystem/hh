package pos

// returns.go, the W7 POS return: idempotent, over-return-guarded, one atomic tx across the money rail
// (CASH drawer pay-out / CARD processor reversal / ACCOUNT_CREDIT), the optional restock (RETURN moves
// at the current weighted average through the pos-declared seam) and the two GL events. The till stays
// the SOLE writer of payments + drawer movements; inventory stays the sole stock writer.

import (
	"context"
	"errors"
	"math"
	"net/http"
	"strings"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
	"github.com/jackc/pgx/v5"
)

// qtyEpsilon absorbs DECIMAL(19,4) float round-trip noise when comparing quantities.
const qtyEpsilon = 1e-6

// reservedRequestKeyPrefix is the idempotency-key namespace ApproveReturnRequest derives its rail key
// from ("rreq:" + request id). It is RESERVED: a public return may never use it (a colliding key would
// hijack a PENDING request's replay slot and stamp the wrong money onto its decision).
const reservedRequestKeyPrefix = "rreq:"

// auditActionRefundApprovalRefused is the audit action of one return the
// till refused for want of an approval: the row names the clerk, the sale
// and the amount (D49).
const auditActionRefundApprovalRefused = "REFUND_APPROVAL_REFUSED"

// refundApprovalRequiredError is the error string the contract pins as the
// discriminator of the approval refusal (the 409 body's Error envelope;
// the approval key rides beside it, present exactly when this refusal is
// the one the till's screen shows the manager dialog for).
const refundApprovalRequiredError = "refund_approval_required"

// ErrRefundApprovalRequired refuses one return above the configured
// amount from a role that holds no approval: AmountCents is the
// configured amount without approval, Approvers the role keys that hold
// it, both as the configuration sets them (X7), and RefundCents is the
// refused refund's own total, the fact the audit row keeps. The handler
// maps it to the contract's 409.
type ErrRefundApprovalRequired struct {
	AmountCents int64
	Approvers   []string
	RefundCents int64
}

func (e ErrRefundApprovalRequired) Error() string {
	return "pos: the refund is above the approval amount and the caller's role holds no approval"
}

// restocker returns the injected restock seam, defaulting to the behavior-preserving no-op.
func (s *Service) restocker() StockRestocker {
	if s.restock != nil {
		return s.restock
	}
	return NoopRestock{}
}

// Return commits one atomic POS return against ONE original committed sale. Idempotent on the client
// key (ux_pos_returns_idem); the over-return guard runs per order line under FOR UPDATE inside the tx.
// Money: per-line refund = qty × original unit price (capped so Σ refunds never exceed the line total);
// tax refunds proportionally from the line's ORIGINAL tax_cents and Σ per line NEVER exceeds it.
//
// The transactional body is returnInTx, over a CALLER-SUPPLIED transaction: Return resolves everything
// that reads the pool first (prepareReturn, while no connection is held), then runs the body inside its
// own RunInTx. A caller already inside a transaction (ApproveReturnRequest) runs the same body on ITS
// transaction, so the money and the decision commit atomically, and no second acquisition ever asks the
// pool for a connection while one is held.
func (s *Service) Return(ctx context.Context, in ReturnInput) (Return, error) {
	plan, err := s.prepareReturn(ctx, in)
	if err != nil {
		return Return{}, err
	}
	// The pre-transaction replay check answered: the key already carries its committed return, judged
	// against the caller's reach of the replayed return's own sale by the same wall. No transaction,
	// no rail, no completion audit row.
	if plan.preReplayed {
		return plan.preReplay, nil
	}
	var out Return
	var replayedHere, approvedHere bool
	err = s.store.RunInTx(ctx, func(tx pgx.Tx) error {
		var txErr error
		out, replayedHere, approvedHere, txErr = s.returnInTx(ctx, tx, plan)
		return txErr
	})
	if err != nil {
		// The approval refusal writes its own audit row naming the clerk,
		// the sale and the amount (D49): the money asked for, refused at
		// the counter, is exactly the fact the trail exists to keep.
		var refusal ErrRefundApprovalRequired
		if errors.As(err, &refusal) {
			s.record(ctx, audit.SensitiveAccess{
				Action: auditActionRefundApprovalRefused, EntityType: "pos.return", EntityID: plan.oc.OrderID,
				ActorID: plan.actor, BranchID: plan.oc.BranchID, Purpose: "POS_RETURN",
				Reason: refundApprovalRequiredError,
				Extra:  map[string]any{"amount_cents": refusal.RefundCents},
			})
			return Return{}, refusal
		}
		// A concurrent duplicate hit ux_pos_returns_idem and rolled back, surface the winner (replay),
		// judged against the winner's own sale by the same wall.
		if isUniqueViolationErr(err) {
			if cached, found, ferr := s.replayIfAllowed(ctx, in.IdempotencyKey, plan.caller); ferr == nil && found {
				return cached, nil
			}
		}
		return Return{}, err
	}
	// The in-transaction duplicate answered its twin's replay: the twin's
	// own completion row already exists, this answer writes none.
	if replayedHere {
		return out, nil
	}

	completed := audit.SensitiveAccess{
		Action: audit.ActReturnCompleted, EntityType: "pos.return", EntityID: out.ReturnID,
		ActorID: plan.actor, BranchID: plan.oc.BranchID, Purpose: "POS_RETURN", Reason: in.RefundMethod,
		Extra: map[string]any{"return_no": out.ReturnNo, "total_cents": out.TotalCents},
	}
	if approvedHere {
		// The manager's decision, beside the clerk who rang it (row 482).
		// Both people are named in one identifier space, the linked staff
		// users id (the lead's P2-4 ruling: the platform subject is a
		// directory string the audit chokepoint may redact), and the
		// platform subject rides subject_ref as the directory reference.
		// approver_id, amount_cents and subject_ref are allowlisted audit
		// keys, so these are the facts the row keeps; amount_cents is the
		// audit vocabulary's own name for an integer money fact, here the
		// refused refund's total.
		if plan.approverStaff != "" {
			completed.Extra["approver_id"] = plan.approverStaff
		}
		completed.Extra["subject_ref"] = plan.decision.approver.PlatformUserID
		completed.Extra["amount_cents"] = out.TotalCents
		completed.Reason = refundApprovalRequiredError
	}
	s.record(ctx, completed)
	return out, nil
}

// returnPlan is everything the return resolves BEFORE any transaction
// opens: the validated input, the original sale's context, the caller and
// the approver facts. Every read here runs on the pool while no connection
// is held; the transactional body (returnInTx) touches only the transaction
// it is handed. A caller already inside its own transaction
// (ApproveReturnRequest) can therefore run the rail on ITS transaction:
// asking the pool for a second connection while the first is held is the
// starvation a pool of pool-size concurrent rails dies of, every holder
// waiting for a connection the pool can never give.
type returnPlan struct {
	in             ReturnInput
	oc             returnOrderCtx
	caller         identity.StaffContext
	actor          string
	pickupRequired bool
	pickupStatus   string
	pickupSite     string
	pickupDate     string
	decision       approverDecision
	judgeErr       error
	// credentialJudged records that a presented credential went through the
	// resolver, whatever it answered: the in-transaction gate consults the
	// verdict only when the CUMULATIVE actually crosses the amount.
	credentialJudged bool
	approverStaff    string
	refusalRowID     string
	// preReplay carries the committed return the pre-transaction replay
	// check answered: set means no transaction and no rail body at all, the
	// caller receives this return (Return) or stamps it as the decision
	// (ApproveReturnRequest).
	preReplay   Return
	preReplayed bool
}

// prepareReturn is the return's pre-transaction section, run while no
// connection is held: validation, the sale's context, the branch wall, the
// replay pre-check, the approver resolution and the decision-record names.
// It returns the plan the transactional body consumes.
func (s *Service) prepareReturn(ctx context.Context, in ReturnInput) (*returnPlan, error) {
	plan := &returnPlan{in: in}
	if in.IdempotencyKey == "" {
		return nil, errMissingKey
	}
	// The "rreq:" namespace belongs to the return-request approval rail alone (see the const above).
	if !in.internalRail && strings.HasPrefix(in.IdempotencyKey, reservedRequestKeyPrefix) {
		return nil, errReservedKey
	}
	if len(in.Lines) == 0 {
		return nil, errNoReturnLines
	}
	switch in.RefundMethod {
	case RefundCash, RefundCard, RefundAccountCredit:
	default:
		return nil, errBadRefundMethod
	}
	if in.OrderID == "" && in.OrderNo == "" {
		return nil, clientErr(http.StatusBadRequest, "pos: order_id or order_no is required")
	}
	for _, ln := range in.Lines {
		if ln.OrderLineID == "" || ln.QtyBase <= 0 {
			return nil, clientErr(http.StatusUnprocessableEntity, "pos: every return line needs an order_line_id and a positive qty_base")
		}
	}
	// A-06 restocking fee: bounded bps, ACCOUNT_CREDIT only (the fee reduces the credited amount, a
	// cash/card refund never nets a fee here).
	if in.FeeBps < 0 || in.FeeBps > 10000 {
		return nil, errBadFeeBps
	}
	if in.FeeBps > 0 && in.RefundMethod != RefundAccountCredit {
		return nil, errFeeNeedsCredit
	}

	// The original sale: must be a committed POS sale; the return runs at ITS branch.
	oc, found, err := s.store.ReturnOrderContext(ctx, in.OrderID, normalizeSaleRef(in.OrderNo))
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, errReturnNotFound
	}
	plan.oc = oc
	// The branch wall runs BEFORE any rail, before any write and before the
	// idempotent replay (X2, fixing HH-218; the lead's P2-2 ruling): no
	// processor reversal, no drawer payout and no account credit may move
	// for a sale the caller's resolved authority does not reach, and the
	// replay of a foreign sale refuses exactly like the first attempt, so
	// another branch's return number, sale, invoice, payment id and money
	// are not a thing a key alone buys. The return runs at the sale's
	// branch; the caller brings their own grants, resolved per request by
	// the guards, never claimed.
	caller, err := s.caller(ctx)
	if err != nil {
		return nil, err
	}
	plan.caller = caller
	if err := requireBranchReach(caller, plan.oc.BranchID); err != nil {
		return nil, err
	}
	if cached, found, err := s.replayIfAllowed(ctx, in.IdempotencyKey, caller); err != nil {
		return nil, err
	} else if found {
		plan.preReplay = cached
		plan.preReplayed = true
		return plan, nil
	}
	if in.RefundMethod == RefundCash && in.TillSessionID == "" {
		return nil, ErrNoOpenTill{}
	}
	if in.RefundMethod == RefundCard {
		if plan.oc.SaleTender != TenderCard || plan.oc.GatewayRef == "" {
			return nil, errCardRefundNoRef
		}
		if s.cardRefund == nil {
			return nil, errNoCardRefunder
		}
	}

	// The gated paths fail closed (the lead's P1-1 ruling): a counter
	// return on a service built with no role configuration refuses, it
	// never skips the refund rule. The A-06 portal rail keeps its
	// exemption: the queue's own decision already approved that request.
	//
	// The approver credential resolves BEFORE the transaction opens (the
	// lead's P3-6 ruling): no verifier call, membership read or authority
	// read ever runs while the sale's lines are held for update; only the
	// comparison against the computed total stays inside. A presented
	// credential is judged wherever it rides: a return that ends up under
	// the amount commits ordinarily, a credential that cannot be judged
	// refuses on its own family with its audit row.
	if !in.internalRail {
		if s.roles == nil {
			return nil, errRoleConfigUnwired()
		}
		if in.ApproverCredential != "" && s.refundMayNeedApproval(ctx, plan.oc, in) {
			resolved, pending, rerr := s.resolveRefundApprover(ctx, in.ApproverCredential, caller, plan.oc)
			plan.decision = resolved
			plan.decision.pending = pending
			plan.judgeErr = rerr
			plan.credentialJudged = true
		}
	}
	// The decision record's names, read before the transaction opens (the
	// lead's P3-D ruling: no trail read under the sale's row locks).
	if plan.decision.pending {
		plan.approverStaff = s.approverStaffID(ctx, plan.decision.approver.PlatformUserID)
		plan.refusalRowID = s.latestRefusalRowID(ctx, plan.oc.OrderID)
	}

	if in.Pickup != nil && in.Pickup.Required {
		plan.pickupRequired = true
		plan.pickupStatus = PickupRequested
		plan.pickupSite = in.Pickup.Site
		plan.pickupDate = in.Pickup.RequestedDate
	} else {
		plan.pickupStatus = PickupNone
	}

	plan.actor = identity.StaffUserIDFromContext(ctx)
	return plan, nil
}

// returnInTx is the return's transactional body over a caller-supplied
// transaction: the per-sale lock, the in-transaction replay re-check, the
// line locks and guards, the money rail, the restock and the two GL events,
// every database touch on tx, none on the pool. Return runs it inside its
// own RunInTx; ApproveReturnRequest runs it on its decision transaction, so
// the money and the decision commit atomically. It reports the two facts
// the post-transaction audit reads: whether the answer is a same-key twin's
// replay (no completion row) and whether the rule fired and a presented
// credential was spent (the completion row names the decision).
func (s *Service) returnInTx(ctx context.Context, tx pgx.Tx, plan *returnPlan) (out Return, replayedHere, approvedHere bool, err error) {
	// The plan's facts, aliased to the names the body has always read.
	in := plan.in
	oc := plan.oc
	caller := plan.caller
	actor := plan.actor
	pickupRequired, pickupStatus, pickupSite, pickupDate := plan.pickupRequired, plan.pickupStatus, plan.pickupSite, plan.pickupDate
	decision := plan.decision
	judgeErr := plan.judgeErr
	credentialJudged := plan.credentialJudged
	approverStaff := plan.approverStaff
	refusalRowID := plan.refusalRowID

	// Serialize every return of the sale before any line lock: the line
	// locks below serialize only returns of the SAME line, two concurrent
	// returns of DIFFERENT lines of one sale would each read the prior
	// sum before the other commits and both slip under the refund
	// approval amount. Every Return takes it, the A-06 portal rail
	// included: its committed rows must count in later sums too. The
	// lock is transaction-scoped, released at commit or rollback.
	if err := s.store.LockSaleForReturn(ctx, tx, oc.OrderID); err != nil {
		return Return{}, false, false, err
	}
	// A same-key duplicate that arrived while its twin held the lock
	// passed the pre-transaction replay check before either row existed.
	// The lock has now serialized them, so whatever is committed under
	// this key is final: the duplicate answers it here as the replay,
	// the same shape as the pre-transaction check (the cached return,
	// judged against its own sale by the branch wall), committing this
	// now-empty transaction with no gate, no rail and no audit row.
	// Without the re-check the duplicate's own gate would read its
	// twin's committed row into the prior sum and answer a false
	// refusal (or trip the over-return guard) for a return that
	// committed. The re-check reads ON the transaction (replayIfAllowedTx):
	// a pool read while this transaction holds a connection would
	// deadlock a full pool of Returns waiting on the sale's lock.
	cached, found, rerr := s.replayIfAllowedTx(ctx, tx, in.IdempotencyKey, caller)
	if rerr != nil {
		return Return{}, false, false, rerr
	}
	if found {
		out = cached
		replayedHere = true
		return out, replayedHere, approvedHere, nil
	}

	// 1. Lock + guard every requested line, derive its refund money/tax.
	lines := make([]returnLineRow, 0, len(in.Lines))
	var subtotal, taxTotal int64
	stockLines := make([]StockLine, 0, len(in.Lines))
	eventLines := make([]map[string]any, 0, len(in.Lines))
	for _, req := range in.Lines {
		rl, ok, lerr := s.store.LockReturnableLine(ctx, tx, oc.OrderID, req.OrderLineID)
		if lerr != nil {
			return Return{}, false, false, lerr
		}
		if !ok {
			return Return{}, false, false, clientErr(http.StatusUnprocessableEntity, "pos: line %s does not belong to order %s", req.OrderLineID, oc.OrderNo)
		}
		remainingQty := rl.QtySold - rl.PriorQty
		if req.QtyBase > remainingQty+qtyEpsilon {
			return Return{}, false, false, clientErr(http.StatusUnprocessableEntity,
				"pos: over-return on line %s, %.4f requested but only %.4f returnable", req.OrderLineID, req.QtyBase, remainingQty)
		}
		var money, tax int64
		if math.Abs(req.QtyBase-remainingQty) <= qtyEpsilon {
			// Returning everything still out: pay out exactly what remains so the line zeroes.
			money = rl.LineTotalCents - rl.PriorMoney
			tax = rl.TaxCents - rl.PriorTax
		} else {
			// Partial return: PROPORTIONAL by base qty, mirroring the tax proration below.
			// qty_base is base-UOM while unit_price_cents is per PRICE-UOM, so qty × unit price
			// mixes units whenever they differ (e.g. 1000 SF entered / 4100 PC base / 300¢ per SF)
			// and over-refunds up to the cap. line_total / qty_sold is unit-consistent by
			// construction; round half-up like the tax line, capped at the remaining un-refunded
			// line money so cumulative partials can never exceed line_total.
			if rl.QtySold > 0 {
				money = int64(math.Round(float64(rl.LineTotalCents) * req.QtyBase / rl.QtySold))
			}
			if rem := rl.LineTotalCents - rl.PriorMoney; money > rem {
				money = rem
			}
			if rl.QtySold > 0 {
				tax = int64(math.Round(float64(rl.TaxCents) * req.QtyBase / rl.QtySold))
			}
			if rem := rl.TaxCents - rl.PriorTax; tax > rem {
				tax = rem
			}
		}
		if money < 0 || tax < 0 {
			return Return{}, false, false, clientErr(http.StatusUnprocessableEntity, "pos: line %s has nothing left to refund", req.OrderLineID)
		}
		// Money invariant (never weakened): Σ refunded tax per order line ≤ the original line tax.
		if rl.PriorTax+tax > rl.TaxCents {
			return Return{}, false, false, clientErr(http.StatusUnprocessableEntity, "pos: tax refund would exceed the original line tax on %s", req.OrderLineID)
		}
		lines = append(lines, returnLineRow{
			OrderLineID: req.OrderLineID, ProductID: rl.ProductID, QtyBase: req.QtyBase,
			UnitPriceCents: rl.UnitPriceCents, LineTotalCents: money, TaxCents: tax, ReasonCode: req.ReasonCode,
		})
		subtotal += money
		taxTotal += tax
		stockLines = append(stockLines, StockLine{ProductID: rl.ProductID, QtyBase: req.QtyBase})
		eventLines = append(eventLines, map[string]any{"product_id": rl.ProductID, "qty_base": req.QtyBase})
	}
	total := subtotal + taxTotal

	// The refund rule reads the dealer's configuration (X7) and counts
	// every return against the same sale together: the sale's earlier
	// committed returns, read inside this transaction after the per-sale
	// lock above so the prior total is exact, ride the comparison, and
	// the gate judges the CUMULATIVE. A return above the configured
	// amount from a role not on the approvers list is refused HERE,
	// after every line is guarded and before any write and any rail, so
	// the refusal commits nothing and touches nobody; returns each under
	// the amount can no longer walk a sale out of the drawer one
	// approved-sized slice at a time. At or under the amount no approver
	// is ever asked. The A-06 portal rail skips this gate: the queue's
	// own decision already approved that request, and the counter rule
	// is not a second veto over it.
	//
	// The manager's credential at the clerk's till (row 482): the same
	// return carried again with the approver's credential in its header
	// commits as the manager's decision when the credential qualified
	// in the pre-transaction resolution (approver.go); anything short of
	// that leaves the standing refusal in place.
	if !in.internalRail {
		prior, err := s.store.SumCommittedReturnTotalsTx(ctx, tx, oc.OrderID)
		if err != nil {
			return Return{}, false, false, err
		}
		cumulative := prior + total
		refusal, refused := s.refundApprovalRefusal(cumulative, total, caller)
		// Above the amount, a presented credential must qualify,
		// whoever carried it (the lead's P2-B ruling): a failed
		// judgment refuses on its own family, a credential that does
		// not qualify refuses with the contract's body, the configured
		// amount and the configured approvers, never null and never
		// zero. Within the amount nothing here stirs: the header is
		// inert, and under no limit (RefundRequiresApproval folding the
		// switch in) it never stirs at all.
		if credentialJudged && s.roles.RefundRequiresApproval(cumulative) {
			if judgeErr != nil {
				return Return{}, false, false, judgeErr
			}
			if !decision.pending {
				refusal = s.standingRefusalFor(total)
				refused = true
			}
		}
		if refused {
			if !decision.pending {
				return Return{}, false, false, refusal
			}
			approvedHere = true
		}
	}

	// A-06 restocking fee, derived from the ACTUAL committed subtotal (round half-up) so the fee is
	// exact under partial/capped refunds. fee ≤ subtotal ≤ total by construction (bps ≤ 10000), so the
	// credited amount (total − fee) can never go negative.
	var feeCents int64
	if in.FeeBps > 0 {
		feeCents = (subtotal*int64(in.FeeBps) + 5000) / 10000
	}

	// 2. Header first, ux_pos_returns_idem makes a concurrent duplicate abort the whole tx before
	//    any rail write.
	returnID, returnNo, ierr := s.store.InsertReturn(ctx, tx, returnRow{
		OrderID: oc.OrderID, InvoiceID: oc.InvoiceID, CustomerID: oc.CustomerID, BranchID: oc.BranchID,
		RefundMethod: in.RefundMethod, SubtotalCents: subtotal, TaxCents: taxTotal, TotalCents: total,
		FeeCents: feeCents, Reason: in.Reason, Restock: in.Restock, TillSessionID: in.TillSessionID,
		PickupRequired: pickupRequired, PickupSite: pickupSite, PickupDate: pickupDate, PickupStatus: pickupStatus,
		IdempotencyKey: in.IdempotencyKey, CreatedBy: actor,
	})
	if ierr != nil {
		return Return{}, false, false, ierr
	}
	if err := s.store.InsertReturnLines(ctx, tx, returnID, lines); err != nil {
		return Return{}, false, false, err
	}

	// 3. The refund rail (all writes behind the till/payment seams; nothing here touches their tables).
	var refundPaymentID string
	switch in.RefundMethod {
	case RefundCash:
		res, err := s.tender.RefundTender(ctx, tx, till.RefundInput{
			TillSessionID: in.TillSessionID, Method: till.MethodCash, AmountCents: total,
			BranchID: oc.BranchID, CustomerID: oc.CustomerID, Reference: returnNo,
			IdempotencyKey: "refund:" + in.IdempotencyKey,
		})
		if err != nil {
			return Return{}, false, false, clientErr(http.StatusConflict, "%s", err.Error())
		}
		refundPaymentID = res.PaymentID
	case RefundCard:
		// The reversal rides THIS transaction (the CardRefunder seam's
		// discipline): a pool read while the Return transaction holds a
		// connection has the per-sale lock's holder wait for a second
		// connection every other connection is a Return waiter that needs
		// the holder's lock, deadlocking a full pool.
		refundRef, err := s.cardRefund.RefundCard(ctx, tx, oc.GatewayRef, total, returnNo, "refund:"+in.IdempotencyKey)
		if err != nil {
			return Return{}, false, false, clientErr(http.StatusBadGateway, "pos: card refund failed, %s", err.Error())
		}
		res, err := s.tender.RefundTender(ctx, tx, till.RefundInput{
			Method: till.MethodCard, AmountCents: total, BranchID: oc.BranchID, CustomerID: oc.CustomerID,
			GatewayRef: refundRef, Reference: returnNo, IdempotencyKey: "refund:" + in.IdempotencyKey,
		})
		if err != nil {
			return Return{}, false, false, clientErr(http.StatusConflict, "%s", err.Error())
		}
		refundPaymentID = res.PaymentID
	case RefundAccountCredit:
		// The restocking fee reduces what the customer's account is credited (total − fee); the fee
		// itself books through the GL event's fee_cents, never a subledger row.
		if err := s.applyAccountCredit(ctx, tx, oc, total-feeCents); err != nil {
			return Return{}, false, false, err
		}
	}
	if refundPaymentID != "" {
		if err := s.store.SetReturnRefundPayment(ctx, tx, returnID, refundPaymentID); err != nil {
			return Return{}, false, false, err
		}
	}

	// 4. Restock, RETURN moves at the current weighted average through the seam, then the stock-side
	//    GL event (mirrors the Sale's decrement + order.fulfilled pairing; skips quietly when no cost).
	if in.Restock {
		if err := s.restocker().RestockForReturnTx(ctx, tx, RestockInput{
			ReturnID: returnID, OrderID: oc.OrderID, BranchID: oc.BranchID, ReturnNo: returnNo, Lines: stockLines,
		}); err != nil {
			return Return{}, false, false, err
		}
		if err := s.store.EmitEvent(ctx, tx, "order.returned", map[string]any{
			"order_id": oc.OrderID, "return_id": returnID, "branch_id": oc.BranchID, "lines": eventLines,
		}); err != nil {
			return Return{}, false, false, err
		}
	}

	// 5. The money-side GL event (DR revenue + DR HST / CR the refund rail). A committed
	//    manager decision names the decision in the record: the approver who decided and the
	//    clerk who rang it in one identifier space (the linked staff users ids, the lead's
	//    P3-3 ruling), the platform subject beside them as the directory reference, and the
	//    refusal the decision answers, cited by its own audit row id where the trail can
	//    name one (row 482, D49).
	committed := map[string]any{
		"return_id": returnID, "order_id": oc.OrderID, "invoice_id": oc.InvoiceID,
		"refund_method": in.RefundMethod, "subtotal_cents": subtotal, "tax_cents": taxTotal,
		"total_cents": total, "fee_cents": feeCents, "branch_id": oc.BranchID,
	}
	if approvedHere {
		committed["decided_by"] = approverStaff
		committed["decided_by_subject"] = decision.approver.PlatformUserID
		committed["decided_by_role"] = decision.approver.RoleID
		committed["rang_by"] = actor
		committed["approval"] = map[string]any{
			"refusal":      refundApprovalRequiredError,
			"refund_cents": total,
		}
		if refusalRowID != "" {
			committed["approval"].(map[string]any)["refusal_id"] = refusalRowID
		}
	}
	if err := s.store.EmitEvent(ctx, tx, "pos.return.committed", committed); err != nil {
		return Return{}, false, false, err
	}

	out = Return{
		ReturnID: returnID, ReturnNo: returnNo, OrderID: oc.OrderID, InvoiceID: oc.InvoiceID,
		RefundMethod: in.RefundMethod, SubtotalCents: subtotal, TaxCents: taxTotal, TotalCents: total,
		FeeCents: feeCents, RefundPaymentID: refundPaymentID, PickupStatus: pickupStatus, Status: "COMMITTED",
	}
	return out, replayedHere, approvedHere, nil
}

// replayIfAllowed answers an idempotent replay only to a caller the wall
// admits to the REPLAYED return's own sale (the lead's P2-2R ruling): the
// key is not a bearer of the answer, so the branch reach is judged against
// the cached return's sale, and a foreign clerk refuses identically to a
// first attempt whatever sale their own request body names.
func (s *Service) replayIfAllowed(ctx context.Context, key string, caller identity.StaffContext) (Return, bool, error) {
	cached, found, err := s.store.FindReturnByKey(ctx, key)
	if err != nil || !found {
		return Return{}, false, err
	}
	oc, found, err := s.store.ReturnOrderContext(ctx, cached.OrderID, "")
	if err != nil {
		return Return{}, false, err
	}
	if !found {
		return Return{}, false, errReturnNotFound
	}
	if err := requireBranchReach(caller, oc.BranchID); err != nil {
		return Return{}, false, err
	}
	cached.Replayed = true
	return cached, true, nil
}

// replayIfAllowedTx is replayIfAllowed on the open Return transaction: the
// in-transaction same-key re-check reads through the transaction's own
// connection, never through the pool. A Return transaction holds one
// connection while it waits on the per-sale advisory lock; a pool read
// there would have the lock holder wait for a second connection every
// other connection is a waiter that needs the holder's lock, and a full
// pool deadlocks until the context ends.
func (s *Service) replayIfAllowedTx(ctx context.Context, tx pgx.Tx, key string, caller identity.StaffContext) (Return, bool, error) {
	cached, found, err := s.store.FindReturnByKeyTx(ctx, tx, key)
	if err != nil || !found {
		return Return{}, false, err
	}
	oc, found, err := s.store.ReturnOrderContextTx(ctx, tx, cached.OrderID, "")
	if err != nil {
		return Return{}, false, err
	}
	if !found {
		return Return{}, false, errReturnNotFound
	}
	if err := requireBranchReach(caller, oc.BranchID); err != nil {
		return Return{}, false, err
	}
	cached.Replayed = true
	return cached, true, nil
}

// standingRefusalFor builds the contract's refusal for one computed
// return: the configured threshold and the configured approver roles,
// never null and never zero, whatever the refund's own total (the lead's
// P2-B ruling).
func (s *Service) standingRefusalFor(totalCents int64) ErrRefundApprovalRequired {
	return ErrRefundApprovalRequired{
		AmountCents: s.roles.RefundAmountWithoutApprovalCents(),
		Approvers:   s.roles.RefundApprovers(),
		RefundCents: totalCents,
	}
}

// refundMayNeedApproval answers, from the sale's lines without locks,
// whether the requested return COULD land the sale's CUMULATIVE refunds
// where the dealer's configuration asks for an approver: the sale's earlier
// committed returns (the pool read) plus the sum over the requested lines
// of their original totals and tax, which over-approximates every remaining
// refund, against RefundRequiresApproval, the one rule that folds the limit
// switch in. A false answer makes the credential header inert (nothing
// needs authorization, nothing is consulted) and stays trustworthy: if the
// prior committed total plus the requested lines' original totals sits at
// or under the amount, the cumulative cannot cross it, and with no limit
// configured nothing ever crosses it. A true one is only a prospect, the
// transaction's own cumulative decides. An unreadable read judges the
// credential: fail closed.
func (s *Service) refundMayNeedApproval(ctx context.Context, oc returnOrderCtx, in ReturnInput) bool {
	lines, err := s.store.ListReturnableLines(ctx, oc.OrderID)
	if err != nil {
		return true
	}
	prior, err := s.store.SumCommittedReturnTotals(ctx, oc.OrderID)
	if err != nil {
		return true
	}
	requested := make(map[string]bool, len(in.Lines))
	for _, ln := range in.Lines {
		requested[ln.OrderLineID] = true
	}
	var prospective int64
	for _, v := range lines {
		if requested[v.OrderLineID] {
			prospective += v.LineTotalCents + v.TaxCents
		}
	}
	return s.roles.RefundRequiresApproval(prior + prospective)
}

// refundApprovalRefusal answers the configured approval rule (X7) for one
// return against its sale's running total: ok false when the CUMULATIVE of
// the sale's earlier committed returns and this return sits at or under the
// amount, when the caller's role holds the approval, or when the dealer
// configured no limit at all (the Dibbits ruling: RefundRequiresApproval
// folds the switch in, so no refund ever asks). The refusal names the
// configured amount and the configured approver roles, and RefundCents
// is the NEW return's own total, the money asked for at the counter, never
// the cumulative: the audit row's amount_cents keeps meaning the money
// asked for, refused at the counter. The caller has already refused a
// service built with no configuration (the nil gate fails closed); the
// configured amount is integer cents and the comparison is integer against
// integer; a role outside the configuration's vocabulary is on no approvers
// list, fail closed.
func (s *Service) refundApprovalRefusal(cumulativeCents, newTotalCents int64, caller identity.StaffContext) (ErrRefundApprovalRequired, bool) {
	amount := s.roles.RefundAmountWithoutApprovalCents()
	if !s.roles.RefundRequiresApproval(cumulativeCents) {
		return ErrRefundApprovalRequired{}, false
	}
	approved := false
	if role, ok := canonicalRole(caller); ok {
		if holds, err := s.roles.ApprovesRefundAboveAmount(role); err == nil {
			approved = holds
		}
	}
	if approved {
		return ErrRefundApprovalRequired{}, false
	}
	return ErrRefundApprovalRequired{
		AmountCents: amount,
		Approvers:   s.roles.RefundApprovers(),
		RefundCents: newTotalCents,
	}, true
}

// applyAccountCredit books the ACCOUNT_CREDIT rail: offset the original invoice first (floor 0; PAID
// only when fully offset), then carry any remainder as a store credit. The customer subledger drops by
// the FULL total either way (balance_after = open − total) and the AR view stays exact: the offset part
// rides the invoice balance (its memo row is source_type 'invoice', excluded from the non-invoice
// union), the remainder is a counted non-invoice CREDIT_MEMO. txn_type CREDIT_MEMO is the 0009 CHECK's
// vocabulary for the design's "CREDIT" row.
func (s *Service) applyAccountCredit(ctx context.Context, tx pgx.Tx, oc returnOrderCtx, total int64) error {
	// The customer read rides THIS transaction (ReadCustomerTx, the
	// FindReturnByKeyTx discipline): a pool read while the Return
	// transaction holds its connection has the per-sale lock's holder wait
	// on a second connection every other connection is a Return waiter
	// that needs the holder's lock, deadlocking a full pool.
	cust, err := s.store.ReadCustomerTx(ctx, tx, oc.CustomerID)
	if err != nil {
		return err
	}
	var applied int64
	if oc.InvoiceStatus == "FINALIZED" || oc.InvoiceStatus == "PARTIALLY_PAID" {
		applied, err = s.store.ApplyCreditToInvoice(ctx, tx, oc.InvoiceID, total)
		if err != nil {
			return err
		}
	}
	open := cust.OpenBalanceCents
	if applied > 0 {
		if err := s.store.InsertCustomerTxn(ctx, tx, customerTxnRow{
			CustomerID: oc.CustomerID, TxnType: "CREDIT_MEMO", AmountCents: -applied,
			BalanceAfter: open - applied, SourceType: "invoice", SourceRefID: oc.InvoiceID, BranchID: oc.BranchID,
		}); err != nil {
			return err
		}
	}
	if remainder := total - applied; remainder > 0 {
		if err := s.store.InsertCustomerTxn(ctx, tx, customerTxnRow{
			CustomerID: oc.CustomerID, TxnType: "CREDIT_MEMO", AmountCents: -remainder,
			BalanceAfter: open - total, SourceType: "pos_return", SourceRefID: oc.OrderID, BranchID: oc.BranchID,
		}); err != nil {
			return err
		}
	}
	return nil
}

// normalizeSaleRef is the typed sale reference as the lookup compares it: trimmed, upper cased, and
// without a leading hash, so "#SO-TRT-21", "so-trt-21", " SO-TRT-21 " and "INV-17" all reach the
// same comparison (the stored numbers carry the hash; the query adds it back).
func normalizeSaleRef(raw string) string {
	return strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(raw)), "#")
}

// LookupReturn is the pre-return read: the original committed sale + its lines with prior-return
// sums netted, so the counter can build the per-line qty entry. Read-only, the commit re-guards.
// The branch wall answers before the lines are read (X2): another branch's sale is refused, its
// returnable quantities are not a thing this counter gets to browse.
func (s *Service) LookupReturn(ctx context.Context, orderID, orderNo string) (ReturnLookup, error) {
	if orderID == "" && orderNo == "" {
		return ReturnLookup{}, clientErr(http.StatusBadRequest, "pos: order_id or order_no is required")
	}
	oc, found, err := s.store.ReturnOrderContext(ctx, orderID, normalizeSaleRef(orderNo))
	if err != nil {
		return ReturnLookup{}, err
	}
	if !found {
		return ReturnLookup{}, errReturnNotFound
	}
	caller, err := s.caller(ctx)
	if err != nil {
		return ReturnLookup{}, err
	}
	if err := requireBranchReach(caller, oc.BranchID); err != nil {
		return ReturnLookup{}, err
	}
	lines, err := s.store.ListReturnableLines(ctx, oc.OrderID)
	if err != nil {
		return ReturnLookup{}, err
	}
	return ReturnLookup{
		OrderID: oc.OrderID, OrderNo: oc.OrderNo, BranchID: oc.BranchID, CustomerID: oc.CustomerID,
		InvoiceID: oc.InvoiceID, SaleTender: oc.SaleTender,
		CardRefundable: oc.SaleTender == TenderCard && oc.GatewayRef != "",
		Lines:          lines,
	}, nil
}

// ListReturnPickups is the logistics hand-off surface: REQUESTED/SCHEDULED pickups for a branch.
func (s *Service) ListReturnPickups(ctx context.Context, branchID string) ([]ReturnPickup, error) {
	if branchID == "" {
		return nil, clientErr(http.StatusBadRequest, "pos: branch_id is required")
	}
	return s.store.ListReturnPickups(ctx, branchID)
}

// AdvancePickup moves a return's pickup along REQUESTED → SCHEDULED → DONE (manager route). Any other
// transition is a 409, the queue only ever walks forward.
func (s *Service) AdvancePickup(ctx context.Context, returnID, to string) (ReturnPickup, error) {
	if returnID == "" {
		return ReturnPickup{}, errBadRequest
	}
	cur, found, err := s.store.GetReturnPickup(ctx, returnID)
	if err != nil {
		return ReturnPickup{}, err
	}
	if !found {
		return ReturnPickup{}, clientErr(http.StatusNotFound, "pos: return not found")
	}
	valid := (cur.PickupStatus == PickupRequested && to == PickupScheduled) ||
		(cur.PickupStatus == PickupScheduled && to == PickupDone)
	if !valid {
		return ReturnPickup{}, clientErr(http.StatusConflict, "pos: pickup cannot move %s → %s", cur.PickupStatus, to)
	}
	if err := s.store.SetPickupStatus(ctx, returnID, cur.PickupStatus, to); err != nil {
		return ReturnPickup{}, err
	}
	cur.PickupStatus = to
	return cur, nil
}
