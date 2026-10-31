package customer

// job_site.go, a job's site facts for the lien module's holdback rule (wave 3 RT-1).
//
// The job row is this module's table, so no other module reads customer_jobs: the lien module asks
// here. The read rides a caller's transaction (WithCallerTx), the way GetJobByID does, because RT-2
// asks for the holdback rule inside an invoice's write transaction.

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// JobByID reads one job by its own id with no reach or customer fence: the caller (the lien rule
// read) holds the job to its own fence. A job id that is not a uuid is a bad request in words; a uuid
// that is no job is not found.
func (s *Service) JobByID(ctx context.Context, jobID string) (Job, error) {
	if s.jobs == nil {
		return Job{}, ErrJobStoreNotConfigured
	}
	jobID = strings.TrimSpace(jobID)
	if _, err := uuid.Parse(jobID); err != nil {
		return Job{}, httpx.BadRequestf("job id must be a uuid")
	}
	job, err := s.jobs.GetJobByID(ctx, jobID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Job{}, jobNotFoundError{}
		}
		return Job{}, err
	}
	return job, nil
}

// SiteProvince is the province code (upper case) the job's ship to address names, "" when it names none.
func (j Job) SiteProvince() string {
	p, _ := j.ShipToAddress["province"].(string)
	return strings.ToUpper(strings.TrimSpace(p))
}
