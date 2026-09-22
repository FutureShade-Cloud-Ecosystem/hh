package adapters

// wiring.go, DIB-478. The compile-time proof that the DEEP seams fit the SHIPPED services, and the
// two-line helper that keeps the composition root's paste from having to invent an adapter.
//
// WHY THIS FILE EXISTS AT ALL. main.go is outside this lane's fence, so the wiring block is written
// as a patch (PARK-478-main-wiring.patch) and pasted by someone else. A seam that did not fit would
// otherwise surface in THEIR paste, at the least convenient moment, in a file this ticket is not
// allowed to touch. Every assertion below turns that into a build error in the package that owns
// the mistake, which is the same argument adapters.go makes for the original five, applied to
// eleven more.
//
// AssistDeps is the second half of the same idea: rather than leaving the paste to assemble eleven
// constructors in the right order, it assembles them HERE, where they are tested, and the patch
// becomes a single call. The composition root still decides WHICH services to hand over, that is
// its job, and nothing here reaches for one.

import (
	"context"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/branches"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customer"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/inventory"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/messaging"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pricing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/quote"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/reporting"
)

// -- the shipped services satisfy the DIB-478 seams ---------------------------------------------

var (
	_ RollupTodayReader = (*messaging.Service)(nil)
	_ ReportRunner      = (*reporting.Service)(nil)
	_ CreditDeskReader  = (*billing.CreditService)(nil)
	_ InvoiceLister     = (*billing.Service)(nil)
	_ OrderLister       = (*order.Service)(nil)
	_ QuoteDeskReader   = (*quote.StaffService)(nil)
	_ PriceResolver     = (*pricing.Service)(nil)
	_ CostBasisReader   = (*inventory.Service)(nil)
	_ BranchLister      = (*branches.Service)(nil)
)

// -- the one adapter the composition root would otherwise have to write -------------------------

// customerTyper resolves an account's pricing tier over customer.Service.
//
// IT IS THE THIRD COPY OF THIS BODY IN THE CODEBASE, and that is deliberate rather than sloppy:
// internal/order and internal/quote each declare their OWN narrow CustomerTypeResolver, because a
// shared one would make either module able to reach the other's (or the customer module's) types.
// main.go already carries two identical adapters for exactly that reason. This one lives HERE rather
// than there only because it is the one seam of the eleven whose adapter is not already written, and
// putting it in the fence means the wiring patch adds no new type to a file this ticket may not edit.
type customerTyper struct{ svc *customer.Service }

var _ CustomerTyper = customerTyper{}

// NewCustomerTyper builds the pricing-tier seam over the shipped customer service.
func NewCustomerTyper(svc *customer.Service) CustomerTyper {
	if svc == nil {
		return nil
	}
	return customerTyper{svc: svc}
}

func (a customerTyper) CustomerType(ctx context.Context, customerID string) (string, error) {
	acct, err := a.svc.ResolveAccount(ctx, customerID)
	if err != nil {
		return "", err
	}
	return acct.CustomerType, nil
}

// -- the whole deep seam set, assembled ---------------------------------------------------------

// Services is what the composition root hands over: the SHIPPED services, not copies and not
// wrappers. Every field is optional, a nil service yields a nil seam, which the assist package
// reads as "not switched on in this deployment" for that command alone.
type Services struct {
	Messaging  *messaging.Service
	Reporting  *reporting.Service
	Credit     *billing.CreditService
	Billing    *billing.Service
	Orders     *order.Service
	Quotes     *quote.StaffService
	Inventory  *inventory.Service
	Purchasing OpenPOReader
	Pricing    *pricing.Service
	Customers  *customer.Service
	Catalogue  CounterSearcher
	Branches   *branches.Service
}

// DeepReaders builds the DIB-478 half of assist.Deps.
//
// It RETURNS A PARTIAL Deps rather than mutating one, so the composition root can see exactly which
// fields this ticket owns and which it does not, the original four (Payments, Orders, Stock,
// Customers) are still wired one by one in main.go, by name, where a reviewer can read them.
func DeepReaders(s Services) assist.Deps {
	branchSeam := NewBranchReader(nilIfNoBranches(s.Branches))
	return assist.Deps{
		Rollup:       NewRollupReader(nilIfNoMessaging(s.Messaging)),
		PaymentRange: NewPaymentRangeReader(nilIfNoReporting(s.Reporting)),
		AR:           NewARReader(nilIfNoCredit(s.Credit)),
		Invoices:     NewInvoiceReader(nilIfNoBilling(s.Billing)),
		OrderList:    NewOrderListReader(nilIfNoOrders(s.Orders)),
		Quotes:       NewQuoteReader(nilIfNoQuotes(s.Quotes), NewBranchNamer(nilIfNoBranches(s.Branches))),
		Incoming:     NewIncomingReader(s.Purchasing, nilIfNoInventory(s.Inventory), NewBranchNamer(nilIfNoBranches(s.Branches))),
		Catalogue:    NewCatalogueReader(s.Catalogue),
		Price: NewPriceReader(
			nilIfNoPricing(s.Pricing),
			NewCustomerTyper(s.Customers),
			nilIfNoCostBasis(s.Inventory),
			nilIfNoCustomers(s.Customers),
			nilIfNoInventory(s.Inventory),
			nilIfNoBranches(s.Branches),
		),
		Branches: branchSeam,
		// DIB-494: the order's merged audit trail, over the SAME order service the composition root
		// already hands this function. No new Services field and no main.go change, the seam rides
		// the wiring DIB-478 shipped.
		Activity: NewOrderActivityReader(nilIfNoOrderTrail(s.Orders)),
	}
}

// -- typed nil guards ---------------------------------------------------------------------------
//
// A nil *T assigned into a non-nil interface is the oldest trap in Go and it is exactly the trap a
// "nil service means an unwired seam" contract walks into: `NewARReader((*billing.CreditService)(nil))`
// would build a non-nil seam over a nil pointer and panic on first use, which is precisely the
// nil-pointer-panic-inside-an-assistant that stock.go's constructor exists to prevent. These convert
// a typed nil into an untyped one so the constructors' `if svc == nil` means what it says.

func nilIfNoMessaging(s *messaging.Service) RollupTodayReader {
	if s == nil {
		return nil
	}
	return s
}
func nilIfNoReporting(s *reporting.Service) ReportRunner {
	if s == nil {
		return nil
	}
	return s
}
func nilIfNoCredit(s *billing.CreditService) CreditDeskReader {
	if s == nil {
		return nil
	}
	return s
}
func nilIfNoBilling(s *billing.Service) InvoiceLister {
	if s == nil {
		return nil
	}
	return s
}
func nilIfNoOrders(s *order.Service) OrderLister {
	if s == nil {
		return nil
	}
	return s
}
func nilIfNoOrderTrail(s *order.Service) OrderTrailReader {
	if s == nil {
		return nil
	}
	return s
}
func nilIfNoQuotes(s *quote.StaffService) QuoteDeskReader {
	if s == nil {
		return nil
	}
	return s
}
func nilIfNoInventory(s *inventory.Service) InTransitReader {
	if s == nil {
		return nil
	}
	return s
}
func nilIfNoCostBasis(s *inventory.Service) CostBasisReader {
	if s == nil {
		return nil
	}
	return s
}
func nilIfNoPricing(s *pricing.Service) PriceResolver {
	if s == nil {
		return nil
	}
	return s
}
func nilIfNoCustomers(s *customer.Service) AccountLookup {
	if s == nil {
		return nil
	}
	return s
}
func nilIfNoBranches(s *branches.Service) BranchLister {
	if s == nil {
		return nil
	}
	return s
}
