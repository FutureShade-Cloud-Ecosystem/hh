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
