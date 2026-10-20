package assist

// price_row_test.go, TanyAI slice 3 sub item 6: the demo boot refuses, in plain words, when the
// model it would call has no price row. The row itself is an operator step (the demo runbook),
// never a seed.

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type failingPrices struct{}

func (failingPrices) PriceFor(context.Context, string, string) (ModelPrice, bool, error) {
	return ModelPrice{}, false, errors.New("connection refused")
}

func TestRequirePriceRow(t *testing.T) {
	ctx := context.Background()
	rows := StaticAliases(DefaultAliases())
	have := memPrices{AliasPremium + "/claude-sonnet-5-5": {ID: "pp", Alias: AliasPremium, Model: "claude-sonnet-5-5", InMicroPerM: 1, OutMicroPerM: 1, Seq: 1}}

	if err := RequirePriceRow(ctx, rows, have, AliasPremium); err != nil {
		t.Fatalf("a priced model refused: %v", err)
	}

	err := RequirePriceRow(ctx, rows, memPrices{}, AliasPremium)
	if err == nil {
		t.Fatal("a model with no price row was accepted")
	}
	for _, want := range []string{"tanyai-premium", "claude-sonnet-5-5", "assist_model_prices", "demo-copies.md"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal %q does not say %q", err, want)
		}
	}

	// A price row for another model, or another alias, is no row for this one.
	other := memPrices{AliasPremium + "/some-other-model": {ID: "x"}, AliasMain + "/claude-sonnet-5-5": {ID: "y"}}
	if err := RequirePriceRow(ctx, rows, other, AliasPremium); err == nil {
		t.Fatal("a price row for another model satisfied the check")
	}

	// An alias that is disabled, unknown or has no model cannot be priced: refuse, plainly.
	for name, tbl := range map[string]StaticAliases{
		"disabled": {{Alias: AliasPremium, AdapterKind: AdapterAnthropic, Model: "claude-sonnet-5-5", Enabled: false}},
		"absent":   {},
		"no model": {{Alias: AliasPremium, AdapterKind: AdapterAnthropic, Enabled: true}},
		"stub":     {{Alias: AliasPremium, AdapterKind: AdapterStub, Model: "hos-stub-1", Enabled: true}},
	} {
		if err := RequirePriceRow(ctx, tbl, have, AliasPremium); err == nil || !strings.Contains(err.Error(), "tanyai-premium") {
			t.Fatalf("%s alias: err = %v, want a refusal naming the alias", name, err)
		}
	}

	// A store that cannot answer is a refusal, not a pass.
	if err := RequirePriceRow(ctx, rows, failingPrices{}, AliasPremium); err == nil {
		t.Fatal("an unreadable price table was accepted")
	}
}
