package compliance

import (
	"context"
	"testing"
	"time"
)

// fakeStore is an in-memory complianceStore.
type fakeStore struct {
	deadlines []Deadline
	reviews   map[string]ReviewStatus
	seq       int
}

func newFakeStore() *fakeStore { return &fakeStore{reviews: map[string]ReviewStatus{}} }

func (f *fakeStore) UpsertDeadline(_ context.Context, jobID, kind, dueDate string, ruleDays int) (bool, error) {
	for _, d := range f.deadlines {
		if d.JobID == jobID && d.Kind == kind && d.DueDate == dueDate {
			return false, nil // idempotent duplicate
		}
	}
	f.seq++
	f.deadlines = append(f.deadlines, Deadline{ID: itoa(f.seq), JobID: jobID, Kind: kind, DueDate: dueDate, RuleDays: ruleDays, Status: "OPEN"})
	return true, nil
}
func (f *fakeStore) OpenDeadlines(_ context.Context) ([]Deadline, error) {
	var out []Deadline
	for _, d := range f.deadlines {
		if d.Status == "OPEN" {
			out = append(out, d)
		}
	}
	return out, nil
}
func (f *fakeStore) ListDeadlines(_ context.Context, status string) ([]Deadline, error) {
	var out []Deadline
	for _, d := range f.deadlines {
		if status == "" || d.Status == status {
			out = append(out, d)
		}
	}
	return out, nil
}
func (f *fakeStore) GetReviewStatus(_ context.Context, st, id string) (ReviewStatus, bool, error) {
	rs, ok := f.reviews[st+"|"+id]
	return rs, ok, nil
}
func (f *fakeStore) UpsertReviewStatus(_ context.Context, in ReviewStatusInput, by string) (ReviewStatus, error) {
	rs := ReviewStatus{SubjectType: in.SubjectType, SubjectID: in.SubjectID, State: in.State, BannerText: in.BannerText, ReviewedBy: by}
	f.reviews[in.SubjectType+"|"+in.SubjectID] = rs
	return rs, nil
}

func newSvc(st complianceStore) *Service {
	return NewService(nil).withStore(st).withClock(func() time.Time { return time.Date(2026, 7, 7, 0, 0, 0, 0, time.UTC) })
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// -- AC: compliance registry (idempotent RegisterDeadline) -------------------------

func TestRegisterDeadline_Idempotent(t *testing.T) {
	st := newFakeStore()
	svc := newSvc(st)
	ctx := context.Background()
	if err := svc.RegisterDeadline(ctx, "job1", "HOLDBACK_RELEASE", "2026-08-21", 45); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := svc.RegisterDeadline(ctx, "job1", "HOLDBACK_RELEASE", "2026-08-21", 45); err != nil {
		t.Fatalf("re-register: %v", err)
	}
	all, _ := st.ListDeadlines(ctx, "")
	if len(all) != 1 {
		t.Fatalf("deadlines=%d, want 1 (idempotent)", len(all))
	}
}

// -- AC: upcoming horizon + OVERDUE derivation -------------------------------------

func TestUpcoming_HorizonAndOverdue(t *testing.T) {
	st := newFakeStore()
	// asOf is 2026-07-07.
	st.deadlines = []Deadline{
		{ID: "1", JobID: "a", Kind: "HOLDBACK_RELEASE", DueDate: "2026-07-01", Status: "OPEN"}, // overdue
		{ID: "2", JobID: "b", Kind: "HOLDBACK_RELEASE", DueDate: "2026-07-20", Status: "OPEN"}, // within 45d
		{ID: "3", JobID: "c", Kind: "HOLDBACK_RELEASE", DueDate: "2026-12-31", Status: "OPEN"}, // beyond horizon
	}
	svc := newSvc(st)
	items, err := svc.Upcoming(context.Background(), 45)
	if err != nil {
		t.Fatalf("upcoming: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("upcoming=%d, want 2 (excludes the Dec deadline)", len(items))
	}
	if items[0].DueDate != "2026-07-01" || items[0].Status != "OVERDUE" {
		t.Fatalf("first item %+v, want the overdue Jul-01 marked OVERDUE", items[0])
	}
}

// -- AC: shared, data-driven review-status banner ----------------------------------

func TestReviewStatus_DefaultDraftAndUpsert(t *testing.T) {
	st := newFakeStore()
	svc := newSvc(st)
	ctx := context.Background()

	// No row → data-driven DRAFT default.
	rs, err := svc.ReviewStatus(ctx, "LIEN_JOB", "job-1")
	if err != nil {
		t.Fatalf("review status: %v", err)
	}
	if rs.State != "DRAFT" || !rs.IsDraft || rs.BannerText != defaultBanner {
		t.Fatalf("default review %+v, want DRAFT/isDraft/defaultBanner", rs)
	}

	// Flip to APPROVED (DIB-42 checkpoint) → no longer draft.
	upd, err := svc.UpsertReviewStatus(ctx, ReviewStatusInput{SubjectType: "LIEN_JOB", SubjectID: "job-1", State: "APPROVED", BannerText: "Approved for issue"})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if upd.State != "APPROVED" || upd.IsDraft {
		t.Fatalf("approved review %+v, want APPROVED/not-draft", upd)
	}
	got, _ := svc.ReviewStatus(ctx, "LIEN_JOB", "job-1")
	if got.State != "APPROVED" || got.BannerText != "Approved for issue" {
		t.Fatalf("persisted review %+v, want APPROVED banner", got)
	}

	// Invalid state → 422.
	_, err = svc.UpsertReviewStatus(ctx, ReviewStatusInput{SubjectType: "LIEN_JOB", SubjectID: "job-1", State: "BOGUS"})
	if err == nil {
		t.Fatalf("invalid state should error")
	}
}
