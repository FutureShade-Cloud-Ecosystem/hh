package pos

import (
	"context"
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
)

// -- injected sibling seams (all interfaces so tests fake them) --------------

// LineResolver is satisfied by *pricing.Service, the server-side price authority (US-A19-04).
type LineResolver interface {
	DeriveLinePrice(ctx context.Context, in pricing.ResolveInput) (pricing.LinePrice, error)
}

// TenderTaker is satisfied by *till.Service (A-03), the SOLE writer of payments + till_movements rows.
// The Sale/Return opens the tx and passes it in so capture joins the same transaction as the order +
// invoice. TakeTender carries the payment id + the opaque gateway_ref (the single source of truth for
// both); RefundTender (W7) is the refund rail, the REFUNDED payments row and, for CASH, the PAID_OUT
// drawer movement, all written by the till inside the return tx.
type TenderTaker interface {
	TakeTender(ctx context.Context, tx pgx.Tx, in till.TenderInput) (till.TenderResult, error)
	RefundTender(ctx context.Context, tx pgx.Tx, in till.RefundInput) (till.RefundResult, error)
	// RecordPaymentReceived is the Q-044 post-commit hook: the till writes the PAYMENT_RECEIVED business
	// record AFTER the caller's money tx commits (never mid-tx, a rollback would orphan the row and a
	// retry would duplicate it). Exactly-once: a replayed tender carries Inserted=false and records nothing.
	RecordPaymentReceived(ctx context.Context, res till.TenderResult)
	// GetSession reads the drawer session so a sale can assert the ring's branch IS the session's branch
	// (DIB-389). Declared here rather than as a new injected seam so the guard needs no main.go rewiring -
	// *till.Service already satisfies it. The till stays the owner of the session; pos only reads it.
	GetSession(ctx context.Context, id string) (till.SessionDetail, error)
}

// CardRefunder is RUN-PAY's card reversal rail (payment.Service.Refund behind a creds-free adapter in
// cmd/server/main.go, mirroring cardChargerAdapter). pos passes the ORIGINAL capture's gateway_ref and
// receives the reversal's own processor ref, creds never cross into pos, and pos never writes payments.
type CardRefunder interface {
	RefundCard(ctx context.Context, gatewayRef string, amountCents int64, reference, idempotencyKey string) (refundRef string, err error)
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
	ResolveWalkIn(ctx context.Context) (string, error)
	// multi-branch stock glance (non-tx read; M2 debt)
	ReadStockOnHand(ctx context.Context, productID string) (map[string]float64, error)
	// DIB-488: unit meta + low thresholds for the counter stock-status line (non-tx read; M2 debt)
	ReadStockUnitMeta(ctx context.Context, productID string) (baseUOM, sellUOM string, sellFactor float64, lowBase map[string]float64, err error)
	// DIB-488 rev 6 (item D): salesperson reads, validation + the counter selector (non-tx; M2 debt)
	ReadStaffName(ctx context.Context, userID string) (string, bool, error)
	ListSalespeople(ctx context.Context) ([]Salesperson, error)
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
	ReturnOrderContext(ctx context.Context, orderID, orderNo string) (returnOrderCtx, bool, error)
	ListReturnableLines(ctx context.Context, orderID string) ([]ReturnableLineView, error)
	LockReturnableLine(ctx context.Context, tx pgx.Tx, orderID, orderLineID string) (returnableLine, bool, error)
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
	// and nil says nothing, never an error, and never "this account is current".
	aging      AgingReader
	tender     TenderTaker
	credit     CreditGate
	sig        SignatureProvider
	tax        TaxAssessor
	stock      StockDecrementer
	restock    StockRestocker
	cardRefund CardRefunder
	audit      audit.Recorder
	// trail is the audit READ seam behind the counter settings change history (policy_history.go).
	// Nil is an empty history, never an error.
	trail PolicyTrailReader
	// roles is the dealer's role configuration the till's rules read (D49):
	// the glance's cost and margin list (X3), the refund amount and its
	// approvers (X7). The startup path loads it through roleconfig.Default
	// (or Load) and stops on the error. Nil keeps a directly-built service
	// (the unit harness, a composition predating the wiring) on the
	// pre-configuration behavior the older tests pin.
	roles *roleconfig.Config
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
// without approval and the approvers above it (X7). The configuration a
// caller hands over must be a loaded one (roleconfig.Default, Load or
// Validate): a refused configuration yields no Config to guard with.
func (s *Service) WithRoleConfig(c *roleconfig.Config) *Service { s.roles = c; return s }

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
		if in.DepositCents < 0 {
			return Sale{}, errDepositTooLarge
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
	if cached, found, err := s.store.FindSaleByKey(ctx, in.IdempotencyKey); err != nil {
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

	// 3. Price every line via the engine (the cashier never types a price). A blocked line aborts.
	lines := make([]orderLineRow, len(in.Lines))
	var subtotal int64
	for i, li := range in.Lines {
		lp, err := s.pricing.DeriveLinePrice(ctx, pricing.ResolveInput{
			CustomerID:   customerID,
			CustomerType: cust.CustomerType,
			ProductID:    li.ProductID,
			BranchID:     in.BranchID,
			QtyEntered:   li.QtyEntered,
			UOMEntered:   li.UOMEntered,
			CostCents:    li.CostCents,
		})
		if err != nil {
			// A pricing FAILURE (e.g. a walk-in RETAIL line with no retail anchor or no uom conversion
			// factor) is a client-correctable 4xx, not a server fault. Surface it with the SAME status the
			// /pricing endpoints return, scoped to the offending line, so the cashier sees a real message.
			// Before this, the raw pricing sentinel fell through the handler's writeErr default to a bare
			// HTTP 500 "internal error", and because a walk-in CASH ring resolves RETAIL pricing, a
			// retail-pricing gap 500'd the whole sale (DIB-197). Every tender hits this same line-pricing
			// step, so the mapping fixes them all without touching a successfully-priced sale.
			if st, msg, ok := pricing.ClientStatus(err); ok {
				return Sale{}, clientErr(st, "line %d: %s", i+1, msg)
			}
			return Sale{}, err
		}
		if lp.Breakdown.Blocked {
			return Sale{}, ErrPriceBlocked{Policy: lp.Breakdown.BlockPolicy, LineNo: i + 1}
		}
		lines[i] = orderLineRow{
			LineNo:         i + 1,
			ProductID:      li.ProductID,
			QtyEntered:     li.QtyEntered,
			UOMEntered:     li.UOMEntered,
			QtyBase:        lp.QtyBase,
			UnitPriceCents: lp.UnitPriceCents,
			PriceUOM:       lp.PriceUOM,
			LineTotalCents: lp.LineTotalCents,
		}
		subtotal += lp.LineTotalCents
	}

	// 4. Totals, assessed through the TaxAssessor seam (M5 owns the real engine). The default FlatHSTAssessor
	//    returns flat 13% (D-A19-9), so at scaffold the binding tax is bit-identical to the old flatHST13.
	//    Relief sets the reason code + snapshot, but the BINDING collected tax stays the assessed value at gate.
	taxReason := TaxStdON13
	if in.Relief != nil && in.Relief.ReasonCode != "" {
		taxReason = in.Relief.ReasonCode
	}
	quote, err := s.taxAssessor().AssessSale(ctx, TaxQuoteInput{BranchID: in.BranchID, SubtotalCents: subtotal, ReasonCode: taxReason})
	if err != nil {
		return Sale{}, err
	}
	tax := quote.TaxCents
	total := subtotal + tax

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

	// Deposit bound: 0 < deposit < total (a deposit equal to the total is just a paid sale, ring it paid).
	deposit := in.DepositCents
	if deposit != 0 && deposit >= total {
		return Sale{}, errDepositTooLarge
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
	var out Sale
	// tenderResults holds one entry per CAPTURED leg, out of the tx so the Q-044 PAYMENT_RECEIVED
	// records can be written POST-COMMIT (a rollback must leave no receipt record; a replay carries
	// Inserted=false). A single-tender sale puts exactly one entry here, as before.
	var tenderResults []till.TenderResult

	// 6-12. One atomic tx: order → tender → invoice → allocate → POS facets → events → commit.
	err = s.store.RunInTx(ctx, func(tx pgx.Tx) error {
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

		// Capture-before-fulfil: take the tender BEFORE the invoice/allocation so a declined card rolls
		// the whole tx back (no order, no paid invoice). A-03 writes the SINGLE payments row in THIS tx.
		// An unpaid-at-ring sale with a DEPOSIT captures the deposit through the same seam, the till
		// stays the sole payments writer and a declined deposit card rolls the whole sale back too.
		var paymentID, gatewayRef string
		// Every capture leg runs through the SAME till seam, inside THIS tx. A split therefore writes N
		// ordinary payments rows and N ordinary drawer movements, the till, the GL and the drawer close
		// see nothing exotic, only several tenders that happen to belong to one order. Each leg carries
		// its OWN derived idempotency key (payments.idempotency_key is UNIQUE, so reusing the sale's key
		// would silently collapse legs 2..n onto leg 1's row and leave the drawer short).
		if len(captures) > 0 && in.TillSessionID == "" {
			return ErrNoOpenTill{}
		}
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
			})
			if err != nil {
				// Only a REAL gateway decline reads as "card declined" to the cashier. Any other till
				// failure (session not open, branch access, conflict) surfaces with its own message -
				// mislabeling them as declines sent cashiers chasing the card when the drawer was the
				// problem. Wrapped into pos's client-error type so the handler maps it (409, not 500).
				// A failing leg rolls the WHOLE tx back, so a declined card on leg 2 does not leave
				// leg 1's cash sitting against an order that was never committed.
				if c.Method == TenderCard && till.IsCardDeclined(err) {
					return errCardDeclined
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
		invoiceID, invoiceNo, err := s.store.InsertInvoice(ctx, tx, invoiceRow{
			CustomerID:    customerID,
			OrderID:       orderID,
			BranchID:      in.BranchID,
			Status:        invStatus,
			DueDate:       dueDate,
			SubtotalCents: subtotal,
			TaxCents:      tax,
			TotalCents:    total,
			BalanceCents:  balance,
			CreatedBy:     actor,
		})
		if err != nil {
			return err
		}
		if err := s.store.InsertInvoiceLines(ctx, tx, invoiceID, lineIDs, lines); err != nil {
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
			running += total
			if err := s.store.InsertCustomerTxn(ctx, tx, customerTxnRow{
				CustomerID: customerID, TxnType: "INVOICE", AmountCents: total,
				BalanceAfter: running, SourceType: "invoice", SourceRefID: invoiceID, BranchID: in.BranchID,
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
			SplitTender: isSplit,
		})
		if err != nil {
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
			if _, err := s.store.InsertTaxSnapshot(ctx, tx, taxSnapshotRow{
				OrderID: orderID, TaxReasonCode: taxReason, RuleVersion: ruleVersionW4, RateBps: flatHSTRateBps,
				FullHSTCents: tax, NetCollectedCents: tax, Automated: false,
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
			"invoice_id": invoiceID, "customer_id": customerID, "total_cents": total, "due_date": dueDate,
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
			TaxReasonCode: taxReason, Status: "COMMITTED", SplitTender: isSplit,
			// DIB-488 rev 6: the item-D facts echoed back; item-F change due for the sold panel.
			POJobRef: poJobRef, SalespersonID: in.SalespersonID, SalespersonName: salespersonName,
			CashTenderedCents: in.CashTenderedCents,
			// DIB-488 rev 8 (item 11): the job facts echoed back for the sold panel.
			JobAddress: jobAddress, JobReference: jobReference,
		}
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
	if err != nil {
		return Sale{}, err
	}

	// Audit the completed sale post-commit (the Recorder owns the audit_log table; zero-trust).
	// DIB-488 rev 6 (item D): the PO / job reference and the credited salesperson ride the SAME
	// record's Extra, the order's audit/activity trail carries who sold it and against what
	// reference, per the existing Extra idiom (ActPaymentAllocated below). Absent when unused.
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
