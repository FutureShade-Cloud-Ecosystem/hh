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
func (s *Service) Return(ctx context.Context, in ReturnInput) (Return, error) {
	if in.IdempotencyKey == "" {
		return Return{}, errMissingKey
	}
	// The "rreq:" namespace belongs to the return-request approval rail alone (see the const above).
	if !in.internalRail && strings.HasPrefix(in.IdempotencyKey, reservedRequestKeyPrefix) {
		return Return{}, errReservedKey
	}
	if len(in.Lines) == 0 {
		return Return{}, errNoReturnLines
	}
	switch in.RefundMethod {
	case RefundCash, RefundCard, RefundAccountCredit:
	default:
		return Return{}, errBadRefundMethod
	}
	if in.OrderID == "" && in.OrderNo == "" {
		return Return{}, clientErr(http.StatusBadRequest, "pos: order_id or order_no is required")
	}
	for _, ln := range in.Lines {
		if ln.OrderLineID == "" || ln.QtyBase <= 0 {
			return Return{}, clientErr(http.StatusUnprocessableEntity, "pos: every return line needs an order_line_id and a positive qty_base")
		}
	}
	// A-06 restocking fee: bounded bps, ACCOUNT_CREDIT only (the fee reduces the credited amount, a
	// cash/card refund never nets a fee here).
	if in.FeeBps < 0 || in.FeeBps > 10000 {
		return Return{}, errBadFeeBps
	}
	if in.FeeBps > 0 && in.RefundMethod != RefundAccountCredit {
		return Return{}, errFeeNeedsCredit
	}

	// The original sale: must be a committed POS sale; the return runs at ITS branch.
	oc, found, err := s.store.ReturnOrderContext(ctx, in.OrderID, in.OrderNo)
	if err != nil {
		return Return{}, err
	}
	if !found {
		return Return{}, errReturnNotFound
	}
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
		return Return{}, err
	}
	if err := requireBranchReach(caller, oc.BranchID); err != nil {
		return Return{}, err
	}
	if cached, found, err := s.replayIfAllowed(ctx, in.IdempotencyKey, caller); err != nil {
		return Return{}, err
	} else if found {
		return cached, nil
	}
	if in.RefundMethod == RefundCash && in.TillSessionID == "" {
		return Return{}, ErrNoOpenTill{}
	}
	if in.RefundMethod == RefundCard {
		if oc.SaleTender != TenderCard || oc.GatewayRef == "" {
			return Return{}, errCardRefundNoRef
		}
		if s.cardRefund == nil {
			return Return{}, errNoCardRefunder
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
	var decision approverDecision
	var judgeErr error
	credentialJudged := false
	// approvedHere records that the rule FIRED and the standing decision
	// was spent: a return that needs nobody's approval commits as its
	// caller's own decision, whoever's credential arrived (the lead's
	// P2-B ruling: within the amount the header is inert, never consulted).
	approvedHere := false
	if !in.internalRail {
		if s.roles == nil {
			return Return{}, errRoleConfigUnwired()
		}
		if in.ApproverCredential != "" && s.refundMayNeedApproval(ctx, oc, in) {
			resolved, pending, rerr := s.resolveRefundApprover(ctx, in.ApproverCredential, caller, oc)
			decision = resolved
			decision.pending = pending
			judgeErr = rerr
			credentialJudged = true
		}
	}
	// The decision record's names, read before the transaction opens (the
	// lead's P3-D ruling: no trail read under the sale's row locks).
	approverStaff := ""
	refusalRowID := ""
	if decision.pending {
		approverStaff = s.approverStaffID(ctx, decision.approver.PlatformUserID)
		refusalRowID = s.latestRefusalRowID(ctx, oc.OrderID)
	}

	pickupStatus := PickupNone
	pickupSite, pickupDate := "", ""
	pickupRequired := false
	if in.Pickup != nil && in.Pickup.Required {
		pickupRequired = true
		pickupStatus = PickupRequested
		pickupSite = in.Pickup.Site
		pickupDate = in.Pickup.RequestedDate
	}

	actor := identity.StaffUserIDFromContext(ctx)
	var out Return

	err = s.store.RunInTx(ctx, func(tx pgx.Tx) error {
		// 1. Lock + guard every requested line, derive its refund money/tax.
		lines := make([]returnLineRow, 0, len(in.Lines))
		var subtotal, taxTotal int64
		stockLines := make([]StockLine, 0, len(in.Lines))
		eventLines := make([]map[string]any, 0, len(in.Lines))
		for _, req := range in.Lines {
			rl, ok, err := s.store.LockReturnableLine(ctx, tx, oc.OrderID, req.OrderLineID)
			if err != nil {
				return err
			}
			if !ok {
				return clientErr(http.StatusUnprocessableEntity, "pos: line %s does not belong to order %s", req.OrderLineID, oc.OrderNo)
			}
			remainingQty := rl.QtySold - rl.PriorQty
			if req.QtyBase > remainingQty+qtyEpsilon {
				return clientErr(http.StatusUnprocessableEntity,
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
				return clientErr(http.StatusUnprocessableEntity, "pos: line %s has nothing left to refund", req.OrderLineID)
			}
			// Money invariant (never weakened): Σ refunded tax per order line ≤ the original line tax.
			if rl.PriorTax+tax > rl.TaxCents {
				return clientErr(http.StatusUnprocessableEntity, "pos: tax refund would exceed the original line tax on %s", req.OrderLineID)
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

		// The refund rule reads the dealer's configuration (X7): a return
		// above the configured amount from a role not on the approvers list
		// is refused HERE, after every line is guarded and before any write
		// and any rail, so the refusal commits nothing and touches nobody.
		// At or under the amount no approver is ever asked. The A-06 portal
		// rail skips this gate: the queue's own decision already approved
		// that request, and the counter rule is not a second veto over it.
		//
		// The manager's credential at the clerk's till (row 482): the same
		// return carried again with the approver's credential in its header
		// commits as the manager's decision when the credential qualified
		// in the pre-transaction resolution (approver.go); anything short of
		// that leaves the standing refusal in place.
		if !in.internalRail {
			refusal, refused := s.refundApprovalRefusal(total, caller)
			// Above the amount, a presented credential must qualify,
			// whoever carried it (the lead's P2-B ruling): a failed
			// judgment refuses on its own family, a credential that does
			// not qualify refuses with the contract's body, the configured
			// amount and the configured approvers, never null and never
			// zero. Within the amount nothing here stirs: the header is
			// inert.
			if credentialJudged && total > s.roles.RefundAmountWithoutApprovalCents() {
				if judgeErr != nil {
					return judgeErr
				}
				if !decision.pending {
					refusal = s.standingRefusalFor(total)
					refused = true
				}
			}
			if refused {
				if !decision.pending {
					return refusal
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
		returnID, returnNo, err := s.store.InsertReturn(ctx, tx, returnRow{
			OrderID: oc.OrderID, InvoiceID: oc.InvoiceID, CustomerID: oc.CustomerID, BranchID: oc.BranchID,
			RefundMethod: in.RefundMethod, SubtotalCents: subtotal, TaxCents: taxTotal, TotalCents: total,
			FeeCents: feeCents, Reason: in.Reason, Restock: in.Restock, TillSessionID: in.TillSessionID,
			PickupRequired: pickupRequired, PickupSite: pickupSite, PickupDate: pickupDate, PickupStatus: pickupStatus,
			IdempotencyKey: in.IdempotencyKey, CreatedBy: actor,
		})
		if err != nil {
			return err
		}
		if err := s.store.InsertReturnLines(ctx, tx, returnID, lines); err != nil {
			return err
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
				return clientErr(http.StatusConflict, "%s", err.Error())
			}
			refundPaymentID = res.PaymentID
		case RefundCard:
			refundRef, err := s.cardRefund.RefundCard(ctx, oc.GatewayRef, total, returnNo, "refund:"+in.IdempotencyKey)
			if err != nil {
				return clientErr(http.StatusBadGateway, "pos: card refund failed, %s", err.Error())
			}
			res, err := s.tender.RefundTender(ctx, tx, till.RefundInput{
				Method: till.MethodCard, AmountCents: total, BranchID: oc.BranchID, CustomerID: oc.CustomerID,
				GatewayRef: refundRef, Reference: returnNo, IdempotencyKey: "refund:" + in.IdempotencyKey,
			})
			if err != nil {
				return clientErr(http.StatusConflict, "%s", err.Error())
			}
			refundPaymentID = res.PaymentID
		case RefundAccountCredit:
			// The restocking fee reduces what the customer's account is credited (total − fee); the fee
			// itself books through the GL event's fee_cents, never a subledger row.
			if err := s.applyAccountCredit(ctx, tx, oc, total-feeCents); err != nil {
				return err
			}
		}
		if refundPaymentID != "" {
			if err := s.store.SetReturnRefundPayment(ctx, tx, returnID, refundPaymentID); err != nil {
				return err
			}
		}

		// 4. Restock, RETURN moves at the current weighted average through the seam, then the stock-side
		//    GL event (mirrors the Sale's decrement + order.fulfilled pairing; skips quietly when no cost).
		if in.Restock {
			if err := s.restocker().RestockForReturnTx(ctx, tx, RestockInput{
				ReturnID: returnID, OrderID: oc.OrderID, BranchID: oc.BranchID, ReturnNo: returnNo, Lines: stockLines,
			}); err != nil {
				return err
			}
			if err := s.store.EmitEvent(ctx, tx, "order.returned", map[string]any{
				"order_id": oc.OrderID, "return_id": returnID, "branch_id": oc.BranchID, "lines": eventLines,
			}); err != nil {
				return err
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
			return err
		}

		out = Return{
			ReturnID: returnID, ReturnNo: returnNo, OrderID: oc.OrderID, InvoiceID: oc.InvoiceID,
			RefundMethod: in.RefundMethod, SubtotalCents: subtotal, TaxCents: taxTotal, TotalCents: total,
			FeeCents: feeCents, RefundPaymentID: refundPaymentID, PickupStatus: pickupStatus, Status: "COMMITTED",
		}
		return nil
	})
	if err != nil {
		// The approval refusal writes its own audit row naming the clerk,
		// the sale and the amount (D49): the money asked for, refused at
		// the counter, is exactly the fact the trail exists to keep.
		var refusal ErrRefundApprovalRequired
		if errors.As(err, &refusal) {
			s.record(ctx, audit.SensitiveAccess{
				Action: auditActionRefundApprovalRefused, EntityType: "pos.return", EntityID: oc.OrderID,
				ActorID: actor, BranchID: oc.BranchID, Purpose: "POS_RETURN",
				Reason: refundApprovalRequiredError,
				Extra:  map[string]any{"amount_cents": refusal.RefundCents},
			})
			return Return{}, refusal
		}
		// A concurrent duplicate hit ux_pos_returns_idem and rolled back, surface the winner (replay),
		// judged against the winner's own sale by the same wall.
		if isUniqueViolationErr(err) {
			if cached, found, ferr := s.replayIfAllowed(ctx, in.IdempotencyKey, caller); ferr == nil && found {
				return cached, nil
			}
		}
		return Return{}, err
	}

	completed := audit.SensitiveAccess{
		Action: audit.ActReturnCompleted, EntityType: "pos.return", EntityID: out.ReturnID,
		ActorID: actor, BranchID: oc.BranchID, Purpose: "POS_RETURN", Reason: in.RefundMethod,
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
		if approverStaff != "" {
			completed.Extra["approver_id"] = approverStaff
		}
		completed.Extra["subject_ref"] = decision.approver.PlatformUserID
		completed.Extra["amount_cents"] = out.TotalCents
		completed.Reason = refundApprovalRequiredError
	}
	s.record(ctx, completed)
	return out, nil
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
// whether the requested return COULD land above the configured amount: the
// sum over the requested lines of their original totals and tax, which
// over-approximates every remaining refund. A false answer makes the
// credential header inert (nothing needs authorization, nothing is
// consulted); a true one is only a prospect, the transaction's own total
// decides. An unreadable read judges the credential: fail closed.
func (s *Service) refundMayNeedApproval(ctx context.Context, oc returnOrderCtx, in ReturnInput) bool {
	lines, err := s.store.ListReturnableLines(ctx, oc.OrderID)
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
	return prospective > s.roles.RefundAmountWithoutApprovalCents()
}

// refundApprovalRefusal answers the configured approval rule (X7) for one
// computed return total: ok false when the total sits at or under the
// amount, or when the caller's role holds the approval. The caller has
// already refused a service built with no configuration (the nil gate
// fails closed); the configured amount is integer cents and the comparison
// is integer against integer; a role outside the configuration's
// vocabulary is on no approvers list, fail closed.
func (s *Service) refundApprovalRefusal(totalCents int64, caller identity.StaffContext) (ErrRefundApprovalRequired, bool) {
	amount := s.roles.RefundAmountWithoutApprovalCents()
	if totalCents <= amount {
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
		RefundCents: totalCents,
	}, true
}

// applyAccountCredit books the ACCOUNT_CREDIT rail: offset the original invoice first (floor 0; PAID
// only when fully offset), then carry any remainder as a store credit. The customer subledger drops by
// the FULL total either way (balance_after = open − total) and the AR view stays exact: the offset part
// rides the invoice balance (its memo row is source_type 'invoice', excluded from the non-invoice
// union), the remainder is a counted non-invoice CREDIT_MEMO. txn_type CREDIT_MEMO is the 0009 CHECK's
// vocabulary for the design's "CREDIT" row.
func (s *Service) applyAccountCredit(ctx context.Context, tx pgx.Tx, oc returnOrderCtx, total int64) error {
	cust, err := s.store.ReadCustomer(ctx, oc.CustomerID)
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

// LookupReturn is the pre-return read: the original committed sale + its lines with prior-return
// sums netted, so the counter can build the per-line qty entry. Read-only, the commit re-guards.
// The branch wall answers before the lines are read (X2): another branch's sale is refused, its
// returnable quantities are not a thing this counter gets to browse.
func (s *Service) LookupReturn(ctx context.Context, orderID, orderNo string) (ReturnLookup, error) {
	if orderID == "" && orderNo == "" {
		return ReturnLookup{}, clientErr(http.StatusBadRequest, "pos: order_id or order_no is required")
	}
	oc, found, err := s.store.ReturnOrderContext(ctx, orderID, orderNo)
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
