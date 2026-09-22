package assist

// commands_test.go, each command's happy path, and each command's empty path.
//
// THE EMPTY PATHS GET AS MUCH ATTENTION AS THE HAPPY ONES, and that is the direct inheritance from
// DIB-443: a surface that reports "nothing found" in the voice of a fault produces a bug report
// about inventory when the real defect was a sentence. Every assertion below that checks
// Result.Empty is also checking that the headline reads as an ANSWER.
//
// MUTATION TARGETS
//
//	make a not-found return an error instead of Empty     -> TestOrderLookup_UnknownNumberIsAnAnswerNotAFault
//	                                                        TestStockLookup_NoHistoryIsAnOrdinaryState
//	collapse "unknown product" and "no stock" into one    -> TestStockLookup_UnknownProductAndNoStockSayDifferentThings
//	drop the all-filters-empty guard in payment.lookup    -> TestPaymentLookup_RefusesAnUnfilteredSearch
//	round instead of refusing sub-cent amounts            -> TestParseAmount_RefusesMorePrecisionThanCents
//	print raw cents anywhere                              -> TestMoney_Formatting

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

func newTestRegistry(f *fakeReaders) *Registry { return NewRegistry(f.deps(), nil) }

// -- payment.lookup -----------------------------------------------------------------------------

func TestPaymentLookup_HappyPath(t *testing.T) {
	f := seededReaders()
	res := mustRun(t, newTestRegistry(f), manager(), "payment.lookup", Args{"reference": "PMT-1042"})

	if res.Empty {
		t.Fatal("a found payment was reported as empty")
	}
	if len(res.Items) != 1 || res.Items[0].Title != "PMT-1042" {
		t.Fatalf("items = %+v", res.Items)
	}
	// The amount is formatted ONCE, in the command layer, so the panel and the chat cannot disagree.
	if got := fieldValue(res.Items[0], "Amount"); got != "$1,240.00" {
		t.Fatalf("Amount = %q, want $1,240.00", got)
	}
	if !strings.Contains(res.Headline, "1 payment") {
		t.Fatalf("headline should be singular for one row: %q", res.Headline)
	}
	if f.gotQuery.Reference != "PMT-1042" {
		t.Fatalf("the reference did not reach the reader: %+v", f.gotQuery)
	}
}

func TestPaymentLookup_ByAmountAndCustomer(t *testing.T) {
	f := seededReaders()
	mustRun(t, newTestRegistry(f), manager(), "payment.lookup", Args{"amount": "$1,240.00", "customer": "Northgate"})

	if f.gotQuery.AmountCents != 124000 {
		t.Fatalf("amount reached the reader as %d cents, want 124000", f.gotQuery.AmountCents)
	}
	if f.gotQuery.CustomerRef != "Northgate" {
		t.Fatalf("customer = %q", f.gotQuery.CustomerRef)
	}
}

func TestPaymentLookup_NothingFoundIsAnAnswer(t *testing.T) {
	f := seededReaders()
	f.payments = nil
	res := mustRun(t, newTestRegistry(f), manager(), "payment.lookup", Args{"reference": "PMT-9999"})

	if !res.Empty {
		t.Fatal("no matches should set Empty")
	}
	if !strings.Contains(res.Headline, "PMT-9999") {
		t.Fatalf("the headline should name what was looked for: %q", res.Headline)
	}
	// The word that must NOT be there. An empty result is not an error, and the surface grades the
	// difference off this shape.
	for _, forbidden := range []string{"error", "Error", "failed", "internal"} {
		if strings.Contains(res.Headline+res.Note, forbidden) {
			t.Fatalf("an empty result is dressed as a fault (%q): %q / %q", forbidden, res.Headline, res.Note)
		}
	}
}

func TestPaymentLookup_RefusesAnUnfilteredSearch(t *testing.T) {
	f := seededReaders()
	_, err := newTestRegistry(f).Run(context.Background(), manager(), "payment.lookup", Args{})

	var bad httpx.BadRequestError
	if !errors.As(err, &bad) {
		t.Fatalf("an unfiltered payment search gave %T (%v), want a 400", err, err)
	}
	if f.calls != 0 {
		t.Fatal("an unfiltered search reached the payment reader, that is a report, not a lookup")
	}
}

func TestParseAmount_ReadsWhatPeopleType(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want int64
	}{
		{"1240", 124000},
		{"1240.00", 124000},
		{"$1,240.00", 124000},
		{"$ 1,240.5", 124050},
		{"0.99", 99},
	} {
		got, err := parseAmountCents(tc.in)
		if err != nil {
			t.Fatalf("parseAmountCents(%q): %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("parseAmountCents(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestParseAmount_RefusesMorePrecisionThanCents(t *testing.T) {
	// Silently rounding 1240.005 would search for an amount the user never typed, and they would
	// have no way to tell. Refusing is the honest failure.
	if _, err := parseAmountCents("1240.005"); err == nil {
		t.Fatal("sub-cent precision was accepted, the search would be for a different amount")
	}
	if _, err := parseAmountCents("nine hundred"); err == nil {
		t.Fatal("a non-numeric amount was accepted")
	}
	if _, err := parseAmountCents("0.00"); err == nil {
		t.Fatal("$0.00 was accepted, it is also the no-filter sentinel, so it must be refused")
	}
}

// -- order.lookup -------------------------------------------------------------------------------

func TestOrderLookup_HappyPath(t *testing.T) {
	f := seededReaders()
	res := mustRun(t, newTestRegistry(f), frontline(), "order.lookup", Args{"order_no": "SO-4"})

	if res.Empty || len(res.Items) != 1 {
		t.Fatalf("res = %+v", res)
	}
	if !strings.Contains(res.Headline, "SO-4") || !strings.Contains(res.Headline, "picking") {
		t.Fatalf("headline should say what and where: %q", res.Headline)
	}
	if got := fieldValue(res.Items[0], "Total"); got != "$5,123.00" {
		t.Fatalf("Total = %q", got)
	}
}

func TestOrderLookup_UnknownNumberIsAnAnswerNotAFault(t *testing.T) {
	f := seededReaders()
	f.orderHit = false
	res := mustRun(t, newTestRegistry(f), frontline(), "order.lookup", Args{"order_no": "SO-999"})

	if !res.Empty {
		t.Fatal("an order that does not exist should be Empty, not an error")
	}
	if !strings.Contains(res.Headline, "SO-999") {
		t.Fatalf("the headline should echo the number that was looked for: %q", res.Headline)
	}
	// The note must offer the two real explanations, one of which is the scope fence.
	if !strings.Contains(res.Note, "access") {
		t.Fatalf("the note should mention branch access as a possibility: %q", res.Note)
	}
}

// -- stock.lookup -------------------------------------------------------------------------------

func TestStockLookup_HappyPath(t *testing.T) {
	f := seededReaders()
	res := mustRun(t, newTestRegistry(f), frontline(), "stock.lookup", Args{"product": "TB-BLU60"})

	if res.Empty || len(res.Items) != 2 {
		t.Fatalf("res = %+v", res)
	}
	// The product is named back the way the catalogue prints it, not echoed as the typed id.
	if !strings.Contains(res.Headline, "Techo-Bloc Blu 60 (TB-BLU60)") {
		t.Fatalf("headline should name the product: %q", res.Headline)
	}
	if !strings.Contains(res.Headline, "260 ea") {
		t.Fatalf("headline should total the available across branches: %q", res.Headline)
	}
	if got := fieldValue(res.Items[0], "Available"); got != "200 ea" {
		t.Fatalf("Available = %q", got)
	}
}

func TestStockLookup_NoHistoryIsAnOrdinaryState(t *testing.T) {
	// The exact shape of DIB-443, one layer up: a product that resolves but has never moved.
	f := seededReaders()
	f.stock = nil
	res := mustRun(t, newTestRegistry(f), frontline(), "stock.lookup", Args{"product": "TB-BLU60"})

	if !res.Empty {
		t.Fatal("a product with no stock history should be Empty")
	}
	if !strings.Contains(res.Note, "received") {
		t.Fatalf("the note should say what would change the answer: %q", res.Note)
	}
	for _, forbidden := range []string{"error", "failed", "internal"} {
		if strings.Contains(strings.ToLower(res.Headline+res.Note), forbidden) {
			t.Fatalf("no-stock is dressed as a fault: %q / %q", res.Headline, res.Note)
		}
	}
}

func TestStockLookup_UnknownProductAndNoStockSayDifferentThings(t *testing.T) {
	// Two different empties. Collapsing them is the mistake that turns "I typed the SKU wrong" into
	// "we have none of these", which is a claim about inventory that a person will act on.
	noSuchProduct := seededReaders()
	noSuchProduct.stockHit = false
	a := mustRun(t, newTestRegistry(noSuchProduct), frontline(), "stock.lookup", Args{"product": "TYPO-1"})

	noHistory := seededReaders()
	noHistory.stock = nil
	b := mustRun(t, newTestRegistry(noHistory), frontline(), "stock.lookup", Args{"product": "TB-BLU60"})

	if a.Headline == b.Headline {
		t.Fatalf("an unknown product and an unstocked product say the same sentence: %q", a.Headline)
	}
	if !strings.Contains(a.Headline, "can't find") {
		t.Fatalf("an unknown product should say it cannot be found: %q", a.Headline)
	}
	if !strings.Contains(b.Headline, "no stock on record") {
		t.Fatalf("a stockless product should say it has no stock: %q", b.Headline)
	}
}

func TestStockLookup_NegativeAvailableIsExplained(t *testing.T) {
	f := seededReaders()
	f.stock = []StockRecord{{BranchName: "Kelowna", OnHand: 0, Reserved: 4, Available: -4, UOM: "ea"}}
	res := mustRun(t, newTestRegistry(f), frontline(), "stock.lookup", Args{"product": "TB-BLU60"})

	if res.Note == "" {
		t.Fatal("a negative available needs the sentence that stops the support call")
	}
	if got := fieldValue(res.Items[0], "Available"); got != "-4 ea" {
		t.Fatalf("Available = %q", got)
	}
}

// -- customer.balance / customer.activity -------------------------------------------------------

func TestCustomerBalance_HappyPath(t *testing.T) {
	f := seededReaders()
	res := mustRun(t, newTestRegistry(f), manager(), "customer.balance", Args{"customer": "Northgate"})

	if res.Empty {
		t.Fatal("a resolved customer with a balance is not empty")
	}
	if !strings.Contains(res.Headline, "$3,488.00") || !strings.Contains(res.Headline, "$1,200.00") {
		t.Fatalf("headline should carry both numbers: %q", res.Headline)
	}
}

func TestCustomerBalance_ZeroIsItsOwnGoodAnswer(t *testing.T) {
	f := seededReaders()
	f.balance = BalanceRecord{}
	res := mustRun(t, newTestRegistry(f), manager(), "customer.balance", Args{"customer": "Northgate"})

	if res.Empty {
		t.Fatal("a zero balance is a real answer, not an empty result")
	}
	if !strings.Contains(res.Headline, "nothing outstanding") {
		t.Fatalf("a zero balance should read as good news: %q", res.Headline)
	}
}

func TestCustomerBalance_NoOverdueReadsDifferently(t *testing.T) {
	f := seededReaders()
	f.balance = BalanceRecord{OpenCents: 50000}
	res := mustRun(t, newTestRegistry(f), manager(), "customer.balance", Args{"customer": "Northgate"})
	if !strings.Contains(res.Headline, "none of it overdue") {
		t.Fatalf("owing but current should say so: %q", res.Headline)
	}
}

func TestCustomerCommands_UnknownCustomerSaysSoOnce(t *testing.T) {
	f := seededReaders()
	f.custHit = false
	for _, code := range []string{"customer.balance", "customer.activity"} {
		res := mustRun(t, newTestRegistry(f), manager(), code, Args{"customer": "Nrthgate"})
		if !res.Empty {
			t.Fatalf("%s: an unknown customer should be Empty", code)
		}
		if !strings.Contains(res.Headline, "Nrthgate") {
			t.Fatalf("%s: the headline should echo what was typed: %q", code, res.Headline)
		}
	}
}

func TestCustomerActivity_HappyPathAndEmpty(t *testing.T) {
	f := seededReaders()
	res := mustRun(t, newTestRegistry(f), frontline(), "customer.activity", Args{"customer": "Northgate"})
	if len(res.Items) != 2 {
		t.Fatalf("items = %+v", res.Items)
	}
	if res.Items[0].Title != "Order SO-4" {
		t.Fatalf("title = %q", res.Items[0].Title)
	}

	f.activity = nil
	empty := mustRun(t, newTestRegistry(f), frontline(), "customer.activity", Args{"customer": "Northgate"})
	if !empty.Empty || !strings.Contains(empty.Headline, "Nothing recent") {
		t.Fatalf("empty activity = %+v", empty)
	}
}

// -- formatting ---------------------------------------------------------------------------------

func TestMoney_Formatting(t *testing.T) {
	for _, tc := range []struct {
		in   int64
		want string
	}{
		{0, "$0.00"},
		{5, "$0.05"},
		{124000, "$1,240.00"},
		{100000000, "$1,000,000.00"},
		{-124000, "-$1,240.00"},
	} {
		if got := money(tc.in); got != tc.want {
			t.Fatalf("money(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestQty_TrimsFalsePrecision(t *testing.T) {
	for _, tc := range []struct {
		in   float64
		want string
	}{{12, "12"}, {18.5, "18.5"}, {0.125, "0.125"}, {0, "0"}, {-4, "-4"}} {
		if got := qty(tc.in); got != tc.want {
			t.Fatalf("qty(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if got := qtyUOM(12, ""); got != "12" {
		t.Fatalf("an empty UOM must not leave a trailing space: %q", got)
	}
}

func TestThousands_GroupsTokenCounts(t *testing.T) {
	if got := thousands(1240000); got != "1,240,000" {
		t.Fatalf("thousands = %q", got)
	}
	if got := thousands(0); got != "0" {
		t.Fatalf("thousands(0) = %q", got)
	}
}

func fieldValue(it Item, label string) string {
	for _, f := range it.Fields {
		if f.Label == label {
			return f.Value
		}
	}
	return ""
}

// -- DIB-465 · the record an answer is ABOUT ----------------------------------------------------
//
// MUTATION TARGET (M3b): build the Ref from the visible number (o.OrderNo, p.SKU) instead of the
// uuid and TestAnswersCarryTheRecordTheyAreAbout dies, a display number is not addressable, and
// the frontend's uuid check would drop the link silently rather than loudly.

func TestAnswersCarryTheRecordTheyAreAbout(t *testing.T) {
	f := seededReaders()
	reg := newTestRegistry(f)
	c := Caller{UserID: "u", RoleCode: "mgr", Tier: 1, Global: true}

	cases := []struct {
		code string
		args Args
		kind string
		id   string
	}{
		{"order.lookup", Args{"order_no": "SO-4"}, RefOrder, "8f14e45f-ceea-467a-9b1e-1f0e2d4a77c1"},
		{"stock.lookup", Args{"product": "TB-BLU60"}, RefProduct, "p-1"},
		{"customer.balance", Args{"customer": "Northgate"}, RefCustomer, "c-1"},
		{"customer.activity", Args{"customer": "Northgate"}, RefCustomer, "c-1"},
	}
	for _, tc := range cases {
		res, err := reg.Run(context.Background(), c, tc.code, tc.args)
		if err != nil {
			t.Fatalf("%s: %v", tc.code, err)
		}
		if res.Ref == nil {
			t.Fatalf("%s: no ref, the answer names a record and cannot link to it", tc.code)
		}
		if res.Ref.Kind != tc.kind || res.Ref.ID != tc.id {
			t.Fatalf("%s: ref = %+v, want %s/%s", tc.code, *res.Ref, tc.kind, tc.id)
		}
	}
}

func TestAPaymentAnswerCarriesNoRefBecauseThereIsNowhereToSendYou(t *testing.T) {
	f := seededReaders()
	res, err := newTestRegistry(f).Run(context.Background(),
		Caller{UserID: "u", RoleCode: "mgr", Tier: 1, Global: true},
		"payment.lookup", Args{"reference": "PMT-1042"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Ref != nil {
		t.Fatalf("a payment has no record surface in this app, inventing a target is the DIB-402 "+
			"mistake: %+v", *res.Ref)
	}
}

func TestAnEmptyStockAnswerStillLinksToTheProduct(t *testing.T) {
	f := seededReaders()
	f.stock = nil // the product resolves; it has no balances
	res, err := newTestRegistry(f).Run(context.Background(),
		Caller{UserID: "u", RoleCode: "counter", Tier: 3}, "stock.lookup", Args{"product": "TB-BLU60"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Empty {
		t.Fatal("no balances is an empty answer")
	}
	if res.Ref == nil || res.Ref.ID != "p-1" {
		t.Fatalf("an empty answer about a product that DOES exist is exactly where the link earns "+
			"its keep: %+v", res.Ref)
	}
}

func TestAnUnresolvedRecordCarriesNoRef(t *testing.T) {
	f := seededReaders()
	f.stockHit = false
	f.custHit = false
	f.orderHit = false
	reg := newTestRegistry(f)
	c := Caller{UserID: "u", RoleCode: "mgr", Tier: 1, Global: true}
	for _, tc := range []struct {
		code string
		args Args
	}{
		{"order.lookup", Args{"order_no": "SO-999"}},
		{"stock.lookup", Args{"product": "NOPE-1"}},
		{"customer.balance", Args{"customer": "Nobody"}},
	} {
		res, err := reg.Run(context.Background(), c, tc.code, tc.args)
		if err != nil {
			t.Fatalf("%s: %v", tc.code, err)
		}
		if res.Ref != nil {
			t.Fatalf("%s: nothing resolved, so there is nothing to link to: %+v", tc.code, *res.Ref)
		}
	}
}
