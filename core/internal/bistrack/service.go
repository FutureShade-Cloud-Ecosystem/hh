package bistrack

import (
	"context"
	"time"
)

// service.go, DIB-53 phase 0 the observability service. It answers "are we configured?" and "what's the
// last job + open-discrepancy count?" and NOTHING ELSE. It deliberately holds NO connector and exposes NO
// sync/run method: there is no field it could be given and no method that could be called to start a
// sync. That is the structural guarantee (supervisor C4), enforced by structural_test.go, not by
// discipline. The frozen connector seam lives in connector.go, constructed + tested there, wired to
// nothing this phase.

// store is the read seam the status surface needs (satisfied by *Repository; a fake in tests). Read-only
// on purpose, the service has no write path.
type store interface {
	LatestJob(ctx context.Context) (*SyncJob, error)
	OpenDiscrepancyCount(ctx context.Context) (int64, error)
}

// Service is the read-only BisTrack sync status surface.
type Service struct {
	store      store
	configured bool
}

// NewService wires the read store. Configuration is injected separately (WithConfigured) so the module
// never imports the config package.
func NewService(store store) *Service { return &Service{store: store} }

// WithConfigured records whether BisTrack credentials are present (cfg.BisTrackConfigured() in main.go).
// It gates ONLY the reported status, no credential value makes phase 0 able to sync.
func (s *Service) WithConfigured(c bool) *Service { s.configured = c; return s }

// Configured reports whether credentials are present.
func (s *Service) Configured() bool { return s.configured }

// HealthStatus is the single token healthz carries. "unconfigured" when no credentials (this phase);
// "idle" when credentials are present but no sync has run, which, in phase 0, is still always the case
// because the module cannot initiate one. It never reports "syncing".
func (s *Service) HealthStatus() string {
	if !s.configured {
		return "unconfigured"
	}
	return "idle"
}

// Status is the GET /api/v1/bistrack/status DTO: {configured, last_sync, discrepancies}. With no jobs and
// no credentials (phase 0) it is the honest {false, null, 0}.
func (s *Service) Status(ctx context.Context) (Status, error) {
	out := Status{Configured: s.configured}
	last, err := s.store.LatestJob(ctx)
	if err != nil {
		return Status{}, err
	}
	if last != nil {
		out.LastSync = lastSyncTime(last)
	}
	n, err := s.store.OpenDiscrepancyCount(ctx)
	if err != nil {
		return Status{}, err
	}
	out.Discrepancies = n
	return out, nil
}

// lastSyncTime is when a job last completed (FinishedAt), falling back to when it was created (a job that
// never ran). nil-safe on the pointer times.
func lastSyncTime(j *SyncJob) *time.Time {
	if j.FinishedAt != nil {
		return j.FinishedAt
	}
	t := j.CreatedAt
	return &t
}
