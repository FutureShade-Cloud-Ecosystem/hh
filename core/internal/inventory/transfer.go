package inventory

// transfer.go, DIB-71: inter-branch stock transfers (Trenton <-> Kingston).
//
// A transfer is a PAIR of stock moves separated in time. The truck is loaded at the source and
// unloaded at the destination hours or days later, and between those two events the units belong to
// neither branch's on-hand. Modelling it as a single instantaneous move would be a lie the balance
// sheet notices: the value would be at the destination before the pallets were.
//
// THE PAIR
//
//	ship    → stock_moves(from = source branch, to = NULL)  and  -qty on the source balance
//	receive → stock_moves(from = NULL, to = dest branch)    and  +qty on the destination balance
//
// Each leg reconciles against v_stock_balance_drift on its own (that view splits every move into a
// +qty leg at to_location and a -qty leg at from_location, 0022), so the whole-DB drift gate stays
// at zero for the entire in-transit window. In-transit is DERIVED from the transfer lines
// (qty_shipped - qty_received), not parked in a stock_balances row; migration 0116 explains at
// length why a TRANSIT location was rejected (every existing on-hand read in the tree sums
// stock_balances by branch with no location filter, including one in another swarm's fence).
//
// WHAT IS ACTUALLY ASSERTED. "Balanced double-entry" is meaningful over the pair, not over one leg:
// for every RECEIVED transfer, units out of source == units into destination, and system-wide
// on-hand is conserved. That is the leased invariant test
// (TestIntegration_Transfer_DoubleEntryBalances_Leased).
//
// THE OVERDRAFT GUARD sits at SHIP, not at create. A draft transfer is a plan and a plan may be
// optimistic; loading a truck is a physical commitment and it grades against AVAILABLE (on-hand
// minus hard holds, DIB-60) at the source. Grading on on-hand alone would let a transfer walk off
// with pallets already promised to a confirmed order, the exact oversell reservations exist to
// stop, arriving through the back door.

import (
	"context"
	"fmt"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
	"github.com/jackc/pgx/v5"
)

// Transfer statuses (stock_transfers.status CHECK set, 0116).
const (
	TransferDraft     = "DRAFT"
	TransferInTransit = "IN_TRANSIT"
	TransferReceived  = "RECEIVED"
	TransferCancelled = "CANCELLED"
)

// MoveTransfer is the 0005 move_type for both legs of a transfer.
const MoveTransfer = "TRANSFER"

// transferSource is the stock_moves.source_type both legs carry, so the ledger can be filtered back
// to the transfer that caused it.
const transferSource = "stock_transfer"

// Transfer is one inter-branch transfer document.
type Transfer struct {
	ID           string `json:"id"`
	TransferNo   string `json:"transfer_no"`
	FromBranchID string `json:"from_branch_id"`
	ToBranchID   string `json:"to_branch_id"`
	Status       string `json:"status"`
	Notes        string `json:"notes"`
	ShippedAt    string `json:"shipped_at"`
	ReceivedAt   string `json:"received_at"`
	CreatedAt    string `json:"created_at"`
	// ETA is the promised arrival DAY (YYYY-MM-DD), captured at ship time, DIB-472, migration 0158.
	// EMPTY means no promise was made (a branch pair with no configured transit time, or a clerk who
	// cleared the field), and the surface then says "shipped <date>" exactly as it did before.
	ETA   string         `json:"eta"`
	Lines []TransferLine `json:"lines"`
}

// TransferLine is one product on a transfer, carrying both halves of the pair.
type TransferLine struct {
	ID              string  `json:"id"`
	LineNo          int     `json:"line_no"`
	ProductID       string  `json:"product_id"`
	SKU             string  `json:"sku"`
	ProductName     string  `json:"product_name"`
	UOM             string  `json:"uom"`
	QtyBase         float64 `json:"qty_base"`
	QtyShippedBase  float64 `json:"qty_shipped_base"`
	QtyReceivedBase float64 `json:"qty_received_base"`
	UnitCostCents   int64   `json:"unit_cost_cents,omitempty"`
	ShipMoveID      string  `json:"ship_move_id"`
	RecvMoveID      string  `json:"recv_move_id"`
}

// InTransitBase is the units still on the truck for this line, the derived IN_TRANSIT bucket.
func (l TransferLine) InTransitBase() float64 { return l.QtyShippedBase - l.QtyReceivedBase }

// CreateTransferInput opens a draft transfer.
type CreateTransferInput struct {
	FromBranchID string               `json:"from_branch_id"`
	ToBranchID   string               `json:"to_branch_id"`
	Notes        string               `json:"notes"`
	Lines        []CreateTransferLine `json:"lines"`
	CreatedBy    string               `json:"-"`
}

// CreateTransferLine is one requested product+qty.
type CreateTransferLine struct {
	ProductID string  `json:"product_id"`
	QtyBase   float64 `json:"qty_base"`
}

// - create -

// CreateTransfer opens a DRAFT transfer between two branches. It validates the shape and mints the
// branch-prefixed number off the SOURCE branch (#TR-TRT-7), but it does NOT touch stock: a draft is
// a plan, and holding stock for a plan that may never be loaded starves the yard that still has it.
func (s *Service) CreateTransfer(ctx context.Context, in CreateTransferInput) (Transfer, error) {
	if in.FromBranchID == "" || in.ToBranchID == "" {
		return Transfer{}, badReq("inventory: from_branch_id and to_branch_id are required")
	}
	if in.FromBranchID == in.ToBranchID {
		return Transfer{}, badReq("inventory: a transfer must cross branches")
	}
	if len(in.Lines) == 0 {
		return Transfer{}, badReq("inventory: a transfer needs at least one line")
	}
	for i, l := range in.Lines {
		if l.ProductID == "" {
			return Transfer{}, badReq("inventory: line %d has no product_id", i+1)
		}
		if l.QtyBase <= 0 {
			return Transfer{}, badReq("inventory: line %d qty_base must be > 0", i+1)
		}
	}
	tx, err := s.repo.Begin(ctx)
	if err != nil {
		return Transfer{}, err
	}
	defer tx.Rollback(ctx)

	t, err := s.repo.InsertTransfer(ctx, tx, in)
	if err != nil {
		return Transfer{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Transfer{}, err
	}
	s.log.Info("inventory: transfer created", "transfer_no", t.TransferNo,
		"from", in.FromBranchID, "to", in.ToBranchID, "lines", len(in.Lines))
	return s.GetTransfer(ctx, t.ID)
}

// - ship -

// ShipTransfer loads the truck: for each line it grades the source's AVAILABLE stock, writes the
// outbound half of the pair at the source's weighted-average cost, decrements the source balance,
// and posts DR in-transit / CR inventory at the source branch.
//
// One transaction for the whole document. A transfer half-shipped because line 4 overdrew is not a
// state anyone can act on, the truck is either loaded or it is not, so the guard failing on any
// line rolls the whole thing back.
//
// The eta rides in that same transaction (DIB-472). ShipTransfer keeps its shipped signature and means
// "no eta supplied", which is the pre-0158 API contract: the branch pair's configured transit time
// fills the column, and a pair with no row leaves it NULL.
func (s *Service) ShipTransfer(ctx context.Context, transferID, actorID string) (Transfer, error) {
	return s.ShipTransferWithETA(ctx, transferID, actorID, ShipETA{})
}

// ShipTransferWithETA is ShipTransfer with the ship flow's promised-arrival decision attached, see
// transfer_eta.go for the precedence rule (what the clerk saw is what is stored).
func (s *Service) ShipTransferWithETA(ctx context.Context, transferID, actorID string, etaIn ShipETA) (Transfer, error) {
	if transferID == "" {
		return Transfer{}, badReq("inventory: transfer id is required")
	}
	// The dealer's calendar, decided before the transaction opens: a zone read must not hold a
	// connection beside the ship transaction, and a failed read ships nothing.
	zone, err := houseday.Resolve(ctx, s.zone)
	if err != nil {
		return Transfer{}, err
	}
	tx, err := s.repo.Begin(ctx)
	if err != nil {
		return Transfer{}, err
	}
	defer tx.Rollback(ctx)

	hdr, lines, err := s.repo.TransferForUpdate(ctx, tx, transferID)
	if err != nil {
		return Transfer{}, err
	}
	if hdr.Status != TransferDraft {
		return Transfer{}, conflictErr("inventory: transfer %s is %s, only a DRAFT can ship", hdr.TransferNo, hdr.Status)
	}

	var totalValue int64
	for _, ln := range lines {
		// The source's weighted average travels with the stock: moving pallets between yards must
		// neither invent nor destroy value. found=false → cost 0, which is the same quiet skip the
		// POS return restock takes for an uncosted product.
		avg, _, err := s.db().GetBranchCost(ctx, tx, ln.ProductID, hdr.FromBranchID)
		if err != nil {
			return Transfer{}, err
		}

		// NO-OVERDRAFT GUARD. Grades AVAILABLE (on-hand minus hard holds) inside this transaction, so
		// two lines of the same product on one transfer are graded against the running total rather
		// than each against the opening balance.
		onHand, reserved, err := s.db().BalanceFactsTx(ctx, tx, ln.ProductID, hdr.FromBranchID)
		if err != nil {
			return Transfer{}, err
		}
		if err := guardOverdraft(onHand, reserved, ln.QtyBase, hdr.TransferNo, ln.LineNo); err != nil {
			return Transfer{}, err
		}

		value, err := qtyTimesCents(ln.QtyBase, avg)
		if err != nil {
			return Transfer{}, err
		}
		moveID, err := s.db().InsertMove(ctx, tx, moveParams{
			ProductID: ln.ProductID, BranchID: hdr.FromBranchID, FromLocation: hdr.FromBranchID,
			UOM: ln.UOM, MoveType: MoveTransfer, QtyBase: ln.QtyBase, UnitCostCents: avg,
			ValueCents: value, SourceType: transferSource, SourceRefID: hdr.ID,
			Reason: "transfer out " + hdr.TransferNo,
		})
		if err != nil {
			return Transfer{}, err
		}
		if err := s.db().UpsertBalanceDelta(ctx, tx, ln.ProductID, hdr.FromBranchID, hdr.FromBranchID, -ln.QtyBase); err != nil {
			return Transfer{}, err
		}
		if err := s.repo.MarkLineShipped(ctx, tx, ln.ID, ln.QtyBase, avg, moveID); err != nil {
			return Transfer{}, err
		}
		total, ok := addCents(totalValue, value)
		if !ok {
			return Transfer{}, fmt.Errorf("inventory: transfer %s total value exceeds integer cents", hdr.TransferNo)
		}
		totalValue = total
	}

	// The promised arrival is decided BEFORE the header write and inside the same transaction, so a
	// mistyped date refuses the whole shipment (400, nothing moved) rather than leaving a loaded truck
	// with a rolled-back eta. shippedAt is read from the clock here and the UPDATE stamps now(); the two
	// are the same instant to the millisecond in one process, and only the DAY of it is ever used.
	eta, err := s.resolveShipETA(ctx, tx, zone, hdr.FromBranchID, hdr.ToBranchID, etaIn, time.Now())
	if err != nil {
		return Transfer{}, err
	}
	if err := s.repo.SetTransferShipped(ctx, tx, hdr.ID, actorID, eta); err != nil {
		return Transfer{}, err
	}
	if err := s.postTransferLegTx(ctx, tx, transferPost{
		TransferID: hdr.ID, TransferNo: hdr.TransferNo,
		BranchID: hdr.FromBranchID, ValueCents: totalValue,
	}, EventTransferShipped, true); err != nil {
		return Transfer{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Transfer{}, err
	}
	s.log.Info("inventory: transfer shipped", "transfer_no", hdr.TransferNo, "value_cents", totalValue, "eta", eta)
	return s.GetTransfer(ctx, hdr.ID)
}

// guardOverdraft is the no-overdraft rule, extracted so it can be unit-tested and mutated on its own:
// a transfer may not take more than the source branch has AVAILABLE.
//
// The comparison is `available + qtyEpsilon < qty`, not `available < qty`: DECIMAL(19,4) round-trips
// through float64 and an exact-to-the-unit transfer of the whole available balance must not be
// refused because the last bit went the wrong way. The epsilon is 1e-6, six orders of magnitude
// below the smallest representable quantity, so it can only ever absorb noise, never a real unit.
func guardOverdraft(onHandBase, reservedBase, qtyBase float64, transferNo string, lineNo int) error {
	available := AvailableBase(onHandBase, reservedBase)
	if available+qtyEpsilon < qtyBase {
		return conflictErr(
			"inventory: transfer %s line %d would overdraw the source branch, %.4f available (%.4f on hand, %.4f reserved), %.4f requested",
			transferNo, lineNo, available, onHandBase, reservedBase, qtyBase)
	}
	return nil
}

// - receive -

// ReceiveTransfer unloads the truck: the inbound half of the pair at the destination, at the cost
// that travelled with the stock, with the destination's weighted average maintained the same way a
// PO receipt maintains it (lock first, then read-modify-write, the L8 MONEY-CRITICAL ordering).
//
// Receiving completes the pair: after this, in-transit for these lines is zero and units out of the
// source equal units into the destination.
func (s *Service) ReceiveTransfer(ctx context.Context, transferID, actorID string) (Transfer, error) {
	if transferID == "" {
		return Transfer{}, badReq("inventory: transfer id is required")
	}
	tx, err := s.repo.Begin(ctx)
	if err != nil {
		return Transfer{}, err
	}
	defer tx.Rollback(ctx)

	hdr, lines, err := s.repo.TransferForUpdate(ctx, tx, transferID)
	if err != nil {
		return Transfer{}, err
	}
	if hdr.Status != TransferInTransit {
		return Transfer{}, conflictErr("inventory: transfer %s is %s, only an IN_TRANSIT transfer can be received", hdr.TransferNo, hdr.Status)
	}

	var totalValue int64
	var totalRounding int64
	for _, ln := range lines {
		outstanding := ln.QtyShippedBase - ln.QtyReceivedBase
		if outstanding <= qtyEpsilon {
			continue
		}
		value, err := qtyTimesCents(outstanding, ln.UnitCostCents)
		if err != nil {
			return Transfer{}, err
		}

		// Weighted-average maintenance at the DESTINATION, identical in shape to applyReceipt: take
		// the (product, branch) advisory lock BEFORE the on-hand + cost reads so a concurrent PO
		// receipt or landed-cost apply on the same product cannot lost-update the average.
		if err := s.db().LockBranchCost(ctx, tx, ln.ProductID, hdr.ToBranchID); err != nil {
			return Transfer{}, err
		}
		onHand, err := s.db().OnHandQtyBranch(ctx, tx, ln.ProductID, hdr.ToBranchID)
		if err != nil {
			return Transfer{}, err
		}
		oldAvg, _, err := s.db().GetBranchCost(ctx, tx, ln.ProductID, hdr.ToBranchID)
		if err != nil {
			return Transfer{}, err
		}
		base := onHand
		if base < 0 {
			base = 0
		}
		newAvg := ln.UnitCostCents
		if denom := base + outstanding; denom > 0 {
			newAvg, err = blendAvg(base, oldAvg, outstanding, ln.UnitCostCents)
			if err != nil {
				return Transfer{}, err
			}
		}
		// subledgerDelta is the destination's exact change in round(avg x on-hand),
		// the figure the receive's GL postings sum to; it is stamped on the inbound
		// move so the as-of stock read unwinds the transfer to the cent (W2-5 round
		// 3, K3's P1 3). Like applyReceipt's blend, the new average revalues the units
		// already on hand, and the difference against the transferred value is the
		// WAC integer-cent rounding: without the stamp the inbound move answered
		// only outstanding x unit cost and the rounding cent had no exact move or
		// journal, so every earlier as-of answer kept it. An on-hand of zero (or a
		// beyond-grain figure the helpers refused) absorbs exactly the transferred
		// value, the PO receipt's own rule.
		subledgerDelta := value
		var rounding int64
		if base > 0 {
			oldSubledgerVal, errOld := qtyTimesCents(base, oldAvg)
			newSubledgerVal, errNew := qtyTimesCents(base+outstanding, newAvg)
			if errOld == nil && errNew == nil {
				rounding = (newSubledgerVal - oldSubledgerVal) - value
				subledgerDelta = newSubledgerVal - oldSubledgerVal
			}
		}
		if err := s.db().UpsertBranchCost(ctx, tx, ln.ProductID, hdr.ToBranchID, newAvg); err != nil {
			return Transfer{}, err
		}

		moveID, err := s.db().InsertMove(ctx, tx, moveParams{
			ProductID: ln.ProductID, BranchID: hdr.ToBranchID, ToLocation: hdr.ToBranchID,
			UOM: ln.UOM, MoveType: MoveTransfer, QtyBase: outstanding, UnitCostCents: ln.UnitCostCents,
			ValueCents: value, SourceType: transferSource, SourceRefID: hdr.ID,
			Reason:   "transfer in " + hdr.TransferNo,
			Metadata: map[string]any{"wac_value_delta_cents": subledgerDelta},
		})
		if err != nil {
			return Transfer{}, err
		}
		if total, ok := addCents(totalRounding, rounding); ok {
			totalRounding = total
		} else {
			return Transfer{}, fmt.Errorf("inventory: transfer %s total rounding exceeds integer cents", hdr.TransferNo)
		}
		if err := s.db().UpsertBalanceDelta(ctx, tx, ln.ProductID, hdr.ToBranchID, hdr.ToBranchID, outstanding); err != nil {
			return Transfer{}, err
		}
		if err := s.repo.MarkLineReceived(ctx, tx, ln.ID, ln.QtyReceivedBase+outstanding, moveID); err != nil {
			return Transfer{}, err
		}
		total, ok := addCents(totalValue, value)
		if !ok {
			return Transfer{}, fmt.Errorf("inventory: transfer %s total value exceeds integer cents", hdr.TransferNo)
		}
		totalValue = total
	}

	if err := s.repo.SetTransferReceived(ctx, tx, hdr.ID, actorID); err != nil {
		return Transfer{}, err
	}
	// Book the blend's rounding at the destination through the same journal
	// shape a PO receipt's WAC rounding uses (postPOReceivedRounding), before
	// the leg journal it adjusts: the rounding and the leg are one fate, the
	// transfer posting convention.
	if totalRounding != 0 {
		if err := s.postTransferReceivedRoundingTx(ctx, tx, transferPost{
			TransferID: hdr.ID, TransferNo: hdr.TransferNo,
			BranchID: hdr.ToBranchID,
		}, totalRounding); err != nil {
			return Transfer{}, err
		}
	}
	if err := s.postTransferLegTx(ctx, tx, transferPost{
		TransferID: hdr.ID, TransferNo: hdr.TransferNo,
		BranchID: hdr.ToBranchID, ValueCents: totalValue,
	}, EventTransferReceived, false); err != nil {
		return Transfer{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Transfer{}, err
	}
	s.log.Info("inventory: transfer received", "transfer_no", hdr.TransferNo, "value_cents", totalValue)
	return s.GetTransfer(ctx, hdr.ID)
}

// CancelTransfer voids a DRAFT transfer. A shipped transfer cannot be cancelled, the stock has
// physically moved and the correction is a transfer back, not an erasure of the one that happened.
func (s *Service) CancelTransfer(ctx context.Context, transferID string) (Transfer, error) {
	tx, err := s.repo.Begin(ctx)
	if err != nil {
		return Transfer{}, err
	}
	defer tx.Rollback(ctx)
	hdr, _, err := s.repo.TransferForUpdate(ctx, tx, transferID)
	if err != nil {
		return Transfer{}, err
	}
	if hdr.Status != TransferDraft {
		return Transfer{}, conflictErr(
			"inventory: transfer %s is %s, a shipped transfer is corrected with a transfer back, not a cancellation",
			hdr.TransferNo, hdr.Status)
	}
	if err := s.repo.SetTransferStatus(ctx, tx, hdr.ID, TransferCancelled); err != nil {
		return Transfer{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Transfer{}, err
	}
	return s.GetTransfer(ctx, hdr.ID)
}

// - reads -

// GetTransfer returns one transfer with its lines.
func (s *Service) GetTransfer(ctx context.Context, transferID string) (Transfer, error) {
	return s.repo.TransferByID(ctx, transferID)
}

// ListTransfers lists transfers touching a branch (either end), newest first. status "" = all.
func (s *Service) ListTransfers(ctx context.Context, branchID, status string, limit int) ([]Transfer, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	return s.repo.ListTransfers(ctx, branchID, status, limit)
}

// - GL posting (the move-posting convention) -
//
// POSTED SYNCHRONOUSLY, AFTER THE STOCK TRANSACTION COMMITS, the invcount / landedcost convention
// (invcount.postSheetGL, landedcost.postCapitalization), not the outbox convention po.received uses.
//
// Why not the outbox: the shared notify.Dispatcher claims events_outbox rows by STATUS ALONE, and by
// the time this bundle's wiring block runs it is already Run()ning with a frozen handler map.
// Emitting a new topic into events_outbox from here would produce rows that a dispatcher with no
// handler for them claims and dead-letters. Registering the handler after Run is a data race. The
// alternative, a whole second dispatcher over a second outbox table for two GL journals, is more
// machinery than the problem deserves when two sibling modules already post this way.
//
// The idempotency key is the event name + the transfer id, so a retry after a GL-side failure posts
// exactly one journal. That matters because the stock side has already committed: the stock truth is
// correct and durable, and the journal converges on retry rather than being lost or doubled.

// The two GL events a transfer raises. Named as constants because they are BOTH the posting-rule
// event_type (matched against the rows migration 0116 inserts) AND the idempotency-key prefix, one
// spelling, so a rename cannot desynchronise the rule lookup from the dedup key.
const (
	EventTransferShipped  = "inventory.transfer_shipped"
	EventTransferReceived = "inventory.transfer_received"
)

// transferPost is the fact set one leg's journal is built from.
type transferPost struct {
	TransferID string
	TransferNo string
	BranchID   string
	ValueCents int64
}

// postTransferLegTx builds and posts the two-line journal for one half of a transfer, on the
// caller's transaction when one is given (round 2, sub item 5: the move and its journal are one
// fate) and on the pool when not. shipping=true debits in-transit and credits inventory at the
// SOURCE; shipping=false does the reverse at the DESTINATION. Both codes resolve through the GL
// posting rules, never a literal.
func (s *Service) postTransferLegTx(ctx context.Context, tx pgx.Tx, p transferPost, event string, shipping bool) error {
	if p.TransferID == "" {
		return fmt.Errorf("inventory: %s missing transfer id", event)
	}
	// A zero-value transfer (every line uncosted, no PO receipt has ever set a weighted average for
	// these products at the source) has nothing to say to the GL. A zero-for-zero journal carries no
	// information and would clutter the ledger for every such move.
	if p.ValueCents == 0 {
		return nil
	}
	if s.gl == nil {
		return errNoGLPoster
	}
	if s.rules == nil {
		return errNoRules
	}
	resolveRule := func(role string) (string, error) {
		return s.resolveTransferRule(ctx, tx, event, role, p.BranchID)
	}
	invCode, err := resolveRule("inventory")
	if err != nil {
		return err
	}
	transitCode, err := resolveRule("in_transit")
	if err != nil {
		return err
	}
	inv := GLLine{AccountCode: invCode, Memo: "branch inventory"}
	transit := GLLine{AccountCode: transitCode, Memo: "stock in transit"}
	if shipping {
		transit.DebitCents, inv.CreditCents = p.ValueCents, p.ValueCents
	} else {
		inv.DebitCents, transit.CreditCents = p.ValueCents, p.ValueCents
	}
	jr := GLJournal{
		// ADJUSTMENT, not a new "STOCK_TRANSFER" source. gl_journals.source carries a CHECK over seven
		// values (0007) that NO migration has ever extended, and every module that posts something
		// outside the core seven, bankrec, invcount, lien, warranty, uses ADJUSTMENT. Widening a GL
		// table's constraint from an inventory migration to add an eighth spelling would put this
		// bundle's DDL on another module's table for a label, and two swarms doing that in the same
		// wave is a union conflict over nothing. The journal is identified precisely regardless: the
		// memo names the transfer number, source_ref_id is the transfer id, and the idempotency key
		// carries the event name.
		Source:         "ADJUSTMENT",
		SourceRefID:    p.TransferID,
		IdempotencyKey: event + ":" + p.TransferID,
		Memo:           "stock transfer " + p.TransferNo,
		BranchID:       p.BranchID,
		Lines:          []GLLine{inv, transit},
	}
	var postErr error
	if tx != nil {
		if gp, ok := s.gl.(GLPosterTx); ok {
			_, postErr = gp.PostJournalTx(ctx, tx, jr)
		} else {
			postErr = s.postTransferLegCommitting(ctx, jr)
		}
	} else {
		postErr = s.postTransferLegCommitting(ctx, jr)
	}
	if postErr != nil {
		s.log.Error("inventory: transfer GL journal did not post",
			"event", event, "transfer_no", p.TransferNo, "transfer_id", p.TransferID, "err", postErr)
		return postErr
	}
	return nil
}

// postTransferLegCommitting is the transfer leg's pool posting path, both arms without a caller
// transaction. Its own function so the branch census finds one committing writer per function
// beside the transaction scoped one above.
func (s *Service) postTransferLegCommitting(ctx context.Context, jr GLJournal) error {
	_, err := s.gl.PostJournal(ctx, jr)
	return err
}

// resolveTransferRule resolves one posting rule for a transfer event on the caller's
// transaction when the resolver supports it (the receiving path holds one), the same way
// postTransferLegTx resolves its legs.
func (s *Service) resolveTransferRule(ctx context.Context, tx pgx.Tx, event, role, branchID string) (string, error) {
	if tx != nil {
		if rr, ok := s.rules.(RuleResolverTx); ok {
			return rr.ResolveRuleTx(ctx, tx, event, role, branchID)
		}
	}
	return s.rules.ResolveRule(ctx, event, role, branchID)
}

// postTransferReceivedRoundingTx posts the WAC integer-cent rounding adjustment journal on a
// transfer receive (W2-5 round 3, K3's P1 3): the destination blend revalued the units already
// on hand, and the difference against the transferred value is booked here through the same
// journal shape a PO receipt's WAC rounding uses (postPOReceivedRounding): DR inventory / CR
// expense when the blend raised the value, the mirror when it lowered it. The expense leg
// resolves through the inventory.adjusted rule, the PO rounding's own rule, never a literal
// except its fallback. The journal rides the receive's own transaction, the transfer posting
// convention (the move and its journals are one fate), and its idempotency key is the receive
// event's own with the rounding suffix, so a retry after a failure posts exactly one journal.
// Its own function so the branch census finds one journal writer per function.
func (s *Service) postTransferReceivedRoundingTx(ctx context.Context, tx pgx.Tx, p transferPost, roundingCents int64) error {
	if p.TransferID == "" {
		return fmt.Errorf("inventory: %s.rounding missing transfer id", EventTransferReceived)
	}
	if s.gl == nil {
		return errNoGLPoster
	}
	if s.rules == nil {
		return errNoRules
	}
	invCode, err := s.resolveTransferRule(ctx, tx, EventTransferReceived, "inventory", p.BranchID)
	if err != nil {
		return err
	}
	expCode, err := s.resolveTransferRule(ctx, tx, "inventory.adjusted", "expense", p.BranchID)
	if err != nil || expCode == "" {
		expCode = "5200"
	}
	amt := roundingCents
	if amt < 0 {
		amt = -amt
	}
	var lines []GLLine
	if roundingCents > 0 {
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
		SourceRefID:    p.TransferID,
		IdempotencyKey: EventTransferReceived + ".rounding:" + p.TransferID,
		Memo:           "WAC integer-cent rounding on transfer receive " + p.TransferNo,
		BranchID:       p.BranchID,
		Lines:          lines,
	}
	if tx != nil {
		if gp, ok := s.gl.(GLPosterTx); ok {
			_, err := gp.PostJournalTx(ctx, tx, jr)
			return err
		}
	}
	return s.postTransferLegCommitting(ctx, jr)
}
