// staff.go, the STAFF-facing quote service (DIB-56, bundle W10-A).
//
// Why this is a separate type rather than methods on Service. The pre-existing Service is the PORTAL
// side and is hard customer-scoped: every one of its methods takes a customerID and every SQL statement
// behind it carries `customer_id = $1`, including the FOR UPDATE lock in Convert. That scoping IS its
// tenancy guarantee. A counter user proves reach by BRANCH, not by owning the customer record, so the
// staff surface cannot reuse those methods, and hanging branch-scoped methods off the same struct would
// leave one type where half the methods enforce tenancy by customer and half by branch, which is exactly
// the shape that eventually gets called the wrong way. Two types, two scoping rules, no overlap.
//
// The split mirrors the order module's own division of labour (portal GetOrder is tenant-scoped by
// customer_id; the staff OrderRead proves reach by branch), it just makes it a type boundary here
// because, unlike order, this package started life portal-only.
package quote

import (
	"context"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pricing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
)

// staffStore is the persistence the staff surface needs. *Repository satisfies it in production; the
// unit tests use a fake that honours the same guards.
type staffStore interface {
	// ResolveQuoteID accepts a real id (UUID) or the visible quote_no ("#Q-4"/"Q-4"), branch-scoped.
	ResolveQuoteID(ctx context.Context, idOrNo string, global bool, branchIDs []string) (string, error)
	// QuoteEditHeader reads the four facts every edit guard needs, status, owning branch, the
	// validity window, and the quote's owner (the salesperson when one is set, else the creator),
	// in one query. Missing id → ErrNotFound (never an existence leak). A NULL valid_until returns
	// nil, which means NEVER EXPIRES (D2a), not "expired". An ownerless quote (no salesperson, no
	// creator) answers "".
	QuoteEditHeader(ctx context.Context, quoteID string) (status, branchID string, validUntil *time.Time, ownerID string, err error)
	// CreateQuote inserts a DRAFT quote and returns its id + minted quote_no.
	CreateQuote(ctx context.Context, in QuoteCreate) (id, quoteNo string, err error)
	// StaffDetail reads one quote plus its lines for the staff surface, from a single MVCC snapshot so
	// header money and Σ lines can never be torn against each other.
	StaffDetail(ctx context.Context, quoteID string) (StaffQuoteView, error)
	// ListStaffQuotes lists quotes the caller can reach, branch-scoped. A non-empty ownerID
	// narrows the list to that owner's quotes (a Work caller's default, plan 3.3).
	ListStaffQuotes(ctx context.Context, global bool, branchIDs []string, ownerID string, f Filter) ([]StaffQuoteSummary, error)
	// LineForEdit reads one line's current state. Missing line → ErrLineNotFound.
	LineForEdit(ctx context.Context, quoteID string, lineNo int) (snap QuoteLineSnapshot, uomEntered string, err error)
	// The guarded writes apply one mutation AND recompute the quote totals in ONE transaction under the
	// quote row lock, with the status guard and the expiry guard re-evaluated inside that lock, and the
	// audit trail folded into the same transaction (R3).
	UpdateLineGuarded(ctx context.Context, w quoteEditWrite, plan floorWritePlan, hooks quoteEditHooks) (before, after QuoteLineSnapshot, totals QuoteTotals, status string, err error)
	InsertLineGuarded(ctx context.Context, w quoteEditWrite, hooks quoteEditHooks) (after QuoteLineSnapshot, totals QuoteTotals, status string, err error)
	DeleteLineGuarded(ctx context.Context, quoteID string, lineNo int, allowedFrom []string, actorID string, hooks quoteEditHooks) (before QuoteLineSnapshot, totals QuoteTotals, status string, err error)
	UpdateValidUntilGuarded(ctx context.Context, quoteID, newDate string, allowedFrom []string, actorID string, hooks quoteDateHooks) (before, after, status string, err error)
	// QuotePricingContext resolves the customer + branch a quote line must be priced for. DIB-483: the
	// counter's convert door reads the SAME pair off it, rather than adding a third reader of the two
	// columns that decide whose quote this is.
	QuotePricingContext(ctx context.Context, quoteID string) (customerID, branchID string, err error)
	// DIB-483: THE conversion (convert.go). Embedded rather than re-declared so the counter door and
	// the contractor door provably reach one function, see convert_seam_test.go.
	convertStore
}

// txRecorder is the transactional half of the audit seam: write one row on the caller's transaction so
// it commits or rolls back with the caller's mutation (satisfied by *audit.Service.RecordTx).
type txRecorder interface {
	RecordTx(ctx context.Context, q audit.Execer, a audit.SensitiveAccess) error
}

// auditTrail is the read half, the per-entity trail the change-history endpoint projects. Satisfied by
// *audit.Service.ReopenTransaction, the same reader the order change history uses.
type auditTrail interface {
	ReopenTransaction(ctx context.Context, entityType, entityID string) ([]audit.Entry, error)
}

// StaffService is the staff quote builder: create, edit lines, set expiry, read the change trail.
type StaffService struct {
	store    staffStore
	shares   shareStore
	audit    audit.Recorder
	auditTx  txRecorder
	trail    auditTrail
	pricer   PriceResolver
	preview  PricePreviewer
	inv      CostBasisReader
	custType CustomerTypeResolver
	actors   ActorNamer
	// W5 seam-3 ports (stock_seam.go / reserve.go): stock raises + releases the quote's SOFT holds
	// inside each line write's own transaction; avail is the at-quote four-bucket read. Both nil until
	// main.go injects them, and a nil seam turns that half of the feature OFF rather than failing -
	// see StockSeamWired / AvailabilitySeamWired, which the surface renders off.
	stock StockReserver
	avail AvailabilityReader
	// holdExpiry is the DIB-428 seam (softhold_expiry_seam.go): W6-C's 'soft hold expired'
	// projection, read through a consumer-declared port. nil = the banner never appears and the
	// envelope says seam_wired=false, rather than a confident silence.
	holdExpiry SoftHoldExpiryReader

	// roles is the dealer's role configuration the quote rules read (plan
	// 3.3 and 3.5, the way the till's service takes it): the edit ladder's
	// quote desk levels, the cost and margin list (X3), the floor clearers
	// (X5) and the floor approvers (D11, CP0-1 answer B). Nil fails closed:
	// a service without a configuration refuses the gated paths, it never
	// answers them permissively.
	roles *roleconfig.Config
	// floor is the pricing owned floor decision seam (floor.go): the same
	// gate and floor source the resolver uses, asked by the two writes that
	// put a line below its rule price. Nil fails closed: without an arbiter
	// a below rule write is refused, never waved through.
	floor FloorArbiter
	// floorRail is the identity approvals rail as the quote module needs it
	// (plan 3.5: the quote module files the request; identity stores it,
	// decides it, and consumes it on the quote's own transaction). Nil
	// fails closed: no request is filed and no approval is consumed.
	floorRail FloorApprovalRail

	// callerFrom resolves the trusted, server-stamped acting staff member. Defaults to the identity
	// guard-stamped caller; tests override it via withCaller. Mirrors order.Service.callerFrom.
	callerFrom func(context.Context) (identity.StaffContext, bool)
	// nowFn is the clock the expiry guard reads. Injectable so an expiry test can pin "today" rather
	// than depending on the wall clock, a date-boundary test that only passes on some days is worse
	// than no test.
	nowFn func() time.Time
}

// NewStaffService builds the staff quote surface over the given store.
func NewStaffService(store staffStore) *StaffService {
	return &StaffService{
		store:      store,
		callerFrom: identity.StaffContextFromContext,
		nowFn:      time.Now,
	}
}

// WithAudit injects the audit recorder. When it also satisfies txRecorder (the real *audit.Service
// does), the edit path binds it as the TRANSACTIONAL recorder so the change trail commits or rolls back
// atomically with the mutation (R3). A fake that implements only Record leaves auditTx nil, and the
// record hook degrades to a no-op rather than failing the write.
func (s *StaffService) WithAudit(r audit.Recorder) *StaffService {
	s.audit = r
	if tx, ok := r.(txRecorder); ok {
		s.auditTx = tx
	}
	return s
}

// WithAuditTrail injects the per-entity audit reader the change-history endpoint projects.
func (s *StaffService) WithAuditTrail(t auditTrail) *StaffService { s.trail = t; return s }

// WithShares injects the DIB-57 share-link/acceptance persistence. Separate from the edit store so a
// deploy (or a test) can carry the builder without the sharing surface, and so the two sets of
// guarantees stay legible apart.
func (s *StaffService) WithShares(sh shareStore) *StaffService { s.shares = sh; return s }

// SharingWired reports whether the share-link surface is available.
func (s *StaffService) SharingWired() bool { return s.shares != nil }

// WithPricing injects the today's-rates pricing seam (main.go wires *pricing.Service).
func (s *StaffService) WithPricing(p PriceResolver) *StaffService { s.pricer = p; return s }

// WithCostBasis injects the cost-basis reader (main.go wires *inventory.Service).
func (s *StaffService) WithCostBasis(r CostBasisReader) *StaffService { s.inv = r; return s }

// WithCustomerType injects the pricing-tier seam used when pricing a quote line.
func (s *StaffService) WithCustomerType(r CustomerTypeResolver) *StaffService {
	s.custType = r
	return s
}

// WithActorNamer injects the staff display-name resolver used to put a human name on a change row.
func (s *StaffService) WithActorNamer(a ActorNamer) *StaffService { s.actors = a; return s }

// WithRoleConfig injects the dealer's role configuration the quote rules
// read (the till's WithRoleConfig posture): the edit ladder's levels, the
// cost and margin list, the floor clearers and the floor approvers. A nil
// configuration is refused at boot by the composition root; a service built
// without one fails the gated paths closed.
func (s *StaffService) WithRoleConfig(c *roleconfig.Config) *StaffService { s.roles = c; return s }

// WithFloorArbiter injects the pricing owned floor decision seam (floor.go).
func (s *StaffService) WithFloorArbiter(a FloorArbiter) *StaffService { s.floor = a; return s }

// WithFloorRail injects the identity approvals rail the quote floor flows
// through (filing, state, consumption on the quote's own transaction).
func (s *StaffService) WithFloorRail(r FloorApprovalRail) *StaffService { s.floorRail = r; return s }

// withCaller overrides the caller seam (tests only).
func (s *StaffService) withCaller(f func(context.Context) (identity.StaffContext, bool)) *StaffService {
	s.callerFrom = f
	return s
}

// withNow pins the expiry clock (tests only).
func (s *StaffService) withNow(f func() time.Time) *StaffService { s.nowFn = f; return s }

// QuoteEditSeamsWired reports whether everything a quote edit needs is present: the pricing resolver,
// the cost-basis reader and the customer-tier resolver. main.go asserts this at boot (fail fast) so a
// dropped wiring cannot be discovered mid-quote at the counter.
func (s *StaffService) QuoteEditSeamsWired() bool {
	return s.pricer != nil && s.inv != nil && s.custType != nil
}

func (s *StaffService) now() time.Time {
	if s.nowFn == nil {
		return time.Now()
	}
	return s.nowFn()
}

func (s *StaffService) caller(ctx context.Context) (identity.StaffContext, error) {
	if s.callerFrom == nil {
		return identity.StaffContext{}, ErrUnauthorized
	}
	c, ok := s.callerFrom(ctx)
	if !ok || c.UserID == "" {
		return identity.StaffContext{}, ErrUnauthorized
	}
	return c, nil
}

// ActorNamer resolves staff display names for the change trail, so a history row reads a person with a role
// rather than a UUID. A failure here degrades to the raw id, it must never fail the history read.
// Satisfied by *Repository.ActorNames.
type ActorNamer interface {
	ActorNames(ctx context.Context, ids []string) (map[string]string, error)
}

// - read models -

// StaffQuoteSummary is one row of the staff quote list.
type StaffQuoteSummary struct {
	ID            string    `json:"id"`
	QuoteNo       string    `json:"quote_no"`
	CustomerID    string    `json:"customer_id"`
	CustomerName  string    `json:"customer_name"`
	BranchID      string    `json:"branch_id"`
	Status        string    `json:"status"`
	ValidUntil    string    `json:"valid_until,omitempty"` // "" = never expires (D2a)
	Expired       bool      `json:"expired"`               // computed, never stored (D2)
	SubtotalCents int64     `json:"subtotal_cents"`
	TaxCents      int64     `json:"tax_cents"`
	TotalCents    int64     `json:"total_cents"`
	UpdatedAt     time.Time `json:"updated_at"`
	// OwnerID is the quote's owner (plan 3.3: the salesperson when one is
	// set, else the creator), so the list can say whose quote a row is.
	OwnerID   string `json:"owner_id,omitempty"`
	OwnerName string `json:"owner_name,omitempty"`

	// cursorKey is the staff list's stable-ordering key (created_at /
	// quote_no), set on scan and read by the handler to mint next_cursor
	// (HH-3.2 B4); never serialized.
	cursorKey string
}

// StaffQuoteView is one quote as the staff builder renders it: the header, the lines, the money, and
// the server-computed edit offer.
type StaffQuoteView struct {
	ID           string `json:"id"`
	QuoteNo      string `json:"quote_no"`
	CustomerID   string `json:"customer_id"`
	CustomerName string `json:"customer_name"`
	BranchID     string `json:"branch_id"`
	Status       string `json:"status"`
	// ValidUntil is "" when the column is NULL, which means the quote NEVER EXPIRES (D2a). The client
	// must render that as "no expiry", never as "expired".
	ValidUntil string `json:"valid_until,omitempty"`
	// Expired is computed from ValidUntil at read time (D2). No EXPIRED status is ever written.
	Expired bool `json:"expired"`
	// EditableFields is which fields THIS caller may edit on this quote right now, computed server-side
	// from the same quoteEditRules the write path enforces. The client evaluates nothing; an ABSENT
	// field list must be read as "nothing is editable", never as "everything is".
	EditableFields []string `json:"editable_fields"`
	// OwnerID is the quote's owner (plan 3.3: the salesperson when one is
	// set, else the creator), so a reader can say whose quote this is.
	OwnerID   string `json:"owner_id,omitempty"`
	OwnerName string `json:"owner_name,omitempty"`
	// CanConvert is whether THIS caller may convert THIS quote to an order right now, DIB-483's
	// counter door, computed server-side from the same two facts the write path enforces (the quote is
	// ACCEPTED, and the caller holds a selling role). The client evaluates nothing, exactly as it
	// evaluates nothing for EditableFields: an authority model mirrored into a browser is an authority
	// model with two versions of itself.
	//
	// An ABSENT field must be read as "no", never as "yes".
	CanConvert    bool            `json:"can_convert"`
	Lines         []QuoteViewLine `json:"lines"`
	SubtotalCents int64           `json:"subtotal_cents"`
	TaxCents      int64           `json:"tax_cents"`
	TotalCents    int64           `json:"total_cents"`
	UpdatedAt     time.Time       `json:"updated_at"`

	// validUntil is the parsed form the service uses to compute Expired/EditableFields. Unexported -
	// it never crosses the wire.
	validUntil *time.Time
}

// QuoteCreate is the staff create payload.
type QuoteCreate struct {
	CustomerID string
	BranchID   string
	ValidUntil string // "" = never expires
	CreatedBy  string
}

// - reads -

// StaffQuoteRead returns one quote for the staff surface, with the server-computed edit offer attached.
func (s *StaffService) StaffQuoteRead(ctx context.Context, quoteID string) (StaffQuoteView, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return StaffQuoteView{}, err
	}
	if caller.Status != identity.StatusActive {
		return StaffQuoteView{}, ErrForbidden
	}
	rGlobal, rBranches := callerBranchScope(caller)
	quoteID, err = s.store.ResolveQuoteID(ctx, quoteID, rGlobal, rBranches)
	if err != nil {
		return StaffQuoteView{}, err
	}
	view, err := s.store.StaffDetail(ctx, quoteID)
	if err != nil {
		return StaffQuoteView{}, err
	}
	if !caller.CanAccessBranch(view.BranchID) {
		return StaffQuoteView{}, ErrForbidden
	}
	view.Expired = quoteExpired(view.validUntil, s.now())
	// Plan 3.3: a Work caller still READS any quote in its branch, but the
	// offer is empty on a quote it does not own (the write would refuse it,
	// and a control that appears must be a control that works).
	view.EditableFields = s.editableFieldsFor(view.Status, view.validUntil, s.ownsQuote(caller, view.OwnerID))
	view.OwnerName = s.ownerName(ctx, view.OwnerID)
	// Per line, for callers on the cost and margin list (X3): whether the
	// rule price itself sits below the floor (CP0-3's mark), and who cleared
	// a below floor price. A caller off the list learns neither: the floor
	// figures are cost information.
	if s.seesCostAndMargin(caller) {
		s.markLineFloors(ctx, &view)
	}
	// DIB-483: the convert OFFER. Deliberately NOT "is the caller a seller" alone, the offer is the
	// conjunction the write path enforces, so a button that appears is a button that works. Expiry is
	// not consulted: converting an ACCEPTED quote is closing a deal the customer already agreed to,
	// and Repository.Convert (the authority) does not refuse on expiry either. Two opinions about that
	// would mean an offered button the server declines.
	// DIB-491: a CONVERTED quote earns the offer again when the dealer allows repeat acceptance -
	// the write path (Repository.Convert) makes exactly this pair of checks under the row lock, so
	// the offer stays "a button that appears is a button that works". multiAcceptOn fails
	// restrictive, matching the write path's default.
	view.CanConvert = (view.Status == "ACCEPTED" ||
		(view.Status == "CONVERTED" && s.multiAcceptOn(ctx))) &&
		identity.CanSellOnCredit(caller)
	return view, nil
}

// StaffQuoteList lists the quotes this caller can reach. A Work caller's
// list defaults to its own quotes (plan 3.3, r3's Quote Desk column); a
// Manage caller reaches every quote in its branches.
func (s *StaffService) StaffQuoteList(ctx context.Context, f Filter) ([]StaffQuoteSummary, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return nil, err
	}
	if caller.Status != identity.StatusActive {
		return nil, ErrForbidden
	}
	if f.Status != "" && !validStatus(f.Status) {
		return nil, httpx.BadRequestf("unknown quote status %q", f.Status)
	}
	rGlobal, rBranches := callerBranchScope(caller)
	owner := ""
	if s.quoteDeskLevel(caller) == roleconfig.LevelWork {
		owner = caller.UserID
	}
	rows, err := s.store.ListStaffQuotes(ctx, rGlobal, rBranches, owner, f)
	if err != nil {
		return nil, err
	}
	now := s.now()
	ids := make([]string, 0, len(rows))
	for i := range rows {
		rows[i].Expired = rowExpired(rows[i].ValidUntil, now)
		if rows[i].OwnerID != "" {
			ids = append(ids, rows[i].OwnerID)
		}
	}
	names := s.actorNames(ctx, ids)
	for i := range rows {
		rows[i].OwnerName = names[rows[i].OwnerID]
	}
	return rows, nil
}

// ownerName resolves one owner's display name, degrading to "" (the raw id
// still rides the row) when no namer is wired or the owner is unknown.
func (s *StaffService) ownerName(ctx context.Context, ownerID string) string {
	if ownerID == "" {
		return ""
	}
	return s.actorNames(ctx, []string{ownerID})[ownerID]
}

// actorNames resolves display names through the ActorNamer seam, degrading
// to an empty map: a name is presentation, never a fact the read depends on.
func (s *StaffService) actorNames(ctx context.Context, ids []string) map[string]string {
	if s.actors == nil || len(ids) == 0 {
		return map[string]string{}
	}
	names, err := s.actors.ActorNames(ctx, ids)
	if err != nil {
		return map[string]string{}
	}
	return names
}

// markLineFloors sets the per line floor marks on a staff read for callers
// on the cost and margin list: the rule price below floor flag (asked of the
// arbiter with the candidate AT the rule price, so the answer is the rule
// price's own margin against the floor) and the stamped clearing's who.
// Degrades to absent marks: a read never fails on the floor question.
func (s *StaffService) markLineFloors(ctx context.Context, view *StaffQuoteView) {
	if s.floor == nil || s.inv == nil || s.custType == nil {
		return
	}
	fs, ok := s.floorStore()
	if !ok {
		return
	}
	custType, err := s.custType.CustomerType(ctx, view.CustomerID)
	if err != nil {
		return
	}
	for i := range view.Lines {
		ln := &view.Lines[i]
		if ln.ProductID == "" || ln.UnitPriceCents <= 0 {
			continue
		}
		if _, _, _, _, clearing, err := fs.LineFloorState(ctx, view.ID, ln.LineNo); err == nil && clearing != nil {
			ln.FloorClearedBy = clearing.By
		}
		qty, err := parseQuoteQty(ln.QtyEntered)
		if err != nil {
			continue
		}
		cost, _, err := s.inv.CurrentCostCents(ctx, ln.ProductID, view.BranchID)
		if err != nil {
			continue
		}
		ans, err := s.floor.EvaluateLineFloor(ctx, pricing.FloorQuestion{
			CustomerID: view.CustomerID, CustomerType: custType,
			ProductID: ln.ProductID, BranchID: view.BranchID,
			QtyEntered: qty, UOMEntered: ln.UOMEntered, CostCents: cost,
			RuleUnitCents: ln.UnitPriceCents, CandidateUnitCents: ln.UnitPriceCents,
			PriceUOM: ln.PriceUOM,
		})
		if err != nil {
			continue
		}
		below := ans.BelowFloor
		ln.RulePriceBelowFloor = &below
	}
}

// rowExpired computes the list-view expiry flag from the wire-shaped date string. "" (NULL in the
// column) means never expires (D2a).
func rowExpired(validUntil string, now time.Time) bool {
	if validUntil == "" {
		return false
	}
	t, err := time.Parse(dateLayout, validUntil)
	if err != nil {
		return false // an unparseable stored date is not grounds to freeze the quote
	}
	return quoteExpired(&t, now)
}
