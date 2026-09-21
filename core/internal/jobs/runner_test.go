package jobs

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// runner_test.go, unit cover against an in-memory store. The claim/advance semantics are mirrored
// from repository.go so the scheduling arithmetic and the recording discipline are exercised without
// a database; the SQL-only facts (the atomic conditional UPDATE under real contention) are proved in
// runner_integration_test.go.

// fakeStore is an in-memory jobs store whose ClaimDue mirrors the SQL claim: it advances next_run_at
// with the SAME nextRun function the repository uses, so the two cannot drift.
type fakeStore struct {
	mu        sync.Mutex
	schedules []Schedule
	runs      []RunRecord
	finished  map[string]string // run id → terminal status
	stamps    map[string]string // schedule id → last status
	seq       int
	claimErr  error
}

func newFakeStore(ss ...Schedule) *fakeStore {
	return &fakeStore{schedules: ss, finished: map[string]string{}, stamps: map[string]string{}}
}

func (f *fakeStore) ListSchedules(context.Context) ([]Schedule, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Schedule(nil), f.schedules...), nil
}

func (f *fakeStore) SeedNextRun(_ context.Context, now time.Time) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for i, s := range f.schedules {
		if s.Enabled && s.NextRunAt == nil {
			next := nextRun(s, now, nil)
			f.schedules[i].NextRunAt = &next
			n++
		}
	}
	return n, nil
}

func (f *fakeStore) ClaimDue(_ context.Context, now time.Time) (Schedule, bool, error) {
	if f.claimErr != nil {
		return Schedule{}, false, f.claimErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, s := range f.schedules {
		if !s.Enabled || s.NextRunAt == nil || s.NextRunAt.After(now) {
			continue
		}
		next := nextRun(s, now, s.NextRunAt)
		f.schedules[i].NextRunAt = &next
		f.schedules[i].LastStatus = StatusRunning
		claimed := s
		claimed.NextRunAt = &next
		return claimed, true, nil
	}
	return Schedule{}, false, nil
}

func (f *fakeStore) StartRun(_ context.Context, jobName, _, trigger string, _ *time.Time) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	id := "run-" + string(rune('a'+f.seq%26)) + itoa(f.seq)
	f.runs = append(f.runs, RunRecord{ID: id, JobName: jobName, Trigger: trigger, Status: StatusRunning})
	return id, nil
}

func (f *fakeStore) FinishRun(_ context.Context, runID, status string, res Result, runErr error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.finished[runID] = status
	for i := range f.runs {
		if f.runs[i].ID == runID {
			f.runs[i].Status = status
			f.runs[i].RowsRead = res.RowsRead
			f.runs[i].RowsWritten = res.RowsWritten
			if runErr != nil {
				f.runs[i].Error = runErr.Error()
			}
		}
	}
	return nil
}

func (f *fakeStore) StampSchedule(_ context.Context, scheduleID, status, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stamps[scheduleID] = status
	return nil
}

func (f *fakeStore) runFor(job string) (RunRecord, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.runs) - 1; i >= 0; i-- {
		if f.runs[i].JobName == job {
			return f.runs[i], true
		}
	}
	return RunRecord{}, false
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func dueSchedule(name string) Schedule {
	past := time.Now().Add(-time.Minute)
	mins := 540
	return Schedule{ID: "sched-" + name, Name: name, Kind: KindNightly, NightlyMinute: &mins, Enabled: true, NextRunAt: &past}
}

func TestRunDueExecutesAndRecordsTheRun(t *testing.T) {
	store := newFakeStore(dueSchedule("yard.stale_pick_sweep"))
	calls := 0
	r := NewRunner(store, nil).Register("yard.stale_pick_sweep", func(context.Context) (Result, error) {
		calls++
		return Result{RowsRead: 7, RowsWritten: 3}, nil
	})

	if n := r.RunDue(context.Background()); n != 1 {
		t.Fatalf("ran %d jobs, want 1", n)
	}
	if calls != 1 {
		t.Fatalf("job called %d times", calls)
	}
	rec, ok := store.runFor("yard.stale_pick_sweep")
	if !ok {
		t.Fatal("no run was recorded")
	}
	if rec.Status != StatusSucceeded || rec.RowsRead != 7 || rec.RowsWritten != 3 {
		t.Fatalf("run record = %+v", rec)
	}
	if store.stamps["sched-yard.stale_pick_sweep"] != StatusSucceeded {
		t.Fatalf("schedule stamp = %q", store.stamps["sched-yard.stale_pick_sweep"])
	}
}

func TestASecondClaimInTheSameWindowFindsNothing(t *testing.T) {
	// The claim consumes the window by advancing next_run_at, so a job cannot double-fire.
	store := newFakeStore(dueSchedule("j"))
	calls := 0
	r := NewRunner(store, nil).Register("j", func(context.Context) (Result, error) { calls++; return Result{}, nil })

	r.RunDue(context.Background())
	r.RunDue(context.Background())
	if calls != 1 {
		t.Fatalf("job ran %d times for one due window", calls)
	}
}

func TestAFailingJobIsRecordedFailedAndDoesNotStopTheRunner(t *testing.T) {
	store := newFakeStore(dueSchedule("bad"), dueSchedule("good"))
	boom := errors.New("nope")
	goodRan := false
	r := NewRunner(store, nil).
		Register("bad", func(context.Context) (Result, error) { return Result{}, boom }).
		Register("good", func(context.Context) (Result, error) { goodRan = true; return Result{}, nil })

	if n := r.RunDue(context.Background()); n != 2 {
		t.Fatalf("ran %d jobs, want both", n)
	}
	if !goodRan {
		t.Fatal("a failing job stopped the next one")
	}
	rec, _ := store.runFor("bad")
	if rec.Status != StatusFailed || rec.Error != "nope" {
		t.Fatalf("failed run = %+v", rec)
	}
}

func TestAPanickingJobIsContainedAndRecorded(t *testing.T) {
	// A panic must not unwind through the ticker goroutine and silently stop every other job.
	store := newFakeStore(dueSchedule("panicky"))
	r := NewRunner(store, nil).Register("panicky", func(context.Context) (Result, error) {
		panic("yard on fire")
	})

	r.RunDue(context.Background()) // must not panic out of here
	rec, ok := store.runFor("panicky")
	if !ok {
		t.Fatal("no run recorded for a panicking job")
	}
	if rec.Status != StatusFailed {
		t.Fatalf("panicking run status = %q, want FAILED", rec.Status)
	}
	if rec.Error == "" {
		t.Fatal("a panicking run must record why")
	}
}

func TestASkippedResultIsRecordedSkippedNotSucceeded(t *testing.T) {
	store := newFakeStore(dueSchedule("noop"))
	r := NewRunner(store, nil).Register("noop", func(context.Context) (Result, error) {
		return Result{Skipped: true}, nil
	})
	r.RunDue(context.Background())
	rec, _ := store.runFor("noop")
	if rec.Status != StatusSkipped {
		t.Fatalf("status = %q, want SKIPPED", rec.Status)
	}
}

func TestAScheduleWithNoRegisteredFunctionIsRecordedSkipped(t *testing.T) {
	// Silently dropping it would make a half-deployed schedule invisible.
	store := newFakeStore(dueSchedule("orphan"))
	r := NewRunner(store, nil)
	r.RunDue(context.Background())

	rec, ok := store.runFor("orphan")
	if !ok {
		t.Fatal("an orphan schedule left no trace at all")
	}
	if rec.Status != StatusSkipped || rec.Error == "" {
		t.Fatalf("orphan run = %+v, want SKIPPED with a reason", rec)
	}
}

func TestCheckWiringReportsBothDirections(t *testing.T) {
	store := newFakeStore(dueSchedule("scheduled-but-unregistered"))
	r := NewRunner(store, nil).Register("registered-but-unscheduled", func(context.Context) (Result, error) {
		return Result{}, nil
	})
	orphans, unscheduled := r.CheckWiring(context.Background())
	if len(orphans) != 1 || orphans[0] != "scheduled-but-unregistered" {
		t.Fatalf("orphans = %v", orphans)
	}
	if len(unscheduled) != 1 || unscheduled[0] != "registered-but-unscheduled" {
		t.Fatalf("unscheduled = %v", unscheduled)
	}
}

func TestDuplicateRegistrationPanicsAtWireTime(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("registering two functions under one name did not panic")
		}
	}()
	NewRunner(newFakeStore(), nil).
		Register("dup", func(context.Context) (Result, error) { return Result{}, nil }).
		Register("dup", func(context.Context) (Result, error) { return Result{}, nil })
}

func TestRunNowExecutesOutOfBandWithoutTouchingTheSchedule(t *testing.T) {
	future := time.Now().Add(6 * time.Hour)
	mins := 540
	store := newFakeStore(Schedule{
		ID: "s1", Name: "j", Kind: KindNightly, NightlyMinute: &mins, Enabled: true, NextRunAt: &future,
	})
	r := NewRunner(store, nil).Register("j", func(context.Context) (Result, error) {
		return Result{RowsRead: 1}, nil
	})

	res, err := r.RunNow(context.Background(), "j")
	if err != nil {
		t.Fatalf("run now: %v", err)
	}
	if res.RowsRead != 1 {
		t.Fatalf("result = %+v", res)
	}
	rec, _ := store.runFor("j")
	if rec.Trigger != TriggerManual {
		t.Fatalf("trigger = %q, want MANUAL", rec.Trigger)
	}
	got := store.schedules[0]
	if got.NextRunAt == nil || !got.NextRunAt.Equal(future) {
		t.Fatalf("an on-demand run shifted the nightly cadence: %v", got.NextRunAt)
	}
}

func TestRunNowOnAnUnknownJobIsAnHonestError(t *testing.T) {
	_, err := NewRunner(newFakeStore(), nil).RunNow(context.Background(), "nope")
	if !errors.Is(err, ErrUnknownJob) {
		t.Fatalf("err = %v, want ErrUnknownJob", err)
	}
}

func TestAJobDoesNotOverlapItselfWithinTheProcess(t *testing.T) {
	store := newFakeStore(dueSchedule("slow"))
	release := make(chan struct{})
	entered := make(chan struct{}, 2)
	concurrent := 0
	var mu sync.Mutex

	r := NewRunner(store, nil).Register("slow", func(context.Context) (Result, error) {
		mu.Lock()
		concurrent++
		if concurrent > 1 {
			mu.Unlock()
			return Result{}, errors.New("overlapped")
		}
		mu.Unlock()
		entered <- struct{}{}
		<-release
		mu.Lock()
		concurrent--
		mu.Unlock()
		return Result{}, nil
	})

	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); _, _ = r.RunNow(context.Background(), "slow") }()
	<-entered
	// Second invocation while the first is still inside the function.
	if _, err := r.RunNow(context.Background(), "slow"); err != nil {
		t.Fatalf("overlapping call returned an error instead of skipping: %v", err)
	}
	close(release)
	wg.Wait()
}

func TestNightlyScheduling(t *testing.T) {
	base := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	// 09:00 UTC has already passed today → tomorrow.
	if got := nextNightly(base, 540); !got.Equal(time.Date(2026, 8, 5, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("nextNightly after the hour = %v", got)
	}
	// 23:30 UTC is still ahead → today.
	if got := nextNightly(base, 23*60+30); !got.Equal(time.Date(2026, 8, 4, 23, 30, 0, 0, time.UTC)) {
		t.Fatalf("nextNightly before the hour = %v", got)
	}
	// Exactly at the minute → the NEXT day, never the instant we are already at (which would fire
	// again immediately and spin).
	at9 := time.Date(2026, 8, 4, 9, 0, 0, 0, time.UTC)
	if got := nextNightly(at9, 540); !got.Equal(time.Date(2026, 8, 5, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("nextNightly exactly on the minute = %v", got)
	}
}

func TestIntervalSchedulingCatchesUpOnceNotFortyTimes(t *testing.T) {
	every := 15 * time.Minute
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)

	// Normal cadence: advance from the previous due time, so a slightly late tick does not drift.
	prev := now.Add(-time.Minute)
	if got := nextInterval(now, &prev, every); !got.Equal(prev.Add(every)) {
		t.Fatalf("cadence not preserved: %v", got)
	}

	// Long outage: the previous due time is ten hours stale. The next fire must be ONE interval from
	// NOW, not forty accumulated windows in the past.
	stale := now.Add(-10 * time.Hour)
	got := nextInterval(now, &stale, every)
	if !got.Equal(now.Add(every)) {
		t.Fatalf("after an outage next = %v, want one interval from now (%v)", got, now.Add(every))
	}
	if got.Before(now) {
		t.Fatal("scheduled a fire time in the past")
	}
}

func TestNextRunFallsBackRatherThanSchedulingTheZeroTime(t *testing.T) {
	// A malformed schedule (kind without its parameter) must not end up with a zero next_run_at,
	// which would be permanently due and spin the runner.
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	got := nextRun(Schedule{Name: "broken", Kind: KindNightly}, now, nil)
	if !got.After(now) {
		t.Fatalf("nextRun for a malformed schedule = %v, want a time in the future", got)
	}
}
