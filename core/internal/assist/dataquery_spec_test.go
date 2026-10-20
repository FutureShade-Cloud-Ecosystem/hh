package assist

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// A catalogue with a gated column, to prove the class check on every position the real views do not
// yet exercise (they carry plain columns only).
func dqTestViews() []ViewDef {
	v := ordersView()
	v.Columns = append(v.Columns,
		ColumnDef{Name: "cost_cents", Label: "Cost", Type: typeCents, Class: ClassCost, Filter: true, Agg: true},
		ColumnDef{Name: "margin_pct", Label: "Margin", Type: typeInt, Class: ClassMargin, Filter: true, Group: true, Agg: true},
	)
	return []ViewDef{v}
}

var (
	dqManager = Caller{UserID: "u1", RoleCode: "manager", Tier: 2, BranchIDs: []string{"22222222-2222-2222-2222-222222222222"}}
	dqToronto = dqMustLoc("America/Toronto")
	dqClock   = time.Date(2000, 3, 1, 3, 0, 0, 0, time.UTC) // already the 1st in UTC, still 29 Feb in Toronto
)

func dqMustLoc(n string) *time.Location {
	l, err := time.LoadLocation(n)
	if err != nil {
		panic(err)
	}
	return l
}

func dqCompileFor(t *testing.T, c Caller, raw string) (Compiled, error) {
	t.Helper()
	sp, err := ParseSpec(raw)
	if err != nil {
		return Compiled{}, err
	}
	full := dqTestViews()
	return Compile(sp, full, filterCatalogue(full, c), dqToronto, dqClock)
}

func TestCompileIsParameterisedAndTakesIdentifiersOnlyFromTheCatalogue(t *testing.T) {
	evil := `x'; DROP TABLE orders; --`
	got, err := dqCompileFor(t, dqManager, `{"view":"ai.orders","select":["order_no","total_cents"],
		"where":[{"col":"status","op":"in","value":["OPEN","`+evil+`"]},{"col":"customer_name","op":"=","value":"`+evil+`"}],
		"order_by":[{"col":"total_cents","dir":"desc"}],"limit":10}`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got.SQL, "DROP") || strings.Contains(got.SQL, evil) || strings.Contains(got.SQL, "OPEN") {
		t.Fatalf("a value reached the statement text: %s", got.SQL)
	}
	if !strings.Contains(got.SQL, `FROM "ai"."orders"`) || !strings.Contains(got.SQL, `"order_no"`) {
		t.Fatalf("identifiers are not the catalogue's, quoted: %s", got.SQL)
	}
	if len(got.Args) != 3 { // in list, equality value, limit
		t.Errorf("args = %d: %v", len(got.Args), got.Args)
	}
	if got.Limit != 10 || !got.Cites {
		t.Errorf("limit=%d cites=%v", got.Limit, got.Cites)
	}
	for _, name := range []string{`x"; DROP`, "orders; select", "ai.orders\" union select", `order_no" FROM x --`} {
		raw := `{"view":"ai.orders","select":["` + strings.ReplaceAll(name, `"`, `\"`) + `"]}`
		_, err := dqCompileFor(t, dqManager, raw)
		var se *SpecError
		if !errors.As(err, &se) || se.Kind != "unknown" {
			t.Errorf("column %q: err = %v, want an unknown refusal", name, err)
		}
	}
	if _, err := dqCompileFor(t, dqManager, `{"view":"public.orders"}`); err == nil {
		t.Error("a base table was accepted as a view")
	}
}

func TestSpecRefusesWhatTheGrammarDoesNotHave(t *testing.T) {
	for name, raw := range map[string]string{
		"an identity field":     `{"view":"ai.orders","branch_ids":["b9"]}`,
		"claims as argument":    `{"view":"ai.orders","claims":{"global":true}}`,
		"two objects":           `{"view":"ai.orders"}{"view":"ai.orders"}`,
		"not json":              `select * from orders`,
		"group without agg":     `{"view":"ai.orders","group_by":["status"]}`,
		"unknown aggregate":     `{"view":"ai.orders","aggregate":[{"fn":"stddev","col":"total_cents"}]}`,
		"sum of text":           `{"view":"ai.orders","aggregate":[{"fn":"sum","col":"status"}]}`,
		"select outside group":  `{"view":"ai.orders","group_by":["status"],"aggregate":[{"fn":"count"}],"select":["order_no"]}`,
		"between of three":      `{"view":"ai.orders","where":[{"col":"total_cents","op":"between","value":[1,2,3]}]}`,
		"like operator":         `{"view":"ai.orders","where":[{"col":"status","op":"like","value":"%"}]}`,
		"text ordering op":      `{"view":"ai.orders","where":[{"col":"status","op":">","value":"A"}]}`,
		"a float cents value":   `{"view":"ai.orders","where":[{"col":"total_cents","op":">","value":1.5}]}`,
		"contains unsearchable": `{"view":"ai.orders","where":[{"col":"customer_name","op":"contains","value":"x"}]}`,
		"hidden column select":  `{"view":"ai.orders","select":["id"]}`,
		"too many filters":      `{"view":"ai.orders","where":[` + strings.Repeat(`{"col":"status","op":"=","value":"A"},`, 8) + `{"col":"status","op":"=","value":"A"}]}`,
	} {
		_, err := dqCompileFor(t, dqManager, raw)
		if err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestLimitIsClamped(t *testing.T) {
	for raw, want := range map[string]int{`{"view":"ai.orders"}`: 25, `{"view":"ai.orders","limit":100000}`: 100, `{"view":"ai.orders","limit":-3}`: 25, `{"view":"ai.orders","limit":7}`: 7} {
		got, err := dqCompileFor(t, dqManager, raw)
		if err != nil || got.Limit != want || got.Args[len(got.Args)-1] != int64(want) {
			t.Errorf("%s: limit %v err %v, want %d", raw, got.Limit, err, want)
		}
	}
}

// The class check runs on every position a column can appear: select, where, group by, order by, and
// inside an aggregate. An asker without the class is refused with the class named; one with it is not.
func TestClassCheckRunsOnEveryReferencedColumn(t *testing.T) {
	positions := map[string]string{
		"select":    `{"view":"ai.orders","select":["order_no","cost_cents"]}`,
		"where":     `{"view":"ai.orders","where":[{"col":"cost_cents","op":">","value":100}]}`,
		"group by":  `{"view":"ai.orders","group_by":["margin_pct"],"aggregate":[{"fn":"count"}]}`,
		"order by":  `{"view":"ai.orders","select":["order_no"],"order_by":[{"col":"cost_cents","dir":"desc"}]}`,
		"aggregate": `{"view":"ai.orders","aggregate":[{"fn":"sum","col":"cost_cents"}]}`,
		"avg":       `{"view":"ai.orders","aggregate":[{"fn":"avg","col":"margin_pct"}]}`,
	}
	for pos, raw := range positions {
		_, err := dqCompileFor(t, dqManager, raw)
		var se *SpecError
		if !errors.As(err, &se) || se.Kind != "class" || (se.Class != ClassCost && se.Class != ClassMargin) {
			t.Errorf("%s: err = %v, want a class refusal", pos, err)
		}
		if err != nil && (strings.Contains(err.Error(), "cost_cents") || strings.Contains(err.Error(), "margin_pct")) {
			t.Errorf("%s: the refusal echoes the column: %q", pos, err)
		}
	}
	// A column that does not exist at all is unknown, not a class refusal: no oracle for guessing names.
	_, err := dqCompileFor(t, dqManager, `{"view":"ai.orders","select":["secret_notes"]}`)
	var se *SpecError
	if !errors.As(err, &se) || se.Kind != "unknown" {
		t.Errorf("a made up column: %v", err)
	}
}

func TestRelativeDateWordsResolveInTheDealersZone(t *testing.T) {
	got, err := dqCompileFor(t, dqManager, `{"view":"ai.orders","where":[{"col":"created_at","op":">=","value":"today"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	start, ok := got.Args[0].(time.Time)
	if !ok {
		t.Fatalf("args[0] = %T", got.Args[0])
	}
	// In Toronto the instant is still 29 February, so "today" starts at that local midnight (05:00 UTC).
	if want := time.Date(2000, 2, 29, 5, 0, 0, 0, time.UTC); !start.Equal(want) {
		t.Errorf("today starts at %s, want %s", start.UTC(), want)
	}
	if got.PeriodFrom != "2000-02-29" {
		t.Errorf("period from = %q", got.PeriodFrom)
	}
	// A date alone means the whole day: = is [start, next day).
	eq, err := dqCompileFor(t, dqManager, `{"view":"ai.orders","where":[{"col":"created_at","op":"=","value":"2000-02-28"}]}`)
	if err != nil || len(eq.Args) != 3 {
		t.Fatalf("eq: %v %v", err, eq.Args)
	}
	a, b := eq.Args[0].(time.Time), eq.Args[1].(time.Time)
	if b.Sub(a) != 24*time.Hour {
		t.Errorf("a day spans %s", b.Sub(a))
	}
	for _, w := range []string{"yesterday", "start_of_month", "start_of_last_month", "start_of_year", "start_of_week", "7_days_ago"} {
		if _, err := dqCompileFor(t, dqManager, `{"view":"ai.orders","where":[{"col":"created_at","op":">=","value":"`+w+`"}]}`); err != nil {
			t.Errorf("%s: %v", w, err)
		}
	}
	if _, err := dqCompileFor(t, dqManager, `{"view":"ai.orders","where":[{"col":"created_at","op":">=","value":"last blue moon"}]}`); err == nil {
		t.Error("a made up date word was accepted")
	}
}

func TestAggregateShapes(t *testing.T) {
	got, err := dqCompileFor(t, dqManager, `{"view":"ai.orders","group_by":["branch_label"],"aggregate":[{"fn":"sum","col":"total_cents"},{"fn":"count"}],"order_by":[{"col":"sum_total_cents","dir":"desc"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if got.Cites || !got.Aggregate || len(got.Outputs) != 3 || got.Outputs[1].Name != "sum_total_cents" || got.Outputs[2].Name != "count" {
		t.Errorf("outputs = %+v cites=%v", got.Outputs, got.Cites)
	}
	if !strings.Contains(got.SQL, `GROUP BY "branch_label"`) {
		t.Errorf("no group by: %s", got.SQL)
	}
}

func TestCatalogueIsPerAskerAndDropsWhatTheTierFloorRefuses(t *testing.T) {
	if len(CatalogueFor(dqManager)) != 7 {
		t.Fatalf("a dqManager sees %d views", len(CatalogueFor(dqManager)))
	}
	frontline := Caller{UserID: "u", Tier: 3, BranchIDs: []string{"22222222-2222-2222-2222-222222222222"}}
	if got := CatalogueFor(frontline); len(got) != 0 {
		t.Errorf("a frontline caller sees %d views", len(got))
	}
	if got := CatalogueFor(Caller{}); len(got) != 0 {
		t.Errorf("a caller with no role sees %d views", len(got))
	}
}
