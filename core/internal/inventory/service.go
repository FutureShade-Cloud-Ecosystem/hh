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

// - PO receiving -

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

	var totalValue int64
	linesRecvd := 0
	allFull := true
	type evLine struct {
		ProductID  string  `json:"product_id"`
		QtyBase    float64 `json:"qty_base"`
		ValueCents int64   `json:"value_cents"`
	}
	var eventLines []evLine

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
		base := math.Max(onHand, 0)
		newAvg := cadUnit
		if denom := base + recvQty; denom > 0 {
			newAvg, err = blendAvg(base, oldAvg, recvQty, cadUnit)
			if err != nil {
				return ReceiptResult{}, err
			}
		}
		if err := s.db().UpsertBranchCost(ctx, tx, ln.ProductID, hdr.BranchID, newAvg); err != nil {
			return ReceiptResult{}, err
		}
		if _, err := s.db().InsertMove(ctx, tx, moveParams{
			ProductID: ln.ProductID, BranchID: hdr.BranchID, ToLocation: hdr.BranchID, UOM: ln.UOM,
			MoveType: MoveReceipt, QtyBase: recvQty, UnitCostCents: cadUnit, ValueCents: recvValue,
			SourceType: "po_receipt", SourceRefID: receiptID, Reason: "PO receipt " + hdr.PONo,
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
		"value_cents": totalValue, "lines": eventLines,
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
// DELIVERED / COLLECT_LATER record ZERO moves and surface on pending-fulfilment, the PARTIAL closure.
func (s *Service) decrementForSale(ctx context.Context, tx pgx.Tx, in DecrementInput) error {
	if in.SaleType != SaleCollectNow {
		return nil // boundary: deferred fulfilment books no move at W6
	}
	for _, ln := range in.Lines {
		if ln.QtyBase <= 0 {
			continue
		}
		avg, _, err := s.db().GetBranchCost(ctx, tx, ln.ProductID, in.BranchID)
		if err != nil {
			return err
		}
		value, err := qtyTimesCents(ln.QtyBase, avg)
		if err != nil {
			return err
		}
		if _, err := s.db().InsertMove(ctx, tx, moveParams{
			ProductID: ln.ProductID, BranchID: in.BranchID, FromLocation: in.BranchID,
			MoveType: MoveShip, QtyBase: ln.QtyBase, UnitCostCents: avg, ValueCents: value,
			SourceType: "pos_sale", SourceRefID: in.OrderID, Reason: "POS COLLECT_NOW sale",
		}); err != nil {
			return err
		}
		if err := s.db().UpsertBalanceDelta(ctx, tx, ln.ProductID, in.BranchID, in.BranchID, -ln.QtyBase); err != nil {
			return err
		}
	}
	return nil
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
	for _, ln := range in.Lines {
		if ln.QtyBase <= 0 {
			continue
		}
		avg, _, err := s.db().GetBranchCost(ctx, tx, ln.ProductID, in.BranchID)
		if err != nil {
			return err
		}
		value, err := qtyTimesCents(ln.QtyBase, avg)
		if err != nil {
			return err
		}
		if _, err := s.db().InsertMove(ctx, tx, moveParams{
			ProductID: ln.ProductID, BranchID: in.BranchID, ToLocation: in.BranchID,
			MoveType: MoveReturn, QtyBase: ln.QtyBase, UnitCostCents: avg, ValueCents: value,
			SourceType: "pos_return", SourceRefID: in.ReturnID, Reason: "POS return " + in.ReturnNo,
		}); err != nil {
			return err
		}
		if err := s.db().UpsertBalanceDelta(ctx, tx, ln.ProductID, in.BranchID, in.BranchID, ln.QtyBase); err != nil {
			return err
		}
	}
	return nil
}

// - posting consumers (po.received + inventory.adjusted) -

type poReceivedPayload struct {
	POID       string `json:"po_id"`
	ReceiptID  string `json:"receipt_id"`
	BranchID   string `json:"branch_id"`
	ValueCents int64  `json:"value_cents"`
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
	if p.ValueCents == 0 {
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
	_, err = s.gl.PostJournal(ctx, GLJournal{
		Source:         "PO_RECEIPT",
		SourceRefID:    p.ReceiptID,
		IdempotencyKey: "po.received:" + p.ReceiptID,
		Memo:           "GRNI on PO receipt",
		BranchID:       p.BranchID,
		Lines: []GLLine{
			{AccountCode: invCode, DebitCents: p.ValueCents, Memo: "inventory received"},
			{AccountCode: apCode, CreditCents: p.ValueCents, Memo: "received not invoiced"},
		},
	})
	return err
}

type inventoryAdjustedPayload struct {
	SourceRefID string `json:"source_ref_id"`
	BranchID    string `json:"branch_id"`
	AccountCode string `json:"account_code"` // reason (expense/contra) leg; "" → resolve the rule default
	ValueCents  int64  `json:"value_cents"`  // signed: >0 stock found (asset up) / <0 loss (asset down)
	Memo        string `json:"memo"`
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
		Lines:          lines,
	})
	return err
}

// - count + landed seams -

// postCountAdjustment writes the signed COUNT move for a counted variance (A-15). A zero delta books nothing.
func (s *Service) postCountAdjustment(ctx context.Context, tx pgx.Tx, productID, locationID, branchID string, qtyDelta float64, unitCostCents int64, reason string) (string, error) {
	if math.Abs(qtyDelta) <= qtyEpsilon {
		return "", nil
	}
	value, err := qtyTimesCents(math.Abs(qtyDelta), unitCostCents)
	if err != nil {
		return "", err
	}
	m := moveParams{
		ProductID: productID, BranchID: branchID, MoveType: MoveCount,
		QtyBase: math.Abs(qtyDelta), UnitCostCents: unitCostCents,
		ValueCents: value,
		SourceType: "count", Reason: reason,
	}
	if qtyDelta > 0 {
		m.ToLocation = locationID
	} else {
		m.FromLocation = locationID
	}
	id, err := s.db().InsertMove(ctx, tx, m)
	if err != nil {
		return "", err
	}
	if err := s.db().UpsertBalanceDelta(ctx, tx, productID, locationID, branchID, qtyDelta); err != nil {
		return "", err
	}
	return id, nil
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
func (s *Service) capitalizeLanded(ctx context.Context, tx pgx.Tx, productID, branchID string, allocatedCadCents int64) (int64, error) {
	absorbed, _, _, err := s.capitalizeLandedSnap(ctx, tx, productID, branchID, allocatedCadCents)
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
func (s *Service) capitalizeLandedSnap(ctx context.Context, tx pgx.Tx, productID, branchID string, allocatedCadCents int64) (absorbed int64, snapAvg int64, hadCost bool, err error) {
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
	// snapAvg is oldAvg, the exact price BEFORE this capitalization, so a later restoreLandedCost can reproduce it
	// verbatim. hadCost tells the caller whether that snapshot was a real stored average or the 0 default.
	return absorbed, oldAvg, hadCost, nil
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
func (s *Service) restoreLandedCost(ctx context.Context, tx pgx.Tx, productID, branchID string, snapAvg, absorbed int64) (int64, error) {
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
	return delta, nil
}
