package adapters

// documents.go, DIB-478. Invoices, orders and quotes: the paper a conversation is usually about.
//
// TWO DIFFERENT FENCES, AND THE DIFFERENCE IS THE DESIGN
// -----------------------------------------------------
// INVOICES and ORDERS are read through TENANT-SCOPED lists (billing.ListInvoices, order.ListOrders):
// both take a customer id and nothing else, because both were written for the portal, where the
// customer IS the session. Staff do not have a customer in their session, so the assist commands
// resolve the account FIRST through customer.LookupAccount, the caller-fenced resolve every other
// account question already uses, and hand the resolved id in. The fence is therefore the RESOLVE,
// exactly as it is for customer.balance (customer.go), and these reads are unreachable without one.
//
// QUOTES are read through the quote module's own STAFF list, which fences itself off the request
// guard's caller (callerBranchScope). No id is threaded in, because that module already knows how to
// answer "which quotes may this person see" and a second opinion here would be a second answer.
//
// Neither shape is better. What matters is that in both cases the fence belongs to the module that
// owns the data, and this file only translates.

import (
	"context"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/quote"
)

// -- invoices -----------------------------------------------------------------------------------

// InvoiceLister is what this adapter needs from internal/billing.
type InvoiceLister interface {
	ListInvoices(ctx context.Context, customerID string, f billing.Filter) ([]billing.Invoice, error)
}

type invoiceAdapter struct{ svc InvoiceLister }

var _ assist.InvoiceReader = invoiceAdapter{}

// NewInvoiceReader builds the seam.
func NewInvoiceReader(svc InvoiceLister) assist.InvoiceReader {
	if svc == nil {
		return nil
	}
	return invoiceAdapter{svc: svc}
}

// InvoicesFor lists one resolved account's invoices.
//
// AN UNKNOWN STATUS IS DROPPED, NOT REFUSED. billing rejects a status outside its CHECK set with a
// 400, which is right for a form; a person typing "unpaid" into a chat window has not made a
// protocol error, and answering "unknown invoice status" instead of showing them the invoices would
// be a pedantic refusal. So an unrecognised word simply stops being a filter and the answer is the
// whole list, which is what they can see anyway.
func (a invoiceAdapter) InvoicesFor(ctx context.Context, c assist.Caller, customerID, status string, limit int) ([]assist.InvoiceRecord, error) {
	// Cursor carry note (HH-3.2 fold): the tenant list's total echo left with the
	// offset family; the scan reads its page and filters on the rows, as before.
	rows, err := a.svc.ListInvoices(ctx, customerID, billing.Filter{Status: knownInvoiceStatus(status), Limit: limit})
	if err != nil {
		return nil, err
	}
	out := make([]assist.InvoiceRecord, 0, len(rows))
	for _, inv := range rows {
		out = append(out, assist.InvoiceRecord{
			InvoiceID:    inv.ID,
			InvoiceNo:    inv.InvoiceNo,
			Status:       inv.Status,
			IssueDate:    inv.IssueDate,
			DueDate:      inv.DueDate,
			TotalCents:   inv.TotalCents,
			BalanceCents: inv.BalanceCents,
		})
	}
	return out, nil
}

// invoiceStatuses mirrors the 0-series CHECK. Kept here rather than reaching for an unexported
// validator in billing: this is a TOLERANCE decision made by the consumer, not a rule owned by the
// module, and pretending otherwise would put a chat window's manners inside the billing domain.
var invoiceStatuses = map[string]bool{
	"DRAFT": true, "FINALIZED": true, "PARTIALLY_PAID": true, "PAID": true, "VOID": true,
}

func knownInvoiceStatus(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	if invoiceStatuses[s] {
		return s
	}
	return ""
}

// -- orders -------------------------------------------------------------------------------------

// OrderLister is what this adapter needs from internal/order.
type OrderLister interface {
	ListOrders(ctx context.Context, customerID string, f order.Filter) ([]order.Order, error)
}

type orderListAdapter struct{ svc OrderLister }

var _ assist.OrderListReader = orderListAdapter{}

// NewOrderListReader builds the seam.
func NewOrderListReader(svc OrderLister) assist.OrderListReader {
	if svc == nil {
		return nil
	}
	return orderListAdapter{svc: svc}
}

// OrdersFor lists one resolved account's orders, carrying the FULFILMENT method through.
//
// The fulfilment method is what makes this answer worth asking for rather than reading off the order
// list: "three confirmed, two of them deliveries" is the shape of the question a counter actually
// asks, and it is one column the portal list already has.
func (a orderListAdapter) OrdersFor(ctx context.Context, c assist.Caller, customerID, status string, limit int) ([]assist.OrderLine, error) {
	rows, err := a.svc.ListOrders(ctx, customerID, order.Filter{Status: knownOrderStatus(status), Limit: limit})
	if err != nil {
		return nil, err
	}
	out := make([]assist.OrderLine, 0, len(rows))
	for _, o := range rows {
		out = append(out, assist.OrderLine{
			OrderID:     o.ID,
			OrderNo:     o.OrderNo,
			Status:      o.Status,
			Fulfillment: o.Fulfillment,
			Requested:   o.RequestedDate,
			TotalCents:  o.TotalCents,
		})
	}
	return out, nil
}

// orderStatuses mirrors the 0008 CHECK, see knownInvoiceStatus for why the tolerance lives here.
var orderStatuses = map[string]bool{
	"DRAFT": true, "CONFIRMED": true, "ALLOCATED": true, "PICKING": true, "PICKED": true,
	"FULFILLED": true, "INVOICED": true, "CLOSED": true, "CANCELLED": true,
}

func knownOrderStatus(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	if orderStatuses[s] {
		return s
	}
	return ""
}

// -- quotes -------------------------------------------------------------------------------------

// QuoteDeskReader is what this adapter needs from internal/quote's staff service. All three reads
// fence themselves off the request guard's caller.
type QuoteDeskReader interface {
	StaffQuoteList(ctx context.Context, f quote.Filter) ([]quote.StaffQuoteSummary, error)
	StaffQuoteRead(ctx context.Context, quoteIDOrNo string) (quote.StaffQuoteView, error)
	QuoteHolds(ctx context.Context, quoteIDOrNo string) ([]quote.QuoteHold, error)
}

// BranchNamer resolves branch ids to the names people say. Optional: a quote hold that cannot be
// given a branch NAME still reports its quantity and its lapse date, which is most of the answer.
type BranchNamer interface {
	BranchNames(ctx context.Context, c assist.Caller) (map[string]string, error)
}

type quoteAdapter struct {
	svc     QuoteDeskReader
	branchN BranchNamer
}

var _ assist.QuoteReader = quoteAdapter{}

// NewQuoteReader builds the seam.
func NewQuoteReader(svc QuoteDeskReader, names BranchNamer) assist.QuoteReader {
	if svc == nil {
		return nil
	}
	return quoteAdapter{svc: svc, branchN: names}
}

// Quotes lists the caller's own reachable quotes, optionally narrowed to one account BY NAME.
//
// THE CUSTOMER NARROWING IS DONE HERE, ON THE ROWS, and that is a deliberate limitation stated
// plainly: quote.Filter carries a status and a page, not a customer, and adding a customer to
// another module's filter is not this ticket's change to make. Filtering the page that came back
// means the narrowing sees only that page, so the command asks for a page and the answer is "the
// most recent quotes for that account", which is what the question means at a counter. (Parked in
// NOTES-FIX-478.md: a customer filter on quote.Filter, if this ever needs to be exhaustive.)
func (a quoteAdapter) Quotes(ctx context.Context, c assist.Caller, customerRef, status string, limit int) ([]assist.QuoteLine, error) {
	page := limit
	if strings.TrimSpace(customerRef) != "" && page < quoteScanPage {
		// Widen the page when we are going to filter it, so a name match is not lost behind newer
		// quotes for other accounts.
		page = quoteScanPage
	}
	// Cursor carry note (HH-3.2 fold): the staff list's total echo left with the offset
	// family (batch B4); the scan reads its page and narrows on the rows, as before.
	rows, err := a.svc.StaffQuoteList(ctx, quote.Filter{Status: knownQuoteStatus(status), Limit: page})
	if err != nil {
		return nil, err
	}
	out := make([]assist.QuoteLine, 0, len(rows))
	for _, q := range rows {
		if ref := strings.TrimSpace(customerRef); ref != "" && !containsFold(q.CustomerName, ref) {
			continue
		}
		out = append(out, quoteLine(q))
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// quoteScanPage is how far back the customer narrowing looks. Big enough that a named account's
// recent quotes are found, small enough that a chat question does not page the whole quote book.
const quoteScanPage = 100

func quoteLine(q quote.StaffQuoteSummary) assist.QuoteLine {
	return assist.QuoteLine{
		QuoteID:      q.ID,
		QuoteNo:      q.QuoteNo,
		CustomerName: q.CustomerName,
		Status:       q.Status,
		ValidUntil:   q.ValidUntil,
		// Expired is the quote module's OWN computed flag, carried through untouched. There is no
		// EXPIRED status in that schema and deriving one here from ValidUntil would be a second
		// definition of a live quote.
		Expired:    q.Expired,
		TotalCents: q.TotalCents,
		UpdatedAt:  q.UpdatedAt,
	}
}

var quoteStatuses = map[string]bool{
	"DRAFT": true, "SENT": true, "ACCEPTED": true, "REJECTED": true, "EXPIRED": true, "CONVERTED": true,
}

func knownQuoteStatus(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	if quoteStatuses[s] {
		return s
	}
	return ""
}

// QuoteHolds reads one quote's header and the stock it is holding.
//
// A QUOTE THE CALLER CANNOT SEE IS found=false, NOT AN ERROR. The quote module answers a number
// outside the caller's reach with a not-found (resolveQuoteForCaller collapses "no such quote" and
// "not yours" deliberately, so a refusal cannot be used as an oracle), and the honest thing here is
// to carry that collapse rather than to re-open it.
func (a quoteAdapter) QuoteHolds(ctx context.Context, c assist.Caller, quoteRef string) (assist.QuoteLine, []assist.QuoteHoldLine, bool, error) {
	view, err := a.svc.StaffQuoteRead(ctx, strings.TrimSpace(quoteRef))
	if err != nil {
		if isNotFoundish(err) {
			return assist.QuoteLine{}, nil, false, nil
		}
		return assist.QuoteLine{}, nil, false, err
	}
	holds, err := a.svc.QuoteHolds(ctx, strings.TrimSpace(quoteRef))
	if err != nil {
		if isNotFoundish(err) {
			return assist.QuoteLine{}, nil, false, nil
		}
		return assist.QuoteLine{}, nil, false, err
	}
	names := map[string]string{}
	if a.branchN != nil {
		if m, nerr := a.branchN.BranchNames(ctx, c); nerr == nil {
			names = m
		}
	}
	out := make([]assist.QuoteHoldLine, 0, len(holds))
	for _, h := range holds {
		out = append(out, assist.QuoteHoldLine{
			ProductName: h.ProductName,
			BranchName:  branchNameOr(names, h.BranchID),
			QtyBase:     h.QtyBase,
			Status:      h.HoldMode,
			ExpiresAt:   h.ExpiresAt,
		})
	}
	return assist.QuoteLine{
		QuoteID:      view.ID,
		QuoteNo:      view.QuoteNo,
		CustomerName: view.CustomerName,
		Status:       view.Status,
		ValidUntil:   view.ValidUntil,
		Expired:      view.Expired,
	}, out, true, nil
}

// branchNameOr never prints a raw uuid at a person. An id nobody could name reads as "another yard",
// which is true, short, and not a database key on a chat line.
func branchNameOr(names map[string]string, id string) string {
	if n := names[id]; n != "" {
		return n
	}
	if id == "" {
		return ""
	}
	return "another yard"
}

// isNotFoundish recognises the "there is nothing here for you" shape across the modules this file
// reads, WITHOUT importing their sentinel errors where they are unexported.
//
// It is deliberately conservative: only a not-found or a forbidden becomes an empty answer. Anything
// else is carried as an error, because "the database was unreachable" and "that quote is not yours"
// must not look the same to the person asking.
func isNotFoundish(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "not found") || strings.Contains(msg, "forbidden") || strings.Contains(msg, "no rows")
}

func containsFold(hay, needle string) bool {
	return strings.Contains(strings.ToLower(hay), strings.ToLower(needle))
}

var _ = time.Time{}
