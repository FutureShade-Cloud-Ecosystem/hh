package tasks

// yard_hold.go, item 9 Opus P2-1: the yard cannot stage or claim a held
// order's task.
//
// The pick feed marks the held rows (the ticket carries the mark, so the
// queue shows the order as held with "Balance due" and the yard knows why it
// waits); the marks are words, and words do not stop a picker whose list says
// pull it. Stage and Claim are the doors: both refuse an order held from
// picking under a pay before release shape with the mandated sentence, the
// same words every other door carries (the order module's status moves, the
// will call mints and confirms).
//
// NIL ALLOWS, an error REFUSES. The lane is additive (a scaffold composition
// without the seam behaves as before; the composition root asserts the wiring
// at boot), but a wired seam that cannot answer is the money direction and
// fails closed.

import "context"

// PickHoldCheck answers whether an order is held from picking under a pay
// before release shape, with the money still owed (for the queue's mark).
// Consumer declared; satisfied in the composition root by an adapter over the
// billing repository's hold read, so the yard's door and every other door
// read the one number.
type PickHoldCheck interface {
	OrderHeldFromPicking(ctx context.Context, orderID string) (held bool, owedCents int64, err error)
}

// WithPickHoldCheck wires (or overrides) the yard doors' hold check.
func (s *Service) WithPickHoldCheck(c PickHoldCheck) *Service { s.holdCheck = c; return s }

// PickHoldCheckWired reports whether the yard doors carry the hold (the
// composition root's boot assertion's fact).
func (s *Service) PickHoldCheckWired() bool { return s.holdCheck != nil }

// refuseHeldTask resolves the task's order and refuses when it is held: nil
// seam allows, a read that cannot be made refuses (ErrHoldUnreadable), a held
// order refuses with the mandated sentence (ErrHeldFromPicking).
func (s *Service) refuseHeldTask(ctx context.Context, taskID string) error {
	if s.holdCheck == nil {
		return nil
	}
	t, err := s.store.GetTask(ctx, taskID)
	if err != nil {
		return err
	}
	held, _, err := s.holdCheck.OrderHeldFromPicking(ctx, t.OrderID)
	if err != nil {
		return ErrHoldUnreadable
	}
	if held {
		return ErrHeldFromPicking
	}
	return nil
}
