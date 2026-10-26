package pos

// counterprice.go, the counter's CUSTOMER-AWARE PRICE READ.
//
// The client, after a day on the redesigned counter: "should display retail + the account specific
// pricing depending on the customer type, should also display savings over retail for special
// pricing and bulk pricing".
//
// This file is the thin POS half of that. It owns NO pricing logic whatsoever: it resolves who the
// customer is (the account's own customer_type beats whatever the client claimed, a browser must
// not be able to talk itself into contractor pricing), then hands the whole question to
// pricing.CounterQuotes, which is the same waterfall the ring itself prices through. Every cent on
// the way out, retail, the customer's price, the saving, the bulk threshold, was decided there.
//
// COST IS NOT HERE AND CANNOT BE. pricing.CounterQuote has no margin, floor, cost or profit field
// to leak; the dealer's side of the counter stays in MarginGlance behind its own role gate. That
// is a property of the type, not a discipline of this handler.

import (
	"context"
	"net/http"
	"strings"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pricing"
)

// maxCounterPriceItems bounds one read. The counter's biggest real ask is a full search page plus
// the quick-pick grid; well under this.
const maxCounterPriceItems = 60

// CounterPriceInput is POST /api/v1/pos/counter-prices.
//
// CustomerID empty is the WALK-IN case and is completely ordinary: retail stands alone, and every
// row comes back with no saving and no basis. The counter never has to ask a different question
// for a walk-in than for an account, it asks this one, always, and renders what comes back.
type CounterPriceInput struct {
	BranchID   string             `json:"branch_id"`
	CustomerID string             `json:"customer_id"`
	Items      []CounterPriceItem `json:"items"`
}

// CounterPriceItem names one product at one quantity. UOM empty resolves to the product's sell
// unit server-side, so a quick-pick tile can ask about a product without knowing how it is sold.
type CounterPriceItem struct {
	ProductID  string  `json:"product_id"`
	QtyEntered float64 `json:"qty_entered"`
	UOMEntered string  `json:"uom_entered"`
}

// CounterPrices is the answer: the resolved rows, plus the customer type they were priced for so
// the counter can say WHOSE prices these are without a second read (and without trusting its own
// copy of the account).
type CounterPrices struct {
	CustomerType string                 `json:"customer_type"`
	Prices       []pricing.CounterQuote `json:"prices"`
}

// CounterQuoter is the pricing authority's counter-facing door, satisfied by *pricing.Service.
// Separate interface from LineResolver because it is a strictly READ-ONLY question, no audit
// rows, no gate side effects, and a fake in a unit test should be able to answer one without the
// other.
type CounterQuoter interface {
	CounterQuotes(ctx context.Context, ins []pricing.CounterQuoteInput) ([]pricing.CounterQuote, error)
}

// counterQuoter returns the counter price authority, ASSERTED ONTO THE LINE RESOLVER ALREADY
// INJECTED rather than demanded as a second constructor argument.
//
// This is pricing's own listPriceBulkStore idiom (listprice_bulk.go), adopted for the same two
// reasons it was chosen there. First, the several fake resolvers in this package's unit tests keep
// compiling unchanged, a test that only ever rings a sale does not have to learn a read it does
// not use. Second, and the reason it matters here: *pricing.Service is ALREADY wired as the line
// resolver in the composition root, so the counter price read needs no new wiring line at all. The
// authority the counter displays from is, by construction, the identical object the ring prices
// through, not a second injection someone could point somewhere else.
//
// An explicit WithCounterQuoter override wins when set (tests). A resolver that satisfies neither
// leaves the read unwired, and unwired answers an empty screen, never an error, never a $0.00.
func (s *Service) counterQuoter() CounterQuoter {
	if s.counterPrices != nil {
		return s.counterPrices
	}
	if q, ok := s.pricing.(CounterQuoter); ok {
		return q
	}
	return nil
}

// WithCounterQuoter overrides the asserted authority, for tests, and for a future composition
// root that wants the read pointed somewhere other than the ring's resolver.
func (s *Service) WithCounterQuoter(q CounterQuoter) *Service { s.counterPrices = q; return s }

// CounterPrices answers the counter's price picture for a set of products.
//
// EMPTY IS NEVER AN ERROR. No items, no wired resolver, an unknown customer, a product the
// waterfall cannot price, each yields an ordinary empty or Unpriceable row, and the counter draws
// a screen with no price on that tile rather than a fault banner. A price nobody can compute must
// never render as $0.00.
func (s *Service) CounterPrices(ctx context.Context, in CounterPriceInput) (CounterPrices, error) {
	// THE BRANCH WALL, before any customer or price is read: a read that prices at a branch the caller's
	// resolved authority does not reach is refused with the words create sale gives for it, never answered
	// at that branch's rules and the named account's prices. A read naming no branch is the dealer wide
	// picture and stays answered; a caller that cannot be resolved is the 401 the guard's door would give.
	caller, err := s.caller(ctx)
	if err != nil {
		return CounterPrices{}, err
	}
	if branch := strings.TrimSpace(in.BranchID); branch != "" && !caller.CanAccessBranch(branch) {
		return CounterPrices{}, clientErr(http.StatusForbidden, "pos: caller cannot access branch %s", branch)
	}
	out := CounterPrices{CustomerType: "RETAIL", Prices: []pricing.CounterQuote{}}
	quoter := s.counterQuoter()
	if len(in.Items) == 0 || quoter == nil {
		return out, nil
	}
	if len(in.Items) > maxCounterPriceItems {
		return CounterPrices{}, clientErr(http.StatusUnprocessableEntity,
			"pos: at most 60 items per counter-price read")
	}
	// THE CUSTOMER TYPE COMES FROM THE ACCOUNT, NOT FROM THE WIRE. The terminal knows the customer
	// it picked, but "what tier is this account" is a pricing input and therefore a server fact.
	// THE CUSTOMER WALL: an id that is no account and an account the caller cannot reach (homed at a branch
	// outside its grants) answer the same, 404 "customer not found" (customerFor), never the account's type
	// or contract prices and never a quiet walk-in price that would tell the two apart.
	customerID := strings.TrimSpace(in.CustomerID)
	if customerID != "" {
		cust, err := s.customerFor(ctx, caller, customerID)
		if err != nil {
			return CounterPrices{}, err
		}
		if cust.CustomerType != "" {
			out.CustomerType = cust.CustomerType
		}
	}
	ins := make([]pricing.CounterQuoteInput, 0, len(in.Items))
	for _, it := range in.Items {
		if strings.TrimSpace(it.ProductID) == "" {
			continue
		}
		qty := it.QtyEntered
		if qty <= 0 {
			qty = 1
		}
		ins = append(ins, pricing.CounterQuoteInput{
			CustomerID:   customerID,
			CustomerType: out.CustomerType,
			ProductID:    it.ProductID,
			BranchID:     in.BranchID,
			QtyEntered:   qty,
			UOMEntered:   it.UOMEntered,
		})
	}
	if len(ins) == 0 {
		return out, nil
	}
	rows, err := quoter.CounterQuotes(ctx, ins)
	if err != nil {
		return CounterPrices{}, err
	}
	if rows != nil {
		out.Prices = rows
	}
	return out, nil
}
