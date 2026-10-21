//go:build integration

// reversal_bounce_exclusion_integration_test.go: a payment is reversed or bounced, never both (SF-2d round 7).
//
// ReversePayment refused only VOID, a reversed payment and a reversing row, and MarkNSF refused only BOUNCED, so
// each order was allowed: bounced then reversed booked Dr AR / Cr 2310 for money that bounced, and reversed then
// bounced reopened the invoice a second time, above its total. Both run under the payment row lock (FOR UPDATE), so
// the refusal is decided in the locked read.
package payments_test

import (
	"context"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
)

// excl is what the case wrote around one payment: its reversing rows, the customer's subledger rows, the invoice
// balance and the reversal and bounce outbox rows that name it.
type excl struct {
	reversingRows, subledgerRows, outbox int
	balance                              int64
	status                               string
}

func (w *reversalGLWorld) excl(orig, invoice string) excl {
	w.r.t.Helper()
	var e excl
	c := context.Background()
	_ = w.r.obs.QueryRow(c, `SELECT count(*) FROM payments WHERE reverses_payment_id = $1::uuid`, orig).Scan(&e.reversingRows)
	_ = w.r.obs.QueryRow(c, `SELECT count(*) FROM customer_transactions WHERE customer_id = $1::uuid`, w.r.custID).Scan(&e.subledgerRows)
	_ = w.r.obs.QueryRow(c, `SELECT count(*) FROM events_outbox WHERE topic IN ('payment.reversed', 'payment.bounced') AND payload::text LIKE '%'||$1||'%'`, orig).Scan(&e.outbox)
	_ = w.r.obs.QueryRow(c, `SELECT balance_cents FROM invoices WHERE id = $1::uuid`, invoice).Scan(&e.balance)
	_ = w.r.obs.QueryRow(c, `SELECT status FROM payments WHERE id = $1::uuid`, orig).Scan(&e.status)
	return e
}

func (w *reversalGLWorld) invoiceOf(orig string) string {
	w.r.t.Helper()
	var inv string
	if err := w.r.obs.QueryRow(context.Background(), `SELECT invoice_id::text FROM payment_allocations WHERE payment_id = $1::uuid LIMIT 1`, orig).Scan(&inv); err != nil {
		w.r.t.Fatalf("the payment's invoice: %v", err)
	}
	return inv
}

func (w *reversalGLWorld) bounce(orig string) error {
	_, err := till.NewService(till.NewRepository(w.r.pool)).MarkNSF(w.r.staffCtx(), till.NSFInput{PaymentID: orig, Reason: "exclusion leg"})
	return err
}

func (w *reversalGLWorld) reverseErr(orig string) error {
	_, err := w.r.repo.ReversePayment(context.Background(), payments.ReverseInput{
		PaymentID: orig, Reason: "exclusion leg", ActorID: w.r.userID, IsManager: true}, time.Now())
	return err
}

func TestIntegration_Exclusion_ABouncedPaymentCannotBeReversed(t *testing.T) {
	w := newReversalGLWorld(t)
	orig := w.receipt("CHEQUE", 10_000)
	inv := w.invoiceOf(orig)
	if err := w.bounce(orig); err != nil {
		t.Fatalf("the bounce: %v", err)
	}
	before := w.excl(orig, inv)
	err := w.reverseErr(orig)
	after := w.excl(orig, inv)
	t.Logf("a bounced payment, reversed: err %v | before %+v after %+v", err, before, after)
	if err == nil {
		t.Errorf("a bounced payment was reversed")
	}
	if after != before {
		t.Errorf("the refused reversal wrote: before %+v, after %+v", before, after)
	}
}

func TestIntegration_Exclusion_AReversedPaymentCannotBeBounced(t *testing.T) {
	w := newReversalGLWorld(t)
	orig := w.receipt("CHEQUE", 10_000)
	inv := w.invoiceOf(orig)
	w.reverse(orig)
	before := w.excl(orig, inv)
	var fees int
	_ = w.r.obs.QueryRow(context.Background(), `SELECT count(*) FROM customer_transactions WHERE customer_id = $1::uuid AND txn_type = 'NSF'`, w.r.custID).Scan(&fees)
	err := w.bounce(orig)
	after := w.excl(orig, inv)
	var feesAfter int
	_ = w.r.obs.QueryRow(context.Background(), `SELECT count(*) FROM customer_transactions WHERE customer_id = $1::uuid AND txn_type = 'NSF'`, w.r.custID).Scan(&feesAfter)
	t.Logf("a reversed payment, bounced: err %v | before %+v after %+v | NSF fee rows %d to %d", err, before, after, fees, feesAfter)
	if err == nil {
		t.Errorf("a reversed payment was bounced")
	}
	if after != before || feesAfter != fees {
		t.Errorf("the refused bounce wrote: before %+v, after %+v, NSF fee rows %d to %d", before, after, fees, feesAfter)
	}
}

// A refund row and a bounced row are not reversible either: only RECEIVED or DEPOSITED.
func TestIntegration_Exclusion_OnlyAReceivedOrDepositedPaymentIsReversible(t *testing.T) {
	for _, status := range []string{"REFUNDED", "BOUNCED", "VOID"} {
		t.Run(status, func(t *testing.T) {
			w := newReversalGLWorld(t)
			orig := w.receipt("CHEQUE", 5_000)
			if _, err := w.r.obs.Exec(context.Background(), `UPDATE payments SET status = $2 WHERE id = $1::uuid`, orig, status); err != nil {
				t.Fatalf("set %s: %v", status, err)
			}
			inv := w.invoiceOf(orig)
			before := w.excl(orig, inv)
			if err := w.reverseErr(orig); err == nil {
				t.Errorf("a %s payment was reversed", status)
			}
			if after := w.excl(orig, inv); after != before {
				t.Errorf("the refused reversal of a %s payment wrote: %+v to %+v", status, before, after)
			}
		})
	}
}

// A reversal beside a bounce of the same payment: both take the payment row FOR UPDATE, so exactly one wins and the
// other is refused. Either order of arrival.
func TestIntegration_Exclusion_AReversalBesideABounceOfTheSamePayment(t *testing.T) {
	for _, reversalFirst := range []bool{true, false} {
		name := "bounce first"
		if reversalFirst {
			name = "reversal first"
		}
		t.Run(name, func(t *testing.T) {
			w := newReversalGLWorld(t)
			ctx := context.Background()
			orig := w.receipt("CHEQUE", 10_000)
			inv := w.invoiceOf(orig)
			park := testkit.NewDrawPark(t, w.r.pool, "payments", 55010701)
			first, second := make(chan error, 1), make(chan error, 1)
			reverse := func(ch chan error, pool *payments.Repository) {
				_, err := pool.ReversePayment(ctx, payments.ReverseInput{PaymentID: orig, Reason: "exclusion race", ActorID: w.r.userID, IsManager: true}, time.Now())
				ch <- err
			}
			bounce := func(ch chan error, svc *till.Service) {
				_, err := svc.MarkNSF(w.r.staffCtx(), till.NSFInput{PaymentID: orig, Reason: "exclusion race"})
				ch <- err
			}
			parkedSvc := till.NewService(till.NewRepository(park.Pool))
			plainSvc := till.NewService(till.NewRepository(w.r.pool))
			if reversalFirst {
				go reverse(first, payments.NewRepository(park.Pool))
				park.WaitParked("the reversal holds the payment row at its first counter draw")
				go bounce(second, plainSvc)
			} else {
				go bounce(first, parkedSvc)
				park.WaitParked("the bounce holds the payment row at its first counter draw")
				go reverse(second, w.r.repo)
			}
			park.WaitBlocked("the second writer queues on the payment row")
			park.AnswersAllowing(map[string]chan error{"first": first, "second": second}, map[string]string{"second": "cannot be"})
			e := w.excl(orig, inv)
			t.Logf("%s: %+v", name, e)
			if reversalFirst && (e.reversingRows != 1 || e.status == "BOUNCED") {
				t.Errorf("the reversal won, but the case reads %+v", e)
			}
			if !reversalFirst && (e.reversingRows != 0 || e.status != "BOUNCED") {
				t.Errorf("the bounce won, but the case reads %+v", e)
			}
		})
	}
}
