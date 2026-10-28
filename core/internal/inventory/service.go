package inventory

// service.go, the INV-CORE business logic behind the frozen module.go + seam signatures: the PO-receiving
// producer (RECEIPT moves + weighted-average maintenance + GRNI emission, USD→CAD at the boundary), the POS
// stock-decrement (COLLECT_NOW SHIP moves; DELIVERED/COLLECT_LATER = zero moves, the DX-01 boundary), the
// weighted-average cost basis (the real gl.CostBasis), and the po.received / inventory.adjusted posting
// consumers (the 1200 leg resolved through gl.ResolveRule, never a literal). All GL values are CAD.

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
)

// qtyEpsilon absorbs DECIMAL(19,4) float round-trip noise when comparing quantities.
const qtyEpsilon = 1e-6

// db resolves the persistence seam: the injected fake in unit tests, else the pgx repository.
func (s *Service) db() store {
	if s.st != nil {
		return s.st
	}
	return s.repo
}

// withStore injects a DB-free store (unit tests).
func (s *Service) withStore(st store) *Service { s.st = st; return s }

// - the real cost basis (gl.CostBasis) -

// unitCost is the extended weighted-average COGS basis for a fulfilled line: the product-level weighted-avg
// unit cost × qty. found=false (no cost row) leaves the order.fulfilled COGS leg dormant.
func (s *Service) unitCost(ctx context.Context, productID string, qtyBase float64) (int64, bool, error) {
	avg, found, err := s.db().ProductCostCents(ctx, productID)
	if err != nil || !found {
		return 0, false, err
	}
	v, err := qtyTimesCents(qtyBase, avg)
	return v, true, err
}

func (s *Service) branchUnitCost(ctx context.Context, productID, branchID string, qtyBase float64) (int64, bool, error) {
	if branchID == "" {
		return s.unitCost(ctx, productID, qtyBase)
	}
	avg, found, err := s.db().BranchCostCents(ctx, productID, branchID)
	if err != nil || !found {
		return 0, false, err
	}
	v, err := qtyTimesCents(qtyBase, avg)
	return v, true, err
}

// - PO receiving -

// movementInstant answers the ONE instant a movement is dated at.
//
// IT IS THE TRANSACTION'S OWN CLOCK, read ONCE: Postgres's now() is the transaction's start, the same value every
// statement in this transaction sees. One read, one instant, for the whole movement.
//
// WHY IT MATTERS (W2-5 fix r8, S5's P2-2 and P3-1). The stock ledger's move and the GL's journal book the SAME
// movement, and a month end reads both sides at one instant. Before this the move took the column default (the
// row's own now()) and the journal took the DRAIN's clock, the two a drain apart: a receipt in a month's last
// moment, drained after the month's end, left the stock ledger holding the goods and 1200 not holding them, and
// the close was refused by the whole receipt. No later month could repair it: the stock side moved in the earlier
// month, the book side in the later one.
//
// Reading the transaction's instant ONCE and stamping it on BOTH the move and the event closes that gap: the event
// carries the instant, the consumer dates the journals with it, and the journals are posted at the same instant
// the movement's own transaction owns. A transaction that straddles midnight no longer splits the two either:
// both sides carry the transaction's own start.
func (s *Service) movementInstant(ctx context.Context, tx pgx.Tx) (time.Time, error) {
	at, err := s.db().MovementInstant(ctx, tx)
	if err != nil {
		return time.Time{}, fmt.Errorf("inventory: read the movement's instant: %w", err)
	}
	return at, nil
}

// applyReceipt is the shared receiving core. requested==nil receives every outstanding unit; a non-nil map
// receives exactly the given qty per po_line id (over-receipt-guarded). It writes RECEIPT moves, maintains the
// weighted-average cost + on-hand balances, advances the PO status, and emits po.received (the GRNI producer).
func (s *Service) applyReceipt(ctx context.Context, tx pgx.Tx, poID string, requested map[string]float64, receivedBy string) (ReceiptResult, error) {
	hdr, err := s.db().POHeaderForReceipt(ctx, tx, poID)
	if err != nil {
		return ReceiptResult{}, err
	}
	lines, err := s.db().POLinesForReceipt(ctx, tx, poID)
	if err != nil {
		return ReceiptResult{}, err
	}
	receiptID, err := s.db().InsertReceipt(ctx, tx, poID, receivedBy)
	if err != nil {
		return ReceiptResult{}, err
	}
	usd := hdr.Currency == "USD"
	// The receipt's ONE instant (W2-5 fix r8): the move row and the event carry the same instant,
	// and the consumer dates the receipt's journals with it. Read once, here, before any row is written.
	at, err := s.movementInstant(ctx, tx)
	if err != nil {
		return ReceiptResult{}, err
	}

	var totalValue int64
	var totalRounding int64
	linesRecvd := 0
	allFull := true
	type evLine struct {
		ProductID  string  `json:"product_id"`
		QtyBase    float64 `json:"qty_base"`
		ValueCents int64   `json:"value_cents"`
	}
	var eventLines []evLine
	var trueUps []poTrueUpLine

	for _, ln := range lines {
		remaining := ln.QtyBase - ln.ReceivedBase
		var recvQty float64
		if requested != nil {
			q, ok := requested[ln.ID]
			if !ok || q == 0 {
				if remaining > qtyEpsilon {
					allFull = false
				}
				continue
			}
			if q > remaining+qtyEpsilon {
				return ReceiptResult{}, conflictErr(
					"inventory: over-receipt on PO line %d, requested %.4f but only %.4f outstanding",
					ln.LineNo, q, remaining)
			}
			recvQty = q
		} else {
			recvQty = remaining
		}
		if recvQty <= qtyEpsilon {
			continue
		}

		// USD→CAD at the boundary: the GL + valuation never see a foreign amount (DX-19 scoped exception).
		cadUnit := ln.UnitCostCents
		if usd {
			cadUnit, err = centsTimesFxRate(ln.UnitCostCents, hdr.FxRate)
			if err != nil {
				return ReceiptResult{}, err
			}
		}
		recvValue, err := qtyTimesCents(recvQty, cadUnit)
		if err != nil {
			return ReceiptResult{}, err
		}

		// Weighted-average maintenance: newAvg = (scaledOnHand × oldAvg + scaledRecv × cadUnit) /
		// (scaledOnHand + scaledRecv), rounded ONCE, half away from zero, in exact integer arithmetic:
		// every term stays unrounded, so equal-cost stock blends back to exactly its cost (0.29 at 750
		// received onto 0.29 at 750 averages 750, where rounding a term to whole cents first reads 751
		// or 752), the single-rounding semantics the float formula's unrounded terms intended.
		// Serialize the WAC read-modify-write on (product, branch) FIRST so a concurrent landed-cost Apply/Void (or
		// another receipt) on the same product+branch blocks here instead of reading the same old WAC and clobbering
		// this update on commit, the lost-update that diverges the subledger from GL 1200 (L8 MONEY-CRITICAL).
		if err := s.db().LockBranchCost(ctx, tx, ln.ProductID, hdr.BranchID); err != nil {
			return ReceiptResult{}, err
		}
		onHand, err := s.db().OnHandQtyBranch(ctx, tx, ln.ProductID, hdr.BranchID)
		if err != nil {
			return ReceiptResult{}, err
		}
		oldAvg, _, err := s.db().GetBranchCost(ctx, tx, ln.ProductID, hdr.BranchID)
		if err != nil {
			return ReceiptResult{}, err
		}
		provMoves, err := s.db().UntruedProvisionalShipMoves(ctx, tx, ln.ProductID, hdr.BranchID)
		if err != nil {
			return ReceiptResult{}, err
		}
		base := math.Max(onHand, 0)
		newAvg := cadUnit
		// subledgerDelta is the line's exact change in round(avg x on-hand), the figure the
		// receipt's GL postings (GRNI, WAC rounding, true-up) sum to; it is stamped on the
		// move row below so the as-of stock read can unwind the receipt to the cent (W2-5).
		// An on-hand of zero (or a beyond-grain figure the helpers refused) absorbs exactly
		// the received value.
		subledgerDelta := recvValue
		if len(provMoves) == 0 {
			if denom := base + recvQty; denom > 0 {
				newAvg, err = blendAvg(base, oldAvg, recvQty, cadUnit)
				if err != nil {
					return ReceiptResult{}, err
				}
			}
			if base > 0 {
				oldSubledgerVal, errOld := qtyTimesCents(base, oldAvg)
				newSubledgerVal, errNew := qtyTimesCents(base+recvQty, newAvg)
				if errOld == nil && errNew == nil {
					totalRounding += (newSubledgerVal - oldSubledgerVal) - recvValue
					subledgerDelta = newSubledgerVal - oldSubledgerVal
				}
			}
		}
		var moveMeta map[string]any
		if len(provMoves) > 0 {
			var provQty float64
			var provValue int64
			moveIDs := make([]string, 0, len(provMoves))
			for _, pm := range provMoves {
				provQty += pm.QtyBase
				provValue += pm.ProvisionalValueCents
				moveIDs = append(moveIDs, pm.MoveID)
			}
			actualSoldValue, err := qtyTimesCents(provQty, cadUnit)
			if err != nil {
				return ReceiptResult{}, err
			}
			// The true-up journal books this delta with its own sign: a positive
			// delta credited 1200 (the provisionally relieved units cost more
			// than the fallback held), a negative delta debited it. The stamp is
			// the figure the receipt's GL postings SUM to (GRNI plus rounding
			// plus the true-up's signed effect on 1200), so the true-up enters
			// subtracted: this branch never blends, so its exact subledger delta
			// is the GRNI minus the true-up delta (W2-5 round 3: the addition
			// here double-counted the true-up against the books to 2x its size).
			trueUpDelta := actualSoldValue - provValue
			subledgerDelta -= trueUpDelta
			moveMeta = map[string]any{
				"trued_up_move_ids":   moveIDs,
				"true_up_delta_cents": trueUpDelta,
			}
			trueUps = append(trueUps, poTrueUpLine{
				ProductID:  ln.ProductID,
				DeltaCents: trueUpDelta,
			})
		}
		if moveMeta == nil {
			moveMeta = map[string]any{}
		}
		moveMeta["wac_value_delta_cents"] = subledgerDelta
		if err := s.db().UpsertBranchCost(ctx, tx, ln.ProductID, hdr.BranchID, newAvg); err != nil {
			return ReceiptResult{}, err
		}
		if _, err := s.db().InsertMove(ctx, tx, moveParams{
			ProductID: ln.ProductID, BranchID: hdr.BranchID, ToLocation: hdr.BranchID, UOM: ln.UOM,
			MoveType: MoveReceipt, QtyBase: recvQty, UnitCostCents: cadUnit, ValueCents: recvValue,
			SourceType: "po_receipt", SourceRefID: receiptID, Reason: "PO receipt " + hdr.PONo,
			Metadata: moveMeta, OccurredAt: at,
		}); err != nil {
			return ReceiptResult{}, err
		}
		if err := s.db().UpsertBalanceDelta(ctx, tx, ln.ProductID, hdr.BranchID, hdr.BranchID, recvQty); err != nil {
			return ReceiptResult{}, err
		}
		if err := s.db().UpdatePOLineReceived(ctx, tx, ln.ID, ln.ReceivedBase+recvQty); err != nil {
			return ReceiptResult{}, err
		}
		if ln.ReceivedBase+recvQty < ln.QtyBase-qtyEpsilon {
			allFull = false
		}
		total, ok := addCents(totalValue, recvValue)
		if !ok {
			return ReceiptResult{}, fmt.Errorf("inventory: receipt %s total value exceeds integer cents", hdr.PONo)
		}
		totalValue = total
		linesRecvd++
		eventLines = append(eventLines, evLine{ProductID: ln.ProductID, QtyBase: recvQty, ValueCents: recvValue})
	}

	if linesRecvd == 0 {
		return ReceiptResult{}, errNothingToDo
	}

	status := POPartiallyReceived
	if allFull {
		status = POReceived
	}
	if err := s.db().UpdatePOStatus(ctx, tx, poID, status); err != nil {
		return ReceiptResult{}, err
	}
	if err := s.db().EmitEvent(ctx, tx, "po.received", map[string]any{
		"po_id": poID, "receipt_id": receiptID, "branch_id": hdr.BranchID,
		"value_cents": totalValue, "rounding_cents": totalRounding, "lines": eventLines, "true_ups": trueUps,
		// The receipt's own instant (W2-5 fix r8): the consumer dates the receipt's journals with
		// it, so the journal and the move it books carry the same moment.
		"occurred_at": at,
	}); err != nil {
		return ReceiptResult{}, err
	}
	return ReceiptResult{
		ReceiptID: receiptID, POID: poID, PONo: hdr.PONo, BranchID: hdr.BranchID, Status: status,
		Currency: hdr.Currency, FxRate: hdr.FxRate, LinesRecvd: linesRecvd, ValueCadCents: totalValue,
	}, nil
}

// receive opens its own tx (the HTTP path) and applies an explicit-or-full receipt.
func (s *Service) receive(ctx context.Context, in ReceiveRequest) (ReceiptResult, error) {
	if in.POID == "" {
		return ReceiptResult{}, badReq("inventory: po_id is required")
	}
	tx, err := s.repo.Begin(ctx)
	if err != nil {
		return ReceiptResult{}, err
	}
	defer tx.Rollback(ctx)

	var requested map[string]float64
	if len(in.Lines) > 0 {
		requested = make(map[string]float64, len(in.Lines))
		for _, l := range in.Lines {
			if l.QtyBase < 0 {
				return ReceiptResult{}, badReq("inventory: negative receive qty on line %s", l.POLineID)
			}
			requested[l.POLineID] = l.QtyBase
		}
	}
	res, err := s.applyReceipt(ctx, tx, in.POID, requested, in.ReceivedBy)
	if err != nil {
		return ReceiptResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ReceiptResult{}, err
	}
	s.log.Info("inventory: PO received", "po_no", res.PONo, "receipt_id", res.ReceiptID,
		"value_cad_cents", res.ValueCadCents, "status", res.Status)
	return res, nil
}

// - POS stock decrement (DX-01 boundary) -

// decrementForSale writes SHIP moves for a COLLECT_NOW sale (stock drops, may go negative by design).
// DELIVERED / COLLECT_LATER record ZERO moves at ring time and relieve stock at fulfilment via FulfilOrderLinesTx.
func (s *Service) decrementForSale(ctx context.Context, tx pgx.Tx, in DecrementInput) ([]ShipLineValue, error) {
	if in.SaleType != SaleCollectNow {
		return nil, nil // boundary: deferred fulfilment relieves stock at fulfilment
	}
	values := make([]ShipLineValue, 0, len(in.Lines))
	for _, ln := range in.Lines {
		if ln.QtyBase <= 0 {
			continue
		}
		// The POS ring's own move keeps the row's default instant (the transaction's clock):
		// the ring's event already carries the sale's own date, and the ring's transaction
		// opens and reads that date within the same breath, so the two are one moment.
		value, err := s.decrementOneShipLine(ctx, tx, in.OrderID, in.BranchID, "pos_sale", "POS COLLECT_NOW sale", ln, time.Time{})
		if err != nil {
			return nil, err
		}
		values = append(values, ShipLineValue{ProductID: ln.ProductID, QtyBase: ln.QtyBase, ValueCents: value})
	}
	return values, nil
}

// decrementOneShipLine writes the SHIP move at the branch average (or the provisional fallback) and
// answers the move's own value_cents (round 2, S3's P1): the fulfilment event carries it, so the COGS
// journal posts the value that moved, never a re-read average. A product with no cost at all ships
// provisionally at $0.00 (round 2, S3's P2): the move is marked, the Controller exceptions list names
// it, and the first receipt trues it up like any other provisional move.
//
// occurredAt is the instant the move is dated at. The zero value rides the row's own DEFAULT (now(),
// the transaction's clock); the order's fulfilment passes the instant it read ONCE for the whole
// fulfilment, so the move and the order.fulfilled event that books it carry the SAME one and the stock
// ledger and 1200 cannot be found on different sides of a month end (W2-5 fix r6).
func (s *Service) decrementOneShipLine(ctx context.Context, tx pgx.Tx, orderID, branchID, sourceType, reason string, ln SaleLine, occurredAt time.Time, extraMeta ...map[string]any) (int64, error) {
	avg, hadCost, err := s.db().GetBranchCost(ctx, tx, ln.ProductID, branchID)
	if err != nil {
		return 0, err
	}
	oldAvg := avg
	provMoves, err := s.db().UntruedProvisionalShipMoves(ctx, tx, ln.ProductID, branchID)
	if err != nil {
		return 0, err
	}
	var moveMeta map[string]any
	for _, extra := range extraMeta {
		for k, v := range extra {
			if moveMeta == nil {
				moveMeta = map[string]any{}
			}
			moveMeta[k] = v
		}
	}
	wroteCost := false
	if !hadCost || avg <= 0 || len(provMoves) > 0 {
		fallbackCost, source, found, err := s.db().FallbackCost(ctx, tx, ln.ProductID, branchID)
		if err != nil {
			return 0, err
		}
		if found && fallbackCost > 0 {
			avg = fallbackCost
			moveMeta = map[string]any{
				"provisional_cost":         true,
				"fallback_source":          source,
				"fallback_unit_cost_cents": fallbackCost,
			}
			if err := s.db().UpsertBranchCost(ctx, tx, ln.ProductID, branchID, avg); err != nil {
				return 0, err
			}
			wroteCost = true
		} else {
			// No cost at all: provisional at $0.00, marked and listed (round 2, S3's P2), never a
			// silent skip.
			moveMeta = map[string]any{
				"provisional_cost":         true,
				"fallback_source":          "none",
				"fallback_unit_cost_cents": int64(0),
			}
		}
	}
	value, err := qtyTimesCents(ln.QtyBase, avg)
	if err != nil {
		return 0, err
	}
	// W2-5 round 5 (S5's P2-1): the move stamps the row's exact change in round(on_hand x avg),
	// not the rounded movement value_cents carries. 12.38 t at 4510 cents is 55,833.8, held as
	// 55,834; a 0.04 t sale is 180.4, booked as 180; the row becomes 12.34 t, 55,653.4, held as
	// 55,653: it moved by 181. The difference is booked as the WAC integer-cent rounding journal
	// in this same transaction, so the stock ledger and 1200 unwind a stamped SHIP to the cent
	// at any as-of instant, the PO receipt's rule.
	avgBeforeEff := int64(0)
	if hadCost {
		avgBeforeEff = oldAvg
	}
	avgAfterEff := avgBeforeEff
	if wroteCost {
		avgAfterEff = avg
	}
	change, err := s.exactRowChange(ctx, tx, ln.ProductID, branchID, branchID, -ln.QtyBase, avgBeforeEff, avgAfterEff)
	if err != nil {
		return 0, err
	}
	if moveMeta == nil {
		moveMeta = map[string]any{}
	}
	moveMeta["wac_value_delta_cents"] = -change
	moveID, err := s.db().InsertMove(ctx, tx, moveParams{
		ProductID: ln.ProductID, BranchID: branchID, FromLocation: branchID,
		MoveType: MoveShip, QtyBase: ln.QtyBase, UnitCostCents: avg, ValueCents: value,
		SourceType: sourceType, SourceRefID: orderID, Reason: reason,
		Metadata: moveMeta, OccurredAt: occurredAt,
	})
	if err != nil {
		return 0, err
	}
	if err := s.db().UpsertBalanceDelta(ctx, tx, ln.ProductID, branchID, branchID, -ln.QtyBase); err != nil {
		return 0, err
	}
	if rounding := change + value; rounding != 0 {
		if err := s.postMoveRoundingTx(ctx, tx, moveRoundingPost{
			Event: "inventory.ship", MoveID: moveID, BranchID: branchID, RoundingCents: rounding,
			InvEvent: "order.fulfilled", Memo: "WAC integer-cent rounding on stock issue",
			OccurredAt: occurredAt,
		}); err != nil {
			return 0, err
		}
	}
	return value, nil
}

// FulfilOrderLinesTx relieves on-hand stock for fulfilled order lines and emits
// order.fulfilled to events_outbox in the same transaction. If the order was
// already relieved (either at POS ring time for a COLLECT_NOW sale or by an
// earlier fulfilment call), HasShipMovesForOrderTx returns true and this is a
// no-op so stock and COGS are never double-posted.
// FulfilOrderLinesTx relieves the ORDER's own lines (the order wide paths: the pickup and the
// delivery roads). Group scoped relief is FulfilGroupLinesTx (round 2, S3's P1).
func (s *Service) FulfilOrderLinesTx(ctx context.Context, tx pgx.Tx, orderID, branchID string, productIDs []string, qtys []float64) error {
	return s.fulfilLinesTx(ctx, tx, orderID, "", branchID, productIDs, qtys)
}

// FulfilGroupLinesTx relieves a SHIP GROUP's own lines and quantities (round 2, S3's P1): the group's
// id rides the move metadata and the event, so a group's relief and its idempotency key are the
// group's own, never the order's.
func (s *Service) FulfilGroupLinesTx(ctx context.Context, tx pgx.Tx, orderID, groupID, branchID string, productIDs []string, qtys []float64) error {
	return s.fulfilLinesTx(ctx, tx, orderID, groupID, branchID, productIDs, qtys)
}

// HasShipMovesForGroupTx answers whether the group's own SHIP moves exist (the group's idempotency).
func (s *Service) HasShipMovesForGroupTx(ctx context.Context, tx pgx.Tx, orderID, groupID string) (bool, error) {
	var exists bool
	err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM stock_moves
			 WHERE source_type = 'order' AND source_ref_id = $1::uuid
			   AND move_type = 'SHIP' AND metadata->>'group_id' = $2
		)`, orderID, groupID).Scan(&exists)
	return exists, err
}

// HasShipMovesForOrderTx answers whether any SHIP move exists for the order, READ ON THE CALLER'S
// TRANSACTION (round 2, S3's P1): the INVOICED holds rule's "the goods have left".
//
// TX SCOPED ON PURPOSE. A move the caller's own transaction wrote is uncommitted, and a pool read
// cannot see it, so the order would be told its goods are still in the yard at the moment they are
// not. The order module asks this question inside the very transaction that may be moving the
// stock, and the answer has to be that transaction's.
func (s *Service) HasShipMovesForOrderTx(ctx context.Context, tx pgx.Tx, orderID string) (bool, error) {
	if s.repo == nil || s.repo.pool == nil {
		return false, nil
	}
	var exists bool
	// THE SAME SHAPE AS THE REPOSITORY'S OWN CHECK, AND THAT IS THE POINT: a ring's sale moves the
	// stock with source_type 'pos_sale' and an order's own relief with source_type 'order', and
	// both are the goods leaving for THIS order. A filter that named one of them would let the other
	// relieve the same stock twice.
	err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM stock_moves
			 WHERE move_type = 'SHIP' AND source_ref_id = $1::uuid
		)`, orderID).Scan(&exists)
	return exists, err
}

// fulfilmentInstant answers the ONE instant a fulfilment is dated at.
//
// IT IS THE TRANSACTION'S OWN CLOCK: Postgres's now() is the transaction's start, the same value every
// statement in this transaction sees. One read, one instant, for the whole fulfilment.
//
// WHY IT MATTERS (W2-5 fix r6). The stock ledger's SHIP move and the GL's COGS journal book the SAME
// sale, and a month end reads both sides at one instant. If the two carry different instants, a month
// end that falls between them sees the stock relieved and 1200 unrelieved: the close is refused by the
// whole sale, and no later month can repair it, because the stock side moved in the earlier month and
// the book side moved in the later one. Before this, the move took the transaction's clock and the
// journal took the consumer's clock (the drain's), and the two were a drain apart.
//
// Reading the transaction's instant ONCE and stamping it on BOTH the move and the event closes that
// gap: the event carries the instant, the GL's consumer reads it off the payload (the same field the
// POS ring's sale has always carried), and the journal is posted at that same instant, the one the
// fulfilment's own transaction owns. A transaction that straddles midnight no longer splits the two
// either: both sides carry the transaction's own start.
func (s *Service) fulfilmentInstant(ctx context.Context, tx pgx.Tx) (time.Time, error) {
	at, err := s.db().MovementInstant(ctx, tx)
	if err != nil {
		return time.Time{}, fmt.Errorf("inventory: read the fulfilment's instant: %w", err)
	}
	return at, nil
}

func (s *Service) fulfilLinesTx(ctx context.Context, tx pgx.Tx, orderID, groupID, branchID string, productIDs []string, qtys []float64) error {
	if orderID == "" || branchID == "" || len(productIDs) == 0 || len(productIDs) != len(qtys) {
		return nil
	}
	if groupID == "" {
		alreadyShipped, err := s.db().HasShipMovesForOrderTx(ctx, tx, orderID)
		if err != nil {
			return fmt.Errorf("inventory: check order ship moves: %w", err)
		}
		if alreadyShipped {
			return nil
		}
	} else {
		alreadyShipped, err := s.HasShipMovesForGroupTx(ctx, tx, orderID, groupID)
		if err != nil {
			return fmt.Errorf("inventory: check group ship moves: %w", err)
		}
		if alreadyShipped {
			return nil
		}
	}
	type outboxLine struct {
		ProductID  string  `json:"product_id"`
		QtyBase    float64 `json:"qty_base"`
		ValueCents int64   `json:"value_cents"`
	}
	// ONE instant for the whole fulfilment: the transaction's own clock, read once, so every
	// SHIP move and the event that books them are dated at the same moment (fulfilmentInstant).
	at, err := s.fulfilmentInstant(ctx, tx)
	if err != nil {
		return err
	}
	outLines := make([]outboxLine, 0, len(productIDs))
	for i := range productIDs {
		pid := productIDs[i]
		qty := qtys[i]
		if pid == "" || qty <= 0 {
			continue
		}
		meta := map[string]any(nil)
		if groupID != "" {
			meta = map[string]any{"group_id": groupID}
		}
		value, err := s.decrementOneShipLine(ctx, tx, orderID, branchID, "order", "order_fulfilled", SaleLine{
			ProductID: pid,
			QtyBase:   qty,
		}, at, meta)
		if err != nil {
			return err
		}
		outLines = append(outLines, outboxLine{
			ProductID:  pid,
			QtyBase:    qty,
			ValueCents: value,
		})
	}
	if len(outLines) == 0 {
		return nil
	}
	event := map[string]any{
		"order_id":  orderID,
		"branch_id": branchID,
		"lines":     outLines,
		// The fulfilment's own instant, the same one the SHIP moves carry: the COGS journal the
		// consumer posts is dated at it, so the stock ledger and 1200 cut this sale at ONE moment
		// and a month end cannot find them on different sides of itself (W2-5 fix r6).
		"occurred_at": at,
	}
	if groupID != "" {
		event["group_id"] = groupID
	}
	return s.db().EmitEvent(ctx, tx, "order.fulfilled", event)
}

// LockStockTx takes the branch's balance rows of these products, sorted, before a move over several
// of them (the swap restocks the returned products and then decrements the new order's): the one
// order every such caller uses, so two crossed moves queue instead of deadlocking.
func (s *Service) LockStockTx(ctx context.Context, tx pgx.Tx, branchID string, productIDs []string) error {
	ids := append([]string(nil), productIDs...)
	sort.Strings(ids)
	return s.db().LockBalances(ctx, tx, branchID, ids)
}

// - POS return restock (W7) -

// restockForReturn writes RETURN moves for a committed POS return: qty comes back IN at the branch's
// CURRENT weighted-average unit cost (found=false → cost 0, value 0, the GL side skips quietly the same
// way), so the average is unchanged; the balance projection gains the qty.
func (s *Service) restockForReturn(ctx context.Context, tx pgx.Tx, in RestockInput) error {
	// The return's ONE instant (W2-5 fix r8, S5's P3-1), read once before any row is
	// written: the RETURN move carries it and the rounding journal is dated with it.
	at, err := s.movementInstant(ctx, tx)
	if err != nil {
		return err
	}
	for _, ln := range in.Lines {
		if ln.QtyBase <= 0 {
			continue
		}
		avg, hadCost, err := s.db().GetBranchCost(ctx, tx, ln.ProductID, in.BranchID)
		if err != nil {
			return err
		}
		value, err := qtyTimesCents(ln.QtyBase, avg)
		if err != nil {
			return err
		}
		// W2-5 round 5 (S5's P2-1): the RETURN move stamps the row's exact change in
		// round(on_hand x avg), not the rounded movement; the difference rides the WAC
		// integer-cent rounding journal in this same transaction, the PO receipt's rule.
		avgEff := int64(0)
		if hadCost {
			avgEff = avg
		}
		change, err := s.exactRowChange(ctx, tx, ln.ProductID, in.BranchID, in.BranchID, ln.QtyBase, avgEff, avgEff)
		if err != nil {
			return err
		}
		moveID, err := s.db().InsertMove(ctx, tx, moveParams{
			ProductID: ln.ProductID, BranchID: in.BranchID, ToLocation: in.BranchID,
			MoveType: MoveReturn, QtyBase: ln.QtyBase, UnitCostCents: avg, ValueCents: value,
			SourceType: "pos_return", SourceRefID: in.ReturnID, Reason: "POS return " + in.ReturnNo,
			Metadata:   map[string]any{"wac_value_delta_cents": change},
			OccurredAt: at,
		})
		if err != nil {
			return err
		}
		if err := s.db().UpsertBalanceDelta(ctx, tx, ln.ProductID, in.BranchID, in.BranchID, ln.QtyBase); err != nil {
			return err
		}
		if rounding := change - value; rounding != 0 {
			if err := s.postMoveRoundingTx(ctx, tx, moveRoundingPost{
				Event: "inventory.return", MoveID: moveID, BranchID: in.BranchID, RoundingCents: rounding,
				InvEvent: "order.returned", Memo: "WAC integer-cent rounding on stock return",
				OccurredAt: at,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

// - the exact-change stamp + rounding journal every fractional value writer posts (W2-5 round 5) -

// exactRowChange answers the branch's exact change in per-row round(on_hand x avg) across one
// quantity move of signedDelta at deltaLocationID: the same figure the as-of read's current value
// is built from (StockValueCents rounds EACH stock_balances row at the branch's weighted average
// and sums), recomputed with the touched row's on-hand moved and the branch's average at its
// post-move value. avgBeforeEff and avgAfterEff are the effective per-unit averages the cost row
// held before and holds after the move (0 when no cost row exists, the valuation the read's
// COALESCE answers for a missing row), so a writer that also revalues the WAC (the issue's
// provisional fallback) stamps the revaluation with the move. The quantity move itself stays the
// caller's job; this reads the row set the caller's own transaction sees, BEFORE the balance
// delta lands.
func (s *Service) exactRowChange(ctx context.Context, tx pgx.Tx, productID, branchID, deltaLocationID string, signedDelta float64, avgBeforeEff, avgAfterEff int64) (int64, error) {
	rows, err := s.db().BalanceRows(ctx, tx, productID, branchID)
	if err != nil {
		return 0, err
	}
	var oldSum, newSum int64
	for _, r := range rows {
		after := r.OnHandBase
		if r.LocationID == deltaLocationID {
			after += signedDelta
		}
		oldVal, err := qtyTimesCents(r.OnHandBase, avgBeforeEff)
		if err != nil {
			return 0, err
		}
		newVal, err := qtyTimesCents(after, avgAfterEff)
		if err != nil {
			return 0, err
		}
		oldSum += oldVal
		newSum += newVal
	}
	return newSum - oldSum, nil
}

// moveRoundingPost is the fact set one move's WAC integer-cent rounding journal is built from
// (W2-5 round 5, S5's P2-1): the signed difference between the row's exact change and the
// movement's booked value, posted in the move's own transaction through the same journal shape
// the PO receipt (postPOReceivedRounding) and the transfer receive (postTransferReceivedRoundingTx)
// post. RoundingCents carries the receipt's sign convention: positive raised 1200, negative
// lowered it.
type moveRoundingPost struct {
	Event         string // the posting-rule event and idempotency-key prefix, one spelling
	MoveID        string // the move the rounding belongs to: one journal per move
	BranchID      string
	RoundingCents int64
	InvEvent      string // the posting-rule event the inventory leg resolves under
	Memo          string
	// OccurredAt is the instant the move it belongs to happened, the one stamped on the move
	// row (W2-5 fix r8, S5's P3-1). The rounding journal is dated with it, so the journal and
	// the move it corrects carry the same moment.
	//
	// The zero value means "no instant named": the journal falls back to the GL's clock, which
	// is what every rounding journal did before the field existed.
	OccurredAt time.Time
}

// postMoveRoundingTx books the difference between a stamped move's exact row change and its
// value_cents through the WAC integer-cent rounding journal shape, on the caller's transaction
// when the poster supports it (the transfer posting convention: the move and its journals are
// one fate), else on the pool. The idempotency key is the event spelling + ".rounding:" + the
// move id, so a retry after a failure posts exactly one journal per move.
func (s *Service) postMoveRoundingTx(ctx context.Context, tx pgx.Tx, p moveRoundingPost) error {
	if p.MoveID == "" {
		return fmt.Errorf("inventory: %s.rounding missing move id", p.Event)
	}
	if s.gl == nil {
		return errNoGLPoster
	}
	if s.rules == nil {
		return errNoRules
	}
	invCode, err := s.resolveTransferRule(ctx, tx, p.InvEvent, "inventory", p.BranchID)
	if err != nil {
		return err
	}
	expCode, err := s.resolveTransferRule(ctx, tx, "inventory.adjusted", "expense", p.BranchID)
	if err != nil || expCode == "" {
		expCode = "5200"
	}
	amt := p.RoundingCents
	if amt < 0 {
		amt = -amt
	}
	var lines []GLLine
	if p.RoundingCents > 0 {
		lines = []GLLine{
			{AccountCode: invCode, DebitCents: amt, Memo: "WAC integer-cent rounding"},
			{AccountCode: expCode, CreditCents: amt, Memo: "WAC integer-cent rounding"},
		}
	} else {
		lines = []GLLine{
			{AccountCode: expCode, DebitCents: amt, Memo: "WAC integer-cent rounding"},
			{AccountCode: invCode, CreditCents: amt, Memo: "WAC integer-cent rounding"},
		}
	}
	jr := GLJournal{
		Source:         "ADJUSTMENT",
		SourceRefID:    p.MoveID,
		IdempotencyKey: p.Event + ".rounding:" + p.MoveID,
		Memo:           p.Memo,
		BranchID:       p.BranchID,
		DocumentDate:   p.OccurredAt,
		Lines:          lines,
	}
	if tx != nil {
		if gp, ok := s.gl.(GLPosterTx); ok {
			_, err := gp.PostJournalTx(ctx, tx, jr)
			return err
		}
	}
	return s.postMoveRoundingCommitting(ctx, jr)
}

// postMoveRoundingCommitting is the move rounding's pool posting path, both arms without a caller
// transaction. Its own function so the branch census finds one committing writer per function
// beside the transaction scoped one above.
func (s *Service) postMoveRoundingCommitting(ctx context.Context, jr GLJournal) error {
	_, err := s.gl.PostJournal(ctx, jr)
	return err
}

// - posting consumers (po.received + inventory.adjusted) -

type poTrueUpLine struct {
	ProductID  string `json:"product_id"`
	DeltaCents int64  `json:"delta_cents"`
}

type poReceivedPayload struct {
	POID          string         `json:"po_id"`
	ReceiptID     string         `json:"receipt_id"`
	BranchID      string         `json:"branch_id"`
	ValueCents    int64          `json:"value_cents"`
	RoundingCents int64          `json:"rounding_cents"`
	TrueUps       []poTrueUpLine `json:"true_ups"`
	// OccurredAt is the receipt's OWN instant, the one the receipt's transaction read once and
	// stamped on the RECEIPT move (W2-5 fix r8, S5's P2-2). Every journal this payload books is
	// dated with it, so the journal and the move it books carry the same moment and a month end
	// finds both sides of the receipt on the same side of itself.
	//
	// The zero value means "no instant carried": the journals fall back to the GL's clock, which
	// is what every receipt did before the field existed.
	OccurredAt time.Time `json:"occurred_at"`
}

// postPOReceived posts the GRNI journal for a receipt: DR 1200 Inventory / CR 2150 Received-Not-Invoiced, both
// legs resolved through the GL posting rules (never a literal), idempotent on the receipt id.
func (s *Service) postPOReceived(ctx context.Context, payload []byte) error {
	var p poReceivedPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return fmt.Errorf("inventory: po.received payload: %w", err)
	}
	if p.ReceiptID == "" {
		return fmt.Errorf("inventory: po.received missing receipt_id")
	}
	if p.ValueCents == 0 && p.RoundingCents == 0 && len(p.TrueUps) == 0 {
		return nil
	}
	if s.gl == nil {
		return errNoGLPoster
	}
	if s.rules == nil {
		return errNoRules
	}
	invCode, err := s.rules.ResolveRule(ctx, "po.received", "inventory", p.BranchID)
	if err != nil {
		return err
	}
	apCode, err := s.rules.ResolveRule(ctx, "po.received", "ap_accrual", p.BranchID)
	if err != nil {
		return err
	}
	if p.ValueCents != 0 {
		if _, err := s.gl.PostJournal(ctx, GLJournal{
			Source:         "PO_RECEIPT",
			SourceRefID:    p.ReceiptID,
			IdempotencyKey: "po.received:" + p.ReceiptID,
			Memo:           "GRNI on PO receipt",
			BranchID:       p.BranchID,
			DocumentDate:   p.OccurredAt,
			Lines: []GLLine{
				{AccountCode: invCode, DebitCents: p.ValueCents, Memo: "inventory received"},
				{AccountCode: apCode, CreditCents: p.ValueCents, Memo: "received not invoiced"},
			},
		}); err != nil {
			return err
		}
	}
	if p.RoundingCents != 0 {
		if err := s.postPOReceivedRounding(ctx, p, invCode); err != nil {
			return err
		}
	}
	for _, tu := range p.TrueUps {
		if tu.DeltaCents == 0 || tu.ProductID == "" {
			continue
		}
		if err := s.postPOReceivedTrueUp(ctx, p, tu, invCode); err != nil {
			return err
		}
	}
	return nil
}

// postPOReceivedRounding posts the WAC integer-cent rounding adjustment journal on a receipt
// (round 1, sub item 2). Its own function so the branch census finds one journal writer per
// function: the receipt's GRNI journal and this adjustment are one payload's separate journals.
func (s *Service) postPOReceivedRounding(ctx context.Context, p poReceivedPayload, invCode string) error {
	expCode, err := s.rules.ResolveRule(ctx, "inventory.adjusted", "expense", p.BranchID)
	if err != nil || expCode == "" {
		expCode = "5200"
	}
	amt := p.RoundingCents
	if amt < 0 {
		amt = -amt
	}
	var lines []GLLine
	if p.RoundingCents > 0 {
		lines = []GLLine{
			{AccountCode: invCode, DebitCents: amt, Memo: "WAC integer-cent rounding"},
			{AccountCode: expCode, CreditCents: amt, Memo: "WAC integer-cent rounding"},
		}
	} else {
		lines = []GLLine{
			{AccountCode: expCode, DebitCents: amt, Memo: "WAC integer-cent rounding"},
			{AccountCode: invCode, CreditCents: amt, Memo: "WAC integer-cent rounding"},
		}
	}
	if _, err := s.gl.PostJournal(ctx, GLJournal{
		Source:         "ADJUSTMENT",
		SourceRefID:    p.ReceiptID,
		IdempotencyKey: "po.received.rounding:" + p.ReceiptID,
		Memo:           "WAC integer-cent rounding on PO receipt",
		BranchID:       p.BranchID,
		DocumentDate:   p.OccurredAt,
		Lines:          lines,
	}); err != nil {
		return err
	}
	return nil
}

// postPOReceivedTrueUp posts one line's pre-receipt sale cost true-up at the first receipt
// (round 1, sub item 4, ruling 82.4). Its own function so the branch census finds one journal
// writer per function: the receipt's payload carries one true-up per sold-before-receipt line.
func (s *Service) postPOReceivedTrueUp(ctx context.Context, p poReceivedPayload, tu poTrueUpLine, invCode string) error {
	cogsCode, err := s.rules.ResolveRule(ctx, "order.fulfilled", "cogs", p.BranchID)
	if err != nil || cogsCode == "" {
		cogsCode = "5000"
	}
	amt := tu.DeltaCents
	if amt < 0 {
		amt = -amt
	}
	var lines []GLLine
	if tu.DeltaCents > 0 {
		lines = []GLLine{
			{AccountCode: cogsCode, DebitCents: amt, Memo: "pre-receipt sale COGS true-up"},
			{AccountCode: invCode, CreditCents: amt, Memo: "pre-receipt sale inventory true-up"},
		}
	} else {
		lines = []GLLine{
			{AccountCode: invCode, DebitCents: amt, Memo: "pre-receipt sale inventory true-up"},
			{AccountCode: cogsCode, CreditCents: amt, Memo: "pre-receipt sale COGS true-up"},
		}
	}
	if _, err := s.gl.PostJournal(ctx, GLJournal{
		Source:         "ADJUSTMENT",
		SourceRefID:    p.ReceiptID,
		IdempotencyKey: "pre_receipt_trueup:" + p.ReceiptID + ":" + tu.ProductID,
		Memo:           "Pre-receipt sale cost true-up on first receipt",
		BranchID:       p.BranchID,
		DocumentDate:   p.OccurredAt,
		Lines:          lines,
	}); err != nil {
		return err
	}
	return nil
}

type inventoryAdjustedPayload struct {
	SourceRefID string `json:"source_ref_id"`
	BranchID    string `json:"branch_id"`
	AccountCode string `json:"account_code"` // reason (expense/contra) leg; "" → resolve the rule default
	ValueCents  int64  `json:"value_cents"`  // signed: >0 stock found (asset up) / <0 loss (asset down)
	Memo        string `json:"memo"`
	// OccurredAt is the instant the counted movement happened, the one the count's own
	// transaction stamped on the COUNT move (W2-5 fix r8, S5's P3-1). The journal is dated with
	// it, so the variance journal and the count move that caused it carry the same moment.
	//
	// The zero value means "no instant carried": the journal falls back to the GL's clock, which
	// is what every adjustment did before the field existed.
	OccurredAt time.Time `json:"occurred_at"`
}

// postInventoryAdjusted posts a reason-coded stock adjustment: the reason leg (payload account_code, or the
// rule default 5200) against the 1200 inventory leg resolved through the GL posting rules. A positive value is
// a gain (DR 1200 / CR reason); a negative value is a loss (DR reason / CR 1200). Idempotent on source_ref_id.
func (s *Service) postInventoryAdjusted(ctx context.Context, payload []byte) error {
	var p inventoryAdjustedPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return fmt.Errorf("inventory: inventory.adjusted payload: %w", err)
	}
	if p.SourceRefID == "" {
		return fmt.Errorf("inventory: inventory.adjusted missing source_ref_id")
	}
	if p.ValueCents == 0 {
		return nil
	}
	if s.gl == nil {
		return errNoGLPoster
	}
	if s.rules == nil {
		return errNoRules
	}
	invCode, err := s.rules.ResolveRule(ctx, "inventory.adjusted", "inventory", p.BranchID)
	if err != nil {
		return err
	}
	reasonCode := p.AccountCode
	if reasonCode == "" {
		reasonCode, err = s.rules.ResolveRule(ctx, "inventory.adjusted", "expense", p.BranchID)
		if err != nil {
			return err
		}
	}
	amt := p.ValueCents
	if amt < 0 {
		amt = -amt
	}
	var lines []GLLine
	if p.ValueCents > 0 { // found stock, inventory asset up
		lines = []GLLine{
			{AccountCode: invCode, DebitCents: amt, Memo: "inventory found"},
			{AccountCode: reasonCode, CreditCents: amt, Memo: p.Memo},
		}
	} else { // loss, inventory asset down
		lines = []GLLine{
			{AccountCode: reasonCode, DebitCents: amt, Memo: p.Memo},
			{AccountCode: invCode, CreditCents: amt, Memo: "inventory loss"},
		}
	}
	_, err = s.gl.PostJournal(ctx, GLJournal{
		Source:         "ADJUSTMENT",
		SourceRefID:    p.SourceRefID,
		IdempotencyKey: "inventory.adjusted:" + p.SourceRefID,
		Memo:           "inventory adjustment",
		BranchID:       p.BranchID,
		DocumentDate:   p.OccurredAt,
		Lines:          lines,
	})
	return err
}

// - count + landed seams -

// postCountAdjustment writes the signed COUNT move for a counted variance (A-15). A zero delta books nothing.
//
// W2-5 fix round 8 (S5's P3-1): the count reads its transaction's instant ONCE and stamps it on the COUNT
// move and on the rounding journal it may raise, so the journal and the move it corrects carry the same
// moment. Before this the move took the column default (the row's own now()) and the journal took the GL's
// clock, the two apart: a count in a month's last moment, journaled after the month's end, left the two
// sides of the variance on different sides of the month's end.
func (s *Service) postCountAdjustment(ctx context.Context, tx pgx.Tx, productID, locationID, branchID string, qtyDelta float64, unitCostCents int64, reason string) (string, error) {
	if math.Abs(qtyDelta) <= qtyEpsilon {
		return "", nil
	}
	value, err := qtyTimesCents(math.Abs(qtyDelta), unitCostCents)
	if err != nil {
		return "", err
	}
	// The count's ONE instant, read once, before any row is written.
	at, err := s.movementInstant(ctx, tx)
	if err != nil {
		return "", err
	}
	// W2-5 round 5 (S5's P2-1): the COUNT move stamps the row's exact change in
	// round(on_hand x avg), not the rounded movement; the difference rides the WAC
	// integer-cent rounding journal in this same transaction, the PO receipt's rule.
	// The count never moves the weighted average, so the effective average is the
	// cost row's own before and after (0 when none exists, the read's COALESCE).
	avg, hadCost, err := s.db().GetBranchCost(ctx, tx, productID, branchID)
	if err != nil {
		return "", err
	}
	avgEff := int64(0)
	if hadCost {
		avgEff = avg
	}
	change, err := s.exactRowChange(ctx, tx, productID, branchID, locationID, qtyDelta, avgEff, avgEff)
	if err != nil {
		return "", err
	}
	m := moveParams{
		ProductID: productID, BranchID: branchID, MoveType: MoveCount,
		QtyBase: math.Abs(qtyDelta), UnitCostCents: unitCostCents,
		ValueCents: value,
		SourceType: "count", Reason: reason,
		OccurredAt: at,
	}
	if qtyDelta > 0 {
		m.ToLocation = locationID
	} else {
		m.FromLocation = locationID
	}
	stamp := change
	if qtyDelta < 0 {
		stamp = -change
	}
	m.Metadata = map[string]any{"wac_value_delta_cents": stamp}
	moveID, err := s.db().InsertMove(ctx, tx, m)
	if err != nil {
		return "", err
	}
	if err := s.db().UpsertBalanceDelta(ctx, tx, productID, locationID, branchID, qtyDelta); err != nil {
		return "", err
	}
	rounding := change - value
	if qtyDelta < 0 {
		rounding = change + value
	}
	if rounding != 0 {
		if err := s.postMoveRoundingTx(ctx, tx, moveRoundingPost{
			Event: "inventory.count", MoveID: moveID, BranchID: branchID, RoundingCents: rounding,
			InvEvent: "inventory.adjusted", Memo: "WAC integer-cent rounding on stock count",
			OccurredAt: at,
		}); err != nil {
			return "", err
		}
	}
	return moveID, nil
}

// capitalizeLanded raises the weighted-average cost of a product at a branch by allocatedCadCents worth of
// landed cost (A-16) and returns the value the perpetual subledger ACTUALLY absorbed, the change in
// round(avg × on-hand), which is exactly the quantity the W5 gl_tieout report measures against GL 1200. Two
// facts make the return value load-bearing, and both were live GL-drift bugs before this fix:
//   - with no positive on-hand there is nothing to capitalize into, so it absorbs 0 (the caller expenses the
//     whole share to COGS rather than stranding a phantom 1200 debit); and
//   - integer-cent average costing cannot represent an arbitrary added value exactly, so the sub-cent rounding
//     remainder (allocatedCadCents − absorbed) is reported back for the caller to route to COGS.
//
// The GL 1200 debit is then booked from the returned absorbed total, never from the raw allocation. Crucially the
// SAME (clamped) newAvg is both upserted and used to derive absorbed, so absorbed == round(newAvg × on-hand) −
// oldValue exactly, the ledger can never drift from the subledger it mirrors, and the un-absorbed remainder
// (allocatedCadCents − absorbed) is the only value that falls to COGS.
func (s *Service) capitalizeLanded(ctx context.Context, tx pgx.Tx, productID, branchID string, allocatedCadCents int64, at time.Time, sourceRefID string) (int64, error) {
	absorbed, _, _, err := s.capitalizeLandedSnap(ctx, tx, productID, branchID, allocatedCadCents, at, sourceRefID)
	return absorbed, err
}

// capitalizeLandedSnap is capitalizeLanded plus the exact pre-Apply average it capitalized OVER, the snapshot the
// landedcost Void path persists (migration 0058: landed_cost_allocations.pre_apply_avg_unit_cost) and later restores
// through restoreLandedCost, so an Apply/Void round-trip reproduces the WAC to the cent even when the on-hand is a
// sub-unit fraction that makes round(oldAvg × on-hand) quantize away oldAvg (L8 MAJOR). It returns:
//   - absorbed:  the realized subledger value delta (what GL 1200 is debited), identical to capitalizeLanded;
//   - snapAvg:   the exact per-unit average that was in effect BEFORE this capitalization (0 when no cost row yet);
//   - hadCost:   whether a product_branch_costs row already existed (so a Void can restore "no row" vs a real avg).
//
// snapAvg is the TRUE pre-Apply price, NOT the round(oldAvg × on-hand)/on-hand re-derivation the forward math uses -
// that re-derivation is exactly what the reversal cannot invert once on-hand < 1 quantizes oldValue to 0. Persisting
// snapAvg and restoring it verbatim on Void is the only cent-exact inverse.
func (s *Service) capitalizeLandedSnap(ctx context.Context, tx pgx.Tx, productID, branchID string, allocatedCadCents int64, at time.Time, sourceRefID string) (absorbed int64, snapAvg int64, hadCost bool, err error) {
	if allocatedCadCents == 0 {
		return 0, 0, false, nil
	}
	// Serialize the WAC read-modify-write on (product, branch) FIRST (before on-hand + WAC reads) so a concurrent PO
	// receipt or another landed-cost Apply/Void on the same product+branch blocks instead of lost-updating the WAC
	// this capitalization is about to raise, the L8 MONEY-CRITICAL lost update that unties the subledger from GL 1200.
	if err = s.db().LockBranchCost(ctx, tx, productID, branchID); err != nil {
		return 0, 0, false, err
	}
	onHand, err := s.db().OnHandQtyBranch(ctx, tx, productID, branchID)
	if err != nil {
		return 0, 0, false, err
	}
	oldAvg, hadCost, err := s.db().GetBranchCost(ctx, tx, productID, branchID)
	if err != nil {
		return 0, 0, false, err
	}
	if onHand <= 0 {
		return 0, oldAvg, hadCost, nil // nothing on hand to capitalize into, caller expenses the whole share to COGS
	}
	// Raise the total on-hand value by the allocation, re-derive the integer per-unit average, and report the
	// realized value delta so the GL debit matches the subledger to the cent. Every value here is exact
	// integer arithmetic on the on-hand's DECIMAL(19,4) scaled integer (HH-183): the float shapes rounded
	// exact halves down, off by a cent from the SQL ROUND legs reading the same stock.
	onHandScaled, ok := scaledFromFloat(onHand, qtyScale)
	if !ok || onHandScaled <= 0 {
		return 0, 0, false, fmt.Errorf("inventory: on-hand %v for product %s at branch %s is below or beyond the exact DECIMAL(19,4) grain", onHand, productID, branchID)
	}
	oldValue, ok := mulScaledHalfUp(onHandScaled, oldAvg, qtyScale)
	if !ok {
		return 0, 0, false, fmt.Errorf("inventory: on-hand value for product %s at branch %s exceeds integer cents", productID, branchID)
	}
	capValue, ok := addCents(oldValue, allocatedCadCents)
	if !ok {
		err = fmt.Errorf("inventory: capitalized value for product %s at branch %s exceeds integer cents", productID, branchID)
		return 0, 0, false, err
	}
	newAvg, err := centsDivQty(capValue, onHand)
	if err != nil {
		return 0, 0, false, err
	}
	// Clamp the PERPETUAL WAC (not just the reported delta) so the realized subledger change never overshoots the
	// allocation it was handed (DIB-189). Quantizing newAvg to whole cents/unit and re-multiplying by on-hand
	// makes the realized value delta round(newAvg*onHand)-oldValue drift past the allocation by up to ~onHand/2
	// cents in EITHER direction. The GL 1200 tie-out (W5 gl_tieout / W6 B1) measures exactly this realized delta
	//, round(avg × on-hand), so newAvg and the returned `absorbed` MUST move together: if we clamped only the
	// reported delta while upserting an un-clamped newAvg, GL 1200 would be booked the clamped amount while the
	// subledger rose by the overshoot, permanently breaking subledger == GL 1200. Instead we step newAvg back
	// toward oldAvg by whole cents until its realized delta lands inside [0, allocatedCadCents] (or
	// [allocatedCadCents, 0] when reversing for Void), then derive `absorbed` from that same clamped newAvg. The
	// un-representable sub-cent remainder (allocatedCadCents − absorbed) falls to COGS, exactly the doctrine this
	// seam already intends, and because the same newAvg is both upserted and returned, GL 1200 == the perpetual
	// subledger to the cent, and an Apply/Void round-trip restores the WAC exactly (no phantom inventory value).
	// realizedDelta is exact over the same scaled on-hand; ok=false only when the realized value cannot
	// be an int64 of cents, in which case the walk stops and the final realizedDelta below surfaces the
	// error rather than clamping against a number nobody can represent.
	realizedDelta := func(avg int64) (int64, bool) {
		v, ok := mulScaledHalfUp(onHandScaled, avg, qtyScale)
		if !ok {
			return 0, false
		}
		return subCents(v, oldValue)
	}
	if allocatedCadCents >= 0 {
		for {
			d, ok := realizedDelta(newAvg)
			if !ok || d <= allocatedCadCents || newAvg <= oldAvg {
				break
			}
			newAvg--
		}
		for {
			d, ok := realizedDelta(newAvg)
			if !ok || d >= 0 || newAvg >= oldAvg {
				break
			}
			newAvg++
		}
	} else {
		for {
			d, ok := realizedDelta(newAvg)
			if !ok || d >= allocatedCadCents || newAvg >= oldAvg {
				break
			}
			newAvg++
		}
		for {
			d, ok := realizedDelta(newAvg)
			if !ok || d <= 0 || newAvg <= oldAvg {
				break
			}
			newAvg--
		}
	}
	// A weighted-average cost is a price; it can never be negative (DIB-189 / L8). On a Void reversal against a
	// small on-hand, e.g. the freight capitalized at Apply over a large on-hand is now reversed over the few
	// units still on hand after a sale, (oldValue+allocatedCadCents)/onHand can fall below zero, so the raw
	// newAvg is negative. The clamp loops above only step newAvg TOWARD oldAvg and stop once the realized delta
	// is in-interval, so they never lift a negative newAvg back to a sane price. Floor newAvg at 0 and derive
	// `absorbed` from that SAME floored value: the reversal then realizes exactly -oldValue (drains the on-hand
	// value to zero, no more), which still equals the perpetual subledger delta round(0*onHand)-oldValue, so GL
	// 1200 stays tied to the subledger on the void-after-sale path. The un-reversed remainder
	// (allocatedCadCents − absorbed) is the caller's residual, routed to COGS exactly as an over-on-hand share is.
	if newAvg < 0 {
		newAvg = 0
	}
	absorbed, ok = realizedDelta(newAvg)
	if !ok {
		err = fmt.Errorf("inventory: realized on-hand value for product %s at branch %s exceeds integer cents", productID, branchID)
		return 0, 0, false, err
	}
	// Belt-and-suspenders: pin absorbed inside the allocation interval in case on-hand is small enough that no
	// integer WAC lands its realized delta exactly in-range (then the residual, still the allocation's sign,
	// falls wholly to COGS). absorbed == round(newAvg*onHand)-oldValue whenever the interval was reachable, which
	// is what keeps GL 1200 tied to the upserted subledger.
	if allocatedCadCents >= 0 {
		if absorbed < 0 {
			absorbed = 0
		} else if absorbed > allocatedCadCents {
			absorbed = allocatedCadCents
		}
	} else {
		if absorbed > 0 {
			absorbed = 0
		} else if absorbed < allocatedCadCents {
			absorbed = allocatedCadCents
		}
	}
	if err := s.db().UpsertBranchCost(ctx, tx, productID, branchID, newAvg); err != nil {
		return 0, 0, false, err
	}
	// The VALUE move this capitalization books (W2-5B): the exact cents the GL posts to 1200 for it, at the
	// instant that GL journal carries, so the stock side and 1200 cut the event together.
	if err := s.bookValueEvent(ctx, tx, productID, branchID, absorbed, at, sourceRefID); err != nil {
		return 0, 0, false, err
	}
	// snapAvg is oldAvg, the exact price BEFORE this capitalization, so a later restoreLandedCost can reproduce it
	// verbatim. hadCost tells the caller whether that snapshot was a real stored average or the 0 default.
	return absorbed, oldAvg, hadCost, nil
}

// bookValueEvent records the VALUE ONLY move (0453, W2-5B) that carries the value a landed-cost
// capitalization, a retro capitalization or a restore moved: the exact cents the GL posting books for that
// event, at the instant the event's own GL journal carries. The close pre flight's stock tie unwinds it, so a
// month that closed before the event excludes it from both sides of the tie and a month that closed after it
// includes it on both sides. An event that moved no value books no row.
func (s *Service) bookValueEvent(ctx context.Context, tx pgx.Tx, productID, branchID string, valueCents int64, at time.Time, sourceRefID string) error {
	if valueCents == 0 {
		return nil
	}
	reason := "landed cost capitalization"
	if valueCents < 0 {
		reason = "landed cost reversal"
	}
	_, err := s.db().InsertMove(ctx, tx, moveParams{
		ProductID: productID, BranchID: branchID, MoveType: MoveValue,
		QtyBase: 0, ValueCents: valueCents,
		SourceType: "landed_cost", SourceRefID: sourceRefID, Reason: reason,
		OccurredAt: at,
	})
	return err
}

// restoreLandedCost reverses a landed-cost capitalization on a Void by REMOVING EXACTLY the value Apply capitalized -
// `absorbed`, the amount that this same allocation raised the perpetual subledger by (landedcost stores it as the
// allocation's AllocatedCadCents after Apply rewrites it). It returns the realized subledger value delta (negative -
// the value drained from inventory) that the caller books as the CR 1200 leg. This is the GENERAL cent-exact inverse
// for EVERY intervening scenario (nothing / receipt / sale) between Apply and Void, and the money-critical property is
// that Σ(debit) == Σ(credit) ALWAYS and NO leg is ever negative (L8 MAJOR, 3rd void edge, general principle).
//
// Why not the old delta = round(snapAvg×onHand) − round(curAvg×onHand)? It measured against the CURRENT on-hand, so an
// intervening RECEIPT (on-hand GREW between Apply and Void) makes round(curAvg×onHand) span the whole enlarged stock,
// not just the units Apply capitalized over, the delta then over-reverses (e.g. Apply 5000 over oh=10, receive 90 @ the
// post-Apply avg → oh=100, old formula credits 50000: 10× the 5000 absorbed → a negative CR 5000 leg → posting rejects →
// the doc is permanently un-voidable). Removing exactly `absorbed` instead reverses the landed-cost contribution and
// nothing else, no matter how much unrelated receipt value now sits alongside it.
//
// Method: new inventory value = clamp-at-zero(currentValue − absorbed); new WAC = round(newValue / onHand), floored at
// 0 (a WAC is a price and can never be negative, the void-after-sale floor, preserved). The realized delta is derived
// from that SAME upserted newAvg (round(newAvg×onHand) − currentValue) so GL 1200 == the perpetual subledger to the
// cent; the un-reversible remainder (absorbed the stock has since sold through, so the value is simply not there to
// drain) is the residual (absorbed − |realized|) the caller routes to COGS, exactly as the sold-through leg always was.
//
// SPECIAL CASE, on-hand UNCHANGED since Apply (drift-0 path, round-2 behavior preserved): when the stock is untouched,
// removing `absorbed` is equivalent to restoring the exact pre-Apply snapshot avg, and restoring snapAvg VERBATIM keeps
// the WAC cent-exact even for sub-unit fractional on-hand where re-deriving round(newValue/onHand) would re-quantize and
// drift. We detect this without a stored pre-Apply on-hand: on-hand is (value-)unchanged iff removing absorbed lands the
// value exactly on round(snapAvg×onHand), i.e. currentValue − absorbed == round(snapAvg×onHand), because that is by
// construction what Apply computed absorbed as over this same on-hand. When it holds, restore snapAvg verbatim (drift 0);
// otherwise on-hand differs, so use the absorbed-based drain.
func (s *Service) restoreLandedCost(ctx context.Context, tx pgx.Tx, productID, branchID string, snapAvg, absorbed int64, at time.Time, sourceRefID string) (int64, error) {
	if snapAvg < 0 {
		snapAvg = 0 // a stored average is never negative; defend anyway so the restored WAC can never go negative
	}
	// Serialize the WAC read-modify-write on (product, branch) FIRST so a concurrent PO receipt or another
	// landed-cost Apply/Void on the same product+branch blocks instead of reading the same current WAC and
	// clobbering this reversal, the L8 MONEY-CRITICAL lost update that unties the subledger from GL 1200.
	if err := s.db().LockBranchCost(ctx, tx, productID, branchID); err != nil {
		return 0, err
	}
	onHand, err := s.db().OnHandQtyBranch(ctx, tx, productID, branchID)
	if err != nil {
		return 0, err
	}
	curAvg, _, err := s.db().GetBranchCost(ctx, tx, productID, branchID)
	if err != nil {
		return 0, err
	}
	if onHand <= 0 {
		// No units left to hold value: the realized subledger delta is 0 (round(x × 0) − round(y × 0)) and there is no
		// WAC row to keep. The absorbed value the units used to carry has fully sold through, so the whole `absorbed`
		// rolls to COGS in the caller (reversedTotal contributes 0). Persist the snapshot avg anyway so the perpetual
		// WAC returns to its pre-Apply value for the next receipt, a no-op on the realized delta.
		if err := s.db().UpsertBranchCost(ctx, tx, productID, branchID, snapAvg); err != nil {
			return 0, err
		}
		return 0, nil
	}
	curValue, err := qtyTimesCents(onHand, curAvg)
	if err != nil {
		return 0, err
	}
	snapValue, err := qtyTimesCents(onHand, snapAvg)
	if err != nil {
		return 0, err
	}
	// Remove exactly the absorbed value; the subtraction is guarded so an unrepresentable difference errors
	// instead of wrapping (the helpers guarantee their own results, not the arithmetic between them).
	remaining, ok := subCents(curValue, absorbed)
	if !ok {
		return 0, fmt.Errorf("inventory: post-reversal value for product %s at branch %s exceeds integer cents", productID, branchID)
	}
	// On-hand unchanged since Apply → restoring snapAvg verbatim is the cent-exact, drift-0 inverse (round-2 path).
	if remaining == snapValue {
		delta, ok := subCents(snapValue, curValue)
		if !ok {
			return 0, fmt.Errorf("inventory: restore delta for product %s at branch %s exceeds integer cents", productID, branchID)
		}
		if err := s.db().UpsertBranchCost(ctx, tx, productID, branchID, snapAvg); err != nil {
			return 0, err
		}
		if err := s.bookValueEvent(ctx, tx, productID, branchID, delta, at, sourceRefID); err != nil {
			return 0, err
		}
		return delta, nil
	}
	// On-hand differs (receipt or sale between Apply and Void): remove EXACTLY the capitalized value, no more. Clamp the
	// post-reversal value at zero (a void can drain at most all the value in stock, never manufacture a negative one)
	// and floor the derived WAC at zero (a WAC is a price).
	newValue := remaining
	if newValue < 0 {
		newValue = 0
	}
	newAvg, err := centsDivQty(newValue, onHand)
	if err != nil {
		return 0, err
	}
	if newAvg < 0 {
		newAvg = 0
	}
	// Derive the realized delta from the SAME upserted newAvg so GL 1200 == the perpetual subledger to the cent; the
	// caller routes the un-reversible remainder (absorbed − |delta|) to COGS.
	realized, err := qtyTimesCents(onHand, newAvg)
	if err != nil {
		return 0, err
	}
	delta, ok := subCents(realized, curValue)
	if !ok {
		return 0, fmt.Errorf("inventory: realized restore delta for product %s at branch %s exceeds integer cents", productID, branchID)
	}
	if err := s.db().UpsertBranchCost(ctx, tx, productID, branchID, newAvg); err != nil {
		return 0, err
	}
	if err := s.bookValueEvent(ctx, tx, productID, branchID, delta, at, sourceRefID); err != nil {
		return 0, err
	}
	return delta, nil
}
