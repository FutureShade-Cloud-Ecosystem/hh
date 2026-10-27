//go:build integration

package billing

// invoice_house_day_integration_test.go, the invoice writers' dates at the pinned clock.
//
// Every writer that used to leave issue_date to the database default (CURRENT_DATE, the session
// zone's day) now binds the house day from Go on its own clock, so both legs pin the writer's
// clock just after the UTC day change: the UTC day has rolled over, the dealer's yard is still on
// the previous evening, and the two spellings of "today" disagree. The wanted dates are fixed
// literals of the pinned instant, not of the wall clock, so each leg is red on code that answers
// either the UTC day or the database session's day, whatever hour the suite runs at.

import (
	"context"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
)

// pinnedHouseDay is the instant both writers' clocks are pinned at and the house day it names.
// The UTC day is the fifth; the house zone is still on the fourth.
func pinnedHouseDay(t *testing.T) (time.Time, string) {
	t.Helper()
	at := houseday.JustAfterUTCDayChange(time.Date(2026, 1, 5, 15, 0, 0, 0, time.UTC))
	return at, "2026-01-04"
}

// houseDaySource is the order-facts seam answering one fixed billable order, so the writer's own
// transaction is the only thing under test.
type houseDaySource struct {
	customer, branch string
	terms            int
}

func (s houseDaySource) InvoiceableOrder(_ context.Context, _ string, _ []string) (OrderInvoiceFacts, error) {
	return OrderInvoiceFacts{
		CustomerID: s.customer, BranchID: s.branch, ClosesOrder: true, TermsDays: s.terms,
		Lines: []OrderInvoiceLine{{
			LineNo: 1, Description: "Pavers", QtyEntered: "1", UOMEntered: "EA", QtyBase: "1",
			UnitPriceCents: 5_000, PriceUOM: "EA", LineTotalCents: 5_000,
		}},
	}, nil
}

func (houseDaySource) MarkGroupsInvoiced(context.Context, []string, string, time.Time) error {
	return nil
}

func TestIntegrationInvoiceWriter_IssueDayIsTheHousesDayAtTheUTCDayChange(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	cust := seedCustomer(t, pool, ns(t))
	at, wantDay := pinnedHouseDay(t)
	w := NewInvoiceWriter(pool).
		WithOrderSource(houseDaySource{customer: cust, branch: trentonBranch, terms: 30}).
		WithClock(func() time.Time { return at })
	doc, err := w.InvoiceOrder(ctx, "")
	if err != nil {
		t.Fatalf("raise the order invoice: %v", err)
	}
	var issue, due string
	if err := pool.QueryRow(ctx,
		`SELECT issue_date::text, due_date::text FROM invoices WHERE id = $1::uuid`,
		doc.InvoiceID).Scan(&issue, &due); err != nil {
		t.Fatalf("read the raised invoice: %v", err)
	}
	if issue != wantDay {
		t.Fatalf("the issue date is the house zone's day at the pinned clock: got %s, want %s", issue, wantDay)
	}
	// The due date counts the terms from the SAME day the document was issued, so an evening
	// invoice is never due a day earlier than its own terms.
	if want := "2026-02-03"; due != want {
		t.Fatalf("the due date is the house issue day plus the terms: got %s, want %s", due, want)
	}
}

// TestIntegrationInvoiceWriter_CashOnDemandIsDueOnIssue, the Dibbits COD rule at the writer: Cash
// On Demand means pay before release, with an invoice number while unpaid, so a terms 0 order's
// invoice is due the day it is issued. Every order invoice that is not rung at the till (the Quote
// Desk order payment, the Counter Till Pay balance raise, the multi-ship group invoice) resolves
// its terms through the same seam, so one leg here covers all three.
func TestIntegrationInvoiceWriter_CashOnDemandIsDueOnIssue(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	cust := seedCustomer(t, pool, ns(t))
	if _, err := pool.Exec(ctx,
		`UPDATE customers SET payment_terms_days = 0 WHERE id = $1::uuid`, cust); err != nil {
		t.Fatalf("make the account Cash On Demand: %v", err)
	}
	at, wantDay := pinnedHouseDay(t)
	w := NewInvoiceWriter(pool).
		WithOrderSource(houseDaySource{customer: cust, branch: trentonBranch, terms: 0}).
		WithClock(func() time.Time { return at })
	doc, err := w.InvoiceOrder(ctx, "")
	if err != nil {
		t.Fatalf("raise the Cash On Demand order invoice: %v", err)
	}
	var issue, due string
	if err := pool.QueryRow(ctx,
		`SELECT issue_date::text, due_date::text FROM invoices WHERE id = $1::uuid`,
		doc.InvoiceID).Scan(&issue, &due); err != nil {
		t.Fatalf("read the raised invoice: %v", err)
	}
	// A Cash On Demand invoice is due on issue: the customer's 0 counts as 0 days, never as the
	// writer's own default month.
	if issue != wantDay || due != wantDay {
		t.Fatalf("a Cash On Demand invoice is due on issue: got issue %s due %s, want both %s", issue, due, wantDay)
	}
}

func TestIntegrationCreditMemo_IssueDayIsTheHousesDayAtTheUTCDayChange(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	cust := seedCustomer(t, pool, ns(t))
	at, wantDay := pinnedHouseDay(t)
	w := NewInvoiceWriter(pool).WithClock(func() time.Time { return at })
	doc, err := w.IssueCreditMemo(ctx, CreditMemoInput{
		CustomerID: cust, BranchID: trentonBranch,
		AmountCents: 2_500, Memo: "house day leg", IdempotencyKey: "hday-cm-" + ns(t),
	})
	if err != nil {
		t.Fatalf("raise the credit memo: %v", err)
	}
	var issue, due string
	if err := pool.QueryRow(ctx,
		`SELECT issue_date::text, due_date::text FROM invoices WHERE id = $1::uuid`,
		doc.InvoiceID).Scan(&issue, &due); err != nil {
		t.Fatalf("read the raised memo: %v", err)
	}
	// A memo is due the day it is issued, and both spellings of that day are the house zone's.
	if issue != wantDay || due != wantDay {
		t.Fatalf("the memo's issue and due days are the house zone's day at the pinned clock: got %s/%s, want %s", issue, due, wantDay)
	}
}
