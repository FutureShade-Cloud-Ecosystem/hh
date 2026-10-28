package pricing

// cost_doors_leak_test.go, FLAKE-10: the price change log's cost gate reads PARSED fields, never
// the raw body.
//
// The gate's promise is that a caller off the cost and margin list receives the retail row only:
// the cost row, and the cost figures it carries, do not reach them. The check used to be a raw
// substring hunt for the bare digits "7400" over the whole body, and the body carries a clock:
// ChangeLogRow.CreatedAt is a time.Time marshalled as RFC 3339 Nano
// (internal/pricing/batch_model.go), so the body reads
// "created_at":"2026-10-28T12:48:30.740012345Z" whenever the clock's nanoseconds spell the
// figure. Measured over two million real stamps, a clock spells "7400" in one run in 2,597, and
// a body with NO cost row at all still tripped the hunt.
//
// The check lives here, untagged, so the unit leg below runs in the untagged suite and the
// integration-free leg in cost_doors_test.go calls the same helper.

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// costDoorLeak reports the cost detail a price change log body carries.
//
// The rows are read as the handler's own type, so a cost row is a row whose field is the cost
// field, and a cost figure is a NUMBER equal to one of the figures or a whole TEXT value that
// reads as one. A timestamp's nanoseconds are one string that does not parse as a number, so no
// clock can be read as a figure.
//
// The repo holds a clock strip for the checks that had to keep a byte scan
// (internal/tasks/cost_gate_test.go: func stripTimestamps, which the yard fence's money half
// uses over a body whose created_at once collapsed to ".131234579Z"). This check does not need
// it: the parsed read takes the clock out of the haystack by construction, which is strictly
// stronger than stripping it, and it keeps the promise the raw hunt made, the cost figure does
// not reach a caller off the cost list, while also catching the figure written as text.
//
// The one thing the raw hunt caught that the parsed read does not is the figure embedded as a
// SUBSTRING of a longer free text value. That is a narrower risk than a clock collision and it
// is named in the report rather than left for a reader to find.
func costDoorLeak(body string, costField string, costFigures ...int64) []string {
	var tree any
	if err := json.Unmarshal([]byte(body), &tree); err != nil {
		return []string{fmt.Sprintf("the body is not a price change log: %v", err)}
	}
	var leaks []string
	rows, ok := tree.([]any)
	if !ok {
		return append(leaks, "the body is not a price change log")
	}
	for i, row := range rows {
		m, ok := row.(map[string]any)
		if !ok {
			continue
		}
		if field, _ := m["field"].(string); field == costField {
			leaks = append(leaks, fmt.Sprintf("the row %d carries a cost row", i))
		}
	}
	return append(leaks, costFiguresIn(tree, "", costFigures)...)
}

// costFiguresIn walks a PARSED body and names the cost figures it carries, with the JSON path of
// the field that carries them.
//
// The figure is read as a NUMBER, or as a whole TEXT value that reads as the figure. The text
// rule is anchored to the WHOLE value, which is what keeps a clock out: a timestamp is one whole
// string that does not parse as a number, while "7400" is.
func costFiguresIn(v any, path string, figures []int64) []string {
	var found []string
	switch t := v.(type) {
	case map[string]any:
		for _, key := range sortedCostKeys(t) {
			found = append(found, costFiguresIn(t[key], costPath(path, key), figures)...)
		}
	case []any:
		for i, item := range t {
			found = append(found, costFiguresIn(item, fmt.Sprintf("%s[%d]", path, i), figures)...)
		}
	case float64:
		for _, figure := range figures {
			if t == float64(figure) {
				found = append(found, fmt.Sprintf("the field %s carries the cost figure %d", path, figure))
			}
		}
	case string:
		for _, figure := range figures {
			if n, ok := wholeCostFigure(t); ok && n == figure {
				found = append(found, fmt.Sprintf("the field %s carries the cost figure %d", path, figure))
			}
		}
	}
	return found
}

// wholeCostFigureText matches a string that is a whole number, with or without a decimal spelling
// of a whole figure. Nothing else: a timestamp, a uuid and a date are all rejected here.
var wholeCostFigureText = regexp.MustCompile(`^[+-]?\d+(\.\d+)?$`)

// wholeCostFigure reads a string that is a whole number.
func wholeCostFigure(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if !wholeCostFigureText.MatchString(s) {
		return 0, false
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n, true
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return int64(f), true
}

// sortedCostKeys returns a map's keys in a fixed order, so a failure names its fields in the same
// order every run.
func sortedCostKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// costPath joins a JSON path.
func costPath(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

// TestCostDoorLeak_ReadsTheFieldNotTheClock is the unit leg of the check itself.
//
// The first body carries NO cost row and a clock whose nanoseconds spell the cost figure. The old
// raw hunt failed it, on the clock, which is the flake. The fixed check must pass it.
//
// The second body carries a real cost row: the fixed check must fail it.
func TestCostDoorLeak_ReadsTheFieldNotTheClock(t *testing.T) {
	clockOnly := `[{"id":"l2","product_id":"a7000000-0000-4000-9000-000000000001",` +
		`"field":"retail_cents","before_cents":10000,"after_cents":10500,` +
		`"created_at":"2026-10-28T12:48:30.740012345Z"}]`
	// The old check, a raw hunt for the bare digits over the whole body.
	if !strings.Contains(clockOnly, "7400") {
		t.Fatal("the crafted body must hold the digits the old raw check read as the cost figure")
	}
	if leaks := costDoorLeak(clockOnly, "cost_cents", 7000, 7400); len(leaks) != 0 {
		t.Fatalf("the check read the clock as a cost figure: %v", leaks)
	}

	withCost := `[{"id":"l1","product_id":"a7000000-0000-4000-9000-000000000001",` +
		`"field":"cost_cents","before_cents":7000,"after_cents":7400,` +
		`"created_at":"2026-10-28T12:48:30.077777777Z"}]`
	if leaks := costDoorLeak(withCost, "cost_cents", 7000, 7400); len(leaks) == 0 {
		t.Fatal("the check let a cost row through")
	}
}

// TestCostDoorLeak_CatchesTheCostFigureAsText is the round 2 leg: the figure written as TEXT.
//
// The raw hunt caught it, because the digits were in the body. A parsed read must catch it too,
// anchored to the whole value, so a clock's own digits can never be read as the figure. The body
// is read as a generic tree, not as the handler's typed row, so a figure written as text cannot
// fail the parse and be reported as a parse fault: it is a leak, and it is reported as one.
func TestCostDoorLeak_CatchesTheCostFigureAsText(t *testing.T) {
	asText := `[{"id":"l1","product_id":"a7000000-0000-4000-9000-000000000001",` +
		`"field":"retail_cents","before_cents":10000,"after_cents":"7400",` +
		`"created_at":"2026-10-28T12:48:30.077777777Z"}]`
	leaks := costDoorLeak(asText, "cost_cents", 7000, 7400)
	if len(leaks) == 0 {
		t.Fatal("the check let the cost figure through as text")
	}
	if !strings.Contains(strings.Join(leaks, " "), "7400") {
		t.Fatalf("the leak must name the figure: %v", leaks)
	}
	for _, leak := range leaks {
		if strings.Contains(leak, "cannot unmarshal") {
			t.Fatalf("the failure must name the leak, not the JSON parser: %q", leak)
		}
	}
}
