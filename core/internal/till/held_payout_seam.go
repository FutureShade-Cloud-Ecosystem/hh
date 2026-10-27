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

// PayOutHeldMoney records a cash pay out of held money as a PAID_OUT movement on an OPEN session of the payment's branch
// the caller can reach, inside the caller's transaction, and answers the session it landed on. A named session is
// checked; an unnamed one is the branch's one OPEN drawer, refused in words when there is none or more than one (the
// Money Desk cannot name a drawer, and the cash is handed back from the till the dealer is standing at).
func (s *Service) PayOutHeldMoney(ctx context.Context, tx pgx.Tx, in payments.DrawerPaidOut) (string, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return "", err
	}
	if in.AmountCents <= 0 {
		return "", payments.PayoutRefusedError{Status: 400, Msg: "till: a pay out amount must be > 0"}
	}
	sessionID := in.SessionID
	if sessionID == "" {
		rows, err := tx.Query(ctx, `SELECT id::text FROM till_sessions WHERE branch_id = $1::uuid AND status = 'OPEN' ORDER BY opened_at LIMIT 2`, in.BranchID)
		if err != nil {
			return "", fmt.Errorf("till: find the open drawer: %w", err)
		}
		var open []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return "", fmt.Errorf("till: scan the open drawer: %w", err)
			}
			open = append(open, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return "", err
		}
		switch len(open) {
		case 0:
			return "", payments.PayoutRefusedError{Status: 409, Msg: "till: no drawer is open at the payment's branch to hand the cash back from"}
		case 1:
			sessionID = open[0]
		default:
			return "", payments.PayoutRefusedError{Status: 409, Msg: "till: more than one drawer is open at the payment's branch, pay it out from the till the cash leaves"}
		}
	}
	sess, err := s.store.GetSessionTx(ctx, tx, sessionID)
	if err != nil {
		if errors.Is(err, errNotFound) {
			return "", payments.PayoutRefusedError{Status: 404, Msg: "till: no such till session"}
		}
		return "", err
	}
	if sess.Status != StatusOpen {
		return "", payments.PayoutRefusedError{Status: 409, Msg: fmt.Sprintf("till: a cash pay out needs an OPEN drawer, and this session is %s", sess.Status)}
	}
	if sess.BranchID != in.BranchID {
		return "", payments.PayoutRefusedError{Status: 409, Msg: "till: that drawer belongs to another branch than the payment"}
	}
	if !caller.CanAccessBranch(sess.BranchID) {
		return "", payments.PayoutRefusedError{Status: 404, Msg: "till: no such till session"}
	}
	// One clock rules the transaction (FW1-13b): the pay out's movement row carries the CALLER's own
	// instant (the payments module reads its clock once for the act that moves the money, and the
	// payout row and the event it writes carry that same instant), so the drawer and the books name
	// the same day. The till's own clock rules when the caller names none.
	at := in.At
	if at.IsZero() {
		at = s.now()
	}
	if err := s.store.InsertMovementTx(ctx, tx, MovementRow{
		TillSessionID: sess.ID,
		MovementType:  MovementPaidOut,
		Method:        MethodCash,
		AmountCents:   in.AmountCents,
		Reason:        "HELD PAYOUT " + in.Reason,
		Reference:     in.Reference,
		CreatedBy:     caller.UserID,
		CreatedAt:     at,
	}); err != nil {
		return "", err
	}
	return sess.ID, nil
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

// SessionCountForWriteOffTx is SessionCountTx with the session row FOR UPDATE: a write off sums the session's earlier
// write offs after this read, and two write offs of one session must not each read the other's sum as zero (PR 606
// round 2, S3's P1). The second queues here until the first commits, then re-reads what changed. The caller already
// holds the payment row, so the order stays payment row, then session; no session writer takes a payment row.
func (s *Service) SessionCountForWriteOffTx(ctx context.Context, tx pgx.Tx, sessionID string) (payments.SessionCount, error) {
	if _, err := s.caller(ctx); err != nil {
		return payments.SessionCount{}, err
	}
	var c payments.SessionCount
	err := tx.QueryRow(ctx,
		`SELECT status, branch_id::text, COALESCE(over_short_cents, 0) FROM till_sessions WHERE id = $1::uuid FOR UPDATE`,
		sessionID).Scan(&c.Status, &c.BranchID, &c.OverShortCents)
	if errors.Is(err, pgx.ErrNoRows) {
		return payments.SessionCount{}, payments.PayoutRefusedError{Status: 404, Msg: "till: no such till session"}
	}
	if err != nil {
		return payments.SessionCount{}, fmt.Errorf("till: read the session count for the write off: %w", err)
	}
	return c, nil
}
