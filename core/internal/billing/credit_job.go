package billing

// credit_job.go, the DIB-65 nightly sweep as a jobs.JobFunc (schedule row 'ar.credit_hold_sweep',
// migration 0104, NIGHTLY at 08:00 UTC ≈ 04:00 Eastern).
//
// The function is exported as a constructor rather than wired here so that composition stays in the
// composition root, which is the same shape internal/tasks' stale-pick sweep takes.
//
// IDEMPOTENCY IS NOT OPTIONAL, and this is the file that has to honour it. The runner has no lease
// and no fencing token: a job still executing when its next window opens is simply STARTED AGAIN
// (internal/jobs' package doc says so in capitals). CreditService.Evaluate converges the hold set -
// PlaceHold is a no-op when an ACTIVE hold exists, enforced by ux_credit_holds_one_active rather than
// by a read-then-write, so a double start places nothing twice.
//
// The run is recorded SKIPPED, not SUCCEEDED, when auto-hold is flag-off. That distinction is the
// operator's answer to "did the sweep run last night?": SUCCEEDED means holds were placed, SKIPPED
// means the feature is off and the dry-run count in Detail is the blast radius of turning it on.
// Recording a dry run as SUCCEEDED would make the job_runs table say the policy is live when it is not.

import (
	"context"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/jobs"
)

// CreditHoldSweepJobName is the job_schedules.name this JobFunc registers under (0104).
const CreditHoldSweepJobName = "ar.credit_hold_sweep"

// CreditHoldSweepJob builds the JobFunc for the nightly AR credit-hold evaluation. branchID "" sweeps
// every branch under the org policy.
func CreditHoldSweepJob(svc *CreditService, branchID string) jobs.JobFunc {
	return func(ctx context.Context) (jobs.Result, error) {
		res, err := svc.Evaluate(ctx, branchID)
		if err != nil {
			return jobs.Result{}, err
		}
		return jobs.Result{
			RowsRead:    int64(res.Evaluated),
			RowsWritten: int64(res.Placed),
			Skipped:     res.DryRun,
			Detail: map[string]any{
				"dry_run":    res.DryRun,
				"would_hold": res.WouldHold,
				"placed":     res.Placed,
				"branch_id":  branchID,
			},
		}, nil
	}
}
