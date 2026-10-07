package quote

// builder.go, DIB-506: the Quote Builder work area's service layer, and the DIB-507 settings-kit
// pilot consumption (D-105 hybrid).
//
//   QUOTE POLICY (typed half)   quote_policy (0173) on the 0043 branch-NULL shape: the NULL row is
//                               the dealer default, a branch row overrides. Validated 422-in-words
//                               through the SHARED kit validators (internal/settings) BEFORE any
//                               CHECK could 500. The PUT applies the Tier-1 GLOBAL admin gate IN
//                               THE SERVICE (settingsAdmin, the acceptcontrols precedent).
//
//   COST/MARGIN GLANCE          computed SERVER-SIDE from the quote's own engine-priced lines
//                               (revenue = the stored line_total_cents the pricing seam wrote;
//                               cost = the per-branch WAC row product_branch_costs, THE engine's
//                               cost source, the same row pos/rev8.go reads) and gated at the
//                               MARSHAL layer by quote_policy.cost_visibility: all-omitempty
//                               response, a non-allowed caller receives `{}` with every cost field
//                               BYTE-ABSENT, fail closed (unreadable policy or anonymous caller ⇒
//                               no). D-010: the client NEVER computes a margin.
//
//   TERMS                       the concept's terms strip + summary fields (0173 columns on
//                               quotes), written under the quote row lock with the edit matrix's
//                               DRAFT/SENT statuses re-checked inside the transaction.
//
//   TAX ON THE TERMS WRITE      the one deliberate extension of quote money: the terms write
//                               stamps tax_cents = flat HST over (subtotal + delivery fee), the
//                               SAME arithmetic as billing/pos FlatHSTAssessor ((amt*bps+5000)/
//                               10000, half-up once at the header), rate from system_settings
//                               hst_rate_bps (fallback 1300). The shipped line-write recompute
//                               still never writes tax; the work area refreshes terms after cart
//                               changes so the stamped tax tracks the lines. Stated in NOTES.

import (
	"context"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/settings"
)

// - the builder store (type-asserted so the shipped staffStore fakes need no new methods) -

type builderStore interface {
	ReadQuotePolicy(ctx context.Context, branchID string) (QuotePolicy, error)
	UpsertQuotePolicy(ctx context.Context, p QuotePolicy) error
	ReadQuoteTerms(ctx context.Context, quoteID string) (QuoteTerms, error)
	UpdateTermsGuarded(ctx context.Context, quoteID string, in TermsInput, allowedFrom []string, actorID string, hstRateBps int64) (QuoteTerms, error)
	UpdateLineDiscountGuarded(ctx context.Context, quoteID string, lineNo, discPct int, allowedFrom []string, actorID string, plan floorWritePlan, hooks quoteDiscountHooks) (QuoteEditResult, error)
	ReadQuoteMarginRows(ctx context.Context, quoteID string) (branchID string, rows []quoteMarginRow, err error)
	ReadHSTRateBps(ctx context.Context) int64
	TopCatalogueApplications(ctx context.Context, limit int) ([]string, error)
}

func (s *StaffService) builder() (builderStore, bool) {
	b, ok := s.store.(builderStore)
	return b, ok
}

// ErrBuilderNotConfigured, the store behind this service does not carry the 0173 surface.
var errBuilderNotConfigured = settings.Errf(http.StatusServiceUnavailable,
	"quote: the builder settings surface is not configured on this server")

// - quote policy (the typed half of the hybrid) -

// QuotePolicy is the quotes.builder dealer policy row (0173), resolved branch-over-NULL.
type QuotePolicy struct {
	BranchID             string   `json:"branch_id,omitempty"`
	CostVisibility       string   `json:"cost_visibility"`
	DefaultValidityDays  int      `json:"default_validity_days"`
	DefaultDepositPct    int      `json:"default_deposit_pct"`
	LineDiscountsEnabled bool     `json:"line_discounts_enabled"`
	SendEmailEnabled     bool     `json:"send_email_enabled"`
	SendSMSEnabled       bool     `json:"send_sms_enabled"`
	IncludeProductImages bool     `json:"include_product_images"`
	QuickChipSet         []string `json:"quick_chip_set"`
	// EffectiveChips is what the chip row renders: the curated set when the dealer authored one,
	// else an honest data-driven derivation (top catalogue applications, the 0170 posture, scoped
	// to what this pilot can answer without a sales-analysis read). Read-only; never written.
	EffectiveChips       []string `json:"effective_chips,omitempty"`
	WorkspaceSplitPct    int      `json:"workspace_split_pct"`
	WorkspaceArrangement string   `json:"workspace_arrangement"`
	// QuantityNotice is the dealer's notice that the quantities are the customer's to confirm, carried
	// on the customer's page, the printed page and the staff quote. Blank on a write means the default.
	QuantityNotice string `json:"quantity_notice"`
}

// DefaultQuantityNotice is the house wording, the column default of quote_policy.quantity_notice (0366).
const DefaultQuantityNotice = "All quantities are to be confirmed by the customer. Please review them before you accept it."

// maxQuantityNotice bounds the notice; it matches the column's CHECK so the service refuses in words
// before the CHECK could 500.
const maxQuantityNotice = 500

// QuotePolicyFor resolves the effective policy for a branch (staff read; the knobs are dealer
// configuration, not gated data, the DATA the cost knob governs is gated at the glance).
func (s *StaffService) QuotePolicyFor(ctx context.Context, branchID string) (QuotePolicy, error) {
	if _, err := s.caller(ctx); err != nil {
		return QuotePolicy{}, err
	}
	b, ok := s.builder()
	if !ok {
		return QuotePolicy{}, errBuilderNotConfigured
	}
	p, err := b.ReadQuotePolicy(ctx, branchID)
	if err != nil {
		return QuotePolicy{}, err
	}
	p.EffectiveChips = p.QuickChipSet
	if len(p.EffectiveChips) == 0 {
		if derived, derr := b.TopCatalogueApplications(ctx, 6); derr == nil {
			p.EffectiveChips = derived
		}
		// A failed derivation leaves the chips absent, the row renders Favorites alone
		// (empty-never-error).
	}
	return p, nil
}

// validateQuotePolicy normalizes/refuses every knob in words through the shared kit validators -
// the D-105 posture: one validation vocabulary for every module policy.
func validateQuotePolicy(p QuotePolicy) (QuotePolicy, error) {
	vis, err := settings.ValidateVisibility("quote", p.CostVisibility)
	if err != nil {
		return p, err
	}
	p.CostVisibility = vis
	if p.DefaultValidityDays == 0 {
		p.DefaultValidityDays = 30
	}
	if err := settings.ValidateIntRange("quote", "default_validity_days", p.DefaultValidityDays, 1, 365); err != nil {
		return p, err
	}
	if err := settings.ValidateIntRange("quote", "default_deposit_pct", p.DefaultDepositPct, 0, 100); err != nil {
		return p, err
	}
	chips, err := settings.ValidateChipSet("quote", p.QuickChipSet)
	if err != nil {
		return p, err
	}
	p.QuickChipSet = chips
	split, arr, err := settings.ValidateGeometry("quote", p.WorkspaceSplitPct, p.WorkspaceArrangement, settings.SplitDefault)
	if err != nil {
		return p, err
	}
	p.WorkspaceSplitPct, p.WorkspaceArrangement = split, arr
	p.QuantityNotice = strings.TrimSpace(p.QuantityNotice)
	if p.QuantityNotice == "" {
		p.QuantityNotice = DefaultQuantityNotice
	}
	if utf8.RuneCountInString(p.QuantityNotice) > maxQuantityNotice {
		return p, httpx.BadRequestf("the quantity notice is too long, keep it to %d characters", maxQuantityNotice)
	}
	return p, nil
}

// UpdateQuotePolicy writes the dealer (or branch) policy row under the
// settings' role rule (CP0-5 answer A, defect D9): the Sales manager
// changes the Quote Desk's settings for its own branch, the General
// Manager sets the dealer wide default. A branch row (BranchID set) is a
// Manage-on-quote-desk caller's write, in a branch it reaches; the NULL
// dealer default row is the General Manager's alone. Every other role is
// refused with the typed settings refusal.
func (s *StaffService) UpdateQuotePolicy(ctx context.Context, p QuotePolicy) (QuotePolicy, error) {
	caller, err := s.settingsWriter(ctx, p.BranchID)
	if err != nil {
		return QuotePolicy{}, err
	}
	_ = caller
	b, ok := s.builder()
	if !ok {
		return QuotePolicy{}, errBuilderNotConfigured
	}
	p, err = validateQuotePolicy(p)
	if err != nil {
		return QuotePolicy{}, err
	}
	if err := b.UpsertQuotePolicy(ctx, p); err != nil {
		return QuotePolicy{}, err
	}
	return s.QuotePolicyFor(ctx, p.BranchID)
}

// - the role-gated cost/margin glance -

// quoteMarginRow is one line's raw margin inputs as the store reads them.
type quoteMarginRow struct {
	LineNo         int
	ProductID      string
	QtyBase        float64
	LineTotalCents int64
	WACCents       int64
	HasCost        bool
}

// QuoteMarginLine is one line's answer. Every field is omitempty: an unanswerable line contributes
// an entry whose cost fields are BYTE-ABSENT (never a fake 100% margin).
type QuoteMarginLine struct {
	LineNo         int    `json:"line_no,omitempty"`
	CostCents      *int64 `json:"cost_cents,omitempty"`
	MarginBps      *int64 `json:"margin_bps,omitempty"`
	EstProfitCents *int64 `json:"est_profit_cents,omitempty"`
}

// QuoteMargin is the glance answer. THE MARSHAL-LAYER GATE: all-omitempty, a caller the dealer's
// cost_visibility does not allow receives the zero value, i.e. a `{}` payload with every cost
// field byte-absent (the pos/rev8.go idiom re-consumed; dib506_test.go asserts byte-absence).
type QuoteMargin struct {
	Lines          []QuoteMarginLine `json:"lines,omitempty"`
	MarginBps      *int64            `json:"margin_bps,omitempty"`
	EstProfitCents *int64            `json:"est_profit_cents,omitempty"`
	Partial        bool              `json:"partial,omitempty"`
}

// costAllowed answers whether THIS caller may receive cost/margin. The
// rule is the role configuration's cost_visibility list (X3, r3: Inside
// Sales, Outside Sales and every manager and leadership role except HR),
// the same reader the till's glance uses; the branch policy's
// MANAGER_UP-tier default no longer governs (defect D8). Fail closed: an
// unwired configuration or an anonymous caller receives nothing.
func (s *StaffService) costAllowed(ctx context.Context, branchID string) bool {
	caller, err := s.caller(ctx)
	if err != nil {
		return false
	}
	return s.seesCostAndMargin(caller)
}

// quoteMarginBps computes gross margin in bps from integer cents (pos.marginBpsOf's arithmetic,
// kept local so a display glance never imports the terminal).
func quoteMarginBps(revenueCents, costCents int64) int64 {
	if revenueCents <= 0 {
		return 0
	}
	return ((revenueCents - costCents) * 10000) / revenueCents
}

// MarginGlance answers the open quote's per-line cost/margin + the footer pair, role-gated at the
// marshal layer. Display-only: it never writes and never blocks. Revenue is the STORED
// line_total_cents (the pricing seam's own figure, net of any line discount, margin is spoken
// after the discount, which is the number the dealer is actually giving up).
func (s *StaffService) MarginGlance(ctx context.Context, quoteID string) (QuoteMargin, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return QuoteMargin{}, err
	}
	b, ok := s.builder()
	if !ok {
		return QuoteMargin{}, errBuilderNotConfigured
	}
	rGlobal, rBranches := callerBranchScope(caller)
	quoteID, err = s.store.ResolveQuoteID(ctx, quoteID, rGlobal, rBranches)
	if err != nil {
		return QuoteMargin{}, err
	}
	branchID, rows, err := b.ReadQuoteMarginRows(ctx, quoteID)
	if err != nil {
		return QuoteMargin{}, err
	}
	if !caller.CanAccessBranch(branchID) {
		return QuoteMargin{}, ErrForbidden
	}
	if !s.costAllowed(ctx, branchID) {
		// THE GATE: the zero value marshals as `{}`, cost/margin BYTE-ABSENT for this caller.
		return QuoteMargin{}, nil
	}
	if len(rows) == 0 {
		return QuoteMargin{}, nil
	}
	out := QuoteMargin{Lines: make([]QuoteMarginLine, len(rows))}
	var revSum, costSum int64
	known := 0
	for i, row := range rows {
		out.Lines[i].LineNo = row.LineNo
		if !row.HasCost || row.ProductID == "" || row.QtyBase <= 0 || row.LineTotalCents <= 0 {
			out.Partial = true
			continue // absent, no cost on file is not a 100% margin
		}
		lineCost := int64(float64(row.WACCents)*row.QtyBase + 0.5)
		m := quoteMarginBps(row.LineTotalCents, lineCost)
		p := row.LineTotalCents - lineCost
		c := lineCost
		out.Lines[i].CostCents = &c
		out.Lines[i].MarginBps = &m
		out.Lines[i].EstProfitCents = &p
		revSum += row.LineTotalCents
		costSum += lineCost
		known++
	}
	if known > 0 {
		fm := quoteMarginBps(revSum, costSum)
		fp := revSum - costSum
		out.MarginBps = &fm
		out.EstProfitCents = &fp
	} else {
		out.Lines = nil
	}
	return out, nil
}

// - terms -

// QuoteTerms is the terms + summary read: everything the right panel edits, plus per-line extras
// the concept's line table renders (disc %, gross totals, thumbnails).
type QuoteTerms struct {
	QuoteID          string      `json:"quote_id"`
	DepositRequired  bool        `json:"deposit_required"`
	DepositPct       int         `json:"deposit_pct"`
	IncludeImages    bool        `json:"include_images"`
	CustomerNotes    string      `json:"customer_notes"`
	ContactName      string      `json:"contact_name"`
	ProjectJob       string      `json:"project_job"`
	CustomerPO       string      `json:"customer_po"`
	SalespersonID    string      `json:"salesperson_id,omitempty"`
	SalespersonName  string      `json:"salesperson_name,omitempty"`
	Fulfillment      string      `json:"fulfillment"`
	DeliveryAddress  string      `json:"delivery_address"`
	RequestedDate    string      `json:"requested_date,omitempty"`
	DeliveryFeeCents int64       `json:"delivery_fee_cents"`
	DepositCents     int64       `json:"deposit_cents"`
	SubtotalCents    int64       `json:"subtotal_cents"`
	TaxCents         int64       `json:"tax_cents"`
	TotalCents       int64       `json:"total_cents"`
	Lines            []TermsLine `json:"lines"`
}

// TermsLine is one line's builder extras, keyed by line_no.
type TermsLine struct {
	LineNo                int    `json:"line_no"`
	DiscPct               int    `json:"disc_pct"`
	PreDiscountTotalCents int64  `json:"pre_discount_total_cents"`
	HasImage              bool   `json:"has_image"`
	SKU                   string `json:"sku,omitempty"`
}

// TermsInput is the PUT body. Pointer-free: the work area always sends the whole strip (it owns
// the whole strip), and an omitted string is an empty string by the same rule the columns default.
type TermsInput struct {
	DepositRequired  bool   `json:"deposit_required"`
	DepositPct       int    `json:"deposit_pct"`
	IncludeImages    bool   `json:"include_images"`
	CustomerNotes    string `json:"customer_notes"`
	ContactName      string `json:"contact_name"`
	ProjectJob       string `json:"project_job"`
	CustomerPO       string `json:"customer_po"`
	SalespersonID    string `json:"salesperson_id"`
	Fulfillment      string `json:"fulfillment"`
	DeliveryAddress  string `json:"delivery_address"`
	RequestedDate    string `json:"requested_date"`
	DeliveryFeeCents int64  `json:"delivery_fee_cents"`
}

// validateTerms refuses every out-of-bounds field in words, BEFORE any CHECK could 500.
func validateTerms(in TermsInput) (TermsInput, error) {
	in.CustomerNotes = strings.TrimSpace(in.CustomerNotes)
	in.ContactName = strings.TrimSpace(in.ContactName)
	in.ProjectJob = strings.TrimSpace(in.ProjectJob)
	in.CustomerPO = strings.TrimSpace(in.CustomerPO)
	in.DeliveryAddress = strings.TrimSpace(in.DeliveryAddress)
	in.SalespersonID = strings.TrimSpace(in.SalespersonID)
	if err := settings.ValidateIntRange("quote", "deposit_pct", in.DepositPct, 0, 100); err != nil {
		return in, err
	}
	if len(in.CustomerNotes) > 500 {
		return in, settings.Errf(http.StatusUnprocessableEntity, "quote: customer_notes exceeds the 500 character maximum")
	}
	if len(in.ContactName) > 120 {
		return in, settings.Errf(http.StatusUnprocessableEntity, "quote: contact_name exceeds the 120 character maximum")
	}
	if len(in.ProjectJob) > 64 {
		return in, settings.Errf(http.StatusUnprocessableEntity, "quote: project_job exceeds the 64 character maximum")
	}
	if len(in.CustomerPO) > 64 {
		return in, settings.Errf(http.StatusUnprocessableEntity, "quote: customer_po exceeds the 64 character maximum")
	}
	if len(in.DeliveryAddress) > 200 {
		return in, settings.Errf(http.StatusUnprocessableEntity, "quote: delivery_address exceeds the 200 character maximum")
	}
	switch in.Fulfillment {
	case "":
		in.Fulfillment = "PICKUP"
	case "PICKUP", "DELIVERY":
	default:
		return in, settings.Errf(http.StatusUnprocessableEntity, "quote: fulfillment must be PICKUP or DELIVERY")
	}
	if in.SalespersonID != "" && !looksLikeUUID(in.SalespersonID) {
		return in, settings.Errf(http.StatusUnprocessableEntity,
			"quote: salesperson_id must be a staff member's id, pick one from the list")
	}
	if in.DeliveryFeeCents < 0 {
		return in, settings.Errf(http.StatusUnprocessableEntity, "quote: delivery_fee_cents cannot be negative")
	}
	if in.RequestedDate != "" {
		if _, err := time.Parse(dateLayout, in.RequestedDate); err != nil {
			return in, settings.Errf(http.StatusUnprocessableEntity, "quote: requested_date must be YYYY-MM-DD")
		}
	}
	if in.Fulfillment == "PICKUP" {
		// A pickup quote carries no delivery facts, clearing them here keeps the printed quote
		// honest when someone flips the tile back after typing an address.
		in.DeliveryAddress, in.RequestedDate, in.DeliveryFeeCents = "", "", 0
	}
	return in, nil
}

// Terms reads the terms + builder extras for one quote.
func (s *StaffService) Terms(ctx context.Context, quoteID string) (QuoteTerms, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return QuoteTerms{}, err
	}
	b, ok := s.builder()
	if !ok {
		return QuoteTerms{}, errBuilderNotConfigured
	}
	rGlobal, rBranches := callerBranchScope(caller)
	quoteID, err = s.store.ResolveQuoteID(ctx, quoteID, rGlobal, rBranches)
	if err != nil {
		return QuoteTerms{}, err
	}
	_, branchID, _, _, err := s.store.QuoteEditHeader(ctx, quoteID)
	if err != nil {
		return QuoteTerms{}, err
	}
	if !caller.CanAccessBranch(branchID) {
		return QuoteTerms{}, ErrForbidden
	}
	return b.ReadQuoteTerms(ctx, quoteID)
}

// UpdateTerms writes the terms strip under the quote row lock. Authorization rides the ratified
// edit matrix's header row (valid_until's DRAFT/SENT statuses, terms are quote-header facts of
// exactly that class), re-checked inside the transaction like every other quote write.
func (s *StaffService) UpdateTerms(ctx context.Context, quoteID string, in TermsInput) (QuoteTerms, error) {
	caller, rule, quoteID, err := s.authorizeQuoteEdit(ctx, quoteID, FieldQuoteValidUntil)
	if err != nil {
		return QuoteTerms{}, err
	}
	b, ok := s.builder()
	if !ok {
		return QuoteTerms{}, errBuilderNotConfigured
	}
	in, err = validateTerms(in)
	if err != nil {
		return QuoteTerms{}, err
	}
	return b.UpdateTermsGuarded(ctx, quoteID, in, rule.fromList(), caller.UserID, b.ReadHSTRateBps(ctx))
}

// - per-line discount -

// SetLineDiscount writes one line's discount percent. Server-gated by the dealer's
// line_discounts_enabled knob (curation hides the input; THIS refusal is the authority), and
// authorized on the line-edit matrix row. A discount is a below-rule write, so it runs the
// floor decision of plan 3.3 (floor.go): below the rule price and above the floor it is
// accepted with no manager; below the floor it is refused with the typed 409 unless the
// caller clears the floor itself or an approved and unused request is consumed by this write.
func (s *StaffService) SetLineDiscount(ctx context.Context, quoteID string, lineNo, discPct int) (QuoteEditResult, error) {
	caller, rule, quoteID, err := s.authorizeQuoteEdit(ctx, quoteID, FieldQuoteLineQty)
	if err != nil {
		return QuoteEditResult{}, err
	}
	b, ok := s.builder()
	if !ok {
		return QuoteEditResult{}, errBuilderNotConfigured
	}
	if discPct < 0 || discPct > 100 {
		return QuoteEditResult{}, settings.Errf(http.StatusUnprocessableEntity,
			"quote: disc_pct must be between 0 and 100")
	}
	_, branchID, _, _, err := s.store.QuoteEditHeader(ctx, quoteID)
	if err != nil {
		return QuoteEditResult{}, err
	}
	if p, perr := b.ReadQuotePolicy(ctx, branchID); perr == nil && !p.LineDiscountsEnabled && discPct != 0 {
		return QuoteEditResult{}, settings.Errf(http.StatusUnprocessableEntity,
			"quote: line discounts are switched off for this dealer, ask an administrator to enable them in the quote settings")
	}
	// The floor decision (a discount is a below-rule write). discPct 0 IS
	// the rule price and is never asked, so the floor seams are consulted
	// only by a real discount.
	var out *floorOutcome
	var productID string
	plan := floorWritePlan{}
	if discPct > 0 {
		fs, ok := s.floorStore()
		if !ok {
			return QuoteEditResult{}, ErrEditNotConfigured
		}
		lineID, _, ruleUnit, gross, priceUOM, clearing, ferr := fs.LineFloorState(ctx, quoteID, lineNo)
		if ferr != nil {
			return QuoteEditResult{}, ferr
		}
		snap, _, lerr := s.store.LineForEdit(ctx, quoteID, lineNo)
		if lerr != nil {
			return QuoteEditResult{}, lerr
		}
		productID = snap.ProductID
		out, err = s.decideLineFloor(ctx, caller, quoteID, branchID, snap.ProductID, lineNo, discPct, lineID, "",
			ruleUnit, gross, priceUOM, clearing)
		if err != nil {
			return QuoteEditResult{}, err
		}
		if out.refusal != nil {
			return QuoteEditResult{}, out.refusal
		}
		plan = out.plan
	}
	res, err := b.UpdateLineDiscountGuarded(ctx, quoteID, lineNo, discPct, rule.fromList(), caller.UserID, plan, quoteDiscountHooks{
		record: s.discountAuditHook(caller.UserID, quoteID),
		// The BYPASS or UPDATE row the decision owes rides the write's own
		// transaction (plan 4.2): a commit with no log row cannot happen.
		pricing: s.pricingLogHook(floorLogRows(caller, quoteID, productID, discPct, out)),
	})
	if err != nil {
		return QuoteEditResult{}, nameQuoteField(err, FieldQuoteLineQty)
	}
	return res, nil
}

// - the settings-kit surface hooks (what the quote module contributes to DIB-507) -

// SurfaceHooks returns the quotes.builder hooks the settings kit consumes: the policy payload
// (staff-resolved; the cost DATA gate lives at the glance, not here) and the dealer-default
// workspace geometry.
func (s *StaffService) SurfaceHooks() settings.SurfaceHooks {
	return settings.SurfaceHooks{
		Policy: func(ctx context.Context, branchID string) (any, error) {
			return s.QuotePolicyFor(ctx, branchID)
		},
		WorkspaceDefaults: func(ctx context.Context, branchID string) (int, string, error) {
			b, ok := s.builder()
			if !ok {
				return settings.SplitDefault, settings.ArrangementPrimaryLeft, nil
			}
			p, err := b.ReadQuotePolicy(ctx, branchID)
			if err != nil {
				return settings.SplitDefault, settings.ArrangementPrimaryLeft, err
			}
			return p.WorkspaceSplitPct, p.WorkspaceArrangement, nil
		},
	}
}

// SettingsKitDeps hands the handler what it needs to mount the shared kit without cmd/* wiring:
// the pgx pool behind this service's store (nil when the store is not the real repository -
// the kit then simply does not mount, and the surface degrades to its shipped defaults).
func (s *StaffService) settingsKitService() *settings.Service {
	r, ok := s.store.(*Repository)
	if !ok || r.pool == nil {
		return nil
	}
	return settings.NewService(settings.NewStore(r.pool)).WithSurface("quotes.builder", s.SurfaceHooks())
}

// identityCallerOK is referenced by tests to prove the gate consults the identity caller, kept
// here so the compiler pins the import even if the glance evolves.
var _ = identity.StaffContext{}
