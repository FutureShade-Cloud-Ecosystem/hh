package billing

// ar_by_job.go, JA-4: the AR aging by job.
//
// ONE DEFINITION OF WHAT IS OWED. The by job aging reads the same open_ar CTE as the account aging
// (agingOpenAR in credit_repository.go) and only changes the grouping: the account aging groups by
// customer, this groups by customer and job. The day rule, the as of rule, the holdback and the
// release rows are therefore the account aging's own, and the rows of one customer sum to that
// customer's row by construction, and every row together to ARBalanceCents (GL 1100) at the same
// reading.
//
// A document with no job (the finance charge is raised with no job, FC-1) and every non-invoice
// movement (an NSF fee, an adjustment, a write off) is ACCOUNT LEVEL: JobID "" and AccountLevel true.
// A credit memo is an invoice row that carries the job of the invoice it reverses (JA-2), so it nets
// that job and the account level charge stays at account level.

import (
	"context"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

var errBadJobID = httpx.BadRequestf("job_id must be a job id, pick a job from the customer's jobs")

// AgingJobRow is one customer's open balance on one job, or at account level (JobID "").
type AgingJobRow struct {
	CustomerID      string `json:"customer_id"`
	CustomerName    string `json:"customer_name"`
	AccountNo       string `json:"account_no"`
	JobID           string `json:"job_id"`
	JobNo           string `json:"job_no"`
	JobName         string `json:"job_name"`
	AccountLevel    bool   `json:"account_level"`
	CurrentCents    int64  `json:"current_cents"`
	Days30Cents     int64  `json:"days_30_cents"`
	Days60Cents     int64  `json:"days_60_cents"`
	Days60To90Cents int64  `json:"days_60_to_90_cents"`
	Days90PlusCents int64  `json:"days_90_plus_cents"`
	TotalCents      int64  `json:"total_cents"`
}

// agingByJobStore is the optional persistence seam, reached by assertion so creditStore (and every
// fake of it) is not widened.
type agingByJobStore interface {
	AgingByJobRowsIn(ctx context.Context, asOf time.Time, bucketDays int, branchID string, reach BranchReach, jobID string) ([]AgingJobRow, error)
}

// AgingByJob is Aging by job, inside the caller's branch reach. jobID "" returns every job and the
// account level rows; a job id returns that job's rows alone.
func (s *CreditService) AgingByJob(ctx context.Context, asOf time.Time, branchID, jobID string) ([]AgingJobRow, error) {
	jobID = strings.ToLower(strings.TrimSpace(jobID))
	if jobID != "" && !looksLikeUUIDish(jobID) {
		return nil, errBadJobID
	}
	reach, err := reachFromContext(ctx).narrow(branchID)
	if err != nil {
		return nil, err
	}
	pol, err := s.store.CreditPolicy(ctx, branchID)
	if err != nil {
		return nil, err
	}
	st, ok := s.store.(agingByJobStore)
	if !ok {
		return nil, ErrCustomerActivityNotConfigured
	}
	return st.AgingByJobRowsIn(ctx, asOf, pol.AgingBucketDays, branchID, reach, jobID)
}
