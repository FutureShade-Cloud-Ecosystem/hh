//go:build integration

// lockorder_reversed_integration_test.go: a reversed payment must never be deposited or matched.
//
// A reversal (reversal.go) stamps reversed_at on the original payment and leaves its status RECEIVED.
// The two writers that move RECEIVED to DEPOSITED, the bank match claim (bankrec PersistMatch) and
// till's MarkDeposited, used to look only at the status, so a reversed payment could be booked into a
// bank match and read DEPOSITED while both writers reported success. The refusal is decided inside the
// UPDATE that moves the status (the row lock and the WHERE re-check after a concurrent commit); there
// is no separate pre-check read to race.
//
// THE RULE FOR THE OTHER ORDER. A payment that was matched or deposited first and reversed after is
// reversible: reversal.go refuses only a payment that is already reversed, is itself a reversing entry
// or has status VOID (the three checks after the locked read), and DEPOSITED is none of them. So the
// match-first order commits both writes, and the payment ends DEPOSITED with reversed_at set.
//
// Every concurrent leg runs at pool 4 and pool 16 in the widened window of the other lock order legs,
// and honours LOCK-ORDER.md: both writers take the payment row, then the payments counter.
package payments_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
)

// isReversedRefusal: the writers' refusal of a reversed payment names the reversal.
func isReversedRefusal(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "reversed")
}

func (r *loRig) reverse(paymentID string) error {
	_, err := r.repo.ReversePayment(context.Background(), payments.ReverseInput{
		PaymentID: paymentID, Reason: "reversed leg", ActorID: r.userID, IsManager: true}, time.Now())
	return err
}

// paymentState reads what the leg asserts on: the payment's status, whether it is reversed, its seq.
func (r *loRig) paymentState(id string) (status string, reversed bool, seq int64) {
	r.t.Helper()
	if err := r.obs.QueryRow(context.Background(),
		`SELECT status, reversed_at IS NOT NULL, COALESCE(seq, 0) FROM payments WHERE id = $1::uuid`, id).
		Scan(&status, &reversed, &seq); err != nil {
		r.t.Fatalf("read payment: %v", err)
	}
	return status, reversed, seq
}

func (r *loRig) lineState(lineID string) (status string, matches int) {
	r.t.Helper()
	if err := r.obs.QueryRow(context.Background(),
		`SELECT l.status, (SELECT count(*) FROM bank_matches m WHERE m.statement_line_id = l.id)
		   FROM bank_statement_lines l WHERE l.id = $1::uuid`, lineID).Scan(&status, &matches); err != nil {
		r.t.Fatalf("read statement line: %v", err)
	}
	return status, matches
}

// (a) a bank match beside a reversal of the same payment, in both orders.
func TestIntegration_LockOrder_ReversalBesideBankMatchOfTheSamePayment(t *testing.T) {
	t.Run("reversal first: the match is refused", func(t *testing.T) {
		loPoolSizes(t, func(t *testing.T, size int32) {
			r := newLORig(t, size)
			p := r.payOnce(r.invoice("RV1", 1_000_000), 10_000)
			line := r.statementLine(10_000)
			r.armSeqWindow("payments")
			revErr := make(chan error, 1)
			go func() { revErr <- r.reverse(p.ID) }()
			r.waitSessions("reversal parked holding the payments counter row", 1, condSleepingInSeq)
			matchErr := make(chan error, 1)
			go func() { matchErr <- r.match(line, p.ID, 10_000) }()
			r.waitSessions("match queued behind the reversal", 1, condLockWaiter)
			rev, mat := loAnswer(t, "reversal", revErr), loAnswer(t, "match", matchErr)
			if rev != nil {
				t.Fatalf("the reversal failed: %v", rev)
			}
			if loIsDeadlock(mat) {
				t.Fatalf("the match deadlocked: %v", mat)
			}
			if !isReversedRefusal(mat) {
				t.Errorf("the match of a reversed payment answered %v, want a refusal naming the reversal", mat)
			}
			if st, n := r.lineState(line); st != "UNMATCHED" || n != 0 {
				t.Errorf("the statement line is %s with %d matches after a refused match, want UNMATCHED with none", st, n)
			}
			if st, reversed, _ := r.paymentState(p.ID); st == "DEPOSITED" || !reversed {
				t.Errorf("the reversed payment is %s (reversed %v), want it left RECEIVED and reversed", st, reversed)
			}
		})
	})
	t.Run("match first: the reversal proceeds (reversal.go refuses only a reversed, reversing or VOID payment)", func(t *testing.T) {
		loPoolSizes(t, func(t *testing.T, size int32) {
			r := newLORig(t, size)
			p := r.payOnce(r.invoice("RV2", 1_000_000), 10_000)
			line := r.statementLine(10_000)
			r.armSeqWindow("payments")
			matchErr := make(chan error, 1)
			go func() { matchErr <- r.match(line, p.ID, 10_000) }()
			r.waitSessions("match parked holding the payments counter row", 1, condSleepingInPaymentUpdate)
			revErr := make(chan error, 1)
			go func() { revErr <- r.reverse(p.ID) }()
			r.waitSessions("reversal queued behind the match", 1, condLockWaiter)
			loBothAnswered(t, "match", loAnswer(t, "match", matchErr), "reversal", loAnswer(t, "reversal", revErr))
			if st, reversed, _ := r.paymentState(p.ID); st != "DEPOSITED" || !reversed {
				t.Errorf("the payment is %s (reversed %v), want DEPOSITED and reversed: banked first, reversed after", st, reversed)
			}
			if st, n := r.lineState(line); st != "MATCHED" || n != 1 {
				t.Errorf("the statement line is %s with %d matches, want MATCHED with one", st, n)
			}
		})
	})
}

// (b) MarkDeposited on a reversed payment is refused; on a payment already DEPOSITED it stays the no op.
func TestIntegration_MarkDepositedRefusesAReversedPaymentAndKeepsTheReplayNoOp(t *testing.T) {
	r := newLORig(t, 4)
	repo := till.NewRepository(r.pool)
	ctx := context.Background()

	reversed := r.payOnce(r.invoice("MD1", 1_000_000), 10_000)
	if err := r.reverse(reversed.ID); err != nil {
		t.Fatalf("reverse: %v", err)
	}
	err := repo.MarkDeposited(ctx, []string{reversed.ID}, "lo-line")
	if !errors.Is(err, till.ErrPaymentReversed) {
		t.Errorf("MarkDeposited of a reversed payment answered %v, want till.ErrPaymentReversed", err)
	}
	if st, _, _ := r.paymentState(reversed.ID); st == "DEPOSITED" {
		t.Errorf("the reversed payment reads DEPOSITED after a refused MarkDeposited")
	}

	live := r.payOnce(r.invoice("MD2", 1_000_000), 10_000)
	if err := repo.MarkDeposited(ctx, []string{live.ID}, "lo-line"); err != nil {
		t.Fatalf("MarkDeposited of a live payment: %v", err)
	}
	_, _, seq := r.paymentState(live.ID)
	if err := repo.MarkDeposited(ctx, []string{live.ID}, "lo-line"); err != nil {
		t.Errorf("MarkDeposited of an already DEPOSITED payment answered %v, want the idempotent no op", err)
	}
	if st, _, after := r.paymentState(live.ID); st != "DEPOSITED" || after != seq {
		t.Errorf("a replayed MarkDeposited moved the payment: status %s seq %d -> %d", st, seq, after)
	}

	// A batch with one live and one reversed payment: the reversed one refuses the call, and the call
	// rolls back whole, so the live one is not moved either.
	mixedLive := r.payOnce(r.invoice("MD3", 1_000_000), 10_000)
	err = repo.MarkDeposited(ctx, []string{mixedLive.ID, reversed.ID}, "lo-line")
	if !errors.Is(err, till.ErrPaymentReversed) {
		t.Errorf("MarkDeposited of a batch holding a reversed payment answered %v, want till.ErrPaymentReversed", err)
	}
	if st, _, _ := r.paymentState(mixedLive.ID); st != "RECEIVED" {
		t.Errorf("the live payment of a refused batch reads %s, want it left RECEIVED (the call rolls back whole)", st)
	}
}

// (c) single connection: a reversed payment cannot be matched, and cannot be deposited; the refused
// write changes no row, so the payments feed is not told anything.
func TestIntegration_AReversedPaymentCannotBeMatchedOrDeposited(t *testing.T) {
	r := newLORig(t, 4)
	p := r.payOnce(r.invoice("PL1", 1_000_000), 10_000)
	line := r.statementLine(10_000)
	if err := r.reverse(p.ID); err != nil {
		t.Fatalf("reverse: %v", err)
	}
	f := r.feeds()
	cursor, _ := f.pull(t, f.payInfo, f.tenant, 0)
	st0, _, seq0 := r.paymentState(p.ID)

	if err := r.match(line, p.ID, 10_000); !isReversedRefusal(err) {
		t.Errorf("matching a reversed payment answered %v, want a refusal naming the reversal", err)
	}
	if err := till.NewRepository(r.pool).MarkDeposited(context.Background(), []string{p.ID}, "lo-line"); !errors.Is(err, till.ErrPaymentReversed) {
		t.Errorf("depositing a reversed payment answered %v, want till.ErrPaymentReversed", err)
	}

	if st, n := r.lineState(line); st != "UNMATCHED" || n != 0 {
		t.Errorf("the statement line is %s with %d matches, want UNMATCHED with none", st, n)
	}
	st1, _, seq1 := r.paymentState(p.ID)
	if st1 != st0 || seq1 != seq0 {
		t.Errorf("a refused write changed the payment: status %s -> %s, seq %d -> %d", st0, st1, seq0, seq1)
	}
	// The feed: nothing past the cursor the device held after the reversal.
	_, got := f.pull(t, f.payInfo, f.tenant, cursor)
	if len(got) != 0 {
		t.Errorf("the payments feed re-served %d rows after refused writes, want none", len(got))
	}
}
