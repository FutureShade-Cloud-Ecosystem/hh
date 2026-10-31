//go:build integration

// discount_edges_integration_test.go, TM-2 items 3 and 4: the legs on the edges of the window, the
// retry, and the invoice whose customer's terms changed after it was issued.
//
// Run: go test -tags integration ./internal/payments/ -run DiscountEdge
package payments

import (
	"context"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
)

// houseInstant answers the instant of a clock time on a house day, in the dealer's zone.
func houseInstant(t *testing.T, day string, hour, min int) time.Time {
	t.Helper()
	loc := houseday.DefaultZone().Location()
	d, err := time.ParseInLocation("2006-01-02", day, loc)
	if err != nil {
		t.Fatalf("parse %s: %v", day, err)
	}
	return time.Date(d.Year(), d.Month(), d.Day(), hour, min, 0, 0, loc)
}

// TestApplyReceived_DiscountEdge_LastMinuteOfTheDateAndTheHouseMidnightAfter pins the clock at
// 23:30 on the discount date (the discount stands) and at half a minute past the house midnight
// that follows it (the window is closed). The 23:30 instant is already the next day in UTC, which
// is the evening guard's own shape: a UTC day would close the window early.
func TestApplyReceived_DiscountEdge_LastMinuteOfTheDateAndTheHouseMidnightAfter(t *testing.T) {
	w := newApplyWorld(t).bindTerms()
	ctx := context.Background()
	day := dayShift(3)

	inEve := w.discountInvoice(t, w.customerID, "EVE", 100000, day)
	w.repo = w.repo.WithDiscountClock(func() time.Time { return houseInstant(t, day, 23, 30) })
	if _, err := w.repo.ApplyReceived(ctx, ApplyRequest{
		CustomerID: w.customerID, Method: "CHEQUE", Reference: "CHQ E1",
		IdempotencyKey: w.tag + "-eve", InvoiceIDs: []string{inEve}, AmountCents: 98000,
	}); err != nil {
		t.Fatalf("apply at 23:30 on the discount date: %v", err)
	}
	if bal, st := w.invoiceState(t, inEve); bal != 0 || st != "PAID" {
		t.Fatalf("23:30 on the discount date still takes the discount: balance %d status %s", bal, st)
	}
	if d, _, _ := w.discountRows(t, inEve); d != 2000 {
		t.Fatalf("the discount at 23:30 is 2000, got %d", d)
	}

	afterMidnight := w.discountInvoice(t, w.customerID, "MID", 100000, day)
	w.repo = w.repo.WithDiscountClock(func() time.Time { return houseInstant(t, dayOf(day, 1), 0, 0).Add(30 * time.Second) })
	if _, err := w.repo.ApplyReceived(ctx, ApplyRequest{
		CustomerID: w.customerID, Method: "CHEQUE", Reference: "CHQ E2",
		IdempotencyKey: w.tag + "-mid", InvoiceIDs: []string{afterMidnight}, AmountCents: 98000,
	}); err != nil {
		t.Fatalf("apply after the house midnight: %v", err)
	}
	if bal, st := w.invoiceState(t, afterMidnight); bal != 2000 || st != "PARTIALLY_PAID" {
		t.Fatalf("after the house midnight 98000 pays at face and leaves 2000 open: balance %d status %s", bal, st)
	}
	if d, a, e := w.discountRows(t, afterMidnight); d != 0 || a != 0 || e != 0 {
		t.Fatalf("no discount row after the window: %d %d %d", d, a, e)
	}
}

// TestApplyReceived_DiscountEdge_ARetryDoesNotDiscountTwice replays the same key and body: the
// first result is answered, and the ledger holds one discount.
func TestApplyReceived_DiscountEdge_ARetryDoesNotDiscountTwice(t *testing.T) {
	w := newApplyWorld(t).bindTerms()
	ctx := context.Background()
	inv := w.discountInvoice(t, w.customerID, "RETRY", 100000, houseToday())
	req := ApplyRequest{
		CustomerID: w.customerID, Method: "CHEQUE", Reference: "CHQ R1",
		IdempotencyKey: w.tag + "-retry", InvoiceIDs: []string{inv}, AmountCents: 40000,
	}
	first, err := w.repo.ApplyReceived(ctx, req)
	if err != nil {
		t.Fatalf("first apply: %v", err)
	}
	again, err := w.repo.ApplyReceived(ctx, req)
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if !again.Replayed || again.PaymentID != first.PaymentID {
		t.Fatalf("the retry answers the first payment: replayed %v ids %s %s", again.Replayed, again.PaymentID, first.PaymentID)
	}
	if bal, _ := w.invoiceState(t, inv); bal != 59200 {
		t.Fatalf("one discount only: balance %d, want 59200", bal)
	}
	d, a, e := w.discountRows(t, inv)
	if d != 800 || a != -800 {
		t.Fatalf("one discount row: allocation %d subledger %d", d, a)
	}
	var events int
	if err := w.pool.QueryRow(ctx, `SELECT COUNT(*) FROM events_outbox WHERE topic = 'payment.discount.taken'
	   AND payload->>'invoice_id' = $1`, inv).Scan(&events); err != nil || events != 1 || e != 800 {
		t.Fatalf("one discount event of 800: count %d cents %d err %v", events, e, err)
	}
}

// TestApplyReceived_DiscountEdge_TheInvoicesOwnTermsNotTheCustomersToday moves the customer to
// another code after the invoice was raised: the 2/10 invoice keeps its discount, and an invoice
// raised without discount terms gets none from a customer who is now on 2/10.
func TestApplyReceived_DiscountEdge_TheInvoicesOwnTermsNotTheCustomersToday(t *testing.T) {
	w := newApplyWorld(t).bindTerms()
	ctx := context.Background()
	setTerms := func(code string) {
		t.Helper()
		if _, err := w.pool.Exec(ctx, `UPDATE customers SET terms_code = $2 WHERE id = $1::uuid`, w.customerID, code); err != nil {
			t.Fatalf("move the customer to %s: %v", code, err)
		}
	}

	disc := w.discountInvoice(t, w.customerID, "KEEP", 100000, houseToday())
	setTerms("NET45")
	if _, err := w.repo.ApplyReceived(ctx, ApplyRequest{
		CustomerID: w.customerID, Method: "CHEQUE", Reference: "CHQ T1",
		IdempotencyKey: w.tag + "-keep", InvoiceIDs: []string{disc}, AmountCents: 98000,
	}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if bal, st := w.invoiceState(t, disc); bal != 0 || st != "PAID" {
		t.Fatalf("the invoice keeps the discount its own terms gave it: balance %d status %s", bal, st)
	}

	plain := w.invoice(t, w.customerID, "PLAIN", 5, -25, 100000)
	setTerms("2-10-NET30")
	if _, err := w.repo.ApplyReceived(ctx, ApplyRequest{
		CustomerID: w.customerID, Method: "CHEQUE", Reference: "CHQ T2",
		IdempotencyKey: w.tag + "-plain", InvoiceIDs: []string{plain}, AmountCents: 98000,
	}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if bal, _ := w.invoiceState(t, plain); bal != 2000 {
		t.Fatalf("today's customer terms do not discount an old invoice: balance %d, want 2000", bal)
	}
	if d, a, e := w.discountRows(t, plain); d != 0 || a != 0 || e != 0 {
		t.Fatalf("no discount rows on the plain invoice: %d %d %d", d, a, e)
	}
}
