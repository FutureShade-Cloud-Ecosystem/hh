package printing

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// fakeQueueStore is the DB-free store seam (the pricing/pos unit-test idiom).
type fakeQueueStore struct {
	jobs     []Job
	markFind bool
	markUpd  bool
	markErr  error
	printed  int
}

func (f *fakeQueueStore) InsertJob(_ context.Context, j Job) (Job, error) {
	j.ID = "00000000-0000-4000-8000-00000000000" + string(rune('0'+len(f.jobs)))
	j.Status = StatusQueued
	f.jobs = append(f.jobs, j)
	return j, nil
}

func (f *fakeQueueStore) MarkJobPrinted(_ context.Context, _ string, _ time.Time) (bool, bool, error) {
	return f.markFind, f.markUpd, f.markErr
}

func (f *fakeQueueStore) ListJobsForRef(_ context.Context, _, _ string) ([]Job, error) {
	return f.jobs, nil
}

func (f *fakeQueueStore) CountPrintedForRef(_ context.Context, _, _ string) (int, error) {
	return f.printed, nil
}

const (
	testRefID    = "11111111-1111-4111-8111-111111111111"
	testBranchID = "22222222-2222-4222-8222-222222222222"
	testJobID    = "33333333-3333-4333-8333-333333333333"
)

func okEnqueue() EnqueueInput {
	return EnqueueInput{DocType: DocReceipt, RefID: testRefID, BranchID: testBranchID, RenderTarget: TargetBrowser}
}

// DIB-411: the doc_type/render_target enums are validated in Go BEFORE the 0088 CHECK, so an unknown
// value is a 400 naming it rather than a 500 carrying a constraint name, and the Go enum cannot
// silently drift from the database's.
func TestEnqueue_RejectsUnknownDocTypeAndTarget(t *testing.T) {
	svc := NewQueueService(&fakeQueueStore{})
	var br httpx.BadRequestError

	in := okEnqueue()
	in.DocType = "INVOICE_PDF" // plausible, and NOT in the 0088 enum
	if _, err := svc.Enqueue(context.Background(), in); !errors.As(err, &br) {
		t.Fatalf("unknown doc_type must be a bad request, got %v", err)
	}

	in = okEnqueue()
	in.RenderTarget = "PDF"
	if _, err := svc.Enqueue(context.Background(), in); !errors.As(err, &br) {
		t.Fatalf("unknown render_target must be a bad request, got %v", err)
	}

	// The three real doc types and both targets are accepted.
	for _, dt := range []string{DocReceipt, DocLabelZPL, DocDeliveryDoc} {
		in = okEnqueue()
		in.DocType = dt
		if _, err := svc.Enqueue(context.Background(), in); err != nil {
			t.Fatalf("doc_type %s must enqueue: %v", dt, err)
		}
	}
}

// A non-uuid ref/branch is refused at the boundary, never handed to Postgres as a 22P02 → 500.
func TestEnqueue_RejectsNonUUIDRefs(t *testing.T) {
	svc := NewQueueService(&fakeQueueStore{})
	var br httpx.BadRequestError
	in := okEnqueue()
	in.RefID = "not-a-uuid"
	if _, err := svc.Enqueue(context.Background(), in); !errors.As(err, &br) {
		t.Fatalf("non-uuid ref_id must be a bad request, got %v", err)
	}
	in = okEnqueue()
	in.BranchID = "branch-1"
	if _, err := svc.Enqueue(context.Background(), in); !errors.As(err, &br) {
		t.Fatalf("non-uuid branch_id must be a bad request, got %v", err)
	}
}

// DIB-411: MarkPrinted moves QUEUED → PRINTED and NOTHING else. A job that is already PRINTED (or
// FAILED) must be re-enqueued rather than restamped, because the receipt's reprint count is
// "how many PRINTED rows exist", restamping would let one print report itself twice, or two prints
// collapse into one.
func TestMarkPrinted_OnlyFromQueued(t *testing.T) {
	store := &fakeQueueStore{markFind: true, markUpd: true}
	svc := NewQueueService(store)
	if err := svc.MarkPrinted(context.Background(), testJobID); err != nil {
		t.Fatalf("a QUEUED job must mark printed: %v", err)
	}

	// The guarded UPDATE matched nothing but the row exists → the job was not QUEUED.
	store.markUpd = false
	if err := svc.MarkPrinted(context.Background(), testJobID); !errors.Is(err, ErrNotQueued) {
		t.Fatalf("a non-QUEUED job must refuse with ErrNotQueued, got %v", err)
	}

	// No such row at all → not found, not a false "already printed".
	store.markFind = false
	if err := svc.MarkPrinted(context.Background(), testJobID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an unknown job must be ErrNotFound, got %v", err)
	}
	// A malformed id never reaches the database.
	if err := svc.MarkPrinted(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a non-uuid job id must be ErrNotFound, got %v", err)
	}
}

func TestListForRef_ValidatesDocType(t *testing.T) {
	svc := NewQueueService(&fakeQueueStore{})
	var br httpx.BadRequestError
	if _, err := svc.ListForRef(context.Background(), "POSTER", testRefID); !errors.As(err, &br) {
		t.Fatalf("unknown doc_type must be a bad request, got %v", err)
	}
	if _, err := svc.ListForRef(context.Background(), DocReceipt, "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("non-uuid ref must be ErrNotFound, got %v", err)
	}
	if _, err := svc.ListForRef(context.Background(), DocReceipt, testRefID); err != nil {
		t.Fatalf("a valid list must succeed: %v", err)
	}
}
