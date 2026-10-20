package assist

// prices_test.go, slice 2 sub item 2: the price table and what settlement does with it.

import (
	"context"
	"testing"
)

func TestModelPriceCost(t *testing.T) {
	p := ModelPrice{InMicroPerM: 3_000_000, OutMicroPerM: 15_000_000, CachedMicroPerM: 300_000}
	cases := []struct {
		name            string
		in, out, cached int
		want            int64
	}{
		{"a million in", 1_000_000, 0, 0, 3_000_000},
		{"a million out", 0, 1_000_000, 0, 15_000_000},
		{"cached is its own price", 0, 0, 1_000_000, 300_000},
		{"mixed", 1000, 200, 50, 6_015},
		{"one token", 1, 0, 0, 3},
		{"nothing costs nothing", 0, 0, 0, 0},
	}
	for _, c := range cases {
		if got := p.Cost(c.in, c.out, c.cached); got != c.want {
			t.Errorf("%s: Cost(%d,%d,%d) = %d, want %d", c.name, c.in, c.out, c.cached, got, c.want)
		}
	}
	// A price that would round to a fraction still records at least one micro cent for real usage.
	tiny := ModelPrice{InMicroPerM: 1}
	if got := tiny.Cost(1, 0, 0); got != 1 {
		t.Errorf("one token at one micro cent per million = %d, want rounded up to 1", got)
	}
}

// seqPrices serves the row with the highest seq, like the Postgres query.
type seqPrices struct{ rows []ModelPrice }

func (s seqPrices) PriceFor(_ context.Context, alias, model string) (ModelPrice, bool, error) {
	var best ModelPrice
	found := false
	for _, r := range s.rows {
		if r.Alias == alias && r.Model == model && (!found || r.Seq > best.Seq) {
			best, found = r, true
		}
	}
	return best, found, nil
}

func TestSettlementUsesTheRowInForceAndRecordsItsID(t *testing.T) {
	sp := &memSpend{}
	old := ModelPrice{ID: "old", Alias: "anthropic", Model: "claude-test-1", InMicroPerM: 1_000_000, OutMicroPerM: 1_000_000, Seq: 1}
	cur := ModelPrice{ID: "new", Alias: "anthropic", Model: "claude-test-1", InMicroPerM: 2_000_000, OutMicroPerM: 4_000_000, Seq: 2}
	p := &pricedProvider{reply: Reply{Text: "ok", TokensIn: 1_000_000, TokensOut: 1_000_000}}
	svc := meteredService(p, sp).WithMetering(sp, seqPrices{rows: []ModelPrice{old, cur}})

	if _, err := svc.complete(context.Background(), manager(), CompletionRequest{}); err != nil {
		t.Fatal(err)
	}
	set := sp.rows[len(sp.rows)-1]
	if set.PriceID != "new" || set.CostMicroCents != 6_000_000 {
		t.Fatalf("settlement = price %q cost %d, want the newer row and 6000000", set.PriceID, set.CostMicroCents)
	}
	// The earlier row is untouched: a past month stays billed at the price it was made under.
	if sp.rows[0].PriceID != "new" {
		t.Fatalf("reservation price = %q", sp.rows[0].PriceID)
	}
}

func TestAModelWithNoPriceRowIsNotCalled(t *testing.T) {
	sp := &memSpend{}
	p := &pricedProvider{reply: Reply{Text: "ok"}}
	svc := meteredService(p, sp).WithMetering(sp, memPrices{})

	_, err := svc.complete(context.Background(), manager(), CompletionRequest{})
	wantErrIs(t, err, ErrMeterUnavailable)
	if p.calls != 0 || len(sp.rows) != 0 {
		t.Fatalf("provider calls %d, rows %d, want a refusal before either", p.calls, len(sp.rows))
	}
}

func TestTheStubNeedsNoPriceAndCostsNothing(t *testing.T) {
	sp := &memSpend{}
	svc := NewService(NewRegistry(seededReaders().deps(), nil), NewStubProvider(), &fakeLedger{}, newFakeLimits(), StaticSettings{}, nil).
		WithMetering(sp, memPrices{})
	if _, err := svc.complete(context.Background(), manager(), CompletionRequest{Messages: []Message{{Role: RoleUser, Text: "hello"}}}); err != nil {
		t.Fatal(err)
	}
	for _, r := range sp.rows {
		if r.CostMicroCents != 0 || r.PriceID != "" {
			t.Fatalf("a stub row carries cost %d price %q, want none", r.CostMicroCents, r.PriceID)
		}
	}
}
