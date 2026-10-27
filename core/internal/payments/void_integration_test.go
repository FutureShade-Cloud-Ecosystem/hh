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

// A receipt that also settled an NSF fee: the void gives the fee back too. A void that moved 1100
// by the receipt's whole amount while the fee stayed paid in the subledger would leave the fee
// charged on a receipt that never happened (S6's P2-4, the sequencing with PR 622).
func TestIntegration_PaymentVoid_UndoesTheNSFFeeItSettled(t *testing.T) {
	w := newReversalGLWorld(t)
	ctx := context.Background()
	orig := w.registerReceipt("CHEQUE", 10_000)
	inv := w.invoiceOf(orig)
	w.drain()

	// The fee the receipt settled, and the subledger row the settle wrote for it: the fee is part of
	// the receipt's money, taken off the customer when the receipt paid it (PR 622's own row: an
	// ADJUSTMENT against the fee, negative, because it reduces what the customer owes).
	var nsfID string
	if err := w.r.obs.QueryRow(ctx,
		`INSERT INTO payment_nsf (payment_id, customer_id, reason, fee_cents, fee_taxable)
		 VALUES ($1::uuid, $2::uuid, 'cheque returned NSF', 4500, false) RETURNING id::text`,
		orig, w.r.custID).Scan(&nsfID); err != nil {
		t.Fatalf("insert the nsf fee: %v", err)
	}
	if _, err := w.r.obs.Exec(ctx,
		`UPDATE payments
		    SET metadata = metadata || jsonb_build_object('nsf_fee_allocations', jsonb_build_object($2::text, 4500::bigint))
		  WHERE id = $1::uuid`, orig, nsfID); err != nil {
		t.Fatalf("stamp the fee allocation: %v", err)
	}
	if _, err := w.r.obs.Exec(ctx,
		`INSERT INTO customer_transactions
		   (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, memo)
		 SELECT p.customer_id, 'ADJUSTMENT', -4500, 0, 'payment_nsf', n.id, p.branch_id, 'NSF fee payment'
		   FROM payment_nsf n JOIN payments p ON p.id = n.payment_id WHERE n.id = $1::uuid`, nsfID); err != nil {
		t.Fatalf("the settle's fee row: %v", err)
	}

	repo := payments.NewRepository(w.r.pool)
	if _, err := repo.VoidPayment(ctx, payments.VoidInput{
		PaymentID: orig, Reason: "keyed against the wrong figures", ActorID: w.r.userID, IsManager: true,
	}, time.Now(), houseday.DefaultZone()); err != nil {
		t.Fatalf("void: %v", err)
	}

	// The fee is open again: its own subledger group nets to zero over the settle and the void.
	var feeNet int64
	if err := w.r.obs.QueryRow(ctx,
		`SELECT COALESCE(SUM(amount_cents), 0) FROM customer_transactions
		  WHERE customer_id = $1::uuid AND source_type = 'payment_nsf' AND source_ref_id = $2::uuid`,
		w.r.custID, nsfID).Scan(&feeNet); err != nil {
		t.Fatalf("read the fee's subledger: %v", err)
	}
	if feeNet != 0 {
		t.Fatalf("the fee's subledger rows net %d, want 0 (the void gives the fee back)", feeNet)
	}
	// The invoice is open again at its full balance, and the receipt's payment rows net to zero.
	var bal int64
	var invStatus string
	if err := w.r.obs.QueryRow(ctx,
		`SELECT balance_cents, status FROM invoices WHERE id = $1::uuid`, inv).Scan(&bal, &invStatus); err != nil {
		t.Fatalf("read the invoice: %v", err)
	}
	if bal != 1_000_000 || invStatus != "FINALIZED" {
		t.Fatalf("the invoice reads %s/%d after the void, want FINALIZED/1000000", invStatus, bal)
	}
	// Nothing is held in 2310 for the voided receipt, fee or not.
	held, err := repo.HeldFor(ctx, orig, nil)
	if err != nil {
		t.Fatalf("held read: %v", err)
	}
	if held.HeldCents != 0 {
		t.Fatalf("a voided receipt holds %d cents, want 0: the money never left the building", held.HeldCents)
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

// R2-P1-2 (ruled, FROM-PRINCIPAL 174): the void moves the fee's allocation map to
// voided_nsf_fee_allocations in the same statement that stamps VOID, so 622's pay path expression
// (it counts every payment's nsf_fee_allocations except reversed, reversing and BOUNCED ones, and
// leaves VOID in) sees a voided receipt's fee no more, and the history keeps what the receipt paid.
// The leg: void a fee paying receipt, then pay the fee again; the fee the pay path counts is the
// new receipt's own part only.
func TestIntegration_PaymentVoid_TheVoidedFeeCountsPaidNoMore(t *testing.T) {
	w := newReversalGLWorld(t)
	ctx := context.Background()
	bounced := w.registerReceipt("CHEQUE", 10_000)
	w.drain()

	// The bounced cheque's fee row and its open ADJUSTMENT on the customer's ledger (PR 622's shape).
	var nsfID string
	if err := w.r.obs.QueryRow(ctx,
		`INSERT INTO payment_nsf (payment_id, customer_id, reason, fee_cents, fee_taxable)
		 VALUES ($1::uuid, $2::uuid, 'cheque returned NSF', 4500, false) RETURNING id::text`,
		bounced, w.r.custID).Scan(&nsfID); err != nil {
		t.Fatalf("insert the nsf fee: %v", err)
	}
	if _, err := w.r.obs.Exec(ctx,
		`INSERT INTO customer_transactions
		   (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, memo)
		 SELECT p.customer_id, 'ADJUSTMENT', 4500, 4500, 'payment_nsf', n.id, p.branch_id, 'NSF fee'
		   FROM payment_nsf n JOIN payments p ON p.id = n.payment_id WHERE n.id = $1::uuid`, nsfID); err != nil {
		t.Fatalf("insert the nsf fee ledger row: %v", err)
	}

	repo := payments.NewRepository(w.r.pool)
	first, err := repo.ApplyReceived(ctx, payments.ApplyRequest{
		CustomerID: w.r.custID, Method: "CHEQUE", Reference: "VOID-FEE-1",
		IdempotencyKey: "void-fee-1-" + nsfID, ReceivedBy: w.r.userID,
		InvoiceIDs: []string{nsfID}, AmountCents: 4_500,
	})
	if err != nil {
		t.Fatalf("pay the fee the first time through ApplyReceived: %v", err)
	}
	w.drain()

	if _, err := repo.VoidPayment(ctx, payments.VoidInput{
		PaymentID: first.PaymentID, Reason: "keyed against the wrong figures", ActorID: w.r.userID, IsManager: true,
	}, time.Now(), houseday.DefaultZone()); err != nil {
		t.Fatalf("void: %v", err)
	}

	// The move: the voided receipt carries the fee under voided_nsf_fee_allocations, and
	// nsf_fee_allocations is gone from it.
	var meta map[string]any
	if err := w.r.obs.QueryRow(ctx, `SELECT metadata FROM payments WHERE id = $1::uuid`, first.PaymentID).Scan(&meta); err != nil {
		t.Fatalf("read the voided receipt: %v", err)
	}
	if _, ok := meta["nsf_fee_allocations"]; ok {
		t.Fatalf("the voided receipt still carries nsf_fee_allocations (%v): the pay path counts a fee that was given back", meta["nsf_fee_allocations"])
	}
	voided, ok := meta["voided_nsf_fee_allocations"].(map[string]any)
	if !ok || voided[nsfID] == nil {
		t.Fatalf("the voided receipt carries no voided_nsf_fee_allocations for the fee: what the receipt paid is lost from the history (%v)", meta["voided_nsf_fee_allocations"])
	}

	// Pay the fee again through real ApplyReceived: because VoidPayment moved the first receipt's
	// map to voided_nsf_fee_allocations, the fee is open again and ApplyReceived accepts the second
	// payment against it.
	second, err := repo.ApplyReceived(ctx, payments.ApplyRequest{
		CustomerID: w.r.custID, Method: "CASH", Reference: "VOID-FEE-2",
		IdempotencyKey: "void-fee-2-" + nsfID, ReceivedBy: w.r.userID,
		InvoiceIDs: []string{nsfID}, AmountCents: 4_500,
	})
	if err != nil {
		t.Fatalf("pay the fee again through ApplyReceived after voiding the first receipt: %v", err)
	}
	if second.AppliedCents != 4_500 {
		t.Fatalf("second ApplyReceived applied %d, want 4500", second.AppliedCents)
	}
	w.drain()

	// The fee the pay path counts (622's own expression: every payment's map except reversed,
	// reversing and BOUNCED) is the second receipt's part only.
	var paid int64
	if err := w.r.obs.QueryRow(ctx,
		`SELECT COALESCE(SUM((p.metadata->'nsf_fee_allocations'->>$2::text)::bigint), 0)
		   FROM payments p
		  WHERE p.customer_id = $1::uuid
		    AND p.reversed_at IS NULL AND p.reverses_payment_id IS NULL AND p.status <> 'BOUNCED'`,
		w.r.custID, nsfID).Scan(&paid); err != nil {
		t.Fatalf("read the fee the pay path counts: %v", err)
	}
	if paid != 4_500 {
		t.Fatalf("the fee the pay path counts paid is %d, want the second receipt's 4500 only: a voided receipt's fee must not stand in the way of paying it again", paid)
	}
}
