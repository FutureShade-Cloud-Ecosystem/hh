//go:build integration

package till_test

// fw113b_round2_one_instant_integration_test.go, FW1-13b round 2: the POS return's rows carry ONE
// instant.
//
// The till's own operations read the clock once and pass that instant to every row of their
// transaction (the lodgement's movement, the slip's link rows, the bounce's row and its subledger
// rows). The POS return was not one of them: the drawer movement took the till's clock while the
// return's own REFUNDED payments row took the database's clock, so one transaction ran on two clocks.
//
// The leg here reads the real rows on a migrated and seeded gate database.

import (
	"context"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
)

// TestFW113BThePosReturnRowsCarryOneInstant is the red leg. Before the fix the drawer movement
// carried the till's clock and the payments row the database's, so the two rows of one return
// carried two different instants.
func TestFW113BThePosReturnRowsCarryOneInstant(t *testing.T) {
	w := newMJWorld(t)
	ctx := context.Background()

	pinned := fw113bRound2PinnedDay(t, w.pool)
	svc := till.NewService(till.NewRepository(w.pool)).WithClock(func() time.Time { return pinned })

	tx, err := w.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin the return's tx: %v", err)
	}
	// The payments row this leg's return writes is cleaned up by its own key, so the shared gate
	// database keeps no row this leg left behind.
	t.Cleanup(func() {
		_, _ = w.pool.Exec(context.Background(), `DELETE FROM payments WHERE idempotency_key LIKE 'fw113b-r2-%'`)
	})
	res, err := svc.RefundTender(staffCtx(w.userID, w.branchID), tx, till.RefundInput{
		TillSessionID: w.sessionID, Method: till.MethodCash, AmountCents: 12_00, BranchID: w.branchID,
		Reference: "FW1-13b round 2 one instant", IdempotencyKey: fw113bRound2Key("one-instant"),
	})
	if err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("the POS return: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit the return's tx: %v", err)
	}

	var paymentAt time.Time
	if err := w.pool.QueryRow(ctx,
		`SELECT created_at FROM payments WHERE id = $1::uuid`, res.PaymentID).Scan(&paymentAt); err != nil {
		t.Fatalf("read the return's payments row: %v", err)
	}
	if !paymentAt.Equal(pinned) {
		t.Fatalf("the POS return's payments row is stamped %s, want the return's one instant %s", paymentAt, pinned)
	}

	var movementAt time.Time
	if err := w.pool.QueryRow(ctx,
		`SELECT created_at FROM till_movements WHERE till_session_id = $1::uuid AND movement_type = 'PAID_OUT'`,
		w.sessionID).Scan(&movementAt); err != nil {
		t.Fatalf("read the return's movement row: %v", err)
	}
	if !movementAt.Equal(pinned) {
		t.Fatalf("the POS return's movement row is stamped %s, want the return's one instant %s", movementAt, pinned)
	}
}
