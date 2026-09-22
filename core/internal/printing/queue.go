package printing

import (
	"context"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"github.com/google/uuid"
)

// queueStore is the print_queue persistence seam (satisfied by *Repository). Declared as an
// interface so the service's guards are unit-testable with no database, the pos/pricing idiom.
type queueStore interface {
	InsertJob(ctx context.Context, j Job) (Job, error)
	// MarkJobPrinted is a GUARDED write: it flips QUEUED → PRINTED in ONE statement
	// (UPDATE ... WHERE status='QUEUED'), so two concurrent marks cannot both succeed and the
	// print count stays one row per actual print. found=false means no such job at all.
	MarkJobPrinted(ctx context.Context, id string, at time.Time) (found, updated bool, err error)
	ListJobsForRef(ctx context.Context, docType, refID string) ([]Job, error)
	CountPrintedForRef(ctx context.Context, docType, refID string) (int, error)
}

// QueueService is the thin service seam over print_queue: Enqueue, MarkPrinted, ListForRef. It
// deliberately owns no rendering and no transport, agents and devices consume it later.
type QueueService struct {
	store queueStore
	now   func() time.Time
}

// NewQueueService builds the queue service over a store.
func NewQueueService(store queueStore) *QueueService {
	return &QueueService{store: store, now: time.Now}
}

// WithClock injects a deterministic clock (tests).
func (s *QueueService) WithClock(now func() time.Time) *QueueService {
	if now != nil {
		s.now = now
	}
	return s
}

// Enqueue records a request for paper. The doc_type/render_target enums are validated HERE, before
// the database CHECK: a caller that invents a doc type gets a 400 naming the value rather than a 500
// carrying a constraint name, and the Go enum and the 0088 CHECK can never silently diverge.
func (s *QueueService) Enqueue(ctx context.Context, in EnqueueInput) (Job, error) {
	if !docTypes[in.DocType] {
		return Job{}, httpx.BadRequestf("unknown print doc_type %q", in.DocType)
	}
	if !renderTargets[in.RenderTarget] {
		return Job{}, httpx.BadRequestf("unknown print render_target %q", in.RenderTarget)
	}
	if _, err := uuid.Parse(in.RefID); err != nil {
		return Job{}, httpx.BadRequestf("print ref_id must be a uuid")
	}
	if _, err := uuid.Parse(in.BranchID); err != nil {
		return Job{}, httpx.BadRequestf("print branch_id must be a uuid")
	}
	return s.store.InsertJob(ctx, Job{
		DocType:      in.DocType,
		RefID:        in.RefID,
		BranchID:     in.BranchID,
		RenderTarget: in.RenderTarget,
		Status:       StatusQueued,
		RequestedBy:  identity.StaffUserIDFromContext(ctx),
		RequestedAt:  s.now(),
	})
}

// MarkPrinted completes a job. Only a QUEUED job may be marked: a FAILED or already-PRINTED job has
// to be re-enqueued, never restamped. That is what makes the receipt's reprint count honest, one
// PRINTED row means exactly one print happened.
func (s *QueueService) MarkPrinted(ctx context.Context, jobID string) error {
	if _, err := uuid.Parse(jobID); err != nil {
		return ErrNotFound
	}
	found, updated, err := s.store.MarkJobPrinted(ctx, jobID, s.now())
	if err != nil {
		return err
	}
	if !found {
		return ErrNotFound
	}
	if !updated {
		return ErrNotQueued
	}
	return nil
}

// ListForRef returns the print history of one document, newest first, "who printed this, when, how
// many times". The reprint marker on a receipt is this list's length made visible.
func (s *QueueService) ListForRef(ctx context.Context, docType, refID string) ([]Job, error) {
	if !docTypes[docType] {
		return nil, httpx.BadRequestf("unknown print doc_type %q", docType)
	}
	if _, err := uuid.Parse(refID); err != nil {
		return nil, ErrNotFound
	}
	return s.store.ListJobsForRef(ctx, docType, refID)
}

// countPrinted is the reprint counter the receipt renderer reads BEFORE enqueuing its own job.
func (s *QueueService) countPrinted(ctx context.Context, docType, refID string) (int, error) {
	return s.store.CountPrintedForRef(ctx, docType, refID)
}
