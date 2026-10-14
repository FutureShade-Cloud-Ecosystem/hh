package pos

// zone_test.go, the ring's invoice dates in the dealer's own zone (item 3b slice 3). The same instant
// is the 27th in Toronto and still the 26th in Los Angeles; the ring files its invoice in its own
// dealer's calendar, two harnesses in one process do not leak, and a failed zone read refuses the
// ring before anything is written.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
)

type zoneSrc struct {
	zone houseday.Zone
	err  error
}

func (z zoneSrc) BusinessZone(context.Context) (houseday.Zone, error) { return z.zone, z.err }

func zoneNamed(t *testing.T, name string) zoneSrc {
	t.Helper()
	z, err := houseday.NewZone(name)
	if err != nil {
		t.Fatal(err)
	}
	return zoneSrc{zone: z}
}

// 05:00 UTC on 27 August: 01:00 on the 27th in Toronto, 22:00 on the 26th in Los Angeles.
var ringInstant = time.Date(2026, time.August, 27, 5, 0, 0, 0, time.UTC)

func TestSale_InvoiceDaysFollowTheDealersZone(t *testing.T) {
	for _, tc := range []struct {
		name       string
		src        houseday.Source
		issue, due string
	}{
		{"no source, the Ontario dealer", nil, "2026-08-27", "2026-09-26"},
		{"Los Angeles", zoneNamed(t, "America/Los_Angeles"), "2026-08-26", "2026-09-25"},
		{"Toronto after Los Angeles", zoneNamed(t, "America/Toronto"), "2026-08-27", "2026-09-26"},
	} {
		h := newHarness()
		h.svc = h.svc.WithClock(func() time.Time { return ringInstant }).WithZone(tc.src)
		h.store.customers["cust-abc"] = customerRec{ID: "cust-abc", CustomerType: "CONTRACTOR", PaymentTermsDays: 30, CreditLimitCents: 5_000_000}
		in := baseInput()
		in.Tender = TenderAccount
		if _, err := h.svc.Sale(context.Background(), in); err != nil {
			t.Fatalf("%s: account sale: %v", tc.name, err)
		}
		inv := h.store.invoices[len(h.store.invoices)-1]
		if inv.IssueDate != tc.issue || inv.DueDate != tc.due {
			t.Fatalf("%s: issue/due = %s/%s, want %s/%s", tc.name, inv.IssueDate, inv.DueDate, tc.issue, tc.due)
		}
	}
}

func TestSale_AFailedZoneReadRefusesTheRingBeforeAnythingIsWritten(t *testing.T) {
	boom := errors.New("settings unavailable")
	h := newHarness()
	h.svc = h.svc.WithClock(func() time.Time { return ringInstant }).WithZone(zoneSrc{err: boom})
	before := len(h.store.invoices)
	if _, err := h.svc.Sale(context.Background(), baseInput()); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the zone read failure surfaced", err)
	}
	if len(h.store.invoices) != before {
		t.Fatalf("a refused ring wrote %d invoice(s)", len(h.store.invoices)-before)
	}
}
