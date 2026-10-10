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
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
)

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
	h := NewHandler(newTestService(f).WithARSubledger(ar), slog.New(slog.NewTextHandler(io.Discard, nil)))

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
