package pricing

// promo.go, DIB-70. Seasonal promotions + clearance, and their place in the waterfall.
//
// THE PRECEDENCE RULING (the "RULED interplay" this ticket asked for)
// ------------------------------------------------------------------
// The Gable convention is stated as: contract > promo > quantity-break > list. Read against the
// resolver that already exists, that sentence is describing TWO different mechanisms with one
// ">" operator, so it needs unpacking before it can be implemented:
//
//   * contract, promo and list are BASE-SELECTION levels, they compete to be the price the line
//     starts from, and exactly one of them wins.
//   * quantity-break is NOT a competing level in this codebase and has not been since the STACKING
//     fix (service.go step 6): the matching break is an ADJUSTMENT applied ON TOP of whatever base
//     won. Making it a base level again would re-introduce the bug that fix removed, a bulk break
//     wiping a contractor's tier discount.
//
// So the ruling implemented here is:
//
//   1. CUSTOMER_PRODUCT contract price wins outright and is FINAL. No promo, no break, no override.
//      A negotiated per-product price is a promise; a "sale" that changes it is a broken promise in
//      either direction.
//   2. PROMO (and clearance-with-a-price, which is the most specific promo there is) outranks the
//      whole customer-override / tier / list waterfall for BASE selection, subject to (2a).
//   2a. THE NEVER-WORSE GUARD. A promo becomes the base only when it is CHEAPER than the base the
//      ordinary waterfall would have produced. Strict precedence without this guard means a 10%-off
//      June promo RAISES the price for a contractor already sitting on an 18% tier discount, which
//      is the single most embarrassing thing a pricing engine can do. When the tier base wins, the
//      Source stays the tier level and the Detail records that the promo was considered and did not
//      beat it, silence would look like the promo was never configured.
//   3. QUANTITY-BREAK stacks on whichever base won (existing behaviour, unchanged), except on the
//      contract path where the contract is final (existing behaviour, unchanged).
//   4. LIST is the base of last resort (existing behaviour, unchanged).
//   5. THE MARGIN GATE EVALUATES THE FINAL RESOLVED PRICE, after promo AND after the break. It is
//      not evaluated on the promo base and then again on the broken price; there is one gate call
//      and its argument is the number the customer would be charged. A promo that clears the floor
//      alone and sinks below it once a volume break stacks on top is BLOCKED, because the blocked
//      thing is the sale, not the promo.
//
// Clearance (DIB-154) folds into step 2 rather than being a sixth level: a clearance row with a
// price is treated as a product-scoped promotion with maximum specificity, and a clearance row
// without one is a BADGE that rides on the Breakdown so POS search and quote lines can mark it.

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// Promo calc types (mirrors the 0111 CHECK).
const (
	PromoPctOff     = "PCT_OFF"     // discount_bps off the base
	PromoAmtOff     = "AMT_OFF"     // discount_cents off the base
	PromoFixedPrice = "FIXED_PRICE" // price_cents replaces the base
)

var promoCalcTypes = map[string]bool{PromoPctOff: true, PromoAmtOff: true, PromoFixedPrice: true}

// SourcePromotion is the Breakdown.Source value when a promotion (or a priced clearance row) won
// base selection. It is a DISTINCT source, not a flavour of TIER_CATEGORY, because a counter clerk
// reading the breakdown has to be able to tell a customer "that's the June sale price" and a
// manager reading the low-margin report has to be able to tell a promo-driven thin line from a
// mis-authored tier rule.
const SourcePromotion = "PROMOTION"

// Promotion is one pricing_promotions row. Empty strings for unset nullable scope dims, matching
// the Rule convention in this package.
type Promotion struct {
	ID            string    `json:"id"`
	Code          string    `json:"code"`
	Name          string    `json:"name"`
	ProductID     string    `json:"product_id,omitempty"`
	CategoryID    string    `json:"category_id,omitempty"`
	BranchID      string    `json:"branch_id,omitempty"`
	StartsAt      time.Time `json:"starts_at"`
	EndsAt        time.Time `json:"ends_at"`
	CalcType      string    `json:"calc_type"`
	DiscountBps   *int64    `json:"discount_bps,omitempty"`
	DiscountCents *int64    `json:"discount_cents,omitempty"`
	PriceCents    *int64    `json:"price_cents,omitempty"`
	PriceUOM      string    `json:"price_uom,omitempty"`
	Priority      int64     `json:"priority"`
	DetailText    string    `json:"detail_text"`
	IsActive      bool      `json:"is_active"`
	Version       int64     `json:"version"`
}

// ClearanceItem is one pricing_clearance_items row. A NIL ClearancePriceCents is the BADGE-ONLY
// case: the item shows as clearance everywhere it appears and still prices through the ordinary
// waterfall.
type ClearanceItem struct {
	ProductID           string     `json:"product_id"`
	SKU                 string     `json:"sku,omitempty"`  // projection-only, from the list read
	Name                string     `json:"name,omitempty"` // projection-only, from the list read
	Reason              string     `json:"reason"`
	ClearancePriceCents *int64     `json:"clearance_price_cents,omitempty"`
	PriceUOM            string     `json:"price_uom,omitempty"`
	StartedAt           time.Time  `json:"started_at"`
	EndsAt              *time.Time `json:"ends_at,omitempty"`
	IsActive            bool       `json:"is_active"`
	Version             int64      `json:"version"`
}

// active reports whether a clearance row is in force at t. Absent ends_at = open-ended, which is
// the normal case (clearance runs until the stock is gone).
func (c ClearanceItem) active(t time.Time) bool {
	if !c.IsActive {
		return false
	}
	if t.Before(c.StartedAt) {
		return false
	}
	if c.EndsAt != nil && !t.Before(*c.EndsAt) {
		return false
	}
	return true
}

// withinWindow is the promotion date-boundary test, and it is HALF-OPEN on purpose: a promo is live
// for t in [starts_at, ends_at). The alternative (inclusive on both ends) makes two back-to-back
// promos overlap for exactly one instant at midnight, and which one wins in that instant depends on
// the sort, a bug that reproduces once a month at 00:00:00.000 and never in a test that uses
// time.Now(). See the boundary tests in promo_test.go: start is IN, end is OUT.
func (p Promotion) withinWindow(t time.Time) bool {
	if t.Before(p.StartsAt) {
		return false
	}
	return t.Before(p.EndsAt)
}

// promoStore is the DIB-70 persistence seam. It is SEPARATE from pricingStore and OPTIONAL: a nil
// promoStore means the promo step does not run at all and the waterfall behaves exactly as it did
// in W3. That is the flag-off default required of a new config seam, and it also means every
// pre-existing resolver test keeps passing against a fake that knows nothing about promotions.
type promoStore interface {
	// ActivePromotions returns the candidate promos for a (product, category, branch) at t. The
	// window filter is applied in SQL; the specificity + priority ordering is applied in Go
	// (selectPromotion) so the ruling above lives in ONE readable place rather than in an ORDER BY.
	ActivePromotions(ctx context.Context, productID, categoryID, branchID string, at time.Time) ([]Promotion, error)
	ClearanceFor(ctx context.Context, productID string) (*ClearanceItem, error)

	ListPromotions(ctx context.Context, includeInactive bool) ([]Promotion, error)
	GetPromotion(ctx context.Context, id string) (Promotion, error)
	CreatePromotion(ctx context.Context, p Promotion) (Promotion, error)
	SavePromotion(ctx context.Context, p Promotion, expectedVersion int64) (Promotion, error)
	DeletePromotion(ctx context.Context, id string, expectedVersion int64) error

	ListClearance(ctx context.Context, includeInactive bool) ([]ClearanceItem, error)
	UpsertClearance(ctx context.Context, c ClearanceItem) (ClearanceItem, error)
	DeleteClearance(ctx context.Context, productID string) error
}

// WithPromoStore injects the DIB-70 promotions/clearance store. Additive and flag-off: until it is
// called the resolver runs the W3 waterfall unchanged.
func (s *Service) WithPromoStore(p promoStore) *Service { s.promo = p; return s }

// promoCandidate is a promotion (or a synthesised clearance promo) plus the specificity it won at.
// Specificity is an explicit small integer rather than an implicit sort key because it is the thing
// the ruling above is written in terms of, and it appears in the audit detail.
type promoCandidate struct {
	promo       Promotion
	specificity int
	clearance   bool
}

const (
	specAll      = 0 // store-wide promo: no product, no category
	specCategory = 1
	specProduct  = 2
	specClear    = 3 // a priced clearance row, the most specific statement about one SKU there is
)

// selectPromotion applies the ruling: clearance-with-a-price beats a product promo beats a category
// promo beats a store-wide promo; within one specificity, priority DESC then id ASC (the same total
// order selectRule uses, so the two selectors cannot disagree about what "deterministic" means).
//
// Branch scope is a FILTER, not a specificity dimension: a branch-scoped promo and a global promo
// at the same product specificity are ranked by priority, because "this yard only" says nothing
// about how specific the offer is, only about where it is honoured.
func selectPromotion(promos []Promotion, clear *ClearanceItem, in ResolveInput, at time.Time) *promoCandidate {
	var cands []promoCandidate

	if clear != nil && clear.active(at) && clear.ClearancePriceCents != nil {
		cands = append(cands, promoCandidate{
			promo: Promotion{
				ID:         clear.ProductID,
				Code:       "CLEARANCE",
				Name:       "Clearance",
				ProductID:  clear.ProductID,
				CalcType:   PromoFixedPrice,
				PriceCents: clear.ClearancePriceCents,
				PriceUOM:   clear.PriceUOM,
				DetailText: clearanceDetail(*clear),
				IsActive:   true,
			},
			specificity: specClear,
			clearance:   true,
		})
	}

	for _, p := range promos {
		if !p.IsActive || !p.withinWindow(at) {
			continue
		}
		// Branch filter. A promo pinned to a branch is invisible everywhere else; a promo with no
		// branch runs everywhere. An EMPTY in.BranchID (a resolve with no branch context, e.g. the
		// low-margin preview) sees only unpinned promos, guessing that "no branch supplied" means
		// "every branch" would let a Kingston-only clearance leak into a Trenton quote.
		if p.BranchID != "" && p.BranchID != in.BranchID {
			continue
		}
		spec := specAll
		switch {
		case p.ProductID != "":
			if in.ProductID == "" || p.ProductID != in.ProductID {
				continue
			}
			spec = specProduct
		case p.CategoryID != "":
			if in.CategoryID == "" || p.CategoryID != in.CategoryID {
				continue
			}
			spec = specCategory
		}
		cands = append(cands, promoCandidate{promo: p, specificity: spec})
	}
	if len(cands) == 0 {
		return nil
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].specificity != cands[j].specificity {
			return cands[i].specificity > cands[j].specificity
		}
		if cands[i].promo.Priority != cands[j].promo.Priority {
			return cands[i].promo.Priority > cands[j].promo.Priority
		}
		return cands[i].promo.ID < cands[j].promo.ID
	})
	w := cands[0]
	return &w
}

// applyPromo turns a winning candidate into a price. anchorCents is the RETAIL ANCHOR in the
// result UOM, NOT the tier-resolved base, because a promotion is a base-selection level: "20%
// off" is 20% off the shelf price, not 20% off whatever discount the customer already had. The
// caller compares the result against the ordinary base and keeps the better of the two (the
// never-worse guard). Returns the promo price and whether the promo is expressible at all in the
// result UOM.
//
// FIXED_PRICE + a UOM MISMATCH is the one case that must not silently apply. A promo authored as
// "$4.00 per SF" cannot be pasted onto a line resolving in PLT, that is a 50× error, not a
// rounding difference. When the UOMs disagree the promo is REFUSED (applied=false) and the
// ordinary base stands; the caller records that in the detail. Refusing is right rather than
// converting because the conversion would need a factor the promo author never supplied, and a
// wrong 50× price at the counter is unrecoverable while a missing sale price is a phone call.
func applyPromo(c promoCandidate, anchorCents int64, resultUOM string) (priceCents int64, applied bool, err error) {
	switch c.promo.CalcType {
	case PromoPctOff:
		if c.promo.DiscountBps == nil {
			return 0, false, badReq("PCT_OFF promotion %q missing discount_bps", c.promo.Code)
		}
		return applyPctOff(anchorCents, *c.promo.DiscountBps), true, nil
	case PromoAmtOff:
		if c.promo.DiscountCents == nil {
			return 0, false, badReq("AMT_OFF promotion %q missing discount_cents", c.promo.Code)
		}
		v := anchorCents - *c.promo.DiscountCents
		if v < 0 {
			v = 0 // a discount never drives the price negative, same floor as the ABS_OFF break
		}
		return v, true, nil
	case PromoFixedPrice:
		if c.promo.PriceCents == nil {
			return 0, false, badReq("FIXED_PRICE promotion %q missing price_cents", c.promo.Code)
		}
		if c.promo.PriceUOM != "" && resultUOM != "" && !eqUOM(c.promo.PriceUOM, resultUOM) {
			return 0, false, nil
		}
		return *c.promo.PriceCents, true, nil
	default:
		return 0, false, badReq("unsupported promotion calc_type %q", c.promo.CalcType)
	}
}

// eqUOM compares two UOM codes the way the database does, uoms.code is CITEXT, so 'plt' and 'PLT'
// are the SAME unit and a case difference must not be read as a mismatch (which, via applyPromo's
// refusal path, would silently drop a correctly-authored promo).
func eqUOM(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if ca >= 'A' && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if cb >= 'A' && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}

func clearanceDetail(c ClearanceItem) string {
	if c.Reason == "" {
		return "Clearance"
	}
	return "Clearance, " + c.Reason
}

// promoDetail composes the human sentence for a promo that WON base selection.
func promoDetail(c promoCandidate) string {
	if c.clearance {
		return c.promo.DetailText
	}
	label := c.promo.Name
	if label == "" {
		label = c.promo.Code
	}
	switch c.promo.CalcType {
	case PromoPctOff:
		return fmt.Sprintf("%s (%s off)", label, pctStr(*c.promo.DiscountBps))
	case PromoAmtOff:
		return fmt.Sprintf("%s (%s off)", label, moneyStr(*c.promo.DiscountCents))
	case PromoFixedPrice:
		return fmt.Sprintf("%s (%s/%s)", label, moneyStr(*c.promo.PriceCents), c.promo.PriceUOM)
	}
	return label
}

// promoUOMMismatchNote is what the breakdown says when a FIXED_PRICE promo could not be expressed
// on this line because it was authored in a different unit. It names BOTH units, because the fix is
// an edit to the promo and the author needs to know which unit the line actually resolved in.
func promoUOMMismatchNote(c promoCandidate, resultUOM string) string {
	label := c.promo.Name
	if label == "" {
		label = c.promo.Code
	}
	return fmt.Sprintf("%s not applied, promo is priced per %s, this line resolves in %s",
		label, c.promo.PriceUOM, resultUOM)
}

// promoConsideredNote is what the breakdown says when a promo matched and the NEVER-WORSE GUARD
// kept the ordinary base. Saying nothing here is the tempting option and the wrong one: the
// customer has seen the sale sign, and the clerk needs a sentence explaining why the till shows a
// different (lower) number than the sign.
func promoConsideredNote(c promoCandidate, promoCents, baseCents int64) string {
	label := c.promo.Name
	if label == "" {
		label = c.promo.Code
	}
	return fmt.Sprintf("%s not applied, your price %s already beats the promo %s",
		label, moneyStr(baseCents), moneyStr(promoCents))
}

// - admin surface -

func (s *Service) promoOK() error {
	if s.promo == nil {
		return errNoDB
	}
	return nil
}

func (s *Service) ListPromotions(ctx context.Context, includeInactive bool) ([]Promotion, error) {
	if err := s.promoOK(); err != nil {
		return nil, err
	}
	return s.promo.ListPromotions(ctx, includeInactive)
}

func (s *Service) GetPromotion(ctx context.Context, id string) (Promotion, error) {
	if err := s.promoOK(); err != nil {
		return Promotion{}, err
	}
	return s.promo.GetPromotion(ctx, id)
}

func (s *Service) CreatePromotion(ctx context.Context, p Promotion, userID string) (Promotion, error) {
	if err := s.promoOK(); err != nil {
		return Promotion{}, err
	}
	normalizePromo(&p)
	if err := validatePromotion(p); err != nil {
		return Promotion{}, err
	}
	p.IsActive = true
	out, err := s.promo.CreatePromotion(ctx, p)
	if err != nil {
		return Promotion{}, err
	}
	refType, refID := promoRef(out)
	_ = s.store.AppendAudit(ctx, AuditEntry{
		EntityType: "promotion", EntityID: out.ID, Action: "CREATE",
		FieldName: "code", NewValue: out.Code, UserID: userID,
		RefEntityType: refType, RefEntityID: refID,
	})
	return out, nil
}

func (s *Service) UpdatePromotion(ctx context.Context, id string, p Promotion, expectedVersion int64, userID string) (Promotion, error) {
	if err := s.promoOK(); err != nil {
		return Promotion{}, err
	}
	cur, err := s.promo.GetPromotion(ctx, id)
	if err != nil {
		return Promotion{}, err
	}
	p.ID = id
	normalizePromo(&p)
	if err := validatePromotion(p); err != nil {
		return Promotion{}, err
	}
	out, err := s.promo.SavePromotion(ctx, p, expectedVersion)
	if err != nil {
		return Promotion{}, err
	}
	refType, refID := promoRef(out)
	for _, e := range promoDiff(cur, out) {
		e.UserID = userID
		e.RefEntityType, e.RefEntityID = refType, refID
		_ = s.store.AppendAudit(ctx, e)
	}
	return out, nil
}

func (s *Service) DeletePromotion(ctx context.Context, id string, expectedVersion int64, userID string) error {
	if err := s.promoOK(); err != nil {
		return err
	}
	cur, err := s.promo.GetPromotion(ctx, id)
	if err != nil {
		return err
	}
	if err := s.promo.DeletePromotion(ctx, id, expectedVersion); err != nil {
		return err
	}
	refType, refID := promoRef(cur)
	_ = s.store.AppendAudit(ctx, AuditEntry{
		EntityType: "promotion", EntityID: id, Action: "DELETE",
		FieldName: "is_active", OldValue: "true", NewValue: "false", UserID: userID,
		RefEntityType: refType, RefEntityID: refID,
	})
	return nil
}

func (s *Service) ListClearance(ctx context.Context, includeInactive bool) ([]ClearanceItem, error) {
	if err := s.promoOK(); err != nil {
		return nil, err
	}
	return s.promo.ListClearance(ctx, includeInactive)
}

func (s *Service) UpsertClearance(ctx context.Context, c ClearanceItem, userID string) (ClearanceItem, error) {
	if err := s.promoOK(); err != nil {
		return ClearanceItem{}, err
	}
	if c.ProductID == "" {
		return ClearanceItem{}, badReq("product_id is required")
	}
	if (c.ClearancePriceCents == nil) != (c.PriceUOM == "") {
		return ClearanceItem{}, unprocessable("clearance_price_cents and price_uom must be set together (a price with no unit is not a price)")
	}
	if c.ClearancePriceCents != nil && *c.ClearancePriceCents < 0 {
		return ClearanceItem{}, unprocessable("clearance_price_cents must be >= 0")
	}
	if c.EndsAt != nil && !c.StartedAt.IsZero() && !c.EndsAt.After(c.StartedAt) {
		return ClearanceItem{}, unprocessable("ends_at must be after started_at")
	}
	out, err := s.promo.UpsertClearance(ctx, c)
	if err != nil {
		return ClearanceItem{}, err
	}
	_ = s.store.AppendAudit(ctx, AuditEntry{
		EntityType: "clearance", EntityID: out.ProductID, Action: "UPDATE",
		FieldName: "is_active", NewValue: fmt.Sprintf("%t", out.IsActive), UserID: userID,
		RefEntityType: RefProduct, RefEntityID: out.ProductID,
	})
	return out, nil
}

func (s *Service) DeleteClearance(ctx context.Context, productID, userID string) error {
	if err := s.promoOK(); err != nil {
		return err
	}
	if err := s.promo.DeleteClearance(ctx, productID); err != nil {
		return err
	}
	_ = s.store.AppendAudit(ctx, AuditEntry{
		EntityType: "clearance", EntityID: productID, Action: "DELETE",
		FieldName: "is_active", OldValue: "true", NewValue: "false", UserID: userID,
		RefEntityType: RefProduct, RefEntityID: productID,
	})
	return nil
}

// promoRef derives the DIB-402 drill-through for a promotion audit row, the same
// most-specific-dimension-first shape as ruleRef. A store-wide promo has no navigable target and
// gets none, exactly like a TIER_DEFAULT rule.
func promoRef(p Promotion) (string, string) {
	switch {
	case p.ProductID != "":
		return RefProduct, p.ProductID
	case p.CategoryID != "":
		return RefCategory, p.CategoryID
	default:
		return "", ""
	}
}

func normalizePromo(p *Promotion) {
	switch p.CalcType {
	case PromoPctOff:
		p.DiscountCents, p.PriceCents = nil, nil
	case PromoAmtOff:
		p.DiscountBps, p.PriceCents = nil, nil
	case PromoFixedPrice:
		p.DiscountBps, p.DiscountCents = nil, nil
	}
	if p.CalcType != PromoFixedPrice {
		p.PriceUOM = ""
	}
}

func validatePromotion(p Promotion) error {
	if p.Code == "" || p.Name == "" {
		return badReq("code and name are required")
	}
	if !promoCalcTypes[p.CalcType] {
		return unprocessable("calc_type must be one of PCT_OFF, AMT_OFF, FIXED_PRICE")
	}
	if p.ProductID != "" && p.CategoryID != "" {
		return unprocessable("a promotion is scoped to a product OR a category, not both")
	}
	if p.StartsAt.IsZero() || p.EndsAt.IsZero() {
		return unprocessable("starts_at and ends_at are required, an unbounded promotion is a price change, not a promotion")
	}
	if !p.EndsAt.After(p.StartsAt) {
		return unprocessable("ends_at must be after starts_at")
	}
	switch p.CalcType {
	case PromoPctOff:
		if p.DiscountBps == nil {
			return unprocessable("PCT_OFF requires discount_bps")
		}
		if err := validateBps("discount_bps", p.DiscountBps); err != nil {
			return err
		}
	case PromoAmtOff:
		if p.DiscountCents == nil {
			return unprocessable("AMT_OFF requires discount_cents")
		}
		if *p.DiscountCents < 0 {
			return unprocessable("discount_cents must be >= 0")
		}
	case PromoFixedPrice:
		if p.PriceCents == nil {
			return unprocessable("FIXED_PRICE requires price_cents")
		}
		if *p.PriceCents < 0 {
			return unprocessable("price_cents must be >= 0")
		}
		if p.PriceUOM == "" {
			return unprocessable("FIXED_PRICE requires price_uom, a set price with no unit cannot be applied to a line")
		}
	}
	return nil
}

func promoDiff(cur, next Promotion) []AuditEntry {
	var out []AuditEntry
	add := func(field, o, n string) {
		if o != n {
			out = append(out, AuditEntry{EntityType: "promotion", EntityID: next.ID, Action: "UPDATE", FieldName: field, OldValue: o, NewValue: n})
		}
	}
	add("name", cur.Name, next.Name)
	add("calc_type", cur.CalcType, next.CalcType)
	add("discount_bps", i64s(cur.DiscountBps), i64s(next.DiscountBps))
	add("discount_cents", i64s(cur.DiscountCents), i64s(next.DiscountCents))
	add("price_cents", i64s(cur.PriceCents), i64s(next.PriceCents))
	add("starts_at", cur.StartsAt.UTC().Format(time.RFC3339), next.StartsAt.UTC().Format(time.RFC3339))
	add("ends_at", cur.EndsAt.UTC().Format(time.RFC3339), next.EndsAt.UTC().Format(time.RFC3339))
	add("branch_id", cur.BranchID, next.BranchID)
	add("is_active", fmt.Sprintf("%t", cur.IsActive), fmt.Sprintf("%t", next.IsActive))
	return out
}
