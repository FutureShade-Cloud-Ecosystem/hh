// Package finstmt is the W5 A-04 Financial Statements module: Income Statement, Balance Sheet, Trial Balance,
// GL Detail, and AR Aging, each built ONLY from the 0034 POSTED-filtered views (+ fiscal_periods) through
// this module's own repository. It performs no posting and reads no other module's tables; a subtly-wrong
// view is fixed by the integrator in the reserved 0036 (CREATE OR REPLACE VIEW), never by finstmt.
//
// SCAFFOLD STATUS: exported types + the handler/route surface are frozen; bodies are errNotImplemented stubs
// so the tree compiles + boots. Worker W5-A04 fills them (statements, per-branch + consolidated, comparatives,
// drill-down, aging buckets, CSV/Excel export, @media print = the W5 PDF path, finstmt_schedules CRUD).
package finstmt

import (
	"context"
	"log/slog"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// StatementRow is one line of a rendered statement (account-level). Amounts are BIGINT cents.
type StatementRow struct {
	AccountCode string `json:"account_code"`
	AccountName string `json:"account_name"`
	AccountType string `json:"account_type"`
	AmountCents int64  `json:"amount_cents"`
}

// AgingRow is one AR aging entry (invoice or non-invoice customer_transactions row). Bucket is the age band.
type AgingRow struct {
	CustomerID   string `json:"customer_id"`
	CustomerName string `json:"customer_name"`
	SourceType   string `json:"source_type"`
	SourceRefID  string `json:"source_ref_id"`
	BranchID     string `json:"branch_id"`
	AmountCents  int64  `json:"amount_cents"`
	AgeDays      int    `json:"age_days"`
	Bucket       string `json:"bucket"`
}

// Schedule is a scheduled-report registry row (0034 finstmt_schedules; registry only at W5).
type Schedule struct {
	ID         string `json:"id"`
	Report     string `json:"report"`
	Recipients string `json:"recipients"`
	Cadence    string `json:"cadence"`
	BranchID   string `json:"branch_id"`
	IsActive   bool   `json:"is_active"`
}

// -- repository --------------------------------------------------------------

// Repository is finstmt's read layer over the 0034 views + fiscal_periods (a nil pool is tolerated).
type Repository struct {
	pool *pgxpool.Pool
}

// NewRepository wraps a pool (may be nil in scaffold mode).
func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

// -- service -----------------------------------------------------------------

// Service builds the statements from the repository's views. WithAudit is the only optional dep.
type Service struct {
	repo  *Repository
	audit audit.Recorder
	log   *slog.Logger
	// callerFrom reads the guard-stamped staff caller (DIB-391 branch fence). Injectable so unit tests
	// can pose as a scoped or global caller without an HTTP round trip (the landedcost/gl idiom).
	callerFrom func(context.Context) (identity.StaffContext, bool)
	// now is the clock the DIB-454 .xlsx exports stamp their "generated" line and their as-at date
	// from. Injectable for the same reason every other clock in this codebase is: a subtitle built
	// from time.Now() is a cell no test can assert (DIB-427).
	now func() time.Time
}

// NewService wires the repository.
func NewService(repo *Repository) *Service {
	return &Service{repo: repo, log: slog.Default(), callerFrom: identity.StaffContextFromContext}
}

// WithClock pins the export clock. Exported because the walkthrough harness needs it too; every
// production caller leaves it nil and gets time.Now.
func (s *Service) WithClock(now func() time.Time) *Service {
	if now != nil {
		s.now = now
	}
	return s
}

// withCaller swaps the caller resolver (tests only).
func (s *Service) withCaller(f func(context.Context) (identity.StaffContext, bool)) *Service {
	s.callerFrom = f
	return s
}

// caller resolves the trusted, server-stamped acting staff member (never a spoofable header).
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

// WithAudit injects the A-21 Recorder (export access stamping).
func (s *Service) WithAudit(r audit.Recorder) *Service { s.audit = r; return s }

// IncomeStatement returns the P&L rows for a fiscal year/month/branch ("" branch = consolidated).
func (s *Service) IncomeStatement(ctx context.Context, year, month int, branchID string) ([]StatementRow, error) {
	if err := s.authorizeBranch(ctx, branchID); err != nil {
		return nil, err
	}
	return s.repo.IncomeStatement(ctx, year, month, branchID)
}

// BalanceSheet returns the balance-sheet rows for a fiscal year/branch.
func (s *Service) BalanceSheet(ctx context.Context, year int, branchID string) ([]StatementRow, error) {
	if err := s.authorizeBranch(ctx, branchID); err != nil {
		return nil, err
	}
	return s.repo.BalanceSheet(ctx, year, branchID)
}

// TrialBalance returns the trial-balance rows for a fiscal year.
func (s *Service) TrialBalance(ctx context.Context, year int) ([]StatementRow, error) {
	if err := s.authorizeConsolidated(ctx); err != nil {
		return nil, err
	}
	return s.repo.TrialBalance(ctx, year)
}

// ARAging returns the AR aging rows (invoice + non-invoice), whose grand total ties to ARBalanceCents.
func (s *Service) ARAging(ctx context.Context, branchID string) ([]AgingRow, error) {
	if err := s.authorizeBranch(ctx, branchID); err != nil {
		return nil, err
	}
	return s.repo.ARAging(ctx, branchID)
}

// -- handler -----------------------------------------------------------------

// Handler exposes the finstmt HTTP surface. SCAFFOLD: routes registered, every handler answers 501.
type Handler struct {
	userLink identity.UserIDLinker
	svc      *Service
	log      *slog.Logger
	// registerSender is the DIB-510 receivables register's delivery port (dib510_register_mount.go).
	// Optional and nil by default, see WithRegisterSender for why unbound is the honest shipped state.
	registerSender RegisterSender
	// paymentTrail says whether a caller holds the payment trail reads (ruling 87); the register's
	// invoice detail shows the payment entries of its timeline to those callers alone.
	paymentTrail billing.PaymentTrailReader
}

// NewHandler wires the service.
func NewHandler(svc *Service, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.Default()
	}
	return &Handler{svc: svc, log: log}
}

// RegisterRoutes self-registers the finstmt routes. Statements are RequireManager (finance information);
// schedules mutation is RequireAdmin (Controller-only). Signature is FROZEN across the W5 handlers.
// Every read route also accepts ?format=csv|xlsx for the export path. Handler bodies live in handler.go.
func (h *Handler) RegisterRoutes(r httpx.Registrar, g *guards.Guards) {
	r.Handle("GET /api/v1/finstmt/income-statement", g.RequireManager(h.staff(h.incomeStatement)))
	r.Handle("GET /api/v1/finstmt/balance-sheet", g.RequireManager(h.staff(h.balanceSheet)))
	r.Handle("GET /api/v1/finstmt/trial-balance", g.RequireManager(h.staff(h.trialBalance)))
	r.Handle("GET /api/v1/finstmt/gl-detail", g.RequireManager(h.staff(h.glDetail)))
	r.Handle("GET /api/v1/finstmt/ar-aging", g.RequireManager(h.staff(h.arAging)))
	r.Handle("GET /api/v1/finstmt/schedules", g.RequireManager(h.staff(h.listSchedules)))
	r.Handle("POST /api/v1/finstmt/schedules", g.RequireAdmin(h.staff(h.createSchedule)))
	r.Handle("PUT /api/v1/finstmt/schedules/{id}", g.RequireAdmin(h.staff(h.updateSchedule)))
	r.Handle("DELETE /api/v1/finstmt/schedules/{id}", g.RequireAdmin(h.staff(h.deleteSchedule)))
	// DIB-510: the receivables register. Implemented in internal/billing and mounted here because this
	// is the Finance unit's registration and the one in the composition root handed all three gates -
	// see dib510_register_mount.go for why it could not live next to the AR console's own routes.
	h.mountInvoiceRegister(r, g)
}
