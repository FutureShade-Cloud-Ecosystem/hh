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
)

// pickHoldWords is the item's mandated sentence, the same words the order
// module's refusal carries.
const pickHoldWords = "This order cannot reach picking until the balance is paid."

// PickHoldGate answers whether an order is held from picking under a pay
// before release shape, with the money still owed (for the refusal's words).
// Consumer declared; satisfied in the composition root by an adapter over the
// billing repository.
type PickHoldGate interface {
	HeldFromPicking(ctx context.Context, orderID string) (held bool, owedCents int64, err error)
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
