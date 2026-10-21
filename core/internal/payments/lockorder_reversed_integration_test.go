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
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/bankrec"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
)

// isReversedRefusal: the bank claim's refusal of a reversed payment carries the claim sentence (it names the
// reversal among its three causes) AND the payment NUMBER of the payment that did not move, so a refusal of some
// other payment does not satisfy it, and never its id: a dealer reads a number, not a uuid. The state checks of
// the legs say which cause it was.
func isReversedRefusal(err error, p payments.Payment) bool {
	if err == nil || p.PaymentNo == "" {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(err.Error(), "payment already claimed, reversed or not RECEIVED: ") &&
		strings.Contains(msg, strings.ToLower(p.PaymentNo)) &&
		!strings.Contains(msg, strings.ToLower(p.ID))
}

// chequeOnce takes a CHEQUE payment through the AR register on one invoice and returns it. A reversed CARD payment is
// matchable since SF-2d round 4 (the processor settles the capture anyway); SF-2b's rule, a reversed payment is never
// matched, holds for every other tender, so the legs of that rule take a cheque.
func (r *loRig) chequeOnce(invoiceID string, cents int64) payments.Payment {
	r.t.Helper()
	if _, err := r.register().RecordPayment(context.Background(), billing.ReceiptInput{
		InvoiceID: invoiceID, Method: "CHEQUE", AmountCents: cents, Reference: "chq lo", IdempotencyKey: fmt.Sprintf("lo-%d", time.Now().UnixNano()),
	}); err != nil {
		r.t.Fatalf("seed cheque: %v", err)
	}
	var p payments.Payment
	if err := r.obs.QueryRow(context.Background(),
		`SELECT p.id::text, p.payment_no::text, p.amount_cents FROM payments p JOIN payment_allocations a ON a.payment_id = p.id
		  WHERE a.invoice_id = $1::uuid AND p.reverses_payment_id IS NULL`, invoiceID).Scan(&p.ID, &p.PaymentNo, &p.AmountCents); err != nil {
		r.t.Fatalf("find the cheque: %v", err)
	}
	p.Method = "CHEQUE"
	return p
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
			p := r.chequeOnce(r.invoice("RV1", 1_000_000), 10_000)
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
			if !isReversedRefusal(mat, p) {
				t.Errorf("the match of a reversed payment answered %v, want the claim sentence naming payment %s", mat, p.ID)
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
			p := r.chequeOnce(r.invoice("RV2", 1_000_000), 10_000)
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

	// A reversed CHEQUE: a reversed CARD is claimable (ruling 55.4, below).
	reversed := r.chequeOnce(r.invoice("MD1", 1_000_000), 10_000)
	if err := r.reverse(reversed.ID); err != nil {
		t.Fatalf("reverse: %v", err)
	}
	err := repo.MarkDeposited(ctx, []string{reversed.ID}, "lo-line")
	if err != nil && (!strings.Contains(err.Error(), reversed.PaymentNo) || strings.Contains(strings.ToLower(err.Error()), strings.ToLower(reversed.ID))) {
		t.Errorf("MarkDeposited's refusal reads %q, want the payment number %s and never the id", err, reversed.PaymentNo)
	}
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

	// Ruling 55.4: MarkDeposited follows the claim, the same predicate (reversed_at IS NULL OR method = 'CARD'), so a
	// reversed CARD is deposited and a reversed cheque stays refused.
	card := r.payOnce(r.invoice("MD4", 1_000_000), 10_000)
	if err := r.reverse(card.ID); err != nil {
		t.Fatalf("reverse the card: %v", err)
	}
	if err := repo.MarkDeposited(ctx, []string{card.ID}, "lo-line"); err != nil {
		t.Errorf("MarkDeposited of a reversed CARD answered %v, want it deposited", err)
	}
	if st, _, _ := r.paymentState(card.ID); st != "DEPOSITED" {
		t.Errorf("the reversed card reads %s after MarkDeposited, want DEPOSITED", st)
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
	p := r.chequeOnce(r.invoice("PL1", 1_000_000), 10_000)
	line := r.statementLine(10_000)
	if err := r.reverse(p.ID); err != nil {
		t.Fatalf("reverse: %v", err)
	}
	f := r.feeds()
	cursor, _ := f.pull(t, f.payInfo, f.tenant, 0)
	st0, _, seq0 := r.paymentState(p.ID)

	if err := r.match(line, p.ID, 10_000); !isReversedRefusal(err, p) {
		t.Errorf("matching a reversed payment answered %v, want the claim sentence naming payment %s", err, p.ID)
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

// The ruled edge (FROM-PRINCIPAL 19): a payment that was deposited, then reversed (legal: reversal.go refuses only
// an already reversed payment, a reversing entry or VOID), and then sent to MarkDeposited AGAIN as a replay is
// REFUSED with till.ErrPaymentReversed, not answered as the idempotent no op an already DEPOSITED payment gets:
// a success would tell the caller the money sits in a deposit when it went back. The row must not move. Pinned
// so a later "idempotence" change cannot turn the refusal into a silent success.
func TestIntegration_MarkDepositedReplayAfterDepositAndReversalIsRefused(t *testing.T) {
	for _, deposit := range []string{"MarkDeposited", "bank match"} {
		t.Run("deposited by "+deposit, func(t *testing.T) {
			r := newLORig(t, 4)
			ctx := context.Background()
			repo := till.NewRepository(r.pool)
			// A cheque: a reversed CARD that is already DEPOSITED replays as the idempotent no op (ruling 55.4).
			p := r.chequeOnce(r.invoice("ED", 1_000_000), 10_000)
			switch deposit {
			case "MarkDeposited":
				if err := repo.MarkDeposited(ctx, []string{p.ID}, "lo-edge"); err != nil {
					t.Fatalf("deposit: %v", err)
				}
			default:
				if err := r.match(r.statementLine(10_000), p.ID, 10_000); err != nil {
					t.Fatalf("match: %v", err)
				}
			}
			if err := r.reverse(p.ID); err != nil {
				t.Fatalf("reversing a DEPOSITED payment is legal, and answered: %v", err)
			}
			st0, rev0, seq0 := r.paymentState(p.ID)
			if st0 != "DEPOSITED" || !rev0 {
				t.Fatalf("precondition: the payment is %s (reversed %v), want DEPOSITED and reversed", st0, rev0)
			}
			err := repo.MarkDeposited(ctx, []string{p.ID}, "lo-edge")
			if !errors.Is(err, till.ErrPaymentReversed) {
				t.Errorf("the replay on a deposited then reversed payment answered %v, want till.ErrPaymentReversed", err)
			}
			if st1, _, seq1 := r.paymentState(p.ID); st1 != st0 || seq1 != seq0 {
				t.Errorf("the refused replay moved the payment: status %s -> %s, seq %d -> %d", st0, st1, seq0, seq1)
			}
		})
	}
}

// The claim's refusal names the payments that did not move (and only them): a batch of a live and a reversed
// payment answers with the reversed one's id, not the live one's, and moves neither (the claim rolls back whole).
func TestIntegration_ClaimRefusalNamesOnlyThePaymentsThatDidNotMove(t *testing.T) {
	r := newLORig(t, 4)
	live := r.chequeOnce(r.invoice("NM1", 1_000_000), 10_000)
	gone := r.chequeOnce(r.invoice("NM2", 1_000_000), 10_000)
	if err := r.reverse(gone.ID); err != nil {
		t.Fatalf("reverse: %v", err)
	}
	line := r.statementLine(20_000)
	_, err := bankrec.NewRepository(r.pool).PersistMatchReach(context.Background(), bankrec.Match{
		StatementLineID: line, TargetType: bankrec.TargetPayment, TargetRefID: live.ID,
		Method: bankrec.MethodManual, AmountCents: 20_000}, []string{live.ID, gone.ID}, bankrec.AllBranches)
	if !isReversedRefusal(err, gone) {
		t.Fatalf("the batch answered %v, want the claim sentence naming payment %s", err, gone.PaymentNo)
	}
	if strings.Contains(strings.ToLower(err.Error()), strings.ToLower(live.ID)) ||
		strings.Contains(strings.ToLower(err.Error()), strings.ToLower(live.PaymentNo)) {
		t.Errorf("the refusal names the live payment %s too: %v", live.PaymentNo, err)
	}
	if !strings.Contains(err.Error(), "(claimed 1 of 2)") {
		t.Errorf("the refusal's count is wrong: %v", err)
	}
	if st, _, _ := r.paymentState(live.ID); st != "RECEIVED" {
		t.Errorf("the live payment of a refused batch reads %s, want RECEIVED (the claim rolls back whole)", st)
	}
	if st, n := r.lineState(line); st != "UNMATCHED" || n != 0 {
		t.Errorf("the statement line is %s with %d matches, want UNMATCHED with none", st, n)
	}
}
