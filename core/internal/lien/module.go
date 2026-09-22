// Package lien is the W6 M6 Lien Cockpit module: the Ontario Construction Act document workflow (Conditional /
// Unconditional Release, Statutory Declaration, Discharge), DRAFT-bannered data-driven templates, the demo persona's
// Monday triage worklist (dual-gated), Bill 60 holdback aging, and the holdback GL (DR/CR 1150/2350 via the GL
// provider). It owns the 0038 lien_* + holdback_ledger tables. Pairs with internal/compliance (the 45-day
// calendar).
//
// SCAFFOLD STATUS: exported types, the frozen consumer-declared seam interfaces, and the service signatures are
// frozen; bodies are errNotImplemented stubs so the tree compiles + boots. M6 has ZERO client-validated
// requirements (statute-derived, DRAFT-bannered; the DIB-42 demo is the checkpoint), the templates are
// data-driven rows, not code. Worker W6-M6 fills the bodies. Signatures are FROZEN.
package lien

import (
	"context"
	"errors"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"log/slog"
	"net/http"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// errNotImplemented marks a scaffold stub the M6 worker fills.
var errNotImplemented = errors.New("lien: not implemented (scaffold stub)")

// - consumer-declared seams (adapters live in billing / order / main.go) -

// HoldbackFactsReader surfaces the invoices carrying a holdback + their retention facts, for the aging +
// release worklist. Satisfied by *billing.Repository via internal/billing/lien_seam.go.
type HoldbackFactsReader interface {
	InvoicesWithHoldback(ctx context.Context, branchID string) ([]HoldbackInvoice, error)
	InvoiceHoldbackFacts(ctx context.Context, invoiceID string) (HoldbackFacts, error)
}

// HoldbackInvoice is one invoice with retention outstanding (the worklist row).
type HoldbackInvoice struct {
	InvoiceID     string `json:"invoice_id"`
	InvoiceNo     string `json:"invoice_no"`
	CustomerID    string `json:"customer_id"`
	BranchID      string `json:"branch_id"`
	HoldbackCents int64  `json:"holdback_cents"`
	FinalizedAt   string `json:"finalized_at"`
}

// HoldbackFacts is the release-decision projection of one invoice's retention.
type HoldbackFacts struct {
	InvoiceID     string `json:"invoice_id"`
	HoldbackCents int64  `json:"holdback_cents"`
	BranchID      string `json:"branch_id"`
	CustomerID    string `json:"customer_id"`
}

// OrderHoldbackReader reads the per-order holdback status (order_lines.lien_holdback_status). Satisfied by
// *order.Repository via internal/order/lien_seam.go.
type OrderHoldbackReader interface {
	HoldbackStatusForOrder(ctx context.Context, orderID string) (string, error)
}

// GLPoster posts the holdback release journal through the GL provider (DR 2350 / CR 1150 on release).
// Satisfied by a main.go adapter over *gl.Service.PostExternal.
type GLPoster interface {
	PostJournal(ctx context.Context, in GLJournal) (string, error)
}

// GLJournal + GLLine mirror gl.ExternalJournal by value (lien does not import gl internals).
type GLJournal struct {
	Source         string
	SourceRefID    string
	IdempotencyKey string
	Memo           string
	BranchID       string
	Lines          []GLLine
}

// GLLine is one leg of a GLJournal.
type GLLine struct {
	AccountCode string
	DebitCents  int64
	CreditCents int64
	Memo        string
}

// CalendarRegistrar registers a compliance deadline (the 45-day release) when a holdback is booked. Satisfied
// by *compliance.Service (the Registrar provider).
type CalendarRegistrar interface {
	RegisterDeadline(ctx context.Context, jobID, kind, dueDate string, ruleDays int) error
}

// CoSignerResolver resolves a claimed holdback-release co-signer's CURRENT server-side authority by user id.
// The four-eyes gate uses it to VERIFY the co-signer actually exists, is active, and truly holds a finance
// authority, never trusting the role asserted in the request body (S3 W6 B4). Satisfied by *identity.Service.
type CoSignerResolver interface {
	ResolveStaff(ctx context.Context, userID string) (identity.StaffContext, error)
}

// - repository / service / handler -

// Repository is lien's pgx access layer over the 0038 lien_* + holdback_ledger tables (nil pool tolerated).
type Repository struct {
	pool *pgxpool.Pool
}

// NewRepository wraps a pool (may be nil in scaffold mode).
func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

// Service is the lien domain seam (documents, holdback aging, release, Monday triage). Sibling seams injected
// via the With* options (main.go wires the adapters).
type Service struct {
	repo     *Repository
	store    lienStore // persistence seam (defaults to repo; unit tests inject a DB-free fake)
	holdback HoldbackFactsReader
	orders   OrderHoldbackReader
	gl       GLPoster
	calendar CalendarRegistrar
	cosigner CoSignerResolver // resolves the four-eyes co-signer's authority server-side (never body-asserted)
	audit    audit.Recorder
	log      *slog.Logger
	now      func() time.Time
	// callerFrom resolves the guard-stamped acting StaffContext (the dual-gate release check). Never a
	// spoofable header, the trusted, server-resolved caller (C-1).
	callerFrom func(context.Context) (identity.StaffContext, bool)
}

// NewService wires the repository. Optional deps are injected via the With* methods.
func NewService(repo *Repository) *Service {
	return &Service{
		repo:       repo,
		store:      repo,
		log:        slog.Default(),
		now:        time.Now,
		callerFrom: identity.StaffContextFromContext,
	}
}

// WithHoldbackFacts injects the billing holdback-facts seam.
func (s *Service) WithHoldbackFacts(r HoldbackFactsReader) *Service { s.holdback = r; return s }

// WithOrderHoldback injects the order holdback-status seam.
func (s *Service) WithOrderHoldback(r OrderHoldbackReader) *Service { s.orders = r; return s }

// WithGLPoster injects the GL posting seam (the 1150/2350 release journal).
func (s *Service) WithGLPoster(p GLPoster) *Service { s.gl = p; return s }

// WithCalendar injects the compliance-calendar registrar.
func (s *Service) WithCalendar(r CalendarRegistrar) *Service { s.calendar = r; return s }

// WithCoSigner injects the server-side co-signer authority resolver (the four-eyes gate).
func (s *Service) WithCoSigner(r CoSignerResolver) *Service { s.cosigner = r; return s }

// WithAudit injects the A-21 Recorder.
func (s *Service) WithAudit(r audit.Recorder) *Service { s.audit = r; return s }

// ReleaseHoldback posts the 45-day holdback release journal (DR 2350 / CR 1150) for a job. FROZEN signature;
// the dual gate + PAYABLE-release-refused 422 live in releaseHoldback. This single-arg entry carries NO
// co-approver, so the four-eyes dual gate refuses it (403), the HTTP route supplies the co-signer and calls
// ReleaseHoldbackApproved. Kept so the frozen provider surface stays intact.
func (s *Service) ReleaseHoldback(ctx context.Context, jobID string) error {
	_, err := s.releaseHoldback(ctx, jobID, ReleaseApproval{})
	return err
}

// - handler -

// Handler exposes the Lien Cockpit HTTP surface. SCAFFOLD: routes registered, every handler answers 501.
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

// RegisterRoutes self-registers the lien routes behind the identity guards. Reads are RequireManager (the
// Monday triage is manager-visible); release is dual-gated (gatekeeper GENERAL_MANAGER + Controller inside the
// service). FROZEN guard signature; M6 fills the route bodies.
func (h *Handler) RegisterRoutes(r httpx.Registrar, g *guards.Guards) {
	// - reads: the Monday triage is manager-visible (RequireManager) -
	r.Handle("GET /api/v1/lien/jobs", g.RequireManager(h.staff(h.listJobs)))
	r.Handle("GET /api/v1/lien/jobs/{id}", g.RequireManager(h.staff(h.getJob)))
	r.Handle("GET /api/v1/lien/triage", g.RequireManager(h.staff(h.triage)))
	r.Handle("GET /api/v1/lien/holdback/aging", g.RequireManager(h.staff(h.aging)))
	r.Handle("GET /api/v1/lien/jobs/{id}/documents", g.RequireManager(h.staff(h.listDocuments)))
	r.Handle("GET /api/v1/lien/documents/{id}/versions", g.RequireManager(h.staff(h.documentVersions)))
	// - mutations (route-gated RequireManager; the release adds the in-service dual gate) -
	r.Handle("POST /api/v1/lien/jobs", g.RequireManager(h.staff(h.createJob)))
	r.Handle("POST /api/v1/lien/jobs/{id}/sync", g.RequireManager(h.staff(h.syncHoldbacks)))
	r.Handle("POST /api/v1/lien/documents", g.RequireManager(h.staff(h.createDocument)))
	r.Handle("POST /api/v1/lien/holdback/{jobID}/release", g.RequireManager(h.staff(h.release)))
}

func (h *Handler) notImplemented(w http.ResponseWriter, _ *http.Request) {
	http.Error(w, "lien: not implemented (scaffold)", http.StatusNotImplemented)
}
