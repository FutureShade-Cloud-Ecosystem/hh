package pricing

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/product"
	"github.com/jackc/pgx/v5"
)

// - error taxonomy (mirrors internal/product) -
type badRequestError struct{ msg string }

func (e badRequestError) Error() string    { return e.msg }
func badReq(format string, a ...any) error { return badRequestError{msg: fmt.Sprintf(format, a...)} }

// unprocessableError is a SEMANTIC validation failure (well-formed request, but the
// values violate a pricing rule): ABSOLUTE on a non-CUSTOMER_PRODUCT scope, floor >
// target, a calc-type with the wrong (or missing) value field. It maps to HTTP 422 so
// a calc-type edit to ABSOLUTE on a TIER_* rule surfaces as a friendly 422 instead of
// the DB CHECK leaking out as a generic 500 (R-3).
type unprocessableError struct{ msg string }

func (e unprocessableError) Error() string { return e.msg }
func unprocessable(format string, a ...any) error {
	return unprocessableError{msg: fmt.Sprintf(format, a...)}
}

var (
	errNotFound       = errors.New("pricing: not found")
	errNoConversion   = errors.New("pricing: no uom conversion factor")
	errConflict       = errors.New("pricing: version conflict")
	errNoListPrice    = errors.New("pricing: no retail anchor (list price) for product")
	errRuleCollision  = errors.New("pricing: a conflicting active rule already exists")
	errOverrideTaken  = errors.New("pricing: an override already exists for this customer + category")
	errBreakCodeTaken = errors.New("pricing: a quantity break with this code already exists")
	// W4-A: the code UNIQUEs on pricing_promotions / rebate_programs, mapped to a friendly 409
	// instead of a bare 23505 leaking as a 500, same treatment as errBreakCodeTaken.
	errPromoCodeTaken  = errors.New("pricing: a promotion with this code already exists")
	errRebateCodeTaken = errors.New("pricing: a rebate program with this code already exists")
	// errFKViolation is a well-formed request that references a customer/product/category/branch
	// id that does not exist (Postgres 23503). It maps to a friendly 422 instead of leaking the
	// FK violation as a generic 500 (finding #10).
	errFKViolation = errors.New("pricing: referenced entity does not exist")
	// errNoTxAudit: this service's store cannot append audit rows ON a
	// caller's transaction (a DB-free fake), so the transactional append
	// refuses rather than silently dropping the row it was asked to ride.
	errNoTxAudit = errors.New("pricing: this store cannot append audit rows on a transaction")
)

// bps bounds for a discount/markup basis-point value. 0..10000 = 0%..100%; anything outside
// is rejected with a 422 (validateRule / validateOverride) AND by the 0025 DB CHECKs. A value
// above 10000 would drive applyPctOff to a NEGATIVE price (finding #1).
const (
	minCalcBps int64 = 0
	maxCalcBps int64 = 10000
)

// defaultFloorBps is the fallback enforced floor when a category has no policy row.
// It is the legacy identity.MarginFloorBps (1200 bps), kept here as a literal so the
// pricing package does not hard-depend on identity just for a constant.
const defaultFloorBps int64 = 1200

// defaultTargetBps is the fallback MARGIN TARGET when a category has no policy row. It is the
// 06-23 policy target (30%). The low-margin classifier seeds the target from this (NOT from the
// floor constant) so a no-policy category is graded against a real target band (finding #12):
// before the fix the target defaulted to defaultFloorBps, so a below-target-but-above-floor line
// looked "clean".
const defaultTargetBps int64 = 3000

// retailMarkupBps is the PROVISIONAL retail markup used to DERIVE a LIST/retail anchor
// from cost when a product has no explicit pricing_list_prices row (the 06-23 "cost × 1.85"
// rule: 8500 bps over cost ⇒ ×1.85). PROVISIONAL pending the dealer principal (Q#3); it is tunable to a
// seeded/row value later WITHOUT a resolver change. Without this, a rule-matched line with
// no anchor priced off a $0 list and returned $0 (the gate-critical bug fix #1 closes).
const retailMarkupBps int64 = 8500

// - injected seams (zero-trust) -

// CategoryResolver resolves catalog metadata for a product WITHOUT SELECTing catalog
// tables (product_categories / products) directly. Satisfied by internal/product.Service.
type CategoryResolver interface {
	GetCategory(ctx context.Context, productID string) (string, error)
	// GetBaseUOM returns the product's base unit-of-measure (products.base_uom) so the
	// resolver can denominate a DERIVED (cost-based) retail anchor in the product's REAL
	// base UOM instead of "". Without it Breakdown.PriceUOM is empty for a derived anchor
	// (26 of 30 seeded products), and a quote/order line CANNOT persist, price_uom is
	// NOT NULL REFERENCES uoms(code). A missing product must surface as product.ErrNotFound
	// so the resolver maps it to its own 404 path (same contract as GetCategory).
	GetBaseUOM(ctx context.Context, productID string) (string, error)
}

// UOMConverter returns qty_in_base for (productID, fromUOM) without SELECTing product_uoms.
// Satisfied by internal/product.Service.ConvertUOM. A missing factor must surface as a
// not-found-shaped error so the resolver can refuse to guess (US-P06).
type UOMConverter interface {
	ConvertUOM(ctx context.Context, productID, fromUOM string) (float64, error)
}

// MarginGate is the below-floor check. The real adapter calls identity.CheckMarginAt
// (A-20, additive) with the effective per-category floor; tests inject a DB-free fake
// or the real identity-backed gate (NewIdentityMarginGate).
type MarginGate interface {
	Check(priceCents, costCents int64, branchID string, hasApprovedOverride bool, floorBps int64) *MarginBlock
}

// pricingStore is the persistence the Service needs. Real impl: Repository (pgx); tests use a fake.
// It reads ONLY pricing-owned tables.
type pricingStore interface {
	MatchingRules(ctx context.Context, in ResolveInput) ([]Rule, error)
	QuantityBreaksFor(ctx context.Context, productID, categoryID string) ([]QuantityBreak, error)
	OverrideFor(ctx context.Context, customerID, categoryID string) (*Override, error)
	ListPrice(ctx context.Context, productID string) (*ListPrice, error)
	CategoryPolicy(ctx context.Context, categoryID string) (*CategoryPolicy, error)
	ProductPolicy(ctx context.Context, productID string) (*ProductPolicy, error)
	AppendAudit(ctx context.Context, e AuditEntry) error
	// Admin CRUD (Tier B surface; the optimistic-lock + audit logic is service-side).
	CreateRule(ctx context.Context, r Rule) (Rule, error)
	GetRule(ctx context.Context, id string) (Rule, error)
	// SaveRule writes a full rule row under an ATOMIC optimistic-lock guard: it updates only the
	// row whose version still equals expectedVersion, returning errConflict (409) on a version
	// mismatch and errNotFound (404) when the row is gone (finding #3).
	SaveRule(ctx context.Context, r Rule, expectedVersion int64) (Rule, error)
	ListRules(ctx context.Context, f RuleFilter) ([]Rule, error)
	// Category margin-policy (the keystone grid, US-P01/02/03).
	ListCategoryPolicies(ctx context.Context) ([]CategoryPolicy, error)
	UpsertCategoryPolicy(ctx context.Context, p CategoryPolicy) (CategoryPolicy, error)
	// Customer overrides (US-P09). GetOverride is keyed by ROW id (the optimistic-version
	// edit path), distinct from OverrideFor(customer,category) the resolver uses.
	ListOverrides(ctx context.Context, customerID string) ([]Override, error)
	GetOverride(ctx context.Context, id string) (Override, error)
	CreateOverride(ctx context.Context, o Override) (Override, error)
	// SaveOverride / DeleteOverride are version-guarded atomically (findings #3/#4): the write
	// touches only the row whose version still equals expectedVersion (errConflict on mismatch,
	// errNotFound when absent).
	SaveOverride(ctx context.Context, o Override, expectedVersion int64) (Override, error)
	DeleteOverride(ctx context.Context, id string, expectedVersion int64) error
	// Audit-log read (US-P12).
	ListAudit(ctx context.Context, f AuditFilter) ([]AuditLogRow, error)
	// Quantity-break admin CRUD (A-07 Tier-B editor). Header + its tiers are written as a unit; the
	// optimistic-version guard + audit live service-side, the items-replace + version guard are atomic
	// in the repo (like SaveRule).
	ListQuantityBreakHeaders(ctx context.Context) ([]QuantityBreakHeader, error)
	GetQuantityBreakHeader(ctx context.Context, id string) (QuantityBreakHeader, error)
	CreateQuantityBreakHeader(ctx context.Context, h QuantityBreakHeader) (QuantityBreakHeader, error)
	SaveQuantityBreakHeader(ctx context.Context, h QuantityBreakHeader, expectedVersion int64) (QuantityBreakHeader, error)
	DeleteQuantityBreakHeader(ctx context.Context, id string, expectedVersion int64) error
}

// salesScanStore is the D-PA-3 live-sales feed seam: it reads the COMMITTED sold lines for the
// daily low-margin scan (order_lines JOIN orders JOIN customers JOIN products LEFT JOIN
// product_branch_costs). This is a REPORTING query that crosses module tables BY DESIGN, the whole
// point of the principal's daily report, so it is a SEPARATE optional seam (auto-wired from the pgx
// *Repository, like batchStore) rather than a method on the zero-trust pricingStore, which reads
// pricing-owned tables only. nil until wired; LowMarginScan returns errNoDB when it is unset.
type salesScanStore interface {
	LowMarginSalesLines(ctx context.Context, from, to time.Time, branchID string) ([]LowMarginSaleLine, error)
}

// txAuditStore is the transactional half of the audit append: the same row
// AppendAudit writes, executed on a caller-supplied transaction. Auto-wired
// from the pgx *Repository like the seams above; a DB-free fake leaves it
// nil and RecordFloorDecisionTx answers errNoTxAudit instead of pretending
// the row landed somewhere.
type txAuditStore interface {
	AppendAuditTx(ctx context.Context, tx pgx.Tx, e AuditEntry) error
}

// Service holds the pricing business logic.
type Service struct {
	store pricingStore
	batch batchStore      // A-08 price-batch/import/change-log seam (nil until WithBatchStore)
	scan  salesScanStore  // D-PA-3 low-margin live-sales feed (nil until WithSalesScanStore)
	txAud txAuditStore    // transactional audit append (auto-wired; nil on DB-free fakes)
	cands FloorCandidates // CP0-6 floor warning product source (nil until WithCandidateSource)
	// W4-A optional seams. All three are FLAG-OFF by default: nil means the corresponding step does
	// not run and the resolver behaves exactly as it did in W3. That is what lets every pre-existing
	// resolver test keep passing against a fake that has never heard of promotions.
	promo  promoStore  // DIB-70 promotions/clearance (nil until WithPromoStore)
	rebate rebateStore // DIB-69 rebate programs + accrual ledger (nil until WithRebateStore)
	conv   convStore   // M2-uom sales conversion graph (nil until WithConversionStore)
	cat    CategoryResolver
	uom    UOMConverter
	gate   MarginGate
	// authority is the DIB-420 server-side margin-override check. NIL MEANS NO OVERRIDE REQUEST IS
	// EVER HONOURED, see override_authority.go for why that is the safe default and not a gap.
	authority OverrideAuthority
	// The price-file import seams (pricefile_model.go). All four are nil-safe and all four fail in
	// the direction that costs the least: no cost store means nobody sees cost, no vendor directory
	// means an empty picker, no catalogue means a preview that says the check did not run.
	pricefile priceFileStore
	costvis   CostVisibility
	vendors   VendorDirectory
	catskus   CatalogSKUs
	// log is nil-safe throughout; it exists so a refused override is observable on the server even
	// though the caller is told nothing about which lever they failed to pull.
	log *slog.Logger
	now func() time.Time
}

func NewService(store pricingStore) *Service {
	s := &Service{store: store, now: time.Now}
	// Auto-wire the A-08 batch seam when the injected store also implements it (the pgx
	// *Repository does). This keeps the frozen main.go wiring untouched, no WithBatchStore
	// call needed in production, while resolver-only fakes (which do not implement batchStore)
	// simply leave s.batch nil.
	if b, ok := store.(batchStore); ok {
		s.batch = b
	}
	if c, ok := store.(FloorCandidates); ok {
		s.cands = c
	}
	// Auto-wire the D-PA-3 low-margin scan seam the same way (the pgx *Repository implements it);
	// DB-free fakes that don't leave s.scan nil, so LowMarginScan returns a friendly errNoDB.
	if sc, ok := store.(salesScanStore); ok {
		s.scan = sc
	}
	// The transactional audit append wires the same way: the pgx *Repository
	// carries it, DB-free fakes don't, so RecordFloorDecisionTx fails loud on
	// a store that cannot ride a transaction rather than dropping the row.
	if ta, ok := store.(txAuditStore); ok {
		s.txAud = ta
	}
	// W4-A seams auto-wire the same way: the pgx *Repository implements all three, so production
	// needs no extra With… call and main.go's pricing block stays a single constructor chain.
	// DB-free fakes implement none of them and leave the new steps switched off.
	if p, ok := store.(promoStore); ok {
		s.promo = p
	}
	if rb, ok := store.(rebateStore); ok {
		s.rebate = rb
	}
	if cv, ok := store.(convStore); ok {
		s.conv = cv
	}
	// The price-file store auto-wires the same way. It covers pricing's OWN tables only; the two
	// foreign reads this surface needs (vendors, products) are consumer-declared seams that
	// cmd/server adapts, and they stay nil here on purpose, a store that could satisfy them by
	// accident is how a module ends up SELECTing somebody else's table again.
	if pf, ok := store.(priceFileStore); ok {
		s.pricefile = pf
	}
	return s
}

func (s *Service) WithCategoryResolver(c CategoryResolver) *Service { s.cat = c; return s }
func (s *Service) WithUOMConverter(u UOMConverter) *Service         { s.uom = u; return s }
func (s *Service) WithMarginGate(g MarginGate) *Service             { s.gate = g; return s }

// WithLogger attaches a logger. Nil-safe everywhere; a Service without one simply says nothing.
func (s *Service) WithLogger(l *slog.Logger) *Service { s.log = l; return s }

// WithBatchStore injects the A-08 price-batch/import/change-log store (additive; the A-07
// resolver is unaffected). Nil until wired, so a resolver-only Service keeps working.
func (s *Service) WithBatchStore(b batchStore) *Service { s.batch = b; return s }

// WithSalesScanStore injects the D-PA-3 low-margin live-sales feed (additive). Production auto-wires
// it from the pgx *Repository via NewService; tests inject a DB-free fake.
func (s *Service) WithSalesScanStore(sc salesScanStore) *Service { s.scan = sc; return s }

// withClock is a test seam for deterministic validity-window checks.
func (s *Service) withClock(f func() time.Time) *Service { s.now = f; return s }

// resolveBaseUOM fetches the product's base unit-of-measure through the catalog seam
// (zero-trust: never a products SELECT from pricing). A not-found product is mapped to
// pricing's errNotFound (→404), consistent with the GetCategory path. When no seam is
// wired (scaffold/unit path) it degrades to "", the legacy derived-anchor behaviour.
func (s *Service) resolveBaseUOM(ctx context.Context, productID string) (string, error) {
	if s.cat == nil {
		return "", nil
	}
	u, err := s.cat.GetBaseUOM(ctx, productID)
	if err != nil {
		if errors.Is(err, product.ErrNotFound) {
			return "", fmt.Errorf("resolve base uom: %w", errNotFound)
		}
		return "", fmt.Errorf("resolve base uom: %w", err)
	}
	return u, nil
}

// uomFactorFor returns qty_in_base for (productID, uom) through the injected UOMConverter, the
// SINGLE uom-factor helper the resolver's factor closure AND the D-PA-3 low-margin scan both use,
// so a scanned line's cost is converted BASE→price_uom exactly the way the priced line was (no
// second, divergent conversion). Semantics match the resolver's original closure: uom=="" → 1
// (dimensionless), no converter wired → 1 (degrade to same-UOM on the scaffold/unit path), and a
// missing factor → errNoConversion (a scan line surfaces that as an invalid "no uom factor" row).
func (s *Service) uomFactorFor(ctx context.Context, productID, uom string) (float64, error) {
	if uom == "" {
		return 1, nil
	}
	if s.uom == nil {
		return 1, nil
	}
	f, err := s.uom.ConvertUOM(ctx, productID, uom)
	if err != nil {
		if isNoConversion(err) {
			return 0, errNoConversion
		}
		return 0, err
	}
	return f, nil
}

// - the resolver waterfall -

// ResolvePrice runs the deterministic precedence waterfall and returns the pre-tax
// Breakdown. The BASE price is resolved first-match-wins over the rule levels:
//
//	CUSTOMER_PRODUCT > CUSTOMER_OVERRIDE / CUSTOMER_CATEGORY > TIER_CATEGORY >
//	TIER_DEFAULT (~99%) > LIST (retail anchor).
//
// A matching quantity break then competes as ONE single price computed off the retail anchor and
// REPLACES the resolved base when strictly cheaper (D1's replace rule, never a stack on top of
// it): a contractor's −18% Pavers base against a −5% full-skid break keeps the −18% price, and
// the same break against the shelf price replaces it with retail×0.95. A surcharge tier is
// structurally excluded from the win (an uplift can never be strictly cheaper), and a break with
// no present, non-zero anchor to price from does not compete at all. PROVISIONAL: the break% /
// threshold values are pending the dealer principal's confirmation. A negotiated CUSTOMER_PRODUCT
// contract price is FINAL, no break (or override) applies on top of it.
// (CUSTOMER_SUPPLIER / SUPPLIER_CATEGORY are RESERVED-UNBUILT, no product→supplier anchor.)
func (s *Service) ResolvePrice(ctx context.Context, in ResolveInput) (Breakdown, error) {
	return s.resolve(ctx, in, false, false)
}

// ResolvePricePreview is ResolvePrice with the audit write suppressed: a strictly READ-ONLY evaluation
// of the waterfall that changes no price, applies no different rule, and writes nothing anywhere.
//
// It exists because "read-only" was not previously expressible from outside this package. The waterfall
// CAN write, when margin enforcement is disabled for a category or product, resolve appends a BYPASS
// row to pricing_audit_log so a disabled item stays observable (no silent bypass). That is correct for
// a real quote or sale, and wrong for a preview: a surface that scores several hypothetical
// alternatives would stamp a BYPASS row per alternative, none of which anyone offered a customer.
//
// The dryRun flag that suppresses it was reachable only through the private resolve, used by
// LowMarginReport. The DIB-59 quote comparison needs the same guarantee, so the door is now exported
// rather than the flag re-derived, the rationale at resolve() above already anticipated this.
//
// Callers that are actually pricing something must use ResolvePrice: a live disabled-enforcement line
// must still be recorded.
func (s *Service) ResolvePricePreview(ctx context.Context, in ResolveInput) (Breakdown, error) {
	return s.resolve(ctx, in, true, false)
}

// resolve is the shared waterfall. dryRun=true is a READ-ONLY evaluation that NEVER mutates the
// pricing_audit_log, specifically it skips the enforcement-disabled BYPASS append. The read-only
// low-margin preview (LowMarginReport) uses dryRun so a preview cannot pollute the audit trail
// with phantom BYPASS rows (finding #5). The live resolve (ResolvePrice / DeriveLinePrice) uses
// dryRun=false so a real disabled-enforcement line is still recorded (no silent bypass).
//
// counterTill=true is the till's resolve (ResolveCounterPrice, counter_resolve.go): the waterfall
// above is identical, and the margin gate step at the bottom is never reached, the floor is the
// quote flow's question and the till's ruled price is accepted as it resolves (D48 row 481, D49).
func (s *Service) resolve(ctx context.Context, in ResolveInput, dryRun, counterTill bool) (Breakdown, error) {
	if in.ProductID == "" {
		return Breakdown{}, badReq("product_id is required")
	}
	if in.QtyEntered <= 0 {
		return Breakdown{}, badReq("qty_entered must be > 0")
	}

	// 1. Resolve category via the catalog seam (never a product_categories SELECT). A
	//    not-found from the catalog seam maps to pricing's errNotFound so the handler returns
	//    404 (not a 500) for an unknown product (fix #6).
	if in.CategoryID == "" && s.cat != nil {
		cid, err := s.cat.GetCategory(ctx, in.ProductID)
		if err != nil {
			if errors.Is(err, product.ErrNotFound) {
				return Breakdown{}, fmt.Errorf("resolve category: %w", errNotFound)
			}
			return Breakdown{}, fmt.Errorf("resolve category: %w", err)
		}
		in.CategoryID = cid
	}

	// UOM factor cache (qty_in_base per 1 uom). Missing factor → errNoConversion. Delegates to the
	// shared uomFactorFor helper so the resolver and the D-PA-3 scan convert through ONE code path.
	fcache := map[string]float64{}
	factor := func(uom string) (float64, error) {
		if v, ok := fcache[uom]; ok {
			return v, nil
		}
		f, err := s.uomFactorFor(ctx, in.ProductID, uom)
		if err != nil {
			return 0, err
		}
		fcache[uom] = f
		return f, nil
	}

	// 2. Retail anchor. An explicit pricing_list_prices row IS the anchor; when one is
	//    ABSENT we DERIVE a PROVISIONAL retail/LIST anchor from cost (cost × 1.85 -
	//    retailMarkupBps, the 06-23 rule) so a rule-matched line NEVER prices off a $0
	//    anchor (fix #1, the gate-critical $0 bug). The derived anchor is cost-denominated,
	//    so it lives in the product BASE UOM (factor 1), exactly like CostCents, which the
	//    caller passes per the product base UOM. With NEITHER an explicit anchor NOR a cost
	//    to derive from, the anchored calc paths (PCT_OFF_LIST / surcharge / bare LIST)
	//    return errNoListPrice (422) rather than computing off 0. ABSOLUTE and MARKUP_ON_COST
	//    need no list anchor (they self-anchor on the contract price / cost respectively).
	lp, err := s.store.ListPrice(ctx, in.ProductID)
	if err != nil {
		return Breakdown{}, fmt.Errorf("list price: %w", err)
	}
	var listCents int64
	listUOM := in.UOMEntered
	derivedAnchor := false
	switch {
	case lp != nil:
		listCents = lp.ListPriceCents
		listUOM = lp.PriceUOM
	case in.CostCents > 0:
		listCents = applyMarkup(in.CostCents, retailMarkupBps)
		// The derived anchor is cost-denominated → it lives in the product BASE UOM (factor 1).
		// Look that base UOM up via the catalog seam so Breakdown.PriceUOM carries the product's
		// REAL base unit rather than "", REQUIRED because quote_lines/order_lines.price_uom is
		// CITEXT NOT NULL REFERENCES uoms(code), so a "" anchor UOM cannot persist a derived-anchor
		// line (the dominant case: 26 of 30 seeded products have no explicit anchor row).
		baseUOM, berr := s.resolveBaseUOM(ctx, in.ProductID)
		if berr != nil {
			return Breakdown{}, berr
		}
		listUOM = baseUOM
		derivedAnchor = true
	}
	hasAnchor := lp != nil || derivedAnchor

	// 3. Candidate rules (one query). Quantity breaks + the customer override are fetched
	//    LAZILY, only when no CUSTOMER_PRODUCT contract price matches, so a contract-priced
	//    line does not pay for two round-trips it can't use (fix #8): a negotiated
	//    CUSTOMER_PRODUCT price is final, and breaks/overrides do not apply on top of it.
	rules, err := s.store.MatchingRules(ctx, in)
	if err != nil {
		return Breakdown{}, fmt.Errorf("matching rules: %w", err)
	}

	// 4. Resolve the BASE price level via the rule waterfall (first match wins). The matching
	//    quantity break is selected here too, and in step 6 it competes as ONE single price computed
	//    off the retail anchor, replacing the resolved base when strictly cheaper (D1's replace
	//    rule, never a stack on top of it). Precedence: CUSTOMER_PRODUCT > CUSTOMER_OVERRIDE /
	//    CUSTOMER_CATEGORY rule (fix #2) > TIER_CATEGORY > TIER_DEFAULT > LIST. declaredUOM = the
	//    winner's stated price_uom.
	var (
		source, detail, declaredUOM, calcType string
		priceVal                              *int64 // ABSOLUTE
		discVal, markupVal                    *int64
		breakAdj                              *QuantityBreak // the matched break, judged in step 6
		// DIB-70. wonPromo is non-nil ONLY when a promotion actually became the base, a promo that
		// matched and lost the never-worse guard leaves it nil, so the Breakdown never advertises a
		// promo code on a line that is not priced at the promo.
		wonPromo          *promoCandidate
		bdClearance       bool
		bdClearanceReason string
	)

	if r := s.selectRule(rules, ScopeCustomerProduct, in, func(r Rule) bool {
		return r.CustomerID == in.CustomerID && r.ProductID == in.ProductID && in.CustomerID != "" && in.ProductID != ""
	}); r != nil {
		source, detail, declaredUOM, calcType = SourceCustomerProduct, ruleDetail(*r, "Customer-product price"), r.PriceUOM, r.CalcType
		priceVal, discVal, markupVal = r.PriceCents, r.DiscountBps, r.MarkupBps
		// Contract price is FINAL: breaks + override are neither fetched nor applied on top.
	} else {
		breaks, err := s.store.QuantityBreaksFor(ctx, in.ProductID, in.CategoryID)
		if err != nil {
			return Breakdown{}, fmt.Errorf("quantity breaks: %w", err)
		}
		override, err := s.store.OverrideFor(ctx, in.CustomerID, in.CategoryID)
		if err != nil {
			return Breakdown{}, fmt.Errorf("override: %w", err)
		}
		// The single applicable break (product-header preferred) is selected here whatever its
		// kind, and judged in step 6 (a discount may replace the base; a surcharge never wins).
		// A missing threshold-UOM conversion surfaces as errNoConversion rather than silently
		// dropping the break (fix #3), a misconfigured break must fail loud, never silently
		// undercharge.
		breakAdj, err = s.selectBreak(breaks, in, factor)
		if err != nil {
			return Breakdown{}, err
		}

		switch {
		case override != nil:
			source, detail, declaredUOM, calcType = SourceCustomerOverride, overrideDetail(*override), override.PriceUOM, override.CalcType
			priceVal, discVal, markupVal = override.PriceCents, override.DiscountBps, override.MarkupBps
		default:
			if r := s.selectRule(rules, ScopeCustomerCategory, in, func(r Rule) bool {
				return r.CustomerID == in.CustomerID && r.CategoryID == in.CategoryID && in.CustomerID != "" && in.CategoryID != ""
			}); r != nil {
				source, detail, declaredUOM, calcType = SourceCustomerCategory, ruleDetail(*r, "Customer-category price"), r.PriceUOM, r.CalcType
				priceVal, discVal, markupVal = r.PriceCents, r.DiscountBps, r.MarkupBps
			} else if r := s.selectRule(rules, ScopeTierCategory, in, func(r Rule) bool {
				return r.CustomerType == in.CustomerType && r.CategoryID == in.CategoryID && in.CategoryID != ""
			}); r != nil {
				source, detail, declaredUOM, calcType = SourceTierCategory, ruleDetail(*r, "Tier-category price"), r.PriceUOM, r.CalcType
				priceVal, discVal, markupVal = r.PriceCents, r.DiscountBps, r.MarkupBps
			} else if r := s.selectRule(rules, ScopeTierDefault, in, func(r Rule) bool {
				return r.CustomerType == in.CustomerType
			}); r != nil {
				source, detail, declaredUOM, calcType = SourceTierDefault, ruleDetail(*r, "Tier default price"), r.PriceUOM, r.CalcType
				priceVal, discVal, markupVal = r.PriceCents, r.DiscountBps, r.MarkupBps
			} else {
				source, detail, declaredUOM, calcType = SourceList, "List price", listUOM, ""
			}
		}
	}

	// The RESULT price_uom: a percentage discount is dimensionless, so it resolves in the
	// retail anchor's UOM (apples-to-apples, the client's #1 pain). Only an ABSOLUTE set
	// price or a MARKUP_ON_COST (cost-denominated) carries its own declared price_uom. The base
	// calc_type is always a rule/override calc here (a break prices its own single figure in step
	// 6, it is never the base calc).
	resultUOM := declaredUOM
	if source != SourceList && calcType == CalcPctOffList {
		resultUOM = listUOM
	}

	// 5. Convert the anchor + cost into the result price_uom and apply the BASE calc.
	fRes, err := factor(resultUOM)
	if err != nil {
		return Breakdown{}, err
	}
	fList, err := factor(listUOM)
	if err != nil {
		return Breakdown{}, err
	}
	if fList == 0 || fRes == 0 {
		return Breakdown{}, errNoConversion
	}
	originalCents := roundCents(float64(listCents) * fRes / fList)
	costInResUOM := roundCents(float64(in.CostCents) * fRes)

	var finalCents int64
	switch {
	case source == SourceList:
		if !hasAnchor {
			return Breakdown{}, errNoListPrice
		}
		finalCents = originalCents
	case calcType == CalcAbsolute:
		if priceVal == nil {
			return Breakdown{}, badReq("ABSOLUTE rule missing price_cents")
		}
		finalCents = *priceVal // a negotiated contract price needs no list anchor
	case calcType == CalcPctOffList:
		if discVal == nil {
			return Breakdown{}, badReq("PCT_OFF_LIST rule missing discount_bps")
		}
		if originalCents == 0 {
			return Breakdown{}, errNoListPrice // never discount off a $0 anchor (fix #1)
		}
		finalCents = applyPctOff(originalCents, *discVal)
	case calcType == CalcMarkupOnCost:
		if markupVal == nil {
			return Breakdown{}, badReq("MARKUP_ON_COST rule missing markup_bps")
		}
		if costInResUOM == 0 {
			return Breakdown{}, errNoListPrice // a markup on a $0 cost basis is not a price (fix #1)
		}
		finalCents = applyMarkup(costInResUOM, *markupVal)
		if originalCents == 0 {
			originalCents = finalCents
		}
	default:
		return Breakdown{}, badReq("unsupported calc_type %q", calcType)
	}

	// 5b. PROMOTION / CLEARANCE (DIB-70). The full ruling, and why it is a ruling rather than an
	//     obvious reading of "contract > promo > quantity-break > list", is at the top of promo.go.
	//     In one paragraph: a promotion competes for BASE selection directly below the contract
	//     price and above the whole override/tier/list waterfall, but only when it BEATS the base
	//     that waterfall produced (the NEVER-WORSE GUARD). A contract price is final, so this whole
	//     block is skipped on the CUSTOMER_PRODUCT path, except for the clearance BADGE, which is
	//     a fact about the product and is true regardless of who is buying it.
	if s.promo != nil {
		// The clearance read runs on BOTH paths, including the contract path the surrounding code
		// carefully keeps to a single round-trip. That is a deliberate cost: the badge is what makes
		// clearance visible on POS search results and quote lines (DIB-154), and a contract customer
		// looking at a clearance SKU needs to see the badge as much as a walk-in does. It is a single
		// primary-key lookup on a table with one row per cleared item.
		clear, cerr := s.promo.ClearanceFor(ctx, in.ProductID)
		if cerr != nil {
			return Breakdown{}, fmt.Errorf("clearance: %w", cerr)
		}
		now := s.now()
		if clear != nil && clear.active(now) {
			bdClearance, bdClearanceReason = true, clear.Reason
		}
		if source != SourceCustomerProduct {
			promos, perr := s.promo.ActivePromotions(ctx, in.ProductID, in.CategoryID, in.BranchID, now)
			if perr != nil {
				return Breakdown{}, fmt.Errorf("promotions: %w", perr)
			}
			if cand := selectPromotion(promos, clear, in, now); cand != nil {
				// THE PROMO IS COMPUTED OFF THE RETAIL ANCHOR, NOT OFF THE TIER-RESOLVED BASE.
				//
				// This is the single line that makes the promo a BASE-SELECTION level rather than a
				// second adjustment, and getting it wrong is subtle: passing finalCents here compiles,
				// passes a naive test, and quietly gives a contractor 18% off AND 25% off (retail ×
				// 0.82 × 0.75 = 61.5% of list) on every promoted line. "20% off" means 20% off the
				// shelf price, that is what the sign in the yard says and what the customer expects.
				// The tier price is then compared against it below, and the customer keeps whichever
				// is better.
				promoCents, applied, aerr := applyPromo(*cand, originalCents, resultUOM)
				if aerr != nil {
					return Breakdown{}, aerr
				}
				switch {
				case !applied:
					// A FIXED_PRICE promo authored in a different UOM than the line resolved in. Not
					// an error and not silently converted, see applyPromo. The base stands and the
					// breakdown says the promo could not be expressed on this line.
					detail = detail + " · " + promoUOMMismatchNote(*cand, resultUOM)
				case promoCents < finalCents:
					// THE PROMO WINS. Source becomes PROMOTION so the breakdown, the low-margin
					// report and the counter all agree on why this line is cheap.
					finalCents = promoCents
					source = SourcePromotion
					detail = promoDetail(*cand)
					wonPromo = cand
				default:
					// THE NEVER-WORSE GUARD FIRED. The customer's ordinary price already beats the
					// sale, so they keep it. Recorded, not silent, the clerk has to be able to
					// answer "but the sign says 10% off".
					detail = detail + " · " + promoConsideredNote(*cand, promoCents, finalCents)
				}
			}
		}
	}

	// 6. QUANTITY-BREAK REPLACE (D1). The matching break competes as ONE single price computed off
	//    the RETAIL ANCHOR and REPLACES the resolved price when strictly cheaper; it never stacks
	//    on it. "10% off at a full skid" means 10% off the shelf price, not 10% off the
	//    already-discounted tier price: the customer gets the best single price, never a pile of
	//    discounts. A break that LOSES the contest (the tier or a promo already beats it) leaves
	//    the price standing and the breakdown says so, the promo's own never-worse precedent,
	//    because silence about a volume sign the customer can read is a support call. A SURCHARGE
	//    tier is structurally excluded from the win branch, with or without an anchor (an uplift
	//    can never be strictly cheaper), so it never adjusts the price; ADR-0015 records that loss
	//    and the vocabulary stays for the editor and the frozen schema. And the best-of is
	//    ANCHORED: a break with no present, non-zero anchor to price from does not compete at all,
	//    because off a zero anchor a discount prices at zero and an ABS_OFF-style surcharge at its
	//    own amount, either of which would "beat" any real price and hand the till a confident
	//    wrong number. When the break wins, Source becomes QUANTITY_BREAK so the breakdown, the
	//    low-margin report and the counter all agree on why this line is cheap, exactly the
	//    treatment a winning promotion gets. A break only ever reaches here on the
	//    non-CUSTOMER_PRODUCT path (a contract price is final).
	if breakAdj != nil {
		if err := requireBreakValue(*breakAdj); err != nil {
			return Breakdown{}, err
		}
		switch {
		case isSurchargeBreak(breakAdj.ValueType):
			// STRUCTURALLY OUT OF THE WIN BRANCH, before any arithmetic: no anchor state can
			// make an uplift strictly cheaper, so its candidate is never even computed.
			detail = appendDetailNote(detail, breakHeldReasonNote(*breakAdj,
				"an uplift cannot win the replace contest"))
		case originalCents <= 0:
			// NO ANCHOR, NO CONTEST. The standing price (an ABSOLUTE override or rule is the
			// reachable case) holds, and the breakdown records the loss with its reason.
			detail = appendDetailNote(detail, breakHeldReasonNote(*breakAdj,
				"no list anchor to price the break from"))
		default:
			breakCents := breakPriceOffAnchor(*breakAdj, originalCents)
			if breakCents < finalCents {
				// THE BREAK WINS. Its single price replaces the tier (and a promo that had won the
				// base): no compounding in either direction, and the breakdown tells the winner's
				// story alone, exactly as a winning promotion replaces the detail it beat.
				finalCents = breakCents
				source = SourceQuantityBreak
				wonPromo = nil // the beaten promo no longer prices this line, so it is not advertised
				detail = breakNote(*breakAdj)
			} else if breakHeldNoteWorthy(*breakAdj) {
				// THE BREAK LOST. The customer keeps their better single price, recorded, not
				// silent, UNLESS the band never offered anything (a 0 bps band below the first
				// threshold is the seeded schedules' "no discount yet" floor band): a no-op
				// explained is noise on every small line.
				detail = appendDetailNote(detail, breakHeldNote(*breakAdj, finalCents, breakCents))
			}
		}
	}

	// 6. Quantities + line total in the result price_uom (pre-tax only).
	fEnt, err := factor(in.UOMEntered)
	if err != nil {
		return Breakdown{}, err
	}
	fBase := 1.0 // qty_base is in the product base UOM; entered × factor(entered) gives base units
	qtyBase := in.QtyEntered * fEnt * fBase
	qtyInResUOM := in.QtyEntered * fEnt / fRes
	lineTotal := roundCents(qtyInResUOM * float64(finalCents))

	bd := Breakdown{
		OriginalCents:   originalCents,
		FinalCents:      finalCents,
		DiscountBps:     bpsOff(originalCents, finalCents),
		Source:          source,
		Detail:          detail,
		PriceUOM:        resultUOM,
		QtyBase:         qtyBase,
		LineTotalCents:  lineTotal,
		Clearance:       bdClearance,
		ClearanceReason: bdClearanceReason,
	}
	if wonPromo != nil {
		bd.PromoCode, bd.PromoName = wonPromo.promo.Code, wonPromo.promo.Name
	}

	// THE COUNTER RESOLVE STOPS HERE (counter_resolve.go). The till's line is priced: the rule
	// and tier waterfall above decided it and that price is accepted. The gate below is the quote
	// flow's question, so the till path returns before it: no gate check, no blocked line, no
	// BYPASS row, and the policy reads below never run for a till line.
	if counterTill {
		return bd, nil
	}

	// 7. Margin gate (reuse A-20, additive) unless enforcement is disabled.
	policy, err := s.store.CategoryPolicy(ctx, in.CategoryID)
	if err != nil {
		return Breakdown{}, fmt.Errorf("category policy: %w", err)
	}
	prodPolicy, err := s.store.ProductPolicy(ctx, in.ProductID)
	if err != nil {
		return Breakdown{}, fmt.Errorf("product policy: %w", err)
	}
	// Per-category floor: the seeded pricing_category_policy row carries the effective floor
	// (base ± category adjuster). When that policy is UNSEEDED, e.g. a migrate-only env with
	// no seed step, where fix #1's cost×1.85 derivation now still PRICES rather than 422-ing
	// on every line, the floor falls back to defaultFloorBps (the legacy 1200 bps). (fix #10)
	floorBps := defaultFloorBps
	if policy != nil {
		floorBps = effectiveFloor(*policy)
	}
	bd.FloorBps = floorBps
	bd.MarginBps = marginBps(finalCents, costInResUOM)

	disabled := (policy != nil && policy.EnforcementDisabled) || (prodPolicy != nil && prodPolicy.EnforcementDisabled)
	if disabled {
		// THE BYPASS REASON (W4-A, migration 0114). Until now the reason was INFERRABLE, you had to
		// know that entity_id being a product id meant "product policy", and inference is exactly
		// what DIB-402 spent a page arguing against. It is now a named, CHECK-constrained value on
		// the row and on the Breakdown, so "how many lines bypassed the floor last month, and why"
		// is a GROUP BY instead of an archaeology exercise.
		//
		// PRODUCT WINS THE ATTRIBUTION when both policies are disabled, matching the entity_id
		// choice immediately below: the narrower policy is the one an operator would go turn off.
		reason := BypassCategoryPolicy
		if prodPolicy != nil && prodPolicy.EnforcementDisabled {
			reason = BypassProductPolicy
		}
		bd.BypassReason = reason
		// NO SILENT BYPASS, record a BYPASS row so a disabled item is observable, EXCEPT on a
		// dry-run (read-only) evaluation, which must not write to the audit log (finding #5).
		if !dryRun {
			entityID := in.CategoryID
			if prodPolicy != nil && prodPolicy.EnforcementDisabled {
				entityID = in.ProductID
			}
			// DIB-402: entityID above says WHICH POLICY disabled the gate; the drill-through says
			// which record was being PRICED. They are different questions and only the second is a
			// place a reader can go, so both are recorded rather than one standing in for the other.
			refType, refID := resolveBypassRef(in)
			_ = s.store.AppendAudit(ctx, AuditEntry{
				EntityType:    "margin_enforcement",
				EntityID:      entityID,
				Action:        "BYPASS",
				FieldName:     "margin_bps",
				NewValue:      fmt.Sprintf("%d", bd.MarginBps),
				UserID:        in.UserID,
				RefEntityType: refType,
				RefEntityID:   refID,
				BypassReason:  reason,
			})
		}
	} else if s.gate != nil {
		// DIB-420. The gate is asked the question WITHOUT the caller's flag, hasApprovedOverride is
		// passed as false, always, so the arithmetic verdict ("is this below the floor?") is decided
		// by the numbers and nothing else. Whether a below-floor price is nevertheless allowed is a
		// SEPARATE decision, made below, from a server-resolved authority. Collapsing the two is what
		// let a request body clear the floor.
		if blk := s.gate.Check(finalCents, costInResUOM, in.BranchID, false, floorBps); blk != nil {
			bd.MarginBps = blk.MarginBps
			bd.FloorBps = blk.FloorBps
			bd.OverrideRequested = in.OverrideRequested
			if grant := s.overrideGrant(ctx, in); grant.Granted {
				// Honoured. The price stands, and WHO cleared it and on WHAT authority is recorded -
				// on the breakdown for the operator and, on a live resolve, on the audit spine. A
				// below-floor price that leaves no trace is the same defect wearing a uniform.
				bd.OverrideAuthority = grant.Source
				bd.BypassReason = bypassReasonForAuthority(grant.Source)
				// A BYPASS row, not a new verb: 0114 already declared APPROVED_OVERRIDE as a bypass
				// reason and the action CHECK admits exactly CREATE/UPDATE/DELETE/BYPASS. 0133 adds
				// the second source. The reader who greps "how did this price clear the floor" finds
				// enforcement-disabled bypasses and authorised overrides in one query.
				if !dryRun {
					refType, refID := resolveBypassRef(in)
					_ = s.store.AppendAudit(ctx, AuditEntry{
						EntityType:    "margin_enforcement",
						EntityID:      in.CategoryID,
						Action:        "BYPASS",
						FieldName:     "margin_bps",
						NewValue:      fmt.Sprintf("%d", bd.MarginBps),
						UserID:        in.UserID,
						RefEntityType: refType,
						RefEntityID:   refID,
						BypassReason:  bd.BypassReason,
					})
				}
			} else {
				bd.Blocked = true
				bd.BlockPolicy = blk.Policy
				bd.Detail = "Below the configured margin floor, manager approval required"
				if in.OverrideRequested {
					// Say that the request was SEEN and refused. Silence here reads as a bug and
					// teaches the counter to keep clicking the box.
					bd.Detail = "Below the configured margin floor, the override request carried no server-side authority, so manager approval is still required"
				}
			}
		}
	}

	return bd, nil
}

// effectiveFloor = the category's stored floor. The "± category adjuster" of the
// 06-23 policy is encoded directly in each category's margin_floor_bps row (so a
// corrected adjuster is a row UPDATE, not a code change). Kept as a function so a
// later per-branch dimension slots in here without touching the resolver.
func effectiveFloor(p CategoryPolicy) int64 { return p.MarginFloorBps }

// selectRule filters candidate rules to a scope + key match, drops out-of-validity
// rows, then applies the TOTAL-ORDER tie-break (priority DESC, valid_from DESC, id ASC)
// and returns the winner, deterministic even when priority AND valid_from collide.
func (s *Service) selectRule(rules []Rule, scope string, in ResolveInput, keyMatch func(Rule) bool) *Rule {
	now := s.now()
	var c []Rule
	for _, r := range rules {
		if r.Scope != scope || !r.IsActive || !keyMatch(r) {
			continue
		}
		if !withinValidity(r, now) {
			continue
		}
		if r.BranchID != "" && in.BranchID != "" && r.BranchID != in.BranchID {
			continue
		}
		c = append(c, r)
	}
	if len(c) == 0 {
		return nil
	}
	sort.SliceStable(c, func(i, j int) bool {
		if c[i].Priority != c[j].Priority {
			return c[i].Priority > c[j].Priority // priority DESC
		}
		vi, vj := validFromSort(c[i]), validFromSort(c[j])
		if !vi.Equal(vj) {
			return vi.After(vj) // valid_from DESC
		}
		return c[i].ID < c[j].ID // id ASC, the final deterministic discriminator
	})
	winner := c[0]
	return &winner
}

// selectBreak finds the SINGLE applicable quantity-break tier whose bucket contains the entered qty
// (converted into the break header's threshold_uom via the catalog ConvertUOM seam), whatever its
// kind; step 6 judges what the matched tier does (a discount may replace the base, a surcharge
// never wins). Product-specific headers are preferred over category headers. A missing
// threshold-UOM (or entered-UOM) conversion for an evaluated break is SURFACED as errNoConversion
// (fix #3), a misconfigured break must fail loud, never silently drop and undercharge.
func (s *Service) selectBreak(breaks []QuantityBreak, in ResolveInput, factor func(string) (float64, error)) (*QuantityBreak, error) {
	if len(breaks) == 0 {
		return nil, nil
	}
	// Prefer product-specific headers over category headers.
	sort.SliceStable(breaks, func(i, j int) bool {
		return breaks[i].ProductID != "" && breaks[j].ProductID == ""
	})
	for i := range breaks {
		b := breaks[i]
		fEnt, err := factor(in.UOMEntered)
		if err != nil {
			return nil, err
		}
		fThr, err := factor(b.ThresholdUOM)
		if err != nil {
			return nil, err
		}
		if fThr == 0 {
			return nil, errNoConversion
		}
		qty := in.QtyEntered * fEnt / fThr
		if qty < b.FromQty {
			continue
		}
		if b.ToQty != nil && qty >= *b.ToQty {
			continue
		}
		return &b, nil
	}
	return nil, nil
}

// isSurchargeBreak reports whether a break value_type is a positive uplift (partial-skid
// surcharge) rather than a discount off the base.
func isSurchargeBreak(valueType string) bool {
	return valueType == BreakSurchargePct || valueType == BreakSurchargeAbs
}

// - wire-in: line-price derivation (built from scratch, no Go order module) -

// LinePrice is the pre-tax price trio a quote/order line derives from the resolver.
// It NEVER includes tax_cents, M5 owns tax.
type LinePrice struct {
	UnitPriceCents int64     `json:"unit_price_cents"`
	PriceUOM       string    `json:"price_uom"`
	LineTotalCents int64     `json:"line_total_cents"`
	QtyBase        float64   `json:"qty_base"`
	Breakdown      Breakdown `json:"breakdown"`
}

// DeriveLinePrice resolves a price and projects it onto the line price trio a quote/
// order line persists (unit_price_cents / price_uom / line_total_cents), pre-tax only.
// The manual-override path is preserved: a blocked line carries Blocked=true + BlockPolicy
// so the caller routes it to the A-20 0016 approval queue instead of committing a
// below-floor price. The order/quote module persists the trio; pricing never writes
// quote_lines/order_lines (zero-trust) and never touches tax_cents.
func (s *Service) DeriveLinePrice(ctx context.Context, in ResolveInput) (LinePrice, error) {
	bd, err := s.ResolvePrice(ctx, in)
	if err != nil {
		return LinePrice{}, err
	}
	return LinePrice{
		UnitPriceCents: bd.FinalCents,
		PriceUOM:       bd.PriceUOM,
		LineTotalCents: bd.LineTotalCents,
		QtyBase:        bd.QtyBase,
		Breakdown:      bd,
	}, nil
}

// - admin CRUD (optimistic lock + audit) -

func (s *Service) CreateRule(ctx context.Context, r Rule, userID string) (Rule, error) {
	if r.Scope == "" || r.CalcType == "" || r.PriceUOM == "" {
		return Rule{}, badReq("scope, calc_type and price_uom are required")
	}
	normalizeRuleCalc(&r)
	normalizeRuleScopeDims(&r)
	if err := validateRule(r); err != nil {
		return Rule{}, err
	}
	r.IsActive = true
	r.Version = 1
	out, err := s.store.CreateRule(ctx, r)
	if err != nil {
		return Rule{}, err // repo maps uq_pricing_rules_active (23505) → errRuleCollision (→409)
	}
	ruleRefType, ruleRefID := ruleRef(out)
	_ = s.store.AppendAudit(ctx, AuditEntry{
		EntityType: "pricing_rule", EntityID: out.ID, Action: "CREATE", UserID: userID,
		RefEntityType: ruleRefType, RefEntityID: ruleRefID,
	})
	return out, nil
}

// GetRule loads one rule for the edit form (US-P08 / route GET /rules/{id}).
func (s *Service) GetRule(ctx context.Context, id string) (Rule, error) {
	if id == "" {
		return Rule{}, badReq("rule id is required")
	}
	return s.store.GetRule(ctx, id)
}

// UpdateRule applies an optimistic-locked patch and audits each changed field. It
// RE-VALIDATES the ABSOLUTE-scope rule + the exactly-one-calc-value rule on the PATCH
// path (not just create, R-3), so a calc-type edit to ABSOLUTE on a TIER_* rule returns
// a friendly 422 and a unique-key collision returns a friendly 409, never a bare 500
// from the DB CHECK / unique index.
func (s *Service) UpdateRule(ctx context.Context, id string, patch RulePatch, expectedVersion int64, userID string) (Rule, error) {
	if id == "" {
		return Rule{}, badReq("rule id is required")
	}
	cur, err := s.store.GetRule(ctx, id)
	if err != nil {
		return Rule{}, err
	}
	next := applyRulePatch(cur, patch)
	normalizeRuleCalc(&next)
	normalizeRuleScopeDims(&next) // clear dims that don't belong to the (possibly edited) scope
	if err := validateRule(next); err != nil {
		return Rule{}, err
	}
	// Compute changed fields for the audit trail from the resolved before→after.
	audits := ruleDiff(cur, next)
	next.Version = cur.Version + 1
	// The version guard is ATOMIC in SaveRule (UPDATE ... WHERE id=$1 AND version=$expected). The
	// GetRule above is only for the audit diff + validation, it is NOT the lock. A racing writer
	// that commits between our read and write makes the guarded UPDATE match 0 rows → errConflict,
	// closing the read-then-check TOCTOU (finding #3).
	saved, err := s.store.SaveRule(ctx, next, expectedVersion)
	if err != nil {
		return Rule{}, err // repo maps 23505 → errRuleCollision (409), 23503 → errFKViolation (422)
	}
	// DIB-402: derive the drill-through from the SAVED row. An edit that re-points a rule at a
	// different product must file its audit trail under where the rule now IS, a reader clicking
	// through from the change lands on the record that now carries it.
	ruleRefType, ruleRefID := ruleRef(saved)
	for _, a := range audits {
		a.EntityType, a.EntityID, a.Action, a.UserID = "pricing_rule", id, "UPDATE", userID
		a.RefEntityType, a.RefEntityID = ruleRefType, ruleRefID
		_ = s.store.AppendAudit(ctx, a)
	}
	return saved, nil
}

// DeleteRule soft-retires a rule (is_active=false) under optimistic version and writes
// a DELETE audit row, the resolver already filters is_active (service.go:selectRule),
// so a retired rule stops matching without losing its history.
func (s *Service) DeleteRule(ctx context.Context, id string, expectedVersion int64, userID string) error {
	if id == "" {
		return badReq("rule id is required")
	}
	cur, err := s.store.GetRule(ctx, id)
	if err != nil {
		return err
	}
	cur.IsActive = false
	cur.Version++
	// Atomic version guard (finding #3): SaveRule's WHERE id=$1 AND version=$expected makes a
	// stale retire match 0 rows → errConflict, without a read-then-check race.
	if _, err := s.store.SaveRule(ctx, cur, expectedVersion); err != nil {
		return err
	}
	delRefType, delRefID := ruleRef(cur)
	_ = s.store.AppendAudit(ctx, AuditEntry{
		EntityType: "pricing_rule", EntityID: id, Action: "DELETE",
		FieldName: "is_active", OldValue: "true", NewValue: "false", UserID: userID,
		RefEntityType: delRefType, RefEntityID: delRefID,
	})
	return nil
}

func (s *Service) ListRules(ctx context.Context, f RuleFilter) ([]Rule, error) {
	return s.store.ListRules(ctx, f)
}

// validateRule enforces the discount-%-only rule (ABSOLUTE only on CUSTOMER_PRODUCT)
// AND exactly-one-calc-value, at the SERVICE layer so both create and patch refuse a
// bad combination before it reaches the DB CHECKs (0023:78-84).
func validateRule(r Rule) error {
	// Scope must be one of the 4 known scopes, validated HERE (422) rather than relying on the
	// DB CHECK (0023:49) to reject a garbage scope as a generic 500 (finding #11).
	switch r.Scope {
	case ScopeCustomerProduct, ScopeCustomerCategory, ScopeTierCategory, ScopeTierDefault:
	default:
		return unprocessable("scope must be one of CUSTOMER_PRODUCT, CUSTOMER_CATEGORY, TIER_CATEGORY, TIER_DEFAULT")
	}
	if r.Scope != ScopeCustomerProduct && r.CalcType == CalcAbsolute {
		return unprocessable("ABSOLUTE price is only allowed on CUSTOMER_PRODUCT (discount-percent-only rule)")
	}
	switch r.CalcType {
	case CalcAbsolute:
		if r.PriceCents == nil {
			return unprocessable("ABSOLUTE rule requires price_cents")
		}
	case CalcPctOffList:
		if r.DiscountBps == nil {
			return unprocessable("PCT_OFF_LIST rule requires discount_bps")
		}
		if err := validateBps("discount_bps", r.DiscountBps); err != nil {
			return err
		}
	case CalcMarkupOnCost:
		if r.MarkupBps == nil {
			return unprocessable("MARKUP_ON_COST rule requires markup_bps")
		}
		if err := validateBps("markup_bps", r.MarkupBps); err != nil {
			return err
		}
	default:
		return unprocessable("calc_type must be ABSOLUTE, PCT_OFF_LIST or MARKUP_ON_COST")
	}
	return nil
}

// validateBps rejects a discount/markup basis-point value outside 0..10000 with a 422 (finding
// #1), mirroring UpsertCategoryPolicy's margin-bps guard. A 15000-bps ("150% off") rule would
// otherwise drive applyPctOff to a negative price; this refuses it before it ever resolves.
func validateBps(field string, v *int64) error {
	if v == nil {
		return nil
	}
	if *v < minCalcBps || *v > maxCalcBps {
		return unprocessable("%s must be between %d and %d basis points", field, minCalcBps, maxCalcBps)
	}
	return nil
}

// normalizeRuleCalc nulls the calc-value fields that do not match calc_type, so the
// exactly-one-value DB CHECK (0023:78-80) can never be tripped by a stale field left
// over from a calc-type change in the editor.
func normalizeRuleCalc(r *Rule) {
	switch r.CalcType {
	case CalcAbsolute:
		r.DiscountBps, r.MarkupBps = nil, nil
	case CalcPctOffList:
		r.PriceCents, r.MarkupBps = nil, nil
	case CalcMarkupOnCost:
		r.PriceCents, r.DiscountBps = nil, nil
	}
}

// normalizeRuleScopeDims clears the scope-KEY dimensions that do NOT belong to the rule's scope
// (finding #2). This is the SERVER-SIDE, authoritative fix for scope-edit dim corruption: when a
// rule's scope is patched (e.g. CUSTOMER_PRODUCT → TIER_CATEGORY) the stale customer_id/product_id
// MUST be cleared, otherwise the uq_pricing_rules_active key (which COALESCEs NULL dims to
// sentinels) keeps the old dims and the edited rule no longer collides with a real same-scope
// peer, silently defeating the determinism guard. branch_id is a cross-cutting filter valid on
// ANY scope (MatchingRules applies it independently), so it is deliberately left untouched here;
// the editor clears it explicitly when the user picks "all branches" (finding #7).
func normalizeRuleScopeDims(r *Rule) {
	switch r.Scope {
	case ScopeTierDefault:
		r.CustomerID, r.CategoryID, r.ProductID = "", "", ""
	case ScopeTierCategory:
		r.CustomerID, r.ProductID = "", ""
	case ScopeCustomerCategory:
		r.CustomerType, r.ProductID = "", ""
	case ScopeCustomerProduct:
		r.CustomerType, r.CategoryID = "", ""
	}
}

// - category margin-policy (the keystone grid, US-P01/02/03) -

func (s *Service) ListCategoryPolicies(ctx context.Context) ([]CategoryPolicy, error) {
	return s.store.ListCategoryPolicies(ctx)
}

// UpsertCategoryPolicy writes a per-category target/floor/enforcement row. The floor ≤
// target invariant is enforced HERE (server-authoritative, mirroring CHECK 0023:20) so
// the UI cannot bypass it, and each changed field is audited old→new with the actor.
func (s *Service) UpsertCategoryPolicy(ctx context.Context, p CategoryPolicy, userID string) (CategoryPolicy, error) {
	if p.CategoryID == "" {
		return CategoryPolicy{}, badReq("category_id is required")
	}
	if p.MarginTargetBps < 0 || p.MarginTargetBps > 10000 || p.MarginFloorBps < 0 || p.MarginFloorBps > 10000 {
		return CategoryPolicy{}, unprocessable("margin must be between 0%% and 100%%")
	}
	if p.MarginFloorBps > p.MarginTargetBps {
		return CategoryPolicy{}, unprocessable("margin floor must be ≤ target")
	}
	cur, err := s.store.CategoryPolicy(ctx, p.CategoryID) // nil when this is the first write
	if err != nil {
		return CategoryPolicy{}, err
	}
	saved, err := s.store.UpsertCategoryPolicy(ctx, p)
	if err != nil {
		return CategoryPolicy{}, err
	}
	action := "CREATE"
	old := CategoryPolicy{}
	if cur != nil {
		action, old = "UPDATE", *cur
	}
	diffs := categoryPolicyDiff(old, saved)
	// A CREATE with no field-level delta (e.g. target=floor=0) still deserves one row so the
	// create is visible. A no-op UPDATE that changes NOTHING must NOT emit a synthetic
	// margin_target_bps row (finding #13), re-saving an identical policy writes zero audit rows.
	if len(diffs) == 0 && action == "CREATE" {
		diffs = append(diffs, AuditEntry{FieldName: "margin_target_bps", NewValue: fmt.Sprintf("%d", saved.MarginTargetBps)})
	}
	for _, a := range diffs {
		a.EntityType, a.EntityID, a.Action, a.UserID = "category_policy", saved.CategoryID, action, userID
		// The policy's PK IS the category, so the drill-through target is exact rather than derived.
		a.RefEntityType, a.RefEntityID = RefCategory, saved.CategoryID
		_ = s.store.AppendAudit(ctx, a)
	}
	return saved, nil
}

func categoryPolicyDiff(old, next CategoryPolicy) []AuditEntry {
	var out []AuditEntry
	if old.MarginTargetBps != next.MarginTargetBps {
		out = append(out, AuditEntry{FieldName: "margin_target_bps", OldValue: fmt.Sprintf("%d", old.MarginTargetBps), NewValue: fmt.Sprintf("%d", next.MarginTargetBps)})
	}
	if old.MarginFloorBps != next.MarginFloorBps {
		out = append(out, AuditEntry{FieldName: "margin_floor_bps", OldValue: fmt.Sprintf("%d", old.MarginFloorBps), NewValue: fmt.Sprintf("%d", next.MarginFloorBps)})
	}
	if old.EnforcementDisabled != next.EnforcementDisabled {
		out = append(out, AuditEntry{FieldName: "margin_enforcement_disabled", OldValue: fmt.Sprintf("%t", old.EnforcementDisabled), NewValue: fmt.Sprintf("%t", next.EnforcementDisabled)})
	}
	return out
}

// - customer overrides (US-P09) -

func (s *Service) ListOverrides(ctx context.Context, customerID string) ([]Override, error) {
	if customerID == "" {
		return nil, badReq("customer_id is required")
	}
	return s.store.ListOverrides(ctx, customerID)
}

// CreateOverride writes a per-customer per-category override. ABSOLUTE is forbidden
// (this IS the customer+category scope, CHECK 0023:164), exactly-one-value is enforced,
// and a duplicate (customer,category) maps to a friendly 409 (UNIQUE 0023:158).
func (s *Service) CreateOverride(ctx context.Context, o Override, userID string) (Override, error) {
	if o.CustomerID == "" || o.CategoryID == "" || o.PriceUOM == "" {
		return Override{}, badReq("customer_id, category_id and price_uom are required")
	}
	normalizeOverrideCalc(&o)
	if err := validateOverride(o); err != nil {
		return Override{}, err
	}
	o.Version = 1
	out, err := s.store.CreateOverride(ctx, o)
	if err != nil {
		return Override{}, err // repo maps 23505 → errOverrideTaken (→409)
	}
	_ = s.store.AppendAudit(ctx, AuditEntry{
		EntityType: "customer_override", EntityID: out.ID, Action: "CREATE", UserID: userID,
		RefEntityType: RefCustomer, RefEntityID: out.CustomerID,
	})
	return out, nil
}

// UpdateOverride patches the editable fields of an override under optimistic version.
func (s *Service) UpdateOverride(ctx context.Context, id string, in Override, expectedVersion int64, userID string) (Override, error) {
	if id == "" {
		return Override{}, badReq("override id is required")
	}
	cur, err := s.store.GetOverride(ctx, id)
	if err != nil {
		return Override{}, err
	}
	next := cur
	next.CalcType, next.PriceCents, next.DiscountBps, next.MarkupBps = in.CalcType, in.PriceCents, in.DiscountBps, in.MarkupBps
	if in.PriceUOM != "" {
		next.PriceUOM = in.PriceUOM
	}
	next.Notes = in.Notes
	normalizeOverrideCalc(&next)
	if err := validateOverride(next); err != nil {
		return Override{}, err
	}
	audits := overrideDiff(cur, next)
	next.Version = cur.Version + 1
	// Atomic version guard (finding #3): the GetOverride read is for the audit diff/validation,
	// not the lock, SaveOverride's WHERE id=$1 AND version=$expected closes the TOCTOU.
	saved, err := s.store.SaveOverride(ctx, next, expectedVersion)
	if err != nil {
		return Override{}, err
	}
	for _, a := range audits {
		a.EntityType, a.EntityID, a.Action, a.UserID = "customer_override", id, "UPDATE", userID
		a.RefEntityType, a.RefEntityID = RefCustomer, saved.CustomerID
		_ = s.store.AppendAudit(ctx, a)
	}
	return saved, nil
}

// DeleteOverride removes an override under an OPTIMISTIC version guard (finding #4), matching
// DeleteRule: a stale delete (the row moved on since the caller loaded it) returns errConflict
// (409) instead of blindly destroying a row the caller never saw. The guard is enforced
// atomically in the repo (DELETE ... WHERE id=$1 AND version=$expected).
func (s *Service) DeleteOverride(ctx context.Context, id string, expectedVersion int64, userID string) error {
	if id == "" {
		return badReq("override id is required")
	}
	// DIB-402: read the row for its drill-through target BEFORE it is gone. Best-effort and
	// deliberately not fatal, a failure here must never turn a legitimate delete into an error;
	// it just costs this one audit row its link.
	var refCustomerID string
	if cur, err := s.store.GetOverride(ctx, id); err == nil {
		refCustomerID = cur.CustomerID
	}
	if err := s.store.DeleteOverride(ctx, id, expectedVersion); err != nil {
		return err
	}
	refType := RefCustomer
	if refCustomerID == "" {
		refType = ""
	}
	_ = s.store.AppendAudit(ctx, AuditEntry{
		EntityType: "customer_override", EntityID: id, Action: "DELETE", UserID: userID,
		RefEntityType: refType, RefEntityID: refCustomerID,
	})
	return nil
}

// validateOverride enforces ABSOLUTE-forbidden (0023:164) + exactly-one-value.
func validateOverride(o Override) error {
	if o.CalcType == CalcAbsolute {
		return unprocessable("ABSOLUTE is forbidden on a customer override, use a CUSTOMER_PRODUCT rule for a negotiated fixed price")
	}
	switch o.CalcType {
	case CalcPctOffList:
		if o.DiscountBps == nil {
			return unprocessable("PCT_OFF_LIST override requires discount_bps")
		}
		if err := validateBps("discount_bps", o.DiscountBps); err != nil {
			return err
		}
	case CalcMarkupOnCost:
		if o.MarkupBps == nil {
			return unprocessable("MARKUP_ON_COST override requires markup_bps")
		}
		if err := validateBps("markup_bps", o.MarkupBps); err != nil {
			return err
		}
	default:
		return unprocessable("override calc_type must be PCT_OFF_LIST or MARKUP_ON_COST")
	}
	return nil
}

func normalizeOverrideCalc(o *Override) {
	switch o.CalcType {
	case CalcPctOffList:
		o.PriceCents, o.MarkupBps = nil, nil
	case CalcMarkupOnCost:
		o.PriceCents, o.DiscountBps = nil, nil
	}
}

func overrideDiff(cur, next Override) []AuditEntry {
	var out []AuditEntry
	add := func(field, old, nw string) {
		if old != nw {
			out = append(out, AuditEntry{FieldName: field, OldValue: old, NewValue: nw})
		}
	}
	add("calc_type", cur.CalcType, next.CalcType)
	add("discount_bps", i64s(cur.DiscountBps), i64s(next.DiscountBps))
	add("markup_bps", i64s(cur.MarkupBps), i64s(next.MarkupBps))
	add("price_uom", cur.PriceUOM, next.PriceUOM)
	add("notes", cur.Notes, next.Notes)
	return out
}

// - quantity-break admin (A-07 Tier-B editor) -

func (s *Service) ListQuantityBreaks(ctx context.Context) ([]QuantityBreakHeader, error) {
	return s.store.ListQuantityBreakHeaders(ctx)
}

// GetQuantityBreak loads one header + its tiers for the edit form.
func (s *Service) GetQuantityBreak(ctx context.Context, id string) (QuantityBreakHeader, error) {
	if id == "" {
		return QuantityBreakHeader{}, badReq("quantity break id is required")
	}
	return s.store.GetQuantityBreakHeader(ctx, id)
}

// CreateQuantityBreak validates + persists a header and its tiers, sets version 1 / is_active, and
// audits the actor. A duplicate code maps to a friendly 409 (errBreakCodeTaken); a nonexistent
// product/category FK to a friendly 422 (errFKViolation).
func (s *Service) CreateQuantityBreak(ctx context.Context, h QuantityBreakHeader, userID string) (QuantityBreakHeader, error) {
	if err := validateQuantityBreak(h); err != nil {
		return QuantityBreakHeader{}, err
	}
	h.IsActive = true
	h.Version = 1
	out, err := s.store.CreateQuantityBreakHeader(ctx, h)
	if err != nil {
		return QuantityBreakHeader{}, err
	}
	qbRefType, qbRefID := breakRef(out)
	_ = s.store.AppendAudit(ctx, AuditEntry{
		EntityType: "quantity_break", EntityID: out.ID, Action: "CREATE", UserID: userID,
		RefEntityType: qbRefType, RefEntityID: qbRefID,
	})
	return out, nil
}

// UpdateQuantityBreak re-validates then replaces a header + its tiers under an ATOMIC optimistic
// version guard (SaveQuantityBreakHeader's WHERE id=$1 AND version=$expected), and audits the actor.
func (s *Service) UpdateQuantityBreak(ctx context.Context, id string, h QuantityBreakHeader, expectedVersion int64, userID string) (QuantityBreakHeader, error) {
	if id == "" {
		return QuantityBreakHeader{}, badReq("quantity break id is required")
	}
	cur, err := s.store.GetQuantityBreakHeader(ctx, id)
	if err != nil {
		return QuantityBreakHeader{}, err
	}
	h.ID = id
	h.IsActive = cur.IsActive // retire is a separate (delete) path
	if err := validateQuantityBreak(h); err != nil {
		return QuantityBreakHeader{}, err
	}
	h.Version = cur.Version + 1
	saved, err := s.store.SaveQuantityBreakHeader(ctx, h, expectedVersion)
	if err != nil {
		return QuantityBreakHeader{}, err
	}
	qbRefType, qbRefID := breakRef(saved)
	_ = s.store.AppendAudit(ctx, AuditEntry{
		EntityType: "quantity_break", EntityID: id, Action: "UPDATE", UserID: userID,
		RefEntityType: qbRefType, RefEntityID: qbRefID,
	})
	return saved, nil
}

// DeleteQuantityBreak soft-retires a header (is_active=false) under an optimistic version guard and
// audits, the resolver filters is_active, so a retired schedule stops matching without losing history.
func (s *Service) DeleteQuantityBreak(ctx context.Context, id string, expectedVersion int64, userID string) error {
	if id == "" {
		return badReq("quantity break id is required")
	}
	// DIB-402: same shape as DeleteOverride, capture the target while the header is still
	// readable, best-effort, never fatal.
	var qbRefType, qbRefID string
	if cur, err := s.store.GetQuantityBreakHeader(ctx, id); err == nil {
		qbRefType, qbRefID = breakRef(cur)
	}
	if err := s.store.DeleteQuantityBreakHeader(ctx, id, expectedVersion); err != nil {
		return err
	}
	_ = s.store.AppendAudit(ctx, AuditEntry{
		EntityType: "quantity_break", EntityID: id, Action: "DELETE",
		FieldName: "is_active", OldValue: "true", NewValue: "false", UserID: userID,
		RefEntityType: qbRefType, RefEntityID: qbRefID,
	})
	return nil
}

// validateQuantityBreak enforces the editor invariants at the SERVICE layer (before the DB CHECKs):
// exactly-one scope (product XOR category), at least one tier, strictly-ascending non-overlapping
// thresholds, and a well-formed value per tier, each mapped to a friendly 400/422.
func validateQuantityBreak(h QuantityBreakHeader) error {
	if h.Code == "" || h.Name == "" || h.ThresholdUOM == "" {
		return badReq("code, name and threshold_uom are required")
	}
	if (h.ProductID == "") == (h.CategoryID == "") {
		return unprocessable("a quantity break must be scoped to EXACTLY ONE of product or category")
	}
	if len(h.Items) == 0 {
		return unprocessable("a quantity break needs at least one tier")
	}
	var prevFrom float64
	var prevTo *float64
	for i, it := range h.Items {
		if err := validateBreakItem(it); err != nil {
			return err
		}
		if it.FromQty < 0 {
			return unprocessable("tier %d: from_qty must be ≥ 0", i+1)
		}
		if it.ToQty != nil && *it.ToQty <= it.FromQty {
			return unprocessable("tier %d: to_qty must be greater than from_qty", i+1)
		}
		if i > 0 {
			if it.FromQty <= prevFrom {
				return unprocessable("tier thresholds must be strictly ascending")
			}
			if prevTo != nil && it.FromQty < *prevTo {
				return unprocessable("tier %d overlaps the previous tier", i+1)
			}
		}
		prevFrom, prevTo = it.FromQty, it.ToQty
	}
	return nil
}

// validateBreakItem enforces value_type ∈ the allowed adjustment set + exactly-one-value (reusing
// the validateBps 0..10000 guard for the bps fields), so a bad tier returns 422 before the DB CHECK.
func validateBreakItem(it QuantityBreakItem) error {
	if !breakValueTypes[it.ValueType] {
		return unprocessable("value_type must be one of PCT_OFF, ABS_OFF, SURCHARGE_PCT, SURCHARGE_ABS")
	}
	if it.PriceUOM == "" {
		return badReq("each tier requires a price_uom")
	}
	switch it.ValueType {
	case BreakPctOff:
		if it.DiscountBps == nil {
			return unprocessable("PCT_OFF tier requires discount_bps")
		}
		if err := validateBps("discount_bps", it.DiscountBps); err != nil {
			return err
		}
		if it.DiscountCents != nil || it.SurchargeBps != nil || it.SurchargeCents != nil {
			return unprocessable("PCT_OFF tier must set only discount_bps")
		}
	case BreakAbsOff:
		if it.DiscountCents == nil {
			return unprocessable("ABS_OFF tier requires discount_cents")
		}
		if *it.DiscountCents < 0 {
			return unprocessable("discount_cents must be ≥ 0")
		}
		if it.DiscountBps != nil || it.SurchargeBps != nil || it.SurchargeCents != nil {
			return unprocessable("ABS_OFF tier must set only discount_cents")
		}
	case BreakSurchargePct:
		if it.SurchargeBps == nil {
			return unprocessable("SURCHARGE_PCT tier requires surcharge_bps")
		}
		if err := validateBps("surcharge_bps", it.SurchargeBps); err != nil {
			return err
		}
		if it.DiscountBps != nil || it.DiscountCents != nil || it.SurchargeCents != nil {
			return unprocessable("SURCHARGE_PCT tier must set only surcharge_bps")
		}
	case BreakSurchargeAbs:
		if it.SurchargeCents == nil {
			return unprocessable("SURCHARGE_ABS tier requires surcharge_cents")
		}
		if *it.SurchargeCents < 0 {
			return unprocessable("surcharge_cents must be ≥ 0")
		}
		if it.DiscountBps != nil || it.DiscountCents != nil || it.SurchargeBps != nil {
			return unprocessable("SURCHARGE_ABS tier must set only surcharge_cents")
		}
	}
	return nil
}

// - audit read (US-P12) + low-margin classifier (US-P13) -

func (s *Service) ListAudit(ctx context.Context, f AuditFilter) ([]AuditLogRow, error) {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	return s.store.ListAudit(ctx, f)
}

// classifyMargin is the pure low-margin classifier (US-P13, test U9): "critical" when
// the resolved margin is below the hard floor, "warn" when below the category target but
// at/above the floor, and not breaching otherwise.
func classifyMargin(marginBps, targetBps, floorBps int64) (level string, breaching bool) {
	switch {
	case marginBps < floorBps:
		return "critical", true
	case marginBps < targetBps:
		return "warn", true
	default:
		return "", false
	}
}

// LowMarginReport runs each supplied line through the resolver and returns only the
// breaching lines (margin < target/floor), worst-first. It is a READ-ONLY batch preview
// over caller-supplied contexts, NOT the principal's daily live-sales report (D-PA-3), which is a
// reporting-module deliverable. A line that fails to resolve (e.g. no anchor) is skipped
// rather than failing the whole batch. Tax is never computed (pre-tax engine, M5 owns tax).
func (s *Service) LowMarginReport(ctx context.Context, inputs []ResolveInput) ([]LowMarginEntry, error) {
	out := make([]LowMarginEntry, 0, len(inputs))
	for _, in := range inputs {
		if in.CategoryID == "" && s.cat != nil {
			if cid, err := s.cat.GetCategory(ctx, in.ProductID); err == nil {
				in.CategoryID = cid
			}
		}
		// Validate up front. A non-positive qty or a negative cost is a BAD line, it is SURFACED
		// as an invalid entry, NEVER silently dropped (finding #6), so a dropped line stays visible
		// and is never indistinguishable from a clean one.
		if in.QtyEntered <= 0 {
			out = append(out, invalidEntry(in, "qty_entered must be > 0"))
			continue
		}
		if in.CostCents < 0 {
			out = append(out, invalidEntry(in, "cost_cents must be ≥ 0"))
			continue
		}
		// Dry-run resolve: a read-only preview must NOT write to the audit log (finding #5).
		bd, err := s.resolve(ctx, in, true, false)
		if err != nil {
			out = append(out, invalidEntry(in, resolveErrReason(err)))
			continue
		}
		// Seed the target from the category policy, else the DEFAULT TARGET band, NOT the floor
		// constant (finding #12), so a no-policy category is graded against a real target.
		target, floor := defaultTargetBps, bd.FloorBps
		if pol, perr := s.store.CategoryPolicy(ctx, in.CategoryID); perr == nil && pol != nil {
			target = pol.MarginTargetBps
		}
		level, breaching := classifyMargin(bd.MarginBps, target, floor)
		if !breaching {
			continue // a clean, valid line is omitted, only breaching + invalid lines surface
		}
		out = append(out, LowMarginEntry{
			ProductID: in.ProductID, CustomerID: in.CustomerID, CategoryID: in.CategoryID,
			FinalCents: bd.FinalCents, MarginBps: bd.MarginBps, TargetBps: target,
			FloorBps: floor, Level: level, Source: bd.Source, PriceUOM: bd.PriceUOM,
		})
	}
	// Worst-first among valid breaching lines; invalid lines group at the end so they are visible
	// without skewing the margin ordering (their MarginBps is 0).
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Invalid != out[j].Invalid {
			return !out[i].Invalid
		}
		return out[i].MarginBps < out[j].MarginBps
	})
	return out, nil
}

// invalidEntry builds a SURFACED invalid line for the preview (finding #6), a bad/unresolvable
// line is reported with a reason, never dropped.
func invalidEntry(in ResolveInput, reason string) LowMarginEntry {
	return LowMarginEntry{
		ProductID: in.ProductID, CustomerID: in.CustomerID, CategoryID: in.CategoryID,
		Invalid: true, Reason: reason,
	}
}

// resolveErrReason renders a typed resolve error as a short, human-readable cause for an invalid
// preview line (kept terse, the preview is a triage tool, not the resolve endpoint).
func resolveErrReason(err error) string {
	switch {
	case errors.Is(err, errNoListPrice):
		return "no retail anchor (list price) for product"
	case errors.Is(err, errNoConversion):
		return "no uom conversion factor"
	case errors.Is(err, errNotFound):
		return "product not found"
	default:
		return err.Error()
	}
}

// LowMarginScan is the principal's AUTO-POPULATED daily low-margin report (D-PA-3): it grades the ACTUAL
// sold price of every COMMITTED sale line in [from, to) (optionally scoped to one branch) against
// the product's CURRENT per-branch weighted-average cost, and returns the breaching rows
// (warn/critical) worst-first plus any cost-missing lines surfaced as invalid "no cost on file".
//
// Unlike LowMarginReport this does NOT re-run the resolver, grading the re-derived engine price
// would MISS a manual discount below floor, which is the entire point of the report. It grades the
// real charged price (order_lines.unit_price_cents). Cost is converted BASE→price_uom the SAME way
// the resolver does, roundCents(cost_base × factor(price_uom)) via the shared uomFactorFor helper -
// and classified through the SAME classifyMargin + category-policy resolution (policy row when
// present, else the default target/floor band). Tax is never computed (pre-tax engine; M5 owns tax).
//
// margin_bps = ((unit_price_cents − costInPriceUOM) × 10000) / unit_price_cents  (price ≤ 0 → invalid;
// cost > price → negative margin → critical). Cost-missing / non-positive-price lines are SURFACED as
// invalid, never omitted (design #5: a coalesced cost=0 would render 100% margin and hide the blindness).
func (s *Service) LowMarginScan(ctx context.Context, from, to time.Time, branchID string) ([]LowMarginScanRow, error) {
	if from.IsZero() || to.IsZero() {
		return nil, badReq("from and to are required")
	}
	if !to.After(from) {
		return nil, badReq("to must be after from")
	}
	if s.scan == nil {
		return nil, errNoDB // the live-sales feed seam is unwired (DB-free build)
	}
	lines, err := s.scan.LowMarginSalesLines(ctx, from, to, branchID)
	if err != nil {
		return nil, fmt.Errorf("low-margin scan: %w", err)
	}
	out := make([]LowMarginScanRow, 0, len(lines))
	for _, ln := range lines {
		row := LowMarginScanRow{
			OrderLineID: ln.OrderLineID, OrderID: ln.OrderID, OrderNo: ln.OrderNo, SoldAt: ln.SoldAt,
			BranchID: ln.BranchID, CustomerID: ln.CustomerID, CustomerType: ln.CustomerType,
			ProductID: ln.ProductID, SKU: ln.SKU, ProductName: ln.ProductName,
			QtyEntered: ln.QtyEntered, UOMEntered: ln.UOMEntered,
			UnitPriceCents: ln.UnitPriceCents, PriceUOM: ln.PriceUOM,
		}
		// Category target/floor via the SAME resolution the classifier uses: the policy row when
		// present, else the default target/floor band (finding #12, a no-policy category is graded
		// against a real target, not the floor).
		target, floor := defaultTargetBps, defaultFloorBps
		if ln.CategoryID != "" {
			if pol, perr := s.store.CategoryPolicy(ctx, ln.CategoryID); perr == nil && pol != nil {
				target, floor = pol.MarginTargetBps, effectiveFloor(*pol)
			}
		}
		row.TargetBps, row.FloorBps = target, floor

		// Cost-missing → SURFACED invalid "no cost on file" (never omitted). A missing/zero cost is
		// not a real cost; treating it as 0 would render a fake 100% margin and hide the blindness.
		if ln.AvgUnitCostCents == nil || *ln.AvgUnitCostCents <= 0 {
			row.Level, row.Reason = "invalid", "no cost on file for this product at this branch"
			out = append(out, row)
			continue
		}
		// A non-positive charged price cannot be graded (margin denominator) → SURFACED invalid.
		if ln.UnitPriceCents <= 0 {
			row.Level, row.Reason = "invalid", "sold price must be greater than 0"
			out = append(out, row)
			continue
		}
		// Convert the per-BASE-uom WAC into the line's price_uom exactly as the resolver does.
		f, ferr := s.uomFactorFor(ctx, ln.ProductID, ln.PriceUOM)
		if ferr != nil {
			row.Level, row.Reason = "invalid", resolveErrReason(ferr)
			out = append(out, row)
			continue
		}
		costInPriceUOM := roundCents(float64(*ln.AvgUnitCostCents) * f)
		row.CostInPriceUOMCents = costInPriceUOM
		row.MarginBps = marginBps(ln.UnitPriceCents, costInPriceUOM)
		level, breaching := classifyMargin(row.MarginBps, target, floor)
		if !breaching {
			continue // a healthy line (margin ≥ target) is omitted, proves the filter
		}
		row.Level = level
		out = append(out, row)
	}
	// Worst-first among valid breaching rows; invalid ("no cost") rows group at the end so they stay
	// visible without skewing the margin ordering (their MarginBps is 0).
	sort.SliceStable(out, func(i, j int) bool {
		iInvalid, jInvalid := out[i].Level == "invalid", out[j].Level == "invalid"
		if iInvalid != jInvalid {
			return !iInvalid
		}
		return out[i].MarginBps < out[j].MarginBps
	})
	return out, nil
}

// - helpers -

func withinValidity(r Rule, now time.Time) bool {
	if r.ValidFrom != nil && now.Before(*r.ValidFrom) {
		return false
	}
	if r.ValidTo != nil && !now.Before(*r.ValidTo) {
		return false // valid_to is exclusive
	}
	return true
}

func validFromSort(r Rule) time.Time {
	if r.ValidFrom == nil {
		return time.Unix(0, 0) // NULL valid_from sorts as 'epoch' (matches the SQL ORDER BY)
	}
	return *r.ValidFrom
}

func isNoConversion(err error) bool {
	// The pricing sentinel (fake converter) AND the real product.ErrNoConversion both mean
	// "no factor", typed errors.Is, not the brittle message-tail match (fix #9).
	return err != nil && (errors.Is(err, errNoConversion) || errors.Is(err, product.ErrNoConversion))
}

func applyPctOff(base, discountBps int64) int64 {
	return mulDivRoundHalfUp(base, 10000-discountBps, 10000)
}
func applyMarkup(cost, markupBps int64) int64 { return mulDivRoundHalfUp(cost, 10000+markupBps, 10000) }
func applySurchargePct(base, surchargeBps int64) int64 {
	return mulDivRoundHalfUp(base, 10000+surchargeBps, 10000)
}

// mulDivRoundHalfUp computes round(v*num/den) with half-up rounding, in integer cents.
func mulDivRoundHalfUp(v, num, den int64) int64 {
	if den == 0 {
		return 0
	}
	n := v * num
	if n >= 0 {
		return (n + den/2) / den
	}
	return -((-n + den/2) / den)
}

func roundCents(x float64) int64 {
	if x >= 0 {
		return int64(math.Floor(x + 0.5))
	}
	return int64(math.Ceil(x - 0.5))
}

func bpsOff(orig, final int64) int64 {
	if orig <= 0 {
		return 0
	}
	return ((orig - final) * 10000) / orig
}

func marginBps(price, cost int64) int64 {
	if price <= 0 {
		return 0
	}
	return ((price - cost) * 10000) / price
}

func ruleDetail(r Rule, fallback string) string {
	if r.DetailText != "" {
		return r.DetailText
	}
	return fallback
}

// PercentFromBps renders a basis-point integer as the percentage a counter says out loud:
// 1500 -> "15%", 2250 -> "22.5%", 625 -> "6.25%". Trailing zeros are trimmed, a discount is a rule,
// not a measurement, and "15.00% off list" reads like the latter.
//
// DIB-445 asked the Check Quote screen to stop speaking basis points. Breakdown DETAIL strings are
// the other half of that sweep: they are prose written for a human (they render as the "DETAIL" line
// under a priced POS line), and they were the only ones left in this package still carrying the
// storage unit into the copy. Basis points remain the storage and wire unit everywhere else.
func PercentFromBps(bps int64) string {
	whole, frac := bps/100, bps%100
	if frac < 0 {
		frac = -frac
	}
	switch {
	case frac == 0:
		return fmt.Sprintf("%d%%", whole)
	case frac%10 == 0:
		return fmt.Sprintf("%s.%d%%", signedWhole(bps, whole), frac/10)
	default:
		return fmt.Sprintf("%s.%02d%%", signedWhole(bps, whole), frac)
	}
}

// signedWhole keeps the minus sign on a value whose whole part rounds to zero (-50 bps is "-0.5%",
// not "0.5%"): integer division of -50/100 is 0, and the sign lives only in the remainder.
func signedWhole(bps, whole int64) string {
	if bps < 0 && whole == 0 {
		return "-0"
	}
	return fmt.Sprintf("%d", whole)
}

func overrideDetail(o Override) string {
	if o.Notes != "" {
		return o.Notes
	}
	if o.DiscountBps != nil {
		return fmt.Sprintf("Account override: %s off list", PercentFromBps(*o.DiscountBps))
	}
	return "Account override"
}

func i64s(p *int64) string {
	if p == nil {
		return ""
	}
	return fmt.Sprintf("%d", *p)
}

// ruleDiff emits an audit entry per changed field, computed from the resolved
// before→after rule (so priority 0, is_active and calc-type edits are all captured).
func ruleDiff(cur, next Rule) []AuditEntry {
	var out []AuditEntry
	add := func(field, old, nw string) {
		if old != nw {
			out = append(out, AuditEntry{FieldName: field, OldValue: old, NewValue: nw})
		}
	}
	add("discount_bps", i64s(cur.DiscountBps), i64s(next.DiscountBps))
	add("price_cents", i64s(cur.PriceCents), i64s(next.PriceCents))
	add("markup_bps", i64s(cur.MarkupBps), i64s(next.MarkupBps))
	add("calc_type", cur.CalcType, next.CalcType)
	add("price_uom", cur.PriceUOM, next.PriceUOM)
	add("detail_text", cur.DetailText, next.DetailText)
	if cur.Priority != next.Priority {
		add("priority", fmt.Sprintf("%d", cur.Priority), fmt.Sprintf("%d", next.Priority))
	}
	if cur.IsActive != next.IsActive {
		add("is_active", fmt.Sprintf("%t", cur.IsActive), fmt.Sprintf("%t", next.IsActive))
	}
	return out
}

// applyRulePatch overlays a sparse RulePatch (pointer = set, nil = leave) onto the
// current rule. Unlike the old Rule-valued patch it CAN set priority=0, retire a rule
// (is_active=false), edit the validity window, and edit the scope dims (R-3).
func applyRulePatch(cur Rule, p RulePatch) Rule {
	n := cur
	if p.Scope != nil {
		n.Scope = *p.Scope
	}
	if p.CustomerID != nil {
		n.CustomerID = *p.CustomerID
	}
	if p.CustomerType != nil {
		n.CustomerType = *p.CustomerType
	}
	if p.CategoryID != nil {
		n.CategoryID = *p.CategoryID
	}
	if p.ProductID != nil {
		n.ProductID = *p.ProductID
	}
	if p.BranchID != nil {
		n.BranchID = *p.BranchID
	}
	if p.CalcType != nil {
		n.CalcType = *p.CalcType
	}
	if p.PriceCents != nil {
		n.PriceCents = p.PriceCents
	}
	if p.DiscountBps != nil {
		n.DiscountBps = p.DiscountBps
	}
	if p.MarkupBps != nil {
		n.MarkupBps = p.MarkupBps
	}
	if p.PriceUOM != nil {
		n.PriceUOM = *p.PriceUOM
	}
	if p.Priority != nil {
		n.Priority = *p.Priority
	}
	// Validity bounds are tri-state (finding #7): Clear* wins → set NULL; else a non-nil pointer
	// sets the value; else leave unchanged. This lets the editor broaden a time-boxed rule back to
	// always-valid by clearing a bound, which a plain pointer (nil = "no edit") could not express.
	if p.ClearValidFrom {
		n.ValidFrom = nil
	} else if p.ValidFrom != nil {
		n.ValidFrom = p.ValidFrom
	}
	if p.ClearValidTo {
		n.ValidTo = nil
	} else if p.ValidTo != nil {
		n.ValidTo = p.ValidTo
	}
	if p.DetailText != nil {
		n.DetailText = *p.DetailText
	}
	if p.IsActive != nil {
		n.IsActive = *p.IsActive
	}
	return n
}

// requireBreakValue keeps the loud failure on a misconfigured break: the matching tier must carry
// exactly the value its value_type names, else 400/422 before any arithmetic (fix #3: a
// misconfigured break fails loud, never silently drops and undercharges).
func requireBreakValue(b QuantityBreak) error {
	switch b.ValueType {
	case BreakPctOff:
		if b.DiscountBps == nil {
			return badReq("PCT_OFF break missing discount_bps")
		}
	case BreakAbsOff:
		if b.DiscountCents == nil {
			return badReq("ABS_OFF break missing discount_cents")
		}
	case BreakSurchargePct:
		if b.SurchargeBps == nil {
			return badReq("SURCHARGE_PCT break missing surcharge_bps")
		}
	case BreakSurchargeAbs:
		if b.SurchargeCents == nil {
			return badReq("SURCHARGE_ABS break missing surcharge_cents")
		}
	default:
		return badReq("unsupported break value_type %q", b.ValueType)
	}
	return nil
}

// breakPriceOffAnchor prices a matched break as ONE single price computed off the RETAIL ANCHOR
// (already converted into the result price_uom), D1's replace arithmetic: the break's adjustment
// stands alone, the tier is never compounded underneath it. A surcharge tier prices its uplifted
// anchor purely so the caller's held-note can name a real number: under the replace rule an uplift
// can never win the cheapest-wins contest (ADR-0015). requireBreakValue has already guaranteed the
// value field for the value_type is present.
func breakPriceOffAnchor(b QuantityBreak, anchorCents int64) int64 {
	switch b.ValueType {
	case BreakPctOff:
		return applyPctOff(anchorCents, *b.DiscountBps)
	case BreakAbsOff:
		c := anchorCents - *b.DiscountCents
		if c < 0 {
			c = 0 // a discount NEVER drives the price negative
		}
		return c
	case BreakSurchargePct:
		return applySurchargePct(anchorCents, *b.SurchargeBps)
	case BreakSurchargeAbs:
		return anchorCents + *b.SurchargeCents
	}
	return anchorCents // unreachable behind requireBreakValue
}

// appendDetailNote keeps the base source's detail and appends a break note, so the breakdown
// shows BOTH the base level AND what the break step did with it.
func appendDetailNote(baseDetail, note string) string {
	if baseDetail == "" {
		return note
	}
	return baseDetail + " · " + note
}

// breakHeldReasonNote is what the breakdown says when a matched break is kept out of the contest
// for a stated reason rather than beaten by a number: a surcharge's structural exclusion, or no
// non-zero anchor to price the break from.
func breakHeldReasonNote(b QuantityBreak, reason string) string {
	label := b.Name
	if label == "" {
		if isSurchargeBreak(b.ValueType) {
			label = "partial-skid surcharge"
		} else {
			label = "volume break"
		}
	}
	return label + " not applied, " + reason
}

// breakHeldNoteWorthy reports whether a break that lost the contest offered anything worth
// explaining: a zero-value band (the seeded schedules' 0 bps floor below the first threshold)
// never could beat anything, so explaining its non-application would be noise on every small line.
func breakHeldNoteWorthy(b QuantityBreak) bool {
	switch b.ValueType {
	case BreakPctOff:
		return b.DiscountBps != nil && *b.DiscountBps != 0
	case BreakAbsOff:
		return b.DiscountCents != nil && *b.DiscountCents != 0
	}
	return false // surcharges never reach the held-with-numbers branch
}

// breakHeldNote is what the breakdown says when a break matched the line's quantity and the
// replace rule left it out: the customer's standing price already beats it, or it is an uplift,
// which can never win the cheapest-wins contest. Silence here reads as a bug the same way a
// silenced sale sign does (the promo precedent).
func breakHeldNote(b QuantityBreak, priceCents, breakCents int64) string {
	label := b.Name
	if label == "" {
		if isSurchargeBreak(b.ValueType) {
			label = "partial-skid surcharge"
		} else {
			label = "volume break"
		}
	}
	return fmt.Sprintf("%s not applied, your price %s already beats the break %s",
		label, moneyStr(priceCents), moneyStr(breakCents))
}

// breakNote renders the adjustment the winning break applies, signed for direction (− discount /
// + surcharge). A surcharge never wins under D1, so the "+ surcharge" arm renders only in the
// held-note arithmetic and in tests that pin the renderer itself.
func breakNote(b QuantityBreak) string {
	label := b.Name
	if label == "" {
		if isSurchargeBreak(b.ValueType) {
			label = "partial-skid surcharge"
		} else {
			label = "volume break"
		}
	}
	var amt string
	switch b.ValueType {
	case BreakPctOff:
		if b.DiscountBps != nil {
			amt = fmt.Sprintf("−%s", pctStr(*b.DiscountBps))
		}
	case BreakAbsOff:
		if b.DiscountCents != nil {
			amt = fmt.Sprintf("−%s", moneyStr(*b.DiscountCents))
		}
	case BreakSurchargePct:
		if b.SurchargeBps != nil {
			amt = fmt.Sprintf("+%s", pctStr(*b.SurchargeBps))
		}
	case BreakSurchargeAbs:
		if b.SurchargeCents != nil {
			amt = fmt.Sprintf("+%s", moneyStr(*b.SurchargeCents))
		}
	}
	if amt == "" {
		return "+ " + label
	}
	return fmt.Sprintf("+ %s (%s)", label, amt)
}

func pctStr(bps int64) string {
	whole := bps / 100
	frac := bps % 100
	if frac == 0 {
		return fmt.Sprintf("%d%%", whole)
	}
	return fmt.Sprintf("%d.%02d%%", whole, frac)
}

func moneyStr(cents int64) string { return fmt.Sprintf("CA$%d.%02d", cents/100, cents%100) }
