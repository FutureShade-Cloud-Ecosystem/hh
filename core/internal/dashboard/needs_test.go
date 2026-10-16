package dashboard

// needs_test.go, the Needs-you queue: severity, and the one promise that matters more than
// severity, which is that this read NEVER FAILS. Every one of the six ways a source can go wrong
// is exercised here alongside a healthy source, so each test proves the queue kept answering
// rather than merely that it did not panic.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

var needsNow = time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)

func needsService(t *testing.T, policy map[string][]NeedsPolicy) *Service {
	t.Helper()
	return NewService(newFakeStore(), nil).
		WithClock(func() time.Time { return needsNow }).
		WithNeedsStore(&fakeNeedsPolicy{rows: policy})
}

func countingSource(n int, oldest time.Time) NeedsSource {
	return NeedsFunc(func(context.Context, Scope) (NeedsMeasure, error) {
		return NeedsMeasure{Count: n, Oldest: oldest}, nil
	})
}

func rowKeys(q NeedsQueue) []string {
	out := make([]string, len(q.Rows))
	for i, r := range q.Rows {
		out[i] = r.SourceKey
	}
	return out
}

// -- the catalog ---------------------------------------------------------------------------------

// Every source must be able to say what it is and where it goes, or its row is an unlabelled number
// that leads nowhere, which is the tile design this whole feature replaced.
func TestEveryNeedsSourceIsWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, s := range NeedsSourceCatalog {
		if s.Key == "" || s.What == "" || s.Module == "" {
			t.Fatalf("needs source is missing a required field: %+v", s)
		}
		if s.Unit == "" || s.Screen == "" {
			t.Fatalf("needs source %q has no deep link: a queue row must be one tap from the work", s.Key)
		}
		if seen[s.Key] {
			t.Fatalf("duplicate needs source key %q", s.Key)
		}
		seen[s.Key] = true
		if NeedsSourceByKey[s.Key].What != s.What {
			t.Fatalf("the index and the catalog disagree about %q", s.Key)
		}
	}
}

func TestRegisterNeedsSourceRefusesATypo(t *testing.T) {
	s := needsService(t, nil)
	if err := s.RegisterNeedsSource("nope.source", countingSource(1, time.Time{})); err == nil {
		t.Fatal("a typo'd source key must be refused at boot: its symptom is a row that never appears")
	}
	if err := s.RegisterNeedsSource(NeedsApprovals, nil); err == nil {
		t.Fatal("a nil source must be refused")
	}
	if err := s.RegisterNeedsSource(NeedsApprovals, countingSource(1, time.Time{})); err != nil {
		t.Fatalf("a real key registers: %v", err)
	}
}

// -- severity ------------------------------------------------------------------------------------

func TestSeverityIsScoredOnCountAndEscalatedOneStepByAge(t *testing.T) {
	p := NeedsPolicy{WarnAt: 3, StopAt: 10, StaleMins: 120}
	for _, tc := range []struct {
		count, age int
		want       string
	}{
		{1, 0, SeverityGood},    // below the warn edge
		{3, 0, SeverityWatch},   // at the warn edge
		{9, 0, SeverityWatch},   // below the stop edge
		{10, 0, SeverityStop},   // at the stop edge
		{1, 121, SeverityWatch}, // good, escalated ONE step by age
		{3, 121, SeverityStop},  // watch, escalated ONE step by age
		{10, 999, SeverityStop}, // already as loud as this vocabulary goes
	} {
		if got := SeverityFor(tc.count, tc.age, p); got != tc.want {
			t.Fatalf("SeverityFor(%d, %dm) = %s, want %s", tc.count, tc.age, got, tc.want)
		}
	}
}

func TestZeroThresholdsAreOffRatherThanAlways(t *testing.T) {
	off := NeedsPolicy{WarnAt: 0, StopAt: 0, StaleMins: 0}
	// WarnAt 0 reads as 1: a row only exists when something is waiting, so "warn at zero" is
	// meaningless and the honest reading is "warn as soon as there is one".
	if got := SeverityFor(1, 0, off); got != SeverityWatch {
		t.Fatalf("warn_at 0 reads as 1, got %s", got)
	}
	if got := SeverityFor(9999, 0, off); got != SeverityWatch {
		t.Fatalf("stop_at 0 means this source never reaches stop, got %s", got)
	}
	if got := SeverityFor(1, 99999, off); got != SeverityWatch {
		t.Fatalf("stale_after_minutes 0 means age never escalates, got %s", got)
	}
}

func TestHumanAgeIsShortEnoughForOneLine(t *testing.T) {
	for minutes, want := range map[int]string{
		0: "", -5: "", 1: "1m", 45: "45m", 60: "1h", 130: "2h",
		60 * 36: "1 day", 60 * 24 * 3: "3 days",
	} {
		if got := HumanAge(minutes); got != want {
			t.Fatalf("HumanAge(%d) = %q, want %q", minutes, got, want)
		}
	}
}

// -- assembly ------------------------------------------------------------------------------------

func TestNeedsQueueOrdersByPolicyAndTotalsServerSide(t *testing.T) {
	s := needsService(t, map[string][]NeedsPolicy{ProfileManager: {
		{SourceKey: NeedsAREscalation, Position: 1, Visible: true, WarnAt: 1, StopAt: 3},
		{SourceKey: NeedsApprovals, Position: 0, Visible: true, WarnAt: 1, StopAt: 5},
	}})
	if err := s.RegisterNeedsSource(NeedsApprovals, countingSource(2, needsNow.Add(-90*time.Minute))); err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterNeedsSource(NeedsAREscalation, countingSource(4, time.Time{})); err != nil {
		t.Fatal(err)
	}
	// CostAllowed is true because this queue carries a manager-only source; the gate itself is
	// asserted in TestCurationCannotPutAManagerOnlySourceOnALowerTiersQueue.
	got := s.NeedsFor(context.Background(), Scope{Profile: ProfileManager, CostAllowed: true})
	if len(got.Rows) != 2 || got.Rows[0].SourceKey != NeedsApprovals {
		t.Fatalf("curated order decides the queue: %v", rowKeys(got))
	}
	if got.Total != 6 {
		t.Fatalf("the SERVER totals the queue, not the client: %d", got.Total)
	}
	if got.Rows[0].Age != "1h" || got.Rows[0].AgeMinutes != 90 {
		t.Fatalf("age is computed against the injected clock: %+v", got.Rows[0])
	}
	if got.Rows[1].Severity != SeverityStop || got.Worst != SeverityStop {
		t.Fatalf("worst severity rides the queue: %+v / %s", got.Rows[1], got.Worst)
	}
	if got.Rows[0].Unit == "" || got.Rows[0].Screen == "" || got.Rows[0].Action == "" {
		t.Fatalf("every row carries its deep link and its one word: %+v", got.Rows[0])
	}
}

func TestASourceWithNothingWaitingIsNotARow(t *testing.T) {
	s := needsService(t, map[string][]NeedsPolicy{ProfileCounter: {
		{SourceKey: NeedsParkedTickets, Position: 0, Visible: true, WarnAt: 1},
	}})
	s.RegisterNeedsSource(NeedsParkedTickets, countingSource(0, time.Time{}))
	got := s.NeedsFor(context.Background(), Scope{Profile: ProfileCounter})
	if len(got.Rows) != 0 {
		t.Fatalf("a zero is not a row: the empty state is a sentence, never a zero in a big font: %v", rowKeys(got))
	}
	if got.Rows == nil {
		t.Fatal("...and the empty queue is an ARRAY, never null")
	}
}

// THE DEGRADATION PROOF. Four broken sources and one healthy one, in one queue: each broken row is
// absent and the healthy row still answers. Every case is asserted together on purpose, a queue
// that survives one failure but not two is a queue that fails on a bad afternoon.
func TestABrokenSourceIsAnAbsentRowAndTheQueueStillAnswers(t *testing.T) {
	s := needsService(t, map[string][]NeedsPolicy{ProfileAdmin: {
		{SourceKey: NeedsApprovals, Position: 0, Visible: true, WarnAt: 1},     // unwired
		{SourceKey: NeedsAREscalation, Position: 1, Visible: true, WarnAt: 1},  // fails
		{SourceKey: NeedsUnassigned, Position: 2, Visible: true, WarnAt: 1},    // panics
		{SourceKey: "gone.source", Position: 3, Visible: true, WarnAt: 1},      // not in this build
		{SourceKey: NeedsStalePicks, Position: 4, Visible: false, WarnAt: 1},   // curated off
		{SourceKey: NeedsParkedTickets, Position: 5, Visible: true, WarnAt: 1}, // healthy
	}})
	s.RegisterNeedsSource(NeedsAREscalation, NeedsFunc(
		func(context.Context, Scope) (NeedsMeasure, error) { return NeedsMeasure{}, errors.New("boom") }))
	s.RegisterNeedsSource(NeedsUnassigned, NeedsFunc(
		func(context.Context, Scope) (NeedsMeasure, error) { panic("a nil map in an adapter") }))
	s.RegisterNeedsSource(NeedsStalePicks, countingSource(7, time.Time{}))
	s.RegisterNeedsSource(NeedsParkedTickets, countingSource(4, time.Time{}))

	got := s.NeedsFor(context.Background(), Scope{Profile: ProfileAdmin, CostAllowed: true})
	if len(got.Rows) != 1 || got.Rows[0].SourceKey != NeedsParkedTickets {
		t.Fatalf("every broken source is an ABSENT row and the healthy one answers: %v", rowKeys(got))
	}
	if got.Total != 4 {
		t.Fatalf("the total counts what is actually there: %d", got.Total)
	}
}

func TestUnreadableNeedsPolicyServesTheShippedDefaultsRatherThanNothing(t *testing.T) {
	s := NewService(newFakeStore(), nil).
		WithClock(func() time.Time { return needsNow }).
		WithNeedsStore(&fakeNeedsPolicy{err: errors.New("table missing")})
	s.RegisterNeedsSource(NeedsAREscalation, countingSource(3, time.Time{}))
	got := s.NeedsFor(context.Background(), Scope{Profile: ProfileManager, CostAllowed: true})
	if len(got.Rows) != 1 || got.Rows[0].SourceKey != NeedsAREscalation {
		t.Fatalf("an unreadable policy must not take away yesterday's queue: %v", rowKeys(got))
	}
}

// The opposite case, and it is a different fact: a dealer who curated every source OFF a shape made
// a decision, and an empty successful read is respected as the answer.
func TestAnEmptyCuratedPolicyIsRespectedRatherThanDefaulted(t *testing.T) {
	s := needsService(t, map[string][]NeedsPolicy{ProfileCounter: {}})
	s.RegisterNeedsSource(NeedsParkedTickets, countingSource(3, time.Time{}))
	got := s.NeedsFor(context.Background(), Scope{Profile: ProfileCounter})
	if len(got.Rows) != 0 {
		t.Fatalf("a dealer's empty curation is an answer, not a gap to fill: %v", rowKeys(got))
	}
}

func TestNoNeedsStoreFallsBackToTheShippedPolicy(t *testing.T) {
	s := NewService(newFakeStore(), nil).WithClock(func() time.Time { return needsNow })
	s.RegisterNeedsSource(NeedsUnassigned, countingSource(2, time.Time{}))
	got := s.NeedsFor(context.Background(), Scope{Profile: ProfileYard})
	if len(got.Rows) != 1 || got.Rows[0].SourceKey != NeedsUnassigned {
		t.Fatalf("a build ahead of 0201 still has a queue: %v", rowKeys(got))
	}
}

// 0395: the logistics manager's fallback carries the approvals they filed that are still pending.
func TestDefaultNeedsForLogisticsIncludesFiledPending(t *testing.T) {
	rows := defaultNeedsFor(ProfileManagerLogistics)
	var keys []string
	for _, r := range rows {
		keys = append(keys, r.SourceKey)
	}
	want := []string{NeedsUnassigned, NeedsDueToCommit, NeedsStalePicks, NeedsFiledPending}
	if len(keys) != len(want) {
		t.Fatalf("logistics manager needs %v, want %v", keys, want)
	}
	for i := range want {
		if keys[i] != want[i] {
			t.Fatalf("logistics manager source %d is %q, want %q", i, keys[i], want[i])
		}
	}
}

// 0395: the yard manager's fallback carries dispatch.due_to_commit, because committing a delivery
// date on a load being built is one of their top jobs.
func TestDefaultNeedsForYardIncludesDueToCommit(t *testing.T) {
	rows := defaultNeedsFor(ProfileManagerYard)
	var keys []string
	for _, r := range rows {
		keys = append(keys, r.SourceKey)
	}
	want := []string{NeedsDueToCommit, NeedsOpenShortages, NeedsStalePicks, NeedsReceivingDifferences}
	if len(keys) != len(want) {
		t.Fatalf("yard manager needs %v, want %v", keys, want)
	}
	for i := range want {
		if keys[i] != want[i] {
			t.Fatalf("yard manager source %d is %q, want %q", i, keys[i], want[i])
		}
	}
}

func TestTheTotalCountsEveryRowRatherThanTheFirstFew(t *testing.T) {
	policy := make([]NeedsPolicy, 0, len(NeedsSourceCatalog))
	s := needsService(t, nil)
	for i, m := range NeedsSourceCatalog {
		policy = append(policy, NeedsPolicy{SourceKey: m.Key, Position: i, Visible: true, WarnAt: 1})
		s.RegisterNeedsSource(m.Key, countingSource(2, time.Time{}))
	}
	s.WithNeedsStore(&fakeNeedsPolicy{rows: map[string][]NeedsPolicy{ProfileAdmin: policy}})
	got := s.NeedsFor(context.Background(), Scope{Profile: ProfileAdmin, CostAllowed: true})
	if len(got.Rows) != len(NeedsSourceCatalog) {
		t.Fatalf("the server does not truncate, how many rows to SHOW is the client's: %d", len(got.Rows))
	}
	if got.Total != 2*len(NeedsSourceCatalog) {
		t.Fatalf("the headline must count every row it has, or it disagrees with its own list: %d", got.Total)
	}
}

// CURATION CANNOT GRANT A MANAGER-ONLY SOURCE, 0182's ruling, re-asserted on the queue, with the
// positive control first so the absence below is measured rather than assumed.
func TestCurationCannotPutAManagerOnlySourceOnALowerTiersQueue(t *testing.T) {
	policy := map[string][]NeedsPolicy{ProfileCounter: {
		{SourceKey: NeedsAREscalation, Position: 0, Visible: true, WarnAt: 1},
		{SourceKey: NeedsParkedTickets, Position: 1, Visible: true, WarnAt: 1},
	}}
	build := func() *Service {
		s := needsService(t, policy)
		s.RegisterNeedsSource(NeedsAREscalation, countingSource(41220, time.Time{}))
		s.RegisterNeedsSource(NeedsParkedTickets, countingSource(6, time.Time{}))
		return s
	}

	// POSITIVE CONTROL: a manager gets the row, so the assertion below is about the gate.
	allowed := build().NeedsFor(context.Background(),
		Scope{Profile: ProfileCounter, CostAllowed: true})
	if len(allowed.Rows) != 2 || allowed.Rows[0].SourceKey != NeedsAREscalation {
		t.Fatalf("positive control failed, a manager must get the escalation row: %v", rowKeys(allowed))
	}

	refused := build().NeedsFor(context.Background(),
		Scope{Profile: ProfileCounter, CostAllowed: false})
	if len(refused.Rows) != 1 || refused.Rows[0].SourceKey != NeedsParkedTickets {
		t.Fatalf("a curated manager-only source must be ABSENT below that tier: %v", rowKeys(refused))
	}
	if refused.Total != 6 {
		t.Fatalf("...and its count must not reach the total either: %d", refused.Total)
	}
}

// The bytes, not the struct: the same queue marshalled, positive control then refusal, including
// the literal "null".
func TestAManagerOnlyNeedsRowIsByteAbsentBelowThatTier(t *testing.T) {
	policy := map[string][]NeedsPolicy{ProfileCounter: {
		{SourceKey: NeedsAREscalation, Position: 0, Visible: true, WarnAt: 1},
		{SourceKey: NeedsParkedTickets, Position: 1, Visible: true, WarnAt: 1},
	}}
	build := func() *Service {
		s := needsService(t, policy)
		s.RegisterNeedsSource(NeedsAREscalation, countingSource(41220, time.Time{}))
		s.RegisterNeedsSource(NeedsParkedTickets, countingSource(6, time.Time{}))
		return s
	}
	allowed, err := json.Marshal(build().NeedsFor(context.Background(),
		Scope{Profile: ProfileCounter, CostAllowed: true}))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{NeedsAREscalation, "escalation edge", "41220", "finance"} {
		if !strings.Contains(string(allowed), want) {
			t.Fatalf("positive control failed: %q missing for an allowed caller: %s", want, allowed)
		}
	}
	refused, err := json.Marshal(build().NeedsFor(context.Background(),
		Scope{Profile: ProfileCounter, CostAllowed: false}))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		// "ar" alone is deliberately NOT probed: two letters appear inside "Parked", and a probe
		// that cannot fail is worse than no probe.
		NeedsAREscalation, "escalation", "41220", "finance", "Escalate", "null",
	} {
		if strings.Contains(string(refused), forbidden) {
			t.Fatalf("byte %q is present below the tier that may see it: %s", forbidden, refused)
		}
	}
	if !strings.Contains(string(refused), NeedsParkedTickets) {
		t.Fatalf("the allowed row must still be there: %s", refused)
	}
}
