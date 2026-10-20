package dispatch

// driver_moneyscan_helper_test.go holds the shared JSON walk and money/bin fence
// helpers used by the driver stop sync test and the driver MyRuns money scan.
// Walking the decoded JSON structure (maps, slices, scalar values) rather than
// running strings.Contains over the raw JSON bytes ensures random UUIDs
// holding "a-12" or fractional-second timestamps holding "9500" never fail by
// chance while keeping the fence's teeth on every key, whole string value,
// whole number value and JSON null.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// driverForbiddenKeyWords are the money and bin words that must never appear
// (case folded) inside any JSON key of a driver payload.
var driverForbiddenKeyWords = []string{
	"cents",
	"price",
	"cost",
	"margin",
	"amount",
	"value",
	"location",
}

// driverForbiddenWholeValues are the fixture bin locations and money figures
// that must never appear as a whole string value or whole number value anywhere
// in a driver payload.
var driverForbiddenWholeValues = []string{
	"A-12",
	"YARD-3",
	"353125",
	"9500",
}

// decodedJSONWalk holds every object key, string scalar, number scalar and
// JSON null encountered while walking a decoded JSON payload.
type decodedJSONWalk struct {
	Keys    []string
	Strings []string
	Numbers []string
	Nulls   int
}

// walkDecodedJSON decodes raw JSON bytes with UseNumber enabled and walks every
// nested map, slice and value, collecting all keys, string values, number
// values and JSON nulls.
func walkDecodedJSON(raw []byte) (decodedJSONWalk, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var root any
	if err := dec.Decode(&root); err != nil {
		return decodedJSONWalk{}, err
	}
	var w decodedJSONWalk
	w.walk(root)
	return w, nil
}

func (w *decodedJSONWalk) walk(v any) {
	switch x := v.(type) {
	case nil:
		w.Nulls++
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			w.Keys = append(w.Keys, k)
			w.walk(x[k])
		}
	case []any:
		for _, elem := range x {
			w.walk(elem)
		}
	case string:
		w.Strings = append(w.Strings, x)
	case json.Number:
		w.Numbers = append(w.Numbers, x.String())
	case float64:
		w.Numbers = append(w.Numbers, strconv.FormatFloat(x, 'f', -1, 64))
	case int:
		w.Numbers = append(w.Numbers, strconv.Itoa(x))
	case int64:
		w.Numbers = append(w.Numbers, strconv.FormatInt(x, 10))
	}
}

// scanDriverPayloadJSON walks the decoded JSON in raw and returns every
// violation of the driver money and bin scan rules:
//  1. Any JSON key containing a forbidden word from driverForbiddenKeyWords
//     (case folded). String values are not scanned for words.
//  2. Any string value carrying a planted value from driverForbiddenWholeValues
//     (or any additional values passed by the caller) case insensitively as a
//     token bounded by a non alphanumeric character or the string's edge, or
//     matching numerically after trimming whitespace, and any number value
//     equal to a planted value.
//  3. Any JSON null value in the decoded walk.
func scanDriverPayloadJSON(raw []byte, extraWholeValues ...string) []string {
	w, err := walkDecodedJSON(raw)
	if err != nil {
		return []string{fmt.Sprintf("decode driver payload json: %v", err)}
	}
	var problems []string
	for _, k := range w.Keys {
		lowerKey := strings.ToLower(k)
		for _, word := range driverForbiddenKeyWords {
			if strings.Contains(lowerKey, strings.ToLower(word)) {
				problems = append(problems, fmt.Sprintf("JSON key %q contains forbidden word %q", k, word))
			}
		}
	}
	wholeList := append([]string{}, driverForbiddenWholeValues...)
	wholeList = append(wholeList, extraWholeValues...)
	for _, planted := range wholeList {
		re := regexp.MustCompile(`(?i)(^|[^a-z0-9])` + regexp.QuoteMeta(planted) + `($|[^a-z0-9])`)
		for _, s := range w.Strings {
			if re.MatchString(s) || numberEqualsPlanted(strings.TrimSpace(s), planted) {
				problems = append(problems, fmt.Sprintf("string value %q matches forbidden value %q", s, planted))
			}
		}
		for _, n := range w.Numbers {
			if numberEqualsPlanted(n, planted) {
				problems = append(problems, fmt.Sprintf("whole number value %s equals forbidden value %q", n, planted))
			}
		}
	}
	if w.Nulls > 0 {
		problems = append(problems, fmt.Sprintf("decoded JSON carries %d null value(s)", w.Nulls))
	}
	return problems
}

func numberEqualsPlanted(numToken, planted string) bool {
	if numToken == planted {
		return true
	}
	nf, err1 := strconv.ParseFloat(numToken, 64)
	pf, err2 := strconv.ParseFloat(planted, 64)
	return err1 == nil && err2 == nil && nf == pf
}

// assertDriverPayloadJSON fails t if raw violates any rule in
// scanDriverPayloadJSON.
func assertDriverPayloadJSON(t testing.TB, raw []byte, extraWholeValues ...string) {
	t.Helper()
	if problems := scanDriverPayloadJSON(raw, extraWholeValues...); len(problems) > 0 {
		t.Fatalf("driver payload failed JSON scan (%s): %s", strings.Join(problems, "; "), raw)
	}
}

var freightRaiseForbiddenKeys = []string{
	"money",
	"quoted_freight_cents",
	"proposed_freight_cents",
	"delta_cents",
}

var freightRaiseForbiddenWholeValues = []string{
	"26000",
	"34000",
	"8000",
}

var union15ForbiddenKeys = []string{
	"value_cents",
	"freight_cents",
}

var union15ForbiddenWholeValues = []string{
	"353125",
	"9500",
}

// findForbiddenDecodedJSON walks the decoded JSON in raw and returns every
// match of forbiddenKeys (as an object key), forbiddenWholeValues (as a whole
// number or whole string value), and (when forbidNull is true) "null" for any
// JSON null value.
func findForbiddenDecodedJSON(raw []byte, forbiddenKeys, forbiddenWholeValues []string, forbidNull bool) ([]string, error) {
	w, err := walkDecodedJSON(raw)
	if err != nil {
		return nil, err
	}
	var found []string
	for _, forbidden := range forbiddenKeys {
		for _, k := range w.Keys {
			if strings.EqualFold(k, forbidden) {
				found = append(found, k)
			}
		}
	}
	for _, forbidden := range forbiddenWholeValues {
		for _, n := range w.Numbers {
			if numberEqualsPlanted(n, forbidden) {
				found = append(found, forbidden)
			}
		}
		for _, s := range w.Strings {
			if s == forbidden || numberEqualsPlanted(strings.TrimSpace(s), forbidden) {
				found = append(found, forbidden)
			}
		}
	}
	if forbidNull && w.Nulls > 0 {
		found = append(found, "null")
	}
	return found, nil
}

// assertFreightRaiseDriverCardJSON walks the decoded JSON of a freight raise
// card and fails t if it carries any forbidden money key, any forbidden whole
// number or whole string figure, or any JSON null value.
func assertFreightRaiseDriverCardJSON(t testing.TB, drvJSON []byte) {
	t.Helper()
	found, err := findForbiddenDecodedJSON(drvJSON, freightRaiseForbiddenKeys, freightRaiseForbiddenWholeValues, true)
	if err != nil {
		t.Fatalf("decode driver's card JSON: %v, %s", err, drvJSON)
		return
	}
	for _, forbidden := range found {
		t.Fatalf("a driver's card carries %q, %s", forbidden, drvJSON)
	}
}

// assertUnion15DriverBoardJSON walks the decoded JSON of a driver's load board
// and fails t if it carries any forbidden money key or forbidden whole number
// or whole string figure.
func assertUnion15DriverBoardJSON(t testing.TB, drvJSON []byte) {
	t.Helper()
	found, err := findForbiddenDecodedJSON(drvJSON, union15ForbiddenKeys, union15ForbiddenWholeValues, false)
	if err != nil {
		t.Fatalf("decode driver's load board JSON: %v, %s", err, drvJSON)
		return
	}
	for _, forbidden := range found {
		t.Fatalf("a driver's load board carries %q. The gate is DEFINED correctly and the struct tags "+
			"are correct; what this asserts is that the one line joining them is still there. Got %s",
			forbidden, drvJSON)
	}
}

type spyTB struct {
	testing.TB
	failed bool
	msgs   []string
}

func (s *spyTB) Helper() {}

func (s *spyTB) Fatalf(format string, args ...any) {
	s.failed = true
	s.msgs = append(s.msgs, fmt.Sprintf(format, args...))
}

func (s *spyTB) Errorf(format string, args ...any) {
	s.failed = true
	s.msgs = append(s.msgs, fmt.Sprintf(format, args...))
}

func TestWalkDecodedJSON_CollectsKeysStringsNumbersAndNulls(t *testing.T) {
	sample := []byte(`{
		"id": "stop-1",
		"stop_no": 1,
		"weight_kg": 1240.5,
		"notes": null,
		"lines": [
			{"line_no": 1, "sku": "LL-PAV-601", "qty": "48.000"},
			null
		]
	}`)
	w, err := walkDecodedJSON(sample)
	if err != nil {
		t.Fatalf("walkDecodedJSON: %v", err)
	}
	wantKeys := []string{"id", "lines", "line_no", "qty", "sku", "notes", "stop_no", "weight_kg"}
	if !reflect.DeepEqual(w.Keys, wantKeys) {
		t.Fatalf("collected keys %v, want %v", w.Keys, wantKeys)
	}
	wantStrings := []string{"stop-1", "48.000", "LL-PAV-601"}
	if !reflect.DeepEqual(w.Strings, wantStrings) {
		t.Fatalf("collected strings %v, want %v", w.Strings, wantStrings)
	}
	wantNumbers := []string{"1", "1", "1240.5"}
	if !reflect.DeepEqual(w.Numbers, wantNumbers) {
		t.Fatalf("collected numbers %v, want %v", w.Numbers, wantNumbers)
	}
	if w.Nulls != 2 {
		t.Fatalf("collected %d nulls, want 2", w.Nulls)
	}
}

func TestAssertDriverPayloadJSON_Teeth(t *testing.T) {
	mustFailCases := []struct {
		name string
		body string
	}{
		{
			name: "planted_forbidden_key_price_cents",
			body: `{"id":"stop-1","price_cents":100}`,
		},
		{
			name: "planted_forbidden_key_case_folded_Value",
			body: `{"id":"stop-1","LoadValue":100}`,
		},
		{
			name: "planted_forbidden_key_cost",
			body: `{"id":"stop-1","unit_cost":100}`,
		},
		{
			name: "planted_forbidden_key_margin",
			body: `{"id":"stop-1","gross_margin":100}`,
		},
		{
			name: "planted_forbidden_key_amount",
			body: `{"id":"stop-1","total_amount":100}`,
		},
		{
			name: "planted_forbidden_key_location",
			body: `{"id":"stop-1","lines":[{"sku":"LL-PAV-601","location":"B-01"}]}`,
		},
		{
			name: "planted_whole_bin_value_A_12",
			body: `{"id":"stop-1","lines":[{"sku":"LL-PAV-601","bin":"A-12"}]}`,
		},
		{
			name: "planted_token_bin_value_Bay_A_12",
			body: `{"id":"stop-1","lines":[{"sku":"LL-PAV-601","bin":"Bay A-12"}]}`,
		},
		{
			name: "planted_padded_bin_value_space_A_12",
			body: `{"id":"stop-1","lines":[{"sku":"LL-PAV-601","bin":" A-12"}]}`,
		},
		{
			name: "planted_lowercase_bin_value_a_12",
			body: `{"id":"stop-1","lines":[{"sku":"LL-PAV-601","bin":"a-12"}]}`,
		},
		{
			name: "planted_whole_bin_value_YARD_3",
			body: `{"id":"stop-1","lines":[{"sku":"LL-AGG-602","bin":"YARD-3"}]}`,
		},
		{
			name: "planted_whole_money_number_353125",
			body: `{"id":"stop-1","load_figure":353125}`,
		},
		{
			name: "planted_whole_money_string_353125",
			body: `{"id":"stop-1","load_figure":"353125"}`,
		},
		{
			name: "planted_decimal_money_string_353125_00",
			body: `{"id":"stop-1","load_figure":"353125.00"}`,
		},
		{
			name: "planted_whole_money_number_9500",
			body: `{"id":"stop-1","freight_figure":9500}`,
		},
		{
			name: "planted_whole_money_string_9500",
			body: `{"id":"stop-1","freight_figure":"9500"}`,
		},
		{
			name: "planted_json_null_value",
			body: `{"id":"stop-1","reopen_reason":null}`,
		},
	}

	for _, tc := range mustFailCases {
		t.Run(tc.name, func(t *testing.T) {
			spy := &spyTB{TB: t}
			assertDriverPayloadJSON(spy, []byte(tc.body))
			if !spy.failed {
				t.Fatalf("expected assertDriverPayloadJSON to fail on %s (%s), but it passed", tc.name, tc.body)
			}
			t.Logf("caught planted violation %s: %s", tc.name, spy.msgs[0])
		})
	}

	cleanPayload, err := json.Marshal(map[string]any{
		"id":           "stop-1",
		"order_id":     "2f65945a-12bf-4d30-9f1c-000000000001",
		"created_at":   "T08:15:30.595008Z",
		"instructions": "Drop at side gate; customer values neat stacking at this location, price tag removed, nullified old ticket",
		"lines": []map[string]any{
			{
				"line_no":     1,
				"sku":         "LL-PAV-601",
				"description": "Brown pavers 60mm",
				"qty":         "48.000",
				"uom":         "EA",
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal clean payload: %v", err)
	}

	t.Run("random_uuid_a_12_and_timestamp_9500_do_not_fail", func(t *testing.T) {
		spy := &spyTB{TB: t}
		assertDriverPayloadJSON(spy, cleanPayload)
		if spy.failed {
			t.Fatalf("expected clean payload with order_id holding a-12 and timestamp holding 9500 to pass, got failure: %v", spy.msgs)
		}
	})
}

func TestFreightRaise_DriverCardScanTeeth(t *testing.T) {
	cleanCard := []byte(`{
		"id": "0195f3a2-8000-7b12-9c34-000000000001",
		"order_id": "0195f3a2-1111-7b12-9c34-000000000002",
		"order_no": "#SO-KGN-1041",
		"customer_name": "Bay of Quinte Landscaping",
		"branch_id": "b-kgn",
		"loads": 2,
		"asked_by": "a dispatch manager",
		"asked_at": "2026-08-31T09:00:00Z",
		"state": "PARKED",
		"approval_id": "0195f3a2-2222-7b12-9c34-000000000003"
	}`)
	t.Run("card_with_id_holding_8000_passes", func(t *testing.T) {
		spy := &spyTB{TB: t}
		assertFreightRaiseDriverCardJSON(spy, cleanCard)
		if spy.failed {
			t.Fatalf("expected card with id holding 8000 to pass, got failure: %v", spy.msgs)
		}
	})

	mustFailCases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "carries_delta_cents_key",
			body: `{"id":"0195f3a2-8000-7b12-9c34-000000000001","delta_cents":0}`,
			want: "delta_cents",
		},
		{
			name: "carries_money_key",
			body: `{"id":"0195f3a2-8000-7b12-9c34-000000000001","money":{}}`,
			want: "money",
		},
		{
			name: "carries_quoted_freight_cents_key",
			body: `{"id":"0195f3a2-8000-7b12-9c34-000000000001","quoted_freight_cents":0}`,
			want: "quoted_freight_cents",
		},
		{
			name: "carries_proposed_freight_cents_key",
			body: `{"id":"0195f3a2-8000-7b12-9c34-000000000001","proposed_freight_cents":0}`,
			want: "proposed_freight_cents",
		},
		{
			name: "carries_whole_8000_number",
			body: `{"id":"0195f3a2-8000-7b12-9c34-000000000001","loads":8000}`,
			want: "8000",
		},
		{
			name: "carries_whole_8000_string",
			body: `{"id":"0195f3a2-8000-7b12-9c34-000000000001","state":"8000"}`,
			want: "8000",
		},
		{
			name: "carries_whole_26000_number",
			body: `{"id":"0195f3a2-8000-7b12-9c34-000000000001","loads":26000}`,
			want: "26000",
		},
		{
			name: "carries_whole_26000_string",
			body: `{"id":"0195f3a2-8000-7b12-9c34-000000000001","state":"26000"}`,
			want: "26000",
		},
		{
			name: "carries_whole_34000_number",
			body: `{"id":"0195f3a2-8000-7b12-9c34-000000000001","loads":34000}`,
			want: "34000",
		},
		{
			name: "carries_whole_34000_string",
			body: `{"id":"0195f3a2-8000-7b12-9c34-000000000001","state":"34000"}`,
			want: "34000",
		},
		{
			name: "carries_json_null",
			body: `{"id":"0195f3a2-8000-7b12-9c34-000000000001","order_no":null}`,
			want: "null",
		},
	}

	for _, tc := range mustFailCases {
		t.Run(tc.name, func(t *testing.T) {
			spy := &spyTB{TB: t}
			assertFreightRaiseDriverCardJSON(spy, []byte(tc.body))
			if !spy.failed {
				t.Fatalf("expected assertFreightRaiseDriverCardJSON to fail on %s (%s), but it passed", tc.name, tc.body)
			}
			if !strings.Contains(spy.msgs[0], tc.want) {
				t.Fatalf("expected failure message %q to name %q", spy.msgs[0], tc.want)
			}
		})
	}
}

func TestUnion15_DriverBoardScanTeeth(t *testing.T) {
	cleanBoard := []byte(`{
		"branch_id": "5d1b3f6e-0000-4000-9000-000000000001",
		"date": "2026-10-20",
		"value_visible": false,
		"unassigned": [
			{
				"id": "0195f3a2-9500-7b12-9c34-353125000001",
				"order_id": "0195f3a2-1111-7b12-9500-000000000002",
				"status": "READY"
			}
		]
	}`)
	t.Run("board_with_ids_holding_9500_and_353125_passes", func(t *testing.T) {
		spy := &spyTB{TB: t}
		assertUnion15DriverBoardJSON(spy, cleanBoard)
		if spy.failed {
			t.Fatalf("expected board with ids holding 9500 and 353125 to pass, got failure: %v", spy.msgs)
		}
	})

	mustFailCases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "carries_value_cents_key",
			body: `{"value_visible":false,"unassigned":[{"id":"s-1","value_cents":0}]}`,
			want: "value_cents",
		},
		{
			name: "carries_freight_cents_key",
			body: `{"value_visible":false,"unassigned":[{"id":"s-1","freight_cents":0}]}`,
			want: "freight_cents",
		},
		{
			name: "carries_whole_353125_number",
			body: `{"value_visible":false,"unassigned":[{"id":"s-1","stop_no":353125}]}`,
			want: "353125",
		},
		{
			name: "carries_whole_353125_string",
			body: `{"value_visible":false,"unassigned":[{"id":"s-1","notes":"353125"}]}`,
			want: "353125",
		},
		{
			name: "carries_whole_9500_number",
			body: `{"value_visible":false,"unassigned":[{"id":"s-1","stop_no":9500}]}`,
			want: "9500",
		},
		{
			name: "carries_whole_9500_string",
			body: `{"value_visible":false,"unassigned":[{"id":"s-1","notes":"9500"}]}`,
			want: "9500",
		},
	}

	for _, tc := range mustFailCases {
		t.Run(tc.name, func(t *testing.T) {
			spy := &spyTB{TB: t}
			assertUnion15DriverBoardJSON(spy, []byte(tc.body))
			if !spy.failed {
				t.Fatalf("expected assertUnion15DriverBoardJSON to fail on %s (%s), but it passed", tc.name, tc.body)
			}
			if !strings.Contains(spy.msgs[0], tc.want) {
				t.Fatalf("expected failure message %q to name %q", spy.msgs[0], tc.want)
			}
		})
	}
}
