package reporting

// catalog_test.go, the invariants that make "no dynamic SQL from user input, EVER" a checked claim
// rather than a comment. These run with no database: they are properties of the compiled catalog.

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// placeholderRe finds $1, $2 … in a query.
var placeholderRe = regexp.MustCompile(`\$(\d+)`)

// maxPlaceholder returns the highest $n in a query (0 when there are none).
func maxPlaceholder(sql string) int {
	max := 0
	for _, m := range placeholderRe.FindAllStringSubmatch(sql, -1) {
		n, _ := strconv.Atoi(m[1])
		if n > max {
			max = n
		}
	}
	return max
}

// TestCatalog_SQLHasNoFormatVerbsOrConcatenation is the core safety property: a catalog query cannot be
// a format string, because if it ever became one the runner's fmt-free call site would be the only
// thing standing between a caller and arbitrary SQL, and "the call site is currently correct" is not
// a property, it is a state.
func TestCatalog_SQLHasNoFormatVerbsOrConcatenation(t *testing.T) {
	t.Parallel()
	// Verbs that would make a query usable with fmt.Sprintf.
	banned := []string{"%s", "%d", "%v", "%q", "%w", "%+v", "%#v"}
	for code, entry := range catalog {
		for _, b := range banned {
			if strings.Contains(entry.sql, b) {
				t.Errorf("template %s: SQL contains format verb %q, this query must never be formattable", code, b)
			}
		}
		// One statement per template. A semicolon would admit a second statement into the same string.
		if strings.Contains(entry.sql, ";") {
			t.Errorf("template %s: SQL contains a semicolon, one statement per template", code)
		}
		// A placeholder inside quotes is a placeholder that does not bind; it is literal text that only
		// LOOKS parameterised, which is exactly the mistake this test exists to catch.
		if n, ok := placeholderInsideQuotes(entry.sql); ok {
			t.Errorf("template %s: $%d appears inside a quoted literal, it would not bind", code, n)
		}
	}
}

// placeholderInsideQuotes scans for a $n that falls between an odd and an even single quote. It walks
// the string rather than using a regex because quote PARITY is what matters and a regex that tries to
// express parity ends up matching from one string's closing quote to the next string's opening one -
// which is exactly the false positive this function was written to replace.
func placeholderInsideQuotes(sql string) (int, bool) {
	inQuote := false
	for i := 0; i < len(sql); i++ {
		switch sql[i] {
		case '\'':
			inQuote = !inQuote
		case '$':
			if inQuote && i+1 < len(sql) && sql[i+1] >= '0' && sql[i+1] <= '9' {
				n, _ := strconv.Atoi(string(sql[i+1]))
				return n, true
			}
		}
	}
	return 0, false
}

// TestCatalog_PlaceholdersAreContiguousFromOne pins that a query's binds are $1..$n with no gaps. A gap
// means the Go arg slice and the SQL disagree about which value is which, and the failure mode is a
// report that silently filters on the wrong column.
func TestCatalog_PlaceholdersAreContiguousFromOne(t *testing.T) {
	t.Parallel()
	for code, entry := range catalog {
		max := maxPlaceholder(entry.sql)
		for n := 1; n <= max; n++ {
			if !strings.Contains(entry.sql, "$"+strconv.Itoa(n)) {
				t.Errorf("template %s: SQL uses $%d as its highest placeholder but never binds $%d", code, max, n)
			}
		}
	}
}

// TestCatalog_ColumnsAreDeclaredAndWellFormed: every template declares at least one column, no name is
// empty or duplicated, every kind is one this module knows, and only numeric kinds are flagged Total.
// A Total flag on a text column would produce a totals row with nonsense under a heading.
func TestCatalog_ColumnsAreDeclaredAndWellFormed(t *testing.T) {
	t.Parallel()
	kinds := map[string]bool{KindText: true, KindInt: true, KindCents: true, KindQty: true, KindDate: true, KindBool: true}
	numeric := map[string]bool{KindInt: true, KindCents: true, KindQty: true}
	for code, entry := range catalog {
		if len(entry.columns) == 0 {
			t.Errorf("template %s: no columns declared, the allowlist would admit anything", code)
			continue
		}
		seen := map[string]bool{}
		for i, c := range entry.columns {
			if c.Name == "" {
				t.Errorf("template %s: column %d has no name", code, i)
			}
			if seen[c.Name] {
				t.Errorf("template %s: duplicate column name %q, the allowlist check compares by position and name", code, c.Name)
			}
			seen[c.Name] = true
			if !kinds[c.Kind] {
				t.Errorf("template %s: column %q has unknown kind %q", code, c.Name, c.Kind)
			}
			if c.Total && !numeric[c.Kind] {
				t.Errorf("template %s: column %q is flagged Total but its kind %q cannot be summed", code, c.Name, c.Kind)
			}
			if c.Label == "" {
				t.Errorf("template %s: column %q has no label, it would render as a blank heading", code, c.Name)
			}
		}
	}
}

// TestCatalog_EveryColumnAppearsAsAnAliasInItsSQL is a cheap textual cross-check between the two halves
// of a template. It cannot replace the runtime FieldDescriptions check (a query can alias a column it
// does not actually project in that order), but it catches the common edit, a column added to the
// allowlist and not to the query, at unit-test speed instead of at integration-test speed.
func TestCatalog_EveryColumnAppearsAsAnAliasInItsSQL(t *testing.T) {
	t.Parallel()
	for code, entry := range catalog {
		for _, c := range entry.columns {
			if !strings.Contains(entry.sql, "AS "+c.Name) && !strings.Contains(entry.sql, " "+c.Name+"\n") {
				t.Errorf("template %s: column %q is on the allowlist but is not aliased in the SQL", code, c.Name)
			}
		}
	}
}

// TestCatalog_HasTheTwentyTwoCoreReports pins the SET of reports this wave promised. A code disappearing
// from the catalog is a report the office loses; this test makes that a deliberate act.
func TestCatalog_HasTheTwentyTwoCoreReports(t *testing.T) {
	t.Parallel()
	want := []string{
		"sales_by_day", "sales_by_branch", "sales_by_rep", "sales_by_category",
		"margin_by_product", "margin_by_category",
		"ar_aging_detail", "ar_aging_summary", "statement_run_history",
		"inventory_valuation", "inventory_movement", "below_reorder",
		"po_status", "receiving_history", "three_way_variance",
		"tax_collected",
		"payments_by_method", "payments_by_day",
		"credit_hold_history",
		"delivery_performance",
		"warranty_by_vendor", "warranty_by_status",
	}
	for _, code := range want {
		if _, ok := catalog[code]; !ok {
			t.Errorf("core report %q is missing from the catalog", code)
		}
	}
	if len(catalog) != len(want) {
		t.Errorf("catalog has %d templates, the core set is %d, a new template needs its own named "+
			"integration test before it joins the catalog", len(catalog), len(want))
	}
}
