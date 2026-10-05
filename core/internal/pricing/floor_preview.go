package pricing

// floor_preview.go, the pricing editors' floor warning (CP0-6, answer A): before an
// editor saves a rule, a customer price, a category margin policy or a quantity break,
// the server answers which existing products the new price would put under the margin
// floor, and afterwards it records that the editor confirmed that warning in the pricing
// log. Nothing here is written by the preview, and the warning never refuses a save:
// the pricing editors are the roles the dealer trusts to set prices.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// FloorChange is the proposed write the preview grades, exactly one of Rule, Override,
// Policy or Break according to Kind. ID names the record being edited, empty on create.
type FloorChange struct {
	Kind     string
	ID       string
	BranchID string
	Rule     *Rule
	Override *Override
	Policy   *CategoryPolicy
	Break    *QuantityBreakHeader
}

// FloorPreviewItem is one product the new price leaves under the floor.
type FloorPreviewItem struct {
	ProductID          string
	SKU                string
	Name               string
	CategoryID         string
	CustomerType       string
	FinalCents         int64
	MarginBps          int64
	FloorBps           int64
	PriceUOM           string
	Source             string
	PreviousFinalCents int64
	PreviousMarginBps  int64
	AlreadyUnderFloor  bool
}

// FloorPreview is the preview's answer.
type FloorPreview struct {
	Items     []FloorPreviewItem
	Evaluated int
	NotGraded int
	NoCost    int
	Truncated bool
}

// CandidateProduct is one active product the preview may grade, with its cost.
type CandidateProduct struct {
	ProductID  string
	CategoryID string
	SKU        string
	Name       string
	BaseUOM    string
	CostCents  int64
}

// FloorCandidates finds the products a change may reach, with their cost. The composition
// root implements it over the product and inventory services, so pricing reads neither' table.
type FloorCandidates interface {
	CandidateProducts(ctx context.Context, categoryID, productID, branchID string, limit int) ([]CandidateProduct, error)
}

// FloorCustomers names the pricing type of a customer, so a customer scoped change is graded
// at the price that customer pays (its tier rules apply beneath the change). The composition
// root implements it over the customer service.
type FloorCustomers interface {
	CustomerType(ctx context.Context, customerID string) (string, error)
}

// ErrFloorCustomerNotFound is the answer of a FloorCustomers seam for an unknown customer.
var ErrFloorCustomerNotFound = fmt.Errorf("floor preview customer: %w", errNotFound)

// WithFloorCustomerType wires the customer type seam the preview grades customer changes with.
func (s *Service) WithFloorCustomerType(c FloorCustomers) *Service {
	s.floorCust = c
	return s
}

// MaxBreakTiers bounds the tiers of one quantity break: the write refuses more, and the preview
// grades every tier for every product and customer type.
const MaxBreakTiers = 20

// MaxPreviewGrades bounds the product, customer type and quantity combinations one preview
// grades. Past it the preview stops and says it was truncated.
const MaxPreviewGrades = 4000

// FloorConfirmation is the editor's confirmation of a floor warning.
type FloorConfirmation struct {
	EntityType string
	EntityID   string
	ProductIDs []string
	RefType    string
	RefID      string
}

// WithCandidateSource wires the product source the preview grades.
func (s *Service) WithCandidateSource(c FloorCandidates) *Service {
	s.cands = c
	return s
}

// PreviewProductCap bounds how many products one preview grades.
const PreviewProductCap = 200

// maxConfirmedProducts bounds the product ids one confirmation may carry, and
// loggedProductIDs bounds how many of them the log row spells out.
const (
	maxConfirmedProducts = 500
	loggedProductIDs     = 25
)

// previewCustomerTypes are the customer types a change that touches every customer
// (a margin policy, a quantity break) is graded for; the worst of them is reported.
var previewCustomerTypes = []string{"CONTRACTOR", "RETAIL", "BUILDER", "GOVERNMENT", "INTERNAL"}

// previewStore lays the proposed change over the stored pricing data, for the
// resolver to read as if the change were saved. It writes nothing.
type previewStore struct {
	pricingStore
	dropRuleID     string
	addRule        *Rule
	dropOverrideID string
	addOverride    *Override
	dropBreakID    string
	addBreak       *QuantityBreakHeader
	policy         *CategoryPolicy
}

// ruleReaches reports whether the stored-rules query would return r for in.
func ruleReaches(r Rule, in ResolveInput) bool {
	if !r.IsActive {
		return false
	}
	if r.BranchID != "" && r.BranchID != in.BranchID {
		return false
	}
	switch r.Scope {
	case ScopeCustomerProduct:
		return in.CustomerID != "" && r.CustomerID == in.CustomerID && r.ProductID == in.ProductID
	case ScopeCustomerCategory:
		return in.CustomerID != "" && r.CustomerID == in.CustomerID && in.CategoryID != "" && r.CategoryID == in.CategoryID
	case ScopeTierCategory:
		return r.CustomerType == in.CustomerType && in.CategoryID != "" && r.CategoryID == in.CategoryID
	case ScopeTierDefault:
		return r.CustomerType == in.CustomerType
	}
	return false
}

func (p previewStore) MatchingRules(ctx context.Context, in ResolveInput) ([]Rule, error) {
	base, err := p.pricingStore.MatchingRules(ctx, in)
	if err != nil {
		return nil, err
	}
	out := make([]Rule, 0, len(base)+1)
	for _, r := range base {
		if p.dropRuleID != "" && r.ID == p.dropRuleID {
			continue
		}
		out = append(out, r)
	}
	if p.addRule != nil && ruleReaches(*p.addRule, in) {
		out = append(out, *p.addRule)
	}
	return out, nil
}

func (p previewStore) OverrideFor(ctx context.Context, customerID, categoryID string) (*Override, error) {
	if p.addOverride != nil && customerID != "" && p.addOverride.CustomerID == customerID && p.addOverride.CategoryID == categoryID {
		o := *p.addOverride
		return &o, nil
	}
	base, err := p.pricingStore.OverrideFor(ctx, customerID, categoryID)
	if err != nil || base == nil {
		return base, err
	}
	if p.dropOverrideID != "" && base.ID == p.dropOverrideID {
		return nil, nil
	}
	return base, nil
}

func (p previewStore) QuantityBreaksFor(ctx context.Context, productID, categoryID string) ([]QuantityBreak, error) {
	base, err := p.pricingStore.QuantityBreaksFor(ctx, productID, categoryID)
	if err != nil {
		return nil, err
	}
	out := make([]QuantityBreak, 0, len(base))
	for _, b := range base {
		if p.dropBreakID != "" && b.HeaderID == p.dropBreakID {
			continue
		}
		out = append(out, b)
	}
	if h := p.addBreak; h != nil && ((h.ProductID != "" && h.ProductID == productID) || (h.CategoryID != "" && h.CategoryID == categoryID)) {
		for _, it := range h.Items {
			out = append(out, QuantityBreak{
				HeaderID: h.ID, Name: h.Name, ProductID: h.ProductID, CategoryID: h.CategoryID,
				ThresholdUOM: h.ThresholdUOM, FromQty: it.FromQty, ToQty: it.ToQty, ValueType: it.ValueType,
				DiscountBps: it.DiscountBps, DiscountCents: it.DiscountCents,
				SurchargeBps: it.SurchargeBps, SurchargeCents: it.SurchargeCents, PriceUOM: it.PriceUOM,
			})
		}
		sort.SliceStable(out, func(i, j int) bool { return out[i].FromQty < out[j].FromQty })
	}
	return out, nil
}

func (p previewStore) CategoryPolicy(ctx context.Context, categoryID string) (*CategoryPolicy, error) {
	if p.policy != nil && p.policy.CategoryID == categoryID {
		c := *p.policy
		return &c, nil
	}
	return p.pricingStore.CategoryPolicy(ctx, categoryID)
}

type previewContext struct{ customerID, customerType string }

type previewQty struct {
	qty float64
	uom string // "" means the product's base unit
}

// PreviewFloor grades the proposed change: for each existing product it can reach, the
// price today and the price with the change laid over the stored data, both through the
// resolver and its margin gate, and the products the new price leaves under the floor.
// A refusal here is the refusal the write itself would give (the same validators), and
// nothing is written, not even the BYPASS row a live resolve leaves.
func (s *Service) PreviewFloor(ctx context.Context, ch FloorChange) (FloorPreview, error) {
	ov := previewStore{pricingStore: s.store}
	var (
		categoryID, productID string
		contexts              []previewContext
		qtys                  = []previewQty{{qty: 1}}
		branchID              = ch.BranchID
	)
	switch ch.Kind {
	case "rule":
		if ch.Rule == nil {
			return FloorPreview{}, badReq("rule is required for a rule preview")
		}
		r := *ch.Rule
		if r.Scope == "" || r.CalcType == "" || r.PriceUOM == "" {
			return FloorPreview{}, badReq("scope, calc_type and price_uom are required")
		}
		normalizeRuleCalc(&r)
		normalizeRuleScopeDims(&r)
		if err := validateRule(r); err != nil {
			return FloorPreview{}, err
		}
		r.IsActive = true
		r.ID = ch.ID
		ov.dropRuleID, ov.addRule = ch.ID, &r
		categoryID, productID = r.CategoryID, r.ProductID
		if r.Scope == ScopeCustomerProduct || r.Scope == ScopeCustomerCategory {
			c, cerr := s.customerContext(ctx, r.CustomerID)
			if cerr != nil {
				return FloorPreview{}, cerr
			}
			contexts = []previewContext{c}
		} else {
			contexts = []previewContext{{customerType: r.CustomerType}}
		}
		if branchID == "" {
			branchID = r.BranchID
		}
	case "override":
		if ch.Override == nil {
			return FloorPreview{}, badReq("override is required for an override preview")
		}
		o := *ch.Override
		if o.CustomerID == "" || o.CategoryID == "" || o.PriceUOM == "" {
			return FloorPreview{}, badReq("customer_id, category_id and price_uom are required")
		}
		normalizeOverrideCalc(&o)
		if err := validateOverride(o); err != nil {
			return FloorPreview{}, err
		}
		o.ID = ch.ID
		ov.dropOverrideID, ov.addOverride = ch.ID, &o
		categoryID = o.CategoryID
		c, cerr := s.customerContext(ctx, o.CustomerID)
		if cerr != nil {
			return FloorPreview{}, cerr
		}
		contexts = []previewContext{c}
	case "category_policy":
		if ch.Policy == nil {
			return FloorPreview{}, badReq("policy is required for a category policy preview")
		}
		p := *ch.Policy
		if p.CategoryID == "" {
			return FloorPreview{}, badReq("category_id is required")
		}
		if p.MarginTargetBps < 0 || p.MarginTargetBps > 10000 || p.MarginFloorBps < 0 || p.MarginFloorBps > 10000 {
			return FloorPreview{}, unprocessable("margin must be between 0%% and 100%%")
		}
		if p.MarginFloorBps > p.MarginTargetBps {
			return FloorPreview{}, unprocessable("margin floor must be ≤ target")
		}
		ov.policy = &p
		categoryID = p.CategoryID
	case "quantity_break":
		if ch.Break == nil {
			return FloorPreview{}, badReq("quantity_break is required for a quantity break preview")
		}
		h := *ch.Break
		if err := validateQuantityBreak(h); err != nil {
			return FloorPreview{}, err
		}
		h.ID = ch.ID
		ov.dropBreakID, ov.addBreak = ch.ID, &h
		categoryID, productID = h.CategoryID, h.ProductID
		// A break prices at its threshold: grade each discount tier at the quantity it starts
		// at, each distinct start once. A tier that starts at zero is graded at one base unit,
		// the smallest positive quantity it covers.
		qtys = nil
		seen := map[previewQty]bool{}
		for _, it := range h.Items {
			if isSurchargeBreak(it.ValueType) {
				continue
			}
			q := previewQty{qty: it.FromQty, uom: h.ThresholdUOM}
			if it.FromQty <= 0 {
				q = previewQty{qty: 1}
			}
			if !seen[q] {
				seen[q] = true
				qtys = append(qtys, q)
			}
		}
	default:
		return FloorPreview{}, badReq("kind must be one of rule, override, category_policy, quantity_break")
	}
	if len(contexts) == 0 {
		for _, t := range previewCustomerTypes {
			contexts = append(contexts, previewContext{customerType: t})
		}
	}
	if s.cands == nil {
		return FloorPreview{}, errNoDB
	}
	products, err := s.cands.CandidateProducts(ctx, categoryID, productID, branchID, PreviewProductCap+1)
	if err != nil {
		return FloorPreview{}, fmt.Errorf("floor preview candidates: %w", err)
	}
	out := FloorPreview{Items: []FloorPreviewItem{}}
	if len(products) > PreviewProductCap {
		products, out.Truncated = products[:PreviewProductCap], true
	}
	if len(qtys) == 0 {
		return out, nil
	}
	overlay := *s
	overlay.store = ov
	perProduct := len(contexts) * len(qtys)
	grades := 0
	for _, p := range products {
		if p.CostCents <= 0 {
			// No cost on file: a margin cannot be told, and a zero would read as a clean 100 percent.
			out.NoCost++
			continue
		}
		if grades+perProduct > MaxPreviewGrades {
			out.Truncated = true
			break
		}
		grades += perProduct
		var worst *FloorPreviewItem
		skipped, graded := false, false
		for _, c := range contexts {
			for _, q := range qtys {
				uom := q.uom
				if uom == "" {
					uom = p.BaseUOM
				}
				in := ResolveInput{
					CustomerID: c.customerID, CustomerType: c.customerType, ProductID: p.ProductID,
					CategoryID: p.CategoryID, BranchID: branchID, QtyEntered: q.qty, UOMEntered: uom, CostCents: p.CostCents,
				}
				after, aerr := overlay.resolve(ctx, in, true, false)
				if aerr != nil {
					if !notGradable(aerr) {
						return FloorPreview{}, fmt.Errorf("floor preview grading %s: %w", p.ProductID, aerr)
					}
					skipped = true
					continue
				}
				graded = true
				if !after.Blocked {
					continue
				}
				before, berr := s.resolve(ctx, in, true, false)
				if berr != nil {
					if !notGradable(berr) {
						return FloorPreview{}, fmt.Errorf("floor preview grading %s: %w", p.ProductID, berr)
					}
					skipped = true
					continue
				}
				it := FloorPreviewItem{
					ProductID: p.ProductID, SKU: p.SKU, Name: p.Name, CategoryID: p.CategoryID, CustomerType: c.customerType,
					FinalCents: after.FinalCents, MarginBps: after.MarginBps, FloorBps: after.FloorBps,
					PriceUOM: after.PriceUOM, Source: after.Source,
					PreviousFinalCents: before.FinalCents, PreviousMarginBps: before.MarginBps, AlreadyUnderFloor: before.Blocked,
				}
				if worst == nil || worseBreach(it, *worst) {
					worst = &it
				}
			}
		}
		switch {
		case skipped:
			// Some combination of this product could not be priced: it is not fully graded, and
			// whatever breach the other combinations showed is still listed.
			out.NotGraded++
		case graded:
			out.Evaluated++
		}
		if worst != nil {
			out.Items = append(out.Items, *worst)
		}
	}
	sort.SliceStable(out.Items, func(i, j int) bool { return out.Items[i].MarginBps < out.Items[j].MarginBps })
	return out, nil
}

// customerContext names the customer a change is graded for, with the pricing type the
// customer is resolved at: the tier rules beneath the change decide what the customer pays today.
func (s *Service) customerContext(ctx context.Context, customerID string) (previewContext, error) {
	if s.floorCust == nil {
		return previewContext{}, errNoDB
	}
	t, err := s.floorCust.CustomerType(ctx, customerID)
	if err != nil {
		return previewContext{}, err
	}
	return previewContext{customerID: customerID, customerType: t}, nil
}

// notGradable reports the per product cases the resolver gives when a product has no price to
// grade (no list price, no unit conversion, a product that is not there). Any other error is
// a failure of the grading itself, and the preview answers it as an error, never as a count
// of products it could not check.
func notGradable(err error) bool {
	return errors.Is(err, errNoListPrice) || isNoConversion(err) || errors.Is(err, errNotFound)
}

// worseBreach orders two breaches of one product: a newly under case outranks one that was
// under already, because the warning is about what this change does; among already-under
// combinations one whose price the change DROPPED outranks one it left standing, for the same
// reason (every type already under is the case where the dropped one is the whole story); among
// the same kind the lower margin is worse.
func worseBreach(a, b FloorPreviewItem) bool {
	if a.AlreadyUnderFloor != b.AlreadyUnderFloor {
		return !a.AlreadyUnderFloor
	}
	aDropped, bDropped := a.FinalCents < a.PreviousFinalCents, b.FinalCents < b.PreviousFinalCents
	if aDropped != bDropped {
		return aDropped
	}
	return a.MarginBps < b.MarginBps
}

// refMatchesItsKind refuses a reference whose id names a record of another kind the pricing
// module can see: a quote reference carrying a product id would link that product as a quote in
// the log. Pricing holds a product read (the candidates seam) and a customer read (the floor
// customers seam), and ids do not cross tables, so an id found by either is a mismatch whenever
// the reference claims another kind; the kinds pricing has no read for (quote, order, category)
// are checked only against those two, and an id neither sees is taken as it comes.
func (s *Service) refMatchesItsKind(ctx context.Context, refType, refID string) error {
	namesProduct, err := s.refIDNamesProduct(ctx, refID)
	if err != nil {
		return err
	}
	namesCustomer, err := s.refIDNamesCustomer(ctx, refID)
	if err != nil {
		return err
	}
	switch {
	case refType != RefProduct && namesProduct:
		return badReq("ref_entity_id names a product, not the %s the ref_entity_type claims", refType)
	case refType != RefCustomer && namesCustomer:
		return badReq("ref_entity_id names a customer, not the %s the ref_entity_type claims", refType)
	}
	return nil
}

func (s *Service) refIDNamesProduct(ctx context.Context, refID string) (bool, error) {
	if s.cands == nil {
		return false, nil
	}
	prods, err := s.cands.CandidateProducts(ctx, "", refID, "", 1)
	if err != nil {
		return false, fmt.Errorf("floor confirmation reference: %w", err)
	}
	return len(prods) > 0, nil
}

func (s *Service) refIDNamesCustomer(ctx context.Context, refID string) (bool, error) {
	if s.floorCust == nil {
		return false, nil
	}
	_, err := s.floorCust.CustomerType(ctx, refID)
	if errors.Is(err, ErrFloorCustomerNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("floor confirmation reference: %w", err)
	}
	return true, nil
}

// RecordFloorConfirmation writes the editor's confirmation of a floor warning into the
// pricing log, against the record the confirmed save wrote. Unlike the writes' own log
// rows, whose failure is dropped, a failure here is returned: the confirmation is the
// point of the call.
func (s *Service) RecordFloorConfirmation(ctx context.Context, c FloorConfirmation, userID string) error {
	if len(c.ProductIDs) == 0 {
		return badReq("a confirmation names at least one product")
	}
	if len(c.ProductIDs) > maxConfirmedProducts {
		return badReq("a confirmation names at most %d products", maxConfirmedProducts)
	}
	if c.EntityID == "" {
		return badReq("entity_id is required")
	}
	if (c.RefType == "") != (c.RefID == "") || (c.RefType != "" && !validRefTypes[c.RefType]) {
		return badReq("ref_entity_type must be one of product, customer, category, quote, order and come with ref_entity_id")
	}
	if c.RefType != "" {
		if err := s.refMatchesItsKind(ctx, c.RefType, c.RefID); err != nil {
			return err
		}
	}
	var err error
	switch c.EntityType {
	case "pricing_rule":
		_, err = s.store.GetRule(ctx, c.EntityID)
	case "customer_override":
		_, err = s.store.GetOverride(ctx, c.EntityID)
	case "quantity_break":
		_, err = s.store.GetQuantityBreakHeader(ctx, c.EntityID)
	case "category_policy":
		var pol *CategoryPolicy
		if pol, err = s.store.CategoryPolicy(ctx, c.EntityID); err == nil && pol == nil {
			err = errNotFound
		}
	default:
		return badReq("entity_type must be one of pricing_rule, customer_override, category_policy, quantity_break")
	}
	if err != nil {
		return err
	}
	shown := c.ProductIDs
	more := ""
	if len(shown) > loggedProductIDs {
		shown, more = shown[:loggedProductIDs], fmt.Sprintf(" and %d more", len(c.ProductIDs)-loggedProductIDs)
	}
	word := "products"
	if len(c.ProductIDs) == 1 {
		word = "product"
	}
	// The row says what the server knows: that the editor confirmed a warning naming these
	// products. It does not state them as under the floor; the server did not recheck.
	if err := s.store.AppendAudit(ctx, AuditEntry{
		EntityType: c.EntityType, EntityID: c.EntityID, Action: "UPDATE",
		FieldName: "floor_warning_confirmed",
		NewValue:  fmt.Sprintf("the editor confirmed a floor warning naming %d %s: %s%s", len(c.ProductIDs), word, strings.Join(shown, ", "), more),
		UserID:    userID, RefEntityType: c.RefType, RefEntityID: c.RefID,
	}); err != nil {
		return fmt.Errorf("record floor confirmation: %w", err)
	}
	return nil
}
