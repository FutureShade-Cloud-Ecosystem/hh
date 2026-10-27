// Package bankrec is the W5 A-02 Bank Reconciliation module: statement import (a pluggable StatementSource),
// the auto-match engine (gateway-ref-exact / lodgement-ref-exact / run-settlement-fuzzy), manual match &
// unmatch with history, the clearing journals (DR 1000 / CR 1020 deposit; DR 1000 / CR 1010 settlement) via
// the GL provider seam, and the monthly reconciliation lifecycle. Owns the 0033 tables.
//
// SCAFFOLD STATUS: exported types + the frozen seam interfaces + the provider/consumer signatures are frozen
// here; bodies are errNotImplemented stubs so the tree compiles + boots. Worker W5-A02 fills them AND is
// merged LAST (the 1020 undeposited-funds doctrine needs GL posting live first, see the 0032 header). bankrec
// never reads the 0034 views directly: it reads GL balances only through GLBalanceReader (a main.go adapter
// over glSvc).
package bankrec

import (
	"context"
	"errors"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"io"
	"log/slog"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// errNotImplemented marks a scaffold stub the A-02 worker fills.
var errNotImplemented = errors.New("bankrec: not implemented (scaffold stub)")

// - seam interfaces (declared here; adapters live in till / main.go) -

// PaymentSource surfaces the unreconciled card payments to match against a statement, and marks the winners
// DEPOSITED once matched. Satisfied by *till.Repository via internal/till/recon_seam.go.
type PaymentSource interface {
	UnreconciledCardPayments(ctx context.Context, from, to time.Time) ([]CandidatePayment, error)
	MarkDeposited(ctx context.Context, paymentIDs []string, statementLineID string) error
}

// LodgementSource surfaces the open cash/cheque lodgements (till_movements of type LODGEMENT) to match
// against a bank deposit line. Satisfied by *till.Repository (recon_seam.go).
type LodgementSource interface {
	OpenLodgements(ctx context.Context, from, to time.Time) ([]Lodgement, error)
	// LodgementFacts answers the branch and the amount of one lodgement by its movement id (SF-2g round 2, SF-2j):
	// found is false for an id that is no lodgement. The till owns the movement; a lodgement is an append only
	// ledger row whose branch is its till session's and whose amount is its own, and neither changes, so the answer
	// cannot move between the reach and amount checks and the write.
	LodgementFacts(ctx context.Context, movementID string) (LodgementFact, bool, error)
}

// LodgementFact is the branch, amount and covered payment ids of one lodgement.
type LodgementFact struct {
	BranchID    string
	AmountCents int64
	PaymentIDs  []string
}

// JournalSource reads one journal for a manual JOURNAL match (SF-2j): the GL module's seam, satisfied by
// *gl.Service. bankrec never reads gl_journals itself.
// JournalMatchFact is one live JOURNAL match of a month, for CompleteReconciliation's recheck.
type JournalMatchFact struct {
	JournalID       string
	LineDescription string
	LineAmountCents int64
}

type JournalSource interface {
	JournalForBank(ctx context.Context, journalID, glAccountCode string) (gl.BankJournalFacts, bool, error)
}

// JournalPoster posts a clearing journal through the GL provider. Satisfied by a main.go adapter over
// *gl.Service.PostExternal. bankrec passes Source "ADJUSTMENT" (the 0007 CHECK set).
type JournalPoster interface {
	PostJournal(ctx context.Context, in gl.ExternalJournal) (string, error)
}

// GLBalanceReader reads a GL account balance for the variance check. Satisfied by a main.go adapter over
// *gl.Service.GLBalanceCents, bankrec never reads the 0034 views directly.
type GLBalanceReader interface {
	GLBalanceCents(ctx context.Context, accountCode string, year, month int, branchID string) (int64, error)
}

// StatementSource parses a raw statement export into normalized lines. CSV is impl #1 (A-02); a Plaid/RBC
// feed is a later adapter. The profile carries the column mapping (0033 bank_import_profiles.mapping).
type StatementSource interface {
	Parse(ctx context.Context, profile ImportProfile, r io.Reader) ([]RawStatementLine, error)
}

// - seam value types -

// CandidatePayment is one unreconciled card payment offered to the matcher.
type CandidatePayment struct {
	PaymentID   string
	BranchID    string
	AmountCents int64
	GatewayRef  string
	ReceivedAt  time.Time
}

// Lodgement is one open cash/cheque banking movement offered to the matcher.
type Lodgement struct {
	MovementID  string
	BranchID    string
	AmountCents int64
	Reference   string
	OccurredAt  time.Time
	PaymentIDs  []string
}

// ImportProfile is the column-mapping profile a StatementSource parses under (0033 bank_import_profiles).
type ImportProfile struct {
	ID      string
	Code    string
	Format  string
	Mapping map[string]any
}

// RawStatementLine is one parsed statement row before dedupe/persist. AmountCents is signed (+ credit / −
// debit); the importer computes DedupeHash deterministically for idempotent re-import.
type RawStatementLine struct {
	TxnDate     time.Time
	Description string
	Reference   string
	AmountCents int64
	DedupeHash  string
}

// - repository -

// Repository is the bankrec pgx access layer over the 0033 tables (a nil pool is tolerated in scaffold mode).
type Repository struct {
	pool *pgxpool.Pool
}

// NewRepository wraps a pool (may be nil in scaffold mode).
func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

// - service -

// Service is the bankrec domain seam. Sibling seams are injected via the With* builder options.
type Service struct {
	repo       *Repository
	payments   PaymentSource
	lodgements LodgementSource
	journals   JournalSource
	poster     JournalPoster
	balances   GLBalanceReader
	source     StatementSource
	audit      audit.Recorder
	log        *slog.Logger
}

// NewService wires the repository. Optional deps are injected via the With* methods.
func NewService(repo *Repository) *Service {
	return &Service{repo: repo, log: slog.Default()}
}

// WithPayments injects the till unreconciled-card-payment seam (+ MarkDeposited).
func (s *Service) WithPayments(p PaymentSource) *Service { s.payments = p; return s }

// WithJournals injects the GL journal seam a manual JOURNAL match reads (SF-2j). Unwired, a JOURNAL target is refused.
func (s *Service) WithJournals(j JournalSource) *Service { s.journals = j; return s }

// WithLodgements injects the till open-lodgement seam.
func (s *Service) WithLodgements(l LodgementSource) *Service { s.lodgements = l; return s }

// WithJournalPoster injects the GL provider (clearing journals).
func (s *Service) WithJournalPoster(j JournalPoster) *Service { s.poster = j; return s }

// WithGLBalances injects the GL balance reader (variance check).
func (s *Service) WithGLBalances(b GLBalanceReader) *Service { s.balances = b; return s }

// WithStatementSource injects the pluggable statement parser (CSV impl #1).
func (s *Service) WithStatementSource(src StatementSource) *Service { s.source = src; return s }

// WithAudit injects the A-21 Recorder.
func (s *Service) WithAudit(r audit.Recorder) *Service { s.audit = r; return s }

// - handler -

// Handler exposes the bankrec HTTP surface (accounts, import, match/unmatch, rules, reconciliation).
// SCAFFOLD: routes are registered but every handler answers 501 until A-02 fills the service.
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

// RegisterRoutes self-registers the bankrec routes. reads are RequireStaff; import/match are RequireManager;
// rule approval + reconciliation completion are RequireAdmin. Signature is FROZEN across the W5 handlers.
func (h *Handler) RegisterRoutes(r httpx.Registrar, g *guards.Guards) {
	r.Handle("GET /api/v1/bankrec/accounts", g.RequireStaff(h.staff(h.accounts)))
	r.Handle("POST /api/v1/bankrec/imports", g.RequireManager(h.staff(h.createImport)))
	r.Handle("GET /api/v1/bankrec/lines", g.RequireStaff(h.staff(h.lines)))
	r.Handle("GET /api/v1/bankrec/matches", g.RequireStaff(h.staff(h.matchHistory)))
	r.Handle("POST /api/v1/bankrec/match", g.RequireManager(h.staff(h.match)))
	r.Handle("POST /api/v1/bankrec/unmatch", g.RequireManager(h.staff(h.unmatch)))
	r.Handle("GET /api/v1/bankrec/rules", g.RequireStaff(h.staff(h.rules)))
	r.Handle("POST /api/v1/bankrec/rules", g.RequireAdmin(h.staff(h.createRule)))
	r.Handle("GET /api/v1/bankrec/reconciliations", g.RequireStaff(h.staff(h.reconciliations)))
	r.Handle("POST /api/v1/bankrec/reconciliations", g.RequireManager(h.staff(h.openReconciliation)))
	r.Handle("POST /api/v1/bankrec/reconciliations/{id}/complete", g.RequireAdmin(h.staff(h.completeReconciliation)))
}
