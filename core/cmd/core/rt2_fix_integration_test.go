//go:build integration

package main

// rt2_fix_integration_test.go, the RT-2 fix round: the readers and doors that predate withholding
// (finance charge, write off, credit memo apply, lien sync, collections, statements) treat the holdback
// as what it is, money the customer may hold, not money past due. Each leg runs on the rt2 fixture.

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
)

// plantPayment records a payment of cents against an invoice, dated at `at` (the planted shape of the
// finance charge legs).
func (l *rt2Leg) plantPayment(invoiceID string, cents int64, at time.Time) {
	l.t.Helper()
	ctx := context.Background()
	l.n++
	var pid string
	if err := l.pool.QueryRow(ctx, `
		INSERT INTO payments (payment_no, customer_id, method, amount_cents, status, received_at, branch_id, created_at)
		VALUES ($1, $2::uuid, 'CASH', $3, 'RECEIVED', $4, $5::uuid, $4) RETURNING id::text`,
		fmt.Sprintf("RT2-PAY-%d", l.n), l.cust, cents, at, l.branch).Scan(&pid); err != nil {
		l.t.Fatalf("plant the payment: %v", err)
	}
	mustExec(l.t, ctx, l.pool, `INSERT INTO payment_allocations (payment_id, invoice_id, amount_cents, created_at) VALUES ($1::uuid, $2::uuid, $3, $4)`,
		pid, invoiceID, cents, at)
	mustExec(l.t, ctx, l.pool, `UPDATE invoices SET balance_cents = balance_cents - $2, status = 'PARTIALLY_PAID' WHERE id = $1::uuid`, invoiceID, cents)
}

// ageInvoice moves an invoice's dates back by days.
func (l *rt2Leg) ageInvoice(invoiceID string, days int) {
	l.t.Helper()
	mustExec(l.t, context.Background(), l.pool, `
		UPDATE invoices SET issue_date = issue_date - $2::int, due_date = due_date - $2::int,
		       finalized_at = finalized_at - make_interval(days => $2::int), created_at = created_at - make_interval(days => $2::int)
		 WHERE id = $1::uuid`, invoiceID, days)
}

// P1-1: a customer who paid the whole net of a 90 day old holdback invoice is charged nothing.
func TestRT2Fix_TheFinanceChargeNeverChargesHoldback(t *testing.T) {
	l := rt2Setup(t, "fc")
	l.newJob(true)
	doc := l.invoice(100_000, 13_000) // net 103,000; holdback 10,000
	l.ageInvoice(doc.InvoiceID, 150)
	l.plantPayment(doc.InvoiceID, 103_000, time.Now().AddDate(0, 0, -60))
	zone, _ := time.LoadLocation("America/Toronto")
	n := time.Now().In(zone)
	prior := time.Date(n.Year(), n.Month(), 1, 0, 0, 0, 0, zone).AddDate(0, -1, 0)
	res, err := l.writer(l.job, 100_000, 0).RunFinanceCharges(context.Background(), billing.FinanceChargeRequest{
		BranchID: l.branch, Month: prior.Format("2006-01"),
	})
	if err != nil {
		t.Fatalf("the finance charge run: %v", err)
	}
	for _, c := range res.Charges {
		if c.CustomerID == l.cust {
			t.Errorf("the customer paid the whole net and was charged: past due %d, charge %d", c.PastDueCents, c.ChargeCents)
		}
	}
}
