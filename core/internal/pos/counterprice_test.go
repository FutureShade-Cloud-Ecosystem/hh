package pos

// counterprice_test.go, the counter's customer-aware price read. DB-free, on the service_test.go
// harness; exactly additive.
//
// The proofs that matter here are about AUTHORITY and ABSENCE, not arithmetic: this package must
// not be able to invent a price, must not be able to be talked into a cheaper tier from the wire,
// and must not be able to leak cost. The arithmetic itself is pricing's, tested there.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pricing"
)

// fakeQuoter records what it was ASKED, and answers a fixed picture. Recording the input is the
// point of most of these tests: the question POS asks the engine is the contract under test.
type fakeQuoter struct {
	got []pricing.CounterQuoteInput
	out []pricing.CounterQuote
	err error
}

func (f *fakeQuoter) CounterQuotes(_ context.Context, ins []pricing.CounterQuoteInput) ([]pricing.CounterQuote, error) {
	f.got = append(f.got, ins...)
	if f.err != nil {
		return nil, f.err
	}
	if f.out != nil {
		return f.out, nil
	}
	out := make([]pricing.CounterQuote, len(ins))
	for i, in := range ins {
		out[i] = pricing.CounterQuote{ProductID: in.ProductID, PriceUOM: "SF", RetailCents: 2200, CustomerCents: 2200}
	}
	return out, nil
}

func counterHarness(t *testing.T) (*harness, *fakeQuoter) {
	t.Helper()
	h := newHarness()
	q := &fakeQuoter{}
	h.svc.WithCounterQuoter(q)
	return h, q
}

// -- the customer type is the SERVER's fact, never the caller's claim ----------------------------

func TestCounterPrices_CustomerTypeComesFromTheAccount(t *testing.T) {
	h, q := counterHarness(t)
	// The fake store's customer record is the authority; the request body carries no type at all,
	// and could not be believed if it did.
	out, err := h.svc.CounterPrices(context.Background(), CounterPriceInput{
		BranchID: "branch-trt", CustomerID: "cust-abc",
		Items: []CounterPriceItem{{ProductID: "prod-paver", QtyEntered: 10, UOMEntered: "SF"}},
	})
	if err != nil {
		t.Fatalf("counter prices: %v", err)
	}
	if len(q.got) != 1 {
		t.Fatalf("one product asked, one quote requested: %+v", q.got)
	}
	if q.got[0].CustomerType != out.CustomerType {
		t.Fatalf("the type the engine was asked with must be the type reported back: %q vs %q",
			q.got[0].CustomerType, out.CustomerType)
	}
	if q.got[0].CustomerID != "cust-abc" {
		t.Fatalf("the resolved account must reach the engine: %+v", q.got[0])
	}
}

// missingCustStore is the fake store with ONE answer changed: no customer row is ever found. The
// shipped fakeStore synthesizes a RETAIL record for any id it does not hold, which is a fine
// default for the ring tests but makes "this account does not exist" inexpressible, and that is
// precisely the branch under test here. Embedding rather than editing the shared fake keeps every
// other test in this package byte-identical.
type missingCustStore struct{ *fakeStore }

func (missingCustStore) ReadCustomer(context.Context, string) (customerRec, error) {
	return customerRec{}, errors.New("pos: no such customer")
}

func TestCounterPrices_UnknownAccountPricesAsRetail(t *testing.T) {
	h, q := counterHarness(t)
	h.svc.store = missingCustStore{h.store}
	out, err := h.svc.CounterPrices(context.Background(), CounterPriceInput{
		CustomerID: "cust-does-not-exist",
		Items:      []CounterPriceItem{{ProductID: "prod-paver", QtyEntered: 1}},
	})
	if err != nil {
		t.Fatalf("an unknown account is not an error: %v", err)
	}
	if out.CustomerType != "RETAIL" {
		t.Fatalf("an unresolvable account falls back to the DEAREST tier, never a guess: %q", out.CustomerType)
	}
	if len(q.got) != 1 || q.got[0].CustomerID != "" {
		t.Fatalf("an unresolvable id must not be forwarded as if it were an account: %+v", q.got)
	}
}

// -- walk-in: retail stands alone, through the same read -----------------------------------------

func TestCounterPrices_WalkInAsksTheSameQuestion(t *testing.T) {
	h, q := counterHarness(t)
	out, err := h.svc.CounterPrices(context.Background(), CounterPriceInput{
		Items: []CounterPriceItem{{ProductID: "prod-paver", QtyEntered: 1}},
	})
	if err != nil {
		t.Fatalf("walk-in: %v", err)
	}
	if out.CustomerType != "RETAIL" || len(out.Prices) != 1 {
		t.Fatalf("a walk-in gets an ordinary answer: %+v", out)
	}
	if q.got[0].CustomerID != "" {
		t.Fatalf("no account means no account on the wire: %+v", q.got[0])
	}
	if out.Prices[0].SavingCents != 0 || out.Prices[0].Basis != "" {
		t.Fatalf("retail alone carries no saving and no basis: %+v", out.Prices[0])
	}
}

// -- empty is never an error, at every door ------------------------------------------------------

func TestCounterPrices_EmptyIsNeverAnError(t *testing.T) {
	h, _ := counterHarness(t)
	for _, tc := range []struct {
		name string
		in   CounterPriceInput
	}{
		{"no items", CounterPriceInput{BranchID: "branch-trt"}},
		{"blank product ids", CounterPriceInput{Items: []CounterPriceItem{{ProductID: "  "}}}},
	} {
		out, err := h.svc.CounterPrices(context.Background(), tc.in)
		if err != nil {
			t.Fatalf("%s must be an empty answer, not an error: %v", tc.name, err)
		}
		if out.Prices == nil {
			t.Fatalf("%s must answer an empty LIST, never a null the client has to guard", tc.name)
		}
	}
}

func TestCounterPrices_UnwiredAuthorityAnswersEmpty(t *testing.T) {
	h := newHarness()
	h.svc.pricing = nil // neither an explicit quoter nor an assertable resolver
	out, err := h.svc.CounterPrices(context.Background(), CounterPriceInput{
		Items: []CounterPriceItem{{ProductID: "prod-paver", QtyEntered: 1}},
	})
	if err != nil {
		t.Fatalf("an unwired price read draws an empty screen, it does not fault: %v", err)
	}
	if len(out.Prices) != 0 {
		t.Fatalf("unwired must answer nothing rather than a fabricated price: %+v", out.Prices)
	}
}

// -- the read is bounded -------------------------------------------------------------------------

func TestCounterPrices_RefusesAnUnboundedRead(t *testing.T) {
	h, _ := counterHarness(t)
	items := make([]CounterPriceItem, maxCounterPriceItems+1)
	for i := range items {
		items[i] = CounterPriceItem{ProductID: "prod-paver", QtyEntered: 1}
	}
	if _, err := h.svc.CounterPrices(context.Background(), CounterPriceInput{Items: items}); err == nil {
		t.Fatalf("an oversized read must be refused in words, not served")
	}
}

// -- a zero/absent quantity is a QUESTION, not a fault -------------------------------------------

func TestCounterPrices_AbsentQuantityAsksAboutOne(t *testing.T) {
	h, q := counterHarness(t)
	if _, err := h.svc.CounterPrices(context.Background(), CounterPriceInput{
		Items: []CounterPriceItem{{ProductID: "prod-paver"}}, // a quick-pick tile knows no qty
	}); err != nil {
		t.Fatalf("a tile with no quantity is an ordinary ask: %v", err)
	}
	if q.got[0].QtyEntered != 1 {
		t.Fatalf("an absent quantity asks about one unit: %v", q.got[0].QtyEntered)
	}
}

// -- THE COST GATE, structurally: there is no cost field to leak ---------------------------------

func TestCounterQuote_CarriesNoCostOrMarginBytes(t *testing.T) {
	// Marshalled exactly as the handler marshals it, the portal's marshal-and-assert-absent idiom.
	// A CounterQuote is the CUSTOMER's side of the counter; margin/cost/floor/profit belong to
	// MarginGlance behind its own role gate and must not appear here under ANY caller, because
	// there is no field on the struct that could carry them.
	b, err := json.Marshal(CounterPrices{CustomerType: "CONTRACTOR", Prices: []pricing.CounterQuote{{
		ProductID: "prod-paver", PriceUOM: "SF", RetailCents: 2200, CustomerCents: 1804,
		SavingCents: 396, SavingBps: 1800, Basis: pricing.BasisAccount,
	}}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, banned := range []string{"cost", "margin", "floor", "profit", "wac"} {
		if strings.Contains(strings.ToLower(string(b)), banned) {
			t.Fatalf("the counter price payload must not carry %q: %s", banned, b)
		}
	}
}

// -- a saving of zero is ABSENT, not a "$0.00 saved" badge ---------------------------------------

func TestCounterQuote_ZeroSavingIsByteAbsent(t *testing.T) {
	b, err := json.Marshal(pricing.CounterQuote{
		ProductID: "prod-paver", PriceUOM: "SF", RetailCents: 2200, CustomerCents: 2200,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, banned := range []string{"saving_cents", "saving_bps", "basis"} {
		if strings.Contains(string(b), banned) {
			t.Fatalf("no advantage means no saving field at all, so no badge can render: %s", b)
		}
	}
}
