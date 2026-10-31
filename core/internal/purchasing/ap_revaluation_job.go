package purchasing

import (
	"context"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/jobs"
)

// APRevaluationReversalJobName is the schedule row migration 0476 seeds and the worker registers.
const APRevaluationReversalJobName = "purchasing.ap_revaluation_reversal"

// APRevaluationReversalJob builds the worker's JobFunc: it reverses the posted month end runs whose
// next house day has begun. It never presses a run. The caller wraps the context in the system
// identity the jobs runner uses, as every sweep does.
func APRevaluationReversalJob(svc *Service) jobs.JobFunc {
	return func(ctx context.Context) (jobs.Result, error) {
		sweep, err := svc.ReverseDueAPRevaluations(ctx)
		return jobs.Result{
			RowsRead:    int64(sweep.Examined),
			RowsWritten: int64(sweep.Reversed),
			Detail: map[string]any{
				"reversed": sweep.Reversed, "not_yet_due": sweep.NotYetDue, "failed": sweep.Failed,
			},
		}, err
	}
}
