//go:build integration

// reversal_integration_test.go, DIB-170. The ruling is "never delete; reversing-entry semantics
// always", so the tests are written as the questions an auditor would ask a year later.
package payments

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestIntegration_Reversal_PostsAReversingEntryAndNeverDeletes is the ruling, executable.
func TestIntegration_Reversal_PostsAReversingEntryAndNeverDeletes(t *testing.T) {
	ctx := context.Background()
	rig := newPayRig(t, NewStubGateway())
	cleanupIntents(t, rig)

	pay, err := rig.svc.Pay(ctx, rig.customerID, PayRequest{
		InvoiceIDs: []string{rig.invoiceA, rig.invoiceB}, PaymentMethodID: rig.methodID,
	})
	if err != nil {
		t.Fatalf("seed payment: %v", err)
	}
	// Both invoices settled to zero before the undo.
	for _, inv := range []string{rig.invoiceA, rig.invoiceB} {
		var bal int64
		var st string
		_ = rig.pool.QueryRow(ctx, `SELECT balance_cents, status FROM invoices WHERE id=$1::uuid`, inv).Scan(&bal, &st)
		if bal != 0 || st != "PAID" {
			t.Fatalf("precondition: invoice %s is %s/%d, want PAID/0", inv, st, bal)
		}
	}

	repo := NewRepository(rig.pool)
	svc := NewReverseService(repo)
	rev, err := svc.Reverse(ctx, ReverseInput{
		PaymentID: pay.ID, Reason: "rung onto the wrong account", ActorID: "", IsManager: true,
	})
	if err != nil {
		t.Fatalf("reverse: %v", err)
	}

	// - NEVER DELETED -
	// The original is present, untouched in amount, and now stamped with who undid it.
	var origAmount int64
	var reversedAt *time.Time
	var reversedBy, reason string
	if err := rig.pool.QueryRow(ctx,
		`SELECT amount_cents, reversed_at, COALESCE(reversed_by_payment_id::text,''), reversal_reason
		   FROM payments WHERE id = $1::uuid`, pay.ID).
		Scan(&origAmount, &reversedAt, &reversedBy, &reason); err != nil {
		t.Fatalf("the ORIGINAL payment row must still exist after an undo: %v", err)
	}
	if origAmount != 55000 {
		t.Fatalf("the original's amount changed to %d, a reversal must never edit the original", origAmount)
	}
	if reversedAt == nil || reversedBy != rev.ReversalPaymentID {
		t.Fatalf("the original is not stamped with its reversal: at=%v by=%q", reversedAt, reversedBy)
	}

	// - THE REVERSING ENTRY -
	var revAmount int64
	var revStatus, reverses, revReason string
	if err := rig.pool.QueryRow(ctx,
		`SELECT amount_cents, status, COALESCE(reverses_payment_id::text,''), reversal_reason
		   FROM payments WHERE id = $1::uuid`, rev.ReversalPaymentID).
		Scan(&revAmount, &revStatus, &reverses, &revReason); err != nil {
		t.Fatalf("read the reversing entry: %v", err)
	}
	if reverses != pay.ID {
		t.Fatalf("the reversing entry points at %q, want the original %s", reverses, pay.ID)
	}
	if revAmount != 55000 || revStatus != "VOID" {
		t.Fatalf("reversing entry = %d/%s, want 55000/VOID (positive amount; direction is carried by "+
			"reverses_payment_id, not by a sign on a column that cannot be negative)", revAmount, revStatus)
	}
	if revReason != "rung onto the wrong account" {
		t.Fatalf("reversal reason = %q, want the supplied reason", revReason)
	}

	// - THE INVOICES RE-OPEN -
	for inv, want := range map[string]int64{rig.invoiceA: 40000, rig.invoiceB: 15000} {
		var bal int64
		var st string
		if err := rig.pool.QueryRow(ctx, `SELECT balance_cents, status FROM invoices WHERE id=$1::uuid`, inv).
			Scan(&bal, &st); err != nil {
			t.Fatalf("read invoice: %v", err)
		}
		if bal != want || st != "FINALIZED" {
			t.Fatalf("invoice %s after the undo = %s/%d, want FINALIZED/%d", inv, st, bal, want)
		}
	}

	// - THE SUBLEDGER NETS TO ZERO -
	// Four rows, not two: the two originals stay, and two contra rows are added. That is what makes
	// the history readable, a customer's statement shows the payment AND its reversal.
	var nTxn int
	var netCents int64
	if err := rig.pool.QueryRow(ctx,
		`SELECT count(*), COALESCE(SUM(amount_cents),0) FROM customer_transactions
		  WHERE customer_id = $1::uuid AND txn_type = 'PAYMENT'`, rig.customerID).Scan(&nTxn, &netCents); err != nil {
		t.Fatalf("read subledger: %v", err)
	}
	if nTxn != 4 {
		t.Fatalf("AR PAYMENT rows = %d, want 4 (two originals kept + two contras added)", nTxn)
	}
	if netCents != 0 {
		t.Fatalf("the subledger nets to %d, want 0, a reversal that does not net to zero has moved money", netCents)
	}

	// - THE GL HAND-OFF -
	var nEvent int
	if err := rig.pool.QueryRow(ctx,
		`SELECT count(*) FROM events_outbox WHERE topic='payment.reversed' AND payload->>'reverses_payment_id' = $1`,
		pay.ID).Scan(&nEvent); err != nil {
		t.Fatalf("read outbox: %v", err)
	}
	if nEvent != 1 {
		t.Fatalf("payment.reversed events = %d, want exactly 1 in the same transaction as the reversal", nEvent)
	}

	// Same-day → the operator's word is VOID, and it is DERIVED, not a second kind of record.
	if !rev.SameDay || rev.Label() != "VOID" {
		t.Fatalf("a same-day reversal must be labelled VOID: sameDay=%v label=%q", rev.SameDay, rev.Label())
	}

	t.Cleanup(func() {
		c := context.Background()
		_, _ = rig.pool.Exec(c, `DELETE FROM events_outbox WHERE payload->>'reverses_payment_id' = $1`, pay.ID)
		_, _ = rig.pool.Exec(c, `UPDATE payments SET reversed_by_payment_id = NULL WHERE id = $1::uuid`, pay.ID)
	})
}

// TestIntegration_Reversal_OnlyOnce is the race that costs real money: two operators clicking undo at
// the same moment. The row lock closes it; the unique index is the backstop.
func TestIntegration_Reversal_OnlyOnce(t *testing.T) {
	ctx := context.Background()
	rig := newPayRig(t, NewStubGateway())
	cleanupIntents(t, rig)

	pay, err := rig.svc.Pay(ctx, rig.customerID, PayRequest{
		InvoiceIDs: []string{rig.invoiceA}, PaymentMethodID: rig.methodID,
	})
	if err != nil {
		t.Fatalf("seed payment: %v", err)
	}
	svc := NewReverseService(NewRepository(rig.pool))

	rev, err := svc.Reverse(ctx, ReverseInput{PaymentID: pay.ID, Reason: "first", IsManager: true})
	if err != nil {
		t.Fatalf("first reversal: %v", err)
	}
	if _, err := svc.Reverse(ctx, ReverseInput{PaymentID: pay.ID, Reason: "second", IsManager: true}); !errors.Is(err, ErrAlreadyReversed) {
		t.Fatalf("a second reversal returned %v, want ErrAlreadyReversed, two reversals move the customer's "+
			"balance the wrong way by the payment amount", err)
	}
	// And the invoice was given back exactly once.
	var bal int64
	_ = rig.pool.QueryRow(ctx, `SELECT balance_cents FROM invoices WHERE id=$1::uuid`, rig.invoiceA).Scan(&bal)
	if bal != 40000 {
		t.Fatalf("invoice balance = %d, want 40000, a double reversal would have restored it twice", bal)
	}
	// Reversing a reversal is refused: that is a re-application, not an undo.
	if _, err := svc.Reverse(ctx, ReverseInput{PaymentID: rev.ReversalPaymentID, Reason: "x", IsManager: true}); !errors.Is(err, ErrNotReversible) {
		t.Fatalf("reversing a reversal returned %v, want ErrNotReversible", err)
	}

	t.Cleanup(func() {
		c := context.Background()
		_, _ = rig.pool.Exec(c, `DELETE FROM events_outbox WHERE payload->>'reverses_payment_id' = $1`, pay.ID)
		_, _ = rig.pool.Exec(c, `UPDATE payments SET reversed_by_payment_id = NULL WHERE id = $1::uuid`, pay.ID)
	})
}

// TestIntegration_Reversal_GatesAndReason pins the two rules that are policy rather than mechanism.
func TestIntegration_Reversal_GatesAndReason(t *testing.T) {
	ctx := context.Background()
	rig := newPayRig(t, NewStubGateway())
	cleanupIntents(t, rig)

	pay, err := rig.svc.Pay(ctx, rig.customerID, PayRequest{
		InvoiceIDs: []string{rig.invoiceB}, PaymentMethodID: rig.methodID,
	})
	if err != nil {
		t.Fatalf("seed payment: %v", err)
	}
	svc := NewReverseService(NewRepository(rig.pool))

	if _, err := svc.Reverse(ctx, ReverseInput{PaymentID: pay.ID, Reason: "oops", IsManager: false}); err == nil {
		t.Fatal("a non-manager reversed a payment, DIB-170 is permission-gated")
	}
	if _, err := svc.Reverse(ctx, ReverseInput{PaymentID: pay.ID, Reason: "", IsManager: true}); !errors.Is(err, ErrReversalReasonRequired) {
		t.Fatal("a reversal with no reason was accepted, the reason is what makes the audit trail worth having")
	}
	// Neither refusal may have written anything.
	var n int
	_ = rig.pool.QueryRow(ctx, `SELECT count(*) FROM payments WHERE reverses_payment_id = $1::uuid`, pay.ID).Scan(&n)
	if n != 0 {
		t.Fatalf("a refused reversal wrote %d row(s)", n)
	}
	var bal int64
	_ = rig.pool.QueryRow(ctx, `SELECT balance_cents FROM invoices WHERE id=$1::uuid`, rig.invoiceB).Scan(&bal)
	if bal != 0 {
		t.Fatalf("a refused reversal re-opened the invoice (balance %d)", bal)
	}
}

// TestIntegration_Reversal_LaterDayIsLabelledAReversalNotAVoid, the same-day sugar must actually be
// same-day. A reversal posted in a later period is a REVERSAL, and calling it a void on a statement
// would misdescribe which period the money moved in.
func TestIntegration_Reversal_LaterDayIsLabelledAReversalNotAVoid(t *testing.T) {
	ctx := context.Background()
	rig := newPayRig(t, NewStubGateway())
	cleanupIntents(t, rig)

	pay, err := rig.svc.Pay(ctx, rig.customerID, PayRequest{
		InvoiceIDs: []string{rig.invoiceA}, PaymentMethodID: rig.methodID,
	})
	if err != nil {
		t.Fatalf("seed payment: %v", err)
	}
	// A clock three days on: same mechanism, different word.
	future := time.Now().Add(72 * time.Hour)
	svc := NewReverseService(NewRepository(rig.pool)).WithClock(func() time.Time { return future })

	rev, err := svc.Reverse(ctx, ReverseInput{PaymentID: pay.ID, Reason: "cheque bounced at the bank", IsManager: true})
	if err != nil {
		t.Fatalf("reverse: %v", err)
	}
	if rev.SameDay || rev.Label() != "REVERSAL" {
		t.Fatalf("a reversal three days later is labelled %q (sameDay=%v), want REVERSAL", rev.Label(), rev.SameDay)
	}
	// The rows are identical in kind to the same-day case, only the label differs.
	var reverses string
	if err := rig.pool.QueryRow(ctx, `SELECT COALESCE(reverses_payment_id::text,'') FROM payments WHERE id=$1::uuid`,
		rev.ReversalPaymentID).Scan(&reverses); err != nil || reverses != pay.ID {
		t.Fatalf("the later-day reversal must use the SAME mechanism: reverses=%q err=%v", reverses, err)
	}

	t.Cleanup(func() {
		c := context.Background()
		_, _ = rig.pool.Exec(c, `DELETE FROM events_outbox WHERE payload->>'reverses_payment_id' = $1`, pay.ID)
		_, _ = rig.pool.Exec(c, `UPDATE payments SET reversed_by_payment_id = NULL WHERE id = $1::uuid`, pay.ID)
	})
}
