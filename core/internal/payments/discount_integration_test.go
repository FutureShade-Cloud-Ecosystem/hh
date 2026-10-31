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
	"strings"
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

// TestApplyReceived_LateDiscountIsRefusedForAClerk is item 2's refusal: a clerk asks for the
// discount after the window closed, and the whole application is refused in words, nothing
// applied, the key unspent.
func TestApplyReceived_LateDiscountIsRefusedForAClerk(t *testing.T) {
	w := newApplyWorld(t).bindTerms()
	ctx := context.Background()
	inv := w.discountInvoice(t, w.customerID, "LATE1", 100000, dayShift(-1))

	_, err := w.repo.ApplyReceived(ctx, ApplyRequest{
		CustomerID: w.customerID, Method: "CHEQUE", Reference: "CHQ 810",
		IdempotencyKey: w.tag + "-clerk", InvoiceIDs: []string{inv}, AmountCents: 98000,
		DiscountApproval: &DiscountApproval{ApprovedBy: "", Reason: "customer phoned", Manager: false},
	})
	if err == nil {
		t.Fatal("a clerk's late discount must be refused in words")
	}
	msg := err.Error()
	if !strings.Contains(msg, "window closed") || !strings.Contains(msg, "manager") {
		t.Fatalf("the refusal names the closed window and asks for a manager, got %q", msg)
	}
	// Nothing applied: the invoice stands open at face and the key is unspent.
	if balance, status := w.invoiceState(t, inv); balance != 100000 || status != "FINALIZED" {
		t.Fatalf("a refused application leaves everything untouched: balance %d status %s", balance, status)
	}
	var n int
	if err := w.pool.QueryRow(ctx, `SELECT COUNT(*) FROM payments WHERE customer_id = $1::uuid`, w.customerID).Scan(&n); err != nil {
		t.Fatalf("count payments: %v", err)
	}
	if n != 0 {
		t.Fatalf("a refused application writes no payment row, found %d", n)
	}
}

// TestApplyReceived_LateDiscountWithAManagerApprovalPosts is item 2's approval: a manager's words
// take the discount outside the window, and the approval is RECORDED: who, when, the invoice, the
// amount and the reason.
func TestApplyReceived_LateDiscountWithAManagerApprovalPosts(t *testing.T) {
	w := newApplyWorld(t).bindTerms()
	ctx := context.Background()
	inv := w.discountInvoice(t, w.customerID, "LATE2", 100000, dayShift(-1))
	approver := seedDiscountApprover(t, w)

	_, err := w.repo.ApplyReceived(ctx, ApplyRequest{
		CustomerID: w.customerID, Method: "CHEQUE", Reference: "CHQ 811",
		IdempotencyKey: w.tag + "-mgr", InvoiceIDs: []string{inv}, AmountCents: 98000,
		DiscountApproval: &DiscountApproval{ApprovedBy: approver, Reason: "cheque mailed inside the window", Manager: true},
	})
	if err != nil {
		t.Fatalf("a manager's approved late discount posts: %v", err)
	}
	if balance, status := w.invoiceState(t, inv); balance != 0 || status != "PAID" {
		t.Fatalf("the approved discount closes the invoice: balance %d status %s", balance, status)
	}
	var who, reason string
	var when time.Time
	var amount int64
	var invID string
	if err := w.pool.QueryRow(ctx, `
		SELECT approved_by::text, approved_at, invoice_id::text, discount_cents, reason
		  FROM invoice_discount_approvals
		 WHERE invoice_id = $1::uuid`, inv).Scan(&who, &when, &invID, &amount, &reason); err != nil {
		t.Fatalf("the approval is recorded: %v", err)
	}
	if who != approver || reason != "cheque mailed inside the window" || amount != 2000 || invID != inv || when.IsZero() {
		t.Fatalf("the approval row carries who %q when %v the invoice %s the amount %d and the reason %q",
			who, when, invID, amount, reason)
	}
	allocDiscount, subAdjust, eventCents := w.discountRows(t, inv)
	if allocDiscount != 2000 || subAdjust != -2000 || eventCents != 2000 {
		t.Fatalf("the approved discount is posted beside the receipt: allocation %d subledger %d event %d",
			allocDiscount, subAdjust, eventCents)
	}
}

// seedDiscountApprover plants one staff user to be the approving manager.
func seedDiscountApprover(t *testing.T, w *applyWorld) string {
	t.Helper()
	var id string
	if err := w.pool.QueryRow(context.Background(),
		`INSERT INTO users (email, display_name, status)
		 VALUES ($1, 'TM2 Approver', 'ACTIVE') RETURNING id::text`,
		w.tag+"-approver@example.test").Scan(&id); err != nil {
		t.Fatalf("seed the approver: %v", err)
	}
	t.Cleanup(func() {
		_, _ = w.pool.Exec(context.Background(),
			`DELETE FROM invoice_discount_approvals WHERE approved_by = $1::uuid`, id)
		_, _ = w.pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1::uuid`, id)
	})
	return id
}
