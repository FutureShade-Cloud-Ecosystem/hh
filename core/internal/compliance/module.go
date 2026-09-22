// Package compliance is the W6 M6 compliance-calendar module: the deadline registry driving the Bill 60 45-day
// holdback-release rule + the lien Monday-triage reminders (badges, there is no scheduler infra at W6; the
// worklist is computed on read). It owns the 0038 compliance_calendar + review_status tables and pairs with
// internal/lien.
//
// STATUS (updated at W2 union, per DIB-T2-F2): LIVE. The Registrar provider interface + service
// signatures remain frozen; all bodies are implemented and the four routes serve real data, the
// compliance HTTP-surface integration test (swarm W2-T) proves it. The scaffold note below is history.
package compliance

import (
	"context"
	"log/slog"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// Registrar is the FROZEN provider surface a producer (lien, on holdback booking) uses to register a compliance
// deadline. *Service satisfies it; lien consumes it via its CalendarRegistrar seam (main.go wires taxSvc→lien).
type Registrar interface {
	RegisterDeadline(ctx context.Context, jobID, kind, dueDate string, ruleDays int) error
}

// Deadline is one compliance-calendar entry (the worklist row).
type Deadline struct {
	ID       string `json:"id"`
	JobID    string `json:"job_id"`
	Kind     string `json:"deadline_kind"`
	DueDate  string `json:"due_date"`
	RuleDays int    `json:"rule_days"`
	Status   string `json:"status"`
}

// -- repository / service / handler --------------------------------------------

// Repository is compliance's pgx access layer over the 0038 compliance_calendar + review_status tables.
type Repository struct {
	pool *pgxpool.Pool
}

// NewRepository wraps a pool (may be nil in scaffold mode).
func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

// Service is the compliance domain seam (deadline registry, aging buckets, review-status banners).
type Service struct {
	repo  *Repository
	store complianceStore // persistence seam (defaults to repo; unit tests inject a DB-free fake)
	audit audit.Recorder
	log   *slog.Logger
	now   func() time.Time
}

// NewService wires the repository.
func NewService(repo *Repository) *Service {
	return &Service{repo: repo, store: repo, log: slog.Default(), now: time.Now}
}

// WithAudit injects the A-21 Recorder.
func (s *Service) WithAudit(r audit.Recorder) *Service { s.audit = r; return s }

// RegisterDeadline implements Registrar: it records a compliance deadline, IDEMPOTENTLY on
// (job_id, deadline_kind, due_date) so a producer that re-registers the same 45-day release window books
// nothing new. FROZEN signature.
func (s *Service) RegisterDeadline(ctx context.Context, jobID, kind, dueDate string, ruleDays int) error {
	if kind == "" {
		kind = "OTHER"
	}
	if ruleDays <= 0 {
		ruleDays = defaultRuleDays
	}
	_, err := s.store.UpsertDeadline(ctx, jobID, kind, dueDate, ruleDays)
	return err
}

// Upcoming returns the open deadlines due within horizonDays (the badge worklist), marking any already past
// due as OVERDUE. FROZEN signature.
func (s *Service) Upcoming(ctx context.Context, horizonDays int) ([]Deadline, error) {
	if horizonDays <= 0 {
		horizonDays = defaultRuleDays
	}
	asOf := s.clock()
	items, err := s.store.OpenDeadlines(ctx)
	if err != nil {
		return nil, err
	}
	return filterUpcoming(items, asOf, horizonDays), nil
}

// compile-time assertion: *Service satisfies the Registrar provider.
var _ Registrar = (*Service)(nil)

// -- handler -----------------------------------------------------------------

// Handler exposes the compliance-calendar HTTP surface. All routes serve real data (see calendar_integration_test.go).
type Handler struct {
	userLink identity.UserIDLinker
	svc      *Service
	log      *slog.Logger
}

// NewHandler wires the service.
func NewHandler(svc *Service, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.Default()
	}
	return &Handler{svc: svc, log: log}
}

// RegisterRoutes self-registers the compliance routes behind the identity guards (reads RequireManager).
// FROZEN guard signature; M6 fills the route bodies.
func (h *Handler) RegisterRoutes(r httpx.Registrar, g *guards.Guards) {
	r.Handle("GET /api/v1/compliance/calendar", g.RequireManager(h.staff(h.calendar)))
	r.Handle("GET /api/v1/compliance/upcoming", g.RequireManager(h.staff(h.upcoming)))
	// The SHARED, DATA-DRIVEN review-status banner source the M6 UI reads (every lien screen derives its DRAFT
	// banner from here, a banner change is a row edit, not a redeploy). Read is manager-visible; the upsert is
	// manager-gated (leadership flips DRAFT→APPROVED at the DIB-42 checkpoint).
	r.Handle("GET /api/v1/compliance/review-status", g.RequireManager(h.staff(h.reviewStatus)))
	r.Handle("PUT /api/v1/compliance/review-status", g.RequireManager(h.staff(h.upsertReviewStatus)))
}
