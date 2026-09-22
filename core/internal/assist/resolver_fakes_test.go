package assist

// resolver_fakes_test.go, DIB-478's doubles. Same rule as fakes_test.go: no database, no network,
// no keys, and every seam RECORDS THE CALLER IT WAS HANDED, because "the assistant runs as the
// asking user" is only assertable from outside by looking at what the read actually received.

import (
	"context"
	"errors"
	"time"
)

// -- the deep read seams ------------------------------------------------------------------------

type fakeDeep struct {
	today    DayTotal
	days     []PaymentDay
	aging    []AgingAccount
	holds    []HoldRecord
	scoped   bool
	invoices []InvoiceRecord
	orders   []OrderLine
	quotes   []QuoteLine
	quote    QuoteLine
	qHolds   []QuoteHoldLine
	qFound   bool
	incoming []IncomingLine
	incProd  ProductRef
	incFound bool
	catalog  []CatalogueHit
	price    PriceQuote
	priceHit bool
	branches []NamedBranch

	err error

	gotCaller Caller
	gotRange  PaymentRangeQuery
	gotTerm   string
	gotProd   string
	calls     int
}

func (f *fakeDeep) PaymentsToday(_ context.Context, c Caller, _ time.Time) (DayTotal, error) {
	f.gotCaller, f.calls = c, f.calls+1
	return f.today, f.err
}

func (f *fakeDeep) PaymentsByDay(_ context.Context, c Caller, q PaymentRangeQuery) ([]PaymentDay, error) {
	f.gotCaller, f.gotRange, f.calls = c, q, f.calls+1
	return f.days, f.err
}

func (f *fakeDeep) Aging(_ context.Context, c Caller, _ time.Time) ([]AgingAccount, error) {
	f.gotCaller, f.calls = c, f.calls+1
	return f.aging, f.err
}

func (f *fakeDeep) Holds(_ context.Context, c Caller) ([]HoldRecord, bool, error) {
	f.gotCaller, f.calls = c, f.calls+1
	return f.holds, f.scoped, f.err
}

func (f *fakeDeep) InvoicesFor(_ context.Context, c Caller, _, _ string, _ int) ([]InvoiceRecord, error) {
	f.gotCaller, f.calls = c, f.calls+1
	return f.invoices, f.err
}

func (f *fakeDeep) OrdersFor(_ context.Context, c Caller, _, _ string, _ int) ([]OrderLine, error) {
	f.gotCaller, f.calls = c, f.calls+1
	return f.orders, f.err
}

func (f *fakeDeep) Quotes(_ context.Context, c Caller, _, _ string, _ int) ([]QuoteLine, error) {
	f.gotCaller, f.calls = c, f.calls+1
	return f.quotes, f.err
}

func (f *fakeDeep) QuoteHolds(_ context.Context, c Caller, _ string) (QuoteLine, []QuoteHoldLine, bool, error) {
	f.gotCaller, f.calls = c, f.calls+1
	return f.quote, f.qHolds, f.qFound, f.err
}

func (f *fakeDeep) Incoming(_ context.Context, c Caller, ref string) (ProductRef, []IncomingLine, bool, error) {
	f.gotCaller, f.gotProd, f.calls = c, ref, f.calls+1
	return f.incProd, f.incoming, f.incFound, f.err
}

// FindProducts FILTERS BY THE TERM, like the real counter search does.
//
// A fake that returned its whole list for every term would make every word in every follow-up look
// like a product, and the resolver tests would then be measuring the fake rather than the resolver -
// which is exactly the trap that made "what about Kingston?" look ambiguous the first time this ran.
func (f *fakeDeep) FindProducts(_ context.Context, c Caller, term string, limit int) ([]CatalogueHit, error) {
	f.gotCaller, f.gotTerm, f.calls = c, term, f.calls+1
	if f.err != nil {
		return nil, f.err
	}
	out := make([]CatalogueHit, 0, len(f.catalog))
	for _, h := range f.catalog {
		if containsFold(h.SKU, term) || containsFold(h.Name, term) {
			out = append(out, h)
		}
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (f *fakeDeep) PriceFor(_ context.Context, c Caller, _, _, _ string) (PriceQuote, bool, error) {
	f.gotCaller, f.calls = c, f.calls+1
	return f.price, f.priceHit, f.err
}

func (f *fakeDeep) Branches(_ context.Context, c Caller) ([]NamedBranch, error) {
	f.gotCaller = c
	return f.branches, f.err
}

// -- a world with something plausible behind every command --------------------------------------

func seededDeep() *fakeDeep {
	at := time.Date(2026, 8, 18, 9, 30, 0, 0, time.UTC)
	return &fakeDeep{
		today: DayTotal{Day: "2026-08-18", Count: 3, Cents: 412500, Channels: 2},
		days: []PaymentDay{
			{Day: "2026-08-10", Count: 2, Cents: 180000},
			{Day: "2026-08-12", Count: 4, Cents: 512300},
		},
		aging: []AgingAccount{
			{CustomerID: "c-1", CustomerName: "Northgate Landscaping", TotalCents: 348800, CurrentCents: 228800, Days30Cents: 120000, OnHold: true, HoldKind: "AGING"},
			{CustomerID: "c-2", CustomerName: "Bay Stone Supply", TotalCents: 96000, CurrentCents: 96000},
		},
		holds: []HoldRecord{
			{CustomerID: "c-1", CustomerName: "Northgate Landscaping", Status: "ACTIVE", Kind: "AGING", AutoPlaced: true, Reason: "balance older than 60 days", PlacedAt: at},
		},
		invoices: []InvoiceRecord{
			{InvoiceID: "i-1", InvoiceNo: "INV-1001", Status: "FINALIZED", TotalCents: 240000, BalanceCents: 120000, IssueDate: at, DueDate: at},
		},
		orders: []OrderLine{
			{OrderID: "8f14e45f-ceea-467a-9b1e-1f0e2d4a77c1", OrderNo: "SO-4", Status: "PICKING", Fulfillment: "DELIVERY", Requested: at, TotalCents: 512300},
		},
		quotes: []QuoteLine{
			{QuoteID: "q-1", QuoteNo: "Q-1042", CustomerName: "Northgate Landscaping", Status: "SENT", ValidUntil: "2026-09-01", TotalCents: 220000, UpdatedAt: at},
		},
		quote:   QuoteLine{QuoteID: "q-1", QuoteNo: "Q-1042", CustomerName: "Northgate Landscaping", Status: "SENT"},
		qHolds:  []QuoteHoldLine{{ProductName: "Techo-Bloc Blu 60", SKU: "TB-BLU60", BranchName: "Kingston", QtyBase: 120, UOM: "ea", Status: "SOFT", ExpiresAt: "2026-08-25"}},
		qFound:  true,
		incProd: ProductRef{ProductID: "p-1", SKU: "TB-BLU60", Name: "Techo-Bloc Blu 60"},
		incoming: []IncomingLine{
			{Source: "TRANSFER", Reference: "TR-77", From: "Trenton", ToBranch: "Kingston", QtyBase: 120, UOM: "ea", ExpectedAt: "2026-08-20", ShippedAt: "2026-08-17", Status: "IN TRANSIT"},
			{Source: "PO", Reference: "PO-KIN-121", From: "Techo-Bloc", ToBranch: "Kingston", QtyBase: 480, UOM: "ea", ExpectedAt: "2026-08-29"},
		},
		incFound: true,
		catalog: []CatalogueHit{
			{ProductID: "p-1", SKU: "TB-BLU60", Name: "Techo-Bloc Blu 60", BaseUOM: "ea", MatchReason: "name prefix"},
		},
		price:    PriceQuote{ProductID: "p-1", SKU: "TB-BLU60", Name: "Techo-Bloc Blu 60", CustomerName: "Northgate Landscaping", BranchName: "Kingston", OriginalCents: 1200, FinalCents: 984, DiscountBps: 1800, PriceUOM: "ea", Source: "CUSTOMER_TIER", Detail: "CONTRACTOR −18%"},
		priceHit: true,
		branches: []NamedBranch{
			{BranchID: "b-kin", Code: "KIN", Name: "Kingston"},
			{BranchID: "b-trt", Code: "TRT", Name: "Trenton"},
		},
	}
}

// deepDeps wires BOTH fakes into one Deps: the original five seams and the eleven DIB-478 ones, so a
// conversation test can move from stock to incoming to price without changing worlds mid-sentence.
func deepDeps(f *fakeReaders, d *fakeDeep) Deps {
	deps := f.deps()
	deps.Rollup = d
	deps.PaymentRange = d
	deps.AR = d
	deps.Invoices = d
	deps.OrderList = d
	deps.Quotes = d
	deps.Incoming = d
	deps.Catalogue = d
	deps.Price = d
	deps.Branches = d
	deps.Now = func() time.Time { return testNow }
	return deps
}

// testNow is the pinned clock: TUESDAY 18 August 2026. Every range assertion in this package is
// written against it, so "last week" has one answer and it is checkable by hand, 10-16 August,
// Monday to Sunday (dates.go).
var testNow = time.Date(2026, 8, 18, 10, 0, 0, 0, time.UTC)

// deepResolver builds the resolver over the fake catalogue and branch list.
func deepResolver(d *fakeDeep) *Resolver {
	return NewResolver(d, d).WithClock(func() time.Time { return testNow })
}

// toolsFor is the caller's own offered set, what the resolver is allowed to resolve onto.
func toolsFor(deps Deps, c Caller) []ToolSpec {
	return toolSpecsFor(NewRegistry(deps, nil).List(context.Background(), c))
}

// turn builds one assistant turn of structured history.
func turn(command string, args Args) Message {
	return Message{Role: RoleAssistant, Text: "…", Ran: []Invocation{{Command: command, Args: args}}}
}

var errBoom = errors.New("boom")
