// Package invcount is the W6 A-15 Inventory Counts module: snapshot-frozen cycle counts, variance valued at
// weighted-average, reason codes with per-reason GL mapping + maker-checker, and the monthly inventory→GL
// reconciliation (lazy current-month run-once). It owns the 0040 count_* + inventory_recon_runs tables and
// posts variance adjustments through the inventory seam + the reason-coded GL rule. Depends on INV-CORE.
//
// SCAFFOLD STATUS: exported types, the frozen consumer-declared seams, and the service signatures are frozen;
// bodies are errNotImplemented stubs so the tree compiles + boots. Worker W6-A15 fills the bodies. FROZEN.
package invcount

import (
	"context"
	"errors"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"log/slog"
	"net/http"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// errNotImplemented marks a scaffold stub the A-15 worker fills.
var errNotImplemented = errors.New("invcount: not implemented (scaffold stub)")

// - consumer-declared seams (adapters over inventory + gl live in main.go) -

// StockCounter surfaces the frozen on-hand snapshot + weighted-avg valuation and posts the COUNT adjustment
// move. Satisfied by a main.go adapter over *inventory.Service (count_seam.go).
type StockCounter interface {
	SnapshotOnHand(ctx context.Context, branchID string) ([]Snapshot, error)
	// SnapshotOnHandTx reads inside the caller's transaction (the sync freeze holds one), so no second pool
	// connection is taken while the first is held.
	SnapshotOnHandTx(ctx context.Context, tx pgx.Tx, branchID string) ([]Snapshot, error)
	WeightedAvgCostCents(ctx context.Context, productID, branchID string) (int64, error)
	// WeightedAvgCostCentsTx reads inside the caller's transaction (the post holds one), so no second pool
	// connection is taken while the first is held.
	WeightedAvgCostCentsTx(ctx context.Context, tx pgx.Tx, productID, branchID string) (int64, error)
	PostCountAdjustmentTx(ctx context.Context, tx pgx.Tx, productID, locationID, branchID string, qtyDelta float64, unitCostCents int64, reason string) (string, error)
}

// Snapshot is one frozen on-hand reading (mirrors inventory.CountSnapshot).
type Snapshot struct {
	ProductID    string  `json:"product_id"`
	LocationID   string  `json:"location_id"`
	OnHandBase   float64 `json:"on_hand_base"`
	AvgCostCents int64   `json:"avg_cost_cents"`
}

// GLPoster posts the reason-coded variance journal (the reason leg + the 1200 inventory leg). Satisfied by a
// main.go adapter over *gl.Service.PostExternal.
type GLPoster interface {
	PostJournal(ctx context.Context, in GLJournal) (string, error)
}

// RuleResolver resolves the 1200 inventory leg through the seeded inventory.adjusted rule. Satisfied by a
// main.go adapter over *gl.Service.ResolveRule.
type RuleResolver interface {
	ResolveRule(ctx context.Context, eventType, lineRole, branchID string) (string, error)
}

// GLJournal + GLLine mirror gl.ExternalJournal by value.
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

// Repository is invcount's pgx access layer over the 0040 tables (nil pool tolerated).
type Repository struct {
	pool *pgxpool.Pool
}

// NewRepository wraps a pool (may be nil in scaffold mode).
func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

// Service is the counts domain seam (sheets, variance, reason-coded posting, monthly recon).
type Service struct {
	repo  *Repository
	stock StockCounter
	gl    GLPoster
	rules RuleResolver
	glBal GLBalanceReader
	audit audit.Recorder
	log   *slog.Logger
}

// NewService wires the repository. Optional deps are injected via the With* methods.
func NewService(repo *Repository) *Service {
	return &Service{repo: repo, log: slog.Default()}
}

// WithStockCounter injects the inventory snapshot/adjust seam.
func (s *Service) WithStockCounter(c StockCounter) *Service { s.stock = c; return s }

// WithGLPoster injects the GL posting seam.
func (s *Service) WithGLPoster(p GLPoster) *Service { s.gl = p; return s }

// WithRuleResolver injects the GL posting-rule resolver.
func (s *Service) WithRuleResolver(r RuleResolver) *Service { s.rules = r; return s }

// WithAudit injects the A-21 Recorder.
func (s *Service) WithAudit(r audit.Recorder) *Service { s.audit = r; return s }

// PostSheet posts a counted sheet's variances (maker-checker gated; idempotent; reason→account matrix): it
// writes the signed COUNT stock-move per line through the inventory seam and the reason-coded GL journal (the
// reason leg + the 1200 inventory leg resolved via gl.ResolveRule), all keyed to a stable per-sheet
// idempotency key so a replay is a no-op. FROZEN signature.
func (s *Service) PostSheet(ctx context.Context, sheetID string) error {
	// DIB-390: the post is where the money moves, signed COUNT stock moves at weighted-average cost and
	// a GL journal, both stamped with the SHEET's branch. So the caller is fenced against that branch
	// (the loaded sheet's, never a request-supplied one) before any of it runs.
	if _, err := s.authorizeSheet(ctx, sheetID); err != nil {
		return err
	}
	return postCountedSheet(ctx, s.store(), s.stock, s.gl, s.rules, sheetID)
}

// ReconcileMonth runs the lazy inventory→GL recon (run-once per period+branch) and returns the tie delta
// (subledger − GL 1200). A second call for the same period returns the same run unchanged. Fenced to the
// caller's branch reach (FW1-10, the DIB-390 rule every count-sheet transition already carries): the
// recon takes a REQUIRED branch, which the caller must reach. FROZEN signature.
func (s *Service) ReconcileMonth(ctx context.Context, year, month int, branchID string) (int64, error) {
	if err := s.authorizeBranch(ctx, branchID); err != nil {
		return 0, err
	}
	runBy := identity.StaffUserIDFromContext(ctx)
	run, err := reconcileMonth(ctx, s.store(), s.stock, s.glBal, runBy, year, month, branchID)
	if err != nil {
		return 0, err
	}
	return run.DeltaCents, nil
}

// - handler -

// Handler exposes the counts HTTP surface. SCAFFOLD: routes registered, handlers answer 501.
type Handler struct {
	userLink  identity.UserIDLinker
	svc       *Service
	log       *slog.Logger
	costRoles CostRoles
}

// NewHandler wires the service.
func NewHandler(svc *Service, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.Default()
	}
	return &Handler{svc: svc, log: log}
}

// RegisterRoutes self-registers the count routes behind the identity guards. Reads are RequireStaff; sheet
// create/freeze/count are RequireStaff; the post (maker-checker) + recon are RequireManager. FROZEN signature.
func (h *Handler) RegisterRoutes(r httpx.Registrar, g *guards.Guards) {
	// reads + sheet create/freeze/count are staff; the checker approval, the post, and the recon are manager.
	r.Handle("GET /api/v1/counts/sheets", g.RequireStaff(h.staff(h.listSheets)))
	r.Handle("POST /api/v1/counts/sheets", g.RequireStaff(h.staff(h.createSheet)))
	r.Handle("GET /api/v1/counts/sheets/{id}", g.RequireStaff(h.staff(h.sheetDetail)))
	r.Handle("POST /api/v1/counts/sheets/{id}/freeze", g.RequireStaff(h.staff(h.freezeSheet)))
	r.Handle("POST /api/v1/counts/sheets/{id}/counts", g.RequireStaff(h.staff(h.submitCounts)))
	r.Handle("POST /api/v1/counts/sheets/{id}/approve", g.RequireManager(h.staff(h.approveSheet)))
	r.Handle("POST /api/v1/counts/sheets/{id}/post", g.RequireManager(h.staff(h.postSheet)))
	r.Handle("POST /api/v1/counts/sheets/{id}/cancel", g.RequireStaff(h.staff(h.cancelSheet)))
	r.Handle("GET /api/v1/counts/reason-codes", g.RequireStaff(h.staff(h.reasonCodes)))
	r.Handle("GET /api/v1/counts/negative-stock", g.RequireStaff(h.staff(h.negativeStock)))
	r.Handle("GET /api/v1/counts/recon", g.RequireManager(h.staff(h.recon)))
}

func (h *Handler) notImplemented(w http.ResponseWriter, _ *http.Request) {
	http.Error(w, "invcount: not implemented (scaffold)", http.StatusNotImplemented)
}
