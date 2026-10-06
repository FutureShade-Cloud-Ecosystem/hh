package apigen

// nullablefields_test.go holds the generator's required-nullable contract
// at the type level, one test per changed field family (HH-LOAD-1's
// generator split, the principal's ruling): a property the document marks
// required and nullable keeps its key in every payload, answering null
// when unset, because the emitted Go field is a pointer whose json tag
// carries no omitempty. The families are the changed fields grouped by
// shape: integer quantities and positions, integer money, float
// measures and factors, text, and a map. The id-key family (a string key
// resolving to a row) is the load frame's own and its test lives beside
// that module.

import (
	"encoding/json"
	"strings"
	"testing"
)

// nilJSON marshals the zero value of one generated type and returns the
// payload: every required-nullable key must survive as an explicit null.
func nilJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal %T: %v", v, err)
	}
	return string(raw)
}

func wantKey(t *testing.T, payload, key string) {
	t.Helper()
	if !strings.Contains(payload, `"`+key+`":null`) {
		t.Fatalf("the required-nullable key %s is not in the payload as null: %s", key, payload)
	}
}

func TestNullableIntegerQuantitiesStayPresent(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload string
		key     string
	}{
		{"product full skid quantity", nilJSON(t, Product{}), "full_skid_quantity"},
		{"product layer quantity", nilJSON(t, Product{}), "layer_quantity"},
		{"product pallet count", nilJSON(t, Product{}), "pallet_count"},
		{"dispatch schedule pallet positions", nilJSON(t, DispatchScheduleInput{}), "pallet_positions"},
		{"task shortage order line", nilJSON(t, TaskShortageRequest{}), "order_line_no"},
		{"warranty override term", nilJSON(t, WarrantyProductOverrideInput{}), "term_months"},
		{"warranty vendor profile term", nilJSON(t, WarrantyVendorProfileInput{}), "term_months"},
	} {
		wantKey(t, tc.payload, tc.key)
	}
}

func TestNullableIntegerMoneyStaysPresent(t *testing.T) {
	wantKey(t, nilJSON(t, WarrantySettlementInput{}), "expected_cents")
}

func TestNullableFloatMeasuresStayPresent(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload string
		key     string
	}{
		{"coverage sell uom factor", nilJSON(t, ProductCoverageBasis{}), "sell_uom_factor"},
		{"coverage per unit", nilJSON(t, ProductCoverageBasis{}), "coverage_sf_per_unit"},
		{"coverage pallet factor", nilJSON(t, ProductCoverageBasis{}), "pallet_factor"},
		{"coverage density", nilJSON(t, ProductCoverageBasis{}), "density_t_per_cyd"},
		{"schedule latitude", nilJSON(t, DispatchScheduleInput{}), "latitude"},
		{"schedule longitude", nilJSON(t, DispatchScheduleInput{}), "longitude"},
		{"schedule volume", nilJSON(t, DispatchScheduleInput{}), "volume_m3"},
		{"schedule weight", nilJSON(t, DispatchScheduleInput{}), "weight_kg"},
		{"count line counted quantity", nilJSON(t, InvCountLine{}), "counted_qty_base"},
	} {
		wantKey(t, tc.payload, tc.key)
	}
}

func TestNullableTextStaysPresent(t *testing.T) {
	wantKey(t, nilJSON(t, Product{}), "minimum_order_qty")
}

func TestNullableMapStaysPresent(t *testing.T) {
	wantKey(t, nilJSON(t, Product{}), "metadata")
}
