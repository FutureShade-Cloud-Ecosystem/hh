package adapters

// adapters_test.go, each adapter's translation, and each of the five commands end to end THROUGH
// THE REGISTRY with no database.
//
// WHY THE END-TO-END HALF IS HERE AND NOT ONLY IN THE INTEGRATION FILE. Registry.Run is the one
// function that carries this feature's security argument, and it is also the only thing that proves
// an adapter satisfies the seam the COMMANDS actually use rather than the seam I remembered writing.
// Running the real registry over real adapters over fake domain services gives that proof in
// milliseconds, with no Postgres, which means it runs on every `go test ./...` rather than on the
// days somebody sets DATABASE_URL.
//
// The integration file then proves the other half: that the real domain services answer these seams
// correctly against real rows. Neither file can substitute for the other, and both are cheap.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customer"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/inventory"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
)

// -- stock --------------------------------------------------------------------------------------

func TestStock_CarriesTheCallersBranchesIntoTheRead(t *testing.T) {
	f := seededStock()
	r := NewStockReader(f)

	if _, _, _, err := r.StockFor(context.Background(), scopedCaller("b-trenton"), "TB-BLU60"); err != nil {
		t.Fatalf("StockFor: %v", err)
	}
	if f.gotReach.Global {
		t.Fatal("the inventory read was asked GLOBALLY for a branch-scoped caller")
	}
	if len(f.gotReach.BranchIDs) != 1 || f.gotReach.BranchIDs[0] != "b-trenton" {
		t.Fatalf("inventory read received branches %v, want [b-trenton]", f.gotReach.BranchIDs)
	}
	if f.gotRef != "TB-BLU60" {
		t.Fatalf("inventory read received ref %q", f.gotRef)
	}
}

func TestStock_MapsEveryBranchRowWithTheProductsUnit(t *testing.T) {
	f := seededStock()
	p, rows, found, err := NewStockReader(f).StockFor(context.Background(), globalCaller(), "TB-BLU60")
	if err != nil || !found {
		t.Fatalf("StockFor: found=%v err=%v", found, err)
	}
	if p.SKU != "TB-BLU60" || p.Name != "Techo-Bloc Blu 60" || p.ProductID != "p-1" {
		t.Fatalf("product ref = %+v", p)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	if rows[0].BranchName != "Trenton" || rows[0].OnHand != 120 || rows[0].Reserved != 20 || rows[0].Available != 100 {
		t.Fatalf("row 0 = %+v", rows[0])
	}
	for i, row := range rows {
		if row.UOM != "EA" {
			t.Errorf("row %d carries UOM %q, want the PRODUCT's base unit EA, a quantity printed against the wrong unit is a wrong answer", i, row.UOM)
		}
	}
}

// TestStock_AvailableIsCarriedNotRecomputed is the "one owner per arithmetic" pin. The fake returns
// an Available that deliberately DISAGREES with on_hand − reserved; the adapter must report the
// server's number anyway, because inventory.AvailableBase is the one definition of that rule and a
// second copy here would drift the day it changes.
func TestStock_AvailableIsCarriedNotRecomputed(t *testing.T) {
	f := seededStock()
	f.rows = []inventory.BranchStock{{BranchName: "Trenton", OnHandBase: 10, ReservedBase: 3, AvailableBase: 999}}

	_, rows, _, err := NewStockReader(f).StockFor(context.Background(), globalCaller(), "TB-BLU60")
	if err != nil {
		t.Fatalf("StockFor: %v", err)
	}
	if rows[0].Available != 999 {
		t.Fatalf("Available = %v, want the server's 999, the adapter recomputed it", rows[0].Available)
	}
}

// TestStock_TheTwoEmptiesStayDifferent is DIB-443 carried one ticket forward. "I don't know that
// product" and "that product has never moved" are not the same sentence and the command above says
// them differently; an adapter that collapsed them would erase a distinction a tester noticed.
func TestStock_TheTwoEmptiesStayDifferent(t *testing.T) {
	t.Run("no such product", func(t *testing.T) {
		f := &fakeStock{found: false}
		_, rows, found, err := NewStockReader(f).StockFor(context.Background(), globalCaller(), "nope")
		if err != nil {
			t.Fatalf("an unknown product is an ANSWER, not an error: %v", err)
		}
		if found {
			t.Fatal("found=true for a product that did not resolve")
		}
		if len(rows) != 0 {
			t.Fatalf("rows = %d for a product that did not resolve", len(rows))
		}
	})
	t.Run("known product, no stock ever", func(t *testing.T) {
		f := seededStock()
		f.rows = nil
		p, rows, found, err := NewStockReader(f).StockFor(context.Background(), globalCaller(), "TB-BLU60")
		if err != nil {
			t.Fatalf("a product with no stock is an ANSWER, not an error: %v", err)
		}
		if !found {
			t.Fatal("found=false for a product that resolved, the two empties have been collapsed")
		}
		if len(rows) != 0 {
			t.Fatalf("rows = %d, want 0", len(rows))
		}
		if p.SKU != "TB-BLU60" {
			t.Fatal("the product's identity was lost, the answer can no longer name it back")
		}
	})
}

func TestStock_ARealFailureIsStillAnError(t *testing.T) {
	f := &fakeStock{err: errBoom}
	if _, _, _, err := NewStockReader(f).StockFor(context.Background(), globalCaller(), "TB-BLU60"); !errors.Is(err, errBoom) {
		t.Fatalf("a read failure must surface as an error, got %v", err)
	}
}

// -- order --------------------------------------------------------------------------------------

func TestOrder_CarriesTheCallersBranchesIntoTheRead(t *testing.T) {
	f := seededOrders()
	if _, _, err := NewOrderReader(f).OrderByNumber(context.Background(), scopedCaller("b-trenton"), "SO-4"); err != nil {
		t.Fatalf("OrderByNumber: %v", err)
	}
	if f.gotScope.Global {
		t.Fatal("the order read was asked GLOBALLY for a branch-scoped caller, a cross-branch #SO would now resolve")
	}
	if len(f.gotScope.BranchIDs) != 1 || f.gotScope.BranchIDs[0] != "b-trenton" {
		t.Fatalf("order read received branches %v", f.gotScope.BranchIDs)
	}
	if f.gotNo != "SO-4" {
		t.Fatalf("order read received %q, want the number as typed", f.gotNo)
	}
}

func TestOrder_MapsTheHeadlineFacts(t *testing.T) {
	rec, found, err := NewOrderReader(seededOrders()).OrderByNumber(context.Background(), globalCaller(), "SO-4")
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if rec.OrderNo != "#SO-4" || rec.Status != "PICKING" || rec.CustomerName != "Northgate Landscaping" ||
		rec.BranchName != "Trenton" || rec.TotalCents != 512300 || rec.LineCount != 3 || !rec.PlacedAt.Equal(at) {
		t.Fatalf("order record = %+v", rec)
	}
}

func TestOrder_NotFoundIsAnAnswerNotAnError(t *testing.T) {
	f := &fakeOrders{found: false}
	rec, found, err := NewOrderReader(f).OrderByNumber(context.Background(), scopedCaller("b-trenton"), "SO-9999")
	if err != nil {
		t.Fatalf("an unknown order number is an ANSWER, not an error: %v", err)
	}
	if found || rec.OrderNo != "" {
		t.Fatalf("found=%v rec=%+v", found, rec)
	}
}

// -- customer -----------------------------------------------------------------------------------

func TestCustomer_ResolveCarriesTheCallersBranches(t *testing.T) {
	acc, ar := seededAccounts(), seededAR()
	if _, _, err := NewCustomerReader(acc, ar).ResolveCustomer(context.Background(), scopedCaller("b-trenton"), "Northgate"); err != nil {
		t.Fatalf("ResolveCustomer: %v", err)
	}
	if acc.gotReach.Global {
		t.Fatal("the account lookup was asked GLOBALLY for a branch-scoped caller")
	}
	if len(acc.gotReach.BranchIDs) != 1 || acc.gotReach.BranchIDs[0] != "b-trenton" {
		t.Fatalf("account lookup received branches %v", acc.gotReach.BranchIDs)
	}
}

func TestCustomer_BalanceIsBillingsOwnNumbers(t *testing.T) {
	acc, ar := seededAccounts(), seededAR()
	bal, err := NewCustomerReader(acc, ar).Balance(context.Background(), globalCaller(), "c-1")
	if err != nil {
		t.Fatalf("Balance: %v", err)
	}
	if bal.OpenCents != 140000 {
		t.Errorf("open = %d, want billing's TotalCents 140000", bal.OpenCents)
	}
	// 25000 + 10000 + 5000, the three LATE buckets, added, never Total − Current.
	if bal.OverdueCents != 40000 {
		t.Errorf("overdue = %d, want the three late buckets summed (40000)", bal.OverdueCents)
	}
	if !bal.AsOf.Equal(at) {
		t.Errorf("as-of = %v, want BILLING's clock %v, the figure and its date must come from one clock", bal.AsOf, at)
	}
	if ar.gotCustomerID != "c-1" {
		t.Errorf("billing was asked about %q", ar.gotCustomerID)
	}
}

// TestCustomer_NoOpenBalanceIsZeroNotAnError pins the AR worklist's own semantics: agingSelect's
// HAVING drops accounts that net to zero, so an account in perfect standing is ABSENT from it. That
// must read as "nothing outstanding", which is a good answer, and never as a failure or an unknown.
func TestCustomer_NoOpenBalanceIsZeroNotAnError(t *testing.T) {
	ar := seededAR()
	ar.found = false
	bal, err := NewCustomerReader(seededAccounts(), ar).Balance(context.Background(), globalCaller(), "c-1")
	if err != nil {
		t.Fatalf("an account with nothing outstanding is an ANSWER, not an error: %v", err)
	}
	if bal.OpenCents != 0 || bal.OverdueCents != 0 {
		t.Fatalf("balance = %+v, want zeros", bal)
	}
	if !bal.AsOf.Equal(at) {
		t.Fatalf("as-of = %v, even a zero balance is only meaningful with the date it was aged against", bal.AsOf)
	}
}

func TestCustomer_ActivityTranslatesTheLedgersVocabulary(t *testing.T) {
	rows, err := NewCustomerReader(seededAccounts(), seededAR()).
		RecentActivity(context.Background(), globalCaller(), "c-1", 10)
	if err != nil {
		t.Fatalf("RecentActivity: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	if rows[0].Kind != "Payment" || rows[0].Reference != "PMT-1042" || rows[0].Cents != -124000 {
		t.Fatalf("row 0 = %+v", rows[0])
	}
	if rows[1].Kind != "Invoice" {
		t.Fatalf("row 1 kind = %q", rows[1].Kind)
	}
}

// TestCustomer_AnUnknownLedgerTypePassesThroughVerbatim: a txn_type added by a later migration must
// appear as ITSELF, not be swallowed into an "Other" bucket that makes the answer look understood.
func TestCustomer_AnUnknownLedgerTypePassesThroughVerbatim(t *testing.T) {
	ar := seededAR()
	ar.activity = []billing.ARActivity{{Kind: "FINANCE_CHARGE", Reference: "FC-1", OccurredAt: at}}
	rows, err := NewCustomerReader(seededAccounts(), ar).
		RecentActivity(context.Background(), globalCaller(), "c-1", 10)
	if err != nil {
		t.Fatalf("RecentActivity: %v", err)
	}
	if rows[0].Kind != "FINANCE_CHARGE" {
		t.Fatalf("kind = %q, want the ledger's own word passed through", rows[0].Kind)
	}
}

func TestCustomer_EmptyActivityIsAnAnswerNotAnError(t *testing.T) {
	ar := seededAR()
	ar.activity = nil
	rows, err := NewCustomerReader(seededAccounts(), ar).
		RecentActivity(context.Background(), globalCaller(), "c-1", 10)
	if err != nil {
		t.Fatalf("an account with no movements is an ANSWER, not an error: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("rows = %d", len(rows))
	}
}

// -- payments -----------------------------------------------------------------------------------

func TestPayment_CarriesTheCallersBranchesAndTheExactAmount(t *testing.T) {
	f := seededPayments()
	_, err := NewPaymentReader(f).FindPayments(context.Background(), scopedCaller("b-trenton"),
		assist.PaymentQuery{AmountCents: 124000, CustomerRef: "Northgate", Limit: 10})
	if err != nil {
		t.Fatalf("FindPayments: %v", err)
	}
	if f.gotReach.Global {
		t.Fatal("the payments read was asked GLOBALLY for a branch-scoped caller")
	}
	if len(f.gotReach.BranchIDs) != 1 || f.gotReach.BranchIDs[0] != "b-trenton" {
		t.Fatalf("payments read received branches %v", f.gotReach.BranchIDs)
	}
	if f.gotQuery.AmountCents != 124000 {
		t.Fatalf("amount reached the read as %d, want 124000 EXACTLY, no rounding, no tolerance", f.gotQuery.AmountCents)
	}
	if f.gotQuery.CustomerRef != "Northgate" || f.gotQuery.Limit != 10 {
		t.Fatalf("query = %+v", f.gotQuery)
	}
}

func TestPayment_MapsTheGatewayReferenceIntoTheAnswer(t *testing.T) {
	rows, err := NewPaymentReader(seededPayments()).
		FindPayments(context.Background(), globalCaller(), assist.PaymentQuery{Reference: "PMT-1042", Limit: 10})
	if err != nil {
		t.Fatalf("FindPayments: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d", len(rows))
	}
	got := rows[0]
	if got.PaymentNo != "PMT-1042" || got.Reference != "gw_88x" || got.CustomerName != "Northgate Landscaping" ||
		got.BranchName != "Trenton" || got.Method != "CHEQUE" || got.Status != "RECEIVED" ||
		got.AmountCents != 124000 || !got.ReceivedAt.Equal(at) {
		t.Fatalf("payment record = %+v", got)
	}
}

func TestPayment_NoMatchesIsAnAnswerNotAnError(t *testing.T) {
	f := &fakePayments{}
	rows, err := NewPaymentReader(f).
		FindPayments(context.Background(), globalCaller(), assist.PaymentQuery{Reference: "PMT-9", Limit: 10})
	if err != nil {
		t.Fatalf("no matches is an ANSWER, not an error: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("rows = %d", len(rows))
	}
}

// -- an unwired seam stays honest ---------------------------------------------------------------

// TestNilServiceProducesANilReaderNotAPanic pins the rollout behaviour PARK 3 promised: an adapter
// whose service is absent must leave the Deps member NIL, so that command answers a 503 and every
// other command keeps working. A constructor that returned a non-nil reader over a nil service would
// turn a partial wiring into a nil-pointer panic inside the assistant.
func TestNilServiceProducesANilReaderNotAPanic(t *testing.T) {
	if r := NewStockReader(nil); r != nil {
		t.Error("NewStockReader(nil) returned a reader")
	}
	if r := NewOrderReader(nil); r != nil {
		t.Error("NewOrderReader(nil) returned a reader")
	}
	if r := NewPaymentReader(nil); r != nil {
		t.Error("NewPaymentReader(nil) returned a reader")
	}
	if r := NewCustomerReader(nil, seededAR()); r != nil {
		t.Error("NewCustomerReader with no account lookup returned a reader, resolve-less balance would answer every account")
	}
	if r := NewCustomerReader(seededAccounts(), nil); r != nil {
		t.Error("NewCustomerReader with no AR reader returned a reader, a half-wired money seam reports zeros as facts")
	}
}

// -- the five commands, end to end through the gate ---------------------------------------------

// wired builds a registry over the REAL adapters and the fake domain services, with no policy
// narrowing, so each test below exercises Registry.Run, the command, the adapter and the seam in
// one call, exactly as an HTTP request or a provider tool call would.
func wired(stock *fakeStock, orders *fakeOrders, acc *fakeAccounts, ar *fakeAR, pay *fakePayments) *assist.Registry {
	return assist.NewRegistry(assist.Deps{
		Payments:  NewPaymentReader(pay),
		Orders:    NewOrderReader(orders),
		Stock:     NewStockReader(stock),
		Customers: NewCustomerReader(acc, ar),
	}, nil)
}

func manager() assist.Caller {
	// PaymentTrail: a branch manager holds the trail reads through the counter till (ruling 87's
	// matrix), the stamp the handler's trail reader would make for this role.
	return assist.Caller{UserID: "u-3", RoleCode: "BRANCH_MANAGER", Tier: 2, BranchIDs: []string{"b-trenton"}, PaymentTrail: true}
}

func TestCommands_AllFiveAnswerThroughTheRegistry(t *testing.T) {
	stock, orders, acc, ar, pay := seededStock(), seededOrders(), seededAccounts(), seededAR(), seededPayments()
	r := wired(stock, orders, acc, ar, pay)
	ctx := context.Background()
	c := manager() // tier 2, may run all five

	cases := []struct {
		code string
		args assist.Args
		want string // a fragment the headline must contain
	}{
		{"stock.lookup", assist.Args{"product": "TB-BLU60"}, "Techo-Bloc Blu 60 (TB-BLU60)"},
		{"order.lookup", assist.Args{"order_no": "SO-4"}, "#SO-4 is picking"},
		{"customer.balance", assist.Args{"customer": "Northgate"}, "$1,400.00"},
		{"customer.activity", assist.Args{"customer": "Northgate"}, "Northgate Landscaping"},
		{"payment.lookup", assist.Args{"reference": "PMT-1042"}, "reference PMT-1042"},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			res, err := r.Run(ctx, c, tc.code, tc.args)
			if err != nil {
				t.Fatalf("Run(%s): %v", tc.code, err)
			}
			if res.Empty {
				t.Fatalf("Run(%s) came back EMPTY with a seeded reader: %q", tc.code, res.Headline)
			}
			if !strings.Contains(res.Headline, tc.want) {
				t.Fatalf("Run(%s) headline = %q, want it to contain %q", tc.code, res.Headline, tc.want)
			}
			if len(res.Items) == 0 {
				t.Fatalf("Run(%s) returned no items", tc.code)
			}
		})
	}
}

// TestCommands_RunAsTheAskingUserThroughTheAdapters is M1's dying test, asserted at the READ SEAM
// rather than at the result: it is what the fake was HANDED that proves the caller travelled, and a
// test that only checked the returned rows would survive an adapter that asked for everything.
func TestCommands_RunAsTheAskingUserThroughTheAdapters(t *testing.T) {
	stock, orders, acc, ar, pay := seededStock(), seededOrders(), seededAccounts(), seededAR(), seededPayments()
	r := wired(stock, orders, acc, ar, pay)
	ctx := context.Background()
	c := manager() // scoped to b-trenton only

	if _, err := r.Run(ctx, c, "stock.lookup", assist.Args{"product": "TB-BLU60"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Run(ctx, c, "order.lookup", assist.Args{"order_no": "SO-4"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Run(ctx, c, "customer.balance", assist.Args{"customer": "Northgate"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Run(ctx, c, "payment.lookup", assist.Args{"reference": "PMT-1042"}); err != nil {
		t.Fatal(err)
	}

	if stock.gotReach.Global || len(stock.gotReach.BranchIDs) != 1 {
		t.Errorf("stock seam was handed %+v, want the caller's single branch", stock.gotReach)
	}
	if orders.gotScope.Global || len(orders.gotScope.BranchIDs) != 1 {
		t.Errorf("order seam was handed %+v, want the caller's single branch", orders.gotScope)
	}
	if acc.gotReach.Global || len(acc.gotReach.BranchIDs) != 1 {
		t.Errorf("customer seam was handed %+v, want the caller's single branch", acc.gotReach)
	}
	if pay.gotReach.Global || len(pay.gotReach.BranchIDs) != 1 {
		t.Errorf("payments seam was handed %+v, want the caller's single branch", pay.gotReach)
	}
}

// TestCommands_TheGateStillRefusesMoneyToFrontline: the adapters must not become a side door around
// the tier floors. A frontline caller may look up stock and orders and activity; the two money
// commands are refused BEFORE any adapter is reached, which is asserted by the seams' call counts.
func TestCommands_TheGateStillRefusesMoneyToFrontline(t *testing.T) {
	stock, orders, acc, ar, pay := seededStock(), seededOrders(), seededAccounts(), seededAR(), seededPayments()
	r := wired(stock, orders, acc, ar, pay)
	ctx := context.Background()
	yard := assist.Caller{UserID: "u-9", RoleCode: "YARD_TEAM", Tier: 3, BranchIDs: []string{"b-trenton"}}

	if _, err := r.Run(ctx, yard, "payment.lookup", assist.Args{"reference": "PMT-1042"}); !errors.Is(err, assist.ErrRefused) {
		t.Fatalf("payment.lookup for a yard hand = %v, want ErrRefused", err)
	}
	if pay.calls != 0 {
		t.Fatalf("the payments read was called %d times for a refused caller, the gate is being run AFTER the read", pay.calls)
	}
	if _, err := r.Run(ctx, yard, "customer.balance", assist.Args{"customer": "Northgate"}); !errors.Is(err, assist.ErrRefused) {
		t.Fatalf("customer.balance for a yard hand = %v, want ErrRefused", err)
	}
	if acc.calls != 0 {
		t.Fatalf("the account lookup was called %d times for a refused caller", acc.calls)
	}
	// ... and the tier-3 commands still work for the same person.
	if _, err := r.Run(ctx, yard, "stock.lookup", assist.Args{"product": "TB-BLU60"}); err != nil {
		t.Fatalf("stock.lookup for a yard hand: %v", err)
	}
	if _, err := r.Run(ctx, yard, "order.lookup", assist.Args{"order_no": "SO-4"}); err != nil {
		t.Fatalf("order.lookup for a yard hand: %v", err)
	}
}

// TestCommands_EmptyIsSpokenNotFailed walks every command's empty state through the registry, since
// that is the shape a user actually meets and the one DIB-443 was filed about.
func TestCommands_EmptyIsSpokenNotFailed(t *testing.T) {
	stock := &fakeStock{found: false}
	orders := &fakeOrders{found: false}
	acc := &fakeAccounts{found: false}
	ar := &fakeAR{asOf: at}
	pay := &fakePayments{}
	r := wired(stock, orders, acc, ar, pay)
	ctx := context.Background()
	c := manager()

	cases := []struct {
		code string
		args assist.Args
		want string
	}{
		{"stock.lookup", assist.Args{"product": "nope"}, `I can't find a product matching "nope".`},
		{"order.lookup", assist.Args{"order_no": "SO-9999"}, "I can't find an order numbered SO-9999."},
		{"customer.balance", assist.Args{"customer": "nobody"}, `I can't find a customer matching "nobody".`},
		{"customer.activity", assist.Args{"customer": "nobody"}, `I can't find a customer matching "nobody".`},
		{"payment.lookup", assist.Args{"reference": "PMT-9"}, "No payment matches reference PMT-9."},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			res, err := r.Run(ctx, c, tc.code, tc.args)
			if err != nil {
				t.Fatalf("an empty result must not be an error: %v", err)
			}
			if !res.Empty {
				t.Fatalf("Result.Empty is false for an empty answer: %q", res.Headline)
			}
			if res.Headline != tc.want {
				t.Fatalf("headline = %q, want %q", res.Headline, tc.want)
			}
			if res.Note == "" {
				t.Error("an empty answer with no note tells the user nothing about what would change it")
			}
		})
	}
}

// TestCommands_AKnownProductWithNoStockSaysSo is the second empty, the one that is NOT "no such
// thing", proved through the registry so the sentence a user reads is the thing under test.
func TestCommands_AKnownProductWithNoStockSaysSo(t *testing.T) {
	stock := seededStock()
	stock.rows = nil
	r := wired(stock, seededOrders(), seededAccounts(), seededAR(), seededPayments())

	res, err := r.Run(context.Background(), manager(), "stock.lookup", assist.Args{"product": "TB-BLU60"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Empty {
		t.Fatal("a product with no stock should report Empty")
	}
	if res.Headline != "Techo-Bloc Blu 60 (TB-BLU60) has no stock on record yet." {
		t.Fatalf("headline = %q, the 'never moved' state must not read like 'no such product'", res.Headline)
	}
}

// TestCommands_AnUnwiredSeamIs503AndTheRestKeepWorking pins PARK 3's incremental-rollout promise.
func TestCommands_AnUnwiredSeamIs503AndTheRestKeepWorking(t *testing.T) {
	r := assist.NewRegistry(assist.Deps{
		Stock: NewStockReader(seededStock()),
		// the other three left unwired, exactly as a partial paste of the main.go block would leave them
	}, nil)
	ctx := context.Background()
	c := manager()

	var unavailable assist.UnavailableError
	if _, err := r.Run(ctx, c, "payment.lookup", assist.Args{"reference": "PMT-1"}); !errors.As(err, &unavailable) {
		t.Fatalf("an unwired payments seam = %v, want UnavailableError (503)", err)
	}
	if _, err := r.Run(ctx, c, "stock.lookup", assist.Args{"product": "TB-BLU60"}); err != nil {
		t.Fatalf("the WIRED command must keep working: %v", err)
	}
}

// -- the seams the shipped services satisfy -----------------------------------------------------

// TestTheShippedServicesSatisfyTheSeams restates the compile-time assertions in adapters.go as a
// runnable test, so the PARK 3 wiring block in NOTES-FIX-431B.md has a named test to point at
// rather than only a var block a reader has to go and find.
func TestTheShippedServicesSatisfyTheSeams(t *testing.T) {
	var (
		_ StockLookup        = (*inventory.Service)(nil)
		_ OrderSummaryReader = (*order.Service)(nil)
		_ AccountLookup      = (*customer.Service)(nil)
		_ ARReader           = (*billing.CreditService)(nil)
		_ PaymentFinder      = (*payments.Service)(nil)
	)
	// A compile-time proof has no runtime body; the assertion above IS the test.
	_ = time.Now
}

// Ruling 87: the account's activity asks the ledger for payment rows only for a caller the handler
// stamped as holding the payment trail reads.
func TestCustomer_ActivityAsksForPaymentsOnlyWhenTheCallerHoldsTheTrail(t *testing.T) {
	ar := seededAR()
	reader := NewCustomerReader(seededAccounts(), ar)
	c := globalCaller()
	if _, err := reader.RecentActivity(context.Background(), c, "c-1", 10); err != nil || ar.gotWithPayments {
		t.Fatalf("a caller without the trail asked for payments: err %v, asked %v", err, ar.gotWithPayments)
	}
	c.PaymentTrail = true
	if _, err := reader.RecentActivity(context.Background(), c, "c-1", 10); err != nil || !ar.gotWithPayments {
		t.Fatalf("a caller with the trail was refused payments: err %v, asked %v", err, ar.gotWithPayments)
	}
}
