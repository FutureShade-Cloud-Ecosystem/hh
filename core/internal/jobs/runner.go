package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// runner.go, the tick loop. It does three things and nothing else: ask the store for a due schedule,
// run the registered function for it, and record what happened.

// defaultTick is how often the runner asks whether anything is due. It is NOT the resolution of a
// schedule, a NIGHTLY job fires within one tick of its minute, which for a nightly sweep is exact
// enough and keeps the idle cost to one indexed query a minute.
const defaultTick = time.Minute

// ErrUnknownJob, a schedule row names a job with no registered JobFunc. Reported at boot and on
// every claim; the run is recorded SKIPPED rather than silently dropped, because a schedule with no
// code behind it is a wiring bug an operator needs to see.
var ErrUnknownJob = errors.New("jobs: no function registered for schedule")

// store is the persistence the Runner needs (the real impl is *Repository; tests use a fake).
type store interface {
	ListSchedules(ctx context.Context) ([]Schedule, error)
	SeedNextRun(ctx context.Context, now time.Time) (int, error)
	ClaimDue(ctx context.Context, now time.Time) (Schedule, bool, error)
	StartRun(ctx context.Context, jobName, scheduleID, trigger string, scheduledAt *time.Time) (string, error)
	FinishRun(ctx context.Context, runID, status string, res Result, runErr error) error
	StampSchedule(ctx context.Context, scheduleID, status, lastError string) error
}

// Runner fires registered jobs on their schedule. Register everything BEFORE calling Run: the
// handler map is written by Register and read by the tick loop, so registering into a running Runner
// is a data race (the same rule the notify dispatcher has, for the same reason).
type Runner struct {
	store store
	log   *slog.Logger
	tick  time.Duration

	mu   sync.RWMutex
	jobs map[string]JobFunc
	// running guards against a job overlapping ITSELF within this process. It is not a distributed
	// lock and does not pretend to be one, see the package doc's single-instance note.
	running map[string]bool
}

// NewRunner builds the runner over a store.
func NewRunner(s store, log *slog.Logger) *Runner {
	return &Runner{store: s, log: log, tick: defaultTick, jobs: map[string]JobFunc{}, running: map[string]bool{}}
}

// WithTick overrides the poll interval (tests use a tiny value; <=0 is ignored).
func (r *Runner) WithTick(d time.Duration) *Runner {
	if d > 0 {
		r.tick = d
	}
	return r
}

// Register binds a JobFunc to a schedule name. A duplicate registration PANICS at wire time, two
// functions under one name means one of them silently never runs, and that must fail at boot rather
// than at 3am (the notify dispatcher's single-handler invariant, applied here).
func (r *Runner) Register(name string, fn JobFunc) *Runner {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.jobs[name]; dup {
		panic("jobs: duplicate registration for job " + name)
	}
	r.jobs[name] = fn
	return r
}

// Registered reports whether a name has a function (used by the boot check and by tests).
func (r *Runner) Registered(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.jobs[name]
	return ok
}

// CheckWiring compares the schedules in the database against the registered functions and reports
// both directions of mismatch. It is called by Run at start and is safe to call directly at boot.
//
// Neither direction is fatal, and both are logged at WARN: an orphan schedule is a row somebody added
// without deploying the code, and an unscheduled function is code deployed without its row. Crashing
// on either would make a partial deploy an outage; saying nothing would make it invisible.
func (r *Runner) CheckWiring(ctx context.Context) (orphanSchedules, unscheduledJobs []string) {
	schedules, err := r.store.ListSchedules(ctx)
	if err != nil {
		if r.log != nil {
			r.log.Warn("jobs: cannot read schedules", "err", err)
		}
		return nil, nil
	}
	seen := map[string]bool{}
	for _, s := range schedules {
		seen[s.Name] = true
		if !r.Registered(s.Name) {
			orphanSchedules = append(orphanSchedules, s.Name)
		}
	}
	r.mu.RLock()
	for name := range r.jobs {
		if !seen[name] {
			unscheduledJobs = append(unscheduledJobs, name)
		}
	}
	r.mu.RUnlock()

	if r.log != nil {
		for _, n := range orphanSchedules {
			r.log.Warn("jobs: schedule has no registered function, it will be recorded SKIPPED", "job", n)
		}
		for _, n := range unscheduledJobs {
			r.log.Warn("jobs: registered function has no schedule row, it will never fire", "job", n)
		}
	}
	return orphanSchedules, unscheduledJobs
}

// Run seeds any unscheduled next_run_at, checks the wiring, then drains due jobs every tick until ctx
// is cancelled. Safe to call in a goroutine.
func (r *Runner) Run(ctx context.Context) {
	if n, err := r.store.SeedNextRun(ctx, time.Now()); err != nil {
		if r.log != nil {
			r.log.Warn("jobs: cannot seed schedules", "err", err)
		}
	} else if n > 0 && r.log != nil {
		r.log.Info("jobs: seeded next run for schedules that had none", "count", n)
	}
	r.CheckWiring(ctx)
	if r.log != nil {
		r.log.Info("jobs: runner started (single instance, no leader election)", "tick", r.tick.String())
	}

	t := time.NewTicker(r.tick)
	defer t.Stop()
	r.RunDue(ctx)
	for {
		select {
		case <-ctx.Done():
			if r.log != nil {
				r.log.Info("jobs: runner stopped")
			}
			return
		case <-t.C:
			r.RunDue(ctx)
		}
	}
}

// RunDue claims and executes every schedule that is due right now, returning how many ran. Exposed
// for tests and for a one-shot drain. It stops at a generous bound so a misconfigured schedule that
// keeps re-arming itself in the past cannot spin this loop forever.
func (r *Runner) RunDue(ctx context.Context) int {
	const maxPerTick = 32
	ran := 0
	for ran < maxPerTick {
		if ctx.Err() != nil {
			return ran
		}
		s, ok, err := r.store.ClaimDue(ctx, time.Now())
		if err != nil {
			if r.log != nil {
				r.log.Error("jobs: claim failed", "err", err)
			}
			return ran
		}
		if !ok {
			return ran
		}
		r.execute(ctx, s, TriggerSchedule)
		ran++
	}
	if r.log != nil {
		r.log.Warn("jobs: hit the per-tick execution bound", "limit", maxPerTick)
	}
	return ran
}

// RunNow executes a registered job out of band (an operator asking for it, a test). It records a
// MANUAL-triggered run and does NOT touch any schedule's next_run_at, an on-demand run must not
// shift the nightly cadence.
func (r *Runner) RunNow(ctx context.Context, name string) (Result, error) {
	r.mu.RLock()
	fn, ok := r.jobs[name]
	r.mu.RUnlock()
	if !ok {
		return Result{}, fmt.Errorf("%w: %s", ErrUnknownJob, name)
	}
	return r.invoke(ctx, Schedule{Name: name}, fn, TriggerManual)
}

// execute runs one claimed schedule, recording a SKIPPED run when nothing is registered for it.
func (r *Runner) execute(ctx context.Context, s Schedule, trigger string) {
	r.mu.RLock()
	fn, ok := r.jobs[s.Name]
	r.mu.RUnlock()
	if !ok {
		// The window is already consumed (next_run_at was advanced at claim time), so this records
		// the miss rather than retrying it in a tight loop.
		runID, err := r.store.StartRun(ctx, s.Name, s.ID, trigger, s.NextRunAt)
		if err == nil {
			_ = r.store.FinishRun(ctx, runID, StatusSkipped, Result{}, ErrUnknownJob)
		}
		_ = r.store.StampSchedule(ctx, s.ID, StatusSkipped, ErrUnknownJob.Error())
		if r.log != nil {
			r.log.Warn("jobs: due schedule has no registered function", "job", s.Name)
		}
		return
	}
	if _, err := r.invoke(ctx, s, fn, trigger); err != nil && r.log != nil {
		r.log.Error("jobs: run failed", "job", s.Name, "err", err)
	}
}

// invoke opens the run row, calls the function with a panic barrier, and stamps the outcome.
//
// A panicking job is recovered and recorded FAILED. It must not unwind through the ticker goroutine
// and kill the runner, because that would silently stop every OTHER scheduled job in the process ,
// the same reasoning behind the notify dispatcher's per-observer recover.
func (r *Runner) invoke(ctx context.Context, s Schedule, fn JobFunc, trigger string) (res Result, err error) {
	r.mu.Lock()
	if r.running[s.Name] {
		r.mu.Unlock()
		if r.log != nil {
			r.log.Warn("jobs: previous run still in progress, skipping this window", "job", s.Name)
		}
		return Result{}, nil
	}
	r.running[s.Name] = true
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.running, s.Name)
		r.mu.Unlock()
	}()

	runID, startErr := r.store.StartRun(ctx, s.Name, s.ID, trigger, s.NextRunAt)
	if startErr != nil {
		return Result{}, startErr
	}

	func() {
		defer func() {
			if rec := recover(); rec != nil {
				err = fmt.Errorf("jobs: %s panicked: %v", s.Name, rec)
				if r.log != nil {
					r.log.Error("jobs: job panicked (recovered; runner continues)", "job", s.Name, "panic", rec)
				}
			}
		}()
		res, err = fn(ctx)
	}()

	status := StatusSucceeded
	switch {
	case err != nil:
		status = StatusFailed
	case res.Skipped:
		status = StatusSkipped
	}
	if ferr := r.store.FinishRun(ctx, runID, status, res, err); ferr != nil && r.log != nil {
		r.log.Error("jobs: cannot record run outcome", "job", s.Name, "err", ferr)
	}
	errText := ""
	if err != nil {
		errText = err.Error()
	}
	_ = r.store.StampSchedule(ctx, s.ID, status, errText)
	if err == nil && r.log != nil {
		r.log.Info("jobs: ran", "job", s.Name, "status", status,
			"rows_read", res.RowsRead, "rows_written", res.RowsWritten)
	}
	return res, err
}
