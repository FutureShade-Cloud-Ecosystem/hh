package compliance

import (
	"context"
	"strings"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// service.go, the compliance reads beyond the frozen Registrar/Upcoming (in module.go): the full calendar
// list and the SHARED, DATA-DRIVEN review-status banner source every M6 screen reads.

const actReviewStatusSet = "COMPLIANCE_REVIEW_STATUS_SET"

// Calendar returns the deadline registry, optionally filtered by status ("" = all).
func (s *Service) Calendar(ctx context.Context, status string) ([]Deadline, error) {
	return s.store.ListDeadlines(ctx, status)
}

// ReviewStatus returns the shared banner for a subject. When no explicit row exists it returns the DATA-DRIVEN
// default DRAFT banner, so the endpoint always answers, the M6 UI never hardcodes a banner.
func (s *Service) ReviewStatus(ctx context.Context, subjectType, subjectID string) (ReviewStatus, error) {
	if strings.TrimSpace(subjectID) == "" {
		return ReviewStatus{}, clientErr(400, "compliance: subject_id is required")
	}
	rs, found, err := s.store.GetReviewStatus(ctx, subjectType, subjectID)
	if err != nil {
		return ReviewStatus{}, err
	}
	if !found {
		return ReviewStatus{
			SubjectType: subjectType,
			SubjectID:   subjectID,
			State:       stateDraft,
			BannerText:  defaultBanner,
			IsDraft:     true,
		}, nil
	}
	rs.IsDraft = rs.State == stateDraft || rs.State == statePending
	return rs, nil
}

// UpsertReviewStatus sets a subject's shared banner (leadership flips DRAFT→APPROVED at the DIB-42 checkpoint).
func (s *Service) UpsertReviewStatus(ctx context.Context, in ReviewStatusInput) (ReviewStatus, error) {
	if strings.TrimSpace(in.SubjectID) == "" {
		return ReviewStatus{}, clientErr(400, "compliance: subject_id is required")
	}
	if in.State == "" {
		in.State = stateDraft
	}
	if !validState(in.State) {
		return ReviewStatus{}, clientErr(422, "compliance: invalid review state %q", in.State)
	}
	if strings.TrimSpace(in.BannerText) == "" {
		in.BannerText = defaultBanner
	}
	actor := identity.StaffUserIDFromContext(ctx)
	rs, err := s.store.UpsertReviewStatus(ctx, in, actor)
	if err != nil {
		return ReviewStatus{}, err
	}
	rs.IsDraft = rs.State == stateDraft || rs.State == statePending
	if s.audit != nil {
		_ = s.audit.Record(ctx, audit.SensitiveAccess{
			Action:     actReviewStatusSet,
			EntityType: "compliance.review_status",
			EntityID:   in.SubjectID,
			ActorID:    actor,
			Extra:      map[string]any{"state": in.State},
		})
	}
	return rs, nil
}
