package main

// branch_fences_rules_list_test.go, FLAKE-10: the pricing rules list is read as PARSED rules.
//
// The list used to be hunted for the bare digits "4242" over the whole body. The body is the rule
// list, and a rule carries its id, its customer, its product and its branch, each a
// gen_random_uuid(), so the hunt could be answered by an id's hex digits: a uuid spells "4242" in
// about one run in 2,260, and the list carries several.
//
// The check lives here, untagged, so the unit leg below runs in the untagged suite and the
// integration leg calls the same helper.

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// listedPricingRule is the shape of a pricing rule as the list returns it: the fields the door
// proof reads.
type listedPricingRule struct {
	ID         string `json:"id"`
	PriceCents *int64 `json:"price_cents"`
}

// parsePricingRuleList reads a pricing rules list as PARSED rules.
func parsePricingRuleList(body []byte) ([]listedPricingRule, error) {
	var listed []listedPricingRule
	if err := json.Unmarshal(body, &listed); err != nil {
		return nil, fmt.Errorf("the body must be the rule list: %w", err)
	}
	return listed, nil
}

// TestParsePricingRuleList_ReadsThePriceNotAnId is the unit leg of the check.
//
// The crafted body carries no rule at the price, and a uuid whose hex digits spell it. The old
// raw hunt failed it, on the id, which is the flake. The fixed check must pass it.
//
// The second body carries a rule at the price: the fixed check must fail it.
func TestParsePricingRuleList_ReadsThePriceNotAnId(t *testing.T) {
	carryingID := []byte(`[{"id":"a4242b00-0000-4000-9000-000000000001","scope":"CUSTOMER_PRODUCT",` +
		`"calc_type":"ABSOLUTE","price_cents":5000,"price_uom":"EA"}]`)
	// The old check, a raw hunt for the bare digits over the whole body.
	if !strings.Contains(string(carryingID), "4242") {
		t.Fatal("the crafted body must hold the digits the old raw check read as the price")
	}
	listed, err := parsePricingRuleList(carryingID)
	if err != nil {
		t.Fatalf("the body must parse: %v", err)
	}
	for _, rl := range listed {
		if rl.PriceCents != nil && *rl.PriceCents == 4242 {
			t.Fatalf("the check read an id's digits as the price: %+v", listed)
		}
	}

	carryingPrice := []byte(`[{"id":"b0000000-0000-4000-9000-000000000002","scope":"CUSTOMER_PRODUCT",` +
		`"calc_type":"ABSOLUTE","price_cents":4242,"price_uom":"EA"}]`)
	listed, err = parsePricingRuleList(carryingPrice)
	if err != nil {
		t.Fatalf("the body must parse: %v", err)
	}
	found := false
	for _, rl := range listed {
		if rl.PriceCents != nil && *rl.PriceCents == 4242 {
			found = true
		}
	}
	if !found {
		t.Fatal("the check let a rule at the price pass")
	}
}
