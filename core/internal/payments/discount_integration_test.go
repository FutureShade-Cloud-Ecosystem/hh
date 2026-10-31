//go:build integration

// discount_integration_test.go, TM-2: the early payment discount at cash application.
//
// A payment applied to an invoice on or before its discount date, house day, takes the discount:
// the invoice's own terms percent of the money the application lands, in integer cents rounded
// half to even, posted DR discounts allowed / CR 1100 beside the receipt and inside the same
// transaction. The window and the percent both come from the invoice itself (its stored discount
// date and the terms code it carries), never from the customer's terms of today.
//
// Every leg witnesses the LEDGER (balances, allocation rows, the subledger, the outbox), not the
// walk's return value, so a green leg says the books moved, not that a struct was filled.
//
// Run: go test -tags integration ./internal/payments/ -run Discount
package payments

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customer"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
)

// discountTermsRead binds the customer module's real terms master onto the payments discount seam,
// the same binding production's mounts give the repository, so the legs prove the real read and
// not a double. The read rides the caller's transaction exactly as the seam's contract says.
type discountTermsRead struct{ svc *customer.Service }

func (d discountTermsRead) DiscountTermsByCode(ctx context.Context, tx pgx.Tx, code string) (DiscountTerms, error) {
	t, err := d.svc.TermsByCode(customer.WithCallerTx(ctx, tx), code)
	if err != nil {
		return DiscountTerms{}, err
	}
	return DiscountTerms{DiscountBps: t.DiscountBps}, nil
}

// bindTerms wires the repository the way the composition root wires it: the discount's terms read
// on the customer module's master.
func (w *applyWorld) bindTerms() *applyWorld {
	w.repo = w.repo.WithDiscountTerms(discountTermsRead{svc: customer.NewService(customer.NewRepository(w.pool))})
	return w
}

// houseToday is the dealer's own day the window is judged on.
func houseToday() string { return houseday.DefaultZone().Today(time.Now) }

// dayShift answers the ISO day a whole calendar days from today, house calendar.
func dayShift(days int) string {
	return time.Now().In(houseday.DefaultZone().Location()).AddDate(0, 0, days).Format("2006-01-02")
}

// discountInvoice plants one open invoice the writer would have raised on 2/10 net 30: dated ten
// house days before its discount day, due twenty after it, carrying the terms code and the
// discount day the terms gave it.
func (w *applyWorld) discountInvoice(t *testing.T, customerID, label string, total int64, discountDay string) string {
	t.Helper()
	var id string
	if err := w.pool.QueryRow(context.Background(),
		`INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                       discount_date, terms_code,
		                       subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, doc_type)
		 VALUES ($1, $2::uuid, $3::uuid, 'FINALIZED', $4::date, $5::date, $6::date, '2-10-NET30',
		         $7, 0, $7, $7, $4, 'INVOICE')
		 RETURNING id::text`,
		w.tag+"-"+label, customerID, dib510Trenton,
		dayOf(discountDay, -10), dayOf(discountDay, 20), discountDay, total).Scan(&id); err != nil {
		t.Fatalf("seed discount invoice %s: %v", label, err)
	}
	return id
}

// dayOf shifts an ISO day by whole calendar days.
func dayOf(day string, days int) string {
	d, err := time.Parse("2006-01-02", day)
	if err != nil {
		return day
	}
	return d.AddDate(0, 0, days).Format("2006-01-02")
}

// discountRows witnesses the discount's own ledger rows for one invoice: the allocation's discount
// column, the subledger ADJUSTMENT row and the payment.discount.taken outbox row's cents.
func (w *applyWorld) discountRows(t *testing.T, invoiceID string) (allocDiscount, subAdjust, eventCents int64) {
	t.Helper()
	ctx := context.Background()
	if err := w.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(discount_cents), 0) FROM payment_allocations WHERE invoice_id = $1::uuid`,
		invoiceID).Scan(&allocDiscount); err != nil {
		t.Fatalf("read the allocation discount: %v", err)
	}
	if err := w.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(amount_cents), 0) FROM customer_transactions
		  WHERE source_type = 'invoice' AND source_ref_id = $1::uuid AND txn_type = 'ADJUSTMENT'`,
		invoiceID).Scan(&subAdjust); err != nil {
		t.Fatalf("read the discount subledger row: %v", err)
	}
	var payload []byte
	if err := w.pool.QueryRow(ctx,
		`SELECT payload FROM events_outbox WHERE topic = 'payment.discount.taken'
		  AND payload->>'invoice_id' = $1 LIMIT 1`, invoiceID).Scan(&payload); err == nil {
		var ev struct {
			DiscountCents int64 `json:"discount_cents"`
		}
		if json.Unmarshal(payload, &ev) == nil {
			eventCents = ev.DiscountCents
		}
	}
	return allocDiscount, subAdjust, eventCents
}

// TestApplyReceived_InsideTheWindowTheDiscountIsTaken is item 1's own leg: a payment that lands on
// the discount date itself, at the payoff the terms price, closes the invoice with the discount,
// and the discount is posted beside the receipt in the same transaction. The total is chosen so
// two percent of it is a half cent: 100125 at 200 bps is 2002.5, and half to even answers 2002,
// not 2003.
func TestApplyReceived_InsideTheWindowTheDiscountIsTaken(t *testing.T) {
	w := newApplyWorld(t).bindTerms()
	ctx := context.Background()
	total := int64(100125) // two percent is 2002.5 cents, half to even keeps it 2002
	inv := w.discountInvoice(t, w.customerID, "INWIN", total, houseToday())

	ans, err := w.repo.ApplyReceived(ctx, ApplyRequest{
		CustomerID: w.customerID, Method: "CHEQUE", Reference: "CHQ 801",
		IdempotencyKey: w.tag + "-in", InvoiceIDs: []string{inv}, AmountCents: total - 2002,
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if ans.AppliedCents != total-2002 {
		t.Fatalf("the whole cheque is applied: %+v", ans.AppliedCents)
	}
	balance, status := w.invoiceState(t, inv)
	if balance != 0 || status != "PAID" {
		t.Fatalf("the invoice closes at the discounted payoff: balance %d status %s", balance, status)
	}
	allocDiscount, subAdjust, eventCents := w.discountRows(t, inv)
	if allocDiscount != 2002 || subAdjust != -2002 || eventCents != 2002 {
		t.Fatalf("the discount is 2002 everywhere (half to even on 2002.5): allocation %d subledger %d event %d",
			allocDiscount, subAdjust, eventCents)
	}
}

// TestApplyReceived_PartialInsideTheWindowDiscountsTheMoneyApplied is item 3's partial choice, on
// the ledger: a partial payment inside the window takes the terms percent of the money it applied,
// and the balance falls by the money and the discount together.
func TestApplyReceived_PartialInsideTheWindowDiscountsTheMoneyApplied(t *testing.T) {
	w := newApplyWorld(t).bindTerms()
	ctx := context.Background()
	inv := w.discountInvoice(t, w.customerID, "INPART", 100000, houseToday())

	if _, err := w.repo.ApplyReceived(ctx, ApplyRequest{
		CustomerID: w.customerID, Method: "CHEQUE", Reference: "CHQ 802",
		IdempotencyKey: w.tag + "-part", InvoiceIDs: []string{inv}, AmountCents: 40000,
	}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	balance, status := w.invoiceState(t, inv)
	if balance != 59200 || status != "PARTIALLY_PAID" {
		t.Fatalf("a 40000 payment on 2/10 leaves 100000 - 40000 - 800 = 59200, got balance %d status %s", balance, status)
	}
	allocDiscount, subAdjust, eventCents := w.discountRows(t, inv)
	if allocDiscount != 800 || subAdjust != -800 || eventCents != 800 {
		t.Fatalf("the partial discount is two percent of the money applied: allocation %d subledger %d event %d",
			allocDiscount, subAdjust, eventCents)
	}
}

// TestApplyReceived_AfterTheWindowNoDiscount is the day late leg: the window closed yesterday, and
// the same money applies at face, no discount row anywhere.
func TestApplyReceived_AfterTheWindowNoDiscount(t *testing.T) {
	w := newApplyWorld(t).bindTerms()
	ctx := context.Background()
	inv := w.discountInvoice(t, w.customerID, "INLATE", 100000, dayShift(-1))

	if _, err := w.repo.ApplyReceived(ctx, ApplyRequest{
		CustomerID: w.customerID, Method: "CHEQUE", Reference: "CHQ 803",
		IdempotencyKey: w.tag + "-late", InvoiceIDs: []string{inv}, AmountCents: 100000,
	}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	balance, status := w.invoiceState(t, inv)
	if balance != 0 || status != "PAID" {
		t.Fatalf("the full payment closes the invoice at face: balance %d status %s", balance, status)
	}
	allocDiscount, subAdjust, eventCents := w.discountRows(t, inv)
	if allocDiscount != 0 || subAdjust != 0 || eventCents != 0 {
		t.Fatalf("a payment after the window takes no discount: allocation %d subledger %d event %d",
			allocDiscount, subAdjust, eventCents)
	}
}
