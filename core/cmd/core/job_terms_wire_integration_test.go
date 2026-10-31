//go:build integration

// job_terms_wire_integration_test.go, the job's terms date the counter's invoice (wave 3 JA-2,
// item 3), inside the caller's one transaction on a pool of one connection.
//
//	Run: DATABASE_URL='postgres://.../scratch?sslmode=disable' \
//	     go test -tags integration ./cmd/core/ -run TestIntegration_JobDocuments_TheOrdersJob -count=1
package main

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/quote"
)

// TestIntegration_JobDocuments_TheOrdersJobReachesItsInvoiceOnAPoolOfOne: the counter's one
// action (convert, raise the agreed invoice, take the payment) runs on a pool of ONE connection.
// The order carries the quote's job, the invoice carries the order's job, and the job's own seven
// days date the invoice on the house day: the terms read rides the action's own transaction, the
// QD5 shape. A job terms read that went to the pool here would wait for a second connection the
// pool cannot give and the action would never finish.
func TestIntegration_JobDocuments_TheOrdersJobReachesItsInvoiceOnAPoolOfOne(t *testing.T) {
	setup := ja2Setup(t, 0)
	tag := fmt.Sprintf("JA2-I-%d", time.Now().UnixNano()%1_000_000_000)
	// The cash customer (no credit line) converts and pays in the one action.
	cust := setup.plantCustomer(0, tag)
	seven := 7
	job := setup.plantJob(cust, qd5Trenton, "J-I-TERMS", &seven, false)
	q := setup.seedJobQuote(cust, job.JobID, tag)

	e := ja2Setup(t, 1)
	ctx := e.counter()
	out, err := e.svc.TakeOrderPayment(ctx, q, quote.OrderPaymentInput{
		Method: "CASH", AmountCents: setup.quoteTotal(q), IdempotencyKey: "ja2-job-" + tag,
	})
	if err != nil {
		t.Fatalf("take the payment on the job's order: %v", err)
	}
	if got := e.documentJob("orders", out.OrderID); got != job.JobID {
		t.Fatalf("the order does not carry the quote's job: got %q", got)
	}
	if got := e.documentJob("invoices", out.InvoiceID); got != job.JobID {
		t.Fatalf("the invoice does not carry the order's job: got %q", got)
	}
	var days int
	if err := e.pool.QueryRow(context.Background(),
		`SELECT due_date - issue_date FROM invoices WHERE id = $1::uuid`, out.InvoiceID).Scan(&days); err != nil {
		t.Fatalf("read the invoice's dates: %v", err)
	}
	if days != 7 {
		t.Fatalf("the invoice is due %d days out, want the job's own seven", days)
	}
}
