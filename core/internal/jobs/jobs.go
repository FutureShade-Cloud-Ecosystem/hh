// Package jobs is the scheduler this ERP did not have: a minimal, shared, cron-style runner that
// fires registered work on a clock and records every attempt.
//
// WHY IT EXISTS. Before this, anything periodic grew its own goroutine and ticker in main.go, the
// notify dispatcher, the messaging retention sweep and the approvals reconciler each did exactly
// that. Three tickers is a pattern; the fourth is a problem, because none of them records whether it
// ran, none can be disabled without a deploy, and none leaves an operator anything to look at when
// somebody asks "did the nightly sweep happen last night?". This runner answers that question from a
// table (job_runs, migration 0096).
//
// -------------------------------------------------------------------------------------------------
// WHAT IT IS NOT, STATED PLAINLY.
//
//	SINGLE INSTANCE. NO LEADER ELECTION. The runner claims a due schedule with one atomic conditional
//	UPDATE, so two processes racing the same due window cannot both win it. That is single-FLIGHT, and
//	it is NOT the same as being safe to run N replicas:
//	  * There is no lease and no fencing token. A job still running when its next window opens is
//	    simply STARTED AGAIN. Every registered job must be idempotent and short.
//	  * A process that dies mid-run leaves its job_runs row RUNNING forever. Nothing reaps it. It is a
//	    tombstone to read, not a lock that blocks the next run.
//	  * Schedules are evaluated in UTC. A NIGHTLY job fires at a fixed UTC minute and does NOT follow
//	    Ontario's DST shift.
//	Run ONE instance with the runner enabled. Multi-instance safety means advisory locks or a real
//	leader election, and that is separate, explicit work, not something to assume from this file.
//
//	NOT A CRON PARSER. Two kinds only: NIGHTLY (a fixed UTC minute-of-day) and INTERVAL (every N
//	seconds). "Every second Tuesday" needs a real expression parser; the schema's CHECK makes the
//	omission loud instead of silently accepting a cron string it would ignore.
//
// -------------------------------------------------------------------------------------------------
//
// RELATIONSHIP TO THE MIGRATION TRACK. job_runs is deliberately shape-compatible with sync_jobs
// (0079, internal/bistrack), same statuses, same run-accounting columns, so the future BisTrack
// sync worker can be scheduled by this runner and record attempts in the shape it already uses. The
// SHAPE is shared; the implementation is not. This package never reads or writes sync_jobs.
package jobs

import (
	"context"
	"time"
)

// Kinds of schedule (0096 CHECK).
const (
	KindNightly  = "NIGHTLY"
	KindInterval = "INTERVAL"
)

// Run statuses (0096 CHECK, matching sync_jobs' vocabulary).
const (
	StatusPending   = "PENDING"
	StatusRunning   = "RUNNING"
	StatusSucceeded = "SUCCEEDED"
	StatusFailed    = "FAILED"
	StatusSkipped   = "SKIPPED"
)

// Triggers (0096 CHECK).
const (
	TriggerSchedule = "SCHEDULE"
	TriggerManual   = "MANUAL"
)

// Schedule is one job_schedules row: what to run and when.
type Schedule struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	Kind          string     `json:"kind"`
	NightlyMinute *int       `json:"nightly_minute,omitempty"` // minutes past midnight UTC (NIGHTLY)
	IntervalSecs  *int       `json:"interval_secs,omitempty"`  // fire period (INTERVAL)
	Enabled       bool       `json:"enabled"`
	NextRunAt     *time.Time `json:"next_run_at,omitempty"`
	LastRunAt     *time.Time `json:"last_run_at,omitempty"`
	LastStatus    string     `json:"last_status,omitempty"`
	LastError     string     `json:"last_error,omitempty"`
}

// Result is what a job reports back. RowsRead / RowsWritten are the sync_jobs-shaped accounting
// columns; Detail is free-form JSON for anything else worth keeping (it must not contain secrets).
type Result struct {
	RowsRead    int64
	RowsWritten int64
	Detail      map[string]any
	// Skipped marks a run that legitimately did nothing and should be recorded SKIPPED rather than
	// SUCCEEDED, e.g. a sweep that found the feature disabled. It is not an error.
	Skipped bool
}

// JobFunc is one unit of scheduled work. It MUST be idempotent (see the no-lease note above) and it
// MUST respect ctx cancellation, the runner cancels on shutdown and the process will not wait.
type JobFunc func(ctx context.Context) (Result, error)

// nextNightly returns the next occurrence of minuteOfDay (minutes past midnight UTC) strictly after
// `from`. Exported through the runner's scheduling only; kept here as a pure function so the wrap
// around midnight is testable without a clock or a database.
func nextNightly(from time.Time, minuteOfDay int) time.Time {
	u := from.UTC()
	today := time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC).Add(time.Duration(minuteOfDay) * time.Minute)
	if today.After(u) {
		return today
	}
	return today.Add(24 * time.Hour)
}

// nextInterval returns the next fire time for a fixed-period schedule.
//
// It advances from the PREVIOUS due time when that is known, so a runner that was down for an hour
// does not permanently shift a 15-minute job off its cadence, but it never returns a time in the
// past, so a long outage produces ONE catch-up run rather than a backlog of missed ones. Firing
// forty accumulated sweeps the moment a server comes back is how a "harmless" housekeeping job takes
// out a database.
func nextInterval(from time.Time, prevDue *time.Time, every time.Duration) time.Time {
	if every <= 0 {
		every = time.Minute
	}
	base := from.UTC()
	if prevDue != nil {
		next := prevDue.UTC().Add(every)
		if next.After(base) {
			return next
		}
	}
	return base.Add(every)
}

// nextRun computes a schedule's next fire time from `now`. prevDue is the schedule's current
// next_run_at (nil on a schedule that has never been scheduled).
func nextRun(s Schedule, now time.Time, prevDue *time.Time) time.Time {
	if s.Kind == KindNightly && s.NightlyMinute != nil {
		return nextNightly(now, *s.NightlyMinute)
	}
	if s.Kind == KindInterval && s.IntervalSecs != nil {
		return nextInterval(now, prevDue, time.Duration(*s.IntervalSecs)*time.Second)
	}
	// A schedule whose parameters do not match its kind cannot be scheduled. The 0096 CHECK makes
	// this unreachable from the database; the fallback keeps a hand-built Schedule from scheduling
	// itself at the zero time and firing forever.
	return now.UTC().Add(time.Hour)
}
