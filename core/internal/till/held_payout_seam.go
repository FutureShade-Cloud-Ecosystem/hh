package till

// held_payout_seam.go, SF-2m and SF-2n: the till's part of paying out the money a reversal holds in 2310.
//
// The payments module owns the act (payments/payout.go): the held figure, the manager gate, the reason, the payout row
// and the GL event. The till owns the drawer, so the PAID_OUT movement a drawer pay out needs is written HERE, on the
// payments transaction the caller hands in, and nowhere else: the till stays the sole writer of till_movements. The
// movement is the one a cash refund writes (RefundTender), so the session's expected cash falls by the cash handed
// back. The session row is read FOR SHARE (GetSessionTx, SF-2j round 2), so a count of the session queues behind the
// movement or finds it committed: nothing lands on a session after its count.
//
// The write off reads the count the same way: a session's status and its over or short, for the payments module to
// judge a write off against.

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
)

var _ payments.TillSeam = (*Service)(nil)

// PayOutHeldMoney records a cash pay out of held money as a PAID_OUT movement on an OPEN session of the payment's
// branch the caller can reach, inside the caller's transaction.
func (s *Service) PayOutHeldMoney(ctx context.Context, tx pgx.Tx, in payments.DrawerPaidOut) error {
	caller, err := s.caller(ctx)
	if err != nil {
		return err
	}
	if in.AmountCents <= 0 {
		return payments.PayoutRefusedError{Status: 400, Msg: "till: a pay out amount must be > 0"}
	}
	sess, err := s.store.GetSessionTx(ctx, tx, in.SessionID)
	if err != nil {
		if errors.Is(err, errNotFound) {
			return payments.PayoutRefusedError{Status: 404, Msg: "till: no such till session"}
		}
		return err
	}
	if sess.Status != StatusOpen {
		return payments.PayoutRefusedError{Status: 409, Msg: fmt.Sprintf("till: a cash pay out needs an OPEN drawer, and this session is %s", sess.Status)}
	}
	if sess.BranchID != in.BranchID {
		return payments.PayoutRefusedError{Status: 409, Msg: "till: that drawer belongs to another branch than the payment"}
	}
	if !caller.CanAccessBranch(sess.BranchID) {
		return payments.PayoutRefusedError{Status: 404, Msg: "till: no such till session"}
	}
	return s.store.InsertMovementTx(ctx, tx, MovementRow{
		TillSessionID: sess.ID,
		MovementType:  MovementPaidOut,
		Method:        MethodCash,
		AmountCents:   in.AmountCents,
		Reason:        "HELD PAYOUT " + in.Reason,
		Reference:     in.Reference,
		CreatedBy:     caller.UserID,
	})
}

// SessionCountTx answers a session's status and over or short on the caller's transaction, the session row FOR SHARE.
func (s *Service) SessionCountTx(ctx context.Context, tx pgx.Tx, sessionID string) (payments.SessionCount, error) {
	if _, err := s.caller(ctx); err != nil {
		return payments.SessionCount{}, err
	}
	var c payments.SessionCount
	err := tx.QueryRow(ctx,
		`SELECT status, branch_id::text, COALESCE(over_short_cents, 0) FROM till_sessions WHERE id = $1::uuid FOR SHARE`,
		sessionID).Scan(&c.Status, &c.BranchID, &c.OverShortCents)
	if errors.Is(err, pgx.ErrNoRows) {
		return payments.SessionCount{}, payments.PayoutRefusedError{Status: 404, Msg: "till: no such till session"}
	}
	if err != nil {
		return payments.SessionCount{}, fmt.Errorf("till: read the session count: %w", err)
	}
	return c, nil
}
