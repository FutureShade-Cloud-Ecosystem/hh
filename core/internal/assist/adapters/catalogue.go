package adapters

// catalogue.go, DIB-478. Finding a product, pricing it for a customer, and naming the yards.
//
// THE SEARCH IS THE COUNTER'S OWN, REUSED AGAIN. quotecatalogue.go already argued this for the quote
// extractor and the argument is unchanged here: product.CounterSearch carries the yard's own synonym
// table, so "patio stone" already means Slab and "Blu 60" already means what the counter thinks it
// means. An assistant with its own matcher would be a second opinion about the same words, and the
// two opinions would differ in front of a customer.
//
// It is also what the CONVERSATION RESOLVER asks when a term might mean two products, so the
// clarifying question "two products match 'Blu', which one?" is asked against the same ranking the
// POS shows, and the options a person picks from are the rows they would have seen there.
//
// THE PRICE IS THE REAL WATERFALL, IN PREVIEW. pricing.ResolvePricePreview resolves exactly as a
// quote line does and writes no audit row, which is the correct shape for a question. A list price
// would have been one query and a lie: most accounts here do not pay list, and a rep who repeats a
// list price on the phone has quoted a number the system will not honour.

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/branches"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/inventory"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pricing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/product"
)

// -- the catalogue search -----------------------------------------------------------------------

type catalogueAdapter struct{ svc CounterSearcher }

var _ assist.CatalogueReader = catalogueAdapter{}

// NewCatalogueReader builds the seam over the SAME CounterSearcher the quote extractor consumes
// (quotecatalogue.go). One interface, one service, one ranking.
func NewCatalogueReader(svc CounterSearcher) assist.CatalogueReader {
	if svc == nil {
		return nil
	}
	return catalogueAdapter{svc: svc}
}

// FindProducts runs the counter search and renames its fields. NOTHING IS RE-RANKED: the counter's
// order is the order, because the whole argument for reuse collapses the moment this file has views.
//
// BranchID is deliberately NOT passed. It scopes the on-hand figures the counter search attaches,
// which this consumer does not render, and asking for them would make the assistant pay for a lateral
// it throws away. A product is the same product at every yard.
func (a catalogueAdapter) FindProducts(ctx context.Context, c assist.Caller, term string, limit int) ([]assist.CatalogueHit, error) {
	if strings.TrimSpace(term) == "" {
		return []assist.CatalogueHit{}, nil
	}
	hits, err := a.svc.CounterSearch(ctx, product.CounterSearchQuery{Q: term, Limit: limit})
	if err != nil {
		return nil, err
	}
	out := make([]assist.CatalogueHit, 0, len(hits))
	for _, h := range hits {
		out = append(out, assist.CatalogueHit{
			ProductID:   h.ProductID,
			SKU:         h.SKU,
			Name:        h.Name,
			BaseUOM:     h.BaseUOM,
			MatchReason: h.MatchReason,
		})
	}
	return out, nil
}

// -- the price ----------------------------------------------------------------------------------

// PriceResolver is what this adapter needs from internal/pricing: the PREVIEW door, and only the
// preview door. ResolvePrice (the writing one) is deliberately absent from this interface, an
// assistant that could reach it could stamp a pricing_audit_log row for a question nobody asked as a
// quote, and read-only has to be structural rather than remembered.
type PriceResolver interface {
	ResolvePricePreview(ctx context.Context, in pricing.ResolveInput) (pricing.Breakdown, error)
}

// CustomerTyper resolves an account's pricing tier, the input the waterfall needs to price for THIS
// customer rather than for a generic one.
type CustomerTyper interface {
	CustomerType(ctx context.Context, customerID string) (string, error)
}

// CostBasisReader is the branch cost the margin gate scores against. Optional: with no cost the
// waterfall still resolves a price and simply cannot judge the margin, which is a smaller loss than
// refusing the question.
type CostBasisReader interface {
	CurrentCostCents(ctx context.Context, productID, branchID string) (int64, bool, error)
}

type priceAdapter struct {
	pricer   PriceResolver
	custType CustomerTyper
	cost     CostBasisReader
	accounts AccountLookup
	stock    InTransitReader
	branches BranchLister
}

var _ assist.PriceReader = priceAdapter{}

// NewPriceReader builds the seam. The pricer, the account lookup and the product lookup are all
// required: a price without a resolved customer is a list price wearing a customer's name, and a
// price without a resolved product is nothing at all.
func NewPriceReader(pricer PriceResolver, custType CustomerTyper, cost CostBasisReader, accounts AccountLookup, stock InTransitReader, br BranchLister) assist.PriceReader {
	if pricer == nil || accounts == nil || stock == nil {
		return nil
	}
	return priceAdapter{pricer: pricer, custType: custType, cost: cost, accounts: accounts, stock: stock, branches: br}
}

// PriceFor resolves the product, the account and the branch, then asks the real waterfall.
//
// EVERY ONE OF THE THREE IS RESOLVED THROUGH THE CALLER'S OWN FENCE: the product through inventory's
// caller-scoped lookup, the account through customer.LookupAccount (the POS picker's rule), the
// branch through the caller's granted list. A price for a customer at a yard the asker cannot reach
// is not an answer this seam can produce, because none of the three resolves would return it.
//
// found=false when the product or the account does not resolve. That is an ANSWER, "I couldn't
// price that for them", and the command says so, rather than reporting a fault.
func (a priceAdapter) PriceFor(ctx context.Context, c assist.Caller, productRef, customerRef, branchRef string) (assist.PriceQuote, bool, error) {
	p, _, found, err := a.stock.LookupStock(ctx, stockReach(c), productRef)
	if err != nil || !found {
		return assist.PriceQuote{}, false, err
	}
	acct, found, err := a.accounts.LookupAccount(ctx, accountReach(c), customerRef)
	if err != nil || !found {
		return assist.PriceQuote{}, false, err
	}

	branchID, branchName := a.pickBranch(ctx, c, branchRef)

	custType := ""
	if a.custType != nil {
		if t, terr := a.custType.CustomerType(ctx, acct.ID); terr == nil {
			custType = t
		}
	}
	var costCents int64
	if a.cost != nil && branchID != "" {
		if v, _, cerr := a.cost.CurrentCostCents(ctx, p.ProductID, branchID); cerr == nil {
			costCents = v
		}
	}

	bd, err := a.pricer.ResolvePricePreview(ctx, pricing.ResolveInput{
		CustomerID:   acct.ID,
		CustomerType: custType,
		ProductID:    p.ProductID,
		BranchID:     branchID,
		// ONE UNIT, IN THE PRODUCT'S OWN BASE UNIT. The question is "what does it cost", not "what
		// does this line come to", so the quantity is the smallest one that resolves, and the answer
		// reports a UNIT price, which is what a person repeats on the phone. A quantity break that
		// only applies to a skid is therefore NOT applied, which is correct: quoting a break price
		// for one piece would be the same lie in the other direction.
		QtyEntered: 1,
		UOMEntered: p.UOM,
		CostCents:  costCents,
	})
	if err != nil {
		// A product the waterfall cannot price (no anchor, an unknown category) is an ANSWER, not a
		// fault: the command says "I couldn't price that", which is true and actionable.
		if isNotFoundish(err) {
			return assist.PriceQuote{}, false, nil
		}
		return assist.PriceQuote{}, false, err
	}
	return assist.PriceQuote{
		ProductID:    p.ProductID,
		SKU:          p.SKU,
		Name:         p.Name,
		CustomerName: acct.Name,
		BranchName:   branchName,
		// Every number below is the resolver's own. Nothing here multiplies, discounts or rounds.
		OriginalCents: bd.OriginalCents,
		FinalCents:    bd.FinalCents,
		DiscountBps:   bd.DiscountBps,
		PriceUOM:      bd.PriceUOM,
		Source:        bd.Source,
		Detail:        bd.Detail,
		Clearance:     bd.Clearance,
		PromoName:     bd.PromoName,
	}, true, nil
}

// pickBranch chooses the yard the price is FOR: the one named, if the caller can reach it, else the
// caller's single granted yard, else none.
//
// A NAMED BRANCH THE CALLER CANNOT REACH RESOLVES TO NONE, not to an error and not to a substitute.
// The price then comes back branchless, the waterfall's own behaviour for a caller with no branch
// context, and the answer simply does not name a yard, rather than quietly pricing at one the
// person did not ask about.
func (a priceAdapter) pickBranch(ctx context.Context, c assist.Caller, want string) (string, string) {
	rows := a.reachableBranches(ctx, c)
	if w := strings.TrimSpace(want); w != "" {
		for _, b := range rows {
			if strings.EqualFold(b.Name, w) || strings.EqualFold(b.Code, w) || containsFold(b.Name, w) {
				return b.ID, b.Name
			}
		}
		return "", ""
	}
	if len(rows) == 1 {
		return rows[0].ID, rows[0].Name
	}
	return "", ""
}

func (a priceAdapter) reachableBranches(ctx context.Context, c assist.Caller) []branches.Branch {
	if a.branches == nil {
		return nil
	}
	all, err := a.branches.List(ctx)
	if err != nil {
		return nil
	}
	return fenceBranches(c, all)
}

// -- branches: the vocabulary a conversation resolves against -----------------------------------

// BranchLister is what this adapter needs from internal/branches.
type BranchLister interface {
	List(ctx context.Context) ([]branches.Branch, error)
}

type branchAdapter struct{ svc BranchLister }

var (
	_ assist.BranchReader = branchAdapter{}
	_ BranchNamer         = branchAdapter{}
)

// NewBranchReader builds the seam.
func NewBranchReader(svc BranchLister) assist.BranchReader {
	if svc == nil {
		return nil
	}
	return branchAdapter{svc: svc}
}

// NewBranchNamer is the same adapter under the name documents.go consumes it by.
func NewBranchNamer(svc BranchLister) BranchNamer {
	if svc == nil {
		return nil
	}
	return branchAdapter{svc: svc}
}

// Branches lists the yards THIS caller may ask about.
//
// THE FENCE IS APPLIED HERE AND IT IS A FILTER, NOT A WIDENING. branches.List is the whole company's
// list, it is the picker's source and it has no caller in its signature, so this adapter narrows
// it to the caller's own grants. That is the only direction this can go: a caller's grants are the
// authority, and a name that is not among them is not a name the assistant will admit exists.
//
// It matters more here than it looks. The resolver asks this to decide whether "Kingston" in "what
// about Kingston?" is a yard; if the list were unfenced, a scoped user could learn the names of
// yards they were never granted by watching which words the assistant recognised.
func (a branchAdapter) Branches(ctx context.Context, c assist.Caller) ([]assist.NamedBranch, error) {
	all, err := a.svc.List(ctx)
	if err != nil {
		return nil, err
	}
	rows := fenceBranches(c, all)
	out := make([]assist.NamedBranch, 0, len(rows))
	for _, b := range rows {
		out = append(out, assist.NamedBranch{BranchID: b.ID, Code: b.Code, Name: b.Name})
	}
	return out, nil
}

// BranchNames is the id→name map the quote-hold answer uses so a hold reads "Kingston" rather than a
// uuid. Same fence, same reason.
func (a branchAdapter) BranchNames(ctx context.Context, c assist.Caller) (map[string]string, error) {
	rows, err := a.Branches(ctx, c)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(rows))
	for _, b := range rows {
		out[b.BranchID] = b.Name
	}
	return out, nil
}

// fenceBranches is the one place the caller's grants narrow a company-wide list.
//
// THREE CASES, AND THE MIDDLE ONE IS THE POINT, the same rule stockReach encodes for a SQL fence:
// global sees all; granted sees those; NO GRANTS SEES NOTHING. An empty grant list must never fall
// through to "everything".
func fenceBranches(c assist.Caller, all []branches.Branch) []branches.Branch {
	if c.Global {
		return activeOnly(all)
	}
	granted := map[string]bool{}
	for _, id := range c.BranchIDs {
		granted[id] = true
	}
	out := make([]branches.Branch, 0, len(granted))
	for _, b := range activeOnly(all) {
		if granted[b.ID] {
			out = append(out, b)
		}
	}
	return out
}

// activeOnly drops retired yards. A closed branch is not somewhere stock is arriving and not
// somewhere a price is quoted, and offering its name as a conversational referent would resolve a
// follow-up onto a yard that no longer trades.
func activeOnly(all []branches.Branch) []branches.Branch {
	out := make([]branches.Branch, 0, len(all))
	for _, b := range all {
		if b.IsActive {
			out = append(out, b)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

var _ = errors.Is
var _ = inventory.BranchReach{}
