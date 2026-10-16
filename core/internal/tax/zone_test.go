package tax

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
)

// Item 3b slice 3: a blank as-of date is the dealer's own today. The same instant is the 27th in
// Toronto and still the 26th in Los Angeles, so a rate effective on the 27th binds in Toronto and not
// yet in Los Angeles.

type taxZoneSrc struct {
	zone houseday.Zone
	err  error
}

func (z taxZoneSrc) BusinessZone(context.Context) (houseday.Zone, error) { return z.zone, z.err }

func TestService_BlankAsOfIsTheDealersToday(t *testing.T) {
	at := time.Date(2026, time.August, 27, 5, 0, 0, 0, time.UTC)
	la, err := houseday.NewZone("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		src  houseday.Source
		want string
	}{
		{"no source, the Ontario dealer", nil, "2026-08-27"},
		{"Los Angeles", taxZoneSrc{zone: la}, "2026-08-26"},
		{"Toronto after Los Angeles", taxZoneSrc{zone: houseday.DefaultZone()}, "2026-08-27"},
	} {
		s := &Service{now: func() time.Time { return at }, zone: tc.src}
		got, err := s.normDate(context.Background(), "")
		if err != nil || got != tc.want {
			t.Fatalf("%s: blank as-of = %q, %v; want %s", tc.name, got, err, tc.want)
		}
		// A named day is kept as named and reads no zone.
		if got, err := s.normDate(context.Background(), "2026-01-02"); err != nil || got != "2026-01-02" {
			t.Fatalf("%s: a named day = %q, %v", tc.name, got, err)
		}
	}
	boom := errors.New("settings unavailable")
	s := &Service{now: func() time.Time { return at }, zone: taxZoneSrc{err: boom}}
	if _, err := s.normDate(context.Background(), ""); !errors.Is(err, boom) {
		t.Fatalf("a failed zone read must refuse the default day: %v", err)
	}
	if got, err := s.normDate(context.Background(), "2026-01-02"); err != nil || got != "2026-01-02" {
		t.Fatalf("a named day must not need the zone: %q %v", got, err)
	}
}
