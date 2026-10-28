//go:build integration

package till

// alloc_reversal_guard_integration_test.go, W2-6A fix round 1 (P3-1): the guard holds after the
// reversal stands.
//
// Migration 0450 keeps the application beside its reversal, and the guard refuses a reversal that
// names no application or carries the wrong money. The read in PR 668 found the gap the other way
// round: once the reversal stands, an UPDATE could still grow the application (or move it to
// another payment or invoice), so the pair stopped netting zero and the as-of history rewrote
// itself with money dated at the original application's instant.
//
// The till's InsertAllocation upsert adds to the APPLICATION row of a (payment, invoice) pair, so
// it is the one writer that could grow a reversed application from the code. The upsert refuses a
// reversed pair in the WHERE, before the trigger, and the guard is the second fence for any other
// UPDATE that reaches the table.
//
// Run: make reset && go test -tags integration ./internal/till/...
import (
	"context"
	"strings"
	"testing"
)

// S4's exact case: a reversed application cannot be grown by 500 cents, moved to another payment,
// or moved to another invoice. The pair still nets zero, and an update that changes no money (the
// allocated_by stamp) still works.
func TestIntegration_W2_6A_TheGuardHoldsAfterTheReversalStands(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	branchID, userID := seedBranchUser(t, pool)
	svc := realSvc(pool)

	pid, _, _, cleanup := seedNSFCase(t, pool, branchID, 1000)
	t.Cleanup(cleanup)

	if _, err := svc.MarkNSF(globalCtx(userID), NSFInput{PaymentID: pid, Reason: "cheque returned NSF"}); err != nil {
		t.Fatalf("bounce: %v", err)
	}

	// S4's line: 500 cents added to the reversed application was accepted, giving 1500 against a
	// reversal of -1000. The pair must refuse it now.
	if _, err := pool.Exec(ctx,
		`UPDATE payment_allocations SET amount_cents = amount_cents + 500
		  WHERE payment_id = $1::uuid AND kind = 'APPLICATION'`, pid); err == nil ||
		!strings.Contains(err.Error(), "cannot be changed") {
		t.Fatalf("growing the reversed application by 500 cents: err = %v, want the guard's refusal", err)
	}

	// Moving the application to another payment or invoice rewrites the history the same way.
	if _, err := pool.Exec(ctx,
		`UPDATE payment_allocations SET payment_id = gen_random_uuid()
		  WHERE payment_id = $1::uuid AND kind = 'APPLICATION'`, pid); err == nil ||
		!strings.Contains(err.Error(), "cannot be changed") {
		t.Fatalf("moving the reversed application to another payment: err = %v, want the guard's refusal", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE payment_allocations SET invoice_id = gen_random_uuid()
		  WHERE payment_id = $1::uuid AND kind = 'APPLICATION'`, pid); err == nil ||
		!strings.Contains(err.Error(), "cannot be changed") {
		t.Fatalf("moving the reversed application to another invoice: err = %v, want the guard's refusal", err)
	}

	// The pair still nets zero: the undo said zero and the refused writes left it there.
	var appAmt, net int64
	if err := pool.QueryRow(ctx,
		`SELECT amount_cents FROM payment_allocations WHERE payment_id = $1::uuid AND kind = 'APPLICATION'`, pid).Scan(&appAmt); err != nil {
		t.Fatalf("read the application: %v", err)
	}
	if appAmt != 1000 {
		t.Fatalf("the application reads %d cents, want 1000 untouched", appAmt)
	}
	if err := pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(amount_cents), 0) FROM payment_allocations WHERE payment_id = $1::uuid`, pid).Scan(&net); err != nil {
		t.Fatalf("read the allocation net: %v", err)
	}
	if net != 0 {
		t.Fatalf("the pair nets %d, want 0: the reversal stands and the application is unchanged", net)
	}

	// An update that changes no money still works: the allocated_by stamp is not a rewrite.
	if _, err := pool.Exec(ctx,
		`UPDATE payment_allocations SET allocated_by = $2::uuid
		  WHERE payment_id = $1::uuid AND kind = 'APPLICATION'`, pid, userID); err != nil {
		t.Fatalf("stamping the reversed application: %v", err)
	}
}

// The upsert is the one writer that could grow a reversed application from the code. It refuses a
// reversed pair in words, and an application with no reversal upserts as it always did.
func TestIntegration_W2_6A_TheUpsertRefusesAReversedPair(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	branchID, userID := seedBranchUser(t, pool)
	svc := realSvc(pool)
	repo := NewRepository(pool)

	pid, invID, _, cleanup := seedNSFCase(t, pool, branchID, 1000)
	t.Cleanup(cleanup)

	if _, err := svc.MarkNSF(globalCtx(userID), NSFInput{PaymentID: pid, Reason: "cheque returned NSF"}); err != nil {
		t.Fatalf("bounce: %v", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	err = repo.InsertAllocation(ctx, tx, pid, invID, 500, userID)
	_ = tx.Rollback(ctx)
	if err == nil || !strings.Contains(err.Error(), "reversed") {
		t.Fatalf("the upsert on the reversed pair: err = %v, want the refusal in words", err)
	}

	// Nothing was added: the application is 1000 and the pair nets zero.
	var appAmt, net int64
	if err := pool.QueryRow(ctx,
		`SELECT amount_cents FROM payment_allocations WHERE payment_id = $1::uuid AND kind = 'APPLICATION'`, pid).Scan(&appAmt); err != nil {
		t.Fatalf("read the application: %v", err)
	}
	if appAmt != 1000 {
		t.Fatalf("the application reads %d cents, want 1000: the refused upsert added nothing", appAmt)
	}
	if err := pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(amount_cents), 0) FROM payment_allocations WHERE payment_id = $1::uuid`, pid).Scan(&net); err != nil {
		t.Fatalf("read the allocation net: %v", err)
	}
	if net != 0 {
		t.Fatalf("the pair nets %d, want 0 after the refused upsert", net)
	}
}

func TestIntegration_W2_6A_TheUpsertStillAddsToAnUnreversedPair(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	branchID, userID := seedBranchUser(t, pool)
	repo := NewRepository(pool)

	pid, invID, _, cleanup := seedNSFCase(t, pool, branchID, 1000)
	t.Cleanup(cleanup)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := repo.InsertAllocation(ctx, tx, pid, invID, 500, userID); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("the upsert on the standing pair: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	var appAmt int64
	if err := pool.QueryRow(ctx,
		`SELECT amount_cents FROM payment_allocations WHERE payment_id = $1::uuid AND kind = 'APPLICATION'`, pid).Scan(&appAmt); err != nil {
		t.Fatalf("read the application: %v", err)
	}
	if appAmt != 1500 {
		t.Fatalf("the application reads %d cents, want 1500: the standing pair upserts as it always did", appAmt)
	}
}
