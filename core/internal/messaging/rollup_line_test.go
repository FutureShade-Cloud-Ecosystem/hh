// rollup_line_test.go, FLAKE-10: the day's line is read as FIELDS, never as a raw substring. The
// line carries the day as well as the count and the money ("Payments 2026-08-11: 3 · $6,041.25"),
// and the day's digits are not the count: a check that looked for the bare digit "3" passed on a
// day whose date held a 3, and a check for "2" passed on every day of every year, because the
// year starts with a 2. The same class as the workspace's payment trail check, which read a
// timestamp's nanoseconds as an amount.
package messaging

import (
	"regexp"
	"strconv"
	"testing"
)

// rollupLineShape is the day's line: the day, the count and the money, in that order. The count
// is read as the count field, so no other field's digits can stand in for it.
var rollupLineShape = regexp.MustCompile(`^Payments (\d{4}-\d{2}-\d{2}): (\d+) · (\$[\d,]+\.\d{2})$`)

// rollupLine reads the day's line as its fields: the day, the count and the money.
func rollupLine(t *testing.T, body string) (day string, count int, money string) {
	t.Helper()
	m := rollupLineShape.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("the day's line must read 'Payments <day>: <count> · <money>', got %q", body)
	}
	n, err := strconv.Atoi(m[2])
	if err != nil {
		t.Fatalf("the day's line must state a count: %q", body)
	}
	return m[1], n, m[3]
}
