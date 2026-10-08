package pos

// sale_house_day_test.go, the ring's invoice dates at the pinned clock. The ring's invoice used to
// leave issue_date to the database default (CURRENT_DATE, the session zone's day) and to derive its
// due dates off the process zone, so both are pinned here at the instant just after the UTC day
// change, where the dealer's yard is still on the previous evening and the two spellings of
// "today" disagree. The wanted dates are fixed literals of the pinned instant, not of the wall
// clock, so each leg is red on code that answers Greenwich's day whatever hour the suite runs at.

import (
	"context"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
)

// pinnedRingClock is the harness clock both legs pin: 00:30 UTC, the house zone's previous evening.
func pinnedRingClock(t *testing.T) (time.Time, string) {
	t.Helper()
	at := houseday.JustAfterUTCDayChange(time.Date(2026, 1, 5, 15, 0, 0, 0, time.UTC))
	return at, "2026-01-04"
}

func TestSale_InvoiceDaysAreTheHousesDayAtTheUTCDayChange(t *testing.T) {
	h := newHarness()
	at, wantDay := pinnedRingClock(t)
	h.svc = h.svc.WithClock(func() time.Time { return at })
	// A cash sale is due on demand: due the day it is rung, the dealer's day.
	if _, err := h.svc.Sale(context.Background(), baseInput()); err != nil {
		t.Fatalf("cash sale: %v", err)
	}
	inv := h.store.invoices[len(h.store.invoices)-1]
	if inv.IssueDate != wantDay || inv.DueDate != wantDay {
		t.Fatalf("the cash invoice's issue and due days are the house zone's day at the pinned clock: got %s/%s, want %s", inv.IssueDate, inv.DueDate, wantDay)
	}
	// An account sale is due on the terms, counted from the SAME day the invoice was issued.
	h.store.customers["cust-abc"] = customerRec{ID: "cust-abc", CustomerType: "CONTRACTOR", PaymentTermsDays: 30, CreditLimitCents: 5_000_000}
	in := baseInput()
	in.Tender = TenderAccount
	if _, err := h.svc.Sale(context.Background(), in); err != nil {
		t.Fatalf("account sale: %v", err)
	}
	inv = h.store.invoices[len(h.store.invoices)-1]
	if inv.IssueDate != wantDay {
		t.Fatalf("the account invoice's issue day is the house zone's day at the pinned clock: got %s, want %s", inv.IssueDate, wantDay)
	}
	if want := "2026-02-03"; inv.DueDate != want {
		t.Fatalf("the account invoice's due day is the house issue day plus the terms: got %s, want %s", inv.DueDate, want)
	}
}
