package billing

// finance_job_door.go, wave 3 FC-1c: THE CONTROLLER'S RECORDED DOOR TO ENABLE OR DISABLE A FINANCE JOB.
//
// The finance charge night job (0477) is seeded off, because it raises customer facing invoices by
// itself and the Controller opts in. Until now opting in was an UPDATE by hand. This is the door: list
// the finance jobs with their schedule state, and turn one on or off.
//
// -- THE ALLOWLIST ----------------------------------------------------------------------
// Only the names the composition root passes are reachable (the finance charge run and the AP
// revaluation reversal). Any other name, a real job of another module included, is a 404 in words:
// this door never becomes a general switch for the job runner.
//
// -- ENABLE AND DISABLE -----------------------------------------------------------------
// Enable gives the job its next slot from now when it has none or its slot is past (jobs.SetEnabled),
// so a running worker claims it on the first tick that finds the slot due, with no restart. Disable
// only clears the flag; the runner never claims a disabled row.
//
// -- RECORDED IN THE SAME TRANSACTION ---------------------------------------------------
// Who, which job, and the flag from and to are recorded with RecordTx on the edit's own transaction,
// so a record that fails leaves the schedule as it was. A recorder without the seam records after the
// commit, best effort (test stubs only; production passes the real audit service).
//
// -- THE GATE ---------------------------------------------------------------------------
// The same leadership tier narrowed to the Controller roles as the run's doors (HR and others are
// refused in words). Unlike the waiver, the General Manager may toggle a job.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/jobs"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// ActFinanceJobEnabledSet is the audit verb of a job toggle.
const ActFinanceJobEnabledSet = "FINANCE_JOB_ENABLED_SET"

// FinanceJobStore is the slice of the jobs repository the door needs.
type FinanceJobStore interface {
	SchedulesNamed(ctx context.Context, names []string) ([]jobs.Schedule, error)
	SetEnabled(ctx context.Context, name string, enabled bool, now time.Time, record jobs.ScheduleChange) (jobs.Schedule, jobs.Schedule, error)
}

// FinanceJobView is one finance job's schedule state as a read answers it.
type FinanceJobView struct {
	Name       string `json:"name"`
	Enabled    bool   `json:"enabled"`
	NextRunAt  string `json:"next_run_at,omitempty"`
	LastRunAt  string `json:"last_run_at,omitempty"`
	LastStatus string `json:"last_status,omitempty"`
	LastError  string `json:"last_error,omitempty"`
}

func financeJobView(s jobs.Schedule) FinanceJobView {
	v := FinanceJobView{Name: s.Name, Enabled: s.Enabled, LastStatus: s.LastStatus, LastError: s.LastError}
	if s.NextRunAt != nil {
		v.NextRunAt = s.NextRunAt.UTC().Format(time.RFC3339)
	}
	if s.LastRunAt != nil {
		v.LastRunAt = s.LastRunAt.UTC().Format(time.RFC3339)
	}
	return v
}

// ErrFinanceJobsNotWired is the answer when the composition root bound no jobs store.
var ErrFinanceJobsNotWired = errors.New("billing: the finance job door is not configured")

// WithJobs binds the door's jobs store, the allowlist of job names it may touch, and the audit
// recorder the toggles are recorded through.
func (s *FinanceChargeService) WithJobs(store FinanceJobStore, names []string, rec audit.Recorder) *FinanceChargeService {
	s.jobs = store
	s.jobNames = append([]string(nil), names...)
	s.jobAudit = rec
	return s
}

func (s *FinanceChargeService) jobAllowed(name string) bool {
	for _, n := range s.jobNames {
		if n == name {
			return true
		}
	}
	return false
}

// Jobs lists the allowlisted finance jobs with their schedule state, in name order.
func (s *FinanceChargeService) Jobs(ctx context.Context) ([]FinanceJobView, error) {
	if _, err := s.caller(ctx); err != nil {
		return nil, err
	}
	if s.jobs == nil {
		return nil, ErrFinanceJobsNotWired
	}
	rows, err := s.jobs.SchedulesNamed(ctx, s.jobNames)
	if err != nil {
		return nil, fmt.Errorf("billing: read the finance jobs: %w", err)
	}
	out := make([]FinanceJobView, 0, len(rows))
	for _, r := range rows {
		out = append(out, financeJobView(r))
	}
	return out, nil
}

// SetJobEnabled turns one allowlisted finance job on or off, recorded in the edit's transaction.
func (s *FinanceChargeService) SetJobEnabled(ctx context.Context, name string, enabled bool) (FinanceJobView, error) {
	c, err := s.caller(ctx)
	if err != nil {
		return FinanceJobView{}, err
	}
	if s.jobs == nil {
		return FinanceJobView{}, ErrFinanceJobsNotWired
	}
	if !s.jobAllowed(name) {
		return FinanceJobView{}, httpx.NotFoundError{Msg: fmt.Sprintf("billing: %q is not a finance job this door may switch", strings.TrimSpace(name))}
	}
	var act audit.SensitiveAccess
	rx, inTx := s.jobAudit.(creditChangeAuditTxRecorder)
	_, to, err := s.jobs.SetEnabled(ctx, name, enabled, time.Now(), func(ctx context.Context, tx pgx.Tx, from, to jobs.Schedule) error {
		act = audit.SensitiveAccess{
			Action: ActFinanceJobEnabledSet, EntityType: "job_schedule", EntityID: to.ID,
			ActorID: c.UserID, Purpose: ActFinanceJobEnabledSet,
			Extra: map[string]any{"job": to.Name, "enabled_from": from.Enabled, "enabled_to": to.Enabled},
		}
		if inTx && s.jobAudit != nil {
			if err := rx.RecordTx(ctx, tx, act); err != nil {
				return fmt.Errorf("billing: record the finance job switch: %w", err)
			}
		}
		return nil
	})
	if errors.Is(err, jobs.ErrNoSuchSchedule) {
		return FinanceJobView{}, httpx.NotFoundError{Msg: fmt.Sprintf("billing: %q has no schedule row", name)}
	}
	if err != nil {
		return FinanceJobView{}, err
	}
	if s.jobAudit != nil && !inTx {
		_ = s.jobAudit.Record(ctx, act)
	}
	return financeJobView(to), nil
}
