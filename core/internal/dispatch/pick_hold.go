package dispatch

// pick_hold.go, item 9.3 (the row 1047 deposits work): the pay before release
// hold on the will call paths that release goods.
//
// Minting a rack tag and the customer's self confirmation are the two doors
// goods leave through on a will call order, so both consult the same facts
// the order module's picking hold reads (an order that owes money under a pay
// before release shape). The tag door refusing is normally enough (a held
// order cannot reach picking, so nothing can be staged for its tag), but a
// token minted before a reopened balance (an NSF bounce) must not stay live
// for the confirm, so the confirm refuses too.
//
// NIL ALLOWS, an error REFUSES. The lane is additive (a scaffold composition
// without the seam behaves as before; the composition root asserts the wiring
// at boot), but a wired seam that cannot answer is the money direction and
// fails closed, unlike the AR dispatch gate beside it, whose error-allow
// posture model_w3.go defends for an advisory check. This one is not
// advisory: the item's own words are a hard stop.

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// pickHoldWords is the item's mandated sentence, the same words the order
// module's refusal carries.
const pickHoldWords = "This order cannot reach picking until the balance is paid."

// PickHoldGate answers whether an order is held from picking under a pay
// before release shape, with the money still owed (for the refusal's words).
// Consumer declared; satisfied in the composition root by an adapter over the
// billing repository. The Tx half is the same read on a caller's transaction:
// the sync confirm guards inside the transaction that seals the pickup, and a
// pool read there would take a second connection while the transaction holds
// its own (the connection starvation the pickups adapter's own comments rule
// out).
type PickHoldGate interface {
	HeldFromPicking(ctx context.Context, orderID string) (held bool, owedCents int64, err error)
	HeldFromPickingTx(ctx context.Context, tx pgx.Tx, orderID string) (held bool, owedCents int64, err error)
}

// WithPickHoldGate wires (or overrides) the hold read behind the will call doors.
func (s *Service) WithPickHoldGate(g PickHoldGate) *Service { s.pickHold = g; return s }

// PickHoldGateWired reports whether the will call doors carry the hold (the
// composition root's boot assertion's fact).
func (s *Service) PickHoldGateWired() bool { return s.pickHold != nil }

// refuseHeldFromPicking guards one will call door: nil seam allows, a read
// that cannot be made refuses (the money direction), a held order refuses
// with the mandated sentence.
func (s *Service) refuseHeldFromPicking(ctx context.Context, orderID string) error {
	if s.pickHold == nil {
		return nil
	}
	held, owed, err := s.pickHold.HeldFromPicking(ctx, orderID)
	if err != nil {
		return conflictf("dispatch: the balance hold could not be read, try again in a moment")
	}
	if held {
		if owed > 0 {
			return conflictf("dispatch: %s (%s is owed)", pickHoldWords, money(owed))
		}
		return conflictf("dispatch: %s", pickHoldWords)
	}
	return nil
}

// refuseHeldFromPickingSync guards a sync door (the board app's rack tag
// mint): the mandated sentence travels as the sync engine's refused reason
// (ErrOpRefused), so the push reports the door's rejection in the module's
// own words instead of failing as an operational fault. NIL ALLOWS, an error
// REFUSES, the same posture as the staff door above.
func refuseHeldFromPickingSync(ctx context.Context, gate PickHoldGate, orderID string) error {
	if gate == nil {
		return nil
	}
	held, owed, err := gate.HeldFromPicking(ctx, orderID)
	return holdRefusalSync(held, owed, err)
}

// refuseHeldFromPickingSyncTx is the confirm's guard: the same read on the
// transaction the confirm runs in, which narrows the window between the read
// and the seal (it does not close it against a reversal that takes no lock the
// confirm waits on).
func refuseHeldFromPickingSyncTx(ctx context.Context, gate PickHoldGate, tx pgx.Tx, orderID string) error {
	if gate == nil {
		return nil
	}
	held, owed, err := gate.HeldFromPickingTx(ctx, tx, orderID)
	return holdRefusalSync(held, owed, err)
}

// holdRefusalSync shapes the guard's answer as the sync engine's refused
// reason: the mandated sentence, never the owed figure. The sync door
// resolves any active staff caller with no role check (the scope's
// predicate reads org only), and a figure never reaches an off list role in
// words either (ruling 91); the STAFF door's refusal above keeps the
// figure, its surface being dispatch-board work, held only by roles on the
// cost and margin list.
func holdRefusalSync(held bool, owed int64, err error) error {
	if err != nil {
		return refused(fmt.Errorf("the balance hold could not be read, try again in a moment"))
	}
	if held {
		return refused(fmt.Errorf("%s", pickHoldWords))
	}
	return nil
}
