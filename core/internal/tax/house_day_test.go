package tax

// house_day_test.go: the day an "as of today" tax lookup means is the dealer's day (the house
// zone), never the server's zone and never UTC. After the UTC day change and before the house
// zone's own, a zero-rate exemption planted for the house zone's day must govern a sale rung
// at that moment, and a row dated for the next day must not.

import (
	"context"
	"testing"
	"time"
)

// edgeInstants is one instant, just after the UTC day change and still the earlier day in the
// house zone, as read by processes in different zones: UTC (a container), Auckland (a day ahead)
// and Los Angeles (same day as the house). The answer must not depend on which one the server is.
func edgeInstants(t *testing.T) map[string]time.Time {
	t.Helper()
	out := map[string]time.Time{}
	base := time.Date(2026, 3, 2, 0, 30, 0, 0, time.UTC)
	for _, name := range []string{"UTC", "Pacific/Auckland", "America/Los_Angeles"} {
		loc, err := time.LoadLocation(name)
		if err != nil {
			t.Fatalf("load %s: %v", name, err)
		}
		out[name] = base.In(loc)
	}
	return out
}

func TestToday_IsTheHouseZonesDay(t *testing.T) {
	for name, at := range edgeInstants(t) {
		now := func() time.Time { return at }
		if got, want := today(now), "2026-03-01"; got != want {
			t.Errorf("clock read in %s: the house zone is still on the earlier day: got %s, want %s", name, got, want)
		}
		if got, err := normDate("", now); err != nil || got != "2026-03-01" {
			t.Errorf("clock read in %s: an empty as-of date means the house zone's day: got %q, %v", name, got, err)
		}
	}
}

// asOfStore records the date the engine asks the rate table about.
type asOfStore struct {
	*fakeStore
	asOf []string
}

func (a *asOfStore) ResolveRate(ctx context.Context, jur, reason, asOf string) (int, bool, error) {
	a.asOf = append(a.asOf, asOf)
	return a.fakeStore.ResolveRate(ctx, jur, reason, asOf)
}

func TestAssessSale_ReadsTheRateTableOnTheHouseDay(t *testing.T) {
	at := edgeInstants(t)["UTC"]
	st := &asOfStore{fakeStore: newFakeStore()}
	svc, _ := newSvc(st.fakeStore)
	svc.repo = st
	svc.now = func() time.Time { return at }
	if _, err := svc.AssessSale(context.Background(), SaleAssessment{SubtotalCents: 26600}); err != nil {
		t.Fatalf("assess: %v", err)
	}
	if len(st.asOf) == 0 {
		t.Fatal("the engine never read the rate table")
	}
	for _, got := range st.asOf {
		if got != "2026-03-01" {
			t.Fatalf("the rate lookup must be as of the house zone's day: got %s", got)
		}
	}
}
