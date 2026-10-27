package order

// reservations.go, W4 seam 3 LANDING: the reservation lifecycle bound to the order status ladder.
//
//	CONFIRMED   → hard-reserve every stocked line   (inventory.ReserveTx, ResTypeOrder)
//	FULFILLED   → close the holds as CONSUMED       (the stock left on this order)
//	INVOICED    → close the holds as CONSUMED       (backstop, see below)
//	CLOSED      → close the holds as CONSUMED       (backstop)
//	CANCELLED   → close the holds as RELEASED       (the stock comes back to available)
//
// EVERY ONE OF THOSE HAPPENS INSIDE THE STATUS TRANSACTION. That is not a nicety. A confirm that
// wrote `orders.status='CONFIRMED'` and then reserved in a second transaction has a window in which
// the order is confirmed and holds nothing; a cancel with the same shape has a window in which the
// order is dead and the stock is still held. Both windows are exactly the oversell DIB-60 exists to
// close, so the reserve and the status write share one transaction and one fate.
//
// - WHAT IS *NOT* ON THIS LIST, AND WHY -
//
// PARTIALLY_FULFILLED does NOT release. Half the order shipped; the other half is still promised to
// this customer and must stay held. Releasing on partial would hand the remainder to the next
// walk-in the moment the first truck left, which is the failure mode with the worst customer story
// in the whole feature ("you delivered half my patio and sold the rest").
//
// PICKING / READY do nothing. The hold was taken at confirm and the goods have not moved; there is
// no state change to record. Adding a no-op release here would only create a chance to get it wrong.
//
// INVOICED and CLOSED consume as a BACKSTOP. The canonical path is FULFILLED→INVOICED, and by the
// time INVOICED arrives the holds are already closed, ReleaseBySourceTx only touches ACTIVE rows,
// so the second call closes nothing and costs one indexed UPDATE. But the POS ring advances an order
// to INVOICED directly, and a manually-confirmed order that is invoiced without a FULFILLED event
// would otherwise hold its stock forever. A hold that outlives its order is a phantom shortage
// nobody can explain and nobody can clear from a screen; one redundant UPDATE is a cheap price.
//
// - FAIL-OPEN ON AN UNWIRED SEAM, DELIBERATELY, AND LOUDLY -
// If StockReserver is not bound the whole feature is OFF: no confirm reserves, so no cancel has
// anything to release, so nothing leaks. Refusing to confirm orders because an inventory seam is
// unbound would be a total order-entry outage in exchange for a feature that was not running
// anyway. This is the same asymmetry gl's approver config states: fail CLOSED when the failure is
// silently moving money (the cancel money predicate does), fail OPEN when the failure is a wiring
// gap in an additive control. main.go asserts the binding at boot and StockSeamWired() reports it to
// the surface, so an unwired deploy is visible rather than quiet.

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// ErrStockSeamUnwired is returned by the direct reservation entry points when the StockReserver port
// is not bound. The status ladder does NOT return it (see the header, that path fails open); it is
// for the explicit "re-run this order's reservations" action, where doing nothing silently would be
// a lie to whoever pressed the button.
var ErrStockSeamUnwired = errStockSeamUnwired{}

type errStockSeamUnwired struct{}

func (errStockSeamUnwired) Error() string {
	return "order: stock reservation seam not wired, reservations are not being maintained"
}

// reservationEffect is what a status target does to this order's holds.
type reservationEffect string

const (
	effectNone     reservationEffect = ""
	effectReserve  reservationEffect = "RESERVE"
	effectConsume  reservationEffect = "CONSUME"
	effectRelease  reservationEffect = "RELEASE"
	effectNoChange reservationEffect = "NOOP" // an explicit "considered and deliberately nothing"
	// effectConsumeIfShipped consumes the holds ONLY when the order's goods have left, and then
	// relieves the stock too (round 2, S3's P1). The question is asked INSIDE the status
	// transaction, so the answer is that transaction's own.
	effectConsumeIfShipped reservationEffect = "CONSUME_IF_SHIPPED"
)

// reservationEffectFor maps a target status to its hold effect. It is a total function over the
// 0008 status set on purpose: a new status added to that CHECK without a decision here shows up as
// effectNone rather than silently inheriting somebody else's behaviour, and the unit test that
// walks every status is what makes that visible.
func reservationEffectFor(target string) reservationEffect {
	switch target {
	case "CONFIRMED":
		return effectReserve
	case "FULFILLED", "CLOSED":
		return effectConsume
	case "CANCELLED":
		return effectRelease
	case "PARTIALLY_FULFILLED", "PICKING", "READY", "DRAFT":
		return effectNoChange
	case "INVOICED":
		// Round 2 (S3's P1): the invoice is not the goods leaving. The holds close at
		// INVOICED only when the goods have left, and stay when the yard still owes the
		// customer the goods; the question is asked inside the status transaction.
		return effectConsumeIfShipped
	default:
		return effectNone
	}
}

// terminalStatusFor maps a consume/release effect to the reservation status it closes holds with.
func terminalStatusFor(e reservationEffect) string {
	switch e {
	case effectConsume:
		return ResConsumed
	case effectRelease:
		return ResReleased
	default:
		return ""
	}
}

// reservationStore is the OPTIONAL transactional half of the status writer, satisfied by the real
// *Repository. It is a type assertion rather than a member of orderStore for the same reason
// txRecorder is: the in-memory fakes that back the unit tests do not (and should not) grow a pgx.Tx
// just so the reservation lane can exist. A fake store simply has no reservation lane, and the
// lifecycle is proved by the integration tests against the real database, which is where a
// tx-scoped invariant can be proved at all.
type reservationStore interface {
	// UpdateStatusFromTx is UpdateStatusFromChecked with an additional hook that runs INSIDE the
	// transaction, after the status UPDATE, carrying the tx so a reservation write shares its fate.
	// It also returns the order's branch, read under the same lock, because a reserve needs it and a
	// second unlocked read could see a different branch than the one the transition just committed.
	UpdateStatusFromTx(ctx context.Context, orderID, newStatus string, allowedFrom []string,
		precheck func(context.Context) error,
		onTx func(ctx context.Context, tx pgx.Tx, oldStatus, branchID string) error) (oldStatus string, err error)
	// ReservableLinesTx reads the (product, qty_base) projection of an order's lines on the caller's
	// transaction, so a confirm reserves exactly the lines it is confirming.
	ReservableLinesTx(ctx context.Context, tx pgx.Tx, orderID string) ([]reservableLine, error)
	// ClaimUnfulfilledLinesTx locks the order's lines, advances fulfilled_base by each line's remaining
	// unfulfilled quantity, and returns the stocked (product_id, qty_base) pairs for this order.
	ReadGroupClaimTx(ctx context.Context, tx pgx.Tx, orderID, groupID string) ([]string, []string, []float64, error)
	AdvanceGroupClaimTx(ctx context.Context, tx pgx.Tx, lineIDs []string, qtys []float64) error
	ClaimUnfulfilledLinesTx(ctx context.Context, tx pgx.Tx, orderID string) ([]string, []float64, error)
	// OrderFulfilmentDebtTx answers whether the order still owes stock to a customer: a DELIVERY
	// or WILL_CALL order (the two fulfilment modes that defer the stock move to the yard) that
	// still has open quantity on its stocked lines. It is the order module's OWN tables, read on
	// the caller's transaction, so the INVOICED gate's answer is the transaction's own.
	OrderFulfilmentDebtTx(ctx context.Context, tx pgx.Tx, orderID string) (deferred bool, openQty float64, err error)
	// AdvanceFulfilledByProductTx advances the order's lines' fulfilled_base by the quantities the
	// order's own event carried, matched by product (round 2: the POS ring's base advance, owned by
	// the order module's own consumer).
	AdvanceFulfilledByProductTx(ctx context.Context, tx pgx.Tx, orderID string, qtys map[string]float64) (int, error)
	// UpdateStatusFromTxOn is UpdateStatusFromTx on the CALLER's transaction, with no commit of its
	// own (round 2, S3's P2): the pickup's confirm runs the order's fulfilment inside the
	// confirm's own transaction.
	UpdateStatusFromTxOn(ctx context.Context, tx pgx.Tx, orderID, newStatus string, allowedFrom []string,
		precheck func(context.Context) error,
		onTx func(ctx context.Context, tx pgx.Tx, oldStatus, branchID string) error) (oldStatus string, err error)
	// LockOrderOnTx is LockOrderTx on the CALLER's transaction, with no commit of its own.
	LockOrderOnTx(ctx context.Context, tx pgx.Tx, orderID string,
		fn func(ctx context.Context, tx pgx.Tx, status, branchID string) error) error
	// LockOrderTx runs fn inside a transaction holding the order row's write lock, WITHOUT changing
	// the status. It is the seam for the two writes that touch holds but are not transitions: the
	// operator resync and the change-order apply. Separate from UpdateStatusFromTx rather than
	// UpdateStatusFromTx(newStatus="") because "lock and do something" and "transition" are genuinely
	// different operations, and collapsing them behind a sentinel value is how a status write ends up
	// happening by accident.
	LockOrderTx(ctx context.Context, orderID string, fn func(ctx context.Context, tx pgx.Tx, status, branchID string) error) error
	// OrderHolds reads the ACTIVE holds this order currently owns, for the surface.
	OrderHolds(ctx context.Context, orderID string) ([]OrderHold, error)
}

// OrderHold is one ACTIVE stock hold raised by this order, the reservation panel's row. It is read
// through this module's own projection rather than inventory's Reservation type: the surface needs
// the product name, which inventory's shape does not carry, and a JOIN in the consumer is the wrong
// shape for a seam.
type OrderHold struct {
	ReservationID string  `json:"reservation_id"`
	ProductID     string  `json:"product_id"`
	ProductName   string  `json:"product_name"`
	SKU           string  `json:"sku"`
	BranchID      string  `json:"branch_id"`
	HoldMode      string  `json:"hold_mode"`
	QtyBase       float64 `json:"qty_base"`
	Status        string  `json:"status"`
	CreatedAt     string  `json:"created_at"`
}

// reservationLane returns the transactional store when BOTH it and the stock seam are available.
// A nil return means the reservation lane is off and the caller uses the plain status path.
func (s *Service) reservationLane() (reservationStore, bool) {
	if s.stock == nil {
		return nil, false
	}
	rs, ok := s.store.(reservationStore)
	return rs, ok
}

// applyReservationsTx is the hook handed to UpdateStatusFromTx. It runs inside the status
// transaction, after the UPDATE, and its error rolls the transition back.
func (s *Service) applyReservationsTx(effect reservationEffect, orderID, actor string) func(context.Context, pgx.Tx, string, string) error {
	return func(ctx context.Context, tx pgx.Tx, _ /*oldStatus*/, branchID string) error {
		switch effect {
		case effectReserve:
			lines, err := s.reservationLaneLines(ctx, tx, orderID)
			if err != nil {
				return err
			}
			_, err = s.reserveOrderLinesTx(ctx, tx, orderID, branchID, actor, lines)
			return err
		case effectConsume, effectRelease:
			if _, err := s.stock.ReleaseBySourceTx(ctx, tx, ResSourceOrder, orderID, terminalStatusFor(effect)); err != nil {
				return err
			}
			if effect == effectConsume && s.stockFulfiller != nil {
				return s.fulfilOrderLinesInTx(ctx, tx, orderID, branchID)
			}
			return nil
		case effectConsumeIfShipped:
			// Round 2 (S3's P1): the goods have left, or they have not. The question is asked
			// HERE, inside the status transaction, so the answer is this transaction's own: a
			// move it wrote a moment ago counts, and a move nobody wrote does not.
			left, err := s.goodsHaveLeftTx(ctx, tx, orderID)
			if err != nil {
				return err
			}
			if !left {
				return nil // the yard still owes the goods: the holds and the stock stay
			}
			// The goods left, so the holds close as CONSUMED and the stock that left is relieved.
			return s.fulfilOrderLinesInTx(ctx, tx, orderID, branchID)
		default:
			return nil
		}
	}
}

// fulfilOrderLinesInTx claims unfulfilled order lines, releases any active order
// reservations as CONSUMED, relieves stock, and emits order.fulfilled inside tx.
func (s *Service) fulfilOrderLinesInTx(ctx context.Context, tx pgx.Tx, orderID, branchID string) error {
	if s.stock != nil {
		if _, err := s.stock.ReleaseBySourceTx(ctx, tx, ResSourceOrder, orderID, ResConsumed); err != nil {
			return err
		}
	} else if s.stockFulfiller != nil {
		if _, err := s.stockFulfiller.ReleaseBySourceTx(ctx, tx, ResSourceOrder, orderID, ResConsumed); err != nil {
			return err
		}
	}
	if s.stockFulfiller == nil {
		return nil
	}
	rs, ok := s.store.(reservationStore)
	if !ok {
		return nil
	}
	// Round 2 (S3's P3): the base claims only when the relief will really move
	// stock; an order whose SHIP moves exist (the POS ring relieved it) stops
	// here, its lines' counts never moving without a move behind them.
	//
	// THE READ IS ON THIS TRANSACTION (round 2, S3's P1). A pool read cannot see
	// the move this very transaction wrote, so the order would be relieved twice:
	// once here and once by the move it had just made.
	if f, ok := s.stockFulfiller.(ShipMoveReaderTx); ok {
		if shipped, err := f.HasShipMovesForOrderTx(ctx, tx, orderID); err == nil && shipped {
			return nil
		}
	}
	productIDs, qtys, err := rs.ClaimUnfulfilledLinesTx(ctx, tx, orderID)
	if err != nil {
		return err
	}
	return s.stockFulfiller.FulfilOrderLinesTx(ctx, tx, orderID, branchID, productIDs, qtys)
}

// goodsHaveLeftTx answers, ON THE CALLER'S TRANSACTION, whether the order's goods have left the
// branch. It is the INVOICED holds rule's own question (round 2, S3's P1).
//
// THE ANSWER IS THE ORDER'S OWN FULFILMENT STATE, not a re-read of the stock. An order that still
// owes stock to a customer is PENDING FULFILMENT, the shape inventory's pending-fulfilment read
// lists: a DELIVERY or WILL_CALL order (the two modes that defer the stock move to the yard) whose
// lines are not all fulfilled and whose stock never moved. Such an order keeps its holds and keeps
// its stock: the goods are still promised to that customer.
//
// Every other order has let the goods go. A PICKUP order hands them over at the counter, so the
// counter's invoice means the customer has them. A DELIVERY order whose lines are all fulfilled put
// them on the truck. And an order with SHIP moves moved the stock whatever its lines say.
//
// THE SHIP MOVE READ IS TX SCOPED, so a move this transaction wrote counts (see the P3 note above).
// An unwired stock seam cannot see any move, so it falls back to the order's own state.
func (s *Service) goodsHaveLeftTx(ctx context.Context, tx pgx.Tx, orderID string) (bool, error) {
	if f, ok := s.stockFulfiller.(ShipMoveReaderTx); ok {
		moved, err := f.HasShipMovesForOrderTx(ctx, tx, orderID)
		if err != nil {
			return false, err
		}
		if moved {
			return true, nil
		}
	}
	rs, ok := s.store.(reservationStore)
	if !ok {
		return false, nil
	}
	deferred, open, err := rs.OrderFulfilmentDebtTx(ctx, tx, orderID)
	if err != nil {
		return false, err
	}
	return !(deferred && open > 0), nil
}

// fulfilOrderOnInvoiced runs the INVOICED gate under the order row lock: when the order's goods
// have left, its holds close as CONSUMED and its stock is relieved; when they have not, the order
// keeps both, because the yard still owes the customer the goods.
//
// It is the door the UNGUARDED choke point takes (SetStatus), which writes the status outside the
// reservation lane. The event driven road (AdvanceFromEvent) asks the same question inside its own
// status transaction, through the reservation lane's effectConsumeIfShipped.
func (s *Service) fulfilOrderOnInvoiced(ctx context.Context, orderID string) error {
	if s.stockFulfiller == nil && s.stock == nil {
		return nil
	}
	rs, ok := s.store.(reservationStore)
	if !ok {
		return nil
	}
	return rs.LockOrderTx(ctx, orderID, func(ctx context.Context, tx pgx.Tx, _, branchID string) error {
		left, err := s.goodsHaveLeftTx(ctx, tx, orderID)
		if err != nil || !left {
			return err
		}
		return s.fulfilOrderLinesInTx(ctx, tx, orderID, branchID)
	})
}

// fulfilGroupOnDelivery relieves a SHIP GROUP's own lines under the order row
// lock (round 2, S3's P1): the group's holds are untouched (they consume at
// INVOICED, FULFILLED and CLOSED), its stock and COGS are the group's own, and
// its idempotency is the group's, never the order's.
func (s *Service) fulfilGroupOnDelivery(ctx context.Context, orderID, groupID string) error {
	if s.stockFulfiller == nil {
		return nil
	}
	// The group's relief is an OPTIONAL capability of the fulfiller (see stock_seam.go). A
	// fulfiller without it relieves the order as a whole, the round 1 shape.
	gf, ok := s.stockFulfiller.(GroupStockFulfiller)
	if !ok {
		return nil
	}
	rs, ok := s.store.(reservationStore)
	if !ok {
		return nil
	}
	return rs.LockOrderTx(ctx, orderID, func(ctx context.Context, tx pgx.Tx, _, branchID string) error {
		shipped, err := gf.HasShipMovesForGroupTx(ctx, tx, orderID, groupID)
		if err != nil || shipped {
			return err
		}
		lineIDs, productIDs, qtys, cerr := rs.ReadGroupClaimTx(ctx, tx, orderID, groupID)
		if cerr != nil || len(productIDs) == 0 {
			return cerr
		}
		if err := gf.FulfilGroupLinesTx(ctx, tx, orderID, groupID, branchID, productIDs, qtys); err != nil {
			return err
		}
		// The base advances only behind the group's real move (the shipped
		// check above gated this write; S3's P3 kept).
		return rs.AdvanceGroupClaimTx(ctx, tx, lineIDs, qtys)
	})
}

// fulfilOrderOnDelivery runs fulfilOrderLinesInTx under the order row lock when
// stockFulfiller (or stock reserver) is wired.
func (s *Service) fulfilOrderOnDelivery(ctx context.Context, orderID string) error {
	if s.stockFulfiller == nil && s.stock == nil {
		return nil
	}
	rs, ok := s.store.(reservationStore)
	if !ok {
		return nil
	}
	return rs.LockOrderTx(ctx, orderID, func(ctx context.Context, tx pgx.Tx, _, branchID string) error {
		return s.fulfilOrderLinesInTx(ctx, tx, orderID, branchID)
	})
}

// reservationLaneLines reads the reservable projection on the transaction.
func (s *Service) reservationLaneLines(ctx context.Context, tx pgx.Tx, orderID string) ([]reservableLine, error) {
	rs, ok := s.store.(reservationStore)
	if !ok {
		return nil, nil
	}
	return rs.ReservableLinesTx(ctx, tx, orderID)
}

// AdvanceFulfilledFromEvent advances the order's lines' fulfilled_base by the quantities the
// order's own fulfilment event carried (round 2: the POS ring's base advance, owned by the order
// module's own consumer so that no other module writes order_lines).
//
// IT RUNS UNDER THE ORDER ROW LOCK, beside the order's other hold-affecting writes, so it cannot
// race a transition that is claiming the same lines. The advance itself is capped by each line's
// own room (qty_base - fulfilled_base - released_base) and is idempotent: a redelivered event
// finds no room and advances nothing.
func (s *Service) AdvanceFulfilledFromEvent(ctx context.Context, orderID string, qtys map[string]float64) error {
	rs, ok := s.store.(reservationStore)
	if !ok {
		return nil
	}
	return rs.LockOrderTx(ctx, orderID, func(ctx context.Context, tx pgx.Tx, _, _ string) error {
		_, err := rs.AdvanceFulfilledByProductTx(ctx, tx, orderID, qtys)
		return err
	})
}

// OrderReservations returns the ACTIVE holds this order owns. Empty (not an error) when the seam is
// unwired, the panel then renders its own "reservations are not being maintained" state off
// StockSeamWired() rather than an error toast, because an unwired seam is an operator fact, not a
// failed request.
func (s *Service) OrderReservations(ctx context.Context, orderID string) ([]OrderHold, error) {
	rs, ok := s.store.(reservationStore)
	if !ok {
		return nil, nil
	}
	return rs.OrderHolds(ctx, orderID)
}

// ResyncReservations re-runs an order's HARD holds from its CURRENT lines, in one transaction:
// every existing order-sourced hold is RELEASED and the current lines are re-reserved.
//
// RELEASE-THEN-RESERVE, not a per-line diff. A diff has to decide what to do about a hold whose
// product no longer appears on the order, a line that split, a quantity that moved twice, and each
// of those decisions is a place to leave a stale hold behind. Release-then-reserve has exactly one
// possible outcome: after it commits, this order's holds ARE its lines. Both halves are in one
// transaction, so no reader ever sees the order holding nothing.
//
// The window this closes is real: it is what a change-order apply needs (the delta changed the
// lines, so the holds must follow), and it is the operator's repair button when a hold and a line
// have drifted for any other reason.
func (s *Service) ResyncReservations(ctx context.Context, orderID, branchID, actor string) (int, error) {
	if s.stock == nil {
		return 0, ErrStockSeamUnwired
	}
	rs, ok := s.reservationLane()
	if !ok {
		return 0, ErrStockSeamUnwired
	}
	var applied int
	err := rs.LockOrderTx(ctx, orderID, func(ctx context.Context, tx pgx.Tx, _, lockedBranch string) error {
		var err error
		applied, err = s.resyncReservationsTx(ctx, tx, orderID, pick(branchID, lockedBranch), actor)
		return err
	})
	if err != nil {
		return 0, err
	}
	return applied, nil
}

// resyncReservationsTx is the in-transaction half of ResyncReservations, reused verbatim by the
// change-order apply so "apply a delta" and "repair the holds" can never diverge.
func (s *Service) resyncReservationsTx(ctx context.Context, tx pgx.Tx, orderID, branchID, actor string) (int, error) {
	if _, err := s.stock.ReleaseBySourceTx(ctx, tx, ResSourceOrder, orderID, ResReleased); err != nil {
		return 0, err
	}
	lines, err := s.reservationLaneLines(ctx, tx, orderID)
	if err != nil {
		return 0, err
	}
	return s.reserveOrderLinesTx(ctx, tx, orderID, branchID, actor, lines)
}

// pick returns the first non-empty string.
func pick(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
