//go:build integration

package billing

// invoice_terms_integration_test.go, the invoice dated by the customer's terms (wave 3, TM-1).
//
// The terms of a customer are a term of the master's: a code, a name, the net days the document
// is due in, and the discount window of a term that carries one. The writer reads them through
// the terms seam and dates the document by them, in the dealer's house days.
//
//  · a 2/10 net 30 invoice dated a house day is due 30 days later, with its discount until day 10
//  · RECEIPT is due the same house day
//  · an evening invoice (after 20:00 house time) is dated the house day, not the UTC day
//  · the document carries the code it was dated by

import (
	"context"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customer"
)

// termsReaderStub answers the terms seam with one fixed term, so the writer's own dating is the
// only thing under test. The master behind it is the customer module's own read, proved there.
type termsReaderStub struct{ term customer.Terms }

func (s termsReaderStub) TermsFor(context.Context, string) (customer.Terms, error) {
	return s.term, nil
}

// eveningClock is the instant of an evening invoice: the UTC day has rolled over, the yard is
// still on the previous evening at 21:30 in its own house time, and the two spellings of "today"
// disagree.
func eveningClock() time.Time { return time.Date(2026, 1, 5, 2, 30, 0, 0, time.UTC) }

func TestIntegrationInvoiceWriter_TheDiscountTermDatesTheDocument(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	cust := seedCustomer(t, pool, ns(t))
	at := eveningClock()
	w := NewInvoiceWriter(pool).
		WithOrderSource(houseDaySource{customer: cust, branch: trentonBranch, terms: 30}).
		WithTerms(termsReaderStub{term: customer.Terms{
			Code: "2-10-NET30", Name: "2/10 net 30", NetDays: 30, DiscountBps: 200, DiscountDays: 10,
		}}).
		WithClock(func() time.Time { return at })
	doc, err := w.InvoiceOrder(ctx, "")
	if err != nil {
		t.Fatalf("raise the order invoice on 2/10 net 30: %v", err)
	}
	var issue, due string
	var discount *string
	var code *string
	if err := pool.QueryRow(ctx,
		`SELECT issue_date::text, due_date::text, discount_date::text, terms_code FROM invoices WHERE id = $1::uuid`,
		doc.InvoiceID).Scan(&issue, &due, &discount, &code); err != nil {
		t.Fatalf("read the raised invoice: %v", err)
	}
	// The house day at the pinned clock is the fourth; the UTC day is the fifth.
	if issue != "2026-01-04" {
		t.Fatalf("the issue date is the house day: got %s, want 2026-01-04", issue)
	}
	// The net days of the term, counted from the same day the document was issued.
	if due != "2026-02-03" {
		t.Fatalf("a 2/10 net 30 invoice is due 30 house days later: got %s, want 2026-02-03", due)
	}
	// The discount runs until the term's own window is out: ten house days from the issue day.
	if discount == nil || *discount != "2026-01-14" {
		t.Fatalf("the discount runs until the tenth house day: got %v, want 2026-01-14", discount)
	}
	if code == nil || *code != "2-10-NET30" {
		t.Fatalf("the invoice carries the code it was dated by: got %v, want 2-10-NET30", code)
	}
}

func TestIntegrationInvoiceWriter_ReceiptIsDueTheSameHouseDay(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	cust := seedCustomer(t, pool, ns(t))
	at := eveningClock()
	w := NewInvoiceWriter(pool).
		WithOrderSource(houseDaySource{customer: cust, branch: trentonBranch, terms: 0}).
		WithTerms(termsReaderStub{term: customer.Terms{Code: "RECEIPT", Name: "Due on receipt", NetDays: 0}}).
		WithClock(func() time.Time { return at })
	doc, err := w.InvoiceOrder(ctx, "")
	if err != nil {
		t.Fatalf("raise the order invoice on receipt terms: %v", err)
	}
	var issue, due string
	var discount *string
	if err := pool.QueryRow(ctx,
		`SELECT issue_date::text, due_date::text, discount_date::text FROM invoices WHERE id = $1::uuid`,
		doc.InvoiceID).Scan(&issue, &due, &discount); err != nil {
		t.Fatalf("read the raised invoice: %v", err)
	}
	if issue != "2026-01-04" {
		t.Fatalf("the issue date is the house day: got %s, want 2026-01-04", issue)
	}
	// Due on receipt is due the day it is issued, never the writer's own default month.
	if due != issue {
		t.Fatalf("a receipt terms invoice is due the day it is issued: got due %s, want %s", due, issue)
	}
	// A term with no discount carries no discount day.
	if discount != nil {
		t.Fatalf("a receipt terms invoice carries no discount day: got %s", *discount)
	}
}

func TestIntegrationInvoiceWriter_AnEveningInvoiceIsDatedTheHouseDay(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	cust := seedCustomer(t, pool, ns(t))
	at := eveningClock()
	w := NewInvoiceWriter(pool).
		WithOrderSource(houseDaySource{customer: cust, branch: trentonBranch, terms: 30}).
		WithTerms(termsReaderStub{term: customer.Terms{Code: "NET30", Name: "Net 30", NetDays: 30}}).
		WithClock(func() time.Time { return at })
	doc, err := w.InvoiceOrder(ctx, "")
	if err != nil {
		t.Fatalf("raise the evening order invoice: %v", err)
	}
	var issue, due string
	if err := pool.QueryRow(ctx,
		`SELECT issue_date::text, due_date::text FROM invoices WHERE id = $1::uuid`,
		doc.InvoiceID).Scan(&issue, &due); err != nil {
		t.Fatalf("read the raised invoice: %v", err)
	}
	// The clock is at 21:30 in the yard's own house time, on the fourth; the UTC day is the
	// fifth. The document is dated the house day, and its due date counts from that same day.
	if issue != "2026-01-04" {
		t.Fatalf("an evening invoice is dated the house day, not the UTC day: got %s, want 2026-01-04", issue)
	}
	if due != "2026-02-03" {
		t.Fatalf("the due date counts the terms from the house issue day: got %s, want 2026-02-03", due)
	}
}
