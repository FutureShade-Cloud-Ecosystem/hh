// Package landedcost is the W6 A-16 Landed Cost module: freight/duty/handling capitalized into product cost,
// allocated across received PO lines by weight/value/quantity (default value), with USD-PO support (a manual
// FX rate; the amounts convert to CAD before the GL, the DX-19 scoped exception, GL stays CAD) and late-
// freight retro-adjustment (capitalize the still-on-hand portion, split the sold-through portion to COGS). It
// owns the 0041 landed_costs + landed_cost_allocations tables. Depends on INV-CORE.
//
// SCAFFOLD STATUS: exported types, the frozen consumer-declared seams, and the service signatures are frozen;
// bodies are errNotImplemented stubs so the tree compiles + boots. Worker W6-A16 fills the bodies. FROZEN.
package landedcost

import (
	"context"
	"errors"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"log/slog"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// errNotImplemented marks a scaffold stub the A-16 worker fills.
var errNotImplemented = errors.New("landedcost: not implemented (scaffold stub)")

// AllocationBasis is the apportionment basis (default VALUE).
const (
	BasisWeight   = "WEIGHT"
	BasisValue    = "VALUE"
	BasisQuantity = "QUANTITY"
)

// - consumer-declared seams (adapters over inventory + gl live in main.go) -

// Capitalizer surfaces the received PO lines to allocate over, and capitalizes an allocated landed cost into
// the weighted-average cost (CAD). Satisfied by a main.go adapter over *inventory.Service (landed_seam.go).
type Capitalizer interface {
	ReceivedLinesForPO(ctx context.Context, poID string) ([]ReceivedLine, error)
	// CapitalizeLandedCostTx capitalizes the allocation into the weighted-average cost and returns the value the
	// subledger actually absorbed (0 when there is no on-hand; less than the allocation by the integer-cent
	// rounding remainder). The service books GL 1200 from the return and expenses the remainder to COGS.
	CapitalizeLandedCostTx(ctx context.Context, tx pgx.Tx, productID, branchID string, allocatedCadCents int64) (int64, error)
	// CapitalizeLandedCostSnapTx capitalizes as above AND returns the exact pre-Apply weighted-average (snapAvg) it
	// capitalized over plus whether a cost row existed. Apply persists snapAvg on the allocation row (0058); Void
	// restores it verbatim via RestoreLandedCostTx so an Apply/Void round-trip reproduces the WAC to the cent even
	// for sub-unit fractional on-hand where the forward round() quantizes oldAvg away (L8 MAJOR).
	CapitalizeLandedCostSnapTx(ctx context.Context, tx pgx.Tx, productID, branchID string, allocatedCadCents int64) (int64, int64, bool, error)
	// RestoreLandedCostTx reverses the capitalization on Void by removing EXACTLY `absorbed` (the value this allocation
	// raised the subledger by at Apply, the allocation's post-Apply AllocatedCadCents) and returns the realized
	// subledger value delta (the CR 1200 magnitude the Void journal books). The GENERAL cent-exact inverse for every
	// intervening scenario (nothing / receipt / sale): Σ(debit)==Σ(credit) always and no leg is ever negative. snapAvg
	// restores the exact pre-Apply WAC on the drift-0 path where on-hand is unchanged since Apply (L8 MAJOR).
	RestoreLandedCostTx(ctx context.Context, tx pgx.Tx, productID, branchID string, snapAvg, absorbed int64) (int64, error)
}

// ReceivedLine mirrors inventory.ReceivedLine (the allocation-basis inputs).
type ReceivedLine struct {
	POLineID     string  `json:"po_line_id"`
	ProductID    string  `json:"product_id"`
	QtyBase      float64 `json:"qty_base"`
	ValueCents   int64   `json:"value_cents"`
	WeightKg     float64 `json:"weight_kg"`
	IsSpecialBuy bool    `json:"is_special_buy"`
}

// GLPoster posts the landed-cost capitalization / retro-split journal through the GL provider. Satisfied by a
// wire_operations.go adapter over *gl.Service. PostJournal is the pool variant (apply / retroAdjust post
// OUTSIDE their transactions, between two WithinTx calls); PostJournalTx is the tx-aware variant (Void posts
// its reversing journal INSIDE the void's own transaction, so a caller that already holds a pool connection
// never asks the shared pool for a second one and the journal commits or rolls back with the caller).
type GLPoster interface {
	PostJournal(ctx context.Context, in GLJournal) (string, error)
	// PostJournalTx is PostJournal through the caller's transaction (a nil tx is refused by the GL store
	// rather than falling back to the pool).
	PostJournalTx(ctx context.Context, tx pgx.Tx, in GLJournal) (string, error)
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

// Repository is landedcost's pgx access layer over the 0041 tables (nil pool tolerated).
type Repository struct {
	pool *pgxpool.Pool
}

// NewRepository wraps a pool (may be nil in scaffold mode).
func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

// Service is the landed-cost domain seam (allocation, USD→CAD, retro-adjustment). Sibling seams injected via
// the With* options.
type Service struct {
	repo  *Repository
	stock Capitalizer
	gl    GLPoster
	audit audit.Recorder
	log   *slog.Logger
	// callerFrom resolves the trusted, server-stamped acting staff member every branch-scoped read/mutation
	// gates against (handler.go / service.go CanAccessBranch checks). Defaults to the identity guard-stamped
	// caller; tests override via withCaller. Mirrors order.Service.callerFrom (order/service.go:68).
	callerFrom func(context.Context) (identity.StaffContext, bool)
}

// NewService wires the repository. Optional deps are injected via the With* methods.
func NewService(repo *Repository) *Service {
	return &Service{repo: repo, log: slog.Default(), callerFrom: identity.StaffContextFromContext}
}

// withCaller is the test seam for the guard-stamped StaffContext (branch scope on every landed-cost surface).
// Mirrors order.Service.withCaller (order/service.go:83).
func (s *Service) withCaller(f func(context.Context) (identity.StaffContext, bool)) *Service {
	s.callerFrom = f
	return s
}

// caller resolves the trusted, server-stamped acting staff member (never a spoofable header), the auth subject
// every branch-scoped landed-cost surface gates against. Mirrors order.Service.caller (order/service.go:90).
func (s *Service) caller(ctx context.Context) (identity.StaffContext, error) {
	if s.callerFrom == nil {
		return identity.StaffContext{}, errUnauthorized
	}
	c, ok := s.callerFrom(ctx)
	if !ok || c.UserID == "" {
		return identity.StaffContext{}, errUnauthorized
	}
	return c, nil
}

// WithCapitalizer injects the inventory capitalization seam.
func (s *Service) WithCapitalizer(c Capitalizer) *Service { s.stock = c; return s }

// WithGLPoster injects the GL posting seam.
func (s *Service) WithGLPoster(p GLPoster) *Service { s.gl = p; return s }

// WithAudit injects the A-21 Recorder.
func (s *Service) WithAudit(r audit.Recorder) *Service { s.audit = r; return s }

// Apply allocates + capitalizes a landed cost (USD→CAD conversion, the 3-basis split, exclude-special-buys).
// FROZEN signature; body delegates to service.go.
func (s *Service) Apply(ctx context.Context, landedCostID string) error {
	return s.apply(ctx, landedCostID)
}

// RetroAdjust applies a late-freight adjustment: capitalize the still-on-hand portion, split the sold-through
// portion to COGS. FROZEN signature; body delegates to service.go.
func (s *Service) RetroAdjust(ctx context.Context, landedCostID string) error {
	return s.retroAdjust(ctx, landedCostID)
}

// - handler -

// Handler exposes the landed-cost HTTP surface. SCAFFOLD: routes registered, handlers answer 501.
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

// RegisterRoutes self-registers the landed-cost routes behind the identity guards. Reads are RequireStaff;
// create/apply/retro are RequireManager. FROZEN guard signature; A-16 fills the route bodies.
func (h *Handler) RegisterRoutes(r httpx.Registrar, g *guards.Guards) {
	r.Handle("GET /api/v1/landed-costs", g.RequireStaff(h.staff(h.list)))
	r.Handle("POST /api/v1/landed-costs", g.RequireManager(h.staff(h.create)))
	r.Handle("GET /api/v1/landed-costs/{id}", g.RequireStaff(h.staff(h.get)))
	r.Handle("GET /api/v1/landed-costs/{id}/preview", g.RequireStaff(h.staff(h.preview)))
	r.Handle("POST /api/v1/landed-costs/{id}/apply", g.RequireManager(h.staff(h.apply)))
	r.Handle("POST /api/v1/landed-costs/{id}/retro", g.RequireManager(h.staff(h.retro)))
	r.Handle("POST /api/v1/landed-costs/{id}/void", g.RequireManager(h.staff(h.void)))
}
