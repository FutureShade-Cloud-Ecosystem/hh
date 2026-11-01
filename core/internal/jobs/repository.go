package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// repository.go, job_schedules / job_runs persistence (0096).
//
// The claim is the only interesting part. ClaimDue advances a due schedule's next_run_at in ONE
// atomic conditional UPDATE with RETURNING:
//
//	UPDATE job_schedules SET next_run_at = <computed> WHERE id = $1 AND next_run_at <= now() ...
//
// Two processes racing the same window: the second one's WHERE no longer matches, so it returns no
// row and does not run the job. That is single-flight per due window, NOT leader election, and NOT a
// lease (see the package doc). It is enough to stop a duplicate run at the same instant, and it is
// explicitly not enough to make N replicas safe.

var errNoDB = errors.New("jobs: database not configured")

// Repository owns all job_schedules + job_runs persistence.
type Repository struct{ pool *pgxpool.Pool }

// NewRepository builds the store; a nil pool makes every method a safe no-op so the server boots.
func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

const scheduleColumns = `id::text, name, kind, nightly_minute, interval_secs, enabled,
	next_run_at, last_run_at, last_status, last_error`

func scanSchedule(row interface{ Scan(...any) error }) (Schedule, error) {
	var s Schedule
	if err := row.Scan(&s.ID, &s.Name, &s.Kind, &s.NightlyMinute, &s.IntervalSecs, &s.Enabled,
		&s.NextRunAt, &s.LastRunAt, &s.LastStatus, &s.LastError); err != nil {
		return Schedule{}, err
	}
	return s, nil
}

// ListSchedules returns every schedule, enabled or not (the boot check reads this to report a
// schedule with no registered JobFunc).
func (r *Repository) ListSchedules(ctx context.Context) ([]Schedule, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx, `SELECT `+scheduleColumns+` FROM job_schedules ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("jobs: list schedules: %w", err)
	}
	defer rows.Close()
	out := []Schedule{}
	for rows.Next() {
		s, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// SeedNextRun sets next_run_at on any enabled schedule that has none, so a freshly migrated database
// starts firing on its own cadence rather than immediately. A schedule with a NULL next_run_at would
// otherwise be either never due or always due, depending on how the comparison is written, this
// removes the ambiguity at boot instead of hiding it in a query.
func (r *Repository) SeedNextRun(ctx context.Context, now time.Time) (int, error) {
	if r.pool == nil {
		return 0, errNoDB
	}
	schedules, err := r.ListSchedules(ctx)
	if err != nil {
		return 0, err
	}
	seeded := 0
	for _, s := range schedules {
		if !s.Enabled || s.NextRunAt != nil {
			continue
		}
		next := nextRun(s, now, nil)
		if _, err := r.pool.Exec(ctx,
			`UPDATE job_schedules SET next_run_at = $2, updated_at = now() WHERE id = $1 AND next_run_at IS NULL`,
			s.ID, next); err != nil {
			return seeded, fmt.Errorf("jobs: seed next_run_at for %q: %w", s.Name, err)
		}
		seeded++
	}
	return seeded, nil
}

// ClaimDue atomically claims the single oldest-due enabled schedule and advances its next_run_at,
// returning it. No due schedule → (Schedule{}, false, nil).
//
// WHAT PROVIDES SINGLE-FLIGHT, precisely, the two statements run in one transaction:
//
//	SELECT … FOR UPDATE SKIP LOCKED  is the mechanism. A second claimer racing the same window finds
//	                                 the row locked, SKIPS it, and returns "nothing due", it neither
//	                                 blocks nor claims. This is what the deterministic interleave test
//	                                 (TestIntegrationClaimSkipsAScheduleAnotherTransactionHasLocked)
//	                                 pins.
//	UPDATE … WHERE next_run_at <= $3  is a second line of defence for the case where a candidate was
//	                                 chosen without that lock. The lock makes it unreachable in normal
//	                                 operation, which is precisely why it has no test of its own:
//	                                 mutation testing showed removing it changes no observable
//	                                 behaviour while the lock stands. It is kept deliberately, and
//	                                 described honestly, rather than presented as a tested guard.
//
// Neither of these is leader election, and neither makes N replicas safe, see the package doc.
func (r *Repository) ClaimDue(ctx context.Context, now time.Time) (Schedule, bool, error) {
	if r.pool == nil {
		return Schedule{}, false, errNoDB
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Schedule{}, false, fmt.Errorf("jobs: begin claim: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	s, err := scanSchedule(tx.QueryRow(ctx, `SELECT `+scheduleColumns+` FROM job_schedules
		WHERE enabled AND next_run_at IS NOT NULL AND next_run_at <= $1
		ORDER BY next_run_at
		LIMIT 1
		FOR UPDATE SKIP LOCKED`, now))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Schedule{}, false, nil
		}
		return Schedule{}, false, fmt.Errorf("jobs: claim: %w", err)
	}

	next := nextRun(s, now, s.NextRunAt)
	tag, err := tx.Exec(ctx, `UPDATE job_schedules
		SET next_run_at = $2, last_run_at = $3, last_status = 'RUNNING', updated_at = now()
		WHERE id = $1 AND next_run_at <= $3`, s.ID, next, now)
	if err != nil {
		return Schedule{}, false, fmt.Errorf("jobs: advance %q: %w", s.Name, err)
	}
	if tag.RowsAffected() == 0 {
		// Somebody else won this window between the SELECT and the UPDATE.
		return Schedule{}, false, nil
	}
	if err := tx.Commit(ctx); err != nil {
		return Schedule{}, false, fmt.Errorf("jobs: commit claim: %w", err)
	}
	s.NextRunAt = &next
	return s, true, nil
}

// StartRun opens a job_runs row in RUNNING and returns its id.
func (r *Repository) StartRun(ctx context.Context, jobName, scheduleID, trigger string, scheduledAt *time.Time) (string, error) {
	if r.pool == nil {
		return "", errNoDB
	}
	var id string
	err := r.pool.QueryRow(ctx, `
		INSERT INTO job_runs (job_name, schedule_id, trigger, status, scheduled_at, started_at)
		VALUES ($1, NULLIF($2,'')::uuid, $3, 'RUNNING', $4, now())
		RETURNING id::text`, jobName, scheduleID, trigger, scheduledAt).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("jobs: start run %q: %w", jobName, err)
	}
	return id, nil
}

// FinishRun stamps a run's terminal status exactly once. A finished row is never edited again.
func (r *Repository) FinishRun(ctx context.Context, runID, status string, res Result, runErr error) error {
	if r.pool == nil {
		return errNoDB
	}
	msg := ""
	if runErr != nil {
		msg = runErr.Error()
		if len(msg) > 2000 {
			msg = msg[:2000]
		}
	}
	detail := []byte("{}")
	if len(res.Detail) > 0 {
		if b, err := json.Marshal(res.Detail); err == nil {
			detail = b
		}
	}
	_, err := r.pool.Exec(ctx, `
		UPDATE job_runs
		SET status = $2, rows_read = $3, rows_written = $4, error = $5, detail = $6, finished_at = now()
		WHERE id = $1 AND finished_at IS NULL`,
		runID, status, res.RowsRead, res.RowsWritten, msg, detail)
	if err != nil {
		return fmt.Errorf("jobs: finish run: %w", err)
	}
	return nil
}

// StampSchedule records a completed run's outcome on the schedule (the "did it work last night?"
// read). The schedule's next_run_at was already advanced at claim time and is NOT touched here.
func (r *Repository) StampSchedule(ctx context.Context, scheduleID, status, lastError string) error {
	if r.pool == nil || scheduleID == "" {
		return nil
	}
	if len(lastError) > 2000 {
		lastError = lastError[:2000]
	}
	_, err := r.pool.Exec(ctx,
		`UPDATE job_schedules SET last_status = $2, last_error = $3, updated_at = now() WHERE id = $1`,
		scheduleID, status, lastError)
	if err != nil {
		return fmt.Errorf("jobs: stamp schedule: %w", err)
	}
	return nil
}

// RecentRuns returns one job's most recent attempts, newest first, the observability read.
func (r *Repository) RecentRuns(ctx context.Context, jobName string, limit int) ([]RunRecord, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id::text, job_name, trigger, status, rows_read, rows_written, error, started_at, finished_at
		FROM job_runs WHERE job_name = $1 ORDER BY created_at DESC LIMIT $2`, jobName, limit)
	if err != nil {
		return nil, fmt.Errorf("jobs: recent runs: %w", err)
	}
	defer rows.Close()
	out := []RunRecord{}
	for rows.Next() {
		var rec RunRecord
		if err := rows.Scan(&rec.ID, &rec.JobName, &rec.Trigger, &rec.Status,
			&rec.RowsRead, &rec.RowsWritten, &rec.Error, &rec.StartedAt, &rec.FinishedAt); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// RunRecord is one job_runs row as read back.
type RunRecord struct {
	ID          string     `json:"id"`
	JobName     string     `json:"job_name"`
	Trigger     string     `json:"trigger"`
	Status      string     `json:"status"`
	RowsRead    int64      `json:"rows_read"`
	RowsWritten int64      `json:"rows_written"`
	Error       string     `json:"error,omitempty"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
}

// ErrNoSuchSchedule is the answer for a schedule name with no job_schedules row.
var ErrNoSuchSchedule = errors.New("jobs: no schedule row with that name")

// ScheduleChange is the caller's record of a schedule edit, run on the edit's own transaction so the
// edit and its record commit or roll back together. A returned error aborts the edit.
type ScheduleChange func(ctx context.Context, tx pgx.Tx, from, to Schedule) error

// SchedulesNamed returns the named schedules, in name order. A name with no row is simply absent.
func (r *Repository) SchedulesNamed(ctx context.Context, names []string) ([]Schedule, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx,
		`SELECT `+scheduleColumns+` FROM job_schedules WHERE name = ANY($1::text[]) ORDER BY name`, names)
	if err != nil {
		return nil, fmt.Errorf("jobs: read schedules: %w", err)
	}
	defer rows.Close()
	out := []Schedule{}
	for rows.Next() {
		s, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// SetEnabled turns one named schedule on or off and answers the row before and after. Switching it from
// off to on gives it its next slot from now when it has none or its slot is already past, so a running
// worker claims it on a later tick with no restart and no burst for an old stamp; a slot still in the
// future is kept. Enabling a row that is already on changes nothing: its slot, even a stale one, is left
// for the running worker to claim on its next tick. Turning it off only clears the flag: ClaimDue never claims a disabled
// row. record, when given, runs on the same transaction before commit.
func (r *Repository) SetEnabled(ctx context.Context, name string, enabled bool, now time.Time, record ScheduleChange) (Schedule, Schedule, error) {
	if r.pool == nil {
		return Schedule{}, Schedule{}, errNoDB
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Schedule{}, Schedule{}, fmt.Errorf("jobs: begin schedule edit: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	from, err := scanSchedule(tx.QueryRow(ctx,
		`SELECT `+scheduleColumns+` FROM job_schedules WHERE name = $1 FOR UPDATE`, name))
	if errors.Is(err, pgx.ErrNoRows) {
		return Schedule{}, Schedule{}, fmt.Errorf("%w: %s", ErrNoSuchSchedule, name)
	}
	if err != nil {
		return Schedule{}, Schedule{}, fmt.Errorf("jobs: read schedule %q: %w", name, err)
	}
	next := from.NextRunAt
	if enabled && !from.Enabled && (next == nil || !next.After(now)) {
		n := nextRun(from, now, nil)
		next = &n
	}
	to, err := scanSchedule(tx.QueryRow(ctx,
		`UPDATE job_schedules SET enabled = $2, next_run_at = $3, updated_at = now() WHERE id = $1
		 RETURNING `+scheduleColumns, from.ID, enabled, next))
	if err != nil {
		return Schedule{}, Schedule{}, fmt.Errorf("jobs: edit schedule %q: %w", name, err)
	}
	if record != nil {
		if err := record(ctx, tx, from, to); err != nil {
			return Schedule{}, Schedule{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Schedule{}, Schedule{}, fmt.Errorf("jobs: commit schedule edit: %w", err)
	}
	return from, to, nil
}
