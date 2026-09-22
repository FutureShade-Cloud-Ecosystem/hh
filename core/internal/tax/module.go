// Package tax is the W6 M5 Tax Engine module: the configurable, effective-dated Ontario HST table + status-card
// exemptions that replaces the W4 flat-13% pos stub (via the frozen pos TaxAssessor seam), the round-once
// largest-remainder per-line tax split, the three FORCED-evidence status-card profiles (masked-only
// serialization, the 0% evidence hard gate preserved), the W4-snapshot recompute (insert-only, audit-truth
// only), and the HST→GL reconciliation. It owns the 0042 tables (inherits the 0029 FN tables). Ontario-only at
// W6; Avalara + postal routing are DORMANT stubs.
//
// SCAFFOLD STATUS: the Provider interface, the GLBalanceReader seam, the dormant stub types, and the service
// signatures are frozen; bodies are errNotImplemented stubs so the tree compiles + boots. pos is NOT wired to
// this at scaffold (it runs on FlatHSTAssessor); the .WithTaxAssessor(posTaxAdapter{taxSvc}) rewire lands with
// W6-M5. Worker W6-M5 fills the bodies. Signatures are FROZEN.
//
// Doc-map: FN_TAX_RELIEF_DECISION_ARCHITECTURE.md, effective-dating + evidence gates are profile flags/rows
// (tax_rates.effective_from, tax_status_card_profiles.requires_*); CEL predicates are deferred pending D-A19-5.
package tax

import (
	"context"
	"errors"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"github.com/jackc/pgx/v5/pgxpool"
	"log/slog"
	"time"
)

// errNotImplemented marks a scaffold stub the M5 worker fills.
var errNotImplemented = errors.New("tax: not implemented (scaffold stub)")

// -- provider surface (the pos TaxAssessor adapter + HST recon consume these) ------------------------------

// Provider is the tax-assessment surface a consumer (the pos adapter at rewire time) depends on. It computes
// the BINDING tax on a sale from the effective-dated rate table + the relief/evidence decision. FROZEN.
type Provider interface {
	AssessSale(ctx context.Context, in SaleAssessment) (Quote, error)
}

// SaleAssessment is the tax-assessment request for one sale subtotal. ReasonCode/Fulfilment/EvidenceAttached
// drive the relief path; an empty ReasonCode is a standard-rated sale.
type SaleAssessment struct {
	BranchID         string
	SubtotalCents    int64
	ReasonCode       string
	Fulfilment       string
	EvidenceAttached bool
	AsOf             string // YYYY-MM-DD sale date for the effective-dated rate lookup ("" = today)
}

// Quote is the assessment result: the binding tax, the binding rate, and the FN split fields (for the
// tax.relief.assessed posting). Round-once at the total; the per-line split is the caller's (moneyutil).
type Quote struct {
	TaxCents          int64  `json:"tax_cents"`
	RateBps           int    `json:"rate_bps"`
	ReasonCode        string `json:"reason_code"`
	OnCreditCents     int64  `json:"on_credit_cents"`
	NetCollectedCents int64  `json:"net_collected_cents"`
	RequiresEvidence  bool   `json:"requires_evidence"`
}

// GLBalanceReader reads a GL account balance for the HST→GL reconciliation. Satisfied by a main.go adapter
// over *gl.Service.GLBalanceCents, tax never reads the GL views directly. FROZEN.
type GLBalanceReader interface {
	GLBalanceCents(ctx context.Context, accountCode string, year, month int, branchID string) (int64, error)
}

// -- dormant stubs (demoted; NOT wired at W6, D-A19-5) ----------------------------------------------------

// AvalaraStub is the DORMANT Avalara routing seam (demoted at W6; tax_config.avalara_enabled is false). It is
// frozen here so a later wave can slot a live client in without a signature churn. It never runs at W6.
type AvalaraStub struct{}

// Dormant reports the stub is inert (always true at W6).
func (AvalaraStub) Dormant() bool { return true }

// PostalRoutingStub is the DORMANT postal-code tax-routing seam (demoted at W6; postal_routing_enabled false).
type PostalRoutingStub struct{}

// Dormant reports the stub is inert (always true at W6).
func (PostalRoutingStub) Dormant() bool { return true }

// -- repository --------------------------------------------------------------

// Repository is tax's pgx access layer over the 0042 tables (+ the inherited 0029 FN tables). Nil pool tolerated.
type Repository struct {
	pool *pgxpool.Pool
}

// NewRepository wraps a pool (may be nil in scaffold mode).
func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

// -- service -----------------------------------------------------------------

// Service is the tax domain seam. It implements Provider (AssessSale) and owns the rate table, status-card
// profiles, evidence, recompute, and the HST recon. Sibling seams are injected via the With* options. The repo
// is held behind the taxStore seam so the unit tests inject an in-memory fake (no DB); *Repository satisfies it.
type Service struct {
	repo      taxStore
	glBalance GLBalanceReader
	audit     audit.Recorder
	log       *slog.Logger
	now       func() time.Time // injectable clock (tests); nil → time.Now
}

// NewService wires the repository. Optional deps are injected via the With* methods.
func NewService(repo *Repository) *Service {
	return &Service{repo: repo, log: slog.Default()}
}

// WithGLBalances injects the GL balance seam (the HST→GL reconciliation tie-out).
func (s *Service) WithGLBalances(r GLBalanceReader) *Service { s.glBalance = r; return s }

// WithAudit injects the A-21 Recorder (evidence-access stamping, A-21 evidence audit).
func (s *Service) WithAudit(r audit.Recorder) *Service { s.audit = r; return s }

// AssessSale implements Provider, the binding, effective-dated ON-HST engine (delegates to assessSale in
// service.go). The main.go posTaxAdapter rewire routes pos's TaxAssessor through here at integration. FROZEN.
func (s *Service) AssessSale(ctx context.Context, in SaleAssessment) (Quote, error) {
	return s.assessSale(ctx, in)
}

// RecomputeSnapshots re-derives the W4 flat-13 tax_decision_snapshots under the M5 engine, INSERT-ONLY
// (audit-truth only, no retro GL correction; the W4 flat-13 collected amounts stay binding). It is idempotent
// (a re-run skips a W4 row that already has its M5 recompute) and returns the count inserted. FROZEN signature.
func (s *Service) RecomputeSnapshots(ctx context.Context, fromDate, toDate string) (int, error) {
	res, err := s.recompute(ctx, fromDate, toDate)
	return res.Inserted, err
}

// ReconcileHST ties collected HST (Σ net_collected on the tax-decision side) to the GL 2200 liability for a
// period and returns the variance (0 = tied out). The richer report is ReconcileReport. FROZEN signature.
func (s *Service) ReconcileHST(ctx context.Context, year, month int, branchID string) (int64, error) {
	rep, err := s.reconcile(ctx, year, month, branchID)
	return rep.VarianceCents, err
}

// RecomputeReport runs the recompute and returns the full run summary (scanned/inserted/skipped) for the
// tax-admin surface. Insert-only + idempotent, same as RecomputeSnapshots.
func (s *Service) RecomputeReport(ctx context.Context, fromDate, toDate string) (RecomputeResult, error) {
	return s.recompute(ctx, fromDate, toDate)
}

// ReconcileReport returns the full HST → GL reconciliation (collected vs the 2200/2201/2202 balances + variance).
func (s *Service) ReconcileReport(ctx context.Context, year, month int, branchID string) (ReconReport, error) {
	return s.reconcile(ctx, year, month, branchID)
}

// compile-time assertion: *Service satisfies the tax Provider surface.
var _ Provider = (*Service)(nil)

// -- handler -----------------------------------------------------------------

// Handler exposes the tax-admin HTTP surface (rates, status-card profiles, config, recon). SCAFFOLD: routes
// registered, every handler answers 501 until M5 fills the service.
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

// RegisterRoutes self-registers the tax-admin routes behind the identity guards. Reads are RequireManager;
// rate/profile/config writes + recompute + recon are RequireAdmin (finance-sensitive). FROZEN guard signature.
func (h *Handler) RegisterRoutes(r httpx.Registrar, g *guards.Guards) {
	// Rate table + profiles + config: reads are manager-gated; finance-sensitive writes are admin-gated.
	r.Handle("GET /api/v1/tax/rates", g.RequireManager(h.staff(h.listRates)))
	r.Handle("POST /api/v1/tax/rates", g.RequireAdmin(h.staff(h.createRate)))
	r.Handle("GET /api/v1/tax/profiles", g.RequireManager(h.staff(h.listProfiles)))
	r.Handle("POST /api/v1/tax/profiles", g.RequireAdmin(h.staff(h.upsertProfile)))
	r.Handle("GET /api/v1/tax/config", g.RequireManager(h.staff(h.getConfig)))
	r.Handle("POST /api/v1/tax/config", g.RequireAdmin(h.staff(h.updateConfig)))
	r.Handle("POST /api/v1/tax/recompute", g.RequireAdmin(h.staff(h.recompute)))
	r.Handle("GET /api/v1/tax/recon", g.RequireManager(h.staff(h.recon)))

	// Evidence (MASKED-only serialization) + A-21 evidence audit (VIEW/CHANGE/DOWNLOAD) + the tcrd relief gate.
	// Capture is staff (a cashier attaches evidence at the counter); reads/downloads are manager-gated.
	r.Handle("GET /api/v1/tax/evidence", g.RequireManager(h.staff(h.listEvidence)))
	r.Handle("POST /api/v1/tax/evidence", g.RequireStaff(h.staff(h.captureEvidence)))
	r.Handle("GET /api/v1/tax/evidence/{id}", g.RequireManager(h.staff(h.viewEvidence)))
	r.Handle("POST /api/v1/tax/evidence/{id}/download", g.RequireManager(h.staff(h.downloadEvidence)))
	r.Handle("POST /api/v1/tax/evidence/{id}/validate", g.RequireManager(h.staff(h.validateEvidence)))

	// Review queue: profiles PENDING ratification (the Certificates/Review-Queue surface).
	r.Handle("GET /api/v1/tax/review-queue", g.RequireManager(h.staff(h.reviewQueue)))
}
