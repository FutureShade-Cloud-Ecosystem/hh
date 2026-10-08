package order

// pick_hold.go, item 9.3 (the row 1047 deposits work): AN ORDER THAT OWES
// MONEY UNDER A PAY BEFORE RELEASE SHAPE CANNOT REACH PICKING.
//
// WHICH ORDERS. Read from the money, never from a stamped flag that can
// drift: the customer's terms are zero day (a cash on demand account, the
// walk in) and the order still owes, where "owes" counts both the unpaid
// balances of its invoices and any part of its total nothing has invoiced yet
// (a converted cash on demand order owes its whole total before its first
// invoice exists, that is the hold this item exists to add). A credit
// customer's on account order is NEVER held by this rule: it owes on terms,
// which is what terms are. Dispatch keeps its own gate (billing CanDispatch);
// this one guards picking and every other path that releases goods.
//
// THE HOLD LIFTS ITSELF. There is no stamp to lift: the facts are computed
// from the invoice balances on every read, so the last dollar lifts the hold
// in the same transaction the payment landed in, whichever route took it (the
// till, the AR register, the desk's order payment), with no person's hand and
// nothing that can disagree with the balance.
//
// THE POSTURE OF AN UNWIRED SEAM. The lane is additive: a service whose pick
// hold reader is nil does not enforce it (the W5 reservation lane's posture),
// because the alternative holds every order of every scaffold composition.
// The composition root (wire_tops.go) wires the real reader and REFUSES TO
// BOOT without it, exactly as the cancel guards do, so a deploy cannot serve
// with the lane dark. A reader that is wired but cannot answer FAILS CLOSED
// (ErrPickHoldUnverifiable): a picking move that could not be checked does
// not happen.

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// PickHoldWords is the refusal's sentence, the item's mandated words, the same
// at every path and on the order read.
const PickHoldWords = "This order cannot reach picking until the balance is paid."

// PickInProcessWords is the order read's chip when a payment has landed and
// money remains: a deposit puts the order in process, and the hold stays.
const PickInProcessWords = "In process: deposit taken"

// ErrHeldFromPicking refuses a goods releasing move on an order that owes
// money under a pay before release shape. Mapped to 409 with the stable word
// held_from_picking. Exported for errors.Is.
var ErrHeldFromPicking = errors.New("order: " + PickHoldWords)

// ErrPickHoldUnverifiable answers a wired pick hold reader that could not be
// read: the move is refused rather than allowed (fail closed), and the 503
// names the fault rather than posing as a business refusal. Unreachable in a
// correct deploy. Exported for errors.Is.
var ErrPickHoldUnverifiable = errors.New("order: the pay before release hold could not be read, the move is refused")

// PickHoldFacts is the hold's own reading of an order, what the order reads
// the till and the yard carry: held or not, the words, the order total, paid
// so far, still owed.
type PickHoldFacts struct {
	// Held is true exactly when the order owes money under a pay before
	// release shape (zero day terms and money outstanding).
	Held bool
	// Words is the refusal sentence, present when Held.
	Words string
	// InProcessWords is the deposit chip, present when a payment has landed
	// and money remains (a deposit puts the order in process, the hold stays).
	InProcessWords string
	// OrderTotalCents, PaidCents, OwedCents are the money, always answered.
	OrderTotalCents int64
	PaidCents       int64
	OwedCents       int64
}

// PickHoldReader is the billing read the hold runs on, consumer declared (the
// order module never reads the invoice tables itself). Satisfied in the
// composition root by an adapter over the billing repository.
type PickHoldReader interface {
	PickHoldFacts(ctx context.Context, orderID string) (PickHoldFacts, error)
}

// PickHoldTxReader is the transaction-joining variant of PickHoldReader (the
// cancel guards' FinalizedInvoiceTxReader shape, item 9 Opus P3-2): the
// guarded status writers run the hold read inside their own transaction under
// the order row lock, and a reader that asked the shared pool there would hold
// two connections per caller (the starvation the tree's store seams rule out).
// A reader that implements this reads through the caller's tx instead; a
// reader with only the pool half keeps working and keeps the pool read.
type PickHoldTxReader interface {
	PickHoldFactsTx(ctx context.Context, tx pgx.Tx, orderID string) (PickHoldFacts, error)
}

// WithPickHoldReader wires (or overrides) the pick hold read.
func (s *Service) WithPickHoldReader(r PickHoldReader) *Service { s.pickHold = r; return s }

// PickHoldWired reports whether the lane is enforced (the composition root's
// boot assertion).
func (s *Service) PickHoldWired() bool { return s.pickHold != nil }

// refuseHeldFromPicking is the guard every goods releasing move consults:
// nil seam allows (the additive lane), a read that fails refuses with
// ErrPickHoldUnverifiable, a held order refuses with ErrHeldFromPicking.
// Inside a guarded write the read joins the write's transaction when the
// reader can (PickHoldTxReader, the context's precheck tx), so the read and
// the move are one act under the order row lock.
func (s *Service) refuseHeldFromPicking(ctx context.Context, orderID string) error {
	if s.pickHold == nil {
		return nil
	}
	var f PickHoldFacts
	var err error
	if r, ok := s.pickHold.(PickHoldTxReader); ok {
		if tx := precheckTx(ctx); tx != nil {
			f, err = r.PickHoldFactsTx(ctx, tx, orderID)
		} else {
			f, err = s.pickHold.PickHoldFacts(ctx, orderID)
		}
	} else {
		f, err = s.pickHold.PickHoldFacts(ctx, orderID)
	}
	if err != nil {
		return ErrPickHoldUnverifiable
	}
	if f.Held {
		return ErrHeldFromPicking
	}
	return nil
}

// OrderPayHold is the order read's pay hold block (OrderView.PayHold).
type OrderPayHold struct {
	HeldFromPicking bool   `json:"held_from_picking"`
	Words           string `json:"words,omitempty"`
	InProcessWords  string `json:"in_process_words,omitempty"`
	OrderTotalCents int64  `json:"order_total_cents"`
	PaidCents       int64  `json:"paid_cents"`
	OwedCents       int64  `json:"owed_cents"`
}

// payHoldView reads the block for the order read. Best effort like the edit
// lines: an unwired seam omits the block entirely (the read keeps answering
// status and actions), a wired seam that cannot answer also omits it, never a
// block that reads as clear.
func (s *Service) payHoldView(ctx context.Context, orderID string) *OrderPayHold {
	if s.pickHold == nil {
		return nil
	}
	f, err := s.pickHold.PickHoldFacts(ctx, orderID)
	if err != nil {
		return nil
	}
	return &OrderPayHold{
		HeldFromPicking: f.Held, Words: f.Words, InProcessWords: f.InProcessWords,
		OrderTotalCents: f.OrderTotalCents, PaidCents: f.PaidCents, OwedCents: f.OwedCents,
	}
}
