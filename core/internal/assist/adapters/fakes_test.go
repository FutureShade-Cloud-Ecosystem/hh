package adapters

// fakes_test.go, the doubles every unit test in this package runs against.
//
// THEY RECORD THE SCOPE THEY WERE HANDED, AND THAT IS THE WHOLE POINT. The invariant this feature
// exists to hold is "a command can never show a user something the app itself would not show them",
// and the only way to assert that from OUTSIDE is to look at what the read seam actually received.
// A test that checked the adapter's return value would pass just as happily against an adapter that
// asked the domain for everything and then filtered nothing.
//
// So every fake below stores gotReach, and every scoping test names it. That makes the mutation in
// NOTES-FIX-431B.md (M1: an adapter drops the caller's scope) fail here rather than in production.

import (
	"context"
	"errors"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customer"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/inventory"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
)

var errBoom = errors.New("the database fell over")

// at is a fixed instant, so every formatted answer in these tests is stable.
var at = time.Date(2026, 8, 11, 9, 30, 0, 0, time.UTC)

// -- inventory ----------------------------------------------------------------------------------

type fakeStock struct {
	product inventory.ProductLookup
	rows    []inventory.BranchStock
	found   bool
	err     error

	gotReach inventory.BranchReach
	gotRef   string
	calls    int
}

func (f *fakeStock) LookupStock(_ context.Context, reach inventory.BranchReach, ref string) (inventory.ProductLookup, []inventory.BranchStock, bool, error) {
	f.gotReach, f.gotRef, f.calls = reach, ref, f.calls+1
	return f.product, f.rows, f.found, f.err
}

func seededStock() *fakeStock {
	return &fakeStock{
		product: inventory.ProductLookup{ProductID: "p-1", SKU: "TB-BLU60", Name: "Techo-Bloc Blu 60", UOM: "EA"},
		rows: []inventory.BranchStock{
			{BranchName: "Trenton", OnHandBase: 120, ReservedBase: 20, AvailableBase: 100},
			{BranchName: "Kingston", OnHandBase: 40, ReservedBase: 0, AvailableBase: 40},
		},
		found: true,
	}
}

// -- order --------------------------------------------------------------------------------------

type fakeOrders struct {
	summary order.Summary
	found   bool
	err     error

	gotScope order.ReadScope
	gotNo    string
	calls    int
}

func (f *fakeOrders) SummaryByNumber(_ context.Context, scope order.ReadScope, no string) (order.Summary, bool, error) {
	f.gotScope, f.gotNo, f.calls = scope, no, f.calls+1
	return f.summary, f.found, f.err
}

func seededOrders() *fakeOrders {
	return &fakeOrders{
		summary: order.Summary{
			OrderID: "o-1", OrderNo: "#SO-4", Status: "PICKING", CustomerName: "Northgate Landscaping",
			BranchID: "b-1", BranchName: "Trenton", TotalCents: 512300, PlacedAt: at, LineCount: 3,
		},
		found: true,
	}
}

// -- customer + billing -------------------------------------------------------------------------

type fakeAccounts struct {
	account customer.SearchResult
	found   bool
	err     error

	gotReach customer.AccountReach
	gotRef   string
	calls    int
}

func (f *fakeAccounts) LookupAccount(_ context.Context, reach customer.AccountReach, ref string) (customer.SearchResult, bool, error) {
	f.gotReach, f.gotRef, f.calls = reach, ref, f.calls+1
	return f.account, f.found, f.err
}

type fakeAR struct {
	row      billing.AgingRow
	asOf     time.Time
	found    bool
	activity []billing.ARActivity
	err      error
	actErr   error

	gotCustomerID string
	gotLimit      int
}

func (f *fakeAR) CustomerAging(_ context.Context, customerID string) (billing.AgingRow, time.Time, bool, error) {
	f.gotCustomerID = customerID
	return f.row, f.asOf, f.found, f.err
}

func (f *fakeAR) CustomerActivity(_ context.Context, customerID string, limit int) ([]billing.ARActivity, error) {
	f.gotCustomerID, f.gotLimit = customerID, limit
	return f.activity, f.actErr
}

func seededAccounts() *fakeAccounts {
	return &fakeAccounts{
		account: customer.SearchResult{ID: "c-1", Name: "Northgate Landscaping", AccountNo: "CUST-1001"},
		found:   true,
	}
}

func seededAR() *fakeAR {
	return &fakeAR{
		// 1_30 + 31_60 + 90+ = 40000 overdue, of a 140000 total.
		row: billing.AgingRow{
			CustomerID: "c-1", CustomerName: "Northgate Landscaping",
			CurrentCents: 100000, Days30Cents: 25000, Days60Cents: 10000, Days90PlusCents: 5000,
			TotalCents: 140000,
		},
		asOf:  at,
		found: true,
		activity: []billing.ARActivity{
			{Kind: "PAYMENT", Reference: "PMT-1042", AmountCents: -124000, OccurredAt: at},
			{Kind: "INVOICE", Reference: "INV-2231", AmountCents: 264000, OccurredAt: at.Add(-48 * time.Hour)},
		},
	}
}

// -- payments -----------------------------------------------------------------------------------

type fakePayments struct {
	rows []payments.PaymentMatch
	err  error

	gotReach payments.BranchReach
	gotQuery payments.LookupQuery
	calls    int
}

func (f *fakePayments) FindPayments(_ context.Context, reach payments.BranchReach, q payments.LookupQuery) ([]payments.PaymentMatch, error) {
	f.gotReach, f.gotQuery, f.calls = reach, q, f.calls+1
	return f.rows, f.err
}

func seededPayments() *fakePayments {
	return &fakePayments{
		rows: []payments.PaymentMatch{{
			ID: "pay-1", PaymentNo: "PMT-1042", GatewayRef: "gw_88x",
			CustomerName: "Northgate Landscaping", BranchName: "Trenton",
			Method: "CHEQUE", Status: "RECEIVED", AmountCents: 124000, ReceivedAt: at,
		}},
	}
}
