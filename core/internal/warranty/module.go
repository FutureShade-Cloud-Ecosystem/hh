// Package warranty is the WTY-01 Warranty-Claim Management module (P1): the vendor warranty registry (FR-1), the
// tracked claim object with #WTY-n capture from office/counter/invoice doorways (FR-2), the CLIENT-CONFIRMED
// Kanban lifecycle with checklist-gated phase advancement (FR-3), the evidence-reference seam (FR-4), manual
// settlement recording with a GL hand-off seam (FR-5), and the FR-7 headline reports (by-manufacturer
// frequency, win/loss, time-to-settlement, funnel counts, exposure by vendor). It owns the 0053/0054
// warranty_* tables and keys off the vendor/customer/product/invoice masters. Spec: PRODUCT_SPEC.md (G-LOCK-077),
// DIB-153.
//
// SCOPE (P1): registry + claim object + Kanban + evidence seam + manual settlement + the FR-7 core reports +
// the FR-9 back-dated migration path. OUT of P1 (clean seams, not built): public web intake (FR-6), AI triage
// assist (FR-10), true-cost session timer (FR-11 soft costs), messaging-hub notifications (FR-8), full GL
// wiring of settlements. Each is a documented seam below.
package warranty

import (
	"context"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"log/slog"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// - consumer-declared seams (adapters live in main.go / integration tests) -

// GLPoster posts the warranty settlement journal through the GL provider (FR-5). Satisfied by a main.go adapter
// over *gl.Service.PostExternal. In P1 this seam is WIRED but the settlement service treats a GL failure as
// non-fatal (records the settlement + amounts regardless), the money is RECORDED on the claim either way, and
// the journal rides when the account mapping is ratified (PRD §9 Q5). See service.RecordSettlement.
type GLPoster interface {
	PostJournal(ctx context.Context, in GLJournal) (string, error)
}

// GLJournal + GLLine mirror gl.ExternalJournal by value (warranty does not import gl internals).
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

// - repository / service / handler -

// Repository is warranty's pgx access layer over the 0053/0054 warranty_* tables (nil pool tolerated in
// scaffold/unit mode).
type Repository struct {
	pool *pgxpool.Pool
}

// NewRepository wraps a pool (may be nil in unit mode).
func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

// Service is the warranty domain seam (registry, claim capture, Kanban lifecycle, settlement, reporting).
// Sibling seams are injected via the With* options (main.go wires the adapters).
type Service struct {
	repo  *Repository
	store warrantyStore // persistence seam (defaults to repo; unit tests inject a DB-free fake)
	gl    GLPoster      // settlement GL hand-off (FR-5); nil = record-only (documented P1 default)
	audit audit.Recorder
	log   *slog.Logger
	now   func() time.Time
	// - W5-B (ERP:warranty-p2 / DIB-223) consumer-declared seams; see moneypath.go + sla.go -
	credit   CustomerCreditIssuer // BILLING: accommodation credit memos; nil ⇒ FALLBACK_GL
	invCost  InventoryCostMover   // INVENTORY: replacement cost movement; nil ⇒ SEAM_UNWIRED (no fallback)
	slaNotif SLANotifier          // NOTIFY: SLA-breach emission; nil ⇒ SUPPRESSED (flag-off default)
	// callerFrom resolves the guard-stamped acting StaffContext (never a spoofable header, the trusted,
	// server-resolved caller, C-1).
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

// WithGLPoster injects the settlement GL posting seam (FR-5). When set, a settlement attempts to post the
// warranty.settled journal; when unset, settlements are RECORDED without a journal (the P1 record-only default).
func (s *Service) WithGLPoster(p GLPoster) *Service { s.gl = p; return s }

// WithAudit injects the A-21 Recorder (settlement + evidence-access are auditable).
func (s *Service) WithAudit(r audit.Recorder) *Service { s.audit = r; return s }

// WithLogger injects a logger.
func (s *Service) WithLogger(l *slog.Logger) *Service {
	if l != nil {
		s.log = l
	}
	return s
}

// - handler -

// Handler exposes the warranty HTTP surface.
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

// RegisterRoutes self-registers the warranty routes behind the identity guards (PRD §4): reads + claim
// capture/lifecycle are RequireStaff (staff create/edit); settlement is RequireManager (manager settle);
// registry configuration is RequireAdmin (admin configure registry).
func (h *Handler) RegisterRoutes(r httpx.Registrar, g *guards.Guards) {
	// - FR-1 vendor registry: reads staff-visible; config admin-gated -
	r.Handle("GET /api/v1/warranty/vendors", g.RequireStaff(h.staff(h.listVendorProfiles)))
	r.Handle("GET /api/v1/warranty/vendors/{vendorID}", g.RequireStaff(h.staff(h.getVendorProfile)))
	r.Handle("PUT /api/v1/warranty/vendors/{vendorID}", g.RequireAdmin(h.staff(h.upsertVendorProfile)))
	r.Handle("POST /api/v1/warranty/vendors/{vendorID}/overrides", g.RequireAdmin(h.staff(h.addOverride)))

	// - walk change 6: the claim threshold policy. Read staff (the till and Manager Home read the
	// threshold; the board tiles and the return sentence answer from it), write admin (Dealer Admin's
	// Warranty page), the counter policy's split. -
	r.Handle("GET /api/v1/warranty/policy", g.RequireStaff(h.staff(h.getPolicy)))
	r.Handle("PUT /api/v1/warranty/policy", g.RequireAdmin(h.staff(h.putPolicy)))

	// - FR-2 claim capture + reads -
	r.Handle("GET /api/v1/warranty/claims", g.RequireStaff(h.staff(h.listClaims)))
	r.Handle("GET /api/v1/warranty/claims/{id}", g.RequireStaff(h.staff(h.getClaim)))
	r.Handle("GET /api/v1/warranty/claims/{id}/events", g.RequireStaff(h.staff(h.claimEvents)))
	r.Handle("GET /api/v1/warranty/claims/{id}/checklist", g.RequireStaff(h.staff(h.claimChecklist)))
	r.Handle("POST /api/v1/warranty/claims", g.RequireStaff(h.staff(h.createClaim)))

	// - FR-3 lifecycle: assign, checklist tick, phase advance, evidence -
	r.Handle("POST /api/v1/warranty/claims/{id}/assign", g.RequireStaff(h.staff(h.assignClaim)))
	r.Handle("POST /api/v1/warranty/claims/{id}/checklist", g.RequireStaff(h.staff(h.tickChecklist)))
	r.Handle("POST /api/v1/warranty/claims/{id}/advance", g.RequireStaff(h.staff(h.advancePhase)))
	r.Handle("POST /api/v1/warranty/claims/{id}/evidence", g.RequireStaff(h.staff(h.attachEvidence)))

	// - FR-5 settlement: manager-gated -
	r.Handle("POST /api/v1/warranty/claims/{id}/settle", g.RequireManager(h.staff(h.settleClaim)))

	// - FR-7 reporting: staff-visible metrics -
	r.Handle("GET /api/v1/warranty/reports", g.RequireStaff(h.staff(h.reports)))

	// - board convenience: the Kanban columns (confirmed phases) -
	r.Handle("GET /api/v1/warranty/board", g.RequireStaff(h.staff(h.board)))
}
