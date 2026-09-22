package adapters

// quotecatalogue.go, quoteassist.CatalogueSearch over the POS counter's OWN ranked product search.
//
// READ-ONLY, AND REUSED RATHER THAN REBUILT. product.Service.CounterSearch already encodes the
// relevance order a counter needs and has been tested against the real catalogue since W3:
//
//	SKU exact / barcode → SKU prefix → name prefix → name → description → synonym → trigram
//
// It also carries the yard's OWN synonym table (product_search_synonyms), which is the single most
// valuable thing an extractor could possibly have: it is where "patio stone" already means Slab and
// where a yard writes down its customers' words. An extractor with its own matcher would be a second
// opinion about what "Blu 60" means, and the two opinions would differ in front of a customer.
//
// WHAT THIS ADAPTER ADDS: nothing. It renames CounterHit's fields onto CatalogueHit's and passes the
// match reason through untouched. No re-ranking, no filtering, no "improving" the order, the
// counter's ordering is the ordering, because the whole argument for reuse collapses the moment this
// file starts having views.

import (
	"context"
	"errors"
	"strings"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/product"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/quoteassist"
)

// CounterSearcher is what this adapter needs from the catalogue module, declared HERE (the consumer
// declares the interface) so the adapter is testable against a double that records the query it was
// handed, the only way to assert from outside that the extractor's term actually reached the search.
type CounterSearcher interface {
	CounterSearch(ctx context.Context, q product.CounterSearchQuery) ([]product.CounterHit, error)
	// ConvertUOM is the catalogue's OWN answer to "can this product be quoted in this unit": it
	// returns product_uoms.qty_in_base and product.ErrNoConversion when no row exists.
	//
	// THE FACTOR IS READ AND THROWN AWAY. quoteassist asks a yes/no question (QuotableUnit) and this
	// adapter is the only thing that ever sees the number, because the multiplication belongs to
	// pricing, which owns it, and a second copy inside a text extractor would be a second answer to
	// "how many pieces in 480 square feet".
	ConvertUOM(ctx context.Context, productID, fromUOM string) (float64, error)
}

// The shipped service satisfies it. This line is PARK-3 risk discharged by the compiler rather than
// by a paragraph: main.go is outside this lane's fence, so the wiring block is pasted by someone
// else, and a seam that did not fit would otherwise surface in THEIR paste.
var _ CounterSearcher = (*product.Service)(nil)

type quoteCatalogueAdapter struct{ svc CounterSearcher }

var _ quoteassist.CatalogueSearch = quoteCatalogueAdapter{}

// NewQuoteCatalogueSearch builds the seam. A nil service returns a NIL SEAM, not a seam that panics:
// quoteassist reads a nil catalogue as "not wired in this deployment" and answers the extract route
// with an honest 503 while scope-gap detection keeps working. A partial wiring is a normal state
// during a rollout; a nil-pointer panic inside an optional feature is an outage in something nobody
// depends on yet.
func NewQuoteCatalogueSearch(svc CounterSearcher) quoteassist.CatalogueSearch {
	if svc == nil {
		return nil
	}
	return quoteCatalogueAdapter{svc: svc}
}

// SearchCatalogue runs one extracted phrase through the counter search.
//
// The branch id scopes the ON-HAND FIGURES the counter search attaches, and nothing else, a product
// is the same product at every branch. It is passed through because an absent branch makes the
// search return every branch's on-hand, which is a bigger payload for information this surface does
// not render. It is never used to hide a product: an extractor that could not see a product the
// catalogue screen shows would produce "couldn't match" rows for things the yard sells.
func (a quoteCatalogueAdapter) SearchCatalogue(ctx context.Context, q quoteassist.CatalogueQuery) ([]quoteassist.CatalogueHit, error) {
	hits, err := a.svc.CounterSearch(ctx, product.CounterSearchQuery{
		Q:        q.Text,
		BranchID: q.BranchID,
		Limit:    q.Limit,
	})
	if err != nil {
		return nil, err
	}
	// An EMPTY (non-nil) slice for no hits. "Nothing in the catalogue matched that" is an answer the
	// extractor renders as a "couldn't match" row, and it must not arrive looking like an absence.
	out := make([]quoteassist.CatalogueHit, 0, len(hits))
	for _, h := range hits {
		out = append(out, quoteassist.CatalogueHit{
			ProductID: h.ProductID,
			SKU:       h.SKU,
			Name:      h.Name,
			BaseUOM:   h.BaseUOM,
			// SoldBy carried as SellUOM, EMPTY WHEN EMPTY. An earlier version defaulted it to the
			// base unit here, which put "PC" on a proposal for every one of the 30 products (of 32)
			// whose sold_by the catalogue leaves blank, a unit nobody had chosen, printed as though
			// somebody had. quoteassist decides what to do with an absent sell unit (units.go
			// defaultUOM), because that is a presentation decision and this is a mapping.
			SellUOM:     h.SoldBy,
			MatchReason: h.MatchReason,
		})
	}
	return out, nil
}

// QuotableUnit asks the catalogue whether this product has a factor for this unit.
//
// ONE QUERY, ONE BOOLEAN, NO ARITHMETIC. product.ErrNoConversion is the catalogue saying "we do not
// quote this product in that unit", an ANSWER, not a fault, so it comes back as false with no error.
// Any other failure is carried, because "the catalogue could not be asked" and "the catalogue said no"
// must not look the same to a rep: the first leaves the unit for them to confirm, the second tells
// them the yard does not sell it that way.
//
// A blank product id or unit is false with no query. Both are client bugs rather than questions, and
// neither is worth a round-trip to establish.
func (a quoteCatalogueAdapter) QuotableUnit(ctx context.Context, productID, uomCode string) (bool, error) {
	if strings.TrimSpace(productID) == "" || strings.TrimSpace(uomCode) == "" {
		return false, nil
	}
	if _, err := a.svc.ConvertUOM(ctx, productID, uomCode); err != nil {
		if errors.Is(err, product.ErrNoConversion) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
