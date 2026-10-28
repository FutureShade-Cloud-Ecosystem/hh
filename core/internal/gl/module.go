// Package gl is the W5 A-01 General Ledger module: chart of accounts, journal lifecycle (maker-checker),
// the transactional-outbox posting consumer, trial balance + subledger tie-out, period/year close, and the
// GP-parity export. It EVOLVES the 0007 GL block (gl_accounts / gl_journals / gl_journal_lines /
// posting_rules / fiscal_periods) with the 0032 additions (idempotency_key, gl_policy, tie-out state, …).
//
// SCAFFOLD STATUS: this file freezes the exported types, the consumer-defined seam interfaces, and the
// provider method signatures that bankrec/finstmt/main.go depend on. Bodies are errNotImplemented stubs so
// the tree compiles + boots; worker W5-A01 fills them (PostExternal + GLBalanceCents must go real early -
// bankrec depends on them). Signatures are FROZEN, a worker fills a body, never changes a signature.
//
// TOPIC → POSTING_RULES.event_type MAPPING (frozen; mirrored in migration 0032's header):
//
//	'ar.invoice.finalized'  → 'invoice.finalized'   (the 0007 legacy key, its rules already live there)
//	every other outbox topic → its LITERAL topic name (0032 seeds the new rules under the topic names)
//
// The two-name scheme is recorded here AND in the migration so a literal-topic posting-rules lookup can
// never silently resolve to zero rules. The eight consumed topics are the RegisterPostingHandlers set.
//
// order.fulfilled PAYLOAD CONTRACT (frozen for the W6 inventory cross-wave):
//
//	{order_id, branch_id, lines:[{product_id, qty_base}]}
//
// The consumer calls CostBasis per line against posting_rules {cogs:5000, inventory:1200}. CostBasis is
// dormant at W5 (main.go wires NoopCostBasis, found=false); the real impl arrives with W6 inventory.
package gl

import (
	"context"
	"errors"
	"fmt"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/taxwords"
	"log/slog"
	"sync"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// errNotImplemented marks a scaffold stub the A-01 worker fills. Every method below returns it until then.
var errNotImplemented = errors.New("gl: not implemented (scaffold stub)")

// - consumer-defined seam interfaces (declared here; adapters live in the sibling modules) -

// InvoiceReader resolves the finance facts of a finalized invoice for the ar.invoice.finalized posting.
// Satisfied by *billing.Repository via internal/billing/gl_seam.go (same-package pool access).
type InvoiceReader interface {
	InvoiceForGL(ctx context.Context, invoiceID string) (InvoiceAmounts, error)
}

// InvoiceAmounts is the finance projection of an invoice used to build its journal legs.
type InvoiceAmounts struct {
	SubtotalCents int64
	TaxCents      int64
	TotalCents    int64
	HoldbackCents int64
	BranchID      string
	CustomerID    string
	InvoiceNo     string
	FinalizedAt   time.Time
	// SourceKind is the document's provenance (invoices.source_kind): the
	// builder posts SUPPLIER_DEPOSIT documents to Customer Deposits instead of
	// revenue and tax (sub item 9.5), the deposit being the liability it is
	// until the special order's fulfilment applies it.
	SourceKind string
}

// TaxSplitReader resolves the FN relief split (full HST / on-credit / net collected) recorded on the sale's
// tax_decision_snapshot, for the tax.relief.assessed posting. Satisfied by *pos.Repository (gl_seam.go).
type TaxSplitReader interface {
	TaxSplit(ctx context.Context, orderID string) (full, onCredit, net int64, found bool, err error)
}

// ARSubledger returns the AR subledger balance as-of a date for the 1100 tie-out. Satisfied by
// *billing.Repository (gl_seam.go). A branchID of "" means the consolidated balance.
type ARSubledger interface {
	ARBalanceCents(ctx context.Context, asOf time.Time, branchID string) (int64, error)
}

// PaymentReader resolves the branch/customer/method/status facts of a payment. REQUIRED: the pos
// payment.received payload carries no branch/invoice, so the consumer resolves the facts here and chooses
// the 1010 (card clearing) / 1020 (undeposited) / 1000 (bank) leg. Satisfied by *till.Repository.
type PaymentReader interface {
	PaymentForGL(ctx context.Context, paymentID string) (PaymentFacts, error)
}

// PaymentFacts is the finance projection of a payment used to pick its debit leg + branch-stamp.
type PaymentFacts struct {
	BranchID    string
	CustomerID  string
	Method      string
	Status      string
	AmountCents int64
	// ReceivedAt is the payment's own receipt instant (payments.received_at): the receipt
	// document's date. FW1-01b: the receipt journal takes this date, not the processing day,
	// so a receipt taken on the last day of a month and consumed after midnight stays in the
	// month it was taken. Zero means the seam did not resolve one, and the journal falls back
	// to the clock.
	ReceivedAt time.Time
}

// CostBasis returns the unit cost for a fulfilled line (order.fulfilled → COGS). Dormant at W5: NoopCostBasis
// returns found=false so the COGS leg is skipped until the W6 inventory module supplies a real cost basis.
type CostBasis interface {
	UnitCostCents(ctx context.Context, productID string, qtyBase float64) (int64, bool, error)
}

// BranchCostBasis is the branch-scoped cost basis seam: it returns the extended cost
// for a line at its fulfilling branch so the journal uses the same branch weighted-average
// cost as the stock move.
type BranchCostBasis interface {
	BranchUnitCostCents(ctx context.Context, productID, branchID string, qtyBase float64) (int64, bool, error)
}

// DeadRequeuer re-queues DEAD outbox rows for the named topics (the boot-time replay of pre-W5 rows that
// were emitted before a handler existed). Satisfied by *notify.PgOutboxStore (internal/notify/requeue.go).
type DeadRequeuer interface {
	RequeueDead(ctx context.Context, topics []string) (int, error)
}

// BranchFacts is the branch facts seam the head office branch reads and writes check their answer
// through (FW1-08b part B, FROM-PRINCIPAL 176): whether an id names a branch row at all
// (loc_type = 'BRANCH'), whether it is a branch of the caller's org, and which branch a yard code
// names (ruling 162: the default reads TRT by code). branches owns locations; gl reads it only
// through this seam, bound in the composition root over *branches.Repository. Satisfied by
// core/cmd/core's glBranchFacts adapter.
type BranchFacts interface {
	Exists(ctx context.Context, id string) (bool, error)
	BranchInOrg(ctx context.Context, orgID, branchID string) (bool, error)
	BranchRowByCodeTx(ctx context.Context, tx pgx.Tx, code string) (branchID string, ok bool, err error)
}

// ApprovalFiler is GL's nil-able approvals-rail seam (0075). When CreateJournal parks a MANUAL journal as
// DRAFT (at/over the shipped gl_policy maker-checker threshold), GL files a JE_OVER_THRESHOLD approval
// request through this seam IN THE PARK'S TRANSACTION (ruling 108): the journal and its request commit
// together, or the create fails and neither exists, so a journal is never parked that can only be posted
// through a request that was never filed. The returned func is the card fan-out, run by GL after the commit.
// The adapter (main.go) wraps identity.Approvals.FileJEApprovalTx. Nil (unwired) is the shipped park
// behavior: a bare DRAFT with no request.
type ApprovalFiler interface {
	FileJEApprovalTx(ctx context.Context, tx pgx.Tx, journalID, branchID string, amountCents int64, createdBy string) (func(context.Context), error)
}

// ApprovalState is where a parked journal stands with the approvals rail.
type ApprovalState string

const (
	ApprovalNone          ApprovalState = "none"           // no JE_OVER_THRESHOLD request exists for the journal
	ApprovalPending       ApprovalState = "pending"        // a request is waiting for its decision
	ApprovalRejected      ApprovalState = "rejected"       // the request was declined
	ApprovalDeleted       ApprovalState = "deleted"        // the request was tombstoned (soft deleted)
	ApprovalAmountDiffers ApprovalState = "amount_differs" // approved, but for another amount than the journal now carries
	ApprovalApproved      ApprovalState = "approved"       // approved for the journal's current amount
)

// ApprovalStateChecker is the rail read the GL's own approve route asks before it posts: where does this
// journal stand with its JE_OVER_THRESHOLD request, given the amount the journal carries now. A journal over
// the threshold is posted by this route only on ApprovalApproved, an APPROVED request for its current
// amount; every other state refuses in its own words, and what happens to the request is decided in the
// approvals queue and not here. An unwired checker refuses the route: a money guard that cannot ask does
// not post.
type ApprovalStateChecker interface {
	JournalApprovalState(ctx context.Context, journalID string, amountCents int64) (ApprovalState, error)
}

// - delivery date and audit seams (round 2, S2 P1-2 and P2-4) -

// DeliveryReader answers the delivery fact MarkDelivered records on an order: when the goods left,
// found=false when the order carries no stamp. Satisfied by order.Repository, wired in cmd/core.
type DeliveryReader interface {
	DeliveredAt(ctx context.Context, orderID string) (time.Time, bool, error)
}

// AuditTxRecorder is the audit seam the Controller's open year records through, on the caller's
// transaction (round 2, S2 P2-4): the audit row commits or rolls back WITH the year it opens.
// Satisfied by audit.Service.RecordTx, wired in cmd/core. Nil (or unwired) records nothing.
type AuditTxRecorder interface {
	RecordTx(ctx context.Context, q audit.Execer, a audit.SensitiveAccess) error
}

// - close pre flight seams (FW1-09b; nil = the check is not wired and passes with no figures) -

// StockValueReader is the stock ledger side of the close pre flight's 1200 tie check. Satisfied by
// inventory.Service.SnapshotOnHand summed (the weighted average on-hand value), wired in cmd/core.
// The branch id is the filter: the pre flight reads the COMPANY (an empty branch id), so the reader
// answers with the consolidated value, and a named branch answers with that branch's (FW1-09d).
type StockValueReader interface {
	StockValueCents(ctx context.Context, branchID string) (int64, error)
}

// TillDrawerReader is the till side of the close pre flight: how many till sessions opened in the
// month window stand OPEN still. The window is the pre flight's to compute (the dealer's zone); the
// reader only queries. Satisfied by till.Repository, wired in cmd/core.
type TillDrawerReader interface {
	OpenDrawers(ctx context.Context, from, to time.Time) (int, error)
}

// OutboxQueueReader is the outbox side of the close pre flight: posting events for the month that are
// still waiting (PENDING) or dead lettered (DEAD). Satisfied by notify.PgOutboxStore, wired in cmd/core.
type OutboxQueueReader interface {
	UnpostedForWindow(ctx context.Context, topics []string, from, to time.Time) (int, error)
}

// NoopCostBasis is the dormant W5 cost-basis seam: it always reports "no cost basis" (found=false) so the
// COGS leg of an order.fulfilled posting is skipped until the W6 inventory module supplies a real one. The
// scaffold wires this into glSvc; W6 swaps in the inventory-backed impl.
type NoopCostBasis struct{}

// UnitCostCents always returns found=false (dormant).
func (NoopCostBasis) UnitCostCents(ctx context.Context, productID string, qtyBase float64) (int64, bool, error) {
	return 0, false, nil
}

// - provider types (bankrec passes these into PostExternal) -

// ExternalJournal is a caller-built journal posted through the GL provider seam. Source uses the 0007 CHECK
// set (MANUAL/INVOICE/PAYMENT/FULFILLMENT/PO_RECEIPT/ADJUSTMENT/CLOSING); bankrec passes "ADJUSTMENT".
// IdempotencyKey de-dupes a replayed post (gl_journals.idempotency_key partial-unique, 0032).
type ExternalJournal struct {
	Source         string
	SourceRefID    string
	IdempotencyKey string
	Memo           string
	BranchID       string
	Lines          []ExternalLine
	// DocumentDate, when set, is the document the journal belongs to (a bankrec clearing journal's
	// statement line): the journal posts in that document's month when it is open, and into the
	// earliest open month, dated by the document and late posted, when it is closed (ruling 82.2).
	// Zero keeps the clock.
	//
	// A document date is a CALENDAR DAY in the dealer's own zone (houseday), the instant that day's
	// house midnight begins, never midnight at Greenwich: the ledger files a journal by the month of
	// the instant in the dealer's zone, so a Greenwich midnight (the previous evening in the house
	// zone) would file a document dated the first of a month into the month before it.
	DocumentDate time.Time
}

// ExternalLine is one leg of an ExternalJournal, addressed by account code (resolved to account_id on post).
type ExternalLine struct {
	AccountCode string
	DebitCents  int64
	CreditCents int64
	Memo        string
}

// - repository -

// Repository is the GL pgx access layer. It reads/writes ONLY GL-owned tables (the 0007 block + 0032
// additions) and the 0034 balance views. A nil pool is tolerated (scaffold/offline mode).
type Repository struct {
	pool *pgxpool.Pool
	// zone is the dealer's business day zone, read ON the posting transaction; nil = the Ontario
	// dealer's. It decides which fiscal period a posting's instant belongs to.
	zone ZoneTxSource
	// hoMu and hoOverride hold an in-process head office branch override before the FW1-08b
	// migration adds gl_policy.head_office_branch_id; once the column exists, gl_policy is the
	// authoritative source.
	hoMu       sync.RWMutex
	hoOverride string
	// branchFacts is the branch facts seam the head office default's read TRT by code runs
	// through (ruling 162); nil in unit scaffolds, where the fixed Trenton uuid stands in.
	branchFacts BranchFacts
}

// ZoneTxSource is the dealer's business day zone read on a caller's open transaction. A journal is
// filed to its period inside the posting transaction, so the zone is read on that transaction and
// never through the pool: a pool read there makes the holder wait on a second connection, which a full
// pool of such holders never frees. The composition root adapts the settings module's dealer
// configuration into one.
type ZoneTxSource interface {
	BusinessZoneTx(ctx context.Context, tx pgx.Tx) (houseday.Zone, error)
}

// NewRepository wraps a pool (may be nil in scaffold mode).
func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

// WithZone binds the dealer's business day zone source. Nil keeps the Ontario dealer's.
func (r *Repository) WithZone(src ZoneTxSource) *Repository { r.zone = src; return r }

// journalZone answers the zone a posting's period is read in, on the posting transaction.
func (r *Repository) journalZone(ctx context.Context, tx pgx.Tx) (houseday.Zone, error) {
	if r.zone == nil {
		return houseday.DefaultZone(), nil
	}
	z, err := r.zone.BusinessZoneTx(ctx, tx)
	if err != nil {
		return houseday.Zone{}, fmt.Errorf("gl: the dealer's business zone could not be read, nothing was posted: %w", err)
	}
	return z, nil
}

// - service -

// Service is the GL domain seam. It owns the journal lifecycle, the posting consumer, tie-out, and close.
// Sibling seams are injected via the With* builder options (main.go wires the real adapters).
type Service struct {
	repo            *Repository
	store           glStore
	invoices        InvoiceReader
	taxSplits       TaxSplitReader
	ar              ARSubledger
	payments        PaymentReader
	costBasis       CostBasis
	dead            DeadRequeuer
	audit           audit.Recorder
	stockValue      StockValueReader
	drawers         TillDrawerReader
	preFlightQueue  OutboxQueueReader    // the pre flight's window read: how many posted events in the closing month are still waiting or dead lettered
	approvalFiler   ApprovalFiler        // 0075: nil-able JE park → approvals-rail filing seam
	standardRate    StandardRateReader   // the dealer's standard tax rate; nil is the Ontario 13 percent
	taxWords        taxwords.Source      // the name of the dealer's tax in journal memos; nil is "HST"
	approvalChecker ApprovalStateChecker // the approve route posts a journal over the threshold only on an approved request; unwired, it refuses every journal
	delivery        DeliveryReader       // round 2: the delivery fact the deposit release's date reads
	auditTx         AuditTxRecorder      // round 2: the open year's audit, in the year's transaction
	strictPreFlight bool                 // round 4: production refuses a close when a pre flight check is unwired; tests may pass nil
	queue           OutboxQueue          // the posting queue's rows: the waiting/dead events the Controller lists and the banner counts (nil-able: unwired, the queue reads refuse)
	branchFacts     BranchFacts          // the branch facts the head office branch reads and writes check their answer through (unwired, the set refuses: fail closed)
	log             *slog.Logger
	now             func() time.Time
	callerFrom      func(context.Context) (identity.StaffContext, bool)
}

// NewService wires the repository. Optional deps are injected via the With* methods. The repository is the
// default glStore (the pgx access layer); unit tests swap in a DB-free fake via withStore. callerFrom reads
// the guard-stamped StaffContext (created_by, the four-eyes approver, the Controller reopen gate).
func NewService(repo *Repository) *Service {
	return &Service{
		repo:       repo,
		store:      repo,
		log:        slog.Default(),
		now:        time.Now,
		callerFrom: identity.StaffContextFromContext,
	}
}

// withStore swaps the persistence seam (unit tests inject a DB-free fake).
func (s *Service) withStore(st glStore) *Service { s.store = st; return s }

// withCaller swaps the guard-stamped StaffContext resolver (unit tests).
func (s *Service) withCaller(f func(context.Context) (identity.StaffContext, bool)) *Service {
	s.callerFrom = f
	return s
}

// withClock injects a deterministic clock (unit tests).
func (s *Service) withClock(now func() time.Time) *Service { s.now = now; return s }

// withLog swaps the structured logger (unit tests assert on WARN/observability output).
func (s *Service) withLog(l *slog.Logger) *Service { s.log = l; return s }

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

// authorizeBranchRead fences a branch-filtered LEDGER READ (DIB-392). The rule is landedcost's
// (service.go:97-103): a GLOBAL caller is unrestricted; a scoped caller must name a branch they can
// reach, an empty filter means "every branch", which is not theirs to ask for.
func (s *Service) authorizeBranchRead(ctx context.Context, branchID string) error {
	caller, err := s.caller(ctx)
	if err != nil {
		return err
	}
	if branchID == "" {
		if caller.Scope != identity.ScopeGlobal {
			return errForbidden
		}
		return nil
	}
	if !caller.CanAccessBranch(branchID) {
		return errForbidden
	}
	return nil
}

// errForbiddenWrite is the write fence's plain words (round 3, SECURITY-SOD
// gap 19): the refusal names the books the sign in covers, never a bare code.
var errForbiddenWrite = clientError{status: 403, msg: "gl: this write reaches books your sign in does not cover; a scoped sign in writes only its own branches, and the consolidated books need a dealer wide one"}

// authorizeBranchWrite fences a LEDGER WRITE (round 3) the way
// authorizeBranchRead fences the reads: a dealer wide (GLOBAL) caller is
// unrestricted; a scoped caller may write only inside their reach; and a
// consolidated write (no branch: the consolidated books, period close and
// reopen, the year end close, the chart of accounts) needs a dealer wide
// caller. Applied BEFORE the write lands, so a refused write leaves nothing
// behind and the answer never contradicts a commit.
func (s *Service) authorizeBranchWrite(ctx context.Context, branchID string) error {
	caller, err := s.caller(ctx)
	if err != nil {
		return err
	}
	if branchID == "" {
		if caller.Scope != identity.ScopeGlobal {
			return errForbiddenWrite
		}
		return nil
	}
	if !caller.CanAccessBranch(branchID) {
		return errForbiddenWrite
	}
	return nil
}

// clock returns the service clock (defaults to time.Now).
func (s *Service) clock() time.Time {
	if s.now == nil {
		return time.Now()
	}
	return s.now()
}

// StandardRateReader answers the dealer's standard tax rate in basis points on a date ("" is today). The
// composition root binds an adapter over the tax engine; gl reads it to back the tax out of a tax-inclusive
// NSF fee, so it never carries a rate of its own. Unwired, the Ontario 13 percent stands.
type StandardRateReader interface {
	StandardRateBps(ctx context.Context, asOf string) (int, error)
}

// WithStandardRate injects the dealer's standard rate seam.
func (s *Service) WithStandardRate(r StandardRateReader) *Service { s.standardRate = r; return s }

// WithTaxLabel injects the dealer's tax word seam (platform/taxwords): the word the tax legs' memos
// carry. Unwired is the Ontario dealer's "HST".
func (s *Service) WithTaxLabel(src taxwords.Source) *Service { s.taxWords = src; return s }

// taxWord reads the word a journal memo names the tax by. A posting never fails for a word: a failed
// read memos the neutral "Tax", true for every dealer.
func (s *Service) taxWord(ctx context.Context) string {
	w, _ := taxwords.Resolve(ctx, s.taxWords)
	return w
}

// WithInvoiceReader injects the billing invoice-facts seam.
func (s *Service) WithInvoiceReader(r InvoiceReader) *Service { s.invoices = r; return s }

// WithDeliveryReader injects the order delivery-fact seam the deposit release's date reads (round 2).
func (s *Service) WithDeliveryReader(r DeliveryReader) *Service { s.delivery = r; return s }

// WithAuditTxRecorder injects the in-transaction audit seam the Controller's open year records through (round 2).
func (s *Service) WithAuditTxRecorder(r AuditTxRecorder) *Service { s.auditTx = r; return s }

// WithStrictPreFlight makes the pre flight refuse a close when a check is unwired (round 4, S3's P2:
// production wiring calls this; tests pass nil seams and skip the checks).
func (s *Service) WithStrictPreFlight() *Service { s.strictPreFlight = true; return s }

// WithStockReader injects the stock-ledger seam the close pre flight's 1200 tie check reads (FW1-09b).
func (s *Service) WithStockReader(r StockValueReader) *Service { s.stockValue = r; return s }

// WithDrawerReader injects the till seam the close pre flight's open drawer check reads (FW1-09b).
func (s *Service) WithDrawerReader(r TillDrawerReader) *Service { s.drawers = r; return s }

// WithQueueReader injects the outbox seam the close pre flight's waiting/dead lettered check reads (FW1-09b).
// It is the pre flight's OWN seam, distinct from the posting queue's read (WithPostingQueue): the pre flight
// asks how many posted events in the closing month are still unposted, the posting queue asks what is waiting
// right now. One outbox store answers both, but each seam stays wired on its own, so an unwired check is named
// as unwired (the strict pre flight refuses) instead of passing silently behind the other seam's wiring.
func (s *Service) WithQueueReader(r OutboxQueueReader) *Service { s.preFlightQueue = r; return s }

// WithTaxSplitReader injects the pos tax-split seam.
func (s *Service) WithTaxSplitReader(r TaxSplitReader) *Service { s.taxSplits = r; return s }

// WithARSubledger injects the billing AR-balance seam (the 1100 tie-out).
func (s *Service) WithARSubledger(r ARSubledger) *Service { s.ar = r; return s }

// WithPaymentReader injects the till payment-facts seam (the 1010/1020/1000 leg choice).
func (s *Service) WithPaymentReader(r PaymentReader) *Service { s.payments = r; return s }

// WithAudit injects the A-21 Recorder for sensitive-access stamping (reopen, close).
func (s *Service) WithAudit(r audit.Recorder) *Service { s.audit = r; return s }

// WithBranchFacts binds the branch facts seam (branches owns locations; gl never reads it). It
// feeds both the head office set's candidate check (Service) and the head office default's
// read TRT by code (Repository), so one wiring point serves both.
func (s *Service) WithBranchFacts(bf BranchFacts) *Service {
	s.branchFacts = bf
	if s.repo != nil {
		s.repo.branchFacts = bf
	}
	return s
}

// WithCostBasis injects the inventory cost-basis seam (dormant at W5, NoopCostBasis).
func (s *Service) WithCostBasis(c CostBasis) *Service { s.costBasis = c; return s }

// WithDeadRequeuer injects the outbox DEAD-row replay seam (boot-time pre-W5 replay).
func (s *Service) WithDeadRequeuer(d DeadRequeuer) *Service { s.dead = d; return s }

// WithApprovalFiler injects the approvals-rail JE-park filing seam (0075). Nil = the shipped maker-checker
// park behaves exactly as today (no rail filing). Chainable.
func (s *Service) WithApprovalFiler(f ApprovalFiler) *Service { s.approvalFiler = f; return s }

// WithApprovalStateChecker injects the request-state read the approve route consults. Chainable.
func (s *Service) WithApprovalStateChecker(c ApprovalStateChecker) *Service {
	s.approvalChecker = c
	return s
}

// - provider surface (bankrec + finstmt + boot depend on these) -

// PostExternal posts a caller-built balanced journal through the GL, idempotent on in.IdempotencyKey, and
// returns the journal id. bankrec's clearing journals go through here. FROZEN signature; A-01 fills the body.
func (s *Service) PostExternal(ctx context.Context, in ExternalJournal) (journalID string, err error) {
	return s.postExternal(ctx, in)
}

// PostExternalTx is PostExternal through the caller's transaction: the same validation, the same idempotency
// key, but every read and write rides tx, so a caller that holds a connection (purchasing PostBill) does not
// ask the shared pool for a second one and the journal commits or rolls back with the caller. Additive: the
// FROZEN PostExternal signature is untouched.
func (s *Service) PostExternalTx(ctx context.Context, tx pgx.Tx, in ExternalJournal) (journalID string, err error) {
	return s.postExternalTx(ctx, tx, in)
}

// JournalKeysExist answers which of the given idempotency keys already name a journal (a keyed
// existence read; the nightly sweeps in billing ask it rather than reading gl_journals).
func (s *Service) JournalKeysExist(ctx context.Context, keys []string) ([]string, error) {
	return s.repo.JournalKeysExist(ctx, keys)
}

// GLBalanceCents returns the balance of an account for a period/branch, read from v_gl_account_balances.
// FROZEN signature; A-01 fills the body (must go real early, bankrec's variance check depends on it).
func (s *Service) GLBalanceCents(ctx context.Context, accountCode string, year, month int, branchID string) (int64, error) {
	return s.glBalanceCents(ctx, accountCode, year, month, branchID)
}

// GLClosingBalanceCents returns the cumulative closing balance of an account at the end of (year, month)
// across all fiscal years, read through AccountBalanceAsOf (FW1-04). Bank reconciliation uses this closing
// balance at month end rather than the single month's movement from GLBalanceCents.
func (s *Service) GLClosingBalanceCents(ctx context.Context, accountCode string, year, month int, branchID string) (int64, error) {
	return s.glClosingBalanceCents(ctx, accountCode, year, month, branchID)
}

// GLBalanceAsOfCents returns the cumulative balance of an account through asOf across all fiscal years,
// read through AccountBalanceAsOf (FW1-04 round 4). Bank reconciliation's cut-over starting point reads
// the ledger's own balance at the cut-over date through this seam rather than summing the uncleared entry list.
func (s *Service) GLBalanceAsOfCents(ctx context.Context, accountCode string, asOf time.Time, branchID string) (int64, error) {
	return s.glBalanceAsOfCents(ctx, accountCode, asOf, branchID)
}

// BankAccountEntriesAsOf returns the POSTED, unreversed book entries on a bank GL account through asOf (FW1-04).
func (s *Service) BankAccountEntriesAsOf(ctx context.Context, glAccountCode string, asOf time.Time) ([]BankBookEntry, error) {
	return s.store.BankAccountEntriesAsOf(ctx, glAccountCode, asOf)
}

// GLBalanceThroughCents returns an account's balance THROUGH a period (every period up to and
// including year/month, cumulative), natural orientation. Additive provider read (FW1-11): the
// inventory tie-out reads 1200's balance as of a period end, where the frozen GLBalanceCents keeps
// answering one period's movement. The same view, so a late posted journal's filed period decides
// where it lands (the FW1-01 rule, 612).
func (s *Service) GLBalanceThroughCents(ctx context.Context, accountCode string, year, month int, branchID string) (int64, error) {
	netDebit, normal, err := s.store.AccountBalanceThrough(ctx, accountCode, year, month, branchID)
	if err != nil {
		return 0, err
	}
	if normal == NormalCredit {
		return -netDebit, nil
	}
	return netDebit, nil
}

// RegisterPostingHandlers wires the outbox consumer for all eight posted topics onto the dispatcher. Each
// handler resolves posting rules (via the two-name mapping above), builds the journal, and posts it
// idempotently. FROZEN topic set. This runs once at boot (main.go), so it is also the natural home of the
// two boot-time controls: ValidatePostingRules (a dangling/inactive posting rule is a loud config error, not
// a silent zero-leg post) and the DeadRequeuer replay (pre-W5 outbox rows that dead-lettered before a handler
// existed are reset to PENDING so the now-registered consumer re-delivers them). Registering here keeps the
// dispatcher domain-free.
func (s *Service) RegisterPostingHandlers(d *notify.Dispatcher) {
	for _, topic := range PostedTopics {
		d.Register(topic, s.postEvent)
	}
	bootCtx := context.Background()
	if err := s.ValidatePostingRules(bootCtx); err != nil {
		s.log.Error("gl: posting-rule validation failed at boot, some events will not post correctly", "err", err)
	}
	if s.dead != nil {
		if n, err := s.dead.RequeueDead(bootCtx, PostedTopics); err != nil {
			s.log.Warn("gl: dead-letter replay at boot failed", "err", err)
		} else if n > 0 {
			s.log.Info("gl: requeued pre-W5 dead-letter outbox rows for posting", "rows", n)
		}
	}
}

// PostedTopics is the frozen set of outbox topics the GL posting consumer owns (the W5 eight + the two
// W7 POS-return topics + the deposit release and the deposit cancel, 0374/0375 + the damaged goods write off, 0378
// + the payment reversal, 0426 + the re-application of a reversal's held money, 0431 + the true void of a
// mis-keyed receipt, FW1-06b + the till paid in and paid out movements, FW1-13).
var PostedTopics = []string{
	"ar.invoice.finalized",
	"payment.received",
	"pos.sale.committed",
	"tax.relief.assessed",
	"payment.bounced",
	"payment.reversed",
	"payment.voided",
	"payment.reapplied",
	"payment.paid_out",
	"order.fulfilled",
	"bank.recon.completed",
	"till.session.completed",
	"till.movement.posted",
	"pos.return.committed",
	"order.returned",
	"deposit.released",
	"deposit.cancelled",
	"pos.return.written_off",
}

// postEvent is the single outbox handler for all eight posted topics. It resolves the mapped posting rules,
// builds the journal legs from the event facts (resolving invoice/payment/tax/cost facts through the sibling
// read seams), and posts the balanced journal idempotently (gl_journals.idempotency_key). Returning an error
// leaves the outbox row PENDING so the event is retried, a financial event is never published without its
// journal. Implementation lives in posting.go.
func (s *Service) postEvent(ctx context.Context, e notify.Event) error {
	return s.consume(ctx, e)
}

// - handler -

// Handler exposes the GL HTTP surface (chart of accounts, journals, trial balance, periods, policy, export).
// SCAFFOLD: routes are registered but every handler answers 501 until A-01 fills the service.
type Handler struct {
	userLink identity.UserIDLinker
	svc      *Service
	log      *slog.Logger
	// zone is the dealer's business day zone source; nil = the Ontario dealer's. An explicit tie-out
	// day is cut at the last instant of the dealer's own day.
	zone houseday.Source
	// legacy answers the dealer's legacy import source; nil or "" means the dealer has none and the
	// legacy import check route is refused.
	legacy LegacyImportSource
}

// LegacyImportSource answers the short lowercase word naming the legacy system a dealer imports from,
// or "" when it has none. The settings module owns the key; the composition root binds the reader.
type LegacyImportSource interface {
	LegacyImportSource(ctx context.Context) (string, error)
}

// WithLegacyImport binds the dealer's legacy import source. Nil leaves the legacy import check off.
func (h *Handler) WithLegacyImport(src LegacyImportSource) *Handler { h.legacy = src; return h }

// WithZone binds the dealer's business day zone source. Nil keeps the Ontario dealer's.
func (h *Handler) WithZone(src houseday.Source) *Handler { h.zone = src; return h }

// NewHandler wires the service.
func NewHandler(svc *Service, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.Default()
	}
	return &Handler{svc: svc, log: log}
}

// RegisterRoutes self-registers the GL routes. DIB-392: the ledger READS (chart of accounts, journals,
// journal detail, trial balance, periods) are RequireManager, not RequireStaff, RequireStaff admits any
// ACTIVE login, including Tier-3 front line and a roleless Tier-0 account, and the general ledger is the
// company's money in full detail (every customer's revenue, every wage line, every loan balance). The
// journal/CoA writes are RequireManager as before;
// period reopen, year-end close, and the GP-parity export are RequireAdmin (Tier-1 GLOBAL; the reopen path
// additionally enforces the Financial-Controller-only rule inside the service). The scaffold froze the guard
// SIGNATURE; the route SET is the worker's to fill, the frozen routes below are joined by the CoA-deactivate,
// journal void/reverse, recurring /run, tie-out, and year-end-close routes the A-01 tasks require. Handler
// bodies live in handler.go.
func (h *Handler) RegisterRoutes(r httpx.Registrar, g *guards.Guards) {
	// Chart of accounts
	r.Handle("GET /api/v1/gl/accounts", g.RequireManager(h.staff(h.listAccounts)))
	r.Handle("POST /api/v1/gl/accounts", g.RequireManager(h.staff(h.createAccount)))
	r.Handle("POST /api/v1/gl/accounts/{code}/deactivate", g.RequireManager(h.staff(h.deactivateAccount)))
	// Journals
	r.Handle("GET /api/v1/gl/journals", g.RequireManager(h.staff(h.listJournals)))
	r.Handle("GET /api/v1/gl/journals/{id}", g.RequireManager(h.staff(h.getJournal)))
	r.Handle("POST /api/v1/gl/journals", g.RequireManager(h.staff(h.createJournal)))
	r.Handle("POST /api/v1/gl/journals/{id}/approve", g.RequireManager(h.staff(h.approveJournal)))
	r.Handle("POST /api/v1/gl/journals/{id}/void", g.RequireManager(h.staff(h.voidJournal)))
	r.Handle("POST /api/v1/gl/journals/{id}/reverse", g.RequireManager(h.staff(h.reverseJournal)))
	// Recurring templates
	r.Handle("GET /api/v1/gl/recurring", g.RequireManager(h.staff(h.listRecurring)))
	r.Handle("POST /api/v1/gl/recurring/{id}/run", g.RequireManager(h.staff(h.runRecurring)))
	// Trial balance + subledger tie-out
	r.Handle("GET /api/v1/gl/trial-balance", g.RequireManager(h.staff(h.trialBalance)))
	r.Handle("GET /api/v1/gl/tie-out", g.RequireManager(h.staff(h.tieOut)))
	// M2-C1 static BisTrack GL reconciliation (read-only; Admin/Finance, same guard as the GP-parity export).
	r.Handle("GET /api/v1/gl/tieout/bistrack", g.RequireAdmin(h.staff(h.bistrackTieOut)))
	// Periods
	r.Handle("GET /api/v1/gl/periods", g.RequireManager(h.staff(h.listPeriods)))
	r.Handle("POST /api/v1/gl/periods/{id}/close", g.RequireManager(h.staff(h.closePeriod)))
	r.Handle("POST /api/v1/gl/periods/{id}/reopen", g.RequireAdmin(h.staff(h.reopenPeriod)))
	r.Handle("POST /api/v1/gl/periods/close-year", g.RequireAdmin(h.staff(h.closeYear)))
	r.Handle("POST /api/v1/gl/periods/open-year", g.RequireAdmin(h.staff(h.openYear)))
	// The posting queue: the Controller's list of journals waiting or dead
	// lettered, and the two counts the Money Desk's banner reads.
	r.Handle("GET /api/v1/gl/posting-queue", g.RequireAdmin(h.staff(h.listPostingQueue)))
	r.Handle("GET /api/v1/gl/posting-queue/count", g.RequireManager(h.staff(h.postingQueueCounts)))
	// Policy + export
	r.Handle("GET /api/v1/gl/policy", g.RequireManager(h.staff(h.policy)))
	r.Handle("POST /api/v1/gl/export", g.RequireAdmin(h.staff(h.export)))
}
