package order

// stock_seam.go, W4 seam 3, consumer side: the stock-reservation port this module declares and
// main.go binds to *inventory.Service.
//
// REPORT-W4-B parked the reservation call sites because internal/order was outside that fence: the
// substrate, the three-tier policy and the HTTP surface all shipped, and NOTHING called ReserveTx.
// This file is the consumer half. It is declared HERE, in the module that needs it, with types this
// module owns, order does not import inventory, and inventory does not know order exists. The
// adapter that maps between the two named-but-identical shapes lives in main.go, which is the one
// place in the tree allowed to know about both.
//
// - WHY THE TRANSACTION IS A PARAMETER -
// ReserveTx takes the CALLER's pgx.Tx. That is inventory's design and it is the correct one: an
// order confirm that reserves stock and then fails to commit its own status row must not leave a
// hold behind. Sharing the transaction is the only way to guarantee that; a compensating release
// afterwards is a promise, not a guarantee, and the compensation itself can fail.
//
// pgx.Tx is therefore in this interface's signature. That is a shared *infrastructure* type (like
// context.Context), not a peer module's domain type, so the zero-trust boundary is intact, the
// adapter converts every DOMAIN value that crosses.
//
// - Applied == false IS NOT AN ERROR -
// A branch may grade a reservation type NONE (allocation switched off). The reserve then writes
// nothing and reports Applied=false. An order must STILL CONFIRM at such a branch. Every call site
// here treats that as success, and TestReserveOnConfirm_PolicyNoneStillConfirms is the guard.

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// Reservation types this module raises. Values match inventory's res_type CHECK set (0115) by
// contract, restated here so the constant this module writes is one this module owns.
const (
	ResTypeOrder = "ORDER"
	ResTypeQuote = "QUOTE"
)

// Terminal reservation statuses this module closes holds with (0115 status CHECK set).
//
// The three-way split is the whole point of routing releases through a status rather than a delete:
//
//	ResConsumed, the stock actually left on this order. The hold ends because the goods shipped.
//	ResReleased, the order will not take the stock after all (cancelled). It goes back to available.
//	ResExpired , the sweep timed a soft hold out. Never written by this module; it is the jobs path.
//
// A DELETE would make all three indistinguishable a week later, and "why did 40 units come back
// into available on Tuesday" is a question a yard manager genuinely asks.
const (
	ResReleased = "RELEASED"
	ResConsumed = "CONSUMED"
)

// Source types this module stamps on the holds it raises, so ReleaseBySourceTx can close them all
// with one call that cannot miss a line.
const (
	ResSourceOrder = "order"
	ResSourceQuote = "quote"
)

// StockReserveInput is the reserve request. Field-for-field the shape inventory.ReserveInput takes,
// declared here so this module's call sites compile against a type this module owns.
type StockReserveInput struct {
	ProductID string
	BranchID  string
	// LocationID defaults to BranchID in the provider, the branch-grain projection every stock
	// writer in the tree already uses. Left settable so a future location-grain caller has a seam.
	LocationID  string
	ResType     string
	QtyBase     float64
	SourceType  string
	SourceRefID string
	Reason      string
	CreatedBy   string
	// EnforceAvailable makes the reserve FAIL rather than push available negative. Order confirm
	// leaves it FALSE on purpose: a backorder is a real thing, and refusing to reserve would LOSE
	// the commitment rather than record it, the order would confirm with no hold at all and the
	// replenishment worklist would never see the gap. The negative available IS the signal.
	EnforceAvailable bool
}

// StockReserveResult is what a reserve returns. Applied=false means branch policy graded this type
// NONE and nothing was written, success, not failure (see the header).
type StockReserveResult struct {
	ReservationID string
	HoldMode      string
	Applied       bool
}

// StockReserver is the consumer-declared reservation port. *inventory.Service satisfies the shape
// through the main.go adapter; nothing in this package imports inventory.
type StockReserver interface {
	ReserveTx(ctx context.Context, tx pgx.Tx, in StockReserveInput) (StockReserveResult, error)
	ReleaseBySourceTx(ctx context.Context, tx pgx.Tx, sourceType, sourceRefID, status string) (int, error)
	// LockHardHoldBalancesTx takes the balance rows of one source document's ACTIVE HARD holds
	// FOR UPDATE, on the caller's transaction, ahead of a later write in that transaction that
	// draws a money counter: the cancel's release unwinds these rows after the deposit settlement
	// draws the invoices feed counter, and the POS ring takes a stock row before that same
	// counter, so the cancel must take its stock rows first or the two writers can meet head on
	// (round 2, the Opus read's P2-1). REQUIRED, not optional, exactly as the other two are: a
	// composition whose reserver cannot lock would silently bring the head on meeting back.
	LockHardHoldBalancesTx(ctx context.Context, tx pgx.Tx, sourceType, sourceRefID string) error
}

// WithStockReserver injects the reservation port (main.go wires the *inventory.Service adapter).
func (s *Service) WithStockReserver(r StockReserver) *Service {
	s.stock = r
	if f, ok := r.(StockFulfiller); ok && s.stockFulfiller == nil {
		s.stockFulfiller = f
	}
	return s
}

// StockFulfiller relieves on-hand stock and emits order.fulfilled when an order is fulfilled.
// *inventory.Service satisfies this interface directly.
//
// IT IS NARROW ON PURPOSE, AND THAT IS A ROUND 2 FIX. The ship group's relief and the "the goods
// have left" read arrived as two NEW capabilities, and widening this port to carry them unwired
// every adapter that does not implement them: WithStockReserver's type assertion fails, the order
// keeps a fulfiller that cannot relieve anything, and the delivery relief stops happening with no
// error anywhere (the PR's own reservation leg, on hand 10 want 6, is what caught it). The two new
// capabilities are OPTIONAL interfaces of their own below, asserted where they are used, so an
// adapter that has them uses them and an adapter that does not still relieves the order.
type StockFulfiller interface {
	FulfilOrderLinesTx(ctx context.Context, tx pgx.Tx, orderID, branchID string, productIDs []string, qtys []float64) error
	ReleaseBySourceTx(ctx context.Context, tx pgx.Tx, sourceType, sourceRefID, status string) (int, error)
}

// GroupStockFulfiller is the OPTIONAL ship group half of the relief port (round 2, S3's P1): a
// group's completion relieves the GROUP's own lines and quantities, behind the GROUP's own
// idempotency, never the order's. A fulfiller without this capability relieves the order as a
// whole, the round 1 shape.
type GroupStockFulfiller interface {
	FulfilGroupLinesTx(ctx context.Context, tx pgx.Tx, orderID, groupID, branchID string, productIDs []string, qtys []float64) error
	HasShipMovesForGroupTx(ctx context.Context, tx pgx.Tx, orderID, groupID string) (bool, error)
}

// ShipMoveReaderTx is the OPTIONAL "the goods have left" read (round 2, S3's P1), the INVOICED
// holds rule's own question.
//
// IT IS TX SCOPED ON PURPOSE. A move the caller's own transaction wrote is uncommitted, and a pool
// read cannot see it: the order would be told its goods are still in the yard at the moment they
// are not (S3's P3's rule about base counts moving without a move behind them, one level up).
type ShipMoveReaderTx interface {
	HasShipMovesForOrderTx(ctx context.Context, tx pgx.Tx, orderID string) (bool, error)
}

// WithStockFulfiller injects the order fulfilment stock-relief port (*inventory.Service).
func (s *Service) WithStockFulfiller(f StockFulfiller) *Service { s.stockFulfiller = f; return s }

// StockSeamWired reports whether the reservation port is bound. main.go asserts it at boot and the
// surface renders the reservation panel off it, so an unwired deploy says so out loud rather than
// silently confirming orders that hold nothing.
func (s *Service) StockSeamWired() bool { return s.stock != nil }

// reserveOrderLinesTx raises one HARD hold per stocked line of an order, inside the caller's
// transaction. It is the ONE place order confirm reserves, so the ResType/SourceType pair cannot
// drift between call sites.
//
// LINES WITHOUT A PRODUCT ARE SKIPPED, not refused. order_lines.product_id is nullable (0008) and a
// free-text line, "delivery surcharge", "cut to size", has no stock to hold. Refusing the whole
// confirm because one line is a service item would be a regression on every order that has one.
//
// A ZERO-OR-NEGATIVE qty_base line is skipped for the same reason: inventory rejects qty <= 0, and
// a confirm must not die on a line the order module itself considers legal.
func (s *Service) reserveOrderLinesTx(ctx context.Context, tx pgx.Tx, orderID, branchID, actor string, lines []reservableLine) (int, error) {
	if s.stock == nil {
		return 0, ErrStockSeamUnwired
	}
	applied := 0
	for _, ln := range lines {
		if ln.ProductID == "" || ln.QtyBase <= 0 {
			continue
		}
		res, err := s.stock.ReserveTx(ctx, tx, StockReserveInput{
			ProductID:   ln.ProductID,
			BranchID:    branchID,
			ResType:     ResTypeOrder,
			QtyBase:     ln.QtyBase,
			SourceType:  ResSourceOrder,
			SourceRefID: orderID,
			Reason:      "order confirmed",
			CreatedBy:   actor,
			// See StockReserveInput.EnforceAvailable: confirm records the commitment even when it
			// overdraws, because a backorder is a fact and losing it is worse than recording it.
			EnforceAvailable: false,
		})
		if err != nil {
			return applied, err
		}
		if res.Applied {
			applied++
		}
	}
	return applied, nil
}

// reservableLine is the narrow projection reserveOrderLinesTx needs off an order line.
type reservableLine struct {
	LineNo    int
	ProductID string
	QtyBase   float64
}
