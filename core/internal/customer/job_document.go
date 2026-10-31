package customer

// job_document.go, the job a NEW DOCUMENT is opened on (wave 3 JA-2).
//
// A quote, an order or an invoice may name the job it is raised for. Every door that takes a job
// asks THIS read first, because the rules are the customer module's own (the job row is this
// module's table, and the module boundary holds: no other module reads customer_jobs itself).
//
// THE THREE REFUSALS ARE IN WORDS, not a bare not found. This differs from the job reads of JA-1
// on purpose. There the caller named a job cold, so an existence oracle was the leak to close.
// Here the caller has already been let through the customer (the document's own customer fence)
// and is choosing among THAT customer's jobs, so the three ways the choice can be wrong are the
// caller's own mistake, and the words are the answer:
//
//	a job of another customer  ->  "that job is another customer's job"
//	a CLOSED job               ->  "the job is closed, so it takes no new document"
//	a job out of the reach     ->  "that job sits at a branch outside your reach"
//
// A job id that is no job at all stays NOT FOUND: that is not a mistake the caller can act on
// differently, and the id space is uuid, so guessing one learns nothing.
//
// The reach leg needs a STAFF caller. The document doors are staff doors (the quote desk, the
// counter, the money desk), and the guard stamps the caller before the door runs. A context with
// no staff row (a portal session, a worker posting a document it was handed) is checked on the
// other two legs only: the customer fence is the session's own identity there, and the branch
// reach is a staff fact with no meaning without a staff row.

import (
	"context"
	"strings"

	"github.com/google/uuid"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// ErrJobNotOpenWords is the closed job's refusal as the document doors answer it. ErrJobClosed
// (job_service.go) is the job doors' own sentinel; a document door refuses before it owns the job,
// and its words name the document, so they are stated here.
const errJobNotOpenWords = "the job is closed, so it takes no new document"

// JobForNewDocument answers whether a job may carry a new document for a customer, and the job
// itself when it may. The three refusals above are the whole rule; nil means the job is the
// customer's, it is OPEN, and it sits inside the caller's reach.
func (s *Service) JobForNewDocument(ctx context.Context, customerID, jobID string) (Job, error) {
	if s.jobs == nil {
		return Job{}, ErrJobStoreNotConfigured
	}
	customerID = strings.TrimSpace(customerID)
	jobID = strings.TrimSpace(jobID)
	if customerID == "" {
		return Job{}, httpx.BadRequestf("customer id is required")
	}
	if _, err := uuid.Parse(customerID); err != nil {
		return Job{}, httpx.BadRequestf("customer id must be a uuid")
	}
	if jobID == "" {
		return Job{}, httpx.BadRequestf("job id is required")
	}
	if _, err := uuid.Parse(jobID); err != nil {
		return Job{}, httpx.BadRequestf("job id must be a uuid")
	}
	// The row by its own id: the customer and the status are checked HERE, in the service, against
	// the whole row, so the three refusals can tell the caller which one they hit. GetJobByID is
	// the unfenced read a write follows; this call is the fence itself, which is the front of every
	// door that reaches it.
	job, err := s.jobs.GetJobByID(ctx, jobID)
	if err != nil {
		return Job{}, err
	}
	if job.CustomerID != customerID {
		return Job{}, httpx.BadRequestf("that job is another customer's job")
	}
	if job.Status != JobStatusOpen {
		return Job{}, httpx.BadRequestf("%s", errJobNotOpenWords)
	}
	// The branch reach. A staff caller's grants are the reach; a context with no staff row has no
	// branch reach to hold the job against, and the two legs above are its whole fence.
	if caller, ok := identity.StaffContextFromContext(ctx); ok && caller.UserID != "" {
		if !caller.CanAccessBranch(job.BranchID) {
			return Job{}, httpx.BadRequestf("that job sits at a branch outside your reach")
		}
	}
	return job, nil
}
