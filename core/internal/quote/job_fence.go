package quote

// job_fence.go, the job a quote and its order are opened on (wave 3 JA-2).
//
// A quote may name the job it is written for, and the conversion may name the job its order is
// opened on. The job row is the customer module's own (customer_jobs, migration 0460), so the
// fence is asked through this narrow seam rather than read: the customer module refuses another
// customer's job, a CLOSED job and a job outside the caller's reach, in words, and this module
// never learns how those rules are decided.
//
// The seam is optional the way every seam here is: nil until the composition root binds it, and a
// caller that names a job against a service with no fence is REFUSED rather than waved through,
// because a job a fence never checked is a job nobody vouched for.

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// JobFence is the customer module's job read a new document is opened against. The error is the
// refusal, already in words; nil means the job may carry the document.
//
// THE TRANSACTION DISCIPLINE (QD5, PR 722 car 18's lesson): tx is the caller's transaction when the
// conversion runs inside one (the counter's payment door), and every database touch the fence makes
// rides it. A fence read that went to the pool there would wait for a second connection the pool
// cannot give on a pool of one. nil means no transaction is carried and the pool is right.
type JobFence interface {
	JobForNewDocument(ctx context.Context, tx pgx.Tx, customerID, jobID string) error
}

// WithJobFence binds the job fence to the staff surface (the quote desk's create and the counter's
// two conversion doors). A service without it takes no job: a named job is refused in words.
func (s *StaffService) WithJobFence(f JobFence) *StaffService { s.jobFence = f; return s }

// WithJobFence binds the job fence to the portal surface, whose conversions inherit the quote's
// own job.
func (s *Service) WithJobFence(f JobFence) *Service { s.jobFence = f; return s }

// quoteJobReader is the quote's own job column, read optionally so a store that predates JA-2
// keeps converting exactly as it did. *Repository implements it; the unit fakes do not, and their
// quotes carry no job. tx is the caller's transaction when one is carried, nil the pool.
type quoteJobReader interface {
	QuoteJobID(ctx context.Context, tx pgx.Tx, quoteID string) (string, error)
}

// ErrJobFenceNotWired is the refusal a named job answers with when the fence is not bound. Fail
// closed, the JA-1 rule: a door that cannot check a job is a door that takes none.
var ErrJobFenceNotWired = httpx.BadRequestf("the job fence is not wired, so no document can name a job here")

// resolveConvertJob fixes the job the conversion's order is opened on, and holds it through the
// fence before anything is written.
//
// THE REQUEST PICKS, ELSE THE QUOTE'S OWN JOB RIDES. A quote written for a job is work at that
// site, and the order it becomes is the same work; the desk may still name another job of the same
// customer at the door (a phased job split, a change of site), and the named job wins. Whichever
// id stands, it is fenced HERE, in the one function both conversion doors pass through, so a job
// that closed between the quote and its conversion refuses the conversion at either door, in the
// customer module's own words.
func resolveConvertJob(ctx context.Context, store convertStore, fence JobFence, customerID, quoteID, named string) (string, error) {
	tx, _ := txFrom(ctx)
	jobID := named
	if jobID == "" {
		if reader, ok := store.(quoteJobReader); ok {
			inherited, err := reader.QuoteJobID(ctx, tx, quoteID)
			if err != nil {
				return "", err
			}
			jobID = inherited
		}
	}
	if jobID == "" {
		return "", nil
	}
	if fence == nil {
		return "", ErrJobFenceNotWired
	}
	if err := fence.JobForNewDocument(ctx, tx, customerID, jobID); err != nil {
		return "", err
	}
	return jobID, nil
}
