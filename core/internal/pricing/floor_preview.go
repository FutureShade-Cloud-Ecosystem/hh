package pricing

// floor_preview.go, the pricing editors' floor warning (CP0-6, answer A): before an
// editor saves a rule, a customer price, a category margin policy or a quantity break,
// the server answers which existing products the new price would put under the margin
// floor, and afterwards it records that the editor confirmed that warning in the pricing
// log. Nothing here is written by the preview, and the warning never refuses a save:
// the pricing editors are the roles the dealer trusts to set prices.

import (
	"context"
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

// FloorCandidates finds the products a change may reach. The pgx Repository satisfies it.
type FloorCandidates interface {
	CandidateProducts(ctx context.Context, categoryID, productID, branchID string, limit int) ([]CandidateProduct, error)
}

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
			contexts = []previewContext{{customerID: r.CustomerID}}
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
		normalizeOverrideCalc(&o)
		if err := validateOverride(o); err != nil {
			return FloorPreview{}, err
		}
		o.ID = ch.ID
		ov.dropOverrideID, ov.addOverride = ch.ID, &o
		categoryID = o.CategoryID
		contexts = []previewContext{{customerID: o.CustomerID}}
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
		// A break prices at its threshold: grade each discount tier at the quantity it starts at.
		qtys = nil
		for _, it := range h.Items {
			if !isSurchargeBreak(it.ValueType) && it.FromQty > 0 {
				qtys = append(qtys, previewQty{qty: it.FromQty, uom: h.ThresholdUOM})
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
	for _, p := range products {
		if p.CostCents <= 0 {
			// No cost on file: a margin cannot be told, and a zero would read as a clean 100 percent.
			out.NotGraded++
			continue
		}
		var worst *FloorPreviewItem
		graded := false
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
					continue
				}
				graded = true
				if !after.Blocked {
					continue
				}
				before, berr := s.resolve(ctx, in, true, false)
				it := FloorPreviewItem{
					ProductID: p.ProductID, SKU: p.SKU, Name: p.Name, CategoryID: p.CategoryID, CustomerType: c.customerType,
					FinalCents: after.FinalCents, MarginBps: after.MarginBps, FloorBps: after.FloorBps,
					PriceUOM: after.PriceUOM, Source: after.Source,
				}
				if berr == nil {
					it.PreviousFinalCents, it.PreviousMarginBps, it.AlreadyUnderFloor = before.FinalCents, before.MarginBps, before.Blocked
				}
				if worst == nil || worseBreach(it, *worst) {
					worst = &it
				}
			}
		}
		if !graded {
			out.NotGraded++
			continue
		}
		out.Evaluated++
		if worst != nil {
			out.Items = append(out.Items, *worst)
		}
	}
	sort.SliceStable(out.Items, func(i, j int) bool { return out.Items[i].MarginBps < out.Items[j].MarginBps })
	return out, nil
}

// worseBreach orders two breaches of one product: a newly under case outranks one that was
// under already, because the warning is about what this change does; among the same kind
// the lower margin is worse.
func worseBreach(a, b FloorPreviewItem) bool {
	if a.AlreadyUnderFloor != b.AlreadyUnderFloor {
		return !a.AlreadyUnderFloor
	}
	return a.MarginBps < b.MarginBps
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
	if err := s.store.AppendAudit(ctx, AuditEntry{
		EntityType: c.EntityType, EntityID: c.EntityID, Action: "UPDATE",
		FieldName: "floor_warning_confirmed",
		NewValue:  fmt.Sprintf("%d %s under the margin floor: %s%s", len(c.ProductIDs), word, strings.Join(shown, ", "), more),
		UserID:    userID, RefEntityType: c.RefType, RefEntityID: c.RefID,
	}); err != nil {
		return fmt.Errorf("record floor confirmation: %w", err)
	}
	return nil
}
