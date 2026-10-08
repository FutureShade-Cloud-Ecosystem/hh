package pos

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/moneyutil"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pricing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// -- injected sibling seams (all interfaces so tests fake them) --------------

// LineResolver is satisfied by *pricing.Service, the server-side price authority (US-A19-04).
// The till prices every line through the COUNTER resolve (D48 row 481, D49): the price the
// customer's rules and tiers give, accepted as it resolves. The gated resolve has no method on
// this seam, so the sale and the pricing order cannot ask the floor through it. The price
// display is the one door back: counterQuoter type-asserts the same service to CounterQuoter,
// whose preview runs the gated resolve in dry-run, read-only, it can write no BYPASS row and
// exposes no block.
type LineResolver interface {
	DeriveCounterLinePrice(ctx context.Context, in pricing.ResolveInput) (pricing.LinePrice, error)
}

// TenderTaker is satisfied by *till.Service (A-03), the SOLE writer of payments + till_movements rows.
// The Sale/Return opens the tx and passes it in so capture joins the same transaction as the order +
// invoice. TakeTender carries the payment id + the opaque gateway_ref (the single source of truth for
// both); RefundTender (W7) is the refund rail, the REFUNDED payments row and, for CASH, the PAID_OUT
// drawer movement, all written by the till inside the return tx.
type TenderTaker interface {
	TakeTender(ctx context.Context, tx pgx.Tx, in till.TenderInput) (till.TenderResult, error)
	RefundTender(ctx context.Context, tx pgx.Tx, in till.RefundInput) (till.RefundResult, error)
	// CaptureCard captures ONE card leg on the pool, before the sale transaction opens (the card
	// charge design 3.3): the till re-checks its session rules, then the rail claims the leg,
	// dispatches and stamps, all committed. pos's capture loop calls this per CARD leg, in seq
	// order; the typed answers are the till's (ErrCardDeclined and its siblings), and a
	// CardCaptureCaptured answer is the only one the transaction may bind.
	CaptureCard(ctx context.Context, in till.CardChargeInput) (till.CardCapture, error)
	// CompensateCard reverses ONE captured leg of a failed ring, on the pool, after the sale
	// transaction returned (the design's step 6). pos calls it for every leg the ring captured.
	CompensateCard(ctx context.Context, attemptID string) (till.CardCompensation, error)
	// RecordPaymentReceived is the Q-044 post-commit hook: the till writes the PAYMENT_RECEIVED business
	// record AFTER the caller's money tx commits (never mid-tx, a rollback would orphan the row and a
	// retry would duplicate it). Exactly-once: a replayed tender carries Inserted=false and records nothing.
	RecordPaymentReceived(ctx context.Context, res till.TenderResult)
	// GetSession reads the drawer session so a sale can assert the ring's branch IS the session's branch
	// (DIB-389). Declared here rather than as a new injected seam so the guard needs no main.go rewiring -
	// *till.Service already satisfies it. The till stays the owner of the session; pos only reads it.
	GetSession(ctx context.Context, id string) (till.SessionDetail, error)
}

// CardRefunder is RUN-PAY's card reversal rail (payment.Service.RefundTx behind a creds-free adapter in
// cmd/server/main.go, mirroring cardChargerAdapter). pos passes the ORIGINAL capture's gateway_ref and
// receives the reversal's own processor ref, creds never cross into pos, and pos never writes payments.
//
// The transaction discipline is part of the seam: the Return transaction is
// passed and every database touch the refunder makes rides IT (the
// FindReturnByKeyTx discipline), because the Return transaction holds a
// connection and the per-sale advisory lock; a refunder reading through the
// pool there would have the lock holder wait for a second connection every
// other connection is a Return waiter that needs the holder's lock,
// deadlocking a full pool until the caller's context ends. Every
// implementer states its discipline where it is written; a refunder that
// cannot ride the caller's transaction cannot silently reintroduce the
// stall.
type CardRefunder interface {
	RefundCard(ctx context.Context, tx pgx.Tx, gatewayRef string, amountCents int64, reference, idempotencyKey string) (refundRef string, err error)
}

// CreditGate is satisfied by an identity-backed adapter (reuses identity.CheckCredit) for ACCOUNT sales.
// Check takes (ctx, customerID) so a W5-A05 hold-aware gate can additionally deny an ACCOUNT tender while the
// customer carries an active credit hold (fail closed), the scaffold IdentityCreditGate ignores them and
// keeps the identity-only credit decision unchanged.
type CreditGate interface {
	Check(ctx context.Context, customerID string, orderTotalCents, openBalanceCents, creditLimitCents int64, termsAreCredit bool, branchID string, hasApprovedOverride bool) *identity.ActionBlocked
}

// IdentityCreditGate wraps identity.CheckCredit so the credit decision stays owned by A-20.
type IdentityCreditGate struct{}

// NewIdentityCreditGate builds the identity-backed credit gate.
func NewIdentityCreditGate() *IdentityCreditGate { return &IdentityCreditGate{} }

// Check delegates to identity.CheckCredit. ctx/customerID are part of the widened seam (the A-05 hold-aware
// gate uses them); this identity-only impl ignores them so behavior is identical to W4.
func (IdentityCreditGate) Check(_ context.Context, _ string, orderTotalCents, openBalanceCents, creditLimitCents int64, termsAreCredit bool, branchID string, hasApprovedOverride bool) *identity.ActionBlocked {
	return identity.CheckCredit(orderTotalCents, openBalanceCents, creditLimitCents, termsAreCredit, branchID, hasApprovedOverride)
}

// posStore is the isolated M2-debt store seam (see repository.go). The service depends on the interface
// so unit tests fake every DB touch (the pricing/service_test.go pattern); *Repository satisfies it.
type posStore interface {
	// tx orchestration, the Sale opens ONE tx; capture (till.TakeTender) joins it.
	RunInTx(ctx context.Context, fn func(tx pgx.Tx) error) error
	// idempotency precheck (non-tx read)
	FindSaleByKey(ctx context.Context, key string) (Sale, bool, error)
	// credit-gate + pricing reads (non-tx)
	ReadCustomer(ctx context.Context, customerID string) (customerRec, error)
	// ReadCustomerTx is ReadCustomer's open-transaction twin: the
	// ACCOUNT_CREDIT rail reads the customer inside the Return transaction,
	// on ITS transaction, never through the pool while the transaction
	// holds a connection (the FindReturnByKeyTx discipline: a pool read
	// there has the per-sale lock's holder wait on a second connection
	// every other connection is a Return waiter that needs the holder's
	// lock).
	ReadCustomerTx(ctx context.Context, tx pgx.Tx, customerID string) (customerRec, error)
	ResolveWalkIn(ctx context.Context) (string, error)
	// multi-branch stock glance (non-tx read; M2 debt)
	ReadStockOnHand(ctx context.Context, productID string) (map[string]float64, error)
	// DIB-488: unit meta + low thresholds for the counter stock-status line (non-tx read; M2 debt)
	ReadStockUnitMeta(ctx context.Context, productID string) (baseUOM, sellUOM string, sellFactor float64, lowBase map[string]float64, err error)
	// DIB-488 rev 6 (item D): salesperson reads, validation + the counter selector (non-tx; M2 debt)
	ReadStaffName(ctx context.Context, userID string) (string, bool, error)
	ListSalespeople(ctx context.Context) ([]Salesperson, error)
	// The counter discount's approver reads (discount.go): the active logins holding a role grant
	// that reaches the branch, one row per grant; userID narrows to one login ("" lists all).
	ListDiscountStaff(ctx context.Context, orgID, branchID, userID string) ([]discountStaffRow, error)
	// The manual discounts a committed sale carries, read back for a replay and a reopen.
	ReadSaleDiscounts(ctx context.Context, orderID string) ([]SaleDiscount, error)
	// DIB-488 rev 8, all display-only M2-debt cross-reads (repository_rev8.go):
	// item 9: the DISCOVERED catalogue weight (kg per base unit) riding the stock glance
	ReadProductWeightKg(ctx context.Context, productID string) (float64, bool, error)
	// item 10: the pricing engine's cost source, per-branch WAC, per product base uom
	ReadBranchCosts(ctx context.Context, productIDs []string, branchID string) (map[string]int64, error)
	// item 8 (DIB-502): the 0169 pairing map + its candidates + the ticket lines' categories
	ReadAddonPairings(ctx context.Context) (map[string][]string, error)
	ListAddonCandidates(ctx context.Context, applications, excludeProductIDs []string, limit int) ([]addonCandidate, error)
	ReadProductApplications(ctx context.Context, productIDs []string) (map[string]string, error)
	// item 4: the data-driven default for the curated quick-chip set
	TopSellingApplications(ctx context.Context, limit int) ([]string, error)

	// The quick-pick grid's two catalogue reads (quickpicks.go). ReadProductsBySKU resolves a
	// dealer's curated SKUs; TopSellingProducts is the DERIVED set a counter sees before anyone
	// curates anything, the TopSellingApplications idiom one level down.
	ReadProductsBySKU(ctx context.Context, skus []string) ([]quickPickRow, error)
	TopSellingProducts(ctx context.Context, limit int) ([]quickPickRow, error)
	// InStockApplicationRepresentatives is one in-stock product per application (category), so the
	// grid can show every category the yard holds stock in, whatever was curated.
	InStockApplicationRepresentatives(ctx context.Context) ([]quickPickRow, error)
	// FN relief, masked-only profile read (non-tx)
	ReadEligibilityProfileMasked(ctx context.Context, profileID string) (eligibilityMasked, error)
	// operator reopen (non-tx read)
	GetSaleView(ctx context.Context, id string) (SaleView, bool, error)
	// DIB-165 past-sale / till-session lookup (non-tx read; branch-fenced in the service)
	LookupSales(ctx context.Context, q SaleLookupQuery) ([]SaleSummary, error)
	// the M2-debt writes (all inside the Sale tx)
	InsertOrder(ctx context.Context, tx pgx.Tx, o orderRow) (id, no string, err error)
	InsertOrderLines(ctx context.Context, tx pgx.Tx, orderID string, lines []orderLineRow) (ids []string, err error)
	InsertInvoice(ctx context.Context, tx pgx.Tx, inv invoiceRow) (id, no string, err error)
	InsertInvoiceLines(ctx context.Context, tx pgx.Tx, invoiceID string, orderLineIDs []string, lines []orderLineRow) error
	InsertPaymentAllocation(ctx context.Context, tx pgx.Tx, paymentID, invoiceID string, amountCents int64, allocatedBy string) error
	InsertCustomerTxn(ctx context.Context, tx pgx.Tx, t customerTxnRow) error
	InsertPosSale(ctx context.Context, tx pgx.Tx, s posSaleRow) (string, error)
	// DIB-165 split tender, the legs, written in the sale tx so 0100's deferred sum guard fires at COMMIT.
	InsertSaleTenders(ctx context.Context, tx pgx.Tx, posSaleID string, legs []saleTenderRow) error
	InsertTaxSnapshot(ctx context.Context, tx pgx.Tx, snap taxSnapshotRow) (string, error)
	EmitEvent(ctx context.Context, tx pgx.Tx, topic string, payload any) error
	// signature persistence (non-tx write, standalone capture)
	InsertSignature(ctx context.Context, sig signatureRow) (string, error)
	// InsertSignatureTx is the same insert on the CALLER'S transaction (nil tx means the pool, the
	// shape of the tree's other store seams): the tx capture writes the row through the caller's
	// transaction so it commits or rolls back with it, and both capture paths share ONE statement.
	InsertSignatureTx(ctx context.Context, tx pgx.Tx, sig signatureRow) (string, error)

	// -- W7 counter-v2 (pos-owned 0043 tables; the invoice-credit write stays in the M2-debt family) --
	// POS policy (branch row overrides the org NULL row)
	ReadPosPolicy(ctx context.Context, branchID string) (PosPolicy, error)
	UpsertPosPolicy(ctx context.Context, p PosPolicy) error
	// DIB-488 rev 2 (0166): the caller's own workspace ergonomics, whole-or-absent override of
	// the pos_policy dealer defaults (see workspace.go for the persistence ruling).
	ReadUserWorkspace(ctx context.Context, userID string) (userWorkspaceRow, bool, error)
	UpsertUserWorkspace(ctx context.Context, row userWorkspaceRow) error
	DeleteUserWorkspace(ctx context.Context, userID string) error
	// returns
	FindReturnByKey(ctx context.Context, key string) (Return, bool, error)
	// FindReturnByKeyTx and ReturnOrderContextTx are the open-transaction
	// twins of the pool reads: the in-transaction same-key replay re-check
	// must read on ITS transaction, never through the pool while the
	// transaction holds a connection (a pool of Returns waiting on the same
	// sale's advisory lock would deadlock the holder waiting for a second
	// connection).
	FindReturnByKeyTx(ctx context.Context, tx pgx.Tx, key string) (Return, bool, error)
	ReturnOrderContext(ctx context.Context, orderID, orderNo string) (returnOrderCtx, bool, error)
	// ReturnOrderContextFor scopes a typed bare number to the branches the caller reaches (nil: all).
	ReturnOrderContextFor(ctx context.Context, orderID, orderNo string, reach []string) (returnOrderCtx, bool, error)
	ReturnOrderContextTx(ctx context.Context, tx pgx.Tx, orderID, orderNo string) (returnOrderCtx, bool, error)
	ListReturnableLines(ctx context.Context, orderID string) ([]ReturnableLineView, error)
	LockReturnableLine(ctx context.Context, tx pgx.Tx, orderID, orderLineID string) (returnableLine, bool, error)
	// the per-sale return sums and lock: the refund approval amount counts
	// every return against the same sale together, so the gate reads the
	// sale's earlier committed returns inside its transaction (the Tx read)
	// and the approval prospect reads them before it opens (the pool read);
	// every Return transaction first takes the per-sale advisory lock, so
	// two returns of different lines of one sale cannot each read the prior
	// sum before the other commits.
	SumCommittedReturnTotals(ctx context.Context, orderID string) (int64, error)
	SumCommittedReturnTotalsTx(ctx context.Context, tx pgx.Tx, orderID string) (int64, error)
	LockSaleForReturn(ctx context.Context, tx pgx.Tx, orderID string) error
	InsertReturn(ctx context.Context, tx pgx.Tx, r returnRow) (id, no string, err error)
	InsertReturnLines(ctx context.Context, tx pgx.Tx, returnID string, lines []returnLineRow) error
	SetReturnRefundPayment(ctx context.Context, tx pgx.Tx, returnID, paymentID string) error
	ApplyCreditToInvoice(ctx context.Context, tx pgx.Tx, invoiceID string, amountCents int64) (applied int64, err error)
	ListReturnPickups(ctx context.Context, branchID string) ([]ReturnPickup, error)
	SetPickupStatus(ctx context.Context, returnID, from, to string) error
	GetReturnPickup(ctx context.Context, returnID string) (ReturnPickup, bool, error)
	// parked tickets
	InsertParkedTicket(ctx context.Context, t ParkedTicket, actor string) (ParkedTicket, error)
	ListParkedTickets(ctx context.Context, branchID string) ([]ParkedTicket, error)
	// GetParkedTicket reads one ticket whatever its status, the parked fence's pre-read
	// (policy.go holds the branch wall on resume and discard before any write).
	GetParkedTicket(ctx context.Context, id string) (ParkedTicket, bool, error)
	MarkParkedResumed(ctx context.Context, id, actor string) (ParkedTicket, error)
	MarkParkedDiscarded(ctx context.Context, id string) error
	// A-06 portal return requests (0045 tables; the request is INTENT ONLY, no money/stock writes).
	// Decisions are serialized on the request row: LockReturnRequestStatus takes FOR UPDATE inside the
	// decision tx and DecideReturnRequestTx stamps the outcome in that SAME tx.
	InsertReturnRequest(ctx context.Context, rr returnRequestRow, lines []ReturnRequestLineInput) (ReturnRequest, error)
	GetReturnRequest(ctx context.Context, id string) (ReturnRequest, bool, error)
	ListReturnRequestsByCustomer(ctx context.Context, customerID string, limit int) ([]ReturnRequest, error)
	ListReturnRequestsByStatus(ctx context.Context, status string, limit int) ([]ReturnRequest, error)
	LockReturnRequestStatus(ctx context.Context, tx pgx.Tx, id string) (status string, found bool, err error)
	DecideReturnRequestTx(ctx context.Context, tx pgx.Tx, d requestDecision) error
	// pricing orders
	InsertPricingOrder(ctx context.Context, po PricingOrder, actor string) (PricingOrder, error)
	GetPricingOrder(ctx context.Context, id string) (PricingOrder, bool, error)
	ListPricingOrders(ctx context.Context, branchID string, limit int) ([]PricingOrder, error)
	// product snapshot for the pricing-doc lines (read-only sibling read, M2-debt family)
	ReadProductSnapshot(ctx context.Context, productID string) (sku, name string, err error)

	// -- item 9.4, Pay balance at the counter (pay_balance.go) --
	// FindOwingOrders answers the orders that owe money and match the term
	// (order number, customer name, phone), fenced to the branch reach (nil
	// ids reach every branch). Read-only.
	FindOwingOrders(ctx context.Context, term string, branchIDs []string, limit int) ([]OwingOrder, error)
	// ReadOwingOrder is the advisory pool read of one order this door might
	// pay: its facts and its whole balance. found false is the 404.
	ReadOwingOrder(ctx context.Context, orderID string) (payBalanceOrderFacts, bool, error)
	// FindPayBalancePaymentByKey answers the payments row a take's
	// idempotency key already wrote (the replay fast path).
	FindPayBalancePaymentByKey(ctx context.Context, key string) (payBalanceReplayRow, bool, error)
	// LockPayBalanceOrderTx is the one-order writer's locked read, in the
	// money lock order: the order row, the payments sequence row (not
	// advancing), then the open invoice rows FOR NO KEY UPDATE ascending.
	LockPayBalanceOrderTx(ctx context.Context, tx pgx.Tx, orderID string) (payBalanceOrderFacts, error)
	// SettleInvoiceTx applies one payment slice to one invoice: the
	// allocation row, the balance move with its feed stamp and status flip,
	// and the subledger PAYMENT row.
	SettleInvoiceTx(ctx context.Context, tx pgx.Tx, s settleInvoice) error
	// OrderPaymentsTrailTx / OrderPaymentsTrail read the money trail (each
	// payment so far with its method and amount) on the caller's transaction
	// and on the pool.
	OrderPaymentsTrailTx(ctx context.Context, tx pgx.Tx, orderID string) ([]OwingPayment, error)
	OrderPaymentsTrail(ctx context.Context, orderID string) ([]OwingPayment, error)
	// PayBalanceHoldTx is the hold's own reading on the caller's transaction
	// (open balances plus the uninvoiced remainder, held by the zero day terms
	// rule), the figures the take's answer carries.
	PayBalanceHoldTx(ctx context.Context, tx pgx.Tx, orderID string) (payBalanceHoldView, error)
}

// Service composes pricing + till + identity into one atomic Sale (and, at W7, the Return).
type Service struct {
	store   posStore
	pricing LineResolver
	// counterPrices is the READ-ONLY counter price picture (counterprice.go), retail beside the
	// account's price and the saving between them. Separate seam from `pricing` because it must
	// never write an audit row; nil until wired, and nil answers an empty screen, not an error.
	counterPrices CounterQuoter
	// curation is the settings kit's READ for the pos.counter surface, which quick-pick tiles a
	// dealer (or this person) put on the grid. Nil until wired; nil resolves the DERIVED grid, so
	// an un-curated counter is a working counter (quickpicks.go).
	curation SurfaceCuration
	// aging is the AR read behind the Customer step's past-due fact (standing.go). Nil until wired,
	// and nil answers the read unavailable, never "this account is current".
	aging AgingReader
	// holds and accounts are the standing read's other two facts (standing.go): the active credit
	// hold, and that the account exists at all. Nil answers the read unavailable, never empty.
	holds    HoldReader
	accounts AccountChecker
	tender   TenderTaker
	credit   CreditGate
	sig      SignatureProvider
	tax      TaxAssessor
	// payInvoicer is the billing writer the Pay balance take raises an
	// uninvoiced order's invoice through (pay_balance.go), the desk's own
	// writer. Nil until wired; a take that needs a raise refuses (503) rather
	// than stranding the remainder.
	payInvoicer PayBalanceInvoicer
	// taxRules is the optional round-rule read behind the counter tax-rate
	// read's round_mode word (taxrate.go). Nil answers INVOICE_TOTAL, the
	// flat engine's actual behavior.
	taxRules   TaxRuleSource
	stock      StockDecrementer
	restock    StockRestocker
	cardRefund CardRefunder
	audit      audit.Recorder
	// trail is the audit READ seam behind the counter settings change history (policy_history.go).
	// Nil is an empty history, never an error.
	trail PolicyTrailReader
	// roles is the dealer's role configuration the till's rules read (D49):
	// the glance's cost and margin list (X3), the refund amount and its
	// approvers (X7), the approval window. The startup path loads it
	// (roleconfig.Default or Load) and stops on the error, and the gated
	// paths refuse when it is absent (the nil gate fails closed, the lead's
	// ruling): a service without a configuration refuses the glance and the
	// gated returns, it never answers them permissively.
	roles *roleconfig.Config
	// approver resolves the manager's credential carried beside the clerk's
	// bearer on a return above the amount (row 482). Nil until wired; the
	// approval path refuses operationally when a credential arrives while
	// the resolver is absent, never commits on one.
	approver ApproverSource
	// userLink resolves the ERP users.id a platform user id links to, the
	// B4 linkage the composition root supplies. The committed decision uses
	// it to name the approver in one identifier space with the clerk
	// (staff users ids) on the record and the audit row (the lead's
	// P2-4/P3-3 rulings); nil leaves the platform subject as the only name.
	userLink identity.UserIDLinker
	// actorNames turns an audit actor id into the person's name for that same history. Nil degrades
	// every row to its role word, which is what the history said before names existed.
	actorNames ActorNameResolver
	now        func() time.Time
	callerFrom func(context.Context) (identity.StaffContext, bool)
}

// NewService wires the store. Sibling seams are injected via the With* methods. tax + stock default to the
// behavior-preserving FlatHSTAssessor{} / NoopStock{} (see sale_hooks.go) so a fresh service is bit-identical
// to W4/W5 until main.go rewires the real M5 tax + INV-CORE stock services.
func NewService(store *Repository) *Service {
	return &Service{store: store, credit: IdentityCreditGate{}, tax: FlatHSTAssessor{}, stock: NoopStock{}, now: time.Now,
		callerFrom: identity.StaffContextFromContext}
}

// caller resolves the guard-stamped staff caller (A-03's shape, till/service.go:115). The nil fallback
// keeps a directly-constructed Service (the unit-test harness builds the struct literally) working
// against the real context helper instead of panicking.
func (s *Service) caller(ctx context.Context) (identity.StaffContext, error) {
	from := s.callerFrom
	if from == nil {
		from = identity.StaffContextFromContext
	}
	c, ok := from(ctx)
	if !ok || c.UserID == "" {
		return identity.StaffContext{}, clientErr(http.StatusUnauthorized, "pos: unauthenticated")
	}
	return c, nil
}

// WithTaxAssessor injects the M5 tax engine (defaults to FlatHSTAssessor{}, flat 13%).
func (s *Service) WithTaxAssessor(t TaxAssessor) *Service { s.tax = t; return s }

// WithStock injects the INV-CORE stock decrement seam (defaults to NoopStock{}, zero moves).
func (s *Service) WithStock(st StockDecrementer) *Service { s.stock = st; return s }

// WithRestocker injects the INV-CORE return restock seam (defaults to NoopRestock{}, zero moves).
func (s *Service) WithRestocker(r StockRestocker) *Service { s.restock = r; return s }

// WithCardRefunder injects RUN-PAY's card reversal rail (the only way a return reverses a CARD capture).
func (s *Service) WithCardRefunder(r CardRefunder) *Service { s.cardRefund = r; return s }

// taxAssessor returns the injected tax engine, defaulting to the behavior-preserving flat-13% assessor (so a
// service built as a bare &Service{}, the unit-test pattern, still assesses tax identically to W4/W5).
func (s *Service) taxAssessor() TaxAssessor {
	if s.tax != nil {
		return s.tax
	}
	return FlatHSTAssessor{}
}

// stockDec returns the injected stock decrementer, defaulting to the behavior-preserving no-op.
func (s *Service) stockDec() StockDecrementer {
	if s.stock != nil {
		return s.stock
	}
	return NoopStock{}
}

// WithLineResolver injects the A-07 pricing authority.
func (s *Service) WithLineResolver(r LineResolver) *Service { s.pricing = r; return s }

// WithTenderTaker injects the A-03 till.
func (s *Service) WithTenderTaker(t TenderTaker) *Service { s.tender = t; return s }

// WithCreditGate injects the identity-backed credit gate.
func (s *Service) WithCreditGate(g CreditGate) *Service { s.credit = g; return s }

// WithSignatureProvider injects the A-19-owned signature seam.
func (s *Service) WithSignatureProvider(p SignatureProvider) *Service { s.sig = p; return s }

// WithAudit injects the A-21 Recorder.
func (s *Service) WithAudit(r audit.Recorder) *Service { s.audit = r; return s }

// WithRoleConfig injects the dealer's role configuration the till's rules
// read (D49): the glance's cost and margin list (X3), the refund amount
// without approval, the approvers above it and the approval window (X7).
// The configuration a caller hands over must be a loaded one
// (roleconfig.Default, Load or Validate): a refused configuration yields
// no Config to guard with.
func (s *Service) WithRoleConfig(c *roleconfig.Config) *Service { s.roles = c; return s }

// RoleConfig returns the role configuration the service answers its rules
// from, so a composition test can prove which one the wiring handed it.
func (s *Service) RoleConfig() *roleconfig.Config { return s.roles }

// WithUserLinker injects the ERP users.id linker the committed decision
// uses to name the approver in the same identifier space as the clerk.
func (s *Service) WithUserLinker(l identity.UserIDLinker) *Service { s.userLink = l; return s }

// errRoleConfigUnwired is the operational refusal of a gated read or rule
// on a service built with no role configuration: the nil gate fails
// closed (the lead's P1-1 ruling), so a miswire refuses loudly instead of
// answering permissively.
func errRoleConfigUnwired() error {
	return clientErr(http.StatusServiceUnavailable, "pos: the dealer's role configuration is not wired")
}

// WithClock injects a deterministic clock (tests).
func (s *Service) WithClock(now func() time.Time) *Service { s.now = now; return s }

// Sale commits one atomic counter sale (price → CheckCredit → order → tender → invoice → allocate, one
// tx; capture-before-fulfil). A declined card rolls the WHOLE tx back; a below-floor line or an
// over-limit account NEVER opens the tx. The client idempotency key makes it replay-safe (US-A19-05).
func (s *Service) Sale(ctx context.Context, in SaleInput) (Sale, error) {
	// 1. Validate + idempotency precheck (offline-safety is not optional).
	if in.IdempotencyKey == "" {
		return Sale{}, errMissingKey
	}
	if len(in.Lines) == 0 {
		return Sale{}, errNoLines
	}
	// DIB-165 split tender: 2..6 legs settling ONE sale. The legs are validated for shape here (the
	// sum check needs the priced total and runs at step 4b); a split derives its pos_sale.tender from
	// the largest leg so every existing reader of that column still sees a real tender.
	splitLegs, err := normalizeSplitLegs(in)
	if err != nil {
		return Sale{}, err
	}
	isSplit := len(splitLegs) > 0
	tender := in.Tender
	if isSplit {
		tender = primaryLeg(splitLegs)
	}
	switch tender {
	case TenderAccount, TenderCash, TenderCard, TenderCheque:
	default:
		return Sale{}, errBadTender
	}
	// DIB-389, branch scope, asserted for EVERY tender before ANY read or write.
	//
	// in.BranchID is stamped onto the order, invoice, stock decrement, customer_transaction, GL event
	// and audit record. The only branch check used to be incidental: till.TakeTender validates the
	// SESSION's branch, and it is reached solely when captureCents > 0. ACCOUNT and COD sales are
	// unpaid at ring (captureCents == 0), so they never touched it, a branch-scoped rep could ring an
	// ACCOUNT sale against any branch and move that branch's stock, AR and books.
	//
	// 403 (not 404) is the in-company out-of-scope convention (order/repository.go:280-281); a branch id
	// is not number-guessable, so there is no enumeration oracle to protect here.
	caller, err := s.caller(ctx)
	if err != nil {
		return Sale{}, err
	}
	if !caller.CanAccessBranch(in.BranchID) {
		return Sale{}, clientErr(http.StatusForbidden, "pos: caller cannot access branch %s", in.BranchID)
	}
	// -- DIB-488 rev 6 (item D): the optional More-options facts, validated in words BEFORE any
	// write (the 0168 CHECK and FK must never be the thing that answers the counter). The
	// salesperson must be an ACTIVE staff member, the read also yields the display name the sold
	// panel shows.
	poJobRef := strings.TrimSpace(in.POJobRef)
	if len([]rune(poJobRef)) > 64 {
		return Sale{}, errPORefTooLong
	}
	// DIB-488 rev 8 (item 11, 0170): the JOB fields, same posture as poJobRef (422 in words
	// before the CHECK could 500; trimmed; optional).
	jobAddress := strings.TrimSpace(in.JobAddress)
	if len([]rune(jobAddress)) > 200 {
		return Sale{}, errJobAddressTooLong
	}
	jobReference := strings.TrimSpace(in.JobReference)
	if len([]rune(jobReference)) > 64 {
		return Sale{}, errJobReferenceTooLong
	}
	salespersonName := ""
	if in.SalespersonID != "" {
		if _, perr := uuid.Parse(in.SalespersonID); perr != nil {
			return Sale{}, errSalespersonNotFound
		}
		name, ok, err := s.store.ReadStaffName(ctx, in.SalespersonID)
		if err != nil {
			return Sale{}, err
		}
		if !ok {
			return Sale{}, errSalespersonNotFound
		}
		salespersonName = name
	}
	// The session-equality half of the guard needs a DB read, so it runs later, see step 5b, just
	// before the tx opens. Keeping it there preserves DIB-197's precedence: an unpriceable line must
	// still surface as the pricing 4xx the cashier can act on, not as a till lookup failure.
	saleType := in.SaleType
	if saleType == "" {
		saleType = SaleCollectNow
	}
	// COD ("cash on DEMAND", a pay-as-you-go posture, NOT cash-on-delivery) is UNPAID at ring exactly
	// like ACCOUNT: the tender field records the settlement method the customer will later pay with.
	codSale := saleType == SaleCOD
	// unpaidAtRing means the WHOLE sale is unpaid at the counter. A split is never wholly unpaid (it
	// carries at most one ACCOUNT leg among two or more), so a split whose PRIMARY leg happens to be
	// ACCOUNT must not take this path, its account remainder is handled through the capture list
	// below, the same way a deposit's remainder is.
	unpaidAtRing := (tender == TenderAccount && !isSplit) || codSale
	if codSale {
		switch in.Fulfilment {
		case "", "PICKUP", "WILL_CALL", "DELIVERY":
		default:
			return Sale{}, errBadFulfilment
		}
		// Policy gate: pos_policy.cod_enabled (branch row overrides the org default). Refused BEFORE
		// any tx, the cashier sees the server message.
		policy, err := s.store.ReadPosPolicy(ctx, in.BranchID)
		if err != nil {
			return Sale{}, err
		}
		if !policy.CODEnabled {
			return Sale{}, errCODDisabled
		}
	}
	// Deposits ride only on an unpaid-at-ring sale; the money bound (0 < deposit < total) is checked
	// once the engine has priced the lines.
	if in.DepositCents != 0 {
		if !unpaidAtRing {
			return Sale{}, errDepositOnPaid
		}
		switch in.DepositMethod {
		case TenderCash, TenderCard, TenderCheque:
		default:
			return Sale{}, errBadDepositMethod
		}
	}
	// -- DIB-488 rev 6 (item F): the optional cash-tendered amount is only meaningful on a plain
	// CASH-tendered, fully-paid ring (a split composes its own legs; an unpaid posture takes no
	// cash now). The must-cover-the-total half waits for the engine-priced total below.
	if in.CashTenderedCents != 0 {
		if in.CashTenderedCents < 0 {
			return Sale{}, errCashTenderedShort
		}
		if isSplit || tender != TenderCash || unpaidAtRing {
			return Sale{}, errCashTenderedNotCash
		}
	}
	if cached, found, err := s.findSaleByKey(ctx, in.IdempotencyKey); err != nil {
		return Sale{}, err
	} else if found {
		cached.Replayed = true
		return cached, nil
	}
	// CARD offline is BLOCKED at the counter by default (D-A19-3); cash/cheque stay first-class offline.
	// A charge happens at ring only for a CARD tender on a paid ring or a CARD deposit, a COD sale
	// whose SETTLEMENT method is card charges nothing now, so it stays offline-capable. A split blocks
	// when ANY leg is CARD, whichever leg is primary.
	chargesCardAtRing := (tender == TenderCard && !unpaidAtRing && !isSplit) ||
		(in.DepositCents > 0 && in.DepositMethod == TenderCard) ||
		(isSplit && hasCardLeg(captureLegsForSplit(splitLegs, in.IdempotencyKey)))
	if chargesCardAtRing && in.Offline {
		return Sale{}, errCardOffline
	}

	// 2. Resolve the customer (the house WALK-IN when none is supplied) + read its credit facts.
	customerID := in.CustomerID
	if customerID == "" {
		walkIn, err := s.store.ResolveWalkIn(ctx)
		if err != nil {
			return Sale{}, err
		}
		customerID = walkIn
	}
	cust, err := s.store.ReadCustomer(ctx, customerID)
	if err != nil {
		return Sale{}, err
	}

	// 3 and 4. Price every line and assess the totals through the shared
	// resolver (priceAndAssess, sale_preview.go): the SAME pricing and tax
	// code path the read-only sale preview answers a ticket with, extracted
	// here verbatim, never a copy. A line that cannot be priced at all (no
	// retail anchor, no uom conversion) is still a client-correctable 4xx,
	// never a plausible zero.
	taxReason := TaxStdON13
	if in.Relief != nil && in.Relief.ReasonCode != "" {
		taxReason = in.Relief.ReasonCode
	}
	priced, err := s.priceAndAssess(ctx, saleMoneyInput{
		customerID:   customerID,
		customerType: cust.CustomerType,
		branchID:     in.BranchID,
		lines:        in.Lines,
		taxReason:    taxReason,
		discount:     in.Discount,
	})
	if err != nil {
		return Sale{}, err
	}
	lines := priced.lines
	subtotal := priced.subtotal
	tax := priced.tax
	total := priced.total

	// Per-line tax_cents, the largest-remainder split of the header tax over the line totals (writes to the
	// 0042 order_lines.tax_cents / invoice_lines.tax_cents columns). The parts sum EXACTLY to the header tax,
	// so the committed totals are unchanged (behavior-freeze); this only adds the per-line breakdown.
	weights := make([]int64, len(lines))
	for i := range lines {
		weights[i] = lines[i].LineTotalCents
	}
	perLineTax := moneyutil.AllocateLargestRemainder(tax, weights)
	for i := range lines {
		lines[i].TaxCents = perLineTax[i]
	}

	// Deposit bound: 0 < deposit < total (a deposit equal to the total is just
	// a paid sale, ring it paid; a negative one is no deposit at all). Judged
	// here, once the engine has priced the lines, so the refusal's body can
	// name the real total (the delta review's run D finding: the problem body
	// style the other deposit refusals use, one spelling of the bound).
	deposit := in.DepositCents
	// supplierDeposit is the supplier's demanded deposit (sub item 9.5),
	// validated against the sale's shape above.
	supplierDeposit := in.SupplierDepositCents
	if deposit != 0 && (deposit < 0 || deposit >= total) {
		return Sale{}, ErrDepositOutOfRange{DepositCents: deposit, TotalCents: total}
	}

	// Item 9.2: a deposit sale cannot be collect now. The goods do not leave on
	// a part payment, so the ring refuses in words rather than moving the sale
	// to a held shape the customer did not ask for (the choice and its reason
	// live on ErrDepositNotCollectNow, sale_deposit.go).
	if deposit > 0 && saleType == SaleCollectNow {
		return Sale{}, ErrDepositNotCollectNow{TotalCents: total, DepositCents: deposit}
	}

	// Item 9 review P3-7 (the principal's ruling): the pay before release
	// sale type is the guard of a Cash On Demand ACCOUNT (zero day terms, the
	// walk in). An account that pays on terms is not in it, whatever the
	// tender; its sale books on account or paid in full, and the hold stays
	// keyed on the account's own terms. It runs before the on account shape
	// refusal: the sale type not fitting the account is the more fundamental
	// refusal, and its words name the two honest shapes for a terms account.
	if codSale && cust.PaymentTermsDays > 0 {
		return Sale{}, ErrCODNotForTermsAccount{}
	}

	// Sub item 9.5: a supplier's demanded deposit on a special order rides
	// only a credit account's plain on-account ring (it is BILLED to the
	// account, so the account must be one that books on account, with no
	// counter deposit and no split beside it), and never more than the sale's
	// pre-tax amount (the rest's invoice keeps an honest subtotal).
	if in.SupplierDepositCents != 0 {
		if !supplierDepositAllowedOn(tender, isSplit, deposit, cust.PaymentTermsDays, cust.CreditLimitCents) {
			return Sale{}, ErrSupplierDepositNotCredit{
				TermsDays: cust.PaymentTermsDays, CreditLimitCents: cust.CreditLimitCents,
			}
		}
		if in.SupplierDepositCents < 0 || in.SupplierDepositCents > subtotal {
			return Sale{}, ErrSupplierDepositOverPretax{DepositCents: in.SupplierDepositCents, SubtotalCents: subtotal}
		}
	}

	// Item 9.2: the shape refusal. Whatever this sale would book ON ACCOUNT (a
	// single ACCOUNT tender, a split's account leg) needs an account the shape
	// was offered to: terms above zero AND a credit line. A pay before release
	// sale (COD) never books on account at all, its line two is due later by
	// definition. This runs BEFORE the credit gate: the gate answers how much
	// credit fits, this answers whether on account is a shape the account has.
	if onAccount := onAccountCents(tender, splitLegs, isSplit, total, deposit); onAccount > 0 {
		if codSale || cust.PaymentTermsDays <= 0 || cust.CreditLimitCents <= 0 {
			return Sale{}, ErrAccountShapeNotAvailable{
				AmountCents:      onAccount,
				CreditLimitCents: cust.CreditLimitCents,
				ZeroDayTerms:     codSale || cust.PaymentTermsDays <= 0,
			}
		}
	}

	// DIB-488 rev 6 (item F): tendered cash must COVER the total, change due is tendered − total,
	// and a short tender is refused in words (the drawer cannot owe the customer a negative).
	if in.CashTenderedCents > 0 && in.CashTenderedCents < total {
		return Sale{}, errCashTenderedShort
	}

	// 4b. DIB-165, the split-tender sum guard, checked the moment the total exists and BEFORE any
	//     write. The deferred constraint trigger in migration 0100 re-derives the same assertion at
	//     COMMIT; this arm is what lets the cashier see the two numbers instead of a 500.
	if isSplit {
		if err := checkTenderSum(splitLegs, total); err != nil {
			return Sale{}, err
		}
	}

	// 4c. The capture plan: every leg the ring takes money for, in one list, so the invoice, the
	//     allocations, the AR subledger and the payment.received events downstream have exactly ONE
	//     shape for the single-tender, deposit and split cases alike.
	//       · fully-paid single tender → one leg for the whole total
	//       · unpaid-at-ring + deposit → one leg for the deposit
	//       · unpaid-at-ring, no deposit → no legs
	//       · split → one leg per non-ACCOUNT leg (the ACCOUNT leg captures nothing)
	paid := !unpaidAtRing
	var captures []captureLeg
	switch {
	case isSplit:
		captures = captureLegsForSplit(splitLegs, in.IdempotencyKey)
	case paid:
		captures = []captureLeg{{Seq: 1, Method: tender, AmountCents: total, IdemKey: in.IdempotencyKey,
			PaymentToken: in.PaymentToken, CardType: in.CardType}}
	case deposit > 0:
		captures = []captureLeg{{Seq: 1, Method: in.DepositMethod, AmountCents: deposit, IdemKey: in.IdempotencyKey,
			PaymentToken: in.PaymentToken, CardType: in.CardType}}
	}
	capturedCents := sumCaptured(captures)
	// fullyPaid replaces the old `paid` from here down: a split with an ACCOUNT leg is neither
	// "paid" nor "unpaid at ring", it is settled up to capturedCents. Every downstream branch keys
	// off the money, not the posture.
	fullyPaid := capturedCents == total

	// 5. Credit gate on the UNCAPTURED remainder, the exposure the business is actually carrying.
	//    ACCOUNT and COD sales expose the whole total (less any deposit); a split exposes its ACCOUNT
	//    leg; a fully-captured sale exposes nothing and is not gated (unchanged). No tx is opened if
	//    it blocks.
	if exposure := total - capturedCents; exposure > 0 {
		if blk := s.credit.Check(ctx, customerID, exposure, cust.OpenBalanceCents, cust.CreditLimitCents, true, in.BranchID, false); blk != nil {
			return Sale{}, ErrCreditBlocked{Policy: string(blk.Policy)}
		}
	}

	// 5b. DIB-389 second gap: the caller reaching in.BranchID is not the same claim as the DRAWER
	//     belonging to it. till.TakeTender only ever validated the SESSION's branch, and only on a
	//     captured sale, so a sale could be stamped for one branch while its money ran through
	//     another's drawer. till/service.go:290 makes exactly this equality check for refunds; a sale
	//     is held to the same standard and to the same 409. Still before any write.
	if in.TillSessionID != "" && s.tender != nil {
		sess, serr := s.tender.GetSession(ctx, in.TillSessionID)
		if serr != nil {
			// The till's error taxonomy is its own package's unexported type, so it cannot be read for
			// a status here. Map it to a 409 rather than letting it fall through pos.writeErr's default
			// and reach the cashier as a bare 500 (the DIB-197 lesson).
			return Sale{}, clientErr(http.StatusConflict, "pos: till session %s unavailable: %s", in.TillSessionID, serr.Error())
		}
		if sess.BranchID != in.BranchID {
			return Sale{}, clientErr(http.StatusConflict, "pos: till session %s belongs to another branch", in.TillSessionID)
		}
	}

	actor := identity.StaffUserIDFromContext(ctx)

	// 5c. The card capture loop (the card charge design 3.3): every CARD leg of the ring is
	//     captured OUTSIDE the sale transaction, in seq order, before it opens. The claim, the
	//     dispatch and the stamp are all committed on the pool by the till, so once a card is
	//     captured the transaction below can only fail on a fault, never on a client-correctable
	//     refusal (every one of those already ran above, and the ErrNoOpenTill check moved here
	//     ahead of the first capture). The transaction's CARD branch only BINDS what this loop
	//     captured; any failure after a capture is compensated and answered in words.
	//
	//     A leg that ends anything but CAPTURED stops the DISPATCHING: the remaining legs are
	//     read read-only (their rows, when a failed ring left any, answer from themselves; a leg
	//     with no row is never charged for a dead ring), the strongest answer wins, the legs this
	//     request captured are compensated, and the ring answers 402 for a decline, 409
	//     card_charge_needs_manager for an unknown, 503 sale_failed_card_reversed when every
	//     captured leg reached VOIDED.
	if len(captures) > 0 && in.TillSessionID == "" {
		return Sale{}, ErrNoOpenTill{}
	}
	ring, err := s.captureCardLegs(ctx, in, captures, customerID, actor)
	if err != nil {
		return Sale{}, err
	}
	if ring.replay != nil {
		return *ring.replay, nil
	}
	if ring.failed() {
		return s.settleFailedCardRing(ctx, ring)
	}

	var out Sale
	// tenderResults holds one entry per CAPTURED leg, out of the tx so the Q-044 PAYMENT_RECEIVED
	// records can be written POST-COMMIT (a rollback must leave no receipt record; a replay carries
	// Inserted=false). A single-tender sale puts exactly one entry here, as before.
	var tenderResults []till.TenderResult

	// 6-12. One atomic tx: order → tender → invoice → allocate → POS facets → events → commit,
	//       with the bounded rerun on a serialization fault (the design's step 5): the captures
	//       are unbound, so a rerun binds them, with no void and no second charge.
	runSaleTx := func() error {
		return s.store.RunInTx(ctx, func(tx pgx.Tx) error {
			meta := map[string]any{
				"sale_type":       saleType,
				"cash_sale":       in.CashSale,
				"tax_reason_code": taxReason,
				"notes":           in.Notes,
			}
			if codSale {
				// Cash on DEMAND: printable/emailable immediately (the FINALIZED invoice below carries the
				// number customers pay against) but LOCKED from fulfilment until paid, no stock moves at
				// ring; the later payment/release flow (stubbed at W7) issues the stock.
				meta["cod"] = true
				meta["cod_posture"] = "AWAITING_PAYMENT"
			}
			if in.CashTenderedCents > 0 {
				// DIB-488 rev 6 (item F): the tendered amount is a display fact of THIS ring (change due
				// derives from it), parked in the order metadata so a replay answers verbatim; the money
				// itself is unchanged (the till captures exactly the total, as always).
				meta["cash_tendered_cents"] = in.CashTenderedCents
			}
			if deposit > 0 {
				// Item 9.2: the taken deposit as the order's own display fact (the
				// receipt's line one, a replay's echo). orders.deposit_required_cents
				// is deliberately NOT written: it names the deposit an order
				// REQUIRES, and this deposit is taken, its remainder lives in the
				// invoice balance, which every money reader already reads.
				meta["deposit_cents"] = deposit
				meta["deposit_method"] = in.DepositMethod
			}
			if supplierDeposit > 0 {
				// Sub item 9.5: the supplier's demanded deposit, billed to the
				// account as its own invoice, the order's own display fact.
				meta["supplier_deposit_cents"] = supplierDeposit
			}
			orderID, orderNo, err := s.store.InsertOrder(ctx, tx, orderRow{
				CustomerID:    customerID,
				BranchID:      in.BranchID,
				Status:        "CONFIRMED",
				Fulfillment:   fulfillmentFor(saleType, in.Fulfilment),
				RequestedDate: in.RequestedDate,
				SubtotalCents: subtotal,
				TaxCents:      tax,
				TotalCents:    total,
				CreatedBy:     actor,
				Metadata:      meta,
				POJobRef:      poJobRef,         // DIB-488 rev 6 (0168)
				SalespersonID: in.SalespersonID, // DIB-488 rev 6 (0168)
				JobAddress:    jobAddress,       // DIB-488 rev 8 (0170)
				JobReference:  jobReference,     // DIB-488 rev 8 (0170)
			})
			if err != nil {
				return err
			}
			lineIDs, err := s.store.InsertOrderLines(ctx, tx, orderID, lines)
			if err != nil {
				return err
			}

			// Stock decrement + COGS trigger, COLLECT_NOW only (DX-01 PARTIAL: COLLECT_LATER/DELIVERED record
			// zero moves and surface on pending-fulfilment; COD is LOCKED from fulfilment until paid, so it
			// never matches here, stock issues on the later payment/release, not at ring). The stock decrement
			// runs in THIS tx (default NoopStock → no moves at scaffold); the order.fulfilled event (frozen
			// payload {order_id, branch_id, lines: [{product_id, qty_base}]}) posts COGS through the GL
			// consumer, dormant at scaffold because the swapped inventory CostBasis returns found=false, so no
			// COGS leg posts and the event is acked quietly.
			if saleType == SaleCollectNow {
				stockLines := make([]StockLine, len(lines))
				fulfilledLines := make([]map[string]any, len(lines))
				for i, l := range lines {
					stockLines[i] = StockLine{ProductID: l.ProductID, QtyBase: l.QtyBase}
					fulfilledLines[i] = map[string]any{"product_id": l.ProductID, "qty_base": l.QtyBase}
				}
				if err := s.stockDec().DecrementForSaleTx(ctx, tx, StockDecrementInput{
					OrderID: orderID, BranchID: in.BranchID, SaleType: saleType, Lines: stockLines,
				}); err != nil {
					return err
				}
				if err := s.store.EmitEvent(ctx, tx, "order.fulfilled", map[string]any{
					"order_id": orderID, "branch_id": in.BranchID, "lines": fulfilledLines,
				}); err != nil {
					return err
				}
			}

			// Capture-before-fulfil: take the tender BEFORE the invoice/allocation so the money row lands
			// before the stock is issued. A-03 writes the SINGLE payments row in THIS tx. A CARD tender no
			// longer charges here: the capture loop above took it on the pool before this transaction
			// opened, and the till BINDS the attempt to the payments row this loop writes (the card charge
			// design 3.3), so a rolled-back transaction leaves the capture for the compensator instead of
			// orphaning a charge nobody recorded. An unpaid-at-ring sale with a DEPOSIT goes through the
			// same two halves for its deposit leg.
			var paymentID, gatewayRef string
			// Every capture leg runs through the SAME till seam, inside THIS tx. A split therefore writes N
			// ordinary payments rows and N ordinary drawer movements, the till, the GL and the drawer close
			// see nothing exotic, only several tenders that happen to belong to one order. Each leg carries
			// its OWN derived idempotency key (payments.idempotency_key is UNIQUE, so reusing the sale's key
			// would silently collapse legs 2..n onto leg 1's row and leave the drawer short).
			tenderResults = tenderResults[:0]
			for i := range captures {
				c := &captures[i]
				res, err := s.tender.TakeTender(ctx, tx, till.TenderInput{
					TillSessionID:  in.TillSessionID,
					Method:         c.Method,
					AmountCents:    c.AmountCents,
					CustomerID:     customerID,
					PaymentToken:   c.PaymentToken,
					CardType:       c.CardType,
					SignatureRef:   in.SignatureRef,
					IdempotencyKey: c.IdemKey,
					Offline:        in.Offline,
					Capture:        till.CardCapture{AttemptID: c.AttemptID, ProcessorRef: c.GatewayRef},
				})
				if err != nil {
					// A CARD failure here is a bind failure, not a decline: the capture loop already took
					// the money, this transaction fails whole, and the compensation below decides the
					// answer (the decline answers were all given before the transaction opened). Every
					// other till failure (session not open, branch access, conflict) surfaces with its own
					// message for the non-card tenders, wrapped into pos's client-error type so the handler
					// maps it (409, not 500).
					if c.Method == TenderCard {
						return err
					}
					return clientErr(http.StatusConflict, "%s", err.Error())
				}
				// The till is the SOLE payments-row writer; its result is the single source of truth for both
				// the response field and the payment.received event (CARD carries the gateway_ref; cash/cheque
				// have none → empty, which is correct for them).
				c.PaymentID = res.PaymentID
				c.GatewayRef = res.GatewayRef
				tenderResults = append(tenderResults, res)
			}
			// pos_sale's scalar payment_id/gateway_ref keep pointing at the PRIMARY leg (the one whose
			// method landed in pos_sale.tender), so single-tender readers are unchanged and split readers
			// get a payment that really is the largest slice of the sale.
			for _, c := range captures {
				if c.Method == tender {
					paymentID, gatewayRef = c.PaymentID, c.GatewayRef
					break
				}
			}

			// Invoice: PAID for a tendered sale; for the unpaid-at-ring postures the invoice FINALIZES now
			// (COD customers need the invoice number to pay against), PARTIALLY_PAID when a deposit landed.
			// COD is due on demand (today); ACCOUNT runs on the customer's terms.
			// Keyed off the MONEY (capturedCents), not the posture, so the split-with-an-ACCOUNT-leg case
			// lands on PARTIALLY_PAID with the account remainder as its balance, the same place a deposit
			// lands, because it is the same fact.
			invStatus := "PAID"
			balance := int64(0)
			dueDate := s.now().Format("2006-01-02")
			if !fullyPaid {
				invStatus = "FINALIZED"
				balance = total - capturedCents
				if capturedCents > 0 {
					invStatus = "PARTIALLY_PAID"
				}
				if !codSale {
					dueDate = s.now().AddDate(0, 0, cust.PaymentTermsDays).Format("2006-01-02")
				}
			}
			// Sub item 9.5: a supplier's demanded deposit rides the sale as its
			// OWN document (FINALIZED, due from the terms, the deposit amount,
			// the SUPPLIER_DEPOSIT source kind whose GL posting is the
			// Customer Deposits liability), written FIRST because the sale's
			// own invoice names it. Its own subledger INVOICE row and its own
			// ar.invoice.finalized event ride the same transaction.
			var supplierDepositInvoiceID, supplierDepositInvoiceNo string
			if supplierDeposit > 0 {
				var err error
				supplierDepositInvoiceID, supplierDepositInvoiceNo, err = s.store.InsertInvoice(ctx, tx, invoiceRow{
					CustomerID: customerID, OrderID: orderID, BranchID: in.BranchID, Status: "FINALIZED",
					DueDate: dueDate, SubtotalCents: supplierDeposit, TotalCents: supplierDeposit,
					BalanceCents: supplierDeposit, CreatedBy: actor, SourceKind: SourceSupplierDeposit,
				})
				if err != nil {
					return err
				}
				if err := s.store.InsertInvoiceLines(ctx, tx, supplierDepositInvoiceID, nil,
					[]orderLineRow{supplierDepositDocumentLine(lines, supplierDeposit)}); err != nil {
					return err
				}
				if err := s.store.EmitEvent(ctx, tx, "ar.invoice.finalized", map[string]any{
					"invoice_id": supplierDepositInvoiceID, "customer_id": customerID,
					"total_cents": supplierDeposit, "due_date": dueDate,
				}); err != nil {
					return err
				}
			}
			// The sale's own invoice carries THE REST: subtotal and total less
			// the deposit (the account's two documents bill the sale's whole
			// total once), the goods lines unchanged at their real prices plus
			// the zero line naming the deposit document (the schema forbids a
			// negative line; sale_supplier_deposit.go states the choice).
			invLines := lines
			invSubtotal, invTotal := subtotal, total
			if supplierDeposit > 0 {
				invSubtotal, invTotal = subtotal-supplierDeposit, total-supplierDeposit
				balance -= supplierDeposit
				invLines = append(append([]orderLineRow{}, lines...),
					supplierDepositPointerLine(lines, supplierDepositInvoiceNo))
			}
			invoiceID, invoiceNo, err := s.store.InsertInvoice(ctx, tx, invoiceRow{
				CustomerID:    customerID,
				OrderID:       orderID,
				BranchID:      in.BranchID,
				Status:        invStatus,
				DueDate:       dueDate,
				SubtotalCents: invSubtotal,
				TaxCents:      tax,
				TotalCents:    invTotal,
				BalanceCents:  balance,
				CreatedBy:     actor,
			})
			if err != nil {
				return err
			}
			if err := s.store.InsertInvoiceLines(ctx, tx, invoiceID, lineIDs, invLines); err != nil {
				return err
			}

			// Allocate + AR, driven by the capture list. The actor is stamped as allocated_by on every
			// allocation (Q-044). `running` is the customer's balance as each row lands, so the subledger
			// reads as a true running total rather than a set of independently-computed snapshots.
			//
			// A fully-captured sale never touches AR: the invoice books PAID, so the running balance is
			// unchanged and only PAYMENT rows are written (identical to the single-tender behavior this
			// generalizes). An uncaptured remainder books the INVOICE first, then each capture nets it
			// down, landing at open + (total − captured), which is exactly the invoice's own balance.
			running := cust.OpenBalanceCents
			if !fullyPaid {
				running += invTotal
				if err := s.store.InsertCustomerTxn(ctx, tx, customerTxnRow{
					CustomerID: customerID, TxnType: "INVOICE", AmountCents: invTotal,
					BalanceAfter: running, SourceType: "invoice", SourceRefID: invoiceID, BranchID: in.BranchID,
				}); err != nil {
					return err
				}
			}
			if supplierDeposit > 0 {
				running += supplierDeposit
				if err := s.store.InsertCustomerTxn(ctx, tx, customerTxnRow{
					CustomerID: customerID, TxnType: "INVOICE", AmountCents: supplierDeposit,
					BalanceAfter: running, SourceType: "invoice", SourceRefID: supplierDepositInvoiceID,
					BranchID: in.BranchID,
				}); err != nil {
					return err
				}
			}
			for _, c := range captures {
				if !fullyPaid {
					running -= c.AmountCents
				}
				if err := s.store.InsertPaymentAllocation(ctx, tx, c.PaymentID, invoiceID, c.AmountCents, actor); err != nil {
					return err
				}
				if err := s.store.InsertCustomerTxn(ctx, tx, customerTxnRow{
					CustomerID: customerID, TxnType: "PAYMENT", AmountCents: c.AmountCents,
					BalanceAfter: running, SourceType: "payment", SourceRefID: c.PaymentID, BranchID: in.BranchID,
				}); err != nil {
					return err
				}
			}

			// POS facets (idempotency record) + optional relief snapshot.
			posSaleID, err := s.store.InsertPosSale(ctx, tx, posSaleRow{
				IdempotencyKey: in.IdempotencyKey, OrderID: orderID, InvoiceID: invoiceID, PaymentID: paymentID,
				BranchID: in.BranchID, TillSessionID: in.TillSessionID, SaleType: saleType, CashSale: in.CashSale,
				Tender: tender, TaxReasonCode: taxReason, OfflineOrigin: in.Offline, CreatedBy: actor,
				SplitTender: isSplit, Discount: priced.saleDiscount,
			})
			if err != nil {
				return err
			}
			// The pricing log row of every manual discount, on THIS transaction (discount.go).
			if err := s.logDiscounts(ctx, tx, orderID, actor, priced.discounts); err != nil {
				return err
			}
			// DIB-165: the legs. Written AFTER pos_sale (they reference it) and inside this tx, so the
			// deferred sum guard in 0100 fires at COMMIT with every leg present.
			if isSplit {
				rows := make([]saleTenderRow, 0, len(splitLegs))
				bySeq := map[int]captureLeg{}
				for _, c := range captures {
					bySeq[c.Seq] = c
				}
				for _, l := range splitLegs {
					r := saleTenderRow{Seq: l.Seq, Method: l.Method, AmountCents: l.AmountCents}
					if c, ok := bySeq[l.Seq]; ok {
						r.PaymentID, r.GatewayRef, r.TillSessionID = c.PaymentID, c.GatewayRef, in.TillSessionID
					}
					rows = append(rows, r)
				}
				if err := s.store.InsertSaleTenders(ctx, tx, posSaleID, rows); err != nil {
					return err
				}
			}
			if in.Relief != nil {
				// The audit snapshot records the quote the ring bound, in the shape the tax package's W4
				// recompute reads back: rate_bps the ASSESSED rate, full_hst_cents the STANDARD-rate HST on
				// the subtotal (flatHST13, bit-identical to the engine's hstOn at 1300; the recompute's
				// reconstructBase divides this column by the constant 1300, so it must never carry the
				// assessed rate's tax), on_credit_cents the quote's ON credit, net_collected_cents the
				// assessed tax the ring actually collected. At the standard rate this row is byte-identical
				// to the old flat-constant write.
				if _, err := s.store.InsertTaxSnapshot(ctx, tx, taxSnapshotRow{
					OrderID: orderID, TaxReasonCode: taxReason, RuleVersion: ruleVersionW4, RateBps: priced.rateBps,
					FullHSTCents: flatHST13(subtotal), OnCreditCents: priced.onCreditCents,
					NetCollectedCents: tax, Automated: false,
					EvidenceDocID: in.Relief.EvidenceDocID,
				}); err != nil {
					return err
				}
			}

			// Events (transactional outbox; carry no PII / no card data / no FN identifiers).
			if err := s.store.EmitEvent(ctx, tx, "pos.sale.committed", map[string]any{
				"order_id": orderID, "invoice_id": invoiceID, "payment_id": paymentID,
				"tender": tender, "sale_type": saleType, "branch_id": in.BranchID, "total_cents": total,
			}); err != nil {
				return err
			}
			// The invoice ALWAYS finalizes for its full total, so buildInvoiceFinalized posts the accrual
			// (DR 1100 AR / CR 4000 revenue / CR 2200 HST) off the invoice's own subtotal/tax facts. Each
			// captured leg then settles its slice of that AR through payment.received (DR 1020 undeposited /
			// CR 1100). A fully-captured counter sale therefore nets to DR 1020 / CR 4000 / CR 2200 with AR
			// closing to zero; a split with an account leg leaves exactly the account leg in 1100, which is
			// the receivable that genuinely exists. Same rules, same builders, no new posting_rules and no
			// new topic, a split is N ordinary payment.received events, not a new kind of money.
			if err := s.store.EmitEvent(ctx, tx, "ar.invoice.finalized", map[string]any{
				"invoice_id": invoiceID, "customer_id": customerID, "total_cents": invTotal, "due_date": dueDate,
			}); err != nil {
				return err
			}
			for _, c := range captures {
				if err := s.store.EmitEvent(ctx, tx, "payment.received", map[string]any{
					"payment_id": c.PaymentID, "order_id": orderID, "amount_cents": c.AmountCents,
					"method": c.Method, "gateway_ref": c.GatewayRef,
				}); err != nil {
					return err
				}
			}
			if in.Relief != nil {
				if err := s.store.EmitEvent(ctx, tx, "tax.relief.assessed", map[string]any{
					"order_id": orderID, "reason_code": taxReason, "automated": false,
				}); err != nil {
					return err
				}
			}

			out = Sale{
				OrderID: orderID, OrderNo: orderNo, InvoiceID: invoiceID, InvoiceNo: invoiceNo,
				PaymentID: paymentID, GatewayRef: gatewayRef, Tender: tender, SaleType: saleType,
				SubtotalCents: subtotal, TaxCents: tax, TotalCents: total, DepositCents: deposit,
				SupplierDepositCents:     supplierDeposit,
				SupplierDepositInvoiceID: supplierDepositInvoiceID, SupplierDepositInvoiceNo: supplierDepositInvoiceNo,
				TaxReasonCode: taxReason, Status: "COMMITTED", SplitTender: isSplit,
				// DIB-488 rev 6: the item-D facts echoed back; item-F change due for the sold panel.
				POJobRef: poJobRef, SalespersonID: in.SalespersonID, SalespersonName: salespersonName,
				CashTenderedCents: in.CashTenderedCents,
				// DIB-488 rev 8 (item 11): the job facts echoed back for the sold panel.
				JobAddress: jobAddress, JobReference: jobReference,
			}
			out.DiscountCents, out.Discounts = reportDiscounts(priced.discounts)
			if in.CashTenderedCents > 0 {
				out.ChangeDueCents = in.CashTenderedCents - total
			}
			if isSplit {
				bySeq := map[int]captureLeg{}
				for _, c := range captures {
					bySeq[c.Seq] = c
				}
				for _, l := range splitLegs {
					r := TenderLegResult{Seq: l.Seq, Method: l.Method, AmountCents: l.AmountCents}
					if c, ok := bySeq[l.Seq]; ok {
						r.PaymentID, r.GatewayRef = c.PaymentID, c.GatewayRef
					}
					out.Tenders = append(out.Tenders, r)
				}
			}
			return nil
		})
	}
	txErr := runSaleTx()
	for rerun := 0; txErr != nil && rerun < saleTxSerializationReruns && isSerializationFault(txErr); rerun++ {
		// The transient fault (the design's step 5): the captures are unbound, so the rerun binds
		// them on a fresh transaction, with no void and no second charge.
		txErr = runSaleTx()
	}
	if txErr != nil {
		if len(ring.captured) > 0 {
			// A fault after the captures: compensate and answer in words, never the bare 500 a
			// rolled-back card ring used to answer (the design's step 6).
			ring.rank = cardRankReversed
			return s.settleFailedCardRing(ctx, ring)
		}
		return Sale{}, txErr
	}

	// Audit the completed sale post-commit (the Recorder owns the audit_log table; zero-trust).
	// DIB-488 rev 6 (item D): the PO / job reference and the credited salesperson ride the SAME
	// record's Extra, the order's audit/activity trail carries the salesperson's ID and the
	// reference it was sold against, per the existing Extra idiom (ActPaymentAllocated below).
	// Absent when unused. The trail keeps ids and references only: salesperson_name and
	// job_address are set here but dropped at the audit chokepoint, a name is read from the id
	// when shown.
	var saleExtra map[string]any
	if poJobRef != "" || in.SalespersonID != "" || jobAddress != "" || jobReference != "" {
		saleExtra = map[string]any{}
		if poJobRef != "" {
			saleExtra["po_job_ref"] = poJobRef
		}
		if in.SalespersonID != "" {
			saleExtra["salesperson_id"] = in.SalespersonID
			saleExtra["salesperson_name"] = salespersonName
		}
		// DIB-488 rev 8 (item 11): the job facts ride the same Extra idiom, absent when unused.
		if jobAddress != "" {
			saleExtra["job_address"] = jobAddress
		}
		if jobReference != "" {
			saleExtra["job_reference"] = jobReference
		}
	}
	s.record(ctx, audit.SensitiveAccess{
		Action: audit.ActSaleCompleted, EntityType: "pos.sale", EntityID: out.OrderID,
		ActorID: actor, BranchID: in.BranchID, Purpose: "POS_SALE", Reason: tender,
		Extra: saleExtra,
	})
	// DIB-159 order-change history: the POS ring is the FIRST status writer, the order was inserted
	// at CONFIRMED (there is no prior status), so this seeds the ORDER_STATUS_CHANGED timeline with the
	// creation transition ("" → CONFIRMED). Recorded post-commit through the SAME Recorder seam + entity
	// ("order") that order.Service.SetStatus uses, so every writer lands on one queryable per-order
	// timeline. Best-effort (the orders table is the truth).
	//
	// We record ONLY the transition orders.status actually underwent: the INSERT-at-CONFIRMED. A
	// COLLECT_NOW sale is fulfilled *semantically* (stock issued + order.fulfilled emitted for GL COGS)
	// but the orders.status COLUMN is never advanced to FULFILLED anywhere in this codebase, so we must
	// NOT record a CONFIRMED→FULFILLED row, the history is the timeline of the status column, and a
	// fabricated FULFILLED transition would contradict every order detail/list view that reads the true
	// status. When a real status writer advances the column (the choke-point order.Service.SetStatus,
	// or a future POS fulfilment write), THAT writer emits its own transition.
	s.recordStatusChange(ctx, out.OrderID, "", "CONFIRMED", actor, in.BranchID, "pos_ring")
	// Every manual discount and the manager who approved it, one audit row each (discount.go).
	s.recordDiscountAudit(ctx, out.OrderID, actor, in.BranchID, priced.discounts)
	// Q-044: PAYMENT_RECEIVED for the tender is recorded HERE, post-commit, through the till's hook
	// (the till still owns the audit semantics; the sole payments-row writer stays A-03). Mid-tx
	// recording produced an orphan row under rollback AND a duplicate under rollback-retry; the hook
	// no-ops on a replayed tender (Inserted=false), keeping exactly-once per committed tender.
	// One hook per CAPTURED leg, a split takes three tenders and the drawer's audit trail must show
	// three receipts, not one.
	for _, res := range tenderResults {
		if res.PaymentID != "" {
			s.tender.RecordPaymentReceived(ctx, res)
		}
	}
	// Q-044: the POS sale writes its payment→invoice allocation through its own repo (not
	// till.Allocate), so the explicit "payment applied to invoice" record is stamped here -
	// entity = payment, so the trail is queryable per payment via the audit transaction reopen.
	// One record per allocation: each leg was applied to the invoice separately.
	if out.InvoiceID != "" {
		for _, c := range captures {
			if c.PaymentID == "" {
				continue
			}
			s.record(ctx, audit.SensitiveAccess{
				Action: audit.ActPaymentAllocated, EntityType: "payment", EntityID: c.PaymentID,
				ActorID: actor, BranchID: in.BranchID, Purpose: "PAYMENT_ALLOCATED",
				Extra: map[string]any{"invoice_id": out.InvoiceID, "amount_cents": c.AmountCents},
			})
		}
	}
	return out, nil
}

// -- the card capture loop, its ring and its compensation (the card charge design 3.3 to 3.7) --

// saleTxSerializationReruns bounds the ring's rerun of the sale transaction on a serialization
// fault (SQLSTATE 40P01, deadlock_detected, or 40001, serialization_failure): a small fixed
// number of fresh transactions with the same captures, the original error returned once they are
// spent (the design's step 5).
const saleTxSerializationReruns = 2

// The from-row answer ranks: the strongest answer a failed ring gives. A manager item outranks a
// decline (money may be held), a decline outranks a reversal (the cashier can act on the decline
// wording), and a clean reversal of every captured leg is the 503 that tells the client to clear
// its key and ring again.
const (
	cardRankNone = iota
	cardRankReversed
	cardRankDeclined
	cardRankManager
)

// cardRing is the capture loop's outcome for one ring: the legs THIS request captured (unbound
// until the sale transaction binds them), the strongest from-row answer it met, and, when a leg
// already belonged to a committed sale, that sale to answer verbatim.
type cardRing struct {
	saleKey       string
	captured      []captureLeg
	replay        *Sale
	rank          int
	managerRefs   []string
	notDispatched error
}

// failed reports whether the ring met an answer that stops it reaching the transaction.
func (r *cardRing) failed() bool { return r.rank > cardRankNone || r.notDispatched != nil }

// shortAttemptRef names an attempt the way a manager reads it: the head of its id.
func shortAttemptRef(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// captureCardLegs is the pre-transaction capture loop (the card charge design 3.3): every CARD
// leg, in seq order, is captured on the pool by the till, and the loop stops dispatching at the
// first leg that ends anything but CAPTURED. The legs after it are read read-only: their rows,
// when a failed ring left any, answer from themselves so the strongest answer wins (a same-key
// retry of a split decline answers the decline, not the reversal of the leg before it), and a leg
// with no row is never charged for a dead ring. No transaction is held anywhere on this path, so
// no statement here can wait on a connection a caller holds (decision log row 786).
func (s *Service) captureCardLegs(ctx context.Context, in SaleInput, captures []captureLeg, customerID, actor string) (*cardRing, error) {
	ring := &cardRing{saleKey: in.IdempotencyKey}
	readOnly := false
	for i := range captures {
		c := &captures[i]
		if c.Method != TenderCard {
			continue
		}
		res, err := s.tender.CaptureCard(ctx, till.CardChargeInput{
			TillSessionID: in.TillSessionID,
			BranchID:      in.BranchID,
			CustomerID:    customerID,
			AmountCents:   c.AmountCents,
			LegKey:        c.IdemKey,
			SaleKey:       in.IdempotencyKey,
			LegSeq:        c.Seq,
			AccountToken:  c.PaymentToken,
			CardType:      c.CardType,
			CreatedBy:     actor,
			ReadOnly:      readOnly,
		})
		if err == nil {
			switch res.Kind {
			case till.CardCaptureCaptured:
				c.AttemptID, c.GatewayRef = res.AttemptID, res.ProcessorRef
				ring.captured = append(ring.captured, *c)
				continue
			case till.CardCaptureBound:
				// The leg already belongs to a committed sale (normally the pool precheck above
				// answered first; this is the race it can lose). Answer that sale, verbatim.
				if sale, found, ferr := s.findSaleByKey(ctx, in.IdempotencyKey); ferr == nil && found {
					sale.Replayed = true
					ring.replay = &sale
					return ring, nil
				}
				// BOUND with no sale behind it: a person has to look.
				ring.rank = cardRankManager
				ring.managerRefs = append(ring.managerRefs, shortAttemptRef(res.AttemptID))
			default:
				// An unclassified capture kind is an unknown outcome (the design's 3.5): when
				// this ring already holds captures their money may be held with no sale, so
				// the ring ranks manager and returns for settlement, and the compensator
				// reverses what it holds. A ring that captured nothing keeps the raw word.
				if len(ring.captured) == 0 {
					return nil, clientErr(http.StatusInternalServerError, "pos: the card rail answered an unknown capture kind %q", res.Kind)
				}
				ring.rank = cardRankManager
				if res.AttemptID != "" {
					ring.managerRefs = append(ring.managerRefs, shortAttemptRef(res.AttemptID))
				}
				return ring, nil
			}
			readOnly = true
			continue
		}
		ref := shortAttemptRef(res.AttemptID)
		switch {
		case errors.Is(err, till.ErrCardLegUnattempted):
			// A read-only pass over a leg no earlier ring reached: nothing to rank, nothing to
			// charge, the loop simply moves on.
			continue
		case errors.Is(err, till.ErrCardInFlight):
			if len(ring.captured) == 0 {
				// The concurrent double submit's loser (the design's 3.7): another request owns the
				// leg, this ring took no money, and the answer is immediate with nothing to
				// compensate.
				return nil, ErrCardChargeInFlight
			}
			// A later leg in flight behind earlier captures cannot arise through this sale key's
			// own legs (a competitor that owns leg N owns leg 1 too, and this ring would have
			// answered in flight there). If it ever does, the money this ring holds goes to a
			// person, not to a void.
			ring.rank = cardRankManager
			ring.managerRefs = append(ring.managerRefs, ref)
		case errors.Is(err, till.ErrCardDeclined):
			if ring.rank < cardRankDeclined {
				ring.rank = cardRankDeclined
			}
		case errors.Is(err, till.ErrCardNeedsManager):
			ring.rank = cardRankManager
			ring.managerRefs = append(ring.managerRefs, ref)
		case errors.Is(err, till.ErrCardReversed), errors.Is(err, till.ErrCardResolved):
			if ring.rank < cardRankReversed {
				ring.rank = cardRankReversed
			}
		case errors.Is(err, till.ErrCardUnresolvedOnTill):
			if len(ring.captured) == 0 {
				return nil, ErrCardChargeUnresolvedOnTill
			}
			// The guard refused a later leg while this ring holds earlier captures: the guard's
			// word would tell the cashier to re-ring, and the captures must go to a person first.
			ring.rank = cardRankManager
			ring.managerRefs = append(ring.managerRefs, ref)
		case errors.Is(err, till.ErrCardNotDispatched):
			if ring.notDispatched == nil {
				ring.notDispatched = err
			}
		default:
			// An error nobody classified is an unknown outcome (the design's 3.5): when this
			// ring already holds captures their money may be held with no sale, so the ring
			// ranks manager and returns for settlement, and the compensator reverses what it
			// holds. A ring that captured nothing keeps the raw error.
			if len(ring.captured) == 0 {
				return nil, err
			}
			ring.rank = cardRankManager
			if ref != "" {
				ring.managerRefs = append(ring.managerRefs, ref)
			}
			return ring, nil
		}
		readOnly = true
	}
	return ring, nil
}

// settleFailedCardRing compensates every leg this request captured and shapes the ring's answer
// (the card charge design 3.3 step 6): the compensator runs on the pool, after the ring gave up,
// and the answer is 503 sale_failed_card_reversed when every captured leg reached VOIDED, 409
// card_charge_needs_manager when any leg reached a state a person has to close (or the
// compensation itself could not tell), the capture loop's own ranked answer when the ring failed
// before the transaction (402 for a decline, 409 for an unknown), and the committed sale itself
// when a compensation finds the commit landed after all (the lost acknowledgement).
func (s *Service) settleFailedCardRing(ctx context.Context, ring *cardRing) (Sale, error) {
	for i := range ring.captured {
		comp, err := s.tender.CompensateCard(ctx, ring.captured[i].AttemptID)
		if err != nil {
			// The compensation could not even be attempted: the attempt stays CAPTURED for the
			// sweep, and a person is told now.
			ring.managerRefs = append(ring.managerRefs, shortAttemptRef(ring.captured[i].AttemptID))
			continue
		}
		switch comp.Kind {
		case till.CardCompCommitted:
			if sale, found, ferr := s.findSaleByKey(ctx, ring.saleKey); ferr == nil && found {
				sale.Replayed = true
				return sale, nil
			}
			ring.managerRefs = append(ring.managerRefs, shortAttemptRef(comp.AttemptID))
		case till.CardCompNeedsManager:
			ring.managerRefs = append(ring.managerRefs, shortAttemptRef(comp.AttemptID))
		}
	}
	if len(ring.managerRefs) > 0 {
		return Sale{}, ErrCardChargeNeedsManager{Refs: ring.managerRefs}
	}
	switch {
	case ring.notDispatched != nil:
		return Sale{}, clientErr(http.StatusServiceUnavailable, "pos: the card charge was not dispatched: %s", ring.notDispatched.Error())
	case ring.rank >= cardRankManager:
		return Sale{}, ErrCardChargeNeedsManager{}
	case ring.rank == cardRankDeclined:
		return Sale{}, ErrCardChargeCardDeclined
	}
	return Sale{}, ErrSaleFailedCardReversed
}

// isSerializationFault reports a transaction error Postgres answers with SQLSTATE 40P01
// (deadlock_detected) or 40001 (serialization_failure): the two transient faults the ring reruns
// on a fresh transaction.
func isSerializationFault(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && (pgErr.Code == "40P01" || pgErr.Code == "40001")
}

// GetSale returns a committed sale for an operator reopen (US-A19-09). The read carries NO FN-evidence
// identifiers, the granular admin trail is the A-21 RequireAdmin viewer.
func (s *Service) GetSale(ctx context.Context, id string) (SaleView, error) {
	if id == "" {
		return SaleView{}, errBadRequest
	}
	// Malformed (non-UUID) id can never match a uuid PK, treat as not-found (IDOR-safe, no
	// existence leak) at the boundary, so the native uuid comparison below keeps its index +
	// case-insensitive match without a 22P02 → 500 on bad input (sweep #46 hardening, GetSaleView).
	if _, perr := uuid.Parse(id); perr != nil {
		return SaleView{}, errSaleNotFound
	}
	v, found, err := s.store.GetSaleView(ctx, id)
	if err != nil {
		return SaleView{}, err
	}
	if !found {
		return SaleView{}, errSaleNotFound
	}
	// DIB-165 branch fence on the reopen. The ring has been branch-scoped since DIB-389, but the
	// READ of a committed sale never was: a branch-scoped cashier could walk sale ids and reopen
	// another branch's counter history, customer, totals, tender and every line. Now that the past-
	// sale search makes ids easy to come by, that gap is not theoretical. Posture matches landedcost:
	// an EXISTING sale in an unreachable branch is 403 (a uuid is not guessable, so there is no
	// enumeration oracle to protect); a MISSING sale stays 404 above.
	caller, cerr := s.caller(ctx)
	if cerr != nil {
		return SaleView{}, cerr
	}
	if !caller.CanAccessBranch(v.BranchID) {
		return SaleView{}, clientErr(http.StatusForbidden, "pos: caller cannot access branch %s", v.BranchID)
	}
	s.record(ctx, audit.SensitiveAccess{
		Action: audit.ActHTTPPost, EntityType: "pos.sale", EntityID: id,
		ActorID: identity.StaffUserIDFromContext(ctx), BranchID: v.BranchID, Purpose: "REOPEN_VIEW",
	})
	return v, nil
}

// AssessRelief runs the per-transaction FN relief assessment (US-A19-06). It pre-populates MASKED-only
// from the eligibility profile, computes the 0%/5%/13%/review outcome on the flat-13% fallback, enforces
// the 0% fulfilment-evidence HARD GATE, never auto-exempts (ignores customers.tax_exempt), and audits
// every evidence access. The binding collected tax stays flat-13% at gate (M5 owns the engine).
func (s *Service) AssessRelief(ctx context.Context, in ReliefAssessInput) (ReliefOutcome, error) {
	var prof eligibilityMasked
	if in.ProfileID != "" {
		p, err := s.store.ReadEligibilityProfileMasked(ctx, in.ProfileID)
		if err != nil {
			return ReliefOutcome{}, err
		}
		prof = p
	}

	// Every view/change/override of FN evidence writes an audit row (actor + timestamp), FN spec §15.
	s.record(ctx, audit.SensitiveAccess{
		Action: audit.ActTaxReliefAssess, EntityType: "fn.eligibility_profile", EntityID: in.ProfileID,
		ActorID: identity.StaffUserIDFromContext(ctx), Purpose: "TAX_RELIEF_ASSESS", MaskedRef: prof.MaskedIdentifier,
		Extra: map[string]any{"declared_use": in.DeclaredUse, "fulfilment": in.Fulfilment},
	})

	// The non-relief FALLBACK rate + full-HST are assessed through the TaxAssessor seam so a DB rate change
	// flows through here too (FlatHSTAssessor returns TaxCents=flatHST13, RateBps=1300 → bit-identical W4/W5).
	fallback, err := s.taxAssessor().AssessSale(ctx, TaxQuoteInput{SubtotalCents: in.SubtotalCents})
	if err != nil {
		return ReliefOutcome{}, err
	}

	out := ReliefOutcome{
		PurchaserType:    firstNonEmpty(prof.PurchaserType, in.PurchaserType),
		MaskedIdentifier: prof.MaskedIdentifier,
		Automated:        false, // M5 flips this true
		FullHSTCents:     fallback.TaxCents,
	}
	eligible := !prof.Expired && (prof.OntarioEligibility == "ON_RESIDENT" || prof.OntarioEligibility == "AKWESASNE")

	switch in.Fulfilment {
	case "pickup", "purchaser_carrier":
		// Customer pickup NEVER qualifies for 0%, the outcome is 5% (Ontario path) or 13%.
		if eligible && in.DeclaredUse == "personal" {
			out.Outcome = "5%"
			out.ReasonCode = TaxONFNPos8
			out.RateBps = ontarioNetBps
			out.OnCreditCents = (in.SubtotalCents*ontarioCreditBps + 5000) / 10000
			out.NetCollectedCents = (in.SubtotalCents*ontarioNetBps + 5000) / 10000
		} else {
			out.Outcome = "13%"
			out.ReasonCode = TaxStdON13
			out.RateBps = fallback.RateBps
			out.NetCollectedCents = out.FullHSTCents
		}
	case "dibbits_truck", "dibbits_carrier":
		// Dibbits delivery to a validated reserve → PROVISIONAL 0%, but the sale CANNOT finalize at 0%
		// until delivery evidence (POD/waybill) is attached, a HARD GATE. Until then it falls to 13%.
		if eligible {
			out.RequiresEvidence = true
			out.ReasonCode = TaxFedVendorDel
			if in.EvidenceAttached {
				out.Outcome = "0%"
				out.RateBps = 0
				out.NetCollectedCents = 0
				out.EvidenceSatisfied = true
			} else {
				out.Outcome = "0% (pending evidence)"
				out.RateBps = fallback.RateBps // binding falls back to the assessed rate until evidence is attached
				out.NetCollectedCents = out.FullHSTCents
			}
		} else {
			out.Outcome = "13%"
			out.ReasonCode = TaxStdON13
			out.RateBps = fallback.RateBps
			out.NetCollectedCents = out.FullHSTCents
		}
	default:
		out.Outcome = "REVIEW"
		out.ReasonCode = TaxManualReview
		out.RateBps = fallback.RateBps
		out.NetCollectedCents = out.FullHSTCents
	}

	// Record the immutable snapshot when bound to an order (the auditable "why"). Insert-only, a
	// return/credit writes a NEW reversing snapshot, never an UPDATE (FN spec §11.4).
	if in.OrderID != "" {
		if err := s.store.RunInTx(ctx, func(tx pgx.Tx) error {
			_, e := s.store.InsertTaxSnapshot(ctx, tx, taxSnapshotRow{
				OrderID: in.OrderID, TaxReasonCode: out.ReasonCode, RuleVersion: ruleVersionW4, RateBps: out.RateBps,
				FullHSTCents: out.FullHSTCents, OnCreditCents: out.OnCreditCents, NetCollectedCents: out.NetCollectedCents,
				Automated: false,
			})
			return e
		}); err != nil {
			return ReliefOutcome{}, err
		}
	}
	return out, nil
}

// ListSalespeople answers the counter's salesperson selector (DIB-488 rev 6, item D): the ACTIVE
// staff, id + display name only. Staff-gated at the route; an empty roster is an ordinary empty
// list, never an error.
func (s *Service) ListSalespeople(ctx context.Context) ([]Salesperson, error) {
	out, err := s.store.ListSalespeople(ctx)
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = []Salesperson{}
	}
	return out, nil
}

// StockGlance returns read-only multi-branch on-hand for a product (US-A19-08). It NEVER decrements or
// allocates, the gate POS does not move inventory (M2). The read is isolated as M2 debt.
func (s *Service) StockGlance(ctx context.Context, productID string) (StockGlance, error) {
	if productID == "" {
		return StockGlance{}, errBadRequest
	}
	onHand, err := s.store.ReadStockOnHand(ctx, productID)
	if err != nil {
		return StockGlance{}, err
	}
	// DIB-488: the unit meta that lets the counter speak the number in the item's sell unit, plus
	// any authored low thresholds. All existing availability data (products / product_uoms / 0117
	// reorder points), one read; the glance remains display-only.
	baseUOM, sellUOM, sellFactor, lowBase, err := s.store.ReadStockUnitMeta(ctx, productID)
	if err != nil {
		return StockGlance{}, err
	}
	if len(lowBase) == 0 {
		lowBase = nil // absent, not empty, no threshold authored is a configured non-state
	}
	// DIB-488 rev 8 (item 9): the catalogue's unit weight rides the same display-only read. No
	// weight on file → the field is absent (0/omitempty) and the ticket line shows an em-dash.
	weightKg, hasWeight, err := s.store.ReadProductWeightKg(ctx, productID)
	if err != nil {
		return StockGlance{}, err
	}
	if !hasWeight {
		weightKg = 0
	}
	return StockGlance{
		ProductID: productID, OnHand: onHand,
		BaseUOM: baseUOM, SellUOM: sellUOM, SellFactor: sellFactor, LowBase: lowBase,
		WeightKgPerBase: weightKg,
	}, nil
}

// CaptureSignature drives the SignatureProvider seam (US-A19-07). The simulated provider encrypts the
// payload at rest, links it to the transaction, and audits the capture.
func (s *Service) CaptureSignature(ctx context.Context, purpose, refType, refID string) (string, error) {
	if s.sig == nil {
		return "", errNoSigProvider
	}
	// Lazily bind the store/audit/clock into the simulated provider (main.go wires it with the vault
	// only; the persistence deps are A-19-internal).
	if sp, ok := s.sig.(*SimulatedSignatureProvider); ok {
		sp.bind(s.store, s.audit, s.now)
	}
	return s.sig.Capture(ctx, purpose, refType, refID)
}

func (s *Service) record(ctx context.Context, a audit.SensitiveAccess) {
	if s.audit == nil {
		return
	}
	_ = s.audit.Record(ctx, a)
}

// recordStatusChange emits ONE DIB-159 ORDER_STATUS_CHANGED row for a status transition driven from
// the POS module (the ring/fulfilment path). It mirrors the order.Service.SetStatus record shape
// exactly, same action + entity type ("order") + Extra keys, so a pos-driven transition lands on the
// same per-order timeline as a portal/ERP one (GET /api/v1/orders/{id}/history). Best-effort: a nil
// Recorder or a failed insert never affects the committed sale.
func (s *Service) recordStatusChange(ctx context.Context, orderID, from, to, actor, branchID, reason string) {
	if s.audit == nil || orderID == "" {
		return
	}
	_ = s.audit.Record(ctx, audit.SensitiveAccess{
		Action: audit.ActOrderStatusChanged, EntityType: "order", EntityID: orderID,
		ActorID: actor, BranchID: branchID, Purpose: "ORDER_STATUS_TRANSITION", Reason: reason,
		Extra: map[string]any{"old_status": from, "new_status": to},
	})
}

// fulfillmentFor maps the sale type to orders.fulfillment. COD is a payment posture, not a shipping
// mode, so it carries its own fulfilment field (any of the three; PICKUP when unspecified), the
// 2026-06-30 correction: "cash on demand", never hard-mapped to DELIVERY.
func fulfillmentFor(saleType, codFulfilment string) string {
	switch saleType {
	case SaleCollectLater:
		return "WILL_CALL"
	case SaleDelivered:
		return "DELIVERY"
	case SaleCOD:
		if codFulfilment != "" {
			return codFulfilment
		}
		return "PICKUP"
	default:
		return "PICKUP"
	}
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
