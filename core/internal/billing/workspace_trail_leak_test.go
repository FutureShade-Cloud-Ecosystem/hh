package billing

// workspace_trail_leak_test.go, FLAKE-10: the customer workspace's payment trail check reads the
// PARSED body for its FIGURES, never the raw body's digits.
//
// The check used to hunt the bare digit runs "50000", "7000" and "99123" over the whole body.
// The body is the workspace, and it carries an "as_of" clock (a time.Time marshalled as RFC 3339
// Nano) and the customer's uuid. A clock's fractional part is a NINE digit run, so a bare five
// digit run sits inside it as easily as a four digit one: measured over two million real stamps,
// a clock spells "41234" in one run in 9,479 and "7000" about as often. The raw hunt also read a
// uuid's hex digits as a leak, and it read the seeded credit limit "500000" as the payment's
// "50000", because a raw substring hunt cannot tell a figure from the tail of a larger one.
//
// The figures are read as PARSED values now: a figure is a NUMBER equal to the figure, or a whole
// TEXT value that reads as the figure. A timestamp is one whole string that does not parse as a
// number, so no clock can be read as a figure, and "500000" is not "50000".
//
// The word shaped needles stay byte scans, the way the repo's own remedy keeps a key scan a byte
// scan (internal/tasks/cost_gate_test.go): a clock carries no letters, so "PAY-1", "EFT" and
// "last_payment" cannot be answered by a clock or by another field's figures.

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// workspaceFiguresIn reads the payment figures a customer workspace body carries.
//
// The figure is read as a NUMBER, or as a whole TEXT value that reads as the figure. The text
// rule is anchored to the WHOLE value, which is what keeps a clock out: a timestamp is one whole
// string that does not parse as a number.
func workspaceFiguresIn(body string, figures ...int64) []string {
	var tree any
	if err := json.Unmarshal([]byte(body), &tree); err != nil {
		return []string{fmt.Sprintf("the body is not JSON: %v", err)}
	}
	var found []string
	return append(found, workspaceFiguresAt(tree, "", figures)...)
}

// workspaceFiguresAt walks a PARSED body and names the figures it carries, with the JSON path of
// the field that carries them.
func workspaceFiguresAt(v any, path string, figures []int64) []string {
	var found []string
	switch t := v.(type) {
	case map[string]any:
		for _, key := range sortedWorkspaceKeys(t) {
			found = append(found, workspaceFiguresAt(t[key], workspaceFieldPath(path, key), figures)...)
		}
	case []any:
		for i, item := range t {
			found = append(found, workspaceFiguresAt(item, fmt.Sprintf("%s[%d]", path, i), figures)...)
		}
	case float64:
		for _, figure := range figures {
			if t == float64(figure) {
				found = append(found, fmt.Sprintf("the field %s carries the figure %d", path, figure))
			}
		}
	case string:
		for _, figure := range figures {
			if n, ok := wholeWorkspaceFigure(t); ok && n == figure {
				found = append(found, fmt.Sprintf("the field %s carries the figure %d", path, figure))
			}
		}
	}
	return found
}

// wholeWorkspaceFigureText matches a string that is a whole number, with or without a decimal
// spelling of a whole figure. Nothing else: a timestamp, a uuid and a date are all rejected here.
var wholeWorkspaceFigureText = regexp.MustCompile(`^[+-]?\d+(\.\d+)?$`)

// wholeWorkspaceFigure reads a string that is a whole number.
func wholeWorkspaceFigure(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if !wholeWorkspaceFigureText.MatchString(s) {
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

// sortedWorkspaceKeys returns a map's keys in a fixed order, so a failure names its fields in the
// same order every run.
func sortedWorkspaceKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// workspaceFieldPath joins a JSON path.
func workspaceFieldPath(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

// TestWorkspaceFiguresIn_ReadsTheFiguresNotTheClock is the unit leg of the check itself.
//
// The first body carries no payment figure at all, but its clock spells one: the old raw hunt
// failed it, on the clock, which is the flake. The fixed check must pass it.
//
// The second body carries the figure, as a number and as text: the fixed check must fail it.
func TestWorkspaceFiguresIn_ReadsTheFiguresNotTheClock(t *testing.T) {
	clockOnly := `{"as_of":"2026-10-28T12:48:30.070000000Z","payments_withheld":true,` +
		`"header":{"customer_id":"c1b00000-0000-4000-a000-000000000099","credit_limit_cents":500000},` +
		`"credit_notes":[{"id":"c-1","memo_no":"CM-1","amount_cents":25000,"unapplied_cents":25000}]}`
	// The old check, a raw hunt for the bare digits over the whole body.
	if !strings.Contains(clockOnly, "7000") {
		t.Fatal("the crafted body must hold the digits the old raw check read as a figure")
	}
	if leaks := workspaceFiguresIn(clockOnly, 50000, 7000, 99123); len(leaks) != 0 {
		t.Fatalf("the check read the clock as a payment figure: %v", leaks)
	}

	withFigure := `{"as_of":"2026-10-28T12:48:30.077777777Z","payments_withheld":true,` +
		`"header":{"customer_id":"c1b00000-0000-4000-a000-000000000099","last_payment_cents":99123}}`
	if leaks := workspaceFiguresIn(withFigure, 50000, 7000, 99123); len(leaks) == 0 {
		t.Fatal("the check let a payment figure through")
	}

	asText := `{"as_of":"2026-10-28T12:48:30.077777777Z","payments_withheld":true,` +
		`"notes":[{"body":"7000"}]}`
	if leaks := workspaceFiguresIn(asText, 50000, 7000, 99123); len(leaks) == 0 {
		t.Fatal("the check let a payment figure through as text")
	}
}

// TestWorkspaceFiguresIn_IgnoresTheTailOfALargerFigure is the unit leg's other side: a figure that
// merely ENDS with the hunted one is not the figure.
//
// The raw hunt could not tell them apart: it read the seeded credit limit "500000" as the
// payment's "50000".
func TestWorkspaceFiguresIn_IgnoresTheTailOfALargerFigure(t *testing.T) {
	larger := `{"as_of":"2026-10-28T12:48:30.077777777Z","payments_withheld":true,` +
		`"header":{"credit_limit_cents":500000}}`
	if !strings.Contains(larger, "50000") {
		t.Fatal("the crafted body must hold the digits the old raw check read as the figure")
	}
	if leaks := workspaceFiguresIn(larger, 50000, 7000, 99123); len(leaks) != 0 {
		t.Fatalf("the check read the tail of a larger figure as the figure: %v", leaks)
	}
}
