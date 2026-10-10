package billing

// ar_evening_age_test.go, the AR family's age and day reads in the house evening window.
//
// Every instant here is houseday.JustAfterUTCDayChange: the UTC day has rolled over and the house
// zone is still on the previous evening, so a read at Greenwich is one day ahead of the dealer's day
// and each leg is red on UTC reading code at any wall clock hour.

import (
	"context"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
)

// eveningOf is the pinned instant whose UTC day is utcDay and whose house day is the day before.
func eveningOf(y int, m time.Month, utcDay int) time.Time {
	return houseday.JustAfterUTCDayChange(time.Date(y, m, utcDay, 12, 0, 0, 0, time.UTC))
}

func TestDaysBetween_ReadsTheClockOnTheHouseDay(t *testing.T) {
	// The clock reads the 27th at Greenwich and the 26th in the yard. An invoice due on the 25th
	// (a DATE scanned as midnight UTC) is one day late, not two.
	due := time.Date(2026, time.August, 25, 0, 0, 0, 0, time.UTC)
	if got := daysBetween(due, eveningOf(2026, time.August, 27)); got != 1 {
		t.Fatalf("due the 25th, house day the 26th: %d days late, want 1", got)
	}
	// Due on the house day itself is not late, however late the evening.
	dueToday := time.Date(2026, time.August, 26, 0, 0, 0, 0, time.UTC)
	if got := daysBetween(dueToday, eveningOf(2026, time.August, 27)); got != 0 {
		t.Fatalf("due on the house day: %d days late, want 0", got)
	}
}

func TestRegisterStatus_DueOnTheHouseDayIsNotOverdueInTheEvening(t *testing.T) {
	row := RegisterRow{StoredStatus: "FINALIZED", DueDate: time.Date(2026, time.August, 26, 0, 0, 0, 0, time.UTC)}
	got, days := registerStatus(row, eveningOf(2026, time.August, 27), 0)
	if got == StatusOverdue || days != 0 {
		t.Fatalf("an invoice due on the house day reads %s with %d days, want it not overdue", got, days)
	}
}

func TestParseAsOf_NamesTheHouseDay(t *testing.T) {
	got, err := parseAsOf("2026-03-10")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if d := houseday.Today(func() time.Time { return got }); d != "2026-03-10" {
		t.Errorf("the house day of an explicit as_of = %s, want 2026-03-10", d)
	}
	if d := got.Format("2006-01-02"); d != "2026-03-10" {
		t.Errorf("the date parameter of an explicit as_of = %s, want 2026-03-10", d)
	}
	now, err := parseAsOf("")
	if err != nil {
		t.Fatalf("parse empty: %v", err)
	}
	if now.Location() != houseday.Location() {
		t.Errorf("the default as_of is read in %s, want the house zone", now.Location())
	}
}

func TestHouseClock_DateParametersNameTheHouseDay(t *testing.T) {
	at := eveningOf(2026, time.August, 27)
	if got := houseClock(at).Format("2006-01-02"); got != "2026-08-26" {
		t.Fatalf("a date parameter cut from the evening instant = %s, want 2026-08-26", got)
	}
}

func TestAddPromise_TodayOnTheHouseDayIsNotPast(t *testing.T) {
	store := &fakeRegisterStore{
		policy: RegisterPolicy{AccountPositionVisibility: VisibilityManagerUp, DueSoonDays: 7},
		header: InvoiceHeaderFacts{ID: "i1", CustomerID: "c1", BalanceCents: 100000},
	}
	svc := newFakeService(t, store, eveningOf(2026, time.August, 27))
	_, _, err := svc.AddPromise(context.Background(), PromiseInput{
		InvoiceID: "i1", PromisedCents: 1000, PromisedDate: "2026-08-26", IdempotencyKey: "k"})
	if err != nil && err.Error() == "the promised date is already past" {
		t.Fatalf("a promise for the house day was refused as past: %v", err)
	}
	if _, _, err := svc.AddPromise(context.Background(), PromiseInput{
		InvoiceID: "i1", PromisedCents: 1000, PromisedDate: "2026-08-25", IdempotencyKey: "k2"}); err == nil {
		t.Fatalf("a promise for the day before the house day was accepted")
	}
}
