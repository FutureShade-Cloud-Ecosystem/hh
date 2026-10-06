//go:build integration

// integration_test.go, the four adapters against REAL domain services on a REAL migrated Postgres,
// every one of them driven through assist.Registry.Run as a seeded staff user.
//
//	DATABASE_URL=... go test -tags integration -count=1 ./internal/assist/adapters/
//
// WHAT ONLY A DATABASE CAN PROVE, AND IS THEREFORE PROVED HERE RATHER THAN AGAINST A FAKE
// ---------------------------------------------------------------------------------------
// The unit tests prove the adapters carry the caller's scope into the read. They cannot prove the
// read HONOURS it, because a fake honours whatever I wrote it to honour. The fence is four SQL
// predicates, and a SQL predicate is only ever really tested by a row it must not return.
//
// So the load-bearing test in this file is TestIntegration_AScopedUserCannotReachAnotherBranch: one
// Trenton-scoped user, four commands, four pieces of Kingston data sitting in the same tables, and
// every answer must come back empty. Under any of the mutations in NOTES-FIX-431B.md it comes back
// full, and the assistant becomes a friendlier version of the thing branch scoping exists to
// prevent.
//
// It also proves the things a fake cannot say anything about at all:
//
//   - the DIB-443 distinction survives contact with SQL: a product with no stock_balances rows
//     answers "no stock on record yet" and NOT "no such product", against a real empty table.
//   - the balance the assistant quotes is byte-identical to the number billing's own AR console
//     computes, because it IS that number, asserted by running both and comparing.
//   - the exact-amount match is exact against real cents: a payment one cent away does not match.
//   - the visible order number resolves through the real CITEXT column and the real '#' handling.
//
// FIXTURES CLEAN UP AFTER THEMSELVES. Everything is created under a per-run prefix and removed in
// reverse dependency order in t.Cleanup, so this file can run against a database that also has the
// demo seed in it (and does) without either polluting the other.

package adapters

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customer"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/inventory"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
	"github.com/jackc/pgx/v5/pgxpool"
)

// -- the harness --------------------------------------------------------------------------------

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set, skipping adapter integration tests")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatalf("ping: %v", err)
	}
	return pool
}

// world is one run's fixtures plus the registry that reads them.
type world struct {
	pool *pgxpool.Pool
	reg  *assist.Registry

	trentonID, kingstonID string

	// products
	stockedSKU  string // stock at BOTH branches
	kingstonSKU string // stock at KINGSTON only, the cross-branch probe
	barrenSKU   string // a real product with NO stock_balances row anywhere

	// customers
	trentonCustID, trentonCustNo, trentonCustName    string
	kingstonCustID, kingstonCustNo, kingstonCustName string

	// orders
	trentonOrderNo, kingstonOrderNo string
	// kingstonOrderID is the raw uuid. ResolveOrderID passes a UUID through UNRESOLVED by design,
	// so this is the probe for the post-load branch re-check in order/summary.go.
	kingstonOrderID string

	// payments
	trentonPayNo, kingstonPayNo string
	// nearPayNo is one cent away from trentonPayNo, same branch, same customer, the exact-match
	// probe. A tolerant matcher returns it for a $1,240.00 search; an exact one never does.
	nearPayNo       string
	trentonPayRef   string
	trentonPayCents int64
}

// newWorld seeds a self-contained slice of the ERP and wires the SHIPPED services over it, the
// same constructors main.go calls, not test-only variants. That is what makes this a test of the
// wiring and not only of the SQL.
func newWorld(t *testing.T) *world {
	t.Helper()
	pool := testPool(t)
	ctx := context.Background()
	tag := strings.ToUpper(fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000_000))

	w := &world{pool: pool}
	mustQuery(t, pool, `SELECT id::text FROM locations WHERE code = 'TRT' AND loc_type = 'BRANCH'`, &w.trentonID)
	mustQuery(t, pool, `SELECT id::text FROM locations WHERE code = 'KGN' AND loc_type = 'BRANCH'`, &w.kingstonID)

	// -- products --
	w.stockedSKU = "DIB431B-STOCKED-" + tag
	w.kingstonSKU = "DIB431B-KGNONLY-" + tag
	w.barrenSKU = "DIB431B-BARREN-" + tag
	stockedID := insertProduct(t, pool, w.stockedSKU, "DIB-431B Stocked Slab "+tag)
	kingstonOnlyID := insertProduct(t, pool, w.kingstonSKU, "DIB-431B Kingston-Only Slab "+tag)
	barrenID := insertProduct(t, pool, w.barrenSKU, "DIB-431B Never Received "+tag)
	_ = barrenID // deliberately given no stock_balances row at all

	insertBalance(t, pool, stockedID, w.trentonID, 120, 20)
	insertBalance(t, pool, stockedID, w.kingstonID, 40, 0)
	insertBalance(t, pool, kingstonOnlyID, w.kingstonID, 77, 7)

	// -- customers --
	w.trentonCustNo, w.trentonCustName = "DIB431B-T-"+tag, "DIB-431B Trenton Contractor "+tag
	w.kingstonCustNo, w.kingstonCustName = "DIB431B-K-"+tag, "DIB-431B Kingston Contractor "+tag
	w.trentonCustID = insertCustomer(t, pool, w.trentonCustNo, w.trentonCustName, w.trentonID)
	w.kingstonCustID = insertCustomer(t, pool, w.kingstonCustNo, w.kingstonCustName, w.kingstonID)

	// -- orders (the visible number carries the '#', as the mint writes it) --
	w.trentonOrderNo = "#SO-DIB431B-T-" + tag
	w.kingstonOrderNo = "#SO-DIB431B-K-" + tag
	trentonOrderID := insertOrder(t, pool, w.trentonOrderNo, w.trentonCustID, w.trentonID, "PICKING", 512300)
	w.kingstonOrderID = insertOrder(t, pool, w.kingstonOrderNo, w.kingstonCustID, w.kingstonID, "CONFIRMED", 99900)
	insertOrderLine(t, pool, trentonOrderID, stockedID, 1)
	insertOrderLine(t, pool, trentonOrderID, kingstonOnlyID, 2)

	// -- AR: one overdue invoice and one current one, so the buckets are not all in one place --
	overdue := insertInvoice(t, pool, "INV-DIB431B-A-"+tag, w.trentonCustID, w.trentonID, 100000, 100000, -45)
	current := insertInvoice(t, pool, "INV-DIB431B-B-"+tag, w.trentonCustID, w.trentonID, 40000, 40000, 15)
	insertInvoice(t, pool, "INV-DIB431B-K-"+tag, w.kingstonCustID, w.kingstonID, 250000, 250000, -90)

	// -- payments --
	w.trentonPayNo, w.trentonPayRef, w.trentonPayCents = "PMT-DIB431B-T-"+tag, "gwref-dib431b-"+tag, 124000
	w.kingstonPayNo = "PMT-DIB431B-K-" + tag
	trentonPayID := insertPayment(t, pool, w.trentonPayNo, w.trentonCustID, w.trentonID, w.trentonPayCents, w.trentonPayRef)
	insertPayment(t, pool, w.kingstonPayNo, w.kingstonCustID, w.kingstonID, 555500, "")
	// One cent away, at the same branch, same customer, the exact-match probe.
	w.nearPayNo = "PMT-DIB431B-NEAR-" + tag
	insertPayment(t, pool, w.nearPayNo, w.trentonCustID, w.trentonID, w.trentonPayCents+1, "")

	// -- the AR ledger the activity read serves --
	insertTxn(t, pool, w.trentonCustID, "INVOICE", 100000, 100000, "invoice", overdue, w.trentonID, -45)
	insertTxn(t, pool, w.trentonCustID, "INVOICE", 40000, 140000, "invoice", current, w.trentonID, -2)
	insertTxn(t, pool, w.trentonCustID, "PAYMENT", -124000, 16000, "payment", trentonPayID, w.trentonID, -1)

	// -- the shipped services, wired exactly as main.go wires them --
	inventorySvc := inventory.NewService(inventory.NewRepository(pool))
	orderSvc := order.NewService(order.NewRepository(pool))
	customerRepo := customer.NewRepository(pool)
	customerSvc := customer.NewService(customerRepo).WithWriter(customerRepo)
	creditSvc := billing.NewCreditService(billing.NewRepository(pool))
	paymentSvc := payments.NewService(payments.NewRepository(pool), payments.NewStubGateway())

	w.reg = assist.NewRegistry(assist.Deps{
		Payments:  NewPaymentReader(paymentSvc),
		Orders:    NewOrderReader(orderSvc),
		Stock:     NewStockReader(inventorySvc),
		Customers: NewCustomerReader(customerSvc, creditSvc),
	}, assist.NewRepository(pool))

	t.Cleanup(func() { w.teardown(ctx) })
	return w
}

// teardown removes everything this run created, in reverse dependency order. Keyed on the fixture
// prefix rather than on remembered ids, so a partially-failed setup still cleans up.
func (w *world) teardown(ctx context.Context) {
	exec := func(sql string) { _, _ = w.pool.Exec(ctx, sql) }
	exec(`DELETE FROM customer_transactions WHERE customer_id IN (SELECT id FROM customers WHERE account_no::text LIKE 'DIB431B-%')`)
	exec(`DELETE FROM payment_allocations WHERE payment_id IN (SELECT id FROM payments WHERE payment_no::text LIKE 'PMT-DIB431B-%')`)
	exec(`DELETE FROM payments WHERE payment_no::text LIKE 'PMT-DIB431B-%'`)
	exec(`DELETE FROM invoice_lines WHERE invoice_id IN (SELECT id FROM invoices WHERE invoice_no::text LIKE 'INV-DIB431B-%')`)
	exec(`DELETE FROM invoices WHERE invoice_no::text LIKE 'INV-DIB431B-%'`)
	exec(`DELETE FROM order_lines WHERE order_id IN (SELECT id FROM orders WHERE order_no::text LIKE '#SO-DIB431B-%')`)
	exec(`DELETE FROM orders WHERE order_no::text LIKE '#SO-DIB431B-%'`)
	exec(`DELETE FROM customers WHERE account_no::text LIKE 'DIB431B-%'`)
	exec(`DELETE FROM stock_balances WHERE product_id IN (SELECT id FROM products WHERE sku::text LIKE 'DIB431B-%')`)
	exec(`DELETE FROM products WHERE sku::text LIKE 'DIB431B-%'`)
}

// -- callers ------------------------------------------------------------------------------------
//
// Tier 2, because the two money commands are tier 2 and this file is about the BRANCH fence, not
// about the tier gate (which registry_test.go owns and mutation M1 in NOTES-FIX-431.md attacked).
// A tier-3 caller would have the money commands refused before an adapter was reached, which would
// make the cross-branch test pass for the wrong reason, the most dangerous kind of green.

func (w *world) trenton() assist.Caller {
	return assist.Caller{UserID: "u-trt", RoleCode: "BRANCH_MANAGER", Tier: 2, BranchIDs: []string{w.trentonID}}
}

func (w *world) kingston() assist.Caller {
	return assist.Caller{UserID: "u-kgn", RoleCode: "BRANCH_MANAGER", Tier: 2, BranchIDs: []string{w.kingstonID}}
}

func (w *world) boss() assist.Caller {
	return assist.Caller{UserID: "u-gm", RoleCode: "GENERAL_MANAGER", Tier: 1, Global: true, IsAdmin: true}
}

func (w *world) run(t *testing.T, c assist.Caller, code string, args assist.Args) assist.Result {
	t.Helper()
	res, err := w.reg.Run(context.Background(), c, code, args)
	if err != nil {
		t.Fatalf("Run(%s, %v): %v", code, args, err)
	}
	return res
}

// ----------------------------------------------------------------------------------------------
// THE TEST THIS FILE EXISTS FOR
// ----------------------------------------------------------------------------------------------

// TestIntegration_AScopedUserCannotReachAnotherBranch, one Trenton user, four commands, four
// pieces of Kingston data in the same tables. Every answer must be empty.
//
// Each sub-case also asserts the POSITIVE control immediately afterwards: the same command, the same
// user, their OWN branch's data, answering fully. Without that, a fence that refused everything
// would pass this test while breaking the feature.
func TestIntegration_AScopedUserCannotReachAnotherBranch(t *testing.T) {
	w := newWorld(t)
	trt := w.trenton()

	t.Run("order.lookup, another branch's order number", func(t *testing.T) {
		res := w.run(t, trt, "order.lookup", assist.Args{"order_no": w.kingstonOrderNo})
		if !res.Empty {
			t.Fatalf("a Trenton manager read a Kingston order through the assistant: %q %+v", res.Headline, res.Items)
		}
		own := w.run(t, trt, "order.lookup", assist.Args{"order_no": w.trentonOrderNo})
		if own.Empty {
			t.Fatalf("...and cannot read their OWN branch's order either: %q", own.Headline)
		}
	})

	// The UUID arm. ResolveOrderID short-circuits a UUID without a round trip, a cross-branch deep
	// link is meant to 403 downstream, so the branch-scoped RESOLVE does nothing here and the
	// post-load check in order/summary.go is the only thing between a pasted id and Kingston's order.
	t.Run("order.lookup, another branch's order by its raw id", func(t *testing.T) {
		res := w.run(t, trt, "order.lookup", assist.Args{"order_no": w.kingstonOrderID})
		if !res.Empty {
			t.Fatalf("a Trenton manager read a Kingston order by pasting its id: %q %+v", res.Headline, res.Items)
		}
	})

	t.Run("stock.lookup, a product stocked only at another branch", func(t *testing.T) {
		res := w.run(t, trt, "stock.lookup", assist.Args{"product": w.kingstonSKU})
		if !res.Empty {
			t.Fatalf("a Trenton manager read Kingston's yard through the assistant: %q %+v", res.Headline, res.Items)
		}
		// The PRODUCT still resolves, the catalogue is not branch-scoped, and telling a user their
		// SKU is wrong when it is not would be a different bug. What must not appear is a quantity.
		if !strings.Contains(res.Headline, "no stock on record") {
			t.Fatalf("headline = %q, want the 'no stock on record' wording (the product exists; the yard is not theirs)", res.Headline)
		}
		own := w.run(t, trt, "stock.lookup", assist.Args{"product": w.stockedSKU})
		if own.Empty || len(own.Items) != 1 {
			t.Fatalf("Trenton's own stock: empty=%v items=%d, a scoped caller must see exactly their own branches", own.Empty, len(own.Items))
		}
		// The branch's display name is whatever the migrations plant (migration 0003 writes the dealer
		// name, then the city); the fence only needs the row to be Trenton's and never Kingston's.
		if !strings.HasSuffix(own.Items[0].Title, "Trenton") || strings.Contains(own.Items[0].Title, "Kingston") {
			t.Fatalf("branch row = %q, want only Trenton's", own.Items[0].Title)
		}
	})

	t.Run("customer.balance, an account homed at another branch", func(t *testing.T) {
		res := w.run(t, trt, "customer.balance", assist.Args{"customer": w.kingstonCustNo})
		if !res.Empty {
			t.Fatalf("a Trenton manager read a Kingston account's balance: %q %+v", res.Headline, res.Items)
		}
		if !strings.Contains(res.Headline, "can't find a customer") {
			t.Fatalf("headline = %q, a refused account must read as not-found, never as 'exists but not yours'", res.Headline)
		}
		own := w.run(t, trt, "customer.balance", assist.Args{"customer": w.trentonCustNo})
		if own.Empty {
			t.Fatalf("...and cannot read their own account either: %q", own.Headline)
		}
	})

	t.Run("payment.lookup, another branch's payment", func(t *testing.T) {
		res := w.run(t, trt, "payment.lookup", assist.Args{"reference": w.kingstonPayNo})
		if !res.Empty {
			t.Fatalf("a Trenton manager read a Kingston payment: %q %+v", res.Headline, res.Items)
		}
		own := w.run(t, trt, "payment.lookup", assist.Args{"reference": w.trentonPayNo})
		if own.Empty {
			t.Fatalf("...and cannot read their own payment either: %q", own.Headline)
		}
	})

	// The mirror image, so the test cannot be passing because Kingston's rows are simply invisible
	// to everyone: the Kingston manager reads exactly the rows the Trenton manager could not.
	t.Run("the Kingston manager sees what Trenton could not", func(t *testing.T) {
		kgn := w.kingston()
		if res := w.run(t, kgn, "order.lookup", assist.Args{"order_no": w.kingstonOrderNo}); res.Empty {
			t.Fatalf("the Kingston order is invisible to Kingston too: %q", res.Headline)
		}
		if res := w.run(t, kgn, "payment.lookup", assist.Args{"reference": w.kingstonPayNo}); res.Empty {
			t.Fatalf("the Kingston payment is invisible to Kingston too: %q", res.Headline)
		}
		res := w.run(t, kgn, "stock.lookup", assist.Args{"product": w.kingstonSKU})
		if res.Empty || len(res.Items) != 1 {
			t.Fatalf("Kingston's own stock: empty=%v items=%d", res.Empty, len(res.Items))
		}
	})
}

// TestIntegration_AGlobalCallerReachesEveryBranch is the other side of the same fence: the rule is
// "only your branches", not "only one branch", and a leadership caller must still see the company.
func TestIntegration_AGlobalCallerReachesEveryBranch(t *testing.T) {
	w := newWorld(t)
	gm := w.boss()

	res := w.run(t, gm, "stock.lookup", assist.Args{"product": w.stockedSKU})
	if len(res.Items) != 2 {
		t.Fatalf("a global caller saw %d branch rows, want both", len(res.Items))
	}
	for _, code := range []string{w.trentonOrderNo, w.kingstonOrderNo} {
		if r := w.run(t, gm, "order.lookup", assist.Args{"order_no": code}); r.Empty {
			t.Fatalf("a global caller could not read %s", code)
		}
	}
	for _, no := range []string{w.trentonPayNo, w.kingstonPayNo} {
		if r := w.run(t, gm, "payment.lookup", assist.Args{"reference": no}); r.Empty {
			t.Fatalf("a global caller could not read %s", no)
		}
	}
	for _, acct := range []string{w.trentonCustNo, w.kingstonCustNo} {
		if r := w.run(t, gm, "customer.balance", assist.Args{"customer": acct}); r.Empty {
			t.Fatalf("a global caller could not read %s", acct)
		}
	}
}

// TestIntegration_ACallerWithNoBranchGrantsReadsNothing is the inversion, against real rows. A
// scoped account with no grants is the state a new user sits in between being created and being
// assigned a branch; a fence that renders that as "no filter" hands them the company.
func TestIntegration_ACallerWithNoBranchGrantsReadsNothing(t *testing.T) {
	w := newWorld(t)
	nobody := assist.Caller{UserID: "u-new", RoleCode: "BRANCH_MANAGER", Tier: 2} // no BranchIDs at all

	if res := w.run(t, nobody, "order.lookup", assist.Args{"order_no": w.trentonOrderNo}); !res.Empty {
		t.Fatalf("a caller with no branch grants read an order: %q", res.Headline)
	}
	if res := w.run(t, nobody, "payment.lookup", assist.Args{"reference": w.trentonPayNo}); !res.Empty {
		t.Fatalf("a caller with no branch grants read a payment: %q", res.Headline)
	}
	res := w.run(t, nobody, "stock.lookup", assist.Args{"product": w.stockedSKU})
	if !res.Empty || len(res.Items) != 0 {
		t.Fatalf("a caller with no branch grants read stock: %q %+v", res.Headline, res.Items)
	}
}

// ----------------------------------------------------------------------------------------------
// EACH COMMAND, ANSWERING WITH REAL DATA
// ----------------------------------------------------------------------------------------------

func TestIntegration_StockLookupReadsRealBalances(t *testing.T) {
	w := newWorld(t)

	res := w.run(t, w.boss(), "stock.lookup", assist.Args{"product": w.stockedSKU})
	if res.Empty {
		t.Fatalf("headline = %q", res.Headline)
	}
	// 120 − 20 at Trenton, 40 − 0 at Kingston → 140 available across two branches.
	if !strings.Contains(res.Headline, "140") || !strings.Contains(res.Headline, "2 branches") {
		t.Fatalf("headline = %q, want 140 available across 2 branches", res.Headline)
	}
	var trenton *assist.Item
	for i := range res.Items {
		if strings.Contains(res.Items[i].Title, "Trenton") {
			trenton = &res.Items[i]
		}
	}
	if trenton == nil {
		t.Fatalf("no Trenton row in %+v", res.Items)
	}
	want := map[string]string{"On hand": "120 EA", "Allocated": "20 EA", "Available": "100 EA"}
	for _, f := range trenton.Fields {
		if exp, ok := want[f.Label]; ok && f.Value != exp {
			t.Errorf("%s = %q, want %q", f.Label, f.Value, exp)
		}
	}
}

// TestIntegration_TheTwoEmptiesAreDifferentSentencesAgainstRealTables is DIB-443, one ticket on and
// against a real empty table rather than a fake that returns nil because I told it to.
func TestIntegration_TheTwoEmptiesAreDifferentSentencesAgainstRealTables(t *testing.T) {
	w := newWorld(t)
	gm := w.boss()

	noSuch := w.run(t, gm, "stock.lookup", assist.Args{"product": "DIB431B-NO-SUCH-SKU-AT-ALL"})
	if !noSuch.Empty {
		t.Fatal("an unknown SKU should be empty")
	}
	if !strings.Contains(noSuch.Headline, "can't find a product") {
		t.Fatalf("unknown SKU headline = %q", noSuch.Headline)
	}

	barren := w.run(t, gm, "stock.lookup", assist.Args{"product": w.barrenSKU})
	if !barren.Empty {
		t.Fatal("a product with no balances should be empty")
	}
	if !strings.Contains(barren.Headline, "has no stock on record yet") {
		t.Fatalf("no-history headline = %q, a real product that has never moved must not read as 'no such product'", barren.Headline)
	}
	if !strings.Contains(barren.Headline, w.barrenSKU) {
		t.Fatalf("the no-history answer does not name the product it found: %q", barren.Headline)
	}
	if noSuch.Headline == barren.Headline {
		t.Fatal("the two empties produced the same sentence, the DIB-443 distinction is gone")
	}
}

func TestIntegration_OrderLookupResolvesTheVisibleNumber(t *testing.T) {
	w := newWorld(t)
	gm := w.boss()

	// With the '#', without it, and in the wrong case, all the ways a number arrives off a receipt.
	for _, typed := range []string{w.trentonOrderNo, strings.TrimPrefix(w.trentonOrderNo, "#"), strings.ToLower(w.trentonOrderNo)} {
		res := w.run(t, gm, "order.lookup", assist.Args{"order_no": typed})
		if res.Empty {
			t.Fatalf("%q did not resolve: %q", typed, res.Headline)
		}
		if !strings.Contains(res.Headline, "picking") {
			t.Errorf("%q headline = %q, want the real status", typed, res.Headline)
		}
		if !strings.Contains(res.Headline, w.trentonCustName) {
			t.Errorf("%q headline = %q, want the real customer name", typed, res.Headline)
		}
		if !strings.Contains(res.Headline, "$5,123.00") {
			t.Errorf("%q headline = %q, want the real total", typed, res.Headline)
		}
	}
	// The line count is a real COUNT over order_lines, not a guess.
	res := w.run(t, gm, "order.lookup", assist.Args{"order_no": w.trentonOrderNo})
	var lines string
	for _, f := range res.Items[0].Fields {
		if f.Label == "Lines" {
			lines = f.Value
		}
	}
	if lines != "2" {
		t.Fatalf("Lines = %q, want 2", lines)
	}
}

// TestIntegration_TheBalanceIsBillingsOwnNumber runs the assistant and the AR console side by side
// over the same rows. They must agree exactly, because they are the same query, that is the whole
// argument for CustomerAging calling Aging() instead of running an "equivalent" one.
func TestIntegration_TheBalanceIsBillingsOwnNumber(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()

	res := w.run(t, w.boss(), "customer.balance", assist.Args{"customer": w.trentonCustNo})
	if res.Empty {
		t.Fatalf("headline = %q", res.Headline)
	}

	// The AR console's own read, straight from billing.
	console, err := billing.NewCreditService(billing.NewRepository(w.pool)).Aging(ctx, time.Now(), "")
	if err != nil {
		t.Fatalf("console Aging: %v", err)
	}
	var row billing.AgingRow
	for _, r := range console {
		if r.CustomerID == w.trentonCustID {
			row = r
		}
	}
	if row.CustomerID == "" {
		t.Fatal("the fixture account is not on the AR console at all, the fixture is wrong, not the code")
	}
	// 100000 + 40000 open; the 45-days-overdue invoice is the late part.
	if row.TotalCents != 140000 {
		t.Fatalf("console total = %d, want 140000 (the fixture's two invoices)", row.TotalCents)
	}
	if row.OverdueCents() != 100000 {
		t.Fatalf("console overdue = %d, want 100000 (only the 45-day invoice is past due)", row.OverdueCents())
	}
	if !strings.Contains(res.Headline, "$1,400.00") {
		t.Fatalf("the assistant said %q, the console says %d cents open", res.Headline, row.TotalCents)
	}
	if !strings.Contains(res.Headline, "$1,000.00") {
		t.Fatalf("the assistant said %q, the console says %d cents overdue", res.Headline, row.OverdueCents())
	}
}

// TestIntegration_ABalancedAccountSaysNothingOutstanding: agingSelect's HAVING drops accounts that
// net to zero, so an account in perfect standing is ABSENT from the worklist. That must read as a
// good answer and not as "I couldn't find out".
func TestIntegration_ABalancedAccountSaysNothingOutstanding(t *testing.T) {
	w := newWorld(t)

	// The Kingston account, read by someone who CAN see it, after its invoice is settled.
	if _, err := w.pool.Exec(context.Background(),
		`UPDATE invoices SET balance_cents = 0, status = 'PAID' WHERE customer_id = $1::uuid`, w.kingstonCustID); err != nil {
		t.Fatalf("settle: %v", err)
	}
	res := w.run(t, w.kingston(), "customer.balance", assist.Args{"customer": w.kingstonCustNo})
	if res.Empty {
		t.Fatalf("a settled account came back EMPTY: %q, 'nothing outstanding' is an ANSWER", res.Headline)
	}
	if !strings.Contains(res.Headline, "has nothing outstanding") {
		t.Fatalf("headline = %q, want 'has nothing outstanding'", res.Headline)
	}
}

func TestIntegration_ActivityReadsTheARLedger(t *testing.T) {
	w := newWorld(t)

	res := w.run(t, w.boss(), "customer.activity", assist.Args{"customer": w.trentonCustNo})
	if res.Empty {
		t.Fatalf("headline = %q", res.Headline)
	}
	if len(res.Items) != 3 {
		t.Fatalf("items = %d, want the fixture's 3 ledger rows", len(res.Items))
	}
	// Newest first: the payment landed yesterday, the invoices before it.
	if !strings.HasPrefix(res.Items[0].Title, "Payment ") {
		t.Fatalf("first row = %q, want the most recent movement (a Payment)", res.Items[0].Title)
	}
	if !strings.Contains(res.Items[0].Title, w.trentonPayNo) {
		t.Fatalf("the payment row does not carry its document number: %q", res.Items[0].Title)
	}
	if !strings.HasPrefix(res.Items[1].Title, "Invoice ") {
		t.Fatalf("second row = %q, want an Invoice", res.Items[1].Title)
	}
}

func TestIntegration_ANewAccountWithNoMovementsSaysSo(t *testing.T) {
	w := newWorld(t)
	// The Kingston account has an invoice but no customer_transactions rows.
	res := w.run(t, w.kingston(), "customer.activity", assist.Args{"customer": w.kingstonCustNo})
	if !res.Empty {
		t.Fatalf("headline = %q, want the empty-but-fine answer", res.Headline)
	}
	if !strings.Contains(res.Headline, "Nothing recent on") {
		t.Fatalf("headline = %q", res.Headline)
	}
}

func TestIntegration_PaymentLookupInAllThreeShapes(t *testing.T) {
	w := newWorld(t)
	gm := w.boss()

	t.Run("by payment number", func(t *testing.T) {
		res := w.run(t, gm, "payment.lookup", assist.Args{"reference": w.trentonPayNo})
		if res.Empty || len(res.Items) != 1 {
			t.Fatalf("empty=%v items=%d headline=%q", res.Empty, len(res.Items), res.Headline)
		}
		if res.Items[0].Title != w.trentonPayNo {
			t.Fatalf("title = %q", res.Items[0].Title)
		}
	})

	t.Run("by gateway reference", func(t *testing.T) {
		res := w.run(t, gm, "payment.lookup", assist.Args{"reference": strings.ToUpper(w.trentonPayRef)})
		if res.Empty || len(res.Items) != 1 {
			t.Fatalf("a gateway reference in upper case did not resolve: %q", res.Headline)
		}
	})

	t.Run("by exact amount", func(t *testing.T) {
		res := w.run(t, gm, "payment.lookup", assist.Args{"amount": "1240.00"})
		if res.Empty {
			t.Fatalf("headline = %q", res.Headline)
		}
		var sawExact bool
		for _, it := range res.Items {
			if it.Title == w.nearPayNo {
				t.Fatalf("the payment one cent away (%s) matched an EXACT $1,240.00 search, a person told the money arrived when it did not", w.nearPayNo)
			}
			if it.Title == w.trentonPayNo {
				sawExact = true
			}
		}
		if !sawExact {
			t.Fatalf("the exactly-matching payment %s was not returned: %+v", w.trentonPayNo, res.Items)
		}
	})

	t.Run("an amount one cent away does not match", func(t *testing.T) {
		// $1,240.01 exists in the fixture; $1,240.02 does not. Both are 'near' $1,240.00, and a
		// tolerant matcher would return the same rows for all three.
		res := w.run(t, gm, "payment.lookup", assist.Args{"amount": "1240.02"})
		if !res.Empty {
			t.Fatalf("$1,240.02 matched %d payments, the amount filter is not exact: %+v", len(res.Items), res.Items)
		}
	})

	t.Run("by customer", func(t *testing.T) {
		res := w.run(t, gm, "payment.lookup", assist.Args{"customer": w.trentonCustName})
		if res.Empty || len(res.Items) != 2 {
			t.Fatalf("by-customer found %d payments (want the fixture's 2): %q", len(res.Items), res.Headline)
		}
	})

	t.Run("amount and customer together narrow", func(t *testing.T) {
		res := w.run(t, gm, "payment.lookup", assist.Args{"customer": w.trentonCustName, "amount": "1240.00"})
		if res.Empty || len(res.Items) != 1 {
			t.Fatalf("combined filters found %d payments, want 1: %q", len(res.Items), res.Headline)
		}
		if res.Items[0].Title != w.trentonPayNo {
			t.Fatalf("title = %q", res.Items[0].Title)
		}
	})

	t.Run("nothing matches is an answer", func(t *testing.T) {
		res := w.run(t, gm, "payment.lookup", assist.Args{"reference": "PMT-DIB431B-DOES-NOT-EXIST"})
		if !res.Empty {
			t.Fatalf("headline = %q", res.Headline)
		}
		if res.Note == "" {
			t.Error("an empty payment answer with no note tells the user nothing about what would change it")
		}
	})
}

// -- fixture helpers ----------------------------------------------------------------------------

func mustQuery(t *testing.T, pool *pgxpool.Pool, sql string, dst *string) {
	t.Helper()
	if err := pool.QueryRow(context.Background(), sql).Scan(dst); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

func insertProduct(t *testing.T, pool *pgxpool.Pool, sku, name string) string {
	t.Helper()
	var id string
	err := pool.QueryRow(context.Background(), `
		INSERT INTO products (sku, name, base_uom, price_uom, is_active)
		VALUES ($1::citext, $2, 'EA', 'EA', TRUE) RETURNING id::text`, sku, name).Scan(&id)
	if err != nil {
		t.Fatalf("insert product %s: %v", sku, err)
	}
	return id
}

func insertBalance(t *testing.T, pool *pgxpool.Pool, productID, branchID string, onHand, reserved float64) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO stock_balances (product_id, location_id, branch_id, on_hand_base, allocated_base, qty_reserved)
		VALUES ($1::uuid, $2::uuid, $2::uuid, $3, $4, $4)`, productID, branchID, onHand, reserved)
	if err != nil {
		t.Fatalf("insert balance: %v", err)
	}
}

func insertCustomer(t *testing.T, pool *pgxpool.Pool, accountNo, name, homeBranchID string) string {
	t.Helper()
	var id string
	err := pool.QueryRow(context.Background(), `
		INSERT INTO customers (account_no, name, customer_type, home_branch_id, payment_terms_days, credit_limit_cents, is_active)
		VALUES ($1::citext, $2, 'CONTRACTOR', $3::uuid, 30, 5000000, TRUE) RETURNING id::text`,
		accountNo, name, homeBranchID).Scan(&id)
	if err != nil {
		t.Fatalf("insert customer %s: %v", accountNo, err)
	}
	return id
}

func insertOrder(t *testing.T, pool *pgxpool.Pool, orderNo, customerID, branchID, status string, totalCents int64) string {
	t.Helper()
	var id string
	err := pool.QueryRow(context.Background(), `
		INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, subtotal_cents, tax_cents, total_cents)
		VALUES ($1::citext, $2::uuid, $3::uuid, $4, 'PICKUP', $5, 0, $5) RETURNING id::text`,
		orderNo, customerID, branchID, status, totalCents).Scan(&id)
	if err != nil {
		t.Fatalf("insert order %s: %v", orderNo, err)
	}
	return id
}

func insertOrderLine(t *testing.T, pool *pgxpool.Pool, orderID, productID string, lineNo int) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO order_lines (order_id, line_no, product_id, description, qty_entered, uom_entered,
		                         qty_base, unit_price_cents, price_uom, line_total_cents)
		VALUES ($1::uuid, $2, $3::uuid, 'DIB-431B fixture line', 1, 'EA', 1, 100, 'EA', 100)`,
		orderID, lineNo, productID)
	if err != nil {
		t.Fatalf("insert order line: %v", err)
	}
}

// insertInvoice raises a FINALIZED invoice due dueInDays from today, NEGATIVE for an overdue one,
// which is what puts it in a late bucket rather than in CURRENT.
func insertInvoice(t *testing.T, pool *pgxpool.Pool, invoiceNo, customerID, branchID string, totalCents, balanceCents int64, dueInDays int) string {
	t.Helper()
	var id string
	err := pool.QueryRow(context.Background(), `
		INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                      subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at)
		VALUES ($1::citext, $2::uuid, $3::uuid, 'FINALIZED',
		        CURRENT_DATE - 60, CURRENT_DATE + $6::int, $4, 0, $4, $5, now())
		RETURNING id::text`, invoiceNo, customerID, branchID, totalCents, balanceCents, dueInDays).Scan(&id)
	if err != nil {
		t.Fatalf("insert invoice %s: %v", invoiceNo, err)
	}
	return id
}

func insertPayment(t *testing.T, pool *pgxpool.Pool, paymentNo, customerID, branchID string, amountCents int64, gatewayRef string) string {
	t.Helper()
	var id string
	err := pool.QueryRow(context.Background(), `
		INSERT INTO payments (payment_no, customer_id, method, amount_cents, status, branch_id, gateway_ref, received_at)
		VALUES ($1::citext, $2::uuid, 'CHEQUE', $3, 'RECEIVED', $4::uuid, $5, now())
		RETURNING id::text`, paymentNo, customerID, amountCents, branchID, gatewayRef).Scan(&id)
	if err != nil {
		t.Fatalf("insert payment %s: %v", paymentNo, err)
	}
	return id
}

func insertTxn(t *testing.T, pool *pgxpool.Pool, customerID, txnType string, amountCents, balanceAfter int64, sourceType, sourceRefID, branchID string, daysAgo int) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO customer_transactions (customer_id, txn_type, amount_cents, balance_after,
		                                   source_type, source_ref_id, branch_id, memo, occurred_at)
		VALUES ($1::uuid, $2, $3, $4, $5, $6::uuid, $7::uuid, 'DIB-431B fixture', now() + ($8::int || ' days')::interval)`,
		customerID, txnType, amountCents, balanceAfter, sourceType, sourceRefID, branchID, daysAgo)
	if err != nil {
		t.Fatalf("insert txn: %v", err)
	}
}
