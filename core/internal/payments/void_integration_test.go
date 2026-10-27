//go:build integration

package payments_test

// void_integration_test.go, FW1-06b: the true void's act legs, beside the journal legs in
// void_gl_integration_test.go. The void undoes a mis-keyed desk receipt as never taken: the
// allocation goes, the invoice re-opens, the mirror subledger row lands, the GL books the receipt's
// own legs mirrored, and NOTHING is held in 2310 (no reversal columns are written, so HeldFor
// stays at zero and the re-application path has nothing to see). The gates keep it apart from the
// reversal, the bounce and a later day.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
)

func TestIntegration_PaymentVoid_UndoesTheReceiptWithNo2310Hold(t *testing.T) {
	w := newReversalGLWorld(t)
	ctx := context.Background()
	orig := w.registerReceipt("CHEQUE", 10_000)
	// The invoice, read while the receipt's allocation still exists: the void deletes it.
	inv := w.invoiceOf(orig)
	w.drain()

	repo := payments.NewRepository(w.r.pool)
	v, err := repo.VoidPayment(ctx, payments.VoidInput{
		PaymentID: orig, Reason: "keyed against the wrong figures", ActorID: w.r.userID, IsManager: true,
	}, time.Now(), houseday.DefaultZone())
	if err != nil {
		t.Fatalf("void: %v", err)
	}
	if v.AmountCents != 10_000 || v.PaymentNo == "" {
		t.Fatalf("the void reads the payment's own facts: %+v", v)
	}

	// The receipt row: stamped VOID with the reason in its metadata, and the reversal's columns
	// never written.
	var status, meta string
	var reversedAt *time.Time
	var reverses any
	if err := w.r.obs.QueryRow(ctx,
		`SELECT status, metadata::text, reversed_at, reverses_payment_id FROM payments WHERE id = $1::uuid`, orig).
		Scan(&status, &meta, &reversedAt, &reverses); err != nil {
		t.Fatalf("read the voided payment: %v", err)
	}
	if status != "VOID" || reversedAt != nil || reverses != nil {
		t.Fatalf("the voided receipt reads %s reversed=%v reverses=%v, want VOID with no reversal columns", status, reversedAt, reverses)
	}
	if !strings.Contains(meta, "keyed against the wrong figures") {
		t.Fatalf("the void's reason is not on the payment row: %s", meta)
	}

	// The allocation went, and the invoice is open again at its full balance.
	var allocs int
	if err := w.r.obs.QueryRow(ctx,
		`SELECT count(*) FROM payment_allocations WHERE payment_id = $1::uuid`, orig).Scan(&allocs); err != nil {
		t.Fatalf("read allocations: %v", err)
	}
	if allocs != 0 {
		t.Fatalf("the void left %d allocation rows, want none: the receipt never happened", allocs)
	}
	var bal int64
	var invStatus string
	if err := w.r.obs.QueryRow(ctx,
		`SELECT balance_cents, status FROM invoices WHERE id = $1::uuid`, inv).Scan(&bal, &invStatus); err != nil {
		t.Fatalf("read the invoice: %v", err)
	}
	if bal != 1_000_000 || invStatus != "FINALIZED" {
		t.Fatalf("the invoice reads %s/%d after the void, want FINALIZED/1000000 (the receipt's share given back, the document open at its full balance)", invStatus, bal)
	}

	// The subledger's PAYMENT rows net to zero: the receipt's row and the mirror-of-cash row beside it.
	var net int64
	if err := w.r.obs.QueryRow(ctx,
		`SELECT COALESCE(SUM(amount_cents), 0) FROM customer_transactions
		  WHERE customer_id = $1::uuid AND txn_type = 'PAYMENT'`, w.r.custID).Scan(&net); err != nil {
		t.Fatalf("read the subledger: %v", err)
	}
	if net != 0 {
		t.Fatalf("the subledger's PAYMENT rows net %d, want 0 (the mirror row undoes the receipt's row)", net)
	}

	// No 2310 hold: the reversal's columns are unwritten, so the held figure stays at zero and the
	// re-application path has nothing to see.
	held, err := repo.HeldFor(ctx, orig, nil)
	if err != nil {
		t.Fatalf("held read: %v", err)
	}
	if held.HeldCents != 0 {
		t.Fatalf("a voided receipt holds %d cents, want 0: the money never left the building, the customer is not owed it", held.HeldCents)
	}

	// The undo journal: the receipt's own leg credited back, 2310 untouched.
	keys := []string{"payment.voided:" + orig, "payment.received:" + orig}
	w.cleanupJournals(keys, nil)
	w.drain()
	got := w.nets(keys, nil)
	for code, want := range map[string]int64{"1020": 0, "1100": 0, "2310": 0} {
		if got[code] != want {
			t.Errorf("account %s nets %d over the receipt and its void, want %d", code, got[code], want)
		}
	}

	// A second void is refused: the receipt is already undone.
	if _, err := repo.VoidPayment(ctx, payments.VoidInput{
		PaymentID: orig, Reason: "again", ActorID: w.r.userID, IsManager: true,
	}, time.Now(), houseday.DefaultZone()); !errors.Is(err, payments.ErrNotVoidable) {
		t.Fatalf("a second void answers %v, want ErrNotVoidable", err)
	}
}

func TestIntegration_PaymentVoid_RefusesTheActsItMustNotTouch(t *testing.T) {
	ctx := context.Background()
	w := newReversalGLWorld(t)

	// A reversed payment: the reversal owns the undo, the void refuses it.
	revd := w.registerReceipt("CHEQUE", 5_000)
	w.drain()
	rev, err := w.r.repo.ReversePayment(ctx, payments.ReverseInput{
		PaymentID: revd, Reason: "reversed first", ActorID: w.r.userID, IsManager: true}, time.Now())
	if err != nil {
		t.Fatalf("reverse: %v", err)
	}
	if _, err := payments.NewRepository(w.r.pool).VoidPayment(ctx, payments.VoidInput{
		PaymentID: revd, Reason: "void a reversed payment", ActorID: w.r.userID, IsManager: true,
	}, time.Now(), houseday.DefaultZone()); !errors.Is(err, payments.ErrNotVoidable) {
		t.Errorf("voiding a reversed payment answers %v, want ErrNotVoidable", err)
	}
	// The reversing row itself is not a receipt either.
	if _, err := payments.NewRepository(w.r.pool).VoidPayment(ctx, payments.VoidInput{
		PaymentID: rev.ReversalPaymentID, Reason: "void a contra row", ActorID: w.r.userID, IsManager: true,
	}, time.Now(), houseday.DefaultZone()); !errors.Is(err, payments.ErrNotVoidable) {
		t.Errorf("voiding a reversing entry answers %v, want ErrNotVoidable", err)
	}

	// A portal card payment the processor really captured (S6's P2-3): the processor has the money,
	// so the undo is a refund through the processor and a void as never received would owe the
	// customer again while the settlement lands.
	capw := newReversalGLWorld(t)
	capID := capw.registerReceipt("CARD", 5_000)
	if _, err := capw.r.obs.Exec(ctx, `UPDATE payments SET gateway_ref = 'pi_round2_capture' WHERE id = $1::uuid`, capID); err != nil {
		t.Fatalf("stamp the capture: %v", err)
	}
	_, err = payments.NewRepository(capw.r.pool).VoidPayment(ctx, payments.VoidInput{
		PaymentID: capID, Reason: "void a captured card payment", ActorID: capw.r.userID, IsManager: true,
	}, time.Now(), houseday.DefaultZone())
	if !errors.Is(err, payments.ErrNotVoidable) {
		t.Errorf("fw1-06b round 2 red: voiding a captured card payment answers %v, want ErrNotVoidable (its undo is a refund)", err)
	}

	// A deposited receipt has cleared to the bank; its undo is a reversal.
	dep := newReversalGLWorld(t)
	depID := dep.registerReceipt("CHEQUE", 5_000)
	if _, err := dep.r.obs.Exec(ctx, `UPDATE payments SET status = 'DEPOSITED' WHERE id = $1::uuid`, depID); err != nil {
		t.Fatalf("set DEPOSITED: %v", err)
	}
	if _, err := payments.NewRepository(dep.r.pool).VoidPayment(ctx, payments.VoidInput{
		PaymentID: depID, Reason: "void a deposited receipt", ActorID: dep.r.userID, IsManager: true,
	}, time.Now(), houseday.DefaultZone()); !errors.Is(err, payments.ErrNotVoidable) {
		t.Errorf("voiding a deposited receipt answers %v, want ErrNotVoidable", err)
	}

	// The same dealer day only: a receipt from yesterday is a reversal's business.
	late := newReversalGLWorld(t)
	lateID := late.registerReceipt("CHEQUE", 5_000)
	yesterday := time.Now().AddDate(0, 0, -1)
	if _, err := late.r.obs.Exec(ctx, `UPDATE payments SET received_at = $2 WHERE id = $1::uuid`, lateID, yesterday); err != nil {
		t.Fatalf("backdate: %v", err)
	}
	_, err = payments.NewRepository(late.r.pool).VoidPayment(ctx, payments.VoidInput{
		PaymentID: lateID, Reason: "void yesterday's receipt", ActorID: late.r.userID, IsManager: true,
	}, time.Now(), houseday.DefaultZone())
	if !errors.Is(err, payments.ErrNotVoidable) {
		t.Errorf("voiding yesterday's receipt answers %v, want ErrNotVoidable (the same dealer day only)", err)
	}

	// A till receipt's undo belongs to the drawer count, not to the void.
	tillw := newReversalGLWorld(t)
	tillID := tillw.registerReceipt("CHEQUE", 5_000)
	if _, err := tillw.r.obs.Exec(ctx,
		`UPDATE payments SET till_session_id = (SELECT id FROM till_sessions WHERE branch_id = $1::uuid LIMIT 1)
		  WHERE id = $2::uuid AND EXISTS (SELECT 1 FROM till_sessions WHERE branch_id = $1::uuid)`,
		tillw.r.branchID, tillID); err != nil {
		t.Fatalf("attach a till session: %v", err)
	}
	var attached bool
	_ = tillw.r.obs.QueryRow(ctx, `SELECT till_session_id IS NOT NULL FROM payments WHERE id = $1::uuid`, tillID).Scan(&attached)
	if attached {
		if _, err := payments.NewRepository(tillw.r.pool).VoidPayment(ctx, payments.VoidInput{
			PaymentID: tillID, Reason: "void a till receipt", ActorID: tillw.r.userID, IsManager: true,
		}, time.Now(), houseday.DefaultZone()); !errors.Is(err, payments.ErrNotVoidable) {
			t.Errorf("voiding a till receipt answers %v, want ErrNotVoidable (the drawer count owns that undo)", err)
		}
	}

	// The service's own gates: manager only, reason required, audited by the caller's wiring.
	svc := payments.NewVoidService(payments.NewRepository(w.r.pool)).WithClock(func() time.Time { return time.Now() })
	if _, err := svc.Void(ctx, payments.VoidInput{
		PaymentID: "00000000-0000-4000-8000-000000000000", Reason: "no manager", IsManager: false,
	}); err == nil || !strings.Contains(err.Error(), "manager") {
		t.Errorf("a non manager's void answers %v, want the manager refusal", err)
	}
	if _, err := svc.Void(ctx, payments.VoidInput{
		PaymentID: "00000000-0000-4000-8000-000000000000", IsManager: true,
	}); !errors.Is(err, payments.ErrVoidReasonRequired) {
		t.Errorf("a void with no reason answers %v, want ErrVoidReasonRequired", err)
	}
}
