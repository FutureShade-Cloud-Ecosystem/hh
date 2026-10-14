package messaging

import (
	"context"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
)

type msgZone struct{ z houseday.Zone }

func (m msgZone) BusinessZone(context.Context) (houseday.Zone, error) { return m.z, nil }

// The tile's day, and the day a routine event is filed under, are the dealer's own calendar day (item
// 3b slice 3): at 05:00 UTC on the 27th a Toronto dealer is on the 27th and a Los Angeles dealer on
// the 26th. Two services in one process do not leak.
func TestPaymentsToday_TheDayIsTheDealersZone(t *testing.T) {
	la, err := houseday.NewZone("America/Los_Angeles")
	if err != nil {
		t.Skipf("no tzdata: %v", err)
	}
	at := time.Date(2026, time.August, 27, 5, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		svc  *Service
		want string
	}{
		{"no source, the Ontario dealer", svcWith(newFakeStore()), "2026-08-27"},
		{"Los Angeles", svcWith(newFakeStore()).WithZone(msgZone{z: la}), "2026-08-26"},
		{"Toronto after Los Angeles", svcWith(newFakeStore()).WithZone(msgZone{z: houseday.DefaultZone()}), "2026-08-27"},
	} {
		got, err := tc.svc.PaymentsTodayFor(context.Background(), nil, true, at)
		if err != nil || got.Day != tc.want {
			t.Fatalf("%s: day = %q, %v; want %s", tc.name, got.Day, err, tc.want)
		}
	}
}
