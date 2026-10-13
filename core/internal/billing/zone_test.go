package billing

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
)

// Item 3b slice 3: the business day is the dealer's own zone, injected per service. A dealer with no
// zone source is America/Toronto exactly; a Los Angeles dealer files the same instant on its own day.

type zoneSource struct {
	zone houseday.Zone
	err  error
}

func (z zoneSource) BusinessZone(context.Context) (houseday.Zone, error) { return z.zone, z.err }

func zoneOf(t *testing.T, name string) zoneSource {
	t.Helper()
	z, err := houseday.NewZone(name)
	if err != nil {
		t.Fatal(err)
	}
	return zoneSource{zone: z}
}

// 05:00 UTC on 27 August: 01:00 on the 27th in Toronto, 22:00 on the 26th in Los Angeles.
var zoneInstant = time.Date(2026, time.August, 27, 5, 0, 0, 0, time.UTC)

func promiseOn(t *testing.T, src houseday.Source, day string) error {
	t.Helper()
	store := &fakeRegisterStore{
		policy: RegisterPolicy{AccountPositionVisibility: VisibilityManagerUp, DueSoonDays: 7},
		header: InvoiceHeaderFacts{ID: "i1", CustomerID: "c1", BalanceCents: 100000},
	}
	svc := newFakeService(t, store, zoneInstant).WithZone(src)
	_, _, err := svc.AddPromise(context.Background(), PromiseInput{
		InvoiceID: "i1", PromisedCents: 1000, PromisedDate: day, IdempotencyKey: "k-" + day})
	return err
}

func isPast(err error) bool { return err != nil && err.Error() == "the promised date is already past" }

func TestAddPromise_TheDealersZoneDecidesWhichDayIsPast(t *testing.T) {
	// No source: the Ontario dealer, whose day at the instant is the 27th, so the 26th is past.
	if err := promiseOn(t, nil, "2026-08-26"); !isPast(err) {
		t.Fatalf("no source, the 26th: err = %v, want already past", err)
	}
	// A Los Angeles dealer is still on the 26th: a promise for it is today's, not past.
	if err := promiseOn(t, zoneOf(t, "America/Los_Angeles"), "2026-08-26"); isPast(err) {
		t.Fatalf("Los Angeles, the 26th: refused as past: %v", err)
	}
	// Two dealers in one process do not leak: Toronto again after Los Angeles still answers past.
	if err := promiseOn(t, zoneOf(t, "America/Toronto"), "2026-08-26"); !isPast(err) {
		t.Fatalf("Toronto after Los Angeles, the 26th: err = %v, want already past", err)
	}
}

func TestAddPromise_AFailedZoneReadRefusesTheDecision(t *testing.T) {
	boom := errors.New("settings unavailable")
	if err := promiseOn(t, zoneSource{err: boom}, "2026-09-30"); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the zone read failure surfaced", err)
	}
}

// -- the receivables' day is the dealer's day ----------------------------------------------------

// An invoice due on the 26th is not late on the dealer's 26th and is one day late on its 27th. At
// zoneInstant (05:00 UTC on the 27th) that is Toronto's 27th and Los Angeles's still-26th.
func TestDaysPastDueIn_CountsTheDealersCalendarDays(t *testing.T) {
	la, _ := houseday.NewZone("America/Los_Angeles")
	due := time.Date(2026, time.August, 26, 0, 0, 0, 0, time.UTC)
	if got := DaysPastDueIn(la, due, zoneInstant); got != 0 {
		t.Fatalf("Los Angeles: %d days past due, want 0 (due today)", got)
	}
	if got := DaysPastDueIn(houseday.DefaultZone(), due, zoneInstant); got != 1 {
		t.Fatalf("Toronto: %d days past due, want 1", got)
	}
	if got := DaysPastDue(due, zoneInstant); got != 1 {
		t.Fatalf("the default moved: %d, want 1", got)
	}
}

func TestBuildCardsIn_BucketsOnTheDealersDay(t *testing.T) {
	la, _ := houseday.NewZone("America/Los_Angeles")
	p := ARLandingPolicy{AgingWarnDays: 0, AgingEscalateDays: 30}
	p.Tier = TierPro
	invs := []OpenInvoice{{
		CustomerID: "c1", BalanceCents: 1000,
		DueDate: time.Date(2026, time.August, 26, 0, 0, 0, 0, time.UTC),
	}}
	count := func(cards []LandingCard, bucket string) int {
		for _, c := range cards {
			if c.Bucket == bucket {
				return c.Invoices
			}
		}
		return -1
	}
	if n := count(BuildCardsIn(la, invs, p, zoneInstant), BucketCurrent); n != 1 {
		t.Fatalf("Los Angeles: current bucket holds %d, want the invoice (due today)", n)
	}
	if n := count(BuildCardsIn(houseday.DefaultZone(), invs, p, zoneInstant), BucketCurrent); n != 0 {
		t.Fatalf("Toronto: current bucket holds %d, want 0 (one day late)", n)
	}
}

func TestRegisterStatusIn_JudgesLatenessOnTheDealersDay(t *testing.T) {
	la, _ := houseday.NewZone("America/Los_Angeles")
	v := RegisterRow{DocType: "INVOICE", StoredStatus: "FINALIZED", DueDate: time.Date(2026, time.August, 26, 0, 0, 0, 0, time.UTC)}
	if st, days := registerStatusIn(la, v, zoneInstant, 0); st == StatusOverdue || days != 0 {
		t.Fatalf("Los Angeles: %s/%d, want not overdue on the due day", st, days)
	}
	if st, days := registerStatusIn(houseday.DefaultZone(), v, zoneInstant, 0); st != StatusOverdue || days != 1 {
		t.Fatalf("Toronto: %s/%d, want overdue by one day", st, days)
	}
}

func TestParseAsOfIn_ReadsTheDayInTheDealersZone(t *testing.T) {
	la, _ := houseday.NewZone("America/Los_Angeles")
	got, err := parseAsOfIn(la, "2026-08-26")
	if err != nil {
		t.Fatal(err)
	}
	if d := la.Today(func() time.Time { return got }); d != "2026-08-26" {
		t.Fatalf("Los Angeles as-of reads as %s, want 2026-08-26", d)
	}
	if got.Location() != la.Location() && got.Format("2006-01-02") != "2026-08-26" {
		t.Fatalf("the instant is not in the dealer's frame: %v", got)
	}
	if _, err := parseAsOfFor(context.Background(), zoneSource{err: errors.New("down")}, "2026-08-26"); err == nil {
		t.Fatal("a failed zone read must refuse the as-of")
	}
}

func TestRepository_ClockAtReadsInTheDealersZone(t *testing.T) {
	la, _ := houseday.NewZone("America/Los_Angeles")
	r := (&Repository{}).WithZone(zoneSource{zone: la})
	got, err := r.clockAt(context.Background(), zoneInstant)
	if err != nil || got.Format("2006-01-02") != "2026-08-26" {
		t.Fatalf("clockAt = %v, %v; want the dealer's 26th", got, err)
	}
	got, err = (&Repository{}).clockAt(context.Background(), zoneInstant)
	if err != nil || got.Format("2006-01-02") != "2026-08-27" {
		t.Fatalf("no source: %v, %v; want Toronto's 27th", got, err)
	}
}
