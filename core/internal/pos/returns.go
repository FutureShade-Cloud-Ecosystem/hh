package pos

// returns.go, the W7 POS return: idempotent, over-return-guarded, one atomic tx across the money rail
// (CASH drawer pay-out / CARD processor reversal / ACCOUNT_CREDIT), the optional restock (RETURN moves
// at the current weighted average through the pos-declared seam) and the two GL events. The till stays
// the SOLE writer of payments + drawer movements; inventory stays the sole stock writer.

import (
	"context"
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
	if cached, found, err := s.store.FindReturnByKey(ctx, in.IdempotencyKey); err != nil {
		return Return{}, err
	} else if found {
		cached.Replayed = true
		return cached, nil
	}

	// The original sale: must be a committed POS sale; the return runs at ITS branch.
	oc, found, err := s.store.ReturnOrderContext(ctx, in.OrderID, in.OrderNo)
	if err != nil {
		return Return{}, err
	}
	if !found {
		return Return{}, errReturnNotFound
	}
	// The branch wall runs BEFORE any rail and before any write (X2, fixing
	// HH-218): no processor reversal, no drawer payout and no account credit
	// may move for a sale the caller's resolved authority does not reach.
	// The return runs at the sale's branch; the caller brings their own
	// grants, resolved per request by the guards, never claimed.
	caller, err := s.caller(ctx)
	if err != nil {
		return Return{}, err
	}
	if err := requireBranchReach(caller, oc.BranchID); err != nil {
		return Return{}, err
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

		// 5. The money-side GL event (DR revenue + DR HST / CR the refund rail).
		if err := s.store.EmitEvent(ctx, tx, "pos.return.committed", map[string]any{
			"return_id": returnID, "order_id": oc.OrderID, "invoice_id": oc.InvoiceID,
			"refund_method": in.RefundMethod, "subtotal_cents": subtotal, "tax_cents": taxTotal,
			"total_cents": total, "fee_cents": feeCents, "branch_id": oc.BranchID,
		}); err != nil {
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
		// A concurrent duplicate hit ux_pos_returns_idem and rolled back, surface the winner (replay).
		if isUniqueViolationErr(err) {
			if cached, found, ferr := s.store.FindReturnByKey(ctx, in.IdempotencyKey); ferr == nil && found {
				cached.Replayed = true
				return cached, nil
			}
		}
		return Return{}, err
	}

	s.record(ctx, audit.SensitiveAccess{
		Action: audit.ActReturnCompleted, EntityType: "pos.return", EntityID: out.ReturnID,
		ActorID: actor, BranchID: oc.BranchID, Purpose: "POS_RETURN", Reason: in.RefundMethod,
		Extra: map[string]any{"return_no": out.ReturnNo, "total_cents": out.TotalCents},
	})
	return out, nil
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
