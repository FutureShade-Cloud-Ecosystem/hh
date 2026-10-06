//go:build integration

// attempts_sweep_integration_test.go: crash window leg 6(c), the bind and sweep race (the card
// charge design section 5.3 leg 6), on the real pool: a live bind transaction takes the attempt
// row's lock FOR UPDATE through the store's guarded bind, the sweep pass starts while that
// transaction is still open, and the leg pins that the sweep WAITS on the row (it cannot finish
// before the bind's commit) and then loses its guarded move cleanly: the row is BOUND, the sweep
// voided nothing, and the processor saw nothing. The sweep NEVER moves a BOUND row.
//
// It rides the attempts store's own rig (attemptsPools, attemptFx) on the database the command
// names and skips without one.

package payment

import (
	"context"
	"testing"
	"time"
)

// TestSweepLeg6c_BindRaceLeavesTheBoundRowAlone is crash window leg 6(c): the sweep's guarded
// void of a CAPTURED row past grace blocks on the live bind's row lock, and once the bind
// commits, matches nothing. The row stays BOUND; the processor saw nothing.
func TestSweepLeg6c_BindRaceLeavesTheBoundRowAlone(t *testing.T) {
	fx := newAttemptFx(t)
	a := fx.claim(fx.in(2500))
	fx.force(a.ID, AttemptCaptured)
	base := time.Now().Add(-time.Hour)
	clock := func() time.Time { return base }

	ctx := context.Background()
	if _, err := fx.helper.Exec(ctx,
		`UPDATE card_charge_attempts SET updated_at = $2 WHERE id = $1::uuid`,
		a.ID, base.Add(-2*AttemptGrace)); err != nil {
		t.Fatalf("backdate the planted row: %v", err)
	}
	payID := fx.newPayment()

	proc := &sweepProc{}
	svc := NewService(fx.store, proc)
	tx, err := fx.helper.Begin(ctx)
	if err != nil {
		t.Fatalf("begin the live bind: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })
	if _, ok, err := fx.store.BindTx(ctx, tx, a.ID, payID); err != nil || !ok {
		t.Fatalf("the live bind did not take the row: ok=%v err=%v", ok, err)
	}
	// The bind's guarded UPDATE now holds the row lock, uncommitted: the exact shape of a live
	// request inside its sale transaction.

	type sweepPass struct {
		res SweepResult
		err error
	}
	done := make(chan sweepPass, 1)
	go func() {
		res, err := svc.SweepCardAttempts(ctx, clock)
		done <- sweepPass{res, err}
	}()

	select {
	case p := <-done:
		t.Fatalf("the sweep finished while the bind transaction was still open (res %+v, err %v): it did not wait on the bound row's lock", p.res, p.err)
	case <-time.After(500 * time.Millisecond):
		// The sweep is inside its guarded void, waiting for the bind: the race is real.
	}

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit the live bind: %v", err)
	}
	p := <-done
	if p.err != nil {
		t.Fatalf("the sweep's pass: %v", p.err)
	}
	if p.res.Examined != 1 || p.res.Voided != 0 || p.res.Lost != 1 {
		t.Fatalf("the sweep's pass = %+v, want the row examined once, its void lost to the bind, nothing voided", p.res)
	}
	row, err := scanAttempt(fx.helper.QueryRow(context.Background(),
		`SELECT `+attemptColumns+` FROM card_charge_attempts WHERE id = $1::uuid`, a.ID))
	if err != nil {
		t.Fatalf("read the attempt: %v", err)
	}
	if row.Status != AttemptBound || row.PaymentID != payID {
		t.Fatalf("row after the race = %s payment %q, want BOUND on the bind's payments row: the sweep never moves a BOUND row", row.Status, row.PaymentID)
	}
	if c, v, r := proc.counts(); c != 0 || v != 0 || r != 0 {
		t.Fatalf("the sweep made %d charge(s), %d void(s), %d refund(s), want none: it lost the race and touched no money", c, v, r)
	}
}
