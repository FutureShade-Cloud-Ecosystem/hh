package gl

// An explicit ?as_of= on the tie-out names the dealer's day. Both arms must cut that same house day:
// the AR arm reads the house day of the instant it is handed (its issue date column), the GL arm cuts
// posted_at at the instant itself. A bare time.Parse hands both midnight at Greenwich, which is the
// previous evening in the yard, so the AR arm dropped the day asked for while the GL arm did not.

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
)

// dealerWideCaller is the caller these route-level tests run under: the tie-out is fenced to the
// caller's branch reach (FW1-10), and a consolidated tie-out (no branch named) is a dealer wide
// caller's read.
var dealerWideCaller = identity.StaffContext{
	UserID: "u-fc", RoleCode: "FINANCIAL_CONTROLLER", Tier: identity.TierLeadership,
	Scope: identity.ScopeGlobal, Status: identity.StatusActive,
}

// seenAR records the instant the AR arm was handed.
type seenAR struct{ at time.Time }

func (s *seenAR) ARBalanceCents(_ context.Context, asOf time.Time, _ string) (int64, error) {
	s.at = asOf
	return 0, nil
}

func TestTieOutRoute_ExplicitAsOfNamesTheHouseDayOnBothArms(t *testing.T) {
	const day = "2026-03-10"
	f := &fakeStore{}
	ar := &seenAR{}
	h := NewHandler(newTestServiceAs(f, dealerWideCaller).WithARSubledger(ar), slog.New(slog.NewTextHandler(io.Discard, nil)))

	req := httptest.NewRequest(http.MethodGet, "/gl/tie-out?as_of="+day, nil)
	rec := httptest.NewRecorder()
	h.tieOut(rec, req, guards.StaffCaller{})
	if rec.Code != http.StatusOK {
		t.Fatalf("tie-out answered %d", rec.Code)
	}

	if !ar.at.Equal(f.asOfSeen) {
		t.Fatalf("the AR arm was handed %s and the GL arm cut at %s: the two sides of the tie-out do not share a basis",
			ar.at.Format(time.RFC3339Nano), f.asOfSeen.Format(time.RFC3339Nano))
	}
	if got := houseday.Today(func() time.Time { return ar.at }); got != day {
		t.Fatalf("as_of=%s was cut on the house day %s; an explicit date must name the day asked for", day, got)
	}
	// Inclusive of the whole day: a posting late that evening is at or before the cut, and the next
	// instant is already the following house day.
	if next := houseday.Today(func() time.Time { return ar.at.Add(time.Microsecond) }); next == day {
		t.Fatalf("the cut %s is not the last instant of %s", ar.at.Format(time.RFC3339Nano), day)
	}
}

type glZoneSrc struct{ z houseday.Zone }

func (s glZoneSrc) BusinessZone(context.Context) (houseday.Zone, error) { return s.z, nil }

// An explicit tie-out day is cut at the last instant of the dealer's own day (item 3b slice 3): a Los
// Angeles dealer's 10 March ends three hours after a Toronto dealer's, and the AR arm and the GL arm
// still share the one cut.
func TestTieOutRoute_ExplicitAsOfIsCutOnTheDealersDay(t *testing.T) {
	const day = "2026-03-10"
	la, err := houseday.NewZone("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		zone houseday.Zone
	}{{"Los Angeles", la}, {"Toronto after Los Angeles", houseday.DefaultZone()}} {
		f := &fakeStore{}
		ar := &seenAR{}
		h := NewHandler(newTestServiceAs(f, dealerWideCaller).WithARSubledger(ar), slog.New(slog.NewTextHandler(io.Discard, nil))).
			WithZone(glZoneSrc{z: tc.zone})
		req := httptest.NewRequest(http.MethodGet, "/gl/tie-out?as_of="+day, nil)
		rec := httptest.NewRecorder()
		h.tieOut(rec, req, guards.StaffCaller{})
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: tie-out answered %d", tc.name, rec.Code)
		}
		if !ar.at.Equal(f.asOfSeen) {
			t.Fatalf("%s: the two arms do not share a basis", tc.name)
		}
		if got := tc.zone.Today(func() time.Time { return ar.at }); got != day {
			t.Fatalf("%s: as_of=%s was cut on the dealer's day %s", tc.name, day, got)
		}
		if next := tc.zone.Today(func() time.Time { return ar.at.Add(time.Microsecond) }); next == day {
			t.Fatalf("%s: the cut is not the last instant of %s", tc.name, day)
		}
	}
	// And the two zones cut at different instants.
	tor := houseday.DefaultZone()
	a, _ := la.EndOfDay(day)
	b, _ := tor.EndOfDay(day)
	if a.Equal(b) {
		t.Fatal("Los Angeles and Toronto cut the same instant")
	}
}
