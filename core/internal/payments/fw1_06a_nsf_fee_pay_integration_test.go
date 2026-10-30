//go:build integration

package payments

import (
	"context"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
)

type fw106aSingleOutbox struct {
	ev       notify.Event
	lastSett notify.Settlement
}

func (s *fw106aSingleOutbox) ProcessBatch(ctx context.Context, _ int, settle func(context.Context, notify.Event) notify.Settlement) (int, error) {
	s.lastSett = settle(ctx, s.ev)
	return 1, nil
}

// TestIntegration_FW1_06a_NSFFeeCanBePaid pins sub-item 3 of FW1-06a and Round 2 rulings:
//   - P2-2: an overpayment against only [invID] when an open NSF fee is not named in InvoiceIDs
//     is refused with the ceiling error; naming [invID, nsfID] settles both.
//   - P1-1 (reversal leg): reversing a payment that settled an invoice + named NSF fee restores
//     both the invoice and the NSF fee in customer_transactions so GL 1100 and the AR subledger tie.
func TestIntegration_FW1_06a_NSFFeeCanBePaid(t *testing.T) {
	w := newApplyWorld(t)
	ctx := context.Background()

	invID := w.invoice(t, w.customerID, "REOPENED", 20, 0, 50000)

	var bouncedPayID, nsfID string
	if err := w.pool.QueryRow(ctx,
		`INSERT INTO payments (payment_no, customer_id, method, amount_cents, status, branch_id)
		 VALUES ($1, $2::uuid, 'CHEQUE', 50000, 'BOUNCED', $3::uuid)
		 RETURNING id::text`,
		w.tag+"-BOUNCED-1", w.customerID, dib510Trenton).Scan(&bouncedPayID); err != nil {
		t.Fatalf("seed bounced payment: %v", err)
	}
	if err := w.pool.QueryRow(ctx,
		`INSERT INTO payment_nsf (payment_id, reason, fee_cents)
		 VALUES ($1::uuid, 'NSF test', 4500)
		 RETURNING id::text`,
		bouncedPayID).Scan(&nsfID); err != nil {
		t.Fatalf("seed payment_nsf: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = w.pool.Exec(c, `DELETE FROM gl_journal_lines WHERE journal_id IN (SELECT id FROM gl_journals WHERE source_ref_id IN (SELECT id::text FROM payments WHERE customer_id = $1::uuid))`, w.customerID)
		_, _ = w.pool.Exec(c, `DELETE FROM gl_journals WHERE source_ref_id IN (SELECT id::text FROM payments WHERE customer_id = $1::uuid)`, w.customerID)
		_, _ = w.pool.Exec(c, `DELETE FROM events_outbox WHERE payload->>'payment_id' IN (SELECT id::text FROM payments WHERE customer_id = $1::uuid) OR payload->>'reverses_payment_id' IN (SELECT id::text FROM payments WHERE customer_id = $1::uuid)`, w.customerID)
		_, _ = w.pool.Exec(c, `UPDATE payments SET reverses_payment_id = NULL WHERE customer_id = $1::uuid`, w.customerID)
		_, _ = w.pool.Exec(c, `DELETE FROM payment_nsf WHERE id = $1::uuid`, nsfID)
	})
	if _, err := w.pool.Exec(ctx,
		`INSERT INTO customer_transactions
		   (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, memo)
		 VALUES
		   ($1::uuid, 'INVOICE', 50000, 50000, 'invoice', $4::uuid, $3::uuid, 'reopened invoice'),
		   ($1::uuid, 'ADJUSTMENT', 4500, 54500, 'payment_nsf', $2::uuid, $3::uuid, 'NSF fee')`,
		w.customerID, nsfID, dib510Trenton, invID); err != nil {
		t.Fatalf("seed NSF fee txn: %v", err)
	}

	// P2-2: an amount exceeding the named lines never touches the unnamed NSF fee. The overage is
	// the account's on account credit (W3 item 0.10): the invoice closes on its 50000, the fee
	// stays open at its 4500, and the 4500 overpayment rests as one receipt_change row, so the fee
	// can still be paid by naming it.
	t.Run("unnamed_nsf_fee_overpayment_never_pays_the_fee", func(t *testing.T) {
		ans, err := w.repo.ApplyReceived(ctx, ApplyRequest{
			CustomerID:     w.customerID,
			InvoiceIDs:     []string{invID},
			AmountCents:    54500,
			Method:         "CHEQUE",
			Reference:      "REPL-CHQ-UNNAMED",
			IdempotencyKey: w.tag + "-apply-unnamed-fee",
		})
		if err != nil {
			t.Fatalf("ApplyReceived with 54500 against only named invoice %s (50000): %v", invID, err)
		}
		if ans.AppliedCents != 50000 || ans.UnappliedCents != 4500 {
			t.Fatalf("AppliedCents = %d UnappliedCents = %d, want 50000 and 4500 on account", ans.AppliedCents, ans.UnappliedCents)
		}
		var feeNet int64
		if err := w.pool.QueryRow(ctx,
			`SELECT COALESCE(SUM(amount_cents), 0) FROM customer_transactions
			  WHERE customer_id = $1::uuid AND source_type = 'payment_nsf' AND source_ref_id = $2::uuid`,
			w.customerID, nsfID).Scan(&feeNet); err != nil {
			t.Fatalf("read NSF fee net: %v", err)
		}
		if feeNet != 4500 {
			t.Fatalf("NSF fee net balance = %d, want 4500: an unnamed fee is never paid", feeNet)
		}
		var changeSum int64
		if err := w.pool.QueryRow(ctx,
			`SELECT COALESCE(SUM(amount_cents), 0) FROM customer_transactions
			  WHERE customer_id = $1::uuid AND source_type = 'receipt_change'`,
			w.customerID).Scan(&changeSum); err != nil {
			t.Fatalf("read the change row: %v", err)
		}
		if changeSum != -4500 {
			t.Fatalf("the overpayment rests on the account as -4500, got %d", changeSum)
		}
	})

	// Reset invoice + fee if the unfixed code mutated them during the subtest above.
	_, _ = w.pool.Exec(ctx, `UPDATE invoices SET balance_cents = 50000, status = 'FINALIZED' WHERE id = $1::uuid`, invID)
	_, _ = w.pool.Exec(ctx, `DELETE FROM payment_allocations WHERE invoice_id = $1::uuid`, invID)
	_, _ = w.pool.Exec(ctx, `DELETE FROM customer_transactions WHERE customer_id = $1::uuid AND amount_cents < 0`, w.customerID)
	_, _ = w.pool.Exec(ctx, `DELETE FROM payments WHERE customer_id = $1::uuid AND id <> $2::uuid`, w.customerID, bouncedPayID)

	// Paying the reopened invoice ($500.00) plus the named NSF fee ($45.00) in one
	// replacement cheque ($545.00) must succeed and clear both.
	applied, err := w.repo.ApplyReceived(ctx, ApplyRequest{
		CustomerID:     w.customerID,
		InvoiceIDs:     []string{invID, nsfID},
		AmountCents:    54500,
		Method:         "CHEQUE",
		Reference:      "REPL-CHQ-1",
		IdempotencyKey: w.tag + "-apply-inv-and-fee",
	})
	if err != nil {
		t.Fatalf("ApplyReceived (invoice + NSF fee): %v", err)
	}
	if applied.AppliedCents != 54500 {
		t.Fatalf("AppliedCents = %d, want 54500", applied.AppliedCents)
	}

	var invBal int64
	if err := w.pool.QueryRow(ctx, `SELECT balance_cents FROM invoices WHERE id = $1::uuid`, invID).Scan(&invBal); err != nil {
		t.Fatalf("read invoice balance: %v", err)
	}
	if invBal != 0 {
		t.Fatalf("invoice balance after payment = %d, want 0", invBal)
	}

	var feeNet int64
	if err := w.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(amount_cents), 0) FROM customer_transactions
		  WHERE customer_id = $1::uuid AND source_type = 'payment_nsf' AND source_ref_id = $2::uuid`,
		w.customerID, nsfID).Scan(&feeNet); err != nil {
		t.Fatalf("read NSF fee net: %v", err)
	}
	if feeNet != 0 {
		t.Fatalf("NSF fee net balance in customer_transactions = %d, want 0", feeNet)
	}

	// P1-1 (reversal leg): reversing the fee-paying payment must restore both the $500.00 invoice
	// and the $45.00 NSF fee in customer_transactions so GL 1100 delta (+54500) matches subledger delta (+54500).
	t.Run("reverse_reopens_nsf_fee", func(t *testing.T) {
		var userID string
		if err := w.pool.QueryRow(ctx, `SELECT id::text FROM users LIMIT 1`).Scan(&userID); err != nil {
			t.Fatalf("user: %v", err)
		}
		cctx := identity.ContextWithStaff(ctx, identity.StaffContext{
			UserID: userID,
			Tier:   identity.TierLeadership,
			Scope:  identity.ScopeGlobal,
			Status: "ACTIVE",
		})
		glSvc := gl.NewService(gl.NewRepository(w.pool))
		outbox := &fw106aSingleOutbox{}
		disp := notify.NewDispatcher(outbox, nil)
		glSvc.RegisterPostingHandlers(disp)

		loc, _ := time.LoadLocation("America/Toronto")
		now := time.Now().In(loc)
		year, month := now.Year(), int(now.Month())

		glBefore, err := glSvc.GLBalanceCents(cctx, "1100", year, month, "")
		if err != nil {
			t.Fatalf("GLBalanceCents 1100 before reversal: %v", err)
		}
		var subBefore int64
		if err := w.pool.QueryRow(ctx, `SELECT COALESCE(SUM(amount_cents), 0) FROM customer_transactions WHERE customer_id = $1::uuid`, w.customerID).Scan(&subBefore); err != nil {
			t.Fatalf("subledger before reversal: %v", err)
		}

		revOut, err := w.repo.ReversePayment(ctx, ReverseInput{
			PaymentID: applied.PaymentID,
			Reason:    "wrong customer account",
			ActorID:   userID,
		}, now)
		if err != nil {
			t.Fatalf("ReversePayment: %v", err)
		}
		var revPayload []byte
		if err := w.pool.QueryRow(ctx,
			`SELECT payload FROM events_outbox WHERE topic = 'payment.reversed' AND payload->>'payment_id' = $1 ORDER BY created_at DESC LIMIT 1`,
			revOut.ReversalPaymentID).Scan(&revPayload); err != nil {
			t.Fatalf("read payment.reversed outbox event: %v", err)
		}
		outbox.ev = notify.Event{Topic: "payment.reversed", Payload: revPayload}
		disp.DrainOnce(ctx)
		if outbox.lastSett.LastError != "" {
			t.Fatalf("deliver payment.reversed: %s", outbox.lastSett.LastError)
		}

		glAfter, err := glSvc.GLBalanceCents(cctx, "1100", year, month, "")
		if err != nil {
			t.Fatalf("GLBalanceCents 1100 after reversal: %v", err)
		}
		var subAfter, feeNetAfter int64
		if err := w.pool.QueryRow(ctx, `SELECT COALESCE(SUM(amount_cents), 0) FROM customer_transactions WHERE customer_id = $1::uuid`, w.customerID).Scan(&subAfter); err != nil {
			t.Fatalf("subledger after reversal: %v", err)
		}
		if err := w.pool.QueryRow(ctx,
			`SELECT COALESCE(SUM(amount_cents), 0) FROM customer_transactions
			  WHERE customer_id = $1::uuid AND source_type = 'payment_nsf' AND source_ref_id = $2::uuid`,
			w.customerID, nsfID).Scan(&feeNetAfter); err != nil {
			t.Fatalf("read NSF fee net after reversal: %v", err)
		}
		glDelta := glAfter - glBefore
		subDelta := subAfter - subBefore
		if glDelta != 54500 || subDelta != 54500 || feeNetAfter != 4500 {
			t.Fatalf("reversal of fee-paying payment drifted AR: GL 1100 delta = %d, subledger delta = %d (want 54500), payment_nsf group sum = %d (want 4500)",
				glDelta, subDelta, feeNetAfter)
		}
	})

	// Second case: an account whose invoice was already settled and that owes
	// only the $45.00 NSF fee can pay the fee directly by naming the NSF id.
	var bouncedPayID2, nsfID2 string
	if err := w.pool.QueryRow(ctx,
		`INSERT INTO payments (payment_no, customer_id, method, amount_cents, status, branch_id)
		 VALUES ($1, $2::uuid, 'CHEQUE', 20000, 'BOUNCED', $3::uuid)
		 RETURNING id::text`,
		w.tag+"-BOUNCED-2", w.otherID, dib510Trenton).Scan(&bouncedPayID2); err != nil {
		t.Fatalf("seed bounced payment 2: %v", err)
	}
	if err := w.pool.QueryRow(ctx,
		`INSERT INTO payment_nsf (payment_id, reason, fee_cents)
		 VALUES ($1::uuid, 'NSF test 2', 4500)
		 RETURNING id::text`,
		bouncedPayID2).Scan(&nsfID2); err != nil {
		t.Fatalf("seed payment_nsf 2: %v", err)
	}
	t.Cleanup(func() {
		_, _ = w.pool.Exec(context.Background(), `DELETE FROM payment_nsf WHERE id = $1::uuid`, nsfID2)
	})
	if _, err := w.pool.Exec(ctx,
		`INSERT INTO customer_transactions
		   (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, memo)
		 VALUES ($1::uuid, 'ADJUSTMENT', 4500, 4500, 'payment_nsf', $2::uuid, $3::uuid, 'NSF fee')`,
		w.otherID, nsfID2, dib510Trenton); err != nil {
		t.Fatalf("seed NSF fee txn 2: %v", err)
	}

	applied2, err := w.repo.ApplyReceived(ctx, ApplyRequest{
		CustomerID:     w.otherID,
		InvoiceIDs:     []string{nsfID2},
		AmountCents:    4500,
		Method:         "CASH",
		Reference:      "FEE-CASH-2",
		IdempotencyKey: w.tag + "-apply-fee-only",
	})
	if err != nil {
		t.Fatalf("ApplyReceived (NSF fee only): %v", err)
	}
	if applied2.AppliedCents != 4500 {
		t.Fatalf("AppliedCents (fee only) = %d, want 4500", applied2.AppliedCents)
	}
	var feeNet2 int64
	if err := w.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(amount_cents), 0) FROM customer_transactions
		  WHERE customer_id = $1::uuid AND source_type = 'payment_nsf' AND source_ref_id = $2::uuid`,
		w.otherID, nsfID2).Scan(&feeNet2); err != nil {
		t.Fatalf("read NSF fee 2 net: %v", err)
	}
	if feeNet2 != 0 {
		t.Fatalf("NSF fee 2 net balance in customer_transactions = %d, want 0", feeNet2)
	}
}
