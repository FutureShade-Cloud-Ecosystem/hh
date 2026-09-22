package assist

// fakes_test.go, the in-memory doubles every unit test in this package runs against.
//
// NO DATABASE, NO NETWORK, NO KEYS. That is not a convenience; it is the same property the feature
// itself claims (the stub provider works with zero external keys), so the test suite is an
// existence proof of the claim rather than a separate scaffold.
//
// THE FAKES RECORD THE CALLER THEY WERE HANDED. That is the whole point of fakeReaders: the
// invariant this package exists to hold is "commands run as the asking user", and the only way to
// assert it from outside is to look at what the read seam actually received. gotCaller is checked
// by name in the RBAC pass-through test, which is mutation target M1.

import (
	"context"
	"errors"
	"testing"
	"time"
)

// -- read seams ---------------------------------------------------------------------------------

type fakeReaders struct {
	payments []PaymentRecord
	order    OrderRecord
	orderHit bool
	product  ProductRef
	stock    []StockRecord
	stockHit bool
	customer CustomerRecord
	custHit  bool
	balance  BalanceRecord
	activity []ActivityRecord

	err error

	// what the seam was handed, for the pass-through assertions
	gotCaller Caller
	gotQuery  PaymentQuery
	gotOrder  string
	gotProd   string
	gotCust   string
	calls     int
}

func (f *fakeReaders) FindPayments(_ context.Context, c Caller, q PaymentQuery) ([]PaymentRecord, error) {
	f.gotCaller, f.gotQuery, f.calls = c, q, f.calls+1
	return f.payments, f.err
}

func (f *fakeReaders) OrderByNumber(_ context.Context, c Caller, no string) (OrderRecord, bool, error) {
	f.gotCaller, f.gotOrder, f.calls = c, no, f.calls+1
	return f.order, f.orderHit, f.err
}

func (f *fakeReaders) StockFor(_ context.Context, c Caller, ref string) (ProductRef, []StockRecord, bool, error) {
	f.gotCaller, f.gotProd, f.calls = c, ref, f.calls+1
	return f.product, f.stock, f.stockHit, f.err
}

func (f *fakeReaders) ResolveCustomer(_ context.Context, c Caller, ref string) (CustomerRecord, bool, error) {
	f.gotCaller, f.gotCust, f.calls = c, ref, f.calls+1
	return f.customer, f.custHit, f.err
}

func (f *fakeReaders) Balance(_ context.Context, c Caller, _ string) (BalanceRecord, error) {
	f.gotCaller = c
	return f.balance, f.err
}

func (f *fakeReaders) RecentActivity(_ context.Context, c Caller, _ string, _ int) ([]ActivityRecord, error) {
	f.gotCaller = c
	return f.activity, f.err
}

// deps wires the fake into all four seams.
func (f *fakeReaders) deps() Deps { return Deps{Payments: f, Orders: f, Stock: f, Customers: f} }

// seededReaders is a fake with a plausible answer for every command, so a happy-path test only has
// to say what it is asserting rather than construct the world each time.
func seededReaders() *fakeReaders {
	at := time.Date(2026, 8, 11, 9, 30, 0, 0, time.UTC)
	return &fakeReaders{
		payments: []PaymentRecord{{
			PaymentNo: "PMT-1042", Reference: "gw_88x", CustomerName: "Northgate Landscaping",
			BranchName: "Kelowna", Method: "CHEQUE", Status: "RECEIVED", AmountCents: 124000, ReceivedAt: at,
		}},
		order:    OrderRecord{OrderID: "8f14e45f-ceea-467a-9b1e-1f0e2d4a77c1", OrderNo: "SO-4", Status: "PICKING", CustomerName: "Northgate Landscaping", BranchName: "Kelowna", TotalCents: 512300, PlacedAt: at, LineCount: 3},
		orderHit: true,
		product:  ProductRef{ProductID: "p-1", SKU: "TB-BLU60", Name: "Techo-Bloc Blu 60"},
		stock: []StockRecord{
			{BranchName: "Kelowna", OnHand: 240, Reserved: 40, Available: 200, UOM: "ea"},
			{BranchName: "Vernon", OnHand: 60, Reserved: 0, Available: 60, UOM: "ea"},
		},
		stockHit: true,
		customer: CustomerRecord{CustomerID: "c-1", Name: "Northgate Landscaping", AccountNo: "AC-1001"},
		custHit:  true,
		balance:  BalanceRecord{OpenCents: 348800, OverdueCents: 120000, AsOf: at},
		activity: []ActivityRecord{
			{Kind: "Order", Reference: "SO-4", When: at, Cents: 512300, Status: "PICKING"},
			{Kind: "Payment", Reference: "PMT-1042", When: at, Cents: 124000, Status: "RECEIVED"},
		},
	}
}

// -- governance stores --------------------------------------------------------------------------

// fakeLedger is an append-only slice. Append-only IN THE FAKE TOO, deliberately: a fake that
// allowed an edit would let a test pass that the real trigger (0154) would refuse.
type fakeLedger struct {
	entries []Entry
	err     error
}

func (f *fakeLedger) Append(_ context.Context, e Entry) error {
	if f.err != nil {
		return f.err
	}
	f.entries = append(f.entries, e)
	return nil
}

func (f *fakeLedger) SpendFor(_ context.Context, userID string, from, to time.Time) (Spend, error) {
	var s Spend
	for _, e := range f.entries {
		if e.UserID != userID || e.At.Before(from) || !e.At.Before(to) {
			continue
		}
		s.Exchanges++
		s.TokensIn += e.TokensIn
		s.TokensOut += e.TokensOut
	}
	return s, nil
}

func (f *fakeLedger) SpendAll(_ context.Context, from, to time.Time) (map[string]Spend, error) {
	out := map[string]Spend{}
	for _, e := range f.entries {
		if e.At.Before(from) || !e.At.Before(to) {
			continue
		}
		s := out[e.UserID]
		s.Exchanges++
		s.TokensIn += e.TokensIn
		s.TokensOut += e.TokensOut
		out[e.UserID] = s
	}
	return out, nil
}

type fakeLimits struct {
	roles map[string]int
	users map[string]int
}

func newFakeLimits() *fakeLimits {
	return &fakeLimits{roles: map[string]int{}, users: map[string]int{}}
}

func (f *fakeLimits) RoleBudget(_ context.Context, role string) (*int, error) {
	if v, ok := f.roles[role]; ok {
		return &v, nil
	}
	return nil, nil
}

func (f *fakeLimits) UserBudget(_ context.Context, user string) (*int, error) {
	if v, ok := f.users[user]; ok {
		return &v, nil
	}
	return nil, nil
}

func (f *fakeLimits) RoleBudgets(context.Context) (map[string]int, error) { return f.roles, nil }
func (f *fakeLimits) UserBudgets(context.Context) (map[string]int, error) { return f.users, nil }

func (f *fakeLimits) SetRoleBudget(_ context.Context, role string, tokens int, _ string) error {
	f.roles[role] = tokens
	return nil
}

func (f *fakeLimits) SetUserBudget(_ context.Context, user string, tokens int, _ string) error {
	f.users[user] = tokens
	return nil
}

func (f *fakeLimits) ClearUserBudget(_ context.Context, user string) error {
	delete(f.users, user)
	return nil
}

type fakeStaff []StaffRow

func (f fakeStaff) Staff(context.Context) ([]StaffRow, error) { return []StaffRow(f), nil }

// -- providers ----------------------------------------------------------------------------------

// scriptedProvider returns canned replies in order and records what it was asked. Used to prove
// things about the SERVICE (the tool loop, the ledger row, the token arithmetic) without depending
// on the stub's parser.
type scriptedProvider struct {
	replies []Reply
	err     error
	seen    []CompletionRequest
	n       int
}

func (p *scriptedProvider) Name() string  { return "scripted" }
func (p *scriptedProvider) Model() string { return "scripted-1" }

func (p *scriptedProvider) Complete(_ context.Context, req CompletionRequest) (Reply, error) {
	p.seen = append(p.seen, req)
	if p.err != nil {
		return Reply{}, p.err
	}
	if p.n < len(p.replies) {
		r := p.replies[p.n]
		p.n++
		return r, nil
	}
	return Reply{Text: "(no more scripted replies)"}, nil
}

// -- callers ------------------------------------------------------------------------------------

func leadership() Caller {
	return Caller{UserID: "u-gm", RoleCode: "GENERAL_MANAGER", Tier: 1, Global: true, IsAdmin: true}
}

func manager() Caller {
	return Caller{UserID: "u-sm", RoleCode: "SALES_MANAGER", Tier: 2, BranchIDs: []string{"b-kel"}}
}

func frontline() Caller {
	return Caller{UserID: "u-is", RoleCode: "INSIDE_SALES", Tier: 3, BranchIDs: []string{"b-kel"}}
}

func unassigned() Caller { return Caller{UserID: "u-new", RoleCode: "", Tier: 0} }

// -- assertions ---------------------------------------------------------------------------------

func mustRun(t *testing.T, r *Registry, c Caller, code string, a Args) Result {
	t.Helper()
	res, err := r.Run(context.Background(), c, code, a)
	if err != nil {
		t.Fatalf("Run(%s) unexpected error: %v", code, err)
	}
	return res
}

func wantErrIs(t *testing.T, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("got error %v, want %v", err, target)
	}
}
