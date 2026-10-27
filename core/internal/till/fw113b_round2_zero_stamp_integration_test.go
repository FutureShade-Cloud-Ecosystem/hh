//go:build integration

package till_test

// fw113b_round2_zero_stamp_integration_test.go, FW1-13b round 2: the till refuses a zero stamp.
//
// Round 1 made the service clock the writer of the movement row, the slip's link rows, the bounce row
// and the journals. The round 1 review found the hole that leaves: the movement INSERT writes whatever
// CreatedAt carries, so a caller that sets nothing no longer takes the column default, it writes the
// zero instant. Every date read of the drawer (the Z report's movement list, any window over
// created_at) would then carry a day that is not a day. A loud refusal beats a silent zero.
//
// The legs here read the real rows on a migrated and seeded gate database:
//   - the store refuses a zero CreatedAt on a movement and a zero At on a bounce, before any write;
//   - the two rails that could miss the stamp (the POS return's drawer movement and the held payout's)
//     carry the pinned clock's day, and refuse the zero instant in words when their clock hands them
//     one.

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
	"github.com/jackc/pgx/v5/pgxpool"
)

// fw113bRound2PinnedDay is the instant the legs in this round pin: 15 April of the books' FIRST
// seeded year, noon in the house zone. April is a month of that year no leg reads (the tie-out legs
// read June, the walk reads the month the run happens in), and a day that ended long before the run,
// so the wall clock's day can never stand in for it.
func fw113bRound2PinnedDay(t *testing.T, pool *pgxpool.Pool) time.Time {
	t.Helper()
	var minYear int
	if err := pool.QueryRow(context.Background(), `SELECT COALESCE(MIN(year), 0) FROM fiscal_periods`).Scan(&minYear); err != nil {
		t.Skipf("no fiscal periods to read: %v", err)
	}
	if minYear == 0 {
		t.Skipf("no fiscal periods seeded")
	}
	return time.Date(minYear, time.April, 15, 12, 0, 0, 0, houseday.Location())
}

// fw113bRound2Key is one idempotency key for a leg's own row, unique per run.
func fw113bRound2Key(what string) string {
	return "fw113b-r2-" + what + "-" + fmt.Sprint(os.Getpid()) + "-" + fmt.Sprint(time.Now().UnixNano())
}

// TestFW113BTheStoreRefusesAZeroStamp is the store leg: a zero stamp is refused in words, before any
// write. Before the fix the movement INSERT wrote the zero instant and the bounce row took the
// column default, so this leg found a row where it wanted an error.
func TestFW113BTheStoreRefusesAZeroStamp(t *testing.T) {
	w := newMJWorld(t)
	ctx := context.Background()
	repo := till.NewRepository(w.pool)

	// The pool path: a movement with no instant.
	err := repo.InsertMovement(ctx, till.MovementRow{
		TillSessionID: w.sessionID, MovementType: till.MovementPaidIn, Method: till.MethodCash,
		AmountCents: 1_00, Reason: "FW1-13b round 2 zero stamp",
	})
	if err == nil {
		t.Fatal("the store wrote a movement with no instant: a zero stamp must be refused in words")
	}
	if !strings.Contains(err.Error(), "instant") {
		t.Fatalf("the refusal does not say what is missing: %v", err)
	}
	var stamped int
	if err := w.pool.QueryRow(ctx,
		`SELECT count(*) FROM till_movements WHERE till_session_id = $1::uuid AND created_at = '0001-01-01 00:00:00+00'`,
		w.sessionID).Scan(&stamped); err != nil {
		t.Fatalf("read the zero-stamped movements: %v", err)
	}
	if stamped != 0 {
		t.Fatalf("the store wrote %d movements stamped with the zero instant, want none", stamped)
	}

	// The caller's-tx path: the same movement inside another module's transaction.
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin the caller's tx: %v", err)
	}
	if err := repo.InsertMovementTx(ctx, tx, till.MovementRow{
		TillSessionID: w.sessionID, MovementType: till.MovementPaidIn, Method: till.MethodCash,
		AmountCents: 1_00, Reason: "FW1-13b round 2 zero stamp in the caller's tx",
	}); err == nil {
		_ = tx.Rollback(ctx)
		t.Fatal("the store wrote a movement with no instant inside the caller's tx: a zero stamp must be refused in words")
	} else if !strings.Contains(err.Error(), "instant") {
		_ = tx.Rollback(ctx)
		t.Fatalf("the refusal does not say what is missing: %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("roll the caller's tx back: %v", err)
	}

	// The bounce: a service whose clock hands out the zero instant must be refused, not written.
	zeroClock := till.NewService(till.NewRepository(w.pool)).WithClock(func() time.Time { return time.Time{} })
	cheque := w.slipPayment("CHEQUE", 30_00)
	if _, err := zeroClock.MarkNSF(staffCtx(w.userID, w.branchID), till.NSFInput{
		PaymentID: cheque, Reason: "FW1-13b round 2 zero stamp bounce",
	}); err == nil {
		t.Fatal("the bounce wrote a row with no instant: a zero stamp must be refused in words")
	} else if !strings.Contains(err.Error(), "instant") {
		t.Fatalf("the refusal does not say what is missing: %v", err)
	}
	var bounced int
	if err := w.pool.QueryRow(ctx,
		`SELECT count(*) FROM payment_nsf WHERE payment_id = $1::uuid`, cheque).Scan(&bounced); err != nil {
		t.Fatalf("read the bounce rows: %v", err)
	}
	if bounced != 0 {
		t.Fatalf("the bounce wrote %d rows for a zero instant, want none", bounced)
	}
}

// TestFW113BTheRailsCarryThePinnedClock is the pinned clock leg: the POS return's drawer movement and
// the held payout's drawer movement carry the pinned clock's day, and both rails refuse the zero
// instant in words rather than write it.
//
// Before the fix the two movements took the zero instant silently when a caller forgot the stamp, so
// the leg read a movement that carried no day at all.
func TestFW113BTheRailsCarryThePinnedClock(t *testing.T) {
	w := newMJWorld(t)
	ctx := context.Background()
	zone := houseday.Location()

	pinned := fw113bRound2PinnedDay(t, w.pool)
	wantDay := pinned.In(zone).Format("2006-01-02")
	svc := till.NewService(till.NewRepository(w.pool)).WithClock(func() time.Time { return pinned })

	// The POS return's drawer movement: the rail the pos module drives through RefundTender.
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin the return's tx: %v", err)
	}
	// The payments rows this leg's rails write are cleaned up by their own key, so the shared gate
	// database keeps no row this leg left behind.
	t.Cleanup(func() {
		_, _ = w.pool.Exec(context.Background(), `DELETE FROM payments WHERE idempotency_key LIKE 'fw113b-r2-%'`)
	})
	res, err := svc.RefundTender(staffCtx(w.userID, w.branchID), tx, till.RefundInput{
		TillSessionID: w.sessionID, Method: till.MethodCash, AmountCents: 12_00, BranchID: w.branchID,
		Reference: "FW1-13b round 2 return", IdempotencyKey: fw113bRound2Key("return"),
	})
	if err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("the POS return: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit the return's tx: %v", err)
	}
	if got := w.dayInHouseZone(
		`SELECT to_char(created_at AT TIME ZONE $2, 'YYYY-MM-DD') FROM till_movements
		  WHERE till_session_id = $1::uuid AND movement_type = 'PAID_OUT'`, w.sessionID, zone.String()); got != wantDay {
		t.Fatalf("the POS return's movement row is stamped %s, want the pinned clock's day %s", got, wantDay)
	}
	if res.PaymentID == "" {
		t.Fatal("the POS return wrote no payments row")
	}

	// The held payout's drawer movement: the rail the payments module drives through PayOutHeldMoney.
	tx, err = w.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin the held payout's tx: %v", err)
	}
	if _, err := svc.PayOutHeldMoney(staffCtx(w.userID, w.branchID), tx, payments.DrawerPaidOut{
		SessionID: w.sessionID, BranchID: w.branchID, AmountCents: 7_00,
		Reason: "FW1-13b round 2 held payout", Reference: "FW1-13b round 2 payout",
	}); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("the held payout: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit the held payout's tx: %v", err)
	}
	if got := w.dayInHouseZone(
		`SELECT to_char(created_at AT TIME ZONE $2, 'YYYY-MM-DD') FROM till_movements
		  WHERE till_session_id = $1::uuid AND reason LIKE 'HELD PAYOUT%'`, w.sessionID, zone.String()); got != wantDay {
		t.Fatalf("the held payout's movement row is stamped %s, want the pinned clock's day %s", got, wantDay)
	}

	// The zero instant: both rails refuse it in words and write no movement.
	zeroClock := till.NewService(till.NewRepository(w.pool)).WithClock(func() time.Time { return time.Time{} })
	tx, err = w.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin the zero-clock return's tx: %v", err)
	}
	if _, err := zeroClock.RefundTender(staffCtx(w.userID, w.branchID), tx, till.RefundInput{
		TillSessionID: w.sessionID, Method: till.MethodCash, AmountCents: 9_00, BranchID: w.branchID,
		Reference: "FW1-13b round 2 zero return", IdempotencyKey: fw113bRound2Key("zero-return"),
	}); err == nil {
		_ = tx.Rollback(ctx)
		t.Fatal("the POS return wrote a movement with no instant: a zero stamp must be refused in words")
	} else if !strings.Contains(err.Error(), "instant") {
		_ = tx.Rollback(ctx)
		t.Fatalf("the refusal does not say what is missing: %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("roll the zero-clock return's tx back: %v", err)
	}

	tx, err = w.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin the zero-clock payout's tx: %v", err)
	}
	if _, err := zeroClock.PayOutHeldMoney(staffCtx(w.userID, w.branchID), tx, payments.DrawerPaidOut{
		SessionID: w.sessionID, BranchID: w.branchID, AmountCents: 9_00,
		Reason: "FW1-13b round 2 zero payout", Reference: "FW1-13b round 2 zero payout",
	}); err == nil {
		_ = tx.Rollback(ctx)
		t.Fatal("the held payout wrote a movement with no instant: a zero stamp must be refused in words")
	} else if !strings.Contains(err.Error(), "instant") {
		_ = tx.Rollback(ctx)
		t.Fatalf("the refusal does not say what is missing: %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("roll the zero-clock payout's tx back: %v", err)
	}
}
