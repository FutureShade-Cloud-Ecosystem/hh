//go:build integration

// discount_doors_integration_test.go, TM-2 fix round, P3-2 and P3-3: one discount rule at every
// door that applies money to an invoice, and a running total of discounts that never passes the
// terms' full discount on the invoice.
//
// The doors, by name:
//   - the money desk: Repository.ApplyReceived (the apply route);
//   - the per invoice door: Repository.RecordReceipt (the register's Record Payment, through
//     billing's PaymentsReceiptWriter);
//   - the portal: Repository.Pay (the portal settle, through Service.Pay and PaySynced).
//
// Run: go test -tags integration ./internal/payments/ -run DiscountDoors
package payments

import (
	"context"
	"fmt"
	"testing"
)

// doorOutcome reads what one door left on one invoice and its payment.
func (w *applyWorld) doorOutcome(t *testing.T, invoiceID, paymentID string) (balance int64, status string, discount, paymentAmount, onAccount int64) {
	t.Helper()
	ctx := context.Background()
	balance, status = w.invoiceState(t, invoiceID)
	discount, _, _ = w.discountRows(t, invoiceID)
	if err := w.pool.QueryRow(ctx, `SELECT amount_cents FROM payments WHERE id = $1::uuid`, paymentID).Scan(&paymentAmount); err != nil {
		t.Fatalf("read the payment: %v", err)
	}
	if err := w.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(-amount_cents), 0) FROM customer_transactions
		  WHERE source_type = 'receipt_change' AND source_ref_id = $1::uuid`, paymentID).Scan(&onAccount); err != nil {
		t.Fatalf("read the change row: %v", err)
	}
	return
}

// TestDiscountDoors_FaceInsideTheWindowIsTheSameAtEveryDoor: a customer sends the invoice's face
// (100000) inside the 2/10 window. The doors keep two rules, stated plainly:
//
//   - the money desk and the per invoice door take the discount: the invoice closes at the
//     discounted payoff (98000 money, 2000 discount) and the 2000 over the payoff stays with the
//     dealer as change on account. The payment row carries the whole 100000 that arrived;
//   - the portal takes NO discount for now (TM-2 fix round 2, P2-1, the lead's choice (b)): the
//     card is charged the face, the invoice closes at face with discount 0 and no change is made.
//     The follow on is plan row TM-2b (the portal charges the discounted payoff and shows it).
func TestDiscountDoors_FaceInsideTheWindowIsTheSameAtEveryDoor(t *testing.T) {
	ctx := context.Background()
	w := newApplyWorld(t).bindTerms()

	type door struct {
		name string
		// atFace is true for the portal: it settles at face with no discount and no change.
		atFace bool
		run    func(inv, key string) (paymentID string, err error)
	}
	doors := []door{
		{"money desk (ApplyReceived)", false, func(inv, key string) (string, error) {
			ans, err := w.repo.ApplyReceived(ctx, ApplyRequest{
				CustomerID: w.customerID, Method: "CHEQUE", Reference: "CHQ D1",
				IdempotencyKey: key, InvoiceIDs: []string{inv}, AmountCents: 100000,
			})
			return ans.PaymentID, err
		}},
		{"per invoice door (RecordReceipt)", false, func(inv, key string) (string, error) {
			out, err := w.repo.RecordReceipt(ctx, ReceiptRequest{
				CustomerID: w.customerID, Method: "CHEQUE", Reference: "CHQ D2", IdempotencyKey: key,
				Allocations: []Allocation{{InvoiceID: inv, AmountCents: 100000}},
			})
			return out.Payment.ID, err
		}},
		{"portal (Pay)", true, func(inv, key string) (string, error) {
			p, err := w.repo.Pay(ctx, w.customerID, "CARD", "gw-"+key, []Allocation{{InvoiceID: inv, AmountCents: 100000}})
			return p.ID, err
		}},
	}
	for i, d := range doors {
		inv := w.discountInvoice(t, w.customerID, fmt.Sprintf("DOOR%d", i), 100000, houseToday())
		pid, err := d.run(inv, fmt.Sprintf("%s-door%d", w.tag, i))
		if err != nil {
			t.Fatalf("%s: %v", d.name, err)
		}
		bal, st, disc, amt, onAcct := w.doorOutcome(t, inv, pid)
		if d.atFace {
			if bal != 0 || st != "PAID" || disc != 0 || amt != 100000 || onAcct != 0 {
				t.Errorf("%s: want balance 0 PAID at face, discount 0, payment 100000, change 0; got balance %d %s, discount %d, payment %d, change %d",
					d.name, bal, st, disc, amt, onAcct)
			}
			continue
		}
		if bal != 0 || st != "PAID" || disc != 2000 || amt != 100000 || onAcct != 2000 {
			t.Errorf("%s: want balance 0 PAID, discount 2000, payment 100000, change 2000; got balance %d %s, discount %d, payment %d, change %d",
				d.name, bal, st, disc, amt, onAcct)
		}
	}
}

// TestDiscountDoors_ManySmallPartialsNeverPassTheTermsAmount: 75 cent partials at 2 percent round
// to 2 cents each (1.5 half to even), which is 2.67 percent. The running total is held at the
// terms' full discount on the invoice's base (200 on 10000), so after the partials and the closing
// payment the invoice is PAID and the discount total is exactly 200.
func TestDiscountDoors_ManySmallPartialsNeverPassTheTermsAmount(t *testing.T) {
	ctx := context.Background()
	w := newApplyWorld(t).bindTerms()
	inv := w.discountInvoice(t, w.customerID, "TINY", 10000, houseToday())

	for i := 0; i < 120; i++ {
		if _, err := w.repo.ApplyReceived(ctx, ApplyRequest{
			CustomerID: w.customerID, Method: "CASH", Reference: fmt.Sprintf("TINY %d", i),
			IdempotencyKey: fmt.Sprintf("%s-tiny-%d", w.tag, i), InvoiceIDs: []string{inv}, AmountCents: 75,
		}); err != nil {
			t.Fatalf("partial %d: %v", i, err)
		}
		if d, _, _ := w.discountRows(t, inv); d > 200 {
			t.Fatalf("after partial %d the discount total is %d, past the terms' 200", i, d)
		}
	}
	bal, _ := w.invoiceState(t, inv)
	if _, err := w.repo.ApplyReceived(ctx, ApplyRequest{
		CustomerID: w.customerID, Method: "CASH", Reference: "TINY CLOSE",
		IdempotencyKey: w.tag + "-tiny-close", InvoiceIDs: []string{inv}, AmountCents: bal,
	}); err != nil {
		t.Fatalf("closing payment of %d: %v", bal, err)
	}
	if bal, st := w.invoiceState(t, inv); bal != 0 || st != "PAID" {
		t.Fatalf("the invoice closes: balance %d status %s", bal, st)
	}
	var events int64
	if err := w.pool.QueryRow(ctx, `SELECT COALESCE(SUM((payload->>'discount_cents')::bigint), 0) FROM events_outbox
	   WHERE topic = 'payment.discount.taken' AND payload->>'invoice_id' = $1`, inv).Scan(&events); err != nil {
		t.Fatalf("sum the discount events: %v", err)
	}
	if d, sub, _ := w.discountRows(t, inv); d != 200 || sub != -200 || events != 200 {
		t.Fatalf("the discount total is the terms amount exactly, 200 everywhere: allocation %d subledger %d events %d", d, sub, events)
	}
}
