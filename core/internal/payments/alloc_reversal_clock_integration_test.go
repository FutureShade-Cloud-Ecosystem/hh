//go:build integration

package payments_test

// alloc_reversal_clock_integration_test.go, W2-6A fix round 4: a reversal is never dated before
// the application it undoes.
//
// TWO CLOCKS FOR ONE FACT. The application row's created_at is the column default, Postgres now().
// The void's reversal row is dated by the Go service clock the void was given. When the two
// disagree by a fraction of a second, the reversal lands BEFORE the money it undoes, and every
// reader that lists the allocations in time order puts the reversal first.
//
// This file gives the void a clock EARLIER than the application row's instant, so the race is
// certain rather than lucky, and asks the questions that matter:
//   - is the reversal row dated before the application it reverses?
//   - does the history list the application first?
// and reads the guard's own word on a reversal that is dated before its application.
//
// Run: go test -tags integration -count=1 ./internal/payments/... with DATABASE_URL on a migrated and seeded database.
import (
	"context"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
)

// The void never dates its reversal before the application it undoes, and the history lists the
// application first.
func TestIntegration_W2_6A_TheVoidNeverDatesItsReversalBeforeTheApplication(t *testing.T) {
	w := newReversalGLWorld(t)
	ctx := context.Background()
	orig := w.registerReceipt("CHEQUE", 10_000)
	w.drain()

	// The receipt's own instant. The void's clock reads at it, which is on the same dealer day (the
	// void is only allowed on the day the receipt was taken) and before the application's row.
	var recvAt time.Time
	if err := w.r.obs.QueryRow(ctx,
		`SELECT received_at FROM payments WHERE id = $1::uuid`, orig).Scan(&recvAt); err != nil {
		t.Fatalf("read the receipt's instant: %v", err)
	}
	// The application's row reads two seconds after the receipt, so the gap between the void's clock
	// and the application's instant is certain rather than a fraction of a second.
	appAt := recvAt.Add(2 * time.Second)
	if _, err := w.r.pool.Exec(ctx,
		`UPDATE payment_allocations SET created_at = $2::timestamptz
		  WHERE payment_id = $1::uuid AND kind = 'APPLICATION'`, orig, appAt); err != nil {
		t.Fatalf("date the application: %v", err)
	}

	repo := payments.NewRepository(w.r.pool)
	if _, err := repo.VoidPayment(ctx, payments.VoidInput{
		PaymentID: orig, Reason: "keyed against the wrong figures", ActorID: w.r.userID, IsManager: true,
	}, recvAt, houseday.DefaultZone()); err != nil {
		t.Fatalf("void: %v", err)
	}

	// The reversal row is never dated before the money it undoes.
	var revAt time.Time
	if err := w.r.obs.QueryRow(ctx,
		`SELECT created_at FROM payment_allocations
		  WHERE payment_id = $1::uuid AND kind = 'REVERSAL'`, orig).Scan(&revAt); err != nil {
		t.Fatalf("read the reversal's instant: %v", err)
	}
	if revAt.Before(appAt) {
		t.Fatalf("the reversal row is dated %s and the application %s: a reversal is never dated before the money it undoes",
			revAt, appAt)
	}

	// The history lists the application first and the reversal beside it.
	h, err := till.NewService(till.NewRepository(w.r.pool)).PaymentHistory(w.r.staffCtx(), orig)
	if err != nil {
		t.Fatalf("PaymentHistory: %v", err)
	}
	if len(h.Allocations) != 2 {
		t.Fatalf("want the application and the reversal beside it, got %d: %+v", len(h.Allocations), h.Allocations)
	}
	if h.Allocations[0].AmountCents != 10_000 {
		t.Fatalf("the history lists the reversal before the application: %+v", h.Allocations)
	}
	if h.Allocations[1].AmountCents != -10_000 {
		t.Fatalf("the reversal row facts are wrong: %+v", h.Allocations[1])
	}

	// The pair still nets zero: the undo is the exact negative of the application.
	var net int64
	if err := w.r.obs.QueryRow(ctx,
		`SELECT COALESCE(SUM(amount_cents), 0) FROM payment_allocations WHERE payment_id = $1::uuid`, orig).
		Scan(&net); err != nil {
		t.Fatalf("read the allocation net: %v", err)
	}
	if net != 0 {
		t.Fatalf("the pair nets %d, want 0: the void took the application back", net)
	}
}

// The history lists the application first and the reversal beside it, for the void's undo as for the
// bounce's. The history read lives in the till module, so the leg reads it through that module's own
// service: the money desk's answer is one answer, whichever module wrote the rows.
func TestIntegration_W2_6A_TheVoidHistoryListsTheApplicationFirst(t *testing.T) {
	w := newReversalGLWorld(t)
	ctx := context.Background()
	orig := w.registerReceipt("CHEQUE", 10_000)
	w.drain()

	var recvAt time.Time
	if err := w.r.obs.QueryRow(ctx,
		`SELECT received_at FROM payments WHERE id = $1::uuid`, orig).Scan(&recvAt); err != nil {
		t.Fatalf("read the receipt's instant: %v", err)
	}
	appAt := recvAt.Add(2 * time.Second)
	if _, err := w.r.pool.Exec(ctx,
		`UPDATE payment_allocations SET created_at = $2::timestamptz
		  WHERE payment_id = $1::uuid AND kind = 'APPLICATION'`, orig, appAt); err != nil {
		t.Fatalf("date the application: %v", err)
	}

	repo := payments.NewRepository(w.r.pool)
	if _, err := repo.VoidPayment(ctx, payments.VoidInput{
		PaymentID: orig, Reason: "keyed against the wrong figures", ActorID: w.r.userID, IsManager: true,
	}, recvAt, houseday.DefaultZone()); err != nil {
		t.Fatalf("void: %v", err)
	}

	h, err := till.NewService(till.NewRepository(w.r.pool)).PaymentHistory(w.r.staffCtx(), orig)
	if err != nil {
		t.Fatalf("PaymentHistory: %v", err)
	}
	if len(h.Allocations) != 2 {
		t.Fatalf("want the application and the reversal beside it, got %d: %+v", len(h.Allocations), h.Allocations)
	}
	if h.Allocations[0].AmountCents != 10_000 {
		t.Fatalf("the history lists the reversal before the application: %+v", h.Allocations)
	}
	if h.Allocations[1].AmountCents != -10_000 {
		t.Fatalf("the reversal row facts are wrong: %+v", h.Allocations[1])
	}
}

// The guard refuses a reversal row dated before the application it reverses, in words. The writers
// never write one (they date the row at the application's own instant at the earliest), so this is
// the fence for any other writer that reaches the table.
func TestIntegration_W2_6A_TheGuardRefusesAReversalDatedBeforeItsApplication(t *testing.T) {
	w := newReversalGLWorld(t)
	ctx := context.Background()
	orig := w.registerReceipt("CHEQUE", 10_000)
	inv := w.invoiceOf(orig)

	var appID string
	var appAt time.Time
	if err := w.r.obs.QueryRow(ctx,
		`SELECT id::text, created_at FROM payment_allocations
		  WHERE payment_id = $1::uuid AND kind = 'APPLICATION'`, orig).Scan(&appID, &appAt); err != nil {
		t.Fatalf("read the application row: %v", err)
	}

	// A reversal dated before the application it names is refused.
	if _, err := w.r.pool.Exec(ctx,
		`INSERT INTO payment_allocations
		   (payment_id, invoice_id, amount_cents, kind, reverses_allocation_id, created_at)
		 VALUES ($1::uuid, $2::uuid, -10000, 'REVERSAL', $3::uuid, $4::timestamptz)`,
		orig, inv, appID, appAt.Add(-time.Hour)); err == nil {
		t.Fatalf("a reversal row dated before the application it reverses was accepted")
	}

	// The same reversal dated at the application's own instant is accepted: the guard refuses the
	// order, not the undo.
	if _, err := w.r.pool.Exec(ctx,
		`INSERT INTO payment_allocations
		   (payment_id, invoice_id, amount_cents, kind, reverses_allocation_id, created_at)
		 VALUES ($1::uuid, $2::uuid, -10000, 'REVERSAL', $3::uuid, $4::timestamptz)`,
		orig, inv, appID, appAt); err != nil {
		t.Fatalf("the reversal row dated at the application's instant was refused: %v", err)
	}
}
