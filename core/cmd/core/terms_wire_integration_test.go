//go:build integration

// terms_wire_integration_test.go, the whole road of the terms master (wave 3, TM-1).
//
// The customer module owns the master's table and reads it; the billing module dates the
// document by the terms and never touches the table. Nothing in between is a hand-off of
// numbers: the composition root binds the customer module's terms read into the billing
// writer's terms seam, and the invoice that comes out is dated by the terms the customer is
// sold on.
//
// The two ends are proved apart as well: the master and the mapping in the customer module, the
// writer's own dating in the billing module, and this one that holds the whole road together.
//
//	Run: DATABASE_URL='postgres://hardscape:hardscape@127.0.0.1:5432/hardscapeos?sslmode=disable' \
//	     go test -tags integration ./cmd/core/ -count=1 -run TestIntegration_TheInvoiceIsDatedByTheCustomersTerms
package main

import (
	"context"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customer"
)

// tm1Branch is the yard's own branch (migration 0003).
const tm1Branch = "5d1b3f6e-0000-4000-9000-000000000001"

// tm1OrderSource answers the order-facts seam with one fixed billable order, so the terms read
// and the writer's dating are the only things under test.
type tm1OrderSource struct{ customer string }

func (s tm1OrderSource) InvoiceableOrder(context.Context, string, []string) (billing.OrderInvoiceFacts, error) {
	return billing.OrderInvoiceFacts{
		CustomerID: s.customer, BranchID: tm1Branch, ClosesOrder: true, TermsDays: 30,
		Lines: []billing.OrderInvoiceLine{{
			LineNo: 1, Description: "Pavers", QtyEntered: "1", UOMEntered: "EA", QtyBase: "1",
			UnitPriceCents: 5_000, PriceUOM: "EA", LineTotalCents: 5_000,
		}},
	}, nil
}

func (tm1OrderSource) MarkGroupsInvoiced(context.Context, []string, string, time.Time) error {
	return nil
}

// TestIntegration_TheInvoiceIsDatedByTheCustomersTerms holds the whole road: a customer put on
// 2/10 net 30 in the customer module, an invoice raised for that customer through the production
// writer, and the document dated by the term the customer is sold on.
func TestIntegration_TheInvoiceIsDatedByTheCustomersTerms(t *testing.T) {
	ctx := context.Background()
	pool := freshMigratedDB(t, "s4fin2tm1")

	repo := customer.NewRepository(pool)
	svc := customer.NewService(repo).WithWriter(repo)
	acct, err := svc.CreateCustomer(ctx, customer.CreateInput{AccountNo: "TM1-ROAD", Name: "The Whole Road"})
	if err != nil {
		t.Fatalf("create the customer: %v", err)
	}
	if _, err := svc.SetTerms(ctx, acct.CustomerID, customer.Terms2PctNet30); err != nil {
		t.Fatalf("put the customer on 2/10 net 30: %v", err)
	}

	// The production writer: the composition root's own builder, with the terms seam bound to
	// the customer module's master.
	at := time.Date(2026, 1, 5, 2, 30, 0, 0, time.UTC)
	writer := newInvoiceWriter(pool).
		WithOrderSource(tm1OrderSource{customer: acct.CustomerID}).
		WithClock(func() time.Time { return at })
	doc, err := writer.InvoiceOrder(ctx, "")
	if err != nil {
		t.Fatalf("raise the invoice for the customer on 2/10 net 30: %v", err)
	}

	var issue, due string
	var discount *string
	var code *string
	if err := pool.QueryRow(ctx,
		`SELECT issue_date::text, due_date::text, discount_date::text, terms_code FROM invoices WHERE id = $1::uuid`,
		doc.InvoiceID).Scan(&issue, &due, &discount, &code); err != nil {
		t.Fatalf("read the raised invoice: %v", err)
	}
	// The clock is at 21:30 in the yard's own house time on the fourth; the UTC day is the fifth.
	if issue != "2026-01-04" {
		t.Fatalf("the invoice is dated the house day: got %s, want 2026-01-04", issue)
	}
	// The term's own net days, counted from that same day.
	if due != "2026-02-03" {
		t.Fatalf("the invoice is due the term's 30 house days later: got %s, want 2026-02-03", due)
	}
	// The term's own discount window.
	if discount == nil || *discount != "2026-01-14" {
		t.Fatalf("the discount runs until the term's tenth house day: got %v, want 2026-01-14", discount)
	}
	if code == nil || *code != customer.Terms2PctNet30 {
		t.Fatalf("the invoice carries the code it was dated by: got %v, want %s", code, customer.Terms2PctNet30)
	}
}
