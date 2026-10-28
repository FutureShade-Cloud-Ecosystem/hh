//go:build integration

package till

// alloc_reversal_clock_integration_test.go, W2-6A fix round 4: a reversal is never dated before
// the application it undoes.
//
// TWO CLOCKS FOR ONE FACT. The application row's created_at is the column default, Postgres now(),
// written by InsertAllocation. The bounce's reversal row is dated by the Go service clock (one clock
// rules the bounce's transaction, FW1-13b). When the two disagree by a fraction of a second, the
// reversal lands BEFORE the money it undoes, and the history (ORDER BY pa.created_at) lists it
// first. An as-of aging taken between the two instants then nets the payment below zero.
//
// The race is why these legs exist: CI saw the history put the reversal before its application
// because the race went that way by a fraction of a second. Here the bounce's clock is given an
// instant EARLIER than the application row's, so the race is certain rather than lucky, and each leg
// asks one question:
//   - is the reversal row dated before the application it reverses?
//   - does the history list the application first?
//
// Run: go test -tags integration -count=1 ./internal/till/... with DATABASE_URL on a migrated and seeded database.
import (
	"context"
	"testing"
	"time"
)

// The bounce never dates its reversal before the application it undoes.
func TestIntegration_W2_6A_TheBounceNeverDatesItsReversalBeforeTheApplication(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	branchID, userID := seedBranchUser(t, pool)

	pid, _, _, cleanup := seedNSFCase(t, pool, branchID, 7700)
	t.Cleanup(cleanup)

	// The application's own instant: the column default, Postgres now().
	var appAt time.Time
	if err := pool.QueryRow(ctx,
		`SELECT created_at FROM payment_allocations
		  WHERE payment_id = $1::uuid AND kind = 'APPLICATION'`, pid).Scan(&appAt); err != nil {
		t.Fatalf("read the application's instant: %v", err)
	}

	// The bounce's service clock reads EARLIER than the application row's instant. That is the race,
	// made certain.
	bounceAt := appAt.Add(-time.Hour)
	svc := realSvc(pool).WithClock(func() time.Time { return bounceAt })
	if _, err := svc.MarkNSF(globalCtx(userID), NSFInput{PaymentID: pid, Reason: "cheque returned NSF"}); err != nil {
		t.Fatalf("bounce: %v", err)
	}

	// The reversal row is never dated before the money it undoes.
	var revAt time.Time
	if err := pool.QueryRow(ctx,
		`SELECT created_at FROM payment_allocations
		  WHERE payment_id = $1::uuid AND kind = 'REVERSAL'`, pid).Scan(&revAt); err != nil {
		t.Fatalf("read the reversal's instant: %v", err)
	}
	if revAt.Before(appAt) {
		t.Fatalf("the reversal row is dated %s and the application %s: a reversal is never dated before the money it undoes",
			revAt, appAt)
	}
}

// The history lists the application first and the reversal beside it. This is the leg CI saw fail:
// the history put the reversal before the application, so the application row's facts read as the
// reversal's.
func TestIntegration_W2_6A_TheBounceHistoryListsTheApplicationFirst(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	branchID, userID := seedBranchUser(t, pool)

	pid, _, _, cleanup := seedNSFCase(t, pool, branchID, 7700)
	t.Cleanup(cleanup)

	var appAt time.Time
	if err := pool.QueryRow(ctx,
		`SELECT created_at FROM payment_allocations
		  WHERE payment_id = $1::uuid AND kind = 'APPLICATION'`, pid).Scan(&appAt); err != nil {
		t.Fatalf("read the application's instant: %v", err)
	}

	bounceAt := appAt.Add(-time.Hour)
	svc := realSvc(pool).WithClock(func() time.Time { return bounceAt })
	if _, err := svc.MarkNSF(globalCtx(userID), NSFInput{PaymentID: pid, Reason: "cheque returned NSF"}); err != nil {
		t.Fatalf("bounce: %v", err)
	}

	h, err := svc.PaymentHistory(globalCtx(userID), pid)
	if err != nil {
		t.Fatalf("PaymentHistory: %v", err)
	}
	if len(h.Allocations) != 2 {
		t.Fatalf("want the application and the reversal beside it, got %d: %+v", len(h.Allocations), h.Allocations)
	}
	if h.Allocations[0].AmountCents != 7700 {
		t.Fatalf("the history lists the reversal before the application: %+v", h.Allocations)
	}
	if h.Allocations[1].AmountCents != -7700 {
		t.Fatalf("the reversal row facts are wrong: %+v", h.Allocations[1])
	}
	if h.Allocations[1].AllocatedAt.Before(h.Allocations[0].AllocatedAt) {
		t.Fatalf("the history dates the reversal %s before the application %s: %+v",
			h.Allocations[1].AllocatedAt, h.Allocations[0].AllocatedAt, h.Allocations)
	}
}
